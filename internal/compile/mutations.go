package compile

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options"
	"github.com/nicko170/dismist/internal/options/regdomain"
	"github.com/nicko170/dismist/internal/rfplan"
)

// Helpers the API's config mutations use (design §4.2, §4.4, §4.6, §4.8).

// NewSiteDefaults fills a site created after schema 2 with the research
// defaults (design §4.6 "Default for new objects"). Identity fields are
// left to the caller.
func NewSiteDefaults(site *model.Site) {
	site.SSH = model.SiteSSH{Enabled: false}
	site.Management = model.ManagementDefaults{VLANID: 1}
	site.APDefaults = model.APDefaults{LEDBrightness: 255, HostnameFromName: true, PoEConstrained: model.PoEKeepRadios}
	site.Protection = model.Protection{ProxyARP: true}
	_, supported := regdomain.CCRev(site.Country)
	site.RF = model.RFProfile{Managed: supported,
		Band24: model.BandProfile{Enabled: true, Width: 20, Power: rfplan.DefaultPower["2.4"], Preamble: "short"},
		Band5:  model.BandProfile{Enabled: true, Width: 80, Power: rfplan.DefaultPower["5"], Preamble: "short"},
	}
	if domain, ok := regdomain.ForCountry(site.Country); ok {
		site.RF.Band24.Channels = append([]int(nil), domain.DefaultAuto["2.4"]...)
		site.RF.Band5.Channels = append([]int(nil), domain.DefaultAuto["5"]...)
	}
}

// NewNetworkDefaults fills a network created after schema 2 with the
// research defaults and assigns its BSSID index (lowest free from 1; an OWE
// transition network also reserves its companion's). Fields the caller set
// before calling are overwritten only where they are zero.
func NewNetworkDefaults(state model.State, network *model.Network) error {
	if network.Security == "" {
		network.Security = model.SecurityWPA2WPA3Personal
	}
	if len(network.Bands) == 0 {
		network.Bands = []string{"2.4", "5"}
	}
	if network.VLANID == 0 {
		network.VLANID = 1
	}
	if network.Rates == "" {
		network.Rates = "compatible"
	}
	if network.DTIM == 0 {
		network.DTIM = 2
	}
	if network.IdleTimeout == 0 {
		network.IdleTimeout = 1800
	}
	if network.ReclaimTimeoutSec == 0 {
		network.ReclaimTimeoutSec = 300
	}
	if network.GroupRekeySec == 0 {
		network.GroupRekeySec = 86400
	}
	if network.UcastDisassocOnBssDown == nil {
		on := true
		network.UcastDisassocOnBssDown = &on
	}
	if network.Roaming == (model.Roaming{}) {
		network.Roaming = model.Roaming{Fast: "none", NeighborReport: true, BSSTransition: true, BSSLoad: true}
	}
	if network.Broadcast == (model.Broadcast{}) {
		network.Broadcast = model.Broadcast{AllowIPv6ND: true}
	}
	network.HonourBlocklist = true
	if network.BSSIDIndex == 0 {
		index, owe, err := AssignBSSIDIndex(state, network.SiteID, network.Security == model.SecurityOWETransition)
		if err != nil {
			return err
		}
		network.BSSIDIndex, network.OWEIndex = index, owe
	}
	return nil
}

// EnsureRoamingKeys generates the site roaming key (32 random bytes, hex)
// for every site where a WLAN uses AP-to-AP key caching and none exists
// yet. It returns the IDs of the sites it changed.
func EnsureRoamingKeys(state *model.State) ([]string, error) {
	need := map[string]bool{}
	for _, network := range state.Networks {
		if network.Roaming.APKeyCache {
			need[network.SiteID] = true
		}
	}
	var changed []string
	for index := range state.Sites {
		site := &state.Sites[index]
		if !need[site.ID] || site.Roaming.KeyEncryptKey != "" {
			continue
		}
		key, err := roamingKey()
		if err != nil {
			return nil, err
		}
		site.Roaming.KeyEncryptKey = key
		changed = append(changed, site.ID)
	}
	return changed, nil
}

// RotateRoamingKey replaces a site's roaming key (a wlan_restart change on
// every WLAN that uses it).
func RotateRoamingKey(state *model.State, siteID string) error {
	for index := range state.Sites {
		if state.Sites[index].ID == siteID {
			key, err := roamingKey()
			if err != nil {
				return err
			}
			state.Sites[index].Roaming.KeyEncryptKey = key
			return nil
		}
	}
	return fmt.Errorf("site %s not found", siteID)
}

