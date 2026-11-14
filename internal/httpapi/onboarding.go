package httpapi

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

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
		Secret       string `json:"secret"`
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
	if err != nil || len(mac) != 6 {
		http.Error(writer, "invalid AP ID: use its Ethernet MAC address", http.StatusBadRequest)
		return
	}
	secret, err := parseAPSecret(input.Secret)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	// The management IP is optional: it pins the AP to one source address.
	managementIP := ""
	if address := strings.TrimSpace(input.ManagementIP); address != "" {
		ip := net.ParseIP(address)
		if ip == nil {
			http.Error(writer, "invalid AP management IP", http.StatusBadRequest)
			return
		}
		managementIP = ip.String()
	}
	id := strings.ReplaceAll(mac.String(), ":", "-")
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
		created = model.APRecord{ID: id, Name: strings.TrimSpace(input.Name), Model: strings.TrimSpace(input.Model),
			ManagementIP: managementIP, SiteID: input.SiteID, Secret: secret,
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

// parseAPSecret reads an AP's 32-byte EEPROM secret given as hex (64
// digits, optionally separated by spaces or colons) or base64 (standard or
// URL alphabet, padded or not).
func parseAPSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("AP secret required: the AP's 32-byte EEPROM secret as hex or base64")
	}
	compact := strings.NewReplacer(" ", "", ":", "", "\n", "", "\r", "", "\t", "").Replace(value)
	if decoded, err := hex.DecodeString(compact); err == nil && len(decoded) == model.SecretSize {
		return decoded, nil
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(compact); err == nil && len(decoded) == model.SecretSize {
			return decoded, nil
		}
	}
	return nil, errors.New("AP secret must be exactly 32 bytes, as hex or base64")
}
