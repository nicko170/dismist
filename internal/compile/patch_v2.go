package compile

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/nicko170/dismist/internal/options/firmware"
)

// Expert patches (design §4.4): key folding, keyed merge, forbidden and
// protected paths.

// Patch scopes.
const (
	ScopeSite = "site"
	ScopeAP   = "ap"
	ScopeWLAN = "wlan"
)

// Patch error codes (API error codes, design §4.8).
const (
	CodeForbiddenPath = "forbidden_path"
	CodeDuplicateKey  = "duplicate_key"
	CodeInvalid       = "invalid"
)

// PatchError is one problem with an expert patch. Pointer is a JSON
// pointer into the patch as the operator wrote it.
type PatchError struct {
	Code    string `json:"code"`
	Pointer string `json:"pointer"`
	Path    string `json:"path,omitempty"` // canonical firmware path pattern
	Message string `json:"message"`
}

// PatchErrors is a list of patch problems; it is an error.
type PatchErrors []PatchError

func (e PatchErrors) Error() string {
	var parts []string
	for _, problem := range e {
		parts = append(parts, problem.Pointer+": "+problem.Message)
	}
	return strings.Join(parts, "; ")
}

// Code returns the API code of the first problem.
func (e PatchErrors) Code() string {
	if len(e) == 0 {
		return CodeInvalid
	}
	return e[0].Code
}

// keyedArrays are merged element-wise by key (design §4.4 "Keyed merge").
var keyedArrays = map[string]string{
	"Radios":              "Band",
	"SwitchPorts":         "Name",
	"SVIs":                "Vlan_ID",
	"WLANs[].BandConfigs": "Band",
	"WLANs[].RateSets":    "Band",
}

// keyOf returns the merge key of a keyed array at a pattern path.
func keyOf(pattern string) string { return keyedArrays[pattern] }

// RemoveKey marks a keyed-array element for removal:
// {"<key>": …, "$remove": true}.
const RemoveKey = "$remove"

func scopeRoot(scope string) (string, *firmware.Node, error) {
	schema := firmware.AP33()
	switch scope {
	case ScopeSite, ScopeAP:
		return "", schema.Root(), nil
	case ScopeWLAN:
		node, ok := schema.Lookup("WLANs[]")
		if !ok {
			return "", nil, errors.New("schema has no WLANs[]")
		}
		return "WLANs[]", node, nil
	}
	return "", nil, fmt.Errorf("unknown patch scope %q", scope)
}

func joinPattern(parent, key string) string {
	if parent == "" {
		return key
	}
	if key == "[]" || key == "{}" {
		return parent + key
	}
	return parent + "." + key
}

func escapePointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// valueNode returns the struct-like node a JSON object decodes into (the
// pointee for pointers), or nil.
func structNode(node *firmware.Node) *firmware.Node {
	if node == nil {
		return nil
	}
	if node.ValueKind() == firmware.KindStruct {
		return node
	}
	return nil
}

// foldState collects the results of folding one patch.
type foldState struct {
	errors  PatchErrors
	ignored []string
}

// foldValue folds the keys of value, which decodes into node at pattern
// path (nil node: unknown to 0.15, kept as it is).
func (f *foldState) foldValue(value any, node *firmware.Node, pattern, pointer string) any {
	switch v := value.(type) {
	case map[string]any:
		if node != nil && node.ValueKind() == firmware.KindMap {
			out := make(map[string]any, len(v))
			for _, key := range sortedKeys(v) {
				out[key] = f.foldValue(v[key], node.Elem, joinPattern(pattern, "{}"), pointer+"/"+escapePointer(key))
			}
			return out
		}
		st := structNode(node)
		out := make(map[string]any, len(v))
		origin := map[string]string{}
		for _, key := range sortedKeys(v) {
			canonical := key
			var fieldNode *firmware.Node
			if key == RemoveKey {
				out[key] = v[key]
				continue
			}
			if st != nil {
				if field, ok := st.Field(key); ok {
					canonical, fieldNode = field.Name, field
				}
			}
			childPointer := pointer + "/" + escapePointer(key)
			if fieldNode == nil {
				f.ignored = append(f.ignored, joinPattern(pattern, key))
			}
			if previous, dup := origin[canonical]; dup {
				f.errors = append(f.errors, PatchError{
					Code: CodeDuplicateKey, Pointer: childPointer, Path: joinPattern(pattern, canonical),
					Message: fmt.Sprintf("%q and %q are the same key to the AP", previous, key),
				})
				continue
			}
			origin[canonical] = key
			out[canonical] = f.foldValue(v[key], fieldNode, joinPattern(pattern, canonical), childPointer)
		}
		return out
	case []any:
		var elem *firmware.Node
		if node != nil {
			elem = node.Elem
		}
		out := make([]any, len(v))
		for index, element := range v {
			out[index] = f.foldValue(element, elem, joinPattern(pattern, "[]"), pointer+"/"+strconv.Itoa(index))
		}
		return out
	}
	return value
}

