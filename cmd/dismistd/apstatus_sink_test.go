//go:build apstatus

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

func TestAPStatusSinkRoundTrip(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = append(state.Sites, model.Site{ID: "site-a", Name: "A"})
		state.APs = append(state.APs, model.APRecord{ID: "02-aa-00-00-00-01", SiteID: "site-a", Secret: make([]byte, model.SecretSize)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sink := newAPStatusSink(store.APStatus())
	at := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	previous, _, err := sink.RecordHello("02-aa-00-00-00-01", at, "AP33-WW", controller.HelloExtras{BootCount: 7, Uptime: 100})
	if err != nil || previous != nil {
		t.Fatalf("first hello %+v %v", previous, err)
	}
	previous, previousAt, err := sink.RecordHello("02-aa-00-00-00-01", at.Add(time.Minute), "AP33-WW", controller.HelloExtras{BootCount: 8})
	if err != nil || previous == nil || previous.BootCount != 7 || !previousAt.Equal(at) {
		t.Fatalf("second hello %+v %v %v", previous, previousAt, err)
	}
	if err := sink.RecordReboot("02-aa-00-00-00-01", controller.Reboot{DetectedAt: at, Evidence: controller.RebootEvidenceBootCount, BootCount: 8}); err != nil {
		t.Fatal(err)
	}
	vaps := []telemetry.VAP{{Interface: "wl0.1", BSSID: "02:aa:00:00:11:01", SSID: "Example", RadioIndex: 0}}
	if changed, err := sink.SetVAPs("02-aa-00-00-00-01", at, vaps); err != nil || !changed {
		t.Fatalf("vaps %v %v", changed, err)
	}
	if err := sink.SetTunnelIPv6("02-aa-00-00-00-01", at, "fd00:db8::1"); err != nil {
		t.Fatal(err)
	}
	if err := sink.SetConfigRequest("02-aa-00-00-00-01", controller.ConfigRequest{At: at, Capabilities: []string{"owe"}}); err != nil {
		t.Fatal(err)
	}
	loaded := lastKnownVAPs(store.APStatus())("02-aa-00-00-00-01")
	if len(loaded) != 1 || loaded[0].Interface != "wl0.1" || loaded[0].Band != telemetry.Band5 {
		t.Fatalf("persisted VAPs %+v", loaded)
	}
	record, err := store.APStatus().Get("02-aa-00-00-00-01")
	if err != nil || len(record.Reboots) != 1 || record.TunnelIPv6 != "fd00:db8::1" || record.ConfigRequest == nil {
		t.Fatalf("ap_status %+v %v", record, err)
	}
}
