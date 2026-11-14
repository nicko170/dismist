package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/protocol"
	"github.com/nicko170/dismist/internal/telemetry"
)

// Push modes (design §5.6, Status.PushMode, APRecord.PushMode).
const (
	PushModeNotify    = "notify"
	PushModeReconnect = "reconnect"
)

// ParsePushMode parses the -config-push flag.
func ParsePushMode(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", PushModeReconnect:
		return PushModeReconnect, nil
	case PushModeNotify:
		return PushModeNotify, nil
	}
	return "", fmt.Errorf("unknown config push mode %q (notify or reconnect)", value)
}

// Push lifecycle states (SSE "push" event state, design §4.10).
const (
	// PushStateUnchanged: the compiled config equals the served one;
	// nothing was sent.
	PushStateUnchanged = "unchanged"
	// PushStateOffline: the AP is not connected; it receives the config on
	// its next connection.
	PushStateOffline = "offline"
	// PushStateReconnecting: the session was closed so the AP reconnects
	// and fetches (push mode reconnect).
	PushStateReconnecting = "reconnecting"
	PushStateNotified     = "notified"
	PushStateFetched      = "fetched"
	PushStateApplied      = "applied"
	PushStateRejected     = "rejected"
	PushStateFallback     = "fallback_reconnect"
	PushStateTimeout      = "timeout"
)

// Push results (dismist_ap_config_pushes_total{result}).
const (
	PushResultApplied  = "applied"
	PushResultRejected = "rejected"
	PushResultFallback = "fallback"
	PushResultTimeout  = "timeout"
)

// PushResults lists the results in a fixed order.
func PushResults() []string {
	return []string{PushResultApplied, PushResultRejected, PushResultFallback, PushResultTimeout}
}

// Timings are the protocol timers; zero fields take the defaults.
type Timings struct {
	// PushFetch is how long a notified AP has to re-request ep-config
	// before dismist falls back to a reconnect (15 s).
	PushFetch time.Duration
	// ReconnectFetch is how long a reconnect push waits for the AP to come
	// back and fetch (2 min).
	ReconnectFetch time.Duration
	// PushApply is how long a fetched config may take to be reported
	// applied (2 min).
	PushApply time.Duration
	// PushCoalesce merges pushes within this window into one notify (2 s).
	PushCoalesce time.Duration
	// Verify is the delay between an apply report (or a delivery without
	// one) and the post-delivery verification (2 min).
	Verify time.Duration
	// Drift is how long field 60 may differ from the served Timestamp
	// before it counts as drift (2 min).
	Drift time.Duration
}

func (t Timings) withDefaults() Timings {
	defaults := Timings{
		PushFetch: 15 * time.Second, ReconnectFetch: 2 * time.Minute, PushApply: 2 * time.Minute,
		PushCoalesce: 2 * time.Second, Verify: 2 * time.Minute, Drift: driftGrace,
	}
	if t.PushFetch <= 0 {
		t.PushFetch = defaults.PushFetch
	}
	if t.ReconnectFetch <= 0 {
		t.ReconnectFetch = defaults.ReconnectFetch
	}
	if t.PushApply <= 0 {
		t.PushApply = defaults.PushApply
	}
	if t.PushCoalesce <= 0 {
		t.PushCoalesce = defaults.PushCoalesce
	}
	if t.Verify <= 0 {
		t.Verify = defaults.Verify
	}
	if t.Drift <= 0 {
		t.Drift = defaults.Drift
	}
	return t
}

