package metrics

import (
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Cardinality limits (design §6.2): every label value that comes from an AP
// is checked against a fixed list or a pattern, and every list an AP reports
// is capped, so a misbehaving AP cannot grow the series count without bound.
const (
	maxRadiosPerAP      = 4
	maxPortsPerAP       = 8
	maxWLANsPerAP       = 32
	maxVLANsPerAP       = 32
	maxNeighborsPerAP   = 8
	maxCountedKeys      = 32
	maxCertificates     = 16
	maxLabelTextRunes   = 64
	maxClientRadioIndex = 15
)

var (
	radioPattern   = regexp.MustCompile(`^r[0-9]{1,2}$`)
	portPattern    = regexp.MustCompile(`^[a-z]{2,8}[0-9]{1,2}$`)
	wlanIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	keywordPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)
)

// Fixed label values.
var (
	// TelemetryTopics are the dismist_ap_telemetry_messages_total topics
	// counted by name; others are "other".
	TelemetryTopics = []string{"ap-stats-full", "ap-stats-fast", "ep-config-applied", "ep-pace-event", "client-pace-event", "ap-cmd-output"}
	// UtilizationTypes are the dismist_radio_channel_utilization_ratio types.
	UtilizationTypes = []string{"all", "tx", "rx_in_bss", "rx_other_bss", "unknown_wifi", "non_wifi", "undecodable_wifi"}
	// CPUModes are the dismist_ap_cpu_seconds_total modes.
	CPUModes = []string{"user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal"}
	// SignalBuckets are the dismist_radio_clients_by_signal buckets, strongest first.
	SignalBuckets = []string{"excellent", "good", "fair", "poor"}
	// Protocols are the dismist_radio_clients_by_protocol values.
	Protocols   = []string{"be", "ax", "ac", "n", "a", "g", "b", "other"}
	pushModes   = []string{"notify", "reconnect"}
	pushResults = []string{"applied", "rejected", "fallback", "timeout", "stale"}
	loadPeriods = []string{"1m", "5m", "15m"}
)

// oneOf returns value when it is in allowed, otherwise "other".
func oneOf(value string, allowed []string) string {
	for _, known := range allowed {
		if value == known {
			return value
		}
	}
	return "other"
}

// keyword returns a lower-case keyword label value (a decode section, a
// topic, a PHY or power source name), or "other" when it does not look like
// one.
func keyword(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if keywordPattern.MatchString(value) {
		return value
	}
	return "other"
}

// labelText bounds free text from an AP for a label value: printable,
// valid UTF-8, at most maxRunes runes.
func labelText(value string, maxRunes int) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "")
	}
	var b strings.Builder
	runes := 0
	for _, r := range value {
		if runes == maxRunes {
			break
		}
		if !unicode.IsPrint(r) {
			continue
		}
		b.WriteRune(r)
		runes++
	}
	return strings.TrimSpace(b.String())
}

// radioName returns "r<index>" for a 32.2/33.2 radio index, or "" when it is
// out of range.
func radioName(index int) string {
	if index < 0 || index > maxClientRadioIndex {
		return ""
	}
	return "r" + strconv.Itoa(index)
}

// clientLabel returns the client MAC as a lower-case dashed label value, or
// "" when it is not a MAC address.
func clientLabel(mac string) string {
	parsed, err := net.ParseMAC(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
	if err != nil || len(parsed) != 6 {
		return ""
	}
	return strings.ReplaceAll(parsed.String(), ":", "-")
}

// countedKeys returns at most maxCountedKeys entries of a counter map with
// their keys mapped by name; counters whose keys map to the same label value
// are added up.
func countedKeys(values map[string]uint64, name func(string) string) map[string]uint64 {
	result := make(map[string]uint64, len(values))
	for key, value := range values {
		label := name(key)
		if _, ok := result[label]; !ok && len(result) >= maxCountedKeys {
			label = "other"
		}
		result[label] += value
	}
	return result
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
