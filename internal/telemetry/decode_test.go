package telemetry

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"testing"
	"time"
)

func TestDecodeCapturedShapeFullAPStats(t *testing.T) {
	raw, _ := hex.DecodeString("500a479001b7039a0107415033332d5757a2011a617066772d302e382e32313735382d686f747069652d623565618202191a0602aa000000042205776c302e312a085465737453534944120e61702d73746174732d66756c6c2d1a1130322d61612d30302d30302d30302d3033")
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != "ap-stats-full-" || got.APID != "02-aa-00-00-00-03" || got.UptimeSeconds != 439 ||
		got.Model != "AP33-WW" || got.Firmware != "apfw-0.8.21758-hotpie-b5ea" || len(got.VAPs) != 1 ||
		got.VAPs[0].Interface != "wl0.1" || got.VAPs[0].SSID != "TestSSID" || got.VAPs[0].BSSID != "02:aa:00:00:00:04" {
		t.Fatalf("short AP stats: %+v", got)
	}
	var zipped bytes.Buffer
	z := gzip.NewWriter(&zipped)
	_, _ = z.Write(raw)
	_ = z.Close()
	wrapped := append([]byte{2}, zipped.Bytes()...)
	fromGzip, err := Decode(wrapped)
	if err != nil || fromGzip.Topic != got.Topic || len(fromGzip.VAPs) != 1 {
		t.Fatalf("gzip frame was not decoded: %+v %v", fromGzip, err)
	}
}

func TestDecodeConfigAppliedAndClientEvent(t *testing.T) {
	status, err := Decode([]byte(`{"Topic":"ep-config-applied-","ConfigTimestamp":1794475705.063165,"Error":"","Firmware":"fw-1"}`))
	if err != nil || status.ConfigTimestamp != 1794475705.063165 || status.Topic != "ep-config-applied-" {
		t.Fatalf("short config status: %+v %v", status, err)
	}
	event, err := Decode([]byte(`{"Topic":"ep-pace-event-","Type":3,"Title":"Client connected","Text":"Joined TestSSID","When":"2026-11-12T09:00:00Z","impacted_client_mac":"02-aa-00-00-00-02","impacted_client_ip":"203.0.113.100"}`))
	if err != nil || event.Event == nil || event.Event.ClientMAC != "02:aa:00:00:00:02" || event.Event.ClientIP != "203.0.113.100" {
		t.Fatalf("short client event: %+v %v", event, err)
	}
}

func TestDecodeCapturedShapeTopicsArrayClientEvent(t *testing.T) {
	// Sanitized from an AP event capture: the wire message has Topics, not Topic.
	raw := []byte(`{"Topics":["ep-pace-event-","client-pace-event-"],"Type":1,"Title":"Client connected","When":"2026-11-12T09:00:00Z","impacted_client_mac":"02-aa-00-00-00-01","impacted_client_ip":"192.0.2.10","SSID":"fixture-net","DHCP_server_ip":"192.0.2.1"}`)
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != "ep-pace-event-" || got.Event == nil || got.Event.Title != "Client connected" ||
		got.Event.ClientMAC != "02:aa:00:00:00:01" || got.Event.ClientIP != "192.0.2.10" ||
		!got.Event.When.Equal(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("captured-shape event was not decoded: %+v", got)
	}
}

func TestDecodeClientOnlyTopicsArrayEvent(t *testing.T) {
	got, err := Decode([]byte(`{"Topics":["client-pace-event-"],"Type":3,"Title":"Client connected","impacted_client_mac":"02-aa-00-00-00-01"}`))
	if err != nil || got.Topic != "client-pace-event-" || got.Event == nil {
		t.Fatalf("client-only event was not decoded: %+v %v", got, err)
	}
}

func TestDecodeTopicsArrayIgnoresUnknownAndRejectsMalformed(t *testing.T) {
	got, err := Decode([]byte(`{"Topics":["unrelated-topic"],"Type":3,"Title":"Private event","impacted_client_mac":"02-aa-00-00-00-01"}`))
	if err != nil || got.Topic != "" || got.Event != nil {
		t.Fatalf("unknown topic was treated as client event: %+v %v", got, err)
	}
	if _, err := Decode([]byte(`{"Topics":["client-pace-event-",7],"Type":3}`)); err == nil {
		t.Fatal("accepted malformed Topics array")
	}
}

func TestDecodeAssociatedClientFromAPStats(t *testing.T) {
	raw, _ := hex.DecodeString("500a588a02550a0602aa00000002220602aa000000042a0a1208546573745353494430af01407b5079585160d202b001f001b801ad8004e20204486f7374f802c801ba0304cb007164920408575041322d50534b9a040443434d50120e61702d73746174732d66756c6c2d1a1130322d61612d30302d30302d30302d3033")
	got, err := Decode(raw)
	if err != nil || len(got.Clients) != 1 {
		t.Fatalf("client record missing: %+v %v", got, err)
	}
	client := got.Clients[0]
	if client.MAC != "02:aa:00:00:00:02" || client.Name != "Host" || client.IP != "203.0.113.100" ||
		client.SSID != "TestSSID" || client.VLANID != 200 || client.RSSI != -62 ||
		client.TxBytes != 338 || client.RxBytes != 65581 || client.BSSID != "02:aa:00:00:00:04" ||
		client.Auth != "WPA2-PSK" || client.Cipher != "CCMP" {
		t.Fatalf("short associated client: %+v", client)
	}
}

func TestDecodeRejectsMalformedCompressedFrame(t *testing.T) {
	if _, err := Decode([]byte{2, 0x1f, 0x8b, 8}); err == nil {
		t.Fatal("accepted truncated gzip")
	}
}
