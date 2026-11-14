package httpapi

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// SessionManager is the persisted web-session store (model.SessionStore).
type SessionManager interface {
	List(now time.Time) ([]model.StoredSession, error)
	Delete(key string) error
	DeleteAll(keep string) (int, error)
}

func (s *Server) sessionsUnavailable(writer http.ResponseWriter) bool {
	if s.sessions != nil {
		return false
	}
	writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "persisted sessions need the bbolt database"))
	return true
}

// listSessions serves GET /api/system/sessions.
func (s *Server) listSessions(writer http.ResponseWriter, request *http.Request, _ params) {
	if s.sessionsUnavailable(writer) {
		return
	}
	stored, err := s.sessions.List(s.now())
	if err != nil {
		log.Printf("list sessions: %v", err)
		writeAPIError(writer, internalError("cannot read sessions"))
		return
	}
	current := principal(request).SessionKey
	list := api.AuthSessionList{Sessions: make([]api.AuthSessionView, 0, len(stored))}
	for _, session := range stored {
		list.Sessions = append(list.Sessions, api.AuthSessionView{ID: session.Key, Created: session.Created, Expires: session.Expires,
			LastUsed: timePtr(session.LastUsed), Remote: session.Remote, UserAgent: session.UserAgent, Current: session.Key == current})
	}
	respond(writer, http.StatusOK, list)
}

// revokeSession serves DELETE /api/system/sessions/$id.
func (s *Server) revokeSession(writer http.ResponseWriter, request *http.Request, p params) {
	if s.sessionsUnavailable(writer) {
		return
	}
	err := s.sessions.Delete(p["id"])
	if errors.Is(err, model.ErrNotFound) {
		writeAPIError(writer, notFound("session"))
		return
	}
	if err != nil {
		log.Printf("revoke session: %v", err)
		writeAPIError(writer, internalError("cannot revoke the session"))
		return
	}
	log.Printf("audit: web session %s revoked by %s", shortID(p["id"]), principal(request))
	respond(writer, http.StatusOK, api.RevokedSessions{Revoked: 1})
}

// revokeAllSessions serves POST /api/system/sessions/revoke-all: every
// session except the caller's ("sign out everywhere else"); with
// ?include_current=1 the caller's too.
func (s *Server) revokeAllSessions(writer http.ResponseWriter, request *http.Request, _ params) {
	if s.sessionsUnavailable(writer) {
		return
	}
	keep := principal(request).SessionKey
	if request.URL.Query().Get("include_current") == "1" {
		keep = ""
	}
	count, err := s.sessions.DeleteAll(keep)
	if err != nil {
		log.Printf("revoke sessions: %v", err)
		writeAPIError(writer, internalError("cannot revoke sessions"))
		return
	}
	log.Printf("audit: %d web sessions revoked by %s", count, principal(request))
	respond(writer, http.StatusOK, api.RevokedSessions{Revoked: count})
}
