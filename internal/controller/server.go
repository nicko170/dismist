package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/protocol"
	"github.com/nicko170/dismist/internal/telemetry"
)

// Config sources (Options.ConfigSource, Status.ConfigSource). They match
// compile.SourceCompiled, SourceDelivered and SourceNone.
const (
	// ConfigSourceCompiled: the AP is served its freshly compiled config.
	ConfigSourceCompiled = "compiled"
	// ConfigSourceDelivered: its compile failed and it is served the last
	// config it was actually delivered (ap_config.delivered), unchanged.
	ConfigSourceDelivered = "delivered"
	// ConfigSourceNone: its compile failed and nothing was ever delivered;
	// ep-config requests are left unanswered until a config is set.
	ConfigSourceNone = "none"
)

type Options struct {
	AllowedIP  string
	ID         string
	Secret     [32]byte
	CertDER    []byte
	Config     []byte
	ServerName string
	Revision   uint64
	// ConfigSource says where Config came from; empty means compiled. With
	// ConfigSourceNone, Config must be empty.
	ConfigSource string
	// CompileError is the compile failure behind a delivered or absent
	// config. It is shown in Status and recorded as a config event.
	CompileError string
	// Features are the protocol features switched on for this AP.
	Features Features
	// OnConfigDelivered is called after every ep-config reply, with the
	// bytes sent (design §4.4: ap_config.delivered is written each time).
	OnConfigDelivered func(ConfigDelivery)
	// OnEvent, when set, receives every typed event (controller and PACE)
	// for the events2 store (design §5.8). It is called outside the
	// server's lock, on the goroutine that produced the event, and must
	// not block for long.
	OnEvent     func(Event)
	OnTelemetry func([]byte)
	OnAPFrame   func(protocol.Message)

	// Status persists last-known facts in ap_status (design §5.4, §8.4):
	// hellos, reboots, the field-32 VAP list, the tunnel address and the
	// ep-config request. nil keeps them in memory only.
	Status StatusSink
	// LastKnownVAPs seeds the last-known VAP list (from ap_status) so the
	// unchanged-config verification after a pod restart has a baseline.
	LastKnownVAPs []telemetry.VAP
	// LastDelivered is the config last delivered to the AP before this
	// session object existed (ap_config.delivered), the baseline of the
	// unchanged-config check.
	LastDelivered []byte
	// LocalFiles is the AP's effective.LocalFiles* state, which the
	// verification's normaliser needs.
	LocalFiles string

	// PushMode is the AP's learned push mode (APRecord.PushMode): "",
	// PushModeNotify or PushModeReconnect.
	PushMode string
	// OnPush receives every push lifecycle state (SSE "push", design
	// §4.10).
	OnPush func(PushEvent)
	// OnPushModeLearned is called when the learned push mode changes, to
	// store it in APRecord.PushMode.
	OnPushModeLearned func(apID, mode string)
	// OnVerified receives every post-delivery verification result, to
	// store it in ap_config.delivered.verified (design §5.9).
	OnVerified func(apID string, result Verification)
	// OnScan receives every neighbour scan (field 69) for the scans
	// bucket (design §5.10).
	OnScan func(apID string, at time.Time, scan telemetry.Scan)
	// OnCommandOutput receives ap-cmd-output- messages (ping, traceroute,
	// arp output, phase 4).
	OnCommandOutput func(apID string, output telemetry.CommandOutput)
	// LocateUntil is when the AP's locate state expires (APRecord
	// .LocateUntil); OnLocateExpired is called then, to clear it and push
	// LED.Locator=false (design §5.6).
	LocateUntil     time.Time
	OnLocateExpired func(apID string)
	// Timings overrides the protocol timers (tests); zero fields keep the
	// defaults.
	Timings Timings

	UDPProbeAddress    string
	UDPProbeAAD        []byte
	UDPProbeSendTest   bool
	UDPProbeRekeyDelay time.Duration
}

// ConfigDelivery is one config sent to the AP in an ep-config reply.
type ConfigDelivery struct {
	APID     string
	Config   []byte
	Revision uint64
	// Timestamp is the config's Timestamp value.
	Timestamp float64
	At        time.Time
}

// apConn is one AP WebSocket connection. Replies (session goroutine),
// pushes and commands (API goroutines) all write through it, so writes are
// serialised.
type apConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
}

func (c *apConn) write(message protocol.Message) error {
	frame, err := protocol.Encode(message)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return c.ws.WriteMessage(websocket.BinaryMessage, frame)
}

