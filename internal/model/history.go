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
}

type APStatusRecord struct {
	SiteID          string    `json:"site_id"`
	APID            string    `json:"ap_id"`
	ObservedAt      time.Time `json:"observed_at"`
	Connected       bool      `json:"connected"`
	Firmware        string    `json:"firmware,omitempty"`
	AppliedRevision uint64    `json:"applied_revision"`
}

type MinuteSample struct {
	SiteID          string    `json:"site_id"`
	APID            string    `json:"ap_id"`
	Minute          time.Time `json:"minute"`
	ObservedAt      time.Time `json:"observed_at"`
	Connected       bool      `json:"connected"`
	ClientCount     int       `json:"client_count"`
	AverageRSSI     int64     `json:"average_rssi"`
	TxBytes         uint64    `json:"tx_bytes"`
	RxBytes         uint64    `json:"rx_bytes"`
	UptimeSeconds   uint64    `json:"uptime_seconds"`
	AppliedRevision uint64    `json:"applied_revision"`
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
		status := APStatusRecord{
			SiteID: observation.SiteID, APID: observation.APID,
			ObservedAt: observation.At, Connected: observation.Connected,
			Firmware: observation.Firmware, AppliedRevision: observation.AppliedRevision,
		}
		if err := putJSON(tx.Bucket([]byte("ap_status")), []byte(observation.APID), status); err != nil {
			return err
		}
		sample := MinuteSample{
			SiteID: observation.SiteID, APID: observation.APID,
			Minute: observation.At.UTC().Truncate(time.Minute), ObservedAt: observation.At,
			Connected: observation.Connected, ClientCount: len(observation.Clients),
			UptimeSeconds: observation.UptimeSeconds, AppliedRevision: observation.AppliedRevision,
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

func listHistory[T any](db *bolt.DB, bucketName, siteID, apID string, limit int) ([]T, error) {
	if siteID == "" {
		return nil, errors.New("site ID required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	result := make([]T, 0)
	prefix := historyPrefix(siteID, apID)
	err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketName))
		if bucket == nil {
			return fmt.Errorf("missing %s bucket", bucketName)
		}
		cursor := bucket.Cursor()
		for key, raw := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, raw = cursor.Next() {
			var item T
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			result = append(result, item)
			if len(result) > limit {
				result = result[1:]
			}
		}
		return nil
	})
	return result, err
}

func (s *BoltStore) ListSamples(siteID, apID string, limit int) ([]MinuteSample, error) {
	return listHistory[MinuteSample](s.db, "samples", siteID, apID, limit)
}

func (s *BoltStore) ListClientSessions(siteID, apID string, limit int) ([]ClientSession, error) {
	return listHistory[ClientSession](s.db, "client_sessions", siteID, apID, limit)
}

func (s *BoltStore) ListEvents(siteID, apID string, limit int) ([]EventRecord, error) {
	return listHistory[EventRecord](s.db, "events", siteID, apID, limit)
}
