package telemetry

import (
	"encoding/json"
	"time"
)

// This file is the telemetry.Update v2 contract (design §5, §8.2): the
// decoded form of one ep-telemetry message. Field numbers in comments are
// the protobuf struct tags of the AP's 0.15 pace binary (stats.APStats and
// its sub-messages). Proto3 rules apply: an absent field is zero, so a
// zero numeric value means "not reported" wherever the comment says so.
//
// Privacy (design P7): client hostnames, user agents, usernames and
// neighbour SSIDs/BSSIDs are personal or third-party data. They belong in
// the authenticated API only and must never become metric labels.

// Telemetry topics.
const (
	TopicStatsFull     = "ap-stats-full-"
	TopicStatsFast     = "ap-stats-fast-"
	TopicConfigApplied = "ep-config-applied-"
	TopicPaceEvent     = "ep-pace-event-"
	TopicClientEvent   = "client-pace-event-"
	// TopicCommandOutput carries the output of an ap-cmd-rx diagnostic
	// (ping, traceroute, arp) as {Topic, HashKey, Payload {Session,
	// Output}} (design §5.6, phase 4).
	TopicCommandOutput = "ap-cmd-output-"
)

// Band labels (API, metrics). BandScan labels the scanning radio, which
// reports band code 0 (2.4 GHz) while it sits on any channel.
const (
	Band24   = "2.4"
	Band5    = "5"
	Band6    = "6"
	BandScan = "scan"
)

// Radio roles (design §3.5 Radios tab): an access radio carries WLANs, the
// scan radio only scans and is excluded from client joins and access-radio
// alerts.
const (
	RoleAccess = "access"
	RoleScan   = "scan"
)

// Decode-error sections (Update.DecodeErrors keys and the section label of
// dismist_ap_telemetry_decode_errors_total). The list is fixed so the
// metric's label set stays bounded.
const (
	SectionFrame     = "frame"     // envelope or APStats bytes that do not scan
	SectionHealth    = "health"    // 5, 21, 23, 24, 70, 71, 75, 80, 84, 93
	SectionCloud     = "cloud"     // 48
	SectionRadio     = "radio"     // 31
	SectionProbe     = "probe"     // 36
	SectionWLAN      = "wlan"      // 32
	SectionClient    = "client"    // 33
	SectionInterface = "interface" // 26
	SectionPort      = "port"      // 25
	SectionLLDP      = "lldp"      // 56, 57
	SectionPower     = "power"     // 63
	SectionIP        = "ip"        // 28, 29, 50, 73, 87
	SectionVLANs     = "vlans"     // 79
	SectionScan      = "scan"      // 69
	SectionEvent     = "event"     // JSON PACE events
	SectionApplied   = "applied"   // ep-config-applied-
	SectionDiag      = "diag"      // 24.1, 38, 97, 103, 106 (phase 4)
)

// DecodeSections lists every decode-error section, in a fixed order.
func DecodeSections() []string {
	return []string{
		SectionFrame, SectionHealth, SectionCloud, SectionRadio, SectionProbe,
		SectionWLAN, SectionClient, SectionInterface, SectionPort, SectionLLDP,
		SectionPower, SectionIP, SectionVLANs, SectionScan, SectionEvent, SectionApplied,
		SectionDiag,
	}
}

// Update is one decoded ep-telemetry message (v2). Stats frames
// (ap-stats-full-, ap-stats-fast-) fill the stats sections; JSON messages
// fill Event or Applied. A section the frame did not carry is nil or
// empty; Present says which APStats fields were on the wire, so a fast
// frame (which carries only a few fields) never clears full-frame data.
type Update struct {
	Topic           string  `json:"topic"`
	APID            string  `json:"ap_id"`
	Model           string  `json:"model,omitempty"`            // 19
	Firmware        string  `json:"firmware,omitempty"`         // 20
	UptimeSeconds   uint64  `json:"uptime_seconds,omitempty"`   // 18
	ConfigTimestamp float64 `json:"config_timestamp,omitempty"` // 60, and ep-config-applied-
	// Error is the ep-config-applied- error ("" when the AP applied it).
	Error string `json:"error,omitempty"`

	Health        *Health        `json:"health,omitempty"`
	Radios        []RadioStats   `json:"radios,omitempty"`     // 31
	VAPs          []VAP          `json:"vaps,omitempty"`       // 32
	Clients       []Client       `json:"clients,omitempty"`    // 33
	Probes        []Probe        `json:"probes,omitempty"`     // 36
	Interfaces    []Interface    `json:"interfaces,omitempty"` // 26
	Ports         []Port         `json:"ports,omitempty"`      // 25
	LLDP          []LLDPNeighbor `json:"lldp,omitempty"`       // 56
	LLDPPower     []LLDPPower    `json:"lldp_power,omitempty"` // 57
	Power         *Power         `json:"power,omitempty"`      // 63
	IP            *IP            `json:"ip,omitempty"`         // 28, 29, 50, 73, 87
	InactiveVLANs []int          `json:"inactive_vlans,omitempty"`
	Scan          *Scan          `json:"scan,omitempty"` // 69

	// Phase 4 (design §5.2): the kernel log lines of this frame (24.1),
	// BLE radio counters (38) and datapath diagnostics (97, 103, 106).
	KMsg []string   `json:"kmsg,omitempty"`
	BLE  []BLERadio `json:"ble,omitempty"`
	Diag *Diag      `json:"diag,omitempty"`

	Event   *Event   `json:"event,omitempty"`
	Applied *Applied `json:"applied,omitempty"`
	// CommandOutput is an ap-cmd-output- message (phase 4).
	CommandOutput *CommandOutput `json:"command_output,omitempty"`

	// Present is the set of APStats field numbers the frame carried.
	Present FieldSet `json:"-"`
	// DecodeErrors counts skipped sub-messages and mistyped fields by
	// section (Section* constants). The rest of the frame still decodes.
	DecodeErrors map[string]int `json:"decode_errors,omitempty"`
}

