package options

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/options/firmware"
)

// Check 1 (design §4.2): every declared option is valid, every firmware path
// exists in ap33-0.15.paths, and no option writes a never-tier path.
func TestRegistryDeclarationsAreValid(t *testing.T) {
	if _, err := New(Declarations(), PathExists); err != nil {
		t.Fatal(err)
	}
	_ = Default()
}

// The never-tier list names real 0.15 paths (a typo would silently allow
// the field it meant to forbid).
func TestNeverPathsAreRealPaths(t *testing.T) {
	schema := firmware.AP33()
	for _, never := range NeverPaths() {
		path := strings.TrimSuffix(never, "*")
		if strings.HasSuffix(never, "*") {
			found := false
			for _, candidate := range schema.Paths() {
				if strings.HasPrefix(candidate, path) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("never prefix %s matches no 0.15 path", never)
			}
			continue
		}
		if _, ok := schema.Lookup(path); !ok {
			t.Errorf("never path %s is not a 0.15 path", never)
		}
	}
	for path, want := range map[string]bool{
		"Ipsec": true, "Ipsec.Enabled": true, "WLANs[].Wxlan.PortalURL": true, "WLANs[].Auth.WEP.Keys[]": true,
		"WLANs[].Hidden": false, "WLANs[].Auth.PSK": false, "DefaultX": false,
	} {
		if _, got := NeverPath(path); got != want {
			t.Errorf("NeverPath(%s) = %v", path, got)
		}
	}
}

// Check 4: generated files are up to date.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	for _, target := range []struct {
		path string
		gen  func(*bytes.Buffer) error
	}{
		{filepath.Join("..", "..", "ui", "src", "gen", "options.gen.ts"), func(b *bytes.Buffer) error { return GenerateTS(b, Default()) }},
		{filepath.Join("..", "..", "docs", "design", "options.gen.md"), func(b *bytes.Buffer) error { return GenerateMarkdown(b, Default()) }},
	} {
		// docs/design is not part of the public repository; without it
		// (a clean checkout, CI) the generator skips the reference too.
		if _, err := os.Stat(filepath.Dir(target.path)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		var want bytes.Buffer
		if err := target.gen(&want); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(target.path)
		if err != nil {
			t.Fatalf("%s: %v (run go generate ./internal/options)", target.path, err)
		}
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("%s is stale; run go generate ./internal/options", target.path)
		}
	}
}

// Check 6: the bundled template is frozen at the hash legacyv0 records.
func TestTemplateHashIsFrozen(t *testing.T) {
	if got := legacyv0.SHA256Hex(aptemplate.Default()); got != legacyv0.TemplateSHA256 {
		t.Fatalf("internal/aptemplate/ap33.json changed (sha256 %s, frozen %s). Template edits change every existing AP; use typed options with Default and LegacyDefault instead (design §4.2 check 6)", got, legacyv0.TemplateSHA256)
	}
}

func sample(key string, scope Scope, tier Tier) Option {
	control := "switch"
	if tier == TierExpert || tier == TierReadonly {
		control = ""
	}
	return Option{
		Key: key, Scope: scope, Label: "Example", Control: control, Type: "bool", Tier: tier,
		Confidence: Confirmed, Disruption: DisruptionWLANRestart, Phase: 2,
		FirmwarePaths: []string{"WLANs[].Hidden"}, Catalogue: "wlans.md §5.1",
	}
}

