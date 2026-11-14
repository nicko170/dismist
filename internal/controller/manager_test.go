package controller

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/auth"
	"github.com/nicko170/dismist/internal/protocol"
)

func TestManagerRegistrationPinsAreOptional(t *testing.T) {
	config := []byte(`{"WLANs":[]}`)
	open, _ := NewServer(Options{ID: "ap-a", CertDER: []byte{1}, Config: config})
	pinned, _ := NewServer(Options{AllowedIP: "127.0.0.2", ID: "ap-b", CertDER: []byte{1}, Config: config})
	shared, _ := NewServer(Options{AllowedIP: "127.0.0.2", ID: "ap-c", CertDER: []byte{1}, Config: config})
	manager := NewManager()
	if err := manager.Register("ap-a", "", open); err != nil {
		t.Fatalf("AP without a source pin rejected: %v", err)
	}
	if err := manager.Register("ap-b", "127.0.0.2", pinned); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("ap-c", "127.0.0.2", shared); err != nil {
		t.Fatalf("second AP sharing a source address rejected: %v", err)
	}
	if err := manager.Register("ap-b", "127.0.0.9", pinned); err == nil {
		t.Fatal("registration address differing from the server's pin accepted")
	}
	if err := manager.Register("ap-b", "", pinned); err == nil {
		t.Fatal("pinned server registered without its pin")
	}
	if err := manager.Register("ap-x", "", open); err == nil {
		t.Fatal("server registered under another AP's ID")
	}
	if got, ok := manager.Server("ap-c"); !ok || got != shared {
		t.Fatal("shared-address AP did not get its own control session")
	}
	plain := httptest.NewRecorder()
	manager.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/ws", nil))
	if plain.Code != http.StatusForbidden {
		t.Fatalf("plain HTTP request accepted: %d", plain.Code)
	}
	other := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/other", nil)
	request.TLS = &tls.ConnectionState{}
	manager.ServeHTTP(other, request)
	if other.Code != http.StatusNotFound {
		t.Fatalf("unknown path = %d", other.Code)
	}
	manager.Remove("ap-b")
	if _, ok := manager.Server("ap-b"); ok {
		t.Fatal("removed AP still registered")
	}
}

// testAP is a minimal AP client: it runs the clock request and ep-auth
// handshake with its own ID and secret against a controller URL.
type testAP struct {
	id     string
	secret [32]byte
}

func (a testAP) connect(t *testing.T, url string, cert []byte) (*websocket.Conn, error) {
	t.Helper()
	dialer := websocket.Dialer{
		Subprotocols:    []string{"mist-ep-v0"},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // Test server certificate only.
	}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	send := func(subject string, handle uint64, body []byte) error {
		frame, err := protocol.Encode(protocol.Message{Version: 1, ServiceClass: 2, Subject: subject, Handle: handle, Body: body})
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.BinaryMessage, frame)
	}
	receive := func(subject string, handle uint64) (protocol.Message, error) {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return protocol.Message{}, err
		}
		message, err := protocol.Decode(raw)
		if err != nil || message.Subject != subject || message.Handle != handle {
			return protocol.Message{}, errors.New("unexpected reply")
		}
		return message, nil
	}
	fail := func(err error) (*websocket.Conn, error) {
		_ = conn.Close()
		return nil, err
	}
	if err := send("get-time", 1, []byte{}); err != nil {
		return fail(err)
	}
	if _, err := receive("get-time", 1); err != nil {
		return fail(err)
	}
	qc := make([]byte, 32)
	for i := range qc {
		qc[i] = byte(i + 7)
	}
	hello := Hello{Version: 2, ID: a.id, AuthType: "ap", Model: "AP33-WW", Firmware: "fw-test", Suite: 1, Counter: 9, QC: qc, ENV: "production"}
	body, _ := json.Marshal(hello)
	if err := send("ep-auth", 2, body); err != nil {
		return fail(err)
	}
	message, err := receive("ep-auth", 2)
	if err != nil {
		return fail(err)
	}
	var reply1 Reply1
	if err := json.Unmarshal(message.Body, &reply1); err != nil {
		return fail(err)
	}
	cipherSuite := conn.UnderlyingConn().(*tls.Conn).ConnectionState().CipherSuite
	context := auth.Version2Context(auth.ContextInputs{
		Version: 2, ID: hello.ID, Model: hello.Model, Firmware: hello.Firmware,
		Suite: 1, Cert: cert, CipherSuite: cipherSuite, Entity: "CLIENT",
	})
	var qcArray, qsArray [32]byte
	copy(qcArray[:], hello.QC)
	copy(qsArray[:], reply1.QS)
	rc := auth.ServerProof(auth.Inputs{
		Secret: a.secret, Counter: hello.Counter, QC: qsArray, QS: qcArray,
		Context: context, DateTimeNS: reply1.DateTimeNS,
	})
	body, _ = json.Marshal(Reply2{Version: 2, NextDateTimeNS: reply1.DateTimeNS + 1, Authed: true, RC: rc[:]})
	if err := send("ep-auth", 2, body); err != nil {
		return fail(err)
	}
	message, err = receive("ep-auth", 2)
	if err != nil {
		return fail(err)
	}
	var reply3 Reply3
	if err := json.Unmarshal(message.Body, &reply3); err != nil || !reply3.Authed {
		return fail(errors.New("not authenticated"))
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn, nil
}

