package httpapi

import (
	"net/http"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
)

// search serves GET /api/search?q= (phase 4 global search): sites, APs
// (name, ID, LAN IP, public address), WLANs (SSID) and live clients (name,
// alias, MAC, IP).
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
	for _, site := range state.Sites {
		if match(site.Name, site.ID) {
			result.Hits = append(result.Hits, api.SearchHit{Kind: "site", ID: site.ID, SiteID: site.ID, Title: site.Name, Detail: site.Country})
		}
		for _, network := range compile.NetworksForSite(state, site.ID) {
			if match(network.SSID, network.ID) {
				result.Hits = append(result.Hits, api.SearchHit{Kind: "wlan", ID: network.ID, SiteID: site.ID, Title: network.SSID, Detail: site.Name})
			}
		}
		for _, client := range s.liveClients(state, site.ID, "", s.clientMeta()) {
			alias := ""
			if client.Alias != nil {
				alias = *client.Alias
			}
			compact := strings.ReplaceAll(client.MAC, "-", "")
			if match(client.Name, alias, client.MAC, compact, client.IP, client.Hostname) {
				result.Hits = append(result.Hits, api.SearchHit{Kind: "client", ID: client.MAC, SiteID: site.ID, Title: client.Name, Detail: client.IP + " · " + client.APName})
			}
		}
	}
	for _, ap := range state.APs {
		view := s.apView(ctx, ap)
		if match(view.Name, view.ID, strings.ReplaceAll(view.ID, "-", ""), view.LANIP, view.RemoteAddress) {
			result.Hits = append(result.Hits, api.SearchHit{Kind: "ap", ID: ap.ID, SiteID: ap.SiteID, Title: firstNonEmpty(ap.Name, ap.ID), Detail: view.LANIP})
		}
	}
	order := map[string]int{"site": 0, "ap": 1, "wlan": 2, "client": 3}
	sort.SliceStable(result.Hits, func(i, j int) bool { return order[result.Hits[i].Kind] < order[result.Hits[j].Kind] })
	if len(result.Hits) > 50 {
		result.Hits = result.Hits[:50]
	}
	respond(writer, http.StatusOK, result)
}
