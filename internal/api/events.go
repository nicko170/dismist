package api

import "time"

// Severity is an event severity.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// EventView is one stored event (design §5.8).
type EventView struct {
	ID     string `json:"id"`
	SiteID string `json:"site_id"`
	APID   string `json:"ap_id"`
	APName string `json:"ap_name"`
	// Kind is for example "connection", "config", "dhcp_failure", "dns",
	// "rsn_config", "authorized", "port", "radar", "reboot", "backup".
	Kind       string         `json:"kind"`
	PaceType   *int           `json:"pace_type" tstype:"number | null,required"`
	Severity   Severity       `json:"severity"`
	Title      string         `json:"title"`
	Detail     string         `json:"detail"`
	ClientMAC  string         `json:"client_mac"`
	WLANID     string         `json:"wlan_id"`
	FirstSeen  time.Time      `json:"first_seen"`
	LastSeen   time.Time      `json:"last_seen"`
	Count      int            `json:"count"`
	ResolvedBy *string        `json:"resolved_by" tstype:"string | null,required"`
	Data       map[string]any `json:"data" tstype:"{ [key: string]: unknown }"`
}

// EventPage is one page of GET /api/sites/$id/events.
type EventPage struct {
	Events     []EventView `json:"events"`
	NextBefore *time.Time  `json:"next_before" tstype:"string | null,required"`
}
