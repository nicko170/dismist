package httpapi

import (
	"net/http"

	"github.com/nicko170/dismist/internal/api"
)

// registerRoutes declares every API endpoint (design §4.9).
func (s *Server) registerRoutes() {
	r := &s.routes
	get, put, post, patch, del := http.MethodGet, http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete

	// System, tokens, sessions, migration, onboarding.
	r.handle(get, "/api/system", s.systemHandler)
	r.handle(get, "/api/system/backup", s.downloadBackup)
	r.handle(get, "/api/system/tokens", s.listTokens)
	r.handle(post, "/api/system/tokens", s.createToken)
	r.handle(del, "/api/system/tokens/{id}", s.revokeToken)
	r.handle(get, "/api/system/sessions", s.listSessions)
	r.handle(del, "/api/system/sessions/{id}", s.revokeSession)
	r.handle(post, "/api/system/sessions/revoke-all", s.revokeAllSessions)
	r.handle(get, "/api/system/migration", s.getMigration)
	r.handle(post, "/api/system/migration/retry", s.retryMigration)
	r.handle(post, "/api/system/migration/finalise", s.finaliseMigration)
	r.handle(get, "/api/onboarding", s.onboardingView)
	r.handle(get, "/api/onboarding/ca-bundle.pem", s.caBundle)
	r.handle(get, "/api/search", s.search)

	// Sites.
	r.handle(get, "/api/sites", s.listSites)
	r.handle(post, "/api/sites", s.createSite)
	r.handle(get, "/api/sites/{site}", s.getSite)
	r.handle(patch, "/api/sites/{site}", s.patchSite)
	r.handle(put, "/api/sites/{site}", s.putSite)
	r.handle(del, "/api/sites/{site}", s.deleteSite)
	r.handle(get, "/api/sites/{site}/ssh-keys", s.sshKeysGet)
	r.handle(put, "/api/sites/{site}/ssh-keys", s.sshKeysPut)
	siteGet, sitePut := expertHandlers(api.ExpertScopeSite)
	for _, name := range []string{"expert", "advanced"} {
		r.handle(get, "/api/sites/{site}/"+name, siteGet(s))
		r.handle(put, "/api/sites/{site}/"+name, sitePut(s))
	}
	r.handle(get, "/api/sites/{site}/rf", s.getRF)
	r.handle(put, "/api/sites/{site}/rf", s.putRF)
	r.handle(post, "/api/sites/{site}/rf/plan", s.rfPlan)
	r.handle(get, "/api/sites/{site}/rf/neighbours", s.neighbours)
	r.handle(get, "/api/sites/{site}/recommendations", s.listRecommendations)
	r.handle(post, "/api/sites/{site}/recommendations/{rid}/apply", s.applyRecommendation)
	r.handle(get, "/api/sites/{site}/clients", s.siteClients)
	r.handle(post, "/api/sites/{site}/clients/{mac}/disconnect", s.disconnectClient)
	r.handle(post, "/api/sites/{site}/clients/{mac}/block", s.blockClient(true))
	r.handle(post, "/api/sites/{site}/clients/{mac}/unblock", s.blockClient(false))
	r.handle(get, "/api/sites/{site}/events", s.listEvents)
	r.handle(get, "/api/sites/{site}/events/{event}", s.getEvent)
	r.handle(get, "/api/sites/{site}/history/{kind}", s.siteHistory)
	r.handle(get, "/api/sites/{site}/changes", s.listChanges)
	r.handle(get, "/api/sites/{site}/changes/{rev}", s.getChange)
	r.handle(post, "/api/sites/{site}/changes/{rev}/restore", s.restoreChange)
	r.handle(get, "/api/sites/{site}/stream", s.streamSite)

	// WLANs.
	r.handle(get, "/api/sites/{site}/networks", s.listNetworks)
	r.handle(post, "/api/sites/{site}/networks", s.createNetwork)
	r.handle(get, "/api/sites/{site}/networks/{nid}", s.getNetwork)
	r.handle(patch, "/api/sites/{site}/networks/{nid}", s.patchNetwork)
	r.handle(put, "/api/sites/{site}/networks/{nid}", s.patchNetwork)
	r.handle(del, "/api/sites/{site}/networks/{nid}", s.deleteNetwork)
	r.handle(post, "/api/sites/{site}/networks/{nid}/reveal", s.reveal)
	wlanGet, wlanPut := expertHandlers(api.ExpertScopeWLAN)
	for _, name := range []string{"expert", "advanced"} {
		r.handle(get, "/api/sites/{site}/networks/{nid}/"+name, wlanGet(s))
		r.handle(put, "/api/sites/{site}/networks/{nid}/"+name, wlanPut(s))
	}

	// Site-nested AP aliases (deprecated, design §4.9).
	r.handle(get, "/api/sites/{site}/aps/{ap}/{kind}", s.siteAPRoute)
	r.handle(get, "/api/sites/{site}/aps/{ap}/history/events", func(w http.ResponseWriter, req *http.Request, p params) {
		p["kind"] = "events"
		s.apHistory(w, req, p)
	})
	apGet, apPut := expertHandlers(api.ExpertScopeAP)
	r.handle(get, "/api/sites/{site}/aps/{ap}/advanced", apGet(s))
	r.handle(put, "/api/sites/{site}/aps/{ap}/advanced", apPut(s))

	// APs.
	r.handle(get, "/api/aps", s.listAPs)
	r.handle(post, "/api/aps", s.registerAP)
	r.handle(get, "/api/aps/pending", s.listPending)
	r.handle(del, "/api/aps/pending/{id}", s.dismissPending)
	r.handle(get, "/api/aps/{ap}", s.getAP)
	r.handle(patch, "/api/aps/{ap}", s.patchAP)
	r.handle(put, "/api/aps/{ap}", s.putAP)
	r.handle(del, "/api/aps/{ap}", s.deleteAP)
	r.handle(put, "/api/aps/{ap}/site", s.moveAP)
	r.handle(get, "/api/aps/{ap}/telemetry", s.apTelemetryHandler)
	r.handle(get, "/api/aps/{ap}/clients", s.apClientsHandler)
	r.handle(get, "/api/aps/{ap}/events", s.apEventsHandler)
	r.handle(get, "/api/aps/{ap}/log", s.apLogHandler)
	r.handle(get, "/api/aps/{ap}/config", s.apConfigView)
	for _, name := range []string{"expert", "advanced"} {
		r.handle(get, "/api/aps/{ap}/"+name, apGet(s))
		r.handle(put, "/api/aps/{ap}/"+name, apPut(s))
	}
	r.handle(post, "/api/aps/{ap}/actions/{action}", s.apAction)

	// Clients.
	r.handle(get, "/api/clients/{mac}", s.clientDetail)
	r.handle(put, "/api/clients/{mac}/meta", s.putClientMeta)

	// Deprecated single-AP endpoints of the first releases.
	r.handle(get, "/api/status", s.legacyStatus)
	r.handle(get, "/api/telemetry", s.legacyTelemetry)
	r.handle(get, "/api/clients", s.legacyClients)
	r.handle(get, "/api/events", s.legacyEvents)
	r.handle(get, "/api/networks", s.legacyNetworks)
	r.handle(post, "/api/networks", s.legacyNetworkCreate)
	r.handle(put, "/api/networks/{id}", s.legacyNetworkEdit)
	r.handle(del, "/api/networks/{id}", s.legacyNetworkDelete)
}

// siteAPRoute dispatches GET /api/sites/$s/aps/$ap/$kind.
func (s *Server) siteAPRoute(writer http.ResponseWriter, request *http.Request, p params) {
	switch p["kind"] {
	case "samples", "sessions":
		s.apHistory(writer, request, p)
	case "advanced":
		get, _ := expertHandlers(api.ExpertScopeAP)
		get(s)(writer, request, p)
	default:
		s.siteAPAlias(writer, request, p)
	}
}
