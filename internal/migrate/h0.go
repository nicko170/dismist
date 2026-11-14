package migrate

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

// H0Options configures RecordH0.
type H0Options struct {
	// Template is the runtime template the daemon runs with (nil = the
	// frozen bundled copy).
	Template []byte
	Now      time.Time
	// APs limits recording to these AP IDs; empty means every AP with a
	// site.
	APs []string
	// Force re-records APs that already have a baseline.
	Force bool
}

// ComputeH0 computes the phase-0 baseline of one AP (design §4.5, §8.3):
// the raw legacyv0 bytes compiled from the raw bucket JSON, their keyed
// HMAC, the normaliser version and the effective hash. The local-file state
// comes from the AP's stored local_files.
func ComputeH0(store *model.BoltStore, template []byte, apID string, now time.Time) (model.H0Record, error) {
	var state legacyv0.State
	if err := store.View(func(tx *bolt.Tx) error {
		var err error
		state, err = legacyv0.ReadState(tx)
		return err
	}); err != nil {
		return model.H0Record{}, err
	}
	return computeH0(store, state, store.Snapshot(), template, apID, now)
}

func computeH0(store *model.BoltStore, frozen legacyv0.State, current model.State, template []byte, apID string, now time.Time) (model.H0Record, error) {
	runtime := legacyv0.Template(template)
	raw, err := legacyv0.CompileAP(runtime, frozen, apID)
	if err != nil {
		return model.H0Record{}, err
	}
	ctx := effective.Context{LocalFiles: effective.LocalFilesUnknown}
	for _, ap := range current.APs {
		if ap.ID == apID {
			ctx = compile.EffectiveContext(ap)
		}
	}
	key := store.HashKey()
	hash, _, _, err := effective.HashConfig(key, raw, ctx)
	if err != nil {
		return model.H0Record{}, err
	}
	ap, _ := frozen.AP(apID)
	return model.H0Record{
		APID: apID, RecordedAt: now.UTC(), ConfigRevision: frozen.ConfigRevision(ap),
		Raw: raw, RawHMAC: effective.RawHMAC(key, raw), NormaliserVersion: effective.Version,
		EffectiveHash: hash, TemplateSHA256: legacyv0.SHA256Hex(runtime),
	}, nil
}

// RecordH0 records meta.h0 for every selected AP that has a site and no
// baseline yet (or every selected AP with Force). It only writes meta; no
// AP config changes. It returns the recorded AP IDs and the per-AP errors
// of APs it could not compile (other APs are still recorded).
func RecordH0(store *model.BoltStore, opts H0Options) ([]string, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	existing, err := store.H0()
	if err != nil {
		return nil, err
	}
	var frozen legacyv0.State
	if err := store.View(func(tx *bolt.Tx) error {
		var err error
		frozen, err = legacyv0.ReadState(tx)
		return err
	}); err != nil {
		return nil, err
	}
	current := store.Snapshot()
	wanted := map[string]bool{}
	for _, id := range opts.APs {
		wanted[id] = true
	}
	var recorded []string
	var failures []error
	for _, ap := range frozen.APs {
		if ap.SiteID == "" || (len(wanted) > 0 && !wanted[ap.ID]) {
			continue
		}
		if _, done := existing[ap.ID]; done && !opts.Force {
			continue
		}
		record, err := computeH0(store, frozen, current, opts.Template, ap.ID, opts.Now)
		if err != nil {
			failures = append(failures, fmt.Errorf("AP %s: %w", ap.ID, err))
			continue
		}
		if err := store.PutH0(record); err != nil {
			return recorded, err
		}
		recorded = append(recorded, ap.ID)
	}
	sort.Strings(recorded)
	return recorded, errors.Join(failures...)
}