// IsStats reports whether the update is an ap-stats frame.
func (u Update) IsStats() bool { return u.Topic == TopicStatsFull || u.Topic == TopicStatsFast }

// FieldSet is a set of protobuf field numbers below 128.
type FieldSet [2]uint64

// Has reports whether field number n is in the set.
func (s FieldSet) Has(n uint64) bool { return n < 128 && s[n/64]&(1<<(n%64)) != 0 }

func (s *FieldSet) add(n uint64) {
	if n < 128 {
		s[n/64] |= 1 << (n % 64)
	}
}

// Health is the AP's system state (design §3.5 Health and Connection
// cards). Fast frames carry only When, Configured and Cloud; the
// controller overlays those on the last full frame's Health (MergeFast).
type Health struct {
	// When is the AP clock when it built the frame (5).
	When time.Time `json:"when,omitzero"`
	// IntervalSeconds is the full-frame period (23, normally 60).
	IntervalSeconds float64 `json:"interval_seconds,omitempty"`
	Configured      bool    `json:"configured"`           // 21
	BootCount       uint64  `json:"boot_count,omitempty"` // 71
	// Country is the regulatory country in effect (75), for example "AU".
	Country    string `json:"country,omitempty"`
	Hostname   string `json:"hostname,omitempty"`    // 70
	CLMVersion string `json:"clm_version,omitempty"` // 93

	Memory      *Memory      `json:"memory,omitempty"`      // 24.2
	Load        *Load        `json:"load,omitempty"`        // 24.4
	RW          *Filesystem  `json:"rw,omitempty"`          // 24.6.1
	CPU         *CPUTimes    `json:"cpu,omitempty"`         // 24.7.1
	CPUs        []CPUTimes   `json:"cpus,omitempty"`        // 24.7.2
	Temperature *Temperature `json:"temperature,omitempty"` // 80.2
	Accel       *Vector3     `json:"accel,omitempty"`       // 80.1.1, g
	Agent       *Agent       `json:"agent,omitempty"`       // 84
	Cloud       *Cloud       `json:"cloud,omitempty"`       // 48
}

// MergeFast overlays the fields a fast frame carried (When, Configured,
// Cloud) on h, the Health of the last full frame. present is the fast
// frame's Update.Present: a field the frame did not carry keeps the full
// frame's value.
func (h *Health) MergeFast(fast *Health, present FieldSet) {
	if fast == nil {
		return
	}
	if present.Has(5) && !fast.When.IsZero() {
		h.When = fast.When
	}
	if present.Has(21) {
		h.Configured = fast.Configured
	}
	if present.Has(48) && fast.Cloud != nil {
		h.Cloud = fast.Cloud
	}
}

// Memory is /proc/meminfo in kB (24.2).
type Memory struct {
	TotalKB     uint64 `json:"total_kb"`
	AvailableKB uint64 `json:"available_kb"`
}

// Load is the load average (24.4): 1, 5 and 15 minutes.
type Load struct {
	Average [3]float64 `json:"average"`
	Running uint32     `json:"running"`
	Total   uint32     `json:"total"`
}

// Filesystem is the /rw overlay usage in kB (24.6.1).
type Filesystem struct {
	TotalKB     uint64 `json:"total_kb"`
	AvailableKB uint64 `json:"available_kb"`
}

// CPUTimes is one /proc/stat CPU line (24.7) in jiffies (USER_HZ 100).
type CPUTimes struct {
	Name    string `json:"name,omitempty"`
	User    uint64 `json:"user"`
	Nice    uint64 `json:"nice"`
	System  uint64 `json:"system"`
	Idle    uint64 `json:"idle"`
	IOWait  uint64 `json:"iowait"`
	IRQ     uint64 `json:"irq"`
	SoftIRQ uint64 `json:"softirq"`
	Steal   uint64 `json:"steal"`
}

// Total is the sum of every mode.
func (c CPUTimes) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// Modes returns the jiffies by mode name (dismist_ap_cpu_seconds_total).
func (c CPUTimes) Modes() map[string]uint64 {
	return map[string]uint64{
		"user": c.User, "nice": c.Nice, "system": c.System, "idle": c.Idle,
		"iowait": c.IOWait, "irq": c.IRQ, "softirq": c.SoftIRQ, "steal": c.Steal,
	}
}

// Temperature is the AP's sensors in °C (80.2); nil means not reported.
type Temperature struct {
	CPU     *float64 `json:"cpu,omitempty"`
	Ambient *float64 `json:"ambient,omitempty"`
}

// Vector3 is an accelerometer reading in g.
type Vector3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Agent is the pace agent's Go runtime (84).
type Agent struct {
	AllocBytes      uint64 `json:"alloc_bytes"`
	Goroutines      uint32 `json:"goroutines"`
	RSSBytes        uint64 `json:"rss_bytes"`
	HeapUnusedBytes uint64 `json:"heap_unused_bytes"`
}

// Cloud is CloudStats (48), both layouts: 0.8 and 0.14 send 1 LastRTT;
// 0.15 sends 6 WebSockRTT and 8 TCPSockRTT. RTTs are seconds; nil means
// not reported.
type Cloud struct {
	LastRTTSeconds      *float64 `json:"last_rtt_s,omitempty"`      // 48.1 (0.8, 0.14)
	WebSocketRTTSeconds *float64 `json:"websocket_rtt_s,omitempty"` // 48.6 (0.15)
	TCPRTTSeconds       *float64 `json:"tcp_rtt_s,omitempty"`       // 48.8 (0.15)
	PMTU                uint32   `json:"pmtu,omitempty"`            // 48.3
	SendMSS             uint32   `json:"send_mss,omitempty"`        // 48.4
	CachedIP            bool     `json:"cached_ip,omitempty"`       // 48.5
}

