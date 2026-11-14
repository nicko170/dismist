package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/telemetry"
)

// observeState is what the telemetry merge remembers between frames.
type observeState struct {
	// bootCount is the highest boot count seen (hello or field 71).
	bootCount uint64
	// clockWarned is set once a session has warned about clock skew.
	clockWarned bool
	// driftSince is when the reported Timestamp (60) first differed from
	// the served one; zero while they match.
	driftSince time.Time
	// channels and radar are each access radio's last channel and radar
	// state, by radio index, for the radio events.
	channels map[int]int
	radar    map[int]bool
	// inactiveVLANs maps a VLAN raised as inactive to its event ID, so the
	// event that clears it resolves it.
	inactiveVLANs map[int]string
	// kmsgSeen are the kernel log sequence numbers already in the ring.
	kmsgSeen map[string]bool
	// lastPorts and lastPortsAt are the previous full frame's ports, for
	// the rx/tx rates.
	lastPorts   []telemetry.Port
	lastPortsAt time.Time
	// lastCPU is the previous full frame's total CPU line.
	lastCPU *telemetry.CPUTimes
}

// newSession resets what is per connection and remembers the hello's boot
// count.
func (o *observeState) newSession(bootCount uint64) {
	o.clockWarned = false
	o.driftSince = time.Time{}
	if bootCount > o.bootCount {
		o.bootCount = bootCount
	}
}

// Thresholds.
const (
	clockSkewWarn = 30 * time.Second
	maxKMsgRing   = 200
)

// observeTelemetry decodes one ep-telemetry message and merges it into
// the AP's state (design §5.2, §5.3).
func (s *Server) observeTelemetry(body []byte) {
	update, err := telemetry.Decode(body)
	if err != nil {
		s.counters.telemetryTopic("")
		s.counters.decodeError(telemetry.SectionFrame, 1)
		return
	}
	s.counters.telemetryTopic(update.Topic)
	for section, count := range update.DecodeErrors {
		s.counters.decodeError(section, count)
	}
	now := time.Now()
	var vaps []telemetry.VAP
	var scan *telemetry.Scan
	var reboot *Reboot
	var output *telemetry.CommandOutput
	s.mu.Lock()
	switch update.Topic {
	case telemetry.TopicStatsFull:
		vaps, scan, reboot = s.mergeFullLocked(update, now)
	case telemetry.TopicStatsFast:
		s.mergeFastLocked(update, now)
	case telemetry.TopicConfigApplied:
		s.appliedLocked(update, now)
	case telemetry.TopicPaceEvent, telemetry.TopicClientEvent:
		s.paceEventLocked(update.Event, now)
	case telemetry.TopicCommandOutput:
		output = update.CommandOutput
		if output != nil {
			s.commands.record(*output, now)
		}
	}
	s.mu.Unlock()
	s.flush()
	if update.Topic == telemetry.TopicConfigApplied && update.Applied != nil {
		s.pushApplied(*update.Applied)
		s.verifyApplied(*update.Applied, now)
	}
	if s.sink != nil && len(vaps) != 0 {
		if _, err := s.sink.SetVAPs(s.auth.ID, now, vaps); err != nil {
			log.Printf("AP %s: cannot record the VAP list: %v", s.auth.ID, err)
		}
	}
	if reboot != nil && s.sink != nil {
		if err := s.sink.RecordReboot(s.auth.ID, *reboot); err != nil {
			log.Printf("AP %s: cannot record the reboot: %v", s.auth.ID, err)
		}
	}
	if scan != nil && s.onScan != nil {
		s.onScan(s.auth.ID, now, *scan)
	}
	if output != nil && s.onCommandOutput != nil {
		s.onCommandOutput(s.auth.ID, *output)
	}
}

