package compile

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/options"
)

// Dry-run impact (design §4.8): the per-AP diff between the config an AP
// runs and the one a mutation would give it, with secrets redacted, and
// the disruption classes of the changed paths from the option registry.

// DiffEntry is one changed leaf ("[changed]" stands for a secret).
type DiffEntry struct {
	Path string `json:"path"`
	From any    `json:"from"`
	To   any    `json:"to"`
}

// APImpact is the dry-run impact on one AP.
type APImpact struct {
	Disruption []string    `json:"disruption"`
	Diff       []DiffEntry `json:"diff"`
}

// Impact compares an AP's old and new compiled config. Paths are
// concrete (WLANs[ID=…], Radios[Band=…], SwitchPorts[Name=…]); Timestamp
// and keys 0.15 ignores are left out.
func Impact(oldConfig, newConfig []byte) (APImpact, error) {
	impact := APImpact{Disruption: []string{}, Diff: []DiffEntry{}}
	oldTree, err := FoldConfig(oldConfig)
	if err != nil {
		return impact, err
	}
	newTree, err := FoldConfig(newConfig)
	if err != nil {
		return impact, err
	}
	delete(oldTree, "Timestamp")
	delete(newTree, "Timestamp")
	var walk func(a, b any, concrete, pattern string, secret bool)
	walk = func(a, b any, concrete, pattern string, secret bool) {
		ao, aok := a.(map[string]any)
		bo, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for key := range ao {
				keys[key] = true
			}
			for key := range bo {
				keys[key] = true
			}
			ordered := make([]string, 0, len(keys))
			for key := range keys {
				ordered = append(ordered, key)
			}
			sort.Strings(ordered)
			for _, key := range ordered {
				walk(ao[key], bo[key], joinPattern(concrete, key), joinPattern(pattern, key), secret || IsSecretKey(key))
			}
			return
		}
		aa, aaok := a.([]any)
		ba, baok := b.([]any)
		if aaok && baok {
			if key := keyOf(pattern); key != "" {
				seen := map[string]bool{}
				for _, element := range ba {
					object, _ := element.(map[string]any)
					id := keyString(object[key])
					seen[id] = true
					match, _ := keyedElement(aa, key, id)
					var from any
					if match != nil {
						from = match
					}
					walk(from, object, concrete+"["+key+"="+id+"]", pattern+"[]", secret)
				}
				for _, element := range aa {
					object, _ := element.(map[string]any)
					if id := keyString(object[key]); !seen[id] {
						walk(object, nil, concrete+"["+key+"="+id+"]", pattern+"[]", secret)
					}
				}
				return
			}
			if pattern == "WLANs" {
				byID := func(list []any) map[string]any {
					out := map[string]any{}
					for _, element := range list {
						if object, ok := element.(map[string]any); ok {
							out[keyString(object["ID"])] = object
						}
					}
					return out
				}
				oldWLANs, newWLANs := byID(aa), byID(ba)
				ids := map[string]bool{}
				for id := range oldWLANs {
					ids[id] = true
				}
				for id := range newWLANs {
					ids[id] = true
				}
				ordered := make([]string, 0, len(ids))
				for id := range ids {
					ordered = append(ordered, id)
				}
				sort.Strings(ordered)
				for _, id := range ordered {
					walk(oldWLANs[id], newWLANs[id], "WLANs[ID="+id+"]", "WLANs[]", secret)
				}
				return
			}
		}
		if equalTree(a, b) {
			return
		}
		entry := DiffEntry{Path: concrete, From: a, To: b}
		if secret {
			entry.From, entry.To = "[changed]", "[changed]"
		}
		impact.Diff = append(impact.Diff, entry)
		impact.Disruption = append(impact.Disruption, string(disruptionOf(pattern)))
	}
	walk(oldTree, newTree, "", "", false)
	impact.Disruption = sortedUniqueStrings(impact.Disruption)
	if impact.Disruption == nil {
		impact.Disruption = []string{}
	}
	return impact, nil
}

// disruptionOf maps a changed firmware path pattern to a disruption class:
// the most disruptive option that declares it, else by area (any WLAN
// field restarts the BSS until the Lab measures otherwise, §9 Q4).
func disruptionOf(pattern string) options.Disruption {
	result := options.DisruptionNone
	matched := false
	for _, option := range options.Default().All() {
		for _, path := range option.FirmwarePaths {
			if pattern == path || strings.HasPrefix(pattern, path+".") || strings.HasPrefix(pattern, path+"[]") {
				result = options.Union(result, option.Disruption)
				matched = true
			}
		}
	}
	if matched {
		return result
	}
	switch {
	case strings.HasPrefix(pattern, "WLANs"):
		return options.DisruptionWLANRestart
	case strings.HasPrefix(pattern, "Radios") || pattern == "Country":
		return options.DisruptionRadioBounce
	case strings.HasPrefix(pattern, "SVIs") || strings.HasPrefix(pattern, "SwitchPorts"):
		return options.DisruptionStrandingRisk
	}
	return options.DisruptionNone
}

// MarshalDiff renders a diff for a response.
func MarshalDiff(diff []DiffEntry) json.RawMessage {
	raw, _ := json.Marshal(diff)
	return raw
}
