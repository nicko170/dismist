package httpapi

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

type pendingDismissalStore interface {
	PendingDismissed() *model.PendingDismissalStore
}

// dismissedPending returns the active dismissals by AP ID.
func (s *Server) dismissedPending() map[string]time.Time {
	result := map[string]time.Time{}
	store, ok := s.store.(pendingDismissalStore)
	if !ok {
		return result
	}
	active, err := store.PendingDismissed().Active(s.now())
	if err != nil {
		log.Printf("pending dismissals: %v", err)
		return result
	}
	for id, dismissal := range active {
		result[id] = dismissal.Until
	}
	return result
}

// listPending serves GET /api/aps/pending[?dismissed=1] (design §3.15): APs
// that said hello and are not served. Dismissed rows are hidden unless
// ?dismissed=1; registered APs refused for their source or a failed
// authentication are listed too.
func (s *Server) listPending(writer http.ResponseWriter, request *http.Request, _ params) {
	result := []api.PendingAP{}
	if s.manager == nil {
		respond(writer, http.StatusOK, result)
		return
	}
	showDismissed := request.URL.Query().Get("dismissed") == "1" || request.URL.Query().Get("dismissed") == "true"
	dismissed := s.dismissedPending()
	for _, entry := range s.manager.Pending() {
		view := api.PendingAP{ID: entry.ID, Model: entry.Model, SKU: entry.SKU, Serial: entry.Serial, Firmware: entry.Firmware,
			LocalIP: entry.LocalIP, RemoteAddress: entry.RemoteAddress, FirstSeen: entry.FirstSeen, LastSeen: entry.LastSeen,
			Attempts: int(entry.Attempts), Reason: api.PendingReason(entry.Reason)}
		if until, hidden := dismissed[entry.ID]; hidden {
			if !showDismissed {
				continue
			}
			view.Dismissed, view.DismissedUntil = true, timePtr(until)
		}
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastSeen.After(result[j].LastSeen) })
	respond(writer, http.StatusOK, result)
}

// dismissPending serves DELETE /api/aps/pending/$id?days=30: the row stays
// hidden for that long (stored in pending_dismissed); its attempts keep
// counting.
func (s *Server) dismissPending(writer http.ResponseWriter, request *http.Request, p params) {
	store, ok := s.store.(pendingDismissalStore)
	if !ok {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "dismissals need the bbolt database"))
		return
	}
	id := normaliseMAC(p["id"])
	if id == "" {
		id = p["id"]
	}
	days := 30
	if raw := request.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 365 {
			writeAPIError(writer, invalidField("days", "days must be 1 to 365"))
			return
		}
		days = parsed
	}
	now := s.now()
	dismissal := model.PendingDismissal{Until: now.Add(time.Duration(days) * 24 * time.Hour), At: now, Actor: principal(request).String()}
	if err := store.PendingDismissed().Dismiss(id, dismissal); err != nil {
		log.Printf("dismiss pending AP %s: %v", id, err)
		writeAPIError(writer, internalError("cannot dismiss the pending AP"))
		return
	}
	s.hub.publishAll(api.StreamEventPending, api.PendingEvent{Count: s.pendingCount()})
	respond(writer, http.StatusOK, api.PendingAP{ID: id, Dismissed: true, DismissedUntil: timePtr(dismissal.Until), FirstSeen: now, LastSeen: now, Reason: api.PendingReasonUnregistered})
}
