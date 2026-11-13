package model

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRejectsInvalidSiteSSHPublicKeys(t *testing.T) {
	for _, candidate := range []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"ssh-ed25519 not-base64",
		"ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB",
	} {
		t.Run(candidate, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"id": "site-a", "name": "A", "ssh_public_keys": []string{candidate}})
			var site Site
			if err := json.Unmarshal(raw, &site); err != nil {
				t.Fatal(err)
			}
			if err := store.Update(func(state *State) error { state.Sites = []Site{site}; return nil }); err == nil {
				t.Fatal("accepted invalid SSH public key")
			}
		})
	}
}

func TestStorePersistsPrivateNetworkStateAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *State) error {
		if state.UpdatedAt.IsZero() {
			t.Fatal("update callback needs its new timestamp for config generation")
		}
		state.APs = append(state.APs, APRecord{ID: "02-aa-00-00-00-03", SecretFile: "secret.bin"})
		state.Networks = append(state.Networks, Network{
			ID: "lab", SSID: "TestSSID", PSK: "example-passphrase-13", VLANID: 123, Enabled: true,
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file must be private: %v %v", info, err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Snapshot()
	if got.Revision != 1 || got.UpdatedAt.IsZero() || len(got.Networks) != 1 || got.Networks[0].PSK != "example-passphrase-13" || got.Networks[0].VLANID != 123 {
		t.Fatalf("state lost after restart: %+v", got)
	}
}

func TestRejectedUpdatePreservesLastGoodRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, _ := Open(path)
	if err := store.Update(func(state *State) error {
		state.Networks = []Network{{ID: "lab", SSID: "TestSSID", PSK: "example-passphrase-13", VLANID: 123, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := store.Update(func(state *State) error {
		state.Networks[0].VLANID = 4095
		return nil
	}); err == nil {
		t.Fatal("accepted invalid VLAN")
	}
	if err := store.Update(func(state *State) error {
		state.Networks[0].SSID = "should-not-save"
		return errors.New("apply failed")
	}); err == nil {
		t.Fatal("accepted failed update")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) || store.Snapshot().Revision != 1 || store.Snapshot().Networks[0].VLANID != 123 {
		t.Fatal("failed update changed last good state")
	}
}
