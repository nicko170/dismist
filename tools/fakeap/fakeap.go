// Package fakeap is a software AP33 for tests and local runs (design §8.3,
// workstream B). It speaks the real AP side of the protocol: the get-time
// clock request, the ep-auth version 2 handshake (it verifies the
// controller's proof and proves its own secret), ep-config requests and
// ep-config-applied reports, pings, synthetic protobuf telemetry built
// with internal/telemetry/pbtest, and optional synthetic PACE events.
//
// Nothing here is captured data: every frame is built from field numbers,
// with documentation addresses (192.0.2.0/24) and locally administered
// MACs (02-aa-…).
//
// fakeap deliberately does not import internal/controller, so the
// controller's own tests can drive it.
package fakeap

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/auth"
	"github.com/nicko170/dismist/internal/protocol"
)

// Defaults for Config.
const (
	DefaultModel    = "AP33-WW"
	DefaultFirmware = "apfw-0.15.00000-fake-0000"
	DefaultTimeout  = 5 * time.Second
	subprotocol     = "mist-ep-v0"
)

var (
	// ErrServerProof means the controller's ep-auth proof did not verify:
	// it does not hold this AP's secret (a real AP refuses it).
	ErrServerProof = errors.New("fakeap: controller proof does not verify")
	// ErrNotAuthenticated means the controller did not accept the AP.
	ErrNotAuthenticated = errors.New("fakeap: controller refused authentication")
	// ErrClosed means the connection ended.
	ErrClosed = errors.New("fakeap: connection closed")
)

// Extras are the ep-auth hello fields beyond the ones authentication uses
// (msgs.AuthHello in 0.15). Zero values are sent as zero, like the AP.
type Extras struct {
	SKU              string
	SerialNo         string
	Developer        bool
	Uptime           int64
	ConnectUptime    int64
	BootCount        uint64
	LastRebootTime   time.Time
	LastRebootReason string
	LastBootState    string
	LocalIP          string
	// TimeoutSec is sent only when non-zero (0.14 has no such field).
	TimeoutSec int64
}

// DefaultExtras are plausible synthetic hello extras.
func DefaultExtras() Extras {
	return Extras{
		SKU: "AP33-WW", SerialNo: "EXAMPLE000001", Uptime: 120, ConnectUptime: 110,
		BootCount: 7, LastRebootTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		LastRebootReason: "unknown", LastBootState: "warm", LocalIP: "192.0.2.10",
	}
}

// Config describes one fake AP.
type Config struct {
	// URL is the controller's AP endpoint, for example wss://host/ws.
	URL    string
	ID     string
	Secret [32]byte
	// Model and Firmware default to DefaultModel and DefaultFirmware.
	Model    string
	Firmware string
	// Counter is the hello counter; zero picks one from the clock.
	Counter uint64
	Extras  Extras
	// HelloJSON, when set, is sent verbatim as the ep-auth hello (tests
	// send mutated hellos with it). The authentication fields are read
	// back from it the way the controller reads them.
	HelloJSON []byte
	// TLS configures the connection; nil skips certificate verification,
	// which is only for test servers.
	TLS *tls.Config
	// Timeout bounds the dial and each request; zero means DefaultTimeout.
	Timeout time.Duration
	// SkipGetTime omits the clock request before the hello.
	SkipGetTime bool
}

