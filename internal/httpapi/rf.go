package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
)

// offeredWidths are the widths the typed forms offer per band. The live
// AP33 has no 160 MHz chanspec under AU/890 (live findings, 2026-11-14),
// and 2.4 GHz 40 MHz needs SecChan (expert only).
var offeredWidths = map[string][]int{"2.4": {20}, "5": {20, 40, 80}}

func widthOffered(band string, width int) bool {
	for _, offered := range offeredWidths[band] {
		if offered == width {
			return true
		}
	}
	return false
}

// validChannel reports whether a primary is valid for the country, band
// and width, and whether it needs DFS.
func validChannel(country, band string, width, channel int) (dfs, ok bool) {
	if !widthOffered(band, width) {
		return false, false
	}
	domain, found := regdomain.ForCountry(country)
	if !found {
		return false, false
	}
	return domain.Valid(band, width, channel)
}

// rfRecord is a site's rf record (design §4.3 RFProfile keys).
type rfRecord struct {
	Managed       bool                 `json:"managed"`
	Band24        *api.BandProfileView `json:"band_24"`
	Band5         *api.BandProfileView `json:"band_5"`
	ScanningRadio *bool                `json:"scanning_radio"`
	Extra         map[string]any       `json:"-"`
	present       bool
}

func readRFRecord(site model.Site) rfRecord {
	var wrapper struct {
		RF *rfRecord `json:"rf"`
	}
	viewFrom(site, &wrapper)
	if wrapper.RF == nil {
		return rfRecord{}
	}
	wrapper.RF.present = true
	return *wrapper.RF
}

func siteRFManaged(site model.Site) bool { return readRFRecord(site).Managed }

func defaultBand(band string) api.BandProfileView {
	if band == "2.4" {
		return api.BandProfileView{Enabled: true, Width: 20, Channels: []int{1, 6, 11}, Power: api.PowerPolicy{Mode: "auto", Min: 8, Max: 15}, Preamble: "short",
			Advanced: api.BandAdvanced{TAF: "default", TWT: true, Antenna: "default"}}
	}
	return api.BandProfileView{Enabled: true, Width: 80, Channels: []int{36, 40, 44, 48, 149, 153, 157, 161}, Power: api.PowerPolicy{Mode: "auto", Min: 12, Max: 20}, Preamble: "short",
		Advanced: api.BandAdvanced{TAF: "default", TWT: true, Antenna: "default"}}
}

func siteBand(site model.Site, band string) api.BandProfileView {
	record := readRFRecord(site)
	stored := record.Band5
	key := "band_5"
	if band == "2.4" {
		stored, key = record.Band24, "band_24"
	}
	if stored == nil {
		return defaultBand(band)
	}
	view := *stored
	// Advanced values the record does not hold are the template's: TWT on,
	// airtime fairness and antennas at the firmware default (design §3.10).
	if object, err := recordMap(site); err == nil {
		if twt, held := lookup(object, "rf."+key+".advanced.twt"); !held || twt == nil {
			view.Advanced.TWT = true
		}
	}
	if view.Advanced.TAF == "" {
		view.Advanced.TAF = "default"
	}
	if view.Advanced.Antenna == "" {
		view.Advanced.Antenna = "default"
	}
	return view
}

// unchangedAdvanced drops the advanced values of a band patch that equal
// what the band does now (the view's values, template defaults included),
// so saving a form unchanged stores nothing and the AP config stays
// byte-identical (an explicit TWT on would add TWTDisabled:false).
func unchangedAdvanced(current api.BandProfileView, patch map[string]any) {
	advanced, ok := patch["advanced"].(map[string]any)
	if !ok {
		return
	}
	now := map[string]any{"twt": current.Advanced.TWT, "taf": current.Advanced.TAF, "antenna": current.Advanced.Antenna}
	for key, value := range now {
		if sent, present := advanced[key]; present && sent != nil && sameJSON(sent, value) {
			delete(advanced, key)
		}
	}
	if len(advanced) == 0 {
		delete(patch, "advanced")
	}
}

func siteBandWidth(site model.Site, band string) int {
	if width := siteBand(site, band).Width; width != 0 {
		return width
	}
	return defaultBand(band).Width
}

func siteBandDFS(site model.Site, band string) bool { return band == "5" && siteBand(site, band).DFS }

