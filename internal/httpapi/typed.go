package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// Typed settings are stored in the model records under the snake_case keys
// of design §4.3, which are the same keys (and nesting) as the API views.
// The API reads and writes them through the records' JSON form, so this
// package needs no Go field of workstream A's typed records: a record that
// does not carry a key yet simply has none to show, and a write to a key
// the record cannot store is refused (501) instead of being dropped.
//
// Option keys follow the same paths: the registry key of a typed field is
// its scope root ("site", "wlan", "ap") plus "." plus its JSON path in the
// view, for example "wlan.roaming.fast" or "site.rf.band_5.width".

// typedEnabled reports whether the schema-2 migration has committed, so
// typed settings can be edited (design §3.0 "Legacy mode", §4.7 step 7).
func (s *Server) typedEnabled() bool {
	return s.migrationView().State == api.MigrationStateCommitted
}

// migrationPending is the 503 for a typed edit in legacy mode.
func migrationPending(keys []string) *apiError {
	e := newError(http.StatusServiceUnavailable, api.ErrorCodeMigrationPending,
		"typed settings are paused: dismist is running your existing config unchanged until the schema-2 migration commits (System › Migration)")
	for _, key := range keys {
		e.withField(key, "read-only until the migration commits")
	}
	return e
}

// unsupportedField is the 501 for a typed key this build's records cannot
// store yet.
func unsupportedField(path string) *apiError {
	return newError(http.StatusNotImplemented, api.ErrorCodeUnavailable,
		"this dismist build cannot store "+path+" yet").withField(path, "not supported by this build")
}

// recordMap returns a record's JSON object with numbers kept exact.
func recordMap(record any) (map[string]any, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return decodeObject(raw)
}

func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

// lookup returns the value at a dotted path of a JSON object.
func lookup(object map[string]any, path string) (any, bool) {
	var current any = object
	for _, part := range strings.Split(path, ".") {
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = asMap[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// mergePatch applies an RFC 7386 merge patch to target in place.
func mergePatch(target map[string]any, patch map[string]any) {
	for key, value := range patch {
		if value == nil {
			delete(target, key)
			continue
		}
		if child, ok := value.(map[string]any); ok {
			existing, _ := target[key].(map[string]any)
			if existing == nil {
				existing = map[string]any{}
			}
			mergePatch(existing, child)
			target[key] = existing
			continue
		}
		target[key] = value
	}
}

// leaves returns the dotted paths of every leaf of a patch (objects are
// walked; arrays, scalars and nulls are leaves; an empty object changes
// nothing and has none).
func leaves(patch map[string]any, prefix string) []string {
	var result []string
	for key, value := range patch {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if child, ok := value.(map[string]any); ok {
			result = append(result, leaves(child, path)...)
			continue
		}
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// isZeroJSON reports whether a decoded JSON value is absent-equivalent:
// null, false, 0, "", [] or {}.
func isZeroJSON(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case bool:
		return !typed
	case string:
		return typed == ""
	case json.Number:
		f, err := typed.Float64()
		return err == nil && f == 0
	case float64:
		return typed == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		for _, child := range typed {
			if !isZeroJSON(child) {
				return false
			}
		}
		return true
	}
	return false
}

// sameStored reports whether a patched value survived a record's round
// trip: sameJSON with a zero object member (null, false, 0, "", [] or {})
// counted as absent at any depth, because records drop omitempty members
// inside list elements too (a blocked client without a note, an SSH key
// without a comment, a syslog target without a tag).
func sameStored(want, got any) bool {
	return sameJSON(dropZeroMembers(want), dropZeroMembers(got))
}

// sameJSON compares two decoded JSON values, treating numbers by value.
func sameJSON(a, b any) bool {
	if isZeroJSON(a) && isZeroJSON(b) {
		return true
	}
	rawA, errA := json.Marshal(normaliseNumbers(a))
	rawB, errB := json.Marshal(normaliseNumbers(b))
	return errA == nil && errB == nil && bytes.Equal(rawA, rawB)
}

// dropZeroMembers returns value with every zero object member removed,
// recursively (list elements are kept, their members cleaned).
func dropZeroMembers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			child = dropZeroMembers(child)
			if !isZeroJSON(child) {
				result[key] = child
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = dropZeroMembers(child)
		}
		return result
	}
	return value
}

func normaliseNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = normaliseNumbers(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = normaliseNumbers(child)
		}
		return result
	}
	return value
}

// errUnsupported carries the first path a record could not store.
type errUnsupported struct{ path string }

func (e *errUnsupported) Error() string { return "unsupported field " + e.path }

