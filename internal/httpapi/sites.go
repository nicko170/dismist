package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
)

// listSites serves GET /api/sites: the bare SiteSummary array (the legacy
// app gets {sites, revision}).
func (s *Server) listSites(writer http.ResponseWriter, request *http.Request, _ params) {
	state := s.store.Snapshot()
	if legacyClient(request) {
		s.legacySiteList(writer, state)
		return
	}
	ctx := s.apContext(state)
	summaries := make([]api.SiteSummary, 0, len(state.Sites))
	for _, site := range state.Sites {
		summaries = append(summaries, s.siteSummary(ctx, site))
	}
	respond(writer, http.StatusOK, summaries)
}

// pendingCount counts the pending APs that are not dismissed.
func (s *Server) pendingCount() int {
	if s.manager == nil {
		return 0
	}
	dismissed := s.dismissedPending()
	count := 0
	for _, entry := range s.manager.Pending() {
		if entry.Reason == "unregistered" && dismissed[entry.ID].IsZero() {
			count++
		}
	}
	return count
}

func (s *Server) siteSummary(ctx apContext, site model.Site) api.SiteSummary {
	summary := api.SiteSummary{ID: site.ID, Name: site.Name, Country: site.Country, Timezone: site.Timezone}
	for _, ap := range ctx.state.APs {
		if ap.SiteID != site.ID {
			continue
		}
		summary.Counts.APs++
		obs := s.observe(ap.ID)
		if obs.status.Connected {
			summary.Counts.APsOnline++
			if obs.fresh(ap.ConfigUpdatedAt) {
				summary.Counts.Clients += len(obs.telemetry.AssociatedClients)
			}
		}
	}
	summary.Counts.WLANs = len(compile.NetworksForSite(ctx.state, site.ID))
	summary.Counts.PendingAP = s.pendingCount()
	return summary
}

// siteView builds GET /api/sites/$id (design §4.9 SiteView).
func (s *Server) siteView(ctx apContext, site model.Site) api.SiteView {
	view := api.SiteView{SiteSummary: s.siteSummary(ctx, site), Experimental: siteExperimental(site), UpdatedRev: site.UpdatedRev}
	if ccrev, ok := regdomain.CCRev(site.Country); ok {
		view.CCRev = &ccrev
	}
	view.SiteSettings = s.siteSettings(ctx, site)
	return view
}

// siteRecord is the typed part of a site record (design §4.3 keys).
type siteRecord struct {
	SSH *struct {
		Enabled      bool           `json:"enabled"`
		Keys         []storedSSHKey `json:"keys"`
		ReadonlyKeys []storedSSHKey `json:"readonly_keys"`
	} `json:"ssh"`
	Syslog       []api.SyslogTarget  `json:"syslog"`
	Management   *api.ManagementView `json:"management"`
	APDefaults   *api.APDefaultsView `json:"ap_defaults"`
	Protection   *api.ProtectionView `json:"protection"`
	Experimental bool                `json:"experimental"`
	Legacy       []string            `json:"ssh_public_keys"`
}

// storedSSHKey is model.SSHKey ({Key, Comment}; JSON matching is
// case-insensitive).
type storedSSHKey struct {
	Key     string `json:"key"`
	Comment string `json:"comment"`
}

func readSiteRecord(site model.Site) siteRecord {
	var record siteRecord
	viewFrom(site, &record)
	return record
}

func siteExperimental(site model.Site) bool { return readSiteRecord(site).Experimental }

// siteSSHKeys returns the site's root SSH keys as authorized_keys lines:
// the typed ssh.keys when present, else the legacy ssh_public_keys.
func siteSSHKeys(site model.Site) []string {
	record := readSiteRecord(site)
	if record.SSH != nil && len(record.SSH.Keys) > 0 {
		lines := make([]string, 0, len(record.SSH.Keys))
		for _, key := range record.SSH.Keys {
			lines = append(lines, strings.TrimSpace(key.Key+" "+key.Comment))
		}
		return lines
	}
	return record.Legacy
}

