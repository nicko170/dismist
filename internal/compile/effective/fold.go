package effective

import (
	"bytes"
	"errors"

	"github.com/nicko170/dismist/internal/options/firmware"
)

// FoldReport describes what Fold dropped or could not decode.
type FoldReport struct {
	Ignored    []string `json:"ignored,omitempty"`
	TypeErrors []string `json:"type_errors,omitempty"`
}

// Fold applies normaliser rules 1 and 2 only, which is what the AP's own
// JSON decoding does: every key is matched case-insensitively to its
// canonical 0.15 name, case variants of one key are applied in document
// order with Go's decoding semantics (the last wins), and keys 0.15 does
// not know are dropped and reported. Values keep their raw meaning: no
// zero pruning, no Prepare emulation, no radio or BSSID resolution, and
// the Timestamp is kept. The result is marshalled with sorted keys.
//
// internal/compile canonicalises the template with it and the schema-2
// migration diffs raw compiles in this form (design §4.4, §4.7 step 4).
func Fold(config []byte) ([]byte, FoldReport, error) {
	var report FoldReport
	parsed, err := parseJSON(config)
	if err != nil {
		return nil, report, err
	}
	if parsed.kind != '{' {
		return nil, report, errors.New("config must be a JSON object")
	}
	scratch := Report{}
	d := &decoder{report: &scratch}
	root := d.decode(parsed, firmware.AP33().Root(), nil, "")
	if root == unchanged || root.kind != 'o' {
		return nil, report, errors.New("config must be a JSON object")
	}
	report.Ignored = sortedUnique(scratch.Ignored)
	report.TypeErrors = scratch.TypeErrors
	var buf bytes.Buffer
	root.marshal(&buf)
	return buf.Bytes(), report, nil
}