// RTT returns the best controller round-trip time in seconds and its
// socket kind ("tcp" or "websocket"): 48.8 TCP, else 48.6 WebSocket,
// else the legacy 48.1 (reported as "tcp", which is what it measured).
func (c *Cloud) RTT() (float64, string, bool) {
	switch {
	case c == nil:
		return 0, "", false
	case c.TCPRTTSeconds != nil:
		return *c.TCPRTTSeconds, "tcp", true
	case c.WebSocketRTTSeconds != nil:
		return *c.WebSocketRTTSeconds, "websocket", true
	case c.LastRTTSeconds != nil:
		return *c.LastRTTSeconds, "tcp", true
	}
	return 0, "", false
}

// RadioStats is one radio (31). Band and Role come from the AP model's
// radio table, never from BandCode alone: the scan radio reports band code
// 0 (2.4 GHz) on any channel (design §5.2).
type RadioStats struct {
	Dev string `json:"dev"` // 31.1, "r0"
	// Interface repeats Dev for the legacy /api/telemetry consumers; it is
	// dropped one release after v1.
	Interface string `json:"interface"`
	Index     int    `json:"index"`     // N of rN, -1 when Dev is not rN
	BandCode  int    `json:"band_code"` // 31.2 raw: 0 = 2.4, 1 = 5, 2 = 6
	Band      string `json:"band"`      // Band24, Band5, Band6 or BandScan
	Role      string `json:"role"`      // RoleAccess or RoleScan
	Channel   int    `json:"channel"`   // 31.3
	Secondary int    `json:"secondary_channel,omitempty"`
	// WidthMHz is 31.5 (key kept from the legacy field-36 radios).
	WidthMHz uint64 `json:"width_mhz"`
	PHY      int    `json:"phy,omitempty"` // 31.26, 9 = 802.11ax
	// NoiseFloorDBm (31.6), TxPowerDBm (31.7) and MaxTxPowerDBm (31.8)
	// are zigzag dBm; 0 means not reported. 31.7 is usually absent, so the
	// UI shows MaxTxPowerDBm when TxPowerDBm is 0.
	NoiseFloorDBm int  `json:"noise_floor_dbm,omitempty"`
	TxPowerDBm    int  `json:"tx_power_dbm,omitempty"`
	MaxTxPowerDBm int  `json:"max_tx_power_dbm,omitempty"`
	Clients       int  `json:"clients"`           // 31.22
	Missing       bool `json:"missing,omitempty"` // 31.23
	DFS           DFS  `json:"dfs"`               // 31.17
	// Utilization is the instantaneous breakdown (31.16, fractions).
	Utilization *UtilBreakdown `json:"utilization,omitempty"`
	// ChanUtil is the averaged channel utilisation (31.30).
	ChanUtil *ChannelUtilization `json:"chan_util,omitempty"`
	Counters RadioCounters       `json:"counters"`
}

// DFS is a radio's DFS state (31.17).
type DFS struct {
	RadarDetected bool `json:"radar_detected"`
	CACState      int  `json:"cac_state"`
}

// UtilBreakdown splits channel utilisation into fractions of airtime.
type UtilBreakdown struct {
	All             float64 `json:"all"`
	Tx              float64 `json:"tx"`
	RxInBSS         float64 `json:"rx_in_bss"`
	RxOtherBSS      float64 `json:"rx_other_bss"`
	UnknownWifi     float64 `json:"unknown_wifi"`
	NonWifi         float64 `json:"non_wifi"`
	UnDecodableWifi float64 `json:"undecodable_wifi"`
}

// Map returns the breakdown by metric type label
// (dismist_radio_channel_utilization_ratio{type}).
func (u UtilBreakdown) Map() map[string]float64 {
	return map[string]float64{
		"all": u.All, "tx": u.Tx, "rx_in_bss": u.RxInBSS, "rx_other_bss": u.RxOtherBSS,
		"unknown_wifi": u.UnknownWifi, "non_wifi": u.NonWifi, "undecodable_wifi": u.UnDecodableWifi,
	}
}

// ChannelUtilization is UtilizationResults (31.30): mean, min and max over
// PeriodSec seconds, plus the integer-percent "ChanIm" view.
type ChannelUtilization struct {
	Mean       UtilBreakdown `json:"mean"`
	Min        UtilBreakdown `json:"min"`
	Max        UtilBreakdown `json:"max"`
	PeriodSec  int           `json:"period_s,omitempty"`
	Count      int           `json:"count,omitempty"`
	Version    int           `json:"version,omitempty"`
	ChanImMean *ChanIm       `json:"chan_im_mean,omitempty"`
	ChanImMin  *ChanIm       `json:"chan_im_min,omitempty"`
	ChanImMax  *ChanIm       `json:"chan_im_max,omitempty"`
}

// ChanIm is ChanImStats: integer percentages of airtime.
type ChanIm struct {
	Tx          int `json:"tx"`
	RxInBSS     int `json:"rx_in_bss"`
	RxOtherBSS  int `json:"rx_other_bss"`
	UnknownWifi int `json:"unknown_wifi"`
	NonWifi     int `json:"non_wifi"`
	Idle        int `json:"idle"`
	TxOp        int `json:"tx_op"`
	TxGood      int `json:"tx_good"`
	TxBad       int `json:"tx_bad"`
}

