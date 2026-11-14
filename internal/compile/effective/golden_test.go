package effective_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
)

var update = flag.Bool("update", false, "rewrite the normaliser golden outputs (bump effective.Version first if an output changes)")

const sshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"

// compiled builds a config the v0 way from the bundled template.
func compiled(t *testing.T, site model.Site, networks ...model.Network) []byte {
	t.Helper()
	if site.ID == "" {
		site.ID, site.Name = "site-a", "Example"
	}
	if len(networks) == 0 {
		networks = []model.Network{{ID: "6f2000aa-0000-4000-8000-000000000001", SiteID: site.ID, SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}}
	}
	ap := model.APRecord{ID: "02-aa-00-00-00-01", Name: "Example AP", SiteID: site.ID}
	raw, err := compile.BuildSiteConfig(legacyv0.Template(nil), site, ap, networks, time.Unix(1794475905, 0))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// patchWLAN merges patch into WLANs[0] after compiling, for security types
// the v0 compiler cannot produce (it forces Auth.Type psk).
func patchWLAN(t *testing.T, config []byte, patch string) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil {
		t.Fatal(err)
	}
	var wlans []json.RawMessage
	_ = json.Unmarshal(root["WLANs"], &wlans)
	merged, err := compile.MergePatch(wlans[0], json.RawMessage(patch))
	if err != nil {
		t.Fatal(err)
	}
	wlans[0] = merged
	root["WLANs"], _ = json.Marshal(wlans)
	out, _ := json.Marshal(root)
	return out
}

func radiosPatch(radios string) model.Site {
	return model.Site{ID: "site-a", Name: "Example", Country: "AU", Advanced: json.RawMessage(`{"Radios":` + radios + `}`)}
}

type goldenCase struct {
	name   string
	config func(t *testing.T) []byte
	ctx    effective.Context
}

func goldenCases() []goldenCase {
	akm := func(flags string) string { return `{"Auth":{"AKM":` + flags + `}}` }
	return []goldenCase{
		{"template-only", func(t *testing.T) []byte { return compiled(t, model.Site{}) }, effective.Context{}},
		{"live-like", func(t *testing.T) []byte {
			site := radiosPatch(`[{"Band":"2.4","CCRev":"AU/890","CCRevSP":"AU/890","Channel":1,"Width":20,"Power":12,"RRM":{"Channel":1,"Width":20,"Power":12}},{"Band":"5","CCRev":"AU/890","CCRevSP":"AU/890","Channel":149,"Width":80,"Power":17,"RRM":{"Channel":149,"Width":80,"Power":17}}]`)
			site.SSHPublicKeys = []string{sshKey}
			network := model.Network{ID: "6f2000aa-0000-4000-8000-000000000001", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true,
				Advanced: json.RawMessage(`{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true,"WPA2_PSK_SHA256":false,"WPA2_PSK_FT":false,"WPA2_EAP":false,"WPA2_EAP_SHA256":false,"WPA2_EAP_FT":false,"WPA_PSK":false,"WPA_EAP":false,"WPA3_PSK_FT":false,"WPA3_OWE":false,"WPA3_EAP":false,"WPA3_EAP_FT":false,"WPA3_SUITEB":false,"WPA3_PSK_SAE_EXT":false,"WPA3_PSK_FT_SAE_EXT":false},"Pairwise":{"GCMP256":false},"WEP":null},"OWE":{"Transition":false}}`)}
			return compiled(t, site, network)
		}, effective.Context{LocalFiles: effective.LocalFilesNone}},
		{"security-wpa2-personal", func(t *testing.T) []byte { return compiled(t, model.Site{}) }, effective.Context{LocalFiles: effective.LocalFilesNone}},
		{"security-wpa2-wpa3-personal", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), akm(`{"WPA2_PSK":true,"WPA3_PSK":true}`))
		}, effective.Context{}},
		{"security-wpa3-personal", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), akm(`{"WPA2_PSK":false,"WPA3_PSK":true}`))
		}, effective.Context{}},
		{"security-wpa3-sae-ext", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), akm(`{"WPA2_PSK":false,"WPA3_PSK_SAE_EXT":true}`))
		}, effective.Context{}},
		{"security-wpa2-personal-11r", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), `{"roamMode":"11r"}`)
		}, effective.Context{}},
		{"security-wpa2-wpa3-personal-11r", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), `{"roamMode":"11r","Auth":{"AKM":{"WPA3_PSK":true}}}`)
		}, effective.Context{}},
		{"security-owe", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), `{"Auth":{"Type":"owe","PSK":"","AKM":null,"Pairwise":null}}`)
		}, effective.Context{}},
		{"security-wpa2-enterprise", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), `{"Auth":{"Type":"eap","PSK":"","AKM":null}}`)
		}, effective.Context{}},
		{"security-wpa3-enterprise", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), `{"Auth":{"Type":"eap","PSK":"","AKM":{"WPA2_PSK":false,"WPA3_EAP":true}}}`)
		}, effective.Context{}},
		{"security-open", func(t *testing.T) []byte {
			return patchWLAN(t, compiled(t, model.Site{}), `{"Auth":{"Type":"open","PSK":"","AKM":null,"Pairwise":null}}`)
		}, effective.Context{}},
		{"rf-template-default", func(t *testing.T) []byte { return compiled(t, model.Site{}) }, effective.Context{}},
		{"rf-static", func(t *testing.T) []byte {
			return compiled(t, radiosPatch(`[{"Band":"2.4","CCRev":"AU/890","Channel":6,"Width":20,"Power":10},{"Band":"5","CCRev":"AU/890","Channel":36,"Width":80,"Power":14}]`))
		}, effective.Context{}},
		{"rf-rrm-only", func(t *testing.T) []byte {
			return compiled(t, radiosPatch(`[{"Band":"2.4","CCRev":"AU/890","RRM":{"Channel":11,"Width":20,"Power":8}},{"Band":"5","CCRev":"AU/890","Channel":0,"Width":20,"RRM":{"Channel":157,"Width":40,"Power":12}}]`))
		}, effective.Context{}},
		{"rf-power-cap-and-width-precedence", func(t *testing.T) []byte {
			return compiled(t, radiosPatch(`[{"Band":"2.4","Channel":1,"Width":40,"Power":30,"RRM":{"Width":20}},{"Band":"5","Disabled":true,"Channel":149,"Width":80,"RRM":{"Width":40,"Power":40}}]`))
		}, effective.Context{}},
		{"local-files-present", func(t *testing.T) []byte { return compiled(t, model.Site{}) }, effective.Context{LocalFiles: effective.LocalFilesPresent}},
	}
}

