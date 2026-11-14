package main

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestClientProofMatchesIndependentVector(t *testing.T) {
	var secret, qc, qs, context [32]byte
	for i := 0; i < 32; i++ {
		secret[i] = byte(i)
		qc[i] = byte(i + 32)
		qs[i] = byte(i + 64)
		context[i] = byte(i + 96)
	}
	got := clientProof(secret, 42, qc, qs, context, 1234567890123456789)
	if hex.EncodeToString(got[:]) != "3245223beb9d399fda330cb485c6e2a33251aa64ca3481a9521bdb794d76df5f" {
		t.Fatalf("wrong client proof: %x", got)
	}
}

func TestSiteSubscriptionRequest(t *testing.T) {
	raw, err := subscriptionRequest("site-connected-wc-", "site-a", 42)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID        uint64 `json:"ID"`
		Topic     string `json:"Topic"`
		Partition int    `json:"Partition"`
		Key       string `json:"Key"`
		Offset    int    `json:"Offset"`
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.ID != 42 || got.Topic != "site-connected-wc-" ||
		got.Partition != -1 || got.Key != "site-a" || got.Offset != -1 {
		t.Fatalf("wrong subscription: %+v %v", got, err)
	}
}

func TestTelemetryFrameClass(t *testing.T) {
	for _, sample := range []struct {
		body []byte
		want byte
	}{
		{[]byte("P\x0a\x00"), 0},
		{[]byte{2, 0x1f, 0x8b, 8}, 2},
		{[]byte(`{"Topic":"ep-config-applied-"}`), 2},
	} {
		got, err := telemetryClass(sample.body)
		if err != nil || got != sample.want {
			t.Fatalf("wrong class %d for %x: %v", got, sample.body, err)
		}
	}
}

func TestRelayDropsAPAuthenticationFrames(t *testing.T) {
	if relayAllowed("ep-auth") || relayAllowed("get-time") {
		t.Fatal("auth or clock exchange could be replayed upstream")
	}
	if !relayAllowed("ep-config") || !relayAllowed("ep-telemetry") || !relayAllowed("udp-setup") {
		t.Fatal("post-auth AP control frames were blocked")
	}
}

func TestParseUDPSetupReply(t *testing.T) {
	got, err := parseUDPSetupReply([]byte(`{"version":1,"address":"127.0.0.1:443","Protocol":1,"aad":"QUJDMTIzNDU="}`))
	if err != nil || got.Address != "127.0.0.1:443" || string(got.AAD) != "ABC12345" {
		t.Fatalf("UDP setup reply not decoded: %+v %v", got, err)
	}
	if _, err := parseUDPSetupReply([]byte(`{"version":1,"address":"127.0.0.1:443","Protocol":1,"aad":"AA=="}`)); err == nil {
		t.Fatal("accepted invalid UDP setup AAD")
	}
}

func TestParseUDPRekeyReply(t *testing.T) {
	got, err := parseUDPRekeyReply([]byte(`{"version":2,"keynum":1,"ok":true}`), 1)
	if err != nil || !got.OK {
		t.Fatalf("UDP rekey acknowledgement not decoded: %+v %v", got, err)
	}
	if _, err := parseUDPRekeyReply([]byte(`{"version":2,"keynum":2,"ok":true}`), 1); err == nil {
		t.Fatal("accepted wrong UDP key number")
	}
}

func TestUDPControlReplies(t *testing.T) {
	testRequest, _ := json.Marshal(map[string]any{"version": 3, "blob": []byte("0123456789abcdef"), "reply": false})
	testReply, ok, err := udpControlReply("udp-test", testRequest)
	if err != nil || !ok {
		t.Fatalf("UDP test was not answered: %v", err)
	}
	var gotTest struct {
		Version int    `json:"version"`
		Blob    []byte `json:"blob"`
		Reply   bool   `json:"reply"`
	}
	if err := json.Unmarshal(testReply, &gotTest); err != nil || !gotTest.Reply || string(gotTest.Blob) != "0123456789abcdef" {
		t.Fatalf("wrong UDP test reply: %+v %v", gotTest, err)
	}
	rekeyRequest, _ := json.Marshal(map[string]any{"version": 2, "keynum": 1, "secrets": make([]byte, 25)})
	rekeyReply, ok, err := udpControlReply("udp-rekey", rekeyRequest)
	if err != nil || !ok {
		t.Fatalf("UDP rekey was not answered: %v", err)
	}
	gotRekey, err := parseUDPRekeyReply(rekeyReply, 1)
	if err != nil || !gotRekey.OK {
		t.Fatalf("wrong UDP rekey reply: %+v %v", gotRekey, err)
	}
	if _, ok, err := udpControlReply("udp-rekey", rekeyReply); err != nil || ok {
		t.Fatal("replied to a UDP rekey acknowledgement")
	}
}