// RadioCounters are a radio's cumulative counters: 31.18 Counter, 31.31
// MacStats, 31.27 ProbeCounter and 31.38 AuthStats. They reset when the
// AP (or the radio driver) restarts.
type RadioCounters struct {
	TxBytes           uint64 `json:"tx_bytes"`            // 18.1
	TxPackets         uint64 `json:"tx_packets"`          // 18.2
	TxMgmt            uint64 `json:"tx_mgmt"`             // 18.3
	TxErrors          uint64 `json:"tx_errors"`           // 18.4
	RxBytes           uint64 `json:"rx_bytes"`            // 18.5
	RxPackets         uint64 `json:"rx_packets"`          // 18.6
	RxMgmt            uint64 `json:"rx_mgmt"`             // 18.7
	RxErrors          uint64 `json:"rx_errors"`           // 18.8
	TxFailed          uint64 `json:"tx_failed"`           // 18.22
	TxRetries         uint64 `json:"tx_retries"`          // 18.23
	TxRetried         uint64 `json:"tx_retried"`          // 18.24
	RxDups            uint64 `json:"rx_dups"`             // 18.25
	RxRetried         uint64 `json:"rx_retried"`          // 18.26
	ReInits           uint64 `json:"reinits"`             // 18.29
	Down              uint64 `json:"down"`                // 18.46
	Up                uint64 `json:"up"`                  // 18.47
	RxDecryptFailures uint64 `json:"rx_decrypt_failures"` // 18.48
	TxStalls          uint64 `json:"tx_stalls"`           // 18.53
	Beacons           uint64 `json:"beacons"`             // 31.5 TxBcnFrm
	RxBadFCS          uint64 `json:"rx_bad_fcs"`          // 31.16
	RxBadPLCP         uint64 `json:"rx_bad_plcp"`         // 31.17
	RxGlitches        uint64 `json:"rx_glitches"`         // 31.18
	ProbeRequests     uint64 `json:"probe_requests"`      // 27.1 + 27.2
	ProbeRequestsRand uint64 `json:"probe_requests_random"`
	RxAuth            uint64 `json:"rx_auth"`            // 38.1
	TxAuthFail        uint64 `json:"tx_auth_fail"`       // 38.3
	RxAssocReq        uint64 `json:"rx_assoc_req"`       // 38.4
	TxAssocRespFail   uint64 `json:"tx_assoc_resp_fail"` // 38.6
	RxEAPOL           uint64 `json:"rx_eapol"`           // 38.7
	TxEAPOL           uint64 `json:"tx_eapol"`           // 38.8
	TxEAPOLFail       uint64 `json:"tx_eapol_fail"`      // 38.9
}

// Map returns every counter by its JSON name.
func (c RadioCounters) Map() map[string]uint64 {
	return map[string]uint64{
		"tx_bytes": c.TxBytes, "tx_packets": c.TxPackets, "tx_mgmt": c.TxMgmt, "tx_errors": c.TxErrors,
		"rx_bytes": c.RxBytes, "rx_packets": c.RxPackets, "rx_mgmt": c.RxMgmt, "rx_errors": c.RxErrors,
		"tx_failed": c.TxFailed, "tx_retries": c.TxRetries, "tx_retried": c.TxRetried,
		"rx_dups": c.RxDups, "rx_retried": c.RxRetried, "reinits": c.ReInits, "down": c.Down, "up": c.Up,
		"rx_decrypt_failures": c.RxDecryptFailures, "tx_stalls": c.TxStalls, "beacons": c.Beacons,
		"rx_bad_fcs": c.RxBadFCS, "rx_bad_plcp": c.RxBadPLCP, "rx_glitches": c.RxGlitches,
		"probe_requests": c.ProbeRequests, "probe_requests_random": c.ProbeRequestsRand,
		"rx_auth": c.RxAuth, "tx_auth_fail": c.TxAuthFail, "rx_assoc_req": c.RxAssocReq,
		"tx_assoc_resp_fail": c.TxAssocRespFail, "rx_eapol": c.RxEAPOL, "tx_eapol": c.TxEAPOL,
		"tx_eapol_fail": c.TxEAPOLFail,
	}
}

// Probe is ProbeStats (36): probe-request counters per radio. Until v1
// dismist misread this field as the radio record.
type Probe struct {
	Dev                   string `json:"dev"`       // 36.1
	BandCode              int    `json:"band_code"` // 36.2
	Channels              []int  `json:"channels,omitempty"`
	WidthMHz              int    `json:"width_mhz,omitempty"`
	RadioMAC              string `json:"radio_mac,omitempty"`
	ProbeRequests         uint64 `json:"probe_requests"`          // 36.6
	ProbeRequestsRandom   uint64 `json:"probe_requests_random"`   // 36.7
	ProbeRequestsBcast    uint64 `json:"probe_requests_bcast"`    // 36.8
	ProbeRequestsOffChann uint64 `json:"probe_requests_off_chan"` // 36.9
}

// VAP is one WLANStats entry (32): a WLAN broadcast on one radio. The
// interface, ssid and bssid JSON keys are the legacy ones.
type VAP struct {
	Interface string `json:"interface"` // 32.4 Dev, "wl1.1"
	SSID      string `json:"ssid"`      // 32.5
	BSSID     string `json:"bssid"`     // 32.3
	// WLANID is the WLAN UUID (32.1), which is the dismist network ID.
	WLANID     string `json:"wlan_id,omitempty"`
	RadioIndex int    `json:"radio_index"` // 32.2
	// Radio is "r<RadioIndex>"; Band is that radio's band label.
	Radio        string      `json:"radio"`
	Band         string      `json:"band,omitempty"`
	Clients      int         `json:"clients"`        // 32.6
	TxMcastPkts  uint64      `json:"tx_mcast_pkts"`  // 32.7
	TxMcastBytes uint64      `json:"tx_mcast_bytes"` // 32.8
	Eapol        *EapolStats `json:"eapol,omitempty"`
	OWE          bool        `json:"owe_transition,omitempty"` // 32.14
}

// WLANStats is the design's name (§8.2) for VAP.
type WLANStats = VAP

