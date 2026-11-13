package main

import (
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/telemetry"
)

func TestObservationMapsLiveClientAndEventsToSite(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	status := controller.Status{Connected: true, Firmware: "fw-1", AppliedRevision: 3}
	stats := controller.TelemetrySnapshot{
		UptimeSeconds:     100,
		LastFull:          now,
		AssociatedClients: []telemetry.Client{{MAC: "02:aa:00:00:00:02", Name: "Host", IP: "203.0.113.100", SSID: "TestSSID", VLANID: 123, RSSI: -62, TxBytes: 100, RxBytes: 200}},
		Events:            []controller.ConnectionEvent{{When: now, Kind: "connection", Title: "AP authenticated"}},
	}
	got := makeObservation("site-a", "ap-a", status, stats, now)
	if got.SiteID != "site-a" || got.APID != "ap-a" || len(got.Clients) != 1 ||
		got.Clients[0].IP != "203.0.113.100" || len(got.Events) != 1 || got.AppliedRevision != 3 {
		t.Fatalf("observation mapping lost data: %+v", got)
	}
	status.Connected = false
	got = makeObservation("site-a", "ap-a", status, stats, now)
	if len(got.Clients) != 0 || !got.SkipClients {
		t.Fatal("disconnected AP retained active clients")
	}
}

func TestObservationWaitsForTelemetryAfterReconnect(t *testing.T) {
	now := time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC)
	status := controller.Status{Connected: true, ConnectedAt: now}
	stats := controller.TelemetrySnapshot{
		LastFast:          now.Add(-time.Minute),
		AssociatedClients: []telemetry.Client{{MAC: "02:aa:00:00:00:02", IP: "203.0.113.100"}},
	}
	before := makeObservation("site-a", "ap-a", status, stats, now.Add(time.Second))
	if !before.SkipClients || len(before.Clients) != 0 {
		t.Fatalf("pre-reconnect client was recorded as live: %+v", before)
	}
	stats.LastFast = now.Add(2 * time.Second)
	after := makeObservation("site-a", "ap-a", status, stats, now.Add(3*time.Second))
	if after.SkipClients || len(after.Clients) != 1 {
		t.Fatalf("fresh client telemetry was ignored: %+v", after)
	}
}
