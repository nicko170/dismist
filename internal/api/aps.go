package api

import "time"

// Band is a radio band.
type Band string

const (
	Band24 Band = "2.4"
	Band5  Band = "5"
)

// APStatus is an AP's headline status: the first that applies, in this
// order (design §3.4).
type APStatus string

const (
	APStatusAuthFailing    APStatus = "auth_failing"
	APStatusSourceMismatch APStatus = "source_mismatch"
	APStatusConfigError    APStatus = "config_error"
	APStatusNeverConnected APStatus = "never_connected"
	APStatusOffline        APStatus = "offline"
	APStatusRejected       APStatus = "rejected"
	APStatusPending        APStatus = "pending"
	APStatusApplying       APStatus = "applying"
	APStatusInSync         APStatus = "in_sync"
)

// TestedFirmware is the firmware build the typed options were built
// against; APView.FirmwareTested compares with it.
const TestedFirmware = "apfw-0.15.35064-noro-84df"

// RadioSummary is one access radio in list views and the SSE.
type RadioSummary struct {
	Band    Band    `json:"band"`
	Channel int     `json:"channel"`
	Width   int     `json:"width"`
	Clients int     `json:"clients"`
	Util    float64 `json:"util"`
}

// APView is one AP in lists (GET /api/aps, phase 1).
type APView struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SiteID string `json:"site_id"`
	Model  string `json:"model"`
	// ModelSource is "ap" when the hello reported the model.
	ModelSource    string   `json:"model_source" tstype:"'ap' | 'record'"`
	Firmware       string   `json:"firmware"`
	FirmwareTested bool     `json:"firmware_tested"`
	Status         APStatus `json:"status"`
	// Flags lists every other status that also applies, plus
	// "verify_mismatch", "rf_plan_missing" and "local_files".
	Flags       []string   `json:"flags"`
	Connected   bool       `json:"connected"`
	ConnectedAt *time.Time `json:"connected_at" tstype:"string | null,required"`
	LastSeen    *time.Time `json:"last_seen" tstype:"string | null,required"`
	// LANIP is the management SVI address, else the hello's LocalIP.
	LANIP string `json:"lan_ip"`
	// RemoteAddress is the current or last session's source (a NAT's
	// public address for APs behind one).
	RemoteAddress string `json:"remote_address"`
	BehindNAT     bool   `json:"behind_nat"`
	// PinnedIP is the optional source pin; "" means any address.
	PinnedIP        string         `json:"pinned_ip"`
	HasSecret       bool           `json:"has_secret"`
	ConfigRevision  uint64         `json:"config_revision"`
	AppliedRevision uint64         `json:"applied_revision"`
	LastError       string         `json:"last_error"`
	ClientCount     int            `json:"client_count"`
	UptimeSeconds   uint64         `json:"uptime_seconds"`
	Radios          []RadioSummary `json:"radios"`
	UpdatedRev      uint64         `json:"updated_rev"`
}

// APIdentity is what the AP reports about itself in its hello.
type APIdentity struct {
	Serial           string     `json:"serial"`
	SKU              string     `json:"sku"`
	BootCount        uint64     `json:"boot_count"`
	LastRebootReason string     `json:"last_reboot_reason"`
	LastRebootTime   *time.Time `json:"last_reboot_time" tstype:"string | null,required"`
	LastBootState    string     `json:"last_boot_state"`
	TunnelIPv6       string     `json:"tunnel_ipv6"`
}

// PushMode is how a config change reaches a connected AP (design §5.6).
type PushMode string

const (
	PushModeNotify    PushMode = "notify"
	PushModeReconnect PushMode = "reconnect"
)

// APConnection describes the AP's controller session.
type APConnection struct {
	ConnectionCount uint64     `json:"connection_count"`
	LastDisconnect  *time.Time `json:"last_disconnect" tstype:"string | null,required"`
	TLSCipher       string     `json:"tls_cipher"`
	RTTMillis       *float64   `json:"rtt_ms" tstype:"number | null,required"`
	PMTU            *int       `json:"pmtu" tstype:"number | null,required"`
	PushMode        PushMode   `json:"push_mode"`
	// ClockSkewSeconds is the AP clock (field 5 When) against the
	// controller's receive time.
	ClockSkewSeconds *float64 `json:"clock_skew_s" tstype:"number | null,required"`
}

