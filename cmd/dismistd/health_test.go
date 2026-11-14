package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
)

func TestStartupHandlerHealthzFirst(t *testing.T) {
	probeErr := error(nil)
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	ready := &readiness{probe: func() error { return probeErr }, now: func() time.Time { return now }}
	handler := &startupHandler{readiness: ready}
	get := func(path string) (*httptest.ResponseRecorder, map[string]any) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v %q", path, err, response.Body.String())
		}
		return response, body
	}
	check := func(stage, path string, status int, field string) {
		t.Helper()
		response, body := get(path)
		if response.Code != status {
			t.Fatalf("%s %s: %d %v", stage, path, response.Code, body)
		}
		if field != "" {
			fields, _ := body["fields"].(map[string]any)
			if body["code"] != string(api.ErrorCodeNotReady) || fields["check"] != field {
				t.Fatalf("%s %s: %v, want check %q", stage, path, body, field)
			}
		}
	}
	// Migrating: healthz passes, readyz names the migration, the API waits.
	check("migrating", "/healthz", http.StatusOK, "")
	check("migrating", "/readyz", http.StatusServiceUnavailable, "migration")
	if response, body := get("/api/sites"); response.Code != http.StatusServiceUnavailable || body["code"] != string(api.ErrorCodeNotReady) || response.Header().Get("Retry-After") == "" {
		t.Fatalf("API during startup: %d %v", response.Code, body)
	}
	ready.migrated.Store(true)
	check("migrated", "/readyz", http.StatusServiceUnavailable, "wss")
	ready.wssBound.Store(true)
	if !ready.Ready() {
		t.Fatal("Ready() false after migration and bind")
	}
	// Every check passes, but the operator API is not installed yet.
	check("bound", "/readyz", http.StatusServiceUnavailable, "app")
	if response, _ := get("/api/sites"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("API before SetApp: %d", response.Code)
	}
	// Once the app is installed it gets everything but the probes.
	handler.SetApp(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	check("app installed", "/readyz", http.StatusOK, "")
	probeErr = errors.New("disk full")
	// The last result stands until probeInterval has passed.
	check("probe failing, cached", "/readyz", http.StatusOK, "")
	now = now.Add(probeInterval)
	check("probe failing", "/readyz", http.StatusServiceUnavailable, "database")
	probeErr = nil
	now = now.Add(probeInterval)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sites", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("app not installed: %d", response.Code)
	}
	check("running", "/healthz", http.StatusOK, "")
	check("running", "/readyz", http.StatusOK, "")
}

// /readyz is unauthenticated and public: however many requests arrive, the
// database write runs at most once per probeInterval, and a slow check
// does not hold up the other requests.
func TestReadyzRateLimitsTheDatabaseCheck(t *testing.T) {
	var probes atomic.Int32
	release := make(chan struct{})
	block := atomic.Bool{}
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var clock sync.Mutex
	ready := &readiness{
		probe: func() error {
			probes.Add(1)
			if block.Load() {
				<-release
			}
			return nil
		},
		now: func() time.Time { clock.Lock(); defer clock.Unlock(); return now },
	}
	advance := func(by time.Duration) { clock.Lock(); now = now.Add(by); clock.Unlock() }
	ready.migrated.Store(true)
	ready.wssBound.Store(true)
	handler := &startupHandler{readiness: ready}
	handler.SetApp(http.NotFoundHandler())
	get := func() int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return response.Code
	}
	var wg sync.WaitGroup
	for range 256 {
		wg.Add(1)
		go func() { defer wg.Done(); get() }()
	}
	wg.Wait()
	if got := probes.Load(); got != 1 {
		t.Fatalf("256 requests ran %d database checks, want 1", got)
	}
	// A slow check: the request that runs it waits; others answer from the
	// last result at once.
	advance(probeInterval)
	block.Store(true)
	slow := make(chan int)
	go func() { slow <- get() }()
	for probes.Load() != 2 {
		time.Sleep(time.Millisecond)
	}
	if code := get(); code != http.StatusOK {
		t.Fatalf("during a running check: %d, want the last result", code)
	}
	// One that runs past probeStale is reported as failing.
	advance(probeStale)
	if code := get(); code != http.StatusServiceUnavailable {
		t.Fatalf("check running for %s: %d, want 503", probeStale, code)
	}
	close(release)
	if code := <-slow; code != http.StatusOK {
		t.Fatalf("slow check: %d", code)
	}
	if got := probes.Load(); got != 2 {
		t.Fatalf("%d database checks, want 2", got)
	}
}
