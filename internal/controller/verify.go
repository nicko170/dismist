package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/telemetry"
)

// Verification is the result of one post-delivery verification (design
// §5.9), stored in ap_config.delivered.verified and shown on AP detail.
type Verification struct {
	At        time.Time `json:"at"`
	Revision  uint64    `json:"revision"`
	Timestamp float64   `json:"timestamp"`
	OK        bool      `json:"ok"`
	// Mismatches are differences between the delivered config and what
	// the AP reports; any one fails the verification.
	Mismatches []string `json:"mismatches,omitempty"`
	// Notes are differences that are not failures (a DFS channel waiting
	// for CAC, checks skipped for lack of data).
	Notes []string `json:"notes,omitempty"`
	// Unchanged is true when the delivered config was effectively equal to
	// the previous delivery, so the AP's state was also compared with what
	// it reported before (the hardware-side migration check).
	Unchanged bool `json:"unchanged,omitempty"`
}

// verifyState schedules the verification of the last delivery.
type verifyState struct {
	generation uint64
	timer      timerHandle
	pending    *pendingVerification
	// previous is the last delivered config, the baseline of the
	// unchanged-config check.
	previous []byte
}

type pendingVerification struct {
	delivery  ConfigDelivery
	unchanged bool
	before    *fingerprint
}

// fingerprint is what the AP reported about its radios and WLANs.
type fingerprint struct {
	BSSIDs  map[string]string // VAP Dev -> BSSID
	Radios  map[string]radioState
	Country string
	WLANs   []string // "wlanID/band", sorted
}

type radioState struct {
	Channel, Width, MaxPower int
}

func (f fingerprint) String() string {
	var parts []string
	devs := make([]string, 0, len(f.BSSIDs))
	for dev := range f.BSSIDs {
		devs = append(devs, dev)
	}
	sort.Strings(devs)
	for _, dev := range devs {
		parts = append(parts, dev+"="+f.BSSIDs[dev])
	}
	bands := make([]string, 0, len(f.Radios))
	for band := range f.Radios {
		bands = append(bands, band)
	}
	sort.Strings(bands)
	for _, band := range bands {
		radio := f.Radios[band]
		parts = append(parts, fmt.Sprintf("%s:%d/%d@%d", band, radio.Channel, radio.Width, radio.MaxPower))
	}
	return strings.Join(parts, " ")
}

// fingerprintOf takes the fingerprint from a snapshot; ok is false when it
// has no full frame.
func fingerprintOf(stats TelemetrySnapshot, vaps []telemetry.VAP) (fingerprint, bool) {
	if len(vaps) == 0 && len(stats.Radios) == 0 {
		return fingerprint{}, false
	}
	result := fingerprint{BSSIDs: map[string]string{}, Radios: map[string]radioState{}, Country: stats.Health.Country}
	for _, vap := range vaps {
		result.BSSIDs[vap.Interface] = vap.BSSID
		result.WLANs = append(result.WLANs, vap.WLANID+"/"+vap.Band)
	}
	sort.Strings(result.WLANs)
	for _, radio := range stats.Radios {
		if radio.Role == telemetry.RoleAccess && radio.Band != "" {
			result.Radios[radio.Band] = radioState{Channel: radio.Channel, Width: int(radio.WidthMHz), MaxPower: radio.MaxTxPowerDBm}
		}
	}
	return result, true
}

// verifyDelivered schedules the verification of a delivery: Timings.Verify
// after the reply, or after the AP's apply report when one arrives first
// (verifyApplied). It runs after every delivery: a push, a reconnect, a
// restart, the first delivery after the migration.
func (s *Server) verifyDelivered(delivery ConfigDelivery) {
	s.mu.Lock()
	v := &s.verify
	unchanged := false
	if v.previous != nil {
		unchanged = sameEffective(v.previous, delivery.Config, s.localFiles)
	}
	v.previous = append([]byte(nil), delivery.Config...)
	var before *fingerprint
	if unchanged {
		stats := s.snapshotLocked()
		vaps := stats.VAPs
		if !stats.LastFull.Before(delivery.At) || len(vaps) == 0 {
			// No full frame from before this delivery in memory: compare
			// the persisted VAP list (BSSIDs) only.
			vaps = stats.LastKnownVAPs
		}
		if print, ok := fingerprintOf(stats, vaps); ok {
			if !stats.LastFull.Before(delivery.At) {
				print.Radios, print.Country = map[string]radioState{}, ""
			}
			before = &print
		}
	}
	v.generation++
	generation := v.generation
	v.pending = &pendingVerification{delivery: delivery, unchanged: unchanged, before: before}
	v.timer.set(s.timings.Verify, func() { s.runVerification(generation) })
	s.mu.Unlock()
}