// EapolStats is a WLAN's EAPOL handshake counters (32.22).
type EapolStats struct {
	RxMsg2           uint64 `json:"rx_msg2"`             // 5
	RxMsg4           uint64 `json:"rx_msg4"`             // 6
	RxGroup2         uint64 `json:"rx_group2"`           // 7
	RxReplayed       uint64 `json:"rx_replayed"`         // 9
	RxInvalidMIC     uint64 `json:"rx_invalid_mic"`      // 11
	TxMsg1           uint64 `json:"tx_msg1"`             // 12
	TxMsg1Retries    uint64 `json:"tx_msg1_retries"`     // 13
	TxMsg3           uint64 `json:"tx_msg3"`             // 14
	TxMsg3Retries    uint64 `json:"tx_msg3_retries"`     // 15
	TxGroup          uint64 `json:"tx_group"`            // 16
	TxGroupRetries   uint64 `json:"tx_group_retries"`    // 17
	RxEapolFrames    uint64 `json:"rx_eapol_frames"`     // 27
	RxEapFrames      uint64 `json:"rx_eap_frames"`       // 28
	RxDecryptErrors  uint64 `json:"rx_decrypt_errors"`   // 8
	RxUnencrypted    uint64 `json:"rx_unencrypted"`      // 4
	RxShortFrame     uint64 `json:"rx_short_frame"`      // 1
	RxExceedsLength  uint64 `json:"rx_exceeds_length"`   // 2
	RxIgnored        uint64 `json:"rx_ignored"`          // 3
	RxRequests       uint64 `json:"rx_requests"`         // 10
	RxEapStarts      uint64 `json:"rx_eap_starts"`       // 18
	RxEapStartDrops  uint64 `json:"rx_eap_start_drops"`  // 19
	RxEapLogoffs     uint64 `json:"rx_eap_logoffs"`      // 20
	RxEapResponses   uint64 `json:"rx_eap_responses"`    // 22
	RxEapCodeRequest uint64 `json:"rx_eap_code_request"` // 23
}

// Client is one associated client (33), the full record (design §5.3).
// The first block of fields keeps the v0 JSON keys and types.
type Client struct {
	MAC              string  `json:"mac"`            // 33.1
	Name             string  `json:"name,omitempty"` // 33.44 Hostname (full frames only)
	IP               string  `json:"ip,omitempty"`   // 33.55
	SSID             string  `json:"ssid"`           // 33.5.2
	BSSID            string  `json:"bssid"`          // 33.4
	VLANID           uint64  `json:"vlan_id"`        // 33.47
	RSSI             int64   `json:"rssi"`           // 33.8, zigzag dBm
	AverageRSSI      int64   `json:"average_rssi"`   // 33.10, zigzag dBm
	ConnectedSeconds uint64  `json:"connected_seconds"`
	InactiveMillis   uint64  `json:"inactive_millis"`    // 33.7
	TxMbps           float64 `json:"tx_mbps"`            // 33.11 (full only), unverified unit
	RxMbps           float64 `json:"rx_mbps"`            // 33.22 (full only), unverified unit
	TxBytes          uint64  `json:"tx_bytes"`           // 33.12, AP to client
	RxBytes          uint64  `json:"rx_bytes"`           // 33.23, client to AP
	Auth             string  `json:"auth,omitempty"`     // 33.66 AKM
	Cipher           string  `json:"cipher,omitempty"`   // 33.67 Pairwise
	Protocol         string  `json:"protocol,omitempty"` // 33.52, "ax"

	RadioIndex     int      `json:"radio_index"` // 33.2
	BandCode       int      `json:"band_code"`   // 33.3
	WLANID         string   `json:"wlan_id,omitempty"`
	PerAntennaRSSI []int64  `json:"per_antenna_rssi,omitempty"` // 33.9
	TxPackets      uint64   `json:"tx_packets"`                 // 33.13
	TxPacketsSent  uint64   `json:"tx_packets_sent"`            // 33.14
	TxRetries      uint64   `json:"tx_retries"`                 // 33.15
	TxRetried      uint64   `json:"tx_retried"`                 // 33.16
	TxFailed       uint64   `json:"tx_failed"`                  // 33.17
	TxBps          uint64   `json:"tx_bps"`                     // 33.18, current
	TxPps          uint64   `json:"tx_pps"`                     // 33.19
	RxPackets      uint64   `json:"rx_packets"`                 // 33.24
	RxMcastPackets uint64   `json:"rx_mcast_packets"`           // 33.25
	RxMcastBytes   uint64   `json:"rx_mcast_bytes"`             // 33.26
	RxRetried      uint64   `json:"rx_retried"`                 // 33.27
	RxRetries      uint64   `json:"rx_retries"`                 // 33.28
	RxDups         uint64   `json:"rx_dups"`                    // 33.29
	RxDecryptFails uint64   `json:"rx_decrypt_failures"`        // 33.30
	RxBps          uint64   `json:"rx_bps"`                     // 33.31, current
	RxPps          uint64   `json:"rx_pps"`                     // 33.32
	UserAgent      string   `json:"user_agent,omitempty"`       // 33.45
	Username       string   `json:"username,omitempty"`         // 33.48
	Streams        int      `json:"streams,omitempty"`          // 33.53
	MultiPSKName   string   `json:"multi_psk_name,omitempty"`   // 33.54
	DualBand       bool     `json:"dual_band,omitempty"`        // 33.58
	BandwidthMHz   int      `json:"bandwidth_mhz,omitempty"`    // 33.80
	LastStatsAgo   int      `json:"last_stats_s_ago,omitempty"` // 33.83
	QoS            []QoS    `json:"qos,omitempty"`              // 33.85 [4]
	IPv6           []string `json:"ipv6,omitempty"`             // 33.90

	// Derived by JoinClients from the client's radio (33.2, never 33.3).
	Radio        string `json:"radio,omitempty"`
	Band         string `json:"band,omitempty"`
	Channel      int    `json:"channel,omitempty"`
	WidthMHz     int    `json:"width_mhz,omitempty"`
	SNR          *int   `json:"snr,omitempty"`
	SelfAssigned bool   `json:"self_assigned,omitempty"` // 33.55 in 169.254/16

	// present is the set of ClientStats fields the frame carried; the
	// fast-frame merge fills absent full-only fields from the last record.
	present FieldSet
}

