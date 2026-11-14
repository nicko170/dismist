package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// This file keeps the phase-0 response shapes the legacy app (served at
// /legacy/ until the release that deletes it) and old scripts read. The
// v1 endpoints answer in these shapes when legacyClient(request) is true;
// the /api/status, /api/telemetry, /api/clients, /api/events and
// /api/networks endpoints and the site-nested AP aliases are deprecated
// (design §4.9) and keep them always.

// legacyAPView is the phase-0 map view of an AP.
func (s *Server) legacyAPView(ap model.APRecord) map[string]any {
	obs := s.observe(ap.ID)
	clientCount := 0
	if obs.fresh(ap.ConfigUpdatedAt) {
		clientCount = len(obs.telemetry.AssociatedClients)
	}
	managementIP := ap.ManagementIP
	if managementIP == "" {
		managementIP = obs.pinnedIP
	}
	apModel := ap.Model
	if obs.status.Model != "" {
		apModel = obs.status.Model
	}
	return map[string]any{
		"id": ap.ID, "name": ap.Name, "site_id": ap.SiteID,
		"model": apModel, "firmware": firstNonEmpty(obs.status.Firmware, ap.Firmware),
		"management_ip": managementIP, "lan_ip": lanIPv4(obs.telemetry.Interfaces),
		"remote_address": obs.status.RemoteAddress, "has_secret": len(ap.Secret) == model.SecretSize,
		"config_revision": ap.ConfigRevision, "connected": obs.status.Connected, "client_count": clientCount,
		"last_seen": obs.status.LastSeen, "applied_revision": obs.status.AppliedRevision,
	}
}

func (s *Server) legacyAPList(writer http.ResponseWriter, state model.State) {
	aps := make([]map[string]any, 0, len(state.APs))
	for _, ap := range state.APs {
		aps = append(aps, s.legacyAPView(ap))
	}
	respond(writer, http.StatusOK, map[string]any{"aps": aps})
}

func legacySiteView(site model.Site) map[string]any {
	return map[string]any{"id": site.ID, "name": site.Name, "country": site.Country,
		"timezone": site.Timezone, "advanced": redactPatch(recordKey(site, "advanced"))}
}

func (s *Server) legacySiteList(writer http.ResponseWriter, state model.State) {
	sites := make([]map[string]any, 0, len(state.Sites))
	for _, site := range state.Sites {
		apCount := len(siteAPs(state, site.ID))
		networkCount := 0
		for _, network := range state.Networks {
			if network.SiteID == site.ID {
				networkCount++
			}
		}
		sites = append(sites, map[string]any{
			"id": site.ID, "name": site.Name, "country": site.Country,
			"timezone": site.Timezone, "ap_count": apCount, "network_count": networkCount,
		})
	}
	respond(writer, http.StatusOK, map[string]any{"sites": sites, "revision": state.Revision})
}

type legacyNetwork struct {
	ID      string `json:"id"`
	SSID    string `json:"ssid"`
	VLANID  int    `json:"vlan_id"`
	Enabled bool   `json:"enabled"`
	HasPSK  bool   `json:"has_psk"`
}

func (s *Server) legacyNetworkList(writer http.ResponseWriter, state model.State, siteID string) {
	networks := make([]legacyNetwork, 0, len(state.Networks))
	legacy := len(state.Sites) == 0
	for _, network := range state.Networks {
		if network.SiteID != siteID && !(legacy && network.SiteID == "") {
			continue
		}
		networks = append(networks, legacyNetwork{ID: network.ID, SSID: network.SSID, VLANID: network.VLANID, Enabled: network.Enabled, HasPSK: network.PSK != ""})
	}
	respond(writer, http.StatusOK, map[string]any{"revision": state.Revision, "networks": networks})
}

