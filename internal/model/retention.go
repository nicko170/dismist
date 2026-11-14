package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Retention defaults (design §4.1).
const (
	DefaultRetainSamples       = 30 * 24 * time.Hour
	DefaultRetainSamplesHourly = 400 * 24 * time.Hour
	DefaultRetainSessions      = 180 * 24 * time.Hour
	DefaultRetainEvents        = 90 * 24 * time.Hour
	DefaultRetainConfigHistory = 500
	DefaultRetentionBatch      = 5000
	DefaultRetentionInterval   = time.Hour
)

// RetentionPolicy says how long history is kept. A zero duration (or
// count) keeps that history forever.
type RetentionPolicy struct {
	// Enabled switches history pruning on. It is off in phase 1 and
	// switched on in phase 3 (design §8.6); while off, RunRetention only
	// expires web sessions and pending-AP dismissals.
	Enabled bool
	// Samples applies to minute samples, SamplesHourly to the hourly
	// rollups (phase 3), Sessions to client sessions (by last activity;
	// an active session is never pruned), Events to events2 and the
	// legacy events bucket (by last_seen).
	Samples       time.Duration
	SamplesHourly time.Duration
	Sessions      time.Duration
	Events        time.Duration
	// ConfigHistory is how many config_history revisions are kept.
	ConfigHistory int
	// BatchSize is the number of keys deleted per transaction.
	BatchSize int
}

// DefaultRetention returns the design defaults, with pruning off.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{
		Samples: DefaultRetainSamples, SamplesHourly: DefaultRetainSamplesHourly,
		Sessions: DefaultRetainSessions, Events: DefaultRetainEvents,
		ConfigHistory: DefaultRetainConfigHistory, BatchSize: DefaultRetentionBatch,
	}
}

// Describe renders the policy for the System page (SystemView.db.retention).
func (p RetentionPolicy) Describe() map[string]string {
	duration := func(d time.Duration) string {
		if d <= 0 {
			return "forever"
		}
		if d%(24*time.Hour) == 0 {
			return fmt.Sprintf("%dd", d/(24*time.Hour))
		}
		return d.String()
	}
	history := "forever"
	if p.ConfigHistory > 0 {
		history = fmt.Sprintf("%d revisions", p.ConfigHistory)
	}
	pruning := "off"
	if p.Enabled {
		pruning = "hourly"
	}
	return map[string]string{
		"pruning": pruning, "samples": duration(p.Samples), "samples_hourly": duration(p.SamplesHourly),
		"sessions": duration(p.Sessions), "events": duration(p.Events), "config_history": history,
	}
}

func (p RetentionPolicy) batch() int {
	if p.BatchSize <= 0 {
		return DefaultRetentionBatch
	}
	return p.BatchSize
}

// PruneResult counts what one prune or purge removed (records, not keys).
type PruneResult struct {
	Samples           int `json:"samples"`
	SamplesHourly     int `json:"samples_hourly"`
	Sessions          int `json:"sessions"`
	Events            int `json:"events"`
	LegacyEvents      int `json:"legacy_events"`
	ConfigHistory     int `json:"config_history"`
	WebSessions       int `json:"web_sessions"`
	PendingDismissals int `json:"pending_dismissals"`
	// Transactions is the number of write transactions used.
	Transactions int `json:"transactions"`
}

// Total is the number of records removed.
func (r PruneResult) Total() int {
	return r.Samples + r.SamplesHourly + r.Sessions + r.Events + r.LegacyEvents + r.ConfigHistory + r.WebSessions + r.PendingDismissals
}

func (r *PruneResult) add(other PruneResult) {
	r.Samples += other.Samples
	r.SamplesHourly += other.SamplesHourly
	r.Sessions += other.Sessions
	r.Events += other.Events
	r.LegacyEvents += other.LegacyEvents
	r.ConfigHistory += other.ConfigHistory
	r.WebSessions += other.WebSessions
	r.PendingDismissals += other.PendingDismissals
	r.Transactions += other.Transactions
}

// batchStep deletes up to budget keys in tx and reports how many it used
// and whether nothing is left to do.
type batchStep func(tx *bolt.Tx, budget int) (used int, done bool, err error)

// batched runs step in write transactions of at most batch keys until it
// is done or ctx ends, and returns the number of transactions.
func (s *BoltStore) batched(ctx context.Context, batch int, step batchStep) (int, error) {
	transactions := 0
	for {
		if err := ctx.Err(); err != nil {
			return transactions, err
		}
		var done bool
		var used int
		err := s.db.Update(func(tx *bolt.Tx) error {
			var err error
			used, done, err = step(tx, batch)
			return err
		})
		if err != nil {
			return transactions, err
		}
		if used > 0 {
			transactions++
		}
		if done || used == 0 {
			return transactions, nil
		}
	}
}

