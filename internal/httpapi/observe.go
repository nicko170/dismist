package httpapi

import (
	"encoding/json"
	"log"
	"time"

	"github.com/nicko170/dismist/internal/controller"
)

// The API reads an AP's controller status and telemetry through their JSON
// form, the frozen contract of workstream B (design §8.2: controller Status
// v2 and telemetry.Update v2), decoded into the mirror types below. A field
// the running controller does not report yet stays zero, so this package
// builds against any controller that keeps the documented JSON keys and
// picks up new sections without a code change here.

// obsHello is controller.HelloExtras.
type obsHello struct {
	SKU              string    `json:"sku"`
	SerialNo         string    `json:"serial"`
	Uptime           int64     `json:"uptime_seconds"`
	ConnectUptime    int64     `json:"connect_uptime_seconds"`
	BootCount        uint64    `json:"boot_count"`
	LastRebootTime   time.Time `json:"last_reboot_time"`
	LastRebootReason string    `json:"last_reboot_reason"`
	LastBootState    string    `json:"last_boot_state"`
	LocalIP          string    `json:"local_ip"`
}

// obsConfigRequest is controller.ConfigRequest.
type obsConfigRequest struct {
	At                     time.Time `json:"at"`
	MfgCountryCode         string    `json:"mfg_country_code"`
	RecoveredFromBadConfig bool      `json:"recovered_from_bad_config"`
	AppliedConfigTimestamp float64   `json:"applied_config_timestamp"`
	Capabilities           []string  `json:"capabilities"`
}

// obsApplied is controller.ConfigApplied.
type obsApplied struct {
	At                     time.Time `json:"at"`
	ConfigTimestamp        float64   `json:"config_timestamp"`
	Error                  string    `json:"error"`
	Good5Min               bool      `json:"good_5min"`
	RecoveredFromBadConfig bool      `json:"recovered_from_bad_config"`
	PowerConstrained       bool      `json:"power_constrained"`
}

// obsStatus is controller.Status (v2).
type obsStatus struct {
	Connected              bool             `json:"connected"`
	ConnectedAt            time.Time        `json:"connected_at"`
	ConnectionCount        uint64           `json:"connection_count"`
	TLSCipherSuite         string           `json:"tls_cipher_suite"`
	LastDisconnect         time.Time        `json:"last_disconnect"`
	Firmware               string           `json:"firmware"`
	Model                  string           `json:"model"`
	LastSeen               time.Time        `json:"last_seen"`
	AppliedRevision        uint64           `json:"applied_revision"`
	LastError              string           `json:"last_error"`
	RemoteAddress          string           `json:"remote_address"`
	Hello                  obsHello         `json:"hello"`
	HelloAt                time.Time        `json:"hello_at"`
	LastDisconnectCause    string           `json:"last_disconnect_cause"`
	ConfigSource           string           `json:"config_source"`
	CompileError           string           `json:"compile_error"`
	DesiredConfigTimestamp float64          `json:"desired_config_timestamp"`
	ConfigRequest          obsConfigRequest `json:"config_request"`
	Applied                obsApplied       `json:"applied"`
	TunnelIPv6             string           `json:"tunnel_ipv6"`
	LastRebootDetected     time.Time        `json:"last_reboot_detected"`
}

type obsUtil struct {
	All             float64 `json:"all"`
	Tx              float64 `json:"tx"`
	RxInBSS         float64 `json:"rx_in_bss"`
	RxOtherBSS      float64 `json:"rx_other_bss"`
	UnknownWifi     float64 `json:"unknown_wifi"`
	NonWifi         float64 `json:"non_wifi"`
	UnDecodableWifi float64 `json:"undecodable_wifi"`
}

func (u obsUtil) parts() map[string]float64 {
	return map[string]float64{
		"tx": u.Tx, "rx_in_bss": u.RxInBSS, "rx_other_bss": u.RxOtherBSS,
		"unknown_wifi": u.UnknownWifi, "non_wifi": u.NonWifi, "undecodable_wifi": u.UnDecodableWifi,
	}
}

