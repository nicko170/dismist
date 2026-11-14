package model

import (
	"encoding/json"
	"time"
)

// Schema-2 typed fields (design §4.3). Every typed value lives under a new
// JSON key, so a schema-1 binary reading a record compiles exactly what it
// compiled before the migration. The legacy fields keep their Go names
// (Site.Advanced, Site.SSHPublicKeys, APRecord.Advanced, Network.Advanced)
// so existing callers keep compiling; only internal/compile/legacyv0 and
// internal/migrate interpret them once schema 2 has committed.

// SiteSSH is the site's SSH policy (design §3.11 Access).
type SiteSSH struct {
	Enabled bool     `json:"enabled"`
	Keys    []SSHKey `json:"keys,omitempty"` // root keys, comments kept
	// ReadonlyKeys are restricted stats keys (experimental).
	ReadonlyKeys []SSHKey `json:"readonly_keys,omitempty"`
}

// SSHKey is one OpenSSH public key with its comment.
type SSHKey struct {
	Key     string `json:"key"`
	Comment string `json:"comment,omitempty"`
}

// SyslogTarget is one remote syslog destination (experimental).
type SyslogTarget struct {
	Protocol string `json:"protocol"` // "udp" | "tcp"
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Facility int    `json:"facility"`
	Severity int    `json:"severity"`
	Tag      string `json:"tag,omitempty"`
}

// ManagementDefaults are the site's management-network defaults.
type ManagementDefaults struct {
	// VLANID 0 or 1 is untagged (native); 2 to 4094 is tagged
	// (experimental, needs config auto-recovery).
	VLANID int      `json:"vlan_id,omitempty"`
	DNS    []string `json:"dns,omitempty"`
	NTP    []string `json:"ntp,omitempty"`
}

// PoE-constrained policies.
const (
	PoEKeepRadios    = "keep_radios"
	PoEDisableRadios = "disable_radios"
)

// APDefaults are the site-wide AP behaviour defaults (design §3.11).
type APDefaults struct {
	LEDBrightness           int    `json:"led_brightness"`
	HostnameFromName        bool   `json:"hostname_from_name"`
	KeepWLANsUpOnUplinkDown bool   `json:"keep_wlans_up_on_uplink_down"`
	PoEConstrained          string `json:"poe_constrained,omitempty"` // PoEKeepRadios | PoEDisableRadios ("" = disable, as v0)
	WiredClientStats        bool   `json:"wired_client_stats"`
	// Eth1 is the default eth1 mode (experimental); nil = same as uplink.
	Eth1 *Eth1 `json:"eth1,omitempty"`
	// Recovery is config auto-recovery (experimental); nil = template.
	Recovery    *Recovery `json:"config_recovery,omitempty"`
	BLEDisabled bool      `json:"ble_disabled,omitempty"`
}

// eth1 modes.
const (
	Eth1SameAsUplink = "same_as_uplink"
	Eth1Access       = "access"
	Eth1Trunk        = "trunk"
	Eth1Disabled     = "disabled"
)

// Eth1 is the secondary port's mode (experimental).
type Eth1 struct {
	Mode   string `json:"mode"`
	VLANID int    `json:"vlan_id,omitempty"` // access VLAN, or the trunk's PVID
	VLANs  []int  `json:"vlans,omitempty"`   // trunk VLANs
}

// Recovery is config auto-recovery (APRecovery).
type Recovery struct {
	Enabled bool `json:"enabled"`
	Minutes int  `json:"minutes,omitempty"` // 1 to 60
}

// Protection holds the site's client-protection settings.
type Protection struct {
	BlockedClients  []BlockedClient `json:"blocked_clients,omitempty"`
	ProxyARP        bool            `json:"proxy_arp"`
	ARPSpoofCheck   bool            `json:"arp_spoof_check"`
	PenaltyBox      *PenaltyBox     `json:"penalty_box,omitempty"` // experimental
	DHCPFingerprint bool            `json:"dhcp_fingerprinting"`
}

// BlockedClient is one site-wide banned client.
type BlockedClient struct {
	MAC  string `json:"mac"` // lower-case dashed
	Note string `json:"note,omitempty"`
}

// PenaltyBox is the repeated-auth-failure penalty box.
type PenaltyBox struct {
	Threshold int `json:"threshold"`
	WindowSec int `json:"window_sec"`
}

// Power modes.
const (
	PowerFixed = "fixed"
	PowerAuto  = "auto"
)

// PowerPolicy is a band's transmit power: fixed dBm, or an Auto range the
// planner chooses within.
type PowerPolicy struct {
	Mode string `json:"mode"`
	DBm  int    `json:"dbm,omitempty"`
	Min  int    `json:"min,omitempty"`
	Max  int    `json:"max,omitempty"`
}

