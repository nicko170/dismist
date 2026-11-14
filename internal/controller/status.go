package controller

import (
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/telemetry"
)

// Status is one AP's control-session state (v2). It is a value: every
// slice and pointer in a Status returned by Server.Status is a copy.
type Status struct {
	Connected       bool      `json:"connected"`
	ConnectedAt     time.Time `json:"connected_at"`
	ConnectionCount uint64    `json:"connection_count"`
	TLSCipherSuite  string    `json:"tls_cipher_suite"`
	LastDisconnect  time.Time `json:"last_disconnect"`
	Firmware        string    `json:"firmware"`
	Model           string    `json:"model,omitempty"`
	LastSeen        time.Time `json:"last_seen"`
	AppliedRevision uint64    `json:"applied_revision"`
	LastError       string    `json:"last_error,omitempty"`
	RemoteAddress   string    `json:"remote_address,omitempty"`
	// Hello holds the extra fields of the last authenticated hello, from
	// the tolerant pass (design §5.4); HelloAt is when it arrived.
	Hello   HelloExtras `json:"hello,omitzero"`
	HelloAt time.Time   `json:"hello_at,omitzero"`
	// LastDisconnectCause says why the last authenticated session ended
	// (controller disconnect, closed by the AP, read error, replaced by a
	// new connection, protocol error).
	LastDisconnectCause string `json:"last_disconnect_cause,omitempty"`
	// ConfigSource is ConfigSourceCompiled, ConfigSourceDelivered or
	// ConfigSourceNone.
	ConfigSource string `json:"config_source,omitempty"`
	// CompileError is the compile failure behind a delivered or absent
	// config; cleared when a compiled config is set.
	CompileError string `json:"compile_error,omitempty"`

	// Status v2 (phase 1, design §5.4, §5.5, §5.7).

	// DesiredConfigTimestamp is the Timestamp of the config dismist
	// serves this AP (dismist_ap_desired_config_timestamp_seconds).
	DesiredConfigTimestamp float64 `json:"desired_config_timestamp,omitempty"`
	// ConfigRequest is the AP's last ep-config request: its applied
	// Timestamp, rollback flag and capability tags.
	ConfigRequest ConfigRequest `json:"config_request,omitzero"`
	// Applied is the AP's last ep-config-applied report.
	Applied ConfigApplied `json:"applied,omitzero"`
	// TunnelIPv6 is the AP's tunnel address from its last ep-ip-register
	// (diagnostic only); TunnelIPv6At is when it arrived.
	TunnelIPv6   string    `json:"tunnel_ipv6,omitempty"`
	TunnelIPv6At time.Time `json:"tunnel_ipv6_at,omitzero"`
	// LastRebootDetected is when dismist last saw the AP had rebooted
	// (a higher boot count in a hello or frame, or an uptime reset).
	LastRebootDetected time.Time `json:"last_reboot_detected,omitzero"`
	// LastFileOffer is the last crash report the AP offered (ep-file).
	LastFileOffer *FileOffer `json:"last_file_offer,omitempty"`
	// ConfigDrift is true while the config Timestamp the AP reports (field
	// 60) has differed from the served one for longer than the drift
	// grace period.
	ConfigDrift bool `json:"config_drift,omitempty"`

	// Status v2 (phase 2 to 4, design §5.6, §5.9).

	// PushMode is how a changed config reaches the AP now: PushModeNotify
	// (config_change_notify on and not learned "reconnect") or
	// PushModeReconnect.
	PushMode string `json:"push_mode"`
	// Push is the last push's lifecycle state.
	Push *PushStatus `json:"push,omitempty"`
	// Verification is the last post-delivery verification; VerifyMismatch
	// is true while it failed (the AP flag verify_mismatch).
	Verification   *Verification `json:"verification,omitempty"`
	VerifyMismatch bool          `json:"verify_mismatch,omitempty"`
	// Capabilities are what dismist can do with this AP.
	Capabilities Capabilities `json:"capabilities"`
	// LocateUntil is when the locate state expires (zero: not locating).
	LocateUntil time.Time `json:"locate_until,omitzero"`
}

// Capabilities are the AP's protocol capabilities (APDetail
// capabilities, design §4.9).
type Capabilities struct {
	// ConfigChangeNotify is the learned ep-config-change capability: nil
	// unknown (never pushed with notify), true worked, false fell back to
	// reconnect twice in a row.
	ConfigChangeNotify *bool `json:"config_change_notify"`
	// APCmd is true when ap-cmd-rx commands may be sent (feature ap_cmd).
	// APCmdVerified is true when the AP's firmware is in the verified
	// command table (apcmd_verified.go).
	APCmd         bool `json:"ap_cmd"`
	APCmdVerified bool `json:"ap_cmd_verified"`
	// Pong2 is true when ping2 is answered with Pong2 (feature pong2).
	Pong2 bool `json:"pong2"`
}

// ConfigRequest is what the AP sent in its last ep-config request (design
// §5.5): recorded after the reply, never used to build it.
type ConfigRequest struct {
	At                     time.Time `json:"at"`
	Firmware               string    `json:"firmware,omitempty"`
	MfgCountryCode         string    `json:"mfg_country_code,omitempty"`
	Accept                 string    `json:"accept,omitempty"`
	RecoveredFromBadConfig bool      `json:"recovered_from_bad_config"`
	AppliedConfigTimestamp float64   `json:"applied_config_timestamp"`
	TimeoutSec             int64     `json:"timeout_sec,omitempty"`
	AuditID                string    `json:"audit_id,omitempty"`
	CLMVersion             string    `json:"clm_version,omitempty"`
	// Capabilities are the request's Tags (emptydns, wpa1pairwise,
	// allwlans, owe, dns6, ipv6, 304, 429, csa, gtkrekeyoff, disablepmtu),
	// sorted and deduplicated.
	Capabilities []string `json:"capabilities,omitempty"`
}

