package compile_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

const (
	apOne   = "02-aa-00-00-00-01"
	apTwo   = "02-aa-00-00-00-02"
	apOther = "02-aa-00-00-00-03"
)

var secret32 = bytes.Repeat([]byte{0x11}, model.SecretSize)

func siteKey() string {
	return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"
}

// newStore makes a store with two sites: site-a (two APs, one WLAN) and
// site-b (one AP), and seeds ap_config.desired through a first Apply.
func newStore(t *testing.T) *model.BoltStore {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutate(t, store, nil, func(state *model.State) {
		state.Sites = []model.Site{{ID: "site-a", Name: "Example A", Country: "AU"}, {ID: "site-b", Name: "Example B"}}
		state.APs = []model.APRecord{
			{ID: apOne, Name: "one", SiteID: "site-a", Secret: secret32},
			{ID: apTwo, Name: "two", SiteID: "site-a", Secret: secret32},
			{ID: apOther, Name: "other", SiteID: "site-b", Secret: secret32},
		}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 10, Enabled: true}}
	})
	return store
}

// mutate runs a config mutation the way httpapi.applyChange does from M0:
// the change, then compile.Apply inside the same transaction.
func mutate(t *testing.T, store *model.BoltStore, sites []string, change func(*model.State)) compile.Applied {
	t.Helper()
	var applied compile.Applied
	previous := store.Snapshot()
	err := store.UpdateTx(func(state *model.State, tx *model.Tx) error {
		change(state)
		var err error
		applied, err = compile.Apply(state, tx.APConfig(), compile.ApplyOptions{
			Template: aptemplate.Default(), Sites: sites, Previous: &previous, Now: state.UpdatedAt,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return applied
}

func revisions(state model.State) map[string]uint64 {
	result := map[string]uint64{}
	for _, ap := range state.APs {
		result[ap.ID] = ap.ConfigRevision
	}
	return result
}

// TestDismistOnlyEditsChangeNoConfigRevision covers design §4.4: a change
// that touches only dismist-side fields leaves every ConfigRevision
// unchanged and queues no push.
func TestDismistOnlyEditsChangeNoConfigRevision(t *testing.T) {
	store := newStore(t)
	for _, test := range []struct {
		name   string
		sites  []string
		change func(*model.State)
	}{
		{"site rename", []string{"site-a"}, func(s *model.State) { s.Sites[0].Name = "Renamed" }},
		{"site timezone", []string{"site-a"}, func(s *model.State) { s.Sites[0].Timezone = "Australia/Sydney" }},
		{"AP pin", []string{"site-a"}, func(s *model.State) { s.APs[0].ManagementIP = "192.0.2.10" }},
		{"AP secret", []string{"site-a"}, func(s *model.State) { s.APs[1].Secret = bytes.Repeat([]byte{0x22}, 32) }},
		{"AP model and firmware", nil, func(s *model.State) { s.APs[2].Model = "AP33"; s.APs[2].Firmware = "apfw-example" }},
		{"local files and features", nil, func(s *model.State) {
			s.APs[0].LocalFiles = []model.LocalFile{}
			s.APs[0].Features = map[string]bool{"pong2": true}
		}},
		{"no-op", nil, func(*model.State) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := store.Snapshot()
			applied := mutate(t, store, test.sites, test.change)
			after := store.Snapshot()
			if len(applied.Pushes) != 0 || len(applied.Result.Changed) != 0 {
				t.Fatalf("pushes %v changed %v", applied.Pushes, applied.Result.Changed)
			}
			if !reflect.DeepEqual(revisions(before), revisions(after)) {
				t.Fatalf("ConfigRevision moved: %v -> %v", revisions(before), revisions(after))
			}
			if after.Revision != before.Revision+1 {
				t.Fatal("the mutation itself was not committed")
			}
		})
	}
}

func TestOnlyChangedAPsAreBumped(t *testing.T) {
	store := newStore(t)
	before := store.Snapshot()
	// Renaming AP one changes its Name and Hostname, nobody else's.
	applied := mutate(t, store, []string{"site-a"}, func(s *model.State) { s.APs[0].Name = "renamed" })
	after := store.Snapshot()
	if !reflect.DeepEqual(applied.Result.Changed, []string{apOne}) || len(applied.Pushes) != 1 || applied.Pushes[0].APID != apOne {
		t.Fatalf("changed %v pushes %+v", applied.Result.Changed, applied.Pushes)
	}
	if after.APs[0].ConfigRevision != after.Revision || !after.APs[0].ConfigUpdatedAt.Equal(after.UpdatedAt) {
		t.Fatalf("changed AP not bumped: %+v (state rev %d)", after.APs[0], after.Revision)
	}
	if after.APs[1].ConfigRevision != before.APs[1].ConfigRevision || after.APs[2].ConfigRevision != before.APs[2].ConfigRevision {
		t.Fatal("unchanged APs were bumped")
	}
	desired, ok, err := store.APConfig().Desired(apOne)
	if err != nil || !ok || desired.Revision != after.Revision || !bytes.Equal(desired.Bytes, applied.Pushes[0].Config) {
		t.Fatalf("desired not stored with the push: %+v %v %v", desired.Revision, ok, err)
	}
	if desired.Timestamp != compile.ConfigTimestamp(after.UpdatedAt) {
		t.Fatal("desired timestamp")
	}
	// The pushed bytes are exactly what the AP would be served after a
	// restart (compile of the stored state).
	input, _ := compile.InputFor(aptemplate.Default(), after, apOne)
	output, _ := compile.AP(input)
	if !bytes.Equal(output.Config, applied.Pushes[0].Config) {
		t.Fatal("pushed config differs from a fresh compile")
	}

	// A WLAN change reaches every AP of its site and no other site.
	applied = mutate(t, store, []string{"site-a"}, func(s *model.State) { s.Networks[0].PSK = "example-passphrase-02" })
	if !reflect.DeepEqual(applied.Result.Changed, []string{apOne, apTwo}) {
		t.Fatalf("WLAN change reached %v", applied.Result.Changed)
	}
	// Applying the same value again changes nothing.
	applied = mutate(t, store, []string{"site-a"}, func(s *model.State) { s.Networks[0].PSK = "example-passphrase-02" })
	if len(applied.Result.Changed) != 0 {
		t.Fatalf("repeat change bumped %v", applied.Result.Changed)
	}
	// A move compiles both sites: the moved AP changes, the others do not.
	applied = mutate(t, store, []string{"site-a", "site-b"}, func(s *model.State) { s.APs[1].SiteID = "site-b" })
	if !reflect.DeepEqual(applied.Result.Changed, []string{apTwo}) {
		t.Fatalf("move changed %v", applied.Result.Changed)
	}
}

func TestChangeDetectionBaselines(t *testing.T) {
	store := newStore(t)
	state := store.Snapshot()
	// Without a stored desired config, the previous state is the baseline.
	result, err := compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: state, Previous: &state})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 0 || result.APs[0].Baseline != compile.BaselinePrevious {
		t.Fatalf("previous baseline: %+v", result)
	}
	// With neither, every AP counts as changed (safe: it gets the config).
	result, _ = compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: state})
	if len(result.Changed) != 3 || result.APs[0].Baseline != compile.BaselineNone {
		t.Fatalf("no baseline: %+v", result.Changed)
	}
	// With ap_config, desired wins.
	result, _ = compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: state, Desired: store.APConfig()})
	if len(result.Changed) != 0 || result.APs[0].Baseline != compile.BaselineDesired {
		t.Fatalf("desired baseline: %+v", result)
	}
	// A desired config of another revision (written before something that
	// left ap_config alone bumped the AP) is not used: the previous state
	// is the baseline, or the AP counts as changed without one.
	bumped := state
	bumped.APs = append([]model.APRecord(nil), state.APs...)
	bumped.APs[0].ConfigRevision++
	result, _ = compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: bumped, Desired: store.APConfig(), Previous: &bumped})
	if one := result.APs[0]; one.Baseline != compile.BaselinePrevious || one.HasDesired || !one.StaleDesired || one.Changed {
		t.Fatalf("stale desired, previous baseline: %+v", one)
	}
	if two := result.APs[1]; two.Baseline != compile.BaselineDesired || two.StaleDesired {
		t.Fatalf("current desired of another AP: %+v", two)
	}
	result, _ = compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: bumped, Desired: store.APConfig()})
	if one := result.APs[0]; one.Baseline != compile.BaselineNone || !one.Changed {
		t.Fatalf("stale desired, no baseline: %+v", one)
	}
	// So is one of another config time at the same revision.
	moved := state
	moved.APs = append([]model.APRecord(nil), state.APs...)
	moved.APs[0].ConfigUpdatedAt = moved.APs[0].ConfigUpdatedAt.Add(time.Second)
	result, _ = compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: moved, Desired: store.APConfig()})
	if one := result.APs[0]; !one.StaleDesired || !one.Changed {
		t.Fatalf("desired of another config time: %+v", one)
	}
	// Timestamp and SiteName are ignored; anything else is not.
	a := []byte(`{"A":1,"SiteName":"x","Timestamp":1}`)
	if same, _ := compile.SameConfig(a, []byte(`{"Timestamp":2,"A":1,"SiteName":"y"}`)); !same {
		t.Fatal("Timestamp or SiteName counted")
	}
	if same, _ := compile.SameConfig(a, []byte(`{"A":1,"timestamp":1}`)); same {
		t.Fatal("a patched lower-case timestamp was ignored")
	}
}