// validChannels lists the primaries per offered width for the UI.
func validChannels(country, band string) []api.ValidChannels {
	result := []api.ValidChannels{}
	domain, ok := regdomain.ForCountry(country)
	if !ok {
		return result
	}
	for _, width := range offeredWidths[band] {
		table := domain.Table(band, width)
		if table == nil || table.ExpertOnly || table.NeedsLabTest {
			continue
		}
		entry := api.ValidChannels{Width: width, Primaries: []api.ChannelChoice{}, Blocks: [][]int{}}
		for _, primary := range table.Primaries {
			entry.Primaries = append(entry.Primaries, api.ChannelChoice{Channel: primary.Channel, DFS: primary.DFS})
		}
		for _, block := range table.Blocks {
			entry.Blocks = append(entry.Blocks, append([]int(nil), block...))
		}
		result = append(result, entry)
	}
	return result
}

// rfProfileView builds the site's RF profile with the plan table (design
// §3.10).
func (s *Server) rfProfileView(ctx apContext, site model.Site) api.RFProfileView {
	record := readRFRecord(site)
	view := api.RFProfileView{Managed: record.Managed, Country: site.Country, ScanningRadio: record.ScanningRadio,
		Band24: siteBand(site, "2.4"), Band5: siteBand(site, "5"), Plan: []api.PlanRow{}, Recommendations: []api.Recommendation{},
		UpdatedRev: site.UpdatedRev}
	if ccrev, ok := regdomain.CCRev(site.Country); ok {
		view.CCRev = &ccrev
	}
	for _, band := range []*api.BandProfileView{&view.Band24, &view.Band5} {
		if band.Channels == nil {
			band.Channels = []int{}
		}
	}
	view.Band24.ValidChannels = validChannels(site.Country, "2.4")
	view.Band5.ValidChannels = validChannels(site.Country, "5")
	for _, ap := range siteAPs(ctx.state, site.ID) {
		obs := s.observe(ap.ID)
		overrides, plan, _ := apOverrides(ap)
		for _, band := range []api.Band{api.Band24, api.Band5} {
			row := api.PlanRow{APID: ap.ID, APName: ap.Name, Band: band, Mode: "auto", Status: api.PlanStatusOffline}
			profile := view.Band5
			if band == api.Band24 {
				profile = view.Band24
			}
			row.Width = profile.Width
			if profile.Power.Mode == "fixed" {
				row.PowerDBm = profile.Power.DBm
			}
			if planned := plan[band]; planned != nil {
				row.Channel, row.PowerDBm, row.DFSFallback, row.Source = planned.Channel, planned.PowerDBm, planned.DFSFallback, "plan"
				if planned.Width != 0 {
					row.Width = planned.Width
				}
			}
			if override := overrides.RF[band]; override != nil {
				if override.Channel != nil {
					row.Mode, row.Channel, row.Source = "static", *override.Channel, "ap_override"
				}
				if override.Width != nil {
					row.Width = *override.Width
				}
				if override.PowerDBm != nil {
					row.PowerDBm = *override.PowerDBm
				}
			}
			if obs.fresh(ap.ConfigUpdatedAt) {
				row.Status = api.PlanStatusRadioMissing
				for _, radio := range obs.telemetry.Radios {
					if radio.Band != string(band) || radio.Role == "scan" {
						continue
					}
					util, _ := radio.util()
					row.Actual = &api.PlanActual{Channel: radio.Channel, Width: radio.WidthMHz, MaxPowerDBm: radio.MaxTxPowerDBm, Util: util, Clients: radio.Clients}
					switch {
					case radio.Missing:
						row.Status = api.PlanStatusRadioMissing
					case radio.DFS.CACState != 0:
						row.Status = api.PlanStatusCAC
					case row.Channel == 0 || (radio.Channel == row.Channel && radio.WidthMHz == row.Width):
						row.Status = api.PlanStatusMatch
					default:
						row.Status = api.PlanStatusMismatch
					}
				}
			}
			view.Plan = append(view.Plan, row)
		}
	}
	if s.config.Recommendations != nil {
		for _, recommendation := range s.config.Recommendations.List(ctx.state, site.ID) {
			if strings.HasPrefix(recommendation.ID, "rf") || strings.Contains(strings.ToLower(recommendation.Title), "radio") ||
				strings.Contains(recommendation.Title, "GHz") || strings.Contains(recommendation.Title, "RF") {
				view.Recommendations = append(view.Recommendations, recommendation)
			}
		}
	}
	return view
}

