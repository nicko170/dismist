package model

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Meta keys (design §4.1). Integers are 8-byte big-endian, like "revision".
const (
	MetaRevision         = "revision"
	MetaUpdatedAt        = "updated_at"
	MetaSchema           = "schema"
	MetaCompat           = "compat"
	MetaHashKey          = "hash_key"
	MetaH0               = "h0"
	MetaReady            = "ready"
	MetaV2Revision       = "v2_revision"
	MetaV2LegacyDigest   = "v2_legacy_digest"
	MetaMigrationReport  = "migration_v2_report"
	MetaMigrationState   = "migration_state"
	MetaPreMigrationCopy = "pre_migration_copies"
)

// SupportedSchema is the newest data format this binary understands. An M0
// binary understands schema 1 only; the phase-2 binary raises it to 2.
const SupportedSchema uint64 = 1

// ExitCompat is the process exit code when the database needs a newer
// binary (EX_CONFIG). Exit 78 is used for nothing else (design §9.3 #2).
const ExitCompat = 78

// HashKeySize is the length of meta.hash_key.
const HashKeySize = 32

// CompatError means meta.compat names a schema this binary does not support.
// Running would compile a wrong config, so dismistd must exit with
// ExitCompat (see ExitIfCompat).
type CompatError struct {
	Compat    uint64 // meta.compat as stored
	Supported uint64 // SupportedSchema of this binary
}

func (e *CompatError) Error() string {
	return fmt.Sprintf("controller.db needs dismist schema %d; restore controller.db.pre-v%d-* to run this version (docs/kubernetes.md#rollback)", e.Compat, e.Compat)
}

// ExitCode is the process exit code for this error.
func (e *CompatError) ExitCode() int { return ExitCompat }

// ExitIfCompat logs the compat message and exits with ExitCompat when err is
// (or wraps) a *CompatError. dismistd calls it right after OpenBolt fails;
// any other error is left to the caller.
func ExitIfCompat(err error) {
	var compat *CompatError
	if errors.As(err, &compat) {
		log.Print(compat.Error())
		os.Exit(compat.ExitCode())
	}
}

// checkCompat refuses a database whose meta.compat is newer than this binary
// supports. Absent means 1. A value that cannot be read is treated as newer:
// guessing would be unsafe.
func checkCompat(tx *bolt.Tx) error {
	meta := tx.Bucket([]byte(BucketMeta))
	if meta == nil {
		return nil
	}
	compat, err := readUint(meta, MetaCompat, 1)
	if err != nil {
		return &CompatError{Compat: SupportedSchema + 1, Supported: SupportedSchema}
	}
	if compat > SupportedSchema {
		return &CompatError{Compat: compat, Supported: SupportedSchema}
	}
	return nil
}

func ensureHashKey(tx *bolt.Tx) ([]byte, error) {
	meta := tx.Bucket([]byte(BucketMeta))
	if existing := meta.Get([]byte(MetaHashKey)); existing != nil {
		if len(existing) != HashKeySize {
			return nil, errors.New("meta.hash_key has the wrong length")
		}
		return append([]byte(nil), existing...), nil
	}
	key := make([]byte, HashKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := meta.Put([]byte(MetaHashKey), key); err != nil {
		return nil, err
	}
	return key, nil
}

func readUint(bucket *bolt.Bucket, key string, absent uint64) (uint64, error) {
	raw := bucket.Get([]byte(key))
	if raw == nil {
		return absent, nil
	}
	if len(raw) != 8 {
		return 0, fmt.Errorf("meta.%s is not an 8-byte integer", key)
	}
	return binary.BigEndian.Uint64(raw), nil
}

func putUint(bucket *bolt.Bucket, key string, value uint64) error {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	return bucket.Put([]byte(key), raw[:])
}

// SchemaInfo is the pair of schema numbers in meta (absent = 1).
type SchemaInfo struct {
	Schema uint64 `json:"schema"`
	Compat uint64 `json:"compat"`
}

// Schema reads meta.schema and meta.compat.
func (s *BoltStore) Schema() (SchemaInfo, error) {
	var info SchemaInfo
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		info, err = (&Tx{bolt: tx}).Schema()
		return err
	})
	return info, err
}

// H0Record is the phase-0 baseline of one AP (design §4.5, §8.3): the raw
// config bytes the legacy compiler produced, their keyed HMAC, the normaliser
// version and the effective hash. Raw contains the PSK; it lives only in the
// private database and is never returned by a view.
type H0Record struct {
	APID              string    `json:"ap_id"`
	RecordedAt        time.Time `json:"recorded_at"`
	ConfigRevision    uint64    `json:"config_revision"`
	Raw               []byte    `json:"raw"`
	RawHMAC           string    `json:"raw_hmac"`
	NormaliserVersion int       `json:"normaliser_version"`
	EffectiveHash     string    `json:"effective_hash"`
	TemplateSHA256    string    `json:"template_sha256,omitempty"`
}

type h0Document struct {
	APs map[string]H0Record `json:"aps"`
}

// H0 returns every recorded baseline, keyed by AP ID.
func (s *BoltStore) H0() (map[string]H0Record, error) {
	var result map[string]H0Record
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		result, err = (&Tx{bolt: tx}).H0()
		return err
	})
	return result, err
}

// PutH0 records (or replaces) the baseline of one AP in meta.h0.
func (s *BoltStore) PutH0(record H0Record) error {
	if record.APID == "" {
		return errors.New("H0 needs an AP ID")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return (&Tx{bolt: tx}).PutH0(record)
	})
}

// Probe checks that the database can be read and written, by writing and
// reading back meta.ready. /readyz uses it (design §4.9).
func (s *BoltStore) Probe() error {
	now, err := time.Now().UTC().MarshalText()
	if err != nil {
		return err
	}
	if err := s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(BucketMeta))
		if meta == nil {
			return errors.New("missing metadata bucket")
		}
		return meta.Put([]byte(MetaReady), now)
	}); err != nil {
		return fmt.Errorf("database write: %w", err)
	}
	return s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(BucketMeta))
		if meta == nil || string(meta.Get([]byte(MetaReady))) != string(now) {
			return errors.New("database read: meta.ready does not match the value just written")
		}
		return nil
	})
}

// MetaJSON reads a JSON meta value (migration_state, migration_v2_report, …)
// into out. It reports false when the key is absent.
func (s *BoltStore) MetaJSON(key string, out any) (bool, error) {
	found := false
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		found, err = (&Tx{bolt: tx}).MetaJSON(key, out)
		return err
	})
	return found, err
}

// PutMetaJSON stores value as JSON under a meta key.
func (s *BoltStore) PutMetaJSON(key string, value any) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return (&Tx{bolt: tx}).PutMetaJSON(key, value)
	})
}

func metaBucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	meta := tx.Bucket([]byte(BucketMeta))
	if meta == nil {
		return nil, errors.New("missing metadata bucket")
	}
	return meta, nil
}

func decodeH0(raw []byte) (map[string]H0Record, error) {
	document := h0Document{APs: map[string]H0Record{}}
	if raw == nil {
		return document.APs, nil
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("meta.h0: %w", err)
	}
	if document.APs == nil {
		document.APs = map[string]H0Record{}
	}
	return document.APs, nil
}
