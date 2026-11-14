package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nicko170/dismist/internal/api"
)

func TestStartupHandlerHealthzFirst(t *testing.T) {
	probeErr := error(nil)
	ready := &readiness{probe: func() error { return probeErr }}
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
	check("bound", "/readyz", http.StatusOK, "")
	probeErr = errors.New("disk full")
	check("probe failing", "/readyz", http.StatusServiceUnavailable, "database")
	probeErr = nil
	// Once the app is installed it gets everything but the probes.
	handler.SetApp(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sites", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("app not installed: %d", response.Code)
	}
	check("running", "/healthz", http.StatusOK, "")
	check("running", "/readyz", http.StatusOK, "")
}
