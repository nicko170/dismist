package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/protocol"
	"github.com/nicko170/dismist/internal/telemetry"
)

type Options struct {
	AllowedIP          string
	ID                 string
	Secret             [32]byte
	CertDER            []byte
	Config             []byte
	ServerName         string
	Revision           uint64
	OnTelemetry        func([]byte)
	OnAPFrame          func(protocol.Message)
	UDPProbeAddress    string
	UDPProbeAAD        []byte
	UDPProbeSendTest   bool
	UDPProbeRekeyDelay time.Duration
}

type Server struct {
	allowedIP          net.IP
	auth               Authenticator
	mu                 sync.RWMutex
	config             []byte
	revision           uint64
	configTS           float64
	active             *websocket.Conn
	status             Status
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
	LastSeen        time.Time `json:"last_seen"`
	AppliedRevision uint64    `json:"applied_revision"`
	LastError       string    `json:"last_error,omitempty"`
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
	FullFrames        uint64             `json:"full_frames"`
	FastFrames        uint64             `json:"fast_frames"`
	LastFull          time.Time          `json:"last_full"`
	LastFast          time.Time          `json:"last_fast"`
	UptimeSeconds     uint64             `json:"uptime_seconds"`
	VAPs              []telemetry.VAP    `json:"vaps"`
	Radios            []telemetry.Radio  `json:"radios"`
	RecentClients     []ClientActivity   `json:"recent_clients"`
	AssociatedClients []telemetry.Client `json:"associated_clients"`
	Events            []ConnectionEvent  `json:"events"`
}

func NewServer(options Options) (*Server, error) {
	allowed := net.ParseIP(options.AllowedIP)
	if allowed == nil || options.ID == "" || len(options.CertDER) == 0 || !json.Valid(options.Config) {
		return nil, errors.New("invalid controller options")
	}
	if options.UDPProbeAddress != "" {
		address, err := net.ResolveUDPAddr("udp4", options.UDPProbeAddress)
		if err != nil || address.IP.To4() == nil || address.Port == 0 {
			return nil, errors.New("invalid UDP probe address")
		}
	}
	if len(options.UDPProbeAAD) != 0 && (options.UDPProbeAddress == "" || len(options.UDPProbeAAD) != 8) {
		return nil, errors.New("invalid UDP probe AAD")
	}
	if options.UDPProbeSendTest && options.UDPProbeAddress == "" {
		return nil, errors.New("UDP test requires UDP probe address")
	}
	if options.UDPProbeRekeyDelay < 0 || options.UDPProbeRekeyDelay > 10*time.Second {
		return nil, errors.New("invalid UDP rekey probe delay")
	}
	probeAAD := options.UDPProbeAAD
	if len(probeAAD) == 0 {
		probeAAD = []byte("Dismist!")
	}
	var metadata struct {
		Timestamp float64 `json:"Timestamp"`
	}
	_ = json.Unmarshal(options.Config, &metadata)
	return &Server{
		allowedIP: allowed,
		auth: Authenticator{
			ID: options.ID, Secret: options.Secret,
			CertDER: append([]byte(nil), options.CertDER...), ServerName: options.ServerName,
		},
		config:             append([]byte(nil), options.Config...),
		revision:           options.Revision,
		configTS:           metadata.Timestamp,
		clients:            make(map[string]ClientActivity),
		associated:         make(map[string]telemetry.Client),
		onTelemetry:        options.OnTelemetry,
		onAPFrame:          options.OnAPFrame,
		udpProbeAddress:    options.UDPProbeAddress,
		udpProbeAAD:        append([]byte(nil), probeAAD...),
		udpProbeSendTest:   options.UDPProbeSendTest,
		udpProbeRekeyDelay: options.UDPProbeRekeyDelay,
	}, nil
}

func (s *Server) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// AllowedManagementIP returns the registered AP address without exposing its credential.
func (s *Server) AllowedManagementIP() string {
	return s.allowedIP.String()
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
	if revision < s.revision || (revision == s.revision && bytes.Equal(config, s.config)) {
		return false, nil
	}
	s.config = append([]byte(nil), config...)
	s.configTS = metadata.Timestamp
	s.revision = revision
	return true, nil
}

