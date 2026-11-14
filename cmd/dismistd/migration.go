package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
)

// migrationRunner runs the registered schema migrations for the API's
// /api/system/migration endpoints (design §4.7 step 7 Retry; §4.1 rule 5
// Finalise). A dry run migrates a private copy of the database in the
// backup staging directory and reports what the real run would do; nothing
// is written to controller.db.
type migrationRunner struct {
	store    *model.BoltStore
	template []byte
	tempDir  string
	accept   []string
	steps    func() []migrate.Step
	finalise func(store *model.BoltStore, dryRun bool) error
	// maxSchema overrides model.SupportedSchema (tests).
	maxSchema uint64
	mu        sync.Mutex
	logf      func(string, ...any)
}

// errFinaliseUnavailable is returned until the schema-2 finalise step
// exists.
var errFinaliseUnavailable = errors.New("this build has no finalise step")

func (r *migrationRunner) Status() api.MigrationStatus {
	return migrationStatusOf(r.store)
}

// migrationStatusOf reads meta.migration_state, the report and the schema.
func migrationStatusOf(store *model.BoltStore) api.MigrationStatus {
	view := migrationView(store)
	status := api.MigrationStatus{MigrationView: view, Paths: []string{}, Schema: 1, Compat: 1}
	if schema, err := store.Schema(); err == nil {
		status.Schema, status.Compat = schema.Schema, schema.Compat
	}
	state, err := migrate.ReadState(store)
	if err == nil {
		if !state.At.IsZero() {
			at := state.At.UTC()
			status.At = &at
		}
		if state.Paths != nil {
			status.Paths = state.Paths
		}
		status.Copy = state.Copy
	}
	var report map[string]any
	if found, err := store.MetaJSON(model.MetaMigrationReport, &report); err == nil && found {
		status.Report = report
	}
	if status.State == api.MigrationStateCommitted && status.At != nil {
		after := status.At.Add(30 * 24 * time.Hour)
		status.FinaliseAfter = &after
		status.CanFinalise = time.Now().After(after) && status.Compat < 2
	}
	status.Finalised = status.State == api.MigrationStateCommitted && status.Compat >= 2
	return status
}

func (r *migrationRunner) options() migrate.Options {
	return migrate.Options{Context: migrate.Context{Template: r.template, AcceptLocalFiles: r.accept, Logf: r.logf}, MaxSchema: r.maxSchema}
}

// Retry runs the migration (the caller holds the API mutation lock).
func (r *migrationRunner) Retry(dryRun bool) (api.MigrationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !dryRun {
		if _, err := migrate.Run(r.store, r.steps(), r.options()); err != nil {
			return api.MigrationStatus{}, err
		}
		return r.Status(), nil
	}
	return r.onCopy(func(copy *model.BoltStore) error {
		_, err := migrate.Run(copy, r.steps(), r.options())
		return err
	})
}

// Finalise clears the legacy fields and raises compat (phase 4).
func (r *migrationRunner) Finalise(dryRun bool) (api.MigrationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalise == nil {
		return api.MigrationStatus{}, errFinaliseUnavailable
	}
	if !dryRun {
		if err := r.finalise(r.store, false); err != nil {
			return api.MigrationStatus{}, err
		}
		return r.Status(), nil
	}
	return r.onCopy(func(copy *model.BoltStore) error { return r.finalise(copy, true) })
}

// onCopy runs fn on a private copy of the database and returns the copy's
// migration status.
func (r *migrationRunner) onCopy(fn func(*model.BoltStore) error) (api.MigrationStatus, error) {
	dir, err := os.MkdirTemp(r.tempDir, "dismist-migrate-")
	if err != nil {
		return api.MigrationStatus{}, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "controller.db")
	if _, err := r.store.WriteBackup(path); err != nil {
		return api.MigrationStatus{}, fmt.Errorf("copy database: %w", err)
	}
	copy, err := model.OpenBolt(path, "")
	if err != nil {
		return api.MigrationStatus{}, fmt.Errorf("open copy: %w", err)
	}
	defer copy.Close()
	if err := fn(copy); err != nil {
		return api.MigrationStatus{}, err
	}
	return migrationStatusOf(copy), nil
}
