package controller

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/protocol"
)

const (
	// routeTimeout bounds how long an unidentified connection may take to
	// send its ep-auth hello.
	routeTimeout = 60 * time.Second
	// routeFrames bounds the clock requests accepted before the hello.
	routeFrames = 16
	// unknownLogInterval rate-limits the log line for an unregistered AP ID.
	unknownLogInterval = 10 * time.Minute
)

// ErrUnknownAP means the hello named an AP that isn't registered, or one
// pinned to a different source address.
var ErrUnknownAP = errors.New("AP not registered")

// Manager routes AP connections to their control sessions by the AP ID in
// the ep-auth hello. The session then authenticates the AP with that AP's own
// secret, so the ID is proven before the connection becomes the AP's session.
// Source addresses only matter for APs registered with an optional pin, so
// any number of APs can share one NAT address.
type Manager struct {
	mu          sync.RWMutex
	byID        map[string]*Server
	unknownSeen map[string]time.Time
	pending     *pendingMap
	routes      [len(routeResults)]atomic.Uint64
}

func NewManager() *Manager {
	return &Manager{
		byID: make(map[string]*Server), unknownSeen: make(map[string]time.Time),
		pending: newPendingMap(),
	}
}

// Route results (dismist_ap_ws_routes_total{result}).
const (
	RouteRouted        = "routed"
	RouteUnknownAP     = "unknown_ap"
	RouteBadFrame      = "bad_frame"
	RouteTimeoutClosed = "timeout_or_closed"
	RouteTooManyFrames = "too_many_frames"
)

var routeResults = [...]string{RouteRouted, RouteUnknownAP, RouteBadFrame, RouteTimeoutClosed, RouteTooManyFrames}

func (m *Manager) countRoute(result string) {
	for index, known := range routeResults {
		if known == result {
			m.routes[index].Add(1)
			return
		}
	}
}

// RouteCounters returns how many AP connections ended routing with each
// result (design §6.3 dismist_ap_ws_routes_total).
func (m *Manager) RouteCounters() map[string]uint64 {
	result := make(map[string]uint64, len(routeResults))
	for index, name := range routeResults {
		result[name] = m.routes[index].Load()
	}
	return result
}

// Pending returns the APs that tried to connect and were not served
// (design §5.4), most recently seen first. The list is bounded (64
// entries) and entries expire 24 hours after they were last seen.
func (m *Manager) Pending() []PendingAP { return m.pending.list() }

// PendingRevision changes whenever the pending list changes, so a stream
// can poll it cheaply and send a pending event only on change.
func (m *Manager) PendingRevision() uint64 { return m.pending.currentRevision() }

// Register installs an AP's control session. address is the optional source
// pin and must match the server's own; empty accepts the AP from anywhere.
func (m *Manager) Register(id, address string, server *Server) error {
	if id == "" || server == nil || server.auth.ID != id {
		return errors.New("invalid AP registration")
	}
	if address == "" {
		if server.allowedIP != nil {
			return errors.New("invalid AP registration")
		}
	} else if ip := net.ParseIP(address); ip == nil || !server.allowedIP.Equal(ip) {
		return errors.New("invalid AP registration")
	}
	m.mu.Lock()
	previous := m.byID[id]
	m.byID[id] = server
	m.mu.Unlock()
	m.pending.forget(id)
	if previous != nil && previous != server {
		previous.DisconnectBecause("registration changed")
	}
	return nil
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	previous := m.byID[id]
	delete(m.byID, id)
	m.mu.Unlock()
	if previous != nil {
		previous.DisconnectBecause("registration withdrawn")
	}
}

func (m *Manager) Server(id string) (*Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	server, ok := m.byID[id]
	return server, ok
}

func (m *Manager) Servers() map[string]*Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*Server, len(m.byID))
	for id, server := range m.byID {
		result[id] = server
	}
	return result
}

func (m *Manager) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/ws" {
		http.NotFound(writer, request)
		return
	}
	if request.TLS == nil {
		http.Error(writer, "TLS required", http.StatusForbidden)
		return
	}
	conn, err := apUpgrader().Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	routed, err := m.route(conn, request.RemoteAddr)
	if err != nil {
		closeWithPolicyViolation(conn, "AP not registered")
		_ = conn.Close()
		return
	}
	end := routed.server.serveConn(conn, request.TLS.CipherSuite, request.RemoteAddr, routed.raw)
	switch {
	case end.authenticated:
		m.pending.forget(routed.hello.ID)
	case end.authFailure != "":
		m.recordPending(routed.hello, routed.body, request.RemoteAddr, PendingAuthFailed)
	}
}

