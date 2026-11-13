package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

func TestSiteSSHKeyUpdateRebuildsOnlyThatSitesAPConfig(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}}
		state.APs = []model.APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "a.bin"}, {ID: "ap-b", SiteID: "site-b", SecretFile: "b.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"RootAuthorizedSSHKeys":["base-root"],"SSH":{"Disabled":false,"External":true,"AuthorizedKeys":["base-ssh"]},"WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase-15"}}]}`)
	a, _ := controller.NewServer(controller.Options{AllowedIP: "127.0.0.1", ID: "ap-a", CertDER: []byte{1}, Config: base, Revision: 1})
	b, _ := controller.NewServer(controller.Options{AllowedIP: "127.0.0.2", ID: "ap-b", CertDER: []byte{1}, Config: base, Revision: 1})
	manager := controller.NewManager()
	_ = manager.Register("ap-a", "127.0.0.1", a)
	_ = manager.Register("ap-b", "127.0.0.2", b)
	handler := NewWithManager(store, manager, base, "ap-a")
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB key-one"
	response := request(t, handler, http.MethodPut, "/api/sites/site-b/ssh-keys", `{"keys":["`+key+`"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("key update failed: %d %s", response.Code, response.Body.String())
	}
	configA, revisionA := a.ConfigSnapshot()
	configB, revisionB := b.ConfigSnapshot()
	if revisionA != 1 || revisionB != 2 || bytes.Contains(configA, []byte("AAAAC3Nza")) || !bytes.Contains(configB, []byte("AAAAC3Nza")) {
		t.Fatalf("site key changed short AP: revA=%d revB=%d", revisionA, revisionB)
	}
	var configured struct {
		RootAuthorizedSSHKeys []string `json:"RootAuthorizedSSHKeys"`
		SSH                   struct {
			AuthorizedKeys []string `json:"AuthorizedKeys"`
		} `json:"SSH"`
	}
	if err := json.Unmarshal(configB, &configured); err != nil {
		t.Fatal(err)
	}
	if len(configured.RootAuthorizedSSHKeys) != 2 || len(configured.SSH.AuthorizedKeys) != 2 || configured.RootAuthorizedSSHKeys[0] != "base-root" || configured.SSH.AuthorizedKeys[0] != "base-ssh" {
		t.Fatalf("base keys not preserved in AP B config")
	}
	got := request(t, handler, http.MethodGet, "/api/sites/site-b/ssh-keys", "")
	if got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte("AAAAC3Nza")) {
		t.Fatalf("site keys unavailable: %d", got.Code)
	}
	bad := request(t, handler, http.MethodPut, "/api/sites/site-b/ssh-keys", `{"keys":["-----BEGIN OPENSSH PRIVATE KEY-----"]}`)
	if bad.Code != http.StatusBadRequest || store.Snapshot().Revision != 2 || bytes.Contains(bad.Body.Bytes(), []byte("OPENSSH")) {
		t.Fatalf("private key accepted or echoed: status=%d revision=%d", bad.Code, store.Snapshot().Revision)
	}
}

func TestSiteEditRebuildsOnlyAPsInThatSite(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}}
		state.APs = []model.APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: "a.bin"}, {ID: "ap-b", SiteID: "site-b", SecretFile: "b.bin"}}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "A", PSK: "example-passphrase-09", VLANID: 123}, {ID: "wlan-b", SiteID: "site-b", SSID: "B", PSK: "example-passphrase-10", VLANID: 124}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"old","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-05"}}]}`)
	a, _ := controller.NewServer(controller.Options{AllowedIP: "127.0.0.1", ID: "ap-a", CertDER: []byte{1}, Config: base, Revision: 1})
	b, _ := controller.NewServer(controller.Options{AllowedIP: "127.0.0.2", ID: "ap-b", CertDER: []byte{1}, Config: base, Revision: 1})
	manager := controller.NewManager()
	_ = manager.Register("ap-a", "127.0.0.1", a)
	_ = manager.Register("ap-b", "127.0.0.2", b)
	handler := NewWithManager(store, manager, base, "ap-a")
	response := request(t, handler, http.MethodPut, "/api/sites/site-b/networks/wlan-b", `{"ssid":"B-NEW"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("site B edit failed: %d %s", response.Code, response.Body.String())
	}
	configA, revisionA := a.ConfigSnapshot()
	configB, revisionB := b.ConfigSnapshot()
	if revisionA != 1 || revisionB != 2 || bytes.Contains(configA, []byte("B-NEW")) || !bytes.Contains(configB, []byte("B-NEW")) {
		t.Fatalf("cross-site config change: revA=%d revB=%d", revisionA, revisionB)
	}
	statusB := request(t, handler, http.MethodGet, "/api/sites/site-b/aps/ap-b/status", "")
	if statusB.Code != http.StatusOK || !bytes.Contains(statusB.Body.Bytes(), []byte(`"desired_revision":2`)) {
		t.Fatalf("site B AP status missing: %d %s", statusB.Code, statusB.Body.String())
	}
	shortStatus := request(t, handler, http.MethodGet, "/api/sites/site-a/aps/ap-b/status", "")
	if shortStatus.Code != http.StatusNotFound {
		t.Fatalf("cross-site AP status accessible: %d", shortStatus.Code)
	}
	moved := request(t, handler, http.MethodPut, "/api/aps/ap-a/site", `{"site_id":"site-b"}`)
	if moved.Code != http.StatusOK {
		t.Fatalf("AP move failed: %d %s", moved.Code, moved.Body.String())
	}
	configA, revisionA = a.ConfigSnapshot()
	_, revisionB = b.ConfigSnapshot()
	if revisionA != 3 || revisionB != 2 || !bytes.Contains(configA, []byte("B-NEW")) ||
		store.Snapshot().APs[0].SiteID != "site-b" {
		t.Fatalf("moved AP did not receive destination site config: revA=%d revB=%d", revisionA, revisionB)
	}
}