// QoS is a client's traffic in one access category (33.85, index order
// BE, BK, VI, VO; the order is the Broadcom AC numbering, unverified).
type QoS struct {
	AC        string `json:"ac"`
	TxPackets uint64 `json:"tx_packets"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	RxBytes   uint64 `json:"rx_bytes"`
}

// Interface is one of the AP's own network interfaces (26) with its
// addresses in CIDR form.
type Interface struct {
	Name      string   `json:"name"`                 // 26.1
	VLAN      int      `json:"vlan,omitempty"`       // 26.2
	Addresses []string `json:"addresses,omitempty"`  // 26.3
	DADFailed []string `json:"dad_failed,omitempty"` // 26.16
}

// Port is one wired port (25). Error counters are omitted by the AP when
// zero.
type Port struct {
	Name        string     `json:"name"`                   // 25.1
	Up          bool       `json:"up"`                     // 25.2 Link
	FullDuplex  bool       `json:"full_duplex"`            // 25.3
	PoweredDown bool       `json:"powered_down,omitempty"` // 25.10
	SpeedMbps   uint32     `json:"speed_mbps,omitempty"`   // 25.5
	RxBytes     uint64     `json:"rx_bytes"`               // 25.6
	RxPackets   uint64     `json:"rx_packets"`             // 25.7
	TxBytes     uint64     `json:"tx_bytes"`               // 25.8
	TxPackets   uint64     `json:"tx_packets"`             // 25.9
	RxPeakBps   uint64     `json:"rx_peak_bps,omitempty"`  // 25.27
	TxPeakBps   uint64     `json:"tx_peak_bps,omitempty"`  // 25.28
	Errors      PortErrors `json:"errors"`
	// RxBps and TxBps are derived by the controller from the byte
	// counters of two full frames; 0 until two frames exist.
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
}

// PortErrors are a port's error counters (25.16 to 25.32).
type PortErrors struct {
	RxPause             uint64 `json:"rx_pause,omitempty"`
	RxErrors            uint64 `json:"rx_errors,omitempty"`
	RxUndersize         uint64 `json:"rx_undersize,omitempty"`
	RxOversize          uint64 `json:"rx_oversize,omitempty"`
	RxJabber            uint64 `json:"rx_jabber,omitempty"`
	RxAlignment         uint64 `json:"rx_alignment,omitempty"`
	RxFCS               uint64 `json:"rx_fcs,omitempty"`
	RxFragment          uint64 `json:"rx_fragment,omitempty"`
	RxSymbol            uint64 `json:"rx_symbol,omitempty"`
	RxOverrun           uint64 `json:"rx_overrun,omitempty"`
	RxDiscards          uint64 `json:"rx_discards,omitempty"`
	TxCollisions        uint64 `json:"tx_collisions,omitempty"`
	TxErrors            uint64 `json:"tx_errors,omitempty"`
	TxExcessiveCollison uint64 `json:"tx_excessive_collisions,omitempty"`
	TxUnderrun          uint64 `json:"tx_underrun,omitempty"`
}

// Map returns the non-zero error counters by name.
func (e PortErrors) Map() map[string]uint64 {
	all := map[string]uint64{
		"rx_pause": e.RxPause, "rx_errors": e.RxErrors, "rx_undersize": e.RxUndersize,
		"rx_oversize": e.RxOversize, "rx_jabber": e.RxJabber, "rx_alignment": e.RxAlignment,
		"rx_fcs": e.RxFCS, "rx_fragment": e.RxFragment, "rx_symbol": e.RxSymbol,
		"rx_overrun": e.RxOverrun, "rx_discards": e.RxDiscards, "tx_collisions": e.TxCollisions,
		"tx_errors": e.TxErrors, "tx_excessive_collisions": e.TxExcessiveCollison,
		"tx_underrun": e.TxUnderrun,
	}
	for name, value := range all {
		if value == 0 {
			delete(all, name)
		}
	}
	return all
}

// LLDPNeighbor is the switch seen over LLDP on one port (56). Strings are
// bounded (descriptions to 256 bytes, the rest to 64).
type LLDPNeighbor struct {
	APPort      string     `json:"ap_port,omitempty"`     // 56.21 (0.15)
	PortDesc    string     `json:"port_desc,omitempty"`   // 56.1
	SystemName  string     `json:"system_name,omitempty"` // 56.2
	SystemDesc  string     `json:"system_desc,omitempty"` // 56.3
	MgmtAddress string     `json:"mgmt_address,omitempty"`
	PortID      string     `json:"port_id,omitempty"` // 56.5
	AutonegSup  string     `json:"autoneg_supported,omitempty"`
	AutonegAdv  string     `json:"autoneg_advertised,omitempty"`
	MTU         int        `json:"mtu,omitempty"`         // 56.8
	NativeVLAN  int        `json:"native_vlan,omitempty"` // 56.9
	VLANs       []LLDPVLAN `json:"vlans,omitempty"`       // 56.10
	ChassisID   string     `json:"chassis_id,omitempty"`  // 56.18
	MgmtAddrs   []string   `json:"mgmt_addresses,omitempty"`
	Model       string     `json:"model,omitempty"`        // 56.16
	Vendor      string     `json:"vendor,omitempty"`       // 56.15
	Serial      string     `json:"serial,omitempty"`       // 56.14, else 56.20.1
	SoftwareRev string     `json:"software_rev,omitempty"` // 56.13
}

// LLDPVLAN is one VLAN the switch port carries.
type LLDPVLAN struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
}

// LLDPPower is the LLDP power negotiation on one port (57), in mW. 0.8
// and 0.14 send one entry; 0.15 sends one per port.
type LLDPPower struct {
	APPort      string `json:"ap_port,omitempty"` // 57.6 (0.15)
	Requests    uint32 `json:"requests"`          // 57.1
	Extended    bool   `json:"extended"`          // 57.2
	AllocatedMW uint32 `json:"allocated_mw"`      // 57.3 Pse_avail
	RequestedMW uint32 `json:"requested_mw"`      // 57.4 Pd_req
	NeededMW    uint32 `json:"needed_mw"`         // 57.5 Pd_needs
}

// Power is the AP's power manager (63), in mW.
type Power struct {
	Source          string   `json:"source,omitempty"` // 63.1, "LLDP"
	AvailableMW     uint32   `json:"available_mw"`     // 63.2
	NeededMW        uint32   `json:"needed_mw"`        // 63.3
	BudgetMW        int32    `json:"budget_mw"`        // 63.5, zigzag
	ConstrainedMode string   `json:"constrained_mode,omitempty"`
	Sources         []string `json:"sources,omitempty"` // 63.7 (0.15)
	MeasuredDrawMW  uint32   `json:"measured_draw_mw,omitempty"`
}

// Constrained reports whether the AP runs in a power-constrained mode.
func (p *Power) Constrained() bool {
	if p == nil {
		return false
	}
	switch p.ConstrainedMode {
	case "", "none", "None", "NONE", "off", "unconstrained":
		return false
	}
	return true
}

// IP is the AP's addressing (28, 87, 29, 73, 50).
type IP struct {
	DHCP       []DHCPLease  `json:"dhcp,omitempty"`        // 28
	DHCP6      []DHCP6Lease `json:"dhcp6,omitempty"`       // 87
	Routes     []Route      `json:"routes,omitempty"`      // 29
	Neighbors  []Neighbor   `json:"neighbors,omitempty"`   // 73
	DNSServers []string     `json:"dns_servers,omitempty"` // 50
}

// DefaultGateway returns the IPv4 default route's gateway and its MAC from
// the neighbour table, or "" when unknown.
func (ip *IP) DefaultGateway() (gateway, mac string) {
	if ip == nil {
		return "", ""
	}
	for _, route := range ip.Routes {
		if (route.Destination == "" || route.Destination == "0.0.0.0/0") && route.Gateway != "" {
			gateway = route.Gateway
			break
		}
	}
	if gateway == "" {
		for _, lease := range ip.DHCP {
			if lease.Gateway != "" {
				gateway = lease.Gateway
				break
			}
		}
	}
	for _, neighbor := range ip.Neighbors {
		if gateway != "" && neighbor.IP == gateway {
			mac = neighbor.MAC
		}
	}
	return gateway, mac
}

// DHCPLease is one DHCPv4 client (28).
type DHCPLease struct {
	Dev          string    `json:"dev"`   // 28.1
	State        string    `json:"state"` // 28.2, "BOUND"
	RenewedAt    time.Time `json:"renewed_at,omitzero"`
	LeaseSeconds uint32    `json:"lease_seconds,omitempty"`
	Address      string    `json:"address,omitempty"` // 28.6
	Prefix       int       `json:"prefix,omitempty"`
	Server       string    `json:"server,omitempty"`  // 28.10
	Gateway      string    `json:"gateway,omitempty"` // 28.11
	DNS          []string  `json:"dns,omitempty"`     // 28.12
	Domain       string    `json:"domain,omitempty"`  // 28.13
	NTP          []string  `json:"ntp,omitempty"`     // 28.14
	ProxyURL     string    `json:"proxy_url,omitempty"`
	Error        string    `json:"error,omitempty"` // 28.9
}

// DHCP6Lease is one DHCPv6 client (87).
type DHCP6Lease struct {
	Dev          string    `json:"dev"`
	State        string    `json:"state"`
	RenewedAt    time.Time `json:"renewed_at,omitzero"`
	LeaseSeconds uint32    `json:"lease_seconds,omitempty"`
	Server       string    `json:"server,omitempty"`
	Addresses    []string  `json:"addresses,omitempty"`
	DNS          []string  `json:"dns,omitempty"`
	Domains      []string  `json:"domains,omitempty"`
	NTP          []string  `json:"ntp,omitempty"`
	ProxyURL     string    `json:"proxy_url,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Route is an IPv4 route (29); Destination "" is the default route.
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
}