// routedConn is a connection the Manager routed to an AP's session.
type routedConn struct {
	server *Server
	// raw is the ep-auth hello frame for the session to authenticate.
	raw []byte
	// hello and body are that hello decoded and its JSON body.
	hello Hello
	body  []byte
}

// route answers clock requests sent before authentication and reads the AP's
// ep-auth hello. It returns the session for the AP the hello names, and the
// raw hello frame for that session to authenticate.
func (m *Manager) route(conn *websocket.Conn, remoteAddr string) (routedConn, error) {
	conn.SetReadLimit(64 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(routeTimeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	for frames := 0; frames < routeFrames; frames++ {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			m.countRoute(RouteTimeoutClosed)
			return routedConn{}, err
		}
		if messageType != websocket.BinaryMessage {
			m.countRoute(RouteBadFrame)
			return routedConn{}, ErrAuthentication
		}
		message, err := protocol.Decode(raw)
		if err != nil || message.Version != 1 || message.ServiceClass != 2 {
			m.countRoute(RouteBadFrame)
			return routedConn{}, ErrAuthentication
		}
		switch message.Subject {
		case "get-time":
			if err := writeReply(conn, message, clockBody()); err != nil {
				m.countRoute(RouteTimeoutClosed)
				return routedConn{}, err
			}
		case "ep-auth":
			var hello Hello
			if err := json.Unmarshal(message.Body, &hello); err != nil || hello.ID == "" {
				m.countRoute(RouteBadFrame)
				return routedConn{}, ErrAuthentication
			}
			m.mu.RLock()
			server := m.byID[hello.ID]
			m.mu.RUnlock()
			if server == nil || !server.acceptsSource(remoteAddr) {
				m.countRoute(RouteUnknownAP)
				reason := PendingUnregistered
				if server != nil {
					server.countSourceMismatch()
					reason = PendingSourceMismatch
				}
				m.logUnknown(hello, remoteAddr, server != nil)
				m.recordPending(hello, message.Body, remoteAddr, reason)
				return routedConn{}, ErrUnknownAP
			}
			m.countRoute(RouteRouted)
			return routedConn{server: server, raw: raw, hello: hello, body: message.Body}, nil
		default:
			m.countRoute(RouteBadFrame)
			return routedConn{}, ErrAuthentication
		}
	}
	m.countRoute(RouteTooManyFrames)
	return routedConn{}, ErrAuthentication
}

// recordPending adds a refused hello to the pending list. The hello is
// unauthenticated: its ID and strings are bounded and cleaned first.
func (m *Manager) recordPending(hello Hello, body []byte, remoteAddr, reason string) {
	hello.ID = cleanHelloString(hello.ID, maxHelloID)
	if hello.ID == "" {
		return
	}
	extras, _ := decodeHelloExtras(body)
	m.pending.record(hello, extras, remoteAddr, reason)
}

// logUnknown records an unregistered AP's ID so an operator can register it,
// at most once per ID and interval. The ID is unauthenticated at this point.
func (m *Manager) logUnknown(hello Hello, remoteAddr string, pinned bool) {
	key := cleanHelloString(hello.ID, maxHelloID)
	now := time.Now()
	m.mu.Lock()
	if last, seen := m.unknownSeen[key]; seen && now.Sub(last) < unknownLogInterval {
		m.mu.Unlock()
		return
	}
	if len(m.unknownSeen) > 1024 {
		m.unknownSeen = make(map[string]time.Time)
	}
	m.unknownSeen[key] = now
	m.mu.Unlock()
	if pinned {
		log.Printf("AP %q connected from %s, which isn't its registered source address", key, remoteAddr)
		return
	}
	log.Printf("unregistered AP %q (model %q, firmware %q) connected from %s", key,
		cleanHelloString(hello.Model, maxHelloString), cleanHelloString(hello.Firmware, maxHelloString), remoteAddr)
}

func apUpgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize: 4096, WriteBufferSize: 4096,
		Subprotocols: []string{"mist-ep-v0"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
}

func closeWithPolicyViolation(conn *websocket.Conn, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason),
		time.Now().Add(time.Second))
}
