package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/model"
)

// hashKeyStore is a store with a per-database HMAC key (meta.hash_key).
type hashKeyStore interface{ HashKey() []byte }

// deliveredStore reads ap_config.delivered.
type deliveredStore interface {
	APConfig() *model.APConfigStore
}

// h0Store reads meta.h0.
type h0Store interface {
	H0() (map[string]model.H0Record, error)
}

// apResource serves GET /api/aps/{id}/... (phase 0: the config view).
func (s *Server) apResource(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[3] == "config" {
		s.apConfigView(writer, request, parts[2])
		return
	}
	routeNotFound(writer)
}

// apConfigView serves GET /api/aps/{id}/config?view=effective|normalised|applied
// (design §4.5, §4.9): the redacted config with its keyed effective hash.
// view=normalised is admin only.
func (s *Server) apConfigView(writer http.ResponseWriter, request *http.Request, apID string) {
	view := api.ConfigView(request.URL.Query().Get("view"))
	if view == "" {
		view = api.ConfigViewEffective
	}
	switch view {
	case api.ConfigViewEffective, api.ConfigViewApplied:
	case api.ConfigViewNormalised:
		if !requireAdmin(writer, request) {
			return
		}
	default:
		writeAPIError(writer, invalidField("view", `view must be "effective", "normalised" or "applied"`))
		return
	}
	state := s.store.Snapshot()
	var ap model.APRecord
	found := false
	for _, candidate := range state.APs {
		if candidate.ID == apID {
			ap, found = candidate, true
			break
		}
	}
	if !found {
		writeAPIError(writer, notFound("AP"))
		return
	}
	var site model.Site
	for _, candidate := range state.Sites {
		if candidate.ID == ap.SiteID {
			site = candidate
		}
	}
	var key []byte
	if keyed, ok := s.store.(hashKeyStore); ok {
		key = keyed.HashKey()
	}
	result := api.APConfigView{
		APID: apID, View: view, NormaliserVersion: effective.Version,
		SitePatch: redactPatch(site.Advanced), APPatch: redactPatch(ap.Advanced),
	}
	var config []byte
	switch view {
	case api.ConfigViewApplied:
		delivered, ok := s.store.(deliveredStore)
		if !ok {
			writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "delivered configs need the bbolt database"))
			return
		}
		last, found, err := delivered.APConfig().Delivered(apID)
		if err != nil {
			log.Printf("AP %s: read delivered config: %v", apID, err)
			writeAPIError(writer, internalError("cannot read the delivered config"))
			return
		}
		if !found || len(last.Bytes) == 0 {
			writeAPIError(writer, notFound("delivered config").withField("view", "no config has been delivered to this AP yet"))
			return
		}
		config, result.Revision = last.Bytes, last.Revision
	default:
		input, err := compile.InputFor(s.base, state, apID)
		if errors.Is(err, compile.ErrNoSite) {
			writeAPIError(writer, notFound("config").withField("site_id", "the AP has no site, so dismist compiles no config for it"))
			return
		}
		var output compile.Output
		if err == nil {
			output, err = compile.AP(input)
		}
		if err != nil {
			writeAPIError(writer, newError(http.StatusUnprocessableEntity, api.ErrorCodeInvalid, "the AP's config does not compile: "+err.Error()))
			return
		}
		config, result.Revision = output.Config, compile.ConfigRevision(state, ap)
	}
	canonical, report, err := effective.Normalise(config, compile.EffectiveContext(ap))
	if err != nil {
		writeAPIError(writer, newError(http.StatusUnprocessableEntity, api.ErrorCodeInvalid, "the AP's config cannot be normalised: "+err.Error()))
		return
	}
	result.Report = &api.NormaliserReport{
		Version: report.Version, Ignored: nonNil(report.Ignored), TypeErrors: nonNil(report.TypeErrors),
		LocalFiles: report.LocalFiles, ZeroRule: report.ZeroRule, Notes: nonNil(report.Notes),
	}
	if len(key) != 0 {
		if result.EffectiveHash, err = effective.Hash(key, canonical); err != nil {
			log.Printf("AP %s: effective hash: %v", apID, err)
			writeAPIError(writer, internalError("cannot compute the effective hash"))
			return
		}
	}
	document := config
	if view == api.ConfigViewNormalised {
		document = canonical
	}
	redacted, err := redactJSON(document)
	if err != nil {
		writeAPIError(writer, internalError("cannot render the config"))
		return
	}
	result.Config = redacted
	if baselines, ok := s.store.(h0Store); ok {
		if all, err := baselines.H0(); err == nil {
			if h0, ok := all[apID]; ok {
				result.H0 = &api.H0View{
					RecordedAt: h0.RecordedAt, ConfigRevision: h0.ConfigRevision,
					NormaliserVersion: h0.NormaliserVersion, EffectiveHash: h0.EffectiveHash,
				}
				if result.EffectiveHash != "" && effective.SameVersion(h0.EffectiveHash, result.EffectiveHash) {
					matches := h0.EffectiveHash == result.EffectiveHash
					result.H0.Matches = &matches
				}
			}
		}
	}
	respond(writer, http.StatusOK, result)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
