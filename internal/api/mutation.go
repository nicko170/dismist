package api

// Disruption is an option's disruption class (design P6). The impact of a
// change is the union over the options it touches.
type Disruption string

const (
	DisruptionNone            Disruption = "none"
	DisruptionWLANRestart     Disruption = "wlan_restart"
	DisruptionRadioBounce     Disruption = "radio_bounce"
	DisruptionAllRadiosBounce Disruption = "all_radios_bounce"
	DisruptionStrandingRisk   Disruption = "stranding_risk"
)

// DiffEntry is one changed path of a compiled config (dry runs only).
// Secrets appear as "[changed]".
type DiffEntry struct {
	Path string `json:"path"`
	From any    `json:"from" tstype:"unknown"`
	To   any    `json:"to" tstype:"unknown"`
}

// ImpactAP is one AP whose compiled config a mutation changes.
type ImpactAP struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Connected  bool         `json:"connected"`
	Push       PushMode     `json:"push"`
	Disruption []Disruption `json:"disruption"`
	// Diff is present only on dry runs.
	Diff []DiffEntry `json:"diff,omitempty"`
}

// Impact lists the APs a mutation changes. An empty APs list means nothing
// is pushed ("No AP changes").
type Impact struct {
	APs        []ImpactAP `json:"aps"`
	Disruption Disruption `json:"disruption"`
	Summary    string     `json:"summary"`
}

// Warning is a non-blocking finding of a mutation or compile.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// MutationResponse is the response of every config mutation (design §4.8;
// phase 2 for the typed endpoints).
type MutationResponse[T any] struct {
	Revision uint64    `json:"revision"`
	Object   T         `json:"object"`
	Impact   Impact    `json:"impact"`
	Warnings []Warning `json:"warnings"`
	DryRun   bool      `json:"dry_run,omitempty"`
}
