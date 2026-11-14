package api

import "time"

// SampleRadio is one radio in a sample.
type SampleRadio struct {
	Band        string  `json:"band"`
	Channel     int     `json:"channel"`
	Width       int     `json:"width"`
	Util        float64 `json:"util"`
	NoiseDBm    int     `json:"noise_dbm"`
	Clients     int     `json:"clients"`
	MaxPowerDBm int     `json:"max_power_dbm"`
}

// Sample is one point of GET /api/sites/$id/history/samples: the site sum
// (or one AP with ?ap=) over one step.
type Sample struct {
	At           time.Time     `json:"at"`
	Clients      int           `json:"clients"`
	APsConnected int           `json:"aps_connected"`
	TxBytes      uint64        `json:"tx_bytes"`
	RxBytes      uint64        `json:"rx_bytes"`
	AverageRSSI  *float64      `json:"average_rssi" tstype:"number | null,required"`
	Radios       []SampleRadio `json:"radios"`
}

// SamplePage is GET /api/sites/$id/history/samples?ap=&range=&step=.
type SamplePage struct {
	SiteID  string   `json:"site_id"`
	APID    string   `json:"ap_id"`
	Range   string   `json:"range"`
	Step    string   `json:"step"`
	Samples []Sample `json:"samples"`
}

// SearchHit is one result of GET /api/search?q= (phase 4).
type SearchHit struct {
	Kind   string `json:"kind" tstype:"'site' | 'ap' | 'wlan' | 'client'"`
	ID     string `json:"id"`
	SiteID string `json:"site_id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// SearchResult is GET /api/search?q=.
type SearchResult struct {
	Query string      `json:"query"`
	Hits  []SearchHit `json:"hits"`
}