// patchRecord merges patch into record's JSON and decodes the result back
// into record. It refuses (errUnsupported) when a patched leaf did not
// survive the round trip, which means the record has no such field.
func patchRecord(record any, patch map[string]any) error {
	if len(patch) == 0 {
		return nil
	}
	if reflect.ValueOf(record).Kind() != reflect.Pointer {
		return errors.New("patchRecord needs a pointer")
	}
	current, err := recordMap(record)
	if err != nil {
		return err
	}
	mergePatch(current, patch)
	raw, err := json.Marshal(current)
	if err != nil {
		return err
	}
	fresh := reflect.New(reflect.TypeOf(record).Elem())
	if err := json.Unmarshal(raw, fresh.Interface()); err != nil {
		return fmt.Errorf("decode record: %w", err)
	}
	stored, err := recordMap(fresh.Interface())
	if err != nil {
		return err
	}
	for _, path := range leaves(patch, "") {
		want, _ := lookup(patch, path)
		got, _ := lookup(stored, path)
		if !sameStored(want, got) {
			return &errUnsupported{path: path}
		}
	}
	reflect.ValueOf(record).Elem().Set(fresh.Elem())
	return nil
}

// setRecordKey stores one raw JSON value under a top-level key of a record
// as it is (nil removes it). It is how the API reads and writes the legacy
// advanced patches and the expert patches without naming their Go fields.
// It refuses (errUnsupported) when the record has no such key.
func setRecordKey(record any, key string, value json.RawMessage) error {
	if reflect.ValueOf(record).Kind() != reflect.Pointer {
		return errors.New("setRecordKey needs a pointer")
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	if object == nil {
		object = map[string]json.RawMessage{}
	}
	present := len(value) != 0 && string(value) != "null"
	if present {
		object[key] = value
	} else {
		delete(object, key)
	}
	raw, err = json.Marshal(object)
	if err != nil {
		return err
	}
	fresh := reflect.New(reflect.TypeOf(record).Elem())
	if err := json.Unmarshal(raw, fresh.Interface()); err != nil {
		return fmt.Errorf("decode record: %w", err)
	}
	if present && len(recordKey(fresh.Elem().Interface(), key)) == 0 {
		return &errUnsupported{path: key}
	}
	reflect.ValueOf(record).Elem().Set(fresh.Elem())
	return nil
}

// recordKey returns the raw JSON of a top-level key of a record.
func recordKey(record any, key string) json.RawMessage {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	value := object[key]
	if string(value) == "null" {
		return nil
	}
	return value
}

func decodeAny(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// viewFrom decodes a record's JSON into a view struct (keys the view does
// not declare, secrets among them, are ignored).
func viewFrom(record any, view any) {
	raw, err := json.Marshal(record)
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, view)
}

// readPatch reads a JSON merge patch body (an object) with numbers kept
// exact.
func readPatch(request *http.Request, limit int64, what string) (map[string]any, error) {
	var raw json.RawMessage
	if err := decodeJSON(request, limit, &raw, what); err != nil {
		return nil, err
	}
	if len(raw) == 0 || raw[0] != '{' {
		return nil, invalid(what + " must be a JSON object")
	}
	return decodeObject(raw)
}

// checkKeys refuses keys a patch may not carry: unknown ones (400) and,
// in legacy mode, typed ones (503 migration_pending). readOnly keys are
// dropped.
func checkKeys(patch map[string]any, allowed, legacy, readOnly map[string]bool, typedOn bool) *apiError {
	var unknown, pending []string
	for key := range patch {
		switch {
		case readOnly[key]:
			delete(patch, key)
		case !allowed[key] && !legacy[key]:
			unknown = append(unknown, key)
		case !typedOn && !legacy[key]:
			pending = append(pending, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		bad := invalid("unknown fields: " + strings.Join(unknown, ", "))
		for _, key := range unknown {
			bad.withField(key, "unknown field")
		}
		return bad
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		return migrationPending(pending)
	}
	return nil
}

// jsonKeys returns the JSON names of a struct type's fields.
func jsonKeys(value any) map[string]bool {
	result := map[string]bool{}
	t := reflect.TypeOf(value)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for index := 0; index < t.NumField(); index++ {
			field := t.Field(index)
			tag := field.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if field.Anonymous && name == "" {
				walk(field.Type)
				continue
			}
			if name == "-" || !field.IsExported() {
				continue
			}
			if name == "" {
				name = field.Name
			}
			result[name] = true
		}
	}
	walk(t)
	return result
}

// optionValues flattens a patch into option keys (scope root + path) and
// their values, for the registry checks.
func optionValues(root string, patch map[string]any) map[string]any {
	result := map[string]any{}
	for _, path := range leaves(patch, "") {
		value, _ := lookup(patch, path)
		result[root+"."+path] = jsonValue(value)
	}
	return result
}

// jsonValue converts a decoded value to plain Go values (int for whole
// numbers) for the registry.
func jsonValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i)
		}
		f, _ := typed.Float64()
		return f
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = jsonValue(child)
		}
		return result
	}
	return value
}

// copyState deep-copies state through its JSON form, so the pre-mutation
// baseline keeps every field of the records, whatever they are.
func copyState(state model.State) model.State {
	raw, err := json.Marshal(state)
	if err != nil {
		return state
	}
	var copied model.State
	if err := json.Unmarshal(raw, &copied); err != nil {
		return state
	}
	return copied
}
