package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// streamMessage is one SSE event.
type streamMessage struct {
	event string
	data  any
}

// streamHub fans config, push and pending events out to the open site
// streams. Publishing never blocks: a stream that is not reading loses
// the message (and the client refetches on reopen, design §4.10).
type streamHub struct {
	mu   sync.Mutex
	subs map[string]map[chan streamMessage]bool
}

func newStreamHub() *streamHub {
	return &streamHub{subs: map[string]map[chan streamMessage]bool{}}
}

func (h *streamHub) subscribe(site string) chan streamMessage {
	ch := make(chan streamMessage, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[site] == nil {
		h.subs[site] = map[chan streamMessage]bool{}
	}
	h.subs[site][ch] = true
	return ch
}

func (h *streamHub) unsubscribe(site string, ch chan streamMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs[site], ch)
	if len(h.subs[site]) == 0 {
		delete(h.subs, site)
	}
}

func (h *streamHub) publish(site, event string, data any) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[site] {
		select {
		case ch <- streamMessage{event: event, data: data}:
		default:
		}
	}
}

func (h *streamHub) publishAll(event string, data any) {
	if h == nil {
		return
	}
	h.mu.Lock()
	sites := make([]string, 0, len(h.subs))
	for site := range h.subs {
		sites = append(sites, site)
	}
	h.mu.Unlock()
	for _, site := range sites {
		h.publish(site, event, data)
	}
}

// pushTracker remembers config pushes so the streams can report when each
// AP applied, rejected or did not take its new revision (reconnect mode;
// the notify lifecycle publishes its own states through PublishPush).
type pushTracker struct {
	mu      sync.Mutex
	pending map[string]trackedPush
}

type trackedPush struct {
	revision uint64
	at       time.Time
}

func newPushTracker() *pushTracker { return &pushTracker{pending: map[string]trackedPush{}} }

func (t *pushTracker) track(apID string, revision uint64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[apID] = trackedPush{revision: revision, at: at}
}

func (t *pushTracker) get(apID string) (trackedPush, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	push, ok := t.pending[apID]
	return push, ok
}

func (t *pushTracker) done(apID string, revision uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if push, ok := t.pending[apID]; ok && push.revision == revision {
		delete(t.pending, apID)
	}
}

// pushTimeout is how long a push may stay unapplied before the stream
// reports a timeout.
const pushTimeout = 5 * time.Minute

// PublishPush sends a push lifecycle state to the AP's site stream (the
// push lifecycle of workstream B calls it, design §4.10).
func (s *Server) PublishPush(event api.PushEvent) {
	ap := findAP(s.store.Snapshot(), event.APID)
	if ap == nil || ap.SiteID == "" {
		return
	}
	switch event.State {
	case api.PushStateApplied, api.PushStateRejected, api.PushStateTimeout:
		s.pushes.done(event.APID, event.Revision)
	}
	s.hub.publish(ap.SiteID, api.StreamEventPush, event)
}

// PublishPending tells every stream that the pending-AP list changed.
func (s *Server) PublishPending() {
	s.hub.publishAll(api.StreamEventPending, api.PendingEvent{Count: s.pendingCount()})
}

// writeSSE writes one event.
func writeSSE(writer io.Writer, flusher http.Flusher, event string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, raw); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// apMaterial is the part of an AP's live view whose change is sent at
// once (design §4.10); everything else at most every 15 s.
type apMaterial struct {
	Status          api.APStatus
	Connected       bool
	AppliedRevision uint64
	ConfigRevision  uint64
	LastError       string
	ClientCount     int
	Channels        string
}

func material(live api.APLive) apMaterial {
	channels := ""
	for _, radio := range live.Radios {
		channels += fmt.Sprintf("%s:%d/%d;", radio.Band, radio.Channel, radio.Width)
	}
	return apMaterial{Status: live.Status, Connected: live.Connected, AppliedRevision: live.AppliedRevision,
		ConfigRevision: live.ConfigRevision, LastError: live.LastError, ClientCount: live.ClientCount, Channels: channels}
}

// siteSnapshot is the snapshot event of a site, or false when the site
// does not exist.
func (s *Server) siteSnapshot(siteID string) (api.StreamSnapshot, map[string]api.APLive, bool) {
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil {
		return api.StreamSnapshot{}, nil, false
	}
	ctx := s.apContext(state)
	snapshot := api.StreamSnapshot{SiteID: siteID, Revision: state.Revision, APs: []api.APLive{}}
	byID := map[string]api.APLive{}
	for _, ap := range siteAPs(state, siteID) {
		live := s.apLive(ctx, ap, s.observe(ap.ID))
		snapshot.APs = append(snapshot.APs, live)
		byID[ap.ID] = live
	}
	return snapshot, byID, true
}

