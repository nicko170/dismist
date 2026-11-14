package controller

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/nicko170/dismist/internal/telemetry"
)

// Controller event kinds (design §5.8). PACE events keep the kinds of
// internal/telemetry (dhcp_failure, dns, ...).
const (
	EventConnection   = "connection"
	EventReboot       = "reboot"
	EventConfig       = "config"
	EventClock        = "clock"
	EventFeature      = "feature"
	EventAuth         = "auth"
	EventFile         = "file"
	EventPush         = "push"
	EventInactiveVLAN = "inactive_vlan"
	EventRadio        = "radio"
	EventAction       = "action"
	// EventAPLegacy is the kind PACE events keep in the in-memory buffer
	// (ConnectionEvent) that the legacy UI reads.
	EventAPLegacy = "ap_event"
)

// Event severities (telemetry and model use the same strings).
const (
	SeverityInfo    = telemetry.SeverityInfo
	SeverityWarning = telemetry.SeverityWarning
	SeverityError   = telemetry.SeverityError
)

// Event is one typed event for the events2 store (design §5.8), handed to
// Options.OnEvent. The observer adds the site and writes it: an event with
// an ID (a PACE event, or a controller event that a later one resolves)
// is upserted by that ID; one without (a controller event) gets a UUIDv7
// and is folded into an identical (AP, kind, title) event of the last
// 60 s. Corrects lists earlier event IDs this one resolves.
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

// maxBufferedEvents bounds the in-memory event buffer.
const maxBufferedEvents = 100

// emitLocked records an event: in the in-memory buffer (legacy UI, SSE)
// and, for OnEvent, in the pending list that flush delivers once the
// caller has released s.mu. Callers hold s.mu.
func (s *Server) emitLocked(event Event) {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if event.Severity == "" {
		event.Severity = SeverityInfo
	}
	event.APID = s.auth.ID
	event.Title = truncateText(event.Title, 200)
	event.Detail = truncateText(event.Detail, 1000)
	kind := event.Kind
	if event.PaceType != nil {
		kind = EventAPLegacy
	}
	buffered := ConnectionEvent{
		When: event.At, Kind: kind, Title: event.Title, Detail: event.Detail,
		ClientMAC: event.ClientMAC, ID: event.ID, Severity: event.Severity,
	}
	replaced := false
	if event.ID != "" {
		for index := range s.stats.Events {
			if s.stats.Events[index].ID == event.ID {
				s.stats.Events[index] = buffered
				replaced = true
				break
			}
		}
	}
	if !replaced {
		s.stats.Events = append(s.stats.Events, buffered)
		if len(s.stats.Events) > maxBufferedEvents {
			s.stats.Events = append([]ConnectionEvent(nil), s.stats.Events[len(s.stats.Events)-maxBufferedEvents:]...)
		}
	}
	if s.onEvent == nil {
		return
	}
	s.eventsMu.Lock()
	if len(s.pendingEvents) < 1024 {
		s.pendingEvents = append(s.pendingEvents, event)
	}
	s.eventsMu.Unlock()
}

// queuePushLocked queues a push event for flush.
func (s *Server) queuePushLocked(event PushEvent) {
	if s.onPush == nil {
		return
	}
	s.eventsMu.Lock()
	s.pendingPushes = append(s.pendingPushes, event)
	s.eventsMu.Unlock()
}

// flush delivers the pending events. It must be called without s.mu held,
// after every section that may have emitted one.
func (s *Server) flush() {
	s.eventsMu.Lock()
	events, pushes := s.pendingEvents, s.pendingPushes
	s.pendingEvents, s.pendingPushes = nil, nil
	s.eventsMu.Unlock()
	for _, event := range events {
		s.onEvent(event)
	}
	for _, push := range pushes {
		s.onPush(push)
	}
}

// paceEvent turns a decoded PACE event into the typed event.
func paceEvent(event telemetry.Event, received time.Time) Event {
	when := event.When
	if when.IsZero() {
		when = received
	}
	paceType := event.Type
	return Event{
		ID: event.ID, Kind: event.Kind, PaceType: &paceType, Severity: event.Severity,
		Title: event.Title, Detail: event.Text, ClientMAC: event.ClientMAC, WLANID: event.WLANID,
		At: when, IsUpdate: event.IsUpdate, Corrects: append([]string(nil), event.Corrects...),
		Data: event.Data,
	}
}

// newEventID returns a UUIDv7 for a controller event that a later event
// resolves (Corrects).
func newEventID(at time.Time) string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	millis := uint64(at.UnixMilli())
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], millis<<16)
	copy(id[:6], stamp[:6])
	id[6] = 0x70 | id[6]&0x0f
	id[8] = 0x80 | id[8]&0x3f
	text := hex.EncodeToString(id[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}
