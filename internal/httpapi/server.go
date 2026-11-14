package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	webassets "github.com/nicko170/dismist"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/proxytrust"
	"github.com/nicko170/dismist/internal/telemetry"
)

var errNotFound = errors.New("network not found")
var errConflict = errors.New("AP already registered")
var errNoChange = errors.New("no AP changes")

type Server struct {
	store           model.StateStore
	control         *controller.Server
	manager         *controller.Manager
	base            []byte
	apID            string
	assets          http.Handler
	hosts           map[string]bool
	streamInterval  time.Duration
	streamHeartbeat time.Duration
	streamMaxAge    time.Duration
	mutationLock    *sync.Mutex
}

type networkInput struct {
	SSID    *string `json:"ssid"`
	PSK     *string `json:"psk"`
	VLANID  *int    `json:"vlan_id"`
	Enabled *bool   `json:"enabled"`
}

type networkView struct {
	ID      string `json:"id"`
	SSID    string `json:"ssid"`
	VLANID  int    `json:"vlan_id"`
	Enabled bool   `json:"enabled"`
	HasPSK  bool   `json:"has_psk"`
}

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

type historyReader interface {
	ListSamples(string, string, int) ([]model.MinuteSample, error)
	ListClientSessions(string, string, int) ([]model.ClientSession, error)
	ListEvents(string, string, int) ([]model.EventRecord, error)
}

func New(store model.StateStore, control *controller.Server, base []byte, apID string, allowedHosts ...string) *Server {
	web, err := fs.Sub(webassets.WebAssets, "web/dist")
	if err != nil {
		panic(err)
	}
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true}
	for _, host := range allowedHosts {
		if host != "" {
			hosts[strings.ToLower(strings.TrimSuffix(host, "."))] = true
		}
	}
	return &Server{
		store: store, control: control, base: append([]byte(nil), base...),
		apID: apID, assets: http.FileServer(http.FS(web)), hosts: hosts,
		streamInterval: time.Second, streamHeartbeat: 15 * time.Second, streamMaxAge: 30 * time.Minute,
		mutationLock: new(sync.Mutex),
	}
}

// UseMutationLock coordinates API writes with AP registry activation. Call it
// before serving requests; the registry must receive the same mutex.
func (s *Server) UseMutationLock(lock *sync.Mutex) *Server {
	if lock != nil {
		s.mutationLock = lock
	}
	return s
}

func NewWithManager(store model.StateStore, manager *controller.Manager, base []byte, defaultAPID string, allowedHosts ...string) *Server {
	control, _ := manager.Server(defaultAPID)
	server := New(store, control, base, defaultAPID, allowedHosts...)
	server.manager = manager
	return server
}

