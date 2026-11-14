// Package options is the option registry: the single source for every typed
// option (design §4.2). Go code reads it for validation, the dry-run
// disruption calculation and the compile trace; cmd/dismist-gen writes the
// TypeScript metadata and zod schemas (ui/src/gen/options.gen.ts) and a
// reference table (docs/design/options.gen.md) from it.
//
// Options are declared in registry_wlan.go, registry_site.go,
// registry_ap.go and registry_rf.go. Phase 0 ships the framework with no
// declarations; phase 2 adds them.
package options

//go:generate go run ../../cmd/dismist-gen options -ts ../../ui/src/gen/options.gen.ts -md ../../docs/design/options.gen.md

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Tier places an option on the screens (design P2).
type Tier string

const (
	TierBasic    Tier = "basic"
	TierAdvanced Tier = "advanced"
	TierExpert   Tier = "expert" // no form control; carries Default/LegacyDefault only
	TierReadonly Tier = "readonly"
	TierNever    Tier = "never"
)

// Confidence is the catalogue confidence of an option or enum value.
type Confidence string

const (
	Confirmed     Confidence = "confirmed"
	Likely        Confidence = "likely"
	NeedsLiveTest Confidence = "needs-live-test" // experimental (design P4)
)

// Disruption is what applying a change does to clients (design P6).
type Disruption string

const (
	DisruptionNone          Disruption = "none"
	DisruptionWLANRestart   Disruption = "wlan_restart"
	DisruptionRadioBounce   Disruption = "radio_bounce"
	DisruptionAllRadios     Disruption = "all_radios_bounce"
	DisruptionStrandingRisk Disruption = "stranding_risk"
)

// disruptionRank orders disruption classes for Union.
var disruptionRank = map[Disruption]int{
	DisruptionNone: 0, DisruptionWLANRestart: 1, DisruptionRadioBounce: 2,
	DisruptionAllRadios: 3, DisruptionStrandingRisk: 4,
}

// Union returns the most disruptive class of the given ones ("none" for
// none).
func Union(classes ...Disruption) Disruption {
	result := DisruptionNone
	for _, class := range classes {
		if disruptionRank[class] > disruptionRank[result] {
			result = class
		}
	}
	return result
}

// Scope is the object an option belongs to.
type Scope string

const (
	ScopeSite   Scope = "site"
	ScopeSiteRF Scope = "site.rf"
	ScopeAP     Scope = "ap"
	ScopeAPRF   Scope = "ap.rf"
	ScopeWLAN   Scope = "wlan"
)

// Controls and value types an option may declare.
var (
	Controls = []string{"text", "password", "number", "switch", "select", "segmented", "chips", "list", "slider", "ssh_keys", "mac_list"}
	Types    = []string{"string", "int", "bool", "[]int", "[]string", "object"}
)

// EnumValue is one allowed value of an enum option.
type EnumValue struct {
	Value, Label, Help string
	Confidence         Confidence // per-value gating, e.g. security=owe; "" = the option's
	Phase              int
}

// Condition shows an option only when another option has one of the given
// values.
type Condition struct {
	Key string   `json:"key"`
	In  []string `json:"in"`
}

// Option declares one typed option.
type Option struct {
	Key           string // stable API key, e.g. "wlan.security", "site.rf.band_5.width"
	Scope         Scope
	Label, Help   string
	Control       string // one of Controls; "" for expert and readonly options
	Type          string // one of Types
	Enum          []EnumValue
	Min, Max      *int
	MaxBytes      int    // e.g. SSID 32 bytes
	Unit          string // "dBm", "s", "kbps", "MHz"
	Default       any    // for objects created after v1
	LegacyDefault any    // for objects that existed before schema 2 (§4.6); nil = same as Default
	Tier          Tier
	Confidence    Confidence
	Disruption    Disruption
	Secret        bool // write-only; redacted in views
	Phase         int
	FirmwarePaths []string // every path this option writes, e.g. "WLANs[].Hidden"
	Catalogue     string   // e.g. "wlans.md §5.1"
	VisibleWhen   []Condition
}

// Int returns a pointer to v, for Min and Max.
func Int(v int) *int { return &v }

