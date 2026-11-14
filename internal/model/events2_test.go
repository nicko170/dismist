package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEvents2UpsertDedupAndNewestFirst(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	pace := 1
	stored, err := events.Upsert([]EventV2{
		{ID: "pace-1", SiteID: "site-a", APID: "ap-1", Kind: "dhcp_failure", PaceType: &pace, Severity: SeverityWarning, Title: "DHCP failed", FirstSeen: base, Data: json.RawMessage(`{"vlan":1}`)},
		{ID: "pace-2", SiteID: "site-a", APID: "ap-2", Kind: "port", Title: "eth0 up", FirstSeen: base.Add(time.Minute)},
		{ID: "pace-3", SiteID: "site-b", APID: "ap-3", Kind: "port", Title: "other site", FirstSeen: base.Add(2 * time.Minute)},
	})
	if err != nil || len(stored) != 3 || stored[0].Count != 1 || stored[1].Severity != SeverityInfo {
		t.Fatalf("stored %+v %v", stored, err)
	}
	// The same PACE event again: count and last_seen move, data merges, and
	// it becomes the newest.
	if _, err := events.Upsert([]EventV2{{ID: "pace-1", SiteID: "site-a", APID: "ap-1", Kind: "dhcp_failure", Severity: SeverityWarning, Title: "DHCP failed", FirstSeen: base.Add(5 * time.Minute), Data: json.RawMessage(`{"attempts":3}`)}}); err != nil {
		t.Fatal(err)
	}
	got, err := events.Get("pace-1")
	if err != nil || got.Count != 2 || !got.FirstSeen.Equal(base) || !got.LastSeen.Equal(base.Add(5*time.Minute)) || string(got.Data) != `{"attempts":3,"vlan":1}` {
		t.Fatalf("upserted %+v %v", got, err)
	}
	list, next, err := events.List(EventQuery{SiteID: "site-a"})
	if err != nil || len(list) != 2 || list[0].ID != "pace-1" || list[1].ID != "pace-2" || !next.IsZero() {
		t.Fatalf("site list %+v %v", list, err)
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", APID: "ap-2"}); len(list) != 1 || list[0].ID != "pace-2" {
		t.Fatalf("AP list %+v", list)
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a", Severity: SeverityWarning}); len(list) != 1 {
		t.Fatalf("severity filter %+v", list)
	}
	if err := events.Resolve([]string{"pace-2"}, "pace-9"); err != nil {
		t.Fatal(err)
	}
	if got, _ := events.Get("pace-2"); got.ResolvedBy != "pace-9" {
		t.Fatal("resolve")
	}
	if _, err := events.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := events.Upsert([]EventV2{{ID: "x", SiteID: "site-a", Kind: "pace", FirstSeen: base, Data: json.RawMessage(`"` + strings.Repeat("a", MaxEventData) + `"`)}}); err == nil {
		t.Fatal("oversized data accepted")
	}
}

