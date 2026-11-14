// Package migrate runs schema migrations of controller.db (design §4.1,
// §4.7). It imports model and compile; model never imports compile.
//
// Before any step runs, the runner checks free space on the database's
// filesystem (the DB size plus 64 MiB) and writes a consistent copy
// controller.db.pre-v<N>-<UTC timestamp> next to the database. A step runs
// in one bbolt write transaction together with the state. A step that
// refuses, or a free-space shortfall, never stops the daemon: the runner
// records meta.migration_state {state: "legacy", …} and returns, and
// dismistd keeps running the legacy compiler.
//
// Phase 0 registers no steps; the schema-2 migration (v2.go) arrives in
// phase 2.
package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

// FreeSpaceMargin is the space a migration needs beyond the database size.
const FreeSpaceMargin = 64 << 20

// Context is what a step gets besides the transaction.
type Context struct {
	// Template is the runtime AP template (-ap-config, or the bundled one).
	Template []byte
	Now      time.Time
	// AcceptLocalFiles lists APs whose local_* files the operator accepted
	// (-migrate-accept-local-files).
	AcceptLocalFiles []string
	// Logf receives progress lines; nil discards them.
	Logf func(format string, args ...any)
}

func (c *Context) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Step migrates the database from schema From to schema To.
type Step struct {
	From, To uint64
	Name     string
	// Run changes state and other buckets inside the transaction. Return a
	// *RefusedError to fall back to legacy mode with a reason; any error
	// rolls the transaction back.
	Run func(state *model.State, tx *model.Tx, ctx *Context) error
}

// RefusedError is a step declining to migrate (verification failed, local
// files, …). The runner records it as legacy mode.
type RefusedError struct {
	Reason string
	// Paths are the JSON paths involved (never values: secrets stay out).
	Paths []string
}

func (e *RefusedError) Error() string { return e.Reason }

// Migration states (meta.migration_state.state, SystemView.migration.state).
const (
	StateNotRun    = "not_run"
	StateCommitted = "committed"
	StateLegacy    = "legacy"
)

