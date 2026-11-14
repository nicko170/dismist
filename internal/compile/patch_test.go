package compile

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

func TestMergePatchKeepsUnknownFieldsAndDeletesNulls(t *testing.T) {
	base := json.RawMessage(`{"Radio":{"Power":10,"Width":20},"Existing":true}`)
	patch := json.RawMessage(`{"Radio":{"Power":12},"Existing":null,"Mystery":{"Flag":true}}`)
	got, err := MergePatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	radio := result["Radio"].(map[string]any)
	if radio["Power"] != float64(12) || radio["Width"] != float64(20) || result["Existing"] != nil ||
		result["Mystery"].(map[string]any)["Flag"] != true {
		t.Fatalf("merge patch lost fields: %+v", result)
	}
}

func TestBuildSiteConfigLayersAdvancedPatchesUnderTypedFields(t *testing.T) {
	base := []byte(`{"ID":"old","Radio":{"Power":10,"Width":20},"WLANs":[{"ID":"old","SSID":"OLD","RoamMode":"none","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-04"}}]}`)
	site := model.Site{ID: "site-a", Name: "Lab", Advanced: json.RawMessage(`{"Radio":{"Power":11},"UnknownSite":{"flag":true},"WLANs":[]}`)}
	ap := model.APRecord{ID: "02-aa-00-00-00-03", SiteID: "site-a", SecretFile: "secret.bin", Advanced: json.RawMessage(`{"Radio":{"Power":12},"UnknownAP":{"value":5},"ID":"short"}`)}
	network := model.Network{ID: "wlan-a", SiteID: "site-a", SSID: "TestSSID", PSK: "example-passphrase-12", VLANID: 123, Enabled: true,
		Advanced: json.RawMessage(`{"RoamMode":"11r","SSID":"short","Auth":{"PSK":"short","AKM":{"WPA2_PSK":true}}}`)}
	raw, err := BuildSiteConfig(base, site, ap, []model.Network{network}, time.Unix(1794475905, 0))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	wlan := result["WLANs"].([]any)[0].(map[string]any)
	auth := wlan["Auth"].(map[string]any)
	if result["ID"] != ap.ID || result["SiteID"] != site.ID || result["Radio"].(map[string]any)["Power"] != float64(12) ||
		result["Radio"].(map[string]any)["Width"] != float64(20) || result["UnknownSite"] == nil || result["UnknownAP"] == nil ||
		wlan["SSID"] != "TestSSID" || wlan["RoamMode"] != "11r" || auth["PSK"] != "example-passphrase-12" {
		t.Fatalf("advanced layering is short: %+v", result)
	}
}

func TestMergePatchRejectsNonObject(t *testing.T) {
	if _, err := MergePatch(json.RawMessage(`{"a":1}`), json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("accepted non-object advanced patch")
	}
}

func TestMergePatchHandlesWhitespaceBase(t *testing.T) {
	got, err := MergePatch(json.RawMessage("   "), json.RawMessage(`{"flag":true}`))
	if err != nil || string(got) != `{"flag":true}` {
		t.Fatalf("whitespace base was not treated as empty object: %s %v", got, err)
	}
}
