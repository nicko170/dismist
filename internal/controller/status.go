package controller

import (
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/telemetry"
)

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
	// persisted in ap_status by the observer). LastKnownVAPsAt is when it
	// was reported.
	LastKnownVAPs   []telemetry.VAP `json:"last_known_vaps,omitempty"`
	LastKnownVAPsAt time.Time       `json:"last_known_vaps_at,omitzero"`
}
