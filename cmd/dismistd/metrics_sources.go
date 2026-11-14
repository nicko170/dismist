package main

import (
	"crypto/tls"
	"flag"
	"log"
	"path/filepath"
	"runtime"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/metrics"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
)

// storeSchema reads meta.schema and meta.compat for dismist_store_schema and
// dismist_store_compat.
func storeSchema(store *model.BoltStore) func() (int, int, bool) {
	return func() (int, int, bool) {
		info, err := store.Schema()
		if err != nil {
			return 0, 0, false
		}
		return int(info.Schema), int(info.Compat), true
	}
}

// compileStatus reports each registered AP's compile state from its control
// session (design §4.4 per-AP isolation): failing while it is served its
// delivered config or none, and the failures its session has counted.
func compileStatus(manager *controller.Manager) func() map[string]metrics.CompileStatus {
	return func() map[string]metrics.CompileStatus {
		servers := manager.Servers()
		result := make(map[string]metrics.CompileStatus, len(servers))
		for id, server := range servers {
			status := server.Status()
			result[id] = metrics.CompileStatus{
				Failing:  status.ConfigSource != "" && status.ConfigSource != controller.ConfigSourceCompiled,
				Failures: server.Counters().CompileErrors,
			}
		}
		return result
	}
}

// storeUsage reports the database size (dismist_store_size_bytes) and the
// free space of the filesystem holding it (dismist_store_free_bytes).
func storeUsage(store *model.BoltStore) func() (metrics.StoreUsage, bool) {
	return func() (metrics.StoreUsage, bool) {
		size, err := store.Size()
		if err != nil {
			return metrics.StoreUsage{}, false
		}
		usage := metrics.StoreUsage{SizeBytes: size}
		if space, err := model.DiskUsage(filepath.Dir(store.Path())); err == nil {
			usage.FreeBytes, usage.HasFree = int64(space.Free), true
		}
		return usage, true
	}
}

// migrationState reads meta.migration_state for
// dismist_schema_migration_state.
func migrationState(store *model.BoltStore) func() (string, bool) {
	return func() (string, bool) {
		state, err := migrate.ReadState(store)
		if err != nil || state.State == "" {
			return "", false
		}
		return state.State, true
	}
}

// backupMetrics reports the scheduled backups for the dismist_backup_*
// series.
func backupMetrics(scheduler *backupScheduler) func() (metrics.BackupStatus, bool) {
	return func() (metrics.BackupStatus, bool) {
		if scheduler == nil {
			return metrics.BackupStatus{}, false
		}
		view := scheduler.Status()
		status := metrics.BackupStatus{Bytes: view.UsedBytes, Failures: scheduler.Failures()}
		if view.LastSuccess != nil {
			status.LastSuccess = *view.LastSuccess
		}
		return status, true
	}
}

// buildInfo labels dismist_build_info.
func buildInfo() metrics.BuildInfo {
	version, revision := buildVersion()
	return metrics.BuildInfo{Version: version, Revision: revision, GoVersion: runtime.Version()}
}

// certificatesFromFlags reads the served chain (-tls-cert, -tls-key) and the
// onboarding CAs (-ap-ca-file, or ca.crt next to the certificate) the
// daemon was started with, for the dismist_tls_certificate_* series. The
// daemon restarts when the certificate is renewed (exit 75), so reading
// them once is enough. It returns nil when the flags are not defined or the
// files cannot be read.
func certificatesFromFlags(flags *flag.FlagSet) []metrics.Certificate {
	value := func(name string) string {
		if f := flags.Lookup(name); f != nil {
			return f.Value.String()
		}
		return ""
	}
	certPath, keyPath := value("tls-cert"), value("tls-key")
	if certPath == "" || keyPath == "" {
		return nil
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil
	}
	cas, err := loadAPCAs(value("ap-ca-file"), certPath)
	if err != nil {
		log.Printf("metrics: onboarding CA certificates: %v", err)
	}
	return metrics.CertificateSeries(pair.Certificate, cas)
}

// apDetail maps one AP session onto the metrics view of telemetry v2 and
// status v2 (design §8.2: B's telemetry.Update v2 and Status v2, A's
// rf_plan). The controller on this branch decodes none of telemetry v2
// yet, so the view is empty and only the series controller.Status,
// TelemetrySnapshot, Counters and Features carry are exported.
//
// INTEGRATION: when telemetry v2 lands, fill metrics.APDetail.Telemetry
// from the session's v2 snapshot field by field (internal/metrics/
// telemetry.go names the pace field behind each one and its unit), Topics,
// Pushes and Verified/VerifyMismatch from Counters and Status v2, and
// PlannedChannels from the AP's static channel or the site's rf_plan.
func apDetail(store model.StateStore) func(string, metrics.Session) metrics.APDetail {
	_ = store
	return func(string, metrics.Session) metrics.APDetail {
		return metrics.APDetail{}
	}
}
