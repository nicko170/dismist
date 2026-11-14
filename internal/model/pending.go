package model

import (
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// PendingDismissal is one dismissed pending AP (design §4.1, phase 1). The
// pending-AP list itself stays in memory (§5.4).
type PendingDismissal struct {
	Until time.Time `json:"until"`
	At    time.Time `json:"at"`
	Actor string    `json:"actor,omitempty"`
}

// PendingDismissalStore is the pending_dismissed bucket.
type PendingDismissalStore struct{ db *bolt.DB }

// PendingDismissed returns the dismissed pending-AP store.
func (s *BoltStore) PendingDismissed() *PendingDismissalStore {
	return &PendingDismissalStore{db: s.db}
}

func pendingBucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	bucket := tx.Bucket([]byte(BucketPendingDismissed))
	if bucket == nil {
		return nil, errors.New("pending_dismissed bucket missing")
	}
	return bucket, nil
}

// Dismiss hides a pending AP until the given time.
func (p *PendingDismissalStore) Dismiss(apID string, dismissal PendingDismissal) error {
	if apID == "" {
		return errors.New("AP ID required")
	}
	return p.db.Update(func(tx *bolt.Tx) error {
		bucket, err := pendingBucket(tx)
		if err != nil {
			return err
		}
		return putJSON(bucket, []byte(apID), dismissal)
	})
}

// Undismiss removes a dismissal.
func (p *PendingDismissalStore) Undismiss(apID string) error {
	return p.db.Update(func(tx *bolt.Tx) error {
		bucket, err := pendingBucket(tx)
		if err != nil {
			return err
		}
		return bucket.Delete([]byte(apID))
	})
}

// Active returns the dismissals still in force at now, keyed by AP ID.
func (p *PendingDismissalStore) Active(now time.Time) (map[string]PendingDismissal, error) {
	result := map[string]PendingDismissal{}
	err := p.db.View(func(tx *bolt.Tx) error {
		bucket, err := pendingBucket(tx)
		if err != nil {
			return err
		}
		return bucket.ForEach(func(key, raw []byte) error {
			var dismissal PendingDismissal
			if err := json.Unmarshal(raw, &dismissal); err != nil {
				return err
			}
			if now.Before(dismissal.Until) {
				result[string(key)] = dismissal
			}
			return nil
		})
	})
	return result, err
}

// Prune deletes dismissals that ended at or before now.
func (p *PendingDismissalStore) Prune(now time.Time) (int, error) {
	removed := 0
	err := p.db.Update(func(tx *bolt.Tx) error {
		bucket, err := pendingBucket(tx)
		if err != nil {
			return err
		}
		cursor := bucket.Cursor()
		for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
			var dismissal PendingDismissal
			if err := json.Unmarshal(raw, &dismissal); err != nil {
				return err
			}
			if !now.Before(dismissal.Until) {
				if err := cursor.Delete(); err != nil {
					return err
				}
				removed++
			}
		}
		return nil
	})
	return removed, err
}