// Experimental reports whether value is experimental for this option: the
// option itself or the chosen enum value is needs-live-test.
func (o Option) Experimental(value any) bool {
	if o.Confidence == NeedsLiveTest {
		return true
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	for _, enum := range o.Enum {
		if enum.Value == text {
			return enum.Confidence == NeedsLiveTest
		}
	}
	return false
}

// InForms reports whether the option has a form control (expert, readonly
// and never options do not, design §4.2 check 5).
func (o Option) InForms() bool {
	return o.Tier == TierBasic || o.Tier == TierAdvanced
}

// Registry is a validated, sorted set of options.
type Registry struct {
	options []Option
	byKey   map[string]int
}

// New validates options and returns a registry. pathExists reports whether
// a firmware path is in the 0.15 path list (nil skips that check).
func New(options []Option, pathExists func(string) bool) (*Registry, error) {
	registry := &Registry{byKey: map[string]int{}}
	sorted := append([]Option(nil), options...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	var problems []error
	for index, option := range sorted {
		if _, dup := registry.byKey[option.Key]; dup {
			problems = append(problems, fmt.Errorf("%s: declared twice", option.Key))
			continue
		}
		registry.byKey[option.Key] = index
		problems = append(problems, validateOption(option, pathExists)...)
	}
	registry.options = sorted
	for _, option := range sorted {
		for _, condition := range option.VisibleWhen {
			if _, ok := registry.byKey[condition.Key]; !ok {
				problems = append(problems, fmt.Errorf("%s: visible_when names unknown option %s", option.Key, condition.Key))
			}
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return registry, nil
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func validateOption(o Option, pathExists func(string) bool) []error {
	var problems []error
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(o.Key+": "+format, args...))
	}
	if o.Key == "" || strings.ToLower(o.Key) != o.Key || strings.ContainsAny(o.Key, " \t") {
		fail("key must be non-empty lower case")
	}
	switch o.Scope {
	case ScopeSite, ScopeSiteRF, ScopeAP, ScopeAPRF, ScopeWLAN:
		if !strings.HasPrefix(o.Key, string(o.Scope)+".") {
			fail("key must start with its scope %q", o.Scope)
		}
	default:
		fail("unknown scope %q", o.Scope)
	}
	switch o.Tier {
	case TierBasic, TierAdvanced:
		if !contains(Controls, o.Control) {
			fail("tier %s needs a control, got %q", o.Tier, o.Control)
		}
	case TierExpert, TierReadonly:
		if o.Control != "" {
			fail("tier %s must not have a form control", o.Tier)
		}
	case TierNever:
		fail("never-tier options are not declared")
	default:
		fail("unknown tier %q", o.Tier)
	}
	switch o.Confidence {
	case Confirmed, Likely, NeedsLiveTest:
	default:
		fail("unknown confidence %q", o.Confidence)
	}
	if _, ok := disruptionRank[o.Disruption]; !ok {
		fail("unknown disruption %q", o.Disruption)
	}
	if !contains(Types, o.Type) {
		fail("unknown type %q", o.Type)
	}
	if o.Min != nil && o.Max != nil && *o.Min > *o.Max {
		fail("min above max")
	}
	if o.Label == "" && o.InForms() {
		fail("form options need a label")
	}
	if o.Catalogue == "" {
		fail("catalogue reference required")
	}
	seen := map[string]bool{}
	for _, enum := range o.Enum {
		if seen[enum.Value] {
			fail("enum value %q twice", enum.Value)
		}
		seen[enum.Value] = true
		switch enum.Confidence {
		case "", Confirmed, Likely, NeedsLiveTest:
		default:
			fail("enum value %q has unknown confidence %q", enum.Value, enum.Confidence)
		}
	}
	if len(o.FirmwarePaths) == 0 && o.Tier != TierReadonly {
		fail("firmware paths required")
	}
	for _, path := range o.FirmwarePaths {
		if pathExists != nil && !pathExists(path) {
			fail("firmware path %s is not in ap33-0.15.paths", path)
		}
		if never, ok := NeverPath(path); ok {
			fail("firmware path %s is never-tier (%s)", path, never)
		}
	}
	return problems
}

// All returns every option, sorted by key.
func (r *Registry) All() []Option { return append([]Option(nil), r.options...) }

// Get returns one option.
func (r *Registry) Get(key string) (Option, bool) {
	index, ok := r.byKey[key]
	if !ok {
		return Option{}, false
	}
	return r.options[index], true
}

// ByScope returns the options of one scope, sorted by key.
func (r *Registry) ByScope(scope Scope) []Option {
	var result []Option
	for _, option := range r.options {
		if option.Scope == scope {
			result = append(result, option)
		}
	}
	return result
}

// ExperimentalError is returned when experimental values are set on a site
// without experimental: true. HTTP handlers map it to 422
// code "experimental_disabled" with one field per key.
type ExperimentalError struct {
	Keys []string
}

func (e *ExperimentalError) Error() string {
	return "experimental options need the site's experimental setting: " + strings.Join(e.Keys, ", ")
}

// Code is the API error code.
func (e *ExperimentalError) Code() string { return "experimental_disabled" }

// CheckExperimental refuses experimental values unless the site has
// experimental on (design §4.2). changed holds only the values the request
// changes (option key → new value); unchanged stored values are always
// kept. Unknown keys are ignored here (validation reports them).
func (r *Registry) CheckExperimental(siteExperimental bool, changed map[string]any) error {
	if siteExperimental {
		return nil
	}
	var keys []string
	for key, value := range changed {
		if option, ok := r.Get(key); ok && option.Experimental(value) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return &ExperimentalError{Keys: keys}
}
