package model

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Event severities.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// MaxEventData caps EventV2.Data (design §5.8). Larger data is replaced by
// {"truncated": <original size>} so the event itself is never lost.
const MaxEventData = 4096

// MaxEventCorrects caps how many IDs one event may correct.
const MaxEventCorrects = 64

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
	// Corrects lists the event IDs this event resolved (PACE Corrects).
	Corrects []string `json:"corrects,omitempty"`
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

// replace stores event in place of previous (stored under key).
func (b eventBuckets) replace(previous EventV2, key []byte, event EventV2) error {
	if err := b.remove(previous, key); err != nil {
		return err
	}
	return b.store(event)
}

func normaliseEvent(event *EventV2) error {
	if event.SiteID == "" || event.ID == "" || event.Kind == "" {
		return errors.New("event needs site, ID and kind")
	}
	if strings.ContainsRune(event.SiteID, 0) || strings.ContainsRune(event.APID, 0) || strings.ContainsRune(event.ID, 0) {
		return errors.New("event site, AP and ID must not contain NUL")
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
	if event.LastSeen.Before(event.FirstSeen) {
		event.FirstSeen, event.LastSeen = event.LastSeen, event.FirstSeen
	}
	event.FirstSeen, event.LastSeen = event.FirstSeen.UTC(), event.LastSeen.UTC()
	if event.Count < 1 {
		event.Count = 1
	}
	switch event.Severity {
	case SeverityInfo, SeverityWarning, SeverityError:
	default:
		event.Severity = SeverityInfo
	}
	event.ClientMAC = NormaliseMAC(event.ClientMAC)
	if len(event.Data) != 0 {
		if !json.Valid(event.Data) {
			return errors.New("event data is not valid JSON")
		}
		if len(event.Data) > MaxEventData {
			event.Data = json.RawMessage(fmt.Sprintf(`{"truncated":%d}`, len(event.Data)))
		}
	}
	if len(event.Corrects) > MaxEventCorrects {
		event.Corrects = event.Corrects[:MaxEventCorrects]
	}
	return nil
}

// NormaliseMAC lower-cases a MAC address and uses ":" separators, the form
// events and sessions store.
func NormaliseMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
}

// mergeData merges two JSON objects key by key; the keys of winner
// replace those of base. Non-objects: winner replaces base.
func mergeData(base, winner json.RawMessage) json.RawMessage {
	if len(winner) == 0 {
		return base
	}
	if len(base) == 0 {
		return winner
	}
	var a, b map[string]json.RawMessage
	if json.Unmarshal(base, &a) != nil || json.Unmarshal(winner, &b) != nil || a == nil || b == nil {
		return winner
	}
	for key, value := range b {
		a[key] = value
	}
	merged, err := json.Marshal(a)
	if err != nil || len(merged) > MaxEventData {
		return winner
	}
	return merged
}

// foldEvent folds next into previous: counts add up, the time span widens,
// data merges, and the newer report (by last_seen) supplies title, detail,
// severity and the winning data keys. A resolution is never undone.
func foldEvent(previous, next EventV2) EventV2 {
	result := previous
	result.Count += next.Count
	newer := !next.LastSeen.Before(previous.LastSeen)
	if next.LastSeen.After(result.LastSeen) {
		result.LastSeen = next.LastSeen
	}
	if next.FirstSeen.Before(result.FirstSeen) {
		result.FirstSeen = next.FirstSeen
	}
	if newer {
		result.Title, result.Detail, result.Severity = next.Title, next.Detail, next.Severity
		result.Data = mergeData(previous.Data, next.Data)
		if next.ClientMAC != "" {
			result.ClientMAC = next.ClientMAC
		}
		if next.WLANID != "" {
			result.WLANID = next.WLANID
		}
	} else {
		result.Data = mergeData(next.Data, previous.Data)
	}
	if result.ResolvedBy == "" {
		result.ResolvedBy = next.ResolvedBy
	}
	result.Corrects = mergeIDs(previous.Corrects, next.Corrects)
	return result
}