// mergeFullLocked replaces the full-frame sections with this frame's. It
// returns the VAP list to persist, a scan to store and a reboot detected
// from the boot count.
func (s *Server) mergeFullLocked(update telemetry.Update, now time.Time) ([]telemetry.VAP, *telemetry.Scan, *Reboot) {
	stats := &s.stats
	stats.FullFrames++
	stats.LastFull = now
	if update.Model != "" {
		stats.Model = update.Model
	}
	if update.Firmware != "" {
		stats.Firmware = update.Firmware
	}
	stats.UptimeSeconds = update.UptimeSeconds
	stats.DecodeErrors = update.DecodeErrors
	if update.Present.Has(60) {
		stats.ConfigTimestamp = update.ConfigTimestamp
	}
	if update.Health != nil {
		stats.Health = *update.Health
		s.cpuBusyLocked(update.Health.CPU)
	}
	s.clockSkewLocked(update, now)
	stats.Radios = append([]telemetry.RadioStats(nil), update.Radios...)
	stats.VAPs = append([]telemetry.VAP(nil), update.VAPs...)
	if len(update.VAPs) != 0 {
		stats.LastKnownVAPs = append([]telemetry.VAP(nil), update.VAPs...)
		stats.LastKnownVAPsAt = now
	}
	stats.Probes = append([]telemetry.Probe(nil), update.Probes...)
	if len(update.Interfaces) != 0 {
		stats.Interfaces = append([]telemetry.Interface(nil), update.Interfaces...)
	}
	s.portsLocked(update.Ports, now)
	stats.LLDP = append([]telemetry.LLDPNeighbor(nil), update.LLDP...)
	stats.LLDPPower = append([]telemetry.LLDPPower(nil), update.LLDPPower...)
	stats.Power = update.Power
	if update.IP != nil {
		stats.IP = *update.IP
	} else {
		stats.IP = telemetry.IP{}
	}
	stats.InactiveVLANs = append([]int(nil), update.InactiveVLANs...)
	if update.Scan != nil {
		stats.Scan, stats.ScanAt = update.Scan, now
	}
	stats.BLE = append([]telemetry.BLERadio(nil), update.BLE...)
	stats.Diag = update.Diag
	s.kmsgLocked(update.KMsg)
	s.associated = telemetry.MergeClients(s.associated, update.Clients, false)
	s.joinClientsLocked()
	s.radioEventsLocked(now)
	s.inactiveVLANsLocked(now)
	s.driftLocked(update, now)
	reboot := s.bootCountLocked(update, now)
	return update.VAPs, update.Scan, reboot
}

// mergeFastLocked overlays a fast frame: the client set with the full-only
// fields carried forward, and the health fields a fast frame carries.
func (s *Server) mergeFastLocked(update telemetry.Update, now time.Time) {
	stats := &s.stats
	stats.FastFrames++
	stats.LastFast = now
	if update.UptimeSeconds != 0 {
		stats.UptimeSeconds = update.UptimeSeconds
	}
	if update.Present.Has(60) {
		stats.ConfigTimestamp = update.ConfigTimestamp
	}
	stats.Health.MergeFast(update.Health, update.Present)
	s.clockSkewLocked(update, now)
	if update.Present.Has(33) || len(s.associated) != 0 {
		s.associated = telemetry.MergeClients(s.associated, update.Clients, true)
		s.joinClientsLocked()
	}
	s.driftLocked(update, now)
}

// joinClientsLocked derives each associated client's radio fields from the
// latest full frame's radios (design §5.3).
func (s *Server) joinClientsLocked() {
	if len(s.associated) == 0 {
		return
	}
	clients := make([]telemetry.Client, 0, len(s.associated))
	for _, client := range s.associated {
		clients = append(clients, client)
	}
	telemetry.JoinClients(clients, s.stats.Radios)
	for _, client := range clients {
		s.associated[client.MAC] = client
	}
}

// cpuBusyLocked derives the busy fraction from the total CPU jiffies of
// two full frames.
func (s *Server) cpuBusyLocked(cpu *telemetry.CPUTimes) {
	previous := s.observe.lastCPU
	if cpu == nil {
		return
	}
	current := *cpu
	s.observe.lastCPU = &current
	if previous == nil || current.Total() <= previous.Total() || current.Idle < previous.Idle {
		s.stats.CPUBusy = nil
		return
	}
	total := float64(current.Total() - previous.Total())
	busy := 1 - float64(current.Idle-previous.Idle)/total
	busy = math.Max(0, math.Min(1, busy))
	s.stats.CPUBusy = &busy
}

