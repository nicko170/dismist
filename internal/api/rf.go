package api

import "time"

// PowerPolicy is a band's transmit power policy: fixed dBm, or an Auto
// range the planner chooses from (design §3.10).
type PowerPolicy struct {
	Mode string `json:"mode" tstype:"'fixed' | 'auto'"`
	// DBm is the fixed power (mode "fixed").
	DBm int `json:"dbm,omitempty"`
	// Min and Max bound Auto power (mode "auto").
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
}

// BandAdvanced are a band's advanced RF settings.
type BandAdvanced struct {
	TAF          string `json:"taf" tstype:"'default' | 'off' | 'on'"`
	TWT          bool   `json:"twt"`
	Antenna      string `json:"antenna" tstype:"'default' | '4x4' | '3x3' | '2x2' | '1x1'"`
	MinProbeRSSI *int   `json:"min_probe_rssi" tstype:"number | null,required"`
	MuMIMO       *bool  `json:"mu_mimo" tstype:"boolean | null,required"`
	TxBF         *bool  `json:"txbf" tstype:"boolean | null,required"`
}

// ChannelChoice is one valid primary channel.
type ChannelChoice struct {
	Channel int  `json:"channel"`
	DFS     bool `json:"dfs"`
}

// ValidChannels are the primaries valid for one width in the site's
// country (the regulatory table, design Appendix A). Widths that are
// expert-only or that AP33 does not offer under the ccrev (160 MHz under
// AU/890) are not listed.
type ValidChannels struct {
	Width     int             `json:"width"`
	Primaries []ChannelChoice `json:"primaries"`
	Blocks    [][]int         `json:"blocks"`
}

// BandProfileView is one band of the site RF profile.
type BandProfileView struct {
	Enabled       bool            `json:"enabled"`
	Width         int             `json:"width"`
	Channels      []int           `json:"channels"`
	DFS           bool            `json:"dfs"`
	DFSFallback   int             `json:"dfs_fallback"`
	Power         PowerPolicy     `json:"power"`
	Preamble      string          `json:"preamble" tstype:"'auto' | 'short' | 'long'"`
	Advanced      BandAdvanced    `json:"advanced"`
	ValidChannels []ValidChannels `json:"valid_channels"`
}

// PlanActual is what a radio reports (fields 31.3, 31.5, 31.8).
type PlanActual struct {
	Channel     int     `json:"channel"`
	Width       int     `json:"width"`
	MaxPowerDBm int     `json:"max_power_dbm"`
	Util        float64 `json:"util"`
	Clients     int     `json:"clients"`
}

// PlanStatus compares a radio's plan with its telemetry.
type PlanStatus string

const (
	PlanStatusMatch        PlanStatus = "match"
	PlanStatusMismatch     PlanStatus = "mismatch"
	PlanStatusCAC          PlanStatus = "cac"
	PlanStatusRadioMissing PlanStatus = "radio_missing"
	PlanStatusOffline      PlanStatus = "offline"
)

// PlanRow is one AP and band of the channel plan table.
type PlanRow struct {
	APID        string      `json:"ap_id"`
	APName      string      `json:"ap_name"`
	Band        Band        `json:"band"`
	Mode        string      `json:"mode" tstype:"'auto' | 'static'"`
	Channel     int         `json:"channel"`
	Width       int         `json:"width"`
	PowerDBm    int         `json:"power_dbm"`
	DFSFallback *int        `json:"dfs_fallback" tstype:"number | null,required"`
	Source      string      `json:"source"`
	Actual      *PlanActual `json:"actual" tstype:"PlanActual | null,required"`
	Status      PlanStatus  `json:"status"`
}

// RFProfileView is GET /api/sites/$id/rf and SiteView.rf.
type RFProfileView struct {
	Managed bool            `json:"managed"`
	Country string          `json:"country"`
	CCRev   *string         `json:"ccrev" tstype:"string | null,required"`
	Band24  BandProfileView `json:"band_24"`
	Band5   BandProfileView `json:"band_5"`
	// ScanningRadio is null when the template decides (on).
	ScanningRadio   *bool            `json:"scanning_radio" tstype:"boolean | null,required"`
	Plan            []PlanRow        `json:"plan"`
	Recommendations []Recommendation `json:"recommendations"`
}