// Verification is a post-delivery verification result (design §5.9).
type Verification struct {
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	Mismatches []string  `json:"mismatches"`
}

// APConfigState is the config part of APDetail.
type APConfigState struct {
	EffectiveHash          string        `json:"effective_hash"`
	AppliedTimestamp       float64       `json:"applied_timestamp"`
	DesiredTimestamp       float64       `json:"desired_timestamp"`
	RecoveredFromBadConfig bool          `json:"recovered_from_bad_config"`
	Good5Min               bool          `json:"good_5min"`
	Shadowed               []string      `json:"shadowed"`
	CompileError           string        `json:"compile_error"`
	LastVerification       *Verification `json:"last_verification" tstype:"Verification | null,required"`
	// CountryReported is field 75 CountryCode.
	CountryReported string `json:"country_reported"`
}

// LocalFileView is one /rw/etc/mist/ep-control/local_* file found on an AP.
type LocalFileView struct {
	Name   string    `json:"name"`
	SHA256 string    `json:"sha256"`
	SeenAt time.Time `json:"seen_at"`
}

// APHealth is the AP's system health (design §3.5 Health card). Load is
// the 1, 5 and 15 minute load average.
//
//tygo:emit export type Load3 = [number, number, number];
type APHealth struct {
	CPUTempC          *float64   `json:"cpu_temp_c" tstype:"number | null,required"`
	CPUBusy           *float64   `json:"cpu_busy" tstype:"number | null,required"`
	MemTotalBytes     uint64     `json:"mem_total_bytes"`
	MemAvailableBytes uint64     `json:"mem_available_bytes"`
	Load              [3]float64 `json:"load" tstype:"Load3"`
	RWTotalBytes      uint64     `json:"rw_total_bytes"`
	RWAvailableBytes  uint64     `json:"rw_available_bytes"`
	AgentHeapBytes    *uint64    `json:"agent_heap_bytes" tstype:"number | null,required"`
}

// PortView is one wired port (eth0, eth1).
type PortView struct {
	Name   string `json:"name"`
	Up     bool   `json:"up"`
	Speed  int    `json:"speed_mbps"`
	Duplex string `json:"duplex"`
	// RxBps and TxBps are bits per second over the last full frame period.
	RxBps  float64           `json:"rx_bps"`
	TxBps  float64           `json:"tx_bps"`
	Errors map[string]uint64 `json:"errors"`
}

// LLDPView is one LLDP neighbour.
type LLDPView struct {
	Port        string `json:"port"`
	SystemName  string `json:"system_name"`
	SystemDesc  string `json:"system_desc"`
	PortID      string `json:"port_id"`
	PortDesc    string `json:"port_desc"`
	ChassisID   string `json:"chassis_id"`
	MgmtAddress string `json:"mgmt_address"`
	NativeVLAN  int    `json:"native_vlan"`
	VLANs       []int  `json:"vlans"`
}

// PowerView is the AP's PoE power state.
type PowerView struct {
	Source      string   `json:"source"`
	AllocatedW  *float64 `json:"allocated_w" tstype:"number | null,required"`
	RequestedW  *float64 `json:"requested_w" tstype:"number | null,required"`
	NeededW     *float64 `json:"needed_w" tstype:"number | null,required"`
	BudgetW     *float64 `json:"budget_w" tstype:"number | null,required"`
	Constrained bool     `json:"constrained"`
}

// APUplink is the Uplink tab.
type APUplink struct {
	Ports         []PortView `json:"ports"`
	LLDP          []LLDPView `json:"lldp"`
	Power         *PowerView `json:"power" tstype:"PowerView | null,required"`
	InactiveVLANs []int      `json:"inactive_vlans"`
}

