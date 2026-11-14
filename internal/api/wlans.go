package api

// Security is a WLAN's security mode (design §3.9 composite table).
type Security string

const (
	SecurityWPA2Personal       Security = "wpa2_personal"
	SecurityWPA2WPA3Personal   Security = "wpa2_wpa3_personal"
	SecurityWPA3Personal       Security = "wpa3_personal"
	SecurityOpen               Security = "open"
	SecurityOWE                Security = "owe"
	SecurityOWETransition      Security = "owe_transition"
	SecurityWPA2Enterprise     Security = "wpa2_enterprise"
	SecurityWPA2WPA3Enterprise Security = "wpa2_wpa3_enterprise"
	SecurityWPA3Enterprise     Security = "wpa3_enterprise"
)

// RADIUSServerView is one RADIUS server; the secret is write-only.
type RADIUSServerView struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	HasSecret bool   `json:"has_secret"`
}

// CoAServerView is one Change-of-Authorisation server; Port is the AP's
// local CoA port (default 3799).
type CoAServerView struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	HasSecret bool   `json:"has_secret"`
}

// CoAView is Change of Authorisation (experimental, phase 4).
type CoAView struct {
	Enabled                bool            `json:"enabled"`
	Servers                []CoAServerView `json:"servers"`
	EventTimestampOptional bool            `json:"event_timestamp_optional"`
}

// DynamicVLANView is RADIUS-assigned VLANs.
type DynamicVLANView struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type" tstype:"'standard' | 'airespace-interface-name'"`
	VLANs   []int  `json:"vlans"`
}

// RADIUSView is a WLAN's RADIUS settings (Enterprise modes, phase 4).
type RADIUSView struct {
	Auth                []RADIUSServerView `json:"auth"`
	Acct                []RADIUSServerView `json:"acct"`
	MacAuth             bool               `json:"mac_auth"`
	AcctInterimSec      int                `json:"acct_interim_sec"`
	NASID               string             `json:"nas_id"`
	NASIP               string             `json:"nas_ip"`
	DynamicVLAN         *DynamicVLANView   `json:"dynamic_vlan" tstype:"DynamicVLANView | null,required"`
	ReauthSec           int                `json:"reauth_sec"`
	ReauthOnSessionTime bool               `json:"reauth_on_session_timeout"`
	CoA                 *CoAView           `json:"coa" tstype:"CoAView | null,required"`
}

// RoamingView is a WLAN's roaming settings.
type RoamingView struct {
	Fast           string `json:"fast" tstype:"'none' | 'okc' | '11r'"`
	NeighborReport bool   `json:"neighbor_report"`
	BSSTransition  bool   `json:"bss_transition"`
	APSteering     bool   `json:"ap_steering"`
	BSSLoad        bool   `json:"bss_load"`
	APKeyCache     bool   `json:"ap_key_cache"`
}

// BroadcastView is a WLAN's broadcast and multicast filtering.
type BroadcastView struct {
	Limit              bool `json:"limit"`
	AllowMDNS          bool `json:"allow_mdns"`
	AllowSSDP          bool `json:"allow_ssdp"`
	AllowIPv6ND        bool `json:"allow_ipv6_nd"`
	ARPFilter          bool `json:"arp_filter"`
	McastToUcast       bool `json:"mcast_to_ucast"`
	WirelessBridgeDHCP bool `json:"wireless_bridge_dhcp"`
}

// RateLimitView is a WLAN's rate limits in kbps (0 = unlimited).
type RateLimitView struct {
	WLANUp     int `json:"wlan_up"`
	WLANDown   int `json:"wlan_down"`
	ClientUp   int `json:"client_up"`
	ClientDown int `json:"client_down"`
}

// Option82View is DHCP Option 82 (phase 4).
type Option82View struct {
	Enabled   bool   `json:"enabled"`
	CircuitID string `json:"circuit_id"`
}