func roamingKey() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// NormaliseMAC returns a client MAC in the stored form (lower case, dashed)
// or an error.
func NormaliseMAC(mac string) (string, error) {
	clean := strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(strings.TrimSpace(mac)))
	if len(clean) != 12 {
		return "", fmt.Errorf("invalid MAC %q", mac)
	}
	if _, err := hex.DecodeString(clean); err != nil {
		return "", fmt.Errorf("invalid MAC %q", mac)
	}
	parts := make([]string, 6)
	for index := range parts {
		parts[index] = clean[index*2 : index*2+2]
	}
	return strings.Join(parts, "-"), nil
}

// BlockClient adds a client to a site's blocked list (phase 3); it reports
// whether the list changed.
func BlockClient(state *model.State, siteID, mac, note string) (bool, error) {
	normalised, err := NormaliseMAC(mac)
	if err != nil {
		return false, err
	}
	for index := range state.Sites {
		site := &state.Sites[index]
		if site.ID != siteID {
			continue
		}
		for _, blocked := range site.Protection.BlockedClients {
			if blocked.MAC == normalised {
				return false, nil
			}
		}
		site.Protection.BlockedClients = append(site.Protection.BlockedClients, model.BlockedClient{MAC: normalised, Note: note})
		return true, nil
	}
	return false, fmt.Errorf("site %s not found", siteID)
}

// UnblockClient removes a client from a site's blocked list.
func UnblockClient(state *model.State, siteID, mac string) (bool, error) {
	normalised, err := NormaliseMAC(mac)
	if err != nil {
		return false, err
	}
	for index := range state.Sites {
		site := &state.Sites[index]
		if site.ID != siteID {
			continue
		}
		list := site.Protection.BlockedClients[:0]
		removed := false
		for _, blocked := range site.Protection.BlockedClients {
			if blocked.MAC == normalised {
				removed = true
				continue
			}
			list = append(list, blocked)
		}
		site.Protection.BlockedClients = list
		return removed, nil
	}
	return false, fmt.Errorf("site %s not found", siteID)
}

// ExperimentalUse is one experimental option or value in use.
type ExperimentalUse struct {
	Option   string `json:"option"`
	Scope    string `json:"scope"`
	ObjectID string `json:"object_id"`
	Value    any    `json:"value,omitempty"`
}

// ExperimentalInUse lists the experimental (needs-live-test) options and
// enum values a site's typed config uses, sorted.
func ExperimentalInUse(state model.State, siteID string) []ExperimentalUse {
	registry := options.Default()
	var uses []ExperimentalUse
	add := func(option, scope, objectID string, value any) {
		if opt, ok := registry.Get(option); ok && opt.Experimental(value) {
			uses = append(uses, ExperimentalUse{Option: option, Scope: scope, ObjectID: objectID, Value: value})
		}
	}
	for _, site := range state.Sites {
		if site.ID != siteID {
			continue
		}
		if len(site.SSH.ReadonlyKeys) > 0 {
			add("site.ssh.readonly_keys", ScopeSite, site.ID, nil)
		}
		if len(site.Syslog) > 0 {
			add("site.syslog", ScopeSite, site.ID, nil)
		}
		if site.Management.VLANID > 1 {
			add("site.management.vlan_id", ScopeSite, site.ID, site.Management.VLANID)
		}
		if site.APDefaults.Eth1 != nil && site.APDefaults.Eth1.Mode != "" && site.APDefaults.Eth1.Mode != model.Eth1SameAsUplink {
			add("site.ap_defaults.eth1", ScopeSite, site.ID, site.APDefaults.Eth1.Mode)
		}
		if site.APDefaults.Recovery != nil {
			add("site.ap_defaults.config_recovery", ScopeSite, site.ID, nil)
		}
		if site.Protection.PenaltyBox != nil {
			add("site.protection.penalty_box", ScopeSite, site.ID, nil)
		}
		if site.RF.Scanning != nil {
			add("site.rf.scanning_radio", ScopeSite, site.ID, *site.RF.Scanning)
		}
		for _, band := range rfplan.Bands {
			advanced := site.RF.Band(band).Advanced
			if advanced.MuMimo != nil {
				add(bandOption(band, "mu_mimo"), ScopeSite, site.ID, *advanced.MuMimo)
			}
			if advanced.TxBf != nil {
				add(bandOption(band, "txbf"), ScopeSite, site.ID, *advanced.TxBf)
			}
		}
	}
	for _, n := range state.Networks {
		if n.SiteID != siteID {
			continue
		}
		add("wlan.security", ScopeWLAN, n.ID, n.Security)
		add("wlan.rates", ScopeWLAN, n.ID, n.Rates)
		add("wlan.roaming.fast", ScopeWLAN, n.ID, n.Roaming.Fast)
		if n.QoSClass != "" && n.QoSClass != "none" {
			add("wlan.qos_class", ScopeWLAN, n.ID, nil)
		}
		if n.OWESSID != "" {
			add("wlan.owe_ssid", ScopeWLAN, n.ID, nil)
		}
		if n.Roaming.APKeyCache {
			add("wlan.roaming.ap_key_cache", ScopeWLAN, n.ID, nil)
		}
		if n.MinRSSI["2.4"] != 0 {
			add("wlan.min_rssi.band_24", ScopeWLAN, n.ID, nil)
		}
		if n.MinRSSI["5"] != 0 {
			add("wlan.min_rssi.band_5", ScopeWLAN, n.ID, nil)
		}
		if n.Broadcast.McastToUcast {
			add("wlan.broadcast.mcast_to_ucast", ScopeWLAN, n.ID, nil)
		}
		if n.RateLimit != nil && *n.RateLimit != (model.RateLimit{}) {
			add("wlan.rate_limit.wlan_up", ScopeWLAN, n.ID, nil)
		}
		if n.RADIUS != nil && n.RADIUS.DynamicVLAN != nil && n.RADIUS.DynamicVLAN.Enabled {
			add("wlan.radius.dynamic_vlan", ScopeWLAN, n.ID, nil)
		}
		if n.RADIUS != nil && n.RADIUS.CoA != nil && n.RADIUS.CoA.Enabled {
			add("wlan.radius.coa", ScopeWLAN, n.ID, nil)
		}
		if n.MPSK != nil && n.MPSK.Enabled {
			add("wlan.mpsk.enabled", ScopeWLAN, n.ID, nil)
		}
		if n.Schedule != nil && n.Schedule.Enabled {
			add("wlan.schedule", ScopeWLAN, n.ID, nil)
		}
	}
	for _, ap := range state.APs {
		if ap.SiteID != siteID {
			continue
		}
		if ap.Management != nil {
			add("ap.management.ip_mode", ScopeAP, ap.ID, nil)
		}
		if ap.Eth1 != nil && ap.Eth1.Mode != "" {
			add("ap.eth1", ScopeAP, ap.ID, ap.Eth1.Mode)
		}
	}
	// One entry per option and object.
	seen := map[string]bool{}
	var result []ExperimentalUse
	for _, use := range uses {
		key := use.Option + "\x00" + use.ObjectID
		if !seen[key] {
			seen[key] = true
			result = append(result, use)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Option != result[j].Option {
			return result[i].Option < result[j].Option
		}
		return result[i].ObjectID < result[j].ObjectID
	})
	return result
}

