package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

type fakeEventStore struct {
	mu       sync.Mutex
	upserts  []model.EventV2
	appended []model.EventV2
	resolved map[string][]string
}

func (f *fakeEventStore) Upsert(events []model.EventV2) ([]model.EventV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, events...)
	return events, nil
}

func (f *fakeEventStore) AppendController(event model.EventV2) (model.EventV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	event.ID = "controller-" + event.Title
	f.appended = append(f.appended, event)
	return event, nil
}

func (f *fakeEventStore) Resolve(ids []string, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolved == nil {
		f.resolved = map[string][]string{}
	}
	f.resolved[by] = append(f.resolved[by], ids...)
	return nil
}

func TestEventSinkUpsertsAppendsAndResolves(t *testing.T) {
	store := &fakeEventStore{}
	var written []model.EventV2
	var mu sync.Mutex
	sink := newEventSink(store, func(apID string) string {
		if apID == "ap-a" {
			return "site-a"
		}
		return ""
	}, func(events []model.EventV2) { mu.Lock(); written = append(written, events...); mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sink.Run(ctx); close(done) }()
	pace := 1
	at := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	sink.Event(controller.Event{APID: "ap-a", ID: "0e4a2b90-1d2c-11ef-8000-0242ac120002", Kind: "dhcp_failure", PaceType: &pace,
		Severity: "warning", Title: "DHCP failure", ClientMAC: "02:AA:00:00:00:02", At: at})
	sink.Event(controller.Event{APID: "ap-a", Kind: "inactive_vlan", Severity: "info", Title: "VLAN 20 is active again",
		Corrects: []string{"raise-id"}, At: at})
	sink.Event(controller.Event{APID: "ap-unassigned", Kind: "connection", Title: "AP authenticated", At: at})
	cancel()
	<-done
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upserts) != 1 || store.upserts[0].SiteID != "site-a" || store.upserts[0].ClientMAC != "02:aa:00:00:00:02" ||
		store.upserts[0].Count != 1 || !store.upserts[0].FirstSeen.Equal(at) || *store.upserts[0].PaceType != 1 {
		t.Fatalf("upserts %+v", store.upserts)
	}
	if len(store.appended) != 1 || store.appended[0].Kind != "inactive_vlan" {
		t.Fatalf("controller events %+v", store.appended)
	}
	if ids := store.resolved["controller-VLAN 20 is active again"]; len(ids) != 1 || ids[0] != "raise-id" {
		t.Fatalf("resolutions %+v", store.resolved)
	}
	mu.Lock()
	if len(written) != 2 {
		t.Fatalf("written %+v", written)
	}
	mu.Unlock()
}

func TestEventSinkDropsWhenFull(t *testing.T) {
	sink := newEventSink(&fakeEventStore{}, func(string) string { return "site" }, nil)
	for range eventQueueSize + 5 {
		sink.Event(controller.Event{APID: "ap", Title: "x"})
	}
	if sink.Dropped() != 5 {
		t.Fatalf("dropped %d", sink.Dropped())
	}
}

func TestConfigPushFeatures(t *testing.T) {
	notify, err := configPushFeatures(0, "notify")
	if err != nil || !notify.Has(controller.FeatureConfigChangeNotify) {
		t.Fatalf("notify %v %v", notify, err)
	}
	reconnect, err := configPushFeatures(0, "")
	if err != nil || reconnect != 0 {
		t.Fatalf("default %v %v", reconnect, err)
	}
	if _, err := configPushFeatures(0, "push"); err == nil {
		t.Fatal("bad mode accepted")
	}
}

func TestRegistryPassesProtocolHooks(t *testing.T) {
	var events []controller.Event
	state := model.State{
		Sites: []model.Site{{ID: "site-a"}},
		APs: []model.APRecord{{ID: "ap-a", SiteID: "site-a", Secret: make([]byte, 32),
			Features: map[string]bool{"config_change_notify": true}, LocalFiles: []model.LocalFile{}}},
	}
	locate := time.Now().Add(time.Hour)
	manager, _, err := prepareAPs(state, registryOptions{
		Base: []byte(`{"Timestamp":1}`), CertDER: []byte{1},
		Events:      func(event controller.Event) { events = append(events, event) },
		PushMode:    func(model.APRecord) string { return controller.PushModeReconnect },
		LocateUntil: func(model.APRecord) time.Time { return locate },
	})
	if err != nil {
		t.Fatal(err)
	}
	server, ok := manager.Server("ap-a")
	if !ok {
		t.Fatal("AP not served")
	}
	status := server.Status()
	if status.PushMode != controller.PushModeReconnect || !status.LocateUntil.Equal(locate) ||
		status.Capabilities.ConfigChangeNotify == nil || *status.Capabilities.ConfigChangeNotify {
		t.Fatalf("protocol options did not reach the session: %+v", status)
	}
}