// ScheduleWindow is one weekday's on-air hours: Day "mon" to "sun",
// Start and End "HH:MM" ("24:00" is the end of the day).
type ScheduleWindow struct {
	Day   string `json:"day" tstype:"'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat' | 'sun'"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// ScheduleView is a WLAN's on-air schedule (experimental, phase 4): the
// WLAN is off outside its windows.
type ScheduleView struct {
	Enabled bool             `json:"enabled"`
	Windows []ScheduleWindow `json:"windows"`
}

// MPSKKeyView is one per-device passphrase; the passphrase is write-only.
type MPSKKeyView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	HasPassphrase bool   `json:"has_passphrase"`
	VLANID        int    `json:"vlan_id"`
	MAC           string `json:"mac"`
	Expires       string `json:"expires"`
}

// MPSKView is Multi-PSK (experimental, phase 4).
type MPSKView struct {
	Enabled bool          `json:"enabled"`
	Private bool          `json:"private"`
	Keys    []MPSKKeyView `json:"keys"`
}

// WLANLiveSummary is what the site's APs report for a WLAN now.
type WLANLiveSummary struct {
	Clients         int `json:"clients"`
	APsBroadcasting int `json:"aps_broadcasting"`
	APsTotal        int `json:"aps_total"`
}

// WLANLiveVAP is one AP band broadcasting the WLAN (field 32, read-only).
type WLANLiveVAP struct {
	APID    string `json:"ap_id"`
	APName  string `json:"ap_name"`
	Band    Band   `json:"band"`
	BSSID   string `json:"bssid"`
	Dev     string `json:"dev"`
	Clients int    `json:"clients"`
}

// WLANView is one WLAN (GET /api/sites/$id/networks, phase 2). Secrets
// (passphrases, RADIUS secrets) are never included.
type WLANView struct {
	ID       string      `json:"id"`
	SiteID   string      `json:"site_id"`
	SSID     string      `json:"ssid"`
	Enabled  bool        `json:"enabled"`
	Security Security    `json:"security"`
	HasPSK   bool        `json:"has_psk"`
	PMF      *string     `json:"pmf" tstype:"'disabled' | 'optional' | 'required' | null,required"`
	RADIUS   *RADIUSView `json:"radius" tstype:"RADIUSView | null,required"`
	Bands    []Band      `json:"bands"`
	// VLANID 1 means untagged.
	VLANID            int             `json:"vlan_id"`
	Hidden            bool            `json:"hidden"`
	Isolation         string          `json:"isolation" tstype:"'none' | 'clients' | 'clients_and_lan'"`
	BSSIDIndex        int             `json:"bssid_index"`
	OWEBSSIDIndex     int             `json:"owe_bssid_index"`
	BandSteering      bool            `json:"band_steering"`
	Rates             string          `json:"rates" tstype:"'compatible' | 'no_legacy' | 'high_density' | 'legacy_only'"`
	MinRSSI           map[string]int  `json:"min_rssi"`
	DTIM              int             `json:"dtim"`
	IdleTimeoutSec    int             `json:"idle_timeout_sec"`
	MaxClients        *int            `json:"max_clients" tstype:"number | null,required"`
	Roaming           RoamingView     `json:"roaming"`
	Broadcast         BroadcastView   `json:"broadcast"`
	DHCPOnly          bool            `json:"dhcp_only"`
	DHCPDNSOnly       bool            `json:"dhcp_dns_only"`
	WMMDisabled       bool            `json:"wmm_disabled"`
	UAPSDDisabled     bool            `json:"uapsd_disabled"`
	Disable11ax       bool            `json:"disable_11ax"`
	Disable11nac      bool            `json:"disable_11nac"`
	GroupRekeySec     int             `json:"group_rekey_sec"`
	EAPOLv1           bool            `json:"eapol_v1"`
	SAEH2EOnly        bool            `json:"sae_h2e_only"`
	TransitionDisable bool            `json:"transition_disable"`
	OWESSID           string          `json:"owe_ssid"`
	RateLimit         *RateLimitView  `json:"rate_limit" tstype:"RateLimitView | null,required"`
	QoSClass          string          `json:"qos_class"`
	HonourBlocklist   bool            `json:"honour_blocklist"`
	Option82          *Option82View   `json:"option82" tstype:"Option82View | null,required"`
	Schedule          *ScheduleView   `json:"schedule" tstype:"ScheduleView | null,required"`
	MPSK              *MPSKView       `json:"mpsk" tstype:"MPSKView | null,required"`
	Expert            ExpertSummary   `json:"expert"`
	ExperimentalInUse []string        `json:"experimental_in_use"`
	Live              WLANLiveSummary `json:"live"`
	// LiveVAPs lists each AP band broadcasting the WLAN now.
	LiveVAPs []WLANLiveVAP `json:"live_vaps"`
	// MDID is the WLAN's 802.11r mobility domain (read-only; the AP uses
	// it only with fast roaming 802.11r).
	MDID       int    `json:"mdid"`
	UpdatedRev uint64 `json:"updated_rev"`
}

// RADIUSServerInput is one RADIUS server in a WLAN change. An absent
// secret keeps the stored one for the same host and port.
type RADIUSServerInput struct {
	Host   string  `json:"host"`
	Port   int     `json:"port"`
	Secret *string `json:"secret,omitempty"`
}

// MPSKKeyInput is one per-device passphrase in a WLAN change. An absent
// passphrase keeps the stored one for the same ID.
type MPSKKeyInput struct {
	ID         string  `json:"id,omitempty"`
	Name       string  `json:"name"`
	Passphrase *string `json:"passphrase,omitempty"`
	VLANID     int     `json:"vlan_id,omitempty"`
	MAC        string  `json:"mac,omitempty"`
	Expires    string  `json:"expires,omitempty"`
}

// WLANInput is POST /api/sites/$id/networks and PATCH …/networks/$nid: a
// JSON merge patch over WLANView's editable fields, plus the write-only
// secrets. The fields not listed here are passed through as the same keys
// of WLANView (for example "roaming", "broadcast", "rate_limit").
type WLANInput struct {
	SSID     *string      `json:"ssid,omitempty"`
	Enabled  *bool        `json:"enabled,omitempty"`
	Security *string      `json:"security,omitempty"`
	PSK      *string      `json:"psk,omitempty"`
	PMF      *string      `json:"pmf,omitempty"`
	Bands    []Band       `json:"bands,omitempty"`
	VLANID   *int         `json:"vlan_id,omitempty"`
	Hidden   *bool        `json:"hidden,omitempty"`
	Preset   string       `json:"preset,omitempty" tstype:"'home' | 'guest' | 'iot'"`
	RADIUS   *RADIUSInput `json:"radius,omitempty"`
	MPSK     *MPSKInput   `json:"mpsk,omitempty"`
}

// RADIUSInput is the radius part of WLANInput.
type RADIUSInput struct {
	Auth []RADIUSServerInput `json:"auth,omitempty"`
	Acct []RADIUSServerInput `json:"acct,omitempty"`
}

// MPSKInput is the mpsk part of WLANInput.
type MPSKInput struct {
	Enabled *bool          `json:"enabled,omitempty"`
	Private *bool          `json:"private,omitempty"`
	Keys    []MPSKKeyInput `json:"keys,omitempty"`
}

// RevealView is POST /api/sites/$id/networks/$nid/reveal (phase 4, admin
// only, audit-logged): the passphrase and the Wi-Fi QR payload
// ("WIFI:T:WPA;S:…;P:…;;").
type RevealView struct {
	SSID   string `json:"ssid"`
	PSK    string `json:"psk"`
	WiFiQR string `json:"wifi_qr"`
}

// WLANDeleted is DELETE /api/sites/$id/networks/$nid.
type WLANDeleted struct {
	ID string `json:"id"`
}
