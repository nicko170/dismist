package controller

import (
	"encoding/json"
	"time"
)

// Controller event kinds (design §5.8). PACE events keep the kinds of
// internal/telemetry (dhcp_failure, dns, ...).
const (
	EventConnection = "connection"
	EventReboot     = "reboot"
	EventConfig     = "config"
	EventClock      = "clock"
	EventFeature    = "feature"
	EventAuth       = "auth"
	EventFile       = "file"
	// EventAPLegacy is the kind PACE events keep in the in-memory buffer
	// (ConnectionEvent) that the legacy UI reads.
	EventAPLegacy = "ap_event"
)

// Event is one typed event for the events2 store (design §5.8), handed to
// Options.OnEvent. The observer adds the site and writes it: an event with
// an ID (a PACE event) is upserted by that ID; one without (a controller
// event) gets a UUIDv7 and is folded into an identical (AP, kind, title)
// event of the last 60 s. Corrects lists earlier PACE event IDs this one
// resolves.
type Event struct {
	APID      string          `json:"ap_id"`
	ID        string          `json:"id,omitempty"`
	Kind      string          `json:"kind"`
	PaceType  *int            `json:"pace_type,omitempty"`
	Severity  string          `json:"severity"`
	Title     string          `json:"title"`
	Detail    string          `json:"detail,omitempty"`
	ClientMAC string          `json:"client_mac,omitempty"`
	WLANID    string          `json:"wlan_id,omitempty"`
	At        time.Time       `json:"at"`
	IsUpdate  bool            `json:"is_update,omitempty"`
	Corrects  []string        `json:"corrects,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}
