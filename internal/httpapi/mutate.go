package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/proxytrust"
)

// errDryRun aborts a dry-run transaction after the result was captured.
var errDryRun = errors.New("dry run")

// mutation is one config change through the common path (design §4.4,
// §4.8, §4.11): change detection, dry run, impact, history, push and the
// SSE config event.
type mutation struct {
	// sites are the affected sites (a move passes both).
	sites []string
	// change edits state inside the store transaction.
	change func(state *model.State, tx *model.Tx) error
	dryRun bool
	// kind is the SSE config event kind, for example "wlan.updated".
	kind     string
	objectID string
	// summary is the history line ("WLAN INTERNET: hidden on").
	summary string
	// historySite is the site the history entry belongs to ("" = none).
	historySite string
	objects     []api.ChangeObject
	// planInitial asks rfplan.Initial to assign Auto bands (design §3.10).
	planInitial bool
	request     *http.Request
	// extraPushes are configs the change itself built (an AP leaving every
	// site gets one last config), pushed after the commit.
	extraPushes *[]compile.Push
}

// outcome is what a mutation did (or, for a dry run, would do).
type outcome struct {
	revision uint64
	// state is the state after the change (hypothetical for a dry run).
	state    model.State
	applied  compile.Applied
	impact   api.Impact
	warnings []api.Warning
	dryRun   bool
}

// run executes a mutation (design §4.4 "Change detection"): inside one
// store transaction it edits state, compiles every AP of the affected
// sites, bumps only the APs whose config changed, appends the history
// entry and, for a dry run, rolls everything back. After a commit it pushes
// the changed APs and publishes the SSE config event.
func (s *Server) run(m mutation) (outcome, error) {
	var out outcome
	out.dryRun = m.dryRun
	update := func(state *model.State, tx *model.Tx) error {
		previous := copyState(*state)
		if err := m.change(state, tx); err != nil {
			return err
		}
		if m.planInitial && s.config.Planner != nil && s.typedEnabled() {
			if err := s.config.Planner.Initial(state, m.sites, state.UpdatedAt); err != nil {
				return err
			}
		}
		var desired compile.DesiredStore
		if tx != nil {
			desired = tx.APConfig()
		}
		applied, err := compile.Apply(state, desired, compile.ApplyOptions{
			Template: s.base, Sites: m.sites, Previous: &previous, Now: state.UpdatedAt, DryRun: m.dryRun,
		})
		if err != nil {
			return err
		}
		out.applied = applied
		out.revision = state.Revision + 1
		out.impact, out.warnings = s.impact(previous, *state, applied, m.dryRun)
		if compatWarning := s.compatRaise(*state); compatWarning {
			out.warnings = append(out.warnings, api.Warning{Code: "compat_raised",
				Message: "After this change, rolling dismist back to a release without schema 2 needs a database restore."})
			if !m.dryRun && tx != nil && model.SupportedSchema >= 2 {
				if err := tx.SetCompat(2); err != nil {
					return err
				}
			}
		}
		if m.dryRun {
			out.state = copyState(*state)
			return errDryRun
		}
		if s.config.History != nil && m.historySite != "" && tx != nil {
			entry := s.historyEntry(m, *state, applied, tx.HashKey(), out.revision)
			if err := s.config.History.Append(tx, *state, entry); err != nil {
				return fmt.Errorf("config history: %w", err)
			}
		}
		out.state = copyState(*state)
		return nil
	}
	var err error
	if store, ok := s.store.(model.TxStateStore); ok {
		err = store.UpdateTx(update)
	} else {
		err = s.store.Update(func(state *model.State) error { return update(state, nil) })
	}
	if errors.Is(err, errDryRun) {
		return out, nil
	}
	if err != nil {
		return outcome{}, err
	}
	// The store stamps the revision and every record's updated_rev when it
	// commits, after the change ran: the views (and the next If-Match)
	// need the committed state.
	out.state = s.store.Snapshot()
	pushes := out.applied.Pushes
	if m.extraPushes != nil {
		pushes = append(append([]compile.Push(nil), pushes...), *m.extraPushes...)
	}
	pushErr := s.push(pushes)
	s.publishConfig(m, out)
	return out, pushErr
}

