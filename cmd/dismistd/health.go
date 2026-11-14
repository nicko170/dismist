package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nicko170/dismist/internal/api"
)

// The database check behind /readyz writes meta.ready (a bbolt write
// transaction with an fsync). /readyz is unauthenticated and reachable
// through the public route, so the write runs at most once per
// probeInterval whatever the request rate; other requests get the last
// result.
const (
	probeInterval = 10 * time.Second
	// probeStale is how long a database check may run before /readyz
	// reports it as failing.
	probeStale = 30 * time.Second
)

// readiness tracks the /readyz checks (design §4.9): the migration has
// committed or fallen back to legacy mode, the AP WSS listener is bound
// (listening, not "has accepted a connection"), and a bbolt read and write
// of meta.ready succeed.
type readiness struct {
	probe    func() error
	now      func() time.Time // nil means time.Now
	migrated atomic.Bool
	wssBound atomic.Bool

	mu           sync.Mutex
	probing      bool      // a database check is running
	probeStarted time.Time // when the running check started
	result       error     // the last check's result
	resultAt     time.Time // when the last check finished; zero: none yet
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
		if err := r.probeDB(); err != nil {
			return "database", "database check failed: " + err.Error(), false
		}
	}
	return "", "", true
}

func (r *readiness) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// probeDB returns the result of the database check, running it only when
// the last result is probeInterval old. While one request runs it, the
// others answer from the last result without waiting, unless there is none
// yet or the running check has taken probeStale.
func (r *readiness) probeDB() error {
	r.mu.Lock()
	now := r.clock()
	if r.probing {
		err := r.result
		switch {
		case now.Sub(r.probeStarted) >= probeStale:
			err = fmt.Errorf("the check has not finished in %s", probeStale)
		case r.resultAt.IsZero():
			err = errors.New("the first check has not finished")
		}
		r.mu.Unlock()
		return err
	}
	if !r.resultAt.IsZero() && now.Sub(r.resultAt) < probeInterval {
		err := r.result
		r.mu.Unlock()
		return err
	}
	r.probing, r.probeStarted = true, now
	r.mu.Unlock()
	err := r.probe()
	r.mu.Lock()
	r.probing, r.result, r.resultAt = false, err, r.clock()
	r.mu.Unlock()
	return err
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
		// Not ready until the operator handler serves; otherwise a probe
		// could pass while every other path still answers "starting".
		if h.app.Load() == nil {
			writeHealth(writer, http.StatusServiceUnavailable, api.ErrorBody{
				Error: "the operator API is not installed yet", Code: api.ErrorCodeNotReady, Fields: map[string]string{"check": "app"},
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
