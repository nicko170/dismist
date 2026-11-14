package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

var (
	secret = bytes.Repeat([]byte{0x11}, model.SecretSize)
	now    = time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
)

func newStore(t *testing.T) *model.BoltStore {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Example", Country: "AU"}}
		state.APs = []model.APRecord{
			{ID: "02-aa-00-00-00-01", Name: "one", SiteID: "site-a", Secret: secret},
			{ID: "02-aa-00-00-00-02", Name: "two", SiteID: "site-a", Secret: secret},
			{ID: "02-aa-00-00-00-03", Name: "spare", Secret: secret},
		}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func plenty(string) (model.DiskSpace, error) {
	return model.DiskSpace{Total: 1 << 40, Free: 1 << 39}, nil
}

func TestNoStepsDoesNothing(t *testing.T) {
	store := newStore(t)
	result, err := Run(store, Steps(), Options{Context: Context{Now: now}, DiskUsage: plenty})
	if err != nil || result.State.State != StateNotRun || len(result.Ran) != 0 {
		t.Fatalf("result %+v %v", result, err)
	}
	if copies, _ := ListCopies(store.Path()); len(copies) != 0 {
		t.Fatalf("copy written without a step: %v", copies)
	}
	if state, _ := ReadState(store); state.State != StateNotRun || state.Schema != 1 {
		t.Fatalf("state %+v", state)
	}
}

func renameStep(name string) Step {
	return Step{From: 1, To: 2, Name: "example", Run: func(state *model.State, tx *model.Tx, ctx *Context) error {
		state.Sites[0].Name = name
		return nil
	}}
}

func TestStepRunsAfterFreeSpaceCheckAndCopy(t *testing.T) {
	store := newStore(t)
	before := store.Snapshot()
	result, err := Run(store, []Step{renameStep("Migrated")}, Options{Context: Context{Now: now}, DiskUsage: plenty, MaxSchema: 2})
	if err != nil || result.State.State != StateCommitted || len(result.Ran) != 1 {
		t.Fatalf("result %+v %v", result, err)
	}
	if info, _ := store.Schema(); info.Schema != 2 || info.Compat != 1 {
		t.Fatalf("schema %+v", info)
	}
	if store.Snapshot().Sites[0].Name != "Migrated" {
		t.Fatal("step did not commit")
	}
	wantName := "controller.db.pre-v2-20261114T010203Z"
	if result.State.Copy != wantName {
		t.Fatalf("copy name %q", result.State.Copy)
	}
	copies, err := ListCopies(store.Path())
	if err != nil || len(copies) != 1 || copies[0].Name != wantName || copies[0].Schema != 2 || copies[0].SizeBytes == 0 {
		t.Fatalf("copies %+v %v", copies, err)
	}
	copyPath := filepath.Join(filepath.Dir(store.Path()), wantName)
	if info, _ := os.Stat(copyPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("copy mode %v", info.Mode())
	}
	// The copy is the pre-migration database.
	db, err := bolt.Open(copyPath, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var frozen legacyv0.State
	_ = db.View(func(tx *bolt.Tx) error { frozen, err = legacyv0.ReadState(tx); return err })
	if frozen.Sites[0].Name != before.Sites[0].Name || frozen.Revision != before.Revision {
		t.Fatalf("copy is not the pre-migration state: %+v", frozen.Sites)
	}
	if state, _ := ReadState(store); state.State != StateCommitted || state.Schema != 2 {
		t.Fatalf("migration state %+v", state)
	}
	// A second run finds nothing to do.
	again, err := Run(store, []Step{renameStep("Again")}, Options{Context: Context{Now: now.Add(time.Hour)}, DiskUsage: plenty, MaxSchema: 2})
	if err != nil || len(again.Ran) != 0 || store.Snapshot().Sites[0].Name != "Migrated" {
		t.Fatalf("second run %+v %v", again, err)
	}
}

func TestLowDiskSpaceFallsBackToLegacyWithoutRunning(t *testing.T) {
	store := newStore(t)
	short := func(string) (model.DiskSpace, error) { return model.DiskSpace{Total: 100 << 20, Free: 10 << 20}, nil }
	result, err := Run(store, []Step{renameStep("Migrated")}, Options{Context: Context{Now: now}, DiskUsage: short, MaxSchema: 2})
	if err != nil || result.State.State != StateLegacy || !strings.Contains(result.State.Reason, "not enough free space") {
		t.Fatalf("result %+v %v", result, err)
	}
	if store.Snapshot().Sites[0].Name != "Example" {
		t.Fatal("step ran without space")
	}
	if copies, _ := ListCopies(store.Path()); len(copies) != 0 {
		t.Fatal("copy written without space")
	}
	if info, _ := store.Schema(); info.Schema != 1 {
		t.Fatal("schema moved")
	}
	if state, _ := ReadState(store); state.State != StateLegacy || state.Reason != result.State.Reason {
		t.Fatalf("stored state %+v", state)
	}
	if err := CheckFreeSpace(store.Path(), 1, nil); err != nil {
		t.Logf("real free-space check: %v", err)
	}
	if err := CheckFreeSpace(store.Path(), 1, short); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatal(err)
	}
}

func TestRefusedOrFailedStepRollsBackToLegacy(t *testing.T) {
	for name, run := range map[string]func(*model.State, *model.Tx, *Context) error{
		"refused": func(state *model.State, tx *model.Tx, ctx *Context) error {
			state.Sites[0].Name = "half"
			return &RefusedError{Reason: "verification failed for AP 02-aa-00-00-00-01", Paths: []string{"Radios[5].Power"}}
		},
		"failed": func(state *model.State, tx *model.Tx, ctx *Context) error {
			state.Sites[0].Name = "half"
			return errors.New("boom")
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := newStore(t)
			result, err := Run(store, []Step{{From: 1, To: 2, Name: "v2", Run: run}}, Options{Context: Context{Now: now}, DiskUsage: plenty, MaxSchema: 2})
			if err != nil || result.State.State != StateLegacy || result.State.Reason == "" {
				t.Fatalf("result %+v %v", result, err)
			}
			if store.Snapshot().Sites[0].Name != "Example" {
				t.Fatal("refused step was committed")
			}
			if info, _ := store.Schema(); info.Schema != 1 {
				t.Fatal("schema moved")
			}
			if name == "refused" && (len(result.State.Paths) != 1 || result.State.Paths[0] != "Radios[5].Power") {
				t.Fatalf("paths %v", result.State.Paths)
			}
		})
	}
}

