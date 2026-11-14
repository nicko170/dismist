package model

import (
	"encoding/json"
	"errors"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

// TxStateStore is a StateStore whose mutations can also write other buckets
// in the same transaction. Both BoltStore and the JSON Store implement it;
// config mutations use it so ap_config.desired commits with the state
// (design §4.4 "Change detection").
type TxStateStore interface {
	StateStore
	UpdateTx(func(*State, *Tx) error) error
}

// Tx is the transaction a config mutation runs in. Its methods read and
// write the non-state buckets and commit or roll back with the state.
type Tx struct {
	bolt    *bolt.Tx
	hashKey []byte
	mem     *memBuckets // the JSON Store keeps ap_config in memory
}

// Bolt returns the underlying bbolt write transaction, or nil for the JSON
// store. internal/migrate uses it for raw bucket access.
func (t *Tx) Bolt() *bolt.Tx { return t.bolt }

// HashKey returns meta.hash_key (nil for the JSON store).
func (t *Tx) HashKey() []byte { return append([]byte(nil), t.hashKey...) }

// APConfig returns the ap_config bucket inside this transaction.
func (t *Tx) APConfig() *APConfigBucket {
	if t.bolt != nil {
		return &APConfigBucket{bucket: t.bolt.Bucket([]byte(BucketAPConfig)), hashKey: t.hashKey}
	}
	if t.mem == nil {
		t.mem = &memBuckets{apConfig: map[string][]byte{}}
	}
	return &APConfigBucket{mem: t.mem.apConfig, hashKey: t.hashKey}
}

// Schema reads meta.schema and meta.compat (absent = 1).
func (t *Tx) Schema() (SchemaInfo, error) {
	info := SchemaInfo{Schema: 1, Compat: 1}
	if t.bolt == nil {
		return info, nil
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return info, err
	}
	if info.Schema, err = readUint(meta, MetaSchema, 1); err != nil {
		return info, err
	}
	if info.Compat, err = readUint(meta, MetaCompat, 1); err != nil {
		return info, err
	}
	return info, nil
}

// SetSchema writes meta.schema (the data format the newest writer used).
func (t *Tx) SetSchema(schema uint64) error {
	if t.bolt == nil {
		return nil
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return err
	}
	return putUint(meta, MetaSchema, schema)
}

// SetCompat writes meta.compat. Raising it above SupportedSchema makes this
// binary refuse the database on its next start, so callers raise it only
// as design §4.1 rule 4 or 5 allows.
func (t *Tx) SetCompat(compat uint64) error {
	if t.bolt == nil {
		return nil
	}
	if compat < 1 {
		return errors.New("compat must be at least 1")
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return err
	}
	return putUint(meta, MetaCompat, compat)
}

// H0 returns every recorded phase-0 baseline.
func (t *Tx) H0() (map[string]H0Record, error) {
	if t.bolt == nil {
		return map[string]H0Record{}, nil
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return nil, err
	}
	return decodeH0(meta.Get([]byte(MetaH0)))
}

// PutH0 records the baseline of one AP.
func (t *Tx) PutH0(record H0Record) error {
	if t.bolt == nil {
		return errors.New("H0 needs the bbolt store")
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return err
	}
	all, err := decodeH0(meta.Get([]byte(MetaH0)))
	if err != nil {
		return err
	}
	all[record.APID] = record
	raw, err := json.Marshal(h0Document{APs: all})
	if err != nil {
		return err
	}
	return meta.Put([]byte(MetaH0), raw)
}

// MetaJSON reads a JSON meta value into out; false when absent.
func (t *Tx) MetaJSON(key string, out any) (bool, error) {
	if t.bolt == nil {
		return false, nil
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return false, err
	}
	raw := meta.Get([]byte(key))
	if raw == nil {
		return false, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return true, fmt.Errorf("meta.%s: %w", key, err)
	}
	return true, nil
}

// PutMetaJSON stores value as JSON under a meta key. The schema, compat,
// revision and hash_key keys are not JSON and are refused.
func (t *Tx) PutMetaJSON(key string, value any) error {
	switch key {
	case MetaRevision, MetaUpdatedAt, MetaSchema, MetaCompat, MetaHashKey:
		return fmt.Errorf("meta.%s is not a JSON key", key)
	}
	if t.bolt == nil {
		return errors.New("meta needs the bbolt store")
	}
	meta, err := metaBucket(t.bolt)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return meta.Put([]byte(key), raw)
}

type memBuckets struct {
	apConfig map[string][]byte
}