type obsRadio struct {
	Dev           string `json:"dev"`
	Interface     string `json:"interface"`
	Band          string `json:"band"`
	Role          string `json:"role"`
	Channel       int    `json:"channel"`
	Secondary     int    `json:"secondary_channel"`
	WidthMHz      int    `json:"width_mhz"`
	PHY           int    `json:"phy"`
	NoiseFloorDBm int    `json:"noise_floor_dbm"`
	TxPowerDBm    int    `json:"tx_power_dbm"`
	MaxTxPowerDBm int    `json:"max_tx_power_dbm"`
	Clients       int    `json:"clients"`
	Missing       bool   `json:"missing"`
	DFS           struct {
		RadarDetected bool `json:"radar_detected"`
		CACState      int  `json:"cac_state"`
	} `json:"dfs"`
	Utilization *obsUtil `json:"utilization"`
	ChanUtil    *struct {
		Mean obsUtil `json:"mean"`
	} `json:"chan_util"`
	Counters map[string]uint64 `json:"counters"`
}

// util returns the radio's mean channel utilisation (0 to 1).
func (r obsRadio) util() (float64, map[string]float64) {
	switch {
	case r.ChanUtil != nil:
		return r.ChanUtil.Mean.All, r.ChanUtil.Mean.parts()
	case r.Utilization != nil:
		return r.Utilization.All, r.Utilization.parts()
	}
	return 0, map[string]float64{}
}

type obsVAP struct {
	Interface  string `json:"interface"`
	SSID       string `json:"ssid"`
	BSSID      string `json:"bssid"`
	WLANID     string `json:"wlan_id"`
	RadioIndex int    `json:"radio_index"`
	Radio      string `json:"radio"`
	Band       string `json:"band"`
	Clients    int    `json:"clients"`
}

type obsQoS struct {
	AC      string `json:"ac"`
	TxBytes uint64 `json:"tx_bytes"`
	RxBytes uint64 `json:"rx_bytes"`
}

type obsClient struct {
	MAC              string   `json:"mac"`
	Name             string   `json:"name"`
	IP               string   `json:"ip"`
	SSID             string   `json:"ssid"`
	BSSID            string   `json:"bssid"`
	VLANID           uint64   `json:"vlan_id"`
	RSSI             int64    `json:"rssi"`
	AverageRSSI      int64    `json:"average_rssi"`
	ConnectedSeconds uint64   `json:"connected_seconds"`
	InactiveMillis   uint64   `json:"inactive_millis"`
	TxMbps           float64  `json:"tx_mbps"`
	RxMbps           float64  `json:"rx_mbps"`
	TxBytes          uint64   `json:"tx_bytes"`
	RxBytes          uint64   `json:"rx_bytes"`
	Auth             string   `json:"auth"`
	Cipher           string   `json:"cipher"`
	Protocol         string   `json:"protocol"`
	RadioIndex       int      `json:"radio_index"`
	WLANID           string   `json:"wlan_id"`
	PerAntennaRSSI   []int64  `json:"per_antenna_rssi"`
	TxRetries        uint64   `json:"tx_retries"`
	RxRetries        uint64   `json:"rx_retries"`
	UserAgent        string   `json:"user_agent"`
	Username         string   `json:"username"`
	Streams          int      `json:"streams"`
	QoS              []obsQoS `json:"qos"`
	IPv6             []string `json:"ipv6"`
	Radio            string   `json:"radio"`
	Band             string   `json:"band"`
	Channel          int      `json:"channel"`
	WidthMHz         int      `json:"width_mhz"`
	SNR              *int     `json:"snr"`
	SelfAssigned     bool     `json:"self_assigned"`
}

type obsInterface struct {
	Name      string   `json:"name"`
	VLAN      int      `json:"vlan"`
	Addresses []string `json:"addresses"`
	DADFailed []string `json:"dad_failed"`
}

type obsPort struct {
	Name       string            `json:"name"`
	Up         bool              `json:"up"`
	FullDuplex bool              `json:"full_duplex"`
	SpeedMbps  int               `json:"speed_mbps"`
	RxBps      float64           `json:"rx_bps"`
	TxBps      float64           `json:"tx_bps"`
	Errors     map[string]uint64 `json:"errors"`
}

