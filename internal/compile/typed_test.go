package compile

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options"
	"github.com/nicko170/dismist/internal/rfplan"
)

const (
	typedSite = "8e31882f-0000-4000-8000-0000000000aa"
	typedAP   = "02-aa-00-00-00-37"
	typedNet  = "6f2000aa-0000-4000-8000-000000000001"
	typedKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"
)

var typedTime = time.Date(2026, 11, 20, 9, 12, 3, 0, time.UTC)

// newTypedState is a schema-2 site with new-object defaults.
func newTypedState() model.State {
	site := model.Site{ID: typedSite, Name: "Home", Country: "AU", Timezone: "Australia/Sydney",
		SSH:        model.SiteSSH{Enabled: true, Keys: []model.SSHKey{{Key: typedKey, Comment: "laptop"}}},
		Management: model.ManagementDefaults{VLANID: 1},
		APDefaults: model.APDefaults{LEDBrightness: 255, HostnameFromName: true, PoEConstrained: model.PoEKeepRadios},
		Protection: model.Protection{ProxyARP: true},
		RF: model.RFProfile{Managed: true,
			Band24: model.BandProfile{Enabled: true, Width: 20, Channels: []int{1, 6, 11}, Power: model.PowerPolicy{Mode: model.PowerAuto, Min: 8, Max: 15}, Preamble: "short"},
			Band5:  model.BandProfile{Enabled: true, Width: 80, Channels: []int{36, 40, 44, 48, 149, 153, 157, 161}, Power: model.PowerPolicy{Mode: model.PowerAuto, Min: 12, Max: 20}, Preamble: "short"}},
	}
	on := true
	network := model.Network{ID: typedNet, SiteID: typedSite, SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true,
		Security: model.SecurityWPA2WPA3Personal, Bands: []string{"2.4", "5"}, BSSIDIndex: 1, Rates: "compatible", DTIM: 2,
		IdleTimeout: 1800, ReclaimTimeoutSec: 300, GroupRekeySec: 86400, HonourBlocklist: true, UcastDisassocOnBssDown: &on,
		Roaming: model.Roaming{Fast: "none", NeighborReport: true, BSSTransition: true, BSSLoad: true}, Broadcast: model.Broadcast{AllowIPv6ND: true}}
	ap := model.APRecord{ID: typedAP, Name: "Hallway AP", SiteID: typedSite, Secret: make([]byte, 32), ConfigRevision: 5, ConfigUpdatedAt: typedTime}
	state := model.State{Revision: 5, UpdatedAt: typedTime, Schema: 2, Sites: []model.Site{site}, APs: []model.APRecord{ap}, Networks: []model.Network{network}}
	rfplan.ApplyInitial(&state, typedTime)
	return state
}

func compileTypedAP(t *testing.T, state model.State, apID string) (Output, map[string]any) {
	t.Helper()
	input, err := InputFor(legacyv0.BundledTemplate(), state, apID)
	if err != nil {
		t.Fatal(err)
	}
	input.Now = typedTime
	output, err := AP(input)
	if err != nil {
		t.Fatalf("compile: %v (validation %+v)", err, output.Validation)
	}
	tree, err := decodeObject(output.Config)
	if err != nil {
		t.Fatal(err)
	}
	return output, tree
}

