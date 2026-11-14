package api

import "time"

// ExpertScope is the object an expert patch belongs to.
type ExpertScope string

const (
	ExpertScopeSite ExpertScope = "site"
	ExpertScopeAP   ExpertScope = "ap"
	ExpertScopeWLAN ExpertScope = "wlan"
)

// ExpertPatchView is GET /api/{sites,aps,sites/$s/networks}/$id/expert
// (alias …/advanced). Patch is redacted; a "[redacted]" value sent back
// keeps the stored secret.
type ExpertPatchView struct {
	Scope    ExpertScope    `json:"scope"`
	ObjectID string         `json:"object_id"`
	Patch    map[string]any `json:"patch" tstype:"{ [key: string]: unknown }"`
	// Field is the stored field the endpoint edits: "expert" once schema 2
	// has committed, "advanced" in legacy mode (design §4.9).
	Field          string   `json:"field" tstype:"'expert' | 'advanced'"`
	Shadowed       []string `json:"shadowed"`
	PartlyShadowed []string `json:"partly_shadowed"`
	IgnoredKeys    []string `json:"ignored_keys"`
	UpdatedRev     uint64   `json:"updated_rev"`
}

// ExpertPatchInput is PUT …/expert. A null patch removes it. Confirm must
// be true to save a patch that shadows typed fields (the 422 lists them).
type ExpertPatchInput struct {
	Patch   map[string]any `json:"patch" tstype:"{ [key: string]: unknown } | null"`
	Confirm bool           `json:"confirm,omitempty"`
}

// TraceView is one typed option's compile trace (AP config view, phase 2).
type TraceView struct {
	Option string   `json:"option"`
	Step   string   `json:"step"`
	Paths  []string `json:"paths"`
}

// ShadowView is a typed option an expert patch overrides.
type ShadowView struct {
	Option   string   `json:"option"`
	Scope    string   `json:"scope"`
	By       string   `json:"by"`
	ObjectID string   `json:"object_id"`
	Paths    []string `json:"paths"`
	Partly   bool     `json:"partly"`
}

// CompileReport is the compile trace, shadowing, ignored keys, warnings and
// validation errors of one AP's config (GET /api/aps/$id/config, phase 2).
type CompileReport struct {
	Trace      []TraceView  `json:"trace"`
	Shadowed   []ShadowView `json:"shadowed"`
	Ignored    []string     `json:"ignored"`
	Warnings   []Warning    `json:"warnings"`
	Validation []Warning    `json:"validation"`
}

// AffectedAP is one AP a change affected and what happened to it.
type AffectedAP struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
}

// ChangeActor is who made a change (no secrets).
type ChangeActor struct {
	Kind   ActorKind `json:"kind"`
	Name   string    `json:"name"`
	ID     string    `json:"id"`
	Remote string    `json:"remote"`
}

// ChangeSummary is one row of GET /api/sites/$id/changes (phase 2).
type ChangeSummary struct {
	Revision    uint64       `json:"revision"`
	At          time.Time    `json:"at"`
	Actor       ChangeActor  `json:"actor"`
	Kind        string       `json:"kind" tstype:"'config' | 'action'"`
	Endpoint    string       `json:"endpoint"`
	Summary     string       `json:"summary"`
	AffectedAPs []AffectedAP `json:"affected_aps"`
}

// ChangePage is GET /api/sites/$id/changes.
type ChangePage struct {
	Changes    []ChangeSummary `json:"changes"`
	NextBefore *uint64         `json:"next_before" tstype:"number | null,required"`
}

// ChangeObject names one object a change touched.
type ChangeObject struct {
	Kind string `json:"kind" tstype:"'site' | 'network' | 'ap'"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ChangeDetail is GET /api/sites/$id/changes/$rev: the summary, the
// redacted typed snapshot at that revision, the diff of the typed objects
// against the previous revision, and each AP's effective hash.
type ChangeDetail struct {
	ChangeSummary `tstype:",extends"`
	Objects       []ChangeObject     `json:"objects"`
	Snapshot      map[string]any     `json:"snapshot" tstype:"{ [key: string]: unknown } | null"`
	Diff          []DiffEntry        `json:"diff"`
	Effective     map[string]string  `json:"effective"`
	Results       map[string]APState `json:"results"`
	Restorable    bool               `json:"restorable"`
}

// APState is one AP's result for a revision.
type APState struct {
	State string     `json:"state"`
	At    *time.Time `json:"at" tstype:"string | null,required"`
	Error string     `json:"error"`
}

// RestoreInput is the body of POST /api/sites/$id/changes/$rev/restore.
// Secrets maps a secret's path (as named in the secret_required error) to
// the old value; KeepCurrent lists paths whose current secret is kept.
type RestoreInput struct {
	Secrets        map[string]string `json:"secrets,omitempty"`
	KeepCurrent    []string          `json:"keep_current,omitempty"`
	KeepNewerWLANs bool              `json:"keep_newer_wlans,omitempty"`
}

// RestorePlan is what a restore does (the object of its mutation response).
type RestorePlan struct {
	Revision     uint64         `json:"revision"`
	Changed      []ChangeObject `json:"changed"`
	Recreated    []ChangeObject `json:"recreated"`
	Removed      []ChangeObject `json:"removed"`
	NotRestored  []ChangeObject `json:"not_restored"`
	SecretFields []string       `json:"secret_fields"`
}

// Recommendation is a named, previewable improvement (design §4.6). The
// change itself is previewed with POST …/recommendations/$id/apply?dry_run=1.
type Recommendation struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Detail     string     `json:"detail"`
	Scope      string     `json:"scope" tstype:"'site' | 'ap' | 'wlan'"`
	ObjectID   string     `json:"object_id"`
	Disruption Disruption `json:"disruption"`
}

// MigrationStatus is GET /api/system/migration (phase 2).
type MigrationStatus struct {
	MigrationView `tstype:",extends"`
	At            *time.Time `json:"at" tstype:"string | null,required"`
	Paths         []string   `json:"paths"`
	Schema        uint64     `json:"schema"`
	Compat        uint64     `json:"compat"`
	Copy          string     `json:"copy"`
	// Report is the v2 lift report (lifted values per object, residual
	// patches, per-AP verification and local-file status), redacted;
	// null before the migration has run.
	Report map[string]any `json:"report" tstype:"{ [key: string]: unknown } | null"`
	// CanFinalise is true once the migration has committed and run for 30
	// days (design §4.1 rule 5); FinaliseAfter is when that is.
	CanFinalise   bool       `json:"can_finalise"`
	FinaliseAfter *time.Time `json:"finalise_after" tstype:"string | null,required"`
	Finalised     bool       `json:"finalised"`
}

// MigrationRun is the response of POST /api/system/migration/retry and
// …/finalise.
type MigrationRun struct {
	DryRun bool            `json:"dry_run"`
	Status MigrationStatus `json:"status"`
}
