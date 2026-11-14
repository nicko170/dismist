package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

type siteClient struct {
	telemetry.Client
	APID   string `json:"ap_id"`
	APName string `json:"ap_name"`
}

type siteClientActivity struct {
	controller.ClientActivity
	APID   string `json:"ap_id"`
	APName string `json:"ap_name"`
}

func (s *Server) listSites(writer http.ResponseWriter) {
	state := s.store.Snapshot()
	sites := make([]map[string]any, 0, len(state.Sites))
	for _, site := range state.Sites {
		apCount, networkCount := 0, 0
		for _, ap := range state.APs {
			if ap.SiteID == site.ID {
				apCount++
			}
		}
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

func (s *Server) createSite(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name     string `json:"name"`
		Country  string `json:"country"`
		Timezone string `json:"timezone"`
	}
	if err := decodeJSON(request, 64*1024, &input, "site"); err != nil {
		writeError(writer, err)
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		writeAPIError(writer, invalidField("name", "site name required"))
		return
	}
	if err := validateSiteMetadata(&input.Country, &input.Timezone); err != nil {
		writeAPIError(writer, err)
		return
	}
	id, err := randomID()
	if err != nil {
		writeAPIError(writer, internalError("cannot create site"))
		return
	}
	site := model.Site{ID: id, Name: strings.TrimSpace(input.Name), Country: strings.ToUpper(strings.TrimSpace(input.Country)), Timezone: input.Timezone}
	if err := s.store.Update(func(state *model.State) error {
		state.Sites = append(state.Sites, site)
		return nil
	}); err != nil {
		writeChangeError(writer, err)
		return
	}
	respond(writer, http.StatusCreated, map[string]any{"site": site})
}

func (s *Server) siteMutation(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "sites" || parts[3] != "networks" {
		routeNotFound(writer)
		return
	}
	if !s.siteExists(parts[2]) {
		writeAPIError(writer, notFound("site"))
		return
	}
	s.createNetwork(writer, request, parts[2])
}

func (s *Server) siteUpdate(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "sites" {
		routeNotFound(writer)
		return
	}
	if !s.siteExists(parts[2]) {
		writeAPIError(writer, notFound("site"))
		return
	}
	if len(parts) == 3 {
		s.updateSiteMetadata(writer, request, parts[2])
		return
	}
	if len(parts) == 4 && parts[3] == "ssh-keys" {
		s.updateSiteSSHKeys(writer, request, parts[2])
		return
	}
	if len(parts) == 5 && parts[3] == "networks" {
		s.editNetwork(writer, request, parts[2], parts[4])
		return
	}
	if parts[len(parts)-1] != "advanced" {
		routeNotFound(writer)
		return
	}
	patch, err := readAdvancedInput(request)
	if err != nil {
		writeError(writer, err)
		return
	}
	siteID := parts[2]
	err = s.applyChange(siteID, func(state *model.State) error {
		switch {
		case len(parts) == 4:
			for index := range state.Sites {
				if state.Sites[index].ID == siteID {
					return setAdvanced(&state.Sites[index].Advanced, patch)
				}
			}
		case len(parts) == 6 && parts[3] == "aps":
			for index := range state.APs {
				if state.APs[index].ID == parts[4] && state.APs[index].SiteID == siteID {
					return setAdvanced(&state.APs[index].Advanced, patch)
				}
			}
			return notFound("AP")
		case len(parts) == 6 && parts[3] == "networks":
			for index := range state.Networks {
				if state.Networks[index].ID == parts[4] && state.Networks[index].SiteID == siteID {
					return setAdvanced(&state.Networks[index].Advanced, patch)
				}
			}
			return notFound("network")
		}
		return newError(http.StatusNotFound, api.ErrorCodeNotFound, "no such endpoint")
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respond(writer, http.StatusOK, map[string]any{"revision": s.store.Snapshot().Revision})
}

func (s *Server) updateSiteSSHKeys(writer http.ResponseWriter, request *http.Request, siteID string) {
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
	err = s.applyChange(siteID, func(state *model.State) error {
		for index := range state.Sites {
			if state.Sites[index].ID == siteID {
				state.Sites[index].SSHPublicKeys = keys
				return nil
			}
		}
		return notFound("site")
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respond(writer, http.StatusOK, map[string]any{"keys": keys, "revision": s.store.Snapshot().Revision})
}

func setAdvanced(existing *json.RawMessage, patch json.RawMessage) error {
	if len(patch) == 0 {
		*existing = nil
		return nil
	}
	var original any
	if len(*existing) != 0 {
		decoder := json.NewDecoder(strings.NewReader(string(*existing)))
		decoder.UseNumber()
		if err := decoder.Decode(&original); err != nil {
			return err
		}
	}
	var incoming any
	decoder := json.NewDecoder(strings.NewReader(string(patch)))
	decoder.UseNumber()
	if err := decoder.Decode(&incoming); err != nil {
		return err
	}
	restored, err := restoreRedacted(original, incoming)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(restored)
	if err != nil {
		return err
	}
	*existing = encoded
	return nil
}

func restoreRedacted(original, incoming any) (any, error) {
	if value, ok := incoming.(string); ok && value == "[redacted]" {
		if original == nil {
			return nil, invalidField("patch", "a [redacted] value has no stored secret to keep")
		}
		return original, nil
	}
	switch value := incoming.(type) {
	case map[string]any:
		previous, _ := original.(map[string]any)
		result := make(map[string]any, len(value))
		for key, child := range value {
			restored, err := restoreRedacted(previous[key], child)
			if err != nil {
				return nil, err
			}
			result[key] = restored
		}
		return result, nil
	case []any:
		previous, _ := original.([]any)
		result := make([]any, len(value))
		for index, child := range value {
			var prior any
			if index < len(previous) {
				prior = previous[index]
			}
			restored, err := restoreRedacted(prior, child)
			if err != nil {
				return nil, err
			}
			result[index] = restored
		}
		return result, nil
	default:
		return incoming, nil
	}
}

func (s *Server) updateSiteMetadata(writer http.ResponseWriter, request *http.Request, siteID string) {
	var input struct {
		Name     *string `json:"name"`
		Country  *string `json:"country"`
		Timezone *string `json:"timezone"`
	}
	if err := decodeJSON(request, 64*1024, &input, "site"); err != nil {
		writeError(writer, err)
		return
	}
	if input.Name == nil && input.Country == nil && input.Timezone == nil {
		writeAPIError(writer, invalid("site fields required: name, country or timezone"))
		return
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		writeAPIError(writer, invalidField("name", "site name required"))
		return
	}
	if err := validateSiteMetadata(input.Country, input.Timezone); err != nil {
		writeAPIError(writer, err)
		return
	}
	err := s.applyChange(siteID, func(state *model.State) error {
		for index := range state.Sites {
			if state.Sites[index].ID != siteID {
				continue
			}
			if input.Name != nil {
				state.Sites[index].Name = strings.TrimSpace(*input.Name)
			}
			if input.Country != nil {
				state.Sites[index].Country = strings.ToUpper(strings.TrimSpace(*input.Country))
			}
			if input.Timezone != nil {
				state.Sites[index].Timezone = *input.Timezone
			}
			return nil
		}
		return notFound("site")
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	for _, site := range s.store.Snapshot().Sites {
		if site.ID == siteID {
			respond(writer, http.StatusOK, map[string]any{"site": siteView(site), "revision": s.store.Snapshot().Revision})
			return
		}
	}
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
		if char < 'A' || char > 'Z' {
			if char < 'a' || char > 'z' {
				return false
			}
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

func (s *Server) siteDelete(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "sites" || parts[3] != "networks" {
		routeNotFound(writer)
		return
	}
	if !s.siteExists(parts[2]) {
		writeAPIError(writer, notFound("site"))
		return
	}
	s.removeNetwork(writer, parts[2], parts[4])
}

func readAdvancedInput(request *http.Request) (json.RawMessage, error) {
	var input struct {
		Patch json.RawMessage `json:"patch"`
	}
	if err := decodeJSON(request, 1<<20, &input, "advanced patch"); err != nil {
		return nil, err
	}
	if len(input.Patch) == 0 {
		return nil, invalidField("patch", "patch is required")
	}
	if string(input.Patch) == "null" {
		return nil, nil
	}
	if len(input.Patch) > 1<<20 || input.Patch[0] != '{' || !json.Valid(input.Patch) {
		return nil, invalidField("patch", "patch must be a JSON object")
	}
	return append(json.RawMessage(nil), input.Patch...), nil
}

func (s *Server) siteExists(id string) bool {
	for _, site := range s.store.Snapshot().Sites {
		if site.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) currentSiteID() string {
	for _, ap := range s.store.Snapshot().APs {
		if ap.ID == s.apID {
			return ap.SiteID
		}
	}
	return ""
}

func (s *Server) siteResource(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "sites" {
		routeNotFound(writer)
		return
	}
	siteID := parts[2]
	state := s.store.Snapshot()
	foundSite := false
	var selectedSite model.Site
	for _, site := range state.Sites {
		if site.ID == siteID {
			foundSite = true
			selectedSite = site
			if len(parts) == 3 {
				respond(writer, http.StatusOK, map[string]any{"site": siteView(site)})
				return
			}
			if len(parts) == 4 && parts[3] == "ssh-keys" {
				keys := site.SSHPublicKeys
				if keys == nil {
					keys = []string{}
				}
				respond(writer, http.StatusOK, map[string]any{"keys": keys})
				return
			}
			if len(parts) == 4 && parts[3] == "networks" {
				s.listNetworks(writer, siteID)
				return
			}
			if len(parts) == 4 && parts[3] == "clients" {
				s.siteClients(writer, state, siteID)
				return
			}
			if len(parts) == 4 && parts[3] == "stream" {
				s.streamSite(writer, request, siteID)
				return
			}
			break
		}
	}
	if foundSite && len(parts) == 5 && parts[3] == "history" {
		s.siteHistory(writer, request, siteID, parts[4])
		return
	}
	if foundSite && len(parts) == 6 && parts[3] == "networks" && parts[5] == "advanced" {
		for _, network := range state.Networks {
			if network.ID == parts[4] && network.SiteID == siteID {
				respond(writer, http.StatusOK, map[string]any{"patch": redactPatch(network.Advanced)})
				return
			}
		}
		writeAPIError(writer, notFound("network"))
		return
	}
	if foundSite && parts[3] == "aps" && ((len(parts) == 6 && (parts[5] == "samples" || parts[5] == "sessions")) ||
		(len(parts) == 7 && parts[5] == "history" && parts[6] == "events")) {
		s.apHistory(writer, request, state, siteID, parts)
		return
	}
	if !foundSite {
		writeAPIError(writer, notFound("site"))
		return
	}
	if len(parts) != 6 || parts[3] != "aps" {
		routeNotFound(writer)
		return
	}
	apID := parts[4]
	assigned := false
	var selectedAP model.APRecord
	for _, ap := range state.APs {
		if ap.ID == apID && ap.SiteID == siteID {
			assigned = true
			selectedAP = ap
			break
		}
	}
	if !assigned {
		writeAPIError(writer, notFound("AP"))
		return
	}
	control := s.controlFor(apID)
	switch parts[5] {
	case "status":
		var status controller.Status
		var desired uint64
		if control != nil {
			status = control.Status()
			_, desired = control.ConfigSnapshot()
		}
		respond(writer, http.StatusOK, map[string]any{"ap_id": apID, "desired_revision": desired, "ap": status})
		return
	case "telemetry":
		if control == nil {
			respond(writer, http.StatusOK, controller.TelemetrySnapshot{})
		} else {
			respond(writer, http.StatusOK, liveTelemetry(selectedAP, control.Status(), control.TelemetrySnapshot()))
		}
		return
	case "clients":
		var associated []telemetry.Client
		var recent []controller.ClientActivity
		if control != nil {
			snapshot := control.TelemetrySnapshot()
			associated = liveTelemetry(selectedAP, control.Status(), snapshot).AssociatedClients
			recent = liveRecent(selectedAP, snapshot.RecentClients)
		}
		if associated == nil {
			associated = []telemetry.Client{}
		}
		if recent == nil {
			recent = []controller.ClientActivity{}
		}
		respond(writer, http.StatusOK, map[string]any{"clients": associated, "recent_activity": recent})
		return
	case "events":
		var events []controller.ConnectionEvent
		if control != nil {
			events = liveEvents(selectedAP, control.TelemetrySnapshot().Events)
		}
		if events == nil {
			events = []controller.ConnectionEvent{}
		}
		respond(writer, http.StatusOK, map[string]any{"events": events})
		return
	}
	if parts[5] == "config" {
		networks := make([]model.Network, 0)
		for _, network := range state.Networks {
			if network.SiteID == siteID {
				networks = append(networks, network)
			}
		}
		effective, err := controller.BuildSiteConfig(s.base, selectedSite, selectedAP, networks, state.UpdatedAt)
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
			"effective":  redacted,
			"site_patch": redactPatch(selectedSite.Advanced),
			"ap_patch":   redactPatch(selectedAP.Advanced),
		})
		return
	}
	routeNotFound(writer)
}

func (s *Server) siteClients(writer http.ResponseWriter, state model.State, siteID string) {
	clients := make([]siteClient, 0)
	recent := make([]siteClientActivity, 0)
	for _, ap := range state.APs {
		if ap.SiteID != siteID {
			continue
		}
		control := s.controlFor(ap.ID)
		if control == nil {
			continue
		}
		snapshot := control.TelemetrySnapshot()
		for _, client := range liveTelemetry(ap, control.Status(), snapshot).AssociatedClients {
			clients = append(clients, siteClient{Client: client, APID: ap.ID, APName: ap.Name})
		}
		for _, activity := range liveRecent(ap, snapshot.RecentClients) {
			recent = append(recent, siteClientActivity{ClientActivity: activity, APID: ap.ID, APName: ap.Name})
		}
	}
	respond(writer, http.StatusOK, map[string]any{"clients": clients, "recent_activity": recent})
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

func siteView(site model.Site) map[string]any {
	return map[string]any{"id": site.ID, "name": site.Name, "country": site.Country,
		"timezone": site.Timezone, "advanced": redactPatch(site.Advanced)}
}

func redactJSON(raw []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
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