// sameContent reports whether folding next into previous would change
// nothing but the count: a re-delivery.
func sameContent(previous, next EventV2) bool {
	if next.LastSeen.After(previous.LastSeen) || next.FirstSeen.Before(previous.FirstSeen) {
		return false
	}
	if next.Title != previous.Title || next.Detail != previous.Detail || next.Severity != previous.Severity {
		return false
	}
	if len(next.Data) != 0 && !bytes.Equal(mergeData(previous.Data, next.Data), mergeData(previous.Data, nil)) {
		return false
	}
	for _, id := range next.Corrects {
		if !containsID(previous.Corrects, id) {
			return false
		}
	}
	return true
}

func containsID(ids []string, id string) bool {
	for _, have := range ids {
		if have == id {
			return true
		}
	}
	return false
}

func mergeIDs(a, b []string) []string {
	result := append([]string(nil), a...)
	for _, id := range b {
		if id != "" && !containsID(result, id) && len(result) < MaxEventCorrects {
			result = append(result, id)
		}
	}
	return result
}

// EventWrite is one event for EventStore.Write.
type EventWrite struct {
	Event EventV2
	// Controller selects the controller rules (design §5.8): a UUIDv7 is
	// assigned when Event.ID is empty, and an identical (AP, kind, title)
	// event whose last_seen is within ControllerDedupWindow is folded into
	// the earlier record. Otherwise the event is a PACE event: Event.ID is
	// the AP's event ID and is required.
	Controller bool
	// IsUpdate is the PACE IsUpdate flag. A stored ID with IsUpdate is
	// upserted (count +1, last_seen and data from the newer report). A
	// stored ID without IsUpdate is a re-delivery and changes nothing.
	IsUpdate bool
	// Corrects is the PACE Corrects list: once stored, this event sets
	// resolved_by on each listed event that is stored and not yet
	// resolved. IDs that are not stored are ignored.
	Corrects []string
}

// EventOutcome says what Write did with one event.
type EventOutcome string

const (
	EventInserted  EventOutcome = "inserted"  // a new record
	EventUpdated   EventOutcome = "updated"   // an upsert of a stored ID
	EventFolded    EventOutcome = "folded"    // a controller event folded into a recent identical one
	EventDuplicate EventOutcome = "duplicate" // a re-delivery: nothing changed
	EventRejected  EventOutcome = "rejected"  // invalid; Err says why; nothing stored
)

// EventResult is the stored record for one EventWrite.
type EventResult struct {
	Event   EventV2
	Outcome EventOutcome
	Err     error
}

// WriteResult is the outcome of one Write.
type WriteResult struct {
	// Events is one result per input, in input order.
	Events []EventResult
	// Resolved are stored events whose resolved_by this write set.
	Resolved []EventV2
}

// Changed returns every record this write created or changed (inserted,
// updated, folded, and resolved), newest record per ID, for publishing
// SSE event summaries.
func (r WriteResult) Changed() []EventV2 {
	var result []EventV2
	index := map[string]int{}
	add := func(event EventV2) {
		if at, ok := index[event.ID]; ok {
			result[at] = event
			return
		}
		index[event.ID] = len(result)
		result = append(result, event)
	}
	for _, item := range r.Events {
		switch item.Outcome {
		case EventInserted, EventUpdated, EventFolded:
			add(item.Event)
		}
	}
	for _, event := range r.Resolved {
		add(event)
	}
	return result
}

// Write stores a batch of events in one transaction (the observer's
// batch, design §5.8). Invalid events are rejected one by one and the
// others are still stored; a storage error fails the whole batch.
// Corrections are applied after every event of the batch is stored, so a
// correction may come before the event it corrects within one batch.
func (e *EventStore) Write(batch []EventWrite) (WriteResult, error) {
	result := WriteResult{Events: make([]EventResult, len(batch))}
	err := e.db.Update(func(tx *bolt.Tx) error {
		result = WriteResult{Events: make([]EventResult, len(batch))}
		corrects := make([][]string, len(batch))
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		for index, write := range batch {
			event := write.Event
			event.Corrects = mergeIDs(event.Corrects, write.Corrects)
			if write.Controller && event.ID == "" {
				at := event.LastSeen
				if at.IsZero() {
					at = event.FirstSeen
				}
				event.ID = NewEventID(at)
			}
			if err := normaliseEvent(&event); err != nil {
				result.Events[index] = EventResult{Event: event, Outcome: EventRejected, Err: err}
				continue
			}
			corrects[index] = event.Corrects
			var stored EventV2
			var outcome EventOutcome
			if write.Controller {
				stored, outcome, err = b.putController(event)
			} else {
				stored, outcome, err = b.putPACE(event, write.IsUpdate)
			}
			if err != nil {
				return err
			}
			result.Events[index] = EventResult{Event: stored, Outcome: outcome}
		}
		for index := range batch {
			item := &result.Events[index]
			if item.Outcome == EventRejected {
				continue
			}
			for _, id := range corrects[index] {
				resolved, changed, err := b.resolve(id, item.Event.ID)
				if err != nil {
					return err
				}
				if changed {
					result.Resolved = append(result.Resolved, resolved)
					for other := range result.Events {
						if result.Events[other].Event.ID == id && result.Events[other].Outcome != EventRejected {
							result.Events[other].Event = resolved
						}
					}
				}
			}
		}
		return nil
	})
	return result, err
}