func (c Config) withDefaults() Config {
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Firmware == "" {
		c.Firmware = DefaultFirmware
	}
	if c.Counter == 0 {
		c.Counter = uint64(time.Now().UnixNano())
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	return c
}

// hello is msgs.AuthHello as 0.15 marshals it: names without tags are the
// Go field names.
type hello struct {
	Version          int    `json:"version"`
	ID               string `json:"id"`
	AuthType         string
	Model            string `json:"model"`
	Firmware         string `json:"firmware"`
	Suite            int    `json:"suite"`
	Counter          uint64 `json:"counter"`
	QC               []byte
	ENV              string
	SKU              string
	SerialNo         string
	Developer        bool `json:",omitempty"`
	Uptime           int64
	ConnectUptime    int64
	BootCount        uint64
	LastRebootTime   time.Time
	LastRebootReason string
	LastBootState    string
	LocalIP          string
	TimeoutSec       int64 `json:"Timeout_sec,omitempty"`
}

type reply1 struct {
	Version    int    `json:"version"`
	RS         []byte `json:"RS"`
	Suite      int    `json:"suite"`
	DateTimeNS uint64 `json:"datetime_ns"`
	QS         []byte `json:"QS"`
	ENV        string `json:"ENV"`
	ServerName string `json:"ServerName"`
}

type reply2 struct {
	Version        int    `json:"version"`
	NextDateTimeNS uint64 `json:"nextdatetime_ns"`
	Authed         bool   `json:"authed"`
	RC             []byte `json:"RC"`
}

// Reply3 is the controller's final ep-auth reply.
type Reply3 struct {
	Version     int    `json:"version"`
	NextCounter uint64 `json:"nextcounter"`
	Authed      bool   `json:"authed"`
}

// Session facts learned during the handshake.
type Session struct {
	// ServerTime is the controller clock from get-time (zero if skipped).
	ServerTime time.Time
	// ServerName is the controller's name from its first ep-auth reply.
	ServerName string
	Reply3     Reply3
}

// AP is one connected, authenticated fake AP.
type AP struct {
	config  Config
	conn    *websocket.Conn
	session Session

	writeMu sync.Mutex
	handle  atomic.Uint64

	mu      sync.Mutex
	waiters map[waitKey]chan protocol.Message
	inbox   chan protocol.Message
	done    chan struct{}
	err     error
}

type waitKey struct {
	subject string
	handle  uint64
}

// Connect dials the controller, runs get-time and the ep-auth handshake,
// and returns the authenticated AP with its frame reader running.
func Connect(ctx context.Context, config Config) (*AP, error) {
	config = config.withDefaults()
	tlsConfig := config.TLS
	if tlsConfig == nil {
		tlsConfig = &tls.Config{InsecureSkipVerify: true} // Test servers only; the CLI requires -ca or -insecure.
	}
	dialer := websocket.Dialer{
		Subprotocols: []string{subprotocol}, TLSClientConfig: tlsConfig,
		HandshakeTimeout: config.Timeout,
	}
	dialCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	conn, response, err := dialer.DialContext(dialCtx, config.URL, http.Header{})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("fakeap: dial: %w", err)
	}
	ap := &AP{
		config: config, conn: conn,
		waiters: make(map[waitKey]chan protocol.Message),
		inbox:   make(chan protocol.Message, 64),
		done:    make(chan struct{}),
	}
	ap.handle.Store(uint64(time.Now().UnixNano()) & 0xffffffff)
	if err := ap.handshake(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	go ap.readLoop()
	return ap, nil
}

func (a *AP) nextHandle() uint64 { return a.handle.Add(1) }

func (a *AP) write(class byte, subject string, handle uint64, body []byte) error {
	frame, err := protocol.Encode(protocol.Message{Version: 1, ServiceClass: class, Subject: subject, Handle: handle, Body: body})
	if err != nil {
		return err
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = a.conn.SetWriteDeadline(time.Now().Add(a.config.Timeout))
	return a.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// readSync reads one reply during the handshake, before the reader runs.
func (a *AP) readSync(subject string, handle uint64) (protocol.Message, error) {
	_ = a.conn.SetReadDeadline(time.Now().Add(a.config.Timeout))
	messageType, raw, err := a.conn.ReadMessage()
	if err != nil {
		return protocol.Message{}, fmt.Errorf("fakeap: waiting for %s: %w", subject, err)
	}
	if messageType != websocket.BinaryMessage {
		return protocol.Message{}, fmt.Errorf("fakeap: %s: non-binary reply", subject)
	}
	message, err := protocol.Decode(raw)
	if err != nil {
		return protocol.Message{}, err
	}
	if message.Subject != subject || message.Handle != handle {
		return protocol.Message{}, fmt.Errorf("fakeap: expected %s/%d, got %s/%d", subject, handle, message.Subject, message.Handle)
	}
	return message, nil
}

// helloBody returns the hello to send and the authentication fields the
// controller will read from it.
func (a *AP) helloBody() ([]byte, hello, error) {
	if a.config.HelloJSON != nil {
		var fields hello
		_ = json.Unmarshal(a.config.HelloJSON, &fields) // a broken hello is the test's point
		return a.config.HelloJSON, fields, nil
	}
	qc := make([]byte, 32)
	if _, err := rand.Read(qc); err != nil {
		return nil, hello{}, err
	}
	extras := a.config.Extras
	fields := hello{
		Version: 2, ID: a.config.ID, AuthType: "ap", Model: a.config.Model,
		Firmware: a.config.Firmware, Suite: 1, Counter: a.config.Counter, QC: qc,
		ENV: "production", SKU: extras.SKU, SerialNo: extras.SerialNo,
		Developer: extras.Developer, Uptime: extras.Uptime, ConnectUptime: extras.ConnectUptime,
		BootCount: extras.BootCount, LastRebootTime: extras.LastRebootTime,
		LastRebootReason: extras.LastRebootReason, LastBootState: extras.LastBootState,
		LocalIP: extras.LocalIP, TimeoutSec: extras.TimeoutSec,
	}
	body, err := json.Marshal(fields)
	return body, fields, err
}

func (a *AP) handshake() error {
	if !a.config.SkipGetTime {
		handle := a.nextHandle()
		if err := a.write(2, "get-time", handle, []byte{}); err != nil {
			return err
		}
		reply, err := a.readSync("get-time", handle)
		if err != nil {
			return err
		}
		if len(reply.Body) != 8 {
			return fmt.Errorf("fakeap: get-time reply of %d bytes", len(reply.Body))
		}
		a.session.ServerTime = time.UnixMicro(int64(binary.BigEndian.Uint64(reply.Body)))
	}
	body, fields, err := a.helloBody()
	if err != nil {
		return err
	}
	handle := a.nextHandle()
	if err := a.write(2, "ep-auth", handle, body); err != nil {
		return err
	}
	message, err := a.readSync("ep-auth", handle)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotAuthenticated, err)
	}
	var first reply1
	if err := json.Unmarshal(message.Body, &first); err != nil || first.Version != 2 || len(first.QS) != 32 || len(first.RS) != 32 {
		return fmt.Errorf("fakeap: malformed first ep-auth reply")
	}
	a.session.ServerName = first.ServerName
	state, ok := a.conn.UnderlyingConn().(*tls.Conn)
	if !ok {
		return errors.New("fakeap: not a TLS connection")
	}
	tlsState := state.ConnectionState()
	if len(tlsState.PeerCertificates) == 0 {
		return errors.New("fakeap: controller sent no certificate")
	}
	contextFor := func(entity string) [32]byte {
		return auth.Version2Context(auth.ContextInputs{
			Version: fields.Version, ID: fields.ID, Model: fields.Model, Firmware: fields.Firmware,
			Suite: fields.Suite, Cert: tlsState.PeerCertificates[0].Raw,
			CipherSuite: tlsState.CipherSuite, Entity: entity,
		})
	}
	var qc, qs [32]byte
	copy(qc[:], fields.QC)
	copy(qs[:], first.QS)
	inputs := auth.Inputs{Secret: a.config.Secret, Counter: fields.Counter, QC: qc, QS: qs, DateTimeNS: first.DateTimeNS}
	inputs.Context = contextFor("SERVER")
	expected := auth.ServerProof(inputs)
	if !hmac.Equal(expected[:], first.RS) {
		return ErrServerProof
	}
	inputs.Context = contextFor("CLIENT")
	proof := auth.ClientProof(inputs)
	body, err = json.Marshal(reply2{Version: 2, NextDateTimeNS: first.DateTimeNS + 1, Authed: true, RC: proof[:]})
	if err != nil {
		return err
	}
	if err := a.write(2, "ep-auth", handle, body); err != nil {
		return err
	}
	message, err = a.readSync("ep-auth", handle)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotAuthenticated, err)
	}
	var last Reply3
	if err := json.Unmarshal(message.Body, &last); err != nil || !last.Authed || last.NextCounter != fields.Counter+1 {
		return ErrNotAuthenticated
	}
	a.session.Reply3 = last
	return nil
}

