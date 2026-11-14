package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

// apView is the operator view of an AP. management_ip is the optional source
// pin; lan_ip is the address the AP reports on its own network; and
// remote_address is where its current or last session came from (a NAT's
// public address for APs behind one). model and firmware prefer what the AP
// itself reported when it authenticated.
func (s *Server) apView(ap model.APRecord) map[string]any {
	var status controller.Status
	var interfaces []telemetry.Interface
	clientCount := 0
	managementIP := ap.ManagementIP
	if control := s.controlFor(ap.ID); control != nil {
		status = control.Status()
		snapshot := control.TelemetrySnapshot()
		interfaces = snapshot.Interfaces
		clientCount = len(liveTelemetry(ap, status, snapshot).AssociatedClients)
		if managementIP == "" {
			managementIP = control.AllowedManagementIP()
		}
	}
	apModel := ap.Model
	if status.Model != "" {
		apModel = status.Model
	}
	return map[string]any{
		"id": ap.ID, "name": ap.Name, "site_id": ap.SiteID,
		"model": apModel, "firmware": preferredFirmware(ap.Firmware, status),
		"management_ip": managementIP, "lan_ip": lanIPv4(interfaces),
		"remote_address": status.RemoteAddress, "has_secret": len(ap.Secret) == model.SecretSize,
		"config_revision": ap.ConfigRevision,
		"connected":       status.Connected, "client_count": clientCount,
		"last_seen": status.LastSeen, "applied_revision": status.AppliedRevision,
	}
}

// lanIPv4 picks the AP's own IPv4 address, preferring its management VLAN
// interface (vlan1 on AP33) over any other with a usable address.
func lanIPv4(interfaces []telemetry.Interface) string {
	first := ""
	for _, iface := range interfaces {
		for _, address := range iface.Addresses {
			ip, _, err := net.ParseCIDR(address)
			if err != nil {
				ip = net.ParseIP(address)
			}
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if iface.Name == "vlan1" {
				return ip.String()
			}
			if first == "" {
				first = ip.String()
			}
		}
	}
	return first
}

func preferredFirmware(stored string, status controller.Status) string {
	if status.Firmware != "" {
		return status.Firmware
	}
	return stored
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
		Secret       *string `json:"secret"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || (input.Name == nil && input.Model == nil && input.ManagementIP == nil && input.Secret == nil) {
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
	// A blank secret on edit keeps the current one.
	var newSecret []byte
	if input.Secret != nil && strings.TrimSpace(*input.Secret) != "" {
		var err error
		if newSecret, err = parseAPSecret(*input.Secret); err != nil {
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
		if newSecret != nil {
			updated.Secret = newSecret
			updated.SecretFile = ""
		}
		previous := state.APs[index]
		if updated.Name == previous.Name && updated.Model == previous.Model && updated.ManagementIP == previous.ManagementIP && bytes.Equal(updated.Secret, previous.Secret) {
			return errNoChange
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

// legacyStatus serves GET /api/status for the default AP.
func (s *Server) legacyStatus(writer http.ResponseWriter) {
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
}

// legacyTelemetry serves GET /api/telemetry for the default AP.
func (s *Server) legacyTelemetry(writer http.ResponseWriter) {
	var snapshot controller.TelemetrySnapshot
	if control := s.controlFor(s.apID); control != nil {
		snapshot = control.TelemetrySnapshot()
	}
	respond(writer, http.StatusOK, snapshot)
}

// legacyClients serves GET /api/clients for the default AP.
func (s *Server) legacyClients(writer http.ResponseWriter) {
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
}
