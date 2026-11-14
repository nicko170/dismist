package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

var (
	wlanLegacyKeys = map[string]bool{"ssid": true, "psk": true, "enabled": true, "vlan_id": true}
	wlanTypedKeys  = map[string]bool{
		"security": true, "pmf": true, "radius": true, "bands": true, "hidden": true, "isolation": true,
		"band_steering": true, "rates": true, "min_rssi": true, "dtim": true, "idle_timeout_sec": true,
		"max_clients": true, "roaming": true, "broadcast": true, "dhcp_only": true, "dhcp_dns_only": true,
		"wmm_disabled": true, "uapsd_disabled": true, "disable_11ax": true, "disable_11nac": true,
		"group_rekey_sec": true, "eapol_v1": true, "sae_h2e_only": true, "transition_disable": true,
		"owe_ssid": true, "rate_limit": true, "qos_class": true, "honour_blocklist": true, "option82": true,
		"schedule": true, "mpsk": true,
	}
	wlanReadOnly = map[string]bool{"id": true, "site_id": true, "has_psk": true, "bssid_index": true,
		"owe_bssid_index": true, "expert": true, "experimental_in_use": true, "live": true, "updated_rev": true}
	personalModes   = map[string]bool{"wpa2_personal": true, "wpa2_wpa3_personal": true, "wpa3_personal": true}
	enterpriseModes = map[string]bool{"wpa2_enterprise": true, "wpa2_wpa3_enterprise": true, "wpa3_enterprise": true}
	securityModes   = map[string]bool{"wpa2_personal": true, "wpa2_wpa3_personal": true, "wpa3_personal": true, "open": true,
		"owe": true, "owe_transition": true, "wpa2_enterprise": true, "wpa2_wpa3_enterprise": true, "wpa3_enterprise": true}
)

// wlanRecord is the typed part of a network record that the views read
// directly (design §4.3 keys).
type wlanRecord struct {
	Security string   `json:"security"`
	Bands    []string `json:"bands"`
	RADIUS   *struct {
		Auth []storedServer `json:"auth"`
		Acct []storedServer `json:"acct"`
		CoA  *struct {
			Enabled                bool           `json:"enabled"`
			Servers                []storedServer `json:"servers"`
			EventTimestampOptional bool           `json:"event_timestamp_optional"`
		} `json:"coa"`
	} `json:"radius"`
	MPSK *struct {
		Enabled bool `json:"enabled"`
		Private bool `json:"private"`
		Keys    []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Passphrase string `json:"passphrase"`
			VLANID     int    `json:"vlan_id"`
			MAC        string `json:"mac"`
			Expires    string `json:"expires"`
		} `json:"keys"`
	} `json:"mpsk"`
	BSSIDIndex int `json:"bssid_index"`
	OWEIndex   int `json:"owe_bssid_index"`
}

type storedServer struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

func readWLANRecord(network model.Network) wlanRecord {
	var record wlanRecord
	viewFrom(network, &record)
	return record
}

// liftedWLAN is what the compiled config shows for a network (display in
// legacy mode, before the migration lifts it into typed fields).
type liftedWLAN struct {
	security     string
	pmf          *string
	bands        []string
	hidden       bool
	bandSteering bool
	idleTimeout  int
	dtim         int
}

// liftWLANs reads each network's security and basics from the site's
// compiled config (any AP of the site, or a synthetic one).
func (s *Server) liftWLANs(state model.State, siteID string) map[string]liftedWLAN {
	result := map[string]liftedWLAN{}
	site := findSite(state, siteID)
	if site == nil && len(state.Sites) > 0 {
		return result
	}
	input := compile.Input{Template: s.base, Networks: compile.NetworksForSite(state, siteID), AP: model.APRecord{ID: "00-00-00-00-00-00", SiteID: siteID}, Time: state.UpdatedAt}
	if site != nil {
		input.Site = *site
	}
	output, err := compile.AP(input)
	if err != nil {
		return result
	}
	root, err := decodeObject(output.Config)
	if err != nil {
		return result
	}
	wlans, _ := getFold(root, "WLANs").([]any)
	for _, item := range wlans {
		wlan, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := getFold(wlan, "ID").(string)
		lifted := liftedWLAN{security: liftSecurity(wlan), bands: []string{}}
		if bands, ok := getFold(wlan, "Bands").([]any); ok {
			for _, band := range bands {
				if text, ok := band.(string); ok {
					lifted.bands = append(lifted.bands, text)
				}
			}
		}
		lifted.hidden, _ = getFold(wlan, "Hidden").(bool)
		if configs, ok := getFold(wlan, "BandConfigs").([]any); ok {
			for _, config := range configs {
				if entry, ok := config.(map[string]any); ok && getFold(entry, "Band") == "2.4" && getFold(entry, "Steer") != nil {
					lifted.bandSteering = true
				}
			}
		}
		if inactivity, ok := getFold(wlan, "WCInactivity").(map[string]any); ok {
			lifted.idleTimeout, _ = asInt(getFold(inactivity, "TimeoutSec"))
		}
		if dtim, ok := getFold(wlan, "DTIM").(map[string]any); ok {
			lifted.dtim, _ = asInt(getFold(dtim, "Period"))
		}
		if lifted.security == "wpa2_personal" || lifted.security == "wpa2_enterprise" {
			if dot11w, ok := getFold(wlan, "Dot11w").(map[string]any); ok {
				enabled, _ := getFold(dot11w, "Enabled").(bool)
				required, _ := getFold(dot11w, "Required").(bool)
				pmf := "disabled"
				switch {
				case required:
					pmf = "required"
				case enabled:
					pmf = "optional"
				}
				lifted.pmf = &pmf
			}
		}
		result[id] = lifted
	}
	return result
}

