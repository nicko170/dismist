// Package metrics exposes bounded, read-only Prometheus telemetry for the controller.
//
// Labels carry IDs only (site UUID, AP ID). Names, IP addresses, SSIDs, client
// MACs, passphrases and secret paths never appear in the default mode.
package metrics

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// ClientMode selects how client data is exported (-metrics-clients).
type ClientMode string

const (
	ClientsOff       ClientMode = "off"
	ClientsAggregate ClientMode = "aggregate"
	ClientsPerClient ClientMode = "per-client"
)

// ParseClientMode accepts "off", "aggregate" or "per-client".
func ParseClientMode(value string) (ClientMode, error) {
	switch mode := ClientMode(value); mode {
	case ClientsOff, ClientsAggregate, ClientsPerClient:
		return mode, nil
	}
	return "", fmt.Errorf("unknown client metrics mode %q (want off, aggregate or per-client)", value)
}

// CompileStatus is one AP's config compile state.
type CompileStatus struct {
	// Failing is true while the AP's last compile failed and it is served
	// its last delivered config instead.
	Failing bool
	// Failures counts failed compiles since the controller started.
	Failures uint64
}

// Options configures the scrape handler. Store and Manager are required.
type Options struct {
	Store   model.StateStore
	Manager *controller.Manager

	// Token, when non-empty, must be presented as "Authorization: Bearer
	// <token>" on every scrape (-metrics-token-file).
	Token string

	// Names adds a name label to *_info series (-metrics-names). Clients and
	// ClientLimit select the client series (-metrics-clients,
	// -metrics-client-limit). Phase 0 exports no *_info or client series yet,
	// so these are accepted and kept for the collectors that use them.
	Names       bool
	Clients     ClientMode
	ClientLimit int

	// StoreSchema reports meta.schema and meta.compat; ok is false when the
	// database cannot be read. Nil omits dismist_store_schema and
	// dismist_store_compat.
	StoreSchema func() (schema, compat int, ok bool)
	// CompileStatus reports the compile state by AP ID; APs absent from the
	// map are reported as compiling cleanly. Nil omits the
	// dismist_ap_config_compile_error(s) series.
	CompileStatus func() map[string]CompileStatus

	// ErrorLog receives scrape errors. Nil logs to the standard logger.
	ErrorLog promhttp.Logger
}

type handler struct {
	scrape http.Handler
	// tokenDigest is the SHA-256 of the required bearer token, or nil when
	// scrapes are not authenticated. Comparing digests keeps the comparison
	// constant-time regardless of the presented token's length.
	tokenDigest []byte
}

// New returns an unauthenticated scrape handler with default options. Its
// only route is GET /metrics; the caller must bind it to a loopback listener
// or put an authenticated proxy in front of it.
func New(store model.StateStore, manager *controller.Manager) http.Handler {
	h, err := NewHandler(Options{Store: store, Manager: manager})
	if err != nil {
		panic(err)
	}
	return h
}

// NewHandler returns the scrape handler for GET /metrics.
func NewHandler(options Options) (http.Handler, error) {
	if options.Store == nil || options.Manager == nil {
		return nil, errors.New("metrics: store and manager are required")
	}
	if options.Clients == "" {
		options.Clients = ClientsAggregate
	}
	if _, err := ParseClientMode(string(options.Clients)); err != nil {
		return nil, err
	}
	if options.ClientLimit < 0 {
		return nil, errors.New("metrics: client limit must not be negative")
	}
	if strings.ContainsFunc(options.Token, isTokenSpace) {
		return nil, errors.New("metrics: token must not contain spaces or control characters")
	}
	registry := prometheus.NewRegistry()
	if err := registry.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}
	if err := registry.Register(newCollector(options)); err != nil {
		return nil, err
	}
	errorLog := options.ErrorLog
	if errorLog == nil {
		errorLog = log.New(log.Writer(), "metrics: ", log.Flags())
	}
	h := &handler{scrape: promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		// A series that fails to collect is logged and left out; the rest of
		// the scrape is still served.
		ErrorHandling: promhttp.ContinueOnError,
		ErrorLog:      errorLog,
	})}
	if options.Token != "" {
		digest := sha256.Sum256([]byte(options.Token))
		h.tokenDigest = digest[:]
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/metrics" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.tokenDigest != nil && !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="dismist metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.scrape.ServeHTTP(w, r)
}

func (h *handler) authorized(r *http.Request) bool {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return subtle.ConstantTimeCompare(digest[:], h.tokenDigest) == 1
}

func isTokenSpace(r rune) bool { return r <= ' ' || r == 0x7f }
