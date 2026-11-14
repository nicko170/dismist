package metrics

import "time"

// APDetail is what the collector exports for one registered AP beyond
// controller.Status, controller.TelemetrySnapshot and controller.Counters:
// the telemetry v2 decode and the status v2 facts (design §8.2). The daemon
// fills it per scrape from the AP's control session (Options.Detail); every
// field is optional and its zero value exports nothing.
type APDetail struct {
	// Telemetry is the decoded telemetry v2 state (design §5.2).
	Telemetry APTelemetry

	// VerifyMismatch is the post-delivery verification result (design §5.9);
	// Verified is false until a verification has run, and then
	// dismist_ap_config_verify_mismatch is not exported.
	Verified, VerifyMismatch bool

	// Pushes counts config pushes by mode and result (design §5.6).
	Pushes []PushCount

	// Topics counts telemetry messages by topic (design §6.3
	// dismist_ap_telemetry_messages_total). Keys outside TelemetryTopics are
	// exported as "other".
	Topics map[string]uint64

	// PlannedChannels is the channel dismist wants each access radio on, by
	// band ("2.4", "5"): the static per-AP channel or the site plan (design
	// §3.10). Absent bands export nothing.
	PlannedChannels map[string]int
}

// PushCount is one dismist_ap_config_pushes_total{mode,result} counter.
type PushCount struct {
	// Mode is "notify" or "reconnect"; Result is "applied", "rejected",
	// "fallback" or "timeout". Other values are exported as "other".
	Mode, Result string
	Count        uint64
}

// APTelemetry is the telemetry v2 decode of one AP (design §5.2), in the
// units the AP reports them: the collector converts (kB to bytes, mW to W,
// jiffies to seconds, percent-free ratios as they come). Field numbers are
// the stats.APStats protobuf fields of the 0.15 pace binary.
//
// Absent means zero (proto3) unless a Has* flag says otherwise; zero values
// that cannot be real readings (an RTT or a temperature of 0) are not
// exported.
type APTelemetry struct {
	// IntervalSeconds is the full-frame interval the AP reports (23). Zero
	// means the default 60 s. Telemetry series are exported only while the
	// AP is connected and its last full frame is younger than three
	// intervals (design §6.1 staleness gating).
	IntervalSeconds float64

	// Configured is 21.
	Configured bool
	// ClockSkewSeconds is the controller's receive time minus the AP clock
	// (5 When); HasClockSkew is false when 5 was absent.
	ClockSkewSeconds float64
	HasClockSkew     bool
	// ConfigTimestamp is the config Timestamp the AP reports it runs (60).
	ConfigTimestamp float64
	// BootCount is 71. The hello's BootCount is used when this is 0.
	BootCount uint64
	// ClmVersion is 93, exported as dismist_ap_info{clm}.
	ClmVersion string

	// MemoryTotalKB and MemoryAvailableKB are 24.2 MemInfo.
	MemoryTotalKB, MemoryAvailableKB uint64
	// LoadAverage is 24.4: the 1, 5 and 15 minute load averages.
	LoadAverage []float64
	// CPUJiffies is 24.7 Stat.TotalCPU in jiffies (USER_HZ 100) by mode:
	// user, nice, system, idle, iowait, irq, softirq, steal.
	CPUJiffies map[string]uint64
	// RWTotalKB and RWAvailableKB are 24.6 FS.RW, the /rw filesystem.
	RWTotalKB, RWAvailableKB uint64
	// CPUTemperatureCelsius and AmbientTemperatureCelsius are 80.2.
	CPUTemperatureCelsius, AmbientTemperatureCelsius float64
	// TCPRTTSeconds and WebSocketRTTSeconds are 48 CloudStats (0.15:
	// 8 TCPSockRTT and 6 WebSockRTT; 0.8/0.14: 1 LastRTT as TCP).
	TCPRTTSeconds, WebSocketRTTSeconds float64
	// AgentHeapBytes, AgentRSSBytes and AgentGoroutines are 84 PaceRuntime
	// (Alloc, GoRssApprox, NumGoRoutine); HasAgent is false when absent.
	AgentHeapBytes, AgentRSSBytes, AgentGoroutines uint64
	HasAgent                                       bool

	// Ports are 25 Switchports.
	Ports []Port
	// LLDPPower is 57 LLDP_pwr (one per port; a single message on 0.14).
	LLDPPower []LLDPPower
	// LLDPNeighbors is 56 LLDP_nd.
	LLDPNeighbors []LLDPNeighbor
	// Power is 63 PMGR_pwr; nil when absent.
	Power *Power
	// InactiveVLANs is 79 VLANs.InactiveWiredVLANs.
	InactiveVLANs []uint16

	// Radios are 31 RadioStats, including the scan radio.
	Radios []Radio
	// WLANs are 32 WLANStats.
	WLANs []WLAN
	// Clients are 33 ClientStats after the fast/full merge (design §5.3).
	Clients []Client
}

