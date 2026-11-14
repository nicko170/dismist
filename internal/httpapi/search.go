package httpapi

import (
	"net/http"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

// search serves GET /api/search?q= (phase 4 global search): sites, APs
// (name, ID, serial, LAN IP, public address), WLANs (SSID), live clients
// (name, alias, MAC, IP), clients of the last 24 hours' sessions (MAC, IP,
// alias) and offline clients by their alias.
func (s *Server) search(writer http.ResponseWriter, request *http.Request, _ params) {
	query := lowerNoSpace(request.URL.Query().Get("q"))
	result := api.SearchResult{Query: query, Hits: []api.SearchHit{}}
	if len(query) < 2 {
		respond(writer, http.StatusOK, result)
		return
	}
	match := func(values ...string) bool {
		for _, value := range values {
			if value != "" && strings.Contains(strings.ToLower(value), query) {
				return true
			}
		}
		return false
	}
	state := s.store.Snapshot()
	ctx := s.apContext(state)
	meta := s.clientMeta()
	names := apNames(state)
	clientsSeen := map[string]bool{}
	for _, site := range state.Sites {
		if match(site.Name, site.ID) {
			result.Hits = append(result.Hits, api.SearchHit{Kind: "site", ID: site.ID, SiteID: site.ID, Title: site.Name, Detail: site.Country})
		}
		for _, network := range compile.NetworksForSite(state, site.ID) {
			if match(network.SSID, network.ID) {
				result.Hits = append(result.Hits, api.SearchHit{Kind: "wlan", ID: network.ID, SiteID: site.ID, Title: network.SSID, Detail: site.Name})
			}
		}
		for _, client := range s.liveClients(state, site.ID, "", meta) {
			clientsSeen[client.MAC] = true
			alias := ""
			if client.Alias != nil {
				alias = *client.Alias
			}
			compact := strings.ReplaceAll(client.MAC, "-", "")
			if match(client.Name, alias, client.MAC, compact, client.IP, client.Hostname) {
				result.Hits = append(result.Hits, api.SearchHit{Kind: "client", ID: client.MAC, SiteID: site.ID, Title: client.Name, Detail: client.IP + " · " + client.APName})
			}
		}
		// Offline clients: the site's sessions of the last 24 hours
		// (bounded), newest first.
		if sessions, ok := s.store.(sessionStore); ok {
			batch, _, err := sessions.RecentSessions(model.SessionQuery{SiteID: site.ID, Limit: 200})
			if err == nil {
				for _, session := range batch {
					mac := normaliseMAC(session.MAC)
					if mac == "" || clientsSeen[mac] {
						continue
					}
					clientsSeen[mac] = true
					alias := meta[mac].Alias
					if match(alias, mac, strings.ReplaceAll(mac, "-", ""), session.IP) {
						result.Hits = append(result.Hits, api.SearchHit{Kind: "client", ID: mac, SiteID: site.ID, Title: firstNonEmpty(alias, mac),
							Detail: "offline · last seen on " + firstNonEmpty(names[session.APID], session.APID)})
					}
				}
			}
		}
	}
	// Offline clients the operator named, seen longer ago.
	for mac, entry := range meta {
		if clientsSeen[mac] || entry.Alias == "" || !match(entry.Alias) {
			continue
		}
		clientsSeen[mac] = true
		result.Hits = append(result.Hits, api.SearchHit{Kind: "client", ID: mac, Title: entry.Alias, Detail: "offline"})
	}
	for _, ap := range state.APs {
		view := s.apView(ctx, ap)
		serial := ""
		if known, ok := s.lastKnown(ap.ID); ok && known.Hello != nil {
			serial = known.Hello.SerialNo
		}
		if hello := s.observe(ap.ID).status.Hello; hello.SerialNo != "" {
			serial = hello.SerialNo
		}
		if match(view.Name, view.ID, strings.ReplaceAll(view.ID, "-", ""), view.LANIP, view.RemoteAddress, serial) {
			result.Hits = append(result.Hits, api.SearchHit{Kind: "ap", ID: ap.ID, SiteID: ap.SiteID, Title: firstNonEmpty(ap.Name, ap.ID), Detail: firstNonEmpty(view.LANIP, serial)})
		}
	}
	order := map[string]int{"site": 0, "ap": 1, "wlan": 2, "client": 3}
	sort.SliceStable(result.Hits, func(i, j int) bool { return order[result.Hits[i].Kind] < order[result.Hits[j].Kind] })
	if len(result.Hits) > 50 {
		result.Hits = result.Hits[:50]
	}
	respond(writer, http.StatusOK, result)
}
