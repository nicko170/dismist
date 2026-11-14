package httpapi

import (
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
		writeError(writer, err)
		return
	}
	if input.SSID == nil || input.PSK == nil || input.VLANID == nil {
		missing := invalid("SSID, passphrase and VLAN are required")
		if input.SSID == nil {
			missing.withField("ssid", "required")
		}
		if input.PSK == nil {
			missing.withField("psk", "required")
		}
		if input.VLANID == nil {
			missing.withField("vlan_id", "required")
		}
		writeAPIError(writer, missing)
		return
	}
	if err := input.validate(); err != nil {
		writeAPIError(writer, err)
		return
	}
	id, err := randomID()
	if err != nil {
		writeAPIError(writer, internalError("cannot create network"))
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
		writeChangeError(writer, err)
		return
	}
	s.listNetworks(writer, siteID)
}

func (s *Server) editNetwork(writer http.ResponseWriter, request *http.Request, siteID, id string) {
	input, err := readInput(request)
	if err != nil {
		writeError(writer, err)
		return
	}
	if err := input.validate(); err != nil {
		writeAPIError(writer, err)
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
		return notFound("network")
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
		return notFound("network")
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
	err := decodeJSON(request, 64*1024, &input, "network")
	return input, err
}

// validate checks the fields that are present with the same rules the
// store applies, so a bad value is reported on its field.
func (input networkInput) validate() *apiError {
	var bad *apiError
	field := func(path, message string) {
		if bad == nil {
			bad = invalid("invalid network: " + message)
		}
		bad.withField(path, message)
	}
	if input.SSID != nil && (len(*input.SSID) < 1 || len(*input.SSID) > 32) {
		field("ssid", "SSID must be 1 to 32 bytes")
	}
	if input.PSK != nil && (len(*input.PSK) < 8 || len(*input.PSK) > 63) {
		field("psk", "passphrase must be 8 to 63 characters")
	}
	if input.VLANID != nil && (*input.VLANID < 1 || *input.VLANID > 4094) {
		field("vlan_id", "VLAN must be 1 to 4094")
	}
	return bad
}
