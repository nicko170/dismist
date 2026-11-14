package effective_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
)

func normalise(t *testing.T, config string, ctx effective.Context) (string, effective.Report) {
	t.Helper()
	out, report, err := effective.Normalise([]byte(config), ctx)
	if err != nil {
		t.Fatalf("%s: %v", config, err)
	}
	return string(out), report
}

func same(t *testing.T, a, b string, ctx effective.Context) bool {
	t.Helper()
	x, _ := normalise(t, a, ctx)
	y, _ := normalise(t, b, ctx)
	return x == y
}

// Rule 2a on value, slice, map and pointer fields (design §4.5 tests).
func TestRule2aIsTypeAware(t *testing.T) {
	none := effective.Context{LocalFiles: effective.LocalFilesNone}
	for _, test := range []struct {
		name      string
		a, b      string
		wantEqual bool
	}{
		{"bool false", `{"WLANs":[{"OWE":{"Transition":false}}]}`, `{"WLANs":[{}]}`, true},
		{"number zero", `{"Monitor":{"Mode":0}}`, `{}`, true},
		{"empty string", `{"Hostname":""}`, `{}`, true},
		{"value struct null", `{"WLANs":[{"Auth":{"Type":"psk","WEP":null}}]}`, `{"WLANs":[{"Auth":{"Type":"psk"}}]}`, true},
		{"value struct of zeros", `{"WLANs":[{"Auth":{"Type":"psk","Pairwise":{"GCMP256":false,"CCMP":true}}}]}`, `{"WLANs":[{"Auth":{"Type":"psk","Pairwise":{"CCMP":true}}}]}`, true},
		{"radio TWTDisabled false", `{"Radios":[{"Band":"5","TWTDisabled":false}]}`, `{"Radios":[{"Band":"5"}]}`, true},
		{"empty slice", `{"NTP":[]}`, `{}`, true},
		{"null slice", `{"NTP":null}`, `{}`, true},
		{"empty map", `{"VLANNames":{}}`, `{}`, true},
		{"full 16-flag AKM object", `{"WLANs":[{"Auth":{"Type":"psk","AKM":{"WPA2_PSK":true,"WPA3_PSK":false,"WPA_PSK":false,"WPA3_SUITEB":false}}}]}`, `{"WLANs":[{"Auth":{"Type":"psk","AKM":{"WPA2_PSK":true}}}]}`, true},
		{"non-empty slice", `{"NTP":[""]}`, `{}`, false},
		{"pointer to zero struct", `{"APState":{}}`, `{}`, false},
		{"pointer null left alone", `{"APState":null}`, `{}`, false},
		{"pointer to false", `{"SVIs":[{"Vlan_ID":"1","DHCP_Ena":false}]}`, `{"SVIs":[{"Vlan_ID":"1"}]}`, false},
		{"opaque custom type", `{"UUID":""}`, `{}`, false},
		{"opaque named type", `{"WLANs":[{"Auth":{"Type":"psk","PSK":""}}]}`, `{"WLANs":[{"Auth":{"Type":"psk"}}]}`, false},
		{"raw message null", `{"BootConfig":null}`, `{}`, false},
		{"real difference", `{"WLANs":[{"Hidden":true}]}`, `{"WLANs":[{}]}`, false},
	} {
		if got := same(t, test.a, test.b, none); got != test.wantEqual {
			x, _ := normalise(t, test.a, none)
			y, _ := normalise(t, test.b, none)
			t.Errorf("%s: equal=%v, want %v\n%s\n%s", test.name, got, test.wantEqual, x, y)
		}
	}
}

