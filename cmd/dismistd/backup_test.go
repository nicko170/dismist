package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

func TestParseByteSize(t *testing.T) {
	for input, want := range map[string]int64{
		"": 0, "0": 0, "1048576": 1 << 20, "600Mi": 600 << 20, "2Gi": 2 << 30, "512Ki": 512 << 10, "1G": 1e9, "5M": 5e6,
	} {
		got, err := parseByteSize(input)
		if err != nil || got != want {
			t.Fatalf("parseByteSize(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, bad := range []string{"-1", "lots", "1.5Gi", "Gi"} {
		if _, err := parseByteSize(bad); err == nil {
			t.Fatalf("parseByteSize(%q) accepted", bad)
		}
	}
}

func TestBackupFlagsDefaults(t *testing.T) {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	config := registerBackupFlags(flags)
	if err := flags.Parse([]string{"-backup-max-bytes=600Mi", "-backup-tmp-max=1Gi"}); err != nil {
		t.Fatal(err)
	}
	if err := config.resolve("/state/controller.db"); err != nil {
		t.Fatal(err)
	}
	if config.dir != "/state/backups" || config.interval != 24*time.Hour || config.keep != 7 || config.maxBytes != 600<<20 || config.tmpMax != 1<<30 {
		t.Fatalf("defaults %+v", config)
	}
	for _, args := range [][]string{{"-backup-keep=0"}, {"-backup-interval=10s"}, {"-backup-interval=-1h"}} {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		config := registerBackupFlags(flags)
		if err := flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		if config.resolve("/state/controller.db") == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func openTestStore(t *testing.T) *model.BoltStore {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func snapshotNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestScheduledSnapshotsWriteAndRetain(t *testing.T) {
	store := openTestStore(t)
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Three older snapshots; one unrelated file that retention ignores.
	for _, name := range []string{"controller-20261101T000000Z.db", "controller-20261102T000000Z.db", "controller-20261103T000000Z.db", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", 1000)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var warnings []string
	scheduler := newBackupScheduler(store, backupConfig{dir: dir, interval: time.Hour, keep: 2}, func(reason, message string) {
		warnings = append(warnings, reason)
	})
	scheduler.usage = func(string) (model.DiskSpace, error) { return model.DiskSpace{Total: 1 << 30, Free: 1 << 29}, nil }
	scheduler.now = func() time.Time { return time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC) }
	if err := scheduler.RunOnce(); err != nil {
		t.Fatal(err)
	}
	names := snapshotNames(t, dir)
	want := []string{"controller-20261103T000000Z.db", "controller-20261114T030000Z.db", "notes.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("after retention: %v, want %v", names, want)
	}
	info, err := os.Stat(filepath.Join(dir, "controller-20261114T030000Z.db"))
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		t.Fatalf("snapshot %v %v", info, err)
	}
	status := scheduler.Status()
	if status.Count != 2 || status.LastSuccess == nil || status.Keep != 2 || status.Dir != dir || status.UsedBytes < info.Size() ||
		status.MaxBytes != int64((1<<30)/100*30) || status.LastError != "" || status.Interval != "1h0m0s" || len(warnings) != 0 {
		t.Fatalf("status %+v warnings %v", status, warnings)
	}
	// A size cap below the snapshots' total deletes the oldest, never the
	// newest.
	scheduler.config.maxBytes = 1
	scheduler.now = func() time.Time { return time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC) }
	if err := scheduler.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if names := snapshotNames(t, dir); strings.Join(names, ",") != "controller-20261115T030000Z.db,notes.txt" {
		t.Fatalf("after size cap: %v", names)
	}
}

func TestScheduledSnapshotSkippedWhenSpaceIsShort(t *testing.T) {
	store := openTestStore(t)
	dir := filepath.Join(t.TempDir(), "backups")
	var warnings []string
	scheduler := newBackupScheduler(store, backupConfig{dir: dir, interval: time.Hour, keep: 7}, func(reason, message string) {
		warnings = append(warnings, reason+": "+message)
	})
	// 2 GiB volume with 600 MiB free: a copy would leave less than 25 %.
	scheduler.usage = func(string) (model.DiskSpace, error) {
		return model.DiskSpace{Total: 2 << 30, Free: 400 << 20}, nil
	}
	if err := scheduler.RunOnce(); err == nil {
		t.Fatal("snapshot written with short space")
	}
	if names := snapshotNames(t, dir); len(names) != 0 {
		t.Fatalf("files written: %v", names)
	}
	if failures := scheduler.Failures(); failures[backupFailSpace] != 1 || len(warnings) != 1 || !strings.HasPrefix(warnings[0], "space: ") {
		t.Fatalf("failures %v warnings %v", failures, warnings)
	}
	if status := scheduler.Status(); status.LastError == "" || status.LastSuccess != nil {
		t.Fatalf("status after skip %+v", status)
	}
}

func TestBackupWarningEventReachesEverySite(t *testing.T) {
	store := openTestStore(t)
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	backupWarning(store)("space", "snapshot skipped")
	for _, site := range []string{"a", "b"} {
		events, _, err := store.Events2().List(model.EventQuery{SiteID: site})
		if err != nil || len(events) != 1 || events[0].Kind != "backup" || events[0].Severity != model.SeverityWarning {
			t.Fatalf("site %s events %+v %v", site, events, err)
		}
	}
}

func TestFirstRunFollowsTheNewestSnapshot(t *testing.T) {
	store := openTestStore(t)
	dir := t.TempDir()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	scheduler := newBackupScheduler(store, backupConfig{dir: dir, interval: 24 * time.Hour, keep: 7}, nil)
	scheduler.now = func() time.Time { return now }
	if got := scheduler.firstRun(); !got.Equal(now.Add(backupFirstDelay)) {
		t.Fatalf("no snapshots: first run %v", got)
	}
	recent := filepath.Join(dir, "controller-20261114T060000Z.db")
	if err := os.WriteFile(recent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(recent, now.Add(-6*time.Hour), now.Add(-6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.firstRun(); !got.Equal(now.Add(18 * time.Hour)) {
		t.Fatalf("recent snapshot: first run %v", got)
	}
}

func writeCAFile(t *testing.T, path string, isCA bool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: isCA, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAPCAs(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	if cas, err := loadAPCAs("", certPath); err != nil || cas != nil {
		t.Fatalf("no ca.crt: %v %v", cas, err)
	}
	writeCAFile(t, filepath.Join(dir, "ca.crt"), true)
	if cas, err := loadAPCAs("", certPath); err != nil || len(cas) != 1 {
		t.Fatalf("default ca.crt: %d %v", len(cas), err)
	}
	leaf := filepath.Join(dir, "leaf.pem")
	writeCAFile(t, leaf, false)
	if _, err := loadAPCAs(leaf, certPath); err == nil {
		t.Fatal("a non-CA certificate was accepted")
	}
	if _, err := loadAPCAs(filepath.Join(dir, "missing.pem"), certPath); err == nil {
		t.Fatal("a missing -ap-ca-file was accepted")
	}
}
