package compile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

// changeIgnoredKeys are the top-level keys change detection ignores:
// Timestamp moves with every revision, and SiteName is ignored by 0.15
// [SITE §0] but still emitted by the v0 compiler, so a site rename must not
// count as a config change (design §4.4 "Change detection").
var changeIgnoredKeys = []string{"Timestamp", "SiteName"}

// ChangeKey returns config with the keys change detection ignores removed,
// re-marshalled with sorted keys. Two configs are the same for change
// detection when their change keys are byte-identical.
func ChangeKey(config []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(config, &root); err != nil {
		return nil, err
	}
	for _, key := range changeIgnoredKeys {
		delete(root, key)
	}
	return json.Marshal(root)
}

// SameConfig reports whether two compiled configs are equal for change
// detection.
func SameConfig(a, b []byte) (bool, error) {
	keyA, err := ChangeKey(a)
	if err != nil {
		return false, err
	}
	keyB, err := ChangeKey(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(keyA, keyB), nil
}

// DesiredLookup reads stored ap_config.desired entries.
// *model.APConfigBucket and *model.APConfigStore implement it.
type DesiredLookup interface {
	Desired(apID string) (model.DesiredConfig, bool, error)
}

// DesiredStore also writes them; *model.APConfigBucket implements it.
type DesiredStore interface {
	DesiredLookup
	PutDesired(apID string, desired model.DesiredConfig) error
}

// Baselines an AP's new compile is compared against.
const (
	BaselineDesired  = "desired"  // ap_config.desired
	BaselinePrevious = "previous" // a compile of the state before the mutation
	BaselineNone     = "none"     // nothing to compare with: counted as changed
)

// SiteInput selects what compile.Site compiles and compares.
type SiteInput struct {
	Template []byte
	State    model.State
	// Sites are the site IDs to compile (a move passes both sites). Empty
	// means every site.
	Sites []string
	// Desired is the stored ap_config. nil means none is stored.
	Desired DesiredLookup
	// Previous is the state before the mutation. For an AP with no stored
	// desired config it supplies the baseline, so the first save after an
	// upgrade does not count every AP as changed.
	Previous *model.State
}

// APResult is the outcome for one AP.
type APResult struct {
	APID   string
	SiteID string
	// Output is compiled with the AP's current config time; Output.Config
	// is nil when Err is set.
	Output Output
	// Err is this AP's compile error. Other APs are unaffected.
	Err error
	// Changed reports that the compiled config differs from the baseline
	// (ignoring Timestamp and SiteName). A failed compile is not changed.
	Changed  bool
	Baseline string
	// HasDesired reports that ap_config.desired existed for the AP.
	HasDesired bool
}

// SiteResult is the outcome for every compiled AP.
type SiteResult struct {
	APs     []APResult // sorted by AP ID
	Changed []string   // IDs of APs whose compiled config changed, sorted
	Failed  []string   // IDs of APs whose compile failed, sorted
}

// Result returns the result of one AP.
func (r SiteResult) Result(apID string) (APResult, bool) {
	for _, result := range r.APs {
		if result.APID == apID {
			return result, true
		}
	}
	return APResult{}, false
}

// Err returns the compile failures joined, naming each AP, or nil.
func (r SiteResult) Err() error {
	var failures []error
	for _, result := range r.APs {
		if result.Err != nil {
			failures = append(failures, &APError{APID: result.APID, Err: result.Err})
		}
	}
	return errors.Join(failures...)
}

// APError is a compile failure of one AP.
type APError struct {
	APID string
	Err  error
}

func (e *APError) Error() string { return fmt.Sprintf("AP %s config: %v", e.APID, e.Err) }
func (e *APError) Unwrap() error { return e.Err }

func selected(sites []string) func(string) bool {
	if len(sites) == 0 {
		return func(string) bool { return true }
	}
	set := make(map[string]bool, len(sites))
	for _, site := range sites {
		set[site] = true
	}
	return func(site string) bool { return set[site] }
}

// Site compiles every AP of the selected sites, each on its own, and
// compares each result with its baseline. It returns an error only when the
// stored desired configs cannot be read.
func Site(in SiteInput) (SiteResult, error) {
	want := selected(in.Sites)
	var result SiteResult
	for _, ap := range in.State.APs {
		if ap.SiteID == "" || !want(ap.SiteID) {
			continue
		}
		one := APResult{APID: ap.ID, SiteID: ap.SiteID, Baseline: BaselineNone}
		input, err := InputFor(in.Template, in.State, ap.ID)
		if err == nil {
			one.Output, err = AP(input)
		}
		if err != nil {
			one.Err = err
			result.APs = append(result.APs, one)
			continue
		}
		var baseline []byte
		if in.Desired != nil {
			desired, found, err := in.Desired.Desired(ap.ID)
			if err != nil {
				return SiteResult{}, err
			}
			if found {
				one.HasDesired = true
				one.Baseline = BaselineDesired
				baseline = desired.Bytes
			}
		}
		if baseline == nil && in.Previous != nil {
			if input, err := InputFor(in.Template, *in.Previous, ap.ID); err == nil {
				if previous, err := AP(input); err == nil {
					one.Baseline = BaselinePrevious
					baseline = previous.Config
				}
			}
		}
		one.Changed = true
		if baseline != nil {
			same, err := SameConfig(one.Output.Config, baseline)
			one.Changed = err != nil || !same
		}
		result.APs = append(result.APs, one)
	}
	sort.Slice(result.APs, func(i, j int) bool { return result.APs[i].APID < result.APs[j].APID })
	for _, one := range result.APs {
		switch {
		case one.Err != nil:
			result.Failed = append(result.Failed, one.APID)
		case one.Changed:
			result.Changed = append(result.Changed, one.APID)
		}
	}
	return result, nil
}

// ApplyOptions configures Apply.
type ApplyOptions struct {
	Template []byte
	// Sites are the affected sites; empty means every site. A move passes
	// the old and the new site.
	Sites []string
	// Previous is the state before the mutation (see SiteInput.Previous).
	Previous *model.State
	// Now is the new ConfigUpdatedAt for changed APs (the mutation's
	// state.UpdatedAt).
	Now time.Time
	// DryRun computes the same result without changing state or ap_config.
	DryRun bool
}

// Push is a config to send to an AP after the mutation commits.
type Push struct {
	APID     string
	Config   []byte
	Revision uint64
}

// Applied is the outcome of Apply.
type Applied struct {
	Result SiteResult
	// Pushes holds the new config of every changed AP, sorted by AP ID. The
	// caller pushes them after the transaction commits.
	Pushes []Push
}

// Apply runs change detection for a config mutation inside its store
// transaction (design §4.4 steps 1 to 4), after the mutation has changed
// state:
//   - every AP of the affected sites is compiled from the new state;
//   - an AP whose config differs from ap_config.desired (ignoring Timestamp
//     and SiteName) gets ConfigRevision = state.Revision+1 and
//     ConfigUpdatedAt = Now, is recompiled with the new Timestamp, has its
//     desired config stored, and is returned in Pushes;
//   - an AP whose config is the same changes nothing (a missing desired
//     config is seeded from its current compile, which is what it is
//     already being served).
//
// A compile failure of any affected AP refuses the whole mutation: Apply
// returns the joined *APError values and changes nothing (isolation is for
// state that is already stored, not for new writes).
func Apply(state *model.State, store DesiredStore, opts ApplyOptions) (Applied, error) {
	var lookup DesiredLookup
	if store != nil {
		lookup = store
	}
	result, err := Site(SiteInput{
		Template: opts.Template, State: *state, Sites: opts.Sites,
		Desired: lookup, Previous: opts.Previous,
	})
	if err != nil {
		return Applied{}, err
	}
	if err := result.Err(); err != nil {
		return Applied{Result: result}, err
	}
	applied := Applied{Result: result}
	changed := make(map[string]bool, len(result.Changed))
	for _, id := range result.Changed {
		changed[id] = true
	}
	revision := state.Revision + 1
	for index := range state.APs {
		ap := &state.APs[index]
		one, ok := result.Result(ap.ID)
		if !ok {
			continue
		}
		if !changed[ap.ID] {
			if !one.HasDesired && store != nil && !opts.DryRun {
				desired := model.DesiredConfig{
					Revision:  ConfigRevision(*state, *ap),
					Timestamp: ConfigTimestamp(ConfigTime(*state, *ap)),
					Bytes:     one.Output.Config,
				}
				if err := store.PutDesired(ap.ID, desired); err != nil {
					return Applied{}, err
				}
			}
			continue
		}
		next := *ap
		next.ConfigRevision = revision
		next.ConfigUpdatedAt = opts.Now
		input, err := InputFor(opts.Template, *state, ap.ID)
		if err != nil {
			return Applied{}, &APError{APID: ap.ID, Err: err}
		}
		input.AP = next
		input.Time = opts.Now
		output, err := AP(input)
		if err != nil {
			return Applied{}, &APError{APID: ap.ID, Err: err}
		}
		applied.Pushes = append(applied.Pushes, Push{APID: ap.ID, Config: output.Config, Revision: revision})
		if opts.DryRun {
			continue
		}
		*ap = next
		if store != nil {
			desired := model.DesiredConfig{Revision: revision, Timestamp: ConfigTimestamp(opts.Now), Bytes: output.Config}
			if err := store.PutDesired(ap.ID, desired); err != nil {
				return Applied{}, err
			}
		}
	}
	sort.Slice(applied.Pushes, func(i, j int) bool { return applied.Pushes[i].APID < applied.Pushes[j].APID })
	return applied, nil
}

// DeliveredLookup reads stored ap_config.delivered entries.
// *model.APConfigStore implements it.
type DeliveredLookup interface {
	Delivered(apID string) (model.DeliveredConfig, bool, error)
}

// Config sources for Served.
const (
	SourceCompiled  = "compiled"
	SourceDelivered = "delivered"
	SourceNone      = "none"
)

// Skip reasons for Served.
const (
	SkipNoSite   = "no_site"
	SkipNoSecret = "no_secret"
)

// Served is what to serve one AP at startup or on (re-)registration.
type Served struct {
	APID   string
	SiteID string
	// Config is nil when there is nothing to serve.
	Config   []byte
	Revision uint64
	// Source is SourceCompiled, SourceDelivered (the compile failed and the
	// AP keeps getting the last config it received, with its original
	// Timestamp), or SourceNone (the compile failed and nothing was ever
	// delivered).
	Source string
	// Err is the compile error when Source is not SourceCompiled.
	Err error
	// Skip is set when the AP is not served at all: SkipNoSite (not
	// assigned, as in v0) or SkipNoSecret (an attention item, not a
	// registry failure).
	Skip string
}

// Prepare decides, for every AP in state and each on its own, what config
// to serve (design §4.4 "One AP never stops another"). Pass the delivered
// store so a compile failure falls back to the delivered bytes; nil
// disables the fallback. The result is sorted by AP ID.
func Prepare(template []byte, state model.State, delivered DeliveredLookup) []Served {
	result := make([]Served, 0, len(state.APs))
	for _, ap := range state.APs {
		one := Served{APID: ap.ID, SiteID: ap.SiteID, Source: SourceNone}
		switch {
		case ap.SiteID == "":
			one.Skip = SkipNoSite
		case len(ap.Secret) != model.SecretSize:
			one.Skip = SkipNoSecret
		}
		if one.Skip != "" {
			result = append(result, one)
			continue
		}
		input, err := InputFor(template, state, ap.ID)
		var output Output
		if err == nil {
			output, err = AP(input)
		}
		if err == nil {
			one.Config, one.Revision, one.Source = output.Config, ConfigRevision(state, ap), SourceCompiled
			result = append(result, one)
			continue
		}
		one.Err = err
		if delivered != nil {
			if last, found, lookupErr := delivered.Delivered(ap.ID); lookupErr == nil && found && len(last.Bytes) != 0 {
				one.Config, one.Revision, one.Source = append([]byte(nil), last.Bytes...), last.Revision, SourceDelivered
			} else if lookupErr != nil {
				one.Err = errors.Join(err, fmt.Errorf("read delivered config: %w", lookupErr))
			}
		}
		result = append(result, one)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].APID < result[j].APID })
	return result
}