func TestRule2aIsOffForAPsWithLocalFiles(t *testing.T) {
	present := effective.Context{LocalFiles: effective.LocalFilesPresent}
	if same(t, `{"WLANs":[{"OWE":{"Transition":false}}]}`, `{"WLANs":[{}]}`, present) {
		t.Fatal("rule 2a applied to an AP with local files")
	}
	_, report := normalise(t, `{}`, present)
	if report.ZeroRule || report.LocalFiles != effective.LocalFilesPresent || !strings.Contains(strings.Join(report.Notes, ","), effective.NoteLocalFiles) {
		t.Fatalf("report %+v", report)
	}
	_, report = normalise(t, `{}`, effective.Context{})
	if !report.ZeroRule || report.LocalFiles != effective.LocalFilesUnknown {
		t.Fatalf("never-checked AP: %+v", report)
	}
	for files, want := range map[*[]model.LocalFile]string{
		nil: effective.LocalFilesUnknown, {}: effective.LocalFilesNone, {{Name: "local_defaults.config"}}: effective.LocalFilesPresent,
	} {
		var list []model.LocalFile
		if files != nil {
			list = *files
			if list == nil {
				list = []model.LocalFile{}
			}
		}
		if got := effective.LocalFilesState(list); got != want {
			t.Errorf("LocalFilesState(%v) = %s, want %s", list, got, want)
		}
	}
	if _, _, err := effective.Normalise([]byte(`{}`), effective.Context{LocalFiles: "maybe"}); err == nil {
		t.Fatal("unknown local-files state accepted")
	}
}

// Rule 1: keys fold to canonical names and the last variant in document
// order wins, with encoding/json's merge semantics for objects and arrays.
func TestKeyFoldingFollowsEncodingJSON(t *testing.T) {
	none := effective.Context{LocalFiles: effective.LocalFilesNone}
	// The template's roamMode beats a patch's RoamMode because Go marshals
	// RoamMode first (WLAN §0 gotcha 1).
	if !same(t, `{"WLANs":[{"RoamMode":"11r","roamMode":"none"}]}`, `{"WLANs":[{"RoamMode":"none"}]}`, none) {
		t.Fatal("last variant did not win")
	}
	if !same(t, `{"WLANs":[{"roamMode":"none","RoamMode":"11r"}]}`, `{"WLANs":[{"RoamMode":"11r"}]}`, none) {
		t.Fatal("last variant did not win (reversed)")
	}
	// A later object merges into the struct; it does not replace it.
	if !same(t, `{"LED":{"Brightness":9,"Locator":true},"led":{"Brightness":5}}`, `{"LED":{"Brightness":5,"Locator":true}}`, none) {
		t.Fatal("struct duplicate did not merge")
	}
	// A later array decodes element-wise into the earlier one and truncates.
	if !same(t, `{"NTP":["a","b"],"ntp":["c"]}`, `{"NTP":["c"]}`, none) {
		t.Fatal("slice duplicate")
	}
	if !same(t, `{"SwitchPorts":[{"Name":"eth0","PortVlanID":"1"}],"switchports":[{"Vlans":["5"]}]}`, `{"SwitchPorts":[{"Name":"eth0","PortVlanID":"1","Vlans":["5"]}]}`, none) {
		t.Fatal("array elements did not merge")
	}
	// null on a value kind is a no-op, even after an earlier variant.
	if !same(t, `{"Hostname":"a","hostname":null}`, `{"Hostname":"a"}`, none) {
		t.Fatal("null replaced a value")
	}
	out, report := normalise(t, `{"wlans":[{"ssid":"x","forftm":true,"ampdu":{"RST":true}}],"SiteName":"s","Timestamp":5}`, none)
	if !strings.Contains(out, `"SSID":"x"`) || strings.Contains(out, "Timestamp") {
		t.Fatalf("canonical %s", out)
	}
	want := []string{"SiteName", "WLANs[].AMPDU.RST", "WLANs[].forftm"}
	if strings.Join(report.Ignored, ",") != strings.Join(want, ",") {
		t.Fatalf("ignored %v", report.Ignored)
	}
}

func TestTypeErrorsAreReportedAndKept(t *testing.T) {
	out, report := normalise(t, `{"WLANs":[{"Vlans":[667]}],"Radios":[{"Band":"5","Power":"17"}]}`, effective.Context{})
	if len(report.TypeErrors) != 2 || !strings.Contains(out, "667") {
		t.Fatalf("type errors %v in %s", report.TypeErrors, out)
	}
	if !strings.Contains(strings.Join(report.Notes, ","), effective.NoteTypeErrorsPresent) {
		t.Fatal("type_errors note missing")
	}
}

