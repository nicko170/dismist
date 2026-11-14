package compile

import (
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/model"
)

// EffectiveContext is the normaliser context of an AP: its local-file state
// (nil local_files = never checked).
func EffectiveContext(ap model.APRecord) effective.Context {
	return effective.Context{LocalFiles: effective.LocalFilesState(ap.LocalFiles)}
}

// EffectiveView is an AP's compiled config with its canonical effective
// form and keyed effective hash (GET /api/aps/$id/config?view=normalised).
// Config and Canonical contain secrets: redact before returning them.
type EffectiveView struct {
	Config    []byte
	Canonical []byte
	Hash      string // "e<Version>:<hex HMAC>"
	Report    effective.Report
}

// Effective compiles one AP from state and normalises the result. key is
// meta.hash_key (model.BoltStore.HashKey).
func Effective(template []byte, state model.State, apID string, key []byte) (EffectiveView, error) {
	input, err := InputFor(template, state, apID)
	if err != nil {
		return EffectiveView{}, err
	}
	output, err := AP(input)
	if err != nil {
		return EffectiveView{}, err
	}
	hash, canonical, report, err := effective.HashConfig(key, output.Config, EffectiveContext(input.AP))
	if err != nil {
		return EffectiveView{}, err
	}
	return EffectiveView{Config: output.Config, Canonical: canonical, Hash: hash, Report: report}, nil
}
