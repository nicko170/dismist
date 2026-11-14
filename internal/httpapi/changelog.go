package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

func (s *Server) historyUnavailable(writer http.ResponseWriter) bool {
	if s.config.History != nil {
		return false
	}
	writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "config history is not available in this build"))
	return true
}

func changeSummary(entry ChangeEntry, state model.State) api.ChangeSummary {
	summary := api.ChangeSummary{Revision: entry.Revision, At: entry.At, Actor: entry.Actor, Kind: entry.Kind,
		Endpoint: entry.Endpoint, Summary: entry.Summary, AffectedAPs: []api.AffectedAP{}}
	if summary.Kind == "" {
		summary.Kind = "config"
	}
	names := apNames(state)
	for apID, result := range entry.Results {
		summary.AffectedAPs = append(summary.AffectedAPs, api.AffectedAP{ID: apID, Name: names[apID], State: result.State, Error: result.Error})
	}
	for apID := range entry.Effective {
		if _, ok := entry.Results[apID]; !ok {
			summary.AffectedAPs = append(summary.AffectedAPs, api.AffectedAP{ID: apID, Name: names[apID], State: "pending"})
		}
	}
	return summary
}

// listChanges serves GET /api/sites/$id/changes?before=&limit=.
func (s *Server) listChanges(writer http.ResponseWriter, request *http.Request, p params) {
	if s.historyUnavailable(writer) {
		return
	}
	state := s.store.Snapshot()
	if findSite(state, p["site"]) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	var before uint64
	if raw := request.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeAPIError(writer, invalidField("before", "before is a revision number"))
			return
		}
		before = parsed
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	entries, next, err := s.config.History.List(p["site"], before, limit)
	if err != nil {
		log.Printf("config history: %v", err)
		writeAPIError(writer, internalError("config history read failed"))
		return
	}
	page := api.ChangePage{Changes: []api.ChangeSummary{}}
	for _, entry := range entries {
		page.Changes = append(page.Changes, changeSummary(entry, state))
	}
	if next != 0 {
		page.NextBefore = &next
	}
	respond(writer, http.StatusOK, page)
}

func (s *Server) changeEntry(writer http.ResponseWriter, p params) (ChangeEntry, bool) {
	revision, err := strconv.ParseUint(p["rev"], 10, 64)
	if err != nil {
		writeAPIError(writer, invalidField("rev", "rev is a revision number"))
		return ChangeEntry{}, false
	}
	entry, err := s.config.History.Get(revision)
	if errors.Is(err, model.ErrNotFound) || (err == nil && entry.SiteID != p["site"]) {
		writeAPIError(writer, notFound("revision"))
		return ChangeEntry{}, false
	}
	if err != nil {
		log.Printf("config history %d: %v", revision, err)
		writeAPIError(writer, internalError("config history read failed"))
		return ChangeEntry{}, false
	}
	return entry, true
}

// getChange serves GET /api/sites/$id/changes/$rev: the summary, the
// redacted snapshot and its diff against the previous config revision.
func (s *Server) getChange(writer http.ResponseWriter, _ *http.Request, p params) {
	if s.historyUnavailable(writer) {
		return
	}
	entry, ok := s.changeEntry(writer, p)
	if !ok {
		return
	}
	state := s.store.Snapshot()
	detail := api.ChangeDetail{ChangeSummary: changeSummary(entry, state), Objects: entry.Objects, Diff: []api.DiffEntry{},
		Effective: map[string]string{}, Results: map[string]api.APState{}, Restorable: entry.Kind != "action" && len(entry.Snapshot) > 0}
	if detail.Objects == nil {
		detail.Objects = []api.ChangeObject{}
	}
	for apID, effectiveEntry := range entry.Effective {
		detail.Effective[apID] = effectiveEntry.Hash
	}
	for apID, result := range entry.Results {
		detail.Results[apID] = result
	}
	if len(entry.Snapshot) > 0 {
		if snapshot, err := redactJSON(entry.Snapshot); err == nil {
			detail.Snapshot, _ = snapshot.(map[string]any)
		}
		if previous, err := s.previousSnapshot(entry); err == nil && previous != nil {
			detail.Diff = diffSnapshots(previous, entry.Snapshot)
		}
	}
	respond(writer, http.StatusOK, detail)
}

