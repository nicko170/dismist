package model

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

type ClientSnapshot struct {
	MAC              string `json:"mac"`
	Name             string `json:"name,omitempty"`
	IP               string `json:"ip,omitempty"`
	SSID             string `json:"ssid"`
	VLANID           uint64 `json:"vlan_id"`
	RSSI             int64  `json:"rssi"`
	TxBytes          uint64 `json:"tx_bytes"`
	RxBytes          uint64 `json:"rx_bytes"`
	ConnectedSeconds uint64 `json:"connected_seconds"`
}

type EventSnapshot struct {
	When      time.Time `json:"when"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	ClientMAC string    `json:"client_mac,omitempty"`
}

type Observation struct {
	SiteID          string           `json:"site_id"`
	APID            string           `json:"ap_id"`
	At              time.Time        `json:"at"`
	Connected       bool             `json:"connected"`
	Firmware        string           `json:"firmware,omitempty"`
	AppliedRevision uint64           `json:"applied_revision"`
	UptimeSeconds   uint64           `json:"uptime_seconds"`
	Clients         []ClientSnapshot `json:"clients,omitempty"`
	Events          []EventSnapshot  `json:"events,omitempty"`
	SkipClients     bool             `json:"-"`
	// Radios are the per-radio minute values stored on the sample (phase 1).
	Radios []RadioSample `json:"radios,omitempty"`
	// Model is the AP-reported model; "" keeps the stored one.
	Model string `json:"model,omitempty"`
	// VAPs is the AP's current field-32 VAP list, stored in ap_status as
	// APStatusStore.SetVAPs does. nil means unknown (no full frame yet):
	// the stored list is kept.
	VAPs []VAPRecord `json:"vaps,omitempty"`
}

// RadioSample is one radio's values in a minute sample (design §4.1).
type RadioSample struct {
	// Dev is the radio interface ("r0", "r1"); optional.
	Dev         string  `json:"dev,omitempty"`
	Band        string  `json:"band"`
	Channel     int     `json:"channel"`
	Width       int     `json:"width"`
	UtilAll     float64 `json:"util_all"`
	NoiseDBm    int     `json:"noise_dbm"`
	Clients     int     `json:"clients"`
	MaxPowerDBm int     `json:"max_power_dbm"`
}

type MinuteSample struct {
	SiteID          string        `json:"site_id"`
	APID            string        `json:"ap_id"`
	Minute          time.Time     `json:"minute"`
	ObservedAt      time.Time     `json:"observed_at"`
	Connected       bool          `json:"connected"`
	ClientCount     int           `json:"client_count"`
	AverageRSSI     int64         `json:"average_rssi"`
	TxBytes         uint64        `json:"tx_bytes"`
	RxBytes         uint64        `json:"rx_bytes"`
	UptimeSeconds   uint64        `json:"uptime_seconds"`
	AppliedRevision uint64        `json:"applied_revision"`
	Radios          []RadioSample `json:"radios,omitempty"`
}

type ClientSession struct {
	SiteID         string    `json:"site_id"`
	APID           string    `json:"ap_id"`
	MAC            string    `json:"mac"`
	Name           string    `json:"name,omitempty"`
	IP             string    `json:"ip,omitempty"`
	SSID           string    `json:"ssid"`
	VLANID         uint64    `json:"vlan_id"`
	ConnectedAt    time.Time `json:"connected_at"`
	LastSeen       time.Time `json:"last_seen"`
	DisconnectedAt time.Time `json:"disconnected_at,omitzero"`
	EndReason      string    `json:"end_reason,omitempty"`
	RSSI           int64     `json:"rssi"`
	TxBytes        uint64    `json:"tx_bytes"`
	RxBytes        uint64    `json:"rx_bytes"`
}

type EventRecord struct {
	SiteID    string    `json:"site_id"`
	APID      string    `json:"ap_id"`
	When      time.Time `json:"when"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail,omitempty"`
	ClientMAC string    `json:"client_mac,omitempty"`
}

