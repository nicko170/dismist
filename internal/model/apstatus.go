package model

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
)

// APStatusRecord is the ap_status bucket value: the last-known facts about
// an AP, kept across restarts. The observer writes the first block every
// 10 s; the phase-1 fields below are written by the protocol code through
// APStatusStore and survive the observer's writes.
type APStatusRecord struct {
	SiteID          string    `json:"site_id"`
	APID            string    `json:"ap_id"`
	ObservedAt      time.Time `json:"observed_at"`
	Connected       bool      `json:"connected"`
	Firmware        string    `json:"firmware,omitempty"`
	AppliedRevision uint64    `json:"applied_revision"`

	// Model is the AP-reported model (hello).
	Model string `json:"model,omitempty"`
	// Hello holds the extra ep-auth hello fields of the last connection
	// (design §5.4), HelloAt when that hello arrived (controller time).
	Hello   *HelloFacts `json:"hello,omitempty"`
	HelloAt time.Time   `json:"hello_at,omitzero"`
	// VAPs is the last-known field-32 VAP list, sorted by Dev (design §8.4
	// B; the v2 migration's BSSID-index input). nil means never reported.
	VAPs []VAPRecord `json:"vaps,omitempty"`
	// VAPsChangedAt is when the VAP list last changed, VAPsSeenAt when it
	// was last confirmed by a frame (refreshed at most every
	// VAPRefreshInterval while unchanged).
	VAPsChangedAt time.Time `json:"vaps_changed_at,omitzero"`
	VAPsSeenAt    time.Time `json:"vaps_seen_at,omitzero"`
	// TunnelIPv6 is the address from the AP's last ep-ip-register.
	TunnelIPv6   string    `json:"tunnel_ipv6,omitempty"`
	TunnelIPv6At time.Time `json:"tunnel_ipv6_at,omitzero"`
	// ConfigRequest holds the fields of the AP's last ep-config request
	// (design §5.5).
	ConfigRequest *ConfigRequestFacts `json:"config_request,omitempty"`
	// Reboots are the detected reboots, newest first, at most
	// MaxRebootRecords.
	Reboots []RebootRecord `json:"reboots,omitempty"`
}

// HelloFacts are the ep-auth hello fields authentication does not use
// (design §5.4). It mirrors controller.HelloExtras (model must not import
// controller); zero means absent.
type HelloFacts struct {
	SKU              string    `json:"sku,omitempty"`
	SerialNo         string    `json:"serial,omitempty"`
	Developer        bool      `json:"developer,omitempty"`
	Uptime           int64     `json:"uptime_seconds,omitempty"`
	ConnectUptime    int64     `json:"connect_uptime_seconds,omitempty"`
	BootCount        uint64    `json:"boot_count,omitempty"`
	LastRebootTime   time.Time `json:"last_reboot_time,omitzero"`
	LastRebootReason string    `json:"last_reboot_reason,omitempty"`
	LastBootState    string    `json:"last_boot_state,omitempty"`
	LocalIP          string    `json:"local_ip,omitempty"`
	TimeoutSec       int64     `json:"timeout_sec,omitempty"`
}

// VAPRecord is one VAP from field 32 (stats.WLANStats).
type VAPRecord struct {
	// Dev is the interface name, for example "wl1.2" (32.4); its index
	// after the dot is the BSSID index.
	Dev   string `json:"dev"`
	BSSID string `json:"bssid"` // 32.3, lower-case with ":"
	SSID  string `json:"ssid,omitempty"`
	// WLANID is 32.1, the dismist network ID the AP was configured with.
	WLANID     string `json:"wlan_id,omitempty"`
	RadioIndex int    `json:"radio_index"` // 32.2
}

// ConfigRequestFacts are the fields of an AP's ep-config request (design
// §5.5).
type ConfigRequestFacts struct {
	At                     time.Time `json:"at"`
	AppliedConfigTimestamp float64   `json:"applied_config_timestamp,omitempty"`
	RecoveredFromBadConfig bool      `json:"recovered_from_bad_config,omitempty"`
	Capabilities           []string  `json:"capabilities,omitempty"`
	MfgCountryCode         string    `json:"mfg_country_code,omitempty"`
}

