package model

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Event severities.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// MaxEventData caps EventV2.Data (design §5.8).
const MaxEventData = 4096

// ControllerDedupWindow folds identical controller events (same AP, kind
// and title) into the previous record (design §5.8).
const ControllerDedupWindow = 60 * time.Second

// EventV2 is the stored event record (design §5.8).
type EventV2 struct {
	ID         string          `json:"id"`
	SiteID     string          `json:"site_id"`
	APID       string          `json:"ap_id"`
	Kind       string          `json:"kind"`
	PaceType   *int            `json:"pace_type,omitempty"`
	Severity   string          `json:"severity"`
	Title      string          `json:"title"`
	Detail     string          `json:"detail,omitempty"`
	ClientMAC  string          `json:"client_mac,omitempty"`
	WLANID     string          `json:"wlan_id,omitempty"`
	FirstSeen  time.Time       `json:"first_seen"`
	LastSeen   time.Time       `json:"last_seen"`
	Count      int             `json:"count"`
	ResolvedBy string          `json:"resolved_by,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// invTime is math.MaxUint64 - unixNano, big-endian: a forward cursor walks
// newest first (design §4.1).
func invTime(at time.Time) []byte {
	var out [8]byte
	binary.BigEndian.PutUint64(out[:], math.MaxUint64-uint64(at.UnixNano()))
	return out[:]
}

func fromInvTime(raw []byte) time.Time {
	return time.Unix(0, int64(math.MaxUint64-binary.BigEndian.Uint64(raw))).UTC()
}

// events2 key: site \x00 invTime(last_seen) \x00 ap \x00 id. Events are keyed
// by their last activity, so a recurring event moves to the top.
func event2Key(e EventV2) []byte {
	key := append([]byte(e.SiteID+"\x00"), invTime(e.LastSeen)...)
	return append(key, []byte("\x00"+e.APID+"\x00"+e.ID)...)
}

// events2_ap key: site \x00 ap \x00 invTime(last_seen) \x00 id.
func event2APKey(e EventV2) []byte {
	key := append([]byte(e.SiteID+"\x00"+e.APID+"\x00"), invTime(e.LastSeen)...)
	return append(key, []byte("\x00"+e.ID)...)
}

// NewEventID returns a UUIDv7 (time-ordered) for controller events.
func NewEventID(at time.Time) string {
	var id [16]byte
	ms := uint64(at.UnixMilli())
	id[0], id[1], id[2], id[3], id[4], id[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	_, _ = rand.Read(id[6:])
	id[6] = (id[6] & 0x0f) | 0x70
	id[8] = (id[8] & 0x3f) | 0x80
	text := hex.EncodeToString(id[:])
	return text[0:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// EventStore is the events2 buckets.
type EventStore struct{ db *bolt.DB }

// Events2 returns the v2 event store.
func (s *BoltStore) Events2() *EventStore { return &EventStore{db: s.db} }

type eventBuckets struct{ main, byID, byAP *bolt.Bucket }

func openEventBuckets(tx *bolt.Tx) (eventBuckets, error) {
	b := eventBuckets{tx.Bucket([]byte(BucketEvents2)), tx.Bucket([]byte(BucketEvents2ID)), tx.Bucket([]byte(BucketEvents2AP))}
	if b.main == nil || b.byID == nil || b.byAP == nil {
		return b, errors.New("events2 buckets missing")
	}
	return b, nil
}

func (b eventBuckets) load(id string) (EventV2, []byte, bool, error) {
	key := b.byID.Get([]byte(id))
	if key == nil {
		return EventV2{}, nil, false, nil
	}
	raw := b.main.Get(key)
	if raw == nil {
		return EventV2{}, nil, false, fmt.Errorf("events2_id %s points at a missing event", id)
	}
	var event EventV2
	if err := json.Unmarshal(raw, &event); err != nil {
		return EventV2{}, nil, false, err
	}
	return event, append([]byte(nil), key...), true, nil
}

func (b eventBuckets) remove(event EventV2, key []byte) error {
	if err := b.main.Delete(key); err != nil {
		return err
	}
	return b.byAP.Delete(event2APKey(event))
}

func (b eventBuckets) store(event EventV2) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	key := event2Key(event)
	if err := b.main.Put(key, raw); err != nil {
		return err
	}
	if err := b.byAP.Put(event2APKey(event), nil); err != nil {
		return err
	}
	return b.byID.Put([]byte(event.ID), key)
}

func normaliseEvent(event *EventV2) error {
	if event.SiteID == "" || event.ID == "" || event.Kind == "" {
		return errors.New("event needs site, ID and kind")
	}
	if event.LastSeen.IsZero() {
		event.LastSeen = event.FirstSeen
	}
	if event.FirstSeen.IsZero() {
		event.FirstSeen = event.LastSeen
	}
	if event.FirstSeen.IsZero() {
		return errors.New("event needs a time")
	}
	event.FirstSeen, event.LastSeen = event.FirstSeen.UTC(), event.LastSeen.UTC()
	if event.Count < 1 {
		event.Count = 1
	}
	if event.Severity == "" {
		event.Severity = SeverityInfo
	}
	if len(event.Data) > MaxEventData {
		return fmt.Errorf("event %s data is %d bytes, above %d", event.ID, len(event.Data), MaxEventData)
	}
	return nil
}

func mergeData(old, new json.RawMessage) json.RawMessage {
	if len(new) == 0 {
		return old
	}
	var a, b map[string]json.RawMessage
	if json.Unmarshal(old, &a) != nil || json.Unmarshal(new, &b) != nil {
		return new
	}
	for key, value := range b {
		a[key] = value
	}
	merged, err := json.Marshal(a)
	if err != nil || len(merged) > MaxEventData {
		return new
	}
	return merged
}

// Upsert stores events in one transaction (the observer's batch). An event
// whose ID is already stored is updated: count increases by the new count,
// last_seen moves forward, title, detail and severity take the new values
// and data is merged. It returns the stored records in input order.
func (e *EventStore) Upsert(events []EventV2) ([]EventV2, error) {
	stored := make([]EventV2, 0, len(events))
	err := e.db.Update(func(tx *bolt.Tx) error {
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := normaliseEvent(&event); err != nil {
				return err
			}
			if previous, key, found, err := b.load(event.ID); err != nil {
				return err
			} else if found {
				if err := b.remove(previous, key); err != nil {
					return err
				}
				event = foldEvent(previous, event)
			}
			if err := b.store(event); err != nil {
				return err
			}
			stored = append(stored, event)
		}
		return nil
	})
	return stored, err
}

func foldEvent(previous, next EventV2) EventV2 {
	result := previous
	result.Count += next.Count
	if next.LastSeen.After(result.LastSeen) {
		result.LastSeen = next.LastSeen
	}
	if next.FirstSeen.Before(result.FirstSeen) {
		result.FirstSeen = next.FirstSeen
	}
	result.Title, result.Detail, result.Severity = next.Title, next.Detail, next.Severity
	if next.ResolvedBy != "" {
		result.ResolvedBy = next.ResolvedBy
	}
	result.Data = mergeData(previous.Data, next.Data)
	return result
}

// AppendController stores a controller event: it gets a UUIDv7 when it has
// no ID, and an identical (AP, kind, title) event whose last_seen is within
// ControllerDedupWindow is incremented instead of adding a record.
func (e *EventStore) AppendController(event EventV2) (EventV2, error) {
	var stored EventV2
	err := e.db.Update(func(tx *bolt.Tx) error {
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		if event.ID == "" {
			at := event.LastSeen
			if at.IsZero() {
				at = event.FirstSeen
			}
			event.ID = NewEventID(at)
		}
		if err := normaliseEvent(&event); err != nil {
			return err
		}
		prefix := []byte(event.SiteID + "\x00" + event.APID + "\x00")
		cursor := b.byAP.Cursor()
		for key, _ := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, _ = cursor.Next() {
			rest := key[len(prefix):]
			if len(rest) < 9 {
				continue
			}
			at := fromInvTime(rest[:8])
			if event.LastSeen.Sub(at) > ControllerDedupWindow {
				break
			}
			previous, mainKey, found, err := b.load(string(rest[9:]))
			if err != nil || !found {
				continue
			}
			if previous.Kind == event.Kind && previous.Title == event.Title && previous.PaceType == nil {
				if err := b.remove(previous, mainKey); err != nil {
					return err
				}
				event = foldEvent(previous, event)
				break
			}
		}
		stored = event
		return b.store(event)
	})
	return stored, err
}

// Resolve sets resolved_by on stored events (an event with Corrects).
func (e *EventStore) Resolve(ids []string, by string) error {
	return e.db.Update(func(tx *bolt.Tx) error {
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			event, key, found, err := b.load(id)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			event.ResolvedBy = by
			raw, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if err := b.main.Put(key, raw); err != nil {
				return err
			}
		}
		return nil
	})
}

// Get returns one event by ID.
func (e *EventStore) Get(id string) (EventV2, error) {
	var event EventV2
	err := e.db.View(func(tx *bolt.Tx) error {
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		var found bool
		event, _, found, err = b.load(id)
		if err == nil && !found {
			return ErrNotFound
		}
		return err
	})
	return event, err
}

// EventQuery selects events newest first.
type EventQuery struct {
	SiteID string // required
	APID   string // optional: uses the per-AP index
	// Before returns events whose last_seen is strictly before it.
	Before   time.Time
	Limit    int // default 50, max 500
	Kind     string
	Severity string
	Client   string
}

func (q EventQuery) match(event EventV2) bool {
	return (q.Kind == "" || event.Kind == q.Kind) && (q.Severity == "" || event.Severity == q.Severity) &&
		(q.Client == "" || event.ClientMAC == q.Client) && (q.APID == "" || event.APID == q.APID)
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

// List returns events newest first and the last_seen to pass as Before for
// the next page (zero at the end).
func (e *EventStore) List(query EventQuery) ([]EventV2, time.Time, error) {
	if query.SiteID == "" {
		return nil, time.Time{}, errors.New("site ID required")
	}
	limit := clampLimit(query.Limit)
	result := make([]EventV2, 0)
	var next time.Time
	err := e.db.View(func(tx *bolt.Tx) error {
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		prefix := []byte(query.SiteID + "\x00")
		index := b.main
		if query.APID != "" {
			prefix = []byte(query.SiteID + "\x00" + query.APID + "\x00")
			index = b.byAP
		}
		start := prefix
		if !query.Before.IsZero() {
			// invTime(Before) sorts at Before; skip it so the page is strictly older.
			start = append(append([]byte(nil), prefix...), invTime(query.Before.Add(-time.Nanosecond))...)
		}
		cursor := index.Cursor()
		for key, raw := cursor.Seek(start); key != nil && bytes.HasPrefix(key, prefix); key, raw = cursor.Next() {
			var event EventV2
			if query.APID != "" {
				rest := key[len(prefix):]
				if len(rest) < 9 {
					continue
				}
				loaded, _, found, err := b.load(string(rest[9:]))
				if err != nil || !found {
					continue
				}
				event = loaded
			} else if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			if !query.match(event) {
				continue
			}
			if len(result) == limit {
				next = result[len(result)-1].LastSeen
				return nil
			}
			result = append(result, event)
		}
		return nil
	})
	return result, next, err
}
