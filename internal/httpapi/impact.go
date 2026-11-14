package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// keyedArrays are the arrays diffed element by element, by the key named
// (design §4.4 "Keyed merge"); WLANs are keyed by ID and shown by SSID.
var keyedArrays = map[string]string{
	"Radios": "Band", "SwitchPorts": "Name", "SVIs": "Vlan_ID",
	"WLANs": "ID", "BandConfigs": "Band", "RateSets": "Band",
}

// diffConfigs returns the leaf-level differences between two compiled
// configs, ignoring Timestamp and SiteName, with secrets shown as
// "[changed]" (design §4.8: dry-run diffs are redacted).
func diffConfigs(before, after []byte) []api.DiffEntry {
	old, _ := decodeConfig(before)
	fresh, _ := decodeConfig(after)
	delete(old, "Timestamp")
	delete(fresh, "Timestamp")
	delete(old, "SiteName")
	delete(fresh, "SiteName")
	var result []api.DiffEntry
	diffValue("", "", old, fresh, false, &result)
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func decodeConfig(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	return decodeObject(raw)
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// secretKey reports whether a key holds a secret (the redactValue rules).
func secretKey(key string) bool {
	redacted, _ := redactValue(map[string]any{key: "x"}).(map[string]any)
	return redacted[key] == "[redacted]"
}

func diffValue(path, key string, before, after any, secret bool, out *[]api.DiffEntry) {
	secret = secret || (key != "" && secretKey(key))
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap && afterIsMap {
		keys := map[string]bool{}
		for k := range beforeMap {
			keys[k] = true
		}
		for k := range afterMap {
			keys[k] = true
		}
		for k := range keys {
			diffValue(joinPath(path, k), k, beforeMap[k], afterMap[k], secret, out)
		}
		return
	}
	beforeList, beforeIsList := before.([]any)
	afterList, afterIsList := after.([]any)
	if field, keyed := keyedArrays[key]; keyed && (beforeIsList || before == nil) && (afterIsList || after == nil) {
		if diffKeyed(path, field, beforeList, afterList, secret, out) {
			return
		}
	}
	if sameJSON(before, after) && (before == nil) == (after == nil) {
		return
	}
	if bothNil := before == nil && after == nil; bothNil {
		return
	}
	entry := api.DiffEntry{Path: path, From: before, To: after}
	if secret {
		entry.From, entry.To = "[changed]", "[changed]"
		if before == nil {
			entry.From = nil
		}
		if after == nil {
			entry.To = nil
		}
	} else {
		entry.From, entry.To = redactValue(before), redactValue(after)
	}
	*out = append(*out, entry)
}

// diffKeyed diffs two arrays of objects by an element key. It returns
// false when an element lacks the key (the caller compares the arrays
// whole).
func diffKeyed(path, field string, before, after []any, secret bool, out *[]api.DiffEntry) bool {
	index := func(list []any) (map[string]map[string]any, []string, bool) {
		result := map[string]map[string]any{}
		var order []string
		for _, item := range list {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, nil, false
			}
			keyValue, ok := object[field]
			if !ok {
				return nil, nil, false
			}
			name := fmt.Sprint(normaliseNumbers(keyValue))
			result[name] = object
			order = append(order, name)
		}
		return result, order, true
	}
	old, oldOrder, ok1 := index(before)
	fresh, freshOrder, ok2 := index(after)
	if !ok1 || !ok2 {
		return false
	}
	label := func(name string, object map[string]any) string {
		if ssid, ok := object["SSID"].(string); ok && ssid != "" {
			return fmt.Sprintf("%s[%s]", path, ssid)
		}
		return fmt.Sprintf("%s[%s]", path, name)
	}
	seen := map[string]bool{}
	for _, name := range append(oldOrder, freshOrder...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		a, inOld := old[name]
		b, inNew := fresh[name]
		object := b
		if object == nil {
			object = a
		}
		elementPath := label(name, object)
		switch {
		case inOld && inNew:
			diffValue(elementPath, "", a, b, secret, out)
		case inNew:
			*out = append(*out, api.DiffEntry{Path: elementPath, From: nil, To: redactValue(b)})
		default:
			*out = append(*out, api.DiffEntry{Path: elementPath, From: redactValue(a), To: nil})
		}
	}
	return true
}

// disruptionOf classifies one diff path (design P6). Every WLAN field is a
// WLAN restart until the Lab measures otherwise (§9 Q4); a country or
// ccrev change bounces every radio.
func disruptionOf(path string) api.Disruption {
	top, _, _ := strings.Cut(path, ".")
	top, _, _ = strings.Cut(top, "[")
	switch top {
	case "WLANs":
		return api.DisruptionWLANRestart
	case "Country":
		return api.DisruptionAllRadiosBounce
	case "Radios":
		if strings.Contains(path, ".CCRev") {
			return api.DisruptionAllRadiosBounce
		}
		return api.DisruptionRadioBounce
	case "Monitor":
		if strings.Contains(path, "CCRev") || strings.HasSuffix(path, "Monitor.Enabled") {
			return api.DisruptionRadioBounce
		}
	case "SVIs", "SwitchPorts", "APRecovery", "Cloud", "DNS", "IP":
		if top == "DNS" {
			return api.DisruptionNone
		}
		return api.DisruptionStrandingRisk
	}
	return api.DisruptionNone
}

var disruptionRank = map[api.Disruption]int{
	api.DisruptionNone: 0, api.DisruptionWLANRestart: 1, api.DisruptionRadioBounce: 2,
	api.DisruptionAllRadiosBounce: 3, api.DisruptionStrandingRisk: 4,
}

func worst(classes ...api.Disruption) api.Disruption {
	result := api.DisruptionNone
	for _, class := range classes {
		if disruptionRank[class] > disruptionRank[result] {
			result = class
		}
	}
	return result
}

// impact describes what a mutation does to the APs it changes (design
// §4.8): one entry per changed AP with its disruption and, on dry runs,
// its redacted diff; a summary line; and the compile warnings.
func (s *Server) impact(previous, current model.State, applied compile.Applied, withDiff bool) (api.Impact, []api.Warning) {
	impact := api.Impact{APs: []api.ImpactAP{}, Disruption: api.DisruptionNone}
	var warnings []api.Warning
	seenWarning := map[string]bool{}
	for _, result := range applied.Result.APs {
		for _, warning := range result.Output.Warnings {
			key := warning.Code + "\x00" + warning.Message
			if seenWarning[key] {
				continue
			}
			seenWarning[key] = true
			warnings = append(warnings, api.Warning{Code: warning.Code, Message: warning.Message})
		}
	}
	wlanRestarts := map[string]int{}
	radioBands := map[string]int{}
	allRadios, stranding, quiet := 0, 0, 0
	clientsOnWLAN := map[string]int{}
	for _, push := range applied.Pushes {
		ap := findAP(current, push.APID)
		if ap == nil {
			continue
		}
		var before []byte
		if input, err := compile.InputFor(s.base, previous, push.APID); err == nil {
			if output, err := compile.AP(input); err == nil {
				before = output.Config
			}
		}
		diff := diffConfigs(before, push.Config)
		classes := map[api.Disruption]bool{}
		for _, entry := range diff {
			class := disruptionOf(entry.Path)
			if before == nil {
				class = api.DisruptionAllRadiosBounce
			}
			classes[class] = true
			switch class {
			case api.DisruptionWLANRestart:
				wlanRestarts[wlanLabel(entry.Path)]++
			case api.DisruptionRadioBounce:
				radioBands[radioLabel(entry.Path)]++
			}
		}
		obs := s.observe(push.APID)
		if obs.fresh(ap.ConfigUpdatedAt) {
			for _, vap := range obs.telemetry.VAPs {
				clientsOnWLAN[vap.SSID] += vap.Clients
			}
		}
		list := make([]api.Disruption, 0, len(classes))
		for class := range classes {
			list = append(list, class)
		}
		sort.Slice(list, func(i, j int) bool { return disruptionRank[list[i]] < disruptionRank[list[j]] })
		if len(list) == 0 {
			list = []api.Disruption{api.DisruptionNone}
		}
		apWorst := worst(list...)
		switch apWorst {
		case api.DisruptionAllRadiosBounce:
			allRadios++
		case api.DisruptionStrandingRisk:
			stranding++
		case api.DisruptionNone:
			quiet++
		}
		entry := api.ImpactAP{ID: ap.ID, Name: ap.Name, Connected: obs.status.Connected,
			Push: s.pushMode(*ap, obs), Disruption: list}
		if withDiff {
			if diff == nil {
				diff = []api.DiffEntry{}
			}
			entry.Diff = diff
		}
		impact.APs = append(impact.APs, entry)
		impact.Disruption = worst(impact.Disruption, apWorst)
	}
	impact.Summary = impactSummary(len(impact.APs), wlanRestarts, radioBands, allRadios, stranding, quiet, clientsOnWLAN)
	return impact, warnings
}

func wlanLabel(path string) string {
	start := strings.Index(path, "[")
	end := strings.Index(path, "]")
	if start < 0 || end < start {
		return ""
	}
	return path[start+1 : end]
}

func radioLabel(path string) string {
	if strings.HasPrefix(path, "Monitor") {
		return "scan"
	}
	return wlanLabel(path)
}

func plural(count int, one string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %ss", count, one)
}

// impactSummary is the UI's impact line, for example "WLAN INTERNET
// restarts on 1 AP; about 19 clients reconnect".
func impactSummary(aps int, wlans, bands map[string]int, allRadios, stranding, quiet int, clients map[string]int) string {
	if aps == 0 {
		return "No AP changes"
	}
	var parts []string
	if allRadios > 0 {
		parts = append(parts, fmt.Sprintf("every radio restarts on %s; all clients reconnect", plural(allRadios, "AP")))
	}
	if stranding > 0 {
		parts = append(parts, fmt.Sprintf("management network changes on %s; a wrong value can strand the AP", plural(stranding, "AP")))
	}
	names := make([]string, 0, len(wlans))
	for name := range wlans {
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		line := fmt.Sprintf("WLAN %s restarts on %s", name, plural(wlans[name], "AP"))
		if count := clients[name]; count > 0 {
			line += fmt.Sprintf("; about %s reconnect", plural(count, "client"))
		}
		parts = append(parts, line)
	}
	bandNames := make([]string, 0, len(bands))
	for band := range bands {
		bandNames = append(bandNames, band)
	}
	sort.Strings(bandNames)
	for _, band := range bandNames {
		switch band {
		case "scan":
			parts = append(parts, fmt.Sprintf("scanning radio restarts on %s", plural(bands[band], "AP")))
		case "":
		default:
			parts = append(parts, fmt.Sprintf("%s GHz radio restarts on %s; clients on %s GHz reconnect", band, plural(bands[band], "AP"), band))
		}
	}
	if len(parts) == 0 {
		verb := "gets"
		if aps != 1 {
			verb = "get"
		}
		return fmt.Sprintf("%s %s a new config; no client impact expected", plural(aps, "AP"), verb)
	}
	return strings.ToUpper(parts[0][:1]) + strings.Join(parts, "; ")[1:]
}

// pushMode is how a config reaches an AP: notify when its
// config_change_notify feature is on, else reconnect.
func (s *Server) pushMode(ap model.APRecord, obs apObs) api.PushMode {
	if obs.features[string(controller.FeatureConfigChangeNotify)] {
		return api.PushModeNotify
	}
	features := controller.ResolveFeatures(s.defaultFeatures, ap.Features)
	if features.Has(controller.FeatureConfigChangeNotify) {
		return api.PushModeNotify
	}
	return api.PushModeReconnect
}

// sameBytes compares JSON documents ignoring formatting.
func sameBytes(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	return sameJSON(x, y)
}
