package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

type historyReader interface {
	ListSamples(string, string, int) ([]model.MinuteSample, error)
	ListClientSessions(string, string, int) ([]model.ClientSession, error)
	ListEvents(string, string, int) ([]model.EventRecord, error)
}

type eventStore interface {
	Events2() *model.EventStore
}

func eventView(event model.EventV2, names map[string]string) api.EventView {
	view := api.EventView{
		ID: event.ID, SiteID: event.SiteID, APID: event.APID, APName: names[event.APID], Kind: event.Kind,
		PaceType: event.PaceType, Severity: api.Severity(event.Severity), Title: event.Title, Detail: event.Detail,
		ClientMAC: firstNonEmpty(normaliseMAC(event.ClientMAC), event.ClientMAC), WLANID: event.WLANID, FirstSeen: event.FirstSeen, LastSeen: event.LastSeen,
		Count: event.Count, Data: map[string]any{},
	}
	if view.Severity == "" {
		view.Severity = api.SeverityInfo
	}
	if event.ResolvedBy != "" {
		resolved := event.ResolvedBy
		view.ResolvedBy = &resolved
	}
	if len(event.Data) > 0 {
		_ = json.Unmarshal(event.Data, &view.Data)
		if view.Data == nil {
			view.Data = map[string]any{}
		}
	}
	return view
}

func eventSummary(event model.EventV2) api.EventSummary {
	summary := api.EventSummary{ID: event.ID, APID: event.APID, Kind: event.Kind, Severity: api.Severity(event.Severity),
		Title: event.Title, FirstSeen: event.FirstSeen, LastSeen: event.LastSeen, Count: event.Count, HasClient: event.ClientMAC != ""}
	if summary.Severity == "" {
		summary.Severity = api.SeverityInfo
	}
	if event.ResolvedBy != "" {
		resolved := event.ResolvedBy
		summary.ResolvedBy = &resolved
	}
	return summary
}

func apNames(state model.State) map[string]string {
	names := make(map[string]string, len(state.APs))
	for _, ap := range state.APs {
		names[ap.ID] = firstNonEmpty(ap.Name, ap.ID)
	}
	return names
}

// rangeSince converts ?range= (1h, 24h, 7d, 30d or a Go duration) to a
// start time.
func rangeSince(value string, now time.Time) (time.Time, bool) {
	if value == "" {
		return time.Time{}, true
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 || days > 400 {
			return time.Time{}, false
		}
		return now.Add(-time.Duration(days) * 24 * time.Hour), true
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return time.Time{}, false
	}
	return now.Add(-duration), true
}

// listEvents serves GET /api/sites/$id/events?ap=&client=&kind=&severity=&range=&since=&before=&limit=
// (stored events, newest first, design §4.9).
func (s *Server) listEvents(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil && siteID != "" {
		writeAPIError(writer, notFound("site"))
		return
	}
	events, ok := s.store.(eventStore)
	if !ok {
		respond(writer, http.StatusOK, api.EventPage{Events: []api.EventView{}})
		return
	}
	query := request.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	var before, since time.Time
	if raw := query.Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeAPIError(writer, invalidField("before", "before must be an RFC 3339 time"))
			return
		}
		before = parsed
	}
	if raw := query.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeAPIError(writer, invalidField("since", "since must be an RFC 3339 time"))
			return
		}
		since = parsed
	}
	if raw := query.Get("range"); raw != "" {
		start, ok := rangeSince(raw, s.now())
		if !ok {
			writeAPIError(writer, invalidField("range", "range must be 1h, 24h, 7d or 30d"))
			return
		}
		if start.After(since) {
			since = start
		}
	}
	client := query.Get("client")
	if client != "" {
		if mac := normaliseMAC(client); mac != "" {
			client = mac
		}
	}
	page := api.EventPage{Events: []api.EventView{}}
	names := apNames(state)
	cursor := before
	for len(page.Events) < limit {
		batch, next, err := events.Events2().List(model.EventQuery{
			SiteID: siteID, APID: query.Get("ap"), Before: cursor, Limit: limit + 1,
			Kind: query.Get("kind"), Severity: query.Get("severity"), Client: client,
		})
		if err != nil {
			writeAPIError(writer, internalError("event history read failed"))
			return
		}
		done := false
		for _, event := range batch {
			if !since.IsZero() && event.LastSeen.Before(since) {
				done = true
				break
			}
			if len(page.Events) == limit {
				nextBefore := page.Events[len(page.Events)-1].LastSeen
				page.NextBefore = &nextBefore
				done = true
				break
			}
			page.Events = append(page.Events, eventView(event, names))
		}
		if done || next.IsZero() {
			break
		}
		cursor = next
	}
	respond(writer, http.StatusOK, page)
}

// getEvent serves GET /api/sites/$id/events/$eventId (with typed data).
func (s *Server) getEvent(writer http.ResponseWriter, _ *http.Request, p params) {
	events, ok := s.store.(eventStore)
	if !ok {
		writeAPIError(writer, notFound("event"))
		return
	}
	event, err := events.Events2().Get(p["event"])
	if err != nil || event.SiteID != p["site"] {
		writeAPIError(writer, notFound("event"))
		return
	}
	respond(writer, http.StatusOK, eventView(event, apNames(s.store.Snapshot())))
}

