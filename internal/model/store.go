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
	// LocalFiles lists the /rw/etc/mist/ep-control/local_* files found on the
	// AP by the CLI. nil means never checked; an empty, non-nil list means
	// checked and none found (omitzero keeps the difference on disk). The
	// normaliser's rule 2a depends on it (design §4.5).
	LocalFiles []LocalFile `json:"local_files,omitzero"`
	// Features holds per-AP protocol feature switches (design §5.7). Absent
	// means the daemon's -ap-features default.
	Features map[string]bool `json:"protocol_features,omitempty"`
	// UpdatedRev is the state revision that last changed this record. The
	// store maintains it; it backs If-Match (design §4.8).
	UpdatedRev uint64 `json:"updated_rev,omitempty"`
}

// LocalFile is one local_* file the CLI found on an AP.
type LocalFile struct {
	Name   string    `json:"name"`
	SHA256 string    `json:"sha256"`
	SeenAt time.Time `json:"seen_at"`
}

type Site struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Country       string          `json:"country,omitempty"`
	Timezone      string          `json:"timezone,omitempty"`
	SSHPublicKeys []string        `json:"ssh_public_keys,omitempty"`
	Advanced      json.RawMessage `json:"advanced,omitempty"`
	UpdatedRev    uint64          `json:"updated_rev,omitempty"`
}

type Network struct {
	ID       string          `json:"id"`
	SiteID   string          `json:"site_id,omitempty"`
	SSID     string          `json:"ssid"`
	PSK      string          `json:"psk"`
	VLANID   int             `json:"vlan_id"`
	Enabled  bool            `json:"enabled"`
	Advanced json.RawMessage `json:"advanced,omitempty"`
	// UpdatedRev is maintained by the store, as for APRecord.
	UpdatedRev uint64 `json:"updated_rev,omitempty"`
}

type State struct {
	Revision  uint64     `json:"revision"`
	UpdatedAt time.Time  `json:"updated_at"`
	Sites     []Site     `json:"sites,omitempty"`
	APs       []APRecord `json:"aps"`
	Networks  []Network  `json:"networks"`
}

// Store is the legacy JSON-file state store. dismistd uses BoltStore; Store
// remains for tests and the one-time import of a legacy state file.
type Store struct {
	mu    sync.RWMutex
	path  string
	state State
	mem   memBuckets
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
	return s.UpdateTx(func(state *State, _ *Tx) error { return change(state) })
}

// UpdateTx implements TxStateStore. The JSON store keeps ap_config in memory
// only (for the life of the process); writes to it are discarded when the
// change fails.
func (s *Store) UpdateTx(change func(*State, *Tx) error) error {
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
	staged := memBuckets{apConfig: make(map[string][]byte, len(s.mem.apConfig))}
	for key, value := range s.mem.apConfig {
		staged.apConfig[key] = value
	}
	if err := change(&next, &Tx{mem: &staged}); err != nil {
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	next.Revision = s.state.Revision + 1
	stampUpdatedRev(&s.state, &next)
	present := make(map[string]bool, len(next.APs))
	for _, ap := range next.APs {
		present[ap.ID] = true
	}
	for id := range staged.apConfig {
		if !present[id] {
			delete(staged.apConfig, id)
		}
	}
	raw, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := writeAtomic(s.path, raw); err != nil {
		return err
	}
	s.state = next
	s.mem = staged
	return nil
}

// stampUpdatedRev sets UpdatedRev to next.Revision on every site, AP and
// network that is new or whose stored JSON changed in this write (for APs,
// ignoring the system-maintained config revision fields).
func stampUpdatedRev(previous, next *State) {
	encode := func(value any) string {
		raw, _ := json.Marshal(value)
		return string(raw)
	}
	sites := make(map[string]Site, len(previous.Sites))
	for _, site := range previous.Sites {
		sites[site.ID] = site
	}
	for index := range next.Sites {
		current := &next.Sites[index]
		old, existed := sites[current.ID]
		old.UpdatedRev = 0
		cmp := *current
		cmp.UpdatedRev = 0
		current.UpdatedRev = sites[current.ID].UpdatedRev
		if !existed || encode(old) != encode(cmp) {
			current.UpdatedRev = next.Revision
		}
	}
	aps := make(map[string]APRecord, len(previous.APs))
	for _, ap := range previous.APs {
		aps[ap.ID] = ap
	}
	for index := range next.APs {
		current := &next.APs[index]
		old, existed := aps[current.ID]
		cmp := *current
		// Config versions are maintained by the system (change detection
		// and the AP's apply reports), not edited: they do not move
		// updated_rev.
		for _, record := range []*APRecord{&old, &cmp} {
			record.UpdatedRev, record.ConfigRevision, record.AppliedRevision = 0, 0, 0
			record.ConfigUpdatedAt = time.Time{}
		}
		current.UpdatedRev = aps[current.ID].UpdatedRev
		if !existed || encode(old) != encode(cmp) {
			current.UpdatedRev = next.Revision
		}
	}
	networks := make(map[string]Network, len(previous.Networks))
	for _, network := range previous.Networks {
		networks[network.SiteID+"\x00"+network.ID] = network
	}
	for index := range next.Networks {
		current := &next.Networks[index]
		key := current.SiteID + "\x00" + current.ID
		old, existed := networks[key]
		old.UpdatedRev = 0
		cmp := *current
		cmp.UpdatedRev = 0
		current.UpdatedRev = networks[key].UpdatedRev
		if !existed || encode(old) != encode(cmp) {
			current.UpdatedRev = next.Revision
		}
	}
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
		ap := &state.APs[index]
		ap.Advanced = append(json.RawMessage(nil), ap.Advanced...)
		ap.Secret = append([]byte(nil), ap.Secret...)
		if ap.LocalFiles != nil {
			ap.LocalFiles = append(make([]LocalFile, 0, len(ap.LocalFiles)), ap.LocalFiles...)
		}
		if ap.Features != nil {
			features := make(map[string]bool, len(ap.Features))
			for name, on := range ap.Features {
				features[name] = on
			}
			ap.Features = features
		}
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