// reply answers a request frame with the same version, class, subject and
// handle.
func (c *apConn) reply(request protocol.Message, body []byte) error {
	return c.write(protocol.Message{
		Version: request.Version, ServiceClass: request.ServiceClass,
		Subject: request.Subject, Handle: request.Handle, Body: body,
	})
}

func (c *apConn) close() error { return c.ws.Close() }

type Server struct {
	allowedIP         net.IP
	auth              Authenticator
	mu                sync.RWMutex
	config            []byte
	revision          uint64
	configTS          float64
	active            *apConn
	closing           *apConn
	closingCause      string
	status            Status
	counters          counters
	features          atomic.Uint32
	onConfigDelivered func(ConfigDelivery)
	onEvent           func(Event)
	onPush            func(PushEvent)
	onPushMode        func(apID, mode string)
	onVerified        func(apID string, result Verification)
	onScan            func(apID string, at time.Time, scan telemetry.Scan)
	onCommandOutput   func(apID string, output telemetry.CommandOutput)
	onLocateExpired   func(apID string)
	sink              StatusSink
	localFiles        string
	timings           Timings
	stats             TelemetrySnapshot
	clients           map[string]ClientActivity
	associated        map[string]telemetry.Client
	observe           observeState
	push              pushState
	verify            verifyState
	commands          commandState
	locate            locateState
	learnedPushMode   string
	otherSubjects     map[string]time.Time

	eventsMu      sync.Mutex
	pendingEvents []Event
	pendingPushes []PushEvent

	onTelemetry        func([]byte)
	onAPFrame          func(protocol.Message)
	udpProbeAddress    string
	udpProbeAAD        []byte
	udpProbeSendTest   bool
	udpProbeRekeyDelay time.Duration
}

type ClientActivity struct {
	MAC       string    `json:"mac"`
	IP        string    `json:"ip,omitempty"`
	LastEvent string    `json:"last_event"`
	LastSeen  time.Time `json:"last_seen"`
}

