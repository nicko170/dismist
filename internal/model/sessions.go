package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// SessionRecord is one persisted web session (design §4.1, phase 1). It is
// stored under SessionKey(session ID), never the session ID itself.
type SessionRecord struct {
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	LastUsed  time.Time `json:"last_used,omitzero"`
	UserAgent string    `json:"user_agent,omitempty"`
	Remote    string    `json:"remote,omitempty"`
}

// StoredSession is a session record with its key.
type StoredSession struct {
	Key string `json:"key"`
	SessionRecord
}

// SessionKey is the stored key of a session ID: hex SHA-256. A database
// reader cannot use it as a cookie.
func SessionKey(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}

// SessionStore is the sessions bucket.
type SessionStore struct{ db *bolt.DB }

// Sessions returns the persisted session store.
func (s *BoltStore) Sessions() *SessionStore { return &SessionStore{db: s.db} }

func (s *SessionStore) bucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	bucket := tx.Bucket([]byte(BucketSessions))
	if bucket == nil {
		return nil, errors.New("sessions bucket missing")
	}
	return bucket, nil
}

// Put stores a session under key (use SessionKey).
func (s *SessionStore) Put(key string, record SessionRecord) error {
	if key == "" {
		return errors.New("session key required")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		return putJSON(bucket, []byte(key), record)
	})
}

// Get returns a session; ErrNotFound when missing or expired at now.
func (s *SessionStore) Get(key string, now time.Time) (SessionRecord, error) {
	var record SessionRecord
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		raw := bucket.Get([]byte(key))
		if raw == nil {
			return ErrNotFound
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if !record.Expires.IsZero() && !now.Before(record.Expires) {
			return ErrNotFound
		}
		return nil
	})
	return record, err
}

// Touch updates last_used and, when expires is non-zero, the expiry.
func (s *SessionStore) Touch(key string, lastUsed, expires time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		raw := bucket.Get([]byte(key))
		if raw == nil {
			return ErrNotFound
		}
		var record SessionRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		record.LastUsed = lastUsed.UTC()
		if !expires.IsZero() {
			record.Expires = expires.UTC()
		}
		return putJSON(bucket, []byte(key), record)
	})
}

// Delete removes one session.
func (s *SessionStore) Delete(key string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		if bucket.Get([]byte(key)) == nil {
			return ErrNotFound
		}
		return bucket.Delete([]byte(key))
	})
}

// List returns the sessions that have not expired at now, newest first.
func (s *SessionStore) List(now time.Time) ([]StoredSession, error) {
	result := make([]StoredSession, 0)
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		return bucket.ForEach(func(key, raw []byte) error {
			var record SessionRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			if record.Expires.IsZero() || now.Before(record.Expires) {
				result = append(result, StoredSession{Key: string(key), SessionRecord: record})
			}
			return nil
		})
	})
	sort.SliceStable(result, func(i, j int) bool { return result[i].Created.After(result[j].Created) })
	return result, err
}

// DeleteExpired removes sessions that expired at or before now and returns
// how many it removed.
func (s *SessionStore) DeleteExpired(now time.Time) (int, error) {
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		cursor := bucket.Cursor()
		for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
			var record SessionRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			if !record.Expires.IsZero() && !now.Before(record.Expires) {
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

// DeleteAll revokes every session except keep (pass "" to revoke all) and
// returns how many it removed.
func (s *SessionStore) DeleteAll(keep string) (int, error) {
	removed := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := s.bucket(tx)
		if err != nil {
			return err
		}
		cursor := bucket.Cursor()
		for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
			if string(key) == keep {
				continue
			}
			if err := cursor.Delete(); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}