func (a *AP) readLoop() {
	var err error
	defer func() {
		a.mu.Lock()
		a.err = err
		for key, waiter := range a.waiters {
			close(waiter)
			delete(a.waiters, key)
		}
		close(a.done) // under the lock, so Request never registers after this
		a.mu.Unlock()
	}()
	for {
		var messageType int
		var raw []byte
		messageType, raw, err = a.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		message, decodeErr := protocol.Decode(raw)
		if decodeErr != nil {
			continue
		}
		key := waitKey{message.Subject, message.Handle}
		a.mu.Lock()
		waiter, found := a.waiters[key]
		if found {
			delete(a.waiters, key)
		}
		a.mu.Unlock()
		if found {
			waiter <- message
			continue
		}
		select {
		case a.inbox <- message:
		default: // a full inbox drops controller-initiated frames
		}
	}
}

// Request sends a frame with a fresh handle and waits for the reply with
// the same subject and handle.
func (a *AP) Request(ctx context.Context, class byte, subject string, body []byte) (protocol.Message, error) {
	handle := a.nextHandle()
	waiter := make(chan protocol.Message, 1)
	key := waitKey{subject, handle}
	a.mu.Lock()
	select {
	case <-a.done:
		a.mu.Unlock()
		return protocol.Message{}, ErrClosed
	default:
	}
	a.waiters[key] = waiter
	a.mu.Unlock()
	cleanup := func() {
		a.mu.Lock()
		delete(a.waiters, key)
		a.mu.Unlock()
	}
	if err := a.write(class, subject, handle, body); err != nil {
		cleanup()
		return protocol.Message{}, err
	}
	timer := time.NewTimer(a.config.Timeout)
	defer timer.Stop()
	select {
	case message, ok := <-waiter:
		if !ok {
			return protocol.Message{}, ErrClosed
		}
		return message, nil
	case <-timer.C:
		cleanup()
		return protocol.Message{}, fmt.Errorf("fakeap: %s: %w", subject, context.DeadlineExceeded)
	case <-ctx.Done():
		cleanup()
		return protocol.Message{}, ctx.Err()
	}
}

