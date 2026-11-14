package model

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ClientMeta is the operator's alias and notes for one client MAC (phase
// 4, design §4.1 client_meta).
type ClientMeta struct {
	Alias     string    `json:"alias,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Client meta limits.
const (
	MaxClientAlias = 64
	MaxClientNotes = 2048
)

// ClientMetaStore reads and writes client_meta.
type ClientMetaStore struct{ db *bolt.DB }

// ClientMeta returns the client meta store.
func (s *BoltStore) ClientMeta() *ClientMetaStore { return &ClientMetaStore{db: s.db} }

func clientMetaKey(mac string) []byte {
	return []byte(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":")))
}

// Get returns a client's meta.
func (c *ClientMetaStore) Get(mac string) (ClientMeta, bool, error) {
	var meta ClientMeta
	found := false
	err := c.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(BucketClientMeta)).Get(clientMetaKey(mac))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &meta)
	})
	return meta, found, err
}

// Put stores a client's meta; an empty alias and notes delete it.
func (c *ClientMetaStore) Put(mac string, meta ClientMeta) error {
	if len(meta.Alias) > MaxClientAlias || len(meta.Notes) > MaxClientNotes {
		return errors.New("client alias or notes too long")
	}
	key := clientMetaKey(mac)
	if len(key) == 0 {
		return errors.New("client MAC required")
	}
	return c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketClientMeta))
		if meta.Alias == "" && meta.Notes == "" {
			return bucket.Delete(key)
		}
		if meta.UpdatedAt.IsZero() {
			meta.UpdatedAt = time.Now().UTC()
		}
		return putJSON(bucket, key, meta)
	})
}

// All returns every client's meta by MAC.
func (c *ClientMetaStore) All() (map[string]ClientMeta, error) {
	result := map[string]ClientMeta{}
	err := c.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(BucketClientMeta)).ForEach(func(key, raw []byte) error {
			var meta ClientMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				return nil
			}
			result[string(key)] = meta
			return nil
		})
	})
	return result, err
}
