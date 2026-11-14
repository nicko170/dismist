package controller

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// HelloExtras are the msgs.AuthHello fields that authentication does not
// use (design §5.4): identity, reboot and connection facts the AP sends
// with every ep-auth hello. Types follow the 0.15 ep-control struct
// (epcontrol015-types.txt). Zero means absent.
//
// They are decoded by decodeHelloExtras, never by the Hello decode, so a
// field with an unexpected type can never fail authentication.
type HelloExtras struct {
	SKU      string `json:"sku,omitempty"`
	SerialNo string `json:"serial,omitempty"`
	// Developer is the AP's developer-image flag (omitted when false).
	Developer bool `json:"developer,omitempty"`
	// Uptime is the AP's uptime in seconds when it sent the hello.
	Uptime int64 `json:"uptime_seconds,omitempty"`
	// ConnectUptime is the AP's uptime in seconds when ep-control started
	// this connection attempt.
	ConnectUptime    int64     `json:"connect_uptime_seconds,omitempty"`
	BootCount        uint64    `json:"boot_count,omitempty"`
	LastRebootTime   time.Time `json:"last_reboot_time,omitzero"`
	LastRebootReason string    `json:"last_reboot_reason,omitempty"`
	LastBootState    string    `json:"last_boot_state,omitempty"`
	// LocalIP is the AP's own LAN address; kept only when it parses as an
	// IP address.
	LocalIP    string `json:"local_ip,omitempty"`
	TimeoutSec int64  `json:"timeout_sec,omitempty"`
}

// HelloDecodeErrors lists the hello fields the tolerant pass skipped
// because their JSON did not decode to the expected type or value.
type HelloDecodeErrors []string

// Limits for strings taken from a hello. The hello of an unregistered AP is
// unauthenticated input, so every string is bounded and stripped of
// control characters before it is stored anywhere.
const (
	maxHelloString = 64
	maxHelloID     = 64
)

// helloField decodes one extra field from its raw JSON. It returns false
// when the value has the wrong type or an invalid value; a JSON null is
// treated as absent (as encoding/json does) and is not an error.
type helloField struct {
	name   string
	decode func(raw json.RawMessage, extras *HelloExtras) bool
}

var helloFields = []helloField{
	{"SKU", stringField(func(e *HelloExtras, v string) { e.SKU = v })},
	{"SerialNo", stringField(func(e *HelloExtras, v string) { e.SerialNo = v })},
	{"Developer", func(raw json.RawMessage, e *HelloExtras) bool {
		return decodeInto(raw, &e.Developer)
	}},
	{"Uptime", intField(func(e *HelloExtras, v int64) { e.Uptime = v })},
	{"ConnectUptime", intField(func(e *HelloExtras, v int64) { e.ConnectUptime = v })},
	{"BootCount", func(raw json.RawMessage, e *HelloExtras) bool {
		return decodeInto(raw, &e.BootCount)
	}},
	{"LastRebootTime", func(raw json.RawMessage, e *HelloExtras) bool {
		var when time.Time
		if !decodeInto(raw, &when) {
			return false
		}
		e.LastRebootTime = when.UTC()
		return true
	}},
	{"LastRebootReason", stringField(func(e *HelloExtras, v string) { e.LastRebootReason = v })},
	{"LastBootState", stringField(func(e *HelloExtras, v string) { e.LastBootState = v })},
	{"LocalIP", func(raw json.RawMessage, e *HelloExtras) bool {
		var value string
		if !decodeInto(raw, &value) {
			return false
		}
		if value == "" {
			return true
		}
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			return false
		}
		e.LocalIP = address.Unmap().String()
		return true
	}},
	{"Timeout_sec", intField(func(e *HelloExtras, v int64) { e.TimeoutSec = v })},
}

func decodeInto(raw json.RawMessage, target any) bool {
	return json.Unmarshal(raw, target) == nil
}

func stringField(set func(*HelloExtras, string)) func(json.RawMessage, *HelloExtras) bool {
	return func(raw json.RawMessage, e *HelloExtras) bool {
		var value string
		if !decodeInto(raw, &value) {
			return false
		}
		set(e, cleanHelloString(value, maxHelloString))
		return true
	}
}

