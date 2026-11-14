package model

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Hourly rollups (design §4.1, phase 3): samples_hourly holds one record per
// AP and hour, keyed like samples (site \x00 ap \x00 <hour start, unix
// seconds>). Minute samples are kept 30 days, rollups 400 days.

// HourlyRadio is one band's hourly figures.
type HourlyRadio struct {
	Band       string  `json:"band"`
	Channel    int     `json:"channel"` // the last minute's
	Width      int     `json:"width"`
	UtilAvg    float64 `json:"util_avg"`
	UtilMax    float64 `json:"util_max"`
	ClientsAvg float64 `json:"clients_avg"`
	ClientsMax int     `json:"clients_max"`
	NoiseAvg   float64 `json:"noise_avg_dbm"`
	MaxPower   int     `json:"max_power_dbm"`
}

// HourlySample is one AP's rollup of the minute samples of one hour.
type HourlySample struct {
	SiteID           string        `json:"site_id"`
	APID             string        `json:"ap_id"`
	Hour             time.Time     `json:"hour"`
	Minutes          int           `json:"minutes"` // minute samples rolled up
	ConnectedMinutes int           `json:"connected_minutes"`
	ClientsAvg       float64       `json:"clients_avg"`
	ClientsMax       int           `json:"clients_max"`
	AverageRSSI      int64         `json:"average_rssi"`
	TxBytesMax       uint64        `json:"tx_bytes_max"`
	RxBytesMax       uint64        `json:"rx_bytes_max"`
	UptimeSeconds    uint64        `json:"uptime_seconds"` // the last minute's
	Radios           []HourlyRadio `json:"radios,omitempty"`
}

// rollupHour folds the minute samples of one hour.
func rollupHour(siteID, apID string, hour time.Time, samples []MinuteSample) HourlySample {
	result := HourlySample{SiteID: siteID, APID: apID, Hour: hour, Minutes: len(samples)}
	type acc struct {
		radio                HourlyRadio
		util, clients, noise float64
		count                int
	}
	radios := map[string]*acc{}
	var clientMinutes int
	var clientSum, rssiSum int64
	var rssiCount int64
	for _, sample := range samples {
		if sample.Connected {
			result.ConnectedMinutes++
		}
		if sample.ClientCount >= 0 {
			clientMinutes++
			clientSum += int64(sample.ClientCount)
			if sample.ClientCount > result.ClientsMax {
				result.ClientsMax = sample.ClientCount
			}
			if sample.ClientCount > 0 {
				rssiSum += sample.AverageRSSI
				rssiCount++
			}
		}
		if sample.TxBytes > result.TxBytesMax {
			result.TxBytesMax = sample.TxBytes
		}
		if sample.RxBytes > result.RxBytesMax {
			result.RxBytesMax = sample.RxBytes
		}
		result.UptimeSeconds = sample.UptimeSeconds
		for _, radio := range sample.Radios {
			a := radios[radio.Band]
			if a == nil {
				a = &acc{radio: HourlyRadio{Band: radio.Band}}
				radios[radio.Band] = a
			}
			a.count++
			a.util += radio.UtilAll
			a.clients += float64(radio.Clients)
			a.noise += float64(radio.NoiseDBm)
			a.radio.Channel, a.radio.Width = radio.Channel, radio.Width
			if radio.UtilAll > a.radio.UtilMax {
				a.radio.UtilMax = radio.UtilAll
			}
			if radio.Clients > a.radio.ClientsMax {
				a.radio.ClientsMax = radio.Clients
			}
			if radio.MaxPowerDBm > a.radio.MaxPower {
				a.radio.MaxPower = radio.MaxPowerDBm
			}
		}
	}
	if clientMinutes > 0 {
		result.ClientsAvg = float64(clientSum) / float64(clientMinutes)
	}
	if rssiCount > 0 {
		result.AverageRSSI = rssiSum / rssiCount
	}
	for _, a := range radios {
		a.radio.UtilAvg = a.util / float64(a.count)
		a.radio.ClientsAvg = a.clients / float64(a.count)
		a.radio.NoiseAvg = a.noise / float64(a.count)
		result.Radios = append(result.Radios, a.radio)
	}
	sort.Slice(result.Radios, func(i, j int) bool { return result.Radios[i].Band < result.Radios[j].Band })
	return result
}

// RollupResult counts what RollupSamples wrote.
type RollupResult struct {
	Hours        int `json:"hours"`
	Transactions int `json:"transactions"`
}