// clockSkewLocked records receive time minus the AP clock (field 5) and
// warns once per session when it is above 30 s (design §5.2).
func (s *Server) clockSkewLocked(update telemetry.Update, now time.Time) {
	if update.Health == nil || update.Health.When.IsZero() || !update.Present.Has(5) {
		return
	}
	skew := now.Sub(update.Health.When).Seconds()
	s.stats.ClockSkewSeconds = &skew
	if math.Abs(skew) <= clockSkewWarn.Seconds() || s.observe.clockWarned {
		return
	}
	s.observe.clockWarned = true
	s.counters.clockSkewWarnings.Add(1)
	direction := "behind"
	if skew < 0 {
		direction = "ahead of"
	}
	s.emitLocked(Event{Kind: EventClock, Severity: SeverityWarning,
		Title: fmt.Sprintf("AP clock is %.0f s %s the controller", math.Abs(skew), direction)})
}

// portsLocked stores the ports with rx/tx bits per second from the byte
// counters of the previous full frame (0 after a counter reset).
func (s *Server) portsLocked(ports []telemetry.Port, now time.Time) {
	next := append([]telemetry.Port(nil), ports...)
	elapsed := now.Sub(s.observe.lastPortsAt).Seconds()
	if !s.observe.lastPortsAt.IsZero() && elapsed > 0 {
		for index := range next {
			for _, previous := range s.observe.lastPorts {
				if previous.Name != next[index].Name {
					continue
				}
				if next[index].RxBytes >= previous.RxBytes {
					next[index].RxBps = float64(next[index].RxBytes-previous.RxBytes) * 8 / elapsed
				}
				if next[index].TxBytes >= previous.TxBytes {
					next[index].TxBps = float64(next[index].TxBytes-previous.TxBytes) * 8 / elapsed
				}
			}
		}
	}
	s.observe.lastPorts = append([]telemetry.Port(nil), ports...)
	s.observe.lastPortsAt = now
	s.stats.Ports = next
}

// kmsgLocked appends this frame's kernel log lines to the ring (last 200),
// skipping lines already in it (the AP repeats recent lines).
func (s *Server) kmsgLocked(lines []string) {
	if len(lines) == 0 {
		return
	}
	if s.observe.kmsgSeen == nil {
		s.observe.kmsgSeen = make(map[string]bool)
	}
	for _, line := range lines {
		key := kmsgKey(line)
		if s.observe.kmsgSeen[key] {
			continue
		}
		s.observe.kmsgSeen[key] = true
		s.stats.KMsg = append(s.stats.KMsg, line)
	}
	if len(s.stats.KMsg) > maxKMsgRing {
		for _, dropped := range s.stats.KMsg[:len(s.stats.KMsg)-maxKMsgRing] {
			delete(s.observe.kmsgSeen, kmsgKey(dropped))
		}
		s.stats.KMsg = append([]string(nil), s.stats.KMsg[len(s.stats.KMsg)-maxKMsgRing:]...)
	}
}

// kmsgKey is a kernel log line's "priority,sequence" prefix, or the line.
func kmsgKey(line string) string {
	if first := strings.IndexByte(line, ','); first > 0 {
		if second := strings.IndexByte(line[first+1:], ','); second > 0 {
			return line[:first+1+second]
		}
	}
	return line
}

// radioEventsLocked raises channel-change and radar events for the access
// radios between full frames.
func (s *Server) radioEventsLocked(now time.Time) {
	if s.observe.channels == nil {
		s.observe.channels, s.observe.radar = make(map[int]int), make(map[int]bool)
	}
	for _, radio := range s.stats.Radios {
		if radio.Role != telemetry.RoleAccess || radio.Channel == 0 {
			continue
		}
		previous, known := s.observe.channels[radio.Index]
		s.observe.channels[radio.Index] = radio.Channel
		if known && previous != radio.Channel {
			data, _ := json.Marshal(map[string]any{"radio": radio.Dev, "band": radio.Band, "from": previous, "to": radio.Channel, "width_mhz": radio.WidthMHz})
			s.emitLocked(Event{At: now, Kind: EventRadio, Severity: SeverityInfo,
				Title: fmt.Sprintf("%s GHz radio moved from channel %d to %d", radio.Band, previous, radio.Channel), Data: data})
		}
		if radio.DFS.RadarDetected && !s.observe.radar[radio.Index] {
			s.emitLocked(Event{At: now, Kind: EventRadio, Severity: SeverityWarning,
				Title: fmt.Sprintf("Radar detected on the %s GHz radio (channel %d)", radio.Band, radio.Channel)})
		}
		s.observe.radar[radio.Index] = radio.DFS.RadarDetected
	}
}