// FoldResult is a folded expert patch.
type FoldResult struct {
	// Patch is the folded patch (canonical keys, sorted), ready to store.
	Patch json.RawMessage
	// Ignored lists the pattern paths of keys 0.15 does not know.
	Ignored []string
	// Warnings are allowed but notable keys (Cloud.Proxy.URL).
	Warnings []Warning
}

// FoldPatch folds an expert patch of a scope to canonical keys and checks
// it: duplicate keys after folding and forbidden or protected paths are
// returned as PatchErrors (API 422 duplicate_key / forbidden_path).
func FoldPatch(scope string, raw []byte) (FoldResult, error) {
	tree, err := decodeObject(raw)
	if err != nil {
		return FoldResult{}, PatchErrors{{Code: CodeInvalid, Pointer: "", Message: "expert patch must be a JSON object"}}
	}
	return foldTree(scope, tree)
}

func foldTree(scope string, tree map[string]any) (FoldResult, error) {
	root, node, err := scopeRoot(scope)
	if err != nil {
		return FoldResult{}, err
	}
	state := &foldState{}
	folded := state.foldValue(tree, node, root, "").(map[string]any)
	problems := append(PatchErrors(nil), state.errors...)
	problems = append(problems, checkForbidden(scope, folded, root)...)
	result := FoldResult{Ignored: sortedUniqueStrings(state.ignored)}
	if _, ok := lookupPath(folded, "Cloud.Proxy.URL"); ok && scope != ScopeWLAN {
		result.Warnings = append(result.Warnings, Warning{Code: "cloud_proxy", Path: "Cloud.Proxy.URL",
			Message: "Cloud.Proxy.URL can redirect the AP's controller connection"})
	}
	if len(problems) > 0 {
		sort.SliceStable(problems, func(i, j int) bool { return problems[i].Pointer < problems[j].Pointer })
		return result, problems
	}
	result.Patch, err = encodeTree(folded)
	return result, err
}

func sortedUniqueStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	sort.Strings(items)
	out := items[:1]
	for _, item := range items[1:] {
		if item != out[len(out)-1] {
			out = append(out, item)
		}
	}
	return out
}

// Forbidden paths (design §4.4), matched on folded keys. A path is
// forbidden when it equals an entry or lies below it; a trailing "*" is a
// name prefix.
var (
	forbiddenSiteAP = []string{
		"WLANs", "ID", "UUID", "SiteID", "OrgID", "Timestamp", "NotModified", "FailedRetryAfter",
		"Stats.Interval", "Stats.FullInterval", "Stats.MemInterval",
		"Ipsec", "Calea", "SiteString", "PAPIOpaque", "DeIdent", "AVS", "BootConfig", "Firmware", "MapID",
		"XYZ", "Default", "LicenseExpired", "RegulatoryOrg", "RadioSwap", "IndoorUse", "DirAntenna",
		"ManagementTunnels", "Monitor.PcapWLAN",
	}
	forbiddenWLAN = []string{
		"WLANs[].ID", "WLANs[].SSID", "WLANs[].Enabled", "WLANs[].Auth.PSK", "WLANs[].Auth.MultiPSK.PSKs",
		"WLANs[].KeyCache.KeyEncryptKey", "WLANs[].BandConfigs[].BSSIDIndex", "WLANs[].WLCmds",
		"WLANs[].Wxlan.Portal*", "WLANs[].ISE", "WLANs[].AirWatch", "WLANs[].DPI", "WLANs[].COA.CloudEnabled",
		"WLANs[].BlackList.Cloud", "WLANs[].Auth.WEP", "WLANs[].Auth.SecondaryType", "WLANs[].Auth.AKM.WPA_PSK",
		"WLANs[].Auth.AKM.WPA_EAP", "WLANs[].Auth.Pairwise.TKIP", "WLANs[].Auth.Pairwise.WPA1", "WLANs[].Mesh",
		"WLANs[].ForMesh",
	}
)

