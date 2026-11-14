package model

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Config history (design §4.11). Every committed config mutation appends a
// ChangeRecord in the same transaction; actions (locate, reboot, backup
// download, PSK reveal, token creation, migration, re-migration) append a
// record of kind "action" without a snapshot. Keys are the state revision
// (8 bytes big-endian) followed by an 8-byte bucket sequence, so actions at
// the same revision never collide and a forward cursor walks oldest first.

// Change record kinds.
const (
	ChangeConfig    = "config"
	ChangeAction    = "action"
	ChangeMigration = "migration"
)

// Actor kinds.
const (
	ActorSession = "session"
	ActorToken   = "token"
	ActorSystem  = "system"
)

// Actor is who made a change.
type Actor struct {
	Kind      string `json:"kind"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Remote    string `json:"remote,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// ObjectRef names one changed object.
type ObjectRef struct {
	Kind string `json:"kind"` // "site" | "network" | "ap"
	ID   string `json:"id"`
}

// ChangeScope is what a change touched.
type ChangeScope struct {
	SiteID  string      `json:"site_id,omitempty"`
	Objects []ObjectRef `json:"objects,omitempty"`
}

// ConfigSnapshot holds the typed records of the affected site with every
// secret replaced by a keyed digest (hmac:<16 hex>).
type ConfigSnapshot struct {
	Sites    []Site     `json:"sites"`
	Networks []Network  `json:"networks"`
	APs      []APRecord `json:"aps"`
}

// EffectiveRef is an AP's effective hash at a revision.
type EffectiveRef struct {
	Hash     string `json:"hash"`
	Revision uint64 `json:"revision"`
}

// ApplyResult is an AP's apply outcome for a revision (from
// ep-config-applied).
type ApplyResult struct {
	State string    `json:"state"` // "pending" | "applied" | "rejected" | "timeout"
	At    time.Time `json:"at,omitzero"`
	Error string    `json:"error,omitempty"`
}

// ChangeRecord is one config_history entry.
type ChangeRecord struct {
	Revision  uint64                  `json:"revision"`
	Seq       uint64                  `json:"seq,omitempty"` // set by the store
	At        time.Time               `json:"at"`
	Kind      string                  `json:"kind"`
	Actor     Actor                   `json:"actor"`
	Endpoint  string                  `json:"endpoint,omitempty"`
	Summary   string                  `json:"summary"`
	Scope     ChangeScope             `json:"scope"`
	Snapshot  *ConfigSnapshot         `json:"snapshot,omitempty"`
	Effective map[string]EffectiveRef `json:"effective,omitempty"`
	Results   map[string]ApplyResult  `json:"results,omitempty"`
	// Report carries a migration's lift report.
	Report json.RawMessage `json:"report,omitempty"`
}

// HistoryBucket reads and writes config_history inside a transaction.
type HistoryBucket struct {
	bucket *bolt.Bucket
	mem    *[]ChangeRecord
}

// ConfigHistory returns the config_history bucket inside this transaction.
func (t *Tx) ConfigHistory() *HistoryBucket {
	if t.bolt != nil {
		return &HistoryBucket{bucket: t.bolt.Bucket([]byte(BucketConfigHistory))}
	}
	if t.mem == nil {
		t.mem = &memBuckets{apConfig: map[string][]byte{}}
	}
	return &HistoryBucket{mem: &t.mem.history}
}

func historyKey(revision, seq uint64) []byte {
	var key [16]byte
	binary.BigEndian.PutUint64(key[:8], revision)
	binary.BigEndian.PutUint64(key[8:], seq)
	return key[:]
}

// Append stores a record and returns it with its sequence number.
func (h *HistoryBucket) Append(record ChangeRecord) (ChangeRecord, error) {
	if record.Kind == "" {
		record.Kind = ChangeConfig
	}
	if record.At.IsZero() {
		record.At = time.Now().UTC()
	}
	if h.mem != nil {
		record.Seq = uint64(len(*h.mem) + 1)
		*h.mem = append(*h.mem, record)
		return record, nil
	}
	if h.bucket == nil {
		return record, errors.New("config_history bucket missing")
	}
	seq, err := h.bucket.NextSequence()
	if err != nil {
		return record, err
	}
	record.Seq = seq
	raw, err := json.Marshal(record)
	if err != nil {
		return record, err
	}
	return record, h.bucket.Put(historyKey(record.Revision, seq), raw)
}

// Get returns the config record of a revision (the first record stored at
// that revision whose kind is not "action"), or the first record when
// every record of the revision is an action.
func (h *HistoryBucket) Get(revision uint64) (ChangeRecord, bool, error) {
	var first *ChangeRecord
	found := false
	var result ChangeRecord
	err := h.each(revision, func(record ChangeRecord) bool {
		if first == nil {
			copied := record
			first = &copied
		}
		if record.Kind != ChangeAction {
			result, found = record, true
			return false
		}
		return true
	})
	if err != nil {
		return ChangeRecord{}, false, err
	}
	if !found && first != nil {
		return *first, true, nil
	}
	return result, found, nil
}

func (h *HistoryBucket) each(revision uint64, visit func(ChangeRecord) bool) error {
	if h.mem != nil {
		for _, record := range *h.mem {
			if record.Revision == revision && !visit(record) {
				return nil
			}
		}
		return nil
	}
	var prefix [8]byte
	binary.BigEndian.PutUint64(prefix[:], revision)
	cursor := h.bucket.Cursor()
	for key, raw := cursor.Seek(prefix[:]); key != nil && bytes.HasPrefix(key, prefix[:]); key, raw = cursor.Next() {
		var record ChangeRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if !visit(record) {
			return nil
		}
	}
	return nil
}

// SetResult records an AP's apply result on the config record of a
// revision. A missing record is not an error (it may have been pruned).
func (h *HistoryBucket) SetResult(revision uint64, apID string, result ApplyResult) error {
	if h.mem != nil {
		for index := range *h.mem {
			record := &(*h.mem)[index]
			if record.Revision == revision && record.Kind != ChangeAction {
				if record.Results == nil {
					record.Results = map[string]ApplyResult{}
				}
				record.Results[apID] = result
				return nil
			}
		}
		return nil
	}
	var prefix [8]byte
	binary.BigEndian.PutUint64(prefix[:], revision)
	cursor := h.bucket.Cursor()
	for key, raw := cursor.Seek(prefix[:]); key != nil && bytes.HasPrefix(key, prefix[:]); key, raw = cursor.Next() {
		var record ChangeRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if record.Kind == ChangeAction {
			continue
		}
		if record.Results == nil {
			record.Results = map[string]ApplyResult{}
		}
		record.Results[apID] = result
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return h.bucket.Put(append([]byte(nil), key...), encoded)
	}
	return nil
}

// HistoryQuery selects config history records.
type HistoryQuery struct {
	// SiteID keeps records scoped to this site and global records (an
	// empty scope, for example a migration); "" keeps every record.
	SiteID string
	// APID keeps records whose scope names the AP or that list it in
	// Effective or Results.
	APID string
	// Before keeps records with a revision below it (0 = no bound).
	Before uint64
	// Kinds keeps only these kinds (empty = every kind).
	Kinds []string
	// Limit caps the result (default 50, max 500).
	Limit int
}

// ConfigHistoryStore reads and writes config_history outside a mutation.
type ConfigHistoryStore struct{ db *bolt.DB }

// ConfigHistory returns the config history store.
func (s *BoltStore) ConfigHistory() *ConfigHistoryStore { return &ConfigHistoryStore{db: s.db} }

// Get returns the config record of a revision.
func (c *ConfigHistoryStore) Get(revision uint64) (ChangeRecord, bool, error) {
	var record ChangeRecord
	var found bool
	err := c.db.View(func(tx *bolt.Tx) error {
		var err error
		record, found, err = (&HistoryBucket{bucket: tx.Bucket([]byte(BucketConfigHistory))}).Get(revision)
		return err
	})
	return record, found, err
}

// Append stores an action record outside a state mutation (backup
// download, PSK reveal, token creation, reboot).
func (c *ConfigHistoryStore) Append(record ChangeRecord) (ChangeRecord, error) {
	err := c.db.Update(func(tx *bolt.Tx) error {
		var err error
		record, err = (&HistoryBucket{bucket: tx.Bucket([]byte(BucketConfigHistory))}).Append(record)
		return err
	})
	return record, err
}

// SetResult records an AP's apply result for a revision.
func (c *ConfigHistoryStore) SetResult(revision uint64, apID string, result ApplyResult) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		return (&HistoryBucket{bucket: tx.Bucket([]byte(BucketConfigHistory))}).SetResult(revision, apID, result)
	})
}