func TestEvents2Pagination(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var batch []EventV2
	for i := 0; i < 7; i++ {
		batch = append(batch, EventV2{ID: fmt.Sprintf("e%d", i), SiteID: "site-a", APID: "ap-1", Kind: "port", Title: "t", FirstSeen: base.Add(time.Duration(i) * time.Second)})
	}
	if _, err := events.Upsert(batch); err != nil {
		t.Fatal(err)
	}
	var seen []string
	before := time.Time{}
	for page := 0; page < 5; page++ {
		list, next, err := events.List(EventQuery{SiteID: "site-a", APID: "ap-1", Before: before, Limit: 3})
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
	if strings.Join(seen, ",") != "e6,e5,e4,e3,e2,e1,e0" {
		t.Fatalf("pages %v", seen)
	}
}

func TestControllerEventsFoldWithinTheWindow(t *testing.T) {
	store, _ := openTestStore(t)
	events := store.Events2()
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	first, err := events.AppendController(EventV2{SiteID: "site-a", APID: "ap-1", Kind: "connection", Title: "AP reconnected", FirstSeen: base})
	if err != nil || len(first.ID) != 36 || first.ID[14] != '7' {
		t.Fatalf("first %+v %v", first, err)
	}
	second, _ := events.AppendController(EventV2{SiteID: "site-a", APID: "ap-1", Kind: "connection", Title: "AP reconnected", FirstSeen: base.Add(30 * time.Second)})
	if second.ID != first.ID || second.Count != 2 {
		t.Fatalf("not folded: %+v", second)
	}
	third, _ := events.AppendController(EventV2{SiteID: "site-a", APID: "ap-1", Kind: "connection", Title: "AP reconnected", FirstSeen: base.Add(5 * time.Minute)})
	if third.ID == first.ID {
		t.Fatal("folded outside the window")
	}
	other, _ := events.AppendController(EventV2{SiteID: "site-a", APID: "ap-1", Kind: "reboot", Title: "AP rebooted", FirstSeen: base.Add(5 * time.Minute)})
	if other.ID == third.ID {
		t.Fatal("different kind folded")
	}
	if list, _, _ := events.List(EventQuery{SiteID: "site-a"}); len(list) != 3 {
		t.Fatalf("list %+v", list)
	}
}

func TestSessionsTIndexListsNewestFirst(t *testing.T) {
	store, path := openTestStore(t)
	seedSite(t, store)
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	observe := func(when time.Time, apID string, clients ...ClientSnapshot) {
		if err := store.RecordObservation(Observation{SiteID: "site-a", APID: apID, At: when, Connected: true, Clients: clients,
			Radios: []RadioSample{{Band: "5", Channel: 149, Width: 80, Clients: len(clients)}}}); err != nil {
			t.Fatal(err)
		}
	}
	// MAC order (aa < bb < cc) is the reverse of start order.
	observe(at, "02-aa-00-00-00-01", ClientSnapshot{MAC: "02:aa:00:00:00:cc", ConnectedSeconds: 0})
	observe(at.Add(time.Minute), "02-aa-00-00-00-01", ClientSnapshot{MAC: "02:aa:00:00:00:cc", ConnectedSeconds: 60}, ClientSnapshot{MAC: "02:aa:00:00:00:bb"})
	observe(at.Add(2*time.Minute), "02-aa-00-00-00-02", ClientSnapshot{MAC: "02:aa:00:00:00:aa"})
	sessions, next, err := store.RecentSessions(SessionQuery{SiteID: "site-a"})
	if err != nil || len(sessions) != 3 || !next.IsZero() {
		t.Fatalf("sessions %+v %v", sessions, err)
	}
	if sessions[0].MAC != "02:aa:00:00:00:aa" || sessions[1].MAC != "02:aa:00:00:00:bb" || sessions[2].MAC != "02:aa:00:00:00:cc" {
		t.Fatalf("order %v %v %v", sessions[0].MAC, sessions[1].MAC, sessions[2].MAC)
	}
	if only, _, _ := store.RecentSessions(SessionQuery{SiteID: "site-a", MAC: "02:aa:00:00:00:cc"}); len(only) != 1 {
		t.Fatalf("MAC filter %+v", only)
	}
	if page, next, _ := store.RecentSessions(SessionQuery{SiteID: "site-a", Limit: 1}); len(page) != 1 || next.IsZero() {
		t.Fatal("limit")
	}
	samples, err := store.ListSamples("site-a", "02-aa-00-00-00-01", 10)
	if err != nil || len(samples) == 0 || len(samples[0].Radios) != 1 || samples[0].Radios[0].Channel != 149 {
		t.Fatalf("samples with radios %+v %v", samples, err)
	}
	// Sessions written before sessions_t existed are indexed once on open.
	if err := store.db.Update(func(tx *boltTx) error { return clearBucket(tx, BucketSessionsT) }); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	reopened, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if again, _, _ := reopened.RecentSessions(SessionQuery{SiteID: "site-a"}); len(again) != 3 || again[0].MAC != "02:aa:00:00:00:aa" {
		t.Fatalf("backfill %+v", again)
	}
}