func (s *Server) Disconnect() {
	s.mu.RLock()
	active := s.active
	s.mu.RUnlock()
	if active != nil {
		_ = active.Close()
	}
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/ws" {
		http.NotFound(writer, request)
		return
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || !net.ParseIP(host).Equal(s.allowedIP) || request.TLS == nil {
		http.Error(writer, "AP not registered", http.StatusForbidden)
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize: 4096, WriteBufferSize: 4096,
		Subprotocols: []string{"mist-ep-v0"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	s.mu.Lock()
	previous := s.active
	s.active = conn
	s.mu.Unlock()
	if previous != nil && previous != conn {
		_ = previous.Close()
	}
	defer func() {
		s.mu.Lock()
		if s.active == conn {
			s.active = nil
			s.status.Connected = false
			s.status.LastDisconnect = time.Now()
			s.addEventLocked(ConnectionEvent{When: time.Now(), Kind: "connection", Title: "AP disconnected"})
		}
		s.mu.Unlock()
	}()
	conn.SetReadLimit(8 << 20)
	authenticator := s.auth
	authenticator.CipherSuite = request.TLS.CipherSuite
	if err := s.handle(conn, authenticator); err != nil {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid AP message"),
			time.Now().Add(time.Second))
	}
}

func (s *Server) handle(conn *websocket.Conn, authenticator Authenticator) error {
	const (
		awaitHello = iota
		awaitClientProof
		authed
	)
	state := awaitHello
	var hello Hello
	var reply1 Reply1
	var authHandle uint64
	for {
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			return nil
		}
		if messageType != websocket.BinaryMessage {
			return ErrAuthentication
		}
		message, err := protocol.Decode(raw)
		if err != nil || message.Version != 1 || message.ServiceClass > 2 {
			return ErrAuthentication
		}
		s.mu.Lock()
		s.status.LastSeen = time.Now()
		s.mu.Unlock()
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
			var body [8]byte
			binary.BigEndian.PutUint64(body[:], uint64(time.Now().UnixMicro()))
			if err := writeReply(conn, message, body[:]); err != nil {
				return err
			}
		case "ep-auth":
			if message.ServiceClass != 2 {
				return ErrAuthentication
			}
			switch state {
			case awaitHello:
				if err := json.Unmarshal(message.Body, &hello); err != nil {
					return ErrAuthentication
				}
				var qs [32]byte
				if _, err := rand.Read(qs[:]); err != nil {
					return err
				}
				reply1, err = authenticator.Reply1(hello, qs, uint64(time.Now().UnixNano()))
				if err != nil {
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
					return ErrAuthentication
				}
				var reply2 Reply2
				if err := json.Unmarshal(message.Body, &reply2); err != nil {
					return ErrAuthentication
				}
				reply3, err := authenticator.Reply3(hello, reply1, reply2)
				if err != nil {
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
				s.mu.Lock()
				s.status.Connected = true
				s.status.ConnectedAt = time.Now()
				s.status.ConnectionCount++
				s.status.TLSCipherSuite = tls.CipherSuiteName(authenticator.CipherSuite)
				s.status.Firmware = hello.Firmware
				s.status.LastError = ""
				s.addEventLocked(ConnectionEvent{When: time.Now(), Kind: "connection", Title: "AP authenticated", Detail: hello.Firmware})
				s.mu.Unlock()
			default:
				return ErrAuthentication
			}
		case "ep-config":
			if state != authed || message.ServiceClass != 2 {
				return ErrAuthentication
			}
			s.mu.RLock()
			config := append([]byte(nil), s.config...)
			s.mu.RUnlock()
			if err := writeReply(conn, message, config); err != nil {
				return err
			}
			s.mu.Lock()
			s.addEventLocked(ConnectionEvent{When: time.Now(), Kind: "config", Title: "Configuration delivered"})
			s.mu.Unlock()
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
