package controller

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/protocol"
)

// sessionState is per-connection state of the frame loop.
type sessionState struct {
	configUnavailableNoted bool
}

// Reply bodies (design §5.7). Each is sent only while its protocol
// feature is on for the AP.
var (
	// subscribeReply is the cloud's captured ep-subscribe reply shape.
	subscribeReply = []byte(`{"Partition":0,"Err":""}`)
	// fileDiscardReply is msgs.FileUploadReply refusing the upload.
	fileDiscardReply = []byte(`{"SendIt":false,"Discard":true,"ErrorMsg":""}`)
	// siteRest404Reply answers wlc-site-rest at once instead of letting the
	// AP time out after 10 s (the shape of the cloud's captured reply).
	siteRest404Reply = []byte(`{"Error":"","StatusCode":404,"Headers":{"ContentType":""},"Body":""}`)
	// notModifiedReply answers an ep-config request for the config the AP
	// already runs (phase 4, experimental, design §5.6).
	notModifiedReply = []byte(`{"NotModified":true}`)
)

// handleConfigRequest answers ep-config with the AP's config (or, with
// not_modified, NotModified), then records the request fields (design
// §5.5) and drives the push lifecycle and the post-delivery verification.
func (s *Server) handleConfigRequest(conn *apConn, message protocol.Message, session *sessionState) error {
	request := parseConfigRequest(message.Body, time.Now())
	s.mu.RLock()
	config := append([]byte(nil), s.config...)
	revision, configTS := s.revision, s.configTS
	hasConfig := s.config != nil
	compileError := s.status.CompileError
	s.mu.RUnlock()
	if !hasConfig {
		// The AP's config failed to compile and it was never delivered
		// one: answer nothing rather than a wrong config.
		s.counters.configUnavailable.Add(1)
		if !session.configUnavailableNoted {
			session.configUnavailableNoted = true
			s.mu.Lock()
			s.emitLocked(Event{Kind: EventConfig, Severity: SeverityError, Title: "Configuration unavailable", Detail: compileError})
			s.mu.Unlock()
			s.flush()
		}
		s.recordConfigRequest(request)
		return nil
	}
	notModified := s.FeatureEnabled(FeatureNotModified) && configTS > 0 &&
		math.Abs(request.AppliedConfigTimestamp-configTS) < 0.000001
	if notModified {
		if err := conn.reply(message, notModifiedReply); err != nil {
			return err
		}
		s.counters.reply(FeatureNotModified)
		s.mu.Lock()
		s.emitLocked(Event{Kind: EventConfig, Severity: SeverityInfo,
			Title: fmt.Sprintf("Configuration not modified (the AP runs revision %d)", revision)})
		s.mu.Unlock()
		s.flush()
		s.recordConfigRequest(request)
		s.pushFetched()
		return nil
	}
	if err := conn.reply(message, config); err != nil {
		return err
	}
	deliveredAt := time.Now()
	s.counters.configDeliveries.Add(1)
	s.mu.Lock()
	s.emitLocked(Event{Kind: EventConfig, Severity: SeverityInfo, Title: "Configuration delivered",
		Detail: fmt.Sprintf("revision %d", revision)})
	s.mu.Unlock()
	s.flush()
	delivery := ConfigDelivery{APID: s.auth.ID, Config: config, Revision: revision, Timestamp: configTS, At: deliveredAt}
	if s.onConfigDelivered != nil {
		s.onConfigDelivered(delivery)
	}
	s.recordConfigRequest(request)
	s.pushFetched()
	s.verifyDelivered(delivery)
	return nil
}