// Canonicalising the template (folding roamMode, dropping AMPDU.RST and
// ForFTM) does not change the effective hash.
func TestCanonicalisingTheTemplateKeepsTheHash(t *testing.T) {
	raw := compiled(t, model.Site{})
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	for _, wlan := range root["WLANs"].([]any) {
		w := wlan.(map[string]any)
		w["RoamMode"] = w["roamMode"]
		delete(w, "roamMode")
		delete(w, "ForFTM")
		delete(w["AMPDU"].(map[string]any), "RST")
	}
	delete(root, "SiteName")
	delete(root, "RootAuthorizedSSHKeys")
	canonicalised, _ := json.Marshal(root)
	key := bytes.Repeat([]byte{7}, 32)
	before, _, _, err := effective.HashConfig(key, raw, effective.Context{})
	if err != nil {
		t.Fatal(err)
	}
	after, _, _, _ := effective.HashConfig(key, canonicalised, effective.Context{})
	if before != after {
		t.Fatal("canonicalising the template changed the effective hash")
	}
	if !strings.HasPrefix(before, "e1:") || len(before) != len("e1:")+64 {
		t.Fatalf("hash format %q", before)
	}
	other, _, _, _ := effective.HashConfig(bytes.Repeat([]byte{8}, 32), raw, effective.Context{})
	if other == before {
		t.Fatal("hash is not keyed")
	}
	if !effective.SameVersion(before, other) || effective.SameVersion(before, "e2:00") || effective.SameVersion("x", "x") {
		t.Fatal("SameVersion")
	}
	if _, err := effective.Hash(nil, []byte(`{}`)); err == nil {
		t.Fatal("unkeyed hash allowed")
	}
}