// liveSnapshot returns the AP's controller telemetry when it belongs to
// the current session and config, else an empty snapshot.
func (s *Server) liveSnapshot(ap model.APRecord) controller.TelemetrySnapshot {
	control := s.controlFor(ap.ID)
	if control == nil {
		return controller.TelemetrySnapshot{}
	}
	if !observeControl(control).fresh(ap.ConfigUpdatedAt) {
		return controller.TelemetrySnapshot{}
	}
	return control.TelemetrySnapshot()
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

func (s *Server) legacySiteClients(writer http.ResponseWriter, state model.State, siteID string) {
	clients := make([]map[string]any, 0)
	recent := make([]map[string]any, 0)
	for _, ap := range siteAPs(state, siteID) {
		control := s.controlFor(ap.ID)
		if control == nil {
			continue
		}
		snapshot := s.liveSnapshot(ap)
		for _, client := range snapshot.AssociatedClients {
			object, _ := recordMap(client)
			object["ap_id"], object["ap_name"] = ap.ID, ap.Name
			clients = append(clients, object)
		}
		for _, activity := range liveRecent(ap, control.TelemetrySnapshot().RecentClients) {
			object, _ := recordMap(activity)
			object["ap_id"], object["ap_name"] = ap.ID, ap.Name
			recent = append(recent, object)
		}
	}
	respond(writer, http.StatusOK, map[string]any{"clients": clients, "recent_activity": recent})
}

// legacyStatus serves GET /api/status for the default AP.
func (s *Server) legacyStatus(writer http.ResponseWriter, _ *http.Request, _ params) {
	var desired uint64
	var status controller.Status
	if control := s.controlFor(s.apID); control != nil {
		_, desired = control.ConfigSnapshot()
		status = control.Status()
	}
	respond(writer, http.StatusOK, map[string]any{"ap_id": s.apID, "desired_revision": desired, "ap": status})
}

// legacyTelemetry serves GET /api/telemetry for the default AP.
func (s *Server) legacyTelemetry(writer http.ResponseWriter, _ *http.Request, _ params) {
	var snapshot controller.TelemetrySnapshot
	if control := s.controlFor(s.apID); control != nil {
		snapshot = control.TelemetrySnapshot()
	}
	respond(writer, http.StatusOK, snapshot)
}

// legacyClients serves GET /api/clients for the default AP.
func (s *Server) legacyClients(writer http.ResponseWriter, _ *http.Request, _ params) {
	var snapshot controller.TelemetrySnapshot
	if control := s.controlFor(s.apID); control != nil {
		snapshot = control.TelemetrySnapshot()
	}
	associated := any(snapshot.AssociatedClients)
	if snapshot.AssociatedClients == nil {
		associated = []any{}
	}
	recent := any(snapshot.RecentClients)
	if snapshot.RecentClients == nil {
		recent = []any{}
	}
	respond(writer, http.StatusOK, map[string]any{"clients": associated, "recent_activity": recent})
}

// legacyEvents serves GET /api/events for the default AP.
func (s *Server) legacyEvents(writer http.ResponseWriter, _ *http.Request, _ params) {
	var events []controller.ConnectionEvent
	if control := s.controlFor(s.apID); control != nil {
		events = control.TelemetrySnapshot().Events
	}
	if events == nil {
		events = []controller.ConnectionEvent{}
	}
	respond(writer, http.StatusOK, map[string]any{"events": events})
}

// The default AP's site for the /api/networks aliases.
func (s *Server) legacyNetworks(writer http.ResponseWriter, request *http.Request, _ params) {
	s.legacyNetworkList(writer, s.store.Snapshot(), s.currentSiteID())
}

func (s *Server) legacyNetworkCreate(writer http.ResponseWriter, request *http.Request, _ params) {
	request.Header.Set("Referer", "/legacy/")
	s.createNetwork(writer, request, params{"site": s.currentSiteID()})
}

func (s *Server) legacyNetworkEdit(writer http.ResponseWriter, request *http.Request, p params) {
	request.Header.Set("Referer", "/legacy/")
	s.updateNetwork(writer, request, s.currentSiteID(), p["id"])
}

func (s *Server) legacyNetworkDelete(writer http.ResponseWriter, request *http.Request, p params) {
	request.Header.Set("Referer", "/legacy/")
	s.deleteNetwork(writer, request, params{"site": s.currentSiteID(), "nid": p["id"]})
}

// siteAPAlias serves the deprecated GET /api/sites/$s/aps/$ap/{status,
// telemetry, clients, events, config}.
func (s *Server) siteAPAlias(writer http.ResponseWriter, request *http.Request, p params) {
	state := s.store.Snapshot()
	if findSite(state, p["site"]) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	ap := findAP(state, p["ap"])
	if ap == nil || ap.SiteID != p["site"] {
		writeAPIError(writer, notFound("AP"))
		return
	}
	control := s.controlFor(ap.ID)
	switch p["kind"] {
	case "status":
		var status controller.Status
		var desired uint64
		if control != nil {
			status = control.Status()
			_, desired = control.ConfigSnapshot()
		}
		respond(writer, http.StatusOK, map[string]any{"ap_id": ap.ID, "desired_revision": desired, "ap": status})
	case "telemetry":
		respond(writer, http.StatusOK, s.liveSnapshot(*ap))
	case "clients":
		snapshot := s.liveSnapshot(*ap)
		associated := any(snapshot.AssociatedClients)
		if snapshot.AssociatedClients == nil {
			associated = []any{}
		}
		recent := []controller.ClientActivity{}
		if control != nil {
			recent = liveRecent(*ap, control.TelemetrySnapshot().RecentClients)
		}
		respond(writer, http.StatusOK, map[string]any{"clients": associated, "recent_activity": recent})
	case "events":
		events := []controller.ConnectionEvent{}
		if control != nil {
			events = liveEvents(*ap, control.TelemetrySnapshot().Events)
		}
		respond(writer, http.StatusOK, map[string]any{"events": events})
	case "config":
		site := findSite(state, ap.SiteID)
		effective, err := compile.BuildSiteConfig(s.base, *site, *ap, compile.NetworksForSite(state, ap.SiteID), state.UpdatedAt)
		if err != nil {
			writeAPIError(writer, invalid("config preview failed: "+err.Error()))
			return
		}
		redacted, err := redactJSON(effective)
		if err != nil {
			writeAPIError(writer, invalid("config preview failed"))
			return
		}
		respond(writer, http.StatusOK, map[string]any{
			"effective": redacted, "site_patch": redactPatch(recordKey(*site, "advanced")), "ap_patch": redactPatch(recordKey(*ap, "advanced")),
		})
	default:
		routeNotFound(writer)
	}
}

// legacyStream is the phase-0 site stream: an "update" event whenever the
// site, its APs, networks or their session state changes, and at least
// once a minute.
func (s *Server) legacyStream(writer http.ResponseWriter, request *http.Request, siteID string) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, internalError("streaming unavailable"))
		return
	}
	update, previous, found, err := s.legacyStreamSnapshot(siteID, time.Now().UTC())
	if err != nil {
		writeAPIError(writer, internalError("stream unavailable"))
		return
	}
	if !found {
		writeAPIError(writer, notFound("site"))
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set("Connection", "keep-alive")
	if err := writeSSE(writer, flusher, "update", update); err != nil {
		return
	}
	interval, heartbeat, maxAge := s.streamInterval, s.streamHeartbeat, s.streamMaxAge
	if interval <= 0 {
		interval = time.Second
	}
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	check := time.NewTicker(interval)
	beat := time.NewTicker(heartbeat)
	deadline := time.NewTimer(maxAge)
	defer check.Stop()
	defer beat.Stop()
	defer deadline.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-deadline.C:
			return
		case <-beat.C:
			if _, err := io.WriteString(writer, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-check.C:
			update, current, found, err := s.legacyStreamSnapshot(siteID, time.Now().UTC())
			if err != nil || !found {
				return
			}
			if current == previous {
				continue
			}
			if err := writeSSE(writer, flusher, "update", update); err != nil {
				return
			}
			previous = current
		}
	}
}

