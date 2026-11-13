package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestBoltMigratesLegacyStateOnceAndKeepsSiteOwnership(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.json")
	dbPath := filepath.Join(dir, "controller.db")
	legacy := State{
		Revision: 3, UpdatedAt: time.Now().UTC(),
		APs:      []APRecord{{ID: "02-aa-00-00-00-03", Name: "bench", SiteID: "site-a", SecretFile: "secret.bin"}},
		Networks: []Network{{ID: "wlan-a", SSID: "TestSSID", PSK: "example-passphrase-11", VLANID: 123, Enabled: true}},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(legacyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenBolt(dbPath, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot()
	if len(got.Sites) != 1 || got.Sites[0].ID != "site-a" || got.Networks[0].SiteID != "site-a" ||
		got.Networks[0].PSK != "example-passphrase-11" || got.Revision != 3 {
		t.Fatalf("migration lost site or WLAN: %+v", got)
	}
	info, _ := os.Stat(dbPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database permissions %v", info.Mode().Perm())
	}
	for _, name := range []string{"sites", "aps", "networks", "templates", "ap_status", "client_sessions", "samples", "events", "meta"} {
		if err := store.db.View(func(tx *bolt.Tx) error {
			if tx.Bucket([]byte(name)) == nil {
				t.Fatalf("missing bucket %s", name)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenBolt(dbPath, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if snapshot := reopened.Snapshot(); snapshot.Revision != 3 || len(snapshot.Sites) != 1 || len(snapshot.Networks) != 1 {
		t.Fatalf("migration repeated or state lost: %+v", snapshot)
	}
}

func TestBoltUpdateIsTransactionalAndSiteScoped(t *testing.T) {
	store, err := OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "Lab A"}, {ID: "site-b", Name: "Lab B"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "a.bin"}, {ID: "ap-b", SiteID: "site-b", SecretFile: "b.bin"}}
		state.Networks = []Network{{ID: "wlan-a", SiteID: "site-a", SSID: "A", PSK: "example-passphrase-09", VLANID: 123}, {ID: "wlan-b", SiteID: "site-b", SSID: "B", PSK: "example-passphrase-10", VLANID: 124}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.APs[0].SiteID = "missing-site"
		return nil
	}); err == nil {
		t.Fatal("accepted AP with missing site")
	}
	if got := store.Snapshot(); got.Revision != 1 || got.APs[0].SiteID != "site-a" || len(got.Networks) != 2 {
		t.Fatalf("failed update changed database: %+v", got)
	}
	if err := store.Update(func(state *State) error {
		state.Sites[0].Advanced = json.RawMessage(`[1,2]`)
		return nil
	}); err == nil {
		t.Fatal("accepted non-object advanced patch")
	}
}

func TestUnrelatedSiteUpdateBackfillsLegacyAPConfigVersion(t *testing.T) {
	store, err := OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "A"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "secret.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	if err := store.Update(func(state *State) error {
		state.Sites = append(state.Sites, Site{ID: "site-b", Name: "B"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := store.Snapshot()
	if after.Revision != 2 || after.APs[0].ConfigRevision != 1 || !after.APs[0].ConfigUpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("site creation changed legacy AP config version: %+v", after.APs[0])
	}
}

func TestOpenBoltBackfillsExistingAPConfigVersionWithoutStateEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "A"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "secret.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	reopened, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state := reopened.Snapshot()
	if state.Revision != 1 || state.APs[0].ConfigRevision != 1 || !state.APs[0].ConfigUpdatedAt.Equal(state.UpdatedAt) {
		t.Fatalf("existing AP config version not backfilled: %+v", state)
	}
}