func TestFirstSaveAfterUpgradeSeedsDesiredWithoutBumping(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A pre-M0 write: state without ap_config.
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Example"}}
		state.APs = []model.APRecord{{ID: apOne, SiteID: "site-a", Secret: secret32, ConfigRevision: 1, ConfigUpdatedAt: time.Unix(100, 0).UTC()}}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	applied := mutate(t, store, []string{"site-a"}, func(s *model.State) { s.Sites[0].Name = "Renamed" })
	if len(applied.Pushes) != 0 {
		t.Fatal("first save after upgrade bumped an unchanged AP")
	}
	desired, ok, _ := store.APConfig().Desired(apOne)
	if !ok || desired.Revision != 1 || desired.Timestamp != compile.ConfigTimestamp(time.Unix(100, 0)) {
		t.Fatalf("desired not seeded from the served config: %+v %v", desired, ok)
	}
}

func TestApplyDryRunChangesNothing(t *testing.T) {
	store := newStore(t)
	before := store.Snapshot()
	err := store.UpdateTx(func(state *model.State, tx *model.Tx) error {
		state.Networks[0].Enabled = false
		dry := *state
		dry.APs = append([]model.APRecord(nil), state.APs...)
		applied, err := compile.Apply(&dry, tx.APConfig(), compile.ApplyOptions{Template: aptemplate.Default(), Now: state.UpdatedAt, DryRun: true})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(applied.Result.Changed, []string{apOne, apTwo}) || len(applied.Pushes) != 2 {
			t.Errorf("dry run changed %v", applied.Result.Changed)
		}
		if !reflect.DeepEqual(revisions(dry), revisions(before)) {
			t.Error("dry run bumped revisions")
		}
		return errors.New("abort")
	})
	if err == nil || err.Error() != "abort" {
		t.Fatal(err)
	}
	if desired, _, _ := store.APConfig().Desired(apOne); desired.Revision != before.APs[0].ConfigRevision {
		t.Fatal("dry run wrote ap_config")
	}
}