// Port is one 25 SwitchportStats entry.
type Port struct {
	// Name is 25.1 (eth0, eth1).
	Name string
	// Up is 25.2 Link; FullDuplex 25.3; Mbps 25.5.
	Up, FullDuplex bool
	Mbps           uint64
	// Byte and packet counters: 25.6 RxBytes, 25.7 RxPkts, 25.8 TxBytes,
	// 25.9 TxPkts.
	RxBytes, RxPackets, TxBytes, TxPackets uint64
	// Error counters: 25.17 RxErrors, 25.22 RxFCSErrors, 25.26
	// RxDiscards, 25.30 TxErrors.
	RxErrors, RxFCSErrors, RxDiscards, TxErrors uint64
}

// LLDPPower is one 57 LLDP_pwr entry, in milliwatts.
type LLDPPower struct {
	// Port is 57.6 AP_Port_name.
	Port string
	// AllocatedMilliwatts is 57.3 Pse_avail; RequestedMilliwatts 57.4 Pd_req.
	AllocatedMilliwatts, RequestedMilliwatts uint64
}

// LLDPNeighbor is one 56 LLDP_nd entry. Exported only with -metrics-names
// (the system name is a host name).
type LLDPNeighbor struct {
	// Port is 56.21 AP_Port_name; ChassisID 56.18; PortID 56.5;
	// SystemName 56.2.
	Port, ChassisID, PortID, SystemName string
}

// Power is 63 PMGR_pwr, in milliwatts.
type Power struct {
	// Source is 63.1 Pwr_src (for example "LLDP").
	Source string
	// AvailableMilliwatts is 63.2; NeededMilliwatts 63.3; BudgetMilliwatts
	// 63.5 (zigzag, negative when short); DrawMilliwatts 63.8 (0.15 only, 0
	// when absent).
	AvailableMilliwatts, NeededMilliwatts, DrawMilliwatts uint64
	BudgetMilliwatts                                      int64
	// Constrained is true when 63.6 ConstrainedMode is non-empty.
	Constrained bool
}

// Radio is one 31 RadioStats entry.
type Radio struct {
	// Dev is 31.1 (r0, r1, r2).
	Dev string
	// Band is "2.4" or "5" from the model's radio table (never from 31.2,
	// which the scan radio reports as 0); Role is "access" or "scan". A
	// scan radio is exported with band="scan".
	Band, Role string
	// PHY is 31.26 PhyType as a short name.
	PHY string
	// Channel 31.3, SecondaryChannel 31.4, BandwidthMHz 31.5.
	Channel, SecondaryChannel, BandwidthMHz uint64
	// NoiseFloorDBm is 31.6 (zigzag).
	NoiseFloorDBm int64
	// TxPowerDBm is 31.7 (usually absent); MaxTxPowerDBm 31.8.
	TxPowerDBm, MaxTxPowerDBm int64
	// Clients is 31.22 NumClients; Missing 31.23 RadioMissing.
	Clients uint64
	Missing bool
	// Utilization is 31.30.1 ChanUtil Mean as a 0-1 ratio by type: all,
	// tx, rx_in_bss, rx_other_bss, unknown_wifi, non_wifi,
	// undecodable_wifi. UtilizationMax is 31.30.3 Max.All. HasUtilization
	// is false when 31.30 was absent.
	Utilization    map[string]float64
	UtilizationMax float64
	HasUtilization bool
	// RadarDetected and CACState are 31.17 DFS.
	RadarDetected bool
	CACState      uint64
	// Counters are 31.18 Counter, 31.31 MacStats and 31.27 ProbeCounter.
	Counters RadioCounters
}

