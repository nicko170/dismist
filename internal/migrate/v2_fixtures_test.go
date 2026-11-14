package migrate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

// expectCommitted runs the migration and checks that every compiled AP's
// effective config is unchanged.
func expectCommitted(t *testing.T, store *model.BoltStore, sc scenario) model.State {
	t.Helper()
	before := oldEffective(t, store)
	result := runMigration(t, store, sc)
	if result.State.State != StateCommitted {
		t.Fatalf("refused: %s %v\n%s", result.State.Reason, result.State.Paths, result.State.Report)
	}
	template := legacyv0.BundledTemplate()
	if sc.opts != nil {
		var opts Options
		sc.opts(&opts)
		template = legacyv0.Template(opts.Template)
	}
	for apID, old := range before {
		view, err := compile.Effective(template, store.Snapshot(), apID, store.HashKey())
		if err != nil {
			t.Fatalf("AP %s: %v", apID, err)
		}
		if !bytes.Equal(view.Canonical, old) {
			t.Fatalf("AP %s effective changed: %v", apID, compile.DiffPaths(old, view.Canonical))
		}
	}
	return store.Snapshot()
}

func expectRefused(t *testing.T, store *model.BoltStore, sc scenario, want string) MigrationState {
	t.Helper()
	legacy := legacyBytes(t, store)
	result := runMigration(t, store, sc)
	if result.State.State != StateLegacy || !strings.Contains(result.State.Reason, want) {
		t.Fatalf("want legacy mode with %q, got %+v", want, result.State)
	}
	if store.Snapshot().Typed() {
		t.Fatal("refused migration left a typed state")
	}
	if info, _ := store.Schema(); info.Schema != 1 {
		t.Fatalf("schema moved: %+v", info)
	}
	if after := legacyBytes(t, store); len(after) != len(legacy) {
		t.Fatal("refusal changed records")
	}
	state, err := ReadState(store)
	if err != nil || state.State != StateLegacy {
		t.Fatalf("migration_state %+v %v", state, err)
	}
	return result.State
}

// The Lab site's patch: channel 11 at 20 MHz and 6 dBm, 5 GHz 36/80 at 10
// dBm, AU/890 on both access radios and the scan radio.
func TestLabSiteMigration(t *testing.T) {
	sc := scenario{extra: func(state *model.State) {
		state.Sites[0].Advanced = json.RawMessage(`{"Radios":[` +
			`{"Band":"2.4","CCRev":"AU/890","CCRevSP":"AU/890","Channel":11,"Width":20,"Power":6,"RRM":{"Channel":11,"Width":20,"Power":6}},` +
			`{"Band":"5","CCRev":"AU/890","CCRevSP":"AU/890","Channel":36,"Width":80,"Power":10,"RRM":{"Channel":36,"Width":80,"Power":10}}],` +
			`"Monitor":{"CCRev":"AU/890"}}`)
		state.Networks[0].Advanced = nil
	}}
	store := liveStore(t, sc)
	state := expectCommitted(t, store, sc)
	ap := state.APs[0]
	if !state.Sites[0].RF.Managed || ap.RF["2.4"].Channel != 11 || ap.RF["5"].Channel != 36 ||
		state.Sites[0].RF.Band24.Power.DBm != 6 || state.Sites[0].RF.Band5.Power.DBm != 10 {
		t.Fatalf("lab lift %+v %+v %+v", state.Sites[0].RF, ap.RF["2.4"], ap.RF["5"])
	}
	if state.Networks[0].Security != model.SecurityWPA2Personal || state.Networks[0].PMF != "optional" {
		t.Fatalf("plain network lift %+v", state.Networks[0])
	}
}

// Without an RF patch the template runs (US/0, 0 dBm): the site lifts as
// unmanaged and nothing is forced into typed RF values.
func TestUnmanagedSiteMigration(t *testing.T) {
	sc := scenario{extra: func(state *model.State) {
		state.Sites[0].Advanced = nil
		state.Networks[0].Advanced = nil
	}}
	store := liveStore(t, sc)
	state := expectCommitted(t, store, sc)
	if state.Sites[0].RF.Managed || state.APs[0].RF != nil {
		t.Fatalf("unmanaged lift %+v %+v", state.Sites[0].RF, state.APs[0].RF)
	}
	if len(state.Sites[0].Expert) != 0 {
		t.Fatalf("residual %s", state.Sites[0].Expert)
	}
}

