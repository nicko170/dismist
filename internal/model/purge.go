package model

import (
	"bytes"
	"context"
	"errors"
	"strings"

	bolt "go.etcd.io/bbolt"
)

// PurgeAPHistory deletes every stored history record of one AP in every
// site: minute and hourly samples, client sessions (with their index and
// active markers), events2 and legacy events, its ap_status record and its
// neighbour scan. It backs DELETE /api/aps/$id?purge_history=1 and is
// called after the AP record is removed (otherwise the observer writes new
// history for it). It works in batches of batch keys per transaction
// (DefaultRetentionBatch when batch <= 0).
func (s *BoltStore) PurgeAPHistory(ctx context.Context, apID string, batch int) (PruneResult, error) {
	if apID == "" || strings.ContainsRune(apID, 0) {
		return PruneResult{}, errors.New("valid AP ID required")
	}
	return s.purge(ctx, batch, func(site []byte) []byte {
		return append(append([]byte(nil), site...), []byte(apID+"\x00")...)
	}, func(tx *bolt.Tx) error {
		for _, name := range []string{BucketAPStatus, BucketScans} {
			if bucket := tx.Bucket([]byte(name)); bucket != nil {
				if err := bucket.Delete([]byte(apID)); err != nil {
					return err
				}
			}
		}
		return nil
	}, apID)
}

// PurgeSiteHistory deletes every stored history record of a site (samples,
// sessions, events). It backs site deletion; call it after the site is
// removed.
func (s *BoltStore) PurgeSiteHistory(ctx context.Context, siteID string, batch int) (PruneResult, error) {
	if siteID == "" || strings.ContainsRune(siteID, 0) {
		return PruneResult{}, errors.New("valid site ID required")
	}
	want := []byte(siteID + "\x00")
	return s.purge(ctx, batch, func(site []byte) []byte {
		if bytes.Equal(site, want) {
			return site
		}
		return nil
	}, nil, "")
}

// purge deletes, for every site prefix where scope returns a non-nil
// prefix, the keys under that prefix in the per-AP history buckets, the
// matching sessions_t entries and the matching events2 records. apID,
// when set, restricts sessions_t and events2 to that AP.
func (s *BoltStore) purge(ctx context.Context, batch int, scope func(site []byte) []byte, finish func(*bolt.Tx) error, apID string) (PruneResult, error) {
	if batch <= 0 {
		batch = DefaultRetentionBatch
	}
	var result PruneResult
	prefixed := []struct {
		name  string
		count *int
	}{
		{BucketSamples, &result.Samples},
		{BucketSamplesHourly, &result.SamplesHourly},
		{BucketEvents, &result.LegacyEvents},
		{BucketClientActive, nil},
		{BucketClientSessions, nil},
	}
	for _, item := range prefixed {
		transactions, err := s.batched(ctx, batch, purgePrefixed(item.name, scope, item.count))
		result.Transactions += transactions
		if err != nil {
			return result, err
		}
	}
	steps := []batchStep{purgeSessionIndex(scope, apID, &result.Sessions), purgeEvents2(scope, apID, &result.Events)}
	for _, step := range steps {
		transactions, err := s.batched(ctx, batch, step)
		result.Transactions += transactions
		if err != nil {
			return result, err
		}
	}
	if finish != nil {
		if err := s.db.Update(finish); err != nil {
			return result, err
		}
		result.Transactions++
	}
	return result, nil
}

func purgePrefixed(name string, scope func([]byte) []byte, count *int) batchStep {
	return func(tx *bolt.Tx, budget int) (int, bool, error) {
		bucket := tx.Bucket([]byte(name))
		if bucket == nil {
			return 0, true, nil
		}
		var doomed [][]byte
		for _, site := range segments(bucket, nil) {
			prefix := scope(site)
			if prefix == nil {
				continue
			}
			cursor := bucket.Cursor()
			for key, _ := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, _ = cursor.Next() {
				if len(doomed) == budget {
					if count != nil {
						*count += len(doomed)
					}
					return len(doomed), false, deleteKeys(bucket, doomed)
				}
				doomed = append(doomed, append([]byte(nil), key...))
			}
		}
		if count != nil {
			*count += len(doomed)
		}
		return len(doomed), true, deleteKeys(bucket, doomed)
	}
}

func purgeSessionIndex(scope func([]byte) []byte, apID string, count *int) batchStep {
	return func(tx *bolt.Tx, budget int) (int, bool, error) {
		index := tx.Bucket([]byte(BucketSessionsT))
		if index == nil {
			return 0, true, nil
		}
		var doomed [][]byte
		for _, site := range segments(index, nil) {
			if scope(site) == nil {
				continue
			}
			cursor := index.Cursor()
			for key, _ := cursor.Seek(site); key != nil && bytes.HasPrefix(key, site); key, _ = cursor.Next() {
				if apID != "" {
					ap, _, ok := splitSessionTKey(key, len(site))
					if !ok || string(ap) != apID {
						continue
					}
				}
				if len(doomed) == budget {
					*count += len(doomed)
					return len(doomed), false, deleteKeys(index, doomed)
				}
				doomed = append(doomed, append([]byte(nil), key...))
			}
		}
		*count += len(doomed)
		return len(doomed), true, deleteKeys(index, doomed)
	}
}

func purgeEvents2(scope func([]byte) []byte, apID string, count *int) batchStep {
	return func(tx *bolt.Tx, budget int) (int, bool, error) {
		b, err := openEventBuckets(tx)
		if err != nil {
			return 0, true, err
		}
		type doomedEvent struct {
			site string
			key  []byte
		}
		var doomed []doomedEvent
		done := true
	sites:
		for _, site := range segments(b.main, nil) {
			if scope(site) == nil {
				continue
			}
			cursor := b.main.Cursor()
			for key, _ := cursor.Seek(site); key != nil && bytes.HasPrefix(key, site); key, _ = cursor.Next() {
				if apID != "" {
					_, ap, _, ok := eventKeyParts(key, len(site))
					if !ok || string(ap) != apID {
						continue
					}
				}
				if 3*(len(doomed)+1) > budget {
					done = false
					break sites
				}
				doomed = append(doomed, doomedEvent{string(site[:len(site)-1]), append([]byte(nil), key...)})
			}
		}
		for _, event := range doomed {
			deleteEvent2(b, event.site, event.key)
		}
		*count += len(doomed)
		return 3 * len(doomed), done, nil
	}
}
