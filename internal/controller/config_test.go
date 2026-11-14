package controller

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

// The controller wrappers must stay byte-identical to internal/compile until
// they are deleted in phase 2.
func TestConfigWrappersForwardToCompile(t *testing.T) {
	base := []byte(`{"SwitchPorts":[{"Name":"eth0","Vlans":["1"]}],"WLANs":[{"Auth":{"Type":"psk","PSK":""}}]}`)
	site := model.Site{ID: "site-a", Name: "Example", Country: "au", Advanced: json.RawMessage(`{"LED":{"Brightness":9}}`)}
	ap := model.APRecord{ID: "02-aa-00-00-00-01", Name: "example-ap", SiteID: "site-a"}
	networks := []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 20, Enabled: true}}
	now := time.Unix(1794475905, 0)
	got, err := BuildSiteConfig(base, site, ap, networks, now)
	if err != nil {
		t.Fatal(err)
	}
	want, err := compile.BuildSiteConfig(base, site, ap, networks, now)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("BuildSiteConfig wrapper differs: %v", err)
	}
	got, _ = BuildConfig(base, ap, networks, now)
	want, _ = compile.BuildConfig(base, ap, networks, now)
	if !bytes.Equal(got, want) {
		t.Fatal("BuildConfig wrapper differs")
	}
	merged, err := MergePatch(json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{"b":null}`))
	if err != nil || string(merged) != `{"a":1}` {
		t.Fatalf("MergePatch wrapper: %s %v", merged, err)
	}
}