func TestSecondAPSiteNoAPsSeveralNetworksAndNoSite(t *testing.T) {
	disabledLast := scenario{extra: func(state *model.State) {
		state.APs = append(state.APs,
			model.APRecord{ID: secondAP, Name: "Study", SiteID: homeSite, Secret: secret, ConfigRevision: 12, ConfigUpdatedAt: liveTime},
			model.APRecord{ID: "02-aa-00-00-00-99", Name: "Spare", Secret: secret})
		state.Sites = append(state.Sites, model.Site{ID: "8e31882f-0000-4000-8000-0000000000bb", Name: "Empty", Country: "AU"})
		state.Networks = append(state.Networks,
			model.Network{ID: guestNet, SiteID: homeSite, SSID: "Example Guest", PSK: "example-passphrase-02", VLANID: 30, Enabled: false},
			model.Network{ID: "6f2000aa-0000-4000-8000-000000000003", SiteID: "8e31882f-0000-4000-8000-0000000000bb", SSID: "Example", PSK: "example-passphrase-03", VLANID: 1, Enabled: true})
	}, vaps: map[string][]model.VAPRecord{
		liveAP:   {{Dev: "wl0.1", WLANID: homeNetwork}, {Dev: "wl1.1", WLANID: homeNetwork, RadioIndex: 1}},
		secondAP: {{Dev: "wl0.1", WLANID: homeNetwork}, {Dev: "wl1.1", WLANID: homeNetwork, RadioIndex: 1}},
	}}
	store := liveStore(t, disabledLast)
	state := expectCommitted(t, store, disabledLast)
	var guest, empty model.Network
	for _, network := range state.Networks {
		switch network.ID {
		case guestNet:
			guest = network
		case "6f2000aa-0000-4000-8000-000000000003":
			empty = network
		}
	}
	if guest.BSSIDIndex != 2 || guest.Enabled || guest.Security != model.SecurityWPA2Personal {
		t.Fatalf("disabled network %+v", guest)
	}
	if empty.BSSIDIndex != 1 || empty.Security == "" {
		t.Fatalf("network of a site with no APs %+v", empty)
	}
	for _, ap := range state.APs {
		if ap.ID == secondAP && (ap.RF["5"] == nil || ap.RF["5"].Channel != 149) {
			t.Fatalf("second AP inherits the site patch's channel as a static override: %+v", ap.RF)
		}
		if ap.ID == "02-aa-00-00-00-99" && ap.RF != nil {
			t.Fatalf("AP without a site got typed RF %+v", ap.RF)
		}
	}
	report, _, _ := ReadReport(store)
	for _, network := range report.Networks {
		if network.ID == guestNet && network.BSSIDSource != "predicted" {
			t.Fatalf("guest index source %+v", network)
		}
	}
}

// A disabled WLAN ahead of an enabled one: the firmware's index for it is
// unverified (§9 Q8), so the migration refuses instead of guessing.
func TestDisabledNetworkAheadRefuses(t *testing.T) {
	sc := scenario{extra: func(state *model.State) {
		state.Networks = append(state.Networks, model.Network{ID: "00000000-0000-4000-8000-000000000000", SiteID: homeSite,
			SSID: "Example Off", PSK: "example-passphrase-04", VLANID: 1, Enabled: false})
	}}
	store := liveStore(t, sc)
	expectRefused(t, store, sc, "disabled WLAN ahead")
}

// An AP patch over a site value stays in that AP's residual; the site
// value comes from the probe (the AP without its patch).
func TestAPPatchOverSiteValueStaysInAPResidual(t *testing.T) {
	sc := scenario{full: true, monitor: true, extra: func(state *model.State) {
		state.APs[0].Advanced = json.RawMessage(`{"LED":{"Brightness":40},"PoePwrConstrained":{"EnableRadios":true}}`)
	}}
	store := liveStore(t, sc)
	state := expectCommitted(t, store, sc)
	ap := state.APs[0]
	if state.Sites[0].APDefaults.LEDBrightness != 255 || ap.LEDBrightness == nil || *ap.LEDBrightness != 40 {
		t.Fatalf("LED lift site %d ap %v", state.Sites[0].APDefaults.LEDBrightness, ap.LEDBrightness)
	}
	if got := string(ap.Expert); got != `{"PoePwrConstrained":{"EnableRadios":true}}` {
		t.Fatalf("AP residual %s", got)
	}
	// The AP residual shadows the site's PoE option across scopes.
	input, _ := compile.InputFor(legacyv0.BundledTemplate(), state, liveAP)
	output, err := compile.AP(input)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, shadow := range output.Shadowed {
		if shadow.Option == "site.ap_defaults.poe_constrained" && shadow.By == "ap" && !shadow.Partly {
			found = true
		}
	}
	if !found {
		t.Fatalf("cross-scope shadow missing: %+v", output.Shadowed)
	}
}

