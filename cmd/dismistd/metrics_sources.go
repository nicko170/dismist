package main

import (
	"crypto/tls"
	"flag"
	"log"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/metrics"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/rfplan"
	"github.com/nicko170/dismist/internal/telemetry"
)

// storeSchema reads meta.schema and meta.compat for dismist_store_schema and
// dismist_store_compat.
func storeSchema(store *model.BoltStore) func() (int, int, bool) {
	return func() (int, int, bool) {
		info, err := store.Schema()
		if err != nil {
			return 0, 0, false
		}
		return int(info.Schema), int(info.Compat), true
	}
}

// compileStatus reports each registered AP's compile state from its control
// session (design §4.4 per-AP isolation): failing while it is served its
// delivered config or none, and the failures its session has counted.
func compileStatus(manager *controller.Manager) func() map[string]metrics.CompileStatus {
	return func() map[string]metrics.CompileStatus {
		servers := manager.Servers()
		result := make(map[string]metrics.CompileStatus, len(servers))
		for id, server := range servers {
			status := server.Status()
			result[id] = metrics.CompileStatus{
				Failing:  status.ConfigSource != "" && status.ConfigSource != controller.ConfigSourceCompiled,
				Failures: server.Counters().CompileErrors,
			}
		}
		return result
	}
}

// storeUsage reports the database size (dismist_store_size_bytes) and the
// free space of the filesystem holding it (dismist_store_free_bytes).
func storeUsage(store *model.BoltStore) func() (metrics.StoreUsage, bool) {
	return func() (metrics.StoreUsage, bool) {
		size, err := store.Size()
		if err != nil {
			return metrics.StoreUsage{}, false
		}
		usage := metrics.StoreUsage{SizeBytes: size}
		if space, err := model.DiskUsage(filepath.Dir(store.Path())); err == nil {
			usage.FreeBytes, usage.HasFree = int64(space.Free), true
		}
		return usage, true
	}
}

// migrationState reads meta.migration_state for
// dismist_schema_migration_state.
func migrationState(store *model.BoltStore) func() (string, bool) {
	return func() (string, bool) {
		state, err := migrate.ReadState(store)
		if err != nil || state.State == "" {
			return "", false
		}
		return state.State, true
	}
}

// backupMetrics reports the scheduled backups for the dismist_backup_*
// series.
func backupMetrics(scheduler *backupScheduler) func() (metrics.BackupStatus, bool) {
	return func() (metrics.BackupStatus, bool) {
		if scheduler == nil {
			return metrics.BackupStatus{}, false
		}
		view := scheduler.Status()
		status := metrics.BackupStatus{Bytes: view.UsedBytes, Failures: scheduler.Failures()}
		if view.LastSuccess != nil {
			status.LastSuccess = *view.LastSuccess
		}
		return status, true
	}
}

// buildInfo labels dismist_build_info.
func buildInfo() metrics.BuildInfo {
	version, revision := buildVersion()
	return metrics.BuildInfo{Version: version, Revision: revision, GoVersion: runtime.Version()}
}

// certificatesFromFlags reads the served chain (-tls-cert, -tls-key) and the
// onboarding CAs (-ap-ca-file, or ca.crt next to the certificate) the
// daemon was started with, for the dismist_tls_certificate_* series. The
// daemon restarts when the certificate is renewed (exit 75), so reading
// them once is enough. It returns nil when the flags are not defined or the
// files cannot be read.
func certificatesFromFlags(flags *flag.FlagSet) []metrics.Certificate {
	value := func(name string) string {
		if f := flags.Lookup(name); f != nil {
			return f.Value.String()
		}
		return ""
	}
	certPath, keyPath := value("tls-cert"), value("tls-key")
	if certPath == "" || keyPath == "" {
		return nil
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil
	}
	cas, err := loadAPCAs(value("ap-ca-file"), certPath)
	if err != nil {
		log.Printf("metrics: onboarding CA certificates: %v", err)
	}
	return metrics.CertificateSeries(pair.Certificate, cas)
}

