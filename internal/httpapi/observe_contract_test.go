package httpapi

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// fill sets every exported field of v (a pointer) to a non-zero value:
// one-element slices and maps, allocated pointers, true, 1 and "x".
func fill(v reflect.Value, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fill(v.Elem(), depth+1)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)))
			return
		}
		for index := 0; index < v.NumField(); index++ {
			if v.Type().Field(index).IsExported() {
				fill(v.Field(index), depth+1)
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte{1})
			return
		}
		slice := reflect.MakeSlice(v.Type(), 1, 1)
		fill(slice.Index(0), depth+1)
		v.Set(slice)
	case reflect.Array:
		for index := 0; index < v.Len(); index++ {
			fill(v.Index(index), depth+1)
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		key := reflect.New(v.Type().Key()).Elem()
		fill(key, depth+1)
		value := reflect.New(v.Type().Elem()).Elem()
		fill(value, depth+1)
		m.SetMapIndex(key, value)
		v.Set(m)
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Interface:
	}
}

// missingKeys lists the JSON key paths of the mirror type t that the
// producer's JSON (value) never carries.
func missingKeys(t reflect.Type, value any, path string, out *[]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
		if list, ok := value.([]any); ok {
			if len(list) == 0 {
				return
			}
			value = list[0]
		}
	}
	if t.Kind() != reflect.Struct || t == reflect.TypeOf(time.Time{}) {
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		*out = append(*out, path+" (not an object)")
		return
	}
	for index := 0; index < t.NumField(); index++ {
		field := t.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		child, ok := object[name]
		if !ok {
			*out = append(*out, path+"."+name)
			continue
		}
		missingKeys(field.Type, child, path+"."+name, out)
	}
}

func producerJSON(t *testing.T, value any) any {
	t.Helper()
	fill(reflect.ValueOf(value), 0)
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// TestObservationMirrorMatchesController checks the API's mirror types
// against workstream B's JSON: every key the API reads must be one the
// controller writes (design §8.2 Status v2 and telemetry.Update v2).
func TestObservationMirrorMatchesController(t *testing.T) {
	cases := []struct {
		name     string
		producer any
		mirror   reflect.Type
	}{
		{"status", &controller.Status{}, reflect.TypeOf(obsStatus{})},
		{"telemetry", &controller.TelemetrySnapshot{}, reflect.TypeOf(obsTelemetry{})},
		{"counters", &controller.Counters{}, reflect.TypeOf(obsCounters{})},
		{"ap_status", &model.APStatusRecord{}, reflect.TypeOf(lastKnown{})},
	}
	for _, tc := range cases {
		var missing []string
		produced := producerJSON(t, tc.producer)
		raw, _ := json.Marshal(produced)
		if err := json.Unmarshal(raw, reflect.New(tc.mirror).Interface()); err != nil {
			t.Errorf("%s: the controller's JSON does not decode into the API's mirror: %v", tc.name, err)
		}
		missingKeys(tc.mirror, produced, tc.name, &missing)
		sort.Strings(missing)
		if len(missing) != 0 {
			t.Errorf("%s: the API reads keys the controller never writes:\n  %s", tc.name, strings.Join(missing, "\n  "))
		}
	}
}
