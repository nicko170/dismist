package compile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// The typed pipeline works on a generic JSON tree: map[string]any objects,
// []any arrays, json.Number numbers (so 5.5 and 86400 keep their literal
// form), strings, bools and nil. Output is marshalled with sorted keys.

// decodeTree parses JSON into a tree with json.Number numbers.
func decodeTree(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("trailing data after JSON value")
	}
	return value, nil
}

// decodeObject parses a JSON object; empty input is an empty object.
func decodeObject(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	value, err := decodeTree(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		if value == nil {
			return map[string]any{}, nil
		}
		return nil, errors.New("expected a JSON object")
	}
	return object, nil
}

func encodeTree(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// deepCopy copies a tree.
func deepCopy(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = deepCopy(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for index, child := range v {
			out[index] = deepCopy(child)
		}
		return out
	}
	return value
}

// jsonNumber returns a json.Number for a literal.
func jsonNumber(literal string) json.Number { return json.Number(literal) }

// num returns a json.Number for an integer.
func num(n int) json.Number { return json.Number(strconv.Itoa(n)) }

// asInt reads an integer from a tree value.
func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			f, ferr := v.Float64()
			if ferr != nil || f != float64(int64(f)) {
				return 0, false
			}
			n = int64(f)
		}
		return int(n), true
	case float64:
		return int(v), v == float64(int(v))
	case int:
		return v, true
	}
	return 0, false
}

func asString(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

func asBool(value any) (bool, bool) {
	b, ok := value.(bool)
	return b, ok
}

// child returns object[key] as an object, creating it when absent or not
// an object.
func child(object map[string]any, key string) map[string]any {
	if existing, ok := object[key].(map[string]any); ok {
		return existing
	}
	created := map[string]any{}
	object[key] = created
	return created
}

// objectAt walks keys through objects; nil when any step is missing.
func objectAt(object map[string]any, keys ...string) map[string]any {
	current := object
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

// valueAt walks keys through objects and returns the final value.
func valueAt(object map[string]any, keys ...string) (any, bool) {
	if len(keys) == 0 {
		return object, true
	}
	parent := objectAt(object, keys[:len(keys)-1]...)
	if parent == nil {
		return nil, false
	}
	value, ok := parent[keys[len(keys)-1]]
	return value, ok
}

// setAt sets a value below nested objects, creating them.
func setAt(object map[string]any, value any, keys ...string) {
	current := object
	for _, key := range keys[:len(keys)-1] {
		current = child(current, key)
	}
	current[keys[len(keys)-1]] = value
}

// keyedElement returns the element of array whose member key equals value
// (compared as strings), and its index.
func keyedElement(array []any, key, value string) (map[string]any, int) {
	for index, element := range array {
		object, ok := element.(map[string]any)
		if !ok {
			continue
		}
		if keyString(object[key]) == value {
			return object, index
		}
	}
	return nil, -1
}

// keyString renders a key member for matching: strings as is, numbers by
// their literal.
func keyString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case nil:
		return ""
	}
	return fmt.Sprint(value)
}

// Concrete paths name one leaf of a compiled config for the trace and the
// shadow report: dot-separated keys, with "[Key=Value]" selecting the
// element of a keyed array, for example "Radios[Band=5].RRM.Width" or
// "WLANs[ID=6f20…].Auth.PSK".
type pathSeg struct {
	key        string
	matchKey   string
	matchValue string
}

func parsePath(path string) []pathSeg {
	var segs []pathSeg
	for _, part := range splitPathParts(path) {
		seg := pathSeg{key: part}
		if open := strings.IndexByte(part, '['); open >= 0 && strings.HasSuffix(part, "]") {
			seg.key = part[:open]
			selector := part[open+1 : len(part)-1]
			if k, v, ok := strings.Cut(selector, "="); ok {
				seg.matchKey, seg.matchValue = k, v
			}
		}
		segs = append(segs, seg)
	}
	return segs
}

// splitPathParts splits on dots outside brackets.
func splitPathParts(path string) []string {
	var parts []string
	depth, start := 0, 0
	for index := 0; index < len(path); index++ {
		switch path[index] {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				parts = append(parts, path[start:index])
				start = index + 1
			}
		}
	}
	return append(parts, path[start:])
}

// lookupPath resolves a concrete path; absent when any step is missing.
func lookupPath(root map[string]any, path string) (any, bool) {
	var current any = root
	for _, seg := range parsePath(path) {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := object[seg.key]
		if !ok {
			return nil, false
		}
		if seg.matchKey != "" {
			array, ok := next.([]any)
			if !ok {
				return nil, false
			}
			element, index := keyedElement(array, seg.matchKey, seg.matchValue)
			if index < 0 {
				return nil, false
			}
			next = element
		}
		current = next
	}
	return current, true
}

// fingerprint renders a value for before/after comparison.
func fingerprint(value any, present bool) string {
	if !present {
		return "\x00absent"
	}
	raw, err := encodeTree(value)
	if err != nil {
		return "\x00error"
	}
	return string(raw)
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
