package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

var eventBase = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func paceEvent(id, ap, kind string, at time.Time, data string) EventV2 {
	pace := 1
	event := EventV2{ID: id, SiteID: "site-a", APID: ap, Kind: kind, PaceType: &pace, Severity: SeverityWarning, Title: kind + " " + id, FirstSeen: at, LastSeen: at}
	if data != "" {
		event.Data = json.RawMessage(data)
	}
	return event
}

// checkEventIndexes asserts that events2, events2_ap and events2_id
// describe the same set of events.
func checkEventIndexes(t *testing.T, store *BoltStore) int {
	t.Helper()
	var count int
	if err := store.View(func(tx *bolt.Tx) error {
		b, err := openEventBuckets(tx)
		if err != nil {
			return err
		}
		main, byAP, byID := b.main.Stats().KeyN, b.byAP.Stats().KeyN, b.byID.Stats().KeyN
		if main != byAP || main != byID {
			return fmt.Errorf("index sizes differ: events2 %d events2_ap %d events2_id %d", main, byAP, byID)
		}
		count = main
		return b.main.ForEach(func(key, raw []byte) error {
			var event EventV2
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			if string(b.byID.Get([]byte(event.ID))) != string(key) {
				return fmt.Errorf("events2_id %s does not point at its record", event.ID)
			}
			if b.byAP.Get(event2APKey(event)) == nil {
				return fmt.Errorf("events2_ap misses %s", event.ID)
			}
			if string(key) != string(event2Key(event)) {
				return fmt.Errorf("event %s stored under a stale key", event.ID)
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestWritePACEUpsertHonoursIsUpdate(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	first := paceEvent("dhcp-1", "ap-1", "dhcp_failure", eventBase, `{"attempts":1,"vlan":10}`)
	result, err := events.Write([]EventWrite{{Event: first}})
	if err != nil || result.Events[0].Outcome != EventInserted {
		t.Fatalf("insert %+v %v", result, err)
	}
	// A re-delivery (no IsUpdate) changes nothing.
	result, _ = events.Write([]EventWrite{{Event: first}})
	if result.Events[0].Outcome != EventDuplicate || result.Events[0].Event.Count != 1 || len(result.Changed()) != 0 {
		t.Fatalf("re-delivery %+v", result)
	}
	// An IsUpdate with an identical report is a re-delivery too.
	result, _ = events.Write([]EventWrite{{Event: first, IsUpdate: true}})
	if result.Events[0].Outcome != EventDuplicate {
		t.Fatalf("identical update %+v", result)
	}
	// A real update: count +1, data merged, newer title, moved to the top.
	other := paceEvent("dns-1", "ap-2", "dns", eventBase.Add(time.Minute), "")
	update := paceEvent("dhcp-1", "ap-1", "dhcp_failure", eventBase.Add(2*time.Minute), `{"attempts":3}`)
	update.FirstSeen = eventBase
	update.Title = "DHCP failed 3 times"
	result, err = events.Write([]EventWrite{{Event: other}, {Event: update, IsUpdate: true}})
	if err != nil || result.Events[1].Outcome != EventUpdated {
		t.Fatalf("update %+v %v", result, err)
	}
	got, _ := events.Get("dhcp-1")
	if got.Count != 2 || got.Title != "DHCP failed 3 times" || string(got.Data) != `{"attempts":3,"vlan":10}` || !got.FirstSeen.Equal(eventBase) || !got.LastSeen.Equal(eventBase.Add(2*time.Minute)) {
		t.Fatalf("updated %+v", got)
	}
	list, _, _ := events.List(EventQuery{SiteID: "site-a"})
	if len(list) != 2 || list[0].ID != "dhcp-1" {
		t.Fatalf("updated event not on top: %+v", list)
	}
	// An older update arriving late counts but does not replace newer values.
	late := paceEvent("dhcp-1", "ap-1", "dhcp_failure", eventBase.Add(time.Minute), `{"attempts":2,"server":"192.0.2.1"}`)
	late.Title = "stale title"
	result, _ = events.Write([]EventWrite{{Event: late, IsUpdate: true}})
	got = result.Events[0].Event
	if result.Events[0].Outcome != EventUpdated || got.Count != 3 || got.Title != "DHCP failed 3 times" || string(got.Data) != `{"attempts":3,"server":"192.0.2.1","vlan":10}` || !got.LastSeen.Equal(eventBase.Add(2*time.Minute)) {
		t.Fatalf("late update %+v", got)
	}
	// An update for an ID never seen is stored as new.
	result, _ = events.Write([]EventWrite{{Event: paceEvent("dhcp-9", "ap-1", "dhcp_failure", eventBase, ""), IsUpdate: true}})
	if result.Events[0].Outcome != EventInserted {
		t.Fatalf("first update %+v", result)
	}
	if n := checkEventIndexes(t, store); n != 3 {
		t.Fatalf("stored %d events", n)
	}
}

func TestWriteCorrectsResolvesStoredEvents(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	failA := paceEvent("fail-a", "ap-1", "dns", eventBase, "")
	failB := paceEvent("fail-b", "ap-1", "dns", eventBase.Add(time.Second), "")
	if _, err := events.Write([]EventWrite{{Event: failA}}); err != nil {
		t.Fatal(err)
	}
	ok := paceEvent("ok-1", "ap-1", "dns", eventBase.Add(time.Minute), "")
	ok.Severity = SeverityInfo
	// The correction comes before fail-b in the same batch, and also
	// names an event that is not stored.
	result, err := events.Write([]EventWrite{
		{Event: ok, Corrects: []string{"fail-a", "fail-b", "never-stored"}},
		{Event: failB},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Resolved) != 2 || result.Events[1].Event.ResolvedBy != "ok-1" {
		t.Fatalf("resolved %+v", result)
	}
	if got := result.Events[0].Event; strings.Join(got.Corrects, ",") != "fail-a,fail-b,never-stored" {
		t.Fatalf("corrects not recorded: %+v", got)
	}
	changed := result.Changed()
	if len(changed) != 3 {
		t.Fatalf("changed %+v", changed)
	}
	for _, id := range []string{"fail-a", "fail-b"} {
		if got, _ := events.Get(id); got.ResolvedBy != "ok-1" {
			t.Fatalf("%s not resolved: %+v", id, got)
		}
	}
	// A later correction does not overwrite the first resolution.
	again := paceEvent("ok-2", "ap-1", "dns", eventBase.Add(2*time.Minute), "")
	result, _ = events.Write([]EventWrite{{Event: again, Corrects: []string{"fail-a"}}})
	if len(result.Resolved) != 0 {
		t.Fatalf("resolution overwritten: %+v", result.Resolved)
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", Unresolved: true}); len(list) != 2 {
		t.Fatalf("unresolved %+v", list)
	}
	// An update of a resolved event keeps it resolved.
	update := failA
	update.LastSeen = eventBase.Add(3 * time.Minute)
	update.Data = json.RawMessage(`{"count":2}`)
	result, _ = events.Write([]EventWrite{{Event: update, IsUpdate: true}})
	if result.Events[0].Event.ResolvedBy != "ok-1" {
		t.Fatalf("update un-resolved %+v", result.Events[0].Event)
	}
	checkEventIndexes(t, store)
}

func TestWriteRejectsInvalidEventsOneByOne(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	result, err := events.Write([]EventWrite{
		{Event: EventV2{ID: "x", SiteID: "site-a", FirstSeen: eventBase}},                                  // no kind
		{Event: EventV2{SiteID: "site-a", Kind: "port", FirstSeen: eventBase}},                             // PACE without ID
		{Event: EventV2{ID: "y", SiteID: "site-a", Kind: "port"}},                                          // no time
		{Event: EventV2{ID: "z", SiteID: "site-a", Kind: "port", FirstSeen: eventBase, Severity: "fatal"}}, // stored, severity coerced
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if result.Events[index].Outcome != EventRejected || result.Events[index].Err == nil {
			t.Fatalf("event %d %+v", index, result.Events[index])
		}
	}
	if result.Events[3].Outcome != EventInserted || result.Events[3].Event.Severity != SeverityInfo {
		t.Fatalf("valid event %+v", result.Events[3])
	}
}

func TestControllerEventWithKnownIDUpdatesInPlace(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	first, err := events.AppendController(EventV2{ID: "ctl-1", SiteID: "site-a", APID: "ap-1", Kind: "config", Title: "Delivered", FirstSeen: eventBase})
	if err != nil || first.ID != "ctl-1" {
		t.Fatal(first, err)
	}
	second, err := events.AppendController(EventV2{ID: "ctl-1", SiteID: "site-a", APID: "ap-1", Kind: "config", Title: "Applied", FirstSeen: eventBase.Add(10 * time.Minute)})
	if err != nil || second.Count != 2 || second.Title != "Applied" {
		t.Fatalf("second %+v %v", second, err)
	}
	if n := checkEventIndexes(t, store); n != 1 {
		t.Fatalf("stored %d events, want 1 (no orphan)", n)
	}
	// A resolved controller event is not folded into.
	_ = events.Resolve([]string{"ctl-1"}, "operator")
	third, _ := events.AppendController(EventV2{SiteID: "site-a", APID: "ap-1", Kind: "config", Title: "Applied", FirstSeen: eventBase.Add(10*time.Minute + time.Second)})
	if third.ID == "ctl-1" {
		t.Fatal("folded into a resolved event")
	}
}

func TestEventListFiltersAndTieSafePaging(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	var batch []EventWrite
	// Three events share one time, so a page of two must not split them.
	for index, at := range []time.Duration{0, time.Second, time.Second, time.Second, 2 * time.Second, 3 * time.Second} {
		event := paceEvent(fmt.Sprintf("e%d", index), fmt.Sprintf("ap-%d", index%2), "port", eventBase.Add(at), "")
		batch = append(batch, EventWrite{Event: event})
	}
	gtk := paceEvent("g1", "ap-0", "gtk_rekey", eventBase.Add(4*time.Second), "")
	client := paceEvent("c1", "ap-1", "authorized", eventBase.Add(5*time.Second), "")
	client.ClientMAC = "02-AA-00-00-00-77"
	batch = append(batch, EventWrite{Event: gtk}, EventWrite{Event: client})
	if _, err := events.Write(batch); err != nil {
		t.Fatal(err)
	}
	var seen []string
	before := time.Time{}
	for page := 0; page < 10; page++ {
		list, next, err := events.List(EventQuery{SiteID: "site-a", Kind: "port", Before: before, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range list {
			seen = append(seen, event.ID)
		}
		if next.IsZero() {
			break
		}
		before = next
	}
	if len(seen) != 6 || seen[0] != "e5" || seen[1] != "e4" || seen[5] != "e0" {
		t.Fatalf("pages %v", seen)
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", ExcludeKinds: []string{"gtk_rekey"}}); len(list) != 7 {
		t.Fatalf("exclude %d", len(list))
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", Kind: "gtk_rekey", ExcludeKinds: []string{"gtk_rekey"}}); len(list) != 1 {
		t.Fatalf("explicit kind %d", len(list))
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", Client: "02:aa:00:00:00:77"}); len(list) != 1 || list[0].ClientMAC != "02:aa:00:00:00:77" {
		t.Fatalf("client %+v", list)
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", Since: eventBase.Add(3 * time.Second)}); len(list) != 3 {
		t.Fatalf("since %d", len(list))
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", APID: "ap-1", Since: eventBase.Add(time.Second)}); len(list) != 4 {
		t.Fatalf("AP since %+v", list)
	}
	// Site-level lists merge every AP by time.
	list, _, _ := events.List(EventQuery{SiteID: "site-a", Limit: 3})
	if len(list) != 3 || list[0].ID != "c1" || list[1].ID != "g1" || list[2].ID != "e5" {
		t.Fatalf("site merge %+v", list)
	}
}

func TestEventWriterBatchesAndBoundsItsQueue(t *testing.T) {
	store, _ := openTestStore(t)
	var mu sync.Mutex
	var written []EventV2
	writer := NewEventWriter(store.Events2(), EventWriterOptions{
		FlushEvery: time.Hour, MaxBatch: 10, MaxQueue: 25,
		OnWrite: func(result WriteResult) {
			mu.Lock()
			written = append(written, result.Changed()...)
			mu.Unlock()
		},
	})
	if result, err := writer.Flush(); err != nil || len(result.Events) != 0 {
		t.Fatal("empty flush", result, err)
	}
	for index := 0; index < 30; index++ {
		writer.Add(EventWrite{Event: paceEvent(fmt.Sprintf("q%02d", index), "ap-1", "port", eventBase.Add(time.Duration(index)*time.Second), "")})
	}
	stats := writer.Stats()
	if stats.Queued != 25 || stats.Dropped != 5 {
		t.Fatalf("queue %+v", stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { writer.Run(ctx); close(done) }()
	// The queue is above MaxBatch, so Run flushes without waiting an hour.
	deadline := time.Now().Add(5 * time.Second)
	for writer.Stats().Flushes == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	writer.Add(EventWrite{Event: paceEvent("last", "ap-1", "port", eventBase.Add(time.Hour), "")}, EventWrite{Event: EventV2{ID: "bad"}})
	cancel()
	<-done
	stats = writer.Stats()
	if stats.Queued != 0 || stats.Written != 27 || stats.Rejected != 1 || stats.Flushes < 2 {
		t.Fatalf("stats %+v", stats)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(written) != 26 {
		t.Fatalf("OnWrite saw %d events", len(written))
	}
	if _, err := store.Events2().Get("q04"); !errors.Is(err, ErrNotFound) {
		t.Fatal("dropped event stored", err)
	}
	if _, err := store.Events2().Get("last"); err != nil {
		t.Fatal("final flush missed", err)
	}
}
