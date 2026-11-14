package model

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// APConfigRecord is one ap_config entry (design §4.1): the last compiled
// config of an AP (change detection) and the last config actually delivered
// to it (served when a later compile fails, §4.4).
type APConfigRecord struct {
	Desired   *DesiredConfig   `json:"desired,omitempty"`
	Delivered *DeliveredConfig `json:"delivered,omitempty"`
}

// DesiredConfig is the compiled config dismist wants the AP to run.
type DesiredConfig struct {
	Revision uint64 `json:"revision"`
	// Timestamp is the config's Timestamp value (ConfigUpdatedAt as
	// fractional Unix seconds).
	Timestamp float64 `json:"timestamp"`
	// SHA is hex HMAC-SHA256(meta.hash_key, Bytes). It is keyed because the
	// bytes contain PSKs; it is still for internal comparison only.
	SHA   string `json:"sha"`
	Bytes []byte `json:"bytes"`
}

// DeliveredConfig is the config last sent in an ep-config reply.
type DeliveredConfig struct {
	Revision  uint64        `json:"revision"`
	Timestamp float64       `json:"timestamp"`
	SHA       string        `json:"sha"`
	Bytes     []byte        `json:"bytes"`
	At        time.Time     `json:"at"`
	Verified  *Verification `json:"verified,omitempty"`
}

// Verification is the result of post-delivery verification (design §5.9).
type Verification struct {
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	Mismatches []string  `json:"mismatches,omitempty"`
}

// APConfigBucket reads and writes ap_config inside one transaction.
type APConfigBucket struct {
	bucket  *bolt.Bucket
	mem     map[string][]byte
	hashKey []byte
}

func (b *APConfigBucket) get(apID string) []byte {
	if b.bucket != nil {
		return b.bucket.Get([]byte(apID))
	}
	return b.mem[apID]
}

func (b *APConfigBucket) put(apID string, raw []byte) error {
	if b.bucket != nil {
		return b.bucket.Put([]byte(apID), raw)
	}
	if b.mem == nil {
		return errors.New("ap_config bucket missing")
	}
	b.mem[apID] = append([]byte(nil), raw...)
	return nil
}

// Get returns the record of one AP; false when there is none.
func (b *APConfigBucket) Get(apID string) (APConfigRecord, bool, error) {
	var record APConfigRecord
	if b.bucket == nil && b.mem == nil {
		return record, false, errors.New("ap_config bucket missing")
	}
	raw := b.get(apID)
	if raw == nil {
		return record, false, nil
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, true, fmt.Errorf("ap_config %s: %w", apID, err)
	}
	return record, true, nil
}

// Desired returns the desired config of one AP; false when none is stored.
func (b *APConfigBucket) Desired(apID string) (DesiredConfig, bool, error) {
	record, _, err := b.Get(apID)
	if err != nil || record.Desired == nil {
		return DesiredConfig{}, false, err
	}
	return *record.Desired, true, nil
}

// PutDesired stores the desired config of one AP, keeping its delivered
// config. SHA is computed when empty.
func (b *APConfigBucket) PutDesired(apID string, desired DesiredConfig) error {
	record, _, err := b.Get(apID)
	if err != nil {
		return err
	}
	desired.Bytes = append([]byte(nil), desired.Bytes...)
	if desired.SHA == "" {
		desired.SHA = b.Digest(desired.Bytes)
	}
	record.Desired = &desired
	return b.store(apID, record)
}

// PutDelivered stores the config just sent to the AP, keeping its desired
// config. It clears any previous verification result. SHA is computed when
// empty.
func (b *APConfigBucket) PutDelivered(apID string, delivered DeliveredConfig) error {
	record, _, err := b.Get(apID)
	if err != nil {
		return err
	}
	delivered.Bytes = append([]byte(nil), delivered.Bytes...)
	if delivered.SHA == "" {
		delivered.SHA = b.Digest(delivered.Bytes)
	}
	record.Delivered = &delivered
	return b.store(apID, record)
}

// SetVerified records the verification result of the delivered config.
func (b *APConfigBucket) SetVerified(apID string, verification Verification) error {
	record, _, err := b.Get(apID)
	if err != nil {
		return err
	}
	if record.Delivered == nil {
		return fmt.Errorf("ap_config %s has no delivered config", apID)
	}
	record.Delivered.Verified = &verification
	return b.store(apID, record)
}

