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
	"math"
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
	OnConfigDelivered  func(ConfigDelivery)
	OnTelemetry        func([]byte)
	OnAPFrame          func(protocol.Message)
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

type Server struct {
	allowedIP          net.IP
	auth               Authenticator
	mu                 sync.RWMutex
	config             []byte
	revision           uint64
	configTS           float64
	active             *websocket.Conn
	closing            *websocket.Conn
	closingCause       string
	status             Status
	counters           counters
	features           atomic.Uint32
	onConfigDelivered  func(ConfigDelivery)
	stats              TelemetrySnapshot
	clients            map[string]ClientActivity
	associated         map[string]telemetry.Client
	onTelemetry        func([]byte)
	onAPFrame          func(protocol.Message)
	udpProbeAddress    string
	udpProbeAAD        []byte
	udpProbeSendTest   bool
	udpProbeRekeyDelay time.Duration
}

type Status struct {
	Connected       bool      `json:"connected"`
	ConnectedAt     time.Time `json:"connected_at"`
	ConnectionCount uint64    `json:"connection_count"`
	TLSCipherSuite  string    `json:"tls_cipher_suite"`
	LastDisconnect  time.Time `json:"last_disconnect"`
	Firmware        string    `json:"firmware"`
	Model           string    `json:"model,omitempty"`
	LastSeen        time.Time `json:"last_seen"`
	AppliedRevision uint64    `json:"applied_revision"`
	LastError       string    `json:"last_error,omitempty"`
	RemoteAddress   string    `json:"remote_address,omitempty"`
	// Hello holds the extra fields of the last authenticated hello, from
	// the tolerant pass (design §5.4); HelloAt is when it arrived.
	Hello   HelloExtras `json:"hello,omitzero"`
	HelloAt time.Time   `json:"hello_at,omitzero"`
	// LastDisconnectCause says why the last authenticated session ended
	// (controller disconnect, closed by the AP, read error, replaced by a
	// new connection, protocol error).
	LastDisconnectCause string `json:"last_disconnect_cause,omitempty"`
	// ConfigSource is ConfigSourceCompiled, ConfigSourceDelivered or
	// ConfigSourceNone.
	ConfigSource string `json:"config_source,omitempty"`
	// CompileError is the compile failure behind a delivered or absent
	// config; cleared when a compiled config is set.
	CompileError string `json:"compile_error,omitempty"`
}

type ClientActivity struct {
	MAC       string    `json:"mac"`
	IP        string    `json:"ip,omitempty"`
	LastEvent string    `json:"last_event"`
	LastSeen  time.Time `json:"last_seen"`
}

