package compile

import (
	"errors"
	"sort"
	"strconv"

	"github.com/nicko170/dismist/internal/compile/effective"
)

// Residual expert patches (design §4.7 step 4): the schema-2 migration
// stores, per scope, a minimal expert patch that turns the typed compile
// into exactly what the AP ran before. These helpers compute the raw diff
// in canonical keys and let the migration prune it leaf by leaf.

// FoldConfig folds a compiled config the way the AP decodes it (normaliser
// rule 1 and unknown keys dropped) and returns it as a tree.
func FoldConfig(config []byte) (map[string]any, error) {
	folded, _, err := effective.Fold(config)
	if err != nil {
		return nil, err
	}
	return decodeObject(folded)
}

// ResidualDiff diffs two folded configs for one scope: the WLAN with the
// given ID for ScopeWLAN, else the whole config without WLANs. Paths an
// expert patch of the scope may not set are dropped and listed.
func ResidualDiff(scope string, typed, legacy map[string]any, networkID string) (map[string]any, []string, error) {
	var from, to any
	pattern := ""
	switch scope {
	case ScopeWLAN:
		pattern = "WLANs[]"
		find := func(root map[string]any) any {
			list, _ := root["WLANs"].([]any)
			element, at := keyedElement(list, "ID", networkID)
			if at < 0 {
				return nil
			}
			return element
		}
		from, to = find(typed), find(legacy)
		if from == nil || to == nil {
			return nil, nil, errors.New("WLAN " + networkID + " missing from a compile")
		}
	case ScopeSite, ScopeAP:
		strip := func(root map[string]any) map[string]any {
			out := make(map[string]any, len(root))
			for key, value := range root {
				if key != "WLANs" {
					out[key] = value
				}
			}
			return out
		}
		from, to = strip(typed), strip(legacy)
	default:
		return nil, nil, errors.New("unknown scope " + scope)
	}
	diff, changed := KeyedDiff(from, to, pattern)
	if !changed {
		return map[string]any{}, nil, nil
	}
	patch, _ := diff.(map[string]any)
	if patch == nil {
		return map[string]any{}, nil, nil
	}
	kept, dropped := stripForbidden(scope, patch, pattern)
	return kept, dropped, nil
}

// stripForbidden removes the forbidden paths of a scope from a patch and
// returns them.
func stripForbidden(scope string, patch map[string]any, root string) (map[string]any, []string) {
	list := forbiddenSiteAP
	if scope == ScopeWLAN {
		list = forbiddenWLAN
	}
	var dropped []string
	var walk func(value any, pattern string) any
	walk = func(value any, pattern string) any {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for _, key := range sortedKeys(v) {
				childPattern := joinPattern(pattern, key)
				if key != RemoveKey {
					if _, forbidden := matchesForbidden(childPattern, list); forbidden {
						dropped = append(dropped, childPattern)
						continue
					}
				}
				reduced := walk(v[key], childPattern)
				if object, ok := reduced.(map[string]any); ok && len(object) == 0 {
					if _, wasObject := v[key].(map[string]any); wasObject && len(v[key].(map[string]any)) > 0 {
						continue
					}
				}
				out[key] = reduced
			}
			return out
		case []any:
			key := keyOf(pattern)
			if key == "" {
				return v
			}
			var out []any
			for _, element := range v {
				object, ok := element.(map[string]any)
				if !ok {
					continue
				}
				if pattern == "Radios" {
					band, _ := asString(object["Band"])
					if remove, _ := asBool(object[RemoveKey]); remove || (band != "2.4" && band != "5") {
						dropped = append(dropped, "Radios[].Band")
						continue
					}
				}
				reduced := walk(object, joinPattern(pattern, "[]")).(map[string]any)
				if len(reduced) <= 1 {
					if _, isRemove := reduced[RemoveKey]; !isRemove {
						if _, hasKey := reduced[key]; hasKey {
							continue
						}
					}
				}
				out = append(out, reduced)
			}
			return out
		}
		return value
	}
	result, _ := walk(patch, root).(map[string]any)
	if result == nil {
		result = map[string]any{}
	}
	return result, sortedUniqueStrings(dropped)
}

// ResidualLeaf is one leaf of a residual patch.
type ResidualLeaf struct {
	Pointer string
	steps   []leafStep
}

// ResidualLeaves lists the leaves of a patch in sorted pointer order. root
// is the scope's pattern ("" or "WLANs[]").
func ResidualLeaves(patch map[string]any, root string) []ResidualLeaf {
	var all [][]leafStep
	collectLeaves(patch, root, nil, &all)
	result := make([]ResidualLeaf, 0, len(all))
	for _, steps := range all {
		if len(steps) == 0 {
			continue
		}
		result = append(result, ResidualLeaf{Pointer: leafPointer(steps), steps: steps})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Pointer < result[j].Pointer })
	return result
}

// Without returns a copy of patch with the leaf removed.
func (l ResidualLeaf) Without(patch map[string]any) map[string]any {
	reduced, _ := withoutLeaf(patch, l.steps).(map[string]any)
	if reduced == nil {
		return map[string]any{}
	}
	return reduced
}

// EncodePatch marshals a patch (nil for an empty one).
func EncodePatch(patch map[string]any) ([]byte, error) {
	if len(patch) == 0 {
		return nil, nil
	}
	return encodeTree(patch)
}

// DecodePatch parses a stored patch.
func DecodePatch(raw []byte) (map[string]any, error) { return decodeObject(raw) }

// PatchPaths lists the leaf pointers of a patch (for reports).
func PatchPaths(patch map[string]any, root string) []string {
	var paths []string
	for _, leaf := range ResidualLeaves(patch, root) {
		paths = append(paths, leaf.Pointer)
	}
	return paths
}

// DiffPaths lists the paths where two canonical (normalised) configs
// differ, for refusal reports (paths only, never values).
func DiffPaths(a, b []byte) []string {
	treeA, errA := decodeObject(a)
	treeB, errB := decodeObject(b)
	if errA != nil || errB != nil {
		return []string{"(unparseable)"}
	}
	var paths []string
	var walk func(x, y any, path string)
	walk = func(x, y any, path string) {
		xo, xok := x.(map[string]any)
		yo, yok := y.(map[string]any)
		if xok && yok {
			keys := map[string]bool{}
			for key := range xo {
				keys[key] = true
			}
			for key := range yo {
				keys[key] = true
			}
			for _, key := range sortedKeys(func() map[string]any {
				out := map[string]any{}
				for key := range keys {
					out[key] = nil
				}
				return out
			}()) {
				walk(xo[key], yo[key], joinPattern(path, key))
			}
			return
		}
		xa, xaok := x.([]any)
		ya, yaok := y.([]any)
		if xaok && yaok && len(xa) == len(ya) {
			for index := range xa {
				walk(xa[index], ya[index], path+"["+strconv.Itoa(index)+"]")
			}
			return
		}
		if !equalTree(x, y) {
			paths = append(paths, path)
		}
	}
	walk(treeA, treeB, "")
	return paths
}
