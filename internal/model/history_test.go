package model

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBoltTracksSiteScopedSamplesClientSessionsAndEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "Lab A"}, {ID: "site-b", Name: "Lab B"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "a.bin"}, {ID: "ap-b", SiteID: "site-b", SecretFile: "b.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 12, 9, 0, 5, 0, time.UTC)
	first := Observation{
		SiteID: "site-a", APID: "ap-a", At: start, Connected: true,
		Firmware: "fw-1", AppliedRevision: 4, UptimeSeconds: 100,
		Clients: []ClientSnapshot{{MAC: "02:aa:00:00:00:02", Name: "Host", IP: "203.0.113.100", SSID: "TestSSID", VLANID: 123, RSSI: -62, TxBytes: 100, RxBytes: 200}},
		Events:  []EventSnapshot{{When: start, Kind: "config", Title: "Configuration applied"}},
	}
	if err := store.RecordObservation(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.At = start.Add(20 * time.Second)
	second.Clients = []ClientSnapshot{{MAC: "02:aa:00:00:00:02", Name: "Host", IP: "203.0.113.100", SSID: "TestSSID", VLANID: 123, RSSI: -58, TxBytes: 150, RxBytes: 350}}
	if err := store.RecordObservation(second); err != nil {
		t.Fatal(err)
	}
	samples, err := store.ListSamples("site-a", "ap-a", 20)
	if err != nil || len(samples) != 1 || samples[0].ClientCount != 1 || samples[0].RxBytes != 350 {
		t.Fatalf("minute aggregation failed: %+v %v", samples, err)
	}
	events, err := store.ListEvents("site-a", "ap-a", 20)
	if err != nil || len(events) != 1 {
		t.Fatalf("event deduplication failed: %+v %v", events, err)
	}
	third := Observation{SiteID: "site-a", APID: "ap-a", At: start.Add(70 * time.Second), Connected: true}
	if err := store.RecordObservation(third); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.ListClientSessions("site-a", "ap-a", 20)
	if err != nil || len(sessions) != 1 || sessions[0].DisconnectedAt.IsZero() || sessions[0].RxBytes != 350 {
		t.Fatalf("client session not closed: %+v %v", sessions, err)
	}
	samples, _ = store.ListSamples("site-a", "ap-a", 20)
	if len(samples) != 2 {
		t.Fatalf("history lost a minute: %+v", samples)
	}
	other, _ := store.ListSamples("site-b", "ap-b", 20)
	if len(other) != 0 {
		t.Fatal("site B received site A samples")
	}
	if err := store.RecordObservation(Observation{SiteID: "site-b", APID: "ap-a", At: third.At}); err == nil {
		t.Fatal("accepted AP observation under short site")
	}
}

func TestControllerReconnectDoesNotSplitKnownClientSession(t *testing.T) {
	store, err := OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "Lab A"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "a.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	first := Observation{SiteID: "site-a", APID: "ap-a", At: start, Connected: true,
		Clients: []ClientSnapshot{{MAC: "02:aa:00:00:00:02", SSID: "TestSSID"}}}
	if err := store.RecordObservation(first); err != nil {
		t.Fatal(err)
	}
	unknown := Observation{SiteID: "site-a", APID: "ap-a", At: start.Add(10 * time.Second), SkipClients: true}
	if err := store.RecordObservation(unknown); err != nil {
		t.Fatal(err)
	}
	sessions, _ := store.ListClientSessions("site-a", "ap-a", 10)
	if len(sessions) != 1 || !sessions[0].DisconnectedAt.IsZero() {
		t.Fatalf("brief controller outage closed client session: %+v", sessions)
	}
}

func TestMovingAPClosesOldSiteClientSession(t *testing.T) {
	store, err := OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "a.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	if err := store.RecordObservation(Observation{SiteID: "site-a", APID: "ap-a", At: when, Connected: true,
		Clients: []ClientSnapshot{{MAC: "02:aa:00:00:00:02", SSID: "TestSSID"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.APs[0].SiteID = "site-b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.ListClientSessions("site-a", "ap-a", 10)
	if err != nil || len(sessions) != 1 || sessions[0].DisconnectedAt.IsZero() || sessions[0].EndReason != "site moved" {
		t.Fatalf("old site session was not closed: %+v %v", sessions, err)
	}
}