// Stored v0 patches with keys that are forbidden today, and case variants.
func TestForbiddenKeysAndCaseVariants(t *testing.T) {
	t.Run("no-op forbidden keys", func(t *testing.T) {
		// WLANs (upper case) in a site patch has no effect in v0 (the
		// WLAN list is written after the patches); Timestamp is
		// overwritten. Neither reaches the residual.
		sc := scenario{full: true, monitor: true, extra: func(state *model.State) {
			var patch map[string]any
			_ = json.Unmarshal(state.Sites[0].Advanced, &patch)
			patch["WLANs"] = []any{map[string]any{"SSID": "ignored"}}
			patch["Timestamp"] = 1
			state.Sites[0].Advanced, _ = json.Marshal(patch)
			state.Networks[0].Advanced = json.RawMessage(`{"roamMode":"none","Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"dot11w":{"enabled":true,"required":false}}`)
		}}
		store := liveStore(t, sc)
		state := expectCommitted(t, store, sc)
		if len(state.Sites[0].Expert) != 0 || len(state.Networks[0].Expert) != 0 {
			t.Fatalf("residuals %s %s", state.Sites[0].Expert, state.Networks[0].Expert)
		}
	})
	t.Run("case variant that changed v0", func(t *testing.T) {
		// A lower-case key in a network patch that really changed what
		// the AP ran (hidden SSID) lifts into the typed field.
		sc := scenario{full: true, monitor: true, extra: func(state *model.State) {
			state.Networks[0].Advanced = json.RawMessage(`{"hidden":true,"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}`)
		}}
		store := liveStore(t, sc)
		state := expectCommitted(t, store, sc)
		if !state.Networks[0].Hidden || len(state.Networks[0].Expert) != 0 {
			t.Fatalf("hidden lift %+v %s", state.Networks[0].Hidden, state.Networks[0].Expert)
		}
	})
	t.Run("lower-case radios band in site patch", func(t *testing.T) {
		sc := scenario{extra: func(state *model.State) {
			state.Sites[0].Advanced = json.RawMessage(`{"radios":[{"band":"2.4","ccrev":"AU/890","ccrevsp":"AU/890","channel":6,"width":20,"power":9},` +
				`{"band":"5","ccrev":"AU/890","ccrevsp":"AU/890","channel":149,"width":80,"power":14}],"monitor":{"ccrev":"AU/890"}}`)
		}}
		store := liveStore(t, sc)
		state := expectCommitted(t, store, sc)
		if !state.Sites[0].RF.Managed || state.APs[0].RF["2.4"].Channel != 6 || state.Sites[0].RF.Band5.Power.DBm != 14 {
			t.Fatalf("lift %+v %+v", state.Sites[0].RF, state.APs[0].RF["2.4"])
		}
	})
	t.Run("lower-case wlans really replaced the WLAN list", func(t *testing.T) {
		// "wlans" sorts after "WLANs", so v0's AP took the patch's list:
		// no typed form can reproduce it, so the migration refuses.
		sc := scenario{full: true, monitor: true, extra: func(state *model.State) {
			var patch map[string]any
			_ = json.Unmarshal(state.Sites[0].Advanced, &patch)
			patch["wlans"] = []any{}
			state.Sites[0].Advanced, _ = json.Marshal(patch)
		}}
		store := liveStore(t, sc)
		expectRefused(t, store, sc, "")
	})
}

