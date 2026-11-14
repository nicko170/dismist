package httpapi

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

// signal buckets RSSI (design §3.12).
func signal(rssi int) api.Signal {
	switch {
	case rssi >= -60:
		return api.SignalExcellent
	case rssi >= -67:
		return api.SignalGood
	case rssi >= -75:
		return api.SignalFair
	}
	return api.SignalPoor
}

// privateMAC reports the locally administered bit.
func privateMAC(mac string) bool {
	compact := strings.NewReplacer(":", "", "-", "").Replace(mac)
	if len(compact) < 2 {
		return false
	}
	first, err := hex.DecodeString(compact[:2])
	return err == nil && first[0]&0x02 != 0
}

func (s *Server) clientMeta() map[string]api.ClientMeta {
	if s.config.ClientMeta == nil {
		return nil
	}
	all, err := s.config.ClientMeta.All()
	if err != nil {
		log.Printf("client meta: %v", err)
		return nil
	}
	return all
}

// clientView builds one ClientView from an AP's telemetry.
func clientView(state model.State, ap model.APRecord, obs apObs, client obsClient, meta map[string]api.ClientMeta) api.ClientView {
	mac := normaliseMAC(client.MAC)
	if mac == "" {
		mac = strings.ToLower(client.MAC)
	}
	view := api.ClientView{
		MAC: mac, PrivateMAC: privateMAC(mac), Hostname: client.Name, IP: client.IP, IPv6: []string{},
		APID: ap.ID, APName: ap.Name, WLANID: client.WLANID, SSID: client.SSID, Band: client.Band, Channel: client.Channel,
		Width: client.WidthMHz, BSSID: strings.ToLower(client.BSSID), Protocol: client.Protocol, RSSI: int(client.RSSI),
		AvgRSSI: int(client.AverageRSSI), Signal: signal(int(client.RSSI)), DownBytes: client.TxBytes, UpBytes: client.RxBytes,
		ConnectedSeconds: client.ConnectedSeconds, IdleMillis: client.InactiveMillis, SNR: client.SNR,
		SelfAssigned: client.SelfAssigned,
	}
	view.IPv6 = append(view.IPv6, client.IPv6...)
	if client.Streams > 0 {
		streams := client.Streams
		view.Streams = &streams
	}
	if client.TxMbps > 0 {
		rate := client.TxMbps
		view.TxRateMbps = &rate
	}
	if client.RxMbps > 0 {
		rate := client.RxMbps
		view.RxRateMbps = &rate
	}
	if ip := net.ParseIP(client.IP); ip != nil && ip.IsLinkLocalUnicast() && ip.To4() != nil {
		view.SelfAssigned = true
	}
	if view.Band == "" || view.Channel == 0 {
		for _, radio := range obs.telemetry.Radios {
			dev := firstNonEmpty(radio.Dev, radio.Interface)
			if dev == fmt.Sprintf("r%d", client.RadioIndex) && radio.Role != "scan" {
				if view.Band == "" && (radio.Band == "2.4" || radio.Band == "5") {
					view.Band = radio.Band
				}
				if view.Channel == 0 {
					view.Channel, view.Width = radio.Channel, radio.WidthMHz
				}
				if view.SNR == nil && radio.NoiseFloorDBm != 0 {
					snr := int(client.RSSI) - radio.NoiseFloorDBm
					view.SNR = &snr
				}
			}
		}
	}
	if view.WLANID == "" {
		for _, network := range compile.NetworksForSite(state, ap.SiteID) {
			if network.SSID == client.SSID {
				view.WLANID = network.ID
				break
			}
		}
	}
	view.Name = firstNonEmpty(view.Hostname, mac)
	if entry, ok := meta[mac]; ok && entry.Alias != "" {
		alias := entry.Alias
		view.Alias = &alias
		view.Name = alias
	}
	return view
}