// sshKeyView parses an authorized_keys line for the view.
func sshKeyView(line, comment string) api.SSHKeyView {
	view := api.SSHKeyView{Key: line, Comment: comment}
	if parsed, parsedComment, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err == nil {
		view.Type = parsed.Type()
		view.Fingerprint = ssh.FingerprintSHA256(parsed)
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			view.Key = fields[0] + " " + fields[1]
		}
		if view.Comment == "" {
			view.Comment = parsedComment
		}
	}
	return view
}

// siteSettings builds the typed part of SiteView. In legacy mode the
// values are what the record carries (defaults where it carries nothing)
// and Typed is false.
func (s *Server) siteSettings(ctx apContext, site model.Site) api.SiteSettings {
	record := readSiteRecord(site)
	settings := api.SiteSettings{
		SSH:        api.SiteSSHView{Keys: []api.SSHKeyView{}, ReadonlyKeys: []api.SSHKeyView{}},
		Syslog:     []api.SyslogTarget{},
		Management: api.ManagementView{VLANID: 1, DNS: []string{}, NTP: []string{}},
		APDefaults: api.APDefaultsView{LEDBrightness: 255, PoEConstrained: "disable_radios"},
		Protection: api.ProtectionView{BlockedClients: []api.BlockedClient{}},
		Typed:      s.typedEnabled(),
	}
	if record.SSH != nil {
		settings.SSH.Enabled = record.SSH.Enabled
		for _, key := range record.SSH.Keys {
			settings.SSH.Keys = append(settings.SSH.Keys, sshKeyView(key.Key, key.Comment))
		}
		for _, key := range record.SSH.ReadonlyKeys {
			settings.SSH.ReadonlyKeys = append(settings.SSH.ReadonlyKeys, sshKeyView(key.Key, key.Comment))
		}
	} else {
		for _, line := range record.Legacy {
			settings.SSH.Keys = append(settings.SSH.Keys, sshKeyView(line, ""))
		}
		settings.SSH.Enabled = len(record.Legacy) > 0
	}
	if record.Syslog != nil {
		settings.Syslog = record.Syslog
	}
	if record.Management != nil {
		settings.Management = *record.Management
		if settings.Management.VLANID == 0 {
			settings.Management.VLANID = 1
		}
		if settings.Management.DNS == nil {
			settings.Management.DNS = []string{}
		}
		if settings.Management.NTP == nil {
			settings.Management.NTP = []string{}
		}
	}
	if record.APDefaults != nil {
		settings.APDefaults = *record.APDefaults
	}
	if record.Protection != nil {
		settings.Protection = *record.Protection
		if settings.Protection.BlockedClients == nil {
			settings.Protection.BlockedClients = []api.BlockedClient{}
		}
	}
	settings.RF = s.rfProfileView(ctx, site)
	settings.Expert = s.expertSummary(ctx.state, api.ExpertScopeSite, site.ID, site)
	settings.ExperimentalInUse = s.experimentalInUse("site", site, []string{"ssh", "syslog", "management", "ap_defaults", "protection", "rf"})
	return settings
}

// experimentalInUse lists the option keys whose stored value is
// experimental (design §3.11 Advanced).
func (s *Server) experimentalInUse(root string, record any, keys []string) []string {
	object, err := recordMap(record)
	result := []string{}
	if err != nil {
		return result
	}
	typed := map[string]any{}
	for _, key := range keys {
		if value, ok := object[key]; ok {
			typed[key] = value
		}
	}
	result = append(result, experimentalKeys(s.registry(), optionValues(root, typed), nil)...)
	return result
}

// getSite serves GET /api/sites/$id.
func (s *Server) getSite(writer http.ResponseWriter, request *http.Request, p params) {
	state := s.store.Snapshot()
	site := findSite(state, p["site"])
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	if legacyClient(request) {
		respond(writer, http.StatusOK, map[string]any{"site": legacySiteView(*site, s.expertField())})
		return
	}
	respond(writer, http.StatusOK, s.siteView(s.apContext(state), *site))
}

