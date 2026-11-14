package effective

import (
	"bytes"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/options/firmware"
	"golang.org/x/crypto/ssh"
)

// pruneZero applies rule 2a below v (design §4.5): ep-control decodes into a
// freshly zeroed EPConfig, so for value kinds (bool, number, string, value
// struct), slices and maps a zero value is the same as an absent key and is
// removed. Pointer fields are never removed (nil and a zero struct differ);
// opaque values (own decoders, unknown named kinds, raw JSON) are never
// zero. It reports whether v itself is zero.
func pruneZero(v *value, node *firmware.Node) bool {
	if v == nil {
		return true
	}
	if node.Kind == firmware.KindPointer {
		if v.kind != '0' {
			pruneValue(v, node, node.Pointee)
		}
		return false
	}
	return pruneValue(v, node, node.Kind)
}

func pruneValue(v *value, node *firmware.Node, kind firmware.Kind) bool {
	switch kind {
	case firmware.KindStruct:
		if v.kind != 'o' {
			return false
		}
		for key, child := range v.obj {
			field, ok := node.Field(key)
			if !ok || field.Name != key {
				continue
			}
			if pruneZero(child, field) && field.Kind != firmware.KindPointer {
				delete(v.obj, key)
			}
		}
		return len(v.obj) == 0
	case firmware.KindSlice:
		if v.kind == '0' {
			return true
		}
		if v.kind != 'a' {
			return false
		}
		for _, elem := range v.arr {
			pruneZero(elem, node.Elem)
		}
		return len(v.arr) == 0
	case firmware.KindArray:
		if v.kind != 'a' {
			return false
		}
		zero := true
		for _, elem := range v.arr {
			if !pruneZero(elem, node.Elem) {
				zero = false
			}
		}
		return zero
	case firmware.KindMap:
		if v.kind == '0' {
			return true
		}
		if v.kind != 'o' {
			return false
		}
		for _, child := range v.obj {
			pruneZero(child, node.Elem)
		}
		return len(v.obj) == 0
	case firmware.KindBool:
		return v.kind == 'b' && !v.b
	case firmware.KindInt, firmware.KindUint, firmware.KindFloat:
		return v.kind == 'n' && v.num == "0"
	case firmware.KindString:
		return v.kind == 's' && v.str == ""
	}
	return false
}

// AKM flag names.
const (
	akmWPA2PSK       = "WPA2_PSK"
	akmWPA2PSKSHA256 = "WPA2_PSK_SHA256"
	akmWPA2PSKFT     = "WPA2_PSK_FT"
	akmWPA2EAP       = "WPA2_EAP"
	akmWPA2EAPSHA256 = "WPA2_EAP_SHA256"
	akmWPA2EAPFT     = "WPA2_EAP_FT"
	akmWPAPSK        = "WPA_PSK"
	akmWPA3PSK       = "WPA3_PSK"
	akmWPA3PSKFT     = "WPA3_PSK_FT"
	akmWPA3OWE       = "WPA3_OWE"
	akmWPA3EAP       = "WPA3_EAP"
	akmWPA3EAPFT     = "WPA3_EAP_FT"
	akmWPA3SuiteB    = "WPA3_SUITEB"
	akmWPA3SAEExt    = "WPA3_PSK_SAE_EXT"
	akmWPA3FTSAEExt  = "WPA3_PSK_FT_SAE_EXT"
)

func setFlag(w *value, on bool, keys ...string) {
	if on {
		w.set(boolValue(true), keys...)
	} else {
		w.del(keys...)
	}
}

// prepareWLANs applies rule 3, the WLAN part of EPConfig.Prepare
// ([WLAN §3], 0.15 disassembly). It runs after rule 2a, so an absent member
// is a zero one. Where the catalogue leaves a combination open the step is
// skipped and the report says prepare_partial.
func prepareWLANs(root *value, report *Report) {
	wlans := root.get("WLANs")
	if wlans == nil || wlans.kind != 'a' {
		return
	}
	for _, wlan := range wlans.arr {
		if wlan != nil && wlan.kind == 'o' {
			prepareWLAN(wlan, report)
		}
	}
}