// previousSnapshot returns the snapshot of the site's config revision
// before entry.
func (s *Server) previousSnapshot(entry ChangeEntry) (json.RawMessage, error) {
	before := entry.Revision
	for attempts := 0; attempts < 10; attempts++ {
		entries, next, err := s.config.History.List(entry.SiteID, before, 20)
		if err != nil {
			return nil, err
		}
		for _, candidate := range entries {
			if candidate.Revision < entry.Revision && candidate.Kind != "action" && len(candidate.Snapshot) > 0 {
				return candidate.Snapshot, nil
			}
		}
		if next == 0 {
			return nil, nil
		}
		before = next
	}
	return nil, nil
}

// diffSnapshots diffs two typed snapshots (redacted; digests show as
// "[changed]").
func diffSnapshots(before, after json.RawMessage) []api.DiffEntry {
	old, _ := decodeAny(before)
	fresh, _ := decodeAny(after)
	var result []api.DiffEntry
	diffValue("", "", old, fresh, false, &result)
	if result == nil {
		return []api.DiffEntry{}
	}
	return result
}

// restoreChange serves POST /api/sites/$id/changes/$rev/restore[?dry_run=1]
// through the normal mutation path (design §4.11 "Restore").
func (s *Server) restoreChange(writer http.ResponseWriter, request *http.Request, p params) {
	if s.historyUnavailable(writer) {
		return
	}
	if s.config.Restorer == nil {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "restore is not available in this build"))
		return
	}
	if !s.typedEnabled() {
		writeAPIError(writer, migrationPending(nil))
		return
	}
	var input api.RestoreInput
	if request.ContentLength > 0 {
		if err := decodeJSON(request, 64*1024, &input, "restore"); err != nil {
			writeError(writer, err)
			return
		}
	}
	entry, ok := s.changeEntry(writer, p)
	if !ok {
		return
	}
	if entry.Kind == "action" || len(entry.Snapshot) == 0 {
		writeAPIError(writer, conflict("this revision is an action, not a config change; there is nothing to restore"))
		return
	}
	var plan api.RestorePlan
	out, err := s.run(mutation{
		sites: []string{entry.SiteID}, dryRun: dryRun(request), request: request, planInitial: true,
		kind: "site.restored", objectID: entry.SiteID, historySite: entry.SiteID,
		summary: "Restored revision " + strconv.FormatUint(entry.Revision, 10),
		change: func(state *model.State, tx *model.Tx) error {
			result, err := s.config.Restorer.Restore(state, tx, entry.SiteID, entry, input)
			plan = result
			return err
		},
	})
	var secrets *SecretsRequiredError
	if errors.As(err, &secrets) {
		e := newError(http.StatusUnprocessableEntity, api.ErrorCodeSecretRequired, secrets.Error())
		for path, message := range secrets.Fields {
			e.withField(path, message)
		}
		writeAPIError(writer, e)
		return
	}
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	plan.Revision = entry.Revision
	normalisePlan(&plan)
	respondMutation(writer, http.StatusOK, out, plan)
}

func normalisePlan(plan *api.RestorePlan) {
	for _, list := range []*[]api.ChangeObject{&plan.Changed, &plan.Recreated, &plan.Removed, &plan.NotRestored} {
		if *list == nil {
			*list = []api.ChangeObject{}
		}
	}
	if plan.SecretFields == nil {
		plan.SecretFields = []string{}
	}
}

// RecordResult stores an AP's ep-config-applied outcome for a revision in
// config history (design §4.11 results) and tells the site stream. B's
// apply reporting calls it.
func (s *Server) RecordResult(apID string, revision uint64, result api.APState) {
	if s.config.History == nil {
		return
	}
	if err := s.config.History.SetResult(revision, apID, result); err != nil && !errors.Is(err, model.ErrNotFound) {
		log.Printf("config history: result of AP %s at %d: %v", apID, revision, err)
	}
}
