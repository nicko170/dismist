package metrics

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestRulesAndDashboardsUseEmittedMetrics collects every dismist_* name the
// chart's alert rules and dashboards query and checks the exporter emits each
// one for a fixture with every optional source wired, so a renamed or
// not-yet-implemented metric fails here instead of silently never alerting.
func TestRulesAndDashboardsUseEmittedMetrics(t *testing.T) {
	chart := filepath.Join("..", "..", "charts", "dismist", "monitoring")
	files := []string{filepath.Join(chart, "rules.yaml")}
	dashboards, err := filepath.Glob(filepath.Join(chart, "dashboards", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, dashboards...)

	name := regexp.MustCompile(`dismist_[a-z0-9_]+`)
	used := map[string][]string{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, metric := range name.FindAllString(string(raw), -1) {
			used[metric] = append(used[metric], filepath.Base(file))
		}
	}
	if len(used) == 0 {
		t.Fatal("no dismist_* metrics found in the chart's rules")
	}

	// Every source wired, per-client mode and names on: the union of what
	// any install can emit.
	store, sessions := fixture(t)
	instruments := NewInstruments()
	instruments.HTTP(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/aps", nil))
	c := testCollector(store, sessions, Options{
		StoreSchema:    func() (int, int, bool) { return 1, 1, true },
		CompileStatus:  func() map[string]CompileStatus { return map[string]CompileStatus{} },
		Names:          true,
		Clients:        ClientsPerClient,
		ClientLimit:    100,
		Build:          BuildInfo{Version: "dev"},
		Certificates:   []Certificate{{Listener: "ap-wss", Role: "leaf", NotAfter: fixtureNow}},
		StoreUsage:     func() (StoreUsage, bool) { return StoreUsage{SizeBytes: 1, FreeBytes: 1, HasFree: true}, true },
		MigrationState: func() (string, bool) { return "committed", true },
		Backups:        func() (BackupStatus, bool) { return BackupStatus{LastSuccess: fixtureNow}, true },
	})
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(c, instruments)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	emitted := map[string]bool{}
	for _, family := range families {
		emitted[family.GetName()] = true
		// Histograms are queried by their series names.
		if family.GetType() == dto.MetricType_HISTOGRAM {
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				emitted[family.GetName()+suffix] = true
			}
		}
	}
	var missing []string
	for metric, where := range used {
		if !emitted[metric] {
			missing = append(missing, metric+" (used in "+where[0]+")")
		}
	}
	sort.Strings(missing)
	for _, metric := range missing {
		t.Errorf("not emitted by the exporter: %s", metric)
	}
}