// Neighbor is an IPv4 ARP entry (73).
type Neighbor struct {
	IP  string `json:"ip"`
	MAC string `json:"mac"`
}

// Scan is the neighbour scan (69), strongest first and at most
// MaxScanNeighbors per band. Neighbour SSIDs and BSSIDs are third-party
// data: authenticated API only, never metrics.
type Scan struct {
	ReportAll    bool           `json:"report_all,omitempty"`
	Uptime       int            `json:"uptime,omitempty"`
	StatsVersion int            `json:"stats_version,omitempty"`
	Band24       []ScanNeighbor `json:"band_24,omitempty"` // 69.7
	Band5        []ScanNeighbor `json:"band_5,omitempty"`  // 69.8
	Band6        []ScanNeighbor `json:"band_6,omitempty"`  // 69.14
	Channels     []ScanChannel  `json:"channels,omitempty"`
}

// MaxScanNeighbors bounds each band's neighbour list (design §5.10).
const MaxScanNeighbors = 200

// ScanNeighbor is one BSS heard by the scan (ScanResults).
type ScanNeighbor struct {
	SSID          string `json:"ssid,omitempty"`
	Hidden        bool   `json:"hidden,omitempty"`
	BSSID         string `json:"bssid"`
	RSSI          int    `json:"rssi"`
	Channel       int    `json:"channel"`
	Secondary     int    `json:"secondary_channel,omitempty"`
	WidthMHz      int    `json:"width_mhz,omitempty"`
	ListenChannel int    `json:"listen_channel,omitempty"`
	// Stations and Utilization come from the BSS load element (QBSS);
	// Utilization is 0 to 255. Nil when the BSS advertises none.
	QBSS *QBSS `json:"qbss,omitempty"`
}

