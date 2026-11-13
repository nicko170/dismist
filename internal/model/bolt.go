package model

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var bucketNames = []string{
	"sites", "aps", "networks", "templates", "ap_status",
	"client_sessions", "samples", "events", "meta",
	"client_active",
}

type BoltStore struct {
	mu    sync.RWMutex
	db    *bolt.DB
	state State
}

func OpenBolt(path, legacyJSON string) (*BoltStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("database file must be private")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	store := &BoltStore{db: db}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range bucketNames {
			if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
				return err
			}
		}
		if tx.Bucket([]byte("meta")).Get([]byte("revision")) != nil {
			return nil
		}
		state := State{Sites: []Site{}, APs: []APRecord{}, Networks: []Network{}}
		if legacyJSON != "" {
			raw, err := os.ReadFile(legacyJSON)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err == nil {
				if err := json.Unmarshal(raw, &state); err != nil {
					return err
				}
				if err := migrateLegacySites(&state); err != nil {
					return err
				}
			}
		}
		if err := validate(state); err != nil {
			return err
		}
		return writeBoltState(tx, state)
	})
	if err == nil {
		err = db.View(func(tx *bolt.Tx) error {
			var readErr error
			store.state, readErr = readBoltState(tx)
			return readErr
		})
	}
	if err == nil {
		changed := false
		for index := range store.state.APs {
			ap := &store.state.APs[index]
			if ap.ConfigRevision == 0 && store.state.Revision > 0 {
				ap.ConfigRevision = store.state.Revision
				ap.ConfigUpdatedAt = store.state.UpdatedAt
				changed = true
			}
		}
		if changed {
			err = db.Update(func(tx *bolt.Tx) error {
				bucket := tx.Bucket([]byte("aps"))
				for _, ap := range store.state.APs {
					if err := putJSON(bucket, []byte(ap.ID), ap); err != nil {
						return err
					}
				}
				return nil
			})
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *BoltStore) Close() error { return s.db.Close() }

func (s *BoltStore) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.state)
}

func (s *BoltStore) Update(change func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.state)
	for index := range next.APs {
		if next.APs[index].ConfigRevision == 0 {
			next.APs[index].ConfigRevision = s.state.Revision
			next.APs[index].ConfigUpdatedAt = s.state.UpdatedAt
		}
	}
	next.UpdatedAt = time.Now().UTC()
	if err := change(&next); err != nil {
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	next.Revision = s.state.Revision + 1
	if err := s.db.Update(func(tx *bolt.Tx) error {
		for _, previous := range s.state.APs {
			for _, current := range next.APs {
				if previous.ID == current.ID && previous.SiteID != current.SiteID {
					if err := closeActiveSessions(tx, previous.SiteID, previous.ID, next.UpdatedAt, "site moved"); err != nil {
						return err
					}
				}
			}
		}
		return writeBoltState(tx, next)
	}); err != nil {
		return err
	}
	s.state = next
	return nil
}

func migrateLegacySites(state *State) error {
	if len(state.Sites) > 0 {
		return nil
	}
	seen := map[string]bool{}
	for index := range state.APs {
		ap := &state.APs[index]
		if ap.ConfigRevision == 0 {
			ap.ConfigRevision = state.Revision
			ap.ConfigUpdatedAt = state.UpdatedAt
		}
		if ap.SiteID == "" || seen[ap.SiteID] {
			continue
		}
		state.Sites = append(state.Sites, Site{ID: ap.SiteID, Name: "Imported site"})
		seen[ap.SiteID] = true
	}
	for index := range state.Networks {
		if state.Networks[index].SiteID == "" {
			if len(state.Sites) != 1 {
				return errors.New("cannot infer legacy WLAN site")
			}
			state.Networks[index].SiteID = state.Sites[0].ID
		}
	}
	return nil
}

func readBoltState(tx *bolt.Tx) (State, error) {
	state := State{Sites: []Site{}, APs: []APRecord{}, Networks: []Network{}}
	meta := tx.Bucket([]byte("meta"))
	if meta == nil {
		return state, errors.New("missing metadata bucket")
	}
	if raw := meta.Get([]byte("revision")); len(raw) == 8 {
		state.Revision = binary.BigEndian.Uint64(raw)
	}
	if raw := meta.Get([]byte("updated_at")); len(raw) != 0 {
		if err := state.UpdatedAt.UnmarshalText(raw); err != nil {
			return state, err
		}
	}
	for _, item := range []struct {
		name string
		add  func([]byte) error
	}{
		{"sites", func(raw []byte) error {
			var value Site
			err := json.Unmarshal(raw, &value)
			state.Sites = append(state.Sites, value)
			return err
		}},
		{"aps", func(raw []byte) error {
			var value APRecord
			err := json.Unmarshal(raw, &value)
			state.APs = append(state.APs, value)
			return err
		}},
		{"networks", func(raw []byte) error {
			var value Network
			err := json.Unmarshal(raw, &value)
			state.Networks = append(state.Networks, value)
			return err
		}},
	} {
		bucket := tx.Bucket([]byte(item.name))
		if bucket == nil {
			return state, fmt.Errorf("missing %s bucket", item.name)
		}
		if err := bucket.ForEach(func(_, raw []byte) error { return item.add(raw) }); err != nil {
			return state, err
		}
	}
	return state, validate(state)
}

func writeBoltState(tx *bolt.Tx, state State) error {
	for _, name := range []string{"sites", "aps", "networks"} {
		if err := tx.DeleteBucket([]byte(name)); err != nil && !errors.Is(err, bolt.ErrBucketNotFound) {
			return err
		}
		if _, err := tx.CreateBucket([]byte(name)); err != nil {
			return err
		}
	}
	put := func(bucket string, key string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return tx.Bucket([]byte(bucket)).Put([]byte(key), raw)
	}
	for _, site := range state.Sites {
		if err := put("sites", site.ID, site); err != nil {
			return err
		}
	}
	for _, ap := range state.APs {
		if err := put("aps", ap.ID, ap); err != nil {
			return err
		}
	}
	for _, network := range state.Networks {
		if err := put("networks", network.SiteID+"\x00"+network.ID, network); err != nil {
			return err
		}
	}
	meta := tx.Bucket([]byte("meta"))
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], state.Revision)
	if err := meta.Put([]byte("revision"), revision[:]); err != nil {
		return err
	}
	date, err := state.UpdatedAt.MarshalText()
	if err != nil {
		return err
	}
	return meta.Put([]byte("updated_at"), date)
}
