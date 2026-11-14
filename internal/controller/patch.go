package controller

import (
	"encoding/json"

	"github.com/nicko170/dismist/internal/compile"
)

// MergePatch forwards to compile.MergePatch. Deprecated: use internal/compile.
func MergePatch(base, patch json.RawMessage) (json.RawMessage, error) {
	return compile.MergePatch(base, patch)
}
