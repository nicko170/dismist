package main

import (
	"path/filepath"
	"testing"

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
