package metrics

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
)

// Instruments counts controller events outside the AP sessions: TLS
// handshake errors on the AP listener, AP DNS answers, failed observation
// writes, and the operator UI's HTTP requests, SSE streams and failed
// sign-ins. The daemon creates one at startup, wraps its listeners with it,
// and passes it to the scrape handler (Options.Instruments). Every method is
// safe on a nil *Instruments and then counts nothing.
type Instruments struct {
	tlsErrors         [len(TLSErrorReasons)]atomic.Uint64
	observationErrors atomic.Uint64
	dns               [len(DNSResults)]atomic.Uint64
	streams           atomic.Int64
	streamUpdates     atomic.Uint64
	loginFailures     atomic.Uint64

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec

	tlsDesc, observationDesc, dnsDesc       *prometheus.Desc
	streamsDesc, updatesDesc, loginFailDesc *prometheus.Desc
}

// TLSErrorReasons are the dismist_ap_tls_handshake_errors_total reasons:
// client_rejected (the client sent a TLS alert, typically an AP that does
// not trust the served chain), closed (the connection ended before a
// handshake: load balancer checks and port scans) and protocol (anything
// else, such as plain HTTP or an unsupported TLS version).
var TLSErrorReasons = [...]string{"client_rejected", "closed", "protocol"}

// DNSResults are the dismist_dns_queries_total results.
var DNSResults = [...]string{"override", "forwarded", "refused", "error"}

// NewInstruments returns zeroed instruments.
func NewInstruments() *Instruments {
	return &Instruments{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dismist_http_requests_total",
			Help: "Operator UI and API requests by route (a fixed route template, never the raw path) and status code.",
		}, []string{"route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dismist_http_request_duration_seconds",
			Help:    "Operator UI and API request duration by route (the SSE stream route is not timed).",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		tlsDesc:         prometheus.NewDesc("dismist_ap_tls_handshake_errors_total", "Failed TLS handshakes on the AP WSS listener by reason: client_rejected (usually an AP that does not trust the served chain), closed (health checks, scans) or protocol.", []string{"reason"}, nil),
		observationDesc: prometheus.NewDesc("dismist_store_observation_errors_total", "AP observations (history samples, events, client sessions) the controller could not write to its database.", nil, nil),
		dnsDesc:         prometheus.NewDesc("dismist_dns_queries_total", "AP DNS queries by result: override (answered with the controller address), forwarded, refused (not a registered AP) or error.", []string{"result"}, nil),
		streamsDesc:     prometheus.NewDesc("dismist_ui_streams", "Open operator UI event streams (SSE).", nil, nil),
		updatesDesc:     prometheus.NewDesc("dismist_ui_stream_updates_total", "Events sent on operator UI event streams (heartbeats not counted).", nil, nil),
		loginFailDesc:   prometheus.NewDesc("dismist_ui_login_failures_total", "Failed operator sign-ins (wrong token, malformed or rate-limited).", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (i *Instruments) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{i.tlsDesc, i.observationDesc, i.dnsDesc, i.streamsDesc, i.updatesDesc, i.loginFailDesc} {
		ch <- desc
	}
	i.requests.Describe(ch)
	i.duration.Describe(ch)
}

// Collect implements prometheus.Collector.
func (i *Instruments) Collect(ch chan<- prometheus.Metric) {
	for index, reason := range TLSErrorReasons {
		counter(ch, i.tlsDesc, float64(i.tlsErrors[index].Load()), reason)
	}
	counter(ch, i.observationDesc, float64(i.observationErrors.Load()))
	for index, result := range DNSResults {
		counter(ch, i.dnsDesc, float64(i.dns[index].Load()), result)
	}
	gauge(ch, i.streamsDesc, float64(i.streams.Load()))
	counter(ch, i.updatesDesc, float64(i.streamUpdates.Load()))
	counter(ch, i.loginFailDesc, float64(i.loginFailures.Load()))
	i.requests.Collect(ch)
	i.duration.Collect(ch)
}

// ObservationError counts one failed observation write.
func (i *Instruments) ObservationError() {
	if i != nil {
		i.observationErrors.Add(1)
	}
}

// TLSErrorLog returns a logger for http.Server.ErrorLog on the AP WSS
// listener: every line goes to w, and lines reporting a TLS handshake error
// are counted.
func (i *Instruments) TLSErrorLog(w io.Writer) *log.Logger {
	return log.New(&tlsErrorWriter{next: w, instruments: i}, "", log.LstdFlags)
}

type tlsErrorWriter struct {
	next        io.Writer
	instruments *Instruments
}

func (w *tlsErrorWriter) Write(line []byte) (int, error) {
	if w.instruments != nil && bytes.Contains(line, []byte("TLS handshake error")) {
		w.instruments.tlsErrors[tlsErrorReason(line)].Add(1)
	}
	if w.next == nil {
		return len(line), nil
	}
	return w.next.Write(line)
}

// tlsErrorReason classifies a "TLS handshake error" log line from net/http.
func tlsErrorReason(line []byte) int {
	text := string(line)
	switch {
	case strings.Contains(text, "remote error"):
		return 0
	case strings.Contains(text, "EOF") || strings.Contains(text, "connection reset") || strings.Contains(text, "i/o timeout") || strings.Contains(text, "broken pipe"):
		return 1
	}
	return 2
}

// DNS wraps the AP DNS handler and counts each answer by result.
func (i *Instruments) DNS(next dns.Handler) dns.Handler {
	if i == nil {
		return next
	}
	return dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
		next.ServeDNS(&dnsCountingWriter{ResponseWriter: writer, instruments: i}, query)
	})
}