func prepareWLAN(w *value, report *Report) {
	akm := func(name string) bool { return w.boolAt("Auth", "AKM", name) }
	setAKM := func(name string, on bool) { setFlag(w, on, "Auth", "AKM", name) }

	// Step 1: RoamMode and FbtHandler.
	roam, roamOK := w.stringAt("RoamMode")
	if roamOK && roam == "" {
		roam = "none"
		w.set(strValue(roam), "RoamMode")
	}
	if fbt, ok := w.stringAt("FbtHandler"); ok && fbt != "Driver" {
		w.set(strValue("Pace"), "FbtHandler")
	}
	r11 := roam == "11r"

	authType, typeOK := w.stringAt("Auth", "Type")
	switch authType {
	case "psk", "eap", "owe", "open", "wep":
	default:
		typeOK = false
	}
	partial := !typeOK || !roamOK

	if !partial {
		// Step 2: AKM defaulting when every flag is false.
		if akmBlock := w.get("Auth", "AKM"); akmBlock == nil || (akmBlock.kind == 'o' && len(akmBlock.obj) == 0) {
			switch authType {
			case "psk":
				setAKM(map[bool]string{true: akmWPA2PSKFT, false: akmWPA2PSK}[r11], true)
			case "eap":
				setAKM(map[bool]string{true: akmWPA2EAPFT, false: akmWPA2EAP}[r11], true)
			}
		}
		// Step 3: AKM completion.
		switch authType {
		case "psk":
			transition := akm(akmWPA2PSK) && akm(akmWPA3PSK)
			wpa3Only := akm(akmWPA3PSK) && !akm(akmWPA2PSK)
			anyWPA3 := akm(akmWPA3PSK) || akm(akmWPA3PSKFT) || akm(akmWPA3SAEExt) || akm(akmWPA3FTSAEExt) ||
				akm(akmWPA3OWE) || akm(akmWPA3EAP) || akm(akmWPA3EAPFT) || akm(akmWPA3SuiteB)
			switch {
			case r11 && transition:
				setAKM(akmWPA2PSKFT, true)
				setAKM(akmWPA2PSKSHA256, true)
				setAKM(akmWPA3PSKFT, true)
			case r11 && wpa3Only:
				setAKM(akmWPA3PSKFT, true)
				setAKM(akmWPA2PSKFT, false)
			case r11:
				setAKM(akmWPA2PSKFT, true)
				if !akm(akmWPA2PSKSHA256) {
					setAKM(akmWPA2PSK, true)
				}
			case !anyWPA3 && !akm(akmWPA2PSKSHA256) && !akm(akmWPAPSK):
				setAKM(akmWPA2PSK, true)
			}
		case "eap":
			if r11 {
				// "adds the FT AKMs": which ones is not pinned down.
				partial = true
			} else if akm(akmWPA3EAP) {
				setAKM(akmWPA2EAPSHA256, true)
			} else {
				setAKM(akmWPA2EAP, true)
			}
		case "owe":
			setAKM(akmWPA3OWE, true)
		}
	}

	pskTransition := akm(akmWPA2PSK) && akm(akmWPA3PSK)
	eapWPA3 := akm(akmWPA3EAP) || akm(akmWPA3EAPFT) || akm(akmWPA3SuiteB)
	// Step 3 itself adds WPA2_EAP_SHA256 to every WPA3_EAP WLAN, so only
	// plain WPA2-EAP AKMs next to WPA3 ones make an EAP "transition" whose
	// PMF rule the catalogue does not pin down.
	eapWPA2 := akm(akmWPA2EAP) || akm(akmWPA2EAPFT)
	if eapWPA3 && (eapWPA2 || pskTransition) {
		partial = true
	}
	if !partial {
		// Step 4: PMF from AKM.
		switch {
		case pskTransition:
			w.set(boolValue(true), "Dot11w", "Enabled")
			w.del("Dot11w", "Required")
		case (akm(akmWPA3PSK) && !akm(akmWPA2PSK)) || eapWPA3:
			w.set(boolValue(true), "Dot11w", "Enabled")
			w.set(boolValue(true), "Dot11w", "Required")
		}
		// Step 5: SAE-EXT forces H2E only.
		if akm(akmWPA3SAEExt) || akm(akmWPA3FTSAEExt) {
			w.set(boolValue(true), "Auth", "SAEHash2ElementOnly")
		}
		// Step 6: default pairwise cipher.
		if pairwise := w.get("Auth", "Pairwise"); (authType == "psk" || authType == "eap" || authType == "owe") &&
			(pairwise == nil || (pairwise.kind == 'o' && len(pairwise.obj) == 0)) {
			if authType == "eap" && akm(akmWPA3SuiteB) {
				w.set(boolValue(true), "Auth", "Pairwise", "GCMP256")
			} else {
				w.set(boolValue(true), "Auth", "Pairwise", "CCMP")
			}
		}
		// Step 7: transition mode Default (1) for WPA3 or OWE transition,
		// only when unset.
		if pskTransition || w.boolAt("OWE", "Transition") {
			if mode, ok := w.intAt("TransitionMode"); ok && mode == 0 {
				w.set(intValue(1), "TransitionMode")
			}
		}
	} else {
		report.note(NotePrepareSkipped)
	}

	// Step 8: Dot11wConfig.prepare.
	if typeOK && authType == "owe" {
		w.set(boolValue(true), "Dot11w", "Enabled")
		w.set(boolValue(true), "Dot11w", "Required")
	}
	if w.boolAt("Dot11w", "Required") && !w.boolAt("Dot11w", "Enabled") {
		w.set(boolValue(true), "Dot11w", "Enabled")
	}
	if w.boolAt("Dot11w", "Enabled") {
		clamp(w, 1, 3, 3, "Dot11w", "SA_Query_Attempts")
		clamp(w, 100, 300, 200, "Dot11w", "SA_Query_Timeout")
	}
	// Steps 9 and 10: range clamps.
	clamp(w, 2, 512, 512, "MaxStaAllowed")
	clamp(w, 16, 32, 16, "Auth", "SAEAntiCloggTokenThreshold")
	clamp(w, 1, 3, 1, "Auth", "GTKRetries")
	// Step 11: KeyCacheConfig.prepare.
	if !w.boolAt("KeyCache", "WLCkeyCache") && !w.boolAt("KeyCache", "LocalAPkeyCache") {
		w.set(boolValue(true), "KeyCache", "WLCkeyCache")
	}
	lookupDefaults := func(name string, attempts, timeout, window int64) {
		for _, field := range []struct {
			key string
			def int64
		}{{"NumAttempts", attempts}, {"TimeoutMsec", timeout}, {"IgnoreKeyErrorWindow", window}} {
			if current, ok := w.intAt("KeyCache", name, field.key); ok && current == 0 {
				w.set(intValue(field.def), "KeyCache", name, field.key)
			}
		}
	}
	if w.boolAt("KeyCache", "FT", "IgnoreKeyError") {
		lookupDefaults("FT", 3, 200, 5)
	}
	if w.boolAt("KeyCache", "OKC", "IgnoreKeyError") {
		lookupDefaults("OKC", 3, 600, 5)
	}
	if !w.boolAt("KeyCache", "RPPSK", "IgnoreKeyError") {
		lookupDefaults("RPPSK", 1, 100, 2)
	}
	// Step 12: SaePweMode on SAE WLANs.
	sae := akm(akmWPA3PSK) || akm(akmWPA3PSKFT) || akm(akmWPA3SAEExt) || akm(akmWPA3FTSAEExt)
	if !partial && sae {
		h2e := akm(akmWPA3SAEExt) || akm(akmWPA3FTSAEExt) || w.boolAt("Auth", "TransitionDisableIndication")
		if configs := w.get("BandConfigs"); configs != nil && configs.kind == 'a' {
			for _, config := range configs.arr {
				if config == nil || config.kind != 'o' {
					continue
				}
				mode, ok := config.intAt("SaePweMode")
				if !ok || (mode >= 1 && mode <= 3) {
					continue
				}
				band, _ := config.stringAt("Band")
				if h2e || band == "6" {
					config.set(intValue(2), "SaePweMode")
				} else {
					config.set(intValue(3), "SaePweMode")
				}
			}
		}
	}
}

