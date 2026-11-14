package main

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/nicko170/dismist/internal/api"
)

// readiness tracks the /readyz checks (design §4.9): the migration has
// committed or fallen back to legacy mode, the AP WSS listener is bound
// (listening, not "has accepted a connection"), and a bbolt read and write
// of meta.ready succeed.
type readiness struct {
	probe    func() error
	migrated atomic.Bool
	wssBound atomic.Bool
}

// Ready reports the checks that need no database write (for /api/system).
func (r *readiness) Ready() bool { return r.migrated.Load() && r.wssBound.Load() }

// check returns the first failing check, its message and whether all
// passed.
func (r *readiness) check() (string, string, bool) {
	if !r.migrated.Load() {
		return "migration", "migrating the database", false
	}
	if !r.wssBound.Load() {
		return "wss", "the AP WSS listener is not bound yet", false
	}
	if r.probe != nil {
		if err := r.probe(); err != nil {
			return "database", "database check failed: " + err.Error(), false
		}
	}
	return "", "", true
}

// startupHandler is the UI listener's handler from the first moment of
// startup (design §4.7): /healthz answers 200 as soon as the process
// listens, /readyz 503 until every readiness check passes, and every other
// path 503 until the operator API is installed with SetApp.
type startupHandler struct {
	readiness *readiness
	app       atomic.Pointer[http.Handler]
}

// SetApp installs the operator handler (web gate, API and UI).
func (h *startupHandler) SetApp(app http.Handler) { h.app.Store(&app) }

func (h *startupHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/healthz":
		writeHealth(writer, http.StatusOK, api.HealthView{Status: "ok"})
		return
	case "/readyz":
		if failing, message, ok := h.readiness.check(); !ok {
			writeHealth(writer, http.StatusServiceUnavailable, api.ErrorBody{
				Error: message, Code: api.ErrorCodeNotReady, Fields: map[string]string{"check": failing},
			})
			return
		}
		writeHealth(writer, http.StatusOK, api.HealthView{Status: "ready"})
		return
	}
	app := h.app.Load()
	if app == nil {
		writer.Header().Set("Retry-After", "5")
		writeHealth(writer, http.StatusServiceUnavailable, api.ErrorBody{
			Error: "dismist is starting; try again shortly", Code: api.ErrorCodeNotReady, Fields: map[string]string{},
		})
		return
	}
	(*app).ServeHTTP(writer, request)
}

func writeHealth(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
