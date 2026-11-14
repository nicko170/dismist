package controller

import (
	"time"

	"github.com/nicko170/dismist/internal/telemetry"
)

// StatusSink persists an AP's last-known protocol facts in ap_status
// (design §4.1, §5.4, §8.4), so they survive a controller restart. The
// model's APStatusStore implements it through an adapter in
// cmd/dismistd; the controller never imports the model.
//
// Calls happen on the AP's session goroutine. SetVAPs is called on every
// full frame with a non-empty VAP list; the store writes only when the
// list changed (or to refresh its seen time).
type StatusSink interface {
	// RecordHello stores a new connection's hello and returns the one
	// stored before it (nil when none) and when it arrived.
	RecordHello(apID string, at time.Time, model string, hello HelloExtras) (previous *HelloExtras, previousAt time.Time, err error)
	// RecordReboot stores a detected reboot.
	RecordReboot(apID string, reboot Reboot) error
	// SetVAPs stores the current field-32 VAP list (Dev, BSSID, SSID,
	// WLAN ID, radio index).
	SetVAPs(apID string, at time.Time, vaps []telemetry.VAP) (changed bool, err error)
	// SetTunnelIPv6 stores the address of an ep-ip-register.
	SetTunnelIPv6(apID string, at time.Time, address string) error
	// SetConfigRequest stores the fields of an ep-config request.
	SetConfigRequest(apID string, request ConfigRequest) error
}

// Reboot is one detected AP reboot (design §5.4 "Reboot vs reconnect");
// it mirrors model.RebootRecord.
type Reboot struct {
	// DetectedAt is the controller time of the hello or frame that
	// revealed the reboot.
	DetectedAt time.Time `json:"detected_at"`
	// Evidence is RebootEvidenceBootCount or RebootEvidenceUptime.
	Evidence          string    `json:"evidence"`
	BootCount         uint64    `json:"boot_count,omitempty"`
	PreviousBootCount uint64    `json:"previous_boot_count,omitempty"`
	Uptime            int64     `json:"uptime_seconds,omitempty"`
	LastRebootTime    time.Time `json:"last_reboot_time,omitzero"`
	Reason            string    `json:"reason,omitempty"`
	BootState         string    `json:"boot_state,omitempty"`
}

// Reboot evidence.
const (
	RebootEvidenceBootCount = "boot_count"
	RebootEvidenceUptime    = "uptime"
)