// createSite serves POST /api/sites {name, country, timezone}. Once schema
// 2 has committed, a new site gets the research defaults (design §4.6).
func (s *Server) createSite(writer http.ResponseWriter, request *http.Request, _ params) {
	var input api.SiteInput
	if err := decodeJSON(request, 64*1024, &input, "site"); err != nil {
		writeError(writer, err)
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 64 {
		writeAPIError(writer, invalidField("name", "site name must be 1 to 64 characters"))
		return
	}
	if err := validateSiteMetadata(&input.Country, &input.Timezone); err != nil {
		writeAPIError(writer, err)
		return
	}
	if s.siteNameTaken(s.store.Snapshot(), name, "") {
		writeAPIError(writer, conflict("a site with this name exists").withField("name", "site names must be unique"))
		return
	}
	id, err := randomID()
	if err != nil {
		writeAPIError(writer, internalError("cannot create site"))
		return
	}
	site := model.Site{ID: id, Name: name, Country: strings.ToUpper(strings.TrimSpace(input.Country)), Timezone: input.Timezone}
	if s.typedEnabled() {
		s.applyNewSiteDefaults(&site)
	}
	out, err := s.run(mutation{
		dryRun: dryRun(request), request: request, kind: "site.created", objectID: id, historySite: id,
		summary: "Created site " + name, objects: []api.ChangeObject{{Kind: "site", ID: id, Name: name}},
		change: func(state *model.State, _ *model.Tx) error {
			if s.siteNameTaken(*state, name, "") {
				return conflict("a site with this name exists").withField("name", "site names must be unique")
			}
			state.Sites = append(state.Sites, site)
			return nil
		},
		sites: []string{id},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	created := findSite(out.state, id)
	if created == nil {
		writeAPIError(writer, internalError("the site was not stored"))
		return
	}
	if legacyClient(request) {
		respond(writer, http.StatusCreated, map[string]any{"site": *created})
		return
	}
	respondMutation(writer, http.StatusCreated, out, s.siteView(s.apContext(out.state), *created))
}

func (s *Server) siteNameTaken(state model.State, name, except string) bool {
	for _, site := range state.Sites {
		if site.ID != except && strings.EqualFold(strings.TrimSpace(site.Name), name) {
			return true
		}
	}
	return false
}

// applyNewSiteDefaults fills a site created after schema 2 with the
// research defaults (design §4.6 "Default for new objects", workstream A's
// compile.NewSiteDefaults): RF managed with the country's regulatory table
// when dismist has one, Auto channels and power, new-object AP defaults.
func (s *Server) applyNewSiteDefaults(site *model.Site) {
	compile.NewSiteDefaults(site)
}

func setPath(object map[string]any, path []string, value any) {
	for index, part := range path {
		if index == len(path)-1 {
			object[part] = value
			return
		}
		child, ok := object[part].(map[string]any)
		if !ok {
			child = map[string]any{}
			object[part] = child
		}
		object = child
	}
}

func cloneJSON(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	clone, _ := decodeObject(raw)
	return clone
}

var (
	siteLegacyKeys = map[string]bool{"name": true, "country": true, "timezone": true}
	siteTypedKeys  = map[string]bool{"experimental": true, "ssh": true, "syslog": true, "management": true,
		"ap_defaults": true, "protection": true, "rf": true, "roaming": true}
	siteReadOnly = func() map[string]bool {
		keys := jsonKeys(api.SiteView{})
		for key := range siteLegacyKeys {
			delete(keys, key)
		}
		for key := range siteTypedKeys {
			delete(keys, key)
		}
		return keys
	}()
)

// patchSite serves PATCH /api/sites/$id (design §4.9) and the legacy PUT
// alias (metadata only).
func (s *Server) patchSite(writer http.ResponseWriter, request *http.Request, p params) {
	s.updateSite(writer, request, p["site"], true)
}

func (s *Server) putSite(writer http.ResponseWriter, request *http.Request, p params) {
	s.updateSite(writer, request, p["site"], false)
}

func (s *Server) updateSite(writer http.ResponseWriter, request *http.Request, siteID string, v1 bool) {
	patch, err := readPatch(request, 1<<20, "site")
	if err != nil {
		writeError(writer, err)
		return
	}
	legacyKeys := map[string]bool{"name": true, "country": true, "timezone": true}
	// SSH keys stay editable in legacy mode (design §4.7 step 7).
	if sshPatch, ok := patch["ssh"].(map[string]any); ok && !s.typedEnabled() {
		onlyKeys := true
		for key := range sshPatch {
			if key != "keys" {
				onlyKeys = false
			}
		}
		if onlyKeys {
			legacyKeys["ssh"] = true
		}
	}
	readOnly := siteReadOnly
	if !v1 {
		readOnly = map[string]bool{}
	}
	if bad := checkKeys(patch, siteTypedKeys, legacyKeys, readOnly, s.typedEnabled()); bad != nil {
		writeAPIError(writer, bad)
		return
	}
	if len(patch) == 0 {
		writeAPIError(writer, invalid("site fields required"))
		return
	}
	state := s.store.Snapshot()
	current := findSite(state, siteID)
	if current == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	if v1 {
		if bad := ifMatch(request, current.UpdatedRev, false); bad != nil {
			staleWith(writer, bad, s.siteView(s.apContext(state), *current))
			return
		}
	}
	edit, bad := s.parseSitePatch(state, *current, patch)
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, planInitial: edit.rf,
		kind: "site.updated", objectID: siteID, historySite: siteID, summary: edit.summary(*current),
		objects: []api.ChangeObject{{Kind: "site", ID: siteID, Name: current.Name}},
		change: func(state *model.State, _ *model.Tx) error {
			site := findSite(*state, siteID)
			if site == nil {
				return notFound("site")
			}
			if edit.name != nil && s.siteNameTaken(*state, *edit.name, siteID) {
				return conflict("a site with this name exists").withField("name", "site names must be unique")
			}
			if err := edit.apply(site); err != nil {
				return err
			}
			if edit.rotateRoamingKey {
				return compile.RotateRoamingKey(state, siteID)
			}
			return nil
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	site := findSite(out.state, siteID)
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	if !v1 || legacyClient(request) {
		respond(writer, http.StatusOK, map[string]any{"site": legacySiteView(*site, s.expertField()), "revision": out.revision})
		return
	}
	respondMutation(writer, http.StatusOK, out, s.siteView(s.apContext(out.state), *site))
}

// siteEdit is a parsed site patch.
type siteEdit struct {
	name, country, timezone *string
	legacyKeys              []string
	typed                   map[string]any
	rf                      bool
	// rotateRoamingKey replaces the site's 802.11r/OKC roaming key
	// ("roaming": {"rotate_key": true}, design §3.11).
	rotateRoamingKey bool
	parts            []string
}

func (e siteEdit) summary(site model.Site) string {
	return "Site " + site.Name + ": " + strings.Join(e.parts, ", ")
}

func (e siteEdit) apply(site *model.Site) error {
	if e.name != nil {
		site.Name = *e.name
	}
	if e.country != nil {
		site.Country = *e.country
	}
	if e.timezone != nil {
		site.Timezone = *e.timezone
	}
	if e.legacyKeys != nil {
		raw, _ := json.Marshal(e.legacyKeys)
		if len(e.legacyKeys) == 0 {
			raw = nil
		}
		if err := setRecordKey(site, "ssh_public_keys", raw); err != nil {
			return err
		}
	}
	if len(e.typed) > 0 {
		if err := patchRecord(site, e.typed); err != nil {
			var unsupported *errUnsupported
			if errors.As(err, &unsupported) {
				return unsupportedField(unsupported.path)
			}
			return err
		}
	}
	return nil
}

func (s *Server) parseSitePatch(state model.State, site model.Site, patch map[string]any) (siteEdit, *apiError) {
	var edit siteEdit
	var errs fieldErrors
	str := func(key string) *string {
		value, ok := patch[key]
		if !ok {
			return nil
		}
		text, isString := value.(string)
		if value != nil && !isString {
			errs.add(key, "must be a JSON string")
			return nil
		}
		return &text
	}
	if name := str("name"); name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" || len(trimmed) > 64 {
			errs.add("name", "site name must be 1 to 64 characters")
		}
		edit.name = &trimmed
		edit.parts = append(edit.parts, "renamed to "+trimmed)
	}
	if country := str("country"); country != nil {
		upper := strings.ToUpper(strings.TrimSpace(*country))
		if !validCountry(upper) {
			errs.add("country", "country must be a two-letter ISO 3166 code")
		} else if s.typedEnabled() && siteRFManaged(site) && upper != "" {
			if _, ok := regdomain.CCRev(upper); !ok {
				errs.add("country", "dismist has no verified regulatory table for "+upper+" yet (supported: "+strings.Join(regdomain.Countries(), ", ")+")")
			}
		}
		edit.country = &upper
		edit.parts = append(edit.parts, "country "+upper)
	}
	if timezone := str("timezone"); timezone != nil {
		if !validTimezone(*timezone) {
			errs.add("timezone", "unknown timezone; use an IANA name such as Australia/Sydney")
		}
		edit.timezone = timezone
		edit.parts = append(edit.parts, "timezone "+*timezone)
	}
	if bad := errs.err("site"); bad != nil {
		return edit, bad
	}
	typed := map[string]any{}
	for key := range siteTypedKeys {
		if value, ok := patch[key]; ok {
			typed[key] = value
		}
	}
	if roaming, ok := typed["roaming"]; ok {
		delete(typed, "roaming")
		object, isObject := roaming.(map[string]any)
		rotate, isBool := object["rotate_key"].(bool)
		if !isObject || len(object) != 1 || !isBool {
			return edit, invalidField("roaming", `roaming takes {"rotate_key": true}`)
		}
		if rotate {
			edit.rotateRoamingKey = true
			edit.parts = append(edit.parts, "roaming key rotated")
		}
	}
	if sshPatch, ok := typed["ssh"].(map[string]any); ok {
		converted, legacyKeys, bad := s.parseSSHPatch(site, sshPatch)
		if bad != nil {
			return edit, bad
		}
		if !s.typedEnabled() {
			edit.legacyKeys = legacyKeys
			delete(typed, "ssh")
		} else {
			typed["ssh"] = converted
		}
		edit.parts = append(edit.parts, "SSH changed")
	}
	if rf, ok := typed["rf"]; ok {
		converted, bad := s.parseRFPatch(state, site, rf)
		if bad != nil {
			return edit, bad
		}
		typed["rf"] = converted
		edit.rf = true
	}
	if protection, ok := typed["protection"].(map[string]any); ok {
		if bad := normaliseBlockedClients(protection); bad != nil {
			return edit, bad
		}
	}
	if bad := validateSiteTyped(typed); bad != nil {
		return edit, bad
	}
	changed := optionValues("site", typed)
	if bad := registryCheck(s.registry(), "site", changed); bad != nil {
		return edit, bad
	}
	experimental := siteExperimental(site)
	if value, ok := typed["experimental"].(bool); ok {
		experimental = value
	}
	currentValues := map[string]any{}
	if object, err := recordMap(site); err == nil {
		currentValues = optionValues("site", object)
	}
	if keys := experimentalKeys(s.registry(), changed, currentValues); len(keys) > 0 && !experimental {
		return edit, experimentalDisabled(keys)
	}
	for key := range typed {
		if key != "ssh" && key != "rf" {
			edit.parts = append(edit.parts, key+" changed")
		} else if key == "rf" {
			edit.parts = append(edit.parts, "RF profile changed")
		}
	}
	if len(typed) > 0 {
		edit.typed = typed
	}
	if len(edit.parts) == 0 {
		edit.parts = []string{"updated"}
	}
	sort.Strings(edit.parts)
	return edit, nil
}

// parseSSHPatch validates ssh.keys and ssh.readonly_keys (authorized_keys
// lines, comments kept) and returns the typed record patch and, for legacy
// mode, the normalised legacy key list.
func (s *Server) parseSSHPatch(site model.Site, patch map[string]any) (map[string]any, []string, *apiError) {
	result := map[string]any{}
	var legacyKeys []string
	for key, value := range patch {
		switch key {
		case "enabled":
			if _, ok := value.(bool); !ok && value != nil {
				return nil, nil, invalidField("ssh.enabled", "must be true or false")
			}
			result[key] = value
		case "keys", "readonly_keys":
			var lines []string
			if value != nil {
				if err := remarshal(value, &lines); err != nil {
					return nil, nil, invalidField("ssh."+key, "must be an array of OpenSSH public key lines")
				}
			}
			if len(lines) > 32 {
				return nil, nil, invalidField("ssh."+key, "at most 32 keys")
			}
			normalised, err := model.NormalizeSSHPublicKeys(lines)
			if err != nil {
				return nil, nil, invalidField("ssh."+key, err.Error())
			}
			stored := make([]any, 0, len(lines))
			seen := map[string]bool{}
			for _, line := range lines {
				parsed, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
				if err != nil {
					continue
				}
				blob := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))
				if seen[blob] {
					continue
				}
				seen[blob] = true
				entry := map[string]any{"key": blob}
				if comment != "" {
					entry["comment"] = comment
				}
				stored = append(stored, entry)
			}
			result[key] = stored
			if key == "keys" {
				legacyKeys = normalised
				if legacyKeys == nil {
					legacyKeys = []string{}
				}
			}
		default:
			return nil, nil, invalidField("ssh."+key, "unknown field")
		}
	}
	if enabled, ok := result["enabled"].(bool); ok && enabled {
		keys, hasKeys := result["keys"].([]any)
		if !hasKeys && len(siteSSHKeys(site)) == 0 || hasKeys && len(keys) == 0 {
			return nil, nil, newError(http.StatusUnprocessableEntity, api.ErrorCodeInvalid, "SSH needs at least one key").withField("ssh.enabled", "add a key first")
		}
	}
	if readonly, ok := result["readonly_keys"].([]any); ok {
		root := map[string]bool{}
		for _, line := range siteSSHKeys(site) {
			if parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err == nil {
				root[strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))] = true
			}
		}
		for _, item := range readonly {
			if entry, ok := item.(map[string]any); ok && root[fmt.Sprint(entry["key"])] {
				return nil, nil, invalidField("ssh.readonly_keys", "a read-only key must not also be a root key")
			}
		}
	}
	return result, legacyKeys, nil
}