// ForbiddenPaths returns the forbidden path patterns of a scope.
func ForbiddenPaths(scope string) []string {
	if scope == ScopeWLAN {
		return append([]string(nil), forbiddenWLAN...)
	}
	return append([]string(nil), forbiddenSiteAP...)
}

func matchesForbidden(pattern string, list []string) (string, bool) {
	for _, forbidden := range list {
		if prefix, ok := strings.CutSuffix(forbidden, "*"); ok {
			if strings.HasPrefix(pattern, prefix) {
				return forbidden, true
			}
			continue
		}
		if pattern == forbidden || strings.HasPrefix(pattern, forbidden+".") || strings.HasPrefix(pattern, forbidden+"[]") {
			return forbidden, true
		}
	}
	return "", false
}

// checkForbidden walks a folded patch and reports forbidden paths.
func checkForbidden(scope string, tree map[string]any, root string) PatchErrors {
	list := forbiddenSiteAP
	if scope == ScopeWLAN {
		list = forbiddenWLAN
	}
	var problems PatchErrors
	var walk func(value any, pattern, pointer string)
	walk = func(value any, pattern, pointer string) {
		switch v := value.(type) {
		case map[string]any:
			for _, key := range sortedKeys(v) {
				if key == RemoveKey {
					continue
				}
				childPattern := joinPattern(pattern, key)
				childPointer := pointer + "/" + escapePointer(key)
				if forbidden, ok := matchesForbidden(childPattern, list); ok {
					problems = append(problems, PatchError{Code: CodeForbiddenPath, Pointer: childPointer, Path: childPattern,
						Message: fmt.Sprintf("%s cannot be set in a %s expert patch", strings.TrimPrefix(forbidden, "WLANs[]."), scope)})
					continue
				}
				walk(v[key], childPattern, childPointer)
			}
		case []any:
			for index, element := range v {
				walk(element, joinPattern(pattern, "[]"), pointer+"/"+strconv.Itoa(index))
			}
		}
	}
	walk(tree, root, "")
	// Radios elements are keyed by Band and cannot add, remove or change a
	// radio (design §4.4: Radios[].Band changes are forbidden).
	if scope != ScopeWLAN {
		if radios, ok := tree["Radios"].([]any); ok {
			for index, element := range radios {
				object, ok := element.(map[string]any)
				pointer := "/Radios/" + strconv.Itoa(index)
				if !ok {
					problems = append(problems, PatchError{Code: CodeInvalid, Pointer: pointer, Path: "Radios[]", Message: "a Radios element must be an object with Band"})
					continue
				}
				band, _ := asString(object["Band"])
				if band != "2.4" && band != "5" {
					problems = append(problems, PatchError{Code: CodeForbiddenPath, Pointer: pointer + "/Band", Path: "Radios[].Band",
						Message: `Radios elements merge by Band, which must be "2.4" or "5"`})
				}
				if remove, _ := asBool(object[RemoveKey]); remove {
					problems = append(problems, PatchError{Code: CodeForbiddenPath, Pointer: pointer + "/" + RemoveKey, Path: "Radios[].Band",
						Message: "a radio cannot be removed"})
				}
			}
		} else if _, present := tree["Radios"]; present {
			problems = append(problems, PatchError{Code: CodeForbiddenPath, Pointer: "/Radios", Path: "Radios",
				Message: "Radios must be a list of per-band elements"})
		}
	}
	return problems
}