func waitConnected(t *testing.T, server *Server, want bool) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status := server.Status()
		if status.Connected == want || time.Now().After(deadline) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagerRoutesAPsBehindOneAddressByAuthenticatedID(t *testing.T) {
	manager := NewManager()
	tlsServer := httptest.NewTLSServer(manager)
	defer tlsServer.Close()
	url := "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws"
	cert := tlsServer.Certificate().Raw
	config := []byte(`{"WLANs":[]}`)
	first := testAP{id: "aa-bb-cc-00-00-01"}
	second := testAP{id: "aa-bb-cc-00-00-02"}
	for i := range first.secret {
		first.secret[i] = byte(i)
		second.secret[i] = byte(200 - i)
	}
	servers := map[string]*Server{}
	for _, ap := range []testAP{first, second} {
		server, err := NewServer(Options{ID: ap.id, Secret: ap.secret, CertDER: cert, Config: config})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Register(ap.id, "", server); err != nil {
			t.Fatal(err)
		}
		servers[ap.id] = server
	}

	// Both APs arrive from the same address (127.0.0.1) and each reaches its
	// own session.
	firstConn, err := first.connect(t, url, cert)
	if err != nil {
		t.Fatalf("first AP: %v", err)
	}
	defer firstConn.Close()
	secondConn, err := second.connect(t, url, cert)
	if err != nil {
		t.Fatalf("second AP: %v", err)
	}
	defer secondConn.Close()
	for id, server := range servers {
		status := waitConnected(t, server, true)
		if !status.Connected || status.RemoteAddress != "127.0.0.1" || status.Model != "AP33-WW" {
			t.Fatalf("AP %s status after auth: %+v", id, status)
		}
	}

	// An unregistered AP ID is refused.
	if _, err := (testAP{id: "aa-bb-cc-00-00-03", secret: first.secret}).connect(t, url, cert); err == nil {
		t.Fatal("unregistered AP authenticated")
	}
	// A client naming the first AP without its secret fails, and does not
	// displace the first AP's live session.
	if _, err := (testAP{id: first.id, secret: second.secret}).connect(t, url, cert); err == nil {
		t.Fatal("AP authenticated with another AP's secret")
	}
	if status := servers[first.id].Status(); !status.Connected {
		t.Fatalf("failed impostor disconnected the real AP: %+v", status)
	}
	if err := ping(firstConn); err != nil {
		t.Fatalf("real AP session broken by impostor: %v", err)
	}

	// A reconnect with the right secret replaces the old session.
	again, err := first.connect(t, url, cert)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer again.Close()
	_ = firstConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := firstConn.ReadMessage(); err == nil || isTimeout(err) {
		t.Fatalf("old session not closed after re-authentication: %v", err)
	}
	if !servers[first.id].Status().Connected {
		t.Fatal("reconnected AP reported disconnected")
	}
}

func TestManagerEnforcesOptionalSourcePin(t *testing.T) {
	manager := NewManager()
	tlsServer := httptest.NewTLSServer(manager)
	defer tlsServer.Close()
	url := "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws"
	cert := tlsServer.Certificate().Raw
	ap := testAP{id: "aa-bb-cc-00-00-09"}
	ap.secret[0] = 9
	server, err := NewServer(Options{AllowedIP: "192.0.2.50", ID: ap.id, Secret: ap.secret, CertDER: cert, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(ap.id, "192.0.2.50", server); err != nil {
		t.Fatal(err)
	}
	if _, err := ap.connect(t, url, cert); err == nil {
		t.Fatal("pinned AP authenticated from another source address")
	}
	if server.Status().Connected {
		t.Fatal("pinned AP marked connected")
	}
}

// ping runs an authenticated ping round trip on an AP connection.
func ping(conn *websocket.Conn) error {
	frame, err := protocol.Encode(protocol.Message{Version: 1, ServiceClass: 2, Subject: "ping", Handle: 77, Body: []byte("hi")})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	message, err := protocol.Decode(raw)
	if err != nil || message.Subject != "ping" || message.Handle != 77 {
		return errors.New("unexpected ping reply")
	}
	return nil
}

func isTimeout(err error) bool {
	var netErr interface{ Timeout() bool }
	return errors.As(err, &netErr) && netErr.Timeout()
}
