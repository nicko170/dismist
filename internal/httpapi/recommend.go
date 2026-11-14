package httpapi

import (
	"net/http"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// listRecommendations serves GET /api/sites/$id/recommendations.
func (s *Server) listRecommendations(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	if findSite(state, p["site"]) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	result := []api.Recommendation{}
	if s.config.Recommendations != nil && s.typedEnabled() {
		result = append(result, s.config.Recommendations.List(state, p["site"])...)
	}
	respond(writer, http.StatusOK, result)
}

// applyRecommendation serves POST /api/sites/$id/recommendations/$rid/apply
// [?dry_run=1]: the recommendation's change through the normal mutation
// path, previewed with a dry run.
func (s *Server) applyRecommendation(writer http.ResponseWriter, request *http.Request, p params) {
	if s.config.Recommendations == nil {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "recommendations are not available in this build"))
		return
	}
	if !s.typedEnabled() {
		writeAPIError(writer, migrationPending(nil))
		return
	}
	siteID, id := p["site"], p["rid"]
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	var recommendation *api.Recommendation
	for _, candidate := range s.config.Recommendations.List(state, siteID) {
		if candidate.ID == id {
			copied := candidate
			recommendation = &copied
		}
	}
	if recommendation == nil {
		writeAPIError(writer, notFound("recommendation"))
		return
	}
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "site.updated", objectID: siteID,
		historySite: siteID, summary: "Applied recommendation: " + recommendation.Title,
		change: func(state *model.State, _ *model.Tx) error {
			_, err := s.config.Recommendations.Apply(state, siteID, id)
			return err
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respondMutation(writer, http.StatusOK, out, *recommendation)
}