func (s *Server) controlFor(apID string) *controller.Server {
	if s.manager != nil {
		control, _ := s.manager.Server(apID)
		return control
	}
	if apID == s.apID {
		return s.control
	}
	return nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if !s.allowedHost(request.Host) || !s.allowedOrigin(request) {
		http.Error(writer, "local access only", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/") && request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
		s.mutationLock.Lock()
		defer s.mutationLock.Unlock()
	}
	switch {
	case request.URL.Path == "/api/aps" && request.Method == http.MethodGet:
		state := s.store.Snapshot()
		aps := make([]map[string]any, 0, len(state.APs))
		for _, ap := range state.APs {
			aps = append(aps, s.apView(ap))
		}
		respond(writer, http.StatusOK, map[string]any{"aps": aps})
	case request.URL.Path == "/api/aps" && request.Method == http.MethodPost:
		s.registerAP(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/aps/") && request.Method == http.MethodPut:
		if len(strings.Split(strings.Trim(request.URL.Path, "/"), "/")) == 3 {
			s.updateAP(writer, request)
		} else {
			s.moveAP(writer, request)
		}
	case request.URL.Path == "/api/sites" && request.Method == http.MethodGet:
		s.listSites(writer)
	case request.URL.Path == "/api/sites" && request.Method == http.MethodPost:
		s.createSite(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodGet:
		s.siteResource(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodPost:
		s.siteMutation(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodPut:
		s.siteUpdate(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodDelete:
		s.siteDelete(writer, request)
	case request.URL.Path == "/api/status" && request.Method == http.MethodGet:
		var desired uint64
		var status controller.Status
		if control := s.controlFor(s.apID); control != nil {
			_, desired = control.ConfigSnapshot()
			status = control.Status()
		}
		respond(writer, http.StatusOK, map[string]any{
			"ap_id":            s.apID,
			"desired_revision": desired,
			"ap":               status,
		})
	case request.URL.Path == "/api/telemetry" && request.Method == http.MethodGet:
		var snapshot controller.TelemetrySnapshot
		if control := s.controlFor(s.apID); control != nil {
			snapshot = control.TelemetrySnapshot()
		}
		respond(writer, http.StatusOK, snapshot)
	case request.URL.Path == "/api/clients" && request.Method == http.MethodGet:
		var snapshot controller.TelemetrySnapshot
		if control := s.controlFor(s.apID); control != nil {
			snapshot = control.TelemetrySnapshot()
		}
		associated := snapshot.AssociatedClients
		if associated == nil {
			associated = []telemetry.Client{}
		}
		recent := snapshot.RecentClients
		if recent == nil {
			recent = []controller.ClientActivity{}
		}
		respond(writer, http.StatusOK, map[string]any{"clients": associated, "recent_activity": recent})
	case request.URL.Path == "/api/events" && request.Method == http.MethodGet:
		var events []controller.ConnectionEvent
		if control := s.controlFor(s.apID); control != nil {
			events = control.TelemetrySnapshot().Events
		}
		if events == nil {
			events = []controller.ConnectionEvent{}
		}
		respond(writer, http.StatusOK, map[string]any{"events": events})
	case request.URL.Path == "/api/networks" && request.Method == http.MethodGet:
		s.listNetworks(writer, s.currentSiteID())
	case request.URL.Path == "/api/networks" && request.Method == http.MethodPost:
		s.createNetwork(writer, request, s.currentSiteID())
	case strings.HasPrefix(request.URL.Path, "/api/networks/") && request.Method == http.MethodPut:
		id := strings.TrimPrefix(request.URL.Path, "/api/networks/")
		s.editNetwork(writer, request, s.currentSiteID(), id)
	case strings.HasPrefix(request.URL.Path, "/api/networks/") && request.Method == http.MethodDelete:
		id := strings.TrimPrefix(request.URL.Path, "/api/networks/")
		s.removeNetwork(writer, s.currentSiteID(), id)
	default:
		if request.Method == http.MethodGet && !strings.HasPrefix(request.URL.Path, "/api/") {
			s.assets.ServeHTTP(writer, request)
		} else {
			http.NotFound(writer, request)
		}
	}
}

func (s *Server) apView(ap model.APRecord) map[string]any {
	var status controller.Status
	clientCount := 0
	managementIP := ap.ManagementIP
	if control := s.controlFor(ap.ID); control != nil {
		status = control.Status()
		clientCount = len(liveTelemetry(ap, status, control.TelemetrySnapshot()).AssociatedClients)
		if managementIP == "" {
			managementIP = control.AllowedManagementIP()
		}
	}
	return map[string]any{
		"id": ap.ID, "name": ap.Name, "site_id": ap.SiteID,
		"model": ap.Model, "firmware": preferredFirmware(ap.Firmware, status),
		"management_ip": managementIP, "config_revision": ap.ConfigRevision,
		"connected": status.Connected, "client_count": clientCount,
		"last_seen": status.LastSeen, "applied_revision": status.AppliedRevision,
	}
}

func preferredFirmware(stored string, status controller.Status) string {
	if status.Firmware != "" {
		return status.Firmware
	}
	return stored
}

func (s *Server) registerAP(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusBadRequest)
		return
	}
	var input struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Model        string `json:"model"`
		ManagementIP string `json:"management_ip"`
		SiteID       string `json:"site_id"`
		SecretFile   string `json:"secret_file"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, "invalid AP registration", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(writer, "one JSON object required", http.StatusBadRequest)
		return
	}
	mac, err := net.ParseMAC(strings.ReplaceAll(strings.TrimSpace(input.ID), "-", ":"))
	ip := net.ParseIP(input.ManagementIP)
	if err != nil || len(mac) != 6 || ip == nil || !filepath.IsAbs(input.SecretFile) {
		http.Error(writer, "invalid AP ID, management IP, or secret file", http.StatusBadRequest)
		return
	}
	id := strings.ReplaceAll(mac.String(), ":", "-")
	if err := validateAPSecretFile(input.SecretFile); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	var created model.APRecord
	err = s.store.Update(func(state *model.State) error {
		var site model.Site
		foundSite := input.SiteID == ""
		for _, candidate := range state.Sites {
			if candidate.ID == input.SiteID {
				site = candidate
				foundSite = true
				break
			}
		}
		if !foundSite {
			return errNotFound
		}
		for _, candidate := range state.APs {
			if candidate.ID == id {
				return errConflict
			}
		}
		if s.apIPConflict(state, id, ip) {
			return errConflict
		}
		created = model.APRecord{ID: id, Name: strings.TrimSpace(input.Name), Model: strings.TrimSpace(input.Model),
			ManagementIP: ip.String(), SiteID: input.SiteID, SecretFile: input.SecretFile,
			ConfigRevision: state.Revision + 1, ConfigUpdatedAt: state.UpdatedAt}
		networks := make([]model.Network, 0)
		for _, network := range state.Networks {
			if network.SiteID == input.SiteID && input.SiteID != "" {
				networks = append(networks, network)
			}
		}
		if _, err := controller.BuildSiteConfig(s.base, site, created, networks, created.ConfigUpdatedAt); err != nil {
			return err
		}
		state.APs = append(state.APs, created)
		return nil
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respond(writer, http.StatusCreated, map[string]any{"ap": s.apView(created), "revision": s.store.Snapshot().Revision})
}

func validateAPSecretFile(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("AP secret file must be an absolute path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("AP secret file must exist and be private")
	}
	if info.Size() != 32 {
		return errors.New("AP secret must be exactly 32 bytes")
	}
	secret, err := os.ReadFile(path)
	if err != nil || len(secret) != 32 {
		return errors.New("AP secret must be readable and exactly 32 bytes")
	}
	return nil
}

func (s *Server) apIPConflict(state *model.State, apID string, ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, candidate := range state.APs {
		if candidate.ID == apID {
			continue
		}
		if net.ParseIP(candidate.ManagementIP).Equal(ip) {
			return true
		}
		if control := s.controlFor(candidate.ID); control != nil && net.ParseIP(control.AllowedManagementIP()).Equal(ip) {
			return true
		}
	}
	return false
}

func (s *Server) updateAP(writer http.ResponseWriter, request *http.Request) {
	apID := strings.TrimPrefix(request.URL.Path, "/api/aps/")
	if apID == "" || strings.Contains(apID, "/") {
		http.NotFound(writer, request)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusBadRequest)
		return
	}
	var input struct {
		Name         *string `json:"name"`
		Model        *string `json:"model"`
		ManagementIP *string `json:"management_ip"`
		SecretFile   *string `json:"secret_file"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Name == nil && input.Model == nil && input.ManagementIP == nil && input.SecretFile == nil) {
		http.Error(writer, "AP fields required", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(writer, "one JSON object required", http.StatusBadRequest)
		return
	}
	var ip net.IP
	if input.ManagementIP != nil && strings.TrimSpace(*input.ManagementIP) != "" {
		ip = net.ParseIP(strings.TrimSpace(*input.ManagementIP))
		if ip == nil {
			http.Error(writer, "invalid AP management IP", http.StatusBadRequest)
			return
		}
	}
	var newSecret string
	if input.SecretFile != nil {
		newSecret = strings.TrimSpace(*input.SecretFile)
	}
	if newSecret != "" {
		if err := validateAPSecretFile(newSecret); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
	}
	var updated model.APRecord
	var config []byte
	err := s.store.Update(func(state *model.State) error {
		index := -1
		for i := range state.APs {
			if state.APs[i].ID == apID {
				index = i
				break
			}
		}
		if index < 0 {
			return errNotFound
		}
		updated = state.APs[index]
		if input.Name != nil {
			updated.Name = strings.TrimSpace(*input.Name)
		}
		if input.Model != nil {
			updated.Model = strings.TrimSpace(*input.Model)
		}
		if input.ManagementIP != nil {
			if ip == nil {
				updated.ManagementIP = ""
			} else {
				updated.ManagementIP = ip.String()
			}
		}
		if newSecret != "" {
			updated.SecretFile = newSecret
		}
		previous := state.APs[index]
		if updated.Name == previous.Name && updated.Model == previous.Model && updated.ManagementIP == previous.ManagementIP && updated.SecretFile == previous.SecretFile {
			return errNoChange
		}
		if s.apIPConflict(state, apID, net.ParseIP(updated.ManagementIP)) {
			return errConflict
		}
		updated.ConfigRevision = state.Revision + 1
		updated.ConfigUpdatedAt = state.UpdatedAt
		var site model.Site
		for _, candidate := range state.Sites {
			if candidate.ID == updated.SiteID {
				site = candidate
				break
			}
		}
		networks := make([]model.Network, 0)
		for _, network := range state.Networks {
			if network.SiteID == updated.SiteID && updated.SiteID != "" {
				networks = append(networks, network)
			}
		}
		var err error
		config, err = controller.BuildSiteConfig(s.base, site, updated, networks, updated.ConfigUpdatedAt)
		if err != nil {
			return err
		}
		state.APs[index] = updated
		return nil
	})
	if errors.Is(err, errNoChange) {
		state := s.store.Snapshot()
		for _, ap := range state.APs {
			if ap.ID == apID {
				respond(writer, http.StatusOK, map[string]any{"ap": s.apView(ap), "revision": state.Revision})
				return
			}
		}
		http.NotFound(writer, request)
		return
	}
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if control := s.controlFor(apID); control != nil {
		changed, err := control.SetConfigIfNewer(config, updated.ConfigRevision)
		if err != nil {
			http.Error(writer, "AP config update failed", http.StatusInternalServerError)
			return
		}
		if changed {
			control.Disconnect()
		}
	}
	respond(writer, http.StatusOK, map[string]any{"ap": s.apView(updated), "revision": s.store.Snapshot().Revision})
}

func (s *Server) moveAP(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "aps" || parts[3] != "site" {
		http.NotFound(writer, request)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusBadRequest)
		return
	}
	var input struct {
		SiteID string `json:"site_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, "invalid site assignment", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(writer, "one JSON object required", http.StatusBadRequest)
		return
	}
	if input.SiteID != "" && !s.siteExists(input.SiteID) {
		http.NotFound(writer, request)
		return
	}
	apID := parts[2]
	for _, ap := range s.store.Snapshot().APs {
		if ap.ID == apID && ap.SiteID == input.SiteID {
			respond(writer, http.StatusOK, map[string]any{"id": apID, "site_id": input.SiteID, "revision": s.store.Snapshot().Revision})
			return
		}
	}
	var oldSite string
	var config []byte
	var configRevision uint64
	err := s.store.Update(func(state *model.State) error {
		var site model.Site
		for _, candidate := range state.Sites {
			if candidate.ID == input.SiteID {
				site = candidate
				break
			}
		}
		for index := range state.APs {
			if state.APs[index].ID != apID {
				continue
			}
			oldSite = state.APs[index].SiteID
			state.APs[index].SiteID = input.SiteID
			state.APs[index].ConfigRevision = state.Revision + 1
			state.APs[index].ConfigUpdatedAt = state.UpdatedAt
			configRevision = state.APs[index].ConfigRevision
			networks := make([]model.Network, 0)
			for _, network := range state.Networks {
				if network.SiteID == input.SiteID {
					networks = append(networks, network)
				}
			}
			var err error
			config, err = controller.BuildSiteConfig(s.base, site, state.APs[index], networks, state.APs[index].ConfigUpdatedAt)
			return err
		}
		return errNotFound
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	revision := s.store.Snapshot().Revision
	if config != nil {
		if control := s.controlFor(apID); control != nil {
			changed, err := control.SetConfigIfNewer(config, configRevision)
			if err != nil {
				http.Error(writer, "AP config update failed", http.StatusInternalServerError)
				return
			}
			if changed {
				control.Disconnect()
			}
		}
	}
	respond(writer, http.StatusOK, map[string]any{
		"id": apID, "site_id": input.SiteID,
		"previous_site_id": oldSite, "revision": revision,
	})
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
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusBadRequest)
		return
	}
	var input struct {
		Name     string `json:"name"`
		Country  string `json:"country"`
		Timezone string `json:"timezone"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Name) == "" {
		http.Error(writer, "site name required", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || !validCountry(input.Country) || !validTimezone(input.Timezone) {
		http.Error(writer, "invalid site metadata", http.StatusBadRequest)
		return
	}
	id, err := randomID()
	if err != nil {
		http.Error(writer, "cannot create site", http.StatusInternalServerError)
		return
	}
	site := model.Site{ID: id, Name: strings.TrimSpace(input.Name), Country: strings.ToUpper(strings.TrimSpace(input.Country)), Timezone: input.Timezone}
	if err := s.store.Update(func(state *model.State) error {
		state.Sites = append(state.Sites, site)
		return nil
	}); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	respond(writer, http.StatusCreated, map[string]any{"site": site})
}

func (s *Server) siteMutation(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "sites" || parts[3] != "networks" {
		http.NotFound(writer, request)
		return
	}
	if !s.siteExists(parts[2]) {
		http.NotFound(writer, request)
		return
	}
	s.createNetwork(writer, request, parts[2])
}

func (s *Server) siteUpdate(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "sites" || !s.siteExists(parts[2]) {
		http.NotFound(writer, request)
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
		http.NotFound(writer, request)
		return
	}
	patch, err := readAdvancedInput(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
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
		case len(parts) == 6 && parts[3] == "networks":
			for index := range state.Networks {
				if state.Networks[index].ID == parts[4] && state.Networks[index].SiteID == siteID {
					return setAdvanced(&state.Networks[index].Advanced, patch)
				}
			}
		}
		return errNotFound
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respond(writer, http.StatusOK, map[string]any{"revision": s.store.Snapshot().Revision})
}

func (s *Server) updateSiteSSHKeys(writer http.ResponseWriter, request *http.Request, siteID string) {
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusBadRequest)
		return
	}
	var input struct {
		Keys []string `json:"keys"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Keys == nil {
		http.Error(writer, "SSH public keys array required", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(writer, "one JSON object required", http.StatusBadRequest)
		return
	}
	keys, err := model.NormalizeSSHPublicKeys(input.Keys)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	err = s.applyChange(siteID, func(state *model.State) error {
		for index := range state.Sites {
			if state.Sites[index].ID == siteID {
				state.Sites[index].SSHPublicKeys = keys
				return nil
			}
		}
		return errNotFound
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
			return nil, errors.New("redacted value has no stored secret")
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
	if request.Header.Get("Content-Type") != "application/json" {
		http.Error(writer, "application/json required", http.StatusBadRequest)
		return
	}
	var input struct {
		Name     *string `json:"name"`
		Country  *string `json:"country"`
		Timezone *string `json:"timezone"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Name == nil && input.Country == nil && input.Timezone == nil) {
		http.Error(writer, "site fields required", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(writer, "one JSON object required", http.StatusBadRequest)
		return
	}
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		http.Error(writer, "site name required", http.StatusBadRequest)
		return
	}
	if input.Country != nil && !validCountry(*input.Country) || input.Timezone != nil && !validTimezone(*input.Timezone) {
		http.Error(writer, "invalid site metadata", http.StatusBadRequest)
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
		return errNotFound
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
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "sites" || parts[3] != "networks" || !s.siteExists(parts[2]) {
		http.NotFound(writer, request)
		return
	}
	s.removeNetwork(writer, parts[2], parts[4])
}

func readAdvancedInput(request *http.Request) (json.RawMessage, error) {
	if request.Header.Get("Content-Type") != "application/json" {
		return nil, errors.New("application/json required")
	}
	var input struct {
		Patch json.RawMessage `json:"patch"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("one JSON object required")
	}
	if len(input.Patch) == 0 {
		return nil, errors.New("patch is required")
	}
	if string(input.Patch) == "null" {
		return nil, nil
	}
	if len(input.Patch) > 1<<20 || input.Patch[0] != '{' || !json.Valid(input.Patch) {
		return nil, errors.New("patch must be a JSON object")
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

func (s *Server) createNetwork(writer http.ResponseWriter, request *http.Request, siteID string) {
	input, err := readInput(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if input.SSID == nil || input.PSK == nil || input.VLANID == nil {
		http.Error(writer, "SSID, passphrase and VLAN are required", http.StatusBadRequest)
		return
	}
	id, err := randomID()
	if err != nil {
		http.Error(writer, "cannot create network", http.StatusInternalServerError)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	err = s.applyChange(siteID, func(state *model.State) error {
		state.Networks = append(state.Networks, model.Network{
			ID: id, SiteID: siteID, SSID: *input.SSID, PSK: *input.PSK,
			VLANID: *input.VLANID, Enabled: enabled,
		})
		return nil
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	s.listNetworks(writer, siteID)
}

func (s *Server) editNetwork(writer http.ResponseWriter, request *http.Request, siteID, id string) {
	input, err := readInput(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	err = s.applyChange(siteID, func(state *model.State) error {
		for index := range state.Networks {
			if state.Networks[index].ID != id || !networkInSite(state.Networks[index], siteID, len(state.Sites) == 0) {
				continue
			}
			network := &state.Networks[index]
			if input.SSID != nil {
				network.SSID = *input.SSID
			}
			if input.PSK != nil {
				network.PSK = *input.PSK
			}
			if input.VLANID != nil {
				network.VLANID = *input.VLANID
			}
			if input.Enabled != nil {
				network.Enabled = *input.Enabled
			}
			return nil
		}
		return errNotFound
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	s.listNetworks(writer, siteID)
}

func (s *Server) removeNetwork(writer http.ResponseWriter, siteID, id string) {
	err := s.applyChange(siteID, func(state *model.State) error {
		for index, network := range state.Networks {
			if network.ID == id && networkInSite(network, siteID, len(state.Sites) == 0) {
				state.Networks = append(state.Networks[:index], state.Networks[index+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	s.listNetworks(writer, siteID)
}

func (s *Server) siteResource(writer http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "sites" {
		http.NotFound(writer, request)
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
		http.NotFound(writer, request)
		return
	}
	if foundSite && parts[3] == "aps" && ((len(parts) == 6 && (parts[5] == "samples" || parts[5] == "sessions")) ||
		(len(parts) == 7 && parts[5] == "history" && parts[6] == "events")) {
		s.apHistory(writer, request, state, siteID, parts)
		return
	}
	if !foundSite || len(parts) != 6 || parts[3] != "aps" {
		http.NotFound(writer, request)
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
		http.NotFound(writer, request)
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
			http.Error(writer, "config preview failed", http.StatusBadRequest)
			return
		}
		redacted, err := redactJSON(effective)
		if err != nil {
			http.Error(writer, "config preview failed", http.StatusBadRequest)
			return
		}
		respond(writer, http.StatusOK, map[string]any{
			"effective":  redacted,
			"site_patch": redactPatch(selectedSite.Advanced),
			"ap_patch":   redactPatch(selectedAP.Advanced),
		})
		return
	}
	http.NotFound(writer, request)
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

func liveTelemetry(ap model.APRecord, status controller.Status, snapshot controller.TelemetrySnapshot) controller.TelemetrySnapshot {
	freshAfter := status.ConnectedAt
	if ap.ConfigUpdatedAt.After(freshAfter) {
		freshAfter = ap.ConfigUpdatedAt
	}
	if !status.Connected || (!snapshot.LastFull.After(freshAfter) && !snapshot.LastFast.After(freshAfter)) {
		return controller.TelemetrySnapshot{}
	}
	return snapshot
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

func (s *Server) siteHistory(writer http.ResponseWriter, request *http.Request, siteID, kind string) {
	history, ok := s.store.(historyReader)
	if !ok {
		http.Error(writer, "history unavailable", http.StatusNotImplemented)
		return
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	switch kind {
	case "samples":
		items, err := history.ListSamples(siteID, "", limit)
		if err != nil {
			http.Error(writer, "history read failed", http.StatusInternalServerError)
			return
		}
		respond(writer, http.StatusOK, map[string]any{"samples": items})
	case "sessions":
		items, err := history.ListClientSessions(siteID, "", limit)
		if err != nil {
			http.Error(writer, "history read failed", http.StatusInternalServerError)
			return
		}
		respond(writer, http.StatusOK, map[string]any{"sessions": items})
	case "events":
		items, err := history.ListEvents(siteID, "", limit)
		if err != nil {
			http.Error(writer, "history read failed", http.StatusInternalServerError)
			return
		}
		respond(writer, http.StatusOK, map[string]any{"events": items})
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) apHistory(writer http.ResponseWriter, request *http.Request, state model.State, siteID string, parts []string) {
	history, ok := s.store.(historyReader)
	if !ok {
		http.Error(writer, "history unavailable", http.StatusNotImplemented)
		return
	}
	apID := parts[4]
	assigned := false
	for _, ap := range state.APs {
		if ap.ID == apID && ap.SiteID == siteID {
			assigned = true
			break
		}
	}
	limit, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	var items any
	var count int
	var err error
	key := parts[5]
	switch key {
	case "samples":
		var values []model.MinuteSample
		values, err = history.ListSamples(siteID, apID, limit)
		items, count = values, len(values)
	case "sessions":
		var values []model.ClientSession
		values, err = history.ListClientSessions(siteID, apID, limit)
		items, count = values, len(values)
	case "history":
		var values []model.EventRecord
		values, err = history.ListEvents(siteID, apID, limit)
		items, count, key = values, len(values), "events"
	}
	if err != nil {
		http.Error(writer, "history read failed", http.StatusInternalServerError)
		return
	}
	if !assigned && count == 0 {
		http.NotFound(writer, request)
		return
	}
	respond(writer, http.StatusOK, map[string]any{key: items})
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

func networkInSite(network model.Network, siteID string, legacy bool) bool {
	return network.SiteID == siteID || (legacy && network.SiteID == "")
}

func (s *Server) listNetworks(writer http.ResponseWriter, siteID string) {
	state := s.store.Snapshot()
	networks := make([]networkView, 0, len(state.Networks))
	for _, network := range state.Networks {
		if !networkInSite(network, siteID, len(state.Sites) == 0) {
			continue
		}
		networks = append(networks, networkView{
			ID: network.ID, SSID: network.SSID, VLANID: network.VLANID,
			Enabled: network.Enabled, HasPSK: network.PSK != "",
		})
	}
	respond(writer, http.StatusOK, map[string]any{"revision": state.Revision, "networks": networks})
}

func (s *Server) applyChange(siteID string, change func(*model.State) error) error {
	type preparedConfig struct {
		data     []byte
		revision uint64
	}
	configs := make(map[string]preparedConfig)
	err := s.store.Update(func(state *model.State) error {
		if err := change(state); err != nil {
			return err
		}
		for index := range state.APs {
			ap := &state.APs[index]
			if ap.SiteID == siteID {
				ap.ConfigRevision = state.Revision + 1
				ap.ConfigUpdatedAt = state.UpdatedAt
				var site model.Site
				for _, candidate := range state.Sites {
					if candidate.ID == ap.SiteID {
						site = candidate
						break
					}
				}
				networks := make([]model.Network, 0)
				for _, network := range state.Networks {
					if networkInSite(network, siteID, len(state.Sites) == 0) {
						networks = append(networks, network)
					}
				}
				config, err := controller.BuildSiteConfig(s.base, site, *ap, networks, ap.ConfigUpdatedAt)
				if err != nil {
					return err
				}
				if s.controlFor(ap.ID) != nil {
					configs[ap.ID] = preparedConfig{data: config, revision: ap.ConfigRevision}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for apID, config := range configs {
		control := s.controlFor(apID)
		if control == nil {
			continue
		}
		changed, err := control.SetConfigIfNewer(config.data, config.revision)
		if err != nil {
			return err
		}
		if changed {
			control.Disconnect()
		}
	}
	return nil
}

func readInput(request *http.Request) (networkInput, error) {
	var input networkInput
	if request.Header.Get("Content-Type") != "application/json" {
		return input, errors.New("application/json required")
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return input, errors.New("one JSON object required")
	}
	return input, nil
}

func (s *Server) allowedHost(host string) bool {
	if s.hosts["*"] {
		return true
	}
	hostname := host
	if parsed, err := url.Parse("http://" + host); err == nil {
		hostname = parsed.Hostname()
	}
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	return s.hosts[hostname]
}

func (s *Server) allowedOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return s.allowedHost(parsed.Host)
	}
	scheme := "http"
	if proxytrust.HTTPS(request) {
		scheme = "https"
	}
	requested, err := url.Parse(scheme + "://" + request.Host)
	if err != nil || requested.User != nil || parsed.Scheme != scheme || !strings.EqualFold(parsed.Hostname(), requested.Hostname()) {
		return false
	}
	originPort, requestPort := parsed.Port(), requested.Port()
	if originPort == "" {
		if scheme == "https" {
			originPort = "443"
		} else {
			originPort = "80"
		}
	}
	if requestPort == "" {
		if scheme == "https" {
			requestPort = "443"
		} else {
			requestPort = "80"
		}
	}
	return originPort == requestPort
}

func randomID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeChangeError(writer http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	if errors.Is(err, errConflict) {
		http.Error(writer, err.Error(), http.StatusConflict)
		return
	}
	http.Error(writer, err.Error(), http.StatusBadRequest)
}
