package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
	"github.com/nicko170/dismist/internal/rfplan"
	"golang.org/x/crypto/ssh"
)

// The schema-2 migration (design §4.7): lift typed values from each AP's
// effective config, store minimal residual expert patches under new keys,
// prove that every AP's normalised effective config is byte-identical, and
// only then commit. The legacy keys (advanced, ssh_public_keys) are never
// touched, so an older binary still compiles today's config; compat stays
// 1. Anything that cannot be proven equivalent is a refusal, and dismist
// keeps running in legacy mode.

// Refusal reasons (stable codes in the report).
const (
	ReasonBSSIDUnresolved = "bssid_unresolved"
	ReasonBSSIDDiffer     = "bssid_indexes_differ"
	ReasonLocalFiles      = "local_files"
	ReasonNotEquivalent   = "not_equivalent"
	ReasonLegacyCompile   = "legacy_compile_failed"
	ReasonTypedCompile    = "typed_compile_failed"
)

// SyntheticAPID is the AP a site with no APs is lifted from.
const SyntheticAPID = "02-00-00-00-00-00"

// V2Step returns the schema-2 migration step.
func V2Step() Step {
	return Step{From: 1, To: 2, Name: "schema-2 typed settings", Run: runV2}
}

// V2Report is the lift report (meta.migration_v2_report, the dry run's
// output, /system/migration). It carries paths and redacted patches only,
// never secrets.
type V2Report struct {
	At          time.Time     `json:"at"`
	Verified    bool          `json:"verified"`
	Reason      string        `json:"reason,omitempty"`
	Code        string        `json:"code,omitempty"`
	Paths       []string      `json:"paths,omitempty"`
	Sites       []SiteLift    `json:"sites"`
	Networks    []NetworkLift `json:"networks"`
	APs         []APLift      `json:"aps"`
	H0          []H0Check     `json:"h0,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	Remigration *Remigration  `json:"remigration,omitempty"`
}

// Remigration describes a re-migration after an older binary wrote.
type Remigration struct {
	FromRevision uint64 `json:"from_revision"` // meta.v2_revision
	ToRevision   uint64 `json:"to_revision"`   // meta.revision
}

// SiteLift is what the migration lifted for one site.
type SiteLift struct {
	ID            string          `json:"id"`
	Probe         string          `json:"probe"` // AP ID, or "synthetic"
	RFManaged     bool            `json:"rf_managed"`
	RFReason      string          `json:"rf_reason,omitempty"`
	Typed         json.RawMessage `json:"typed"`
	Residual      json.RawMessage `json:"residual,omitempty"`
	ResidualPaths []string        `json:"residual_paths,omitempty"`
	Dropped       []string        `json:"dropped,omitempty"`
}

// NetworkLift is what the migration lifted for one network.
type NetworkLift struct {
	ID            string          `json:"id"`
	SiteID        string          `json:"site_id"`
	SSID          string          `json:"ssid"`
	Security      string          `json:"security"`
	BSSIDIndex    int             `json:"bssid_index"`
	BSSIDSource   string          `json:"bssid_source"` // "measured" | "predicted"
	Typed         json.RawMessage `json:"typed"`
	Residual      json.RawMessage `json:"residual,omitempty"`
	ResidualPaths []string        `json:"residual_paths,omitempty"`
	Dropped       []string        `json:"dropped,omitempty"`
}

// APLift is the lift and verification of one AP.
type APLift struct {
	ID            string          `json:"id"`
	SiteID        string          `json:"site_id"`
	Compiled      bool            `json:"compiled"` // false: no site, v0 serves nothing
	LocalFiles    string          `json:"local_files"`
	RFOverride    json.RawMessage `json:"rf_override,omitempty"`
	Residual      json.RawMessage `json:"residual,omitempty"`
	ResidualPaths []string        `json:"residual_paths,omitempty"`
	Dropped       []string        `json:"dropped,omitempty"`
	OldHash       string          `json:"old_hash,omitempty"`
	NewHash       string          `json:"new_hash,omitempty"`
	Equal         bool            `json:"equal"`
	Mismatch      []string        `json:"mismatch,omitempty"`
}

// H0Check compares the phase-0 baseline, recomputed with this normaliser,
// with the AP's current effective hash.
type H0Check struct {
	APID        string `json:"ap_id"`
	Recorded    string `json:"recorded"`
	Recomputed  string `json:"recomputed"`
	Current     string `json:"current"`
	MatchesNow  bool   `json:"matches_now"`
	RecordedVer int    `json:"recorded_version"`
}

// V2Input is everything the migration reads; it is gathered before
// anything is written.
type V2Input struct {
	// Frozen is the raw bucket JSON decoded with legacyv0's structs.
	Frozen legacyv0.State
	// Current is the model state (shared and typed fields).
	Current model.State
	// Template is the runtime template (nil: the frozen bundled copy).
	Template []byte
	// VAPs are the last-known field-32 VAP lists by AP ID.
	VAPs             map[string][]model.VAPRecord
	AcceptLocalFiles []string
	HashKey          []byte
	H0               map[string]model.H0Record
	Now              time.Time
}

// V2Result is the planned schema-2 state with its report.
type V2Result struct {
	State  model.State
	Report V2Report
	// Desired holds each verified AP's v2 compile (seeds ap_config).
	Desired map[string][]byte
}

type apContext struct {
	ap     model.APRecord
	raw0   []byte
	old    []byte
	folded map[string]any
	ctx    effective.Context
}

func refuse(report *V2Report, code, reason string, paths ...string) *RefusedError {
	report.Verified = false
	report.Code, report.Reason, report.Paths = code, reason, paths
	return &RefusedError{Reason: reason, Paths: paths}
}

// PlanV2 runs the lift, residual and verification steps (design §4.7 steps
// 2 to 5) without writing anything. A refusal returns *RefusedError with
// the report explaining it.
func PlanV2(in V2Input) (V2Result, error) {
	report := V2Report{At: in.Now.UTC(), Sites: []SiteLift{}, Networks: []NetworkLift{}, APs: []APLift{}}
	result := V2Result{Desired: map[string][]byte{}}
	template := legacyv0.Template(in.Template)
	accepted := map[string]bool{}
	for _, id := range in.AcceptLocalFiles {
		accepted[strings.TrimSpace(id)] = true
	}
	records := map[string]model.APRecord{}
	for _, ap := range in.Current.APs {
		records[ap.ID] = ap
	}

	// Step 2: old effective config of every AP v0 compiles.
	contexts := map[string]*apContext{}
	for _, frozen := range in.Frozen.APs {
		ap := records[frozen.ID]
		lift := APLift{ID: frozen.ID, SiteID: frozen.SiteID, LocalFiles: effective.LocalFilesState(ap.LocalFiles)}
		raw0, err := legacyv0.CompileAP(template, in.Frozen, frozen.ID)
		if errors.Is(err, legacyv0.ErrNoSite) {
			report.APs = append(report.APs, lift)
			continue
		}
		if err != nil {
			report.APs = append(report.APs, lift)
			if _, siteExists := in.Frozen.Site(frozen.SiteID); !siteExists {
				report.Warnings = append(report.Warnings, fmt.Sprintf("AP %s names a site that does not exist; v0 serves it nothing", frozen.ID))
				continue
			}
			return V2Result{Report: report}, refuse(&report, ReasonLegacyCompile, fmt.Sprintf("AP %s does not compile with the legacy compiler: %v", frozen.ID, err))
		}
		lift.Compiled = true
		ctx := compile.EffectiveContext(ap)
		if lift.LocalFiles == effective.LocalFilesPresent {
			if !accepted[frozen.ID] {
				report.APs = append(report.APs, lift)
				return V2Result{Report: report}, refuse(&report, ReasonLocalFiles,
					fmt.Sprintf("AP %s has local config files on the AP; equivalence cannot be proven (accept with -migrate-accept-local-files=%s)", frozen.ID, frozen.ID))
			}
			report.Warnings = append(report.Warnings, fmt.Sprintf("AP %s: local files accepted by the operator", frozen.ID))
		}
		if lift.LocalFiles == effective.LocalFilesUnknown {
			report.Warnings = append(report.Warnings, fmt.Sprintf("AP %s: local files never checked (dismist ap33 inspect)", frozen.ID))
		}
		old, normReport, err := effective.Normalise(raw0, ctx)
		if err != nil {
			report.APs = append(report.APs, lift)
			return V2Result{Report: report}, refuse(&report, ReasonLegacyCompile, fmt.Sprintf("AP %s: normalise: %v", frozen.ID, err))
		}
		for _, note := range normReport.Notes {
			if note == effective.NoteBSSIDUnresolved {
				report.APs = append(report.APs, lift)
				return V2Result{Report: report}, refuse(&report, ReasonBSSIDUnresolved,
					fmt.Sprintf("AP %s: a disabled WLAN ahead of an enabled one (or an unresolvable BSSID layout); the BSSID indexes cannot be predicted", frozen.ID))
			}
		}
		folded, err := compile.FoldConfig(raw0)
		if err != nil {
			return V2Result{Report: report}, refuse(&report, ReasonLegacyCompile, fmt.Sprintf("AP %s: fold: %v", frozen.ID, err))
		}
		if len(in.HashKey) > 0 {
			lift.OldHash, _ = effective.Hash(in.HashKey, old)
		}
		contexts[frozen.ID] = &apContext{ap: ap, raw0: raw0, old: old, folded: folded, ctx: ctx}
		report.APs = append(report.APs, lift)
	}

	// S1: the current state with typed fields lifted. Records are copied
	// field by field; the legacy fields are never modified.
	s1 := in.Current
	s1.Schema = 2
	s1.Sites = append([]model.Site(nil), in.Current.Sites...)
	s1.APs = append([]model.APRecord(nil), in.Current.APs...)
	s1.Networks = append([]model.Network(nil), in.Current.Networks...)
	for index := range s1.APs {
		s1.APs[index].Expert, s1.APs[index].RF, s1.APs[index].RFPlan = nil, nil, nil
		s1.APs[index].LEDBrightness, s1.APs[index].SSHEnabled = nil, nil
	}

	sitesDone := map[string]bool{}
	for siteIndex := range s1.Sites {
		site := &s1.Sites[siteIndex]
		frozenSite, _ := in.Frozen.Site(site.ID)
		if err := planSite(in, template, &s1, site, frozenSite, contexts, &report); err != nil {
			var refused *RefusedError
			if errors.As(err, &refused) {
				return V2Result{Report: report}, err
			}
			return V2Result{Report: report}, refuse(&report, ReasonTypedCompile, err.Error())
		}
		sitesDone[site.ID] = true
	}
	// Networks of no existing site (legacy, empty SiteID with no sites):
	// lifted from a synthetic AP on a synthetic site; nothing serves them.
	orphans := map[string]bool{}
	for _, network := range s1.Networks {
		if !sitesDone[network.SiteID] {
			orphans[network.SiteID] = true
		}
	}
	for siteID := range orphans {
		frozenSite := legacyv0.Site{ID: siteID, Name: "legacy"}
		site := model.Site{ID: siteID, Name: "legacy"}
		if err := planSite(in, template, &s1, &site, frozenSite, nil, &report); err != nil {
			var refused *RefusedError
			if errors.As(err, &refused) {
				return V2Result{Report: report}, err
			}
			return V2Result{Report: report}, refuse(&report, ReasonTypedCompile, err.Error())
		}
	}

	// Step 5: verify every compiled AP byte for byte.
	for index := range report.APs {
		lift := &report.APs[index]
		c := contexts[lift.ID]
		if c == nil {
			continue
		}
		input, err := compile.InputFor(template, s1, lift.ID)
		if err != nil {
			return V2Result{Report: report}, refuse(&report, ReasonTypedCompile, fmt.Sprintf("AP %s: %v", lift.ID, err))
		}
		output, err := compile.AP(input)
		if err != nil {
			return V2Result{Report: report}, refuse(&report, ReasonTypedCompile, fmt.Sprintf("AP %s: typed compile: %v", lift.ID, err))
		}
		canonical, _, err := effective.Normalise(output.Config, c.ctx)
		if err != nil {
			return V2Result{Report: report}, refuse(&report, ReasonTypedCompile, fmt.Sprintf("AP %s: normalise: %v", lift.ID, err))
		}
		if len(in.HashKey) > 0 {
			lift.NewHash, _ = effective.Hash(in.HashKey, canonical)
		}
		lift.Equal = string(canonical) == string(c.old)
		if !lift.Equal {
			lift.Mismatch = compile.DiffPaths(c.old, canonical)
			return V2Result{Report: report}, refuse(&report, ReasonNotEquivalent,
				fmt.Sprintf("AP %s: the typed config is not equivalent to what the AP runs", lift.ID), lift.Mismatch...)
		}
		result.Desired[lift.ID] = output.Config
	}

	// H0, recomputed with this normaliser.
	for _, apID := range sortedKeys(in.H0) {
		record := in.H0[apID]
		check := H0Check{APID: apID, Recorded: record.EffectiveHash, RecordedVer: record.NormaliserVersion}
		if c := contexts[apID]; c != nil && len(in.HashKey) > 0 {
			if hash, _, _, err := effective.HashConfig(in.HashKey, record.Raw, c.ctx); err == nil {
				check.Recomputed = hash
			}
			check.Current, _ = effective.Hash(in.HashKey, c.old)
			check.MatchesNow = check.Recomputed != "" && check.Recomputed == check.Current
			if !check.MatchesNow {
				report.Warnings = append(report.Warnings, fmt.Sprintf("AP %s: H0 recomputed does not match the current legacy config (the config changed since phase 0)", apID))
			}
		}
		report.H0 = append(report.H0, check)
	}
	report.Verified = true
	result.State = s1
	result.Report = report
	return result, nil
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// planSite lifts one site, its networks and its APs, and computes the
// residuals.
func planSite(in V2Input, template []byte, s1 *model.State, site *model.Site, frozenSite legacyv0.Site, contexts map[string]*apContext, report *V2Report) error {
	// The probe: the site's first AP (by ID) without its AP patch, or a
	// synthetic AP.
	var siteAPs []string
	for _, ap := range in.Frozen.APs {
		if ap.SiteID == site.ID && contexts[ap.ID] != nil {
			siteAPs = append(siteAPs, ap.ID)
		}
	}
	sort.Strings(siteAPs)
	probeFrozen := legacyv0.APRecord{ID: SyntheticAPID, SiteID: site.ID}
	probeRecord := model.APRecord{ID: SyntheticAPID, SiteID: site.ID, LocalFiles: []model.LocalFile{}}
	probeName := "synthetic"
	probeTime := in.Frozen.UpdatedAt
	if len(siteAPs) > 0 {
		frozen, _ := in.Frozen.AP(siteAPs[0])
		probeFrozen = frozen
		probeFrozen.Advanced = nil
		probeRecord = contexts[siteAPs[0]].ap
		probeName = siteAPs[0]
		probeTime = in.Frozen.ConfigTime(frozen)
	}
	networks := in.Frozen.NetworksForSite(site.ID)
	probeRaw0, err := legacyv0.BuildSiteConfig(template, frozenSite, probeFrozen, networks, probeTime)
	if err != nil {
		return refuse(report, ReasonLegacyCompile, fmt.Sprintf("site %s: legacy compile of the probe AP: %v", site.ID, err))
	}
	probeCtx := compile.EffectiveContext(probeRecord)
	probeOld, _, err := effective.Normalise(probeRaw0, probeCtx)
	if err != nil {
		return err
	}
	probeFolded, err := compile.FoldConfig(probeRaw0)
	if err != nil {
		return err
	}
	effectiveProbe, err := compile.DecodePatch(probeOld)
	if err != nil {
		return err
	}

	// Step 3: lift site and WLAN typed values from the probe's effective
	// config.
	siteLift := SiteLift{ID: site.ID, Probe: probeName}
	liftSite(site, frozenSite, effectiveProbe, &siteLift)
	lifts := map[string]*NetworkLift{}
	for index := range s1.Networks {
		network := &s1.Networks[index]
		if network.SiteID != site.ID {
			continue
		}
		lift := &NetworkLift{ID: network.ID, SiteID: network.SiteID, SSID: network.SSID}
		if err := liftNetwork(network, effectiveProbe, siteAPs, in.VAPs, lift); err != nil {
			report.Networks = append(report.Networks, *lift)
			var refused *RefusedError
			if errors.As(err, &refused) {
				return refuse(report, ReasonBSSIDDiffer, refused.Reason)
			}
			return err
		}
		lifts[network.ID] = lift
	}
	// Networks the normaliser left unindexed (disabled, nothing measured)
	// take the lowest index no other network of the site uses.
	used := map[int]bool{}
	for _, network := range s1.Networks {
		if network.SiteID == site.ID && network.BSSIDIndex > 0 {
			used[network.BSSIDIndex] = true
		}
	}
	for index := range s1.Networks {
		network := &s1.Networks[index]
		if network.SiteID == site.ID && network.BSSIDIndex == 0 {
			network.BSSIDIndex = compile.FreeBSSIDIndex(used)
			used[network.BSSIDIndex] = true
			if lift := lifts[network.ID]; lift != nil {
				lift.BSSIDIndex = network.BSSIDIndex
			}
		}
	}

	// AP typed values for the probe (site-scope residuals) and each AP.
	probeTyped := probeRecord
	probeTyped.SiteID = site.ID
	probeTyped.Advanced = nil
	liftAP(&probeTyped, *site, effectiveProbe, probeFolded)
	for index := range s1.APs {
		ap := &s1.APs[index]
		if c := contexts[ap.ID]; c != nil && ap.SiteID == site.ID {
			effectiveAP, err := compile.DecodePatch(c.old)
			if err != nil {
				return err
			}
			liftAP(ap, *site, effectiveAP, c.folded)
		}
	}

	compileProbe := func() ([]byte, error) {
		input := compile.Input{Template: template, Site: *site, AP: probeTyped,
			Networks: compile.NetworksForSite(*s1, site.ID), Time: probeTime, Schema: 2}
		output, err := compile.AP(input)
		return output.Config, err
	}
	probeEqual := func() (bool, error) {
		config, err := compileProbe()
		if err != nil {
			return false, err
		}
		canonical, _, err := effective.Normalise(config, probeCtx)
		return err == nil && string(canonical) == string(probeOld), err
	}

	// Step 4: residuals in compile order. WLANs first.
	for index := range s1.Networks {
		network := &s1.Networks[index]
		lift := lifts[network.ID]
		if lift == nil {
			continue
		}
		raw1, err := compileProbe()
		if err != nil {
			return fmt.Errorf("network %s: typed compile: %w", network.ID, err)
		}
		folded1, err := compile.FoldConfig(raw1)
		if err != nil {
			return err
		}
		residual, dropped, err := compile.ResidualDiff(compile.ScopeWLAN, folded1, probeFolded, network.ID)
		if err != nil {
			return err
		}
		lift.Dropped = dropped
		if err := prune(residual, "WLANs[]", func(patch map[string]any) (bool, error) {
			network.Expert, err = compile.EncodePatch(patch)
			if err != nil {
				return false, err
			}
			return probeEqual()
		}, &network.Expert); err != nil {
			return err
		}
		if len(network.Expert) > 0 {
			lift.Residual = compile.RedactPatch(network.Expert)
			patch, _ := compile.DecodePatch(network.Expert)
			lift.ResidualPaths = compile.PatchPaths(patch, "WLANs[]")
		}
		report.Networks = append(report.Networks, *lift)
	}
	// Then the site, against the probe.
	raw1, err := compileProbe()
	if err != nil {
		return fmt.Errorf("site %s: typed compile: %w", site.ID, err)
	}
	folded1, err := compile.FoldConfig(raw1)
	if err != nil {
		return err
	}
	residual, dropped, err := compile.ResidualDiff(compile.ScopeSite, folded1, probeFolded, "")
	if err != nil {
		return err
	}
	siteLift.Dropped = dropped
	if err := prune(residual, "", func(patch map[string]any) (bool, error) {
		site.Expert, err = compile.EncodePatch(patch)
		if err != nil {
			return false, err
		}
		return probeEqual()
	}, &site.Expert); err != nil {
		return err
	}
	if len(site.Expert) > 0 {
		siteLift.Residual = compile.RedactPatch(site.Expert)
		patch, _ := compile.DecodePatch(site.Expert)
		siteLift.ResidualPaths = compile.PatchPaths(patch, "")
	}
	if typed, err := json.Marshal(struct {
		Country    string                   `json:"country"`
		SSH        model.SiteSSH            `json:"ssh"`
		Syslog     []model.SyslogTarget     `json:"syslog,omitempty"`
		Management model.ManagementDefaults `json:"management"`
		APDefaults model.APDefaults         `json:"ap_defaults"`
		Protection model.Protection         `json:"protection"`
		RF         model.RFProfile          `json:"rf"`
	}{site.Country, site.SSH, site.Syslog, site.Management, site.APDefaults, site.Protection, site.RF}); err == nil {
		siteLift.Typed = typed
	}
	report.Sites = append(report.Sites, siteLift)

	// Then each AP, against its full v0 output.
	for index := range s1.APs {
		ap := &s1.APs[index]
		c := contexts[ap.ID]
		if c == nil || ap.SiteID != site.ID {
			continue
		}
		compileAP := func() ([]byte, error) {
			input, err := compile.InputFor(template, *s1, ap.ID)
			if err != nil {
				return nil, err
			}
			output, err := compile.AP(input)
			return output.Config, err
		}
		raw1, err := compileAP()
		if err != nil {
			return fmt.Errorf("AP %s: typed compile: %w", ap.ID, err)
		}
		folded1, err := compile.FoldConfig(raw1)
		if err != nil {
			return err
		}
		residual, dropped, err := compile.ResidualDiff(compile.ScopeAP, folded1, c.folded, "")
		if err != nil {
			return err
		}
		if err := prune(residual, "", func(patch map[string]any) (bool, error) {
			ap.Expert, err = compile.EncodePatch(patch)
			if err != nil {
				return false, err
			}
			config, err := compileAP()
			if err != nil {
				return false, err
			}
			canonical, _, err := effective.Normalise(config, c.ctx)
			return err == nil && string(canonical) == string(c.old), err
		}, &ap.Expert); err != nil {
			return err
		}
		for reportIndex := range report.APs {
			lift := &report.APs[reportIndex]
			if lift.ID != ap.ID {
				continue
			}
			lift.Dropped = dropped
			if ap.RF != nil {
				lift.RFOverride, _ = json.Marshal(ap.RF)
			}
			if len(ap.Expert) > 0 {
				lift.Residual = compile.RedactPatch(ap.Expert)
				patch, _ := compile.DecodePatch(ap.Expert)
				lift.ResidualPaths = compile.PatchPaths(patch, "")
			}
		}
	}
	return nil
}

// prune stores the full residual, checks it, then removes each leaf in
// sorted pointer order and keeps the removal only when the scope still
// verifies (design §4.7 step 4.3 and 4.4). check stores the patch it is
// given into the scope's expert field and verifies. On return the scope's
// expert field holds the pruned residual. A residual that does not verify
// even in full is left in place; the final verification then refuses.
func prune(residual map[string]any, root string, check func(map[string]any) (bool, error), target *json.RawMessage) error {
	ok, err := check(residual)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	current := residual
	for _, leaf := range compile.ResidualLeaves(residual, root) {
		candidate := leaf.Without(current)
		ok, err := check(candidate)
		if err != nil {
			return err
		}
		if ok {
			current = candidate
		}
	}
	// Leave the scope holding the pruned residual.
	if _, err := check(current); err != nil {
		return err
	}
	raw, err := compile.EncodePatch(current)
	if err != nil {
		return err
	}
	*target = raw
	return nil
}

// Effective-config readers (canonical normalised JSON, zero values
// pruned: absent means zero).

func objectAt(root map[string]any, keys ...string) map[string]any {
	current := root
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func valueAt(root map[string]any, keys ...string) any {
	if len(keys) == 0 {
		return nil
	}
	parent := objectAt(root, keys[:len(keys)-1]...)
	if parent == nil {
		return nil
	}
	return parent[keys[len(keys)-1]]
}

func intAt(root map[string]any, keys ...string) int {
	switch v := valueAt(root, keys...).(type) {
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			f, _ := v.Float64()
			return int(f)
		}
		return int(n)
	case float64:
		return int(v)
	}
	return 0
}

func boolAt(root map[string]any, keys ...string) bool {
	b, _ := valueAt(root, keys...).(bool)
	return b
}

func stringAt(root map[string]any, keys ...string) string {
	s, _ := valueAt(root, keys...).(string)
	return s
}

func has(root map[string]any, keys ...string) bool {
	parent := objectAt(root, keys[:len(keys)-1]...)
	if parent == nil {
		return false
	}
	_, ok := parent[keys[len(keys)-1]]
	return ok
}

func elementBy(root map[string]any, array, key, value string) map[string]any {
	list, _ := root[array].([]any)
	for _, element := range list {
		object, ok := element.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprint(object[key]) == value {
			return object
		}
	}
	return nil
}

func stringsAt(root map[string]any, keys ...string) []string {
	list, _ := valueAt(root, keys...).([]any)
	var out []string
	for _, value := range list {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

var preambleNames = map[int]string{0: "auto", 1: "short", 2: "long"}
var tafNames = map[int]string{0: "default", 1: "off", 2: "on"}
var antennaNames = map[int]string{0: "default", 15: "4x4", 7: "3x3", 3: "2x2", 1: "1x1"}

// liftSite lifts the site-scoped typed values (design §4.6 legacy values,
// §4.7 step 3).
func liftSite(site *model.Site, frozen legacyv0.Site, eff map[string]any, lift *SiteLift) {
	// SSH: the parsed legacy keys; enabled when keys exist.
	site.SSH = model.SiteSSH{Enabled: len(frozen.SSHPublicKeys) > 0}
	for _, line := range frozen.SSHPublicKeys {
		key, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			site.SSH.Keys = append(site.SSH.Keys, model.SSHKey{Key: strings.TrimSpace(line)})
			continue
		}
		site.SSH.Keys = append(site.SSH.Keys, model.SSHKey{Key: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), Comment: comment})
	}
	// Syslog targets.
	site.Syslog = nil
	if list, ok := eff["Syslogs"].([]any); ok {
		for _, element := range list {
			entry, _ := element.(map[string]any)
			address := stringAt(entry, "Address")
			if address == "" {
				continue
			}
			host, portText, _ := strings.Cut(address, ":")
			if index := strings.LastIndex(address, ":"); index >= 0 {
				host, portText = strings.Trim(address[:index], "[]"), address[index+1:]
			}
			port, _ := strconv.Atoi(portText)
			priority := intAt(entry, "Priority")
			protocol := stringAt(entry, "Network")
			if protocol == "" {
				protocol = "udp"
			}
			site.Syslog = append(site.Syslog, model.SyslogTarget{Protocol: protocol, Host: host, Port: port,
				Facility: priority / 8, Severity: priority % 8, Tag: stringAt(entry, "Tag")})
		}
	}
	site.Management = model.ManagementDefaults{VLANID: 1, NTP: stringsAt(eff, "NTP"), DNS: stringsAt(eff, "DNS")}
	if svis, ok := eff["SVIs"].([]any); ok && len(svis) > 0 {
		if svi, ok := svis[0].(map[string]any); ok {
			if vlan, err := strconv.Atoi(stringAt(svi, "Vlan_ID")); err == nil && vlan > 1 {
				site.Management.VLANID = vlan
			}
		}
	}
	poe := model.PoEDisableRadios
	if boolAt(eff, "PoePwrConstrained", "EnableRadios") {
		poe = model.PoEKeepRadios
	}
	site.APDefaults = model.APDefaults{
		LEDBrightness:           intAt(eff, "LED", "Brightness"),
		HostnameFromName:        false,
		KeepWLANsUpOnUplinkDown: !boolAt(eff, "Monitor", "EthUplink", "Enabled"),
		PoEConstrained:          poe,
		WiredClientStats:        boolAt(eff, "EnableWiredClientStats"),
		BLEDisabled:             boolAt(eff, "BLE", "Disabled"),
	}
	if recovery := objectAt(eff, "APRecovery"); recovery != nil && (!boolAt(recovery, "Disabled") || intAt(recovery, "RecoveryTimeMin") != 0) {
		site.APDefaults.Recovery = &model.Recovery{Enabled: !boolAt(recovery, "Disabled"), Minutes: intAt(recovery, "RecoveryTimeMin")}
	}
	site.Protection = model.Protection{
		ProxyARP:        boolAt(eff, "ProxyArp", "Enabled"),
		ARPSpoofCheck:   !boolAt(eff, "ProxyArp", "Disable_ReqSpoofCheck"),
		DHCPFingerprint: boolAt(eff, "Fingerprint", "DHCPPayload"),
	}
	for _, mac := range stringsAt(eff, "BlackList", "WCMacs") {
		site.Protection.BlockedClients = append(site.Protection.BlockedClients, model.BlockedClient{MAC: strings.ToLower(mac)})
	}
	if wids, ok := eff["WIDS"].([]any); ok {
		for _, element := range wids {
			entry, _ := element.(map[string]any)
			if stringAt(entry, "Alarm") == "PenaltyBox" && boolAt(entry, "Enabled") {
				site.Protection.PenaltyBox = &model.PenaltyBox{Threshold: intAt(entry, "AlarmCounter"), WindowSec: intAt(entry, "Interval")}
			}
		}
	}

	// RF: managed when both access radios run the country's ccrev and
	// every value fits the typed form; the scan radio is not considered.
	site.RF = defaultRF(site.Country)
	ccrev, supported := regdomain.CCRev(site.Country)
	domain, _ := regdomain.ForCountry(site.Country)
	managed := supported
	reason := ""
	if !supported {
		reason = "country has no verified ccrev"
	}
	for _, band := range rfplan.Bands {
		radio := elementBy(eff, "Radios", "Band", band)
		if radio == nil {
			managed, reason = false, band+" GHz radio missing"
			continue
		}
		if managed && stringAt(radio, "ccrev") != ccrev {
			managed, reason = false, fmt.Sprintf("%s GHz radio runs %s, not %s", band, stringAt(radio, "ccrev"), ccrev)
		}
		profile := site.RF.Band(band)
		profile.Enabled = !boolAt(radio, "Disabled")
		width, power, channel := intAt(radio, "width"), intAt(radio, "power"), intAt(radio, "channel")
		profile.Width = width
		if managed {
			table := domain.Table(band, width)
			switch {
			case table == nil || table.ExpertOnly:
				managed, reason = false, fmt.Sprintf("%s GHz width %d MHz has no typed form", band, width)
			case profile.Enabled && power == 0:
				managed, reason = false, fmt.Sprintf("%s GHz power 0 (0 dBm target) has no typed form", band)
			case power > rfplan.MaxPower:
				managed, reason = false, fmt.Sprintf("%s GHz power %d dBm is out of range", band, power)
			}
			if _, valid := domain.Valid(band, width, channel); managed && !valid {
				managed, reason = false, fmt.Sprintf("%s GHz channel %d is not valid at %d MHz", band, channel, width)
			}
		}
		if power > 0 {
			profile.Power = model.PowerPolicy{Mode: model.PowerFixed, DBm: power}
		}
		profile.Preamble = preambleNames[intAt(radio, "PreambleLength")]
		profile.Advanced.TAF = tafNames[intAt(radio, "TAFMode")]
		twt := !boolAt(radio, "TWTDisabled")
		profile.Advanced.TWT = &twt
		if name, ok := antennaNames[intAt(radio, "ChainSelectMode", "RxChain")]; ok && intAt(radio, "ChainSelectMode", "RxChain") == intAt(radio, "ChainSelectMode", "TxChain") {
			profile.Advanced.Antenna = name
		}
		if boolAt(radio, "ProbeControl", "NoProbeRespRSSI") {
			threshold := intAt(radio, "ProbeControl", "NoProbeRespRSSIThresh")
			profile.Advanced.MinProbeRSSI = &threshold
		}
		if boolAt(radio, "MuMimo", "Enabled") {
			on := true
			profile.Advanced.MuMimo = &on
		}
		if boolAt(radio, "TxBf", "Enabled") {
			on := true
			profile.Advanced.TxBf = &on
		}
		if band == "5" && domain != nil {
			if dfs, _ := domain.Valid(band, width, channel); dfs {
				profile.DFS = true
			}
		}
	}
	if !boolAt(eff, "Monitor", "Enabled") {
		off := false
		site.RF.Scanning = &off
	}
	site.RF.Managed = managed
	if !managed {
		// The firmware defaults stay; the profile holds new-site defaults
		// for when the operator turns management on.
		fresh := defaultRF(site.Country)
		fresh.Scanning = site.RF.Scanning
		site.RF = fresh
	}
	lift.RFManaged, lift.RFReason = managed, reason
}

// defaultRF is the RF profile of a new site (design §3.10), unmanaged.
func defaultRF(country string) model.RFProfile {
	rf := model.RFProfile{
		Band24: model.BandProfile{Enabled: true, Width: 20, Power: rfplan.DefaultPower["2.4"], Preamble: "short"},
		Band5:  model.BandProfile{Enabled: true, Width: 80, Power: rfplan.DefaultPower["5"], Preamble: "short"},
	}
	if domain, ok := regdomain.ForCountry(country); ok {
		rf.Band24.Channels = append([]int(nil), domain.DefaultAuto["2.4"]...)
		rf.Band5.Channels = append([]int(nil), domain.DefaultAuto["5"]...)
	}
	return rf
}

// liftAP lifts an AP's typed values: per-band static channels (every AP of
// a managed site gets its own resolved channel, design §4.7) and overrides
// that differ from the site.
func liftAP(ap *model.APRecord, site model.Site, eff map[string]any, raw0 map[string]any) {
	ap.RF, ap.RFPlan, ap.LEDBrightness, ap.SSHEnabled, ap.Expert = nil, nil, nil, nil, nil
	if brightness := intAt(eff, "LED", "Brightness"); brightness != site.APDefaults.LEDBrightness {
		ap.LEDBrightness = &brightness
	}
	if !site.RF.Managed {
		return
	}
	for _, band := range rfplan.Bands {
		radio := elementBy(eff, "Radios", "Band", band)
		if radio == nil {
			continue
		}
		override := &model.RadioOverride{Channel: intAt(radio, "channel")}
		enabled := !boolAt(radio, "Disabled")
		if enabled != site.RF.Band(band).Enabled {
			override.Enabled = &enabled
		}
		if width := intAt(radio, "width"); width != site.RF.Band(band).Width {
			override.Width = width
		}
		if power := intAt(radio, "power"); power != 0 && (site.RF.Band(band).Power.Mode != model.PowerFixed || power != site.RF.Band(band).Power.DBm) {
			override.PowerDBm = power
		}
		if ap.RF == nil {
			ap.RF = map[string]*model.RadioOverride{}
		}
		ap.RF[band] = override
	}
	_ = raw0
}

// liftNetwork lifts one network's typed values from its WLAN in the
// probe's effective config, and its BSSID index from field 32 (measured)
// or the normaliser's rule 4 (predicted).
func liftNetwork(network *model.Network, eff map[string]any, siteAPs []string, vaps map[string][]model.VAPRecord, lift *NetworkLift) error {
	w := elementBy(eff, "WLANs", "ID", network.ID)
	if w == nil {
		return fmt.Errorf("network %s is missing from the compiled config", network.ID)
	}
	authType := stringAt(w, "Auth", "Type")
	akm := func(flag string) bool { return boolAt(w, "Auth", "AKM", flag) }
	dot11wOn, dot11wReq := boolAt(w, "Dot11w", "Enabled"), boolAt(w, "Dot11w", "Required")
	network.PMF = ""
	switch {
	case authType == "psk" && akm("WPA2_PSK") && akm("WPA3_PSK"):
		network.Security = model.SecurityWPA2WPA3Personal
	case authType == "psk" && akm("WPA3_PSK"):
		network.Security = model.SecurityWPA3Personal
	case authType == "psk":
		network.Security = model.SecurityWPA2Personal
		network.PMF = pmfName(dot11wOn, dot11wReq)
	case authType == "open" && boolAt(w, "OWE", "Transition"):
		network.Security = model.SecurityOWETransition
		network.OWESSID = stringAt(w, "OWE", "SSID")
	case authType == "open":
		network.Security = model.SecurityOpen
	case authType == "owe":
		network.Security = model.SecurityOWE
	case authType == "eap" && akm("WPA2_EAP") && akm("WPA3_EAP"):
		network.Security = model.SecurityWPA2WPA3Enterprise
	case authType == "eap" && akm("WPA3_EAP"):
		network.Security = model.SecurityWPA3Enterprise
	case authType == "eap":
		network.Security = model.SecurityWPA2Enterprise
		network.PMF = pmfName(dot11wOn, dot11wReq)
	default:
		network.Security = model.SecurityWPA2Personal
	}
	lift.Security = network.Security
	network.SAEH2EOnly = boolAt(w, "Auth", "SAEHash2ElementOnly")
	network.TransitionDisable = boolAt(w, "Auth", "TransitionDisableIndication")
	network.EAPOLv1 = intAt(w, "Auth", "EAPOLVersion") == 1
	network.GroupRekeySec = intAt(w, "Auth", "GroupRekeyIntervalSec")
	network.Bands = stringsAt(w, "Bands")
	network.Hidden = boolAt(w, "Hidden")
	switch {
	case boolAt(w, "Isolation") && boolAt(w, "NetIsolation"):
		network.Isolation = "clients_and_lan"
	case boolAt(w, "Isolation"):
		network.Isolation = "clients"
	default:
		network.Isolation = ""
	}
	network.BandSteering = false
	network.MinRSSI = nil
	if configs, ok := w["BandConfigs"].([]any); ok {
		for _, element := range configs {
			bc, _ := element.(map[string]any)
			band := stringAt(bc, "Band")
			if band == "2.4" && objectAt(bc, "Steer") != nil {
				network.BandSteering = true
			}
			if geo := objectAt(bc, "GeoFence"); geo != nil && boolAt(geo, "DenyDot11Auth") {
				if network.MinRSSI == nil {
					network.MinRSSI = map[string]int{}
				}
				network.MinRSSI[band] = intAt(geo, "DenyDot11AuthRSSIThresh")
			}
		}
	}
	network.Rates = liftRates(w)
	network.DTIM = intAt(w, "DTIM", "Period")
	network.IdleTimeout = intAt(w, "WCInactivity", "TimeoutSec")
	network.ReclaimTimeoutSec = intAt(w, "WCInactivity", "ReclaimTimeoutSec")
	network.MaxClients = intAt(w, "MaxStaAllowed")
	if network.MaxClients == 512 {
		network.MaxClients = 0
	}
	roam := stringAt(w, "RoamMode")
	if roam == "" {
		roam = "none"
	}
	network.Roaming = model.Roaming{
		Fast: roam, NeighborReport: boolAt(w, "WNM", "NeighborReport"), BSSLoad: boolAt(w, "WNM", "BSSLoad"),
		BSSTransition: boolAt(w, "WNM", "BSSTransitionManagement", "Passive"),
		APSteering:    boolAt(w, "WNM", "BSSTransitionManagement", "Active"),
		APKeyCache:    boolAt(w, "KeyCache", "LocalAPkeyCache"),
	}
	network.Broadcast = model.Broadcast{
		Limit: boolAt(w, "BcastFilter", "Mcast"), AllowMDNS: boolAt(w, "BcastFilter", "Mdns_allow"),
		AllowSSDP: boolAt(w, "BcastFilter", "Ssdp_allow"), AllowIPv6ND: boolAt(w, "BcastFilter", "AllowICMPv6NeighborDiscovery"),
		ARPFilter: boolAt(w, "BcastFilter", "Arp"), McastToUcast: boolAt(w, "Bcast2Ucast", "ForcedEnabled"),
		WirelessBridgeDHCP: boolAt(w, "BcastFilter", "AllowDHCPtoWirelessBridge"),
	}
	network.DHCPOnly = boolAt(w, "NoStaticIP")
	network.DHCPDNSOnly = boolAt(w, "RestrictDNS", "FromDHCP")
	network.WMMDisabled = boolAt(w, "WME", "Disabled")
	network.UAPSDDisabled = boolAt(w, "WME", "DisableApsd")
	network.Disable11ax = boolAt(w, "DisabledIEs", "DisableHeIes")
	network.Disable11nac = boolAt(w, "DisabledIEs", "DisableHTIes") && boolAt(w, "DisabledIEs", "DisableVHTIes")
	network.RateLimit = nil
	if limit := (model.RateLimit{WLANUp: intAt(w, "RateLimit", "WlanUp"), WLANDown: intAt(w, "RateLimit", "WlanDown"),
		ClientUp: intAt(w, "RateLimit", "ClientUp"), ClientDown: intAt(w, "RateLimit", "ClientDown")}); limit != (model.RateLimit{}) {
		network.RateLimit = &limit
	}
	network.QoSClass = ""
	if boolAt(w, "QOS", "Enabled") {
		for name, class := range map[string]int{"best_effort": 0, "background": 1, "video": 2, "voice": 3} {
			if intAt(w, "QOS", "AccessClass") == class {
				network.QoSClass = name
			}
		}
	}
	network.HonourBlocklist = boolAt(w, "BlackList", "Enabled")
	network.UcastDisassocOnBssDown = nil
	if boolAt(w, "UcastDisassocOnBssDown") {
		on := true
		network.UcastDisassocOnBssDown = &on
	}
	network.Option82 = nil
	if boolAt(w, "DhcpOption82") {
		network.Option82 = &model.Option82{Enabled: true, CircuitID: stringAt(w, "DhcpOption82Data")}
	}

	// BSSID index: measured from field 32 when any AP of the site
	// reported this network, else predicted (rule 4). Never 0.
	measured := map[int]bool{}
	for _, apID := range siteAPs {
		for _, vap := range vaps[apID] {
			if vap.WLANID != network.ID {
				continue
			}
			_, suffix, ok := strings.Cut(vap.Dev, ".")
			index, err := strconv.Atoi(suffix)
			if !ok || err != nil || index < 1 || index > 15 {
				return &RefusedError{Reason: fmt.Sprintf("network %s: VAP %s has no usable BSSID index", network.ID, vap.Dev)}
			}
			measured[index] = true
		}
	}
	predicted := map[int]bool{}
	if configs, ok := w["BandConfigs"].([]any); ok {
		for _, element := range configs {
			bc, _ := element.(map[string]any)
			if index := intAt(bc, "BSSIDIndex"); index > 0 {
				predicted[index] = true
			}
		}
	}
	switch {
	case len(measured) > 1:
		return &RefusedError{Reason: fmt.Sprintf("network %s: BSSID indexes differ between bands or APs", network.ID)}
	case len(measured) == 1:
		for index := range measured {
			network.BSSIDIndex = index
		}
		lift.BSSIDSource = "measured"
	case len(predicted) == 1:
		for index := range predicted {
			network.BSSIDIndex = index
		}
		lift.BSSIDSource = "predicted"
	case len(predicted) > 1:
		return &RefusedError{Reason: fmt.Sprintf("network %s: predicted BSSID indexes differ between bands", network.ID)}
	default:
		// A disabled network the normaliser did not index: the lowest
		// index no other network of the site predicts is assigned later.
		lift.BSSIDSource = "predicted"
	}
	lift.BSSIDIndex = network.BSSIDIndex
	if typed, err := json.Marshal(redactedNetwork(*network)); err == nil {
		lift.Typed = typed
	}
	return nil
}

func redactedNetwork(network model.Network) model.Network {
	network.PSK = ""
	network.Advanced, network.Expert = nil, nil
	network.RADIUS, network.MPSK = nil, nil
	return network
}

func pmfName(on, required bool) string {
	switch {
	case required:
		return "required"
	case on:
		return "optional"
	}
	return "disabled"
}

// liftRates maps RateSets to a profile; anything else stays "compatible"
// with the difference in the residual.
func liftRates(w map[string]any) string {
	sets, _ := w["RateSets"].([]any)
	numbers := func(set map[string]any, key string) string {
		list, _ := set[key].([]any)
		var parts []string
		for _, value := range list {
			parts = append(parts, fmt.Sprint(value))
		}
		return strings.Join(parts, ",")
	}
	profiles := map[string]map[string][2]string{
		"no_legacy":    {"2.4": {"6,12,24", "9,18,36,48,54"}, "5": {"6,12,24", "9,18,36,48,54"}},
		"high_density": {"2.4": {"12,24", "18,36,48,54"}, "5": {"12,24", "18,36,48,54"}},
	}
	for _, name := range []string{"no_legacy", "high_density"} {
		match := len(sets) > 0
		for _, element := range sets {
			set, _ := element.(map[string]any)
			want, ok := profiles[name][stringAt(set, "Band")]
			if !ok || numbers(set, "BasicRates") != want[0] || numbers(set, "LegacyRates") != want[1] {
				match = false
			}
		}
		if match {
			return name
		}
	}
	allZero := len(sets) > 0
	for _, element := range sets {
		set, _ := element.(map[string]any)
		for _, mask := range []string{"HTMCSRateMask", "VHTMCSRateMask", "HEMCSRateMask"} {
			if value := stringAt(set, mask); value != "" && value != "0000000000000000" {
				allZero = false
			}
		}
	}
	if allZero {
		return "legacy_only"
	}
	return "compatible"
}
