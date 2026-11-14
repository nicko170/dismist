// Package effective models what AP33 0.15 ep-control actually runs from a
// controller config (design §4.5): Normalise returns the canonical
// effective config and Hash its keyed effective hash.
//
// Rule for the implementer: when in doubt, do not normalise. A missing rule
// shows up as a reported difference; a wrong rule could hide one. Bump
// Version whenever a rule is added or changed (the golden test fails if an
// output changes without a bump).
package effective

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/options/firmware"
)

// Version identifies the rule set. Hashes are compared only when their
// versions match.
const Version = 1

// Local-file states (design §4.3 LocalFiles, §4.5 rule 2a).
const (
	LocalFilesUnknown = "unknown" // never checked: rule 2a on, reported
	LocalFilesNone    = "none"    // checked, none found: rule 2a on
	LocalFilesPresent = "present" // local_* files merge around the config: rule 2a off
)

// Context carries what Normalise needs to know about the AP.
type Context struct {
	// LocalFiles is one of the LocalFiles* states; "" means unknown.
	LocalFiles string
}

// LocalFilesState maps an AP's stored local_files list to a state: nil is
// never checked, empty is none found.
func LocalFilesState[T any](files []T) string {
	switch {
	case files == nil:
		return LocalFilesUnknown
	case len(files) == 0:
		return LocalFilesNone
	}
	return LocalFilesPresent
}

// Notes Normalise can report.
const (
	NoteBSSIDUnresolved   = "bssid_unresolved"    // rule 4 not applied: a disabled WLAN ahead of an enabled one, an OWE transition companion, or more than 15 WLANs on a band
	NoteLocalFiles        = "local_files_present" // rules 2a to 5 not applied
	NotePrepareSkipped    = "prepare_partial"     // a WLAN's security combination is outside what rule 3 models
	NoteRadioUnresolved   = "radio_unresolved"    // a radio's channel, width or power is not a plain integer
	NoteTypeErrorsPresent = "type_errors"         // the AP would reject the whole config
)

// Report describes what Normalise did.
type Report struct {
	Version int `json:"version"`
	// Ignored lists the paths of keys 0.15 does not decode (dropped, rule 2).
	Ignored []string `json:"ignored,omitempty"`
	// TypeErrors lists values whose JSON type does not match the 0.15 Go
	// type. The AP rejects the whole config when there is any.
	TypeErrors []string `json:"type_errors,omitempty"`
	// LocalFiles is the local-file state the rules ran with.
	LocalFiles string `json:"local_files"`
	// ZeroRule reports whether rule 2a was applied.
	ZeroRule bool     `json:"zero_rule"`
	Notes    []string `json:"notes,omitempty"`
}

func (r *Report) note(note string) {
	for _, existing := range r.Notes {
		if existing == note {
			return
		}
	}
	r.Notes = append(r.Notes, note)
}

// Normalise returns the canonical effective form of a compiled config.
func Normalise(config []byte, ctx Context) ([]byte, Report, error) {
	return normalise(config, ctx, firmware.AP33())
}

func normalise(config []byte, ctx Context, schema *firmware.Schema) ([]byte, Report, error) {
	report := Report{Version: Version, LocalFiles: ctx.LocalFiles}
	if report.LocalFiles == "" {
		report.LocalFiles = LocalFilesUnknown
	}
	switch report.LocalFiles {
	case LocalFilesUnknown, LocalFilesNone, LocalFilesPresent:
	default:
		return nil, report, fmt.Errorf("unknown local-files state %q", ctx.LocalFiles)
	}
	parsed, err := parseJSON(config)
	if err != nil {
		return nil, report, err
	}
	if parsed.kind != '{' {
		return nil, report, errors.New("config must be a JSON object")
	}
	d := &decoder{report: &report}
	root := d.decode(parsed, schema.Root(), nil, "")
	if root == unchanged || root.kind != 'o' {
		return nil, report, errors.New("config must be a JSON object")
	}
	report.ZeroRule = report.LocalFiles != LocalFilesPresent
	if report.ZeroRule {
		pruneZero(root, schema.Root()) // rule 2a
		prepareWLANs(root, &report)    // rule 3
		pruneZero(root, schema.Root()) // zeros rule 3 produced
		resolveBSSIDs(root, &report)   // rule 4
		resolveRadios(root, &report)   // rule 5
	} else {
		report.note(NoteLocalFiles)
	}
	canonicalSets(root)           // rule 6
	delete(root.obj, "Timestamp") // rule 7
	if len(report.TypeErrors) > 0 {
		report.note(NoteTypeErrorsPresent)
	}
	report.Ignored = sortedUnique(report.Ignored)
	sort.Strings(report.Notes)
	var buf bytes.Buffer
	root.marshal(&buf) // rule 8
	return buf.Bytes(), report, nil
}

func sortedUnique(items []string) []string {
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

// HashPrefix is the prefix of an effective hash of this Version.
func HashPrefix() string { return fmt.Sprintf("e%d:", Version) }

// Hash is the effective hash of a canonical config: "e<Version>:" +
// hex(HMAC-SHA256(key, canonical)). key is meta.hash_key.
func Hash(key, canonical []byte) (string, error) {
	if len(key) == 0 {
		return "", errors.New("effective hash needs the database hash key")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return HashPrefix() + hex.EncodeToString(mac.Sum(nil)), nil
}

// HashConfig normalises config and hashes the result.
func HashConfig(key, config []byte, ctx Context) (string, []byte, Report, error) {
	canonical, report, err := Normalise(config, ctx)
	if err != nil {
		return "", nil, report, err
	}
	hash, err := Hash(key, canonical)
	return hash, canonical, report, err
}

// RawHMAC is hex HMAC-SHA256(key, raw): the keyed digest of raw config
// bytes recorded with H0 (design §4.5).
func RawHMAC(key, raw []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("raw\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

// SameVersion reports whether two effective hashes were produced by the same
// normaliser version and may be compared.
func SameVersion(a, b string) bool {
	versionOf := func(hash string) string {
		prefix, _, ok := strings.Cut(hash, ":")
		if !ok {
			return ""
		}
		return prefix
	}
	return versionOf(a) != "" && versionOf(a) == versionOf(b)
}