// RollupSamples writes the hourly rollup of every complete hour (before
// now's hour) that has minute samples and no rollup yet, for every AP.
// Each transaction rolls up at most batch hours (default 500).
func (s *BoltStore) RollupSamples(ctx context.Context, now time.Time, batch int) (RollupResult, error) {
	if batch <= 0 {
		batch = 500
	}
	limit := now.UTC().Truncate(time.Hour)
	var result RollupResult
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		written := 0
		err := s.db.Update(func(tx *bolt.Tx) error {
			samples := tx.Bucket([]byte(BucketSamples))
			hourly := tx.Bucket([]byte(BucketSamplesHourly))
			if samples == nil || hourly == nil {
				return errors.New("samples buckets missing")
			}
			for _, prefix := range historyAPPrefixes(samples) {
				start := time.Time{}
				// Resume after the newest rollup of this AP.
				cursor := hourly.Cursor()
				key, _ := cursor.Seek(prefixEnd(prefix))
				if key == nil {
					key, _ = cursor.Last()
				} else {
					key, _ = cursor.Prev()
				}
				if key != nil && bytes.HasPrefix(key, prefix) && len(key) == len(prefix)+8 {
					start = time.Unix(int64(binary.BigEndian.Uint64(key[len(prefix):])), 0).UTC().Add(time.Hour)
				}
				parts := bytes.SplitN(prefix, []byte{0}, 3)
				siteID, apID := string(parts[0]), string(parts[1])
				sampleCursor := samples.Cursor()
				seek := prefix
				if !start.IsZero() {
					seek = append(append([]byte(nil), prefix...), timeKey(start.Unix())...)
				}
				var bucketHour time.Time
				var pending []MinuteSample
				flush := func() error {
					if len(pending) == 0 {
						return nil
					}
					rolled := rollupHour(siteID, apID, bucketHour, pending)
					pending = nil
					written++
					return putJSON(hourly, append(append([]byte(nil), prefix...), timeKey(bucketHour.Unix())...), rolled)
				}
				for key, raw := sampleCursor.Seek(seek); key != nil && bytes.HasPrefix(key, prefix) && written < batch; key, raw = sampleCursor.Next() {
					if len(key) != len(prefix)+8 {
						continue
					}
					minute := time.Unix(int64(binary.BigEndian.Uint64(key[len(prefix):])), 0).UTC()
					hour := minute.Truncate(time.Hour)
					if !hour.Before(limit) {
						break
					}
					if !hour.Equal(bucketHour) {
						if err := flush(); err != nil {
							return err
						}
						bucketHour = hour
					}
					var sample MinuteSample
					if err := json.Unmarshal(raw, &sample); err != nil {
						continue
					}
					pending = append(pending, sample)
				}
				if written < batch {
					if err := flush(); err != nil {
						return err
					}
				}
				if written >= batch {
					return nil
				}
			}
			return nil
		})
		result.Transactions++
		result.Hours += written
		if err != nil {
			return result, err
		}
		if written < batch {
			return result, nil
		}
	}
}

// HourlyQuery selects hourly rollups.
type HourlyQuery struct {
	SiteID       string // required
	APID         string // "" = every AP of the site
	Since, Until time.Time
}

// HourlySamples returns the matching rollups in ascending hour order (then
// AP ID).
func (s *BoltStore) HourlySamples(query HourlyQuery) ([]HourlySample, error) {
	if query.SiteID == "" {
		return nil, errors.New("site ID required")
	}
	var result []HourlySample
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BucketSamplesHourly))
		prefixes := [][]byte{historyPrefix(query.SiteID, query.APID)}
		if query.APID == "" {
			prefixes = segments(bucket, historyPrefix(query.SiteID, ""))
		}
		for _, prefix := range prefixes {
			cursor := bucket.Cursor()
			seek := prefix
			if !query.Since.IsZero() {
				seek = append(append([]byte(nil), prefix...), timeKey(query.Since.Truncate(time.Hour).Unix())...)
			}
			for key, raw := cursor.Seek(seek); key != nil && bytes.HasPrefix(key, prefix); key, raw = cursor.Next() {
				var sample HourlySample
				if err := json.Unmarshal(raw, &sample); err != nil {
					continue
				}
				if !query.Until.IsZero() && !sample.Hour.Before(query.Until) {
					break
				}
				result = append(result, sample)
			}
		}
		return nil
	})
	sort.SliceStable(result, func(i, j int) bool {
		if !result[i].Hour.Equal(result[j].Hour) {
			return result[i].Hour.Before(result[j].Hour)
		}
		return result[i].APID < result[j].APID
	})
	return result, err
}