type legacyStreamAP struct {
	ID              string `json:"id"`
	Connected       bool   `json:"connected"`
	AppliedRevision uint64 `json:"applied_revision"`
	FullFrames      uint64 `json:"full_frames"`
	FastFrames      uint64 `json:"fast_frames"`
	ClientCount     int    `json:"client_count"`
}

type legacyStreamUpdate struct {
	SiteID     string           `json:"site_id"`
	Revision   uint64           `json:"revision"`
	ObservedAt time.Time        `json:"observed_at"`
	APs        []legacyStreamAP `json:"aps"`
}

func (s *Server) legacyStreamSnapshot(siteID string, at time.Time) (legacyStreamUpdate, [32]byte, bool, error) {
	state := s.store.Snapshot()
	update := legacyStreamUpdate{SiteID: siteID, Revision: state.Revision, ObservedAt: at, APs: make([]legacyStreamAP, 0)}
	site := findSite(state, siteID)
	if site == nil {
		return legacyStreamUpdate{}, [32]byte{}, false, nil
	}
	aps := siteAPs(state, siteID)
	statuses := make([]obsStatus, 0, len(aps))
	for _, ap := range aps {
		obs := s.observe(ap.ID)
		statuses = append(statuses, obs.status)
		clients := 0
		if obs.fresh(ap.ConfigUpdatedAt) {
			clients = len(obs.telemetry.AssociatedClients)
		}
		update.APs = append(update.APs, legacyStreamAP{ID: ap.ID, Connected: obs.status.Connected, AppliedRevision: obs.status.AppliedRevision,
			FullFrames: obs.telemetry.FullFrames, FastFrames: obs.telemetry.FastFrames, ClientCount: clients})
	}
	networks := compile.NetworksForSite(state, siteID)
	fingerprint, err := legacyFingerprint(*site, aps, networks, statuses, update.APs, at.Unix()/60)
	if err != nil {
		return legacyStreamUpdate{}, [32]byte{}, false, err
	}
	return update, fingerprint, true, nil
}

func legacyFingerprint(site model.Site, aps []model.APRecord, networks []model.Network, statuses []obsStatus, summaries []legacyStreamAP, minute int64) ([32]byte, error) {
	input := struct {
		Site      model.Site
		APs       []model.APRecord
		Networks  []model.Network
		Statuses  []obsStatus
		Summaries []legacyStreamAP
		Minute    int64
	}{site, aps, networks, statuses, summaries, minute}
	raw, err := json.Marshal(input)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