// APIP is the AP's management IP lease.
type APIP struct {
	VLAN       string     `json:"vlan"`
	DHCPState  string     `json:"dhcp_state"`
	Address    string     `json:"address"`
	Prefix     int        `json:"prefix"`
	RenewedAt  *time.Time `json:"renewed_at" tstype:"string | null,required"`
	LeaseSecs  uint64     `json:"lease_seconds"`
	Gateway    string     `json:"gateway"`
	GatewayMAC string     `json:"gateway_mac"`
	DNS        []string   `json:"dns"`
	DHCPServer string     `json:"dhcp_server"`
	Domain     string     `json:"domain"`
	NTP        []string   `json:"ntp"`
	DADFailed  []string   `json:"dad_failed"`
}

// RadioRole is "access" for a radio that carries WLANs, "scan" for the
// scanning radio (design §3.5 Radios tab).
type RadioRole string

const (
	RadioRoleAccess RadioRole = "access"
	RadioRoleScan   RadioRole = "scan"
)

// RadioView is one radio (r0, r1, r2) on the Radios tab.
type RadioView struct {
	Dev  string    `json:"dev"`
	Role RadioRole `json:"role"`
	// Band is null for the scanning radio.
	Band        *Band    `json:"band" tstype:"Band | null,required"`
	Channel     int      `json:"channel"`
	Secondary   int      `json:"secondary"`
	Width       int      `json:"width"`
	PHY         int      `json:"phy"`
	TxPowerDBm  *float64 `json:"tx_power_dbm" tstype:"number | null,required"`
	MaxPowerDBm *float64 `json:"max_power_dbm" tstype:"number | null,required"`
	NoiseDBm    *float64 `json:"noise_dbm" tstype:"number | null,required"`
	Util        float64  `json:"util"`
	// UtilParts splits utilisation: tx, rx_bss, rx_other, non_wifi,
	// unknown_wifi, undecodable, idle.
	UtilParts map[string]float64 `json:"util_parts"`
	Clients   int                `json:"clients"`
	DFS       *DFSState          `json:"dfs" tstype:"DFSState | null,required"`
	Counters  map[string]uint64  `json:"counters"`
}

// DFSState is a radio's CAC state.
type DFSState struct {
	CAC           string `json:"cac"`
	RadarDetected bool   `json:"radar_detected"`
}

// WLANLive is one VAP the AP broadcasts (field 32).
type WLANLive struct {
	WLANID  string `json:"wlan_id"`
	SSID    string `json:"ssid"`
	Band    string `json:"band"`
	BSSID   string `json:"bssid"`
	Dev     string `json:"dev"`
	Clients int    `json:"clients"`
}

// RadioOverride is an AP's per-band radio override (phase 2).
type RadioOverride struct {
	Disabled *bool `json:"disabled" tstype:"boolean | null,required"`
	Channel  *int  `json:"channel" tstype:"number | null,required"`
	Width    *int  `json:"width" tstype:"number | null,required"`
	PowerDBm *int  `json:"power_dbm" tstype:"number | null,required"`
}

// PlannedRadio is the channel plan's assignment for one radio (phase 2).
type PlannedRadio struct {
	Mode        string `json:"mode" tstype:"'auto' | 'static'"`
	Channel     int    `json:"channel"`
	Width       int    `json:"width"`
	PowerDBm    int    `json:"power_dbm"`
	DFSFallback *int   `json:"dfs_fallback" tstype:"number | null,required"`
}

// APOverrides are the AP's typed overrides of site settings (phase 2).
type APOverrides struct {
	LEDBrightness *int                    `json:"led_brightness" tstype:"number | null,required"`
	SSHEnabled    *bool                   `json:"ssh_enabled" tstype:"boolean | null,required"`
	RF            map[Band]*RadioOverride `json:"rf" tstype:"{ [band in Band]: RadioOverride | null }"`
	Management    any                     `json:"management" tstype:"unknown"`
	Eth1          any                     `json:"eth1" tstype:"unknown"`
}

// APCapabilities are what dismist has learned the AP's firmware supports.
type APCapabilities struct {
	// ConfigChangeNotify is null until learned.
	ConfigChangeNotify *bool `json:"config_change_notify" tstype:"boolean | null,required"`
	APCmd              bool  `json:"ap_cmd"`
	Pong2              bool  `json:"pong2"`
}

