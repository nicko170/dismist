package httpapi

import (
	"encoding/json"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// Status thresholds (design §3.4).
const (
	pendingAfter     = 2 * time.Minute
	recentFailWindow = 15 * time.Minute
)

// lastKnown is the persisted ap_status record of an AP (workstream A's
// APStatusRecord, read through its JSON form).
type lastKnown struct {
	ObservedAt time.Time `json:"observed_at"`
	Connected  bool      `json:"connected"`
	Firmware   string    `json:"firmware"`
	Model      string    `json:"model"`
	Hello      *obsHello `json:"hello"`
	HelloAt    time.Time `json:"hello_at"`
	VAPs       []struct {
		Dev        string `json:"dev"`
		BSSID      string `json:"bssid"`
		SSID       string `json:"ssid"`
		WLANID     string `json:"wlan_id"`
		RadioIndex int    `json:"radio_index"`
	} `json:"vaps"`
	Reboots []struct {
		DetectedAt time.Time `json:"detected_at"`
	} `json:"reboots"`
}

func (s *Server) lastKnown(apID string) (lastKnown, bool) {
	if s.config.LastKnown == nil {
		return lastKnown{}, false
	}
	record, ok := s.config.LastKnown(apID)
	if !ok || record == nil {
		return lastKnown{}, false
	}
	var result lastKnown
	mirror(record, &result)
	return result, true
}

// apContext is everything the AP views read, gathered once per request.
type apContext struct {
	state     model.State
	pending   []controller.PendingAP
	delivered func(apID string) (model.DeliveredConfig, bool)
	now       time.Time
	hashKey   []byte
}

func (s *Server) apContext(state model.State) apContext {
	ctx := apContext{state: state, now: s.now()}
	if s.manager != nil {
		ctx.pending = s.manager.Pending()
	}
	if store, ok := s.store.(deliveredStore); ok {
		configs := store.APConfig()
		ctx.delivered = func(apID string) (model.DeliveredConfig, bool) {
			delivered, found, err := configs.Delivered(apID)
			return delivered, err == nil && found
		}
	}
	if keyed, ok := s.store.(hashKeyStore); ok {
		ctx.hashKey = keyed.HashKey()
	}
	return ctx
}

// recentPending reports whether the pending map holds a refusal of this
// (registered) AP for reason within the failure window.
func (c apContext) recentPending(apID, reason string) bool {
	for _, entry := range c.pending {
		if entry.ID == apID && entry.Reason == reason && c.now.Sub(entry.LastSeen) < recentFailWindow {
			return true
		}
	}
	return false
}

// apStatus returns the AP's headline status and the other statuses that
// also apply, in the precedence order of design §3.4.
func (s *Server) apStatus(ctx apContext, ap model.APRecord, obs apObs) (api.APStatus, []string) {
	var applies []api.APStatus
	configRevision := compile.ConfigRevision(ctx.state, ap)
	applied := obs.status.AppliedRevision
	if applied == 0 {
		applied = ap.AppliedRevision
	}
	if ctx.recentPending(ap.ID, controller.PendingAuthFailed) {
		applies = append(applies, api.APStatusAuthFailing)
	}
	if ctx.recentPending(ap.ID, controller.PendingSourceMismatch) {
		applies = append(applies, api.APStatusSourceMismatch)
	}
	if obs.status.CompileError != "" || obs.status.ConfigSource == controller.ConfigSourceDelivered || obs.status.ConfigSource == controller.ConfigSourceNone {
		applies = append(applies, api.APStatusConfigError)
	}
	everConnected := obs.status.Connected || obs.status.ConnectionCount > 0 || !obs.status.LastSeen.IsZero()
	if !everConnected && ctx.delivered != nil {
		_, everConnected = ctx.delivered(ap.ID)
	}
	if !everConnected {
		if known, ok := s.lastKnown(ap.ID); ok && !known.ObservedAt.IsZero() {
			everConnected = true
		}
	}
	switch {
	case !everConnected:
		applies = append(applies, api.APStatusNeverConnected)
	case !obs.status.Connected:
		applies = append(applies, api.APStatusOffline)
	}
	if obs.status.LastError != "" {
		applies = append(applies, api.APStatusRejected)
	}
	if obs.status.Connected && obs.status.LastError == "" {
		switch {
		case applied == configRevision:
			applies = append(applies, api.APStatusInSync)
		case ctx.now.Sub(compile.ConfigTime(ctx.state, ap)) >= pendingAfter:
			applies = append(applies, api.APStatusPending)
		default:
			applies = append(applies, api.APStatusApplying)
		}
	}
	order := []api.APStatus{
		api.APStatusAuthFailing, api.APStatusSourceMismatch, api.APStatusConfigError,
		api.APStatusNeverConnected, api.APStatusOffline, api.APStatusRejected,
		api.APStatusPending, api.APStatusApplying, api.APStatusInSync,
	}
	present := map[api.APStatus]bool{}
	for _, status := range applies {
		present[status] = true
	}
	var headline api.APStatus
	flags := []string{}
	for _, status := range order {
		if !present[status] {
			continue
		}
		if headline == "" {
			headline = status
		} else if status != api.APStatusInSync {
			flags = append(flags, string(status))
		}
	}
	if headline == "" {
		headline = api.APStatusOffline
	}
	if ctx.delivered != nil {
		if delivered, ok := ctx.delivered(ap.ID); ok && delivered.Verified != nil && !delivered.Verified.OK {
			flags = append(flags, "verify_mismatch")
		}
	}
	if len(ap.LocalFiles) > 0 {
		flags = append(flags, "local_files")
	}
	return headline, flags
}

// lanIP is the AP's management address: the management SVI from field 26
// (vlan1, or the SVI on the tagged management VLAN), else the hello's
// LocalIP.
func lanIP(obs apObs, known lastKnown) string {
	interfaces := make([]obsInterface, len(obs.telemetry.Interfaces))
	copy(interfaces, obs.telemetry.Interfaces)
	first := ""
	for _, iface := range interfaces {
		for _, address := range iface.Addresses {
			ip, _, err := net.ParseCIDR(address)
			if err != nil {
				ip = net.ParseIP(address)
			}
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if iface.Name == "vlan1" {
				return ip.String()
			}
			if first == "" {
				first = ip.String()
			}
		}
	}
	if first != "" {
		return first
	}
	if obs.status.Hello.LocalIP != "" {
		return obs.status.Hello.LocalIP
	}
	if known.Hello != nil {
		return known.Hello.LocalIP
	}
	return ""
}

// lanIPv4 is kept for the legacy views.
func lanIPv4(interfaces []obsInterface) string {
	return lanIP(apObs{telemetry: obsTelemetry{Interfaces: interfaces}}, lastKnown{})
}

func remoteHost(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return address
}

// behindNAT compares the session's source with the AP's own address.
func behindNAT(lan, remote string) bool {
	if lan == "" || remote == "" {
		return false
	}
	a, errA := netip.ParseAddr(lan)
	b, errB := netip.ParseAddr(remoteHost(remote))
	if errA != nil || errB != nil {
		return lan != remoteHost(remote)
	}
	return a.Unmap() != b.Unmap()
}

func timePtr(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

// accessRadios returns the AP's access radios from telemetry (the scan
// radio is excluded, design §5.2).
func accessRadios(obs apObs) []api.RadioSummary {
	result := []api.RadioSummary{}
	for _, radio := range obs.telemetry.Radios {
		if radio.Role == "scan" || (radio.Band != "2.4" && radio.Band != "5") {
			continue
		}
		util, _ := radio.util()
		result = append(result, api.RadioSummary{Band: api.Band(radio.Band), Channel: radio.Channel, Width: radio.WidthMHz, Clients: radio.Clients, Util: util})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Band < result[j].Band })
	return result
}

// apView builds the list view of one AP (design §4.9 APView).
func (s *Server) apView(ctx apContext, ap model.APRecord) api.APView {
	obs := s.observe(ap.ID)
	return s.apViewFrom(ctx, ap, obs)
}

func (s *Server) apViewFrom(ctx apContext, ap model.APRecord, obs apObs) api.APView {
	known, _ := s.lastKnown(ap.ID)
	status, flags := s.apStatus(ctx, ap, obs)
	view := api.APView{
		ID: ap.ID, Name: ap.Name, SiteID: ap.SiteID, Model: ap.Model, ModelSource: "record",
		Firmware: ap.Firmware, Status: status, Flags: flags, Connected: obs.status.Connected,
		ConnectedAt: timePtr(obs.status.ConnectedAt), LastSeen: timePtr(obs.status.LastSeen),
		RemoteAddress: remoteHost(obs.status.RemoteAddress), PinnedIP: ap.ManagementIP,
		HasSecret: len(ap.Secret) == model.SecretSize, ConfigRevision: compile.ConfigRevision(ctx.state, ap),
		AppliedRevision: obs.status.AppliedRevision, LastError: obs.status.LastError,
		Radios: []api.RadioSummary{}, UpdatedRev: ap.UpdatedRev,
	}
	if !obs.status.Connected {
		view.ConnectedAt = nil
	}
	if view.AppliedRevision == 0 {
		view.AppliedRevision = ap.AppliedRevision
	}
	if model := firstNonEmpty(obs.status.Model, obs.telemetry.Model, known.Model); model != "" {
		view.Model, view.ModelSource = model, "ap"
	}
	view.Firmware = firstNonEmpty(obs.status.Firmware, obs.telemetry.Firmware, known.Firmware, ap.Firmware)
	view.FirmwareTested = view.Firmware == api.TestedFirmware
	if view.LastSeen == nil {
		view.LastSeen = timePtr(known.ObservedAt)
	}
	if view.PinnedIP == "" {
		view.PinnedIP = obs.pinnedIP
	}
	view.LANIP = lanIP(obs, known)
	view.BehindNAT = behindNAT(view.LANIP, view.RemoteAddress)
	if obs.fresh(ap.ConfigUpdatedAt) {
		view.ClientCount = len(obs.telemetry.AssociatedClients)
		view.UptimeSeconds = obs.telemetry.UptimeSeconds
		view.Radios = accessRadios(obs)
	}
	if view.Flags == nil {
		view.Flags = []string{}
	}
	return view
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// apLive is the SSE form of an AP (design §4.10).
func (s *Server) apLive(ctx apContext, ap model.APRecord, obs apObs) api.APLive {
	view := s.apViewFrom(ctx, ap, obs)
	seen := obs.status.LastSeen
	if seen.IsZero() {
		seen = ctx.now
	}
	return api.APLive{
		ID: view.ID, Status: view.Status, Connected: view.Connected, AppliedRevision: view.AppliedRevision,
		ConfigRevision: view.ConfigRevision, LastError: view.LastError, ClientCount: view.ClientCount,
		UptimeSeconds: view.UptimeSeconds, LANIP: view.LANIP, RemoteAddress: view.RemoteAddress,
		Radios: view.Radios, SeenAt: seen.UTC(),
	}
}

// radioViews are every radio of the AP (Radios tab).
func radioViews(obs apObs) []api.RadioView {
	result := []api.RadioView{}
	for _, radio := range obs.telemetry.Radios {
		dev := firstNonEmpty(radio.Dev, radio.Interface)
		view := api.RadioView{Dev: dev, Role: api.RadioRoleAccess, Channel: radio.Channel, Secondary: radio.Secondary,
			Width: radio.WidthMHz, PHY: radio.PHY, Clients: radio.Clients, Counters: radio.Counters}
		if radio.Role == "scan" || radio.Band == "scan" {
			view.Role = api.RadioRoleScan
		} else if radio.Band == "2.4" || radio.Band == "5" {
			band := api.Band(radio.Band)
			view.Band = &band
		}
		if radio.TxPowerDBm != 0 {
			value := float64(radio.TxPowerDBm)
			view.TxPowerDBm = &value
		}
		if radio.MaxTxPowerDBm != 0 {
			value := float64(radio.MaxTxPowerDBm)
			view.MaxPowerDBm = &value
		}
		if radio.NoiseFloorDBm != 0 {
			value := float64(radio.NoiseFloorDBm)
			view.NoiseDBm = &value
		}
		view.Util, view.UtilParts = radio.util()
		if radio.DFS.RadarDetected || radio.DFS.CACState != 0 {
			cac := "none"
			if radio.DFS.CACState != 0 {
				cac = "in_progress"
			}
			view.DFS = &api.DFSState{CAC: cac, RadarDetected: radio.DFS.RadarDetected}
		}
		if view.Counters == nil {
			view.Counters = map[string]uint64{}
		}
		result = append(result, view)
	}
	return result
}

func wlansLive(obs apObs) []api.WLANLive {
	result := []api.WLANLive{}
	for _, vap := range obs.telemetry.VAPs {
		result = append(result, api.WLANLive{WLANID: vap.WLANID, SSID: vap.SSID, Band: vap.Band, BSSID: vap.BSSID, Dev: vap.Interface, Clients: vap.Clients})
	}
	return result
}

func healthView(obs apObs) api.APHealth {
	h := obs.telemetry.Health
	view := api.APHealth{CPUBusy: obs.telemetry.CPUBusy}
	if h.Temperature != nil {
		view.CPUTempC = h.Temperature.CPU
	}
	if h.Memory != nil {
		view.MemTotalBytes, view.MemAvailableBytes = h.Memory.TotalKB*1024, h.Memory.AvailableKB*1024
	}
	if h.Load != nil {
		view.Load = h.Load.Average
	}
	if h.RW != nil {
		view.RWTotalBytes, view.RWAvailableBytes = h.RW.TotalKB*1024, h.RW.AvailableKB*1024
	}
	if h.Agent != nil {
		heap := h.Agent.RSSBytes
		if heap == 0 {
			heap = h.Agent.AllocBytes
		}
		view.AgentHeapBytes = &heap
	}
	return view
}

func milliwatts(mw uint32) *float64 {
	if mw == 0 {
		return nil
	}
	w := float64(mw) / 1000
	return &w
}

func uplinkView(obs apObs) api.APUplink {
	view := api.APUplink{Ports: []api.PortView{}, LLDP: []api.LLDPView{}, InactiveVLANs: []int{}}
	for _, port := range obs.telemetry.Ports {
		duplex := "half"
		if port.FullDuplex {
			duplex = "full"
		}
		errors := port.Errors
		if errors == nil {
			errors = map[string]uint64{}
		}
		view.Ports = append(view.Ports, api.PortView{Name: port.Name, Up: port.Up, Speed: port.SpeedMbps, Duplex: duplex, RxBps: port.RxBps, TxBps: port.TxBps, Errors: errors})
	}
	for _, neighbour := range obs.telemetry.LLDP {
		vlans := []int{}
		for _, vlan := range neighbour.VLANs {
			vlans = append(vlans, vlan.ID)
		}
		systemDesc := neighbour.SystemDesc
		if len(systemDesc) > 64 {
			systemDesc = systemDesc[:64]
		}
		view.LLDP = append(view.LLDP, api.LLDPView{Port: neighbour.APPort, SystemName: neighbour.SystemName, SystemDesc: systemDesc,
			PortID: neighbour.PortID, PortDesc: neighbour.PortDesc, ChassisID: neighbour.ChassisID, MgmtAddress: neighbour.MgmtAddress,
			NativeVLAN: neighbour.NativeVLAN, VLANs: vlans})
	}
	if power := obs.telemetry.Power; power != nil || len(obs.telemetry.LLDPPower) > 0 {
		pv := &api.PowerView{}
		if power != nil {
			pv.Source = power.Source
			pv.NeededW = milliwatts(power.NeededMW)
			if power.BudgetMW != 0 {
				budget := float64(power.BudgetMW) / 1000
				pv.BudgetW = &budget
			}
			pv.Constrained = power.constrained()
		}
		if len(obs.telemetry.LLDPPower) > 0 {
			lldp := obs.telemetry.LLDPPower[0]
			pv.AllocatedW = milliwatts(lldp.AllocatedMW)
			pv.RequestedW = milliwatts(lldp.RequestedMW)
			if pv.NeededW == nil {
				pv.NeededW = milliwatts(lldp.NeededMW)
			}
		}
		view.Power = pv
	}
	if obs.telemetry.InactiveVLANs != nil {
		view.InactiveVLANs = append(view.InactiveVLANs, obs.telemetry.InactiveVLANs...)
	}
	return view
}

func ipView(obs apObs) api.APIP {
	view := api.APIP{DNS: []string{}, NTP: []string{}, DADFailed: []string{}}
	ip := obs.telemetry.IP
	var lease *obsLease
	for index := range ip.DHCP {
		if ip.DHCP[index].Address != "" || lease == nil {
			lease = &ip.DHCP[index]
		}
		if ip.DHCP[index].Dev == "vlan1" {
			lease = &ip.DHCP[index]
			break
		}
	}
	if lease != nil {
		view.VLAN, view.DHCPState, view.Address, view.Prefix = lease.Dev, lease.State, lease.Address, lease.Prefix
		view.RenewedAt, view.LeaseSecs, view.DHCPServer, view.Domain = timePtr(lease.RenewedAt), lease.LeaseSeconds, lease.Server, lease.Domain
		view.Gateway = lease.Gateway
		view.DNS = append(view.DNS, lease.DNS...)
		view.NTP = append(view.NTP, lease.NTP...)
	}
	for _, route := range ip.Routes {
		if (route.Destination == "" || route.Destination == "0.0.0.0/0") && route.Gateway != "" {
			view.Gateway = route.Gateway
			break
		}
	}
	for _, neighbor := range ip.Neighbors {
		if view.Gateway != "" && neighbor.IP == view.Gateway {
			view.GatewayMAC = neighbor.MAC
		}
	}
	for _, server := range ip.DNSServers {
		if !containsString(view.DNS, server) {
			view.DNS = append(view.DNS, server)
		}
	}
	for _, iface := range obs.telemetry.Interfaces {
		view.DADFailed = append(view.DADFailed, iface.DADFailed...)
	}
	return view
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// rebootTime returns the hello's last reboot time, or nil when it is
// missing or looks like the parse bug that reports the hello time itself
// (the AP had been up for minutes, so it cannot have rebooted "now").
func rebootTime(hello obsHello, helloAt time.Time) *time.Time {
	if hello.LastRebootTime.IsZero() {
		return nil
	}
	if !helloAt.IsZero() && hello.Uptime > 120 {
		delta := helloAt.Sub(hello.LastRebootTime)
		if delta < 5*time.Second && delta > -5*time.Second {
			return nil
		}
	}
	return timePtr(hello.LastRebootTime)
}

// apDetail builds GET /api/aps/$id (design §4.9 APDetail).
func (s *Server) apDetail(ctx apContext, ap model.APRecord) api.APDetail {
	obs := s.observe(ap.ID)
	known, _ := s.lastKnown(ap.ID)
	detail := api.APDetail{APView: s.apViewFrom(ctx, ap, obs)}
	hello, helloAt := obs.status.Hello, obs.status.HelloAt
	if helloAt.IsZero() && known.Hello != nil {
		hello, helloAt = *known.Hello, known.HelloAt
	}
	bootCount := hello.BootCount
	if obs.telemetry.Health.BootCount > bootCount {
		bootCount = obs.telemetry.Health.BootCount
	}
	detail.Identity = api.APIdentity{Serial: hello.SerialNo, SKU: hello.SKU, BootCount: bootCount,
		LastRebootReason: hello.LastRebootReason, LastRebootTime: rebootTime(hello, helloAt),
		LastBootState: hello.LastBootState, TunnelIPv6: obs.status.TunnelIPv6}
	detail.Connection = api.APConnection{ConnectionCount: obs.status.ConnectionCount, LastDisconnect: timePtr(obs.status.LastDisconnect),
		TLSCipher: obs.status.TLSCipherSuite, RTTMillis: obs.telemetry.Health.rtt(), PushMode: s.pushMode(ap, obs),
		ClockSkewSeconds: obs.telemetry.ClockSkewSeconds}
	if cloud := obs.telemetry.Health.Cloud; cloud != nil && cloud.PMTU != 0 {
		pmtu := cloud.PMTU
		detail.Connection.PMTU = &pmtu
	}
	detail.Config = s.apConfigState(ctx, ap, obs)
	features := controller.ResolveFeatures(s.defaultFeatures, ap.Features)
	if obs.present {
		features = controller.Features(0)
		for name, on := range obs.features {
			if on {
				features = features.With(controller.Feature(name))
			}
		}
	}
	detail.ProtocolFeatures = map[string]bool{}
	for _, name := range featureNames() {
		detail.ProtocolFeatures[name] = features.Has(controller.Feature(name)) || (name == FeatureAPCmd && apCmdOn(ap, obs))
	}
	if ap.LocalFiles != nil {
		detail.LocalFiles = make([]api.LocalFileView, 0, len(ap.LocalFiles))
		for _, file := range ap.LocalFiles {
			detail.LocalFiles = append(detail.LocalFiles, api.LocalFileView{Name: file.Name, SHA256: file.SHA256, SeenAt: file.SeenAt})
		}
	}
	fresh := obs.fresh(ap.ConfigUpdatedAt)
	detail.Health = api.APHealth{}
	detail.Uplink = api.APUplink{Ports: []api.PortView{}, LLDP: []api.LLDPView{}, InactiveVLANs: []int{}}
	detail.IP = api.APIP{DNS: []string{}, NTP: []string{}, DADFailed: []string{}}
	detail.RadiosDetail = []api.RadioView{}
	detail.WLANsLive = []api.WLANLive{}
	if fresh {
		detail.Health = healthView(obs)
		detail.Uplink = uplinkView(obs)
		detail.IP = ipView(obs)
		detail.RadiosDetail = radioViews(obs)
		detail.WLANsLive = wlansLive(obs)
	}
	detail.Overrides, detail.RFPlan, detail.LocateUntil = apOverrides(ap)
	detail.Capabilities = api.APCapabilities{
		APCmd: apCmdOn(ap, obs),
		Pong2: features.Has(controller.FeaturePong2),
	}
	if notify := recordKey(ap, "push_mode"); len(notify) > 0 {
		var mode string
		if json.Unmarshal(notify, &mode) == nil && mode != "" {
			learned := mode == string(api.PushModeNotify)
			detail.Capabilities.ConfigChangeNotify = &learned
		}
	}
	detail.Commands = s.commandSupport(ctx.state, ap, obs, features)
	detail.RebootsDetected = []time.Time{}
	if !obs.status.LastRebootDetected.IsZero() {
		detail.RebootsDetected = append(detail.RebootsDetected, obs.status.LastRebootDetected.UTC())
	}
	for _, reboot := range known.Reboots {
		if len(detail.RebootsDetected) >= 10 {
			break
		}
		if !reboot.DetectedAt.IsZero() && !reboot.DetectedAt.Equal(obs.status.LastRebootDetected) {
			detail.RebootsDetected = append(detail.RebootsDetected, reboot.DetectedAt.UTC())
		}
	}
	return detail
}

// apConfigState is the Config card of AP detail.
func (s *Server) apConfigState(ctx apContext, ap model.APRecord, obs apObs) api.APConfigState {
	state := api.APConfigState{
		AppliedTimestamp: obs.status.Applied.ConfigTimestamp, DesiredTimestamp: obs.status.DesiredConfigTimestamp,
		RecoveredFromBadConfig: obs.status.Applied.RecoveredFromBadConfig || obs.status.ConfigRequest.RecoveredFromBadConfig,
		Good5Min:               obs.status.Applied.Good5Min, Shadowed: []string{}, CompileError: obs.status.CompileError,
		CountryReported: obs.telemetry.Health.Country,
	}
	if state.AppliedTimestamp == 0 {
		state.AppliedTimestamp = obs.telemetry.ConfigTimestamp
	}
	if state.DesiredTimestamp == 0 {
		state.DesiredTimestamp = compile.ConfigTimestamp(compile.ConfigTime(ctx.state, ap))
	}
	if ap.SiteID != "" {
		if input, err := compile.InputFor(s.base, ctx.state, ap.ID); err == nil {
			if output, err := compile.AP(input); err == nil {
				if len(ctx.hashKey) > 0 {
					if hash, _, _, err := effective.HashConfig(ctx.hashKey, output.Config, compile.EffectiveContext(ap)); err == nil {
						state.EffectiveHash = hash
					}
				}
				for _, shadow := range output.Shadowed {
					if shadow.Scope == "ap" {
						state.Shadowed = append(state.Shadowed, shadow.Option)
					}
				}
			} else if state.CompileError == "" {
				state.CompileError = err.Error()
			}
		}
	}
	if ctx.delivered != nil {
		if delivered, ok := ctx.delivered(ap.ID); ok && delivered.Verified != nil {
			mismatches := delivered.Verified.Mismatches
			if mismatches == nil {
				mismatches = []string{}
			}
			state.LastVerification = &api.Verification{At: delivered.Verified.At, OK: delivered.Verified.OK, Mismatches: mismatches}
		}
	}
	return state
}

// apOverrides reads the AP's typed overrides, rf_plan and locate time from
// its record (design §4.3 keys).
func apOverrides(ap model.APRecord) (api.APOverrides, map[api.Band]*api.PlannedRadio, *time.Time) {
	overrides := api.APOverrides{RF: map[api.Band]*api.RadioOverride{api.Band24: nil, api.Band5: nil}}
	plan := map[api.Band]*api.PlannedRadio{api.Band24: nil, api.Band5: nil}
	object, err := recordMap(ap)
	if err != nil {
		return overrides, plan, nil
	}
	var record struct {
		LEDBrightness    *int            `json:"led_brightness"`
		SSHEnabled       *bool           `json:"ssh_enabled"`
		Management       json.RawMessage `json:"management"`
		Eth1             json.RawMessage `json:"eth1"`
		WiredClientStats *bool           `json:"wired_client_stats"`
		RF               map[string]*struct {
			Enabled  *bool `json:"enabled"`
			Channel  int   `json:"channel"`
			Width    int   `json:"width"`
			PowerDBm int   `json:"power_dbm"`
		} `json:"rf_override"`
		RFPlan map[string]struct {
			Channel     int       `json:"channel"`
			Width       int       `json:"width"`
			PowerDBm    int       `json:"power_dbm"`
			DFSFallback int       `json:"dfs_fallback"`
			Source      string    `json:"source"`
			Reason      string    `json:"reason"`
			PlannedAt   time.Time `json:"planned_at"`
		} `json:"rf_plan"`
		LocateUntil time.Time `json:"locate_until"`
	}
	viewFrom(object, &record)
	overrides.LEDBrightness, overrides.SSHEnabled = record.LEDBrightness, record.SSHEnabled
	if len(record.Management) > 0 && string(record.Management) != "null" {
		overrides.Management = json.RawMessage(record.Management)
	}
	if len(record.Eth1) > 0 && string(record.Eth1) != "null" {
		overrides.Eth1 = json.RawMessage(record.Eth1)
	}
	for band, override := range record.RF {
		if override == nil || (band != "2.4" && band != "5") {
			continue
		}
		view := &api.RadioOverride{}
		if override.Enabled != nil {
			disabled := !*override.Enabled
			view.Disabled = &disabled
		}
		if override.Channel != 0 {
			channel := override.Channel
			view.Channel = &channel
		}
		if override.Width != 0 {
			width := override.Width
			view.Width = &width
		}
		if override.PowerDBm != 0 {
			power := override.PowerDBm
			view.PowerDBm = &power
		}
		overrides.RF[api.Band(band)] = view
	}
	for band, planned := range record.RFPlan {
		if band != "2.4" && band != "5" {
			continue
		}
		view := &api.PlannedRadio{Mode: "auto", Channel: planned.Channel, Width: planned.Width, PowerDBm: planned.PowerDBm}
		if planned.DFSFallback != 0 {
			fallback := planned.DFSFallback
			view.DFSFallback = &fallback
		}
		if override := overrides.RF[api.Band(band)]; override != nil && override.Channel != nil {
			view.Mode = "static"
		}
		plan[api.Band(band)] = view
	}
	return overrides, plan, timePtr(record.LocateUntil)
}

// apTelemetry is GET /api/aps/$id/telemetry.
func (s *Server) apTelemetry(ap model.APRecord) api.APTelemetry {
	obs := s.observe(ap.ID)
	view := api.APTelemetry{APID: ap.ID, Radios: []api.RadioView{}, WLANs: []api.WLANLive{},
		Uplink: api.APUplink{Ports: []api.PortView{}, LLDP: []api.LLDPView{}, InactiveVLANs: []int{}},
		IP:     api.APIP{DNS: []string{}, NTP: []string{}, DADFailed: []string{}}}
	if !obs.fresh(ap.ConfigUpdatedAt) {
		return view
	}
	t := obs.telemetry
	view.LastFull, view.LastFast = timePtr(t.LastFull), timePtr(t.LastFast)
	view.FullFrames, view.FastFrames, view.UptimeSeconds = t.FullFrames, t.FastFrames, t.UptimeSeconds
	view.ClockSkewSeconds, view.ConfigTimestamp, view.Country = t.ClockSkewSeconds, t.ConfigTimestamp, t.Health.Country
	view.Health, view.Uplink, view.IP = healthView(obs), uplinkView(obs), ipView(obs)
	view.Radios, view.WLANs = radioViews(obs), wlansLive(obs)
	view.BLE = bleView(obs)
	return view
}

// bleView sums the AP's BLE radios (normally one) for the indicator.
func bleView(obs apObs) *api.APBLE {
	if len(obs.telemetry.BLE) == 0 {
		return nil
	}
	var view api.APBLE
	for _, radio := range obs.telemetry.BLE {
		view.TxAdverts += radio.TxAdverts
		view.RxAdverts += radio.RxAdverts
		view.Scans += radio.Scans
		view.Errors += radio.TxErrors + radio.RxErrors
		view.ResetsHung += radio.ResetsHung + radio.ResetsHungRx
		view.Alive = view.Alive || radio.TxAdverts+radio.RxAdverts+radio.RxEvents+radio.Scans > 0
	}
	return &view
}

// lowerNoSpace is a search helper.
func lowerNoSpace(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
