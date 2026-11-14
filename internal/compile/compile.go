// Package compile turns stored records into the config JSON an AP runs
// (design §4.4). In phase 0 compile.AP is exactly the ap-identity compiler
// (BuildSiteConfig, moved here unchanged); the typed steps, keyed merge,
// shadowing and validation arrive in phase 2 behind the same signatures.
//
// compile.Site adds change detection: which APs' compiled config changed
// (ignoring Timestamp), so a mutation bumps and pushes only those APs, and
// one AP that fails to compile never stops another.
package compile

import (
	"errors"
	"fmt"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

// Input is everything compile.AP reads for one AP.
type Input struct {
	// Template is the runtime template: the -ap-config file the daemon
	// loaded, or the bundled AP33 template.
	Template []byte
	Site     model.Site
	AP       model.APRecord
	// Networks are the site's networks in compile order (NetworksForSite).
	Networks []model.Network
	// Time becomes the config's Timestamp (the AP's ConfigUpdatedAt).
	Time time.Time
}

// Output is the result of compiling one AP. Only Config is filled in phase
// 0; the other fields are part of the frozen contract (design §8.2) and are
// populated from phase 2.
type Output struct {
	Config     []byte            `json:"-"`
	Trace      []TraceEntry      `json:"trace,omitempty"`
	Shadowed   []Shadow          `json:"shadowed,omitempty"`
	Ignored    []string          `json:"ignored,omitempty"`
	Warnings   []Warning         `json:"warnings,omitempty"`
	Validation []ValidationError `json:"validation,omitempty"`
}

// TraceEntry records which compile step last wrote a typed option's paths.
type TraceEntry struct {
	Option string   `json:"option"`
	Step   string   `json:"step"` // "template", "site", "site_expert", "ap", "ap_expert", "wlan", "wlan_expert", "protected"
	Paths  []string `json:"paths"`
}

// Shadow is a typed option whose firmware paths an expert patch changed.
type Shadow struct {
	Option   string   `json:"option"`
	Scope    string   `json:"scope"` // scope of the option: "site", "ap", "wlan"
	By       string   `json:"by"`    // scope of the patch: "site", "ap", "wlan"
	ObjectID string   `json:"object_id,omitempty"`
	Paths    []string `json:"paths"`
	Partly   bool     `json:"partly,omitempty"`
}

// Warning is a non-blocking compile finding.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Option  string `json:"option,omitempty"`
	Path    string `json:"path,omitempty"`
}

// ValidationError is a firmware rule the compiled config breaks. A mutation
// whose result has any is refused (422).
type ValidationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Option  string `json:"option,omitempty"`
	Path    string `json:"path,omitempty"`
}

// AP compiles one AP. A compile error affects only this AP.
func AP(in Input) (Output, error) {
	config, err := BuildSiteConfig(in.Template, in.Site, in.AP, in.Networks, in.Time)
	if err != nil {
		return Output{}, err
	}
	return Output{Config: config}, nil
}

// ErrNoSite means the AP is not assigned to a site, so nothing is compiled
// for it (as in v0).
var ErrNoSite = errors.New("AP has no site")

// NetworksForSite returns the networks an AP of siteID carries, in stored
// order: the site's networks, plus networks with an empty SiteID when the
// state has no sites at all (the legacy rule v0 used).
func NetworksForSite(state model.State, siteID string) []model.Network {
	legacy := len(state.Sites) == 0
	networks := make([]model.Network, 0)
	for _, network := range state.Networks {
		if network.SiteID == siteID || (legacy && network.SiteID == "") {
			networks = append(networks, network)
		}
	}
	return networks
}

// ConfigTime is the Timestamp source for an AP: ConfigUpdatedAt, or the
// state's UpdatedAt for a record that predates it.
func ConfigTime(state model.State, ap model.APRecord) time.Time {
	if ap.ConfigUpdatedAt.IsZero() {
		return state.UpdatedAt
	}
	return ap.ConfigUpdatedAt
}

// ConfigRevision is the revision served with an AP's config.
func ConfigRevision(state model.State, ap model.APRecord) uint64 {
	if ap.ConfigRevision == 0 {
		return state.Revision
	}
	return ap.ConfigRevision
}

// ConfigTimestamp is the Timestamp value written for a config time.
func ConfigTimestamp(at time.Time) float64 { return float64(at.UnixNano()) / 1e9 }

// InputFor builds the compile input of one AP from state, with the AP's own
// config time. It returns ErrNoSite for an AP without a site and an error
// when the AP's site does not exist.
func InputFor(template []byte, state model.State, apID string) (Input, error) {
	var ap *model.APRecord
	for index := range state.APs {
		if state.APs[index].ID == apID {
			ap = &state.APs[index]
			break
		}
	}
	if ap == nil {
		return Input{}, fmt.Errorf("AP %s not found", apID)
	}
	if ap.SiteID == "" {
		return Input{}, ErrNoSite
	}
	var site *model.Site
	for index := range state.Sites {
		if state.Sites[index].ID == ap.SiteID {
			site = &state.Sites[index]
			break
		}
	}
	if site == nil {
		return Input{}, fmt.Errorf("AP %s has no site", ap.ID)
	}
	return Input{
		Template: template, Site: *site, AP: *ap,
		Networks: NetworksForSite(state, ap.SiteID), Time: ConfigTime(state, *ap),
	}, nil
}
