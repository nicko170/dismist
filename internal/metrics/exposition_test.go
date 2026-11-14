package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestWriteFixtureExposition writes what the exporter emits for the full
// fixture (every source wired, per-client mode, names on) to the file named
// by DISMIST_FIXTURE_EXPOSITION. tools/dashboards/check-queries.sh loads it
// into Prometheus and evaluates every dashboard query against it. Without
// the variable the test is skipped.
func TestWriteFixtureExposition(t *testing.T) {
	path := os.Getenv("DISMIST_FIXTURE_EXPOSITION")
	if path == "" {
		t.Skip("DISMIST_FIXTURE_EXPOSITION is not set")
	}
	store, sessions := fixture(t)
	instruments := NewInstruments()
	instruments.HTTP(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/aps", nil))
	c := testCollector(store, sessions, Options{
		StoreSchema:   func() (int, int, bool) { return 1, 1, true },
		CompileStatus: func() map[string]CompileStatus { return nil },
		Names:         true,
		Clients:       ClientsPerClient,
		ClientLimit:   100,
		Build:         BuildInfo{Version: "dev", Revision: "fixture", GoVersion: "go"},
		Certificates:  []Certificate{{Listener: "ap-wss", Role: "leaf", NotAfter: time.Now().Add(60 * 24 * time.Hour)}},
		StoreUsage: func() (StoreUsage, bool) {
			return StoreUsage{SizeBytes: 1 << 20, FreeBytes: 1 << 30, HasFree: true}, true
		},
		MigrationState: func() (string, bool) { return "committed", true },
		Backups:        func() (BackupStatus, bool) { return BackupStatus{LastSuccess: time.Now()}, true },
	})
	if err := os.WriteFile(path, []byte(scrape(t, c, instruments)), 0o644); err != nil {
		t.Fatal(err)
	}
}
