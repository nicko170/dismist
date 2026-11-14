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

// splitSessionTKey returns the AP and MAC of a sessions_t key whose site
// prefix is prefixLen bytes long.
func splitSessionTKey(key []byte, prefixLen int) (apID, mac []byte, ok bool) {
	if len(key) < prefixLen+9 || key[prefixLen+8] != 0 {
		return nil, nil, false
	}
	rest := key[prefixLen+9:]
	cut := bytes.IndexByte(rest, 0)
	if cut < 0 {
		return nil, nil, false
	}
	return rest[:cut], rest[cut+1:], true
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

// SessionLookback bounds how far before ActiveSince a query looks for
// sessions that started earlier and were still active at ActiveSince.
const SessionLookback = 7 * 24 * time.Hour

// SessionQuery selects client sessions newest first by start.
type SessionQuery struct {
	// SiteID selects one site; "" searches every site (merged by start
	// time), as the site-less GET /api/clients/$mac needs.
	SiteID string
	APID   string
	// MAC matches the client (any case, ":" or "-").
	MAC string
	// Before returns sessions that started strictly before it (the
	// next_before of the previous page).
	Before time.Time
	// ActiveSince returns sessions still active at or after it: last seen
	// at or after it, or not yet ended. Sessions that started more than
	// SessionLookback before it are not searched.
	ActiveSince time.Time
	// Ended returns only sessions that have ended ("recent" view).
	Ended bool
	// Limit is the page size: default 50, max 500. As for events, a page
	// can hold more when sessions at its last start time share it.
	Limit int
}

// RecentSessions returns client sessions newest first and the start time to
// pass as Before for the next page (zero at the end).
func (s *BoltStore) RecentSessions(query SessionQuery) ([]ClientSession, time.Time, error) {
	page := pager[ClientSession]{limit: clampLimit(query.Limit), items: make([]ClientSession, 0), at: func(c ClientSession) time.Time { return c.ConnectedAt }}
	err := s.scanSessions(query, page.add)
	return page.items, page.next, err
}

// LatestSession returns the most recent session of a client in any site;
// ErrNotFound when there is none.
func (s *BoltStore) LatestSession(mac string) (ClientSession, error) {
	sessions, _, err := s.RecentSessions(SessionQuery{MAC: mac, Limit: 1})
	if err != nil {
		return ClientSession{}, err
	}
	if len(sessions) == 0 {
		return ClientSession{}, ErrNotFound
	}
	return sessions[0], nil
}

// recentSessions returns up to limit sessions newest first (no tie
// extension, for the legacy list).
func (s *BoltStore) recentSessions(query SessionQuery, limit int) ([]ClientSession, error) {
	if query.SiteID == "" {
		return nil, errors.New("site ID required")
	}
	result := make([]ClientSession, 0)
	err := s.scanSessions(query, func(session ClientSession) bool {
		result = append(result, session)
		return len(result) < limit
	})
	return result, err
}

// scanSessions offers the matching sessions newest first to visit until it
// returns false.
func (s *BoltStore) scanSessions(query SessionQuery, visit func(ClientSession) bool) error {
	mac := []byte(NormaliseMAC(query.MAC))
	return s.db.View(func(tx *bolt.Tx) error {
		index := tx.Bucket([]byte(BucketSessionsT))
		sessions := tx.Bucket([]byte(BucketClientSessions))
		active := tx.Bucket([]byte(BucketClientActive))
		if index == nil || sessions == nil || active == nil {
			return errors.New("session buckets missing")
		}
		prefixes := [][]byte{[]byte(query.SiteID + "\x00")}
		if query.SiteID == "" {
			prefixes = segments(index, nil)
		}
		streams := make([]*stream, len(prefixes))
		for i, prefix := range prefixes {
			var start []byte
			if !query.Before.IsZero() {
				start = append(append([]byte(nil), prefix...), invTime(query.Before.Add(-time.Nanosecond))...)
			}
			streams[i] = openStream(index, prefix, start, false, inverted(len(prefix)))
		}
		for {
			pick := pickNewest(streams)
			if pick < 0 {
				return nil
			}
			current := streams[pick]
			if !query.ActiveSince.IsZero() && current.when.Before(query.ActiveSince.Add(-SessionLookback)) {
				return nil
			}
			key, value := current.key, current.value
			apID, keyMAC, ok := splitSessionTKey(key, len(current.prefix))
			current.advance()
			if !ok || (query.APID != "" && string(apID) != query.APID) || (len(mac) != 0 && !bytes.Equal(keyMAC, mac)) {
				continue
			}
			raw := sessions.Get(value)
			if raw == nil {
				continue
			}
			var session ClientSession
			if err := json.Unmarshal(raw, &session); err != nil {
				return err
			}
			if query.Ended && session.DisconnectedAt.IsZero() {
				continue
			}
			if !query.ActiveSince.IsZero() && session.LastSeen.Before(query.ActiveSince) && !session.DisconnectedAt.IsZero() {
				continue
			}
			if !visit(session) {
				return nil
			}
		}
	})
}