// MigrationState is stored in meta.migration_state.
type MigrationState struct {
	State  string    `json:"state"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
	Paths  []string  `json:"paths,omitempty"`
	// Schema is meta.schema after the run.
	Schema uint64 `json:"schema"`
	// Copy is the pre-migration copy written for this run.
	Copy string `json:"copy,omitempty"`
}

// Options configures Run.
type Options struct {
	Context
	// DiskUsage reports free space (model.DiskUsage when nil).
	DiskUsage func(dir string) (model.DiskSpace, error)
	// MaxSchema is the newest schema a step may write (model.SupportedSchema
	// when 0): a binary never migrates to a schema it does not understand.
	MaxSchema uint64
}

// Result is what Run did.
type Result struct {
	State MigrationState
	// Ran lists the names of the steps that committed.
	Ran []string
}

// ErrInsufficientSpace is returned (wrapped) by CheckFreeSpace.
var ErrInsufficientSpace = errors.New("not enough free space")

// CheckFreeSpace verifies that the filesystem holding dbPath has room for a
// copy of a database of dbSize bytes plus FreeSpaceMargin.
func CheckFreeSpace(dbPath string, dbSize int64, usage func(string) (model.DiskSpace, error)) error {
	if usage == nil {
		usage = model.DiskUsage
	}
	space, err := usage(filepath.Dir(dbPath))
	if err != nil {
		return fmt.Errorf("free-space check: %w", err)
	}
	need := uint64(dbSize) + FreeSpaceMargin
	if space.Free < need {
		return fmt.Errorf("%w on %s: need %d bytes (database %d + %d), have %d", ErrInsufficientSpace, filepath.Dir(dbPath), need, dbSize, FreeSpaceMargin, space.Free)
	}
	return nil
}

// CopyName is the file name of a pre-migration copy.
func CopyName(dbPath string, toSchema uint64, at time.Time) string {
	return fmt.Sprintf("%s.pre-v%d-%s", filepath.Base(dbPath), toSchema, at.UTC().Format("20060102T150405Z"))
}

// WritePreMigrationCopy writes controller.db.pre-v<N>-<ts> next to the
// database (tx.WriteTo into a temporary file, fsync, rename).
func WritePreMigrationCopy(store *model.BoltStore, toSchema uint64, at time.Time) (string, error) {
	path := filepath.Join(filepath.Dir(store.Path()), CopyName(store.Path(), toSchema, at))
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("pre-migration copy %s already exists", path)
	}
	if _, err := store.WriteBackup(path); err != nil {
		return "", err
	}
	return path, nil
}

// CopyInfo describes a pre-migration copy on disk.
type CopyInfo struct {
	Name      string `json:"name"`
	Schema    uint64 `json:"schema"`
	SizeBytes int64  `json:"size_bytes"`
}

// ListCopies returns the pre-migration copies next to dbPath, newest first
// (for the System page). Copies are never deleted automatically while the
// migration they belong to is not finalised (design §4.1).
func ListCopies(dbPath string) ([]CopyInfo, error) {
	pattern := filepath.Join(filepath.Dir(dbPath), filepath.Base(dbPath)+".pre-v*-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var result []CopyInfo
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		var schema uint64
		rest := strings.TrimPrefix(filepath.Base(match), filepath.Base(dbPath)+".pre-v")
		if _, err := fmt.Sscanf(rest, "%d-", &schema); err != nil {
			continue
		}
		result = append(result, CopyInfo{Name: filepath.Base(match), Schema: schema, SizeBytes: info.Size()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name > result[j].Name })
	return result, nil
}

// Run applies every step whose From matches meta.schema, in order. It
// returns an error only when the database itself fails; a refusal or a
// free-space shortfall is a legacy-mode Result.
func Run(store *model.BoltStore, steps []Step, opts Options) (Result, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	info, err := store.Schema()
	if err != nil {
		return Result{}, err
	}
	result := Result{State: MigrationState{State: StateNotRun, At: opts.Now, Schema: info.Schema}}
	current := info.Schema
	for {
		step, ok := next(steps, current)
		if !ok {
			break
		}
		maxSchema := opts.MaxSchema
		if maxSchema == 0 {
			maxSchema = model.SupportedSchema
		}
		if step.To > maxSchema {
			return result, fmt.Errorf("step %s writes schema %d, above the %d this binary supports", step.Name, step.To, maxSchema)
		}
		size, err := store.Size()
		if err != nil {
			return result, err
		}
		if err := CheckFreeSpace(store.Path(), size, opts.DiskUsage); err != nil {
			return legacy(store, result, err.Error(), nil, opts.Now)
		}
		copyPath, err := WritePreMigrationCopy(store, step.To, opts.Now)
		if err != nil {
			return legacy(store, result, "pre-migration copy failed: "+err.Error(), nil, opts.Now)
		}
		result.State.Copy = filepath.Base(copyPath)
		opts.Context.logf("migrate: %s (schema %d to %d); copy %s", step.Name, step.From, step.To, copyPath)
		err = store.UpdateTx(func(state *model.State, tx *model.Tx) error {
			if err := step.Run(state, tx, &opts.Context); err != nil {
				return err
			}
			return tx.SetSchema(step.To)
		})
		var refused *RefusedError
		if errors.As(err, &refused) {
			return legacy(store, result, refused.Reason, refused.Paths, opts.Now)
		}
		if err != nil {
			return legacy(store, result, fmt.Sprintf("step %s failed: %v", step.Name, err), nil, opts.Now)
		}
		result.Ran = append(result.Ran, step.Name)
		current = step.To
		result.State = MigrationState{State: StateCommitted, At: opts.Now, Schema: current, Copy: result.State.Copy}
	}
	if len(result.Ran) > 0 {
		if err := store.PutMetaJSON(model.MetaMigrationState, result.State); err != nil {
			return result, err
		}
	}
	return result, nil
}

func next(steps []Step, from uint64) (Step, bool) {
	for _, step := range steps {
		if step.From == from && step.To > from {
			return step, true
		}
	}
	return Step{}, false
}

func legacy(store *model.BoltStore, result Result, reason string, paths []string, at time.Time) (Result, error) {
	result.State.State = StateLegacy
	result.State.At = at
	result.State.Reason = reason
	result.State.Paths = paths
	if info, err := store.Schema(); err == nil {
		result.State.Schema = info.Schema
	}
	if err := store.PutMetaJSON(model.MetaMigrationState, result.State); err != nil {
		return result, err
	}
	return result, nil
}

// ReadState reads meta.migration_state (StateNotRun when absent).
func ReadState(store *model.BoltStore) (MigrationState, error) {
	var state MigrationState
	found, err := store.MetaJSON(model.MetaMigrationState, &state)
	if err != nil {
		return state, err
	}
	if !found {
		info, err := store.Schema()
		if err != nil {
			return state, err
		}
		return MigrationState{State: StateNotRun, Schema: info.Schema}, nil
	}
	return state, nil
}

// Steps returns the registered migration steps. Phase 0 has none.
func Steps() []Step { return nil }
