package compile

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nicko170/dismist/internal/compile/effective"
)

// The schema-2 pipeline (design §4.4), Mist's precedence: template → site
// typed → site expert patch → AP typed → AP expert patch, with WLANs
// compiled on their own (template WLAN → network typed → network expert
// patch → WLAN protected fields) and inserted after, then the envelope and
// protected fields, then firmware validation.

// Compile steps, as reported in the trace.
const (
	StepTemplate   = "template"
	StepSite       = "site"
	StepSiteExpert = "site_expert"
	StepAP         = "ap"
	StepAPExpert   = "ap_expert"
	StepWLAN       = "wlan"
	StepWLANExpert = "wlan_expert"
	StepProtected  = "protected"
)

// ValidationFailed is returned by AP when the compiled config breaks a
// firmware rule: the AP would reject it, so it is never served or pushed
// (API 422 firmware_rule). Output carries the config and the details.
type ValidationFailed struct {
	Errors []ValidationError
}

func (e *ValidationFailed) Error() string {
	var parts []string
	for _, problem := range e.Errors {
		parts = append(parts, problem.Message)
	}
	return "config breaks firmware rules: " + strings.Join(parts, "; ")
}

// Code is the API error code.
func (e *ValidationFailed) Code() string { return "firmware_rule" }

type canonical struct {
	root map[string]any
	wlan map[string]any
}

var templateCache sync.Map // sha256 → *canonical

// canonicalTemplate folds the runtime template (step 0): keys in canonical
// case, keys 0.15 ignores dropped (AMPDU.RST, ForFTM, …), WLANs[0] split
// off as the WLAN template. The result is cached; callers get copies.
func canonicalTemplate(template []byte) (map[string]any, map[string]any, error) {
	sum := sha256.Sum256(template)
	if cached, ok := templateCache.Load(sum); ok {
		c := cached.(*canonical)
		return deepCopy(c.root).(map[string]any), deepCopy(c.wlan).(map[string]any), nil
	}
	folded, _, err := effective.Fold(template)
	if err != nil {
		return nil, nil, fmt.Errorf("template: %w", err)
	}
	root, err := decodeObject(folded)
	if err != nil {
		return nil, nil, err
	}
	wlans, _ := root["WLANs"].([]any)
	if len(wlans) == 0 {
		return nil, nil, errors.New("AP config needs a WLAN template")
	}
	wlan, ok := wlans[0].(map[string]any)
	if !ok {
		return nil, nil, errors.New("AP config needs a WLAN template")
	}
	delete(root, "WLANs")
	templateCache.Store(sum, &canonical{root: root, wlan: wlan})
	return deepCopy(root).(map[string]any), deepCopy(wlan).(map[string]any), nil
}

// rootPatch folds, checks and decodes a stored site or AP expert patch.
func rootPatch(scope string, raw []byte) (map[string]any, []string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	folded, err := FoldPatch(scope, raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s expert patch: %w", scope, err)
	}
	patch, err := decodeObject(folded.Patch)
	return patch, folded.Ignored, err
}