// apDetail maps one AP session onto the metrics view of telemetry v2 and
// status v2 (design §8.2: B's telemetry.Update v2 and Status v2, A's
// rf_plan), keeping the AP's own units (the collector converts).
func apDetail(store model.StateStore) func(string, metrics.Session) metrics.APDetail {
	return func(apID string, session metrics.Session) metrics.APDetail {
		var detail metrics.APDetail
		snapshot := session.TelemetrySnapshot()
		detail.Telemetry = apTelemetry(snapshot)
		status := session.Status()
		if status.Verification != nil {
			detail.Verified = true
			detail.VerifyMismatch = !status.Verification.OK || status.VerifyMismatch
		}
		if counting, ok := session.(interface{ Counters() controller.Counters }); ok {
			counters := counting.Counters()
			for mode, results := range counters.Pushes {
				for result, count := range results {
					detail.Pushes = append(detail.Pushes, metrics.PushCount{Mode: mode, Result: result, Count: count})
				}
			}
			sort.Slice(detail.Pushes, func(i, j int) bool {
				if detail.Pushes[i].Mode != detail.Pushes[j].Mode {
					return detail.Pushes[i].Mode < detail.Pushes[j].Mode
				}
				return detail.Pushes[i].Result < detail.Pushes[j].Result
			})
			if len(counters.Telemetry) > 0 {
				detail.Topics = map[string]uint64{}
				for topic, count := range counters.Telemetry {
					detail.Topics[topic] = count
				}
			}
		}
		if store != nil {
			detail.PlannedChannels = plannedChannels(store.Snapshot(), apID)
		}
		return detail
	}
}

// plannedChannels is the channel dismist wants each access radio of an AP
// on: its static channel, else the site plan (design §3.10).
func plannedChannels(state model.State, apID string) map[string]int {
	for _, ap := range state.APs {
		if ap.ID != apID {
			continue
		}
		var result map[string]int
		for _, band := range []string{telemetry.Band24, telemetry.Band5} {
			channel := rfplan.Static(ap, band)
			if channel == 0 {
				if plan, ok := ap.RFPlan[band]; ok {
					channel = plan.Channel
				}
			}
			if channel != 0 {
				if result == nil {
					result = map[string]int{}
				}
				result[band] = channel
			}
		}
		return result
	}
	return nil
}

// phyName names 31.26 PhyType (9 is 802.11ax on the AP33).
func phyName(phy int) string {
	switch phy {
	case 0:
		return ""
	case 9:
		return "ax"
	}
	return "phy" + strconv.Itoa(phy)
}