// getFold reads a key case-insensitively; the last variant in sorted key
// order wins, as the AP's decoder sees a Go-marshalled document.
func getFold(object map[string]any, key string) any {
	var keys []string
	for candidate := range object {
		if strings.EqualFold(candidate, key) {
			keys = append(keys, candidate)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return object[keys[len(keys)-1]]
}

// liftSecurity maps (Auth.Type, AKM, OWE.Transition) to the composite
// security value (design §3.9 table).
func liftSecurity(wlan map[string]any) string {
	auth, _ := getFold(wlan, "Auth").(map[string]any)
	if auth == nil {
		return ""
	}
	authType, _ := getFold(auth, "Type").(string)
	akm, _ := getFold(auth, "AKM").(map[string]any)
	flag := func(name string) bool {
		on, _ := getFold(akm, name).(bool)
		return akm != nil && on
	}
	switch strings.ToLower(authType) {
	case "psk":
		switch {
		case flag("WPA3_PSK") && flag("WPA2_PSK"):
			return "wpa2_wpa3_personal"
		case flag("WPA3_PSK"):
			return "wpa3_personal"
		}
		return "wpa2_personal"
	case "open":
		if owe, ok := getFold(wlan, "OWE").(map[string]any); ok {
			if transition, _ := getFold(owe, "Transition").(bool); transition {
				return "owe_transition"
			}
		}
		return "open"
	case "owe":
		return "owe"
	case "eap":
		switch {
		case flag("WPA3_EAP") && flag("WPA2_EAP"):
			return "wpa2_wpa3_enterprise"
		case flag("WPA3_EAP"):
			return "wpa3_enterprise"
		}
		return "wpa2_enterprise"
	}
	return ""
}

// wlanView builds one WLANView (design §4.9).
func (s *Server) wlanView(ctx apContext, network model.Network, lifted map[string]liftedWLAN) api.WLANView {
	view := api.WLANView{
		Bands: []api.Band{}, MinRSSI: map[string]int{}, ExperimentalInUse: []string{}, Isolation: "none", Rates: "compatible",
		Roaming: api.RoamingView{Fast: "none", NeighborReport: true, BSSTransition: true, BSSLoad: true},
	}
	viewFrom(network, &view)
	record := readWLANRecord(network)
	view.ID, view.SiteID, view.SSID, view.Enabled, view.VLANID = network.ID, network.SiteID, network.SSID, network.Enabled, network.VLANID
	view.HasPSK = network.PSK != ""
	view.UpdatedRev = network.UpdatedRev
	view.RADIUS = nil
	if record.RADIUS != nil {
		radius := &api.RADIUSView{Auth: []api.RADIUSServerView{}, Acct: []api.RADIUSServerView{}}
		viewFrom(lookupRaw(network, "radius"), radius)
		radius.Auth, radius.Acct, radius.CoA = []api.RADIUSServerView{}, []api.RADIUSServerView{}, nil
		for _, server := range record.RADIUS.Auth {
			radius.Auth = append(radius.Auth, api.RADIUSServerView{Host: server.Host, Port: server.Port, HasSecret: server.Secret != ""})
		}
		for _, server := range record.RADIUS.Acct {
			radius.Acct = append(radius.Acct, api.RADIUSServerView{Host: server.Host, Port: server.Port, HasSecret: server.Secret != ""})
		}
		if coa := record.RADIUS.CoA; coa != nil {
			radius.CoA = &api.CoAView{Enabled: coa.Enabled, Servers: []api.CoAServerView{}, EventTimestampOptional: coa.EventTimestampOptional}
			for _, server := range coa.Servers {
				radius.CoA.Servers = append(radius.CoA.Servers, api.CoAServerView{Host: server.Host, Port: server.Port, HasSecret: server.Secret != ""})
			}
		}
		view.RADIUS = radius
	}
	view.MPSK = nil
	if record.MPSK != nil {
		mpsk := &api.MPSKView{Enabled: record.MPSK.Enabled, Private: record.MPSK.Private, Keys: []api.MPSKKeyView{}}
		for _, key := range record.MPSK.Keys {
			mpsk.Keys = append(mpsk.Keys, api.MPSKKeyView{ID: key.ID, Name: key.Name, HasPassphrase: key.Passphrase != "", VLANID: key.VLANID, MAC: key.MAC, Expires: key.Expires})
		}
		view.MPSK = mpsk
	}
	if record.Security == "" {
		if lift, ok := lifted[network.ID]; ok {
			view.Security = api.Security(lift.security)
			view.PMF = lift.pmf
			view.Hidden = lift.hidden
			view.BandSteering = lift.bandSteering
			view.IdleTimeoutSec = lift.idleTimeout
			view.DTIM = lift.dtim
			if len(record.Bands) == 0 {
				for _, band := range lift.bands {
					view.Bands = append(view.Bands, api.Band(band))
				}
			}
		}
	}
	if view.Security == "" {
		view.Security = api.SecurityWPA2Personal
	}
	if view.Bands == nil {
		view.Bands = []api.Band{}
	}
	if view.MinRSSI == nil {
		view.MinRSSI = map[string]int{}
	}
	view.Expert = s.expertSummary(ctx.state, api.ExpertScopeWLAN, network.ID, network)
	view.ExperimentalInUse = s.experimentalInUse("wlan", network, mapKeys(wlanTypedKeys))
	view.Live, view.LiveVAPs = s.wlanLive(ctx, network)
	view.MDID = compile.MDID(network.ID)
	return view
}

func lookupRaw(record any, key string) map[string]any {
	object, err := recordMap(record)
	if err != nil {
		return nil
	}
	value, _ := object[key].(map[string]any)
	return value
}

func mapKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// wlanLive counts the network's clients and the APs broadcasting it now.
func (s *Server) wlanLive(ctx apContext, network model.Network) (api.WLANLiveSummary, []api.WLANLiveVAP) {
	var live api.WLANLiveSummary
	vaps := []api.WLANLiveVAP{}
	for _, ap := range ctx.state.APs {
		if ap.SiteID != network.SiteID || network.SiteID == "" {
			continue
		}
		live.APsTotal++
		obs := s.observe(ap.ID)
		if !obs.fresh(ap.ConfigUpdatedAt) {
			continue
		}
		broadcasting := false
		for _, vap := range obs.telemetry.VAPs {
			if vap.WLANID == network.ID || (vap.WLANID == "" && vap.SSID == network.SSID) {
				broadcasting = true
				live.Clients += vap.Clients
				vaps = append(vaps, api.WLANLiveVAP{APID: ap.ID, APName: firstNonEmpty(ap.Name, ap.ID), Band: api.Band(vap.Band),
					BSSID: strings.ToLower(vap.BSSID), Dev: vap.Interface, Clients: vap.Clients})
			}
		}
		if broadcasting {
			live.APsBroadcasting++
		}
	}
	return live, vaps
}

// listNetworks serves GET /api/sites/$id/networks: the bare WLANView array
// (the legacy app gets {revision, networks}).
func (s *Server) listNetworks(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil && (len(state.Sites) > 0 || siteID != "") {
		writeAPIError(writer, notFound("site"))
		return
	}
	if request != nil && legacyClient(request) {
		s.legacyNetworkList(writer, state, siteID)
		return
	}
	respond(writer, http.StatusOK, s.wlanViews(state, siteID))
}

func (s *Server) wlanViews(state model.State, siteID string) []api.WLANView {
	ctx := s.apContext(state)
	lifted := s.liftWLANs(state, siteID)
	views := []api.WLANView{}
	for _, network := range compile.NetworksForSite(state, siteID) {
		views = append(views, s.wlanView(ctx, network, lifted))
	}
	return views
}

// getNetwork serves GET /api/sites/$id/networks/$nid.
func (s *Server) getNetwork(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	network := findNetwork(state, p["site"], p["nid"])
	if network == nil {
		writeAPIError(writer, notFound("network"))
		return
	}
	respond(writer, http.StatusOK, s.wlanView(s.apContext(state), *network, s.liftWLANs(state, network.SiteID)))
}

// wlanPresets are the "New WLAN" presets (design §3.8).
var wlanPresets = map[string]map[string]any{
	"home":  {"security": "wpa2_wpa3_personal", "bands": []any{"2.4", "5"}},
	"guest": {"security": "wpa2_wpa3_personal", "bands": []any{"2.4", "5"}, "isolation": "clients_and_lan"},
	"iot":   {"security": "wpa2_personal", "bands": []any{"2.4"}, "band_steering": false},
}

// newWLANDefaults are the design's defaults for WLANs created after schema
// 2 (§3.9, §4.6), used where the registry declares none.
var newWLANDefaults = map[string]any{
	"security": "wpa2_wpa3_personal", "bands": []any{"2.4", "5"}, "isolation": "none", "band_steering": false,
	"rates": "compatible", "dtim": 2, "idle_timeout_sec": 1800, "group_rekey_sec": 86400, "honour_blocklist": true,
	"roaming":                    map[string]any{"fast": "none", "neighbor_report": true, "bss_transition": true, "ap_steering": false, "bss_load": true},
	"broadcast":                  map[string]any{"limit": false, "allow_ipv6_nd": true},
	"ucast_disassoc_on_bss_down": true, "reclaim_timeout_sec": 300,
}

// createNetwork serves POST /api/sites/$id/networks.
func (s *Server) createNetwork(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	patch, err := readPatch(request, 64*1024, "network")
	if err != nil {
		writeError(writer, err)
		return
	}
	preset, _ := patch["preset"].(string)
	delete(patch, "preset")
	if preset != "" && wlanPresets[preset] == nil {
		writeAPIError(writer, invalidField("preset", `preset must be "home", "guest" or "iot"`))
		return
	}
	typedOn := s.typedEnabled()
	if bad := checkKeys(patch, wlanTypedKeys, wlanLegacyKeys, wlanReadOnly, typedOn); bad != nil {
		writeAPIError(writer, bad)
		return
	}
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil && (len(state.Sites) > 0 || siteID != "") {
		writeAPIError(writer, notFound("site"))
		return
	}
	if !typedOn {
		missing := invalid("SSID, passphrase and VLAN are required")
		for _, key := range []string{"ssid", "psk", "vlan_id"} {
			if _, ok := patch[key]; !ok {
				missing.withField(key, "required")
			}
		}
		if len(missing.fields) > 0 {
			writeAPIError(writer, missing)
			return
		}
	}
	full := map[string]any{}
	if typedOn {
		mergePatch(full, cloneJSON(newWLANDefaults))
		for _, option := range s.registry().ByScope("wlan") {
			if option.Default != nil {
				setPath(full, strings.Split(strings.TrimPrefix(option.Key, "wlan."), "."), option.Default)
			}
		}
		if preset != "" {
			mergePatch(full, cloneJSON(wlanPresets[preset]))
		}
	}
	mergePatch(full, patch)
	if _, ok := full["enabled"]; !ok {
		full["enabled"] = true
	}
	if _, ok := full["vlan_id"]; !ok {
		full["vlan_id"] = 1
	}
	edit, bad := s.parseWLANPatch(state, siteID, nil, full, true)
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	if typedOn {
		// Expert-tier defaults have no form control but are stored for new
		// WLANs (design §3.9 "Defaults that have no control", §4.6).
		for _, key := range []string{"ucast_disassoc_on_bss_down", "reclaim_timeout_sec"} {
			if value, ok := full[key]; ok {
				edit.typed[key] = value
			}
		}
	}
	id, err := randomID()
	if err != nil {
		writeAPIError(writer, internalError("cannot create network"))
		return
	}
	ssid := edit.ssidOr("")
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "wlan.created", objectID: id,
		historySite: siteID, summary: "Created WLAN " + ssid, objects: []api.ChangeObject{{Kind: "network", ID: id, Name: ssid}},
		change: func(state *model.State, _ *model.Tx) error {
			network := model.Network{ID: id, SiteID: siteID}
			if typedOn {
				index, owe := freeBSSIDIndexes(*state, siteID, edit.security() == "owe_transition")
				if index == 0 {
					return newError(http.StatusUnprocessableEntity, api.ErrorCodeFirmwareRule, "no free BSSID index: a site carries at most 15 WLANs per band")
				}
				edit.typed["bssid_index"] = index
				if owe != 0 {
					edit.typed["owe_bssid_index"] = owe
				}
			}
			if err := edit.apply(&network); err != nil {
				return err
			}
			state.Networks = append(state.Networks, network)
			return s.checkSiteWLANs(*state, siteID)
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if legacyClient(request) {
		s.legacyNetworkList(writer, s.store.Snapshot(), siteID)
		return
	}
	network := findNetwork(out.state, siteID, id)
	if network == nil {
		writeAPIError(writer, internalError("the network was not stored"))
		return
	}
	respondMutation(writer, http.StatusCreated, out, s.wlanView(s.apContext(out.state), *network, s.liftWLANs(out.state, siteID)))
}

// freeBSSIDIndexes returns the lowest free BSSID index from 1 to 15 in the
// site (and the next for an OWE transition companion); 0 when none is
// free (design §4.4 "BSSID index").
func freeBSSIDIndexes(state model.State, siteID string, owe bool) (int, int) {
	used := map[int]bool{}
	for _, network := range state.Networks {
		if network.SiteID != siteID {
			continue
		}
		record := readWLANRecord(network)
		used[record.BSSIDIndex] = true
		used[record.OWEIndex] = true
	}
	var found []int
	for index := 1; index <= 15 && len(found) < 2; index++ {
		if !used[index] {
			found = append(found, index)
			if !owe {
				break
			}
		}
	}
	switch {
	case len(found) == 0 || (owe && len(found) < 2):
		return 0, 0
	case owe:
		return found[0], found[1]
	}
	return found[0], 0
}

// checkSiteWLANs enforces the per-site WLAN rules after a change: no exact
// SSID duplicate among enabled WLANs on overlapping bands, and at most 8
// WLANs per band (an OWE transition counts 2).
func (s *Server) checkSiteWLANs(state model.State, siteID string) error {
	type entry struct {
		ssid  string
		bands []string
		owe   bool
	}
	var entries []entry
	for _, network := range compile.NetworksForSite(state, siteID) {
		if !network.Enabled {
			continue
		}
		record := readWLANRecord(network)
		bands := record.Bands
		if len(bands) == 0 {
			bands = []string{"2.4", "5"}
		}
		entries = append(entries, entry{ssid: network.SSID, bands: bands, owe: record.Security == "owe_transition"})
	}
	perBand := map[string]int{}
	for index, a := range entries {
		for _, band := range a.bands {
			perBand[band]++
			if a.owe {
				perBand[band]++
			}
		}
		for _, b := range entries[index+1:] {
			if a.ssid != b.ssid {
				continue
			}
			for _, band := range a.bands {
				if containsString(b.bands, band) {
					return newError(http.StatusUnprocessableEntity, api.ErrorCodeInvalid,
						fmt.Sprintf("another enabled WLAN already broadcasts %q on %s GHz", a.ssid, band)).withField("ssid", "duplicate SSID")
				}
			}
		}
	}
	for band, count := range perBand {
		if count > 8 {
			return newError(http.StatusUnprocessableEntity, api.ErrorCodeFirmwareRule,
				fmt.Sprintf("at most 8 WLANs per band: %s GHz would carry %d", band, count)).withField("enabled", "too many WLANs on "+band+" GHz")
		}
	}
	return nil
}

// patchNetwork serves PATCH /api/sites/$id/networks/$nid (and the legacy
// PUT alias).
func (s *Server) patchNetwork(writer http.ResponseWriter, request *http.Request, p params) {
	s.updateNetwork(writer, request, p["site"], p["nid"])
}

func (s *Server) updateNetwork(writer http.ResponseWriter, request *http.Request, siteID, id string) {
	patch, err := readPatch(request, 64*1024, "network")
	if err != nil {
		writeError(writer, err)
		return
	}
	if bad := checkKeys(patch, wlanTypedKeys, wlanLegacyKeys, wlanReadOnly, s.typedEnabled()); bad != nil {
		writeAPIError(writer, bad)
		return
	}
	state := s.store.Snapshot()
	current := findNetwork(state, siteID, id)
	if current == nil {
		writeAPIError(writer, notFound("network"))
		return
	}
	if bad := ifMatch(request, current.UpdatedRev, false); bad != nil {
		staleWith(writer, bad, s.wlanView(s.apContext(state), *current, s.liftWLANs(state, siteID)))
		return
	}
	edit, bad := s.parseWLANPatch(state, siteID, current, patch, false)
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "wlan.updated", objectID: id,
		historySite: siteID, summary: "WLAN " + current.SSID + ": " + edit.describe(),
		objects: []api.ChangeObject{{Kind: "network", ID: id, Name: current.SSID}},
		change: func(state *model.State, _ *model.Tx) error {
			network := findNetwork(*state, siteID, id)
			if network == nil {
				return notFound("network")
			}
			if err := edit.apply(network); err != nil {
				return err
			}
			return s.checkSiteWLANs(*state, siteID)
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if legacyClient(request) {
		s.legacyNetworkList(writer, s.store.Snapshot(), siteID)
		return
	}
	network := findNetwork(out.state, siteID, id)
	if network == nil {
		writeAPIError(writer, notFound("network"))
		return
	}
	respondMutation(writer, http.StatusOK, out, s.wlanView(s.apContext(out.state), *network, s.liftWLANs(out.state, siteID)))
}

// deleteNetwork serves DELETE /api/sites/$id/networks/$nid.
func (s *Server) deleteNetwork(writer http.ResponseWriter, request *http.Request, p params) {
	siteID, id := p["site"], p["nid"]
	state := s.store.Snapshot()
	current := findNetwork(state, siteID, id)
	if current == nil {
		writeAPIError(writer, notFound("network"))
		return
	}
	if bad := ifMatch(request, current.UpdatedRev, false); bad != nil {
		staleWith(writer, bad, s.wlanView(s.apContext(state), *current, s.liftWLANs(state, siteID)))
		return
	}
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "wlan.deleted", objectID: id,
		historySite: siteID, summary: "Deleted WLAN " + current.SSID,
		objects: []api.ChangeObject{{Kind: "network", ID: id, Name: current.SSID}},
		change: func(state *model.State, _ *model.Tx) error {
			legacy := len(state.Sites) == 0
			for index, network := range state.Networks {
				if network.ID == id && (network.SiteID == siteID || (legacy && network.SiteID == "")) {
					state.Networks = append(state.Networks[:index], state.Networks[index+1:]...)
					return nil
				}
			}
			return notFound("network")
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if legacyClient(request) {
		s.legacyNetworkList(writer, s.store.Snapshot(), siteID)
		return
	}
	respondMutation(writer, http.StatusOK, out, api.WLANDeleted{ID: id})
}

// wlanEdit is a validated WLAN patch.
type wlanEdit struct {
	ssid    *string
	psk     *string
	enabled *bool
	vlanID  *int
	typed   map[string]any
	keys    []string
	current *model.Network
}

func (e wlanEdit) ssidOr(fallback string) string {
	if e.ssid != nil {
		return *e.ssid
	}
	return fallback
}

func (e wlanEdit) security() string {
	if value, ok := e.typed["security"].(string); ok {
		return value
	}
	if e.current != nil {
		return readWLANRecord(*e.current).Security
	}
	return ""
}

func (e wlanEdit) describe() string {
	var parts []string
	for _, key := range e.keys {
		switch key {
		case "psk":
			parts = append(parts, "passphrase changed")
		case "enabled":
			if e.enabled != nil && *e.enabled {
				parts = append(parts, "enabled")
			} else {
				parts = append(parts, "disabled")
			}
		case "ssid":
			parts = append(parts, "renamed to "+e.ssidOr(""))
		default:
			parts = append(parts, key+" changed")
		}
	}
	if len(parts) == 0 {
		return "updated"
	}
	return strings.Join(parts, ", ")
}

func (e wlanEdit) apply(network *model.Network) error {
	if e.ssid != nil {
		network.SSID = *e.ssid
	}
	if e.psk != nil {
		network.PSK = *e.psk
	}
	if e.enabled != nil {
		network.Enabled = *e.enabled
	}
	if e.vlanID != nil {
		network.VLANID = *e.vlanID
	}
	if len(e.typed) > 0 {
		if err := patchRecord(network, e.typed); err != nil {
			var unsupported *errUnsupported
			if errors.As(err, &unsupported) {
				return unsupportedField(unsupported.path)
			}
			return err
		}
	}
	return nil
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// parseWLANPatch validates a WLAN patch (design §3.9 rules). current is
// nil for a create, where patch is the full new object.
func (s *Server) parseWLANPatch(state model.State, siteID string, current *model.Network, patch map[string]any, create bool) (wlanEdit, *apiError) {
	edit := wlanEdit{current: current, typed: map[string]any{}}
	var errs fieldErrors
	typedOn := s.typedEnabled()
	var stored wlanRecord
	if current != nil {
		stored = readWLANRecord(*current)
	}
	security := stored.Security
	if value, ok := patch["security"].(string); ok {
		security = value
	}
	if security == "" && !typedOn {
		security = "wpa2_wpa3_personal"
	}
	for key := range patch {
		edit.keys = append(edit.keys, key)
	}
	sort.Strings(edit.keys)
	if value, ok := patch["ssid"]; ok {
		text, isString := value.(string)
		if !isString {
			errs.add("ssid", "must be a JSON string")
		} else if message := ssidError(text); message != "" {
			errs.add("ssid", message)
		}
		edit.ssid = &text
	}
	if value, ok := patch["enabled"]; ok {
		on, isBool := value.(bool)
		if !isBool {
			errs.add("enabled", "must be true or false")
		}
		edit.enabled = &on
	}
	if value, ok := patch["vlan_id"]; ok {
		vlan, isInt := asInt(value)
		if !isInt || vlan < 1 || vlan > 4094 {
			errs.add("vlan_id", "VLAN must be 1 (untagged) or 2 to 4094")
		}
		edit.vlanID = &vlan
	}
	if value, ok := patch["psk"]; ok {
		text, isString := value.(string)
		switch {
		case !isString:
			errs.add("psk", "must be a JSON string")
		case typedOn && !personalModes[security]:
			if text != "" {
				errs.add("psk", "this security mode has no passphrase")
			}
		case !typedOn:
			if len(text) < 8 || len(text) > 63 {
				errs.add("psk", "passphrase must be 8 to 63 characters")
			} else if message := passphraseError(text, false); message != "" {
				errs.add("psk", message)
			}
		default:
			if message := passphraseError(text, security == "wpa2_personal"); message != "" {
				errs.add("psk", message)
			}
		}
		edit.psk = &text
	}
	if typedOn && personalModes[security] {
		hasPSK := current != nil && current.PSK != ""
		if edit.psk != nil {
			hasPSK = *edit.psk != ""
		}
		if !hasPSK {
			errs.add("psk", "a passphrase is required for "+security)
		}
		if edit.psk == nil && current != nil && current.PSK != "" && security != stored.Security {
			if message := passphraseError(current.PSK, security == "wpa2_personal"); message != "" {
				errs.add("psk", "the saved passphrase does not suit "+security+": "+message)
			}
		}
	}
	typed := map[string]any{}
	for key := range wlanTypedKeys {
		if value, ok := patch[key]; ok {
			typed[key] = value
		}
	}
	if typedOn {
		s.validateWLANTyped(&errs, state, siteID, current, stored, security, typed, patch)
	}
	if bad := errs.err("network"); bad != nil {
		return edit, bad
	}
	if radius, ok := typed["radius"]; ok && radius != nil {
		merged, bad := mergeRADIUSSecrets(radius, stored)
		if bad != nil {
			return edit, bad
		}
		typed["radius"] = merged
	}
	if mpsk, ok := typed["mpsk"]; ok && mpsk != nil {
		merged, bad := mergeMPSKSecrets(mpsk, current, stored, edit.psk)
		if bad != nil {
			return edit, bad
		}
		typed["mpsk"] = merged
	}
	changed := optionValues("wlan", typed)
	if bad := registryCheck(s.registry(), "wlan", changed); bad != nil {
		return edit, bad
	}
	site := findSite(state, siteID)
	experimental := site != nil && siteExperimental(*site)
	if keys := experimentalKeys(s.registry(), changed); len(keys) > 0 && !experimental {
		return edit, experimentalDisabled(keys)
	}
	if len(typed) > 0 {
		edit.typed = typed
	}
	_ = create
	return edit, nil
}

func (s *Server) validateWLANTyped(errs *fieldErrors, state model.State, siteID string, current *model.Network, stored wlanRecord, security string, typed, patch map[string]any) {
	if value, ok := typed["security"]; ok {
		text, _ := value.(string)
		if !securityModes[text] {
			errs.add("security", "unknown security mode")
		}
	}
	bands := stored.Bands
	if value, ok := typed["bands"]; ok {
		var list []string
		if err := remarshal(value, &list); err != nil || len(list) == 0 {
			errs.add("bands", "choose at least one band")
		}
		seen := map[string]bool{}
		for _, band := range list {
			if band != "2.4" && band != "5" {
				errs.add("bands", `bands are "2.4" and "5"`)
			}
			if seen[band] {
				errs.add("bands", "each band once")
			}
			seen[band] = true
		}
		bands = list
	}
	if len(bands) == 0 {
		bands = []string{"2.4", "5"}
	}
	enumField := func(key string, allowed ...string) {
		if value, ok := typed[key]; ok && value != nil {
			text, _ := value.(string)
			if !containsString(allowed, text) {
				errs.add(key, fmt.Sprintf("must be one of %s", strings.Join(allowed, ", ")))
			}
		}
	}
	intField := func(key string, min, max int, nullable bool) {
		if value, ok := typed[key]; ok {
			if value == nil && nullable {
				return
			}
			number, isInt := asInt(value)
			if !isInt || number < min || number > max {
				errs.add(key, fmt.Sprintf("must be %d to %d", min, max))
			}
		}
	}
	boolField := func(key string) {
		if value, ok := typed[key]; ok && value != nil {
			if _, isBool := value.(bool); !isBool {
				errs.add(key, "must be true or false")
			}
		}
	}
	enumField("isolation", "none", "clients", "clients_and_lan")
	enumField("rates", "compatible", "no_legacy", "high_density", "legacy_only")
	enumField("qos_class", "", "none", "best_effort", "background", "video", "voice")
	intField("dtim", 1, 255, false)
	intField("idle_timeout_sec", 60, 86400, false)
	intField("max_clients", 2, 512, true)
	intField("group_rekey_sec", 60, 86400, false)
	for _, key := range []string{"hidden", "band_steering", "dhcp_only", "dhcp_dns_only", "wmm_disabled", "uapsd_disabled",
		"disable_11ax", "disable_11nac", "eapol_v1", "sae_h2e_only", "transition_disable", "honour_blocklist"} {
		boolField(key)
	}
	if value, ok := typed["pmf"]; ok && value != nil {
		enumField("pmf", "disabled", "optional", "required")
		if security != "wpa2_personal" && security != "wpa2_enterprise" {
			errs.add("pmf", "PMF is set by the security mode for "+security)
		}
	}
	if on, _ := typed["band_steering"].(bool); on && len(bands) < 2 {
		errs.add("band_steering", "band steering needs both bands")
	}
	if on, _ := typed["sae_h2e_only"].(bool); on && !strings.Contains(security, "wpa3") {
		errs.add("sae_h2e_only", "SAE H2E applies to WPA3 modes only")
	}
	if on, _ := typed["transition_disable"].(bool); on && security != "wpa2_wpa3_personal" && security != "wpa2_wpa3_enterprise" {
		errs.add("transition_disable", "transition disable applies to transition modes only")
	}
	if value, ok := typed["owe_ssid"]; ok && value != nil {
		text, _ := value.(string)
		if text != "" && (security != "owe_transition" || ssidError(text) != "") {
			errs.add("owe_ssid", "the OWE companion SSID is 1 to 32 bytes and only for OWE transition")
		}
	}
	if value, ok := typed["min_rssi"]; ok && value != nil {
		var thresholds map[string]int
		if err := remarshal(value, &thresholds); err != nil {
			errs.add("min_rssi", "must be an object of band: dBm")
		}
		for band, rssi := range thresholds {
			if band != "2.4" && band != "5" {
				errs.add("min_rssi."+band, "unknown band")
			} else if rssi < -95 || rssi > -50 {
				errs.add("min_rssi."+band, "minimum RSSI must be -95 to -50 dBm")
			}
		}
	}
	if value, ok := typed["rate_limit"]; ok && value != nil {
		var limits map[string]int
		if err := remarshal(value, &limits); err != nil {
			errs.add("rate_limit", "must be an object of kbps limits")
		}
		for key, kbps := range limits {
			if !containsString([]string{"wlan_up", "wlan_down", "client_up", "client_down"}, key) {
				errs.add("rate_limit."+key, "unknown field")
			} else if kbps != 0 && (kbps < 1 || kbps > 999000) {
				errs.add("rate_limit."+key, "rate limit must be 1 to 999000 kbps (0 = unlimited)")
			}
		}
	}
	if value, ok := typed["roaming"].(map[string]any); ok {
		if fast, present := value["fast"]; present && fast != nil {
			if text, _ := fast.(string); !containsString([]string{"none", "okc", "11r"}, text) {
				errs.add("roaming.fast", `must be "none", "okc" or "11r"`)
			}
		}
	}
	if value, ok := typed["option82"].(map[string]any); ok {
		if circuit, _ := value["circuit_id"].(string); len(circuit) > 64 {
			errs.add("option82.circuit_id", "at most 64 characters")
		}
	}
	if value, ok := typed["schedule"].(map[string]any); ok {
		var schedule api.ScheduleView
		if err := remarshal(value, &schedule); err != nil {
			errs.add("schedule", "must be {enabled, windows}")
		}
		days := map[string]bool{"mon": true, "tue": true, "wed": true, "thu": true, "fri": true, "sat": true, "sun": true}
		for index, entry := range schedule.Windows {
			end := entry.End == "24:00" || hhmm.MatchString(entry.End)
			if !days[entry.Day] || !hhmm.MatchString(entry.Start) || !end || entry.Start >= entry.End {
				errs.add(fmt.Sprintf("schedule.windows[%d]", index), "each window is a weekday mon to sun with start before end as HH:MM")
			}
		}
	}
	if enterpriseModes[security] {
		authServers := 0
		if stored.RADIUS != nil {
			authServers = len(stored.RADIUS.Auth)
		}
		if radius, ok := typed["radius"].(map[string]any); ok {
			if list, ok := radius["auth"].([]any); ok {
				authServers = len(list)
			}
		}
		if authServers == 0 {
			errs.add("radius.auth", "Enterprise security needs at least one RADIUS server")
		}
	}
	if radius, ok := typed["radius"].(map[string]any); ok {
		for _, list := range []string{"auth", "acct"} {
			servers, _ := radius[list].([]any)
			if len(servers) > 2 {
				errs.add("radius."+list, "at most 2 servers")
			}
			for index, item := range servers {
				var server api.RADIUSServerInput
				path := fmt.Sprintf("radius.%s[%d]", list, index)
				if remarshal(item, &server) != nil || !hostOrIP(server.Host) {
					errs.add(path+".host", "host must be an IP or a DNS name")
				}
				if server.Port < 1 || server.Port > 65535 {
					errs.add(path+".port", "port must be 1 to 65535")
				}
				if server.Secret != nil && *server.Secret == "" {
					errs.add(path+".secret", "the shared secret must not be empty")
				}
			}
		}
	}
	if mpsk, ok := typed["mpsk"].(map[string]any); ok {
		if security != "wpa2_personal" {
			errs.add("mpsk", "per-device passphrases need WPA2-Personal (SAE needs the plaintext)")
		}
		keys, _ := mpsk["keys"].([]any)
		if len(keys) > 32 {
			errs.add("mpsk.keys", "at most 32 keys")
		}
		names := map[string]bool{}
		for index, item := range keys {
			var key api.MPSKKeyInput
			path := fmt.Sprintf("mpsk.keys[%d]", index)
			if remarshal(item, &key) != nil {
				errs.add(path, "invalid key")
				continue
			}
			if key.Name == "" || len(key.Name) > 32 || names[key.Name] {
				errs.add(path+".name", "names are 1 to 32 characters and unique")
			}
			names[key.Name] = true
			if key.Passphrase != nil {
				if message := passphraseError(*key.Passphrase, false); message != "" {
					errs.add(path+".passphrase", message)
				}
			}
			if key.VLANID != 0 && (key.VLANID < 2 || key.VLANID > 4094) {
				errs.add(path+".vlan_id", "VLAN must be 2 to 4094 (0 = the WLAN's VLAN)")
			}
			if key.MAC != "" && normaliseMAC(key.MAC) == "" {
				errs.add(path+".mac", "invalid MAC address")
			}
		}
	}
	_ = state
	_ = siteID
	_ = current
	_ = patch
}

// mergeRADIUSSecrets keeps a stored server's secret when the input omits
// it for the same host and port (write-only secrets, design §4.8).
func mergeRADIUSSecrets(raw any, stored wlanRecord) (any, *apiError) {
	radius, ok := raw.(map[string]any)
	if !ok {
		return raw, nil
	}
	previous := map[string]string{}
	if stored.RADIUS != nil {
		for _, server := range append(append([]storedServer(nil), stored.RADIUS.Auth...), stored.RADIUS.Acct...) {
			previous[fmt.Sprintf("%s:%d", strings.ToLower(server.Host), server.Port)] = server.Secret
		}
	}
	for _, list := range []string{"auth", "acct"} {
		servers, ok := radius[list].([]any)
		if !ok {
			continue
		}
		for index, item := range servers {
			server, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if _, has := server["secret"]; has {
				continue
			}
			port, _ := asInt(server["port"])
			key := fmt.Sprintf("%s:%d", strings.ToLower(fmt.Sprint(server["host"])), port)
			secret, found := previous[key]
			if !found {
				return nil, invalidField(fmt.Sprintf("radius.%s[%d].secret", list, index), "a new server needs its shared secret")
			}
			server["secret"] = secret
		}
	}
	// CoA servers keep their stored secret the same way (by host and port).
	if coa, ok := radius["coa"].(map[string]any); ok {
		previousCoA := map[string]string{}
		if stored.RADIUS != nil && stored.RADIUS.CoA != nil {
			for _, server := range stored.RADIUS.CoA.Servers {
				previousCoA[fmt.Sprintf("%s:%d", strings.ToLower(server.Host), server.Port)] = server.Secret
			}
		}
		servers, _ := coa["servers"].([]any)
		for index, item := range servers {
			server, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if _, has := server["secret"]; has {
				continue
			}
			port, _ := asInt(server["port"])
			secret, found := previousCoA[fmt.Sprintf("%s:%d", strings.ToLower(fmt.Sprint(server["host"])), port)]
			if !found {
				return nil, invalidField(fmt.Sprintf("radius.coa.servers[%d].secret", index), "a new CoA server needs its shared secret")
			}
			server["secret"] = secret
		}
	}
	return radius, nil
}

// mergeMPSKSecrets keeps stored per-device passphrases for keys sent
// without one, gives new keys an ID, and refuses a key equal to the main
// passphrase.
func mergeMPSKSecrets(raw any, current *model.Network, stored wlanRecord, newPSK *string) (any, *apiError) {
	mpsk, ok := raw.(map[string]any)
	if !ok {
		return raw, nil
	}
	previous := map[string]string{}
	if stored.MPSK != nil {
		for _, key := range stored.MPSK.Keys {
			previous[key.ID] = key.Passphrase
		}
	}
	main := ""
	if current != nil {
		main = current.PSK
	}
	if newPSK != nil {
		main = *newPSK
	}
	keys, _ := mpsk["keys"].([]any)
	seen := map[string]bool{}
	for index, item := range keys {
		key, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := key["id"].(string)
		if id == "" {
			fresh, err := randomID()
			if err != nil {
				return nil, internalError("cannot create a key ID")
			}
			key["id"], id = fresh, fresh
		}
		if _, has := key["passphrase"]; !has {
			passphrase, found := previous[id]
			if !found {
				return nil, invalidField(fmt.Sprintf("mpsk.keys[%d].passphrase", index), "a new key needs a passphrase")
			}
			key["passphrase"] = passphrase
		}
		passphrase, _ := key["passphrase"].(string)
		if passphrase == main && main != "" {
			return nil, invalidField(fmt.Sprintf("mpsk.keys[%d].passphrase", index), "must differ from the WLAN's passphrase")
		}
		if seen[passphrase] {
			return nil, invalidField(fmt.Sprintf("mpsk.keys[%d].passphrase", index), "passphrases must be unique within the WLAN")
		}
		seen[passphrase] = true
		if mac, ok := key["mac"].(string); ok && mac != "" {
			key["mac"] = normaliseMAC(mac)
		}
	}
	return mpsk, nil
}

// reveal serves POST /api/sites/$id/networks/$nid/reveal (phase 4): the
// passphrase and a Wi-Fi QR payload; admin only and audit-logged.
func (s *Server) reveal(writer http.ResponseWriter, request *http.Request, p params) {
	if !requireAdmin(writer, request) {
		return
	}
	state := s.store.Snapshot()
	network := findNetwork(state, p["site"], p["nid"])
	if network == nil {
		writeAPIError(writer, notFound("network"))
		return
	}
	security := readWLANRecord(*network).Security
	if security == "" {
		if lift, ok := s.liftWLANs(state, network.SiteID)[network.ID]; ok {
			security = lift.security
		}
	}
	view := api.RevealView{SSID: network.SSID, PSK: network.PSK, WiFiQR: wifiQR(network.SSID, network.PSK, security, readHidden(*network))}
	log.Printf("audit: passphrase of WLAN %s revealed to %s from %s", network.ID, principal(request), clientAddress(request))
	s.recordAction(request, network.SiteID, "Revealed the passphrase of WLAN "+network.SSID)
	respond(writer, http.StatusOK, view)
}

func readHidden(network model.Network) bool {
	var record struct {
		Hidden bool `json:"hidden"`
	}
	viewFrom(network, &record)
	return record.Hidden
}

// wifiQR renders the WIFI: URI scheme for a QR code.
func wifiQR(ssid, psk, security string, hidden bool) string {
	escape := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `:`, `\:`, `"`, `\"`)
	kind := "WPA"
	switch security {
	case "open", "owe", "owe_transition":
		kind = "nopass"
	case "wpa3_personal":
		kind = "SAE"
	}
	result := "WIFI:T:" + kind + ";S:" + escape.Replace(ssid) + ";"
	if kind != "nopass" {
		result += "P:" + escape.Replace(psk) + ";"
	}
	if hidden {
		result += "H:true;"
	}
	return result + ";"
}