func TestTypedCompileNewSite(t *testing.T) {
	state := newTypedState()
	output, root := compileTypedAP(t, state, typedAP)
	wlan := root["WLANs"].([]any)[0].(map[string]any)
	auth := wlan["Auth"].(map[string]any)
	akm := auth["AKM"].(map[string]any)
	if len(akm) != 16 || akm["WPA2_PSK"] != true || akm["WPA3_PSK"] != true || akm["WPA_PSK"] != false {
		t.Fatalf("AKM %v", akm)
	}
	if auth["WEP"] != nil || auth["Type"] != "psk" || auth["PSK"] != "example-passphrase-01" {
		t.Fatalf("auth %v", auth)
	}
	if _, ok := wlan["roamMode"]; ok || wlan["RoamMode"] != "none" {
		t.Fatalf("roam mode not canonical: %v", wlan["RoamMode"])
	}
	if _, ok := wlan["ForFTM"]; ok {
		t.Fatal("ForFTM kept")
	}
	bc := wlan["BandConfigs"].([]any)
	for _, element := range bc {
		config := element.(map[string]any)
		if config["BSSIDIndex"] != json.Number("1") {
			t.Fatalf("BSSID index %v", config)
		}
		if _, steer := config["Steer"]; steer {
			t.Fatal("band steering is off for new WLANs")
		}
	}
	if root["Hostname"] != "hallway-ap" {
		t.Fatalf("hostname %v", root["Hostname"])
	}
	svis := root["SVIs"].([]any)
	if svis[0].(map[string]any)["DHCP"].(map[string]any)["Hostname"] != "hallway-ap" {
		t.Fatal("DHCP hostname")
	}
	radios := root["Radios"].([]any)
	for _, element := range radios {
		radio := element.(map[string]any)
		if radio["CCRev"] != "AU/890" || radio["Power"] == json.Number("0") || radio["Power"] == nil {
			t.Fatalf("radio %v", radio)
		}
	}
	ssh := root["SSH"].(map[string]any)
	if ssh["Disabled"] != false || ssh["AuthorizedKeys"].([]any)[0] != typedKey+" laptop" {
		t.Fatalf("SSH %v", ssh)
	}
	if root["PoePwrConstrained"].(map[string]any)["EnableRadios"] != true {
		t.Fatal("PoE")
	}
	if len(output.Validation) != 0 {
		t.Fatalf("validation %+v", output.Validation)
	}
	if len(output.Trace) == 0 {
		t.Fatal("no trace")
	}
	// The config the AP gets has no type errors.
	if _, report, err := effective.Fold(output.Config); err != nil || len(report.TypeErrors) != 0 {
		t.Fatalf("type errors %v %v", report.TypeErrors, err)
	}
}