// liveClients returns the clients associated with the site's APs now (one
// AP with apID).
func (s *Server) liveClients(state model.State, siteID, apID string, meta map[string]api.ClientMeta) []api.ClientView {
	clients := []api.ClientView{}
	for _, ap := range siteAPs(state, siteID) {
		if apID != "" && ap.ID != apID {
			continue
		}
		obs := s.observe(ap.ID)
		if !obs.fresh(ap.ConfigUpdatedAt) {
			continue
		}
		for _, client := range obs.telemetry.AssociatedClients {
			clients = append(clients, clientView(state, ap, obs, client, meta))
		}
	}
	sort.SliceStable(clients, func(i, j int) bool { return clients[i].RSSI > clients[j].RSSI })
	return clients
}

func sessionView(session model.ClientSession, names map[string]string) api.SessionView {
	view := api.SessionView{SiteID: session.SiteID, APID: session.APID, APName: names[session.APID], MAC: session.MAC,
		SSID: session.SSID, IP: session.IP, VLANID: session.VLANID, ConnectedAt: session.ConnectedAt, LastSeen: session.LastSeen,
		DisconnectedAt: timePtr(session.DisconnectedAt), EndReason: session.EndReason, RSSI: int(session.RSSI),
		DownBytes: session.TxBytes, UpBytes: session.RxBytes}
	return view
}

type sessionStore interface {
	RecentSessions(model.SessionQuery) ([]model.ClientSession, time.Time, error)
}

// siteClients serves GET /api/sites/$id/clients?view=live|recent&ap=&wlan=:
// live, the clients associated now (the bare ClientView array); recent,
// the sessions that ended in the last 24 hours (the bare SessionView
// array).
func (s *Server) siteClients(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	query := request.URL.Query()
	if legacyClient(request) {
		s.legacySiteClients(writer, state, siteID)
		return
	}
	switch query.Get("view") {
	case "", "live":
		clients := s.liveClients(state, siteID, query.Get("ap"), s.clientMeta())
		if wlan := query.Get("wlan"); wlan != "" {
			filtered := clients[:0]
			for _, client := range clients {
				if client.WLANID == wlan || client.SSID == wlan {
					filtered = append(filtered, client)
				}
			}
			clients = filtered
		}
		respond(writer, http.StatusOK, clients)
	case "recent":
		sessions, ok := s.store.(sessionStore)
		if !ok {
			respond(writer, http.StatusOK, []api.SessionView{})
			return
		}
		cutoff := s.now().Add(-24 * time.Hour)
		names := apNames(state)
		result := []api.SessionView{}
		var before time.Time
		for len(result) < 500 {
			batch, next, err := sessions.RecentSessions(model.SessionQuery{SiteID: siteID, APID: query.Get("ap"), Before: before, Limit: 200})
			if err != nil {
				writeAPIError(writer, internalError("session history read failed"))
				return
			}
			stop := false
			for _, session := range batch {
				if session.LastSeen.Before(cutoff) && session.ConnectedAt.Before(cutoff) {
					stop = true
					break
				}
				if session.DisconnectedAt.IsZero() {
					continue
				}
				if wlan := query.Get("wlan"); wlan != "" && session.SSID != wlan {
					continue
				}
				result = append(result, sessionView(session, names))
			}
			if stop || next.IsZero() {
				break
			}
			before = next
		}
		respond(writer, http.StatusOK, result)
	default:
		writeAPIError(writer, invalidField("view", `view must be "live" or "recent"`))
	}
}