// RebootRecord is one detected AP reboot (design §5.4 "Reboot vs
// reconnect").
type RebootRecord struct {
	// DetectedAt is the controller time of the hello (or frame) that
	// revealed the reboot.
	DetectedAt time.Time `json:"detected_at"`
	// Evidence is "boot_count" (BootCount went up) or "uptime" (uptime
	// below the previous uptime plus the time since).
	Evidence          string `json:"evidence"`
	BootCount         uint64 `json:"boot_count,omitempty"`
	PreviousBootCount uint64 `json:"previous_boot_count,omitempty"`
	// Uptime is the AP uptime in seconds at DetectedAt; DetectedAt minus
	// Uptime estimates the boot time on the controller's clock.
	Uptime int64 `json:"uptime_seconds,omitempty"`
	// LastRebootTime, Reason and BootState are what the AP reported
	// (hello LastRebootTime, LastRebootReason, LastBootState).
	LastRebootTime time.Time `json:"last_reboot_time,omitzero"`
	Reason         string    `json:"reason,omitempty"`
	BootState      string    `json:"boot_state,omitempty"`
	// EventID is the events2 "reboot" event, when one was stored.
	EventID string `json:"event_id,omitempty"`
}

// BootedAt estimates the boot time from DetectedAt and Uptime (zero when
// the uptime is unknown).
func (r RebootRecord) BootedAt() time.Time {
	if r.Uptime <= 0 || r.DetectedAt.IsZero() {
		return time.Time{}
	}
	return r.DetectedAt.Add(-time.Duration(r.Uptime) * time.Second)
}

// MaxRebootRecords bounds APStatusRecord.Reboots.
const MaxRebootRecords = 50

// VAPRefreshInterval is how often an unchanged VAP list rewrites its
// VAPsSeenAt.
const VAPRefreshInterval = 10 * time.Minute

// ErrUnknownAP means the AP is not registered (or not in the given site).
var ErrUnknownAP = errors.New("AP is not registered")

// APStatusStore reads and writes the ap_status bucket.
type APStatusStore struct{ store *BoltStore }

// APStatus returns the ap_status store.
func (s *BoltStore) APStatus() *APStatusStore { return &APStatusStore{store: s} }

func statusBucket(tx *bolt.Tx) (*bolt.Bucket, error) {
	bucket := tx.Bucket([]byte(BucketAPStatus))
	if bucket == nil {
		return nil, errors.New("ap_status bucket missing")
	}
	return bucket, nil
}

func readStatus(bucket *bolt.Bucket, apID string) (APStatusRecord, bool, error) {
	raw := bucket.Get([]byte(apID))
	if raw == nil {
		return APStatusRecord{APID: apID}, false, nil
	}
	var record APStatusRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return APStatusRecord{APID: apID}, false, err
	}
	return record, true, nil
}

// Get returns an AP's last-known status; ErrNotFound when none is stored.
func (a *APStatusStore) Get(apID string) (APStatusRecord, error) {
	var record APStatusRecord
	err := a.store.db.View(func(tx *bolt.Tx) error {
		bucket, err := statusBucket(tx)
		if err != nil {
			return err
		}
		var found bool
		record, found, err = readStatus(bucket, apID)
		if err == nil && !found {
			return ErrNotFound
		}
		return err
	})
	return record, err
}

// All returns every stored status keyed by AP ID. Unreadable records are
// skipped.
func (a *APStatusStore) All() (map[string]APStatusRecord, error) {
	result := map[string]APStatusRecord{}
	err := a.store.db.View(func(tx *bolt.Tx) error {
		bucket, err := statusBucket(tx)
		if err != nil {
			return err
		}
		return bucket.ForEach(func(key, raw []byte) error {
			var record APStatusRecord
			if json.Unmarshal(raw, &record) == nil {
				result[string(key)] = record
			}
			return nil
		})
	})
	return result, err
}

// Update runs change on the AP's stored status (a zero record with APID
// set when none exists) and stores the result, in one transaction. The AP
// must be registered: ErrUnknownAP otherwise, so a write racing an AP's
// deletion cannot leave an orphan record. SiteID is set from the AP record.
// change returning an error stores nothing.
func (a *APStatusStore) Update(apID string, change func(*APStatusRecord) error) (APStatusRecord, error) {
	var record APStatusRecord
	s := a.store
	s.mu.RLock()
	defer s.mu.RUnlock()
	siteID, registered := "", false
	for _, ap := range s.state.APs {
		if ap.ID == apID {
			siteID, registered = ap.SiteID, true
			break
		}
	}
	if apID == "" || !registered {
		return record, ErrUnknownAP
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket, err := statusBucket(tx)
		if err != nil {
			return err
		}
		record, _, err = readStatus(bucket, apID)
		if err != nil {
			// An unreadable record is replaced rather than blocking every
			// later status write.
			record = APStatusRecord{APID: apID}
		}
		if err := change(&record); err != nil {
			return err
		}
		record.APID, record.SiteID = apID, siteID
		return putJSON(bucket, []byte(apID), record)
	})
	return record, err
}

