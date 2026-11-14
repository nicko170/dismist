package compile

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
	"golang.org/x/crypto/ssh"
)

// Firmware validation (design §4.4 step 7), every message mapped to an
// option key where possible. A config with any error is never served.

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type validator struct {
	errors   []ValidationError
	warnings []Warning
}

func (v *validator) fail(code, option, path, format string, args ...any) {
	v.errors = append(v.errors, ValidationError{Code: code, Option: option, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (v *validator) warn(code, option, path, format string, args ...any) {
	v.warnings = append(v.warnings, Warning{Code: code, Option: option, Path: path, Message: fmt.Sprintf(format, args...)})
}

func intOf(object map[string]any, keys ...string) int {
	value, _ := valueAt(object, keys...)
	n, _ := asInt(value)
	return n
}

func boolOf(object map[string]any, keys ...string) bool {
	value, _ := valueAt(object, keys...)
	b, _ := asBool(value)
	return b
}

func stringOf(object map[string]any, keys ...string) string {
	value, _ := valueAt(object, keys...)
	s, _ := asString(value)
	return s
}

func validVLANString(text string) bool {
	n, err := strconv.Atoi(text)
	return err == nil && n >= 1 && n <= 4094 && strconv.Itoa(n) == text
}

// validateConfig checks a compiled config tree.
func validateConfig(root map[string]any, in Input, warnings []Warning) ([]ValidationError, []Warning) {
	v := &validator{warnings: warnings}
	raw, err := encodeTree(root)
	if err == nil {
		if _, report, ferr := effective.Fold(raw); ferr == nil {
			for _, problem := range report.TypeErrors {
				v.fail("type", "", "", "wrong JSON type, the AP would reject the whole config: %s", problem)
			}
			for _, ignored := range report.Ignored {
				if ignored == "RootAuthorizedSSHKeys" {
					continue // written on purpose for pre-0.15 firmware
				}
				v.warn("ignored_key", "", ignored, "%s is ignored by AP33 0.15", ignored)
			}
		}
	}
	validateTop(v, root)
	bandsWithWLAN := validateWLANs(v, root)
	validateRadios(v, root, in, bandsWithWLAN)
	return v.errors, v.warnings
}

func validateTop(v *validator, root map[string]any) {
	for _, key := range []string{"SiteID", "OrgID"} {
		if id := stringOf(root, key); id != "" && !uuidPattern.MatchString(id) {
			v.fail("firmware_rule", "", key, "%s must be a UUID or empty", key)
		}
	}
	if ntp, ok := root["NTP"].([]any); ok && len(ntp) > 6 {
		v.fail("firmware_rule", "site.management.ntp", "NTP", "at most 6 NTP servers")
	}
	if syslogs, ok := root["Syslogs"].([]any); ok && len(syslogs) > 16 {
		v.fail("firmware_rule", "site.syslog", "Syslogs", "at most 16 syslog targets")
	}
	if keys, ok := valueAt(root, "SSH", "AuthorizedKeys"); ok {
		list, _ := keys.([]any)
		if len(list) > 32 {
			v.fail("firmware_rule", "site.ssh.keys", "SSH.AuthorizedKeys", "at most 32 SSH keys")
		}
		for _, entry := range list {
			line, _ := asString(entry)
			if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err != nil {
				v.fail("firmware_rule", "site.ssh.keys", "SSH.AuthorizedKeys", "an SSH key cannot be parsed")
			}
		}
		if !boolOf(root, "SSH", "Disabled") && len(list) == 0 {
			v.fail("firmware_rule", "site.ssh.enabled", "SSH.Disabled", "SSH access needs at least 1 key")
		}
	}
	if brightness, ok := valueAt(root, "LED", "Brightness"); ok {
		if n, ok := asInt(brightness); !ok || n < 0 || n > 255 {
			v.fail("firmware_rule", "site.ap_defaults.led_brightness", "LED.Brightness", "LED brightness must be 0 to 255")
		}
	}
	ports, _ := root["SwitchPorts"].([]any)
	var eth0VLANs []string
	for _, element := range ports {
		port, ok := element.(map[string]any)
		if !ok {
			continue
		}
		vlans, _ := port["Vlans"].([]any)
		for _, vlan := range vlans {
			text, _ := asString(vlan)
			if !validVLANString(text) {
				v.fail("firmware_rule", "wlan.vlan_id", "SwitchPorts[].Vlans", "port VLAN %q must be 1 to 4094", text)
			}
			if stringOf(port, "Name") == "eth0" {
				eth0VLANs = append(eth0VLANs, text)
			}
		}
		if pvid := stringOf(port, "PortVlanID"); pvid != "" && !validVLANString(pvid) {
			v.fail("firmware_rule", "", "SwitchPorts[].PortVlanID", "port VLAN ID %q must be 1 to 4094", pvid)
		}
	}
	if svis, ok := root["SVIs"].([]any); ok && len(svis) > 0 {
		if svi, ok := svis[0].(map[string]any); ok {
			mgmt := stringOf(svi, "Vlan_ID")
			if mgmt != "" && mgmt != "1" {
				found := false
				for _, vlan := range eth0VLANs {
					found = found || vlan == mgmt
				}
				if !found {
					v.fail("firmware_rule", "site.management.vlan_id", "SVIs[].Vlan_ID", "the management VLAN %s must be on eth0", mgmt)
				}
			}
		}
	}
}

// validateWLANs checks every WLAN [WLAN §4] and returns the bands that
// have an enabled WLAN.
func validateWLANs(v *validator, root map[string]any) map[string]bool {
	wlans, _ := root["WLANs"].([]any)
	onBand := map[string]bool{}
	perBand := map[string]int{}
	indexes := map[string]map[int]string{"2.4": {}, "5": {}}
	for _, element := range wlans {
		w, ok := element.(map[string]any)
		if !ok {
			continue
		}
		id := stringOf(w, "ID")
		path := "WLANs[ID=" + id + "]"
		ssid := stringOf(w, "SSID")
		if len(ssid) < 1 || len(ssid) > 32 {
			v.fail("firmware_rule", "wlan.ssid", path+".SSID", "SSID must be 1 to 32 bytes")
		}
		bands, _ := w["Bands"].([]any)
		if len(bands) == 0 {
			v.fail("firmware_rule", "wlan.bands", path+".Bands", "a WLAN needs at least one band")
		}
		bandSet := map[string]bool{}
		for _, band := range bands {
			text, _ := asString(band)
			if text != "2.4" && text != "5" {
				v.fail("firmware_rule", "wlan.bands", path+".Bands", `bands must be "2.4" or "5" (got %q)`, text)
				continue
			}
			bandSet[text] = true
		}
		vlans, _ := w["Vlans"].([]any)
		for _, vlan := range vlans {
			text, _ := asString(vlan)
			if !validVLANString(text) {
				v.fail("firmware_rule", "wlan.vlan_id", path+".Vlans", "VLAN %q must be a string 1 to 4094", text)
			}
		}
		enabled := boolOf(w, "Enabled")
		oweTransition := boolOf(w, "OWE", "Transition")
		for band := range bandSet {
			if enabled {
				onBand[band] = true
				perBand[band]++
				if oweTransition {
					perBand[band]++
				}
			}
		}
		if configs, ok := w["BandConfigs"].([]any); ok {
			for _, element := range configs {
				bc, ok := element.(map[string]any)
				if !ok {
					continue
				}
				band := stringOf(bc, "Band")
				index := intOf(bc, "BSSIDIndex")
				if index < 1 || index > 15 {
					v.fail("firmware_rule", "wlan.bssid_index", path+".BandConfigs.BSSIDIndex", "BSSID index must be 1 to 15 (got %d)", index)
					continue
				}
				if other, taken := indexes[band][index]; taken && other != id {
					v.fail("firmware_rule", "wlan.bssid_index", path+".BandConfigs.BSSIDIndex", "BSSID index %d is used twice on %s GHz", index, band)
				}
				if indexes[band] != nil {
					indexes[band][index] = id
				}
				if _, steer := bc["Steer"]; steer && band == "2.4" && !(bandSet["2.4"] && bandSet["5"]) {
					v.fail("firmware_rule", "wlan.band_steering", path+".BandConfigs.Steer", "band steering needs both bands")
				}
			}
		}
		if max := intOf(w, "MaxStaAllowed"); max != 0 && (max < 2 || max > 512) {
			v.fail("firmware_rule", "wlan.max_clients", path+".MaxStaAllowed", "max clients must be 2 to 512")
		}
		validateSecurity(v, w, path)
	}
	for band, count := range perBand {
		if count > 8 {
			v.fail("firmware_rule", "wlan.enabled", "WLANs", "at most 8 WLANs per band (%s GHz has %d; OWE transition counts 2)", band, count)
		}
	}
	return onBand
}

func validateSecurity(v *validator, w map[string]any, path string) {
	authType := stringOf(w, "Auth", "Type")
	akm := objectAt(w, "Auth", "AKM")
	set := func(flag string) bool { return akm != nil && boolOf(akm, flag) }
	anyAKM := false
	for _, flag := range AKMFlags {
		anyAKM = anyAKM || set(flag)
	}
	dot11wOn, dot11wReq := boolOf(w, "Dot11w", "Enabled"), boolOf(w, "Dot11w", "Required")
	wpa3 := set("WPA3_PSK") || set("WPA3_PSK_FT") || set("WPA3_PSK_SAE_EXT") || set("WPA3_PSK_FT_SAE_EXT") ||
		set("WPA3_EAP") || set("WPA3_EAP_FT") || set("WPA3_SUITEB") || set("WPA3_OWE")
	switch authType {
	case "open", "wep":
		if anyAKM {
			v.fail("firmware_rule", "wlan.security", path+".Auth.AKM", "AKM should be empty for %s", authType)
		}
	case "psk", "eap":
		if !anyAKM {
			v.fail("firmware_rule", "wlan.security", path+".Auth.AKM", "AKM should not be empty for auth_type %s", authType)
		}
	case "owe":
	default:
		v.fail("firmware_rule", "wlan.security", path+".Auth.Type", "unknown auth type %q", authType)
	}
	if wpa3 && !dot11wOn {
		v.fail("firmware_rule", "wlan.pmf", path+".Dot11w", "11w capable is mandatory for WPA3.")
	}
	wpa3Only := (set("WPA3_PSK") && !set("WPA2_PSK")) || set("WPA3_PSK_FT") && !set("WPA2_PSK") ||
		(set("WPA3_EAP") && !set("WPA2_EAP")) || set("WPA3_SUITEB") || set("WPA3_OWE")
	if wpa3Only && !dot11wReq {
		v.fail("firmware_rule", "wlan.pmf", path+".Dot11w.Required", "11w required mandatory for WPA3-only and OWE")
	}
	if dot11wReq && set("WPA2_PSK") && !set("WPA3_PSK") && !set("WPA2_PSK_SHA256") {
		v.fail("firmware_rule", "wlan.pmf", path+".Auth.AKM", "11w mandatory, but AKM[WPA2_PSK_SHA256] missing on WPA2-PSK wlan")
	}
	if dot11wReq && set("WPA2_EAP") && !set("WPA3_EAP") && !set("WPA2_EAP_SHA256") {
		v.fail("firmware_rule", "wlan.pmf", path+".Auth.AKM", "11w mandatory, but AKM[WPA2_EAP_SHA256] missing on WPA2-EAP wlan")
	}
	if dot11wOn && (set("WPA_PSK") || set("WPA_EAP")) {
		v.fail("firmware_rule", "wlan.security", path+".Auth.AKM", "11w capable is not allowed with WPA1 AKM")
	}
	if wpa3 && (boolOf(w, "Auth", "Pairwise", "TKIP") || boolOf(w, "Auth", "Pairwise", "WPA1", "TKIP")) {
		v.fail("firmware_rule", "wlan.security", path+".Auth.Pairwise", "TKIP is not allowed with WPA3")
	}
	for ft, base := range map[string]string{"WPA2_PSK_FT": "WPA2_PSK", "WPA3_PSK_FT": "WPA3_PSK", "WPA2_EAP_FT": "WPA2_EAP", "WPA3_EAP_FT": "WPA3_EAP"} {
		if set(ft) && !set(base) && !(ft == "WPA2_PSK_FT" && set("WPA2_PSK_SHA256")) {
			v.fail("firmware_rule", "wlan.roaming.fast", path+".Auth.AKM", "akm %s can't be enabled without akm %s", ft, base)
		}
	}
	if authType == "psk" {
		psk := stringOf(w, "Auth", "PSK")
		sae := set("WPA3_PSK") || set("WPA3_PSK_FT") || set("WPA3_PSK_SAE_EXT")
		switch {
		case sae && !model.ValidPassphrase(psk):
			v.fail("firmware_rule", "wlan.psk", path+".Auth.PSK", "WPA3 passphrases are 8 to 63 printable characters")
		case !sae && !model.ValidPassphrase(psk) && !model.IsHexPMK(psk):
			v.fail("firmware_rule", "wlan.psk", path+".Auth.PSK", "the passphrase must be 8 to 63 printable characters or 64 hex digits")
		}
	}
	if psks, ok := valueAt(w, "Auth", "MultiPSK", "PSKs"); ok {
		list, _ := psks.([]any)
		for _, element := range list {
			entry, _ := element.(map[string]any)
			if !model.IsHexPMK(stringOf(entry, "PSK")) {
				v.fail("firmware_rule", "wlan.mpsk.keys", path+".Auth.MultiPSK.PSKs", "a Multi-PSK key must be exactly 64 hex digits")
			}
		}
	}
	if (authType == "eap") && len(func() []any { l, _ := w["Auth_servers"].([]any); return l }()) == 0 {
		v.fail("firmware_rule", "wlan.radius.auth", path+".Auth_servers", "Enterprise security needs at least 1 RADIUS server")
	}
}

func validateRadios(v *validator, root map[string]any, in Input, bandsWithWLAN map[string]bool) {
	radios, _ := root["Radios"].([]any)
	count := map[string]int{}
	for _, element := range radios {
		radio, ok := element.(map[string]any)
		if !ok {
			continue
		}
		count[stringOf(radio, "Band")]++
	}
	if count["2.4"] != 1 || count["5"] != 1 {
		v.fail("firmware_rule", "site.rf.managed", "Radios", "the config needs exactly one 2.4 GHz and one 5 GHz radio")
	}
	managed := in.Site.RF.Managed
	domain, _ := regdomain.ForCountry(in.Site.Country)
	ccrev, _ := regdomain.CCRev(in.Site.Country)
	for _, element := range radios {
		radio, ok := element.(map[string]any)
		if !ok {
			continue
		}
		band := stringOf(radio, "Band")
		if band != "2.4" && band != "5" {
			continue
		}
		path := "Radios[Band=" + band + "]"
		disabled := boolOf(radio, "Disabled")
		if !disabled && !bandsWithWLAN[band] {
			v.warn("no_wlan_on_band", bandOption(band, "enabled"), path, "%s GHz is enabled but no WLAN uses it", band)
		}
		if preamble := intOf(radio, "PreambleLength"); preamble < 0 || preamble > 2 {
			v.fail("firmware_rule", bandOption(band, "preamble"), path+".PreambleLength", "PreambleLength must be 0 to 2")
		}
		if taf := intOf(radio, "TAFMode"); taf < 0 || taf > 2 {
			v.fail("firmware_rule", bandOption(band, "taf"), path+".TAFMode", "TAFMode must be 0 to 2")
		}
		masks := map[int]bool{0: true, 1: true, 3: true, 7: true, 15: true}
		if band == "2.4" {
			masks = map[int]bool{0: true, 1: true, 3: true}
		}
		for _, key := range []string{"RxChain", "TxChain"} {
			if !masks[intOf(radio, "ChainSelectMode", key)] {
				v.fail("firmware_rule", bandOption(band, "antenna"), path+".ChainSelectMode."+key, "invalid chain mask for %s GHz", band)
			}
		}
		if !managed || disabled {
			continue
		}
		power := intOf(radio, "Power")
		if power == 0 {
			power = intOf(radio, "RRM", "Power")
		}
		if power < 1 || power > 25 {
			v.fail("firmware_rule", apBandOption(band, "power_dbm"), path+".Power", "an enabled radio needs a power of 1 to 25 dBm (got %d)", power)
		} else if power > 20 {
			v.warn("power_above_20", apBandOption(band, "power_dbm"), path+".Power", "%s GHz power %d dBm is above 20 dBm", band, power)
		}
		channel := intOf(radio, "Channel")
		if channel == 0 {
			channel = intOf(radio, "RRM", "Channel")
		}
		if channel == 0 {
			v.fail("firmware_rule", apBandOption(band, "channel"), path+".Channel", "an enabled radio needs a channel (Channel and RRM.Channel are both 0)")
			continue
		}
		if stringOf(radio, "CCRev") != ccrev || ccrev == "" {
			v.fail("firmware_rule", "site.country", path+".CCRev", "the radio's ccrev must be %s", ccrev)
		}
		width := intOf(radio, "RRM", "Width")
		if width == 0 {
			width = intOf(radio, "Width")
		}
		if domain == nil {
			continue
		}
		table := domain.Table(band, width)
		if table == nil {
			v.fail("firmware_rule", bandOption(band, "width"), path+".Width", "%d MHz is not available on %s GHz under %s", width, band, ccrev)
			continue
		}
		dfs, valid := domain.Valid(band, width, channel)
		if !valid {
			v.fail("firmware_rule", apBandOption(band, "channel"), path+".Channel", "channel %d is not valid at %d MHz under %s", channel, width, ccrev)
			continue
		}
		if dfs {
			if !in.Site.RF.Band5.DFS {
				v.fail("firmware_rule", "site.rf.band_5.dfs", path+".Channel", "channel %d needs DFS, which is off", channel)
			}
			specs, _ := valueAt(radio, "DFSjumplist", "ChanSpecs")
			if list, _ := specs.([]any); len(list) == 0 {
				v.fail("firmware_rule", "site.rf.band_5.dfs_fallback", path+".DFSjumplist", "a DFS channel needs a fallback channel")
			}
		}
	}
}
