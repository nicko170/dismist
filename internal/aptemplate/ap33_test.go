package aptemplate

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

func TestBundledAP33TemplateIsCompleteAndSanitized(t *testing.T) {
	base := Default()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(base, &root); err != nil {
		t.Fatal(err)
	}
	if len(root) < 30 {
		t.Fatalf("AP33 base config lost too many fields: %d", len(root))
	}
	for _, key := range []string{"Radios", "WLANs", "SVIs", "SSH", "SwitchPorts", "Monitor", "Stats", "ProxyArp", "APRecovery"} {
		if len(root[key]) == 0 {
			t.Fatalf("missing AP33 setting %s", key)
		}
	}
	var radios, ports, wlans []json.RawMessage
	for _, item := range []struct {
		key    string
		output *[]json.RawMessage
	}{{"Radios", &radios}, {"SwitchPorts", &ports}, {"WLANs", &wlans}} {
		if err := json.Unmarshal(root[item.key], item.output); err != nil {
			t.Fatal(err)
		}
	}
	if len(radios) != 2 || len(ports) != 2 || len(wlans) != 1 {
		t.Fatalf("AP33 base structure: %d radios, %d ports, %d WLANs", len(radios), len(ports), len(wlans))
	}
	for _, key := range []string{"ID", "UUID", "OrgID", "SiteID", "Name", "SiteName", "Hostname", "PAPIOpaque"} {
		if !bytes.Equal(root[key], []byte(`""`)) {
			t.Fatalf("%s contains an identifier or name", key)
		}
	}
	if !bytes.Equal(root["Country"], []byte(`""`)) {
		t.Fatal("template forces a regulatory country")
	}
	for _, radioRaw := range radios {
		var radioFields map[string]json.RawMessage
		if err := json.Unmarshal(radioRaw, &radioFields); err != nil {
			t.Fatal(err)
		}
		if _, present := radioFields["Power"]; present {
			t.Fatal("template forces a radio transmit power")
		}
		var rrmFields map[string]json.RawMessage
		if err := json.Unmarshal(radioFields["RRM"], &rrmFields); err != nil {
			t.Fatal(err)
		}
		if _, present := rrmFields["Power"]; present {
			t.Fatal("template forces an RRM transmit power")
		}
		var radio struct {
			Channel     int
			CCRev       string
			CCRevSP     string
			RRM         struct{ Channel int }
			DFSjumplist struct{ ChanSpecs []json.RawMessage }
		}
		if err := json.Unmarshal(radioRaw, &radio); err != nil || radio.Channel != 0 || radio.RRM.Channel != 0 || radio.CCRev != "" || radio.CCRevSP != "" || len(radio.DFSjumplist.ChanSpecs) != 0 {
			t.Fatal("template forces a regional radio channel or country revision")
		}
	}
	for _, key := range []string{"RootAuthorizedSSHKeys", "OurOUIs", "CnaHostnames"} {
		if !bytes.Equal(root[key], []byte(`[]`)) {
			t.Fatalf("%s contains local or device information", key)
		}
	}
	var wlan struct {
		ID      string
		SSID    string
		Enabled bool
		Auth    struct{ PSK string }
	}
	if err := json.Unmarshal(wlans[0], &wlan); err != nil {
		t.Fatal(err)
	}
	if wlan.ID != "" || wlan.SSID != "" || wlan.Auth.PSK != "" || wlan.Enabled {
		t.Fatal("WLAN template contains a live WLAN identity or PSK")
	}
	var templateWLAN struct{ Vlans []string }
	if err := json.Unmarshal(wlans[0], &templateWLAN); err != nil || len(templateWLAN.Vlans) != 0 {
		t.Fatal("WLAN template contains a local VLAN")
	}
	var ssh struct{ AuthorizedKeys []string }
	if err := json.Unmarshal(root["SSH"], &ssh); err != nil || len(ssh.AuthorizedKeys) != 0 {
		t.Fatal("SSH template contains authorized keys")
	}
	var svi []struct{ DHCP struct{ Hostname string } }
	if err := json.Unmarshal(root["SVIs"], &svi); err != nil || len(svi) != 1 || svi[0].DHCP.Hostname != "" {
		t.Fatal("SVI contains a local hostname")
	}
	if regexp.MustCompile(`(?i)(ssh-(rsa|ed25519)|-----BEGIN|(?:\d{1,3}\.){3}\d{1,3}|(?:[0-9a-f]{2}:){5}[0-9a-f]{2})`).Match(base) {
		t.Fatal("template contains a key, IP address, or MAC address")
	}
}

func TestBundledAP33TemplateBuildsWorkingWLAN(t *testing.T) {
	ap := model.APRecord{ID: "02-aa-00-00-00-03", Name: "example-ap", SiteID: "example-site"}
	network := model.Network{ID: "example-wlan", SiteID: ap.SiteID, SSID: "ExampleSSID", PSK: "example-passphrase-04", VLANID: 123, Enabled: true}
	raw, err := controller.BuildSiteConfig(Default(), model.Site{ID: ap.SiteID}, ap, []model.Network{network}, time.Unix(1794475905, 0))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		ID    string
		WLANs []struct {
			ID    string
			SSID  string
			Vlans []string
			Auth  struct {
				PSK  string
				Type string
				AKM  map[string]bool
			}
			BandConfigs []json.RawMessage
		}
		SwitchPorts []struct{ Vlans []string }
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.ID != ap.ID || len(config.WLANs) != 1 || config.WLANs[0].ID != network.ID || config.WLANs[0].SSID != network.SSID || config.WLANs[0].Auth.PSK != network.PSK || config.WLANs[0].Auth.Type != "psk" || !config.WLANs[0].Auth.AKM["WPA2_PSK"] || len(config.WLANs[0].BandConfigs) != 2 || len(config.WLANs[0].Vlans) != 1 || config.WLANs[0].Vlans[0] != "123" {
		t.Fatalf("generated WLAN is incomplete: %+v", config.WLANs)
	}
	if len(config.SwitchPorts) != 2 || len(config.SwitchPorts[0].Vlans) != 2 || config.SwitchPorts[0].Vlans[1] != "123" {
		t.Fatalf("WLAN VLAN missing from switch ports: %+v", config.SwitchPorts)
	}
}
