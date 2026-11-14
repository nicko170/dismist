package httpapi

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/options"
)

// experimentalFallback lists the needs-live-test options and values of the
// design (§3.9 to §3.11) that stay experimental even when the registry
// does not (yet) declare them. The server enforces the union of these and
// the registry's confidence (design P4, D8).
var experimentalFallback = map[string][]string{
	"wlan.security":                    {"owe", "owe_transition"},
	"wlan.roaming.fast":                {"okc", "11r"},
	"wlan.roaming.ap_key_cache":        {"*"},
	"wlan.rates":                       {"high_density", "legacy_only"},
	"wlan.min_rssi":                    {"*"},
	"wlan.rate_limit":                  {"*"},
	"wlan.qos_class":                   {"*"},
	"wlan.owe_ssid":                    {"*"},
	"wlan.broadcast.mcast_to_ucast":    {"*"},
	"wlan.schedule":                    {"*"},
	"wlan.mpsk":                        {"*"},
	"wlan.radius.dynamic_vlan":         {"*"},
	"wlan.radius.coa":                  {"*"},
	"site.ssh.readonly_keys":           {"*"},
	"site.syslog":                      {"*"},
	"site.management.vlan_id":          {"!1"},
	"site.ap_defaults.eth1":            {"*"},
	"site.ap_defaults.config_recovery": {"*"},
	"site.protection.penalty_box":      {"*"},
	"site.rf.band_24.advanced.mu_mimo": {"*"},
	"site.rf.band_24.advanced.txbf":    {"*"},
	"site.rf.band_5.advanced.mu_mimo":  {"*"},
	"site.rf.band_5.advanced.txbf":     {"*"},
	"site.rf.scanning_radio":           {"*"},
	"ap.management":                    {"*"},
	"ap.eth1":                          {"*"},
	"ap.locate":                        {"*"},
}