// inactiveVLANsLocked raises an event when field 79 lists a VLAN that an
// enabled WLAN of the served config uses, and resolves it when the VLAN
// is active again (design §5.8).
func (s *Server) inactiveVLANsLocked(now time.Time) {
	used := wlanVLANs(s.config)
	inactive := make(map[int]bool, len(s.stats.InactiveVLANs))
	for _, vlan := range s.stats.InactiveVLANs {
		inactive[vlan] = true
	}
	if s.observe.inactiveVLANs == nil {
		s.observe.inactiveVLANs = make(map[int]string)
	}
	for vlan, ssids := range used {
		if !inactive[vlan] {
			continue
		}
		if _, raised := s.observe.inactiveVLANs[vlan]; raised {
			continue
		}
		id := newEventID(now)
		s.observe.inactiveVLANs[vlan] = id
		data, _ := json.Marshal(map[string]any{"vlan": vlan, "ssids": ssids})
		s.emitLocked(Event{ID: id, At: now, Kind: EventInactiveVLAN, Severity: SeverityWarning,
			Title:  fmt.Sprintf("VLAN %d is not active on the AP's uplink", vlan),
			Detail: "used by " + strings.Join(ssids, ", "), Data: data})
	}
	for vlan, id := range s.observe.inactiveVLANs {
		if inactive[vlan] {
			continue
		}
		delete(s.observe.inactiveVLANs, vlan)
		s.emitLocked(Event{At: now, Kind: EventInactiveVLAN, Severity: SeverityInfo,
			Title: fmt.Sprintf("VLAN %d is active on the AP's uplink again", vlan), Corrects: []string{id}})
	}
}

// wlanVLANs returns the VLANs enabled WLANs of a config use, with their
// SSIDs. Keys match the way the firmware decodes JSON (case-insensitive).
func wlanVLANs(config []byte) map[int][]string {
	var parsed struct {
		WLANs []struct {
			SSID    string
			Enabled bool
			Vlans   []json.RawMessage
		}
	}
	if json.Unmarshal(config, &parsed) != nil {
		return nil
	}
	result := make(map[int][]string)
	for _, wlan := range parsed.WLANs {
		if !wlan.Enabled || len(wlan.Vlans) == 0 {
			continue
		}
		var text string
		var number int
		vlan := 0
		if json.Unmarshal(wlan.Vlans[0], &text) == nil {
			vlan, _ = strconv.Atoi(strings.TrimSpace(text))
		} else if json.Unmarshal(wlan.Vlans[0], &number) == nil {
			vlan = number
		}
		if vlan >= 1 && vlan <= 4094 {
			result[vlan] = append(result[vlan], cleanHelloString(wlan.SSID, 32))
		}
	}
	return result
}

// driftGrace is how long the reported config Timestamp may differ from
// the served one before it counts as drift (a delivery and apply take a
// few seconds; a reconnect push up to a minute).
const driftGrace = 2 * time.Minute

// driftLocked compares field 60 with the served Timestamp on every stats
// frame and raises a config event when they have differed for longer than
// driftGrace.
func (s *Server) driftLocked(update telemetry.Update, now time.Time) {
	if !update.Present.Has(60) || s.configTS == 0 || update.ConfigTimestamp == 0 {
		return
	}
	if math.Abs(update.ConfigTimestamp-s.configTS) < 0.000001 {
		if s.status.ConfigDrift {
			s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityInfo, Title: "AP runs the served configuration again"})
		}
		s.observe.driftSince, s.status.ConfigDrift = time.Time{}, false
		return
	}
	if s.observe.driftSince.IsZero() {
		s.observe.driftSince = now
	}
	if !s.status.ConfigDrift && now.Sub(s.observe.driftSince) >= s.timings.Drift {
		s.status.ConfigDrift = true
		s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityWarning,
			Title:  "AP runs a different configuration than the one served",
			Detail: fmt.Sprintf("the AP reports Timestamp %.6f; dismist serves %.6f (revision %d)", update.ConfigTimestamp, s.configTS, s.revision)})
	}
}