// CheckExperimental refuses a mutation that newly uses an experimental
// option or value on a site without experimental: true (design P4,
// §4.2): every experimental use after the change that was not in use
// before is reported. Stored values (for example lifted by the migration)
// are kept. Returns *options.ExperimentalError (422 experimental_disabled).
func CheckExperimental(before, after model.State, siteID string) error {
	for _, site := range after.Sites {
		if site.ID == siteID && site.Experimental {
			return nil
		}
	}
	old := map[string]bool{}
	for _, use := range ExperimentalInUse(before, siteID) {
		old[use.Option+"\x00"+use.ObjectID+"\x00"+fmt.Sprint(use.Value)] = true
	}
	var keys []string
	for _, use := range ExperimentalInUse(after, siteID) {
		if !old[use.Option+"\x00"+use.ObjectID+"\x00"+fmt.Sprint(use.Value)] {
			keys = append(keys, use.Option)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return &options.ExperimentalError{Keys: sortedUniqueStrings(keys)}
}

// untypedSite reports a site whose typed fields were never written (every
// typed write sets the PoE policy): a record created by a path that does
// not know schema 2.
func untypedSite(site model.Site) bool {
	return site.APDefaults == (model.APDefaults{}) && site.Management.VLANID == 0 && !site.RF.Managed &&
		site.RF.Band24.Width == 0 && site.RF.Band5.Width == 0 && !site.SSH.Enabled && len(site.SSH.Keys) == 0 &&
		!site.Protection.ProxyARP && len(site.Protection.BlockedClients) == 0
}

// withDefaults gives records that no typed write has touched the
// new-object defaults at compile time, so a site or WLAN created through
// a path that does not set typed fields never compiles with the LED off
// or 802.11k disabled. The BSSID index is never invented here: a network
// without one fails validation.
func withDefaults(site model.Site, networks []model.Network) (model.Site, []model.Network) {
	if untypedSite(site) {
		NewSiteDefaults(&site)
		site.SSH = model.SiteSSH{Enabled: len(site.SSHPublicKeys) > 0}
		for _, line := range site.SSHPublicKeys {
			site.SSH.Keys = append(site.SSH.Keys, model.SSHKey{Key: line})
		}
	}
	out := make([]model.Network, len(networks))
	for index, network := range networks {
		if network.Security == "" {
			index0 := network.BSSIDIndex
			security := network.Security
			_ = NewNetworkDefaults(model.State{}, &network)
			network.Security, network.BSSIDIndex = security, index0
		}
		out[index] = network
	}
	return site, out
}
