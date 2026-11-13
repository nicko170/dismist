package controller

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/auth"
	"github.com/nicko170/dismist/internal/protocol"
)

func TestNewerAPConfigWinsOutOfOrderDelivery(t *testing.T) {
	first := []byte(`{"Timestamp":1,"Name":"first"}`)
	second := []byte(`{"Timestamp":2,"Name":"second"}`)
	server, err := NewServer(Options{AllowedIP: "127.0.0.1", ID: "ap-a", CertDER: []byte{1}, Config: first, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := server.SetConfigIfNewer(second, 3)
	if err != nil || !changed {
		t.Fatalf("new config was not installed: changed=%t err=%v", changed, err)
	}
	changed, err = server.SetConfigIfNewer(first, 2)
	if err != nil || changed {
		t.Fatalf("older config was installed: changed=%t err=%v", changed, err)
	}
	got, revision := server.ConfigSnapshot()
	if revision != 3 || !bytes.Equal(got, second) {
		t.Fatalf("older config replaced latest: rev=%d config=%s", revision, got)
	}
}

func TestWSSAuthenticatesAndDeliversLabConfig(t *testing.T) {
	a, hello, _, _ := testAuthFixture()
	config := []byte(`{"ID":"ap-1","Timestamp":123,"WLANs":[{"SSID":"TestSSID","Vlans":["123"]}]}`)
	capturedTelemetry := make(chan []byte, 4)
	capturedFrames := make(chan protocol.Message, 32)
	var handler *Server
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer tlsServer.Close()
	var err error
	handler, err = NewServer(Options{
		AllowedIP: "127.0.0.1", ID: "ap-1", Secret: a.Secret,
		CertDER: tlsServer.Certificate().Raw, Config: config, ServerName: "example-controller", Revision: 7,
		OnTelemetry:        func(body []byte) { capturedTelemetry <- body },
		OnAPFrame:          func(message protocol.Message) { capturedFrames <- message },
		UDPProbeAddress:    "198.51.100.19:9443",
		UDPProbeAAD:        []byte("ABC12345"),
		UDPProbeSendTest:   true,
		UDPProbeRekeyDelay: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	dialer := websocket.Dialer{
		Subprotocols:    []string{"mist-ep-v0"},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test server certificate only.
	}
	conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(tlsServer.URL, "https")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.Subprotocol() != "mist-ep-v0" {
		t.Fatalf("short subprotocol %q", conn.Subprotocol())
	}
	writeClass := func(serviceClass byte, subject string, handle uint64, body any) {
		t.Helper()
		var data []byte
		if raw, ok := body.([]byte); ok {
			data = raw
		} else {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		frame, err := protocol.Encode(protocol.Message{Version: 1, ServiceClass: serviceClass, Subject: subject, Handle: handle, Body: data})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	write := func(subject string, handle uint64, body any) {
		writeClass(2, subject, handle, body)
	}
	read := func(subject string, handle uint64) protocol.Message {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		msg, err := protocol.Decode(raw)
		if err != nil || msg.Subject != subject || msg.Handle != handle {
			t.Fatalf("short reply: %+v %v", msg, err)
		}
		return msg
	}
	write("get-time", 7, []byte{})
	timeReply := read("get-time", 7)
	if len(timeReply.Body) != 8 || binary.BigEndian.Uint64(timeReply.Body) == 0 {
		t.Fatalf("short clock reply %x", timeReply.Body)
	}
	write("ep-auth", 42, hello)
	var reply1 Reply1
	if err := json.Unmarshal(read("ep-auth", 42).Body, &reply1); err != nil {
		t.Fatal(err)
	}
	cert := tlsServer.Certificate().Raw
	cipherSuite := conn.UnderlyingConn().(*tls.Conn).ConnectionState().CipherSuite
	context := auth.Version2Context(auth.ContextInputs{
		Version: 2, ID: hello.ID, Model: hello.Model, Firmware: hello.Firmware,
		Suite: 1, Cert: cert, CipherSuite: cipherSuite, Entity: "CLIENT",
	})
	var qcArray, qsArray [32]byte
	copy(qcArray[:], hello.QC)
	copy(qsArray[:], reply1.QS)
	rc := auth.ServerProof(auth.Inputs{
		Secret: a.Secret, Counter: hello.Counter, QC: qsArray, QS: qcArray,
		Context: context, DateTimeNS: reply1.DateTimeNS,
	})
	write("ep-auth", 42, Reply2{
		Version: 2, NextDateTimeNS: reply1.DateTimeNS + 1, Authed: true, RC: rc[:],
	})
	var reply3 Reply3
	if err := json.Unmarshal(read("ep-auth", 42).Body, &reply3); err != nil {
		t.Fatal(err)
	}
	if !reply3.Authed || reply3.NextCounter != 43 {
		t.Fatalf("auth failed: %+v", reply3)
	}
	write("udp-setup", 76, map[string]any{"version": 1, "Protocols": []int{1}, "MTU": 1500})
	var udpReply struct {
		Version  int    `json:"version"`
		Address  string `json:"address"`
		Protocol int    `json:"Protocol"`
		AAD      string `json:"aad"`
	}
	if err := json.Unmarshal(read("udp-setup", 76).Body, &udpReply); err != nil {
		t.Fatal(err)
	}
	aad, err := base64.StdEncoding.DecodeString(udpReply.AAD)
	if err != nil || udpReply.Version != 1 || udpReply.Protocol != 1 ||
		udpReply.Address != "198.51.100.19:9443" || string(aad) != "ABC12345" {
		t.Fatalf("short UDP probe reply: %+v %v", udpReply, err)
	}
	sentRekeyAt := time.Now()
	write("udp-rekey", 80, map[string]any{
		"version": 2, "keynum": 1, "secrets": bytes.Repeat([]byte{3}, 25),
	})
	var rekeyReply struct {
		Version   int  `json:"version"`
		KeyNumber int  `json:"keynum"`
		OK        bool `json:"ok"`
	}
	if err := json.Unmarshal(read("udp-rekey", 80).Body, &rekeyReply); err != nil {
		t.Fatal(err)
	}
	if rekeyReply.Version != 2 || rekeyReply.KeyNumber != 1 || !rekeyReply.OK {
		t.Fatalf("short UDP rekey reply: %+v", rekeyReply)
	}
	if time.Since(sentRekeyAt) < 40*time.Millisecond {
		t.Fatal("UDP rekey probe reply was not delayed")
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, udpTestRaw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	udpTest, err := protocol.Decode(udpTestRaw)
	if err != nil || udpTest.Subject != "udp-test" || udpTest.ServiceClass != 2 || len(udpTest.Body) != 1428 {
		t.Fatalf("short UDP test request: %+v %v", udpTest, err)
	}
	select {
	case captured := <-capturedFrames:
		if captured.Subject != "udp-setup" || captured.Handle != 76 || captured.ServiceClass != 2 {
			t.Fatalf("short AP frame captured: %+v", captured)
		}
	case <-time.After(time.Second):
		t.Fatal("authenticated AP frame not captured")
	}
	connectionStatus := handler.Status()
	if !connectionStatus.Connected || connectionStatus.ConnectionCount != 1 ||
		connectionStatus.ConnectedAt.IsZero() || connectionStatus.TLSCipherSuite == "" {
		t.Fatalf("connection details missing: %+v", connectionStatus)
	}
	write("ep-config", 77, map[string]any{"Firmware": "fw-1", "Accept": "gz,json"})
	if got := read("ep-config", 77).Body; !bytes.Equal(got, config) {
		t.Fatalf("short config: %s", got)
	}
	writeClass(0, "ep-telemetry", 0, []byte{1, 2, 3})
	writeClass(1, "ip6", 0, []byte{4, 5, 6})
	write("ping", 79, []byte("alive"))
	if got := read("ping", 79).Body; string(got) != "alive" {
		t.Fatalf("short aliveness reply %q", got)
	}
	ping2Body := []byte{0x08, 0xce, 0x8f, 0xe6, 0x91, 0x9d, 0x34}
	write("ping2", 80, ping2Body)
	if got := read("ping2", 80).Body; !bytes.Equal(got, ping2Body) {
		t.Fatalf("short ping2 aliveness reply %x", got)
	}
	write("ep-config", 78, map[string]any{"Firmware": "fw-1", "Accept": "gz,json"})
	if got := read("ep-config", 78).Body; !bytes.Equal(got, config) {
		t.Fatal("connection closed after telemetry")
	}
	write("ep-telemetry", 0, map[string]any{"Topic": "ep-config-applied-", "ConfigTimestamp": 123, "Error": ""})
	captureDeadline := time.After(time.Second)
captureLoop:
	for {
		select {
		case captured := <-capturedTelemetry:
			if bytes.Contains(captured, []byte("ep-config-applied-")) {
				break captureLoop
			}
		case <-captureDeadline:
			t.Fatal("telemetry callback not called for config status")
		}
	}
	status := handler.Status()
	for deadline := time.Now().Add(time.Second); status.AppliedRevision != 7 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
		status = handler.Status()
	}
	if !status.Connected || status.AppliedRevision != 7 || status.Firmware != "fw-1" {
		t.Fatalf("AP apply status missing: %+v", status)
	}
	newConfig := []byte(`{"ID":"ap-1","Timestamp":124,"WLANs":[]}`)
	if err := handler.SetConfig(newConfig, 8); err != nil {
		t.Fatal(err)
	}
	if err := handler.SetConfig([]byte("not-json"), 9); err == nil {
		t.Fatal("accepted malformed new config")
	}
	write("ep-config", 80, map[string]any{"Firmware": "fw-1", "Accept": "gz,json"})
	if got := read("ep-config", 80).Body; !bytes.Equal(got, newConfig) {
		t.Fatalf("failed update replaced last valid config: %s", got)
	}
	write("ep-telemetry", 0, map[string]any{
		"Topic": "ep-pace-event-", "Type": 3, "Title": "Client connected",
		"Text": "Joined TestSSID", "When": "2026-11-12T09:00:00Z",
		"impacted_client_mac": "02-aa-00-00-00-02", "impacted_client_ip": "203.0.113.100",
	})
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		telemetry := handler.TelemetrySnapshot()
		if len(telemetry.RecentClients) == 1 {
			if telemetry.RecentClients[0].MAC != "02:aa:00:00:00:02" || len(telemetry.Events) == 0 {
				t.Fatalf("client activity not recorded: %+v", telemetry)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(handler.TelemetrySnapshot().RecentClients) != 1 {
		t.Fatal("client telemetry did not reach controller")
	}
}