// PushStatus is the state of an AP's last push.
type PushStatus struct {
	Revision  uint64    `json:"revision"`
	Mode      string    `json:"mode"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	At        time.Time `json:"at"`
}

// PushEvent is one push lifecycle state change (SSE "push": {ap_id,
// revision, state, error}).
type PushEvent struct {
	APID     string    `json:"ap_id"`
	Revision uint64    `json:"revision"`
	Mode     string    `json:"mode"`
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
}

// PushHandle is what Push returns: the revision and how it is being
// delivered. Later states arrive as PushEvents (Options.OnPush).
type PushHandle struct {
	APID     string `json:"ap_id"`
	Revision uint64 `json:"revision"`
	// Changed is false when the config was not newer (nothing to push).
	Changed bool   `json:"changed"`
	Mode    string `json:"mode"`
	State   string `json:"state"`
}

// ErrNoConfig means there is no config to re-send.
var ErrNoConfig = errors.New("AP has no configuration to send")

// pushState is the push lifecycle of one AP (design §5.6):
//
//	reconnect: close the session; wait for the AP to fetch; wait for apply
//	notify:    send ep-config-change; wait 15 s for the fetch, else fall
//	           back to a reconnect (two fallbacks in a row learn
//	           "reconnect"); wait for apply
type pushState struct {
	generation uint64
	active     bool
	revision   uint64
	timestamp  float64
	mode       string
	auditID    string
	fetched    bool
	counted    bool
	startedAt  time.Time
	notifiedAt time.Time
	timer      timerHandle
	coalesce   timerHandle
	// fallbacks counts consecutive notify pushes that fell back.
	fallbacks int
	last      *PushStatus
}

// timerHandle wraps *time.Timer so a nil handle can be stopped.
type timerHandle struct{ timer *time.Timer }

func (t *timerHandle) stop() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (t *timerHandle) set(delay time.Duration, fire func()) {
	t.stop()
	t.timer = time.AfterFunc(delay, fire)
}

// pushModeLocked is the effective push mode.
func (s *Server) pushModeLocked() string {
	if Features(s.features.Load()).Has(FeatureConfigChangeNotify) && s.learnedPushMode != PushModeReconnect {
		return PushModeNotify
	}
	return PushModeReconnect
}

// PushMode is how a changed config reaches the AP now.
func (s *Server) PushMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pushModeLocked()
}

// Push serves a newly compiled config and delivers it (design §5.6): an
// offline AP gets it on its next connection; otherwise it is pushed with
// ep-config-change (config_change_notify) or a reconnect. Called only for
// APs whose compiled config changed (design §4.4); an equal or older
// config pushes nothing.
func (s *Server) Push(config []byte, revision uint64) (PushHandle, error) {
	changed, err := s.SetConfigIfNewer(config, revision)
	if err != nil {
		return PushHandle{}, err
	}
	if !changed {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return PushHandle{APID: s.auth.ID, Revision: revision, Mode: s.pushModeLocked(), State: PushStateUnchanged}, nil
	}
	return s.startPush(), nil
}

// Resend delivers the current config again (the "Re-send config"
// action) with the same lifecycle.
func (s *Server) Resend() (PushHandle, error) {
	s.mu.RLock()
	hasConfig := s.config != nil
	s.mu.RUnlock()
	if !hasConfig {
		return PushHandle{}, ErrNoConfig
	}
	return s.startPush(), nil
}

func (s *Server) startPush() PushHandle {
	now := time.Now()
	s.mu.Lock()
	p := &s.push
	handle := PushHandle{APID: s.auth.ID, Revision: s.revision, Changed: true, Mode: s.pushModeLocked()}
	if s.active == nil {
		handle.State = PushStateOffline
		s.mu.Unlock()
		return handle
	}
	if p.active && !p.counted && !p.fetched && p.mode != handle.Mode {
		// A push in another mode is superseded; it delivered nothing.
		p.coalesce.stop()
	}
	p.generation++
	generation := p.generation
	p.active, p.counted, p.fetched = true, false, false
	p.revision, p.timestamp, p.mode, p.startedAt = s.revision, s.configTS, handle.Mode, now
	p.auditID = fmt.Sprintf("dismist-r%d", s.revision)
	if handle.Mode == PushModeReconnect {
		handle.State = PushStateReconnecting
		s.setPushStateLocked(PushStateReconnecting, "", now)
		p.timer.set(s.timings.ReconnectFetch, func() { s.pushTimeout(generation, "the AP did not fetch the configuration after reconnecting") })
		conn := s.active
		s.closing, s.closingCause = conn, "config change"
		s.mu.Unlock()
		s.flush()
		_ = conn.close()
		return handle
	}
	handle.State = PushStateNotified
	sinceNotify := now.Sub(p.notifiedAt)
	if !p.notifiedAt.IsZero() && sinceNotify < s.timings.PushCoalesce {
		// Coalesce: one notify per window. The AP fetches the newest
		// config whenever it fetches.
		delay := s.timings.PushCoalesce - sinceNotify
		p.coalesce.set(delay, func() { s.sendNotify(generation) })
		p.timer.set(delay+s.timings.PushFetch, func() { s.pushFallback(generation) })
		s.mu.Unlock()
		return handle
	}
	s.mu.Unlock()
	s.sendNotify(generation)
	return handle
}

// sendNotify writes ep-config-change for push generation.
//
// Wire (recovered from 0.15 ep-control): main.reapply registers the
// subject "ep-config-change" with the connector, unmarshals the body into
// struct { AuditID string } and logs "ep_cfg_change (AuditID %q);
// refetching config soon" before re-sending ep-config about a second
// later. The connector looks up a non-zero handle among the AP's own
// outstanding requests first and logs "failed to find conversation with
// handle" when there is none, so dismist sends handle 0. The service class
// is not checked on that path; dismist uses 2, the class of every cloud
// message captured. Confidence: likely (needs-live-test, design §5.6).
func (s *Server) sendNotify(generation uint64) {
	now := time.Now()
	s.mu.Lock()
	p := &s.push
	if p.generation != generation || !p.active || p.fetched {
		s.mu.Unlock()
		return
	}
	conn := s.active
	if conn == nil {
		s.mu.Unlock()
		return
	}
	body, _ := json.Marshal(struct{ AuditID string }{AuditID: p.auditID})
	p.notifiedAt = now
	s.setPushStateLocked(PushStateNotified, "", now)
	p.timer.set(s.timings.PushFetch, func() { s.pushFallback(generation) })
	s.mu.Unlock()
	s.flush()
	err := conn.writeMessage(protocol.Message{Version: 1, ServiceClass: 2, Subject: "ep-config-change"}, body)
	if err != nil {
		s.pushFallback(generation)
	}
}

// pushFallback runs when a notified AP did not re-fetch in time: the
// session is closed so the AP reconnects and fetches (design §5.6). Two
// consecutive fallbacks learn push mode "reconnect".
func (s *Server) pushFallback(generation uint64) {
	now := time.Now()
	s.mu.Lock()
	p := &s.push
	if p.generation != generation || !p.active || p.fetched || p.mode != PushModeNotify {
		s.mu.Unlock()
		return
	}
	p.coalesce.stop()
	p.fallbacks++
	learned := ""
	if p.fallbacks >= 2 && s.learnedPushMode != PushModeReconnect {
		s.learnedPushMode, learned = PushModeReconnect, PushModeReconnect
	}
	s.counters.push(PushModeNotify, PushResultFallback)
	p.counted = true
	p.mode = PushModeReconnect
	s.setPushStateLocked(PushStateFallback, "the AP did not re-fetch after ep-config-change", now)
	title := "Config push fell back to a reconnect"
	if learned != "" {
		title += "; push mode is now reconnect for this AP"
	}
	s.emitLocked(Event{At: now, Kind: EventPush, Severity: SeverityWarning, Title: title,
		Detail: fmt.Sprintf("revision %d; no ep-config within %s of ep-config-change", p.revision, s.timings.PushFetch)})
	p.timer.set(s.timings.ReconnectFetch, func() { s.pushTimeout(generation, "the AP did not fetch the configuration after reconnecting") })
	conn := s.active
	if conn != nil {
		s.closing, s.closingCause = conn, "config push fallback"
	}
	s.mu.Unlock()
	s.flush()
	if conn != nil {
		_ = conn.close()
	}
	if learned != "" && s.onPushMode != nil {
		s.onPushMode(s.auth.ID, learned)
	}
}

// pushTimeout ends a push that was not fetched or applied in time.
func (s *Server) pushTimeout(generation uint64, reason string) {
	now := time.Now()
	s.mu.Lock()
	p := &s.push
	if p.generation != generation || !p.active {
		s.mu.Unlock()
		return
	}
	p.active = false
	p.coalesce.stop()
	if !p.counted {
		s.counters.push(p.mode, PushResultTimeout)
		p.counted = true
	}
	s.setPushStateLocked(PushStateTimeout, reason, now)
	s.emitLocked(Event{At: now, Kind: EventPush, Severity: SeverityWarning, Title: "Config push timed out",
		Detail: fmt.Sprintf("revision %d: %s", p.revision, reason)})
	s.mu.Unlock()
	s.flush()
}

// pushFetched notes that the AP fetched its config (after every ep-config
// reply).
func (s *Server) pushFetched() {
	now := time.Now()
	learned := ""
	s.mu.Lock()
	p := &s.push
	if !p.active || p.fetched {
		s.mu.Unlock()
		return
	}
	p.fetched = true
	p.coalesce.stop()
	if p.mode == PushModeNotify && !p.counted {
		p.fallbacks = 0
		if s.learnedPushMode != PushModeNotify {
			s.learnedPushMode, learned = PushModeNotify, PushModeNotify
		}
	}
	s.setPushStateLocked(PushStateFetched, "", now)
	generation := p.generation
	p.timer.set(s.timings.PushApply, func() { s.pushTimeout(generation, "the AP did not report the configuration applied") })
	s.mu.Unlock()
	s.flush()
	if learned != "" && s.onPushMode != nil {
		s.onPushMode(s.auth.ID, learned)
	}
}

// pushApplied ends a push on the AP's apply report: rejected when it
// carries an error, applied when its Timestamp is the pushed one. A report
// for an older Timestamp leaves the push waiting.
func (s *Server) pushApplied(applied telemetry.Applied) {
	now := time.Now()
	s.mu.Lock()
	p := &s.push
	if !p.active {
		s.mu.Unlock()
		return
	}
	matches := math.Abs(applied.ConfigTimestamp-p.timestamp) < 0.000001
	switch {
	case applied.Error != "" && (matches || p.fetched):
		p.active = false
		p.timer.stop()
		if !p.counted {
			s.counters.push(p.mode, PushResultRejected)
			p.counted = true
		}
		s.setPushStateLocked(PushStateRejected, truncateText(applied.Error, 300), now)
	case applied.Error == "" && matches:
		p.active = false
		p.timer.stop()
		if !p.counted {
			s.counters.push(p.mode, PushResultApplied)
			p.counted = true
		}
		s.setPushStateLocked(PushStateApplied, "", now)
	}
	s.mu.Unlock()
	s.flush()
}

// setPushStateLocked records and publishes a push state.
func (s *Server) setPushStateLocked(state, reason string, now time.Time) {
	p := &s.push
	p.last = &PushStatus{Revision: p.revision, Mode: p.mode, State: state, Error: reason, StartedAt: p.startedAt, At: now}
	s.queuePushLocked(PushEvent{APID: s.auth.ID, Revision: p.revision, Mode: p.mode, State: state, Error: reason, At: now})
}

// capabilitiesLocked reports what dismist can do with the AP.
func (s *Server) capabilitiesLocked() Capabilities {
	features := Features(s.features.Load())
	capabilities := Capabilities{
		APCmd:         features.Has(FeatureAPCmd),
		APCmdVerified: APCmdVerified(s.status.Firmware),
		Pong2:         features.Has(FeaturePong2),
	}
	switch s.learnedPushMode {
	case PushModeNotify:
		yes := true
		capabilities.ConfigChangeNotify = &yes
	case PushModeReconnect:
		no := false
		capabilities.ConfigChangeNotify = &no
	}
	return capabilities
}
