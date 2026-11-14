package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SecretSize is the length of an AP's EEPROM authentication secret.
const SecretSize = 32

type APRecord struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	SiteID       string `json:"site_id,omitempty"`
	ManagementIP string `json:"management_ip,omitempty"`
	Model        string `json:"model,omitempty"`
	Firmware     string `json:"firmware,omitempty"`
	// Secret is the AP's 32-byte EEPROM authentication secret. It lives in
	// the private database and is never returned by the API.
	Secret []byte `json:"secret,omitempty"`
	// SecretFile is a legacy path to the secret on disk. dismistd imports it
	// into Secret at startup and clears it.
	SecretFile      string          `json:"secret_file,omitempty"`
	ConfigRevision  uint64          `json:"config_revision,omitempty"`
	ConfigUpdatedAt time.Time       `json:"config_updated_at,omitzero"`
	AppliedRevision uint64          `json:"applied_revision,omitempty"`
	Advanced        json.RawMessage `json:"advanced,omitempty"`
}

type Site struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Country       string          `json:"country,omitempty"`
	Timezone      string          `json:"timezone,omitempty"`
	SSHPublicKeys []string        `json:"ssh_public_keys,omitempty"`
	Advanced      json.RawMessage `json:"advanced,omitempty"`
}

type Network struct {
	ID       string          `json:"id"`
	SiteID   string          `json:"site_id,omitempty"`
	SSID     string          `json:"ssid"`
	PSK      string          `json:"psk"`
	VLANID   int             `json:"vlan_id"`
	Enabled  bool            `json:"enabled"`
	Advanced json.RawMessage `json:"advanced,omitempty"`
}

type State struct {
	Revision  uint64     `json:"revision"`
	UpdatedAt time.Time  `json:"updated_at"`
	Sites     []Site     `json:"sites,omitempty"`
	APs       []APRecord `json:"aps"`
	Networks  []Network  `json:"networks"`
}

type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

type StateStore interface {
	Snapshot() State
	Update(func(*State) error) error
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, state: State{APs: []APRecord{}, Networks: []Network{}}}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("state file must be private")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &store.state); err != nil {
		return nil, err
	}
	if err := validate(store.state); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.state)
}

func (s *Store) Update(change func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.state)
	for index := range next.APs {
		if next.APs[index].ConfigRevision == 0 {
			next.APs[index].ConfigRevision = s.state.Revision
			next.APs[index].ConfigUpdatedAt = s.state.UpdatedAt
		}
	}
	next.UpdatedAt = time.Now().UTC()
	if err := change(&next); err != nil {
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	next.Revision = s.state.Revision + 1
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := writeAtomic(s.path, raw); err != nil {
		return err
	}
	s.state = next
	return nil
}

func clone(state State) State {
	state.APs = append([]APRecord(nil), state.APs...)
	state.Networks = append([]Network(nil), state.Networks...)
	state.Sites = append([]Site(nil), state.Sites...)
	for index := range state.Sites {
		state.Sites[index].Advanced = append(json.RawMessage(nil), state.Sites[index].Advanced...)
		state.Sites[index].SSHPublicKeys = append([]string(nil), state.Sites[index].SSHPublicKeys...)
	}
	for index := range state.APs {
		state.APs[index].Advanced = append(json.RawMessage(nil), state.APs[index].Advanced...)
	}
	for index := range state.Networks {
		state.Networks[index].Advanced = append(json.RawMessage(nil), state.Networks[index].Advanced...)
	}
	return state
}

func validate(state State) error {
	sites := map[string]bool{}
	for _, site := range state.Sites {
		if site.ID == "" || site.Name == "" || sites[site.ID] || validateAdvanced(site.Advanced) != nil {
			return errors.New("invalid or duplicate site")
		}
		if _, err := NormalizeSSHPublicKeys(site.SSHPublicKeys); err != nil {
			return err
		}
		sites[site.ID] = true
	}
	seenAP := map[string]bool{}
	for _, ap := range state.APs {
		if ap.ID == "" || (len(ap.Secret) != SecretSize && ap.SecretFile == "") || seenAP[ap.ID] || (len(sites) > 0 && ap.SiteID != "" && !sites[ap.SiteID]) || validateAdvanced(ap.Advanced) != nil {
			return errors.New("invalid or duplicate AP record")
		}
		seenAP[ap.ID] = true
	}
	seenNetwork := map[string]bool{}
	for _, network := range state.Networks {
		if network.ID == "" || seenNetwork[network.SiteID+"/"+network.ID] || (len(sites) > 0 && !sites[network.SiteID]) {
			return errors.New("invalid or duplicate network ID")
		}
		seenNetwork[network.SiteID+"/"+network.ID] = true
		if len(network.SSID) < 1 || len(network.SSID) > 32 || network.VLANID < 1 || network.VLANID > 4094 ||
			len(network.PSK) < 8 || len(network.PSK) > 63 || validateAdvanced(network.Advanced) != nil {
			return fmt.Errorf("invalid network %s", network.ID)
		}
	}
	return nil
}

func validateAdvanced(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 1<<20 || raw[0] != '{' || !json.Valid(raw) {
		return errors.New("advanced config must be a JSON object below 1 MiB")
	}
	return nil
}

func writeAtomic(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".mist-state-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