// HasCapability reports whether the AP advertised tag in its last
// ep-config request.
func (r ConfigRequest) HasCapability(tag string) bool {
	for _, known := range r.Capabilities {
		if strings.EqualFold(known, tag) {
			return true
		}
	}
	return false
}

// ConfigApplied is the AP's last ep-config-applied report (design §5.5).
type ConfigApplied struct {
	At                     time.Time `json:"at"`
	ConfigTimestamp        float64   `json:"config_timestamp"`
	Error                  string    `json:"error,omitempty"`
	Good5Min               bool      `json:"good_5min"`
	RecoveredFromBadConfig bool      `json:"recovered_from_bad_config"`
	PowerConstrained       bool      `json:"power_constrained"`
	Count                  int       `json:"count,omitempty"`
	AuditID                string    `json:"audit_id,omitempty"`
}

// FileOffer is a file the AP offered with ep-file (a crash report).
type FileOffer struct {
	At   time.Time `json:"at"`
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Type string    `json:"type,omitempty"`
	// Discarded is true when dismist answered the offer with Discard
	// (feature file_discard); false when the offer was left unanswered.
	Discarded bool `json:"discarded"`
}

// TelemetrySnapshot is the AP's merged telemetry (v2): the latest full
// frame's sections, overlaid with every later fast frame. It is what the
// API, the SSE stream and the metrics read. Every slice is a copy.
type TelemetrySnapshot struct {
	FullFrames    uint64    `json:"full_frames"`
	FastFrames    uint64    `json:"fast_frames"`
	LastFull      time.Time `json:"last_full"`
	LastFast      time.Time `json:"last_fast"`
	UptimeSeconds uint64    `json:"uptime_seconds"`
	// VAPs are the WLANs the AP broadcasts now (field 32 of the last full
	// frame); empty while it broadcasts none.
	VAPs []telemetry.VAP `json:"vaps"`
	// Radios are field 31 with roles and bands from the model's radio
	// table. Each keeps the legacy "interface" and "width_mhz" keys.
	Radios            []telemetry.RadioStats `json:"radios"`
	RecentClients     []ClientActivity       `json:"recent_clients"`
	AssociatedClients []telemetry.Client     `json:"associated_clients"`
	Interfaces        []telemetry.Interface  `json:"interfaces"`
	Events            []ConnectionEvent      `json:"events"`

	// v2 (phase 1, design §5.2).

	// Model and Firmware are as the frames report them (19, 20).
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	// Health is the last full frame's Health with the newest fast frame's
	// When, Configured and Cloud.
	Health telemetry.Health `json:"health"`
	// ClockSkewSeconds is the controller's receive time minus the AP clock
	// (field 5) of the newest stats frame; positive means the AP is behind.
	ClockSkewSeconds *float64 `json:"clock_skew_s,omitempty"`
	// CPUBusy is the fraction of CPU time not idle between the last two
	// full frames (24.7 deltas); nil until two frames exist.
	CPUBusy *float64 `json:"cpu_busy,omitempty"`
	// ConfigTimestamp is the config Timestamp the AP reports running
	// (field 60, every frame).
	ConfigTimestamp float64                  `json:"config_timestamp,omitempty"`
	Probes          []telemetry.Probe        `json:"probes,omitempty"`
	Ports           []telemetry.Port         `json:"ports,omitempty"`
	LLDP            []telemetry.LLDPNeighbor `json:"lldp,omitempty"`
	LLDPPower       []telemetry.LLDPPower    `json:"lldp_power,omitempty"`
	Power           *telemetry.Power         `json:"power,omitempty"`
	IP              telemetry.IP             `json:"ip"`
	InactiveVLANs   []int                    `json:"inactive_vlans,omitempty"`
	// Scan is the latest neighbour scan (field 69; phase 3 feature, kept
	// in memory only).
	Scan *telemetry.Scan `json:"scan,omitempty"`
	// LastKnownVAPs is the last non-empty VAP list, kept while the AP
	// broadcasts nothing or is offline (the migration's BSSID-index input;
	// persisted in ap_status through Options.Status). LastKnownVAPsAt is
	// when it was reported.
	LastKnownVAPs   []telemetry.VAP `json:"last_known_vaps,omitempty"`
	LastKnownVAPsAt time.Time       `json:"last_known_vaps_at,omitzero"`
	// ScanAt is when Scan arrived.
	ScanAt time.Time `json:"scan_at,omitzero"`
	// DecodeErrors counts the sections skipped in the last full frame
	// (the cumulative counts are in Counters).
	DecodeErrors map[string]int `json:"decode_errors,omitempty"`

	// Phase 4: the AP kernel log ring (24.1, last 200 lines, oldest
	// first), BLE radio counters (38) and datapath diagnostics.
	KMsg []string             `json:"kmsg,omitempty"`
	BLE  []telemetry.BLERadio `json:"ble,omitempty"`
	Diag *telemetry.Diag      `json:"diag,omitempty"`
}