// ConnectionEvent is one entry of the AP's in-memory event buffer (the
// last 100), which the legacy UI and the SSE stream read. Stored events
// are the typed Event (OnEvent).
type ConnectionEvent struct {
	When      time.Time `json:"when"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	ClientMAC string    `json:"client_mac,omitempty"`
	// ID is the PACE event ID; a re-sent event with the same ID updates
	// the buffered one instead of adding another.
	ID       string `json:"id,omitempty"`
	Severity string `json:"severity,omitempty"`
}

func NewServer(options Options) (*Server, error) {
	// AllowedIP optionally pins the AP to one source address. Without it the
	// AP is accepted from anywhere and identified by its authenticated ID.
	var allowed net.IP
	if address := strings.TrimSpace(options.AllowedIP); address != "" {
		if allowed = net.ParseIP(address); allowed == nil {
			return nil, errors.New("invalid controller options")
		}
	}
	source := options.ConfigSource
	if source == "" {
		source = ConfigSourceCompiled
	}
	switch {
	case options.ID == "" || len(options.CertDER) == 0:
		return nil, errors.New("invalid controller options")
	case source == ConfigSourceNone:
		if len(options.Config) != 0 {
			return nil, errors.New("invalid controller options")
		}
	case source == ConfigSourceCompiled || source == ConfigSourceDelivered:
		if !json.Valid(options.Config) {
			return nil, errors.New("invalid controller options")
		}
	default:
		return nil, errors.New("invalid controller options")
	}
	switch options.PushMode {
	case "", PushModeNotify, PushModeReconnect:
	default:
		return nil, errors.New("invalid controller options")
	}
	if err := CheckUDPProbe(options.UDPProbeAddress, options.UDPProbeAAD, options.UDPProbeSendTest, options.UDPProbeRekeyDelay); err != nil {
		return nil, err
	}
	probeAAD := options.UDPProbeAAD
	if len(probeAAD) == 0 {
		probeAAD = []byte("Dismist!")
	}
	var metadata struct {
		Timestamp float64 `json:"Timestamp"`
	}
	var config []byte
	if source != ConfigSourceNone {
		_ = json.Unmarshal(options.Config, &metadata)
		config = append([]byte(nil), options.Config...)
	}
	server := &Server{
		allowedIP: allowed,
		auth: Authenticator{
			ID: options.ID, Secret: options.Secret,
			CertDER: append([]byte(nil), options.CertDER...), ServerName: options.ServerName,
		},
		config:             config,
		revision:           options.Revision,
		configTS:           metadata.Timestamp,
		clients:            make(map[string]ClientActivity),
		associated:         make(map[string]telemetry.Client),
		otherSubjects:      make(map[string]time.Time),
		onConfigDelivered:  options.OnConfigDelivered,
		onEvent:            options.OnEvent,
		onPush:             options.OnPush,
		onPushMode:         options.OnPushModeLearned,
		onVerified:         options.OnVerified,
		onScan:             options.OnScan,
		onCommandOutput:    options.OnCommandOutput,
		onLocateExpired:    options.OnLocateExpired,
		sink:               options.Status,
		localFiles:         options.LocalFiles,
		timings:            options.Timings.withDefaults(),
		learnedPushMode:    options.PushMode,
		onTelemetry:        options.OnTelemetry,
		onAPFrame:          options.OnAPFrame,
		udpProbeAddress:    options.UDPProbeAddress,
		udpProbeAAD:        append([]byte(nil), probeAAD...),
		udpProbeSendTest:   options.UDPProbeSendTest,
		udpProbeRekeyDelay: options.UDPProbeRekeyDelay,
	}
	server.status.ConfigSource = source
	server.status.DesiredConfigTimestamp = metadata.Timestamp
	server.features.Store(uint32(options.Features))
	if len(options.LastKnownVAPs) != 0 {
		server.stats.LastKnownVAPs = append([]telemetry.VAP(nil), options.LastKnownVAPs...)
	}
	if len(options.LastDelivered) != 0 && json.Valid(options.LastDelivered) {
		server.verify.previous = append([]byte(nil), options.LastDelivered...)
	}
	if options.CompileError != "" {
		server.status.CompileError = truncateText(options.CompileError, 500)
		server.counters.compileErrors.Add(1)
		title := "Configuration compile failed; serving the last delivered configuration"
		if source == ConfigSourceNone {
			title = "Configuration compile failed; no configuration to serve"
		}
		server.emitLocked(Event{Kind: EventConfig, Severity: SeverityError, Title: title, Detail: server.status.CompileError})
	}
	server.SetLocateUntil(options.LocateUntil)
	server.flush()
	return server, nil
}

// CheckUDPProbe validates the UDP probe options every AP shares, so a
// daemon-wide misconfiguration can fail fast instead of failing each AP.
func CheckUDPProbe(address string, aad []byte, sendTest bool, rekeyDelay time.Duration) error {
	if address != "" {
		resolved, err := net.ResolveUDPAddr("udp4", address)
		if err != nil || resolved.IP.To4() == nil || resolved.Port == 0 {
			return errors.New("invalid UDP probe address")
		}
	}
	if len(aad) != 0 && (address == "" || len(aad) != 8) {
		return errors.New("invalid UDP probe AAD")
	}
	if sendTest && address == "" {
		return errors.New("UDP test requires UDP probe address")
	}
	if rekeyDelay < 0 || rekeyDelay > 10*time.Second {
		return errors.New("invalid UDP rekey probe delay")
	}
	return nil
}

func (s *Server) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	status.PushMode = s.pushModeLocked()
	status.Capabilities = s.capabilitiesLocked()
	status.LocateUntil = s.locate.until
	if s.push.last != nil {
		last := *s.push.last
		status.Push = &last
	}
	if s.status.Verification != nil {
		verification := *s.status.Verification
		verification.Mismatches = append([]string(nil), verification.Mismatches...)
		verification.Notes = append([]string(nil), verification.Notes...)
		status.Verification = &verification
	}
	return status
}

// ID is the AP ID this session serves.
func (s *Server) ID() string { return s.auth.ID }

// Counters returns a snapshot of the AP's protocol counters (design §5.4).
func (s *Server) Counters() Counters { return s.counters.snapshot() }

// Features returns the protocol features switched on for this AP.
func (s *Server) Features() Features { return Features(s.features.Load()) }

// SetFeatures switches protocol features; the change applies from the
// next frame the AP sends, without a reconnect. Switching
// config_change_notify on again forgets a learned "reconnect" push mode,
// so the operator can retry notify pushes.
func (s *Server) SetFeatures(features Features) {
	previous := Features(s.features.Swap(uint32(features)))
	if previous == features {
		return
	}
	var learned bool
	s.mu.Lock()
	if !previous.Has(FeatureConfigChangeNotify) && features.Has(FeatureConfigChangeNotify) && s.learnedPushMode == PushModeReconnect {
		s.learnedPushMode, s.push.fallbacks, learned = "", 0, true
	}
	for _, feature := range allFeatures {
		if previous.Has(feature) != features.Has(feature) {
			state := "off"
			if features.Has(feature) {
				state = "on"
			}
			s.emitLocked(Event{Kind: EventFeature, Severity: SeverityInfo,
				Title: fmt.Sprintf("Protocol feature %s switched %s", feature, state)})
		}
	}
	s.mu.Unlock()
	s.flush()
	if learned && s.onPushMode != nil {
		s.onPushMode(s.auth.ID, "")
	}
}

// FeatureEnabled reports whether one protocol feature is on for this AP.
func (s *Server) FeatureEnabled(feature Feature) bool { return s.Features().Has(feature) }

// countSourceMismatch records a hello naming this AP from the wrong source.
func (s *Server) countSourceMismatch(remoteAddr string) {
	s.counters.sourceMismatches.Add(1)
	s.mu.Lock()
	s.emitLocked(Event{Kind: EventAuth, Severity: SeverityWarning,
		Title:  "AP connected from an address other than its pinned source",
		Detail: "from " + remoteHost(remoteAddr)})
	s.mu.Unlock()
	s.flush()
}

// AllowedManagementIP returns the AP's optional source address pin, or "".
func (s *Server) AllowedManagementIP() string {
	if s.allowedIP == nil {
		return ""
	}
	return s.allowedIP.String()
}

// acceptsSource reports whether a connection from remoteAddr may be this AP:
// always, unless the AP is pinned to a source address.
func (s *Server) acceptsSource(remoteAddr string) bool {
	if s.allowedIP == nil {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	return err == nil && net.ParseIP(host).Equal(s.allowedIP)
}

func (s *Server) ConfigSnapshot() ([]byte, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.config...), s.revision
}

func (s *Server) TelemetrySnapshot() TelemetrySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *Server) snapshotLocked() TelemetrySnapshot {
	result := s.stats
	result.VAPs = append([]telemetry.VAP(nil), s.stats.VAPs...)
	result.Radios = append([]telemetry.RadioStats(nil), s.stats.Radios...)
	result.Events = append([]ConnectionEvent(nil), s.stats.Events...)
	result.Interfaces = append([]telemetry.Interface(nil), s.stats.Interfaces...)
	result.Probes = append([]telemetry.Probe(nil), s.stats.Probes...)
	result.Ports = append([]telemetry.Port(nil), s.stats.Ports...)
	result.LLDP = append([]telemetry.LLDPNeighbor(nil), s.stats.LLDP...)
	result.LLDPPower = append([]telemetry.LLDPPower(nil), s.stats.LLDPPower...)
	result.InactiveVLANs = append([]int(nil), s.stats.InactiveVLANs...)
	result.LastKnownVAPs = append([]telemetry.VAP(nil), s.stats.LastKnownVAPs...)
	result.KMsg = append([]string(nil), s.stats.KMsg...)
	result.BLE = append([]telemetry.BLERadio(nil), s.stats.BLE...)
	result.RecentClients = make([]ClientActivity, 0, len(s.clients))
	for _, client := range s.clients {
		result.RecentClients = append(result.RecentClients, client)
	}
	sort.Slice(result.RecentClients, func(i, j int) bool {
		return result.RecentClients[i].LastSeen.After(result.RecentClients[j].LastSeen)
	})
	result.AssociatedClients = make([]telemetry.Client, 0, len(s.associated))
	for _, client := range s.associated {
		result.AssociatedClients = append(result.AssociatedClients, client)
	}
	sort.Slice(result.AssociatedClients, func(i, j int) bool {
		return result.AssociatedClients[i].MAC < result.AssociatedClients[j].MAC
	})
	return result
}

func (s *Server) SetConfig(config []byte, revision uint64) error {
	_, err := s.SetConfigIfNewer(config, revision)
	return err
}

// SetConfigIfNewer refuses an older committed revision that arrives after a
// newer site or AP mutation. Its return value tells callers whether the AP
// needs to fetch a changed config; Push runs the delivery lifecycle too.
func (s *Server) SetConfigIfNewer(config []byte, revision uint64) (bool, error) {
	if !json.Valid(config) {
		return false, errors.New("invalid AP config JSON")
	}
	var metadata struct {
		Timestamp float64 `json:"Timestamp"`
	}
	if err := json.Unmarshal(config, &metadata); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if revision < s.revision || (revision == s.revision && s.config != nil && bytes.Equal(config, s.config)) {
		return false, nil
	}
	s.config = append([]byte(nil), config...)
	s.configTS = metadata.Timestamp
	s.revision = revision
	s.status.ConfigSource = ConfigSourceCompiled
	s.status.CompileError = ""
	s.status.DesiredConfigTimestamp = metadata.Timestamp
	return true, nil
}

// Disconnect closes the AP's session; the AP reconnects and fetches its
// config again. The disconnect is recorded as a controller disconnect.
func (s *Server) Disconnect() { s.DisconnectBecause("controller disconnect") }

// DisconnectBecause closes the AP's session and records cause as the
// reason it ended (for example "config change").
func (s *Server) DisconnectBecause(cause string) {
	s.mu.Lock()
	active := s.active
	if active != nil {
		s.closing, s.closingCause = active, cause
	}
	s.mu.Unlock()
	if active != nil {
		_ = active.close()
	}
}

// ServeHTTP serves this AP alone. The Manager routes by AP ID instead; this
// entry point remains for single-AP use and tests.
func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/ws" {
		http.NotFound(writer, request)
		return
	}
	if request.TLS == nil || !s.acceptsSource(request.RemoteAddr) {
		http.Error(writer, "AP not registered", http.StatusForbidden)
		return
	}
	conn, err := apUpgrader().Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	s.serveConn(conn, request.TLS.CipherSuite, request.RemoteAddr, nil, nil)
}

// sessionEnd describes how one AP connection ended.
type sessionEnd struct {
	// authenticated is true once the connection passed authentication.
	authenticated bool
	// authFailure is AuthStageHello or AuthStageProof when authentication
	// failed.
	authFailure string
	// readErr is the read error that ended the session, if any.
	readErr error
	// subject is the subject of the last frame received.
	subject string
	// onAuthenticated is called once when the AP authenticates.
	onAuthenticated func()
}

// serveConn runs one AP connection. hello, when set, is an ep-auth frame the
// Manager already read to route the connection. The connection only becomes
// this AP's active session once it authenticates, so an unauthenticated
// client naming this AP cannot displace the real one. onAuthenticated, when
// set, is called once the AP has authenticated.
func (s *Server) serveConn(ws *websocket.Conn, cipherSuite uint16, remoteAddr string, hello []byte, onAuthenticated func()) sessionEnd {
	conn := &apConn{ws: ws}
	defer conn.close()
	ws.SetReadLimit(8 << 20)
	authenticator := s.auth
	authenticator.CipherSuite = cipherSuite
	end := sessionEnd{onAuthenticated: onAuthenticated}
	err := s.handle(conn, authenticator, remoteAddr, hello, &end)
	if err != nil {
		closeWithPolicyViolation(ws, "invalid AP message")
	}
	if end.authFailure != "" {
		s.counters.authFailure(end.authFailure)
		s.mu.Lock()
		s.emitLocked(Event{Kind: EventAuth, Severity: SeverityWarning,
			Title:  "AP authentication failed (" + end.authFailure + ")",
			Detail: "from " + remoteHost(remoteAddr)})
		s.mu.Unlock()
		s.flush()
	}
	s.mu.Lock()
	if s.active == conn {
		cause := describeEnd(end, err)
		if s.closing == conn {
			cause = s.closingCause
		}
		s.active = nil
		s.closing, s.closingCause = nil, ""
		s.status.Connected = false
		s.status.LastDisconnect = time.Now()
		s.status.LastDisconnectCause = cause
		s.counters.disconnects.Add(1)
		s.emitLocked(Event{Kind: EventConnection, Severity: SeverityInfo, Title: "AP disconnected", Detail: cause})
	} else if s.closing == conn {
		s.closing, s.closingCause = nil, ""
	}
	s.mu.Unlock()
	s.flush()
	return end
}

// describeEnd classifies why an authenticated session ended, for the
// disconnect event and Status.LastDisconnectCause.
func describeEnd(end sessionEnd, err error) string {
	if err != nil {
		// ErrAuthentication marks a frame the protocol does not allow; any
		// other error is a failed write or an internal failure.
		label := "session error"
		if errors.Is(err, ErrAuthentication) {
			label = "protocol error"
		}
		if end.subject != "" {
			return fmt.Sprintf("%s after %s: %s", label, truncateText(end.subject, 40), truncateText(err.Error(), 120))
		}
		return label + ": " + truncateText(err.Error(), 120)
	}
	readErr := end.readErr
	if readErr == nil {
		return "connection ended"
	}
	var closeErr *websocket.CloseError
	var netErr net.Error
	switch {
	case errors.As(readErr, &closeErr) && closeErr.Code == websocket.CloseAbnormalClosure:
		return "connection lost without a close frame"
	case errors.As(readErr, &closeErr):
		cause := fmt.Sprintf("closed by the AP (code %d", closeErr.Code)
		if text := cleanHelloString(closeErr.Text, 80); text != "" {
			cause += ": " + text
		}
		return cause + ")"
	case errors.Is(readErr, websocket.ErrReadLimit):
		return "frame over the read limit"
	case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
		return "connection lost without a close frame"
	case errors.Is(readErr, syscall.ECONNRESET):
		return "connection reset"
	case errors.As(readErr, &netErr) && netErr.Timeout():
		return "read timeout"
	case errors.Is(readErr, net.ErrClosed):
		return "connection closed by the controller"
	default:
		return "read error: " + truncateText(readErr.Error(), 120)
	}
}

func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func clockBody() []byte {
	var body [8]byte
	binary.BigEndian.PutUint64(body[:], uint64(time.Now().UnixMicro()))
	return body[:]
}

// handle runs the session's frame loop. The authentication states and the
// frames they accept are unchanged from phase 0 (design §5.4): the hello is
// decoded into Hello only, and everything new is recorded after
// authentication.
func (s *Server) handle(conn *apConn, authenticator Authenticator, remoteAddr string, pending []byte, end *sessionEnd) error {
	const (
		awaitHello = iota
		awaitClientProof
		authed
	)
	state := awaitHello
	var hello Hello
	var helloBody []byte
	var reply1 Reply1
	var authHandle uint64
	session := &sessionState{}
	for {
		var messageType int
		var raw []byte
		if pending != nil {
			messageType, raw, pending = websocket.BinaryMessage, pending, nil
		} else {
			var err error
			messageType, raw, err = conn.ws.ReadMessage()
			if err != nil {
				end.readErr = err
				if state == awaitClientProof {
					// The AP left after our proof instead of sending its
					// own: it holds a different secret (a real AP checks
					// our proof first), or it timed out.
					end.authFailure = AuthStageProof
				}
				return nil
			}
		}
		if messageType != websocket.BinaryMessage {
			return ErrAuthentication
		}
		message, err := protocol.Decode(raw)
		if err != nil {
			end.subject = ""
			return ErrAuthentication
		}
		end.subject = message.Subject
		if message.Version != 1 || message.ServiceClass > 2 {
			return ErrAuthentication
		}
		s.counters.frame(message.Subject)
		if state == authed {
			s.mu.Lock()
			s.status.LastSeen = time.Now()
			s.mu.Unlock()
		}
		if state == authed && s.onAPFrame != nil {
			copyMessage := message
			copyMessage.Body = append([]byte(nil), message.Body...)
			s.onAPFrame(copyMessage)
		}
		switch message.Subject {
		case "get-time":
			if message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if err := conn.reply(message, clockBody()); err != nil {
				return err
			}
		case "ep-auth":
			if message.ServiceClass != 2 {
				return ErrAuthentication
			}
			switch state {
			case awaitHello:
				if err := json.Unmarshal(message.Body, &hello); err != nil {
					end.authFailure = AuthStageHello
					return ErrAuthentication
				}
				helloBody = message.Body
				var qs [32]byte
				if _, err := rand.Read(qs[:]); err != nil {
					return err
				}
				reply1, err = authenticator.Reply1(hello, qs, uint64(time.Now().UnixNano()))
				if err != nil {
					end.authFailure = AuthStageHello
					return err
				}
				body, err := json.Marshal(reply1)
				if err != nil {
					return err
				}
				if err := conn.reply(message, body); err != nil {
					return err
				}
				authHandle = message.Handle
				state = awaitClientProof
			case awaitClientProof:
				if message.Handle != authHandle {
					end.authFailure = AuthStageProof
					return ErrAuthentication
				}
				var reply2 Reply2
				if err := json.Unmarshal(message.Body, &reply2); err != nil {
					end.authFailure = AuthStageProof
					return ErrAuthentication
				}
				reply3, err := authenticator.Reply3(hello, reply1, reply2)
				if err != nil {
					end.authFailure = AuthStageProof
					return err
				}
				body, err := json.Marshal(reply3)
				if err != nil {
					return err
				}
				if err := conn.reply(message, body); err != nil {
					return err
				}
				state = authed
				end.authenticated = true
				s.authenticated(conn, authenticator, hello, helloBody, remoteAddr)
				if end.onAuthenticated != nil {
					end.onAuthenticated()
				}
			default:
				return ErrAuthentication
			}
		case "ep-config":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if err := s.handleConfigRequest(conn, message, session); err != nil {
				return err
			}
		case "ping":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if err := conn.reply(message, message.Body); err != nil {
				return err
			}
		case "ping2":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if err := s.handlePing2(conn, message); err != nil {
				return err
			}
		case "udp-setup":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if s.udpProbeAddress == "" {
				continue
			}
			var request struct {
				Version   int   `json:"version"`
				Protocols []int `json:"Protocols"`
			}
			if err := json.Unmarshal(message.Body, &request); err != nil || request.Version != 1 {
				return ErrAuthentication
			}
			supportsV1 := false
			for _, number := range request.Protocols {
				if number == 1 {
					supportsV1 = true
				}
			}
			if !supportsV1 {
				return ErrAuthentication
			}
			aad := s.udpProbeAAD
			body, err := json.Marshal(struct {
				Version  int    `json:"version"`
				Address  string `json:"address"`
				Protocol int    `json:"Protocol"`
				AAD      []byte `json:"aad"`
			}{Version: 1, Address: s.udpProbeAddress, Protocol: 1, AAD: aad})
			if err != nil {
				return err
			}
			if err := conn.reply(message, body); err != nil {
				return err
			}
		case "udp-rekey":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if s.udpProbeAddress == "" {
				continue
			}
			var request struct {
				Version   int    `json:"version"`
				KeyNumber int    `json:"keynum"`
				Secrets   []byte `json:"secrets"`
			}
			if err := json.Unmarshal(message.Body, &request); err != nil ||
				request.Version != 2 || request.KeyNumber < 0 || len(request.Secrets) != 25 {
				return ErrAuthentication
			}
			if s.udpProbeRekeyDelay > 0 {
				time.Sleep(s.udpProbeRekeyDelay)
			}
			body, err := json.Marshal(struct {
				Version   int  `json:"version"`
				KeyNumber int  `json:"keynum"`
				OK        bool `json:"ok"`
			}{Version: 2, KeyNumber: request.KeyNumber, OK: true})
			if err != nil {
				return err
			}
			if err := conn.reply(message, body); err != nil {
				return err
			}
			if s.udpProbeSendTest {
				blob := make([]byte, 16)
				if _, err := rand.Read(blob); err != nil {
					return err
				}
				testBody, err := json.Marshal(struct {
					Version int    `json:"version"`
					Blob    []byte `json:"blob"`
					Reply   bool   `json:"reply"`
				}{Version: 3, Blob: blob, Reply: false})
				if err != nil {
					return err
				}
				if len(testBody) < 1428 {
					testBody = append(testBody, bytes.Repeat([]byte{' '}, 1428-len(testBody))...)
				}
				var handleBytes [4]byte
				if _, err := rand.Read(handleBytes[:]); err != nil {
					return err
				}
				if err := conn.writeMessage(protocol.Message{Version: 1, ServiceClass: 2,
					Subject: "udp-test", Handle: uint64(binary.BigEndian.Uint32(handleBytes[:]))}, testBody); err != nil {
					return err
				}
			}
		case "ep-telemetry":
			if state != authed {
				return ErrAuthentication
			}
			if s.onTelemetry != nil {
				s.onTelemetry(append([]byte(nil), message.Body...))
			}
			s.observeTelemetry(message.Body)
		case "ep-subscribe":
			if state != authed {
				return ErrAuthentication
			}
			if err := s.handleSubscribe(conn, message); err != nil {
				return err
			}
		case "ep-file":
			if state != authed {
				return ErrAuthentication
			}
			if err := s.handleFileOffer(conn, message); err != nil {
				return err
			}
		case "wlc-site-rest":
			if state != authed {
				return ErrAuthentication
			}
			if err := s.handleSiteRest(conn, message); err != nil {
				return err
			}
		case "ep-ip-register":
			if state != authed {
				return ErrAuthentication
			}
			s.handleIPRegister(message)
		default:
			if state != authed {
				return ErrAuthentication
			}
			s.noteOtherSubject(message.Subject)
		}
	}
}

// writeMessage sends a controller-initiated frame (not a reply).
func (c *apConn) writeMessage(message protocol.Message, body []byte) error {
	message.Body = body
	return c.write(message)
}

// authenticated makes conn the AP's active session after a successful
// handshake, then runs the tolerant hello pass (design §5.4): the extra
// hello fields land in Status, decode failures are counted, and the
// connection event says whether the AP rebooted or only reconnected.
func (s *Server) authenticated(conn *apConn, authenticator Authenticator, hello Hello, helloBody []byte, remoteAddr string) {
	extras, failed := decodeHelloExtras(helloBody)
	now := time.Now()
	extras.normaliseRebootTime(now)
	// The previous hello: this session object's own, or after a daemon
	// restart the one ap_status kept (design §5.4 "Reboot vs reconnect").
	s.mu.RLock()
	previousHello, previousAt := s.status.Hello, s.status.HelloAt
	s.mu.RUnlock()
	if s.sink != nil {
		stored, storedAt, err := s.sink.RecordHello(s.auth.ID, now, hello.Model, extras)
		if err != nil {
			log.Printf("AP %s: cannot record the hello: %v", s.auth.ID, err)
		} else if previousAt.IsZero() && stored != nil {
			previousHello, previousAt = *stored, storedAt
		}
	}
	rebooted, evidence := rebootEvidence(previousHello, previousAt, extras, now)
	s.mu.Lock()
	previous := s.active
	if previous != nil && previous != conn {
		// The AP opened a new session while the old one still looked open:
		// typical after an AP-side restart. Record the old one as ended.
		cause := "replaced by a new connection"
		if s.closing == previous {
			cause = s.closingCause
		}
		s.closing, s.closingCause = previous, cause
		s.status.LastDisconnect = now
		s.status.LastDisconnectCause = cause
		s.counters.disconnects.Add(1)
		s.emitLocked(Event{Kind: EventConnection, Severity: SeverityInfo, Title: "AP disconnected", Detail: cause})
	}
	s.active = conn
	s.status.Connected = true
	s.status.LastSeen = now
	s.status.RemoteAddress = remoteHost(remoteAddr)
	s.status.ConnectedAt = now
	s.status.ConnectionCount++
	s.status.TLSCipherSuite = tls.CipherSuiteName(authenticator.CipherSuite)
	s.status.Firmware = hello.Firmware
	s.status.Model = hello.Model
	s.status.LastError = ""
	s.status.Hello = extras
	s.status.HelloAt = now
	s.observe.newSession(extras.BootCount)
	detail := hello.Firmware
	if summary := extras.summary(); summary != "" {
		detail += "; " + summary
	}
	var reboot *Reboot
	switch {
	case rebooted:
		reboot = &Reboot{
			DetectedAt: now, Evidence: evidence, BootCount: extras.BootCount,
			PreviousBootCount: previousHello.BootCount, Uptime: extras.Uptime,
			LastRebootTime: extras.LastRebootTime, Reason: extras.LastRebootReason,
			BootState: extras.LastBootState,
		}
		s.recordRebootLocked(*reboot, detail)
	case !previousAt.IsZero():
		s.emitLocked(Event{Kind: EventConnection, Severity: SeverityInfo, Title: "AP reconnected", Detail: detail})
	default:
		s.emitLocked(Event{Kind: EventConnection, Severity: SeverityInfo, Title: "AP authenticated", Detail: detail})
	}
	s.mu.Unlock()
	s.flush()
	if reboot != nil && s.sink != nil {
		if err := s.sink.RecordReboot(s.auth.ID, *reboot); err != nil {
			log.Printf("AP %s: cannot record the reboot: %v", s.auth.ID, err)
		}
	}
	// Counted after Status holds the new hello, so a reader that sees the
	// connection counted also sees its hello.
	s.counters.decodeError("hello", len(failed))
	s.counters.connections.Add(1)
	if previous != nil && previous != conn {
		_ = previous.close()
	}
	logged := hello.Firmware
	if summary := extras.summary(); summary != "" {
		logged += "; " + summary
	}
	if rebooted {
		logged += "; rebooted since the last connection"
	}
	log.Printf("AP %s authenticated from %s (%s)", authenticator.ID, remoteHost(remoteAddr), logged)
	if len(failed) != 0 {
		log.Printf("AP %s hello: skipped fields with unexpected types: %s", authenticator.ID, strings.Join(failed, ", "))
	}
}

// recordRebootLocked notes a detected reboot: the counter, the status and
// a reboot event.
func (s *Server) recordRebootLocked(reboot Reboot, detail string) {
	s.counters.reboots.Add(1)
	s.status.LastRebootDetected = reboot.DetectedAt
	var why []string
	if reboot.Reason != "" {
		why = append(why, reboot.Reason)
	}
	if reboot.BootState != "" {
		why = append(why, reboot.BootState)
	}
	title := "AP rebooted"
	if len(why) != 0 {
		title += " (" + strings.Join(why, ", ") + ")"
	}
	data, _ := json.Marshal(reboot)
	s.emitLocked(Event{Kind: EventReboot, Severity: SeverityWarning, Title: title, Detail: detail, Data: data})
}

func isClientActivity(apID string, event telemetry.Event) bool {
	if event.ClientMAC == "" {
		return false
	}
	apMAC := strings.ToLower(strings.ReplaceAll(apID, "-", ":"))
	return !strings.EqualFold(event.ClientMAC, apMAC)
}

// writeReply answers a request on a raw connection (the Manager's routing
// phase, before a session exists).
func writeReply(conn *websocket.Conn, request protocol.Message, body []byte) error {
	frame, err := protocol.Encode(protocol.Message{
		Version: request.Version, ServiceClass: request.ServiceClass,
		Subject: request.Subject, Handle: request.Handle, Body: body,
	})
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func remoteHost(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