// apTelemetry copies the controller's telemetry v2 snapshot into the
// metrics view, field by field (internal/metrics/telemetry.go).
func apTelemetry(snapshot controller.TelemetrySnapshot) metrics.APTelemetry {
	health := snapshot.Health
	view := metrics.APTelemetry{
		IntervalSeconds: health.IntervalSeconds, Configured: health.Configured, ConfigTimestamp: snapshot.ConfigTimestamp,
		BootCount: health.BootCount, ClmVersion: health.CLMVersion,
	}
	if snapshot.ClockSkewSeconds != nil {
		view.ClockSkewSeconds, view.HasClockSkew = *snapshot.ClockSkewSeconds, true
	}
	if health.Memory != nil {
		view.MemoryTotalKB, view.MemoryAvailableKB = health.Memory.TotalKB, health.Memory.AvailableKB
	}
	if health.Load != nil {
		view.LoadAverage = append([]float64(nil), health.Load.Average[:]...)
	}
	if cpu := health.CPU; cpu != nil {
		view.CPUJiffies = map[string]uint64{
			"user": cpu.User, "nice": cpu.Nice, "system": cpu.System, "idle": cpu.Idle,
			"iowait": cpu.IOWait, "irq": cpu.IRQ, "softirq": cpu.SoftIRQ, "steal": cpu.Steal,
		}
	}
	if health.RW != nil {
		view.RWTotalKB, view.RWAvailableKB = health.RW.TotalKB, health.RW.AvailableKB
	}
	if temperature := health.Temperature; temperature != nil {
		if temperature.CPU != nil {
			view.CPUTemperatureCelsius = *temperature.CPU
		}
		if temperature.Ambient != nil {
			view.AmbientTemperatureCelsius = *temperature.Ambient
		}
	}
	if cloud := health.Cloud; cloud != nil {
		switch {
		case cloud.TCPRTTSeconds != nil:
			view.TCPRTTSeconds = *cloud.TCPRTTSeconds
		case cloud.LastRTTSeconds != nil:
			view.TCPRTTSeconds = *cloud.LastRTTSeconds
		}
		if cloud.WebSocketRTTSeconds != nil {
			view.WebSocketRTTSeconds = *cloud.WebSocketRTTSeconds
		}
	}
	if agent := health.Agent; agent != nil {
		view.AgentHeapBytes, view.AgentRSSBytes, view.AgentGoroutines, view.HasAgent = agent.AllocBytes, agent.RSSBytes, uint64(agent.Goroutines), true
	}
	for _, port := range snapshot.Ports {
		view.Ports = append(view.Ports, metrics.Port{
			Name: port.Name, Up: port.Up, FullDuplex: port.FullDuplex, Mbps: uint64(port.SpeedMbps),
			RxBytes: port.RxBytes, RxPackets: port.RxPackets, TxBytes: port.TxBytes, TxPackets: port.TxPackets,
			RxErrors: port.Errors.RxErrors, RxFCSErrors: port.Errors.RxFCS, RxDiscards: port.Errors.RxDiscards, TxErrors: port.Errors.TxErrors,
		})
	}
	for _, power := range snapshot.LLDPPower {
		view.LLDPPower = append(view.LLDPPower, metrics.LLDPPower{Port: power.APPort,
			AllocatedMilliwatts: uint64(power.AllocatedMW), RequestedMilliwatts: uint64(power.RequestedMW)})
	}
	for _, neighbour := range snapshot.LLDP {
		view.LLDPNeighbors = append(view.LLDPNeighbors, metrics.LLDPNeighbor{Port: neighbour.APPort,
			ChassisID: neighbour.ChassisID, PortID: neighbour.PortID, SystemName: neighbour.SystemName})
	}
	if power := snapshot.Power; power != nil {
		mode := strings.ToLower(strings.TrimSpace(power.ConstrainedMode))
		view.Power = &metrics.Power{Source: power.Source, AvailableMilliwatts: uint64(power.AvailableMW),
			NeededMilliwatts: uint64(power.NeededMW), DrawMilliwatts: uint64(power.MeasuredDrawMW),
			BudgetMilliwatts: int64(power.BudgetMW), Constrained: mode != "" && mode != "none" && mode != "off" && mode != "unconstrained"}
	}
	for _, vlan := range snapshot.InactiveVLANs {
		if vlan > 0 && vlan < 4096 {
			view.InactiveVLANs = append(view.InactiveVLANs, uint16(vlan))
		}
	}
	for _, radio := range snapshot.Radios {
		entry := metrics.Radio{
			Dev: radio.Dev, Band: radio.Band, Role: radio.Role, PHY: phyName(radio.PHY),
			Channel: uint64(max(radio.Channel, 0)), SecondaryChannel: uint64(max(radio.Secondary, 0)), BandwidthMHz: radio.WidthMHz,
			NoiseFloorDBm: int64(radio.NoiseFloorDBm), TxPowerDBm: int64(radio.TxPowerDBm), MaxTxPowerDBm: int64(radio.MaxTxPowerDBm),
			Clients: uint64(max(radio.Clients, 0)), Missing: radio.Missing,
			RadarDetected: radio.DFS.RadarDetected, CACState: uint64(max(radio.DFS.CACState, 0)),
			Counters: metrics.RadioCounters{
				TxRetries: radio.Counters.TxRetries, TxFailed: radio.Counters.TxFailed, RxDecryptFailures: radio.Counters.RxDecryptFailures,
				Reinits: radio.Counters.ReInits, TxStalls: radio.Counters.TxStalls, Beacons: radio.Counters.Beacons,
				RxBadFCS: radio.Counters.RxBadFCS, RxBadPLCP: radio.Counters.RxBadPLCP, RxGlitches: radio.Counters.RxGlitches,
				ProbeRequests: radio.Counters.ProbeRequests,
			},
		}
		switch {
		case radio.ChanUtil != nil:
			entry.Utilization, entry.UtilizationMax, entry.HasUtilization = radio.ChanUtil.Mean.Map(), radio.ChanUtil.Max.All, true
		case radio.Utilization != nil:
			entry.Utilization, entry.HasUtilization = radio.Utilization.Map(), true
		}
		view.Radios = append(view.Radios, entry)
	}
	for _, vap := range snapshot.VAPs {
		wlan := metrics.WLAN{ID: vap.WLANID, RadioIndex: vap.RadioIndex, Clients: uint64(max(vap.Clients, 0)), HasClients: true,
			TxMulticastPackets: vap.TxMcastPkts, TxMulticastBytes: vap.TxMcastBytes}
		if vap.Eapol != nil {
			wlan.EapolInvalidMIC, wlan.EapolMsg1Retries, wlan.EapolMsg3Retries = vap.Eapol.RxInvalidMIC, vap.Eapol.TxMsg1Retries, vap.Eapol.TxMsg3Retries
		}
		view.WLANs = append(view.WLANs, wlan)
	}
	for _, client := range snapshot.AssociatedClients {
		view.Clients = append(view.Clients, metrics.Client{
			MAC: client.MAC, RadioIndex: client.RadioIndex, WLANID: client.WLANID, RSSIDBm: client.RSSI, Protocol: client.Protocol,
			TxBitsPerSecond: client.TxBps, RxBitsPerSecond: client.RxBps, TxRateMbps: client.TxMbps, RxRateMbps: client.RxMbps,
			RxBytes: client.RxBytes, TxBytes: client.TxBytes, TxRetries: client.TxRetries, ConnectedSeconds: client.ConnectedSeconds,
			Hostname: client.Name,
		})
	}
	return view
}
