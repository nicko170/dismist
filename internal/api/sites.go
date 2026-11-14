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
}

// SiteInput is POST /api/sites.
type SiteInput struct {
	Name     string `json:"name"`
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
}
