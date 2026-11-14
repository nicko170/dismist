package httpapi

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options"
)

// The interfaces in this file are what the API needs from the other
// workstreams (design §8.2). dismistd wires the real implementations; a nil
// member turns the endpoints that need it into 501 "unavailable" (or, for
// the optional hooks, into today's behaviour), so this package builds and
// its tests run against fakes until every producer has landed.

// Config is workstream A's typed-config layer as the API uses it.
type Config struct {
	// Registry is the option registry (options.Default()). nil means the
	// default registry.
	Registry *options.Registry
	// History is config_history (design §4.11).
	History ChangeLog
	// Restorer plans and applies a restore to an earlier revision.
	Restorer Restorer
	// Recommendations lists recommendations and applies one.
	Recommendations Recommender
	// Migration runs the schema-2 migration (state, retry, finalise).
	Migration MigrationRunner
	// Expert folds and checks expert patches (compile.FoldKeys and the
	// forbidden lists, design §4.4). nil uses the API's own case-insensitive
	// forbidden-path and duplicate-key check, which stores patches as
	// written.
	Expert ExpertPolicy
	// Planner is internal/rfplan: Initial (phase 2) and Plan (phase 3).
	Planner Planner
	// ClientMeta is the client_meta bucket (phase 4).
	ClientMeta ClientMetaStore
	// LastKnown returns an AP's persisted ap_status record (any JSON value
	// with the model.APStatusRecord keys), used while the AP is offline.
	LastKnown func(apID string) (any, bool)
}

// ChangeEntry is one config_history record (design §4.11 ChangeRecord).
// The ChangeLog fills Snapshot (typed records with keyed secret digests).
type ChangeEntry struct {
	Revision  uint64                    `json:"revision"`
	At        time.Time                 `json:"at"`
	Kind      string                    `json:"kind"` // "config" or "action"
	Actor     api.ChangeActor           `json:"actor"`
	Endpoint  string                    `json:"endpoint"`
	Summary   string                    `json:"summary"`
	SiteID    string                    `json:"site_id"`
	Objects   []api.ChangeObject        `json:"objects"`
	Snapshot  json.RawMessage           `json:"snapshot,omitempty"`
	Effective map[string]EffectiveEntry `json:"effective,omitempty"`
	Results   map[string]api.APState    `json:"results,omitempty"`
}

// EffectiveEntry is one AP's effective hash at a revision.
type EffectiveEntry struct {
	Hash     string `json:"hash"`
	Revision uint64 `json:"revision"`
}

// ChangeLog is the config_history store.
type ChangeLog interface {
	// Append stores entry in the mutation's transaction. state is the new
	// state, from which the log builds the redacted snapshot of entry's
	// site.
	Append(tx *model.Tx, state model.State, entry ChangeEntry) error
	// List returns a site's entries newest first, starting below before
	// (0 = newest), and the revision to pass for the next page (0 at the
	// end).
	List(siteID string, before uint64, limit int) ([]ChangeEntry, uint64, error)
	// Get returns one entry (model.ErrNotFound when absent).
	Get(revision uint64) (ChangeEntry, error)
	// AppendAction records an action (locate, reboot, backup download,
	// PSK reveal, token creation, migration) without a snapshot and
	// without a state revision of its own (design §4.11).
	AppendAction(entry ChangeEntry) error
	// SetResult records an AP's result for a revision (from
	// ep-config-applied, design §4.11 results).
	SetResult(revision uint64, apID string, result api.APState) error
}

// SecretsRequiredError is a restore that needs secrets the operator must
// enter or explicitly keep (422 secret_required, design §4.11). Fields maps
// each secret's path to a sentence.
type SecretsRequiredError struct {
	Fields map[string]string
}

func (e *SecretsRequiredError) Error() string {
	return "restoring this revision needs secrets that differ from today's"
}

// Restorer makes a site look like an earlier revision (design §4.11
// "Restore"). Restore changes state in the mutation's transaction and
// returns what it did; it returns *SecretsRequiredError when a secret has
// to be entered or kept.
type Restorer interface {
	Restore(state *model.State, tx *model.Tx, siteID string, entry ChangeEntry, input api.RestoreInput) (api.RestorePlan, error)
}

