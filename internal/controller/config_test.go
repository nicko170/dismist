package controller

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

func TestBuildConfigReplacesTemplateWLANsAndIncrementsTimestamp(t *testing.T) {
	base := []byte(`{"ID":"old","Name":"old","SiteID":"old-site","Timestamp":1,"WLANs":[{"ID":"old-wlan","SSID":"OLD","Enabled":true,"Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-04","AKM":{"WPA2_PSK":true}}}]}`)
	ap := model.APRecord{ID: "02-aa-00-00-00-03", Name: "example-ap-2", SiteID: "example-site", SecretFile: "secret.bin"}
	networks := []model.Network{
		{ID: "wlan-1", SSID: "TestSSID", PSK: "example-passphrase-03", VLANID: 123, Enabled: true},
		{ID: "wlan-2", SSID: "AP33-GUEST", PSK: "example-passphrase-02", VLANID: 124, Enabled: false},
	}
	raw, err := BuildConfig(base, ap, networks, time.Unix(1794475905, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ID        string  `json:"ID"`
		Name      string  `json:"Name"`
		SiteID    string  `json:"SiteID"`
		Timestamp float64 `json:"Timestamp"`
		WLANs     []struct {
			ID      string   `json:"ID"`
			SSID    string   `json:"SSID"`
			Enabled bool     `json:"Enabled"`
			Vlans   []string `json:"Vlans"`
			Auth    struct {
				PSK string `json:"PSK"`
			} `json:"Auth"`
		} `json:"WLANs"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != ap.ID || got.Name != ap.Name || got.SiteID != ap.SiteID || got.Timestamp != 1794475905 || len(got.WLANs) != 2 {
		t.Fatalf("short config metadata: %+v", got)
	}
	if got.WLANs[0].SSID != "TestSSID" || got.WLANs[0].Vlans[0] != "123" || got.WLANs[0].Auth.PSK != "example-passphrase-03" ||
		got.WLANs[1].SSID != "AP33-GUEST" || got.WLANs[1].Enabled || got.WLANs[1].Vlans[0] != "124" {
		t.Fatalf("short WLANs: %+v", got.WLANs)
	}
}

func TestBuildConfigRejectsMissingWLANTemplate(t *testing.T) {
	_, err := BuildConfig([]byte(`{"WLANs":[]}`), model.APRecord{ID: "ap"}, nil, time.Now())
	if err == nil {
		t.Fatal("accepted missing WLAN template")
	}
}

func TestBuildSiteConfigAppliesTypedCountry(t *testing.T) {
	base := []byte(`{"Country":"GB","WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase"}}]}`)
	site := model.Site{ID: "site-a", Country: "US"}
	ap := model.APRecord{ID: "02-aa-00-00-00-03", SiteID: "site-a", Advanced: json.RawMessage(`{"Country":"CA"}`)}
	raw, err := BuildSiteConfig(base, site, ap, nil, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Country string `json:"Country"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Country != "US" {
		t.Fatalf("AP received country %q instead of its typed site country", got.Country)
	}
}