func TestRegistryValidation(t *testing.T) {
	good := sample("wlan.hidden", ScopeWLAN, TierBasic)
	if _, err := New([]Option{good}, PathExists); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Option){
		"unknown path":      func(o *Option) { o.FirmwarePaths = []string{"WLANs[].NoSuchField"} },
		"never path":        func(o *Option) { o.FirmwarePaths = []string{"WLANs[].Auth.WEP.KeyIndex"} },
		"scope prefix":      func(o *Option) { o.Key = "site.hidden" },
		"expert control":    func(o *Option) { o.Tier = TierExpert },
		"basic no control":  func(o *Option) { o.Control = "" },
		"never tier":        func(o *Option) { o.Tier = TierNever },
		"bad confidence":    func(o *Option) { o.Confidence = "maybe" },
		"bad disruption":    func(o *Option) { o.Disruption = "some" },
		"bad type":          func(o *Option) { o.Type = "float" },
		"min above max":     func(o *Option) { o.Min, o.Max = Int(5), Int(1) },
		"no catalogue":      func(o *Option) { o.Catalogue = "" },
		"no paths":          func(o *Option) { o.FirmwarePaths = nil },
		"duplicate enum":    func(o *Option) { o.Enum = []EnumValue{{Value: "a"}, {Value: "a"}} },
		"unknown condition": func(o *Option) { o.VisibleWhen = []Condition{{Key: "wlan.nope", In: []string{"x"}}} },
		"upper case key":    func(o *Option) { o.Key = "wlan.Hidden" },
	} {
		option := good
		mutate(&option)
		if _, err := New([]Option{option}, PathExists); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := New([]Option{good, good}, PathExists); err == nil {
		t.Error("duplicate key accepted")
	}
}

// Check 5: expert options have no form control and are not in the
// generated forms.
func TestExpertOptionsStayOutOfGeneratedForms(t *testing.T) {
	expert := sample("wlan.ucast_disassoc_on_bss_down", ScopeWLAN, TierExpert)
	expert.FirmwarePaths = []string{"WLANs[].UcastDisassocOnBssDown"}
	expert.Default, expert.LegacyDefault = true, false
	basic := sample("wlan.hidden", ScopeWLAN, TierBasic)
	basic.Enum = nil
	registry, err := New([]Option{expert, basic}, PathExists)
	if err != nil {
		t.Fatal(err)
	}
	var ts, md bytes.Buffer
	if err := GenerateTS(&ts, registry); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ts.String(), "ucast_disassoc") || !strings.Contains(ts.String(), `"wlan.hidden": z.boolean()`) {
		t.Fatalf("generated TS:\n%s", ts.String())
	}
	if err := GenerateMarkdown(&md, registry); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "`wlan.ucast_disassoc_on_bss_down`") {
		t.Fatal("expert option missing from the reference table")
	}
}

func TestCheckExperimental(t *testing.T) {
	security := sample("wlan.security", ScopeWLAN, TierBasic)
	security.Type, security.Control = "string", "select"
	security.FirmwarePaths = []string{"WLANs[].Auth.Type"}
	security.Enum = []EnumValue{{Value: "wpa2_personal"}, {Value: "owe", Confidence: NeedsLiveTest}}
	rate := sample("wlan.rate_limit", ScopeWLAN, TierAdvanced)
	rate.Confidence = NeedsLiveTest
	rate.FirmwarePaths = []string{"WLANs[].RateLimit.ClientUp"}
	registry, err := New([]Option{security, rate}, PathExists)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.CheckExperimental(false, map[string]any{"wlan.security": "wpa2_personal"}); err != nil {
		t.Fatal(err)
	}
	err = registry.CheckExperimental(false, map[string]any{"wlan.security": "owe", "wlan.rate_limit": true})
	var experimental *ExperimentalError
	if !errors.As(err, &experimental) || experimental.Code() != "experimental_disabled" || len(experimental.Keys) != 2 {
		t.Fatalf("experimental values accepted: %v", err)
	}
	if err := registry.CheckExperimental(true, map[string]any{"wlan.security": "owe"}); err != nil {
		t.Fatal("experimental site refused")
	}
}

func TestDisruptionUnion(t *testing.T) {
	if Union() != DisruptionNone || Union(DisruptionWLANRestart, DisruptionRadioBounce, DisruptionNone) != DisruptionRadioBounce {
		t.Fatal("union")
	}
}