// normaliseBlockedClients rewrites protection.blocked_clients MACs to the
// lower-case dashed form.
func normaliseBlockedClients(protection map[string]any) *apiError {
	list, ok := protection["blocked_clients"].([]any)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	result := make([]any, 0, len(list))
	for index, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return invalidField(fmt.Sprintf("protection.blocked_clients[%d]", index), "must be {mac, note}")
		}
		mac := normaliseMAC(fmt.Sprint(entry["mac"]))
		if mac == "" {
			return invalidField(fmt.Sprintf("protection.blocked_clients[%d].mac", index), "invalid MAC address")
		}
		if seen[mac] {
			continue
		}
		seen[mac] = true
		clean := map[string]any{"mac": mac}
		if note, _ := entry["note"].(string); note != "" {
			clean["note"] = note
		}
		result = append(result, clean)
	}
	protection["blocked_clients"] = result
	return nil
}

// validateSiteTyped checks the typed site fields (design §3.11).
func validateSiteTyped(typed map[string]any) *apiError {
	var errs fieldErrors
	errs.status = http.StatusUnprocessableEntity
	if management, ok := typed["management"].(map[string]any); ok {
		if vlan, present := management["vlan_id"]; present && vlan != nil {
			if number, isInt := asInt(vlan); !isInt || (number != 1 && (number < 2 || number > 4094)) {
				errs.add("management.vlan_id", "management VLAN must be untagged (1) or 2 to 4094")
			}
		}
		if list, ok := management["ntp"].([]any); ok {
			if len(list) > 6 {
				errs.add("management.ntp", "at most 6 NTP servers")
			}
			for _, item := range list {
				if text, _ := item.(string); !hostOrIP(text) {
					errs.add("management.ntp", fmt.Sprintf("%q is not a hostname or IP", text))
				}
			}
		}
		if list, ok := management["dns"].([]any); ok {
			if len(list) > 3 {
				errs.add("management.dns", "at most 3 DNS servers")
			}
			for _, item := range list {
				if text, _ := item.(string); !hostOrIP(text) || strings.ContainsAny(text, "abcdefghijklmnopqrstuvwxyz") && !strings.Contains(text, ":") {
					errs.add("management.dns", fmt.Sprintf("%q is not an IP address", text))
				}
			}
		}
	}
	if defaults, ok := typed["ap_defaults"].(map[string]any); ok {
		if value, present := defaults["led_brightness"]; present && value != nil {
			if number, isInt := asInt(value); !isInt || number < 0 || number > 255 {
				errs.add("ap_defaults.led_brightness", "LED brightness must be 0 to 255")
			}
		}
		if value, present := defaults["poe_constrained"]; present && value != nil {
			if value != "keep_radios" && value != "disable_radios" {
				errs.add("ap_defaults.poe_constrained", `must be "keep_radios" or "disable_radios"`)
			}
		}
		if recovery, ok := defaults["config_recovery"].(map[string]any); ok {
			if minutes, present := recovery["minutes"]; present {
				if number, isInt := asInt(minutes); !isInt || number < 1 || number > 60 {
					errs.add("ap_defaults.config_recovery.minutes", "recovery time must be 1 to 60 minutes")
				}
			}
		}
	}
	if list, ok := typed["syslog"].([]any); ok {
		if len(list) > 16 {
			errs.add("syslog", "at most 16 syslog targets")
		}
		for index, item := range list {
			var target api.SyslogTarget
			if remarshal(item, &target) != nil {
				errs.add(fmt.Sprintf("syslog[%d]", index), "must be {protocol, host, port, facility, severity, tag}")
				continue
			}
			path := fmt.Sprintf("syslog[%d]", index)
			if target.Protocol != "udp" && target.Protocol != "tcp" {
				errs.add(path+".protocol", `protocol must be "udp" or "tcp"`)
			}
			if !hostOrIP(target.Host) {
				errs.add(path+".host", "host must be a hostname or IP")
			}
			if target.Port < 1 || target.Port > 65535 {
				errs.add(path+".port", "port must be 1 to 65535")
			}
			if target.Facility < 0 || target.Facility > 23 {
				errs.add(path+".facility", "facility must be 0 to 23")
			}
			if target.Severity < 0 || target.Severity > 7 {
				errs.add(path+".severity", "severity must be 0 to 7")
			}
		}
	}
	if protection, ok := typed["protection"].(map[string]any); ok {
		if box, ok := protection["penalty_box"].(map[string]any); ok {
			threshold, _ := asInt(box["threshold"])
			window, _ := asInt(box["window_sec"])
			if threshold < 1 || window < 1 {
				errs.add("protection.penalty_box", "threshold and window_sec must be positive")
			}
		}
		if list, ok := protection["blocked_clients"].([]any); ok && len(list) > 256 {
			errs.add("protection.blocked_clients", "at most 256 blocked clients")
		}
	}
	return errs.err("site settings")
}

