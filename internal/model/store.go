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

	// Schema-2 typed fields (design §4.3); new keys only.

	// Expert is the AP expert patch, stored folded (canonical keys).
	Expert        json.RawMessage `json:"expert,omitempty"`
	LEDBrightness *int            `json:"led_brightness,omitempty"`
	SSHEnabled    *bool           `json:"ssh_enabled,omitempty"`
	// RF holds per-band overrides keyed "2.4" and "5".
	RF map[string]*RadioOverride `json:"rf_override,omitempty"`
	// RFPlan is written by rfplan (initial assignment or planner).
	RFPlan           map[string]PlannedRadio `json:"rf_plan,omitempty"`
	Management       *ManagementOverride     `json:"management,omitempty"` // experimental
	Eth1             *Eth1                   `json:"eth1,omitempty"`       // experimental
	WiredClientStats *bool                   `json:"wired_client_stats,omitempty"`
	// LocateUntil blinks the LED (LED.Locator) until this time.
	LocateUntil time.Time `json:"locate_until,omitzero"`
	// PushMode is learned by the controller: "notify" or "reconnect".
	PushMode string `json:"push_mode,omitempty"`
}

// LocalFile is one local_* file the CLI found on an AP.
type LocalFile struct {
	Name   string    `json:"name"`
	SHA256 string    `json:"sha256"`
	SeenAt time.Time `json:"seen_at"`
}