// historyEntry builds the config_history record of a committed mutation.
func (s *Server) historyEntry(m mutation, state model.State, applied compile.Applied, key []byte, revision uint64) ChangeEntry {
	entry := ChangeEntry{
		Revision: revision, At: state.UpdatedAt, Kind: "config", Actor: actorOf(m.request),
		Endpoint: endpointOf(m.request), Summary: m.summary, SiteID: m.historySite, Objects: m.objects,
		Effective: map[string]EffectiveEntry{}, Results: map[string]api.APState{},
	}
	for _, push := range applied.Pushes {
		ap := findAP(state, push.APID)
		if ap == nil {
			continue
		}
		if hash, _, _, err := effective.HashConfig(key, push.Config, compile.EffectiveContext(*ap)); err == nil && len(key) > 0 {
			entry.Effective[push.APID] = EffectiveEntry{Hash: hash, Revision: push.Revision}
		}
		entry.Results[push.APID] = api.APState{State: "pending"}
	}
	return entry
}

// actorOf names who made a request (never a secret).
func actorOf(request *http.Request) api.ChangeActor {
	if request == nil {
		return api.ChangeActor{Kind: api.ActorKindSystem, Name: "dismist"}
	}
	p := principal(request)
	actor := api.ChangeActor{Kind: api.ActorKindSession, Name: "admin", Remote: clientAddress(request)}
	if p.Via == api.AuthViaToken {
		actor.Kind, actor.Name, actor.ID = api.ActorKindToken, p.TokenName, p.TokenID
	} else if p.SessionKey != "" {
		actor.ID = shortID(p.SessionKey)
	}
	return actor
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func endpointOf(request *http.Request) string {
	if request == nil {
		return ""
	}
	return request.Method + " " + request.URL.Path
}

// publishConfig sends the SSE config event of a committed mutation to the
// affected sites, and starts tracking the pushes.
func (s *Server) publishConfig(m mutation, out outcome) {
	if s.hub == nil {
		return
	}
	affected := make([]string, 0, len(out.applied.Pushes))
	for _, push := range out.applied.Pushes {
		affected = append(affected, push.APID)
		s.pushes.track(push.APID, push.Revision, s.now())
	}
	kind := m.kind
	if kind == "" {
		kind = "config.updated"
	}
	event := api.ConfigEvent{Revision: out.revision, Kind: kind, ObjectID: m.objectID, AffectedAPs: affected, Actor: api.Actor{Kind: actorOf(m.request).Kind, Name: actorOf(m.request).Name}}
	seen := map[string]bool{}
	for _, site := range m.sites {
		if site == "" || seen[site] {
			continue
		}
		seen[site] = true
		s.hub.publish(site, api.StreamEventConfig, event)
	}
}

// push hands committed configs to the APs' control sessions, then lets the
// push lifecycle deliver them (design §5.6). Without one it reconnects the
// AP, which fetches the new config when it comes back.
func (s *Server) push(pushes []compile.Push) error {
	var failed error
	for _, push := range pushes {
		control := s.controlFor(push.APID)
		if control == nil {
			continue
		}
		changed, err := control.SetConfigIfNewer(push.Config, push.Revision)
		if err != nil {
			log.Printf("AP %s: set config revision %d: %v", push.APID, push.Revision, err)
			failed = internalError("AP config update failed")
			continue
		}
		if !changed {
			continue
		}
		if s.pusher != nil {
			s.pusher.Push(push.APID, push.Revision)
			continue
		}
		control.Disconnect()
	}
	return failed
}

// compatRaise reports whether state holds something a schema-1 binary
// would compile wrongly (design §4.1 rule 4): a network whose security is
// not WPA2 or WPA2/WPA3 Personal, a 64-hex passphrase, or Multi-PSK keys.
// Only typed records can hold those, so it is false in legacy mode.
func (s *Server) compatRaise(state model.State) bool {
	if !s.typedEnabled() {
		return false
	}
	for _, network := range state.Networks {
		object, err := recordMap(network)
		if err != nil {
			continue
		}
		if security, ok := object["security"].(string); ok && security != "" &&
			security != string(api.SecurityWPA2Personal) && security != string(api.SecurityWPA2WPA3Personal) {
			return true
		}
		if len(network.PSK) == 64 && passphraseError(network.PSK, false) != "" {
			return true
		}
		if keys, ok := lookup(object, "mpsk.keys"); ok && !isZeroJSON(keys) {
			return true
		}
	}
	return false
}

// respondMutation writes a mutation's {revision, object, impact,
// warnings} response (design §4.8).
func respondMutation[T any](writer http.ResponseWriter, status int, out outcome, object T) {
	if out.dryRun {
		status = http.StatusOK
	}
	warnings := out.warnings
	if warnings == nil {
		warnings = []api.Warning{}
	}
	respond(writer, status, api.MutationResponse[T]{
		Revision: out.revision, Object: object, Impact: out.impact, Warnings: warnings, DryRun: out.dryRun,
	})
}

// dryRun reports whether the request asks for a dry run (?dry_run=1).
func dryRun(request *http.Request) bool {
	switch strings.ToLower(request.URL.Query().Get("dry_run")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// ifMatch checks an If-Match header against an object's updated_rev. A
// missing header passes unless required.
func ifMatch(request *http.Request, updatedRev uint64, required bool) *apiError {
	header := strings.TrimSpace(request.Header.Get("If-Match"))
	if header == "" {
		if required {
			return newError(http.StatusPreconditionRequired, api.ErrorCodeInvalid,
				"If-Match is required: send the object's updated_rev as If-Match: \"<updated_rev>\"").withField("If-Match", "required")
		}
		return nil
	}
	if header == "*" {
		return nil
	}
	header = strings.TrimPrefix(header, "W/")
	header = strings.Trim(header, `"`)
	if header == fmt.Sprint(updatedRev) {
		return nil
	}
	return newError(http.StatusConflict, api.ErrorCodeStale,
		"this object changed since you loaded it; reload it or overwrite").withField("If-Match", fmt.Sprintf("current updated_rev is %d", updatedRev))
}

// staleWith writes a 409 stale with the current object, so the UI can
// offer Reload or Overwrite (design §4.8).
func staleWith(writer http.ResponseWriter, e *apiError, current any) {
	body := struct {
		api.ErrorBody
		Current any `json:"current"`
	}{api.ErrorBody{Error: e.message, Code: e.code, Fields: e.fields}, current}
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	respond(writer, e.status, body)
}

func findAP(state model.State, id string) *model.APRecord {
	for index := range state.APs {
		if state.APs[index].ID == id {
			return &state.APs[index]
		}
	}
	return nil
}

func findSite(state model.State, id string) *model.Site {
	for index := range state.Sites {
		if state.Sites[index].ID == id {
			return &state.Sites[index]
		}
	}
	return nil
}

func findNetwork(state model.State, siteID, id string) *model.Network {
	legacy := len(state.Sites) == 0
	for index := range state.Networks {
		network := &state.Networks[index]
		if network.ID == id && (network.SiteID == siteID || (legacy && network.SiteID == "")) {
			return network
		}
	}
	return nil
}

// siteAPs returns the APs of a site, sorted by ID.
func siteAPs(state model.State, siteID string) []model.APRecord {
	var result []model.APRecord
	for _, ap := range state.APs {
		if ap.SiteID == siteID {
			result = append(result, ap)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// clientAddress is the request's client address (trusted proxies honoured).
func clientAddress(request *http.Request) string {
	if addr := proxytrust.ClientAddr(request); addr.IsValid() {
		return addr.String()
	}
	return ""
}
