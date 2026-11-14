package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
)

// A dry run migrates a private copy: the real database keeps its schema
// and migration state, whatever the step does.
func TestMigrationRunnerDryRunWritesNothing(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "home", Name: "Home"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	commit := migrate.Step{From: 1, To: 2, Name: "test-v2", Run: func(state *model.State, _ *model.Tx, _ *migrate.Context) error {
		state.Sites[0].Name = "Migrated"
		return nil
	}}
	refuse := migrate.Step{From: 1, To: 2, Name: "test-v2", Run: func(*model.State, *model.Tx, *migrate.Context) error {
		return &migrate.RefusedError{Reason: "BSSID indexes differ", Paths: []string{"WLANs[0]"}}
	}}
	runner := &migrationRunner{store: store, tempDir: t.TempDir(), maxSchema: 2}
	for _, step := range []migrate.Step{commit, refuse} {
		runner.steps = func() []migrate.Step { return []migrate.Step{step} }
		status, err := runner.Retry(true)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == api.MigrationStateNotRun {
			t.Fatalf("dry run did not run the step: %+v", status)
		}
		real := runner.Status()
		if real.State != api.MigrationStateNotRun || real.Schema != 1 || store.Snapshot().Sites[0].Name != "Home" {
			t.Fatalf("dry run changed the database: %+v %+v", real, store.Snapshot().Sites)
		}
	}
	runner.steps = func() []migrate.Step { return []migrate.Step{refuse} }
	status, err := runner.Retry(false)
	if err != nil || status.State != api.MigrationStateLegacy || status.Reason != "BSSID indexes differ" || len(status.Paths) != 1 {
		t.Fatalf("refused retry: %v %+v", err, status)
	}
	if _, err := runner.Finalise(true); err != errFinaliseUnavailable {
		t.Fatalf("finalise without a step: %v", err)
	}
}

// The daemon's runner finalises (design §4.1 rule 5): a dry run on a copy
// reports compat 2 and leaves the database alone; the real run clears the
// legacy fields and raises compat.
func TestMigrationRunnerFinalises(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "8e31882f-0000-4000-8000-0000000000aa", Name: "Home", Country: "AU", Advanced: []byte(`{"LED":{"Brightness":10}}`)}}
		state.Networks = []model.Network{{ID: "6f2000aa-0000-4000-8000-000000000001", SiteID: "8e31882f-0000-4000-8000-0000000000aa", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	committedAt := time.Now().Add(-40 * 24 * time.Hour)
	if result, err := migrate.Run(store, migrate.Steps(), migrate.Options{Context: migrate.Context{Now: committedAt}}); err != nil || result.State.State != migrate.StateCommitted {
		t.Fatalf("migrate: %+v %v", result.State, err)
	}
	runner := newMigrationRunner(store, nil, t.TempDir(), nil)
	if status := runner.Status(); !status.CanFinalise {
		t.Fatalf("status %+v", status)
	}
	dry, err := runner.Finalise(true)
	if err != nil || !dry.Finalised {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if real := runner.Status(); real.Finalised || len(store.Snapshot().Sites[0].Advanced) == 0 {
		t.Fatalf("the dry run finalised the database: %+v", real)
	}
	done, err := runner.Finalise(false)
	if err != nil || !done.Finalised || done.Compat != 2 || len(store.Snapshot().Sites[0].Advanced) != 0 {
		t.Fatalf("finalise: %+v %v", done, err)
	}
}