func TestBuildSiteConfigAppliesTypedSiteName(t *testing.T) {
	base := []byte(`{"SiteName":"old","WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase"}}]}`)
	site := model.Site{ID: "site-a", Name: "Office"}
	ap := model.APRecord{ID: "02-aa-00-00-00-03", SiteID: "site-a", Advanced: json.RawMessage(`{"SiteName":"wrong"}`)}
	raw, err := BuildSiteConfig(base, site, ap, nil, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SiteName string `json:"SiteName"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.SiteName != site.Name {
		t.Fatalf("AP received site name %q instead of %q", got.SiteName, site.Name)
	}
}

func TestSiteSSHKeyEnablesExternalSSHOnBundledTemplate(t *testing.T) {
	base := []byte(`{"SSH":{"Disabled":true,"External":false,"AuthorizedKeys":[]},"RootAuthorizedSSHKeys":[],"WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase"}}]}`)
	ap := model.APRecord{ID: "02-aa-00-00-00-03", SiteID: "site-a"}
	for _, test := range []struct {
		name         string
		keys         []string
		wantDisabled bool
		wantExternal bool
	}{
		{"no site key", nil, true, false},
		{"site key", []string{"ssh-ed25519 example-site-key"}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := BuildSiteConfig(base, model.Site{ID: "site-a", SSHPublicKeys: test.keys}, ap, nil, time.Unix(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				SSH struct {
					Disabled       bool
					External       bool
					AuthorizedKeys []string
				}
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got.SSH.Disabled != test.wantDisabled || got.SSH.External != test.wantExternal || len(got.SSH.AuthorizedKeys) != len(test.keys) {
				t.Fatalf("SSH config with %d site keys: %+v", len(test.keys), got.SSH)
			}
		})
	}
}

func TestBuildSiteConfigAppendsSiteSSHKeysWithoutLosingTemplateKeys(t *testing.T) {
	base := []byte(`{"RootAuthorizedSSHKeys":["ssh-ed25519 base-root"],"SSH":{"Disabled":false,"External":true,"AuthorizedKeys":["ssh-ed25519 base-ssh"]},"WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase-15"}}]}`)
	var site model.Site
	if err := json.Unmarshal([]byte(`{"id":"lab","name":"Lab","ssh_public_keys":["ssh-ed25519 base-root","ssh-ed25519 site-key"]}`), &site); err != nil {
		t.Fatal(err)
	}
	raw, err := BuildSiteConfig(base, site, model.APRecord{ID: "ap-1", SiteID: "lab"}, nil, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		RootAuthorizedSSHKeys []string `json:"RootAuthorizedSSHKeys"`
		SSH                   struct {
			Disabled       bool     `json:"Disabled"`
			External       bool     `json:"External"`
			AuthorizedKeys []string `json:"AuthorizedKeys"`
		} `json:"SSH"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ssh-ed25519 base-root", "ssh-ed25519 site-key"}; !reflect.DeepEqual(got.RootAuthorizedSSHKeys, want) {
		t.Fatalf("root keys = %q, want %q", got.RootAuthorizedSSHKeys, want)
	}
	if want := []string{"ssh-ed25519 base-ssh", "ssh-ed25519 base-root", "ssh-ed25519 site-key"}; !reflect.DeepEqual(got.SSH.AuthorizedKeys, want) {
		t.Fatalf("SSH keys = %q, want %q", got.SSH.AuthorizedKeys, want)
	}
	if got.SSH.Disabled || !got.SSH.External {
		t.Fatalf("SSH flags changed: %+v", got.SSH)
	}
}

func TestBuildSiteConfigCarriesEnabledWLANVLANsOnEthernetPorts(t *testing.T) {
	base := []byte(`{"SwitchPorts":[{"Name":"eth0","PortVlanID":"1","Vlans":["1"]},{"Name":"eth1","PortVlanID":"1","Vlans":["1","123"]}],"WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase-14"}}]}`)
	ap := model.APRecord{ID: "02-aa-00-00-00-03", SiteID: "lab"}
	networks := []model.Network{
		{ID: "lab", SSID: "TestSSID", PSK: "example-passphrase-07", VLANID: 123, Enabled: true},
		{ID: "future", SSID: "FUTURE", PSK: "example-passphrase-08", VLANID: 124, Enabled: false},
	}
	raw, err := BuildSiteConfig(base, model.Site{ID: "lab"}, ap, networks, time.Unix(1794475905, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SwitchPorts []struct {
			Name       string   `json:"Name"`
			PortVlanID string   `json:"PortVlanID"`
			Vlans      []string `json:"Vlans"`
		} `json:"SwitchPorts"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.SwitchPorts) != 2 {
		t.Fatalf("switch ports = %+v", got.SwitchPorts)
	}
	for _, port := range got.SwitchPorts {
		if port.PortVlanID != "1" || len(port.Vlans) != 2 || port.Vlans[0] != "1" || port.Vlans[1] != "123" {
			t.Fatalf("%s does not carry the active WLAN VLAN exactly once: %+v", port.Name, port)
		}
	}
}