// BandProfileInput is one band of an RF profile change (merge patch).
type BandProfileInput struct {
	Enabled     *bool         `json:"enabled,omitempty"`
	Width       *int          `json:"width,omitempty"`
	Channels    []int         `json:"channels,omitempty"`
	DFS         *bool         `json:"dfs,omitempty"`
	DFSFallback *int          `json:"dfs_fallback,omitempty"`
	Power       *PowerPolicy  `json:"power,omitempty"`
	Preamble    *string       `json:"preamble,omitempty"`
	Advanced    *BandAdvanced `json:"advanced,omitempty" tstype:"Partial<BandAdvanced>"`
}

// RFProfileInput is PUT /api/sites/$id/rf and SitePatch.rf (merge patch).
type RFProfileInput struct {
	Managed       *bool             `json:"managed,omitempty"`
	Band24        *BandProfileInput `json:"band_24,omitempty"`
	Band5         *BandProfileInput `json:"band_5,omitempty"`
	ScanningRadio *bool             `json:"scanning_radio,omitempty"`
}

// PlanChannel is a channel and power before or after planning.
type PlanChannel struct {
	Channel     int  `json:"channel"`
	Width       int  `json:"width"`
	PowerDBm    int  `json:"power_dbm"`
	DFSFallback *int `json:"dfs_fallback" tstype:"number | null,required"`
}

// PlanChange is one AP and band of a plan proposal.
type PlanChange struct {
	APID       string      `json:"ap_id"`
	APName     string      `json:"ap_name"`
	Band       Band        `json:"band"`
	From       PlanChannel `json:"from"`
	To         PlanChannel `json:"to"`
	CostBefore float64     `json:"cost_before"`
	CostAfter  float64     `json:"cost_after"`
	// Keep is true when the proposal is within the hysteresis (less than
	// 25 % better) and the radio stays where it is.
	Keep       bool       `json:"keep"`
	Reason     string     `json:"reason"`
	Disruption Disruption `json:"disruption"`
}

// PlanInputs summarises what the planner used.
type PlanInputs struct {
	APs int `json:"aps"`
	// ScansAgeSeconds is, per AP ID, the age of its last neighbour scan
	// (null when it has none).
	ScansAgeSeconds map[string]*float64 `json:"scans_age_s" tstype:"{ [ap: string]: number | null }"`
}

// PlanProposal is POST /api/sites/$id/rf/plan?dry_run=1, and the object of
// the apply response.
type PlanProposal struct {
	Inputs  PlanInputs   `json:"inputs"`
	Changes []PlanChange `json:"changes"`
}

// PlanApplyInput is the body of POST /api/sites/$id/rf/plan (optional).
// Only lists the AP and band pairs to apply; empty means every change that
// is not "keep".
type PlanApplyInput struct {
	Only []PlanSelection `json:"only,omitempty"`
}

// PlanSelection names one AP and band.
type PlanSelection struct {
	APID string `json:"ap_id"`
	Band Band   `json:"band"`
}

// Neighbour is one BSS another party runs, heard by a site AP's scan.
// Neighbour SSIDs and BSSIDs never leave the authenticated API.
type Neighbour struct {
	SSID         string    `json:"ssid"`
	BSSID        string    `json:"bssid"`
	Band         Band      `json:"band"`
	Channel      int       `json:"channel"`
	Width        int       `json:"width"`
	RSSI         int       `json:"rssi"`
	SeenBy       string    `json:"seen_by"`
	SeenByName   string    `json:"seen_by_name"`
	StationCount *int      `json:"station_count" tstype:"number | null,required"`
	Util         *float64  `json:"util" tstype:"number | null,required"`
	LastSeen     time.Time `json:"last_seen"`
}

// ScanAge is the age of one AP's last scan on one band.
type ScanAge struct {
	APID    string     `json:"ap_id"`
	APName  string     `json:"ap_name"`
	Band    Band       `json:"band"`
	At      *time.Time `json:"at" tstype:"string | null,required"`
	Entries int        `json:"entries"`
}

// NeighbourPage is GET /api/sites/$id/rf/neighbours?band=.
type NeighbourPage struct {
	Neighbours []Neighbour `json:"neighbours"`
	// Own lists the site's own BSSIDs heard by another site AP (AP-to-AP
	// visibility); they are not neighbours.
	Own   []Neighbour `json:"own"`
	Scans []ScanAge   `json:"scans"`
}