// parseConfigRequest reads msgs.ConfigRequest field by field, so a field
// with an unexpected type is skipped. Capabilities are the Tags, sorted.
func parseConfigRequest(body []byte, at time.Time) ConfigRequest {
	request := ConfigRequest{At: at}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return request
	}
	text := func(name string, limit int) string {
		var value string
		if raw, ok := lookupField(fields, name); ok && json.Unmarshal(raw, &value) == nil {
			return cleanHelloString(value, limit)
		}
		return ""
	}
	request.Firmware = text("Firmware", 96)
	request.MfgCountryCode = text("MfgCountryCode", 8)
	request.Accept = text("Accept", 64)
	request.AuditID = text("AuditID", 64)
	request.CLMVersion = text("RunningCLMVersion", 64)
	if raw, ok := lookupField(fields, "RecoveredFromBadConfig"); ok {
		_ = json.Unmarshal(raw, &request.RecoveredFromBadConfig)
	}
	if raw, ok := lookupField(fields, "AppliedConfigTimestamp"); ok {
		var value float64
		if json.Unmarshal(raw, &value) == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
			request.AppliedConfigTimestamp = value
		}
	}
	if raw, ok := lookupField(fields, "Timeout_sec"); ok {
		_ = json.Unmarshal(raw, &request.TimeoutSec)
	}
	seen := map[string]bool{}
	for _, tag := range strings.Split(text("Tags", 512), ",") {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" && !seen[tag] && len(seen) < 32 {
			seen[tag] = true
			request.Capabilities = append(request.Capabilities, tag)
		}
	}
	sort.Strings(request.Capabilities)
	return request
}

// recordConfigRequest stores the request in the status and ap_status, and
// raises a warning when the AP says it rolled back from a bad config.
func (s *Server) recordConfigRequest(request ConfigRequest) {
	s.mu.Lock()
	recovered := request.RecoveredFromBadConfig && !s.status.ConfigRequest.RecoveredFromBadConfig
	s.status.ConfigRequest = request
	if recovered {
		s.emitLocked(Event{Kind: EventConfig, Severity: SeverityWarning,
			Title:  "AP recovered from a bad configuration",
			Detail: fmt.Sprintf("its ep-config request says it rolled back; it runs Timestamp %.6f", request.AppliedConfigTimestamp)})
	}
	s.mu.Unlock()
	s.flush()
	if s.sink != nil {
		if err := s.sink.SetConfigRequest(s.auth.ID, request); err != nil {
			log.Printf("AP %s: cannot record the ep-config request: %v", s.auth.ID, err)
		}
	}
}

// handlePing2 answers ping2. With pong2 the reply is protobuf msgs.Pong2
// {1 PingSent (the AP's Ping2.Sent), 2 Recvd (controller µs)}, which
// 0.15 uses for CloudStats.WebSockRTT; otherwise the body is echoed as in
// phase 0.
func (s *Server) handlePing2(conn *apConn, message protocol.Message) error {
	if !s.FeatureEnabled(FeaturePong2) {
		return conn.reply(message, message.Body)
	}
	sent := ping2Sent(message.Body)
	var body []byte
	body = binary.AppendUvarint(body, 1<<3)
	body = binary.AppendUvarint(body, uint64(sent))
	body = binary.AppendUvarint(body, 2<<3)
	body = binary.AppendUvarint(body, uint64(time.Now().UnixMicro()))
	if err := conn.reply(message, body); err != nil {
		return err
	}
	s.counters.reply(FeaturePong2)
	return nil
}

// ping2Sent reads msgs.Ping2 {1 Sent int64}; 0 when absent or malformed.
func ping2Sent(body []byte) int64 {
	for len(body) > 0 {
		key, n := binary.Uvarint(body)
		if n <= 0 {
			return 0
		}
		body = body[n:]
		switch key & 7 {
		case 0:
			value, n := binary.Uvarint(body)
			if n <= 0 {
				return 0
			}
			body = body[n:]
			if key>>3 == 1 {
				return int64(value)
			}
		case 1:
			if len(body) < 8 {
				return 0
			}
			body = body[8:]
		case 2:
			length, n := binary.Uvarint(body)
			if n <= 0 || length > uint64(len(body)-n) {
				return 0
			}
			body = body[n+int(length):]
		case 5:
			if len(body) < 4 {
				return 0
			}
			body = body[4:]
		default:
			return 0
		}
	}
	return 0
}