// parseRFPatch validates an RF profile merge patch (design §3.10) and
// returns the record patch.
func (s *Server) parseRFPatch(state model.State, site model.Site, raw any) (any, *apiError) {
	if raw == nil {
		return nil, nil
	}
	patch, ok := raw.(map[string]any)
	if !ok {
		return nil, invalidField("rf", "must be an object")
	}
	var errs fieldErrors
	errs.status = http.StatusUnprocessableEntity
	for key, value := range patch {
		switch key {
		case "managed", "scanning_radio":
			if _, isBool := value.(bool); value != nil && !isBool {
				errs.add("rf."+key, "must be true or false")
			}
			if key == "managed" && value == true {
				if _, ok := regdomain.CCRev(site.Country); !ok {
					errs.add("rf.managed", "managed RF needs a country with a verified regulatory table ("+strings.Join(regdomain.Countries(), ", ")+")")
				}
			}
		case "band_24", "band_5":
			band := "5"
			if key == "band_24" {
				band = "2.4"
			}
			bandPatch, isObject := value.(map[string]any)
			if !isObject {
				errs.add("rf."+key, "must be an object")
				continue
			}
			merged := siteBand(site, band)
			unchangedAdvanced(merged, bandPatch)
			mergedMap, _ := recordMap(merged)
			mergePatch(mergedMap, bandPatch)
			var result api.BandProfileView
			if err := remarshal(mergedMap, &result); err != nil {
				errs.add("rf."+key, "invalid band profile")
				continue
			}
			s.checkBand(&errs, "rf."+key, site.Country, band, result, bandPatch)
			delete(bandPatch, "valid_channels")
		default:
			errs.add("rf."+key, "unknown field")
		}
	}
	if bad := errs.err("RF profile"); bad != nil {
		return nil, bad
	}
	return patch, nil
}

func (s *Server) checkBand(errs *fieldErrors, path, country, band string, profile api.BandProfileView, patch map[string]any) {
	if _, ok := patch["width"]; ok && !widthOffered(band, profile.Width) {
		errs.add(path+".width", fmt.Sprintf("width must be one of %v MHz on %s GHz", offeredWidths[band], band))
		return
	}
	if band == "2.4" {
		if _, ok := patch["dfs"]; ok && profile.DFS {
			errs.add(path+".dfs", "DFS is a 5 GHz setting")
		}
	}
	if _, ok := patch["channels"]; ok || patch["width"] != nil || patch["dfs"] != nil {
		if len(profile.Channels) == 0 {
			errs.add(path+".channels", "choose at least one channel for Auto")
		}
		for _, channel := range profile.Channels {
			dfs, ok := validChannel(country, band, profile.Width, channel)
			if !ok {
				errs.add(path+".channels", fmt.Sprintf("channel %d is not valid at %d MHz on %s GHz", channel, profile.Width, band))
				break
			}
			if dfs && !profile.DFS {
				errs.add(path+".channels", fmt.Sprintf("channel %d needs DFS; turn on Use DFS channels", channel))
				break
			}
		}
	}
	if _, ok := patch["dfs_fallback"]; ok && profile.DFSFallback != 0 {
		dfs, ok := validChannel(country, band, profile.Width, profile.DFSFallback)
		if !ok || dfs {
			errs.add(path+".dfs_fallback", "the DFS fallback must be a non-DFS channel valid at the band's width")
		}
	}
	if _, ok := patch["power"]; ok {
		power := profile.Power
		switch power.Mode {
		case "fixed":
			if power.DBm < 1 || power.DBm > 25 {
				errs.add(path+".power.dbm", "transmit power must be 1 to 25 dBm (never 0)")
			}
		case "auto":
			if power.Min < 1 || power.Max > 25 || power.Min > power.Max {
				errs.add(path+".power", "Auto power needs 1 ≤ min ≤ max ≤ 25 dBm")
			}
		default:
			errs.add(path+".power.mode", `power mode must be "fixed" or "auto"`)
		}
	}
	if _, ok := patch["preamble"]; ok {
		switch profile.Preamble {
		case "auto", "short", "long":
		default:
			errs.add(path+".preamble", `preamble must be "auto", "short" or "long"`)
		}
	}
	if advanced, ok := patch["advanced"].(map[string]any); ok {
		if _, present := advanced["taf"]; present {
			switch profile.Advanced.TAF {
			case "default", "off", "on":
			default:
				errs.add(path+".advanced.taf", `airtime fairness must be "default", "off" or "on"`)
			}
		}
		if _, present := advanced["antenna"]; present {
			allowed := map[string]bool{"default": true, "2x2": true, "1x1": true}
			if band == "5" {
				allowed["4x4"], allowed["3x3"] = true, true
			}
			if !allowed[profile.Advanced.Antenna] {
				errs.add(path+".advanced.antenna", "antenna mode not available on this band")
			}
		}
		if rssi := profile.Advanced.MinProbeRSSI; rssi != nil && (*rssi < -95 || *rssi > -50) {
			errs.add(path+".advanced.min_probe_rssi", "minimum probe RSSI must be -95 to -50 dBm")
		}
	}
}