// Recommender is the recommendations engine (design §4.6).
type Recommender interface {
	List(state model.State, siteID string) []api.Recommendation
	// Apply changes state to apply recommendation id and returns a summary
	// for history.
	Apply(state *model.State, siteID, id string) (string, error)
}

// MigrationRunner runs the schema-2 migration (design §4.7, §4.1 rule 5).
type MigrationRunner interface {
	Status() api.MigrationStatus
	// Retry runs the migration from legacy mode. A dry run works on a copy
	// and writes nothing.
	Retry(dryRun bool) (api.MigrationStatus, error)
	// Finalise clears the legacy fields and sets compat 2 (phase 4).
	Finalise(dryRun bool) (api.MigrationStatus, error)
}

// ExpertError is a refused expert patch (422 forbidden_path or
// duplicate_key). Pointer is the JSON pointer of the offending key.
type ExpertError struct {
	Code    api.ErrorCode
	Pointer string
	Message string
}

func (e *ExpertError) Error() string { return e.Message }

// ExpertPolicy checks an expert patch and returns the bytes to store. In
// legacy mode (the legacy advanced field, compiled by the frozen v0
// compiler) it must not fold keys.
type ExpertPolicy interface {
	Check(scope api.ExpertScope, patch []byte, legacy bool) (stored []byte, ignored []string, err error)
}

// PlanInput is what the channel planner needs from the API: the site, its
// APs and what each AP reports now.
type PlanInput struct {
	State  model.State
	SiteID string
	// Radios are the current access radios per AP ID.
	Radios map[string][]api.RadioView
	// Scans are the latest neighbour scans per AP ID.
	Scans map[string]ScanReport
	// OwnBSSIDs maps each BSSID of the site's own APs to its AP ID.
	OwnBSSIDs map[string]string
	Now       time.Time
}

// ScanReport is one AP's latest neighbour scan.
type ScanReport struct {
	At         time.Time
	Neighbours []api.Neighbour
}

// Planner is internal/rfplan.
type Planner interface {
	// Initial writes an initial rf_plan for every AP of the sites whose
	// Auto band has no valid plan (design §3.10), in the mutation's
	// transaction.
	Initial(state *model.State, siteIDs []string, now time.Time) error
	// Propose plans channels for a site (phase 3).
	Propose(input PlanInput) (api.PlanProposal, error)
	// Apply writes the chosen changes into the APs' rf_plan.
	Apply(state *model.State, siteID string, changes []api.PlanChange, now time.Time) error
}

// ClientMetaStore is the client_meta bucket (phase 4).
type ClientMetaStore interface {
	Get(mac string) (api.ClientMeta, bool, error)
	Put(meta api.ClientMeta) error
	All() (map[string]api.ClientMeta, error)
}

// APCommand is one ap-cmd-rx command (design §5.6). The 0.15 ep-control
// decodes the body into struct { Cmd string; Params json.RawMessage;
// Clients []ethernet.MAC; Sender string; Reason string } (recovered from
// the binary's type information; not yet verified on an AP).
type APCommand struct {
	Cmd     string          `json:"Cmd"`
	Params  json.RawMessage `json:"Params"`
	Clients []string        `json:"Clients"`
	Sender  string          `json:"Sender"`
	Reason  string          `json:"Reason"`
}

// ErrCommandUnsupported means the AP has no verified command channel.
var ErrCommandUnsupported = errors.New("the AP has no verified command channel")

// Commands sends AP commands (workstream B's ap-cmd-rx, behind the per-AP
// protocol feature "ap_cmd"). It returns ErrCommandUnsupported when the AP
// has the feature off or is not connected.
type Commands interface {
	SendCommand(apID string, command APCommand) (string, error)
}

// Pusher delivers a committed config to a connected AP (workstream B's
// push lifecycle, design §5.6). It is called after the control session
// holds the new config; it returns the mode used ("notify" or
// "reconnect"). nil means reconnect (today's behaviour).
type Pusher interface {
	Push(apID string, revision uint64) string
}