// handleSubscribe answers ep-subscribe with the cloud's reply when
// subscribe_reply is on; otherwise it is dropped and the AP retries every
// minute, as in phase 0.
func (s *Server) handleSubscribe(conn *apConn, message protocol.Message) error {
	if message.ServiceClass != 2 || !s.FeatureEnabled(FeatureSubscribeReply) {
		return nil
	}
	if err := conn.reply(message, subscribeReply); err != nil {
		return err
	}
	s.counters.reply(FeatureSubscribeReply)
	return nil
}

// handleFileOffer records an ep-file offer (msgs.FileUploadOffer) and,
// with file_discard, tells the AP to discard the file.
func (s *Server) handleFileOffer(conn *apConn, message protocol.Message) error {
	s.counters.fileOffers.Add(1)
	offer := FileOffer{At: time.Now()}
	var fields struct {
		Name string
		Size int64
		Type string
	}
	if json.Unmarshal(message.Body, &fields) == nil {
		offer.Name = cleanHelloString(fields.Name, 128)
		offer.Size = max(fields.Size, 0)
		offer.Type = cleanHelloString(fields.Type, 32)
	}
	discard := message.ServiceClass == 2 && s.FeatureEnabled(FeatureFileDiscard)
	if discard {
		if err := conn.reply(message, fileDiscardReply); err != nil {
			return err
		}
		s.counters.reply(FeatureFileDiscard)
		offer.Discarded = true
	}
	name := offer.Name
	if name == "" {
		name = "a file"
	}
	s.mu.Lock()
	s.status.LastFileOffer = &offer
	s.emitLocked(Event{Kind: EventFile, Severity: SeverityInfo,
		Title:  fmt.Sprintf("AP offered crash report %s (%d bytes)", name, offer.Size),
		Detail: map[bool]string{true: "discarded", false: "left unanswered"}[discard]})
	s.mu.Unlock()
	s.flush()
	return nil
}

// handleSiteRest answers wlc-site-rest (the AP's site bloom-filter GET
// every 5 minutes) with a 404 at once when site_rest_404 is on.
func (s *Server) handleSiteRest(conn *apConn, message protocol.Message) error {
	if message.ServiceClass != 2 || !s.FeatureEnabled(FeatureSiteRest404) {
		return nil
	}
	if err := conn.reply(message, siteRest404Reply); err != nil {
		return err
	}
	s.counters.reply(FeatureSiteRest404)
	return nil
}

// handleIPRegister records the AP's tunnel IPv6 (16 raw bytes). Nothing
// is sent back (the cloud sends nothing either).
func (s *Server) handleIPRegister(message protocol.Message) {
	if len(message.Body) != 16 {
		s.counters.decodeError("ip_register", 1)
		return
	}
	address := netip.AddrFrom16([16]byte(message.Body)).String()
	now := time.Now()
	s.counters.ipRegistrations.Add(1)
	s.mu.Lock()
	changed := s.status.TunnelIPv6 != address
	s.status.TunnelIPv6, s.status.TunnelIPv6At = address, now
	s.mu.Unlock()
	if changed && s.sink != nil {
		if err := s.sink.SetTunnelIPv6(s.auth.ID, now, address); err != nil {
			log.Printf("AP %s: cannot record the tunnel address: %v", s.auth.ID, err)
		}
	}
}

// otherSubjectLogInterval rate-limits the log line for a subject dismist
// does not handle (once per subject per AP per hour).
const otherSubjectLogInterval = time.Hour

// noteOtherSubject logs a subject dismist does not handle, at most once
// per subject per hour. ip6 (IPv6 over the session) and the AP's answers
// to frames dismist sends are expected and not logged.
func (s *Server) noteOtherSubject(subject string) {
	switch subject {
	case "ip6", "ep-config-change", "ap-cmd-rx", "pong", "pong2":
		return
	}
	key := cleanHelloString(subject, 64)
	now := time.Now()
	s.mu.Lock()
	last, seen := s.otherSubjects[key]
	if seen && now.Sub(last) < otherSubjectLogInterval {
		s.mu.Unlock()
		return
	}
	if len(s.otherSubjects) > 256 {
		s.otherSubjects = make(map[string]time.Time)
	}
	s.otherSubjects[key] = now
	s.mu.Unlock()
	log.Printf("AP %s sent unhandled subject %q (dropped)", s.auth.ID, key)
}