// verifyApplied re-schedules the verification Timings.Verify after the
// AP's apply report for the delivered Timestamp.
func (s *Server) verifyApplied(applied telemetry.Applied, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := &s.verify
	if v.pending == nil || applied.Error != "" || math.Abs(applied.ConfigTimestamp-v.pending.delivery.Timestamp) > 0.000001 {
		return
	}
	generation := v.generation
	v.timer.set(s.timings.Verify, func() { s.runVerification(generation) })
}

// sameEffective reports whether two configs normalise to the same
// effective config (design §4.5); a config that does not normalise is
// compared byte for byte.
func sameEffective(a, b []byte, localFiles string) bool {
	normalisedA, _, errA := effective.Normalise(a, effective.Context{LocalFiles: localFiles})
	normalisedB, _, errB := effective.Normalise(b, effective.Context{LocalFiles: localFiles})
	if errA != nil || errB != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(normalisedA, normalisedB)
}

// runVerification compares the AP's telemetry with the delivered config.
func (s *Server) runVerification(generation uint64) {
	now := time.Now()
	s.mu.Lock()
	v := &s.verify
	if v.generation != generation || v.pending == nil {
		s.mu.Unlock()
		return
	}
	pending := *v.pending
	v.pending = nil
	stats := s.snapshotLocked()
	connected := s.status.Connected
	s.mu.Unlock()
	result := verifyConfig(pending, stats, connected, s.localFiles, now)
	s.counters.verifications.Add(1)
	if !result.OK {
		s.counters.verifyMismatches.Add(1)
	}
	s.mu.Lock()
	s.status.Verification = &result
	wasMismatch := s.status.VerifyMismatch
	s.status.VerifyMismatch = !result.OK
	if !result.OK {
		data, _ := json.Marshal(map[string]any{"revision": result.Revision, "mismatches": result.Mismatches})
		s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityWarning,
			Title:  fmt.Sprintf("Post-delivery verification failed for revision %d", result.Revision),
			Detail: strings.Join(result.Mismatches, "; "), Data: data})
	} else if wasMismatch {
		s.emitLocked(Event{At: now, Kind: EventConfig, Severity: SeverityInfo,
			Title: fmt.Sprintf("Post-delivery verification passed for revision %d", result.Revision)})
	}
	s.mu.Unlock()
	s.flush()
	if s.onVerified != nil {
		s.onVerified(s.auth.ID, result)
	}
}

// normalisedConfig is the part of the normalised effective config the
// verification reads (resolved radios: lower-case channel, width, power,
// ccrev; WLAN BSSID indexes in BandConfigs).
type normalisedConfig struct {
	Country string
	Radios  []struct {
		Band     string
		Disabled bool
		Channel  int    `json:"channel"`
		Width    int    `json:"width"`
		Power    int    `json:"power"`
		CCRev    string `json:"ccrev"`
	}
	WLANs []struct {
		ID          string
		SSID        string
		Enabled     bool
		Bands       []string
		BandConfigs []struct {
			Band       string
			BSSIDIndex int
		}
	}
	Monitor struct {
		CCRev string `json:"CCRev"`
	}
}

// isDFS reports whether a 5 GHz channel is a DFS channel (52 to 144).
func isDFS(channel int) bool { return channel >= 52 && channel <= 144 }