// Prune deletes history older than the policy allows, at now, in batches
// of policy.BatchSize keys per transaction. It prunes whatever the policy
// names even when policy.Enabled is false (an explicit call).
func (s *BoltStore) Prune(ctx context.Context, policy RetentionPolicy, now time.Time) (PruneResult, error) {
	var result PruneResult
	batch := policy.batch()
	type job struct {
		keep  time.Duration
		count *int
		step  func(cutoff time.Time, count *int) batchStep
	}
	jobs := []job{
		{policy.Samples, &result.Samples, func(c time.Time, n *int) batchStep {
			return pruneAscending(BucketSamples, c, ascendingSeconds, n)
		}},
		{policy.SamplesHourly, &result.SamplesHourly, func(c time.Time, n *int) batchStep {
			return pruneAscending(BucketSamplesHourly, c, ascendingSeconds, n)
		}},
		{policy.Events, &result.LegacyEvents, func(c time.Time, n *int) batchStep {
			return pruneAscending(BucketEvents, c, ascendingNanos, n)
		}},
		{policy.Events, &result.Events, pruneEvents2},
		{policy.Sessions, &result.Sessions, pruneSessions},
	}
	for _, job := range jobs {
		if job.keep <= 0 {
			continue
		}
		transactions, err := s.batched(ctx, batch, job.step(now.Add(-job.keep), job.count))
		result.Transactions += transactions
		if err != nil {
			return result, err
		}
	}
	if policy.ConfigHistory > 0 {
		transactions, err := s.batched(ctx, batch, pruneKeepNewest(BucketConfigHistory, policy.ConfigHistory, &result.ConfigHistory))
		result.Transactions += transactions
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// Expire deletes expired web sessions and pending-AP dismissals. It runs
// whether or not history pruning is enabled.
func (s *BoltStore) Expire(now time.Time) (PruneResult, error) {
	var result PruneResult
	var err error
	if result.WebSessions, err = s.Sessions().DeleteExpired(now); err != nil {
		return result, err
	}
	result.PendingDismissals, err = s.PendingDismissed().Prune(now)
	return result, err
}

// RunRetention expires sessions and dismissals, and prunes history when
// policy.Enabled, once at start and then every interval (default hourly)
// until ctx ends. report, when set, receives each run's result.
func (s *BoltStore) RunRetention(ctx context.Context, policy RetentionPolicy, interval time.Duration, report func(PruneResult, error)) {
	if interval <= 0 {
		interval = DefaultRetentionInterval
	}
	run := func() {
		now := time.Now().UTC()
		result, err := s.Expire(now)
		if err == nil && policy.Enabled {
			var pruned PruneResult
			pruned, err = s.Prune(ctx, policy, now)
			result.add(pruned)
		}
		if report != nil && !errors.Is(err, context.Canceled) {
			report(result, err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// historyAPPrefixes lists every site\x00ap\x00 prefix in a bucket keyed
// site \x00 ap \x00 ....
func historyAPPrefixes(bucket *bolt.Bucket) [][]byte {
	var result [][]byte
	for _, site := range segments(bucket, nil) {
		result = append(result, segments(bucket, site)...)
	}
	return result
}

// pruneAscending deletes keys older than cutoff from a bucket keyed
// site \x00 ap \x00 <ascending time>.
func pruneAscending(name string, cutoff time.Time, at func(int) func([]byte) (time.Time, bool), count *int) batchStep {
	return func(tx *bolt.Tx, budget int) (int, bool, error) {
		bucket := tx.Bucket([]byte(name))
		if bucket == nil {
			return 0, true, nil
		}
		var doomed [][]byte
		for _, prefix := range historyAPPrefixes(bucket) {
			for s := openStream(bucket, prefix, nil, false, at(len(prefix))); s.valid() && s.when.Before(cutoff); s.advance() {
				if len(doomed) == budget {
					*count += len(doomed)
					return len(doomed), false, deleteKeys(bucket, doomed)
				}
				doomed = append(doomed, append([]byte(nil), s.key...))
			}
		}
		*count += len(doomed)
		return len(doomed), true, deleteKeys(bucket, doomed)
	}
}

// eventKeyParts splits an events2 main key (site prefix of prefixLen
// bytes) into its time bytes, AP ID and event ID.
func eventKeyParts(key []byte, prefixLen int) (inv, apID, id []byte, ok bool) {
	if len(key) < prefixLen+9 || key[prefixLen+8] != 0 {
		return nil, nil, nil, false
	}
	rest := key[prefixLen+9:]
	cut := bytes.IndexByte(rest, 0)
	if cut < 0 {
		return nil, nil, nil, false
	}
	return key[prefixLen : prefixLen+8], rest[:cut], rest[cut+1:], true
}

// deleteEvent2 deletes one event's three keys, given its main key.
func deleteEvent2(b eventBuckets, siteID string, mainKey []byte) bool {
	prefixLen := len(siteID) + 1
	inv, apID, id, ok := eventKeyParts(mainKey, prefixLen)
	if !ok {
		_ = b.main.Delete(mainKey)
		return false
	}
	apKey := append([]byte(siteID+"\x00"+string(apID)+"\x00"), inv...)
	apKey = append(apKey, append([]byte{0}, id...)...)
	_ = b.byAP.Delete(apKey)
	if bytes.Equal(b.byID.Get(id), mainKey) {
		_ = b.byID.Delete(id)
	}
	_ = b.main.Delete(mainKey)
	return true
}

// pruneEvents2 deletes events whose last_seen is before cutoff, oldest
// first, with their index entries.
func pruneEvents2(cutoff time.Time, count *int) batchStep {
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
		for _, prefix := range segments(b.main, nil) {
			for s := openStream(b.main, prefix, nil, true, inverted(len(prefix))); s.valid() && s.when.Before(cutoff); s.advance() {
				if 3*(len(doomed)+1) > budget {
					done = false
					break sites
				}
				doomed = append(doomed, doomedEvent{string(prefix[:len(prefix)-1]), append([]byte(nil), s.key...)})
			}
		}
		for _, event := range doomed {
			deleteEvent2(b, event.site, event.key)
		}
		*count += len(doomed)
		return 3 * len(doomed), done, nil
	}
}

// pruneSessions deletes client sessions that started before cutoff and
// were last active before it; active sessions are kept.
func pruneSessions(cutoff time.Time, count *int) batchStep {
	return func(tx *bolt.Tx, budget int) (int, bool, error) {
		index := tx.Bucket([]byte(BucketSessionsT))
		sessions := tx.Bucket([]byte(BucketClientSessions))
		active := tx.Bucket([]byte(BucketClientActive))
		if index == nil || sessions == nil || active == nil {
			return 0, true, errors.New("session buckets missing")
		}
		var indexKeys, sessionKeys [][]byte
		done := true
	sites:
		for _, prefix := range segments(index, nil) {
			for s := openStream(index, prefix, nil, true, inverted(len(prefix))); s.valid() && s.when.Before(cutoff); s.advance() {
				apID, mac, ok := splitSessionTKey(s.key, len(prefix))
				if !ok {
					continue
				}
				activeKey := append(append(append([]byte(nil), prefix...), apID...), 0)
				activeKey = append(activeKey, mac...)
				if bytes.Equal(active.Get(activeKey), s.value) {
					continue
				}
				if raw := sessions.Get(s.value); raw != nil {
					var session ClientSession
					if json.Unmarshal(raw, &session) == nil && !session.LastSeen.Before(cutoff) {
						continue
					}
				}
				if 2*(len(indexKeys)+1) > budget {
					done = false
					break sites
				}
				indexKeys = append(indexKeys, append([]byte(nil), s.key...))
				sessionKeys = append(sessionKeys, append([]byte(nil), s.value...))
			}
		}
		if err := deleteKeys(index, indexKeys); err != nil {
			return 0, true, err
		}
		if err := deleteKeys(sessions, sessionKeys); err != nil {
			return 0, true, err
		}
		*count += len(indexKeys)
		return 2 * len(indexKeys), done, nil
	}
}

// pruneKeepNewest keeps the newest keep keys of a bucket (keys sort by
// age, like config_history's big-endian revision) and deletes the rest.
func pruneKeepNewest(name string, keep int, count *int) batchStep {
	return func(tx *bolt.Tx, budget int) (int, bool, error) {
		bucket := tx.Bucket([]byte(name))
		if bucket == nil {
			return 0, true, nil
		}
		cursor := bucket.Cursor()
		key, _ := cursor.Last()
		for kept := 0; key != nil && kept < keep; kept++ {
			key, _ = cursor.Prev()
		}
		var doomed [][]byte
		for ; key != nil; key, _ = cursor.Prev() {
			if len(doomed) == budget {
				*count += len(doomed)
				return len(doomed), false, deleteKeys(bucket, doomed)
			}
			doomed = append(doomed, append([]byte(nil), key...))
		}
		*count += len(doomed)
		return len(doomed), true, deleteKeys(bucket, doomed)
	}
}