type golden struct {
	Version   int              `json:"version"`
	Report    effective.Report `json:"report"`
	Canonical json.RawMessage  `json:"canonical"`
}

// TestGoldenOutputs pins the canonical output of every fixture and enforces
// the version rule: an output may change only together with a Version bump
// (design §4.5).
func TestGoldenOutputs(t *testing.T) {
	seen := map[string]bool{}
	for _, test := range goldenCases() {
		t.Run(test.name, func(t *testing.T) {
			if seen[test.name] {
				t.Fatal("duplicate case")
			}
			seen[test.name] = true
			canonical, report, err := effective.Normalise(test.config(t), test.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.TypeErrors) != 0 {
				t.Fatalf("type errors in a compiled fixture: %v", report.TypeErrors)
			}
			path := filepath.Join("testdata", test.name+".json")
			if *update {
				raw, _ := json.MarshalIndent(golden{Version: effective.Version, Report: report, Canonical: canonical}, "", " ")
				if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("missing golden (run with -update): %v", err)
			}
			var want golden
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatal(err)
			}
			var wantCanonical bytes.Buffer
			_ = json.Compact(&wantCanonical, want.Canonical)
			same := bytes.Equal(wantCanonical.Bytes(), canonical)
			switch {
			case same && want.Version == effective.Version:
			case !same && want.Version == effective.Version:
				t.Fatalf("normaliser output changed without bumping effective.Version (%d)\ngot:  %s\nwant: %s", effective.Version, canonical, wantCanonical.Bytes())
			default:
				t.Fatalf("effective.Version is %d but the golden was made by %d: rerun with -update", effective.Version, want.Version)
			}
		})
	}
}

// Every fixture the compiler produces type-checks against the 0.15 schema
// (registry check 3, design §4.2).
func TestCompiledFixturesTypeCheck(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "testdata", "golden", "*.json"))
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Fatal("no compile goldens")
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, report, err := effective.Normalise(raw, effective.Context{})
		if err != nil || len(report.TypeErrors) != 0 {
			t.Fatalf("%s: %v %v", path, err, report.TypeErrors)
		}
	}
	_, report, err := effective.Normalise(legacyv0.Template(nil), effective.Context{})
	if err != nil || len(report.TypeErrors) != 0 {
		t.Fatalf("bundled template: %v %v", err, report.TypeErrors)
	}
}