func TestFoldPatchCaseForbiddenAndDuplicates(t *testing.T) {
	for _, test := range []struct {
		scope, patch, code string
	}{
		{ScopeSite, `{"wlans":[]}`, CodeForbiddenPath},
		{ScopeSite, `{"WLANs":[]}`, CodeForbiddenPath},
		{ScopeAP, `{"timestamp":1}`, CodeForbiddenPath},
		{ScopeAP, `{"ID":"x"}`, CodeForbiddenPath},
		{ScopeSite, `{"stats":{"interval":5}}`, CodeForbiddenPath},
		{ScopeSite, `{"ipsec":{}}`, CodeForbiddenPath},
		{ScopeWLAN, `{"ssid":"x"}`, CodeForbiddenPath},
		{ScopeWLAN, `{"auth":{"psk":"x"}}`, CodeForbiddenPath},
		{ScopeWLAN, `{"Auth":{"akm":{"wpa_psk":true}}}`, CodeForbiddenPath},
		{ScopeWLAN, `{"bandconfigs":[{"band":"5","bssidindex":3}]}`, CodeForbiddenPath},
		{ScopeWLAN, `{"Wxlan":{"PortalURL":"x"}}`, CodeForbiddenPath},
		{ScopeWLAN, `{"SSID":"a","ssid":"b"}`, CodeForbiddenPath},
		{ScopeWLAN, `{"Hidden":true,"hidden":false}`, CodeDuplicateKey},
		{ScopeSite, `{"Radios":[{"band":"6","Power":3}]}`, CodeForbiddenPath},
		{ScopeSite, `{"Radios":[{"Band":"5","$remove":true}]}`, CodeForbiddenPath},
	} {
		_, err := FoldPatch(test.scope, []byte(test.patch))
		var problems PatchErrors
		if !errors.As(err, &problems) || problems.Code() != test.code {
			t.Errorf("%s %s: %v", test.scope, test.patch, err)
		}
	}
	folded, err := FoldPatch(ScopeWLAN, []byte(`{"roammode":"11r","bandconfigs":[{"band":"5","geofence":{"denydot11auth":true}}],"Unknown":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(folded.Patch) != `{"BandConfigs":[{"Band":"5","GeoFence":{"DenyDot11Auth":true}}],"RoamMode":"11r","Unknown":1}` {
		t.Fatalf("folded %s", folded.Patch)
	}
	if !reflect.DeepEqual(folded.Ignored, []string{"WLANs[].Unknown"}) {
		t.Fatalf("ignored %v", folded.Ignored)
	}
	if folded, err := FoldPatch(ScopeSite, []byte(`{"cloud":{"proxy":{"url":"https://example.test"}}}`)); err != nil || len(folded.Warnings) != 1 {
		t.Fatalf("proxy warning %+v %v", folded, err)
	}
}

func TestKeyedMergeAndDiff(t *testing.T) {
	base := map[string]any{"Radios": []any{
		map[string]any{"Band": "2.4", "QOS": map[string]any{"Dscp2Ac": []any{1}}, "Width": json.Number("20")},
		map[string]any{"Band": "5", "Width": json.Number("40")},
	}, "SwitchPorts": []any{map[string]any{"Name": "eth0", "Vlans": []any{"1"}}, map[string]any{"Name": "eth1"}}}
	patch := map[string]any{"Radios": []any{map[string]any{"Band": "5", "Width": json.Number("80")}},
		"SwitchPorts": []any{map[string]any{"Name": "eth1", "$remove": true}}, "LED": map[string]any{"Brightness": json.Number("3")}}
	merged, err := KeyedMerge(deepCopy(base), patch, "")
	if err != nil {
		t.Fatal(err)
	}
	m := merged.(map[string]any)
	radios := m["Radios"].([]any)
	if len(radios) != 2 || radios[1].(map[string]any)["Width"] != json.Number("80") || radios[0].(map[string]any)["QOS"] == nil {
		t.Fatalf("radios %v", radios)
	}
	if len(m["SwitchPorts"].([]any)) != 1 {
		t.Fatal("$remove")
	}
	// Diff back: KeyedMerge(base, diff) == merged.
	diff, changed := KeyedDiff(base, merged, "")
	if !changed {
		t.Fatal("no diff")
	}
	again, err := KeyedMerge(deepCopy(base), diff, "")
	if err != nil || !equalTree(again, merged) {
		t.Fatalf("round trip %v", diff)
	}
	// A missing object is nulled leaf by leaf, never replaced whole.
	diff, _ = KeyedDiff(map[string]any{"ProbeControl": map[string]any{"ProbeReqSW": true}}, map[string]any{}, "Radios[]")
	if got, _ := encodeTree(diff); string(got) != `{"ProbeControl":{"ProbeReqSW":null}}` {
		t.Fatalf("leaf nulls %s", got)
	}
}

// Precedence (design §4.4): a site patch on Radios cannot override an AP
// static channel; an AP patch over a site field is cross-scope shadowing.
func TestPrecedenceAndShadowing(t *testing.T) {
	state := newTypedState()
	state.APs[0].RF = map[string]*model.RadioOverride{"5": {Channel: 149}}
	state.Sites[0].Expert = json.RawMessage(`{"Radios":[{"Band":"5","Channel":36,"RRM":{"Channel":36}}],"LED":{"Brightness":10}}`)
	state.APs[0].Expert = json.RawMessage(`{"LED":{"Brightness":20}}`)
	output, root := compileTypedAP(t, state, typedAP)
	radio, _ := keyedElement(root["Radios"].([]any), "Band", "5")
	if radio["Channel"] != json.Number("149") {
		t.Fatalf("site patch overrode the AP static channel: %v", radio["Channel"])
	}
	if root["LED"].(map[string]any)["Brightness"] != json.Number("20") {
		t.Fatal("AP patch must win")
	}
	shadows := map[string]Shadow{}
	for _, shadow := range output.Shadowed {
		shadows[shadow.By+":"+shadow.Option] = shadow
	}
	if s, ok := shadows["site:site.ap_defaults.led_brightness"]; !ok || s.Partly {
		t.Fatalf("same-scope shadow missing: %+v", output.Shadowed)
	}
	if _, ok := shadows["ap:site.ap_defaults.led_brightness"]; !ok {
		t.Fatalf("cross-scope shadow missing: %+v", output.Shadowed)
	}
	var trace TraceEntry
	for _, entry := range output.Trace {
		if entry.Option == "site.ap_defaults.led_brightness" {
			trace = entry
		}
	}
	if trace.Step != StepAPExpert {
		t.Fatalf("trace %+v", trace)
	}
	// compile.Site aggregates cross-scope shadowing per site option.
	result, err := Site(SiteInput{Template: legacyv0.BundledTemplate(), State: state})
	if err != nil {
		t.Fatal(err)
	}
	if aps := result.SiteShadows()["site.ap_defaults.led_brightness"]; !reflect.DeepEqual(aps, []string{typedAP}) {
		t.Fatalf("site shadows %v", result.SiteShadows())
	}
}

func TestValidationRefusesFirmwareRuleBreaks(t *testing.T) {
	for name, change := range map[string]func(*model.State){
		"power 0 on a static radio": func(s *model.State) {
			s.APs[0].RF = map[string]*model.RadioOverride{"5": {Channel: 149}}
			s.APs[0].Expert = json.RawMessage(`{"Radios":[{"Band":"5","Power":0,"RRM":{"Power":0}}]}`)
		},
		"160 MHz under AU/890": func(s *model.State) { s.Sites[0].RF.Band5.Width = 160 },
		"DFS channel with DFS off": func(s *model.State) {
			s.APs[0].RF = map[string]*model.RadioOverride{"5": {Channel: 52}}
		},
		"WPA3 without PMF via expert": func(s *model.State) {
			s.Networks[0].Expert = json.RawMessage(`{"Dot11w":{"Enabled":false}}`)
		},
		"SAE with a 64-hex PSK": func(s *model.State) { s.Networks[0].PSK = strings.Repeat("a", 64) },
		"steering on one band": func(s *model.State) {
			s.Networks[0].Bands = []string{"2.4"}
			s.Networks[0].BandSteering = true
		},
		"duplicate BSSID index": func(s *model.State) {
			second := s.Networks[0]
			second.ID = "6f2000aa-0000-4000-8000-000000000002"
			s.Networks = append(s.Networks, second)
		},
		"SSH on without keys": func(s *model.State) { s.Sites[0].SSH.Keys = nil },
		"non-UUID site": func(s *model.State) {
			s.Sites[0].ID, s.APs[0].SiteID, s.Networks[0].SiteID = "site-a", "site-a", "site-a"
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := newTypedState()
			change(&state)
			input, err := InputFor(legacyv0.BundledTemplate(), state, typedAP)
			if err != nil {
				t.Fatal(err)
			}
			_, err = AP(input)
			if err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// The Auto property (design §8.5): no enabled radio ever compiles with
// power 0 or with both channels 0, whatever the plan state.
func TestAutoNeverCompilesPowerOrChannelZero(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	for iteration := 0; iteration < 200; iteration++ {
		state := newTypedState()
		site := &state.Sites[0]
		site.RF.Band5.Width = []int{20, 40, 80}[random.Intn(3)]
		site.RF.Band5.DFS = random.Intn(2) == 0
		site.RF.Band5.Channels = [][]int{nil, {36}, {52, 149}, {100}, {149, 153}}[random.Intn(5)]
		if random.Intn(2) == 0 {
			site.RF.Band24.Power = model.PowerPolicy{Mode: model.PowerFixed, DBm: 1 + random.Intn(25)}
		}
		switch random.Intn(3) {
		case 0:
			state.APs[0].RFPlan = nil // no plan: fallback
		case 1:
			state.APs[0].RFPlan = map[string]model.PlannedRadio{"5": {Channel: 116, Width: 80, PowerDBm: 0}} // invalid plan
		}
		input, _ := InputFor(legacyv0.BundledTemplate(), state, typedAP)
		output, err := AP(input)
		if err != nil {
			var failed *ValidationFailed
			if errors.As(err, &failed) {
				for _, problem := range failed.Errors {
					if strings.Contains(problem.Message, "power of 1 to 25") || strings.Contains(problem.Message, "needs a channel") {
						t.Fatalf("iteration %d compiled power or channel 0: %+v", iteration, failed.Errors)
					}
				}
				continue
			}
			if strings.Contains(err.Error(), "no allowed channel") {
				continue // an empty allowed list is refused, not compiled
			}
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		root, _ := decodeObject(output.Config)
		for _, element := range root["Radios"].([]any) {
			radio := element.(map[string]any)
			if boolOf(radio, "Disabled") {
				continue
			}
			if intOf(radio, "Power") == 0 && intOf(radio, "RRM", "Power") == 0 {
				t.Fatalf("iteration %d: power 0 %v", iteration, radio)
			}
			if intOf(radio, "Channel") == 0 && intOf(radio, "RRM", "Channel") == 0 {
				t.Fatalf("iteration %d: channel 0 %v", iteration, radio)
			}
		}
	}
}

func TestRFPlanMissingWarnsAndFallsBack(t *testing.T) {
	state := newTypedState()
	state.APs[0].RFPlan = nil
	output, root := compileTypedAP(t, state, typedAP)
	found := false
	for _, warning := range output.Warnings {
		found = found || warning.Code == "rf_plan_missing"
	}
	radio, _ := keyedElement(root["Radios"].([]any), "Band", "5")
	if !found || intOf(radio, "RRM", "Channel") == 0 || intOf(radio, "Channel") != 0 {
		t.Fatalf("fallback %v %+v", radio, output.Warnings)
	}
}

func TestSecurityCompositeTable(t *testing.T) {
	cases := map[string]struct {
		flags        []string
		authType     string
		on, required bool
	}{
		model.SecurityWPA2Personal:       {[]string{"WPA2_PSK"}, "psk", true, false},
		model.SecurityWPA2WPA3Personal:   {[]string{"WPA2_PSK", "WPA3_PSK"}, "psk", true, false},
		model.SecurityWPA3Personal:       {[]string{"WPA3_PSK"}, "psk", true, true},
		model.SecurityOpen:               {nil, "open", false, false},
		model.SecurityOWE:                {[]string{"WPA3_OWE"}, "owe", true, true},
		model.SecurityOWETransition:      {nil, "open", false, false},
		model.SecurityWPA2Enterprise:     {[]string{"WPA2_EAP"}, "eap", true, false},
		model.SecurityWPA2WPA3Enterprise: {[]string{"WPA2_EAP", "WPA3_EAP", "WPA2_EAP_SHA256"}, "eap", true, false},
		model.SecurityWPA3Enterprise:     {[]string{"WPA3_EAP", "WPA2_EAP_SHA256"}, "eap", true, true},
	}
	for security, want := range cases {
		mapping, err := MapSecurity(model.Network{Security: security})
		if err != nil {
			t.Fatal(err)
		}
		var flags []string
		for _, flag := range AKMFlags {
			if mapping.AKM[flag] {
				flags = append(flags, flag)
			}
		}
		if mapping.AuthType != want.authType || mapping.Dot11wOn != want.on || mapping.Dot11wReq != want.required {
			t.Errorf("%s: %+v", security, mapping)
		}
		if len(flags) != len(want.flags) {
			t.Errorf("%s flags %v want %v", security, flags, want.flags)
		}
	}
	required, _ := MapSecurity(model.Network{Security: model.SecurityWPA2Personal, PMF: "required"})
	if !required.AKM["WPA2_PSK_SHA256"] || !required.Dot11wReq {
		t.Fatal("PMF required needs SHA256")
	}
	ft, _ := MapSecurity(model.Network{Security: model.SecurityWPA2WPA3Personal, Roaming: model.Roaming{Fast: "11r"}})
	if !ft.AKM["WPA2_PSK_FT"] || !ft.AKM["WPA3_PSK_FT"] || !ft.AKM["WPA2_PSK_SHA256"] {
		t.Fatalf("11r FT AKMs %v", ft.AKM)
	}
}

func TestEnterpriseMPSKOption82AndSchedule(t *testing.T) {
	state := newTypedState()
	enterprise := state.Networks[0]
	enterprise.ID, enterprise.BSSIDIndex, enterprise.SSID = "6f2000aa-0000-4000-8000-000000000002", 2, "Example Corp"
	enterprise.Security, enterprise.PSK = model.SecurityWPA2WPA3Enterprise, ""
	enterprise.RADIUS = &model.RADIUSConfig{Auth: []model.RADIUSServer{{Host: "192.0.2.10", Secret: "radius-secret"}},
		Acct: []model.RADIUSServer{{Host: "radius.example.test", Secret: "acct-secret"}}, NASID: "ap", AcctInterimSec: 600,
		DynamicVLAN: &model.DynamicVLAN{Enabled: true, VLANs: []int{40, 41}}}
	mpsk := state.Networks[0]
	mpsk.ID, mpsk.BSSIDIndex, mpsk.SSID = "6f2000aa-0000-4000-8000-000000000003", 3, "Example Devices"
	mpsk.Security = model.SecurityWPA2Personal
	mpsk.MPSK = &model.MultiPSK{Enabled: true, Keys: []model.MPSKKey{{ID: "11111111-0000-4000-8000-000000000001", Name: "tv", Passphrase: "tv-passphrase-01", VLANID: 50, MAC: "02:aa:00:00:00:99"}}}
	mpsk.Option82 = &model.Option82{Enabled: true, CircuitID: "{SSID}:{AP_MAC}"}
	mpsk.Schedule = &model.Schedule{Enabled: true, Windows: []model.ScheduleWindow{{Day: "mon", Start: "00:00", End: "01:00"}}, OffAir: true}
	state.Networks = append(state.Networks, enterprise, mpsk)
	_, root := compileTypedAP(t, state, typedAP)
	wlans := root["WLANs"].([]any)
	byID := map[string]map[string]any{}
	for _, element := range wlans {
		w := element.(map[string]any)
		byID[w["ID"].(string)] = w
	}
	e := byID[enterprise.ID]
	servers := e["Auth_servers"].([]any)
	if servers[0].(map[string]any)["IP"] != "192.0.2.10" || servers[0].(map[string]any)["Port"] != json.Number("1812") {
		t.Fatalf("auth servers %v", servers)
	}
	if e["Acct_servers"].([]any)[0].(map[string]any)["Hostname"] != "radius.example.test" || e["Nas_ID"] != "ap" {
		t.Fatalf("acct %v", e)
	}
	m := byID[mpsk.ID]
	psks := m["Auth"].(map[string]any)["MultiPSK"].(map[string]any)["PSKs"].([]any)
	pmk, _ := MPSKPMK("tv-passphrase-01", "Example Devices")
	entry := psks[0].(map[string]any)
	if entry["PSK"] != pmk || len(pmk) != 64 || entry["WC"] != "02-aa-00-00-00-99" || entry["Vlan_ID"] != json.Number("50") {
		t.Fatalf("MPSK entry %v", entry)
	}
	if m["DhcpOption82Data"] != "Example Devices:02-aa-00-00-00-37" {
		t.Fatalf("option 82 %v", m["DhcpOption82Data"])
	}
	if bss, ok := m["BSS"].(map[string]any); !ok || bss["Disabled"] != true || bss["Reason"] != json.Number("8") {
		t.Fatalf("schedule off-air: %v", m["BSS"])
	}
	eth0, _ := keyedElement(root["SwitchPorts"].([]any), "Name", "eth0")
	vlans := eth0["Vlans"].([]any)
	want := []any{"1", "40", "41", "50"}
	if !reflect.DeepEqual(vlans, want) {
		t.Fatalf("eth0 trunk %v", vlans)
	}
}

// Known PBKDF2 vector (IEEE 802.11i): passphrase "password", SSID "IEEE".
func TestMPSKPMKVector(t *testing.T) {
	pmk, err := MPSKPMK("password", "IEEE")
	if err != nil || pmk != "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e" {
		t.Fatalf("pmk %s %v", pmk, err)
	}
}

func TestSanitiseHostname(t *testing.T) {
	for in, want := range map[string]string{
		"Hallway AP": "hallway-ap", "Café  Lounge!!": "cafe-lounge", "": "ap-02aa00000037", "---": "ap-02aa00000037",
		strings.Repeat("a", 70): strings.Repeat("a", 63),
	} {
		if got := SanitiseHostname(in, typedAP); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestAssignBSSIDIndex(t *testing.T) {
	state := newTypedState()
	index, owe, err := AssignBSSIDIndex(state, typedSite, true)
	if err != nil || index != 2 || owe != 3 {
		t.Fatalf("index %d owe %d %v", index, owe, err)
	}
	state.Networks = append(state.Networks, model.Network{SiteID: typedSite, BSSIDIndex: 2})
	if index, _, _ := AssignBSSIDIndex(state, typedSite, false); index != 3 {
		t.Fatalf("index %d", index)
	}
}

// Locate (LED.Locator) is on until LocateUntil.
func TestLocateCompile(t *testing.T) {
	state := newTypedState()
	state.APs[0].LocateUntil = typedTime.Add(time.Minute)
	_, root := compileTypedAP(t, state, typedAP)
	if root["LED"].(map[string]any)["Locator"] != true {
		t.Fatal("locate on")
	}
	state.APs[0].LocateUntil = typedTime.Add(-time.Minute)
	_, root = compileTypedAP(t, state, typedAP)
	if _, ok := root["LED"].(map[string]any)["Locator"]; ok {
		t.Fatal("locate expired")
	}
}

// Change detection on typed states: a dismist-only edit changes nothing.
func TestTypedChangeDetection(t *testing.T) {
	state := newTypedState()
	store := &memDesired{}
	if _, err := Apply(&state, store, ApplyOptions{Template: legacyv0.BundledTemplate(), Now: typedTime}); err != nil {
		t.Fatal(err)
	}
	previous := state
	state.Sites[0].Name, state.Sites[0].Timezone = "Renamed", "UTC"
	state.Sites[0].RF.Band24.Channels = []int{1, 6, 11, 13} // every plan stays valid
	applied, err := Apply(&state, store, ApplyOptions{Template: legacyv0.BundledTemplate(), Previous: &previous, Now: typedTime.Add(time.Hour)})
	if err != nil || len(applied.Pushes) != 0 {
		t.Fatalf("dismist-only edit pushed %v %v", applied.Pushes, err)
	}
	state.Networks[0].Hidden = true
	applied, err = Apply(&state, store, ApplyOptions{Template: legacyv0.BundledTemplate(), Now: typedTime.Add(2 * time.Hour)})
	if err != nil || len(applied.Pushes) != 1 {
		t.Fatalf("WLAN edit pushes %v %v", applied.Pushes, err)
	}
}

type memDesired struct {
	m map[string]model.DesiredConfig
}

func (d *memDesired) Desired(id string) (model.DesiredConfig, bool, error) {
	v, ok := d.m[id]
	return v, ok, nil
}

func (d *memDesired) PutDesired(id string, v model.DesiredConfig) error {
	if d.m == nil {
		d.m = map[string]model.DesiredConfig{}
	}
	d.m[id] = v
	return nil
}

// Mapping fidelity (design §4.2 check 2): changing one option changes only
// the firmware paths it declares.
func TestMappingFidelity(t *testing.T) {
	registry := options.Default()
	toggles := map[string]func(*model.State){
		"wlan.hidden":                     func(s *model.State) { s.Networks[0].Hidden = true },
		"wlan.isolation":                  func(s *model.State) { s.Networks[0].Isolation = "clients_and_lan" },
		"wlan.dtim":                       func(s *model.State) { s.Networks[0].DTIM = 3 },
		"wlan.band_steering":              func(s *model.State) { s.Networks[0].BandSteering = true },
		"wlan.rates":                      func(s *model.State) { s.Networks[0].Rates = "no_legacy" },
		"wlan.idle_timeout_sec":           func(s *model.State) { s.Networks[0].IdleTimeout = 600 },
		"wlan.max_clients":                func(s *model.State) { s.Networks[0].MaxClients = 30 },
		"wlan.roaming.neighbor_report":    func(s *model.State) { s.Networks[0].Roaming.NeighborReport = false },
		"wlan.broadcast.limit":            func(s *model.State) { s.Networks[0].Broadcast.Limit = true },
		"wlan.qos_class":                  func(s *model.State) { s.Networks[0].QoSClass = "voice" },
		"wlan.honour_blocklist":           func(s *model.State) { s.Networks[0].HonourBlocklist = false },
		"wlan.security":                   func(s *model.State) { s.Networks[0].Security = model.SecurityWPA2Personal },
		"wlan.sae_h2e_only":               func(s *model.State) { s.Networks[0].SAEH2EOnly = true },
		"wlan.group_rekey_sec":            func(s *model.State) { s.Networks[0].GroupRekeySec = 3600 },
		"site.ap_defaults.led_brightness": func(s *model.State) { s.Sites[0].APDefaults.LEDBrightness = 10 },
		"site.protection.proxy_arp":       func(s *model.State) { s.Sites[0].Protection.ProxyARP = false },
		"site.ap_defaults.poe_constrained": func(s *model.State) {
			s.Sites[0].APDefaults.PoEConstrained = model.PoEDisableRadios
		},
		"site.rf.band_24.preamble": func(s *model.State) { s.Sites[0].RF.Band24.Preamble = "long" },
		"ap.led_brightness":        func(s *model.State) { b := 7; s.APs[0].LEDBrightness = &b },
	}
	for key, toggle := range toggles {
		option, ok := registry.Get(key)
		if !ok {
			t.Fatalf("unknown option %s", key)
		}
		base := newTypedState()
		_, before := compileTypedAP(t, base, typedAP)
		changedState := newTypedState()
		toggle(&changedState)
		_, after := compileTypedAP(t, changedState, typedAP)
		for _, path := range changedPaths(before, after, "") {
			allowed := false
			for _, declared := range option.FirmwarePaths {
				allowed = allowed || path == declared || strings.HasPrefix(path, declared+".") || strings.HasPrefix(path, declared+"[]")
			}
			if !allowed {
				t.Errorf("%s changed %s, not in its firmware paths %v", key, path, option.FirmwarePaths)
			}
		}
	}
}

// changedPaths lists pattern paths whose values differ.
func changedPaths(a, b any, pattern string) []string {
	ao, aok := a.(map[string]any)
	bo, bok := b.(map[string]any)
	if aok && bok {
		var out []string
		keys := map[string]bool{}
		for k := range ao {
			keys[k] = true
		}
		for k := range bo {
			keys[k] = true
		}
		for k := range keys {
			out = append(out, changedPaths(ao[k], bo[k], joinPattern(pattern, k))...)
		}
		return out
	}
	aa, aaok := a.([]any)
	ba, baok := b.([]any)
	if aaok && baok && len(aa) == len(ba) {
		var out []string
		for i := range aa {
			out = append(out, changedPaths(aa[i], ba[i], joinPattern(pattern, "[]"))...)
		}
		return out
	}
	if equalTree(a, b) {
		return nil
	}
	return []string{pattern}
}
