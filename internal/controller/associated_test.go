package controller

import (
	"encoding/hex"
	"testing"

	"github.com/nicko170/dismist/internal/telemetry"
)

func TestAssociatedClientsFollowLiveAPStats(t *testing.T) {
	server, err := NewServer(Options{
		AllowedIP: "127.0.0.1", ID: "02-aa-00-00-00-03",
		CertDER: []byte{1}, Config: []byte(`{"Timestamp":1,"WLANs":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	full, _ := hex.DecodeString("500a588a02550a0602aa00000002220602aa000000042a0a1208546573745353494430af01407b5079585160d202b001f001b801ad8004e20204486f7374f802c801ba0304cb007164920408575041322d50534b9a040443434d50120e61702d73746174732d66756c6c2d1a1130322d61612d30302d30302d30302d3033")
	server.observeTelemetry(full)
	got := server.TelemetrySnapshot().AssociatedClients
	if len(got) != 1 || got[0].Name != "Host" || got[0].IP != "203.0.113.100" || got[0].RSSI != -62 {
		t.Fatalf("associated client missing: %+v", got)
	}
	fastEmpty := append([]byte("P\x0a\x00\x12\x0e"), []byte("ap-stats-fast-")...)
	server.observeTelemetry(fastEmpty)
	if len(server.TelemetrySnapshot().AssociatedClients) != 0 {
		t.Fatal("disconnected client remained associated")
	}
}

func TestFastClientStatsKeepNameFromFullReport(t *testing.T) {
	mac := "02:aa:00:00:00:02"
	previous := map[string]telemetry.Client{mac: {MAC: mac, Name: "Host", SSID: "TestSSID", RSSI: -62}}
	next := mergeAssociated(previous, []telemetry.Client{{MAC: mac, SSID: "TestSSID", RSSI: -60}})
	if next[mac].Name != "Host" || next[mac].RSSI != -60 {
		t.Fatalf("fast report lost client identity: %+v", next[mac])
	}
}