// deleteSite serves DELETE /api/sites/$id: refused (409) while APs are
// assigned; the site's WLANs go with it (design §3.11 Danger).
func (s *Server) deleteSite(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	state := s.store.Snapshot()
	site := findSite(state, siteID)
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	if bad := ifMatch(request, site.UpdatedRev, false); bad != nil {
		staleWith(writer, bad, s.siteView(s.apContext(state), *site))
		return
	}
	if count := len(siteAPs(state, siteID)); count > 0 {
		writeAPIError(writer, conflict(fmt.Sprintf("move or delete its %s first", plural(count, "AP"))).withField("aps", fmt.Sprintf("%d assigned", count)))
		return
	}
	name := site.Name
	out, err := s.run(mutation{
		sites: []string{siteID}, dryRun: dryRun(request), request: request, kind: "site.deleted", objectID: siteID,
		summary: "Deleted site " + name,
		change: func(state *model.State, _ *model.Tx) error {
			for _, ap := range state.APs {
				if ap.SiteID == siteID {
					return conflict("the site has APs again; retry")
				}
			}
			sites := state.Sites[:0]
			for _, candidate := range state.Sites {
				if candidate.ID != siteID {
					sites = append(sites, candidate)
				}
			}
			state.Sites = sites
			networks := state.Networks[:0]
			for _, network := range state.Networks {
				if network.SiteID != siteID {
					networks = append(networks, network)
				}
			}
			state.Networks = networks
			return nil
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if !out.dryRun {
		if purger, ok := s.store.(interface{ PurgeSiteHistory(siteID string) error }); ok {
			if err := purger.PurgeSiteHistory(siteID); err != nil {
				log.Printf("site %s: purge history: %v", siteID, err)
			}
		}
		log.Printf("audit: site %s (%s) deleted by %s", siteID, name, principal(request))
	}
	respondMutation(writer, http.StatusOK, out, api.SiteDeleted{ID: siteID, Revision: out.revision})
}

// sshKeysGet serves the legacy GET /api/sites/$id/ssh-keys.
func (s *Server) sshKeysGet(writer http.ResponseWriter, _ *http.Request, p params) {
	site := findSite(s.store.Snapshot(), p["site"])
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	keys := siteSSHKeys(*site)
	if keys == nil {
		keys = []string{}
	}
	respond(writer, http.StatusOK, map[string]any{"keys": keys})
}

// sshKeysPut serves the legacy PUT /api/sites/$id/ssh-keys {keys}: an alias
// of PATCH {ssh: {keys}} that answers {keys, revision}.
func (s *Server) sshKeysPut(writer http.ResponseWriter, request *http.Request, p params) {
	siteID := p["site"]
	var input struct {
		Keys []string `json:"keys"`
	}
	if err := decodeJSON(request, 64*1024, &input, "SSH public keys"); err != nil {
		writeError(writer, err)
		return
	}
	if input.Keys == nil {
		writeAPIError(writer, invalidField("keys", "SSH public keys array required"))
		return
	}
	keys, err := model.NormalizeSSHPublicKeys(input.Keys)
	if err != nil {
		writeAPIError(writer, invalidField("keys", err.Error()))
		return
	}
	state := s.store.Snapshot()
	site := findSite(state, siteID)
	if site == nil {
		writeAPIError(writer, notFound("site"))
		return
	}
	edit, bad := s.parseSitePatch(state, *site, map[string]any{"ssh": map[string]any{"keys": toAnySlice(input.Keys)}})
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	_, err = s.run(mutation{
		sites: []string{siteID}, request: request, kind: "site.updated", objectID: siteID, historySite: siteID,
		summary: "Site " + site.Name + ": SSH keys changed",
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
	if keys == nil {
		keys = []string{}
	}
	respond(writer, http.StatusOK, map[string]any{"keys": keys, "revision": s.store.Snapshot().Revision})
}

func toAnySlice(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

// validateSiteMetadata checks the country and timezone that are present.
func validateSiteMetadata(country, timezone *string) *apiError {
	var bad *apiError
	field := func(path, message string) {
		if bad == nil {
			bad = invalid("invalid site metadata: " + message)
		}
		bad.withField(path, message)
	}
	if country != nil && !validCountry(*country) {
		field("country", "country must be a two-letter ISO 3166 code")
	}
	if timezone != nil && !validTimezone(*timezone) {
		field("timezone", "unknown timezone; use an IANA name such as Australia/Sydney")
	}
	return bad
}

func validCountry(country string) bool {
	country = strings.TrimSpace(country)
	if country == "" {
		return true
	}
	if len(country) != 2 {
		return false
	}
	for _, char := range country {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			return false
		}
	}
	return true
}

func validTimezone(timezone string) bool {
	if timezone == "" {
		return true
	}
	_, err := time.LoadLocation(timezone)
	return err == nil
}

func (s *Server) siteExists(id string) bool { return findSite(s.store.Snapshot(), id) != nil }

func (s *Server) currentSiteID() string {
	for _, ap := range s.store.Snapshot().APs {
		if ap.ID == s.apID {
			return ap.SiteID
		}
	}
	return ""
}

func redactPatch(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	redacted, err := redactJSON(raw)
	if err != nil {
		return map[string]any{}
	}
	return redacted
}

func redactJSON(raw []byte) (any, error) {
	value, err := decodeAny(raw)
	if err != nil {
		return nil, err
	}
	return redactValue(value), nil
}

func redactValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, child := range item {
			lower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			switch {
			case lower == "psk", lower == "keys", lower == "papiopaque",
				strings.Contains(lower, "password"), strings.Contains(lower, "passphrase"),
				strings.Contains(lower, "secret"), strings.Contains(lower, "token"),
				strings.Contains(lower, "credential"), strings.Contains(lower, "privatekey"),
				strings.Contains(lower, "authorizedkeys"), strings.HasSuffix(lower, "key"),
				strings.HasSuffix(lower, "keys"):
				result[key] = "[redacted]"
			default:
				result[key] = redactValue(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(item))
		for index, child := range item {
			result[index] = redactValue(child)
		}
		return result
	default:
		return value
	}
}
