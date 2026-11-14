package model

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Neighbour scans (design §5.10, phase 3). The latest scan per AP and band
// is kept in the scans bucket, keyed by AP ID and overwritten: at most
// MaxScanNeighbours neighbours per band, strongest first, and at most
// MaxScanBytes per AP. BSSIDs of the site's own APs are tagged Own and
// become AP-to-AP visibility edges for the planner.

// Scan limits.
const (
	MaxScanNeighbours = 200
	MaxScanBytes      = 64 << 10
)

// ScanNeighbour is one BSSID heard in a scan.
type ScanNeighbour struct {
	BSSID        string   `json:"bssid"`
	SSID         string   `json:"ssid,omitempty"`
	Channel      int      `json:"channel"`
	Width        int      `json:"width,omitempty"`
	RSSI         int      `json:"rssi"`
	StationCount *int     `json:"station_count,omitempty"` // QBSS
	Util         *float64 `json:"util,omitempty"`          // QBSS channel utilisation, 0 to 1
	// Own is set when the BSSID belongs to an AP of the site; OwnAPID
	// names it.
	Own     bool   `json:"own,omitempty"`
	OwnAPID string `json:"own_ap_id,omitempty"`
}

// ChannelStat is one channel's measured load (field 69.9).
type ChannelStat struct {
	Channel  int     `json:"channel"`
	Util     float64 `json:"util"`      // 0 to 1
	NoiseDBm int     `json:"noise_dbm"` // noise floor
}

// BandScan is the latest scan of one band.
type BandScan struct {
	At         time.Time       `json:"at"`
	Neighbours []ScanNeighbour `json:"neighbours,omitempty"`
	Channels   []ChannelStat   `json:"channels,omitempty"`
}

// APScan is the scans record of one AP.
type APScan struct {
	APID   string              `json:"ap_id"`
	SiteID string              `json:"site_id"`
	Bands  map[string]BandScan `json:"bands"` // "2.4", "5"
}

// ScanStore reads and writes the scans bucket.
type ScanStore struct{ db *bolt.DB }

// Scans returns the scan store.
func (s *BoltStore) Scans() *ScanStore { return &ScanStore{db: s.db} }

// trimScan sorts neighbours strongest first (then by BSSID) and applies the
// limits, dropping the weakest neighbours of the largest band until the
// record fits MaxScanBytes.
func trimScan(record *APScan) ([]byte, error) {
	for band, scan := range record.Bands {
		sort.SliceStable(scan.Neighbours, func(i, j int) bool {
			a, b := scan.Neighbours[i], scan.Neighbours[j]
			if a.RSSI != b.RSSI {
				return a.RSSI > b.RSSI
			}
			return a.BSSID < b.BSSID
		})
		if len(scan.Neighbours) > MaxScanNeighbours {
			scan.Neighbours = scan.Neighbours[:MaxScanNeighbours]
		}
		record.Bands[band] = scan
	}
	for {
		raw, err := json.Marshal(record)
		if err != nil || len(raw) <= MaxScanBytes {
			return raw, err
		}
		largest := ""
		for band, scan := range record.Bands {
			if largest == "" || len(scan.Neighbours) > len(record.Bands[largest].Neighbours) {
				largest = band
			}
		}
		scan := record.Bands[largest]
		if len(scan.Neighbours) == 0 {
			return nil, errors.New("scan record too large")
		}
		scan.Neighbours = scan.Neighbours[:len(scan.Neighbours)-len(scan.Neighbours)/4-1]
		record.Bands[largest] = scan
	}
}

// Put replaces the scan of one band of an AP (other bands are kept).
func (c *ScanStore) Put(siteID, apID, band string, scan BandScan) error {
	if apID == "" || (band != "2.4" && band != "5") {
		return errors.New("scan needs an AP ID and band 2.4 or 5")
	}
	return c.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketScans))
		record := APScan{APID: apID, SiteID: siteID, Bands: map[string]BandScan{}}
		if raw := bucket.Get([]byte(apID)); raw != nil {
			var existing APScan
			if err := json.Unmarshal(raw, &existing); err == nil && existing.SiteID == siteID && existing.Bands != nil {
				record.Bands = existing.Bands
			}
		}
		scan.Neighbours = append([]ScanNeighbour(nil), scan.Neighbours...)
		for index := range scan.Neighbours {
			scan.Neighbours[index].BSSID = strings.ToLower(scan.Neighbours[index].BSSID)
		}
		record.Bands[band] = scan
		raw, err := trimScan(&record)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(apID), raw)
	})
}

// Get returns the scans of one AP.
func (c *ScanStore) Get(apID string) (APScan, bool, error) {
	var record APScan
	found := false
	err := c.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(BucketScans)).Get([]byte(apID))
		if raw == nil {
			return nil
		}
		found = true
		return json.Unmarshal(raw, &record)
	})
	return record, found, err
}

// Site returns the scans of every AP of a site, sorted by AP ID.
func (c *ScanStore) Site(siteID string) ([]APScan, error) {
	var result []APScan
	err := c.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(BucketScans)).ForEach(func(_, raw []byte) error {
			var record APScan
			if err := json.Unmarshal(raw, &record); err != nil {
				return nil // a damaged record is skipped, not fatal
			}
			if record.SiteID == siteID {
				result = append(result, record)
			}
			return nil
		})
	})
	sort.Slice(result, func(i, j int) bool { return result[i].APID < result[j].APID })
	return result, err
}

// Delete removes an AP's scans (AP removed or moved).
func (c *ScanStore) Delete(apID string) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(BucketScans)).Delete([]byte(apID))
	})
}