// Rule 4: BSSID index resolution, and a disabled WLAN ahead of an enabled
// one leaves indexes unresolved (§9 Q8).
func TestBSSIDIndexResolution(t *testing.T) {
	wlan := func(id string, enabled bool, idx24, idx5 int) string {
		return `{"ID":"` + id + `","Enabled":` + map[bool]string{true: "true", false: "false"}[enabled] +
			`,"Bands":["2.4","5"],"BandConfigs":[{"Band":"2.4","BSSIDIndex":` + itoa(idx24) + `},{"Band":"5","BSSIDIndex":` + itoa(idx5) + `}]}`
	}
	unresolved := func(report effective.Report) bool {
		return strings.Contains(strings.Join(report.Notes, ","), effective.NoteBSSIDUnresolved)
	}
	out, report := normalise(t, `{"WLANs":[`+wlan("a", true, 0, 0)+`,`+wlan("b", true, 0, 3)+`,`+wlan("c", true, 1, 3)+`]}`, effective.Context{})
	if unresolved(report) {
		t.Fatalf("notes %v", report.Notes)
	}
	indexes := bssids(t, out)
	want := map[string]int{"a/2.4": 1, "a/5": 1, "b/2.4": 2, "b/5": 3, "c/2.4": 3, "c/5": 2}
	for key, index := range want {
		if indexes[key] != index {
			t.Fatalf("%s resolved to %d, want %d (all %v)", key, indexes[key], index, indexes)
		}
	}
	// A requested 0 and an explicit 1 for the only WLAN are the same.
	if !same(t, `{"WLANs":[`+wlan("a", true, 0, 0)+`]}`, `{"WLANs":[`+wlan("a", true, 1, 1)+`]}`, effective.Context{}) {
		t.Fatal("requested 0 did not resolve to 1")
	}
	_, report = normalise(t, `{"WLANs":[`+wlan("a", false, 0, 0)+`,`+wlan("b", true, 0, 0)+`]}`, effective.Context{})
	if !unresolved(report) {
		t.Fatalf("disabled WLAN ahead of an enabled one: %v", report.Notes)
	}
	// Disabled WLANs after the enabled ones do not block resolution.
	_, report = normalise(t, `{"WLANs":[`+wlan("a", true, 0, 0)+`,`+wlan("b", false, 0, 0)+`]}`, effective.Context{})
	if unresolved(report) {
		t.Fatalf("trailing disabled WLAN: %v", report.Notes)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func bssids(t *testing.T, canonical string) map[string]int {
	t.Helper()
	var root struct {
		WLANs []struct {
			ID          string
			BandConfigs []struct {
				Band       string
				BSSIDIndex int
			}
		}
	}
	if err := json.Unmarshal([]byte(canonical), &root); err != nil {
		t.Fatal(err)
	}
	result := map[string]int{}
	for _, wlan := range root.WLANs {
		for _, config := range wlan.BandConfigs {
			result[wlan.ID+"/"+config.Band] = config.BSSIDIndex
		}
	}
	return result
}

// Rule 5: the raw radio fields collapse to {channel, width, power, ccrev},
// so Channel vs RRM.Channel spellings of the same plan are equal.
func TestRadioResolution(t *testing.T) {
	ctx := effective.Context{}
	if !same(t, `{"Radios":[{"Band":"5","Channel":149,"Width":80,"Power":17,"CCRev":"AU/890"}]}`,
		`{"Radios":[{"Band":"5","RRM":{"Channel":149,"Width":80,"Power":17},"CCRev":"AU/890"}]}`, ctx) {
		t.Fatal("Channel and RRM.Channel spellings differ")
	}
	if same(t, `{"Radios":[{"Band":"5","Channel":149}]}`, `{"Radios":[{"Band":"5","Channel":153}]}`, ctx) {
		t.Fatal("different channels compared equal")
	}
	if !same(t, `{"Monitor":{"CCRev":""}}`, `{"Monitor":{"CCRev":"US/0"}}`, ctx) {
		t.Fatal("scan radio ccrev default")
	}
	if !same(t, `{"Radios":[{"Band":"2.4","Power":40}]}`, `{"Radios":[{"Band":"2.4","Power":25}]}`, ctx) {
		t.Fatal("power cap")
	}
}

// Rule 6: sets and keyed arrays.
func TestSetsAndKeyedArrays(t *testing.T) {
	ctx := effective.Context{}
	if !same(t, `{"SwitchPorts":[{"Name":"eth1","Vlans":["1"]},{"Name":"eth0","Vlans":["20","1","20"]}]}`,
		`{"SwitchPorts":[{"Name":"eth0","Vlans":["1","20"]},{"Name":"eth1","Vlans":["1"]}]}`, ctx) {
		t.Fatal("switch ports")
	}
	if !same(t, `{"SSH":{"AuthorizedKeys":["`+sshKey+` laptop","`+sshKey+`"]}}`, `{"SSH":{"AuthorizedKeys":["`+sshKey+`"]}}`, ctx) {
		t.Fatal("SSH keys compared with comments")
	}
	if same(t, `{"WLANs":[{"ID":"a","Vlans":["10","20"]}]}`, `{"WLANs":[{"ID":"a","Vlans":["20","10"]}]}`, ctx) {
		t.Fatal("the first WLAN VLAN is not a set member")
	}
	if !same(t, `{"WLANs":[{"ID":"a","Vlans":["10","30","20"]}]}`, `{"WLANs":[{"ID":"a","Vlans":["10","20","30"]}]}`, ctx) {
		t.Fatal("WLAN VLANs after the first")
	}
}

func TestNormaliseRejectsNonObjects(t *testing.T) {
	for _, bad := range []string{`[]`, `"x"`, `{"a":`, ``} {
		if _, _, err := effective.Normalise([]byte(bad), effective.Context{}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, report, err := effective.Normalise(legacyv0.Template(nil), effective.Context{}); err != nil || report.Version != effective.Version {
		t.Fatal(err)
	}
}