// BandAdvanced are the per-band advanced RF options. nil and "" leave the
// template's value.
type BandAdvanced struct {
	TAF          string `json:"taf,omitempty"`     // "default" | "off" | "on"
	TWT          *bool  `json:"twt,omitempty"`     // nil = template (on)
	Antenna      string `json:"antenna,omitempty"` // "default" | "4x4" | "3x3" | "2x2" | "1x1"
	MinProbeRSSI *int   `json:"min_probe_rssi,omitempty"`
	MuMimo       *bool  `json:"mu_mimo,omitempty"`
	TxBf         *bool  `json:"txbf,omitempty"`
}

// BandProfile is one band of the site RF profile.
type BandProfile struct {
	Enabled     bool         `json:"enabled"`
	Width       int          `json:"width"`
	Channels    []int        `json:"channels,omitempty"`     // allowed list for Auto (planner input only)
	DFS         bool         `json:"dfs,omitempty"`          // 5 GHz only
	DFSFallback int          `json:"dfs_fallback,omitempty"` // 0 = auto
	Power       PowerPolicy  `json:"power"`
	Preamble    string       `json:"preamble,omitempty"` // "auto" | "short" | "long"; "" = template
	Advanced    BandAdvanced `json:"advanced,omitzero"`
}

// RFProfile is the site RF profile (design §3.10).
type RFProfile struct {
	Managed  bool        `json:"managed"`
	Band24   BandProfile `json:"band_24"`
	Band5    BandProfile `json:"band_5"`
	Scanning *bool       `json:"scanning_radio,omitempty"` // experimental; nil = template (on)
}

// Band returns the profile of "2.4" or "5".
func (r *RFProfile) Band(band string) *BandProfile {
	if band == "2.4" {
		return &r.Band24
	}
	return &r.Band5
}

// SiteRoaming holds the site roaming key (AP-to-AP key caching).
type SiteRoaming struct {
	// KeyEncryptKey is 32 random bytes hex, generated on first use; a
	// secret, never in views.
	KeyEncryptKey string `json:"key_encrypt_key,omitempty"`
}

// Security values (design §3.9).
const (
	SecurityWPA2Personal       = "wpa2_personal"
	SecurityWPA2WPA3Personal   = "wpa2_wpa3_personal"
	SecurityWPA3Personal       = "wpa3_personal"
	SecurityOpen               = "open"
	SecurityOWE                = "owe"
	SecurityOWETransition      = "owe_transition"
	SecurityWPA2Enterprise     = "wpa2_enterprise"
	SecurityWPA2WPA3Enterprise = "wpa2_wpa3_enterprise"
	SecurityWPA3Enterprise     = "wpa3_enterprise"
)

// PersonalSecurity reports whether a security value uses a passphrase.
// "" is the schema-1 default (WPA2-Personal).
func PersonalSecurity(security string) bool {
	switch security {
	case "", SecurityWPA2Personal, SecurityWPA2WPA3Personal, SecurityWPA3Personal:
		return true
	}
	return false
}

// EnterpriseSecurity reports whether a security value uses RADIUS.
func EnterpriseSecurity(security string) bool {
	switch security {
	case SecurityWPA2Enterprise, SecurityWPA2WPA3Enterprise, SecurityWPA3Enterprise:
		return true
	}
	return false
}

// RADIUSServer is one authentication or accounting server.
type RADIUSServer struct {
	Host   string `json:"host"`
	Port   int    `json:"port,omitempty"`
	Secret string `json:"secret,omitempty"` // write-only
}

// DynamicVLAN is RADIUS-assigned VLANs.
type DynamicVLAN struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type,omitempty"` // "standard" | "airespace-interface-name"
	VLANs   []int  `json:"vlans,omitempty"`
}

// CoAServer is one Change-of-Authorisation peer.
type CoAServer struct {
	Host   string `json:"host"`
	Secret string `json:"secret,omitempty"` // write-only
	Port   int    `json:"port,omitempty"`   // local port, default 3799
}

// CoA is Change of Authorisation (experimental).
type CoA struct {
	Enabled                bool        `json:"enabled"`
	Servers                []CoAServer `json:"servers,omitempty"`
	EventTimestampOptional bool        `json:"event_timestamp_optional,omitempty"`
}

// RADIUSConfig holds the Enterprise and MAC-auth RADIUS settings (phase 4).
type RADIUSConfig struct {
	Auth                   []RADIUSServer `json:"auth,omitempty"`
	Acct                   []RADIUSServer `json:"acct,omitempty"`
	MACAuth                bool           `json:"mac_auth,omitempty"`
	AcctInterimSec         int            `json:"acct_interim_sec,omitempty"`
	NASID                  string         `json:"nas_id,omitempty"`
	NASIP                  string         `json:"nas_ip,omitempty"`
	DynamicVLAN            *DynamicVLAN   `json:"dynamic_vlan,omitempty"`
	ReauthSec              int            `json:"reauth_sec,omitempty"`
	ReauthOnSessionTimeout bool           `json:"reauth_on_session_timeout,omitempty"`
	CoA                    *CoA           `json:"coa,omitempty"`
}

