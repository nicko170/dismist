package migrate

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

// Synthetic identities (public repository: no real IDs, MACs or SSIDs).
const (
	homeSite    = "8e31882f-0000-4000-8000-0000000000aa"
	liveAP      = "02-aa-00-00-00-37"
	secondAP    = "02-aa-00-00-00-38"
	homeNetwork = "6f2000aa-0000-4000-8000-000000000001"
	guestNet    = "6f2000aa-0000-4000-8000-000000000002"
	siteKey     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"
)

// livePatches are the production AP's two advanced patches, exactly as
// the operator described them: the network patch turning on WPA2+WPA3 PSK
// with PMF optional, and the site patch replacing Radios[] (AU/890 on both
// access radios, 5 GHz 149/80 at 17 dBm and 2.4 GHz 1/20 at 12 dBm, every
// value in both Channel/Width/Power and RRM) plus Monitor.CCRev AU/890.
const liveNetworkPatch = `{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}`

func liveRadios(full bool) []map[string]any {
	radios := []map[string]any{
		{"Band": "2.4", "CCRev": "AU/890", "CCRevSP": "AU/890", "Channel": 1, "Width": 20, "Power": 12,
			"RRM": map[string]any{"Channel": 1, "Width": 20, "Power": 12}},
		{"Band": "5", "CCRev": "AU/890", "CCRevSP": "AU/890", "Channel": 149, "Width": 80, "Power": 17,
			"RRM": map[string]any{"Channel": 149, "Width": 80, "Power": 17}},
	}
	if !full {
		return radios
	}
	// The same radios as complete copies of the template elements (QOS
	// tables, preamble, probe control and the rest included).
	var template map[string]any
	_ = json.Unmarshal(legacyv0.BundledTemplate(), &template)
	elements, _ := template["Radios"].([]any)
	for index, element := range elements {
		base := element.(map[string]any)
		for key, value := range radios[index] {
			if key == "RRM" {
				rrm := base["RRM"].(map[string]any)
				for k, v := range value.(map[string]any) {
					rrm[k] = v
				}
				continue
			}
			base[key] = value
		}
		radios[index] = base
	}
	return radios
}