func (s *BoltStore) RecordObservation(observation Observation) error {
	if observation.SiteID == "" || observation.APID == "" || observation.At.IsZero() {
		return errors.New("observation needs site, AP, and time")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	registered := false
	for _, ap := range s.state.APs {
		if ap.ID == observation.APID && ap.SiteID == observation.SiteID {
			registered = true
			break
		}
	}
	if !registered {
		return errors.New("AP is not assigned to observation site")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := recordStatus(tx, observation); err != nil {
			return err
		}
		sample := MinuteSample{
			SiteID: observation.SiteID, APID: observation.APID,
			Minute: observation.At.UTC().Truncate(time.Minute), ObservedAt: observation.At,
			Connected: observation.Connected, ClientCount: len(observation.Clients),
			UptimeSeconds: observation.UptimeSeconds, AppliedRevision: observation.AppliedRevision,
			Radios: observation.Radios,
		}
		if observation.SkipClients {
			sample.ClientCount = -1
		}
		for _, client := range observation.Clients {
			sample.TxBytes += client.TxBytes
			sample.RxBytes += client.RxBytes
			sample.AverageRSSI += client.RSSI
		}
		if len(observation.Clients) != 0 {
			sample.AverageRSSI /= int64(len(observation.Clients))
		}
		key := append(historyPrefix(observation.SiteID, observation.APID), timeKey(sample.Minute.Unix())...)
		if err := putJSON(tx.Bucket([]byte("samples")), key, sample); err != nil {
			return err
		}
		if err := recordClients(tx, observation); err != nil {
			return err
		}
		return recordEvents(tx, observation)
	})
}

// recordStatus merges the observation into the AP's ap_status record,
// keeping the fields other writers own (hello, reboots, tunnel address).
func recordStatus(tx *bolt.Tx, observation Observation) error {
	bucket, err := statusBucket(tx)
	if err != nil {
		return err
	}
	status, _, err := readStatus(bucket, observation.APID)
	if err != nil {
		status = APStatusRecord{}
	}
	status.SiteID, status.APID = observation.SiteID, observation.APID
	status.ObservedAt, status.Connected = observation.At, observation.Connected
	status.Firmware, status.AppliedRevision = observation.Firmware, observation.AppliedRevision
	if observation.Model != "" {
		status.Model = observation.Model
	}
	if observation.VAPs != nil {
		next := normaliseVAPs(observation.VAPs)
		at := observation.At.UTC()
		if status.VAPs == nil || !sameVAPs(status.VAPs, next) {
			status.VAPs, status.VAPsChangedAt = next, at
		}
		status.VAPsSeenAt = at
	}
	return putJSON(bucket, []byte(observation.APID), status)
}

func recordClients(tx *bolt.Tx, observation Observation) error {
	if observation.SkipClients {
		return nil
	}
	active := tx.Bucket([]byte("client_active"))
	sessions := tx.Bucket([]byte("client_sessions"))
	prefix := historyPrefix(observation.SiteID, observation.APID)
	seen := make(map[string]bool, len(observation.Clients))
	for _, client := range observation.Clients {
		mac := strings.ToLower(strings.ReplaceAll(client.MAC, "-", ":"))
		if mac == "" {
			continue
		}
		seen[mac] = true
		activeKey := append(append([]byte(nil), prefix...), mac...)
		sessionKey := active.Get(activeKey)
		var session ClientSession
		if sessionKey == nil {
			start := observation.At.Add(-time.Duration(client.ConnectedSeconds) * time.Second)
			session = ClientSession{
				SiteID: observation.SiteID, APID: observation.APID,
				MAC: mac, ConnectedAt: start,
			}
			sessionKey = append(append(append([]byte(nil), prefix...), []byte(mac)...), 0)
			sessionKey = append(sessionKey, timeKey(start.UnixNano())...)
			if err := active.Put(activeKey, sessionKey); err != nil {
				return err
			}
			if err := indexSession(tx, observation.SiteID, observation.APID, mac, start, sessionKey); err != nil {
				return err
			}
		} else if err := json.Unmarshal(sessions.Get(sessionKey), &session); err != nil {
			return err
		}
		session.Name = client.Name
		session.IP = client.IP
		session.SSID = client.SSID
		session.VLANID = client.VLANID
		session.LastSeen = observation.At
		session.RSSI = client.RSSI
		session.TxBytes = client.TxBytes
		session.RxBytes = client.RxBytes
		if err := putJSON(sessions, sessionKey, session); err != nil {
			return err
		}
	}
	cursor := active.Cursor()
	for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
		mac := string(key[len(prefix):])
		if seen[mac] {
			continue
		}
		var session ClientSession
		if err := json.Unmarshal(sessions.Get(value), &session); err != nil {
			return err
		}
		session.DisconnectedAt = observation.At
		session.EndReason = "not observed"
		if !observation.Connected {
			session.EndReason = "AP connection lost"
		}
		if err := putJSON(sessions, value, session); err != nil {
			return err
		}
		if err := cursor.Delete(); err != nil {
			return err
		}
	}
	return nil
}