func (b eventBuckets) putPACE(event EventV2, isUpdate bool) (EventV2, EventOutcome, error) {
	previous, key, found, err := b.load(event.ID)
	if err != nil {
		return event, "", err
	}
	if !found {
		return event, EventInserted, b.store(event)
	}
	if !isUpdate || sameContent(previous, event) {
		return previous, EventDuplicate, nil
	}
	folded := foldEvent(previous, event)
	return folded, EventUpdated, b.replace(previous, key, folded)
}

func (b eventBuckets) putController(event EventV2) (EventV2, EventOutcome, error) {
	if previous, key, found, err := b.load(event.ID); err != nil {
		return event, "", err
	} else if found {
		folded := foldEvent(previous, event)
		return folded, EventUpdated, b.replace(previous, key, folded)
	}
	prefix := []byte(event.SiteID + "\x00" + event.APID + "\x00")
	s := openStream(b.byAP, prefix, nil, false, inverted(len(prefix)))
	for ; s.valid(); s.advance() {
		if event.LastSeen.Sub(s.when) > ControllerDedupWindow {
			break
		}
		rest := s.key[len(prefix)+8:]
		if len(rest) < 2 {
			continue
		}
		previous, key, found, err := b.load(string(rest[1:]))
		if err != nil || !found {
			continue
		}
		if previous.Kind == event.Kind && previous.Title == event.Title && previous.PaceType == nil && previous.ResolvedBy == "" {
			folded := foldEvent(previous, event)
			return folded, EventFolded, b.replace(previous, key, folded)
		}
	}
	return event, EventInserted, b.store(event)
}

// resolve sets resolved_by on a stored, unresolved event.
func (b eventBuckets) resolve(id, by string) (EventV2, bool, error) {
	if id == "" || id == by {
		return EventV2{}, false, nil
	}
	event, key, found, err := b.load(id)
	if err != nil || !found || event.ResolvedBy != "" {
		return event, false, err
	}
	event.ResolvedBy = by
	raw, err := json.Marshal(event)
	if err != nil {
		return event, false, err
	}
	return event, true, b.main.Put(key, raw)
}

// Upsert stores PACE events in one transaction, treating every repeated ID
// as an update (count +1). It is Write with IsUpdate set on every event,
// and fails on the first invalid event; new code should use Write.
func (e *EventStore) Upsert(events []EventV2) ([]EventV2, error) {
	batch := make([]EventWrite, len(events))
	for index, event := range events {
		batch[index] = EventWrite{Event: event, IsUpdate: true}
	}
	result, err := e.Write(batch)
	if err != nil {
		return nil, err
	}
	stored := make([]EventV2, 0, len(result.Events))
	for _, item := range result.Events {
		if item.Outcome == EventRejected {
			return nil, item.Err
		}
		stored = append(stored, item.Event)
	}
	return stored, nil
}

// AppendController stores one controller event (Write with Controller).
func (e *EventStore) AppendController(event EventV2) (EventV2, error) {
	result, err := e.Write([]EventWrite{{Event: event, Controller: true}})
	if err != nil {
		return EventV2{}, err
	}
	if item := result.Events[0]; item.Outcome == EventRejected {
		return item.Event, item.Err
	}
	return result.Events[0].Event, nil
}

// Resolve sets resolved_by on stored events, overwriting an earlier
// resolution (an explicit operator or controller action).
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

// Get returns one event by ID; ErrNotFound when it is not stored.
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