// compileTyped is compile.AP for a schema-2 state.
func compileTyped(in Input) (Output, error) {
	var output Output
	root, wlanTemplate, err := canonicalTemplate(in.Template)
	if err != nil {
		return output, err
	}
	rec := &recorder{}
	in.Site, in.Networks = withDefaults(in.Site, in.Networks)

	// Step W: every network's WLAN.
	wlans := make([]map[string]any, 0, len(in.Networks))
	var shadows []Shadow
	for _, network := range in.Networks {
		wlan, wlanShadows, err := compileWLAN(wlanInput{template: wlanTemplate, site: in.Site, ap: in.AP,
			network: network, now: in.Now, rec: rec, warnings: &output.Warnings})
		if err != nil {
			return output, err
		}
		wlans = append(wlans, wlan)
		shadows = append(shadows, wlanShadows...)
		if len(network.Expert) != 0 {
			if folded, err := FoldPatch(ScopeWLAN, network.Expert); err == nil {
				for _, ignored := range folded.Ignored {
					output.Ignored = append(output.Ignored, strings.Replace(ignored, "WLANs[]", "WLANs[ID="+network.ID+"]", 1))
				}
			}
		}
	}

	// Step 1: site typed fields and derived ports.
	siteRec := rec.scoped(ScopeSite, "", StepSite)
	if err := siteTyped(root, in.Site, wlans, in.Networks, siteRec, &output.Warnings); err != nil {
		return output, err
	}
	// Step 2: site expert patch.
	sitePatch, ignored, err := rootPatch(ScopeSite, in.Site.Expert)
	if err != nil {
		return output, err
	}
	output.Ignored = append(output.Ignored, ignored...)
	if sitePatch != nil {
		before := siteRec.snapshot(root)
		merged, err := KeyedMerge(root, sitePatch, "")
		if err != nil {
			return output, fmt.Errorf("site expert patch: %w", err)
		}
		root = merged.(map[string]any)
		shadows = append(shadows, siteRec.shadows(before, root, ScopeSite, in.Site.ID)...)
	}
	// Step 3: AP typed fields.
	apRec := rec.scoped(ScopeAP, "", StepAP)
	if err := apTyped(root, in.Site, in.AP, in.Now, apRec, &output.Warnings); err != nil {
		return output, err
	}
	// Step 4: AP expert patch.
	apPatch, ignored, err := rootPatch(ScopeAP, in.AP.Expert)
	if err != nil {
		return output, err
	}
	output.Ignored = append(output.Ignored, ignored...)
	if apPatch != nil {
		before := apRec.snapshot(root)
		merged, err := KeyedMerge(root, apPatch, "")
		if err != nil {
			return output, fmt.Errorf("AP expert patch: %w", err)
		}
		root = merged.(map[string]any)
		shadows = append(shadows, apRec.shadows(before, root, ScopeAP, in.AP.ID)...)
	}
	// Step 5: WLANs.
	list := make([]any, len(wlans))
	for index, wlan := range wlans {
		list[index] = wlan
	}
	root["WLANs"] = list
	// Step 6: envelope and protected fields.
	root["ID"] = in.AP.ID
	if compact := strings.ReplaceAll(in.AP.ID, "-", ""); len(compact) == 12 {
		root["UUID"] = "00000000-0000-0000-1000-" + compact
	}
	siteID := in.AP.SiteID
	if siteID == "" {
		siteID = in.Site.ID
	}
	root["SiteID"] = siteID
	root["OrgID"] = ""
	root["Timestamp"] = jsonNumber(formatTimestamp(ConfigTimestamp(in.Time)))
	stats := child(root, "Stats")
	stats["Interval"], stats["FullInterval"], stats["MemInterval"] = num(2), num(60), num(600)
	delete(root, "NotModified")
	delete(root, "FailedRetryAfter")

	config, err := encodeTree(root)
	if err != nil {
		return output, err
	}
	output.Config = config
	output.Shadowed = shadows
	output.Trace = rec.trace(shadows)
	output.Ignored = sortedUniqueStrings(output.Ignored)
	for _, shadow := range shadows {
		output.Warnings = append(output.Warnings, Warning{Code: "shadowed", Option: shadow.Option,
			Path: strings.Join(shadow.Paths, ", "), Message: fmt.Sprintf("the %s expert patch overrides %s", shadow.By, shadow.Option)})
	}
	// Step 7: firmware validation.
	output.Validation, output.Warnings = validateConfig(root, in, output.Warnings)
	if len(output.Validation) > 0 {
		return output, &ValidationFailed{Errors: output.Validation}
	}
	return output, nil
}

// formatTimestamp renders the Timestamp exactly as v0 marshals a float64
// (encoding/json's float format).
func formatTimestamp(value float64) string {
	raw, err := encodeTree(value)
	if err != nil {
		return "0"
	}
	return string(raw)
}

// IsTyped reports whether an input compiles with the schema-2 pipeline.
func (in Input) IsTyped() bool { return in.Schema >= 2 }

// legacyOrTyped dispatches compile.AP.
func legacyOrTyped(in Input) (Output, error) {
	if in.IsTyped() {
		return compileTyped(in)
	}
	config, err := BuildSiteConfig(in.Template, in.Site, in.AP, in.Networks, in.Time)
	if err != nil {
		return Output{}, err
	}
	return Output{Config: config}, nil
}