// clamp replaces a number outside [min, max] (absent counts as 0) with def.
func clamp(w *value, min, max, def int64, keys ...string) {
	current, ok := w.intAt(keys...)
	if !ok {
		return
	}
	if current < min || current > max {
		w.set(intValue(def), keys...)
	}
}

// resolveBSSIDs applies rule 4, apply.getVapIdx [WLAN §2]: per band, in
// WLAN array order, a requested index that is valid (1 to 15) and free is
// kept, otherwise the first free index from 1 is used, and the resolved
// index replaces the requested one. Whether a disabled WLAN takes an index
// is unverified (§9 Q8), so a disabled WLAN ahead of an enabled one leaves
// every index unresolved and reports bssid_unresolved, as do OWE transition
// companions and more than 15 WLANs on a band.
func resolveBSSIDs(root *value, report *Report) {
	wlans := root.get("WLANs")
	if wlans == nil || wlans.kind != 'a' {
		return
	}
	type entry struct {
		wlan  *value
		bands []string
	}
	var enabled []entry
	seenDisabled := false
	for _, wlan := range wlans.arr {
		if wlan == nil || wlan.kind != 'o' {
			report.note(NoteBSSIDUnresolved)
			return
		}
		if !wlan.boolAt("Enabled") {
			seenDisabled = true
			continue
		}
		if seenDisabled || wlan.boolAt("OWE", "Transition") {
			report.note(NoteBSSIDUnresolved)
			return
		}
		bandsValue := wlan.get("Bands")
		if bandsValue == nil || bandsValue.kind != 'a' || len(bandsValue.arr) == 0 {
			report.note(NoteBSSIDUnresolved)
			return
		}
		var bands []string
		for _, band := range bandsValue.arr {
			name, ok := band.asString()
			if !ok {
				report.note(NoteBSSIDUnresolved)
				return
			}
			bands = append(bands, name)
		}
		enabled = append(enabled, entry{wlan: wlan, bands: bands})
	}
	resolved := make(map[*value]map[string]int64)
	for _, band := range []string{"2.4", "5"} {
		taken := map[int64]bool{}
		for _, e := range enabled {
			if !containsString(e.bands, band) {
				continue
			}
			config := bandConfig(e.wlan, band)
			requested := int64(0)
			if config != nil {
				var ok bool
				if requested, ok = config.intAt("BSSIDIndex"); !ok {
					report.note(NoteBSSIDUnresolved)
					return
				}
			}
			index := int64(0)
			if requested >= 1 && requested <= 15 && !taken[requested] {
				index = requested
			} else {
				for candidate := int64(1); candidate <= 15; candidate++ {
					if !taken[candidate] {
						index = candidate
						break
					}
				}
			}
			if index == 0 {
				report.note(NoteBSSIDUnresolved)
				return
			}
			taken[index] = true
			if resolved[e.wlan] == nil {
				resolved[e.wlan] = map[string]int64{}
			}
			resolved[e.wlan][band] = index
		}
	}
	for wlan, bands := range resolved {
		for band, index := range bands {
			config := bandConfig(wlan, band)
			if config == nil {
				configs := wlan.get("BandConfigs")
				if configs == nil || configs.kind != 'a' {
					configs = &value{kind: 'a'}
					wlan.set(configs, "BandConfigs")
				}
				config = newObject()
				config.set(strValue(band), "Band")
				configs.arr = append(configs.arr, config)
			}
			config.set(intValue(index), "BSSIDIndex")
		}
	}
}

