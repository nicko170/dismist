package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

// sessions_t key: site \x00 invTime(start) \x00 ap \x00 mac; the value is the
// client_sessions key. It lists sessions newest first by start, which
// client_sessions (keyed by AP and MAC) cannot (design §4.1).
func sessionTKey(siteID, apID, mac string, start time.Time) []byte {
	key := append([]byte(siteID+"\x00"), invTime(start)...)
	return append(key, []byte("\x00"+apID+"\x00"+mac)...)
}

func indexSession(tx *bolt.Tx, siteID, apID, mac string, start time.Time, sessionKey []byte) error {
	bucket := tx.Bucket([]byte(BucketSessionsT))
	if bucket == nil {
		return errors.New("sessions_t bucket missing")
	}
	return bucket.Put(sessionTKey(siteID, apID, mac, start), sessionKey)
}

// backfillSessionIndex indexes client sessions written before sessions_t
// existed. It runs once, while sessions_t is empty.
func backfillSessionIndex(tx *bolt.Tx) error {
	index := tx.Bucket([]byte(BucketSessionsT))
	sessions := tx.Bucket([]byte(BucketClientSessions))
	if index == nil || sessions == nil {
		return nil
	}
	if key, _ := index.Cursor().First(); key != nil {
		return nil
	}
	return sessions.ForEach(func(key, raw []byte) error {
		var session ClientSession
		if err := json.Unmarshal(raw, &session); err != nil {
			return nil // an unreadable record is skipped, not fatal
		}
		return index.Put(sessionTKey(session.SiteID, session.APID, session.MAC, session.ConnectedAt), append([]byte(nil), key...))
	})
}

// SessionQuery selects client sessions newest first by start.
type SessionQuery struct {
	SiteID string // required
	APID   string
	MAC    string
	// Before returns sessions that started strictly before it.
	Before time.Time
	Limit  int // default 50, max 500
}

// RecentSessions returns client sessions newest first and the start time to
// pass as Before for the next page (zero at the end).
func (s *BoltStore) RecentSessions(query SessionQuery) ([]ClientSession, time.Time, error) {
	if query.SiteID == "" {
		return nil, time.Time{}, errors.New("site ID required")
	}
	limit := clampLimit(query.Limit)
	result := make([]ClientSession, 0)
	var next time.Time
	err := s.db.View(func(tx *bolt.Tx) error {
		index := tx.Bucket([]byte(BucketSessionsT))
		sessions := tx.Bucket([]byte(BucketClientSessions))
		if index == nil || sessions == nil {
			return errors.New("session buckets missing")
		}
		prefix := []byte(query.SiteID + "\x00")
		start := prefix
		if !query.Before.IsZero() {
			start = append(append([]byte(nil), prefix...), invTime(query.Before.Add(-time.Nanosecond))...)
		}
		cursor := index.Cursor()
		for key, value := cursor.Seek(start); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
			raw := sessions.Get(value)
			if raw == nil {
				continue
			}
			var session ClientSession
			if err := json.Unmarshal(raw, &session); err != nil {
				return err
			}
			if (query.APID != "" && session.APID != query.APID) || (query.MAC != "" && session.MAC != query.MAC) {
				continue
			}
			if len(result) == limit {
				next = result[len(result)-1].ConnectedAt
				return nil
			}
			result = append(result, session)
		}
		return nil
	})
	return result, next, err
}