// verifyConfig is the verification itself (design §5.9).
func verifyConfig(pending pendingVerification, stats TelemetrySnapshot, connected bool, localFiles string, now time.Time) Verification {
	delivery := pending.delivery
	result := Verification{At: now, Revision: delivery.Revision, Timestamp: delivery.Timestamp, Unchanged: pending.unchanged}
	fail := func(format string, args ...any) {
		result.Mismatches = append(result.Mismatches, fmt.Sprintf(format, args...))
	}
	note := func(format string, args ...any) { result.Notes = append(result.Notes, fmt.Sprintf(format, args...)) }
	if !connected {
		note("the AP disconnected before the verification")
	}
	if stats.LastFull.Before(delivery.At) {
		fail("no full telemetry frame since the delivery")
		result.OK = false
		return result
	}
	canonical, report, err := effective.Normalise(delivery.Config, effective.Context{LocalFiles: localFiles})
	var config normalisedConfig
	if err != nil || json.Unmarshal(canonical, &config) != nil {
		fail("the delivered config does not normalise")
		return result
	}
	notes := map[string]bool{}
	for _, item := range report.Notes {
		notes[item] = true
	}
	if math.Abs(stats.ConfigTimestamp-delivery.Timestamp) > 0.000001 {
		fail("the AP reports config Timestamp %.6f, not the delivered %.6f", stats.ConfigTimestamp, delivery.Timestamp)
	}
	radios := map[string]telemetry.RadioStats{}
	for _, radio := range stats.Radios {
		if radio.Role == telemetry.RoleAccess && radio.Band != "" {
			radios[radio.Band] = radio
		}
	}
	enabledBands := map[string]bool{}
	ccrev := config.Monitor.CCRev
	for _, configured := range config.Radios {
		if configured.Band != telemetry.Band24 && configured.Band != telemetry.Band5 {
			continue
		}
		if configured.Disabled {
			continue
		}
		enabledBands[configured.Band] = true
		if configured.CCRev != "" {
			ccrev = configured.CCRev
		}
		if notes[effective.NoteRadioUnresolved] || notes[effective.NoteLocalFiles] {
			continue
		}
		radio, ok := radios[configured.Band]
		if !ok {
			fail("no %s GHz radio in the telemetry", configured.Band)
			continue
		}
		if radio.Missing {
			fail("the %s GHz radio is reported missing", configured.Band)
		}
		if radio.Channel != configured.Channel && configured.Channel != 0 {
			if configured.Band == telemetry.Band5 && (isDFS(configured.Channel) || radio.DFS.CACState != 0) {
				note("5 GHz radio on channel %d instead of %d (DFS fallback or CAC)", radio.Channel, configured.Channel)
			} else {
				fail("%s GHz radio on channel %d, configured %d", configured.Band, radio.Channel, configured.Channel)
			}
		}
		if configured.Width != 0 && int(radio.WidthMHz) != configured.Width {
			fail("%s GHz radio at %d MHz, configured %d MHz", configured.Band, radio.WidthMHz, configured.Width)
		}
		if configured.Power > 0 && radio.MaxTxPowerDBm != configured.Power {
			fail("%s GHz radio max power %d dBm, configured %d dBm", configured.Band, radio.MaxTxPowerDBm, configured.Power)
		}
	}
	country := config.Country
	if country == "" && ccrev != "" {
		country, _, _ = strings.Cut(ccrev, "/")
	}
	if country != "" && stats.Health.Country != "" && !strings.EqualFold(country, stats.Health.Country) {
		fail("the AP reports country %s, configured %s", stats.Health.Country, country)
	}
	vaps := map[string]telemetry.VAP{}
	for _, vap := range stats.VAPs {
		vaps[strings.ToLower(vap.WLANID)+"/"+vap.Band] = vap
	}
	for _, wlan := range config.WLANs {
		if !wlan.Enabled {
			continue
		}
		for _, band := range wlan.Bands {
			if band != telemetry.Band24 && band != telemetry.Band5 {
				continue
			}
			if !enabledBands[band] && len(config.Radios) != 0 {
				continue
			}
			vap, ok := vaps[strings.ToLower(wlan.ID)+"/"+band]
			if !ok {
				fail("WLAN %q is not broadcast on %s GHz", wlan.SSID, band)
				continue
			}
			if vap.SSID != wlan.SSID {
				fail("WLAN %s broadcasts SSID %q, configured %q", wlan.ID, vap.SSID, wlan.SSID)
			}
			if notes[effective.NoteBSSIDUnresolved] {
				continue
			}
			for _, bandConfig := range wlan.BandConfigs {
				if bandConfig.Band == band && bandConfig.BSSIDIndex > 0 {
					if index := telemetry.VAPIndex(vap.Interface); index != bandConfig.BSSIDIndex {
						fail("WLAN %q on %s GHz runs as %s, expected BSSID index %d", wlan.SSID, band, vap.Interface, bandConfig.BSSIDIndex)
					}
				}
			}
		}
	}
	if pending.unchanged {
		if pending.before == nil {
			note("no telemetry from before the delivery to compare")
		} else if after, ok := fingerprintOf(stats, stats.VAPs); ok {
			before := *pending.before
			for dev, bssid := range before.BSSIDs {
				if after.BSSIDs[dev] != bssid {
					fail("unchanged config, but %s BSSID changed from %s to %s", dev, bssid, after.BSSIDs[dev])
				}
			}
			for dev := range after.BSSIDs {
				if _, known := before.BSSIDs[dev]; !known && len(before.BSSIDs) != 0 {
					fail("unchanged config, but the AP now broadcasts %s", dev)
				}
			}
			for band, radio := range before.Radios {
				if after.Radios[band] != radio {
					fail("unchanged config, but the %s GHz radio changed from %d/%d@%d to %d/%d@%d", band,
						radio.Channel, radio.Width, radio.MaxPower,
						after.Radios[band].Channel, after.Radios[band].Width, after.Radios[band].MaxPower)
				}
			}
			if before.Country != "" && before.Country != after.Country {
				fail("unchanged config, but the country changed from %s to %s", before.Country, after.Country)
			}
		}
	}
	result.OK = len(result.Mismatches) == 0
	return result
}
