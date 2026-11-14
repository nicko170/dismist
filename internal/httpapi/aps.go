package httpapi

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// listAPs serves GET /api/aps?site= (design §4.9: the bare APView array;
// the legacy app gets its {aps: [...]} shape).
func (s *Server) listAPs(writer http.ResponseWriter, request *http.Request, _ params) {
	state := s.store.Snapshot()
	if legacyClient(request) {
		s.legacyAPList(writer, state)
		return
	}
	site := request.URL.Query().Get("site")
	ctx := s.apContext(state)
	views := make([]api.APView, 0, len(state.APs))
	for _, ap := range state.APs {
		if site != "" && ap.SiteID != site {
			continue
		}
		views = append(views, s.apView(ctx, ap))
	}
	sort.SliceStable(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	respond(writer, http.StatusOK, views)
}

// getAP serves GET /api/aps/$id (APDetail).
func (s *Server) getAP(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	ap := findAP(state, p["ap"])
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	respond(writer, http.StatusOK, s.apDetail(s.apContext(state), *ap))
}

// registerAP serves POST /api/aps (design §4.9: no model; the AP reports
// it). The new AP compiles as changed, so change detection sets its
// revision.
func (s *Server) registerAP(writer http.ResponseWriter, request *http.Request, _ params) {
	var input struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Model        string `json:"model"`
		ManagementIP string `json:"management_ip"`
		SiteID       string `json:"site_id"`
		Secret       string `json:"secret"`
		// LocalFiles are the AP's local_* files the CLI found (names and
		// SHA-256 only); absent means never checked.
		LocalFiles []api.LocalFileView `json:"local_files"`
	}
	if err := decodeJSON(request, 64*1024, &input, "AP registration"); err != nil {
		writeError(writer, err)
		return
	}
	id := normaliseMAC(input.ID)
	if id == "" {
		writeAPIError(writer, invalidField("id", "invalid AP ID: use its Ethernet MAC address"))
		return
	}
	secret, err := parseAPSecret(input.Secret)
	if err != nil {
		writeAPIError(writer, invalidField("secret", err.Error()))
		return
	}
	managementIP := ""
	if address := strings.TrimSpace(input.ManagementIP); address != "" {
		ip := net.ParseIP(address)
		if ip == nil {
			writeAPIError(writer, invalidField("management_ip", "invalid AP management IP"))
			return
		}
		managementIP = ip.String()
	}
	name := strings.TrimSpace(input.Name)
	if len(name) > 64 {
		writeAPIError(writer, invalidField("name", "name must be at most 64 characters"))
		return
	}
	if name == "" {
		name = "ap-" + strings.ReplaceAll(id, "-", "")
	}
	localFiles, badFiles := parseLocalFiles(input.LocalFiles, s.now())
	if badFiles != nil {
		writeAPIError(writer, badFiles)
		return
	}
	out, err := s.run(mutation{
		sites: []string{input.SiteID}, dryRun: dryRun(request), request: request, planInitial: true,
		kind: "ap.created", objectID: id, historySite: input.SiteID,
		summary: "Registered AP " + name, objects: []api.ChangeObject{{Kind: "ap", ID: id, Name: name}},
		change: func(state *model.State, tx *model.Tx) error {
			if input.SiteID != "" && findSite(*state, input.SiteID) == nil {
				return notFound("site").withField("site_id", "no such site")
			}
			if findAP(*state, id) != nil {
				return conflict("AP already registered").withField("id", "an AP with this ID is already registered")
			}
			state.APs = append(state.APs, model.APRecord{ID: id, Name: name, Model: strings.TrimSpace(input.Model),
				ManagementIP: managementIP, SiteID: input.SiteID, Secret: secret, LocalFiles: localFiles,
				ConfigRevision: state.Revision + 1, ConfigUpdatedAt: state.UpdatedAt})
			return nil
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if !out.dryRun {
		log.Printf("audit: AP %s registered by %s", id, principal(request))
	}
	ap := findAP(out.state, id)
	if ap == nil {
		writeAPIError(writer, internalError("the registered AP was not stored"))
		return
	}
	if legacyClient(request) {
		respond(writer, http.StatusCreated, map[string]any{"ap": s.legacyAPView(*ap), "revision": out.revision})
		return
	}
	respondMutation(writer, http.StatusCreated, out, s.apView(s.apContext(out.state), *ap))
}

// patchAP serves PATCH /api/aps/$id (design §4.9): name, site (a move),
// pin, secret, local_files and protocol_features (phase 1), and the typed
// overrides (phase 2). If-Match is required.
func (s *Server) patchAP(writer http.ResponseWriter, request *http.Request, p params) {
	s.updateAPFields(writer, request, p["ap"], true)
}

// putAP serves the legacy PUT /api/aps/$id (no If-Match).
func (s *Server) putAP(writer http.ResponseWriter, request *http.Request, p params) {
	s.updateAPFields(writer, request, p["ap"], false)
}

var (
	apPhase1Keys = map[string]bool{"name": true, "site_id": true, "management_ip": true, "secret": true,
		"local_files": true, "protocol_features": true, "model": true}
	apTypedKeys = map[string]bool{"led_brightness": true, "ssh_enabled": true, "rf": true, "management": true,
		"eth1": true, "wired_client_stats": true}
	apReadOnly = jsonKeys(api.APDetail{})
)

func (s *Server) updateAPFields(writer http.ResponseWriter, request *http.Request, apID string, v1 bool) {
	patch, err := readPatch(request, 64*1024, "AP update")
	if err != nil {
		writeError(writer, err)
		return
	}
	readOnly := map[string]bool{}
	for key := range apReadOnly {
		if !apPhase1Keys[key] && !apTypedKeys[key] {
			readOnly[key] = true
		}
	}
	if !v1 {
		readOnly = map[string]bool{}
	} else {
		delete(patch, "model")
	}
	if bad := checkKeys(patch, apTypedKeys, apPhase1Keys, readOnly, s.typedEnabled()); bad != nil {
		writeAPIError(writer, bad)
		return
	}
	if len(patch) == 0 {
		writeAPIError(writer, invalid("AP fields required: name, site_id, management_ip, secret, local_files, protocol_features or an override"))
		return
	}
	state := s.store.Snapshot()
	current := findAP(state, apID)
	if current == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	if bad := ifMatch(request, current.UpdatedRev, v1); bad != nil {
		if bad.code == api.ErrorCodeStale {
			staleWith(writer, bad, s.apDetail(s.apContext(state), *current))
			return
		}
		writeAPIError(writer, bad)
		return
	}
	edit, bad := s.parseAPPatch(state, *current, patch)
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	sites := []string{current.SiteID}
	if edit.siteID != nil && *edit.siteID != current.SiteID {
		sites = append(sites, *edit.siteID)
	}
	var extra []compile.Push
	out, err := s.run(mutation{
		sites: sites, dryRun: dryRun(request), request: request, planInitial: edit.siteID != nil, extraPushes: &extra,
		kind: "ap.updated", objectID: apID, historySite: current.SiteID,
		summary: edit.summary(*current), objects: []api.ChangeObject{{Kind: "ap", ID: apID, Name: current.Name}},
		change: func(state *model.State, tx *model.Tx) error {
			ap := findAP(*state, apID)
			if ap == nil {
				return notFound("AP")
			}
			if ap.SiteID != current.SiteID {
				return conflict("the AP moved to another site; retry")
			}
			before := *ap
			if err := edit.apply(s, state, tx, ap, &extra); err != nil {
				return err
			}
			if sameAPRecord(before, *ap) {
				return errNoChange
			}
			return nil
		},
	})
	if errors.Is(err, errNoChange) {
		state := s.store.Snapshot()
		ap := findAP(state, apID)
		if ap == nil {
			writeAPIError(writer, notFound("AP"))
			return
		}
		if !v1 || legacyClient(request) {
			respond(writer, http.StatusOK, map[string]any{"ap": s.legacyAPView(*ap), "revision": state.Revision})
			return
		}
		respondMutation(writer, http.StatusOK, outcome{revision: state.Revision, state: state,
			impact: api.Impact{APs: []api.ImpactAP{}, Disruption: api.DisruptionNone, Summary: "No AP changes"}},
			s.apDetail(s.apContext(state), *ap))
		return
	}
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if edit.features != nil && !out.dryRun {
		s.applyFeatures(apID, out.state)
		log.Printf("audit: AP %s protocol features changed by %s", apID, principal(request))
	}
	if edit.secret != nil && !out.dryRun {
		log.Printf("audit: AP %s secret replaced by %s", apID, principal(request))
	}
	ap := findAP(out.state, apID)
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	if !v1 || legacyClient(request) {
		respond(writer, http.StatusOK, map[string]any{"ap": s.legacyAPView(*ap), "revision": out.revision})
		return
	}
	respondMutation(writer, http.StatusOK, out, s.apDetail(s.apContext(out.state), *ap))
}

// sameAPRecord compares two AP records by their stored JSON, ignoring the
// fields the store maintains.
func sameAPRecord(a, b model.APRecord) bool {
	a.UpdatedRev, b.UpdatedRev = 0, 0
	rawA, _ := recordMap(a)
	rawB, _ := recordMap(b)
	return sameJSON(rawA, rawB)
}

// apEdit is a parsed AP patch.
type apEdit struct {
	name         *string
	model        *string
	siteID       *string
	managementIP *string
	secret       []byte
	localFiles   *[]model.LocalFile
	features     map[string]*bool
	typed        map[string]any
}

func (e apEdit) summary(ap model.APRecord) string {
	var parts []string
	label := ap.Name
	if label == "" {
		label = ap.ID
	}
	if e.name != nil {
		parts = append(parts, fmt.Sprintf("renamed to %s", *e.name))
	}
	if e.siteID != nil {
		parts = append(parts, "moved")
	}
	if e.managementIP != nil {
		parts = append(parts, "source pin changed")
	}
	if e.secret != nil {
		parts = append(parts, "secret replaced")
	}
	if e.localFiles != nil {
		parts = append(parts, "local files recorded")
	}
	if e.features != nil {
		parts = append(parts, "protocol features changed")
	}
	keys := make([]string, 0, len(e.typed))
	for key := range e.typed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+" changed")
	}
	return "AP " + label + ": " + strings.Join(parts, ", ")
}

func (s *Server) parseAPPatch(state model.State, current model.APRecord, patch map[string]any) (apEdit, *apiError) {
	var edit apEdit
	var errs fieldErrors
	text := func(key string) (*string, bool) {
		value, present := patch[key]
		if !present {
			return nil, false
		}
		if value == nil {
			empty := ""
			return &empty, true
		}
		str, ok := value.(string)
		if !ok {
			errs.add(key, "must be a JSON string")
			return nil, true
		}
		return &str, true
	}
	if name, ok := text("name"); ok && name != nil {
		trimmed := strings.TrimSpace(*name)
		if len(trimmed) > 64 {
			errs.add("name", "name must be at most 64 characters")
		}
		edit.name = &trimmed
	}
	if value, ok := text("model"); ok && value != nil {
		trimmed := strings.TrimSpace(*value)
		edit.model = &trimmed
	}
	if site, ok := text("site_id"); ok && site != nil {
		if *site != "" && findSite(state, *site) == nil {
			return edit, notFound("site").withField("site_id", "no such site")
		}
		if *site != current.SiteID {
			edit.siteID = site
		}
	}
	if pin, ok := text("management_ip"); ok && pin != nil {
		trimmed := strings.TrimSpace(*pin)
		if trimmed != "" {
			ip := net.ParseIP(trimmed)
			if ip == nil {
				errs.add("management_ip", "invalid AP management IP")
			} else {
				trimmed = ip.String()
			}
		}
		edit.managementIP = &trimmed
	}
	if secret, ok := text("secret"); ok && secret != nil && strings.TrimSpace(*secret) != "" {
		parsed, err := parseAPSecret(*secret)
		if err != nil {
			errs.add("secret", err.Error())
		}
		edit.secret = parsed
	}
	if raw, ok := patch["local_files"]; ok {
		var files []api.LocalFileView
		if raw != nil {
			if err := remarshal(raw, &files); err != nil {
				errs.add("local_files", "must be an array of {name, sha256}")
			}
		}
		if files == nil {
			files = []api.LocalFileView{}
		}
		parsed, bad := parseLocalFiles(files, s.now())
		if bad != nil {
			return edit, bad
		}
		edit.localFiles = &parsed
	}
	if raw, ok := patch["protocol_features"]; ok {
		features := map[string]*bool{}
		if raw != nil {
			if err := remarshal(raw, &features); err != nil {
				errs.add("protocol_features", "must be an object of feature: true|false|null")
			}
		}
		for name := range features {
			if !knownFeature(name) {
				errs.add("protocol_features."+name, "unknown protocol feature; known: "+strings.Join(featureNames(), ", "))
			}
		}
		edit.features = features
	}
	if bad := errs.err("AP update"); bad != nil {
		return edit, bad
	}
	typed := map[string]any{}
	for key := range apTypedKeys {
		if value, ok := patch[key]; ok {
			typed[key] = value
		}
	}
	if len(typed) > 0 {
		converted, bad := s.apTypedPatch(state, current, typed)
		if bad != nil {
			return edit, bad
		}
		edit.typed = converted
	}
	return edit, nil
}

// apply changes the AP record (inside the mutation transaction).
func (e apEdit) apply(s *Server, state *model.State, tx *model.Tx, ap *model.APRecord, extra *[]compile.Push) error {
	if e.name != nil {
		ap.Name = *e.name
	}
	if e.model != nil {
		ap.Model = *e.model
	}
	if e.managementIP != nil {
		ap.ManagementIP = *e.managementIP
	}
	if e.secret != nil {
		ap.Secret = e.secret
		ap.SecretFile = ""
	}
	if e.localFiles != nil {
		ap.LocalFiles = *e.localFiles
	}
	if e.features != nil {
		features := map[string]bool{}
		for name, on := range ap.Features {
			features[name] = on
		}
		for name, on := range e.features {
			if on == nil {
				delete(features, name)
			} else {
				features[name] = *on
			}
		}
		if len(features) == 0 {
			features = nil
		}
		ap.Features = features
	}
	if len(e.typed) > 0 {
		if err := patchRecord(ap, e.typed); err != nil {
			var unsupported *errUnsupported
			if errors.As(err, &unsupported) {
				return unsupportedField(apPathName(unsupported.path))
			}
			return err
		}
	}
	if e.siteID != nil {
		ap.SiteID = *e.siteID
		if *e.siteID == "" {
			return s.unassign(state, tx, ap, extra)
		}
	}
	return nil
}

func apPathName(path string) string {
	path = strings.Replace(path, "rf_override", "rf", 1)
	return path
}

// unassign gives an AP that leaves every site one last config with no
// site and no WLANs, at a new revision, before the registry withdraws it
// (phase-0 behaviour); its desired config is dropped so a later
// assignment counts as a change.
func (s *Server) unassign(state *model.State, tx *model.Tx, ap *model.APRecord, extra *[]compile.Push) error {
	ap.ConfigRevision = state.Revision + 1
	ap.ConfigUpdatedAt = state.UpdatedAt
	networks := make([]model.Network, 0)
	for _, network := range state.Networks {
		if network.SiteID == "" {
			networks = append(networks, network)
		}
	}
	config, err := compile.BuildSiteConfig(s.base, model.Site{}, *ap, networks, ap.ConfigUpdatedAt)
	if err != nil {
		return &compile.APError{APID: ap.ID, Err: err}
	}
	*extra = append(*extra, compile.Push{APID: ap.ID, Config: config, Revision: ap.ConfigRevision})
	if tx != nil {
		return tx.APConfig().Delete(ap.ID)
	}
	return nil
}

// knownFeature accepts the controller's fixed features plus ap_cmd, the
// experimental command channel the API gates on (design §5.6).
func knownFeature(name string) bool { return controller.ValidFeature(name) || name == FeatureAPCmd }

func featureNames() []string {
	names := controller.FeatureNames()
	if !controller.ValidFeature(FeatureAPCmd) {
		names = append(names, FeatureAPCmd)
	}
	return names
}

// applyFeatures switches a changed AP's protocol features on its control
// session at once (design §5.7: on the next frame, without a restart).
func (s *Server) applyFeatures(apID string, state model.State) {
	control := s.controlFor(apID)
	ap := findAP(state, apID)
	if control == nil || ap == nil {
		return
	}
	control.SetFeatures(controller.ResolveFeatures(s.defaultFeatures, ap.Features))
}

// apTypedPatch validates the typed override part of an AP patch and
// returns the record patch (design §3.6, §4.3 keys).
func (s *Server) apTypedPatch(state model.State, ap model.APRecord, typed map[string]any) (map[string]any, *apiError) {
	var errs fieldErrors
	errs.status = http.StatusUnprocessableEntity
	result := map[string]any{}
	changed := map[string]any{}
	if value, ok := typed["led_brightness"]; ok {
		if value != nil {
			brightness, ok := asInt(value)
			if !ok || brightness < 0 || brightness > 255 {
				errs.add("led_brightness", "LED brightness must be 0 to 255")
			}
		}
		result["led_brightness"] = value
		changed["ap.led_brightness"] = jsonValue(value)
	}
	if value, ok := typed["ssh_enabled"]; ok {
		if value != nil {
			on, isBool := value.(bool)
			if !isBool {
				errs.add("ssh_enabled", "must be true, false or null")
			} else if on {
				site := findSite(state, ap.SiteID)
				if site == nil || len(siteSSHKeys(*site)) == 0 {
					errs.add("ssh_enabled", "SSH needs at least one key in the site's Access settings")
				}
			}
		}
		result["ssh_enabled"] = value
		changed["ap.ssh_enabled"] = value
	}
	if value, ok := typed["wired_client_stats"]; ok {
		if _, isBool := value.(bool); value != nil && !isBool {
			errs.add("wired_client_stats", "must be true, false or null")
		}
		result["wired_client_stats"] = value
		changed["ap.wired_client_stats"] = value
	}
	for _, key := range []string{"management", "eth1"} {
		if value, ok := typed[key]; ok {
			if _, isObject := value.(map[string]any); value != nil && !isObject {
				errs.add(key, "must be an object or null")
			}
			result[key] = value
			changed["ap."+key] = value
		}
	}
	if raw, ok := typed["rf"]; ok {
		bands, isObject := raw.(map[string]any)
		if raw != nil && !isObject {
			errs.add("rf", "must be an object keyed by band")
		}
		rfPatch := map[string]any{}
		site := findSite(state, ap.SiteID)
		for band, value := range bands {
			if band != "2.4" && band != "5" {
				errs.add("rf."+band, `band must be "2.4" or "5"`)
				continue
			}
			if value == nil {
				rfPatch[band] = nil
				continue
			}
			override, isObject := value.(map[string]any)
			if !isObject {
				errs.add("rf."+band, "must be an object or null")
				continue
			}
			stored := map[string]any{}
			for key, field := range override {
				path := "rf." + band + "." + key
				switch key {
				case "disabled":
					if field == nil {
						stored["enabled"] = nil
						continue
					}
					disabled, isBool := field.(bool)
					if !isBool {
						errs.add(path, "must be true, false or null")
						continue
					}
					stored["enabled"] = !disabled
				case "channel", "width", "power_dbm":
					if field == nil {
						stored[key] = nil
						continue
					}
					number, isInt := asInt(field)
					if !isInt {
						errs.add(path, "must be a whole number or null")
						continue
					}
					stored[key] = number
				default:
					errs.add(path, "unknown field")
				}
			}
			if message, path := s.checkRadioOverride(state, site, ap, band, stored); message != "" {
				errs.add("rf."+band+"."+path, message)
			}
			rfPatch[band] = stored
			for key, field := range stored {
				changed["ap.rf."+band+"."+key] = jsonValue(field)
			}
		}
		if raw == nil {
			result["rf_override"] = nil
		} else {
			result["rf_override"] = rfPatch
		}
	}
	if bad := errs.err("AP settings"); bad != nil {
		return nil, bad
	}
	if bad := registryCheck(s.registry(), "ap", changed); bad != nil {
		return nil, bad
	}
	experimental := false
	if site := findSite(state, ap.SiteID); site != nil {
		experimental = siteExperimental(*site)
	}
	if keys := experimentalKeys(s.registry(), changed); len(keys) > 0 && !experimental {
		return nil, experimentalDisabled(keys)
	}
	return result, nil
}

// checkRadioOverride validates a static channel, width and power for the
// site's country (design §3.6 Radios, Appendix A, live findings: no 160
// MHz under AU/890).
func (s *Server) checkRadioOverride(state model.State, site *model.Site, ap model.APRecord, band string, stored map[string]any) (string, string) {
	width, _ := asInt(stored["width"])
	channel, _ := asInt(stored["channel"])
	power, hasPower := asInt(stored["power_dbm"])
	if hasPower && power != 0 && (power < 1 || power > 25) {
		return "transmit power must be 1 to 25 dBm", "power_dbm"
	}
	if width != 0 && !widthOffered(band, width) {
		return fmt.Sprintf("width %d MHz is not offered on %s GHz", width, band), "width"
	}
	if channel == 0 {
		return "", ""
	}
	if site == nil {
		return "a static channel needs the AP to be in a site", "channel"
	}
	if width == 0 {
		width = siteBandWidth(*site, band)
	}
	dfs, ok := validChannel(site.Country, band, width, channel)
	if !ok {
		return fmt.Sprintf("channel %d is not valid at %d MHz on %s GHz in %s", channel, width, band, firstNonEmpty(site.Country, "this country")), "channel"
	}
	if dfs && !siteBandDFS(*site, band) {
		return fmt.Sprintf("channel %d needs DFS; turn on Use DFS channels in the site's RF profile first", channel), "channel"
	}
	return "", ""
}

// deleteAP serves DELETE /api/aps/$id?purge_history=1: the record and its
// secret go, active sessions close with "AP removed" (the store does
// that), and with purge_history the AP's history goes too.
func (s *Server) deleteAP(writer http.ResponseWriter, request *http.Request, p params) {
	apID := p["ap"]
	state := s.store.Snapshot()
	current := findAP(state, apID)
	if current == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	if bad := ifMatch(request, current.UpdatedRev, false); bad != nil {
		staleWith(writer, bad, s.apView(s.apContext(state), *current))
		return
	}
	purge := request.URL.Query().Get("purge_history") == "1" || request.URL.Query().Get("purge_history") == "true"
	name := firstNonEmpty(current.Name, current.ID)
	out, err := s.run(mutation{
		sites: []string{current.SiteID}, dryRun: dryRun(request), request: request,
		kind: "ap.deleted", objectID: apID, historySite: current.SiteID,
		summary: "Deleted AP " + name, objects: []api.ChangeObject{{Kind: "ap", ID: apID, Name: current.Name}},
		change: func(state *model.State, tx *model.Tx) error {
			for index := range state.APs {
				if state.APs[index].ID == apID {
					state.APs = append(state.APs[:index], state.APs[index+1:]...)
					if tx != nil {
						return tx.APConfig().Delete(apID)
					}
					return nil
				}
			}
			return notFound("AP")
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	purged := false
	if !out.dryRun {
		if control := s.controlFor(apID); control != nil {
			control.DisconnectBecause("AP removed")
		}
		if purge {
			if purger, ok := s.store.(interface {
				PurgeAPHistory(siteID, apID string) error
			}); ok {
				if err := purger.PurgeAPHistory(current.SiteID, apID); err != nil {
					log.Printf("AP %s: purge history: %v", apID, err)
				} else {
					purged = true
				}
			}
		}
		log.Printf("audit: AP %s deleted by %s (purge history %v)", apID, principal(request), purged)
	}
	respondMutation(writer, http.StatusOK, out, api.APDeleted{ID: apID, Revision: out.revision, PurgedHistory: purged})
}

// moveAP serves the legacy PUT /api/aps/$id/site.
func (s *Server) moveAP(writer http.ResponseWriter, request *http.Request, p params) {
	var input struct {
		SiteID string `json:"site_id"`
	}
	if err := decodeJSON(request, 64*1024, &input, "site assignment"); err != nil {
		writeError(writer, err)
		return
	}
	if input.SiteID != "" && !s.siteExists(input.SiteID) {
		writeAPIError(writer, notFound("site").withField("site_id", "no such site"))
		return
	}
	apID := p["ap"]
	state := s.store.Snapshot()
	current := findAP(state, apID)
	if current == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	if current.SiteID == input.SiteID {
		respond(writer, http.StatusOK, map[string]any{"id": apID, "site_id": input.SiteID, "revision": state.Revision})
		return
	}
	oldSite := current.SiteID
	target := input.SiteID
	var extra []compile.Push
	_, err := s.run(mutation{
		sites: []string{oldSite, target}, request: request, extraPushes: &extra, planInitial: true, kind: "ap.updated", objectID: apID,
		historySite: oldSite, summary: "Moved AP " + firstNonEmpty(current.Name, apID),
		change: func(state *model.State, tx *model.Tx) error {
			ap := findAP(*state, apID)
			if ap == nil {
				return notFound("AP")
			}
			if ap.SiteID != oldSite {
				return conflict("the AP moved to another site; retry")
			}
			ap.SiteID = target
			if target == "" {
				return s.unassign(state, tx, ap, &extra)
			}
			return nil
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respond(writer, http.StatusOK, map[string]any{"id": apID, "site_id": input.SiteID, "previous_site_id": oldSite, "revision": s.store.Snapshot().Revision})
}

// apTelemetryHandler serves GET /api/aps/$id/telemetry.
func (s *Server) apTelemetryHandler(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	ap := findAP(state, p["ap"])
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	respond(writer, http.StatusOK, s.apTelemetry(*ap))
}

// apClientsHandler serves GET /api/aps/$id/clients (the site client list
// filtered to the AP).
func (s *Server) apClientsHandler(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	ap := findAP(state, p["ap"])
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	respond(writer, http.StatusOK, s.liveClients(state, ap.SiteID, ap.ID, s.clientMeta()))
}

// apEventsHandler serves GET /api/aps/$id/events (the site events filtered
// to the AP).
func (s *Server) apEventsHandler(writer http.ResponseWriter, request *http.Request, p params) {
	state := s.store.Snapshot()
	ap := findAP(state, p["ap"])
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	query := request.URL.Query()
	query.Set("ap", ap.ID)
	request.URL.RawQuery = query.Encode()
	s.listEvents(writer, request, params{"site": ap.SiteID})
}

// apLogHandler serves GET /api/aps/$id/log: the bounded kernel-log ring
// (24.1, phase 4).
func (s *Server) apLogHandler(writer http.ResponseWriter, _ *http.Request, p params) {
	state := s.store.Snapshot()
	ap := findAP(state, p["ap"])
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	view := api.APLog{APID: ap.ID, Lines: []api.APLogLine{}}
	for _, line := range s.observe(ap.ID).telemetry.KernelLog {
		view.Lines = append(view.Lines, api.APLogLine{At: nil, Text: line})
	}
	respond(writer, http.StatusOK, view)
}
