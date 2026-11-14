package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nicko170/dismist/internal/model"
)

func TestImportSecretFilesMovesLegacySecretsIntoDatabase(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.bin")
	if err := os.WriteFile(good, bytes.Repeat([]byte{9}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	readable := filepath.Join(dir, "readable.bin")
	if err := os.WriteFile(readable, bytes.Repeat([]byte{8}, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := model.OpenBolt(filepath.Join(dir, "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "s", Name: "S"}}
		state.APs = []model.APRecord{
			{ID: "ap-good", SiteID: "s", SecretFile: good},
			{ID: "ap-missing", SiteID: "s", SecretFile: filepath.Join(dir, "missing.bin")},
			{ID: "ap-readable", SiteID: "s", SecretFile: readable},
			{ID: "ap-both", SiteID: "s", SecretFile: good, Secret: bytes.Repeat([]byte{1}, 32)},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := importSecretFiles(store); err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.APRecord{}
	for _, ap := range store.Snapshot().APs {
		byID[ap.ID] = ap
	}
	if ap := byID["ap-good"]; !bytes.Equal(ap.Secret, bytes.Repeat([]byte{9}, 32)) || ap.SecretFile != "" {
		t.Fatalf("good secret not imported: %+v", ap)
	}
	for _, id := range []string{"ap-missing", "ap-readable"} {
		if ap := byID[id]; len(ap.Secret) != 0 || ap.SecretFile == "" {
			t.Fatalf("%s: unusable secret file imported or forgotten: %+v", id, ap)
		}
	}
	if ap := byID["ap-both"]; !bytes.Equal(ap.Secret, bytes.Repeat([]byte{1}, 32)) || ap.SecretFile != "" {
		t.Fatalf("existing database secret replaced by file: %+v", ap)
	}
	// Only unusable files remain: a retry commits nothing.
	revision := store.Snapshot().Revision
	if err := importSecretFiles(store); err != nil || store.Snapshot().Revision != revision {
		t.Fatalf("retry with nothing importable: err=%v revision %d -> %d", err, revision, store.Snapshot().Revision)
	}
	if ready := withSecrets(store.Snapshot()); len(ready.APs) != 2 {
		t.Fatalf("APs with secrets = %d, want 2", len(ready.APs))
	}
}