// getRF serves GET /api/sites/$id/rf.
func (s *Server) getRF(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	site := findSite(state, p["site"])
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	respond(writer, http.StatusOK, s.rfProfileView(s.apContext(state), *site))
}

// putRF serves PUT /api/sites/$id/rf: a merge patch of the RF profile
// (same as PATCH /api/sites/$id {rf}).
func (s *Server) putRF(writer http.ResponseWriter, request *http.Request, p params) {
	if !s.typedEnabled() {
		writeAPIError(writer, migrationPending([]string{"rf"}))
		return
	}
	patch, err := readPatch(request, 64*1024, "RF profile")
	if err != nil {
		writeError(writer, err)
		return
	}
	siteID := p["site"]
	state := s.store.Snapshot()
	site := findSite(state, siteID)
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	if bad := ifMatch(request, site.UpdatedRev, false); bad != nil {
		staleWith(writer, bad, s.rfProfileView(s.apContext(state), *site))
		return
	}
	for _, key := range []string{"plan", "recommendations", "country", "ccrev"} {
		delete(patch, key)
	}
	edit, bad := s.parseSitePatch(state, *site, map[string]any{"rf": patch})
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, planInitial: true,
		kind: "site.updated", objectID: siteID, historySite: siteID, summary: "Site " + site.Name + ": RF profile changed",
		objects: []api.ChangeObject{{Kind: "site", ID: siteID, Name: site.Name}},
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
	updated := findSite(out.state, siteID)
	if updated == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	respondMutation(writer, http.StatusOK, out, s.rfProfileView(s.apContext(out.state), *updated))
}

// planInput gathers what the planner needs for a site.
func (s *Server) planInput(state model.State, siteID string) PlanInput {
	input := PlanInput{State: state, SiteID: siteID, Radios: map[string][]api.RadioView{}, Scans: map[string]ScanReport{}, OwnBSSIDs: map[string]string{}, Now: s.now()}
	for _, ap := range siteAPs(state, siteID) {
		obs := s.observe(ap.ID)
		if !obs.fresh(ap.ConfigUpdatedAt) {
			continue
		}
		input.Radios[ap.ID] = radioViews(obs)
		for _, vap := range obs.telemetry.VAPs {
			if vap.BSSID != "" {
				input.OwnBSSIDs[strings.ToLower(vap.BSSID)] = ap.ID
			}
		}
		if obs.telemetry.Scan != nil {
			input.Scans[ap.ID] = ScanReport{At: obs.telemetry.LastFull, Neighbours: scanNeighbours(ap, obs)}
		}
	}
	return input
}

func scanNeighbours(ap model.APRecord, obs apObs) []api.Neighbour {
	var result []api.Neighbour
	if obs.telemetry.Scan == nil {
		return result
	}
	add := func(band api.Band, list []obsScanNeighbour) {
		for _, entry := range list {
			neighbour := api.Neighbour{SSID: entry.SSID, BSSID: strings.ToLower(entry.BSSID), Band: band, Channel: entry.Channel,
				Width: entry.WidthMHz, RSSI: entry.RSSI, SeenBy: ap.ID, SeenByName: ap.Name, LastSeen: obs.telemetry.LastFull.UTC()}
			if entry.QBSS != nil {
				stations := entry.QBSS.Stations
				util := float64(entry.QBSS.Utilization) / 255
				neighbour.StationCount, neighbour.Util = &stations, &util
			}
			result = append(result, neighbour)
		}
	}
	add(api.Band24, obs.telemetry.Scan.Band24)
	add(api.Band5, obs.telemetry.Scan.Band5)
	return result
}