// EventQuery selects events newest first (by last_seen).
type EventQuery struct {
	SiteID string // required
	APID   string // optional: uses the per-AP index
	// Before returns events whose last_seen is strictly before it (the
	// next_before of the previous page).
	Before time.Time
	// Since returns events whose last_seen is at or after it.
	Since time.Time
	// Limit is the page size: default 50, max 500. A page can hold more
	// (up to twice the limit) when events at its last time share it, so
	// that paging by Before never skips an event.
	Limit    int
	Kind     string
	Severity string
	// Client matches client_mac (any case, ":" or "-").
	Client string
	// ExcludeKinds drops these kinds (for example "gtk_rekey", hidden by
	// default, design §5.8). Ignored for a kind named in Kind.
	ExcludeKinds []string
	// Unresolved drops events with resolved_by set.
	Unresolved bool
}

func (q EventQuery) match(event EventV2, client string) bool {
	if q.Kind != "" && event.Kind != q.Kind {
		return false
	}
	if q.Kind == "" {
		for _, kind := range q.ExcludeKinds {
			if event.Kind == kind {
				return false
			}
		}
	}
	return (q.Severity == "" || event.Severity == q.Severity) && (client == "" || event.ClientMAC == client) &&
		(q.APID == "" || event.APID == q.APID) && (!q.Unresolved || event.ResolvedBy == "")
}

// needles are byte strings a stored record must contain to match: a cheap
// check before decoding JSON.
func (q EventQuery) needles(client string) [][]byte {
	var result [][]byte
	add := func(field, value string) {
		if value == "" {
			return
		}
		encoded, _ := json.Marshal(value)
		result = append(result, append([]byte(`"`+field+`":`), encoded...))
	}
	add("kind", q.Kind)
	add("severity", q.Severity)
	add("client_mac", client)
	return result
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

// pager collects one page newest first. Once the page is full it still
// takes items at the page's last time (up to twice the limit), so that a
// strict Before on that time skips nothing; next is that time when another
// matching item exists.
type pager[T any] struct {
	limit    int
	items    []T
	at       func(T) time.Time
	next     time.Time
	full     bool
	boundary time.Time
}

// add offers the next matching item; false means stop scanning.
func (p *pager[T]) add(item T) bool {
	if p.full {
		if p.at(item).Equal(p.boundary) && len(p.items) < 2*p.limit {
			p.items = append(p.items, item)
			return true
		}
		p.next = p.boundary
		return false
	}
	p.items = append(p.items, item)
	if len(p.items) == p.limit {
		p.full, p.boundary = true, p.at(item)
	}
	return true
}

// List returns events newest first and the last_seen to pass as Before for
// the next page (zero at the end).
func (e *EventStore) List(query EventQuery) ([]EventV2, time.Time, error) {
	if query.SiteID == "" {
		return nil, time.Time{}, errors.New("site ID required")
	}
	client := NormaliseMAC(query.Client)
	needles := query.needles(client)
	page := pager[EventV2]{limit: clampLimit(query.Limit), items: make([]EventV2, 0), at: func(e EventV2) time.Time { return e.LastSeen }}
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
		var start []byte
		if !query.Before.IsZero() {
			// invTime(Before) sorts at Before; start just after it so the
			// page is strictly older.
			start = append(append([]byte(nil), prefix...), invTime(query.Before.Add(-time.Nanosecond))...)
		}
		s := openStream(index, prefix, start, false, inverted(len(prefix)))
		for ; s.valid(); s.advance() {
			if !query.Since.IsZero() && s.when.Before(query.Since) {
				break
			}
			raw := s.value
			if query.APID != "" {
				rest := s.key[len(prefix)+8:]
				if len(rest) < 2 {
					continue
				}
				mainKey := b.byID.Get(rest[1:])
				if mainKey == nil {
					continue
				}
				if raw = b.main.Get(mainKey); raw == nil {
					continue
				}
			}
			if !containsAll(raw, needles) {
				continue
			}
			var event EventV2
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			if !query.match(event, client) {
				continue
			}
			if !page.add(event) {
				break
			}
		}
		return nil
	})
	return page.items, page.next, err
}

func containsAll(raw []byte, needles [][]byte) bool {
	for _, needle := range needles {
		if !bytes.Contains(raw, needle) {
			return false
		}
	}
	return true
}
