package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/webgate"
)

// TokenManager is the API token store (model.TokenStore implements it).
type TokenManager interface {
	Create(name, scope string, now time.Time) (model.TokenRecord, string, error)
	List() ([]model.TokenRecord, error)
	Get(id string) (model.TokenRecord, error)
	Delete(id string) error
}

// UseTokens sets the token store for /api/system/tokens. New picks the
// store's own Tokens() when it has one.
func (s *Server) UseTokens(tokens TokenManager) *Server {
	s.tokens = tokens
	return s
}

// principal returns who made the request. A request that did not pass
// through the gate (tests, an in-process caller) counts as admin.
func principal(request *http.Request) webgate.Principal {
	if p, ok := webgate.PrincipalFrom(request.Context()); ok {
		return p
	}
	return webgate.Principal{Via: api.AuthViaSession, Scope: api.TokenScopeAdmin}
}

// requireAdmin refuses read-scope tokens on admin-only resources.
func requireAdmin(writer http.ResponseWriter, request *http.Request) bool {
	if principal(request).Admin() {
		return true
	}
	writeAPIError(writer, forbidden("this needs an admin session or an admin-scope API token"))
	return false
}

func tokenView(record model.TokenRecord) api.TokenView {
	view := api.TokenView{ID: record.ID, Name: record.Name, Scope: api.TokenScope(record.Scope), Created: record.Created}
	if !record.LastUsed.IsZero() {
		used := record.LastUsed
		view.LastUsed = &used
	}
	return view
}

func (s *Server) tokensUnavailable(writer http.ResponseWriter) bool {
	if s.tokens != nil {
		return false
	}
	writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "API tokens need the bbolt database"))
	return true
}

// listTokens serves GET /api/system/tokens.
func (s *Server) listTokens(writer http.ResponseWriter, _ *http.Request, _ params) {
	if s.tokensUnavailable(writer) {
		return
	}
	records, err := s.tokens.List()
	if err != nil {
		log.Printf("list API tokens: %v", err)
		writeAPIError(writer, internalError("cannot read API tokens"))
		return
	}
	list := api.TokenList{Tokens: make([]api.TokenView, 0, len(records))}
	for _, record := range records {
		list.Tokens = append(list.Tokens, tokenView(record))
	}
	respond(writer, http.StatusOK, list)
}

// createToken serves POST /api/system/tokens (the token is shown once).
func (s *Server) createToken(writer http.ResponseWriter, request *http.Request, _ params) {
	if s.tokensUnavailable(writer) {
		return
	}
	var input api.TokenInput
	if err := decodeJSON(request, 4*1024, &input, "token"); err != nil {
		writeError(writer, err)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 64 {
		writeAPIError(writer, invalidField("name", "token name must be 1 to 64 characters"))
		return
	}
	if input.Scope != api.TokenScopeRead && input.Scope != api.TokenScopeAdmin {
		writeAPIError(writer, invalidField("scope", `scope must be "read" or "admin"`))
		return
	}
	record, token, err := s.tokens.Create(input.Name, string(input.Scope), time.Now().UTC())
	if err != nil {
		log.Printf("create API token: %v", err)
		writeAPIError(writer, internalError("cannot create the API token"))
		return
	}
	log.Printf("audit: API token %s (%s, %s) created by %s", record.ID, record.Name, record.Scope, principal(request))
	s.recordAction(request, "", "Created API token "+record.Name+" ("+record.Scope+")")
	respond(writer, http.StatusCreated, api.TokenCreated{TokenView: tokenView(record), Token: token})
}

// revokeToken serves DELETE /api/system/tokens/$id.
func (s *Server) revokeToken(writer http.ResponseWriter, request *http.Request, p params) {
	if s.tokensUnavailable(writer) {
		return
	}
	id := p["id"]
	record, err := s.tokens.Get(id)
	if err == nil {
		err = s.tokens.Delete(id)
	}
	if errors.Is(err, model.ErrNotFound) {
		writeAPIError(writer, notFound("API token"))
		return
	}
	if err != nil {
		log.Printf("revoke API token %s: %v", id, err)
		writeAPIError(writer, internalError("cannot revoke the API token"))
		return
	}
	log.Printf("audit: API token %s (%s) revoked by %s", record.ID, record.Name, principal(request))
	s.recordAction(request, "", "Revoked API token "+record.Name)
	respond(writer, http.StatusOK, tokenView(record))
}