type obsLLDP struct {
	APPort      string `json:"ap_port"`
	PortDesc    string `json:"port_desc"`
	SystemName  string `json:"system_name"`
	SystemDesc  string `json:"system_desc"`
	MgmtAddress string `json:"mgmt_address"`
	PortID      string `json:"port_id"`
	NativeVLAN  int    `json:"native_vlan"`
	VLANs       []struct {
		ID int `json:"id"`
	} `json:"vlans"`
	ChassisID string `json:"chassis_id"`
}

type obsLLDPPower struct {
	APPort      string `json:"ap_port"`
	AllocatedMW uint32 `json:"allocated_mw"`
	RequestedMW uint32 `json:"requested_mw"`
	NeededMW    uint32 `json:"needed_mw"`
}

type obsPower struct {
	Source          string `json:"source"`
	AvailableMW     uint32 `json:"available_mw"`
	NeededMW        uint32 `json:"needed_mw"`
	BudgetMW        int32  `json:"budget_mw"`
	ConstrainedMode string `json:"constrained_mode"`
}

func (p *obsPower) constrained() bool {
	if p == nil {
		return false
	}
	switch p.ConstrainedMode {
	case "", "none", "None", "NONE", "off", "unconstrained":
		return false
	}
	return true
}

type obsLease struct {
	Dev          string    `json:"dev"`
	State        string    `json:"state"`
	RenewedAt    time.Time `json:"renewed_at"`
	LeaseSeconds uint64    `json:"lease_seconds"`
	Address      string    `json:"address"`
	Prefix       int       `json:"prefix"`
	Server       string    `json:"server"`
	Gateway      string    `json:"gateway"`
	DNS          []string  `json:"dns"`
	Domain       string    `json:"domain"`
	NTP          []string  `json:"ntp"`
}

type obsIP struct {
	DHCP   []obsLease `json:"dhcp"`
	Routes []struct {
		Destination string `json:"destination"`
		Gateway     string `json:"gateway"`
	} `json:"routes"`
	Neighbors []struct {
		IP  string `json:"ip"`
		MAC string `json:"mac"`
	} `json:"neighbors"`
	DNSServers []string `json:"dns_servers"`
}

type obsScanNeighbour struct {
	SSID     string `json:"ssid"`
	BSSID    string `json:"bssid"`
	RSSI     int    `json:"rssi"`
	Channel  int    `json:"channel"`
	WidthMHz int    `json:"width_mhz"`
	QBSS     *struct {
		Stations    int `json:"stations"`
		Utilization int `json:"utilization"`
	} `json:"qbss"`
}

type obsScan struct {
	Band24 []obsScanNeighbour `json:"band_24"`
	Band5  []obsScanNeighbour `json:"band_5"`
}

type obsHealth struct {
	When       time.Time `json:"when"`
	BootCount  uint64    `json:"boot_count"`
	Country    string    `json:"country"`
	CLMVersion string    `json:"clm_version"`
	Memory     *struct {
		TotalKB     uint64 `json:"total_kb"`
		AvailableKB uint64 `json:"available_kb"`
	} `json:"memory"`
	Load *struct {
		Average [3]float64 `json:"average"`
	} `json:"load"`
	RW *struct {
		TotalKB     uint64 `json:"total_kb"`
		AvailableKB uint64 `json:"available_kb"`
	} `json:"rw"`
	Temperature *struct {
		CPU *float64 `json:"cpu"`
	} `json:"temperature"`
	Agent *struct {
		AllocBytes uint64 `json:"alloc_bytes"`
		RSSBytes   uint64 `json:"rss_bytes"`
	} `json:"agent"`
	Cloud *struct {
		LastRTTSeconds      *float64 `json:"last_rtt_s"`
		WebSocketRTTSeconds *float64 `json:"websocket_rtt_s"`
		TCPRTTSeconds       *float64 `json:"tcp_rtt_s"`
		PMTU                int      `json:"pmtu"`
		SendMSS             int      `json:"send_mss"`
	} `json:"cloud"`
}