func closeActiveSessions(tx *bolt.Tx, siteID, apID string, at time.Time, reason string) error {
	active := tx.Bucket([]byte("client_active"))
	sessions := tx.Bucket([]byte("client_sessions"))
	if active == nil || sessions == nil {
		return errors.New("client session bucket missing")
	}
	prefix := historyPrefix(siteID, apID)
	cursor := active.Cursor()
	for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
		var session ClientSession
		if err := json.Unmarshal(sessions.Get(value), &session); err != nil {
			return err
		}
		session.DisconnectedAt = at
		session.EndReason = reason
		if err := putJSON(sessions, value, session); err != nil {
			return err
		}
		if err := cursor.Delete(); err != nil {
			return err
		}
	}
	return nil
}

func recordEvents(tx *bolt.Tx, observation Observation) error {
	bucket := tx.Bucket([]byte("events"))
	for _, event := range observation.Events {
		when := event.When
		if when.IsZero() {
			when = observation.At
		}
		record := EventRecord{
			SiteID: observation.SiteID, APID: observation.APID, When: when,
			Kind: event.Kind, Title: event.Title, Detail: event.Detail,
			ClientMAC: event.ClientMAC,
		}
		hash := sha256.Sum256([]byte(event.Kind + "\x00" + event.Title + "\x00" + event.Detail + "\x00" + event.ClientMAC))
		key := append(historyPrefix(observation.SiteID, observation.APID), timeKey(when.UnixNano())...)
		key = append(key, hash[:8]...)
		if err := putJSON(bucket, key, record); err != nil {
			return err
		}
	}
	return nil
}

func putJSON(bucket *bolt.Bucket, key []byte, value any) error {
	if bucket == nil {
		return errors.New("history bucket missing")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put(key, raw)
}

func historyPrefix(siteID, apID string) []byte {
	if apID == "" {
		return []byte(siteID + "\x00")
	}
	return []byte(siteID + "\x00" + apID + "\x00")
}

func timeKey(value int64) []byte {
	var result [8]byte
	binary.BigEndian.PutUint64(result[:], uint64(value))
	return result[:]
}

// listLegacy returns the newest limit records under siteID (and apID), in
// ascending time order, for buckets keyed site \x00 ap \x00 <8-byte
// ascending time> (samples: seconds, legacy events: nanoseconds). A
// site-level query merges the APs by time, so with several APs it returns
// the site's newest records, not the last AP's.
func listLegacy[T any](db *bolt.DB, bucketName, siteID, apID string, limit int, at func(int) func([]byte) (time.Time, bool)) ([]T, error) {
	if siteID == "" {
		return nil, errors.New("site ID required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	result := make([]T, 0)
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return fmt.Errorf("missing %s bucket", bucketName)
		}
		prefixes := [][]byte{historyPrefix(siteID, apID)}
		if apID == "" {
			prefixes = segments(bucket, historyPrefix(siteID, ""))
		}
		streams := make([]*stream, len(prefixes))
		for index, prefix := range prefixes {
			streams[index] = openStream(bucket, prefix, nil, true, at(len(prefix)))
		}
		for len(result) < limit {
			pick := pickNewest(streams)
			if pick < 0 {
				break
			}
			var item T
			if err := json.Unmarshal(streams[pick].value, &item); err != nil {
				return err
			}
			result = append(result, item)
			streams[pick].advance()
		}
		return nil
	})
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, err
}

// ListSamples returns the newest limit minute samples of a site (all APs
// merged by time) or one AP, oldest first.
func (s *BoltStore) ListSamples(siteID, apID string, limit int) ([]MinuteSample, error) {
	return listLegacy[MinuteSample](s.db, BucketSamples, siteID, apID, limit, ascendingSeconds)
}

// ListClientSessions returns the newest limit client sessions (by start)
// of a site or one AP, oldest first. It reads the sessions_t index.
func (s *BoltStore) ListClientSessions(siteID, apID string, limit int) ([]ClientSession, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	sessions, err := s.recentSessions(SessionQuery{SiteID: siteID, APID: apID}, limit)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(sessions)-1; i < j; i, j = i+1, j-1 {
		sessions[i], sessions[j] = sessions[j], sessions[i]
	}
	return sessions, nil
}

// ListEvents returns the newest limit records of the legacy events bucket
// (readable for one release, design §4.1), oldest first.
func (s *BoltStore) ListEvents(siteID, apID string, limit int) ([]EventRecord, error) {
	return listLegacy[EventRecord](s.db, BucketEvents, siteID, apID, limit, ascendingNanos)
}