func bandConfig(wlan *value, band string) *value {
	configs := wlan.get("BandConfigs")
	if configs == nil || configs.kind != 'a' {
		return nil
	}
	for _, config := range configs.arr {
		if config == nil || config.kind != 'o' {
			continue
		}
		if name, ok := config.stringAt("Band"); ok && name == band {
			return config
		}
	}
	return nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// resolveRadios applies rule 5 [RF §3]: per 2.4 and 5 GHz radio,
// channel = Channel || RRM.Channel || 1 (2.4) / 36 (5); width = RRM.Width ||
// Width, or 20 when the channel was defaulted; power = Power || RRM.Power,
// capped at 25 (0 stays 0); ccrev = CCRev || "US/0". The raw fields are
// replaced by {channel, width, power, ccrev}. Monitor.CCRev becomes
// Monitor.CCRev || "US/0".
func resolveRadios(root *value, report *Report) {
	if radios := root.get("Radios"); radios != nil && radios.kind == 'a' {
		for _, radio := range radios.arr {
			if radio == nil || radio.kind != 'o' {
				continue
			}
			band, ok := radio.stringAt("Band")
			if !ok || (band != "2.4" && band != "5") {
				continue
			}
			channel, ok1 := radio.intAt("Channel")
			rrmChannel, ok2 := radio.intAt("RRM", "Channel")
			width, ok3 := radio.intAt("Width")
			rrmWidth, ok4 := radio.intAt("RRM", "Width")
			power, ok5 := radio.intAt("Power")
			rrmPower, ok6 := radio.intAt("RRM", "Power")
			ccrev, ok7 := radio.stringAt("CCRev")
			if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) {
				report.note(NoteRadioUnresolved)
				continue
			}
			resolvedChannel := channel
			if resolvedChannel == 0 {
				resolvedChannel = rrmChannel
			}
			defaulted := resolvedChannel == 0
			if defaulted {
				resolvedChannel = map[string]int64{"2.4": 1, "5": 36}[band]
			}
			resolvedWidth := rrmWidth
			if resolvedWidth == 0 {
				resolvedWidth = width
			}
			if defaulted {
				resolvedWidth = 20
			}
			resolvedPower := power
			if resolvedPower == 0 {
				resolvedPower = rrmPower
			}
			if resolvedPower > 25 {
				resolvedPower = 25
			}
			if ccrev == "" {
				ccrev = "US/0"
			}
			for _, key := range []string{"Channel", "Width", "Power", "CCRev"} {
				delete(radio.obj, key)
			}
			if rrm := radio.get("RRM"); rrm != nil && rrm.kind == 'o' {
				for _, key := range []string{"Channel", "Width", "Power"} {
					delete(rrm.obj, key)
				}
				if len(rrm.obj) == 0 {
					delete(radio.obj, "RRM")
				}
			}
			radio.obj["channel"] = intValue(resolvedChannel)
			radio.obj["width"] = intValue(resolvedWidth)
			radio.obj["power"] = intValue(resolvedPower)
			radio.obj["ccrev"] = strValue(ccrev)
		}
	}
	if ccrev, ok := root.stringAt("Monitor", "CCRev"); ok {
		if ccrev == "" {
			ccrev = "US/0"
		}
		root.set(strValue(ccrev), "Monitor", "CCRev")
	}
}