// MPSKKey is one per-device passphrase.
type MPSKKey struct {
	ID         string    `json:"id"`   // UUID; Auth.MultiPSK.PSKs[].Name
	Name       string    `json:"name"` // KeyName
	Passphrase string    `json:"passphrase,omitempty"`
	VLANID     int       `json:"vlan_id,omitempty"` // 0 = the WLAN VLAN
	MAC        string    `json:"mac,omitempty"`
	Expires    time.Time `json:"expires,omitzero"`
}

// MultiPSK is per-device passphrases (phase 4, experimental).
type MultiPSK struct {
	Enabled bool      `json:"enabled"`
	Private bool      `json:"private,omitempty"`
	Keys    []MPSKKey `json:"keys,omitempty"`
}

// Roaming holds a WLAN's roaming options.
type Roaming struct {
	Fast           string `json:"fast,omitempty"` // "none" | "okc" | "11r"
	NeighborReport bool   `json:"neighbor_report"`
	BSSTransition  bool   `json:"bss_transition"`
	APSteering     bool   `json:"ap_steering"`
	BSSLoad        bool   `json:"bss_load"`
	APKeyCache     bool   `json:"ap_key_cache,omitempty"` // experimental, phase 3
}

// Broadcast holds a WLAN's broadcast and multicast filters.
type Broadcast struct {
	Limit              bool `json:"limit"`
	AllowMDNS          bool `json:"allow_mdns"`
	AllowSSDP          bool `json:"allow_ssdp"`
	AllowIPv6ND        bool `json:"allow_ipv6_nd"`
	ARPFilter          bool `json:"arp_filter"`
	McastToUcast       bool `json:"mcast_to_ucast"`
	WirelessBridgeDHCP bool `json:"wireless_bridge_dhcp"`
}

// RateLimit holds a WLAN's shapers in kbps; 0 = unlimited (experimental).
type RateLimit struct {
	WLANUp     int `json:"wlan_up,omitempty"`
	WLANDown   int `json:"wlan_down,omitempty"`
	ClientUp   int `json:"client_up,omitempty"`
	ClientDown int `json:"client_down,omitempty"`
}

// Option82 is DHCP Option 82 insertion (phase 4).
type Option82 struct {
	Enabled bool `json:"enabled"`
	// CircuitID may contain {SSID} and {AP_MAC}; dismist expands them.
	CircuitID string `json:"circuit_id,omitempty"`
}

// ScheduleWindow is one weekday's on-air window, in the site timezone.
type ScheduleWindow struct {
	Day   string `json:"day"`   // "mon" … "sun"
	Start string `json:"start"` // "HH:MM"
	End   string `json:"end"`   // "HH:MM"; "24:00" is the end of the day
}

// Schedule turns a WLAN off outside its windows (phase 4, experimental).
type Schedule struct {
	Enabled bool             `json:"enabled"`
	Windows []ScheduleWindow `json:"windows,omitempty"`
	// OffAir is the schedule's last evaluation (design §3.9): true while
	// the WLAN is outside its windows and the site's APs take config
	// pushes by notification. The API's schedule evaluator sets it every
	// minute through a normal config change, so the compiled config never
	// depends on the wall clock (a restart cannot change what an AP is
	// served at the same revision). Read-only in the API.
	OffAir bool `json:"off_air,omitempty"`
}

// RadioOverride is a per-AP radio override (design §3.6).
type RadioOverride struct {
	Enabled  *bool `json:"enabled,omitempty"`
	Channel  int   `json:"channel"`             // 0 = auto (rf_plan)
	Width    int   `json:"width,omitempty"`     // 0 = inherit
	PowerDBm int   `json:"power_dbm,omitempty"` // 0 = inherit
}

// PlannedRadio is the planner's (or the initial assignment's) choice for
// one band of one AP.
type PlannedRadio struct {
	Channel     int       `json:"channel"`
	Width       int       `json:"width"`
	PowerDBm    int       `json:"power_dbm"`
	DFSFallback int       `json:"dfs_fallback,omitempty"`
	Source      string    `json:"source"` // "initial" | "planner"
	Reason      string    `json:"reason,omitempty"`
	InputsHash  string    `json:"inputs_hash,omitempty"`
	PlannedAt   time.Time `json:"planned_at,omitzero"`
}

// Plan sources.
const (
	PlanSourceInitial = "initial"
	PlanSourcePlanner = "planner"
)

// ManagementOverride is an AP's management-network override (experimental,
// recovery-gated).
type ManagementOverride struct {
	Static  bool     `json:"static,omitempty"`
	Address string   `json:"address,omitempty"`
	Prefix  int      `json:"prefix,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
	// VLANID nil inherits the site; 1 is untagged; 2 to 4094 tagged.
	VLANID *int     `json:"vlan_id,omitempty"`
	NTP    []string `json:"ntp,omitempty"`
}

// cloneJSON deep-copies a value through JSON. Typed sub-records are plain
// data with JSON tags, so the round trip is lossless.
func cloneJSON[T any](value T) T {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return value
	}
	return out
}