// APDetail is GET /api/aps/$id (phase 1).
type APDetail struct {
	APView           `tstype:",extends"`
	Identity         APIdentity             `json:"identity"`
	Connection       APConnection           `json:"connection"`
	Config           APConfigState          `json:"config"`
	ProtocolFeatures map[string]bool        `json:"protocol_features"`
	LocalFiles       []LocalFileView        `json:"local_files" tstype:"LocalFileView[] | null,required"`
	Health           APHealth               `json:"health"`
	Uplink           APUplink               `json:"uplink"`
	IP               APIP                   `json:"ip"`
	RadiosDetail     []RadioView            `json:"radios_detail"`
	WLANsLive        []WLANLive             `json:"wlans_live"`
	Overrides        APOverrides            `json:"overrides"`
	RFPlan           map[Band]*PlannedRadio `json:"rf_plan" tstype:"{ [band in Band]: PlannedRadio | null }"`
	LocateUntil      *time.Time             `json:"locate_until" tstype:"string | null,required"`
	Capabilities     APCapabilities         `json:"capabilities"`
}

// PendingReason is why an AP is in the pending-adoption list.
type PendingReason string

const (
	PendingReasonUnregistered   PendingReason = "unregistered"
	PendingReasonSourceMismatch PendingReason = "source_mismatch"
	PendingReasonAuthFailed     PendingReason = "auth_failed"
)

// PendingAP is an AP that said hello but is not served (GET
// /api/aps/pending, phase 1).
type PendingAP struct {
	ID            string        `json:"id"`
	Model         string        `json:"model"`
	SKU           string        `json:"sku"`
	Serial        string        `json:"serial"`
	Firmware      string        `json:"firmware"`
	LocalIP       string        `json:"local_ip"`
	RemoteAddress string        `json:"remote_address"`
	FirstSeen     time.Time     `json:"first_seen"`
	LastSeen      time.Time     `json:"last_seen"`
	Attempts      int           `json:"attempts"`
	Reason        PendingReason `json:"reason"`
}

// APInput is POST /api/aps. Secret is the AP's 32-byte EEPROM secret as hex
// or base64. ManagementIP is the optional source pin.
type APInput struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	SiteID       string          `json:"site_id"`
	Secret       string          `json:"secret"`
	ManagementIP string          `json:"management_ip,omitempty"`
	LocalFiles   []LocalFileView `json:"local_files,omitempty"`
}

// ConfigView selects GET /api/aps/$id/config?view=.
type ConfigView string

const (
	// ConfigViewEffective is the compiled config dismist serves, redacted.
	ConfigViewEffective ConfigView = "effective"
	// ConfigViewNormalised is the canonical effective form (design §4.5),
	// redacted; admin only.
	ConfigViewNormalised ConfigView = "normalised"
	// ConfigViewApplied is the config last delivered to the AP, redacted.
	ConfigViewApplied ConfigView = "applied"
)

// NormaliserReport is what effective.Normalise reported.
type NormaliserReport struct {
	Version    int      `json:"version"`
	Ignored    []string `json:"ignored"`
	TypeErrors []string `json:"type_errors"`
	// LocalFiles is "unknown", "none" or "present".
	LocalFiles string   `json:"local_files"`
	ZeroRule   bool     `json:"zero_rule"`
	Notes      []string `json:"notes"`
}

// APConfigView is GET /api/aps/$id/config. Config is the redacted config
// document for the view. EffectiveHash is "e<version>:<hex HMAC>" of the
// AP's compiled config, keyed with the database's hash key; hashes are
// compared only when their versions match.
type APConfigView struct {
	APID              string            `json:"ap_id"`
	View              ConfigView        `json:"view"`
	Revision          uint64            `json:"revision"`
	EffectiveHash     string            `json:"effective_hash"`
	NormaliserVersion int               `json:"normaliser_version"`
	Config            any               `json:"config" tstype:"{ [key: string]: unknown }"`
	Report            *NormaliserReport `json:"report" tstype:"NormaliserReport | null,required"`
	SitePatch         any               `json:"site_patch" tstype:"{ [key: string]: unknown }"`
	APPatch           any               `json:"ap_patch" tstype:"{ [key: string]: unknown }"`
}
