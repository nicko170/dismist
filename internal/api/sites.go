package api

// SiteCounts are the per-site counters shown on lists and the overview.
type SiteCounts struct {
	APs       int `json:"aps"`
	APsOnline int `json:"aps_online"`
	Clients   int `json:"clients"`
	WLANs     int `json:"wlans"`
	PendingAP int `json:"pending_aps"`
}

// SiteSummary is one row of GET /api/sites (phase 1).
type SiteSummary struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Country  string     `json:"country"`
	Timezone string     `json:"timezone"`
	Counts   SiteCounts `json:"counts"`
}

// SiteView is GET /api/sites/$id (phase 1 fields). The typed settings
// (ssh, syslog, management, ap_defaults, protection, rf, expert) are added
// in phase 2, additively.
type SiteView struct {
	SiteSummary `tstype:",extends"`
	// CCRev is the regulatory revision for Country, null when the country
	// is not in the table.
	CCRev        *string `json:"ccrev" tstype:"string | null,required"`
	Experimental bool    `json:"experimental"`
	UpdatedRev   uint64  `json:"updated_rev"`
	// SiteSettings are the typed settings (phase 2).
	SiteSettings `tstype:",extends"`
}

// SiteInput is POST /api/sites.
type SiteInput struct {
	Name     string `json:"name"`
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
}

// SiteList is GET /api/sites: every site with its counters (phase 1).
// The response body is the bare array.
type SiteList = []SiteSummary

// SSHKeyView is one SSH public key (design §3.11 Access). Key is the
// "type base64" part; Comment is kept from the pasted line.
type SSHKeyView struct {
	Key         string `json:"key"`
	Comment     string `json:"comment"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

// SiteSSHView is the site's SSH access (ssh.*).
type SiteSSHView struct {
	Enabled      bool         `json:"enabled"`
	Keys         []SSHKeyView `json:"keys"`
	ReadonlyKeys []SSHKeyView `json:"readonly_keys"`
}

// SyslogTarget is one remote syslog destination (syslog[]).
type SyslogTarget struct {
	Protocol string `json:"protocol" tstype:"'udp' | 'tcp'"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Facility int    `json:"facility"`
	Severity int    `json:"severity"`
	Tag      string `json:"tag"`
}

// ManagementView are the site's management defaults (management.*).
type ManagementView struct {
	// VLANID 1 means untagged (the native VLAN).
	VLANID int      `json:"vlan_id"`
	DNS    []string `json:"dns"`
	NTP    []string `json:"ntp"`
}

// Eth1View is an eth1 port mode (experimental).
type Eth1View struct {
	Mode string `json:"mode" tstype:"'same_as_uplink' | 'access' | 'trunk' | 'disabled'"`
	// VLANID is the access VLAN, or a trunk's native VLAN (PVID).
	VLANID int `json:"vlan_id,omitempty"`
	// VLANs are a trunk's VLANs.
	VLANs []int `json:"vlans,omitempty"`
}

// RecoveryView is config auto-recovery (experimental).
type RecoveryView struct {
	Enabled bool `json:"enabled"`
	Minutes int  `json:"minutes"`
}

// APDefaultsView are the site's AP defaults (ap_defaults.*).
type APDefaultsView struct {
	LEDBrightness         int           `json:"led_brightness"`
	HostnameFromName      bool          `json:"hostname_from_name"`
	KeepWLANsUpOnUplinkDn bool          `json:"keep_wlans_up_on_uplink_down"`
	PoEConstrained        string        `json:"poe_constrained" tstype:"'keep_radios' | 'disable_radios'"`
	WiredClientStats      bool          `json:"wired_client_stats"`
	Eth1                  *Eth1View     `json:"eth1" tstype:"Eth1View | null,required"`
	ConfigRecovery        *RecoveryView `json:"config_recovery" tstype:"RecoveryView | null,required"`
	BLEDisabled           bool          `json:"ble_disabled"`
}

// BlockedClient is one entry of protection.blocked_clients.
type BlockedClient struct {
	MAC  string `json:"mac"`
	Note string `json:"note"`
}

// PenaltyBoxView is the repeated auth-failure penalty box (experimental).
type PenaltyBoxView struct {
	Threshold int `json:"threshold"`
	WindowSec int `json:"window_sec"`
}

// ProtectionView is protection.*.
type ProtectionView struct {
	BlockedClients     []BlockedClient `json:"blocked_clients"`
	ProxyARP           bool            `json:"proxy_arp"`
	ARPSpoofCheck      bool            `json:"arp_spoof_check"`
	PenaltyBox         *PenaltyBoxView `json:"penalty_box" tstype:"PenaltyBoxView | null,required"`
	DHCPFingerprinting bool            `json:"dhcp_fingerprinting"`
}

// ExpertSummary says whether an object has an expert patch and which typed
// options it shadows (design §3.0).
type ExpertSummary struct {
	HasPatch bool `json:"has_patch"`
	// Field is "expert" once schema 2 has committed, "advanced" (the legacy
	// field) in legacy mode.
	Field          string   `json:"field" tstype:"'expert' | 'advanced'"`
	Shadowed       []string `json:"shadowed"`
	PartlyShadowed []string `json:"partly_shadowed"`
	// OverriddenOn lists, per site option, the APs whose expert patch or
	// typed override replaces it (cross-scope shadowing).
	OverriddenOn map[string][]string `json:"overridden_on"`
}

// SiteSettings are the typed site settings of SiteView (phase 2).
type SiteSettings struct {
	SSH        SiteSSHView    `json:"ssh"`
	Syslog     []SyslogTarget `json:"syslog"`
	Management ManagementView `json:"management"`
	APDefaults APDefaultsView `json:"ap_defaults"`
	Protection ProtectionView `json:"protection"`
	RF         RFProfileView  `json:"rf"`
	Expert     ExpertSummary  `json:"expert"`
	// ExperimentalInUse lists the option keys whose stored value is
	// experimental in this site.
	ExperimentalInUse []string `json:"experimental_in_use"`
	// Typed is false while the schema-2 migration has not committed: the
	// typed values above are then defaults, every typed form is read-only
	// and dismist runs the legacy config unchanged (design §3.0).
	Typed bool `json:"typed"`
}

// SitePatch is PATCH /api/sites/$id: a JSON merge patch over SiteView.
// Absent means unchanged; null resets a field to its default. ssh.keys and
// ssh.readonly_keys take OpenSSH authorized_keys lines (comments kept).
type SitePatch struct {
	Name         *string         `json:"name,omitempty"`
	Country      *string         `json:"country,omitempty"`
	Timezone     *string         `json:"timezone,omitempty"`
	Experimental *bool           `json:"experimental,omitempty"`
	SSH          *SiteSSHInput   `json:"ssh,omitempty"`
	Syslog       []SyslogTarget  `json:"syslog,omitempty"`
	Management   *ManagementView `json:"management,omitempty"`
	APDefaults   *APDefaultsView `json:"ap_defaults,omitempty" tstype:"Partial<APDefaultsView>"`
	Protection   *ProtectionView `json:"protection,omitempty" tstype:"Partial<ProtectionView>"`
	RF           *RFProfileInput `json:"rf,omitempty"`
}

// SiteSSHInput is the ssh part of SitePatch.
type SiteSSHInput struct {
	Enabled      *bool    `json:"enabled,omitempty"`
	Keys         []string `json:"keys,omitempty"`
	ReadonlyKeys []string `json:"readonly_keys,omitempty"`
}

// SiteDeleted is DELETE /api/sites/$id.
type SiteDeleted struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}