// Delete removes the record of one AP.
func (b *APConfigBucket) Delete(apID string) error {
	if b.bucket != nil {
		return b.bucket.Delete([]byte(apID))
	}
	delete(b.mem, apID)
	return nil
}

// All returns every record, keyed by AP ID.
func (b *APConfigBucket) All() (map[string]APConfigRecord, error) {
	result := map[string]APConfigRecord{}
	decode := func(key, raw []byte) error {
		var record APConfigRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return fmt.Errorf("ap_config %s: %w", key, err)
		}
		result[string(key)] = record
		return nil
	}
	if b.bucket != nil {
		return result, b.bucket.ForEach(decode)
	}
	keys := make([]string, 0, len(b.mem))
	for key := range b.mem {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := decode([]byte(key), b.mem[key]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Digest is the keyed digest stored as SHA.
func (b *APConfigBucket) Digest(config []byte) string {
	return configDigest(b.hashKey, config)
}

func configDigest(key, config []byte) string {
	if len(key) == 0 {
		sum := sha256.Sum256(config)
		return hex.EncodeToString(sum[:])
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("ap_config\x00"))
	mac.Write(config)
	return hex.EncodeToString(mac.Sum(nil))
}

func (b *APConfigBucket) store(apID string, record APConfigRecord) error {
	if apID == "" {
		return errors.New("ap_config needs an AP ID")
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return b.put(apID, raw)
}

// APConfigStore is the ap_config bucket outside a state mutation: the
// controller writes delivered configs and verification results through it.
type APConfigStore struct {
	store *BoltStore
}

// APConfig returns the ap_config store.
func (s *BoltStore) APConfig() *APConfigStore { return &APConfigStore{store: s} }

func (a *APConfigStore) view(fn func(*APConfigBucket) error) error {
	return a.store.db.View(func(tx *bolt.Tx) error {
		return fn((&Tx{bolt: tx, hashKey: a.store.hashKey}).APConfig())
	})
}

func (a *APConfigStore) update(fn func(*APConfigBucket) error) error {
	return a.store.db.Update(func(tx *bolt.Tx) error {
		return fn((&Tx{bolt: tx, hashKey: a.store.hashKey}).APConfig())
	})
}

// Get returns the record of one AP.
func (a *APConfigStore) Get(apID string) (APConfigRecord, bool, error) {
	var record APConfigRecord
	var found bool
	err := a.view(func(bucket *APConfigBucket) error {
		var err error
		record, found, err = bucket.Get(apID)
		return err
	})
	return record, found, err
}

// Desired returns the desired config of one AP.
func (a *APConfigStore) Desired(apID string) (DesiredConfig, bool, error) {
	record, _, err := a.Get(apID)
	if err != nil || record.Desired == nil {
		return DesiredConfig{}, false, err
	}
	return *record.Desired, true, nil
}

// Delivered returns the delivered config of one AP.
func (a *APConfigStore) Delivered(apID string) (DeliveredConfig, bool, error) {
	record, _, err := a.Get(apID)
	if err != nil || record.Delivered == nil {
		return DeliveredConfig{}, false, err
	}
	return *record.Delivered, true, nil
}

// PutDesired stores a desired config outside a state mutation (startup
// seeding of APs that have none yet).
func (a *APConfigStore) PutDesired(apID string, desired DesiredConfig) error {
	return a.update(func(bucket *APConfigBucket) error { return bucket.PutDesired(apID, desired) })
}

// PutDelivered records a config just sent in an ep-config reply.
func (a *APConfigStore) PutDelivered(apID string, delivered DeliveredConfig) error {
	return a.update(func(bucket *APConfigBucket) error { return bucket.PutDelivered(apID, delivered) })
}

// SetVerified records a post-delivery verification result.
func (a *APConfigStore) SetVerified(apID string, verification Verification) error {
	return a.update(func(bucket *APConfigBucket) error { return bucket.SetVerified(apID, verification) })
}

// All returns every record.
func (a *APConfigStore) All() (map[string]APConfigRecord, error) {
	var result map[string]APConfigRecord
	err := a.view(func(bucket *APConfigBucket) error {
		var err error
		result, err = bucket.All()
		return err
	})
	return result, err
}

// Digest is the keyed digest the store writes as SHA.
func (a *APConfigStore) Digest(config []byte) string { return configDigest(a.store.hashKey, config) }