// canonicalSets applies rule 6: keyed arrays sorted by key (Radios by
// Band, WLANs by ID, SwitchPorts by Name, SVIs by Vlan_ID), and value sets
// compared as sets (SwitchPorts[].Vlans, WLANs[].Vlans after the first
// entry, SSH.AuthorizedKeys by key blob without comments).
func canonicalSets(root *value) {
	sortByKey(root.get("Radios"), "Band")
	sortByKey(root.get("WLANs"), "ID")
	sortByKey(root.get("SwitchPorts"), "Name")
	sortByKey(root.get("SVIs"), "Vlan_ID")
	if ports := root.get("SwitchPorts"); ports != nil && ports.kind == 'a' {
		for _, port := range ports.arr {
			if port != nil && port.kind == 'o' {
				if vlans := port.get("Vlans"); vlans != nil && vlans.kind == 'a' {
					vlans.arr = stringSet(vlans.arr)
				}
			}
		}
	}
	if wlans := root.get("WLANs"); wlans != nil && wlans.kind == 'a' {
		for _, wlan := range wlans.arr {
			if wlan == nil || wlan.kind != 'o' {
				continue
			}
			if vlans := wlan.get("Vlans"); vlans != nil && vlans.kind == 'a' && len(vlans.arr) > 1 {
				vlans.arr = append(vlans.arr[:1:1], stringSet(vlans.arr[1:])...)
			}
		}
	}
	if keys := root.get("SSH", "AuthorizedKeys"); keys != nil && keys.kind == 'a' {
		for index, key := range keys.arr {
			if key == nil || key.kind != 's' {
				continue
			}
			if parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key.str)); err == nil {
				keys.arr[index] = strValue(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed))))
			}
		}
		keys.arr = stringSet(keys.arr)
	}
}

// sortByKey stably sorts an array of objects by the JSON form of a member.
func sortByKey(array *value, key string) {
	if array == nil || array.kind != 'a' {
		return
	}
	sortKey := func(v *value) string {
		if v == nil || v.kind != 'o' || v.obj[key] == nil {
			return ""
		}
		var buf bytes.Buffer
		v.obj[key].marshal(&buf)
		return buf.String()
	}
	sort.SliceStable(array.arr, func(i, j int) bool { return sortKey(array.arr[i]) < sortKey(array.arr[j]) })
}

// stringSet sorts and de-duplicates values by their JSON form.
func stringSet(values []*value) []*value {
	type item struct {
		key string
		v   *value
	}
	items := make([]item, 0, len(values))
	for _, v := range values {
		var buf bytes.Buffer
		if v == nil {
			buf.WriteString("null")
		} else {
			v.marshal(&buf)
		}
		items = append(items, item{key: buf.String(), v: v})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].key < items[j].key })
	out := make([]*value, 0, len(items))
	for i, it := range items {
		if i > 0 && it.key == items[i-1].key {
			continue
		}
		out = append(out, it.v)
	}
	return out
}