// clientDetail serves GET /api/clients/$mac?site=: the live client, its
// sessions and its events. Without site it searches every site's live
// clients, then the session history, and reports where the client was last
// seen (the /clients/$mac stable link).
func (s *Server) clientDetail(writer http.ResponseWriter, request *http.Request, p params) {
	mac := normaliseMAC(p["mac"])
	if mac == "" {
		writeAPIError(writer, invalidField("mac", "invalid MAC address"))
		return
	}
	state := s.store.Snapshot()
	siteID := request.URL.Query().Get("site")
	if siteID != "" && findSite(state, siteID) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	meta := s.clientMeta()
	detail := api.ClientDetail{PerAntennaRSSI: []int{}, QoS: []api.ClientQoS{}, Sessions: []api.SessionView{}, Events: []api.EventView{}}
	for _, ap := range state.APs {
		if ap.SiteID == "" || (siteID != "" && ap.SiteID != siteID) {
			continue
		}
		obs := s.observe(ap.ID)
		if !obs.fresh(ap.ConfigUpdatedAt) {
			continue
		}
		for _, client := range obs.telemetry.AssociatedClients {
			if normaliseMAC(client.MAC) != mac {
				continue
			}
			view := clientView(state, ap, obs, client, meta)
			detail.Live, detail.SiteID = &view, ap.SiteID
			for _, rssi := range client.PerAntennaRSSI {
				detail.PerAntennaRSSI = append(detail.PerAntennaRSSI, int(rssi))
			}
			detail.Username, detail.UserAgent = client.Username, client.UserAgent
			detail.Retries = api.ClientRetries{Tx: client.TxRetries, Rx: client.RxRetries}
			for _, qos := range client.QoS {
				detail.QoS = append(detail.QoS, api.ClientQoS{AC: qos.AC, TxBytes: qos.TxBytes, RxBytes: qos.RxBytes})
			}
		}
	}
	sites := []string{}
	if siteID != "" {
		sites = append(sites, siteID)
	} else {
		for _, site := range state.Sites {
			sites = append(sites, site.ID)
		}
	}
	names := apNames(state)
	if sessions, ok := s.store.(sessionStore); ok {
		var latest time.Time
		for _, site := range sites {
			batch, _, err := sessions.RecentSessions(model.SessionQuery{SiteID: site, MAC: mac, Limit: 50})
			if err != nil {
				continue
			}
			for _, session := range batch {
				detail.Sessions = append(detail.Sessions, sessionView(session, names))
				if session.LastSeen.After(latest) && detail.Live == nil {
					latest, detail.SiteID = session.LastSeen, session.SiteID
				}
			}
		}
		sort.SliceStable(detail.Sessions, func(i, j int) bool { return detail.Sessions[i].ConnectedAt.After(detail.Sessions[j].ConnectedAt) })
		if len(detail.Sessions) > 50 {
			detail.Sessions = detail.Sessions[:50]
		}
	}
	if events, ok := s.store.(eventStore); ok {
		for _, site := range sites {
			batch, _, err := events.Events2().List(model.EventQuery{SiteID: site, Client: mac, Limit: 50})
			if err != nil {
				continue
			}
			for _, event := range batch {
				detail.Events = append(detail.Events, eventView(event, names))
			}
		}
		sort.SliceStable(detail.Events, func(i, j int) bool { return detail.Events[i].LastSeen.After(detail.Events[j].LastSeen) })
	}
	if detail.Live == nil && len(detail.Sessions) == 0 && len(detail.Events) == 0 {
		writeAPIError(writer, notFound("client"))
		return
	}
	if detail.SiteID == "" && len(detail.Events) > 0 {
		detail.SiteID = detail.Events[0].SiteID
	}
	respond(writer, http.StatusOK, detail)
}