// samples serves GET /api/sites/$id/history/samples?ap=&range=&step=: the
// site sum (or one AP) of the minute samples per step.
func (s *Server) samples(writer http.ResponseWriter, request *http.Request, siteID string) {
	history, ok := s.store.(historyReader)
	if !ok {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "history unavailable"))
		return
	}
	query := request.URL.Query()
	rangeValue := query.Get("range")
	if rangeValue == "" {
		rangeValue = "24h"
	}
	start, ok := rangeSince(rangeValue, s.now())
	if !ok {
		writeAPIError(writer, invalidField("range", "range must be 1h, 24h, 7d or 30d"))
		return
	}
	stepValue := query.Get("step")
	step := 10 * time.Minute
	if stepValue != "" {
		parsed, err := time.ParseDuration(stepValue)
		if err != nil || parsed < time.Minute || parsed > 24*time.Hour {
			writeAPIError(writer, invalidField("step", "step must be 1m to 24h"))
			return
		}
		step = parsed
	} else {
		stepValue = step.String()
	}
	apID := query.Get("ap")
	limit := int(s.now().Sub(start)/time.Minute)*maxInt(1, len(siteAPs(s.store.Snapshot(), siteID))) + 10
	if limit > 50000 {
		limit = 50000
	}
	items, err := history.ListSamples(siteID, apID, limit)
	if err != nil {
		writeAPIError(writer, internalError("history read failed"))
		return
	}
	type bucket struct {
		sample    api.Sample
		perAP     map[string]int
		connected map[string]bool
		rssiSum   float64
		rssiN     int
		radios    map[string]*api.SampleRadio
		radioN    map[string]int
	}
	buckets := map[int64]*bucket{}
	for _, item := range items {
		if item.Minute.Before(start) {
			continue
		}
		key := item.Minute.Truncate(step).Unix()
		b := buckets[key]
		if b == nil {
			b = &bucket{sample: api.Sample{At: time.Unix(key, 0).UTC(), Radios: []api.SampleRadio{}}, perAP: map[string]int{},
				connected: map[string]bool{}, radios: map[string]*api.SampleRadio{}, radioN: map[string]int{}}
			buckets[key] = b
		}
		if item.ClientCount > b.perAP[item.APID] {
			b.perAP[item.APID] = item.ClientCount
		}
		if item.Connected {
			b.connected[item.APID] = true
		}
		b.sample.TxBytes += item.TxBytes
		b.sample.RxBytes += item.RxBytes
		if item.AverageRSSI != 0 {
			b.rssiSum += float64(item.AverageRSSI)
			b.rssiN++
		}
		for _, radio := range item.Radios {
			radioKey := item.APID + "\x00" + radio.Band
			if apID == "" {
				radioKey = radio.Band
			}
			current := b.radios[radioKey]
			if current == nil {
				current = &api.SampleRadio{Band: radio.Band}
				b.radios[radioKey] = current
			}
			current.Channel, current.Width, current.MaxPowerDBm = radio.Channel, radio.Width, radio.MaxPowerDBm
			current.Util += radio.UtilAll
			current.NoiseDBm = radio.NoiseDBm
			if radio.Clients > current.Clients {
				current.Clients = radio.Clients
			}
			b.radioN[radioKey]++
		}
	}
	page := api.SamplePage{SiteID: siteID, APID: apID, Range: rangeValue, Step: stepValue, Samples: []api.Sample{}}
	keys := make([]int64, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		b := buckets[key]
		for _, count := range b.perAP {
			b.sample.Clients += count
		}
		b.sample.APsConnected = len(b.connected)
		if b.rssiN > 0 {
			average := b.rssiSum / float64(b.rssiN)
			b.sample.AverageRSSI = &average
		}
		radioKeys := make([]string, 0, len(b.radios))
		for radioKey := range b.radios {
			radioKeys = append(radioKeys, radioKey)
		}
		sort.Strings(radioKeys)
		for _, radioKey := range radioKeys {
			radio := *b.radios[radioKey]
			radio.Util /= float64(maxInt(1, b.radioN[radioKey]))
			b.sample.Radios = append(b.sample.Radios, radio)
		}
		page.Samples = append(page.Samples, b.sample)
	}
	respond(writer, http.StatusOK, page)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// siteHistory serves GET /api/sites/$id/history/{samples,sessions,events}:
// samples in the v1 form; sessions and events as before (legacy).
func (s *Server) siteHistory(writer http.ResponseWriter, request *http.Request, p params) {
	siteID, kind := p["site"], p["kind"]
	if findSite(s.store.Snapshot(), siteID) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	if kind == "samples" && !legacyClient(request) && request.URL.Query().Get("limit") == "" {
		s.samples(writer, request, siteID)
		return
	}
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

// apHistory serves the deprecated site-nested AP history aliases:
// …/aps/$ap/samples, …/sessions and …/history/events.
func (s *Server) apHistory(writer http.ResponseWriter, request *http.Request, p params) {
	history, ok := s.store.(historyReader)
	if !ok {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "history unavailable"))
		return
	}
	siteID, apID := p["site"], p["ap"]
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	ap := findAP(state, apID)
	assigned := ap != nil && ap.SiteID == siteID
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	var items any
	var count int
	var err error
	key := p["kind"]
	switch key {
	case "samples":
		var values []model.MinuteSample
		values, err = history.ListSamples(siteID, apID, limit)
		items, count = values, len(values)
	case "sessions":
		var values []model.ClientSession
		values, err = history.ListClientSessions(siteID, apID, limit)
		items, count = values, len(values)
	case "events":
		var values []model.EventRecord
		values, err = history.ListEvents(siteID, apID, limit)
		items, count = values, len(values)
	default:
		routeNotFound(writer)
		return
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