// bootCountLocked detects a reboot from a higher boot count (field 71)
// than the hello or an earlier frame reported.
func (s *Server) bootCountLocked(update telemetry.Update, now time.Time) *Reboot {
	if update.Health == nil || update.Health.BootCount == 0 {
		return nil
	}
	count := update.Health.BootCount
	previous := s.observe.bootCount
	if count > s.observe.bootCount {
		s.observe.bootCount = count
	}
	if previous == 0 || count <= previous {
		return nil
	}
	reboot := Reboot{
		DetectedAt: now, Evidence: RebootEvidenceBootCount, BootCount: count,
		PreviousBootCount: previous, Uptime: int64(update.UptimeSeconds),
	}
	s.recordRebootLocked(reboot, fmt.Sprintf("boot count %d (was %d)", count, previous))
	return &reboot
}

// appliedLocked records an ep-config-applied report (design §5.5).
func (s *Server) appliedLocked(update telemetry.Update, now time.Time) {
	applied := telemetry.Applied{ConfigTimestamp: update.ConfigTimestamp, Error: update.Error, Firmware: update.Firmware}
	if update.Applied != nil {
		applied = *update.Applied
	}
	previous := s.status.Applied
	s.status.Applied = ConfigApplied{
		At: now, ConfigTimestamp: applied.ConfigTimestamp, Error: applied.Error,
		Good5Min: applied.Good5Min, RecoveredFromBadConfig: applied.RecoveredFromBadConfig,
		PowerConstrained: applied.PowerConstrained, Count: applied.Count, AuditID: applied.AuditID,
	}
	switch {
	case applied.Error != "":
		s.counters.configRejections.Add(1)
		s.status.LastError = applied.Error
		s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityError, Title: "Configuration rejected", Detail: applied.Error})
	case s.configTS > 0 && math.Abs(applied.ConfigTimestamp-s.configTS) < 0.000001:
		if s.status.AppliedRevision != s.revision {
			s.status.AppliedRevision = s.revision
			s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityInfo, Title: "Configuration applied",
				Detail: fmt.Sprintf("revision %d", s.revision)})
		}
		s.status.LastError = ""
	}
	if applied.RecoveredFromBadConfig && !previous.RecoveredFromBadConfig {
		s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityWarning,
			Title:  "AP recovered from a bad configuration",
			Detail: fmt.Sprintf("it rolled back and runs Timestamp %.6f", applied.ConfigTimestamp)})
	}
}

// paceEventLocked stores a PACE event: typed for OnEvent, in the buffer for
// the legacy UI, and as recent client activity. Noise (PACE 42 "No
// packets.") is only counted.
func (s *Server) paceEventLocked(event *telemetry.Event, now time.Time) {
	if event == nil {
		return
	}
	if event.Noise {
		s.counters.paceNoise.Add(1)
		return
	}
	typed := paceEvent(*event, now)
	s.emitLocked(typed)
	if isClientActivity(s.auth.ID, *event) {
		s.clients[event.ClientMAC] = ClientActivity{
			MAC: event.ClientMAC, IP: event.ClientIP,
			LastEvent: event.Title, LastSeen: typed.At,
		}
		if len(s.clients) > 1024 {
			s.pruneClientsLocked()
		}
	}
}

// pruneClientsLocked keeps the 512 most recent client activities.
func (s *Server) pruneClientsLocked() {
	cutoff := time.Time{}
	times := make([]time.Time, 0, len(s.clients))
	for _, client := range s.clients {
		times = append(times, client.LastSeen)
	}
	if len(times) > 512 {
		// The 512th newest time is the cutoff.
		for range 512 {
			newest := 0
			for index := range times {
				if times[index].After(times[newest]) {
					newest = index
				}
			}
			cutoff = times[newest]
			times[newest] = time.Time{}
		}
	}
	for mac, client := range s.clients {
		if client.LastSeen.Before(cutoff) {
			delete(s.clients, mac)
		}
	}
}