func intField(set func(*HelloExtras, int64)) func(json.RawMessage, *HelloExtras) bool {
	return func(raw json.RawMessage, e *HelloExtras) bool {
		var value int64
		if !decodeInto(raw, &value) {
			return false
		}
		set(e, value)
		return true
	}
}

// decodeHelloExtras is the separate tolerant pass over an ep-auth hello
// (design §5.4). It unmarshals the hello into raw fields and decodes each
// extra field on its own: a field with an unexpected type is skipped and
// named in the returned errors, and the others still land. Field names
// match like encoding/json does (exact first, then case-insensitively).
// A body that is not a JSON object yields no extras and the error "hello".
func decodeHelloExtras(body []byte) (HelloExtras, HelloDecodeErrors) {
	var extras HelloExtras
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return extras, HelloDecodeErrors{"hello"}
	}
	var failed HelloDecodeErrors
	for _, field := range helloFields {
		raw, found := lookupField(fields, field.name)
		if !found || isJSONNull(raw) {
			continue
		}
		if !field.decode(raw, &extras) {
			failed = append(failed, field.name)
		}
	}
	return extras, failed
}

// lookupField finds a key the way encoding/json matches struct fields: an
// exact match wins, otherwise the first case-insensitive match in sorted
// key order (deterministic, unlike map iteration).
func lookupField(fields map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	if raw, ok := fields[name]; ok {
		return raw, true
	}
	var bestKey string
	var best json.RawMessage
	found := false
	for key, raw := range fields {
		if strings.EqualFold(key, name) && (!found || key < bestKey) {
			bestKey, best, found = key, raw, true
		}
	}
	return best, found
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// cleanHelloString bounds a string from the AP to limit runes and drops
// control and invalid characters.
func cleanHelloString(value string, limit int) string {
	var builder strings.Builder
	count := 0
	for _, r := range value {
		if count >= limit {
			break
		}
		if r == utf8.RuneError || unicode.IsControl(r) {
			continue
		}
		builder.WriteRune(r)
		count++
	}
	return strings.TrimSpace(builder.String())
}

// rebootDetected compares two hellos of the same AP (design §5.4 "Reboot vs
// reconnect"): the AP rebooted when its boot count went up, or when its
// uptime is below the previous uptime plus the time since (with slack for
// clock granularity). known is false when the hellos carry neither fact.
func rebootDetected(previous HelloExtras, previousAt time.Time, current HelloExtras, now time.Time) (rebooted, known bool) {
	if previousAt.IsZero() {
		return false, false
	}
	if previous.BootCount > 0 && current.BootCount > 0 {
		known = true
		if current.BootCount > previous.BootCount {
			return true, true
		}
	}
	if previous.Uptime > 0 && current.Uptime > 0 {
		known = true
		const slack = 60 * time.Second
		elapsed := now.Sub(previousAt)
		if elapsed < 0 {
			elapsed = 0
		}
		expected := time.Duration(previous.Uptime)*time.Second + elapsed
		if time.Duration(current.Uptime)*time.Second+slack < expected {
			return true, true
		}
	}
	return false, known
}

// summary is the one-line description stored in connection events, so the
// stability investigation can read every connection's facts from history.
func (e HelloExtras) summary() string {
	var parts []string
	if e.BootCount > 0 {
		parts = append(parts, fmt.Sprintf("boot count %d", e.BootCount))
	}
	if e.Uptime > 0 {
		parts = append(parts, fmt.Sprintf("uptime %ds", e.Uptime))
	}
	if e.ConnectUptime > 0 {
		parts = append(parts, fmt.Sprintf("connect uptime %ds", e.ConnectUptime))
	}
	if !e.LastRebootTime.IsZero() || e.LastRebootReason != "" || e.LastBootState != "" {
		reboot := "last reboot"
		if !e.LastRebootTime.IsZero() {
			reboot += " " + e.LastRebootTime.UTC().Format(time.RFC3339)
		}
		var why []string
		if e.LastRebootReason != "" {
			why = append(why, "reason "+e.LastRebootReason)
		}
		if e.LastBootState != "" {
			why = append(why, e.LastBootState+" boot")
		}
		if len(why) != 0 {
			reboot += " (" + strings.Join(why, ", ") + ")"
		}
		parts = append(parts, reboot)
	}
	if e.LocalIP != "" {
		parts = append(parts, "local IP "+e.LocalIP)
	}
	return strings.Join(parts, "; ")
}
