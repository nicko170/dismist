package api

import "time"

// SSE event names of GET /api/sites/$id/stream (design §4.10). Each event's
// data is the JSON of the type named in StreamMessage.
//
//tygo:emit export type StreamMessage = | { type: "snapshot"; data: StreamSnapshot } | { type: "ap"; data: APLive } | { type: "event"; data: EventSummary } | { type: "config"; data: ConfigEvent } | { type: "push"; data: PushEvent } | { type: "pending"; data: PendingEvent };
const (
	StreamEventSnapshot = "snapshot"
	StreamEventAP       = "ap"
	StreamEventEvent    = "event"
	StreamEventConfig   = "config"
	StreamEventPush     = "push"
	StreamEventPending  = "pending"
)

// APLive is the `ap` event and one entry of a snapshot.
type APLive struct {
	ID              string         `json:"id"`
	Status          APStatus       `json:"status"`
	Connected       bool           `json:"connected"`
	AppliedRevision uint64         `json:"applied_revision"`
	ConfigRevision  uint64         `json:"config_revision"`
	LastError       string         `json:"last_error"`
	ClientCount     int            `json:"client_count"`
	UptimeSeconds   uint64         `json:"uptime_seconds"`
	LANIP           string         `json:"lan_ip"`
	RemoteAddress   string         `json:"remote_address"`
	Radios          []RadioSummary `json:"radios"`
	SeenAt          time.Time      `json:"seen_at"`
}

// StreamSnapshot is the `snapshot` event: on connect and after any
// `config` event.
type StreamSnapshot struct {
	SiteID   string   `json:"site_id"`
	Revision uint64   `json:"revision"`
	APs      []APLive `json:"aps"`
}

// EventSummary is the `event` event: an EventView without the client MAC,
// WLAN and data. The drawer fetches the full event.
type EventSummary struct {
	ID         string    `json:"id"`
	APID       string    `json:"ap_id"`
	Kind       string    `json:"kind"`
	Severity   Severity  `json:"severity"`
	Title      string    `json:"title"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Count      int       `json:"count"`
	ResolvedBy *string   `json:"resolved_by" tstype:"string | null,required"`
	HasClient  bool      `json:"has_client"`
}

// ActorKind says who made a change.
type ActorKind string

const (
	ActorKindSession ActorKind = "session"
	ActorKindToken   ActorKind = "token"
	ActorKindSystem  ActorKind = "system"
)

// Actor is who made a change (no secrets).
type Actor struct {
	Kind ActorKind `json:"kind"`
	Name string    `json:"name"`
}

// ConfigEvent is the `config` event, sent after a committed mutation.
// Kind is for example "wlan.updated", "site.updated", "ap.updated".
type ConfigEvent struct {
	Revision    uint64   `json:"revision"`
	Kind        string   `json:"kind"`
	ObjectID    string   `json:"object_id"`
	AffectedAPs []string `json:"affected_aps"`
	Actor       Actor    `json:"actor"`
}

// PushState is a config push lifecycle state (design §5.6, phase 2).
type PushState string

const (
	PushStateNotified          PushState = "notified"
	PushStateFetched           PushState = "fetched"
	PushStateApplied           PushState = "applied"
	PushStateRejected          PushState = "rejected"
	PushStateFallbackReconnect PushState = "fallback_reconnect"
	PushStateTimeout           PushState = "timeout"
)

// PushEvent is the `push` event (phase 2).
type PushEvent struct {
	APID     string    `json:"ap_id"`
	Revision uint64    `json:"revision"`
	State    PushState `json:"state"`
	Error    string    `json:"error"`
}

// PendingEvent is the `pending` event: the pending-AP count changed.
type PendingEvent struct {
	Count int `json:"count"`
}