type dnsCountingWriter struct {
	dns.ResponseWriter
	instruments *Instruments
}

func (w *dnsCountingWriter) WriteMsg(answer *dns.Msg) error {
	w.instruments.dns[dnsResult(answer)].Add(1)
	return w.ResponseWriter.WriteMsg(answer)
}

func dnsResult(answer *dns.Msg) int {
	switch {
	case answer == nil:
		return 3
	case answer.Rcode == dns.RcodeRefused:
		return 2
	case answer.Rcode == dns.RcodeServerFailure || answer.Rcode == dns.RcodeFormatError:
		return 3
	case answer.Authoritative:
		return 0
	}
	return 1
}

// HTTP wraps the operator UI listener's handler: it counts requests by
// route and code, times them, tracks open SSE streams and their events, and
// counts failed sign-ins.
func (i *Instruments) HTTP(next http.Handler) http.Handler {
	if i == nil {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		route := Route(request.URL.Path)
		recorder := &statusRecorder{ResponseWriter: writer, code: http.StatusOK}
		stream := route == streamRoute
		if stream {
			recorder.events = &i.streamUpdates
			i.streams.Add(1)
			defer i.streams.Add(-1)
		}
		started := time.Now()
		next.ServeHTTP(recorder, request)
		code := recorder.code
		i.requests.WithLabelValues(route, statusCode(code)).Inc()
		if !stream {
			i.duration.WithLabelValues(route).Observe(time.Since(started).Seconds())
		}
		if route == "/auth/login" && request.Method == http.MethodPost && code >= 400 {
			i.loginFailures.Add(1)
		}
	})
}

func statusCode(code int) string {
	if code < 100 || code > 599 {
		return "other"
	}
	return strconv.Itoa(code)
}

// statusRecorder records the response code and, on an event stream, counts
// the events written (an event with a data line, ended by a blank line).
type statusRecorder struct {
	http.ResponseWriter
	code        int
	wroteHeader bool

	events       *atomic.Uint64
	inLine       []byte
	eventHasData bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.code, r.wroteHeader = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.code, r.wroteHeader = http.StatusOK, true
	}
	if r.events != nil {
		r.countEvents(body)
	}
	return r.ResponseWriter.Write(body)
}

// countEvents follows the SSE wire format across writes.
func (r *statusRecorder) countEvents(body []byte) {
	for _, b := range body {
		if b != '\n' {
			if len(r.inLine) < 5 {
				r.inLine = append(r.inLine, b)
			}
			continue
		}
		line := string(r.inLine)
		r.inLine = r.inLine[:0]
		switch {
		case line == "" || line == "\r":
			if r.eventHasData {
				r.events.Add(1)
			}
			r.eventHasData = false
		case strings.HasPrefix(line, "data"):
			r.eventHasData = true
		}
	}
}

