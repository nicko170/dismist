package httpapi

import (
	"net/http"
	"strconv"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

type historyReader interface {
	ListSamples(string, string, int) ([]model.MinuteSample, error)
	ListClientSessions(string, string, int) ([]model.ClientSession, error)
	ListEvents(string, string, int) ([]model.EventRecord, error)
}

func liveTelemetry(ap model.APRecord, status controller.Status, snapshot controller.TelemetrySnapshot) controller.TelemetrySnapshot {
	freshAfter := status.ConnectedAt
	if ap.ConfigUpdatedAt.After(freshAfter) {
		freshAfter = ap.ConfigUpdatedAt
	}
	if !status.Connected || (!snapshot.LastFull.After(freshAfter) && !snapshot.LastFast.After(freshAfter)) {
		return controller.TelemetrySnapshot{}
	}
	return snapshot
}

func liveEvents(ap model.APRecord, events []controller.ConnectionEvent) []controller.ConnectionEvent {
	result := make([]controller.ConnectionEvent, 0, len(events))
	for _, event := range events {
		if ap.ConfigUpdatedAt.IsZero() || !event.When.Before(ap.ConfigUpdatedAt) {
			result = append(result, event)
		}
	}
	return result
}

func liveRecent(ap model.APRecord, activities []controller.ClientActivity) []controller.ClientActivity {
	result := make([]controller.ClientActivity, 0, len(activities))
	for _, activity := range activities {
		if ap.ConfigUpdatedAt.IsZero() || !activity.LastSeen.Before(ap.ConfigUpdatedAt) {
			result = append(result, activity)
		}
	}
	return result
}

func (s *Server) siteHistory(writer http.ResponseWriter, request *http.Request, siteID, kind string) {
	history, ok := s.store.(historyReader)
	if !ok {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "history unavailable"))
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	switch kind {
	case "samples":
		items, err := history.ListSamples(siteID, "", limit)
		if err != nil {
			writeAPIError(writer, internalError("history read failed"))
			return
		}
		respond(writer, http.StatusOK, map[string]any{"samples": items})
	case "sessions":
		items, err := history.ListClientSessions(siteID, "", limit)
		if err != nil {
			writeAPIError(writer, internalError("history read failed"))
			return
		}
		respond(writer, http.StatusOK, map[string]any{"sessions": items})
	case "events":
		items, err := history.ListEvents(siteID, "", limit)
		if err != nil {
			writeAPIError(writer, internalError("history read failed"))
			return
		}
		respond(writer, http.StatusOK, map[string]any{"events": items})
	default:
		routeNotFound(writer)
	}
}

func (s *Server) apHistory(writer http.ResponseWriter, request *http.Request, state model.State, siteID string, parts []string) {
	history, ok := s.store.(historyReader)
	if !ok {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "history unavailable"))
		return
	}
	apID := parts[4]
	assigned := false
	for _, ap := range state.APs {
		if ap.ID == apID && ap.SiteID == siteID {
			assigned = true
			break
		}
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	var items any
	var count int
	var err error
	key := parts[5]
	switch key {
	case "samples":
		var values []model.MinuteSample
		values, err = history.ListSamples(siteID, apID, limit)
		items, count = values, len(values)
	case "sessions":
		var values []model.ClientSession
		values, err = history.ListClientSessions(siteID, apID, limit)
		items, count = values, len(values)
	case "history":
		var values []model.EventRecord
		values, err = history.ListEvents(siteID, apID, limit)
		items, count, key = values, len(values), "events"
	}
	if err != nil {
		writeAPIError(writer, internalError("history read failed"))
		return
	}
	if !assigned && count == 0 {
		writeAPIError(writer, notFound("AP"))
		return
	}
	respond(writer, http.StatusOK, map[string]any{key: items})
}

// legacyEvents serves GET /api/events for the default AP.
func (s *Server) legacyEvents(writer http.ResponseWriter) {
	var events []controller.ConnectionEvent
	if control := s.controlFor(s.apID); control != nil {
		events = control.TelemetrySnapshot().Events
	}
	if events == nil {
		events = []controller.ConnectionEvent{}
	}
	respond(writer, http.StatusOK, map[string]any{"events": events})
}
