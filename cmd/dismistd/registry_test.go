package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

func TestPreparedAPsShareSiteConfigAndKeepSeparateSecrets(t *testing.T) {
	dir := t.TempDir()
	secret := func(name string, value byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, bytes.Repeat([]byte{value}, 32), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	state := model.State{
		Revision: 4, UpdatedAt: time.Unix(1794475905, 0),
		Sites: []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}},
		APs: []model.APRecord{
			{ID: "ap-a", SiteID: "site-a", SecretFile: secret("a.bin", 1)},
			{ID: "ap-c", SiteID: "site-a", ManagementIP: "127.0.0.3", SecretFile: secret("c.bin", 3)},
			{ID: "ap-b", SiteID: "site-b", ManagementIP: "127.0.0.2", SecretFile: secret("b.bin", 2)},
		},
		Networks: []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "A-WLAN", PSK: "example-passphrase-09", VLANID: 123}, {ID: "wlan-b", SiteID: "site-b", SSID: "B-WLAN", PSK: "example-passphrase-10", VLANID: 124}},
	}
	base := []byte(`{"ID":"old","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-05"}}]}`)
	manager, addresses, err := prepareAPs(state, registryOptions{DefaultID: "ap-a", DefaultIP: "127.0.0.1", Base: base, CertDER: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 3 {
		t.Fatalf("short AP address count %d", len(addresses))
	}
	for _, id := range []string{"ap-a", "ap-c"} {
		control, ok := manager.Server(id)
		if !ok {
			t.Fatalf("site A AP %s missing", id)
		}
		config, revision := control.ConfigSnapshot()
		if revision != 4 || !bytes.Contains(config, []byte("A-WLAN")) || bytes.Contains(config, []byte("B-WLAN")) {
			t.Fatalf("site A AP %s received short config", id)
		}
	}
	controlB, ok := manager.Server("ap-b")
	if !ok {
		t.Fatal("site B AP missing")
	}
	configB, _ := controlB.ConfigSnapshot()
	if !bytes.Contains(configB, []byte("B-WLAN")) || bytes.Contains(configB, []byte("A-WLAN")) {
		t.Fatal("site B AP received site A config")
	}
}

func TestUnrelatedSiteChangeDoesNotBumpAPConfigOnRestart(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret.bin")
	if err := os.WriteFile(secretPath, bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	configTime := time.Unix(1794475905, 0)
	state := model.State{
		Revision: 4, UpdatedAt: configTime.Add(time.Hour),
		Sites:    []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}},
		APs:      []model.APRecord{{ID: "ap-a", SiteID: "site-a", SecretFile: secretPath, ConfigRevision: 3, ConfigUpdatedAt: configTime}},
		Networks: []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "A-WLAN", PSK: "example-passphrase-09", VLANID: 123}},
	}
	base := []byte(`{"ID":"old","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-05"}}]}`)
	manager, _, err := prepareAPs(state, registryOptions{DefaultID: "ap-a", DefaultIP: "127.0.0.1", Base: base, CertDER: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	control, _ := manager.Server("ap-a")
	config, revision := control.ConfigSnapshot()
	if revision != 3 || !bytes.Contains(config, []byte(`"Timestamp":1794475905`)) {
		t.Fatalf("unrelated site edit changed AP config revision or timestamp: %d %s", revision, config)
	}
}

func TestPrepareAPsAllowsFreshEmptyInventory(t *testing.T) {
	manager, addresses, err := prepareAPs(model.State{}, registryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || len(manager.Servers()) != 0 || len(addresses) != 0 {
		t.Fatalf("fresh inventory should have no AP servers or DNS addresses: %v %v", manager, addresses)
	}
}

func TestPrepareAPsSkipsUnassignedAP(t *testing.T) {
	state := model.State{
		Sites: []model.Site{{ID: "office", Name: "Office"}},
		APs:   []model.APRecord{{ID: "ap-pending", ManagementIP: "127.0.0.4", SecretFile: "/not/ready/secret.bin"}},
	}
	manager, addresses, err := prepareAPs(state, registryOptions{})
	if err != nil || len(addresses) != 0 || len(manager.Servers()) != 0 {
		t.Fatalf("unassigned AP should remain inactive: addresses %v, err %v", addresses, err)
	}
}