// blockClient serves POST /api/sites/$id/clients/$mac/block and /unblock:
// it edits protection.blocked_clients through the normal mutation path
// (a config change with impact, design §3.12).
func (s *Server) blockClient(block bool) handlerFunc {
	return func(writer http.ResponseWriter, request *http.Request, p params) {
		if !s.typedEnabled() {
			writeAPIError(writer, migrationPending([]string{"protection.blocked_clients"}))
			return
		}
		mac := normaliseMAC(p["mac"])
		if mac == "" {
			writeAPIError(writer, invalidField("mac", "invalid MAC address"))
			return
		}
		var input api.ActionInput
		if request.ContentLength > 0 {
			if err := decodeJSON(request, 4096, &input, "block"); err != nil {
				writeError(writer, err)
				return
			}
		}
		siteID := p["site"]
		state := s.store.Snapshot()
		site := findSite(state, siteID)
		if site == nil {
			writeAPIError(writer, notFound("site"))
			return
		}
		blocked := s.siteSettings(s.apContext(state), *site).Protection.BlockedClients
		next := make([]any, 0, len(blocked)+1)
		found := false
		for _, entry := range blocked {
			if entry.MAC == mac {
				found = true
				if !block {
					continue
				}
			}
			next = append(next, map[string]any{"mac": entry.MAC, "note": entry.Note})
		}
		if block && !found {
			next = append(next, map[string]any{"mac": mac, "note": input.Note})
		}
		verb := "Blocked"
		if !block {
			verb = "Unblocked"
		}
		edit, bad := s.parseSitePatch(state, *site, map[string]any{"protection": map[string]any{"blocked_clients": next}})
		if bad != nil {
			writeAPIError(writer, bad)
			return
		}
		out, err := s.run(mutation{
			sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "site.updated", objectID: siteID,
			historySite: siteID, summary: verb + " client " + mac, objects: []api.ChangeObject{{Kind: "site", ID: siteID, Name: site.Name}},
			change: func(state *model.State, _ *model.Tx) error {
				current := findSite(*state, siteID)
				if current == nil {
					return notFound("site")
				}
				return edit.apply(current)
			},
		})
		if err != nil {
			writeChangeError(writer, err)
			return
		}
		action := "block"
		if !block {
			action = "unblock"
		}
		respondMutation(writer, http.StatusOK, out, api.ActionResult{Action: action, State: "done", Via: "config", Revision: out.revision})
	}
}

// disconnectClient serves POST /api/sites/$id/clients/$mac/disconnect,
// gated like reboot (an ap-cmd-rx command, design §5.6).
func (s *Server) disconnectClient(writer http.ResponseWriter, request *http.Request, p params) {
	mac := normaliseMAC(p["mac"])
	if mac == "" {
		writeAPIError(writer, invalidField("mac", "invalid MAC address"))
		return
	}
	state := s.store.Snapshot()
	if findSite(state, p["site"]) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	var target *model.APRecord
	for _, ap := range siteAPs(state, p["site"]) {
		obs := s.observe(ap.ID)
		for _, client := range obs.telemetry.AssociatedClients {
			if normaliseMAC(client.MAC) == mac {
				copied := ap
				target = &copied
			}
		}
	}
	if target == nil {
		writeAPIError(writer, notFound("client").withField("mac", "the client is not connected to an AP of this site"))
		return
	}
	id, err := s.sendCommand(request, *target, APCommand{Cmd: "disconnect", Params: []byte("{}"), Clients: []string{strings.ReplaceAll(mac, "-", ":")}, Reason: "dismist operator"})
	if err != nil {
		writeCommandError(writer, *target, err)
		return
	}
	s.recordAction(request, target.SiteID, "Disconnected client "+mac+" from AP "+firstNonEmpty(target.Name, target.ID))
	respond(writer, http.StatusOK, api.ActionResult{Action: "disconnect", State: "accepted", ActionID: id, Via: "ap-cmd-rx"})
}

// putClientMeta serves PUT /api/clients/$mac/meta {alias, notes} (phase 4).
func (s *Server) putClientMeta(writer http.ResponseWriter, request *http.Request, p params) {
	if s.config.ClientMeta == nil {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "client aliases are not available in this build"))
		return
	}
	mac := normaliseMAC(p["mac"])
	if mac == "" {
		writeAPIError(writer, invalidField("mac", "invalid MAC address"))
		return
	}
	var input api.ClientMetaInput
	if err := decodeJSON(request, 8*1024, &input, "client meta"); err != nil {
		writeError(writer, err)
		return
	}
	input.Alias = strings.TrimSpace(input.Alias)
	if len(input.Alias) > 64 {
		writeAPIError(writer, invalidField("alias", "alias must be at most 64 characters"))
		return
	}
	if len(input.Notes) > 2000 {
		writeAPIError(writer, invalidField("notes", "notes must be at most 2000 characters"))
		return
	}
	meta := api.ClientMeta{MAC: mac, Alias: input.Alias, Notes: input.Notes}
	if err := s.config.ClientMeta.Put(meta); err != nil {
		log.Printf("client meta %s: %v", mac, err)
		writeAPIError(writer, internalError("cannot save the client's alias"))
		return
	}
	respond(writer, http.StatusOK, meta)
}

var errNoClient = errors.New("no client")