func TestCustomTemplateMigration(t *testing.T) {
	custom, err := os.ReadFile(filepath.Join("..", "compile", "testdata", "templates", "custom-ap33.json"))
	if err != nil {
		t.Fatal(err)
	}
	sc := scenario{full: true, monitor: true, opts: func(opts *Options) { opts.Template = custom }}
	store := liveStore(t, sc)
	before := map[string][]byte{}
	var frozen legacyv0.State
	_ = store.View(func(tx *bolt.Tx) error { frozen, err = legacyv0.ReadState(tx); return err })
	raw, err := legacyv0.CompileAP(custom, frozen, liveAP)
	if err != nil {
		t.Fatal(err)
	}
	before[liveAP] = raw
	result := runMigration(t, store, sc)
	if result.State.State != StateCommitted {
		t.Fatalf("refused: %s\n%s", result.State.Reason, result.State.Report)
	}
	view, err := compile.Effective(custom, store.Snapshot(), liveAP, store.HashKey())
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := effective.Normalise(raw, compile.EffectiveContext(store.Snapshot().APs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(view.Canonical, old) {
		t.Fatalf("custom template: %v", compile.DiffPaths(old, view.Canonical))
	}
}

// Networks created in this process sit at the end of the in-memory slice
// but are read back in key order: the migration reads the stored order.
func TestStoredNetworkOrder(t *testing.T) {
	sc := scenario{extra: func(state *model.State) {
		state.Networks = append([]model.Network{{ID: "ffffffff-0000-4000-8000-000000000009", SiteID: homeSite, SSID: "Example Z",
			PSK: "example-passphrase-05", VLANID: 40, Enabled: true}}, state.Networks...)
	}, vaps: map[string][]model.VAPRecord{liveAP: {
		{Dev: "wl0.1", WLANID: homeNetwork}, {Dev: "wl1.1", WLANID: homeNetwork, RadioIndex: 1},
		{Dev: "wl0.2", WLANID: "ffffffff-0000-4000-8000-000000000009"}, {Dev: "wl1.2", WLANID: "ffffffff-0000-4000-8000-000000000009", RadioIndex: 1},
	}}}
	store := liveStore(t, sc)
	state := expectCommitted(t, store, sc)
	for _, network := range state.Networks {
		want := map[string]int{homeNetwork: 1, "ffffffff-0000-4000-8000-000000000009": 2}[network.ID]
		if network.BSSIDIndex != want {
			t.Fatalf("network %s index %d, want %d", network.ID, network.BSSIDIndex, want)
		}
	}
}

func TestBSSIDMeasuredDisagreementRefuses(t *testing.T) {
	sc := scenario{vaps: map[string][]model.VAPRecord{liveAP: {{Dev: "wl0.1", WLANID: homeNetwork}, {Dev: "wl1.2", WLANID: homeNetwork, RadioIndex: 1}}}}
	store := liveStore(t, sc)
	expectRefused(t, store, sc, "BSSID indexes differ")
}

func TestLocalFilesRefuseUnlessAccepted(t *testing.T) {
	withFiles := func(state *model.State) {
		state.APs[0].LocalFiles = []model.LocalFile{{Name: "local_overrides.config", SHA256: strings.Repeat("0", 64), SeenAt: now}}
	}
	sc := scenario{full: true, monitor: true, extra: withFiles}
	store := liveStore(t, sc)
	expectRefused(t, store, sc, "local config files")
	// Accepted by the operator: the normaliser runs without rule 2a and
	// the migration decides on its own.
	accepted := scenario{full: true, monitor: true, extra: withFiles, opts: func(opts *Options) { opts.AcceptLocalFiles = []string{liveAP} }}
	store = liveStore(t, accepted)
	result := runMigration(t, store, accepted)
	if result.State.State == StateLegacy && strings.Contains(result.State.Reason, "local config files") {
		t.Fatalf("accepted AP still refused: %+v", result.State)
	}
}

func TestInsufficientSpaceWritesNothing(t *testing.T) {
	sc := scenario{full: true, monitor: true, opts: func(opts *Options) {
		opts.DiskUsage = func(string) (model.DiskSpace, error) { return model.DiskSpace{Total: 1 << 30, Free: 1 << 20}, nil }
	}}
	store := liveStore(t, sc)
	state := expectRefused(t, store, sc, "not enough free space")
	if state.Copy != "" {
		t.Fatal("copy written without space")
	}
	if copies, _ := ListCopies(store.Path()); len(copies) != 0 {
		t.Fatalf("copies %v", copies)
	}
}

// writeAsOlderBinary rewrites the state buckets the way a schema-1 binary
// does (its own structs, dropping every typed key) and bumps the revision.
func writeAsOlderBinary(t *testing.T, path string, rename string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(func(tx *bolt.Tx) error {
		frozen, err := legacyv0.ReadState(tx)
		if err != nil {
			return err
		}
		for _, ap := range frozen.APs {
			if rename != "" {
				ap.Name = rename
			}
			raw, _ := json.Marshal(ap)
			if err := tx.Bucket([]byte(model.BucketAPs)).Put([]byte(ap.ID), raw); err != nil {
				return err
			}
		}
		for _, site := range frozen.Sites {
			raw, _ := json.Marshal(site)
			if err := tx.Bucket([]byte(model.BucketSites)).Put([]byte(site.ID), raw); err != nil {
				return err
			}
		}
		for _, network := range frozen.Networks {
			raw, _ := json.Marshal(network)
			if err := tx.Bucket([]byte(model.BucketNetworks)).Put([]byte(network.SiteID+"\x00"+network.ID), raw); err != nil {
				return err
			}
		}
		meta := tx.Bucket([]byte(model.BucketMeta))
		revision := binary.BigEndian.Uint64(meta.Get([]byte(model.MetaRevision)))
		var next [8]byte
		binary.BigEndian.PutUint64(next[:], revision+1)
		return meta.Put([]byte(model.MetaRevision), next[:])
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRemigrationAfterOlderWriter(t *testing.T) {
	sc := scenario{full: false, monitor: true}
	store := liveStore(t, sc)
	path := store.Path()
	before := oldEffective(t, store)
	if result := runMigration(t, store, sc); result.State.State != StateCommitted {
		t.Fatalf("first migration %+v", result.State)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// The previous release starts, renames the AP and writes.
	writeAsOlderBinary(t, path, "Hallway Renamed")
	reopened, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	status := reopened.SchemaStatus()
	if !status.OlderWriter || reopened.Snapshot().Typed() {
		t.Fatalf("older writer not detected: %+v", status)
	}
	// The legacy compiler runs until the re-migration commits.
	if view, err := compile.Effective(legacyv0.BundledTemplate(), reopened.Snapshot(), liveAP, reopened.HashKey()); err != nil {
		t.Fatal(err)
	} else if before[liveAP] == nil || bytes.Equal(view.Canonical, nil) {
		t.Fatal("no legacy compile")
	}
	result, err := Run(reopened, Steps(), Options{Context: Context{Now: now.Add(1)}, DiskUsage: plenty})
	if err != nil || result.State.State != StateCommitted {
		t.Fatalf("re-migration %+v %v", result.State, err)
	}
	state := reopened.Snapshot()
	if !state.Typed() || state.APs[0].Name != "Hallway Renamed" || state.APs[0].RF["5"].Channel != 149 {
		t.Fatalf("re-migrated state %+v", state.APs[0])
	}
	records, _, err := reopened.ConfigHistory().List(model.HistoryQuery{Kinds: []string{model.ChangeMigration}})
	if err != nil || len(records) != 2 || !strings.Contains(records[0].Summary, "Re-migrated after running an older release") {
		t.Fatalf("history %+v %v", records, err)
	}
}

// compat rises to 2 the first time a typed state stores something a
// schema-1 binary would compile wrongly (design §4.1 rule 4).
func TestCompatRaisedForSchema2OnlyValues(t *testing.T) {
	sc := scenario{full: true, monitor: true}
	store := liveStore(t, sc)
	runMigration(t, store, sc)
	if info, _ := store.Schema(); info.Compat != 1 {
		t.Fatalf("compat %d after migration", info.Compat)
	}
	// A rename keeps compat 1.
	if err := store.Update(func(state *model.State) error { state.APs[0].Name = "Hall"; return nil }); err != nil {
		t.Fatal(err)
	}
	if info, _ := store.Schema(); info.Compat != 1 {
		t.Fatalf("compat %d after a rename", info.Compat)
	}
	// WPA3-only would be downgraded by v0: compat 2.
	if err := store.Update(func(state *model.State) error {
		state.Networks[0].Security = model.SecurityWPA3Personal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if info, _ := store.Schema(); info.Compat != 2 {
		t.Fatalf("compat %d after WPA3-only", info.Compat)
	}
	if required, reasons := model.RequiredCompat(store.Snapshot()); required != 2 || len(reasons) != 1 {
		t.Fatalf("required %d %v", required, reasons)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	sc := scenario{full: false, monitor: true}
	store := liveStore(t, sc)
	before, _ := os.ReadFile(store.Path())
	report, err := DryRun(store, Context{Now: now})
	if err != nil || !report.Verified || len(report.Sites) != 1 || len(report.Sites[0].ResidualPaths) == 0 {
		t.Fatalf("dry run %+v %v", report, err)
	}
	after, _ := os.ReadFile(store.Path())
	if !bytes.Equal(before, after) {
		t.Fatal("dry run modified the database")
	}
	if store.Snapshot().Typed() {
		t.Fatal("dry run committed")
	}
}
