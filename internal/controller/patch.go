package controller

import (
	"bytes"
	"encoding/json"
	"errors"
)

const maxAdvancedPatch = 1 << 20

func MergePatch(base, patch json.RawMessage) (json.RawMessage, error) {
	return mergePatch(base, patch, 0)
}

func mergePatch(base, patch json.RawMessage, depth int) (json.RawMessage, error) {
	if depth > 32 || len(patch) > maxAdvancedPatch {
		return nil, errors.New("advanced config patch is too large or deep")
	}
	patch = bytes.TrimSpace(patch)
	if len(patch) == 0 || patch[0] != '{' {
		return nil, errors.New("advanced config patch must be a JSON object")
	}
	var patchFields map[string]json.RawMessage
	if err := json.Unmarshal(patch, &patchFields); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	base = bytes.TrimSpace(base)
	if len(base) == 0 || base[0] != '{' {
		fields = make(map[string]json.RawMessage)
	} else if err := json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	for key, value := range patchFields {
		value = bytes.TrimSpace(value)
		if bytes.Equal(value, []byte("null")) {
			delete(fields, key)
			continue
		}
		if len(value) > 0 && value[0] == '{' {
			merged, err := mergePatch(fields[key], value, depth+1)
			if err != nil {
				return nil, err
			}
			fields[key] = merged
		} else {
			fields[key] = append(json.RawMessage(nil), value...)
		}
	}
	return json.Marshal(fields)
}