// RadioCounters are the cumulative radio counters.
type RadioCounters struct {
	TxRetries, TxFailed, RxDecryptFailures, Reinits, TxStalls, Beacons uint64
	RxBadFCS, RxBadPLCP, RxGlitches, ProbeRequests                     uint64
}

// WLAN is one 32 WLANStats entry (one VAP).
type WLAN struct {
	// ID is 32.1, the network UUID dismist sent as the WLAN ID.
	ID string
	// RadioIndex is 32.2 (the radio is "r<RadioIndex>").
	RadioIndex int
	// Clients is 32.6 NumClients; when HasClients is false the collector
	// counts Clients by WLAN ID and radio instead.
	Clients    uint64
	HasClients bool
	// TxMulticastPackets 32.7, TxMulticastBytes 32.8.
	TxMulticastPackets, TxMulticastBytes uint64
	// EAPOL counters from 32.22 EapolStats: 11 RxEapolInvalidmic, 13
	// TxEapolMsg1retries, 15 TxEapolMsg3retries.
	EapolInvalidMIC, EapolMsg1Retries, EapolMsg3Retries uint64
}

// Client is one associated client (33 ClientStats).
type Client struct {
	// MAC is 33.1, in any common notation; exported lower-case and dashed,
	// and only in per-client mode.
	MAC string
	// RadioIndex is 33.2 (the radio is "r<RadioIndex>"); WLANID 33.5.1.
	RadioIndex int
	WLANID     string
	// RSSIDBm is 33.8 RSSI (zigzag).
	RSSIDBm int64
	// Protocol is 33.52 ("ax", "ac", "n", ...).
	Protocol string
	// TxBitsPerSecond and RxBitsPerSecond are 33.18 Txbps and 33.31 Rxbps
	// (current throughput; units unverified, design §9 Q7).
	TxBitsPerSecond, RxBitsPerSecond uint64
	// TxRateMbps and RxRateMbps are 33.11 and 33.22 link rates (wifi.Rate as
	// decoded today; units unverified, design §9 Q7). Per-client mode only.
	TxRateMbps, RxRateMbps float64
	// RxBytes 33.23, TxBytes 33.12, TxRetries 33.15, ConnectedSeconds 33.6.
	RxBytes, TxBytes, TxRetries, ConnectedSeconds uint64
	// Hostname is 33.44; exported only in per-client mode with
	// -metrics-names.
	Hostname string
}

// BuildInfo labels dismist_build_info.
type BuildInfo struct {
	Version, Revision, GoVersion string
}

// Certificate is one certificate dismist serves or bundles for APs
// (dismist_tls_certificate_*_timestamp_seconds).
type Certificate struct {
	// Listener is "ap-wss" for the served chain and "ap-onboarding" for the
	// extra CAs of the onboarding bundle.
	Listener string
	// Role is "leaf", "intermediate" or "root".
	Role string
	// CN is the subject common name. A leaf's CN (a host name) is exported
	// only with -metrics-names.
	CN                  string
	NotBefore, NotAfter time.Time
}

// StoreUsage is the database size and the free space on its filesystem.
type StoreUsage struct {
	SizeBytes, FreeBytes int64
	// HasFree is false when the filesystem could not be measured.
	HasFree bool
}

// BackupStatus is the scheduled backup state.
type BackupStatus struct {
	// LastSuccess is zero until a snapshot succeeded.
	LastSuccess time.Time
	// Bytes is the space every kept snapshot uses together.
	Bytes int64
	// Failures counts skipped or failed snapshots by reason (space, io).
	Failures map[string]uint64
}