// KeyedMerge merges patch into base (design §4.4): objects per RFC 7386
// (null deletes); arrays at keyed paths element-wise by key (an element
// with "$remove": true removes the base element, an element with no match
// is appended); every other array is replaced. pattern is the firmware
// path pattern of base ("" for the config root, "WLANs[]" for a WLAN).
func KeyedMerge(base, patch any, pattern string) (any, error) {
	switch p := patch.(type) {
	case map[string]any:
		object, ok := base.(map[string]any)
		if !ok {
			object = map[string]any{}
		}
		for _, key := range sortedKeys(p) {
			value := p[key]
			if value == nil {
				delete(object, key)
				continue
			}
			merged, err := KeyedMerge(object[key], value, joinPattern(pattern, key))
			if err != nil {
				return nil, err
			}
			object[key] = merged
		}
		return object, nil
	case []any:
		key := keyOf(pattern)
		if key == "" {
			return deepCopy(p), nil
		}
		baseArray, _ := base.([]any)
		result := append([]any(nil), baseArray...)
		for index, element := range p {
			object, ok := element.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: elements of a keyed list must be objects with %s", pattern, index, key)
			}
			keyValue, present := object[key]
			if !present {
				return nil, fmt.Errorf("%s[%d]: element has no %s", pattern, index, key)
			}
			match, at := keyedElement(result, key, keyString(keyValue))
			remove, _ := asBool(object[RemoveKey])
			if remove {
				if at >= 0 {
					result = append(result[:at], result[at+1:]...)
				}
				continue
			}
			clean := make(map[string]any, len(object))
			for k, v := range object {
				if k != RemoveKey {
					clean[k] = v
				}
			}
			if at < 0 {
				merged, err := KeyedMerge(map[string]any{}, clean, joinPattern(pattern, "[]"))
				if err != nil {
					return nil, err
				}
				result = append(result, merged)
				continue
			}
			merged, err := KeyedMerge(match, clean, joinPattern(pattern, "[]"))
			if err != nil {
				return nil, err
			}
			result[at] = merged
		}
		return result, nil
	}
	return deepCopy(patch), nil
}

// equalTree compares two trees by value.
func equalTree(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	return fingerprint(a, true) == fingerprint(b, true)
}

// KeyedDiff returns a patch r with KeyedMerge(from, r) equal to to, made
// of leaves only (design §4.7 step 4.2): objects are diffed member by
// member; a member present in from and absent in to becomes null leaf by
// leaf (a whole null only for pointer fields, where an empty struct is not
// the same as nil); keyed arrays are diffed element by element by key;
// other arrays are replaced whole when they differ. It reports whether
// anything differs.
func KeyedDiff(from, to any, pattern string) (any, bool) {
	fromObject, fromIsObject := from.(map[string]any)
	toObject, toIsObject := to.(map[string]any)
	if fromIsObject && toIsObject {
		patch := map[string]any{}
		for _, key := range sortedKeys(toObject) {
			childPattern := joinPattern(pattern, key)
			if fromValue, ok := fromObject[key]; ok {
				if sub, changed := KeyedDiff(fromValue, toObject[key], childPattern); changed {
					patch[key] = sub
				}
				continue
			}
			patch[key] = deepCopy(toObject[key])
		}
		for _, key := range sortedKeys(fromObject) {
			if _, ok := toObject[key]; ok {
				continue
			}
			patch[key] = nullLeaves(fromObject[key], joinPattern(pattern, key))
		}
		return patch, len(patch) > 0
	}
	fromArray, fromIsArray := from.([]any)
	toArray, toIsArray := to.([]any)
	if key := keyOf(pattern); key != "" && fromIsArray && toIsArray {
		var patch []any
		for _, element := range toArray {
			object, ok := element.(map[string]any)
			if !ok {
				return deepCopy(to), !equalTree(from, to)
			}
			match, at := keyedElement(fromArray, key, keyString(object[key]))
			if at < 0 {
				patch = append(patch, deepCopy(object))
				continue
			}
			if sub, changed := KeyedDiff(match, object, joinPattern(pattern, "[]")); changed {
				subObject := sub.(map[string]any)
				subObject[key] = object[key]
				patch = append(patch, subObject)
			}
		}
		for _, element := range fromArray {
			object, ok := element.(map[string]any)
			if !ok {
				return deepCopy(to), !equalTree(from, to)
			}
			if _, at := keyedElement(toArray, key, keyString(object[key])); at < 0 {
				patch = append(patch, map[string]any{key: object[key], RemoveKey: true})
			}
		}
		if len(patch) == 0 {
			return nil, false
		}
		return patch, true
	}
	if equalTree(from, to) {
		return nil, false
	}
	if to == nil {
		return nullLeaves(from, pattern), true
	}
	return deepCopy(to), true
}