func liveSitePatch(t *testing.T, full, monitor bool) json.RawMessage {
	t.Helper()
	patch := map[string]any{"Radios": liveRadios(full)}
	if monitor {
		patch["Monitor"] = map[string]any{"CCRev": "AU/890"}
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type scenario struct {
	full, monitor bool
	extra         func(*model.State)
	vaps          map[string][]model.VAPRecord
	opts          func(*Options)
}

var liveTime = time.Date(2026, 11, 14, 7, 56, 2, 500000000, time.UTC)

func liveStore(t *testing.T, sc scenario) *model.BoltStore {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: homeSite, Name: "Home", Country: "AU", Timezone: "Australia/Sydney",
			SSHPublicKeys: []string{siteKey}, Advanced: liveSitePatch(t, sc.full, sc.monitor)}}
		state.APs = []model.APRecord{{ID: liveAP, Name: "Hallway", SiteID: homeSite, Secret: secret,
			ConfigRevision: 40, ConfigUpdatedAt: liveTime, AppliedRevision: 40, LocalFiles: []model.LocalFile{}}}
		state.Networks = []model.Network{{ID: homeNetwork, SiteID: homeSite, SSID: "Example", PSK: "example-passphrase-01",
			VLANID: 1, Enabled: true, Advanced: json.RawMessage(liveNetworkPatch)}}
		if sc.extra != nil {
			sc.extra(state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	vaps := sc.vaps
	if vaps == nil {
		vaps = map[string][]model.VAPRecord{liveAP: {
			{Dev: "wl0.1", BSSID: "02:aa:00:00:01:01", WLANID: homeNetwork, RadioIndex: 0},
			{Dev: "wl1.1", BSSID: "02:aa:00:00:01:11", WLANID: homeNetwork, RadioIndex: 1},
		}}
	}
	for apID, list := range vaps {
		if _, err := store.APStatus().SetVAPs(apID, now, list); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// oldEffective is what every AP runs before the migration.
func oldEffective(t *testing.T, store *model.BoltStore) map[string][]byte {
	t.Helper()
	var frozen legacyv0.State
	if err := store.View(func(tx *bolt.Tx) error {
		var err error
		frozen, err = legacyv0.ReadState(tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	result := map[string][]byte{}
	for _, ap := range state.APs {
		raw, err := legacyv0.CompileAP(legacyv0.BundledTemplate(), frozen, ap.ID)
		if err != nil {
			continue
		}
		canonical, _, err := effective.Normalise(raw, compile.EffectiveContext(ap))
		if err != nil {
			t.Fatal(err)
		}
		result[ap.ID] = canonical
	}
	return result
}

// legacyCompile is the config the legacy compiler builds for one AP.
func legacyCompile(t *testing.T, store *model.BoltStore, apID string) []byte {
	t.Helper()
	var frozen legacyv0.State
	if err := store.View(func(tx *bolt.Tx) error {
		var err error
		frozen, err = legacyv0.ReadState(tx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := legacyv0.CompileAP(legacyv0.BundledTemplate(), frozen, apID)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// legacyBytes returns the raw legacy members of every record.
func legacyBytes(t *testing.T, store *model.BoltStore) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := store.View(func(tx *bolt.Tx) error {
		for _, bucket := range []string{model.BucketSites, model.BucketAPs, model.BucketNetworks} {
			if err := tx.Bucket([]byte(bucket)).ForEach(func(key, raw []byte) error {
				var record map[string]json.RawMessage
				if err := json.Unmarshal(raw, &record); err != nil {
					return err
				}
				for _, member := range []string{"advanced", "ssh_public_keys", "psk", "ssid", "vlan_id", "enabled", "name", "secret", "config_revision", "config_updated_at"} {
					result[bucket+"/"+string(key)+"/"+member] = string(record[member])
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func runMigration(t *testing.T, store *model.BoltStore, sc scenario) Result {
	t.Helper()
	opts := Options{Context: Context{Now: now}, DiskUsage: plenty}
	if sc.opts != nil {
		sc.opts(&opts)
	}
	result, err := Run(store, Steps(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func compiledEffective(t *testing.T, store *model.BoltStore, apID string) []byte {
	t.Helper()
	view, err := compile.Effective(legacyv0.BundledTemplate(), store.Snapshot(), apID, store.HashKey())
	if err != nil {
		t.Fatal(err)
	}
	return view.Canonical
}

func residual(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestLiveProductionMigration is the production safety fixture: the live
// AP's two advanced patches lift into typed settings, every residual is
// empty, the normalised effective config is byte-identical, and the legacy
// fields, revisions and compat are untouched.
func TestLiveProductionMigration(t *testing.T) {
	for _, full := range []bool{false, true} {
		name := "minimal-radios"
		if full {
			name = "template-complete-radios"
		}
		t.Run(name, func(t *testing.T) {
			sc := scenario{full: full, monitor: true}
			store := liveStore(t, sc)
			before := oldEffective(t, store)
			legacy := legacyBytes(t, store)
			revisionBefore := store.Snapshot().Revision
			result := runMigration(t, store, sc)
			if result.State.State != StateCommitted {
				t.Fatalf("migration refused: %s %v\n%s", result.State.Reason, result.State.Paths, result.State.Report)
			}
			state := store.Snapshot()
			if !state.Typed() || store.SchemaStatus().Config != 2 {
				t.Fatalf("state not typed: %+v", store.SchemaStatus())
			}
			// Equivalence: what the AP runs is unchanged.
			if got := compiledEffective(t, store, liveAP); !bytes.Equal(got, before[liveAP]) {
				t.Fatalf("effective config changed: %v", compile.DiffPaths(before[liveAP], got))
			}
			// Legacy fields byte-identical (rollback compiles today's config).
			if after := legacyBytes(t, store); !reflect.DeepEqual(after, legacy) {
				for key, value := range legacy {
					if after[key] != value {
						t.Errorf("legacy member %s changed: %s -> %s", key, value, after[key])
					}
				}
			}
			info, _ := store.Schema()
			if info.Schema != 2 || info.Compat != 1 {
				t.Fatalf("schema %+v (compat must stay 1)", info)
			}
			if state.Revision != revisionBefore+1 {
				t.Fatalf("revision %d", state.Revision)
			}
			// The lift (design §4.7 "Expected result for the live site").
			network := state.Networks[0]
			if network.Security != model.SecurityWPA2WPA3Personal || network.PMF != "" || !network.BandSteering ||
				network.IdleTimeout != 3600 || network.BSSIDIndex != 1 || network.ReclaimTimeoutSec != 600 ||
				network.UcastDisassocOnBssDown != nil || network.Rates != "compatible" || network.DTIM != 2 {
				t.Fatalf("network lift %+v", network)
			}
			if len(network.Expert) != 0 {
				t.Fatalf("INTERNET residual must be empty, got %s", network.Expert)
			}
			site := state.Sites[0]
			if !site.RF.Managed || site.RF.Band5.Width != 80 || site.RF.Band5.Power != (model.PowerPolicy{Mode: model.PowerFixed, DBm: 17}) ||
				site.RF.Band24.Width != 20 || site.RF.Band24.Power != (model.PowerPolicy{Mode: model.PowerFixed, DBm: 12}) ||
				!site.RF.Band24.Enabled || !site.RF.Band5.Enabled {
				t.Fatalf("RF lift %+v", site.RF)
			}
			if !reflect.DeepEqual(site.RF.Band24.Channels, []int{1, 6, 11}) || !reflect.DeepEqual(site.RF.Band5.Channels, []int{36, 40, 44, 48, 149, 153, 157, 161}) {
				t.Fatalf("allowed channels %v %v", site.RF.Band24.Channels, site.RF.Band5.Channels)
			}
			if !site.SSH.Enabled || len(site.SSH.Keys) != 1 || site.APDefaults.LEDBrightness != 255 ||
				site.APDefaults.PoEConstrained != model.PoEDisableRadios || site.APDefaults.HostnameFromName {
				t.Fatalf("site lift %+v %+v", site.SSH, site.APDefaults)
			}
			ap := state.APs[0]
			if ap.RF["5"] == nil || ap.RF["5"].Channel != 149 || ap.RF["2.4"] == nil || ap.RF["2.4"].Channel != 1 ||
				ap.RF["5"].PowerDBm != 0 || ap.RF["5"].Width != 0 {
				t.Fatalf("AP rf_override %+v %+v", ap.RF["5"], ap.RF["2.4"])
			}
			if ap.ConfigRevision != 40 || !ap.ConfigUpdatedAt.Equal(liveTime) {
				t.Fatalf("AP revision moved: %d %v", ap.ConfigRevision, ap.ConfigUpdatedAt)
			}
			if len(ap.Expert) != 0 {
				t.Fatalf("AP residual %s", ap.Expert)
			}
			siteResidual := residual(t, site.Expert)
			if full {
				if siteResidual != nil {
					t.Fatalf("site residual must be empty with template-complete radios: %s", site.Expert)
				}
			} else {
				// The patch dropped the template's QOS tables, probe control
				// and non-aggregate regulation: those stay removed, leaf by
				// leaf, and nothing else.
				radios, _ := siteResidual["Radios"].([]any)
				if len(radios) != 2 || len(siteResidual) != 1 {
					t.Fatalf("site residual %s", site.Expert)
				}
				for _, element := range radios {
					radio := element.(map[string]any)
					if _, ok := radio["QOS"]; !ok {
						t.Fatalf("site residual keeps the template QOS tables: %s", site.Expert)
					}
					for key := range radio {
						switch key {
						case "Band", "QOS", "NonAggrRegulate", "ProbeControl", "DFSjumplist", "PreambleLength":
						default:
							t.Fatalf("unexpected residual key %s in %s", key, site.Expert)
						}
					}
				}
			}
			// ap_config.desired is seeded with the v2 bytes at the AP's
			// own revision and Timestamp: nothing is pushed.
			desired, found, err := store.APConfig().Desired(liveAP)
			if err != nil || !found || desired.Revision != 40 || desired.Timestamp != compile.ConfigTimestamp(liveTime) {
				t.Fatalf("desired %+v %v %v", desired, found, err)
			}
			// The legacy compile is pinned and served unchanged at this
			// revision: the upgrade sends the AP the previous release's bytes.
			if want := legacyCompile(t, store, liveAP); !bytes.Equal(desired.Pinned, want) {
				t.Fatalf("pinned bytes are not the legacy compile:\n%s\n%s", desired.Pinned, want)
			}
			served := compile.Prepare(legacyv0.BundledTemplate(), store.Snapshot(), store.APConfig())
			if len(served) != 1 || !served[0].Pinned || !bytes.Equal(served[0].Config, desired.Pinned) || served[0].Revision != 40 {
				t.Fatalf("served %+v", served)
			}
			// History and report.
			record, found, err := store.ConfigHistory().Get(state.Revision)
			if err != nil || !found || record.Kind != model.ChangeMigration || !strings.Contains(record.Summary, "Migrated to schema 2") {
				t.Fatalf("history %+v %v %v", record, found, err)
			}
			report, found, err := ReadReport(store)
			if err != nil || !found || !report.Verified || len(report.APs) != 1 || !report.APs[0].Equal || report.Networks[0].BSSIDSource != "measured" {
				t.Fatalf("report %+v", report)
			}
			if bytes.Contains(record.Report, []byte("example-passphrase")) {
				t.Fatal("report leaks the passphrase")
			}
			// A second start does nothing.
			again := runMigration(t, store, sc)
			if len(again.Ran) != 0 {
				t.Fatalf("migrated twice: %+v", again)
			}
			// Change detection: the migrated state compiles to the desired
			// bytes, so the first mutation that changes nothing pushes
			// nothing.
			siteResult, err := compile.Site(compile.SiteInput{Template: legacyv0.BundledTemplate(), State: store.Snapshot(), Desired: store.APConfig()})
			if err != nil || len(siteResult.Changed) != 0 || len(siteResult.Failed) != 0 {
				t.Fatalf("change detection after migration %+v %v", siteResult, err)
			}
		})
	}
}

// With Monitor.CCRev left empty the site still lifts as managed (only the
// access radios count) and the scan radio's US/0 stays in the residual.
func TestLiveMigrationWithoutMonitorCCRev(t *testing.T) {
	sc := scenario{full: true, monitor: false}
	store := liveStore(t, sc)
	before := oldEffective(t, store)
	result := runMigration(t, store, sc)
	if result.State.State != StateCommitted {
		t.Fatalf("refused: %s\n%s", result.State.Reason, result.State.Report)
	}
	state := store.Snapshot()
	if !state.Sites[0].RF.Managed {
		t.Fatal("managed must be true")
	}
	if got := string(state.Sites[0].Expert); got != `{"Monitor":{"CCRev":""}}` {
		t.Fatalf("residual %s", got)
	}
	if got := compiledEffective(t, store, liveAP); !bytes.Equal(got, before[liveAP]) {
		t.Fatalf("effective changed: %v", compile.DiffPaths(before[liveAP], got))
	}
	// The residual shadows part of site.country (Monitor.CCRev): partly.
	input, _ := compile.InputFor(legacyv0.BundledTemplate(), state, liveAP)
	output, err := compile.AP(input)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, shadow := range output.Shadowed {
		if shadow.Option == "site.country" && shadow.Partly && shadow.By == "site" {
			found = true
		}
	}
	if !found {
		t.Fatalf("shadow report %+v", output.Shadowed)
	}
}

// A 5 GHz radio on a DFS channel without a DFS fallback in the running
// config lifts as unmanaged RF, with a reason, instead of refusing the
// whole migration on the firmware rule; with a fallback of the typed
// shape it lifts as managed with that fallback.
func TestLiveMigrationDFSChannel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		jumplist any
		managed  bool
		fallback int
	}{
		{"no fallback", nil, false, 0},
		{"typed fallback", map[string]any{"ChanSpecs": []any{map[string]any{"Channel": 36, "Width": 80}}}, true, 36},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := scenario{full: true, monitor: true, extra: func(state *model.State) {
				radios := liveRadios(true)
				for _, radio := range radios {
					if radio["Band"] == "5" {
						radio["Channel"], radio["RRM"].(map[string]any)["Channel"] = 100, 100
						if tc.jumplist != nil {
							radio["DFSjumplist"] = tc.jumplist
						}
					}
				}
				raw, _ := json.Marshal(map[string]any{"Radios": radios, "Monitor": map[string]any{"CCRev": "AU/890"}})
				state.Sites[0].Advanced = raw
			}}
			store := liveStore(t, sc)
			before := oldEffective(t, store)
			result := runMigration(t, store, sc)
			if result.State.State != StateCommitted {
				t.Fatalf("refused: %s\n%s", result.State.Reason, result.State.Report)
			}
			if got := compiledEffective(t, store, liveAP); !bytes.Equal(got, before[liveAP]) {
				t.Fatalf("effective config changed: %v", compile.DiffPaths(before[liveAP], got))
			}
			site := store.Snapshot().Sites[0]
			if site.RF.Managed != tc.managed || (tc.managed && site.RF.Band5.DFSFallback != tc.fallback) {
				t.Fatalf("RF lift managed %v fallback %d", site.RF.Managed, site.RF.Band5.DFSFallback)
			}
			report, _, _ := ReadReport(store)
			if !tc.managed && (len(report.Sites) != 1 || !strings.Contains(report.Sites[0].RFReason, "DFS")) {
				t.Fatalf("report %+v", report.Sites)
			}
		})
	}
}