func TestStepAboveSupportedSchemaIsRefused(t *testing.T) {
	store := newStore(t)
	if _, err := Run(store, []Step{renameStep("x")}, Options{Context: Context{Now: now}, DiskUsage: plenty}); err == nil {
		t.Fatal("M0 binary migrated to schema 2")
	}
}

func TestRecordH0(t *testing.T) {
	store := newStore(t)
	recorded, err := RecordH0(store, H0Options{Now: now})
	if err != nil || len(recorded) != 2 {
		t.Fatalf("recorded %v %v", recorded, err)
	}
	all, err := store.H0()
	if err != nil {
		t.Fatal(err)
	}
	record := all["02-aa-00-00-00-01"]
	state := store.Snapshot()
	input, _ := compile.InputFor(aptemplate.Default(), state, "02-aa-00-00-00-01")
	output, _ := compile.AP(input)
	if !bytes.Equal(record.Raw, output.Config) {
		t.Fatal("H0 raw bytes differ from what the AP is served")
	}
	view, _ := compile.Effective(aptemplate.Default(), state, "02-aa-00-00-00-01", store.HashKey())
	if record.EffectiveHash != view.Hash || record.NormaliserVersion != effective.Version ||
		record.RawHMAC != effective.RawHMAC(store.HashKey(), record.Raw) || record.TemplateSHA256 != legacyv0.TemplateSHA256 ||
		record.ConfigRevision != compile.ConfigRevision(state, state.APs[0]) || !record.RecordedAt.Equal(now) {
		record.Raw = nil
		t.Fatalf("H0 record %+v", record)
	}
	if _, ok := all["02-aa-00-00-00-03"]; ok {
		t.Fatal("AP without a site recorded")
	}
	// Recorded once; Force re-records.
	if again, _ := RecordH0(store, H0Options{Now: now.Add(time.Hour)}); len(again) != 0 {
		t.Fatalf("re-recorded %v", again)
	}
	if forced, _ := RecordH0(store, H0Options{Now: now.Add(time.Hour), Force: true, APs: []string{"02-aa-00-00-00-02"}}); len(forced) != 1 {
		t.Fatalf("forced %v", forced)
	}
	// A custom -ap-config template is honoured.
	custom := []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}],"LED":{"Brightness":1}}`)
	computed, err := ComputeH0(store, custom, "02-aa-00-00-00-01", now)
	if err != nil || computed.TemplateSHA256 != legacyv0.SHA256Hex(custom) || !bytes.Contains(computed.Raw, []byte(`"Brightness":1`)) {
		t.Fatalf("custom template H0 %v", err)
	}
	raw, _ := json.Marshal(record)
	if !bytes.Contains(raw, []byte(`"raw":`)) {
		t.Fatal("record shape")
	}
}

func TestRecordH0IsolatesBrokenAPs(t *testing.T) {
	store := newStore(t)
	if err := store.Update(func(state *model.State) error {
		state.Sites[0].SSHPublicKeys = []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"}
		state.APs[1].Advanced = json.RawMessage(`{"SSH":"broken"}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recorded, err := RecordH0(store, H0Options{Now: now})
	if err == nil || len(recorded) != 1 || recorded[0] != "02-aa-00-00-00-01" {
		t.Fatalf("recorded %v err %v", recorded, err)
	}
}