// experimentalKeys returns the option keys of changed whose new value is
// experimental, by the registry or by the fallback list. A null (reset)
// is never experimental.
// experimentalKeys lists the option keys of changed whose values are
// experimental. A value that is off (false, empty, zero) never counts; nor
// does one equal to the object's current value (current, by option key:
// forms send whole sections) or, for a new object (current nil), the
// option's default.
func experimentalKeys(registry *options.Registry, changed, current map[string]any) []string {
	var keys []string
	for key, value := range changed {
		if value == nil {
			continue
		}
		// Writing an experimental option at its default, or off (false,
		// empty, zero), never needs the site's switch: it asks the AP for
		// nothing it does not do already. Forms send whole sections, so
		// these arrive with every save.
		if isZeroJSON(value) {
			continue
		}
		if current != nil {
			if stored, ok := current[key]; ok && sameJSON(value, stored) {
				continue
			}
		}
		experimental := false
		if registry != nil {
			if option, ok := registry.Get(key); ok {
				if current == nil && option.Default != nil && sameJSON(value, option.Default) {
					continue
				}
				experimental = option.Experimental(value)
			}
		}
		for prefix, values := range experimentalFallback {
			if key != prefix && !strings.HasPrefix(key, prefix+".") {
				continue
			}
			text := fmt.Sprint(value)
			for _, candidate := range values {
				switch {
				case candidate == "*":
					experimental = experimental || !isZeroJSON(value)
				case strings.HasPrefix(candidate, "!"):
					experimental = experimental || (text != candidate[1:] && !isZeroJSON(value))
				case candidate == text:
					experimental = true
				}
			}
		}
		if experimental {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// experimentalDisabled is the 422 for experimental values on a site
// without experimental: true.
func experimentalDisabled(keys []string) *apiError {
	e := newError(http.StatusUnprocessableEntity, api.ErrorCodeExperimentalDisabled,
		"experimental options need the site's experimental setting (Settings › Advanced): "+strings.Join(keys, ", "))
	for _, key := range keys {
		e.withField(key, "experimental: not yet verified on AP33 0.15")
	}
	return e
}

// registryCheck validates changed values against their registry options:
// enum values, integer bounds and string byte limits.
func registryCheck(registry *options.Registry, root string, changed map[string]any) *apiError {
	if registry == nil {
		return nil
	}
	var bad *apiError
	field := func(path, message string) {
		if bad == nil {
			bad = invalid(message)
		}
		bad.withField(path, message)
	}
	keys := make([]string, 0, len(changed))
	for key := range changed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := changed[key]
		option, ok := registry.Get(key)
		if !ok || value == nil {
			continue
		}
		path := strings.TrimPrefix(key, root+".")
		if len(option.Enum) > 0 {
			if text, isString := value.(string); isString {
				found := false
				for _, enum := range option.Enum {
					if enum.Value == text {
						found = true
					}
				}
				if !found {
					field(path, fmt.Sprintf("%q is not an allowed value", text))
				}
			}
		}
		if number, isInt := value.(int); isInt {
			if option.Min != nil && number < *option.Min {
				field(path, fmt.Sprintf("must be at least %d", *option.Min))
			}
			if option.Max != nil && number > *option.Max {
				field(path, fmt.Sprintf("must be at most %d", *option.Max))
			}
		}
		if text, isString := value.(string); isString && option.MaxBytes > 0 && len(text) > option.MaxBytes {
			field(path, fmt.Sprintf("must be at most %d bytes", option.MaxBytes))
		}
	}
	return bad
}

// fieldErrors collects field errors into one 400 (or 422 with status).
type fieldErrors struct {
	status int
	fields map[string]string
	first  string
}

func (f *fieldErrors) add(path, message string) {
	if f.fields == nil {
		f.fields = map[string]string{}
	}
	if _, exists := f.fields[path]; !exists {
		f.fields[path] = message
	}
	if f.first == "" {
		f.first = message
	}
}

func (f *fieldErrors) err(what string) *apiError {
	if len(f.fields) == 0 {
		return nil
	}
	status := f.status
	if status == 0 {
		status = http.StatusBadRequest
	}
	message := "invalid " + what + ": " + f.first
	if len(f.fields) > 1 {
		message = fmt.Sprintf("invalid %s: %s (and %d more)", what, f.first, len(f.fields)-1)
	}
	e := newError(status, api.ErrorCodeInvalid, message)
	for path, text := range f.fields {
		e.withField(path, text)
	}
	return e
}

// Passphrase rules (design §3.9): 8 to 63 printable ASCII, or (WPA2
// only) exactly 64 hex digits.
func passphraseError(psk string, allowHex bool) string {
	if len(psk) == 64 {
		if _, err := hex.DecodeString(psk); err == nil {
			if allowHex {
				return ""
			}
			return "a 64-hex key works with WPA2 only; SAE needs an 8 to 63 character passphrase"
		}
	}
	if len(psk) < 8 || len(psk) > 63 {
		return "passphrase must be 8 to 63 characters"
	}
	for _, char := range psk {
		if char < 0x20 || char > 0x7e {
			return "passphrase must be printable ASCII"
		}
	}
	return ""
}

// ssidError checks an SSID: 1 to 32 bytes of UTF-8.
func ssidError(ssid string) string {
	if !utf8.ValidString(ssid) {
		return "SSID must be UTF-8"
	}
	if len(ssid) < 1 || len(ssid) > 32 {
		return "SSID must be 1 to 32 bytes"
	}
	return ""
}

var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// hostOrIP accepts an IP literal or a DNS name.
func hostOrIP(value string) bool {
	return net.ParseIP(value) != nil || hostnamePattern.MatchString(value)
}

// normaliseMAC returns a MAC in lower-case dashed form, or "".
func normaliseMAC(value string) string {
	compact := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(strings.TrimSpace(value))
	if len(compact) != 12 {
		return ""
	}
	raw, err := hex.DecodeString(compact)
	if err != nil {
		return ""
	}
	parts := make([]string, 6)
	for index, b := range raw {
		parts[index] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, "-")
}
