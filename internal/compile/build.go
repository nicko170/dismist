package compile

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/model"
	"golang.org/x/crypto/ssh"
)

func BuildConfig(base []byte, ap model.APRecord, networks []model.Network, now time.Time) ([]byte, error) {
	return BuildSiteConfig(base, model.Site{}, ap, networks, now)
}

func BuildSiteConfig(base []byte, site model.Site, ap model.APRecord, networks []model.Network, now time.Time) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(base, &root); err != nil {
		return nil, err
	}
	var templates []json.RawMessage
	if err := json.Unmarshal(root["WLANs"], &templates); err != nil || len(templates) == 0 {
		return nil, errors.New("AP config needs a WLAN template")
	}
	effective, err := addWLANVLANsToSwitchPorts(root, networks)
	if err != nil {
		return nil, err
	}
	for _, patch := range []json.RawMessage{site.Advanced, ap.Advanced} {
		if len(patch) == 0 {
			continue
		}
		effective, err = MergePatch(effective, patch)
		if err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(effective, &root); err != nil {
		return nil, err
	}
	if err := setJSON(root, "ID", ap.ID); err != nil {
		return nil, err
	}
	if ap.Name != "" {
		if err := setJSON(root, "Name", ap.Name); err != nil {
			return nil, err
		}
		if err := setJSON(root, "Hostname", ap.Name); err != nil {
			return nil, err
		}
	}
	siteID := ap.SiteID
	if siteID == "" {
		siteID = site.ID
	}
	if err := setJSON(root, "SiteID", siteID); err != nil {
		return nil, err
	}
	if site.Name != "" {
		if err := setJSON(root, "SiteName", site.Name); err != nil {
			return nil, err
		}
	}
	if country := strings.ToUpper(strings.TrimSpace(site.Country)); country != "" {
		if err := setJSON(root, "Country", country); err != nil {
			return nil, err
		}
	}
	if compactMAC := strings.ReplaceAll(ap.ID, "-", ""); len(compactMAC) == 12 {
		if err := setJSON(root, "UUID", "00000000-0000-0000-1000-"+compactMAC); err != nil {
			return nil, err
		}
	}
	if err := setJSON(root, "Timestamp", float64(now.UnixNano())/1e9); err != nil {
		return nil, err
	}
	wlans := make([]json.RawMessage, 0, len(networks))
	for _, network := range networks {
		if network.ID == "" || len(network.SSID) < 1 || len(network.SSID) > 32 ||
			network.VLANID < 1 || network.VLANID > 4094 || len(network.PSK) < 8 || len(network.PSK) > 63 {
			return nil, fmt.Errorf("invalid network %s", network.ID)
		}
		var wlan map[string]json.RawMessage
		wlanRaw := templates[0]
		if len(network.Advanced) != 0 {
			var err error
			wlanRaw, err = MergePatch(wlanRaw, network.Advanced)
			if err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(wlanRaw, &wlan); err != nil {
			return nil, err
		}
		for key, value := range map[string]any{
			"ID": network.ID, "SSID": network.SSID, "Enabled": network.Enabled,
			"Vlans": []string{strconv.Itoa(network.VLANID)},
		} {
			if err := setJSON(wlan, key, value); err != nil {
				return nil, err
			}
		}
		var authConfig map[string]json.RawMessage
		if err := json.Unmarshal(wlan["Auth"], &authConfig); err != nil {
			return nil, errors.New("WLAN template needs PSK auth")
		}
		if err := setJSON(authConfig, "Type", "psk"); err != nil {
			return nil, err
		}
		if err := setJSON(authConfig, "PSK", network.PSK); err != nil {
			return nil, err
		}
		if err := setJSON(wlan, "Auth", authConfig); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(wlan)
		if err != nil {
			return nil, err
		}
		wlans = append(wlans, encoded)
	}
	if err := setJSON(root, "WLANs", wlans); err != nil {
		return nil, err
	}
	if err := appendSiteSSHKeys(root, site.SSHPublicKeys); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

func appendSiteSSHKeys(root map[string]json.RawMessage, siteKeys []string) error {
	if len(siteKeys) == 0 {
		return nil
	}
	appendKeys := func(raw json.RawMessage) ([]string, error) {
		var existing []string
		if len(raw) != 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &existing); err != nil {
				return nil, errors.New("AP config has invalid SSH key list")
			}
		}
		seen := make(map[string]bool, len(existing)+len(siteKeys))
		identity := func(line string) string {
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
			if err == nil {
				return string(key.Marshal())
			}
			return line
		}
		for _, key := range existing {
			seen[identity(key)] = true
		}
		for _, key := range siteKeys {
			id := identity(key)
			if !seen[id] {
				existing = append(existing, key)
				seen[id] = true
			}
		}
		return existing, nil
	}
	rootKeys, err := appendKeys(root["RootAuthorizedSSHKeys"])
	if err != nil {
		return err
	}
	if err := setJSON(root, "RootAuthorizedSSHKeys", rootKeys); err != nil {
		return err
	}
	var sshConfig map[string]json.RawMessage
	if len(root["SSH"]) != 0 && string(root["SSH"]) != "null" {
		if err := json.Unmarshal(root["SSH"], &sshConfig); err != nil {
			return errors.New("AP config has invalid SSH settings")
		}
	}
	if sshConfig == nil {
		sshConfig = make(map[string]json.RawMessage)
	}
	sshKeys, err := appendKeys(sshConfig["AuthorizedKeys"])
	if err != nil {
		return err
	}
	if err := setJSON(sshConfig, "AuthorizedKeys", sshKeys); err != nil {
		return err
	}
	if err := setJSON(sshConfig, "Disabled", false); err != nil {
		return err
	}
	if err := setJSON(sshConfig, "External", true); err != nil {
		return err
	}
	return setJSON(root, "SSH", sshConfig)
}

// The AP's Ethernet VLAN allow list must include every active local WLAN VLAN.
// Advanced site and AP patches can replace SwitchPorts when a port needs
// explicitly different forwarding.
func addWLANVLANsToSwitchPorts(root map[string]json.RawMessage, networks []model.Network) (json.RawMessage, error) {
	raw, present := root["SwitchPorts"]
	if !present || string(raw) == "null" {
		return json.Marshal(root)
	}
	var ports []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ports); err != nil {
		return nil, errors.New("AP config has invalid SwitchPorts")
	}
	for _, port := range ports {
		var vlans []string
		if raw, present := port["Vlans"]; present {
			if err := json.Unmarshal(raw, &vlans); err != nil {
				return nil, errors.New("AP config has invalid SwitchPorts Vlans")
			}
		}
		seen := make(map[string]bool, len(vlans))
		for _, vlan := range vlans {
			seen[vlan] = true
		}
		for _, network := range networks {
			if !network.Enabled {
				continue
			}
			vlan := strconv.Itoa(network.VLANID)
			if !seen[vlan] {
				vlans = append(vlans, vlan)
				seen[vlan] = true
			}
		}
		if err := setJSON(port, "Vlans", vlans); err != nil {
			return nil, err
		}
	}
	if err := setJSON(root, "SwitchPorts", ports); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}

func setJSON(object map[string]json.RawMessage, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	object[key] = raw
	return nil
}
