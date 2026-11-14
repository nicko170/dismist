package main

import (
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/metrics"
	"github.com/nicko170/dismist/internal/model"
)

// TestMetricsSourcesFromTheDatabase wires the sources startMetrics finds
// on its own against a real bbolt database and checks their series.
func TestMetricsSourcesFromTheDatabase(t *testing.T) {
	dir := t.TempDir()
	store, err := model.OpenBolt(filepath.Join(dir, "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	writeTestPair(t, dir, 7)
	flags := flag.NewFlagSet("dismistd", flag.ContinueOnError)
	flags.String("tls-cert", filepath.Join(dir, "tls.crt"), "")
	flags.String("tls-key", filepath.Join(dir, "tls.key"), "")
	flags.String("ap-ca-file", "", "")

	options := metrics.Options{Store: store, Manager: controller.NewManager()}
	fillMetricsSources(&options, flags)
	if options.Build.GoVersion == "" || options.StoreUsage == nil || options.MigrationState == nil || options.StoreSchema == nil || options.Detail == nil || options.CompileStatus == nil {
		t.Fatalf("sources not filled: %+v", options)
	}
	if len(options.Certificates) != 1 || options.Certificates[0].Role != "leaf" || options.Certificates[0].Listener != "ap-wss" {
		t.Fatalf("certificates = %+v", options.Certificates)
	}
	handler, err := metrics.NewHandler(options)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, want := range []string{
		"dismist_build_info{goversion=",
		"dismist_store_size_bytes ",
		"dismist_store_free_bytes ",
		`dismist_schema_migration_state{state="not_run"} 1`,
		`dismist_tls_certificate_not_after_timestamp_seconds{cn="",listener="ap-wss",role="leaf"} `,
		"dismist_store_schema ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Sources already set are kept.
	kept := metrics.Options{Store: store, Manager: controller.NewManager(), Build: metrics.BuildInfo{Version: "x"}, Certificates: []metrics.Certificate{}}
	fillMetricsSources(&kept, flags)
	if kept.Build.Version != "x" || len(kept.Certificates) != 0 {
		t.Errorf("overwrote set sources: %+v", kept)
	}
	// Without the flags there are no certificates, and no error.
	if certs := certificatesFromFlags(flag.NewFlagSet("empty", flag.ContinueOnError)); certs != nil {
		t.Errorf("certificates without flags = %+v", certs)
	}
}

type fakeSnapshots struct {
	size int64
}

func (f fakeSnapshots) WriteBackup(string) (int64, error) { return f.size, nil }
func (f fakeSnapshots) Size() (int64, error)              { return f.size, nil }

func TestBackupMetrics(t *testing.T) {
	if _, ok := backupMetrics(nil)(); ok {
		t.Fatal("a nil scheduler reported status")
	}
	dir := t.TempDir()
	scheduler := newBackupScheduler(fakeSnapshots{size: 10}, backupConfig{dir: dir, keep: 3, interval: time.Hour}, func(string, string) {})
	scheduler.usage = func(string) (model.DiskSpace, error) { return model.DiskSpace{}, errors.New("no statfs") }
	status, ok := backupMetrics(scheduler)()
	if !ok || !status.LastSuccess.IsZero() || status.Bytes != 0 {
		t.Fatalf("status before any snapshot = %+v, %v", status, ok)
	}
	success := time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC)
	scheduler.mu.Lock()
	scheduler.lastSuccess = success
	scheduler.failures["space"] = 2
	scheduler.mu.Unlock()
	status, _ = backupMetrics(scheduler)()
	if !status.LastSuccess.Equal(success) || status.Failures["space"] != 2 {
		t.Fatalf("status = %+v", status)
	}
}
