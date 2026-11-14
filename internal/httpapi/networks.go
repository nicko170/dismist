package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/nicko170/dismist/internal/model"
)

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