func (q HistoryQuery) matches(record ChangeRecord) bool {
	if q.SiteID != "" && record.Scope.SiteID != "" && record.Scope.SiteID != q.SiteID {
		return false
	}
	if len(q.Kinds) > 0 {
		ok := false
		for _, kind := range q.Kinds {
			ok = ok || kind == record.Kind
		}
		if !ok {
			return false
		}
	}
	if q.APID != "" {
		_, inEffective := record.Effective[q.APID]
		_, inResults := record.Results[q.APID]
		named := false
		for _, object := range record.Scope.Objects {
			named = named || (object.Kind == "ap" && object.ID == q.APID)
		}
		if !inEffective && !inResults && !named {
			return false
		}
	}
	return true
}

// List returns matching records newest first, and the revision to pass as
// Before for the next page (0 at the end).
func (c *ConfigHistoryStore) List(query HistoryQuery) ([]ChangeRecord, uint64, error) {
	limit := query.Limit
	switch {
	case limit <= 0:
		limit = 50
	case limit > 500:
		limit = 500
	}
	var result []ChangeRecord
	var next uint64
	err := c.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket([]byte(BucketConfigHistory)).Cursor()
		var key, raw []byte
		if query.Before > 0 {
			key, raw = cursor.Seek(historyKey(query.Before, 0))
			if key == nil {
				key, raw = cursor.Last()
			} else {
				key, raw = cursor.Prev()
			}
		} else {
			key, raw = cursor.Last()
		}
		for ; key != nil; key, raw = cursor.Prev() {
			var record ChangeRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			if !query.matches(record) {
				continue
			}
			if len(result) == limit {
				next = result[len(result)-1].Revision
				// Records of one revision stay on one page: the next page
				// starts below the last revision returned.
				for len(result) > 0 && result[len(result)-1].Revision == next && record.Revision == next {
					result = result[:len(result)-1]
				}
				if len(result) == 0 {
					return errors.New("more records at one revision than the page limit")
				}
				next = result[len(result)-1].Revision
				return nil
			}
			result = append(result, record)
		}
		return nil
	})
	return result, next, err
}
