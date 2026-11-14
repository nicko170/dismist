package migrate

import (
	"errors"
	"fmt"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

// FinaliseMinAge is how long schema 2 must have run before the legacy
// keys may be cleared (design §4.1 rule 5).
const FinaliseMinAge = 30 * 24 * time.Hour

// ErrFinaliseTooEarly and friends explain a refused finalise.
var (
	ErrNotCommitted     = errors.New("the schema-2 migration has not committed")
	ErrFinaliseTooEarly = errors.New("schema 2 must run for 30 days before the migration is finalised")
)

// Finalise is the explicit operator action that ends rollback without a
// restore (phase 4): it clears the legacy keys (advanced patches and
// ssh_public_keys) and sets compat = 2 in one transaction, and records a
// history entry. Older binaries then refuse the database (exit 78).
func Finalise(store *model.BoltStore, now time.Time, actor model.Actor) error {
	state, err := ReadState(store)
	if err != nil {
		return err
	}
	if !store.Snapshot().Typed() {
		return ErrNotCommitted
	}
	if state.State == StateCommitted && now.Sub(state.At) < FinaliseMinAge {
		return fmt.Errorf("%w (committed %s)", ErrFinaliseTooEarly, state.At.UTC().Format(time.RFC3339))
	}
	return store.UpdateTx(func(next *model.State, tx *model.Tx) error {
		for index := range next.Sites {
			next.Sites[index].Advanced = nil
			next.Sites[index].SSHPublicKeys = nil
		}
		for index := range next.APs {
			next.APs[index].Advanced = nil
		}
		for index := range next.Networks {
			next.Networks[index].Advanced = nil
		}
		if err := tx.RaiseCompat(next.Revision + 1); err != nil {
			return err
		}
		_, err := tx.ConfigHistory().Append(model.ChangeRecord{
			Revision: next.Revision + 1, At: now.UTC(), Kind: model.ChangeAction, Actor: actor,
			Summary: "Finalised the schema-2 migration: legacy keys cleared, compat 2 (rolling back now needs a database restore)",
		})
		return err
	})
}