// rtt returns the best controller RTT in milliseconds.
func (h obsHealth) rtt() *float64 {
	if h.Cloud == nil {
		return nil
	}
	for _, seconds := range []*float64{h.Cloud.TCPRTTSeconds, h.Cloud.WebSocketRTTSeconds, h.Cloud.LastRTTSeconds} {
		if seconds != nil {
			ms := *seconds * 1000
			return &ms
		}
	}
	return nil
}

type obsActivity struct {
	MAC       string    `json:"mac"`
	IP        string    `json:"ip"`
	LastEvent string    `json:"last_event"`
	LastSeen  time.Time `json:"last_seen"`
}

// obsTelemetry is controller.TelemetrySnapshot (v2).
type obsTelemetry struct {
	FullFrames        uint64         `json:"full_frames"`
	FastFrames        uint64         `json:"fast_frames"`
	LastFull          time.Time      `json:"last_full"`
	LastFast          time.Time      `json:"last_fast"`
	UptimeSeconds     uint64         `json:"uptime_seconds"`
	VAPs              []obsVAP       `json:"vaps"`
	Radios            []obsRadio     `json:"radios"`
	RecentClients     []obsActivity  `json:"recent_clients"`
	AssociatedClients []obsClient    `json:"associated_clients"`
	Interfaces        []obsInterface `json:"interfaces"`
	Model             string         `json:"model"`
	Firmware          string         `json:"firmware"`
	Health            obsHealth      `json:"health"`
	ClockSkewSeconds  *float64       `json:"clock_skew_s"`
	CPUBusy           *float64       `json:"cpu_busy"`
	ConfigTimestamp   float64        `json:"config_timestamp"`
	Ports             []obsPort      `json:"ports"`
	LLDP              []obsLLDP      `json:"lldp"`
	LLDPPower         []obsLLDPPower `json:"lldp_power"`
	Power             *obsPower      `json:"power"`
	IP                obsIP          `json:"ip"`
	InactiveVLANs     []int          `json:"inactive_vlans"`
	Scan              *obsScan       `json:"scan"`
	LastKnownVAPs     []obsVAP       `json:"last_known_vaps"`
	// KernelLog is the AP kernel log ring (24.1, oldest first); the AP
	// sends lines without a time of their own.
	KernelLog []string `json:"kmsg"`
}

// obsCounters is controller.Counters.
type obsCounters struct {
	Connections      uint64            `json:"connections"`
	Disconnects      uint64            `json:"disconnects"`
	AuthFailures     map[string]uint64 `json:"auth_failures"`
	SourceMismatches uint64            `json:"source_mismatches"`
	ConfigRejections uint64            `json:"config_rejections"`
	CompileErrors    uint64            `json:"compile_errors"`
}

// apObs is everything the controller knows about one AP right now.
type apObs struct {
	present   bool // a control session object exists for the AP
	status    obsStatus
	telemetry obsTelemetry
	counters  obsCounters
	features  map[string]bool
	pinnedIP  string
}

// mirror decodes value's JSON into target. A failure leaves target zero
// and is logged: it means the controller broke the JSON contract.
func mirror(value, target any) {
	raw, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(raw, target)
	}
	if err != nil {
		log.Printf("API: controller snapshot does not match the API contract: %v", err)
	}
}

// observeControl reads a control session's status, telemetry and counters.
func observeControl(control *controller.Server) apObs {
	if control == nil {
		return apObs{}
	}
	var obs apObs
	obs.present = true
	mirror(control.Status(), &obs.status)
	mirror(control.TelemetrySnapshot(), &obs.telemetry)
	mirror(control.Counters(), &obs.counters)
	obs.features = control.Features().Map()
	obs.pinnedIP = control.AllowedManagementIP()
	return obs
}

// observe reads one AP from the controller.
func (s *Server) observe(apID string) apObs {
	return observeControl(s.controlFor(apID))
}

// fresh reports whether the AP's telemetry belongs to its current session
// and config (not stale data from before a reconnect or a move).
func (o apObs) fresh(configUpdatedAt time.Time) bool {
	if !o.status.Connected {
		return false
	}
	after := o.status.ConnectedAt
	if configUpdatedAt.After(after) {
		after = configUpdatedAt
	}
	return o.telemetry.LastFull.After(after) || o.telemetry.LastFast.After(after)
}