// Flush passes flushes through, which the SSE stream needs.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

const streamRoute = "/api/sites/{id}/stream"

// routes are the route templates dismist_http_requests_total uses; any
// other path is counted under a catch-all, so a client cannot create series
// by requesting made-up paths. "{id}" matches one path segment.
var routes = buildRoutes([]string{
	"/healthz", "/readyz", "/auth/login", "/auth/logout", "/auth/session",
	"/api/status", "/api/telemetry", "/api/clients", "/api/events", "/api/networks", "/api/networks/{id}",
	"/api/aps", "/api/aps/pending", "/api/aps/pending/{id}", "/api/aps/{id}", "/api/aps/{id}/clients",
	"/api/aps/{id}/config", "/api/aps/{id}/telemetry", "/api/aps/{id}/expert", "/api/aps/{id}/events",
	"/api/aps/{id}/actions/{id}", "/api/clients/{id}", "/api/clients/{id}/meta",
	"/api/sites", "/api/sites/{id}", streamRoute, "/api/sites/{id}/networks", "/api/sites/{id}/networks/{id}",
	"/api/sites/{id}/networks/{id}/expert", "/api/sites/{id}/networks/{id}/reveal", "/api/sites/{id}/expert",
	"/api/sites/{id}/ssh-keys", "/api/sites/{id}/aps", "/api/sites/{id}/rf", "/api/sites/{id}/rf/plan", "/api/sites/{id}/rf/neighbours",
	"/api/sites/{id}/changes", "/api/sites/{id}/changes/{id}", "/api/sites/{id}/changes/{id}/restore",
	"/api/sites/{id}/recommendations", "/api/sites/{id}/recommendations/{id}/apply",
	"/api/sites/{id}/clients", "/api/sites/{id}/clients/{id}", "/api/sites/{id}/clients/{id}/block",
	"/api/sites/{id}/clients/{id}/disconnect", "/api/sites/{id}/events", "/api/sites/{id}/events/{id}",
	"/api/sites/{id}/history/samples", "/api/system", "/api/system/backup", "/api/system/migration",
	"/api/system/migration/retry", "/api/system/migration/finalise", "/api/system/sessions",
	"/api/system/sessions/revoke-all", "/api/system/sessions/{id}", "/api/system/tokens", "/api/system/tokens/{id}",
	"/api/onboarding", "/api/onboarding/ca-bundle.pem",
})

type routeTemplate struct {
	template string
	segments []string
	literals int
}

func buildRoutes(templates []string) map[int][]routeTemplate {
	result := map[int][]routeTemplate{}
	for _, template := range templates {
		segments := strings.Split(strings.Trim(template, "/"), "/")
		literals := 0
		for _, segment := range segments {
			if segment != "{id}" {
				literals++
			}
		}
		result[len(segments)] = append(result[len(segments)], routeTemplate{template, segments, literals})
	}
	return result
}

// Route maps a request path to its route template: one of the API routes,
// "/api/other", "/auth/other", "static" (hashed UI assets), "legacy" (the
// old UI) or "ui" (client-side routes served index.html).
func Route(path string) string { return classify(path) }

func classify(path string) string {
	switch {
	case strings.HasPrefix(path, "/_ui/") || strings.HasPrefix(path, "/assets/"):
		return "static"
	case path == "/legacy" || strings.HasPrefix(path, "/legacy/"):
		return "legacy"
	case !strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/auth/") && path != "/healthz" && path != "/readyz":
		return "ui"
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	best, bestLiterals := "", -1
	for _, candidate := range routes[len(segments)] {
		if candidate.literals > bestLiterals && matches(candidate.segments, segments) {
			best, bestLiterals = candidate.template, candidate.literals
		}
	}
	if best != "" {
		return best
	}
	if strings.HasPrefix(path, "/auth/") {
		return "/auth/other"
	}
	return "/api/other"
}

func matches(template, segments []string) bool {
	for index, segment := range template {
		if segments[index] == "" {
			return false
		}
		if segment != "{id}" && segment != segments[index] {
			return false
		}
	}
	return true
}