// streamSite serves GET /api/sites/$id/stream (design §4.10): a snapshot,
// then typed deltas. The legacy app gets its "update" events.
func (s *Server) streamSite(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	if legacyClient(request) {
		s.legacyStream(writer, request, siteID)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, internalError("streaming unavailable"))
		return
	}
	snapshot, sent, found := s.siteSnapshot(siteID)
	if !found {
		writeAPIError(writer, notFound("site"))
		return
	}
	messages := s.hub.subscribe(siteID)
	defer s.hub.unsubscribe(siteID, messages)
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	if err := writeSSE(writer, flusher, api.StreamEventSnapshot, snapshot); err != nil {
		return
	}
	interval, heartbeat, maxAge := s.streamInterval, s.streamHeartbeat, s.streamMaxAge
	if interval <= 0 {
		interval = time.Second
	}
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	lastSent := map[string]time.Time{}
	for id := range sent {
		lastSent[id] = time.Now()
	}
	events := newEventCursor(s, siteID)
	pendingRevision := uint64(0)
	if s.manager != nil {
		pendingRevision = s.manager.PendingRevision()
	}
	reported := map[string]uint64{}
	check := time.NewTicker(interval)
	beat := time.NewTicker(heartbeat)
	deadline := time.NewTimer(maxAge)
	defer check.Stop()
	defer beat.Stop()
	defer deadline.Stop()
	resnapshot := func() bool {
		snapshot, current, found := s.siteSnapshot(siteID)
		if !found {
			return false
		}
		sent = current
		for id := range current {
			lastSent[id] = time.Now()
		}
		return writeSSE(writer, flusher, api.StreamEventSnapshot, snapshot) == nil
	}
	for {
		select {
		case <-request.Context().Done():
			return
		case <-deadline.C:
			return
		case <-beat.C:
			if _, err := io.WriteString(writer, ": hb\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case message := <-messages:
			if err := writeSSE(writer, flusher, message.event, message.data); err != nil {
				return
			}
			if message.event == api.StreamEventConfig && !resnapshot() {
				return
			}
		case <-check.C:
			snapshot, current, found := s.siteSnapshot(siteID)
			if !found {
				return
			}
			if len(current) != len(sent) {
				if !resnapshot() {
					return
				}
				continue
			}
			now := time.Now()
			for _, live := range snapshot.APs {
				previous, known := sent[live.ID]
				if !known {
					if !resnapshot() {
						return
					}
					break
				}
				changedMaterial := material(previous) != material(live)
				changedAny := previous.UptimeSeconds != live.UptimeSeconds || previous.LANIP != live.LANIP ||
					previous.RemoteAddress != live.RemoteAddress || fmt.Sprint(previous.Radios) != fmt.Sprint(live.Radios)
				if changedMaterial || (changedAny && now.Sub(lastSent[live.ID]) >= 15*time.Second) {
					if err := writeSSE(writer, flusher, api.StreamEventAP, live); err != nil {
						return
					}
					sent[live.ID], lastSent[live.ID] = live, now
				}
				if err := s.reportPush(writer, flusher, live, reported); err != nil {
					return
				}
			}
			for _, summary := range events.poll() {
				if err := writeSSE(writer, flusher, api.StreamEventEvent, summary); err != nil {
					return
				}
			}
			if s.manager != nil {
				if revision := s.manager.PendingRevision(); revision != pendingRevision {
					pendingRevision = revision
					if err := writeSSE(writer, flusher, api.StreamEventPending, api.PendingEvent{Count: s.pendingCount()}); err != nil {
						return
					}
				}
			}
		}
	}
}

// reportPush emits push applied/rejected/timeout for a tracked reconnect
// push once per stream.
func (s *Server) reportPush(writer io.Writer, flusher http.Flusher, live api.APLive, reported map[string]uint64) error {
	push, ok := s.pushes.get(live.ID)
	if !ok || reported[live.ID] == push.revision {
		return nil
	}
	var state api.PushState
	switch {
	case live.AppliedRevision >= push.revision && live.LastError == "":
		state = api.PushStateApplied
	case live.LastError != "" && live.ConfigRevision == push.revision:
		state = api.PushStateRejected
	case time.Since(push.at) > pushTimeout:
		state = api.PushStateTimeout
	default:
		return nil
	}
	reported[live.ID] = push.revision
	if time.Since(push.at) > 2*pushTimeout {
		s.pushes.done(live.ID, push.revision)
	}
	return writeSSE(writer, flusher, api.StreamEventPush, api.PushEvent{APID: live.ID, Revision: push.revision, State: state, Error: live.LastError})
}

// eventCursor polls the stored events of a site and returns the ones
// stored or updated since the stream opened (design §4.10 event).
type eventCursor struct {
	server *Server
	siteID string
	since  time.Time
	seen   map[string]string
	last   time.Time
}

func newEventCursor(s *Server, siteID string) *eventCursor {
	return &eventCursor{server: s, siteID: siteID, since: time.Now().UTC(), seen: map[string]string{}}
}

func (c *eventCursor) poll() []api.EventSummary {
	if time.Since(c.last) < 2*time.Second {
		return nil
	}
	c.last = time.Now()
	events, ok := c.server.store.(eventStore)
	if !ok {
		return nil
	}
	batch, _, err := events.Events2().List(model.EventQuery{SiteID: c.siteID, Limit: 50})
	if err != nil {
		return nil
	}
	var result []api.EventSummary
	for index := len(batch) - 1; index >= 0; index-- {
		event := batch[index]
		if event.LastSeen.Before(c.since) {
			continue
		}
		fingerprint := fmt.Sprintf("%d/%s/%s", event.Count, event.LastSeen.Format(time.RFC3339Nano), event.ResolvedBy)
		if c.seen[event.ID] == fingerprint {
			continue
		}
		c.seen[event.ID] = fingerprint
		result = append(result, eventSummary(event))
	}
	if len(c.seen) > 1000 {
		c.seen = map[string]string{}
		c.since = time.Now().UTC()
	}
	return result
}
