package controller

import (
	"testing"

	"github.com/nicko170/dismist/internal/telemetry"
)

func TestClientActivityExcludesAPManagementIdentity(t *testing.T) {
	apID := "02-aa-00-00-00-03"
	if isClientActivity(apID, telemetry.Event{
		ClientMAC: "02:aa:00:00:00:03", ClientIP: "192.0.2.101",
		Title: "Succeeding DNS query from AP",
	}) {
		t.Fatal("AP management event appeared as wireless client")
	}
	if !isClientActivity(apID, telemetry.Event{
		ClientMAC: "02:aa:00:00:00:02", ClientIP: "203.0.113.100",
		Title: "Client connected",
	}) {
		t.Fatal("wireless client activity was dropped")
	}
}

func TestTopicsArrayEventsReachControllerClientActivity(t *testing.T) {
	server, err := NewServer(Options{AllowedIP: "127.0.0.1", ID: "ap-fixture", CertDER: []byte{1}, Config: []byte(`{"Timestamp":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	server.observeTelemetry([]byte(`{"Topics":["ep-pace-event-","client-pace-event-"],"Type":3,"Title":"Client connected","Text":"Associated","When":"2026-11-12T09:00:00Z","impacted_client_mac":"02-aa-00-00-00-01","impacted_client_ip":"192.0.2.10"}`))
	server.observeTelemetry([]byte(`{"Topics":["client-pace-event-"],"Type":4,"Title":"Client disconnected","When":"2026-11-12T09:05:00Z","impacted_client_mac":"02-aa-00-00-00-01"}`))
	server.observeTelemetry([]byte(`{"Topics":["unrelated-topic"],"Type":5,"Title":"Not a client event","impacted_client_mac":"02-aa-00-00-00-01"}`))
	stats := server.TelemetrySnapshot()
	if len(stats.Events) != 2 || stats.Events[0].Title != "Client connected" || stats.Events[1].Title != "Client disconnected" {
		t.Fatalf("Topics array events not recorded exactly once each: %+v", stats.Events)
	}
	if len(stats.RecentClients) != 1 || stats.RecentClients[0].MAC != "02:aa:00:00:00:01" ||
		stats.RecentClients[0].LastEvent != "Client disconnected" {
		t.Fatalf("client activity was not updated: %+v", stats.RecentClients)
	}
}