// RecordHello stores the hello facts and model of a new connection and
// returns the previous hello (nil when none was stored) and when it
// arrived, so reboot detection works across controller restarts.
func (a *APStatusStore) RecordHello(apID string, at time.Time, model string, hello HelloFacts) (previous *HelloFacts, previousAt time.Time, err error) {
	_, err = a.Update(apID, func(record *APStatusRecord) error {
		previous, previousAt = record.Hello, record.HelloAt
		facts := hello
		facts.LastRebootTime = facts.LastRebootTime.UTC()
		record.Hello, record.HelloAt = &facts, at.UTC()
		if model != "" {
			record.Model = model
		}
		return nil
	})
	return previous, previousAt, err
}

// RecordReboot adds a detected reboot (newest first, at most
// MaxRebootRecords). A record with the same DetectedAt replaces the
// stored one, so a retried write does not duplicate it.
func (a *APStatusStore) RecordReboot(apID string, reboot RebootRecord) error {
	if reboot.DetectedAt.IsZero() {
		return errors.New("reboot needs a detection time")
	}
	reboot.DetectedAt = reboot.DetectedAt.UTC()
	reboot.LastRebootTime = reboot.LastRebootTime.UTC()
	_, err := a.Update(apID, func(record *APStatusRecord) error {
		kept := make([]RebootRecord, 0, len(record.Reboots)+1)
		kept = append(kept, reboot)
		for _, old := range record.Reboots {
			if !old.DetectedAt.Equal(reboot.DetectedAt) {
				kept = append(kept, old)
			}
		}
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].DetectedAt.After(kept[j].DetectedAt) })
		if len(kept) > MaxRebootRecords {
			kept = kept[:MaxRebootRecords]
		}
		record.Reboots = kept
		return nil
	})
	return err
}

// Reboots returns an AP's detected reboots, newest first.
func (a *APStatusStore) Reboots(apID string) ([]RebootRecord, error) {
	record, err := a.Get(apID)
	if errors.Is(err, ErrNotFound) {
		return []RebootRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	return append([]RebootRecord{}, record.Reboots...), nil
}

// normaliseVAPs sorts by Dev (then BSSID) and normalises BSSIDs, so equal
// lists compare equal.
func normaliseVAPs(vaps []VAPRecord) []VAPRecord {
	result := make([]VAPRecord, len(vaps))
	for index, vap := range vaps {
		vap.BSSID = NormaliseMAC(vap.BSSID)
		result[index] = vap
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Dev != result[j].Dev {
			return result[i].Dev < result[j].Dev
		}
		return result[i].BSSID < result[j].BSSID
	})
	return result
}

func sameVAPs(a, b []VAPRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// SetVAPs stores the AP's current field-32 VAP list. It writes only when
// the list changed, or to refresh VAPsSeenAt every VAPRefreshInterval, so
// it can be called on every full frame. An empty, non-nil list is stored
// (the AP broadcasts nothing); pass nil only to mean "unknown", which
// changes nothing. changed reports a different list.
func (a *APStatusStore) SetVAPs(apID string, at time.Time, vaps []VAPRecord) (changed bool, err error) {
	if vaps == nil {
		return false, nil
	}
	next := normaliseVAPs(vaps)
	at = at.UTC()
	current, err := a.Get(apID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if current.VAPs != nil && sameVAPs(current.VAPs, next) && at.Sub(current.VAPsSeenAt) < VAPRefreshInterval {
		return false, nil
	}
	_, err = a.Update(apID, func(record *APStatusRecord) error {
		changed = record.VAPs == nil || !sameVAPs(record.VAPs, next)
		if changed {
			record.VAPs, record.VAPsChangedAt = next, at
		}
		record.VAPsSeenAt = at
		return nil
	})
	return changed, err
}

// SetTunnelIPv6 records the address of an ep-ip-register.
func (a *APStatusStore) SetTunnelIPv6(apID string, at time.Time, address string) error {
	_, err := a.Update(apID, func(record *APStatusRecord) error {
		record.TunnelIPv6, record.TunnelIPv6At = address, at.UTC()
		return nil
	})
	return err
}

// SetConfigRequest records the fields of an ep-config request.
func (a *APStatusStore) SetConfigRequest(apID string, facts ConfigRequestFacts) error {
	facts.At = facts.At.UTC()
	facts.Capabilities = append([]string(nil), facts.Capabilities...)
	sort.Strings(facts.Capabilities)
	_, err := a.Update(apID, func(record *APStatusRecord) error {
		record.ConfigRequest = &facts
		return nil
	})
	return err
}