// brokenSSHPatch makes one AP's compile fail (its patch replaces SSH with a
// string, which the site SSH keys step cannot extend) while the stored
// record is still valid.
var brokenSSHPatch = json.RawMessage(`{"SSH":"broken"}`)

func TestPerAPIsolation(t *testing.T) {
	store := newStore(t)
	// Store the broken record directly (as an older release or a patch saved
	// before site keys existed could have).
	if err := store.Update(func(state *model.State) error {
		state.Sites[0].SSHPublicKeys = []string{siteKey()}
		state.APs[1].Advanced = brokenSSHPatch
		state.APs = append(state.APs, model.APRecord{ID: "02-aa-00-00-00-04", SiteID: "site-a", SecretFile: "not-imported.bin"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	result, err := compile.Site(compile.SiteInput{Template: aptemplate.Default(), State: state, Desired: store.APConfig()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Failed, []string{apTwo}) {
		t.Fatalf("failed %v", result.Failed)
	}
	for _, id := range []string{apOne, apOther} {
		if one, _ := result.Result(id); one.Err != nil || len(one.Output.Config) == 0 {
			t.Fatalf("AP %s not compiled next to a broken AP: %v", id, one.Err)
		}
	}
	var apErr *compile.APError
	if !errors.As(result.Err(), &apErr) || apErr.APID != apTwo {
		t.Fatalf("joined error %v", result.Err())
	}

	// A mutation whose result fails for any AP is refused as a whole.
	previous := store.Snapshot()
	err = store.UpdateTx(func(state *model.State, tx *model.Tx) error {
		state.Networks[0].SSID = "Changed"
		_, err := compile.Apply(state, tx.APConfig(), compile.ApplyOptions{Template: aptemplate.Default(), Previous: &previous, Now: state.UpdatedAt})
		return err
	})
	if !errors.As(err, &apErr) || apErr.APID != apTwo {
		t.Fatalf("mutation with a failing AP: %v", err)
	}
	if store.Snapshot().Networks[0].SSID != "Example" {
		t.Fatal("refused mutation was stored")
	}

	// Serving: the broken AP gets its delivered bytes, the AP without a
	// secret is skipped, the others are compiled.
	delivered := []byte(`{"delivered":true}`)
	if err := store.APConfig().PutDelivered(apTwo, model.DeliveredConfig{Revision: 1, Bytes: delivered, At: time.Unix(5, 0)}); err != nil {
		t.Fatal(err)
	}
	served := compile.Prepare(aptemplate.Default(), state, store.APConfig())
	byID := map[string]compile.Served{}
	for _, one := range served {
		byID[one.APID] = one
	}
	if one := byID[apOne]; one.Source != compile.SourceCompiled || one.Err != nil || len(one.Config) == 0 {
		t.Fatalf("healthy AP: %+v", one)
	}
	if one := byID[apOther]; one.Source != compile.SourceCompiled {
		t.Fatalf("other site AP: %+v", one)
	}
	if one := byID[apTwo]; one.Source != compile.SourceDelivered || !bytes.Equal(one.Config, delivered) || one.Revision != 1 || one.Err == nil {
		t.Fatalf("broken AP not served its delivered bytes: %+v", one)
	}
	if one := byID["02-aa-00-00-00-04"]; one.Skip != compile.SkipNoSecret || one.Config != nil {
		t.Fatalf("AP without secret: %+v", one)
	}
	// Without a delivered config the broken AP is served nothing.
	served = compile.Prepare(aptemplate.Default(), state, nil)
	for _, one := range served {
		if one.APID == apTwo && (one.Source != compile.SourceNone || one.Config != nil || one.Err == nil) {
			t.Fatalf("broken AP without delivered config: %+v", one)
		}
	}
}

func TestPrepareSkipsUnassignedAPs(t *testing.T) {
	state := model.State{
		Sites: []model.Site{{ID: "site-a", Name: "Example"}},
		APs:   []model.APRecord{{ID: apOne, Secret: secret32}, {ID: apTwo, SiteID: "site-missing", Secret: secret32}},
	}
	served := compile.Prepare(aptemplate.Default(), state, nil)
	if served[0].Skip != compile.SkipNoSite || served[1].Err == nil || served[1].Source != compile.SourceNone {
		t.Fatalf("served %+v", served)
	}
}

func TestEffectiveViewIsKeyedAndUsesLocalFiles(t *testing.T) {
	store := newStore(t)
	state := store.Snapshot()
	view, err := compile.Effective(aptemplate.Default(), state, apOne, store.HashKey())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Hash) != len("e1:")+64 || view.Report.LocalFiles != "unknown" || len(view.Canonical) == 0 {
		t.Fatalf("view %+v", view.Report)
	}
	state.APs[0].LocalFiles = []model.LocalFile{{Name: "local_defaults.config"}}
	present, _ := compile.Effective(aptemplate.Default(), state, apOne, store.HashKey())
	if present.Report.LocalFiles != "present" || present.Hash == view.Hash {
		t.Fatalf("local files ignored: %+v", present.Report)
	}
	if _, err := compile.Effective(aptemplate.Default(), state, apOne, nil); err == nil {
		t.Fatal("unkeyed hash")
	}
}

func TestNetworksForSiteIsInBucketKeyOrder(t *testing.T) {
	ids := func(networks []model.Network) []string {
		var result []string
		for _, network := range networks {
			result = append(result, network.ID)
		}
		return result
	}
	// The order a running store holds after appends, and the order it
	// reads back after a restart, compile the same.
	appended := model.State{
		Sites: []model.Site{{ID: "a"}, {ID: "b"}},
		Networks: []model.Network{
			{ID: "zzzz", SiteID: "a"}, {ID: "m", SiteID: "b"}, {ID: "0123", SiteID: "a"}, {ID: "b", SiteID: "a"},
		},
	}
	if got, want := ids(compile.NetworksForSite(appended, "a")), []string{"0123", "b", "zzzz"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("site a networks %v, want %v", got, want)
	}
	// Legacy rule: site-less networks key before the site's own.
	legacy := model.State{Networks: []model.Network{{ID: "a", SiteID: "s"}, {ID: "z"}, {ID: "b"}}}
	if got, want := ids(compile.NetworksForSite(legacy, "s")), []string{"b", "z", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy networks %v, want %v", got, want)
	}
}