// Send sends a frame that expects no reply and returns its handle.
func (a *AP) Send(class byte, subject string, body []byte) (uint64, error) {
	handle := a.nextHandle()
	return handle, a.write(class, subject, handle, body)
}

// Session returns what the handshake learned.
func (a *AP) Session() Session { return a.session }

// ID is the AP's ID.
func (a *AP) ID() string { return a.config.ID }

// Inbox delivers controller-initiated frames (anything that is not a reply
// to a pending request), such as udp-test or ep-config-change.
func (a *AP) Inbox() <-chan protocol.Message { return a.inbox }

// Done is closed when the connection ends.
func (a *AP) Done() <-chan struct{} { return a.done }

// Err is the read error that ended the connection, once Done is closed.
func (a *AP) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// Close sends a normal close frame and closes the connection.
func (a *AP) Close() error {
	a.writeMu.Lock()
	_ = a.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	a.writeMu.Unlock()
	return a.conn.Close()
}

// Drop closes the TCP connection without a close frame, like an AP that
// lost power.
func (a *AP) Drop() error { return a.conn.Close() }

// ConfigRequest is the AP's ep-config request (msgs.ConfigRequest).
type ConfigRequest struct {
	Firmware               string
	MfgCountryCode         string
	Tags                   string
	Accept                 string
	RecoveredFromBadConfig bool
	AppliedConfigTimestamp float64
	TimeoutSec             int `json:"Timeout_sec"`
}

// FetchConfig sends ep-config and returns the config the controller sent.
func (a *AP) FetchConfig(ctx context.Context, request ConfigRequest) ([]byte, error) {
	if request.Firmware == "" {
		request.Firmware = a.config.Firmware
	}
	if request.Accept == "" {
		request.Accept = "gz,json"
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	reply, err := a.Request(ctx, 2, "ep-config", body)
	if err != nil {
		return nil, err
	}
	return reply.Body, nil
}

// Ping sends a ping and returns the echoed body.
func (a *AP) Ping(ctx context.Context, body []byte) ([]byte, error) {
	reply, err := a.Request(ctx, 2, "ping", body)
	if err != nil {
		return nil, err
	}
	return reply.Body, nil
}

// ReportApplied sends ep-config-applied for a config Timestamp; errorText
// empty means the AP applied it.
func (a *AP) ReportApplied(timestamp float64, errorText string) error {
	return a.SendJSON(map[string]any{
		"Topic": "ep-config-applied-", "ConfigTimestamp": timestamp, "Error": errorText,
		"Firmware": a.config.Firmware, "Good5Min": false, "RecoveredFromBadConfig": false,
	})
}

// SendJSON sends a JSON ep-telemetry message (class 2), as the AP sends
// PACE events and apply reports.
func (a *AP) SendJSON(value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = a.Send(2, "ep-telemetry", body)
	return err
}

// SendTelemetry sends a binary ep-telemetry body (class 0, handle 0), as
// the AP sends protobuf stats.
func (a *AP) SendTelemetry(body []byte) error { return a.write(0, "ep-telemetry", 0, body) }