type Site struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Country  string `json:"country,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	// SSHPublicKeys is the schema-1 key list (design calls it
	// LegacySSHKeys); kept byte-identical until the migration is finalised.
	SSHPublicKeys []string `json:"ssh_public_keys,omitempty"`
	// Advanced is the schema-1 site patch (LegacyAdvanced); same rule.
	Advanced   json.RawMessage `json:"advanced,omitempty"`
	UpdatedRev uint64          `json:"updated_rev,omitempty"`

	// Schema-2 typed fields (design §4.3); new keys only.
	SSH          SiteSSH            `json:"ssh,omitzero"`
	Syslog       []SyslogTarget     `json:"syslog,omitempty"`
	Management   ManagementDefaults `json:"management,omitzero"`
	APDefaults   APDefaults         `json:"ap_defaults,omitzero"`
	Protection   Protection         `json:"protection,omitzero"`
	RF           RFProfile          `json:"rf,omitzero"`
	Roaming      SiteRoaming        `json:"roaming,omitzero"`
	Experimental bool               `json:"experimental,omitempty"`
	// Expert is the site expert patch, stored folded (canonical keys).
	Expert json.RawMessage `json:"expert,omitempty"`
}

type Network struct {
	ID      string `json:"id"`
	SiteID  string `json:"site_id,omitempty"`
	SSID    string `json:"ssid"`
	PSK     string `json:"psk"`
	VLANID  int    `json:"vlan_id"`
	Enabled bool   `json:"enabled"`
	// Advanced is the schema-1 network patch (LegacyAdvanced); kept
	// byte-identical until the migration is finalised.
	Advanced json.RawMessage `json:"advanced,omitempty"`
	// UpdatedRev is maintained by the store, as for APRecord.
	UpdatedRev uint64 `json:"updated_rev,omitempty"`

	// Schema-2 typed fields (design §4.3, §3.9); new keys only.
	Security   string        `json:"security,omitempty"`
	PMF        string        `json:"pmf,omitempty"` // "disabled" | "optional" | "required"; WPA2 modes only
	RADIUS     *RADIUSConfig `json:"radius,omitempty"`
	MPSK       *MultiPSK     `json:"mpsk,omitempty"`
	Bands      []string      `json:"bands,omitempty"`
	Hidden     bool          `json:"hidden,omitempty"`
	Isolation  string        `json:"isolation,omitempty"`   // "", "clients", "clients_and_lan"
	BSSIDIndex int           `json:"bssid_index,omitempty"` // 1..15, never 0
	// OWEIndex is reserved for the OWE transition companion.
	OWEIndex          int            `json:"owe_bssid_index,omitempty"`
	BandSteering      bool           `json:"band_steering,omitempty"`
	Rates             string         `json:"rates,omitempty"`    // "compatible" | "no_legacy" | "high_density" | "legacy_only"
	MinRSSI           map[string]int `json:"min_rssi,omitempty"` // experimental, per band
	DTIM              int            `json:"dtim,omitempty"`
	IdleTimeout       int            `json:"idle_timeout_sec,omitempty"`
	MaxClients        int            `json:"max_clients,omitempty"`
	Roaming           Roaming        `json:"roaming,omitzero"`
	Broadcast         Broadcast      `json:"broadcast,omitzero"`
	DHCPOnly          bool           `json:"dhcp_only,omitempty"`
	DHCPDNSOnly       bool           `json:"dhcp_dns_only,omitempty"`
	WMMDisabled       bool           `json:"wmm_disabled,omitempty"`
	UAPSDDisabled     bool           `json:"uapsd_disabled,omitempty"`
	Disable11ax       bool           `json:"disable_11ax,omitempty"`
	Disable11nac      bool           `json:"disable_11nac,omitempty"`
	GroupRekeySec     int            `json:"group_rekey_sec,omitempty"`
	EAPOLv1           bool           `json:"eapol_v1,omitempty"`
	SAEH2EOnly        bool           `json:"sae_h2e_only,omitempty"`
	TransitionDisable bool           `json:"transition_disable,omitempty"`
	OWESSID           string         `json:"owe_ssid,omitempty"`   // experimental
	RateLimit         *RateLimit     `json:"rate_limit,omitempty"` // experimental
	QoSClass          string         `json:"qos_class,omitempty"`  // experimental: "", "best_effort", "background", "video", "voice"
	// HonourBlocklist enforces the site's blocked clients (BlackList.Enabled).
	HonourBlocklist bool `json:"honour_blocklist,omitempty"`
	// UcastDisassocOnBssDown is an expert-tier default: new WLANs true,
	// migrated WLANs nil (absent).
	UcastDisassocOnBssDown *bool `json:"ucast_disassoc_on_bss_down,omitempty"`
	// ReclaimTimeoutSec is an expert-tier default: new 300, legacy 600.
	ReclaimTimeoutSec int       `json:"reclaim_timeout_sec,omitempty"`
	Option82          *Option82 `json:"option82,omitempty"` // phase 4
	Schedule          *Schedule `json:"schedule,omitempty"` // experimental, phase 4
	// Expert is the WLAN expert patch, stored folded (canonical keys).
	Expert json.RawMessage `json:"expert,omitempty"`
}

type State struct {
	Revision  uint64     `json:"revision"`
	UpdatedAt time.Time  `json:"updated_at"`
	Sites     []Site     `json:"sites,omitempty"`
	APs       []APRecord `json:"aps"`
	Networks  []Network  `json:"networks"`
	// Schema is the config schema the compiler uses for this state: 2 once
	// the schema-2 migration has committed (and no older binary has
	// written since), otherwise 1 (legacy mode, legacyv0 rules). The bbolt
	// store derives it from meta at read time and never stores it in the
	// state buckets; the JSON store keeps it in the file.
	Schema uint64 `json:"schema,omitempty"`
}

// Typed reports whether the state compiles with the schema-2 (typed)
// pipeline.
func (s State) Typed() bool { return s.Schema >= 2 }

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
	staged := memBuckets{apConfig: make(map[string][]byte, len(s.mem.apConfig)), history: append([]ChangeRecord(nil), s.mem.history...)}
	for key, value := range s.mem.apConfig {
		staged.apConfig[key] = value
	}
	tx := &Tx{mem: &staged}
	if err := change(&next, tx); err != nil {
		return err
	}
	if tx.schemaSet != 0 {
		next.Schema = tx.schemaSet
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
		site := &state.Sites[index]
		site.Advanced = append(json.RawMessage(nil), site.Advanced...)
		site.SSHPublicKeys = append([]string(nil), site.SSHPublicKeys...)
		site.Expert = append(json.RawMessage(nil), site.Expert...)
		site.SSH = cloneJSON(site.SSH)
		site.Syslog = cloneJSON(site.Syslog)
		site.Management = cloneJSON(site.Management)
		site.APDefaults = cloneJSON(site.APDefaults)
		site.Protection = cloneJSON(site.Protection)
		site.RF = cloneJSON(site.RF)
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
		ap.Expert = append(json.RawMessage(nil), ap.Expert...)
		if ap.LEDBrightness != nil {
			value := *ap.LEDBrightness
			ap.LEDBrightness = &value
		}
		if ap.SSHEnabled != nil {
			value := *ap.SSHEnabled
			ap.SSHEnabled = &value
		}
		if ap.WiredClientStats != nil {
			value := *ap.WiredClientStats
			ap.WiredClientStats = &value
		}
		if ap.RF != nil {
			ap.RF = cloneJSON(ap.RF)
		}
		if ap.RFPlan != nil {
			plans := make(map[string]PlannedRadio, len(ap.RFPlan))
			for band, plan := range ap.RFPlan {
				plans[band] = plan
			}
			ap.RFPlan = plans
		}
		if ap.Management != nil {
			ap.Management = cloneJSON(ap.Management)
		}
		if ap.Eth1 != nil {
			ap.Eth1 = cloneJSON(ap.Eth1)
		}
	}
	for index := range state.Networks {
		network := &state.Networks[index]
		network.Advanced = append(json.RawMessage(nil), network.Advanced...)
		network.Expert = append(json.RawMessage(nil), network.Expert...)
		network.Bands = append([]string(nil), network.Bands...)
		for _, field := range []func(){
			func() { network.RADIUS = cloneJSON(network.RADIUS) },
			func() { network.MPSK = cloneJSON(network.MPSK) },
			func() { network.RateLimit = cloneJSON(network.RateLimit) },
			func() { network.Option82 = cloneJSON(network.Option82) },
			func() { network.Schedule = cloneJSON(network.Schedule) },
			func() { network.UcastDisassocOnBssDown = cloneJSON(network.UcastDisassocOnBssDown) },
		} {
			field()
		}
		if network.MinRSSI != nil {
			minRSSI := make(map[string]int, len(network.MinRSSI))
			for band, value := range network.MinRSSI {
				minRSSI[band] = value
			}
			network.MinRSSI = minRSSI
		}
	}
	return state
}

// ErrInvalid marks the errors of the store's own state rules (a duplicate
// or missing ID, an out-of-range network value, a malformed advanced
// patch): the state a mutation produced is refused as it is, and retrying
// will not help. errors.Is(err, ErrInvalid) tells them apart from storage
// failures (an I/O error, a full disk), which are not the caller's fault.
var ErrInvalid = errors.New("invalid state")

// invalidState is a validate error: its message, matching ErrInvalid.
type invalidState struct{ err error }

func (e invalidState) Error() string        { return e.err.Error() }
func (e invalidState) Unwrap() error        { return e.err }
func (e invalidState) Is(target error) bool { return target == ErrInvalid }

func validate(state State) error {
	if err := checkState(state); err != nil {
		return invalidState{err: err}
	}
	return nil
}

func checkState(state State) error {
	sites := map[string]bool{}
	for _, site := range state.Sites {
		if site.ID == "" || site.Name == "" || sites[site.ID] || validateAdvanced(site.Advanced) != nil || validateAdvanced(site.Expert) != nil {
			return errors.New("invalid or duplicate site")
		}
		if _, err := NormalizeSSHPublicKeys(site.SSHPublicKeys); err != nil {
			return err
		}
		sites[site.ID] = true
	}
	seenAP := map[string]bool{}
	for _, ap := range state.APs {
		if ap.ID == "" || (len(ap.Secret) != SecretSize && ap.SecretFile == "") || seenAP[ap.ID] || (len(sites) > 0 && ap.SiteID != "" && !sites[ap.SiteID]) || validateAdvanced(ap.Advanced) != nil || validateAdvanced(ap.Expert) != nil {
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
			!validNetworkPSK(network) || validateAdvanced(network.Advanced) != nil || validateAdvanced(network.Expert) != nil {
			return fmt.Errorf("invalid network %s", network.ID)
		}
	}
	return nil
}

// validNetworkPSK applies the passphrase rules of the network's security
// (design §3.9): schema-1 networks and WPA2-Personal take 8 to 63
// characters (WPA2-Personal also exactly 64 hex digits); the SAE modes 8 to
// 63 printable ASCII; modes without a passphrase must not carry one.
func validNetworkPSK(network Network) bool {
	switch network.Security {
	case "":
		return len(network.PSK) >= 8 && len(network.PSK) <= 63
	case SecurityWPA2Personal:
		return ValidPassphrase(network.PSK) || IsHexPMK(network.PSK)
	case SecurityWPA2WPA3Personal, SecurityWPA3Personal:
		return ValidPassphrase(network.PSK)
	}
	return network.PSK == ""
}

// ValidPassphrase reports whether psk is 8 to 63 printable ASCII
// characters.
func ValidPassphrase(psk string) bool {
	if len(psk) < 8 || len(psk) > 63 {
		return false
	}
	for index := 0; index < len(psk); index++ {
		if psk[index] < 0x20 || psk[index] > 0x7e {
			return false
		}
	}
	return true
}

// IsHexPMK reports whether psk is exactly 64 hex digits (a raw PMK).
func IsHexPMK(psk string) bool {
	if len(psk) != 64 {
		return false
	}
	for index := 0; index < len(psk); index++ {
		c := psk[index]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
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
