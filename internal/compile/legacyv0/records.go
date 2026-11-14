package legacyv0

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

// APRecord is the ap-identity AP record, frozen.
type APRecord struct {
	ID              string          `json:"id"`
	Name            string          `json:"name,omitempty"`
	SiteID          string          `json:"site_id,omitempty"`
	ManagementIP    string          `json:"management_ip,omitempty"`
	Model           string          `json:"model,omitempty"`
	Firmware        string          `json:"firmware,omitempty"`
	Secret          []byte          `json:"secret,omitempty"`
	SecretFile      string          `json:"secret_file,omitempty"`
	ConfigRevision  uint64          `json:"config_revision,omitempty"`
	ConfigUpdatedAt time.Time       `json:"config_updated_at,omitzero"`
	AppliedRevision uint64          `json:"applied_revision,omitempty"`
	Advanced        json.RawMessage `json:"advanced,omitempty"`
}

// Site is the ap-identity site record, frozen.
type Site struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Country       string          `json:"country,omitempty"`
	Timezone      string          `json:"timezone,omitempty"`
	SSHPublicKeys []string        `json:"ssh_public_keys,omitempty"`
	Advanced      json.RawMessage `json:"advanced,omitempty"`
}

// Network is the ap-identity network record, frozen.
type Network struct {
	ID       string          `json:"id"`
	SiteID   string          `json:"site_id,omitempty"`
	SSID     string          `json:"ssid"`
	PSK      string          `json:"psk"`
	VLANID   int             `json:"vlan_id"`
	Enabled  bool            `json:"enabled"`
	Advanced json.RawMessage `json:"advanced,omitempty"`
}

// State is the configuration state as ap-identity read it from bbolt.
type State struct {
	Revision  uint64
	UpdatedAt time.Time
	Sites     []Site
	APs       []APRecord
	Networks  []Network
}

// Raw is the raw bucket content: each value exactly as stored, in bucket
// (key) order, plus the meta revision and updated_at values.
type Raw struct {
	Revision  []byte
	UpdatedAt []byte
	Sites     [][]byte
	APs       [][]byte
	Networks  [][]byte
}

// ReadRaw copies the raw sites, aps and networks bucket values and the meta
// revision and updated_at from a bbolt transaction.
func ReadRaw(tx *bolt.Tx) (Raw, error) {
	var raw Raw
	meta := tx.Bucket([]byte("meta"))
	if meta == nil {
		return raw, errors.New("missing metadata bucket")
	}
	raw.Revision = append([]byte(nil), meta.Get([]byte("revision"))...)
	raw.UpdatedAt = append([]byte(nil), meta.Get([]byte("updated_at"))...)
	for _, item := range []struct {
		name string
		into *[][]byte
	}{{"sites", &raw.Sites}, {"aps", &raw.APs}, {"networks", &raw.Networks}} {
		bucket := tx.Bucket([]byte(item.name))
		if bucket == nil {
			return raw, fmt.Errorf("missing %s bucket", item.name)
		}
		if err := bucket.ForEach(func(_, value []byte) error {
			*item.into = append(*item.into, append([]byte(nil), value...))
			return nil
		}); err != nil {
			return raw, err
		}
	}
	return raw, nil
}

// Decode decodes raw bucket JSON into the frozen structs, as ap-identity's
// readBoltState did (without its validation: a compiler reports errors per
// AP instead of refusing the whole state).
func Decode(raw Raw) (State, error) {
	state := State{Sites: []Site{}, APs: []APRecord{}, Networks: []Network{}}
	if len(raw.Revision) == 8 {
		state.Revision = binary.BigEndian.Uint64(raw.Revision)
	}
	if len(raw.UpdatedAt) != 0 {
		if err := state.UpdatedAt.UnmarshalText(raw.UpdatedAt); err != nil {
			return state, err
		}
	}
	for _, value := range raw.Sites {
		var site Site
		if err := json.Unmarshal(value, &site); err != nil {
			return state, fmt.Errorf("site record: %w", err)
		}
		state.Sites = append(state.Sites, site)
	}
	for _, value := range raw.APs {
		var ap APRecord
		if err := json.Unmarshal(value, &ap); err != nil {
			return state, fmt.Errorf("AP record: %w", err)
		}
		state.APs = append(state.APs, ap)
	}
	for _, value := range raw.Networks {
		var network Network
		if err := json.Unmarshal(value, &network); err != nil {
			return state, fmt.Errorf("network record: %w", err)
		}
		state.Networks = append(state.Networks, network)
	}
	return state, nil
}

// ReadState is ReadRaw followed by Decode.
func ReadState(tx *bolt.Tx) (State, error) {
	raw, err := ReadRaw(tx)
	if err != nil {
		return State{}, err
	}
	return Decode(raw)
}

// ErrNoSite means the AP has no site, so v0 compiles nothing for it.
var ErrNoSite = errors.New("AP has no site; v0 does not compile it")

// NetworksForSite returns the networks v0 gives an AP of siteID, in stored
// order: those of the site, plus networks with an empty SiteID when the
// state has no sites at all (ap-identity's networkInSite legacy rule).
func (s State) NetworksForSite(siteID string) []Network {
	legacy := len(s.Sites) == 0
	networks := make([]Network, 0)
	for _, network := range s.Networks {
		if network.SiteID == siteID || (legacy && network.SiteID == "") {
			networks = append(networks, network)
		}
	}
	return networks
}

// Site returns the site with the given ID.
func (s State) Site(id string) (Site, bool) {
	for _, site := range s.Sites {
		if site.ID == id {
			return site, true
		}
	}
	return Site{}, false
}

// AP returns the AP with the given ID.
func (s State) AP(id string) (APRecord, bool) {
	for _, ap := range s.APs {
		if ap.ID == id {
			return ap, true
		}
	}
	return APRecord{}, false
}

// ConfigTime is the Timestamp source v0 uses for an AP: ConfigUpdatedAt,
// or the state's UpdatedAt for a record that predates it.
func (s State) ConfigTime(ap APRecord) time.Time {
	if ap.ConfigUpdatedAt.IsZero() {
		return s.UpdatedAt
	}
	return ap.ConfigUpdatedAt
}

// ConfigRevision is the revision v0 serves for an AP.
func (s State) ConfigRevision(ap APRecord) uint64 {
	if ap.ConfigRevision == 0 {
		return s.Revision
	}
	return ap.ConfigRevision
}

// CompileAP returns the config bytes the ap-identity daemon serves the AP
// (its prepareAPs path): the AP's site, that site's networks in stored
// order and the AP's config time, on the given template. Pass the runtime
// template, or Template(nil) for the bundled one.
func CompileAP(template []byte, state State, apID string) ([]byte, error) {
	ap, ok := state.AP(apID)
	if !ok {
		return nil, fmt.Errorf("AP %s not found", apID)
	}
	if ap.SiteID == "" {
		return nil, ErrNoSite
	}
	site, ok := state.Site(ap.SiteID)
	if !ok {
		return nil, fmt.Errorf("AP %s has no site", ap.ID)
	}
	return BuildSiteConfig(template, site, ap, state.NetworksForSite(ap.SiteID), state.ConfigTime(ap))
}
