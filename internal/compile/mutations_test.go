package compile

import (
	"errors"
	"testing"

	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options"
)

func TestNewObjectDefaultsCompile(t *testing.T) {
	site := model.Site{ID: typedSite, Name: "New", Country: "AU"}
	NewSiteDefaults(&site)
	state := model.State{Schema: 2, Sites: []model.Site{site},
		APs: []model.APRecord{{ID: typedAP, SiteID: typedSite, Secret: make([]byte, 32)}}}
	network := model.Network{ID: typedNet, SiteID: typedSite, SSID: "Example", PSK: "example-passphrase-01", Enabled: true}
	if err := NewNetworkDefaults(state, &network); err != nil {
		t.Fatal(err)
	}
	if network.BSSIDIndex != 1 || network.Security != model.SecurityWPA2WPA3Personal || network.IdleTimeout != 1800 || network.BandSteering {
		t.Fatalf("network defaults %+v", network)
	}
	state.Networks = []model.Network{network}
	if _, err := Apply(&state, &memDesired{}, ApplyOptions{Template: legacyv0.BundledTemplate(), Now: typedTime}); err == nil {
		// Without an initial assignment the fallback compiles and warns.
	} else {
		t.Fatal(err)
	}
	if !site.RF.Managed || site.APDefaults.PoEConstrained != model.PoEKeepRadios || !site.APDefaults.HostnameFromName {
		t.Fatalf("site defaults %+v", site)
	}
}

func TestRoamingKeyAndBlockedClients(t *testing.T) {
	state := newTypedState()
	state.Networks[0].Roaming.APKeyCache = true
	changed, err := EnsureRoamingKeys(&state)
	if err != nil || len(changed) != 1 || len(state.Sites[0].Roaming.KeyEncryptKey) != 64 {
		t.Fatalf("roaming key %v %v", changed, err)
	}
	key := state.Sites[0].Roaming.KeyEncryptKey
	if again, _ := EnsureRoamingKeys(&state); len(again) != 0 {
		t.Fatal("key regenerated")
	}
	_, root := compileTypedAP(t, state, typedAP)
	wlan := root["WLANs"].([]any)[0].(map[string]any)
	if wlan["KeyCache"].(map[string]any)["KeyEncryptKey"] != key || wlan["KeyCache"].(map[string]any)["LocalAPkeyCache"] != true {
		t.Fatalf("key cache %v", wlan["KeyCache"])
	}
	if err := RotateRoamingKey(&state, typedSite); err != nil || state.Sites[0].Roaming.KeyEncryptKey == key {
		t.Fatal("rotate")
	}
	if added, err := BlockClient(&state, typedSite, "02:AA:00:00:00:99", "noisy"); err != nil || !added {
		t.Fatal(err)
	}
	if added, _ := BlockClient(&state, typedSite, "02aa00000099", ""); added {
		t.Fatal("blocked twice")
	}
	_, root = compileTypedAP(t, state, typedAP)
	if macs := root["BlackList"].(map[string]any)["WCMacs"].([]any); len(macs) != 1 || macs[0] != "02-aa-00-00-00-99" {
		t.Fatalf("WCMacs %v", macs)
	}
	if removed, _ := UnblockClient(&state, typedSite, "02-aa-00-00-00-99"); !removed {
		t.Fatal("unblock")
	}
	if _, err := NormaliseMAC("nope"); err == nil {
		t.Fatal("bad MAC accepted")
	}
}

func TestExperimentalGate(t *testing.T) {
	before := newTypedState()
	after := newTypedState()
	after.Networks[0].Security = model.SecurityOWE
	after.Networks[0].PSK = ""
	err := CheckExperimental(before, after, typedSite)
	var experimental *options.ExperimentalError
	if !errors.As(err, &experimental) || experimental.Keys[0] != "wlan.security" || experimental.Code() != "experimental_disabled" {
		t.Fatalf("OWE without experimental: %v", err)
	}
	after.Sites[0].Experimental = true
	if err := CheckExperimental(before, after, typedSite); err != nil {
		t.Fatal(err)
	}
	// A stored experimental value that does not change is kept.
	if err := CheckExperimental(after, func() model.State { s := after; s.Sites = []model.Site{before.Sites[0]}; return s }(), typedSite); err != nil {
		t.Fatalf("unchanged stored value refused: %v", err)
	}
	if uses := ExperimentalInUse(newTypedState(), typedSite); len(uses) != 0 {
		t.Fatalf("defaults are experimental: %+v", uses)
	}
}

func TestImpactDiffAndDisruption(t *testing.T) {
	state := newTypedState()
	_, _ = compileTypedAP(t, state, typedAP)
	input, _ := InputFor(legacyv0.BundledTemplate(), state, typedAP)
	before, _ := AP(input)
	state.Networks[0].Hidden = true
	state.Networks[0].PSK = "another-passphrase-01"
	state.Sites[0].APDefaults.LEDBrightness = 10
	input, _ = InputFor(legacyv0.BundledTemplate(), state, typedAP)
	after, _ := AP(input)
	impact, err := Impact(before.Config, after.Config)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]DiffEntry{}
	for _, entry := range impact.Diff {
		paths[entry.Path] = entry
	}
	if _, ok := paths["WLANs[ID="+typedNet+"].Hidden"]; !ok {
		t.Fatalf("diff %+v", impact.Diff)
	}
	if entry := paths["WLANs[ID="+typedNet+"].Auth.PSK"]; entry.To != "[changed]" {
		t.Fatalf("secret not redacted: %+v", entry)
	}
	if len(impact.Disruption) != 2 || impact.Disruption[0] != "none" || impact.Disruption[1] != "wlan_restart" {
		t.Fatalf("disruption %v", impact.Disruption)
	}
}

// A site and WLAN created by a path that does not set typed fields still
// compile with the new-object defaults (never the LED off).
func TestUntypedRecordsCompileWithDefaults(t *testing.T) {
	state := model.State{Schema: 2,
		Sites:    []model.Site{{ID: typedSite, Name: "Plain", Country: "AU"}},
		APs:      []model.APRecord{{ID: typedAP, SiteID: typedSite, Secret: make([]byte, 32)}},
		Networks: []model.Network{{ID: typedNet, SiteID: typedSite, SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true, BSSIDIndex: 1}},
	}
	_, root := compileTypedAP(t, state, typedAP)
	if root["LED"].(map[string]any)["Brightness"] == float64(0) || root["LED"].(map[string]any)["Brightness"] != jsonNumber("255") {
		t.Fatalf("LED %v", root["LED"])
	}
	wlan := root["WLANs"].([]any)[0].(map[string]any)
	if wlan["WNM"].(map[string]any)["NeighborReport"] != true || wlan["Auth"].(map[string]any)["AKM"].(map[string]any)["WPA3_PSK"] != false {
		t.Fatalf("untyped WLAN %v", wlan["WNM"])
	}
}
