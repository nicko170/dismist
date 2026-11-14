package api

import "time"

// Signal is a client's signal quality bucket.
type Signal string

const (
	SignalExcellent Signal = "excellent"
	SignalGood      Signal = "good"
	SignalFair      Signal = "fair"
	SignalPoor      Signal = "poor"
)

// ClientView is one wireless client (design §3.12).
type ClientView struct {
	MAC          string   `json:"mac"`
	PrivateMAC   bool     `json:"private_mac"`
	Name         string   `json:"name"`
	Hostname     string   `json:"hostname"`
	Alias        *string  `json:"alias" tstype:"string | null,required"`
	IP           string   `json:"ip"`
	SelfAssigned bool     `json:"self_assigned"`
	IPv6         []string `json:"ipv6"`
	APID         string   `json:"ap_id"`
	APName       string   `json:"ap_name"`
	WLANID       string   `json:"wlan_id"`
	SSID         string   `json:"ssid"`
	Band         string   `json:"band"`
	Channel      int      `json:"channel"`
	Width        int      `json:"width"`
	BSSID        string   `json:"bssid"`
	Protocol     string   `json:"protocol"`
	Streams      *int     `json:"streams" tstype:"number | null,required"`
	RSSI         int      `json:"rssi"`
	AvgRSSI      int      `json:"avg_rssi"`
	SNR          *int     `json:"snr" tstype:"number | null,required"`
	Signal       Signal   `json:"signal"`
	// TxRateMbps and RxRateMbps have unverified units (design §9 Q7).
	TxRateMbps       *float64 `json:"tx_rate_mbps" tstype:"number | null,required"`
	RxRateMbps       *float64 `json:"rx_rate_mbps" tstype:"number | null,required"`
	DownBytes        uint64   `json:"down_bytes"`
	UpBytes          uint64   `json:"up_bytes"`
	ConnectedSeconds uint64   `json:"connected_seconds"`
	IdleMillis       uint64   `json:"idle_ms"`
}

// SessionView is one client session (a connection to one AP).
type SessionView struct {
	SiteID         string     `json:"site_id"`
	APID           string     `json:"ap_id"`
	APName         string     `json:"ap_name"`
	MAC            string     `json:"mac"`
	SSID           string     `json:"ssid"`
	IP             string     `json:"ip"`
	VLANID         uint64     `json:"vlan_id"`
	ConnectedAt    time.Time  `json:"connected_at"`
	LastSeen       time.Time  `json:"last_seen"`
	DisconnectedAt *time.Time `json:"disconnected_at" tstype:"string | null,required"`
	EndReason      string     `json:"end_reason"`
	RSSI           int        `json:"rssi"`
	DownBytes      uint64     `json:"down_bytes"`
	UpBytes        uint64     `json:"up_bytes"`
}

// ClientRetries are per-direction retry counters.
type ClientRetries struct {
	Tx uint64 `json:"tx"`
	Rx uint64 `json:"rx"`
}

// ClientQoS is per access category traffic.
type ClientQoS struct {
	AC      string `json:"ac"`
	TxBytes uint64 `json:"tx_bytes"`
	RxBytes uint64 `json:"rx_bytes"`
}

// ClientDetail is GET /api/clients/$mac (phase 1). SiteID says where the
// client was last seen (used by the /clients/$mac stable link).
type ClientDetail struct {
	SiteID         string        `json:"site_id"`
	Live           *ClientView   `json:"live" tstype:"ClientView | null,required"`
	PerAntennaRSSI []int         `json:"per_antenna_rssi"`
	Username       string        `json:"username"`
	UserAgent      string        `json:"user_agent"`
	Retries        ClientRetries `json:"retries"`
	QoS            []ClientQoS   `json:"qos"`
	Sessions       []SessionView `json:"sessions"`
	Events         []EventView   `json:"events"`
	NextBefore     *time.Time    `json:"next_before" tstype:"string | null,required"`
}