// QBSS is a neighbour's BSS load element.
type QBSS struct {
	Stations    int `json:"stations"`
	Utilization int `json:"utilization"`
	Capacity    int `json:"capacity,omitempty"`
}

// ScanChannel is one scanned channel's statistics (69.9).
type ScanChannel struct {
	Channel     int           `json:"channel"`
	WidthMHz    int           `json:"width_mhz,omitempty"`
	BandCode    int           `json:"band_code"`
	NoiseDBm    int           `json:"noise_dbm,omitempty"`
	NoiseMinDBm int           `json:"noise_min_dbm,omitempty"`
	NoiseMaxDBm int           `json:"noise_max_dbm,omitempty"`
	Utilization UtilBreakdown `json:"utilization"`
	When        time.Time     `json:"when,omitzero"`
}

// Event is a PACE event (JSON ep-pace-event- or client-pace-event-),
// typed per design §5.8. The first block keeps the v0 fields.
type Event struct {
	When      time.Time `json:"when"`
	Type      int       `json:"type"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	ClientMAC string    `json:"client_mac,omitempty"`
	ClientIP  string    `json:"client_ip,omitempty"`

	// ID is the PACE event ID (a time UUID); a re-sent event with the same
	// ID and IsUpdate updates the stored one.
	ID       string   `json:"id,omitempty"`
	IsUpdate bool     `json:"is_update,omitempty"`
	Corrects []string `json:"corrects,omitempty"`
	Duration int      `json:"duration,omitempty"`
	// Kind and Severity are the typed classification (Kind* constants).
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	WLANID   string `json:"wlan_id,omitempty"`
	// Data holds the type's fields as JSON, at most MaxEventData bytes.
	Data json.RawMessage `json:"data,omitempty"`
	// Noise marks an event that is counted but not stored (PACE 42
	// "No packets.").
	Noise bool `json:"noise,omitempty"`
}

// MaxEventData caps Event.Data (design §5.8, model.MaxEventData).
const MaxEventData = 4096

// Event kinds (design §5.8) for PACE events.
const (
	KindDHCPFailure   = "dhcp_failure"
	KindDNS           = "dns"
	KindDisassociated = "disassociated"
	KindDNSServer     = "dns_server"
	KindDNSLatency    = "dns_latency"
	KindRSNConfig     = "rsn_config"
	KindPCAP          = "pcap"
	KindAuthorized    = "authorized"
	KindGTKRekey      = "gtk_rekey"
	KindPort          = "port"
	KindPace          = "pace"
)

// Event severities (model.Severity*).
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Applied is the ep-config-applied- report (design §5.5).
type Applied struct {
	ConfigTimestamp        float64 `json:"config_timestamp"`
	Error                  string  `json:"error,omitempty"`
	Firmware               string  `json:"firmware,omitempty"`
	Good5Min               bool    `json:"good_5min"`
	RecoveredFromBadConfig bool    `json:"recovered_from_bad_config"`
	PowerConstrained       bool    `json:"power_constrained"`
	Count                  int     `json:"count,omitempty"`
	AuditID                string  `json:"audit_id,omitempty"`
}

// BLERadio is one BLE radio's counters (38, BTRadioStats.BLE): enough to
// show "BLE alive" (phase 4).
type BLERadio struct {
	MAC          string `json:"mac,omitempty"` // 38.1.1
	TxBytes      uint64 `json:"tx_bytes"`      // 38.1.3
	TxCommands   uint64 `json:"tx_commands"`   // 38.1.4
	RxBytes      uint64 `json:"rx_bytes"`      // 38.1.5
	RxEvents     uint64 `json:"rx_events"`     // 38.1.6
	TxAdverts    uint64 `json:"tx_adverts"`    // 38.1.7
	Scans        uint64 `json:"scans"`         // 38.1.9
	RxScanPkts   uint64 `json:"rx_scan_pkts"`  // 38.1.10
	RxAdverts    uint64 `json:"rx_adverts"`    // 38.1.13
	TxErrors     uint64 `json:"tx_errors"`     // 38.1.16
	RxErrors     uint64 `json:"rx_errors"`     // 38.1.17
	ResetsHung   uint64 `json:"resets_hung"`   // 38.1.18
	ResetsHungRx uint64 `json:"resets_hung_rx"`
}

// Diag is the agent and datapath diagnostics (phase 4): 97 WxlanPkts,
// 103 PaceDispatchPkt and 106 LACP.
type Diag struct {
	Wxlan    []WxlanCounters   `json:"wxlan,omitempty"`
	Dispatch map[string]uint64 `json:"dispatch,omitempty"`
	LACP     *LACP             `json:"lacp,omitempty"`
}

// WxlanCounters is one WxlanStats entry (97).
type WxlanCounters struct {
	Dev       string `json:"dev"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
	ARPRx     uint64 `json:"arp_rx"`
	ARPReply  uint64 `json:"arp_reply"`
	ARPDrop   uint64 `json:"arp_drop"`
	BCMCDrop  uint64 `json:"bcmc_drop"`
	PktDrop   uint64 `json:"pkt_drop"`
}

// LACP is LACPStats (106).
type LACP struct {
	FailCount   uint64 `json:"fail_count"`
	ActivePorts uint64 `json:"active_ports"`
}

// CommandOutput is the payload of an ap-cmd-output- message: the output of
// one ap-cmd-rx diagnostic session.
type CommandOutput struct {
	Session string `json:"session"`
	Output  string `json:"output"`
}

// MaxCommandOutput bounds CommandOutput.Output.
const MaxCommandOutput = 16 << 10