// nullLeaves is the patch that removes value leaf by leaf: every member of
// an object becomes null (recursively), anything else a single null.
// Pointer-typed fields are nulled whole.
func nullLeaves(value any, pattern string) any {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 {
		return nil
	}
	if node, found := firmware.AP33().Lookup(pattern); !found || node.Kind == firmware.KindPointer || node.ValueKind() != firmware.KindStruct {
		return nil
	}
	out := make(map[string]any, len(object))
	for key, member := range object {
		out[key] = nullLeaves(member, joinPattern(pattern, key))
	}
	return out
}

// leafPaths lists every leaf of a patch as a path of steps; a step is a
// key, or a keyed-array element selector.
type leafStep struct {
	key      string // object key
	elemKey  string // keyed array element: the key member name
	elemVal  any    // and its value
	isRemove bool
}

func collectLeaves(value any, pattern string, prefix []leafStep, out *[][]leafStep) {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			*out = append(*out, append([]leafStep(nil), prefix...))
			return
		}
		for _, key := range sortedKeys(v) {
			collectLeaves(v[key], joinPattern(pattern, key), append(append([]leafStep(nil), prefix...), leafStep{key: key}), out)
		}
	case []any:
		key := keyOf(pattern)
		if key == "" {
			*out = append(*out, append([]leafStep(nil), prefix...))
			return
		}
		for _, element := range v {
			object, ok := element.(map[string]any)
			if !ok {
				continue
			}
			step := leafStep{elemKey: key, elemVal: object[key]}
			if remove, _ := asBool(object[RemoveKey]); remove {
				step.isRemove = true
				*out = append(*out, append(append([]leafStep(nil), prefix...), step))
				continue
			}
			members := 0
			for _, member := range sortedKeys(object) {
				if member == key {
					continue
				}
				members++
				collectLeaves(object[member], joinPattern(pattern+"[]", member),
					append(append(append([]leafStep(nil), prefix...), step), leafStep{key: member}), out)
			}
			if members == 0 {
				*out = append(*out, append(append([]leafStep(nil), prefix...), step))
			}
		}
	default:
		*out = append(*out, append([]leafStep(nil), prefix...))
	}
}

func leafPointer(steps []leafStep) string {
	var b strings.Builder
	for _, step := range steps {
		if step.elemKey != "" {
			fmt.Fprintf(&b, "/[%s=%s]", step.elemKey, keyString(step.elemVal))
			continue
		}
		b.WriteString("/" + escapePointer(step.key))
	}
	return b.String()
}

// withoutLeaf returns a copy of patch with one leaf removed (empty parents
// pruned).
func withoutLeaf(patch any, steps []leafStep) any {
	if len(steps) == 0 {
		return nil
	}
	step := steps[0]
	switch v := patch.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			out[key] = value
		}
		child, ok := out[step.key]
		if !ok {
			return out
		}
		if len(steps) == 1 {
			delete(out, step.key)
		} else {
			reduced := withoutLeaf(child, steps[1:])
			if empty(reduced) {
				delete(out, step.key)
			} else {
				out[step.key] = reduced
			}
		}
		return out
	case []any:
		var out []any
		for _, element := range v {
			object, ok := element.(map[string]any)
			if !ok || keyString(object[step.elemKey]) != keyString(step.elemVal) {
				out = append(out, element)
				continue
			}
			if len(steps) == 1 {
				continue
			}
			reduced := withoutLeaf(object, steps[1:])
			reducedObject, _ := reduced.(map[string]any)
			if len(reducedObject) <= 1 {
				continue
			}
			out = append(out, reducedObject)
		}
		return out
	}
	return patch
}

func empty(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return false
}