type ConnectionEvent struct {
	When      time.Time `json:"when"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	ClientMAC string    `json:"client_mac,omitempty"`
}

type TelemetrySnapshot struct {
	FullFrames        uint64                `json:"full_frames"`
	FastFrames        uint64                `json:"fast_frames"`
	LastFull          time.Time             `json:"last_full"`
	LastFast          time.Time             `json:"last_fast"`
	UptimeSeconds     uint64                `json:"uptime_seconds"`
	VAPs              []telemetry.VAP       `json:"vaps"`
	Radios            []telemetry.Radio     `json:"radios"`
	RecentClients     []ClientActivity      `json:"recent_clients"`
	AssociatedClients []telemetry.Client    `json:"associated_clients"`
	Interfaces        []telemetry.Interface `json:"interfaces"`
	Events            []ConnectionEvent     `json:"events"`
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
		onConfigDelivered:  options.OnConfigDelivered,
		onTelemetry:        options.OnTelemetry,
		onAPFrame:          options.OnAPFrame,
		udpProbeAddress:    options.UDPProbeAddress,
		udpProbeAAD:        append([]byte(nil), probeAAD...),
		udpProbeSendTest:   options.UDPProbeSendTest,
		udpProbeRekeyDelay: options.UDPProbeRekeyDelay,
	}
	server.status.ConfigSource = source
	server.features.Store(uint32(options.Features))
	if options.CompileError != "" {
		server.status.CompileError = truncateText(options.CompileError, 500)
		server.counters.compileErrors.Add(1)
		title := "Configuration compile failed; serving the last delivered configuration"
		if source == ConfigSourceNone {
			title = "Configuration compile failed; no configuration to serve"
		}
		server.addEventLocked(ConnectionEvent{When: time.Now(), Kind: "config", Title: title, Detail: server.status.CompileError})
	}
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
	return s.status
}

// ID is the AP ID this session serves.
func (s *Server) ID() string { return s.auth.ID }

// Counters returns a snapshot of the AP's protocol counters (design §5.4).
func (s *Server) Counters() Counters { return s.counters.snapshot() }

// Features returns the protocol features switched on for this AP.
func (s *Server) Features() Features { return Features(s.features.Load()) }

// SetFeatures switches protocol features; the change applies from the
// next frame the AP sends, without a reconnect.
func (s *Server) SetFeatures(features Features) { s.features.Store(uint32(features)) }

// FeatureEnabled reports whether one protocol feature is on for this AP.
func (s *Server) FeatureEnabled(feature Feature) bool { return s.Features().Has(feature) }

// countSourceMismatch records a hello naming this AP from the wrong source.
func (s *Server) countSourceMismatch() { s.counters.sourceMismatches.Add(1) }

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
	result := s.stats
	result.VAPs = append([]telemetry.VAP(nil), s.stats.VAPs...)
	result.Radios = append([]telemetry.Radio(nil), s.stats.Radios...)
	result.Events = append([]ConnectionEvent(nil), s.stats.Events...)
	result.Interfaces = append([]telemetry.Interface(nil), s.stats.Interfaces...)
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
// needs to reconnect to fetch a changed config.
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
		_ = active.Close()
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
func (s *Server) serveConn(conn *websocket.Conn, cipherSuite uint16, remoteAddr string, hello []byte, onAuthenticated func()) sessionEnd {
	defer conn.Close()
	conn.SetReadLimit(8 << 20)
	authenticator := s.auth
	authenticator.CipherSuite = cipherSuite
	end := sessionEnd{onAuthenticated: onAuthenticated}
	err := s.handle(conn, authenticator, remoteAddr, hello, &end)
	if err != nil {
		closeWithPolicyViolation(conn, "invalid AP message")
	}
	if end.authFailure != "" {
		s.counters.authFailure(end.authFailure)
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
		s.addEventLocked(ConnectionEvent{When: time.Now(), Kind: "connection", Title: "AP disconnected", Detail: cause})
	} else if s.closing == conn {
		s.closing, s.closingCause = nil, ""
	}
	s.mu.Unlock()
	return end
}

// describeEnd classifies why an authenticated session ended, for the
// disconnect event and Status.LastDisconnectCause.
func describeEnd(end sessionEnd, err error) string {
	if err != nil {
		if end.subject != "" {
			return fmt.Sprintf("protocol error after %s: %s", truncateText(end.subject, 40), truncateText(err.Error(), 120))
		}
		return "protocol error: " + truncateText(err.Error(), 120)
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

func (s *Server) handle(conn *websocket.Conn, authenticator Authenticator, remoteAddr string, pending []byte, end *sessionEnd) error {
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
	configUnavailableNoted := false
	for {
		var messageType int
		var raw []byte
		if pending != nil {
			messageType, raw, pending = websocket.BinaryMessage, pending, nil
		} else {
			var err error
			messageType, raw, err = conn.ReadMessage()
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
		if err != nil || message.Version != 1 || message.ServiceClass > 2 {
			return ErrAuthentication
		}
		s.counters.frame(message.Subject)
		end.subject = message.Subject
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
			if err := writeReply(conn, message, clockBody()); err != nil {
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
				if err := writeReply(conn, message, body); err != nil {
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
				if err := writeReply(conn, message, body); err != nil {
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
			s.mu.RLock()
			config := append([]byte(nil), s.config...)
			revision, configTS := s.revision, s.configTS
			hasConfig := s.config != nil
			compileError := s.status.CompileError
			s.mu.RUnlock()
			if !hasConfig {
				// The AP's config failed to compile and it was never
				// delivered one: answer nothing rather than a wrong config.
				s.counters.configUnavailable.Add(1)
				if !configUnavailableNoted {
					configUnavailableNoted = true
					s.mu.Lock()
					s.addEventLocked(ConnectionEvent{When: time.Now(), Kind: "config", Title: "Configuration unavailable", Detail: compileError})
					s.mu.Unlock()
				}
				continue
			}
			if err := writeReply(conn, message, config); err != nil {
				return err
			}
			deliveredAt := time.Now()
			s.counters.configDeliveries.Add(1)
			s.mu.Lock()
			s.addEventLocked(ConnectionEvent{When: deliveredAt, Kind: "config", Title: "Configuration delivered"})
			s.mu.Unlock()
			if s.onConfigDelivered != nil {
				s.onConfigDelivered(ConfigDelivery{
					APID: s.auth.ID, Config: config, Revision: revision, Timestamp: configTS, At: deliveredAt,
				})
			}
		case "ping", "ping2":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			if err := writeReply(conn, message, message.Body); err != nil {
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
			if err := writeReply(conn, message, body); err != nil {
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
			if err := writeReply(conn, message, body); err != nil {
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
				if err := writeReply(conn, protocol.Message{Version: 1, ServiceClass: 2,
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
		default:
			if state != authed {
				return ErrAuthentication
			}
		}
	}
}

func (s *Server) observeTelemetry(body []byte) {
	update, err := telemetry.Decode(body)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	switch update.Topic {
	case "ap-stats-full-":
		s.stats.FullFrames++
		s.stats.LastFull = now
		s.stats.VAPs = append([]telemetry.VAP(nil), update.VAPs...)
		s.stats.Radios = append([]telemetry.Radio(nil), update.Radios...)
		s.stats.UptimeSeconds = update.UptimeSeconds
		if len(update.Interfaces) != 0 {
			s.stats.Interfaces = append([]telemetry.Interface(nil), update.Interfaces...)
		}
		s.associated = mergeAssociated(s.associated, update.Clients)
	case "ap-stats-fast-":
		s.stats.FastFrames++
		s.stats.LastFast = now
		if update.UptimeSeconds != 0 {
			s.stats.UptimeSeconds = update.UptimeSeconds
		}
		s.associated = mergeAssociated(s.associated, update.Clients)
	case "ep-config-applied-":
		if update.Error != "" {
			s.counters.configRejections.Add(1)
			s.status.LastError = update.Error
			s.addEventLocked(ConnectionEvent{When: now, Kind: "config", Title: "Configuration rejected", Detail: update.Error})
		} else if s.configTS > 0 && math.Abs(update.ConfigTimestamp-s.configTS) < 0.000001 {
			if s.status.AppliedRevision != s.revision {
				s.status.AppliedRevision = s.revision
				s.addEventLocked(ConnectionEvent{When: now, Kind: "config", Title: "Configuration applied"})
			}
			s.status.LastError = ""
		}
	case "ep-pace-event-", "client-pace-event-":
		if update.Event == nil {
			return
		}
		event := update.Event
		when := event.When
		if when.IsZero() {
			when = now
		}
		s.addEventLocked(ConnectionEvent{
			When: when, Kind: "ap_event", Title: event.Title,
			Detail: event.Text, ClientMAC: event.ClientMAC,
		})
		if isClientActivity(s.auth.ID, *event) {
			s.clients[event.ClientMAC] = ClientActivity{
				MAC: event.ClientMAC, IP: event.ClientIP,
				LastEvent: event.Title, LastSeen: when,
			}
		}
	}
}

// authenticated makes conn the AP's active session after a successful
// handshake, then runs the tolerant hello pass (design §5.4): the extra
// hello fields land in Status, decode failures are counted, and the
// connection event says whether the AP rebooted or only reconnected.
func (s *Server) authenticated(conn *websocket.Conn, authenticator Authenticator, hello Hello, helloBody []byte, remoteAddr string) {
	extras, failed := decodeHelloExtras(helloBody)
	now := time.Now()
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
		s.addEventLocked(ConnectionEvent{When: now, Kind: "connection", Title: "AP disconnected", Detail: cause})
	}
	previousHello, previousAt := s.status.Hello, s.status.HelloAt
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
	detail := hello.Firmware
	if summary := extras.summary(); summary != "" {
		detail += "; " + summary
	}
	rebooted, _ := rebootDetected(previousHello, previousAt, extras, now)
	switch {
	case rebooted:
		var why []string
		if extras.LastRebootReason != "" {
			why = append(why, extras.LastRebootReason)
		}
		if extras.LastBootState != "" {
			why = append(why, extras.LastBootState)
		}
		title := "AP rebooted"
		if len(why) != 0 {
			title += " (" + strings.Join(why, ", ") + ")"
		}
		s.addEventLocked(ConnectionEvent{When: now, Kind: "reboot", Title: title, Detail: detail})
	case !previousAt.IsZero():
		s.addEventLocked(ConnectionEvent{When: now, Kind: "connection", Title: "AP reconnected", Detail: detail})
	default:
		s.addEventLocked(ConnectionEvent{When: now, Kind: "connection", Title: "AP authenticated", Detail: detail})
	}
	s.mu.Unlock()
	// Counted after Status holds the new hello, so a reader that sees the
	// connection counted also sees its hello.
	s.counters.decodeError("hello", len(failed))
	s.counters.connections.Add(1)
	if previous != nil && previous != conn {
		_ = previous.Close()
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

func mergeAssociated(previous map[string]telemetry.Client, incoming []telemetry.Client) map[string]telemetry.Client {
	next := make(map[string]telemetry.Client, len(incoming))
	for _, client := range incoming {
		if client.MAC == "" {
			continue
		}
		if old, ok := previous[client.MAC]; ok {
			if client.Name == "" {
				client.Name = old.Name
			}
			if client.Auth == "" {
				client.Auth = old.Auth
			}
			if client.Cipher == "" {
				client.Cipher = old.Cipher
			}
			if client.BSSID == "" {
				client.BSSID = old.BSSID
			}
			if client.SSID == "" {
				client.SSID = old.SSID
			}
			if client.IP == "" {
				client.IP = old.IP
			}
		}
		next[client.MAC] = client
	}
	return next
}

func isClientActivity(apID string, event telemetry.Event) bool {
	if event.ClientMAC == "" {
		return false
	}
	apMAC := strings.ToLower(strings.ReplaceAll(apID, "-", ":"))
	return !strings.EqualFold(event.ClientMAC, apMAC)
}

func (s *Server) addEventLocked(event ConnectionEvent) {
	s.stats.Events = append(s.stats.Events, event)
	if len(s.stats.Events) > 100 {
		s.stats.Events = append([]ConnectionEvent(nil), s.stats.Events[len(s.stats.Events)-100:]...)
	}
}

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