// rfPlan serves POST /api/sites/$id/rf/plan[?dry_run=1] (phase 3): a dry
// run returns the proposal; without it the proposal is applied (only the
// selected changes, or every change that is not "keep").
func (s *Server) rfPlan(writer http.ResponseWriter, request *http.Request, p params) {
	if s.config.Planner == nil {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "the channel planner is not available in this build"))
		return
	}
	if !s.typedEnabled() {
		writeAPIError(writer, migrationPending([]string{"rf"}))
		return
	}
	siteID := p["site"]
	var input api.PlanApplyInput
	if request.ContentLength > 0 {
		if err := decodeJSON(request, 64*1024, &input, "plan selection"); err != nil {
			writeError(writer, err)
			return
		}
	}
	state := s.store.Snapshot()
	site := findSite(state, siteID)
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	proposal, err := s.config.Planner.Propose(s.planInput(state, siteID))
	if err != nil {
		writeAPIError(writer, newError(http.StatusUnprocessableEntity, api.ErrorCodeInvalid, "cannot plan channels: "+err.Error()))
		return
	}
	if proposal.Changes == nil {
		proposal.Changes = []api.PlanChange{}
	}
	selected := selectPlanChanges(proposal.Changes, input.Only)
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "site.updated", objectID: siteID,
		historySite: siteID, summary: fmt.Sprintf("Site %s: channel plan applied (%s)", site.Name, plural(len(selected), "radio")),
		objects: []api.ChangeObject{{Kind: "site", ID: siteID, Name: site.Name}},
		change: func(state *model.State, _ *model.Tx) error {
			if len(selected) == 0 {
				return nil
			}
			return s.config.Planner.Apply(state, siteID, selected, state.UpdatedAt)
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respondMutation(writer, http.StatusOK, out, proposal)
}

func selectPlanChanges(changes []api.PlanChange, only []api.PlanSelection) []api.PlanChange {
	var result []api.PlanChange
	for _, change := range changes {
		if len(only) == 0 {
			if !change.Keep {
				result = append(result, change)
			}
			continue
		}
		for _, selection := range only {
			if selection.APID == change.APID && selection.Band == change.Band {
				result = append(result, change)
			}
		}
	}
	return result
}

// neighbours serves GET /api/sites/$id/rf/neighbours?band=&ap= (phase 3).
// ap limits the page to what one AP's scanning radio heard (the AP
// Radios tab). Neighbour SSIDs and BSSIDs stay in the authenticated API.
func (s *Server) neighbours(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	state := s.store.Snapshot()
	if findSite(state, siteID) == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	band := request.URL.Query().Get("band")
	if band != "" && band != "2.4" && band != "5" {
		writeAPIError(writer, invalidField("band", `band must be "2.4" or "5"`))
		return
	}
	onlyAP := ""
	if raw := request.URL.Query().Get("ap"); raw != "" {
		onlyAP = raw
		if mac := normaliseMAC(raw); mac != "" && findAP(state, mac) != nil {
			onlyAP = mac
		}
		if ap := findAP(state, onlyAP); ap == nil || ap.SiteID != siteID {
			writeAPIError(writer, notFound("AP").withField("ap", "no such AP on this site"))
			return
		}
	}
	input := s.planInput(state, siteID)
	page := api.NeighbourPage{Neighbours: []api.Neighbour{}, Own: []api.Neighbour{}, Scans: []api.ScanAge{}}
	for _, ap := range siteAPs(state, siteID) {
		if onlyAP != "" && ap.ID != onlyAP {
			continue
		}
		report, ok := input.Scans[ap.ID]
		counts := map[api.Band]int{}
		for _, neighbour := range report.Neighbours {
			if band != "" && string(neighbour.Band) != band {
				continue
			}
			counts[neighbour.Band]++
			if owner, own := input.OwnBSSIDs[neighbour.BSSID]; own && owner != ap.ID {
				page.Own = append(page.Own, neighbour)
				continue
			}
			page.Neighbours = append(page.Neighbours, neighbour)
		}
		for _, b := range []api.Band{api.Band24, api.Band5} {
			if band != "" && string(b) != band {
				continue
			}
			age := api.ScanAge{APID: ap.ID, APName: ap.Name, Band: b, Entries: counts[b]}
			if ok {
				age.At = timePtr(report.At)
			}
			page.Scans = append(page.Scans, age)
		}
	}
	sort.SliceStable(page.Neighbours, func(i, j int) bool { return page.Neighbours[i].RSSI > page.Neighbours[j].RSSI })
	respond(writer, http.StatusOK, page)
}
