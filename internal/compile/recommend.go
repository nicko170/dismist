package compile

import (
	"fmt"
	"sort"
	"time"

	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
	"github.com/nicko170/dismist/internal/rfplan"
)

// Recommendations (design §4.6): a named gap between what a migrated
// object runs and the research default, fixed through a normal, previewed
// mutation. The caller previews Apply with a dry run (change detection
// gives the impact and the per-AP diff) and commits it like any other
// config change.

// Recommendation is one offered change.
type Recommendation struct {
	ID         string `json:"id"` // "<kind>:<object ID>", stable
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Scope      string `json:"scope"` // "site" | "ap" | "wlan"
	ObjectID   string `json:"object_id"`
	Disruption string `json:"disruption"`
	// Informational recommendations only explain; Apply still offers the
	// change.
	Informational bool `json:"informational,omitempty"`
}

// Recommendation kinds.
const (
	RecommendManageRF        = "manage_rf"
	RecommendScanRadioCCRev  = "scan_radio_ccrev"
	RecommendTemplateQOS     = "template_qos"
	RecommendPoEKeepRadios   = "poe_keep_radios"
	RecommendDHCPHostname    = "dhcp_hostname"
	RecommendBandSteeringOff = "band_steering_off"
)

func residualHas(raw []byte, check func(map[string]any) bool) bool {
	if len(raw) == 0 {
		return false
	}
	patch, err := decodeObject(raw)
	return err == nil && check(patch)
}

func hasScanCCRev(patch map[string]any) bool {
	_, ok := valueAt(patch, "Monitor", "CCRev")
	return ok
}

func hasQOSNull(patch map[string]any) bool {
	radios, _ := patch["Radios"].([]any)
	for _, element := range radios {
		if radio, ok := element.(map[string]any); ok {
			if _, ok := radio["QOS"]; ok {
				return true
			}
		}
	}
	return false
}

// Recommendations lists the recommendations of a site, sorted by ID.
func Recommendations(state model.State, siteID string) []Recommendation {
	var result []Recommendation
	var site *model.Site
	for index := range state.Sites {
		if state.Sites[index].ID == siteID {
			site = &state.Sites[index]
		}
	}
	if site == nil {
		return nil
	}
	if !site.RF.Managed {
		if ccrev, ok := regdomain.CCRev(site.Country); ok {
			result = append(result, Recommendation{
				ID: RecommendManageRF + ":" + site.ID, Kind: RecommendManageRF, Scope: ScopeSite, ObjectID: site.ID,
				Title:      "Manage RF",
				Detail:     fmt.Sprintf("Your APs use the firmware defaults: channel 36/20 and 1/20, US/0 rules and a 0 dBm power target. Manage RF with %s, Auto channels and Auto power.", ccrev),
				Disruption: "all_radios_bounce",
			})
		}
	}
	if residualHas(site.Expert, hasScanCCRev) {
		ccrev, _ := regdomain.CCRev(site.Country)
		result = append(result, Recommendation{
			ID: RecommendScanRadioCCRev + ":" + site.ID, Kind: RecommendScanRadioCCRev, Scope: ScopeSite, ObjectID: site.ID,
			Title:      fmt.Sprintf("Scan radio uses US/0. Apply %s", ccrev),
			Detail:     "The expert patch keeps the scanning radio on the US/0 rules it ran before the migration. Remove that key so the scan radio uses the site's country.",
			Disruption: "radio_bounce",
		})
	}
	if residualHas(site.Expert, hasQOSNull) {
		result = append(result, Recommendation{
			ID: RecommendTemplateQOS + ":" + site.ID, Kind: RecommendTemplateQOS, Scope: ScopeSite, ObjectID: site.ID,
			Title:      "Restore the template's radio QoS tables",
			Detail:     "The old Radios patch removed the DSCP-to-access-category tables and other radio defaults. Remove those keys from the expert patch.",
			Disruption: "all_radios_bounce",
		})
	}
	if site.APDefaults.PoEConstrained != model.PoEKeepRadios {
		result = append(result, Recommendation{
			ID: RecommendPoEKeepRadios + ":" + site.ID, Kind: RecommendPoEKeepRadios, Scope: ScopeSite, ObjectID: site.ID,
			Title: "Keep radios on when PoE is short (Mist default)", Detail: "Today the APs turn radios off when the switch cannot supply full PoE power.",
			Disruption: "none",
		})
	}
	if !site.APDefaults.HostnameFromName {
		result = append(result, Recommendation{
			ID: RecommendDHCPHostname + ":" + site.ID, Kind: RecommendDHCPHostname, Scope: ScopeSite, ObjectID: site.ID,
			Title: "Send the AP name as DHCP hostname so your router shows it", Detail: "The hostname is sanitised (lower case, a to z, 0 to 9 and -).",
			Disruption: "none",
		})
	}
	for _, network := range state.Networks {
		if network.SiteID == siteID && network.BandSteering {
			result = append(result, Recommendation{
				ID: RecommendBandSteeringOff + ":" + network.ID, Kind: RecommendBandSteeringOff, Scope: ScopeWLAN, ObjectID: network.ID,
				Title:         fmt.Sprintf("Band steering is on for %s; Mist's default is off", network.SSID),
				Detail:        "Band steering denies 2.4 GHz associations to dual-band clients. Some clients join more slowly with it.",
				Disruption:    "wlan_restart",
				Informational: true,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// ErrUnknownRecommendation is returned for an ID the site does not offer.
var ErrUnknownRecommendation = fmt.Errorf("unknown recommendation")

// ApplyRecommendation makes the change of one recommendation in state (the
// caller runs it inside a mutation, with a dry run first).
func ApplyRecommendation(state *model.State, siteID, id string, now time.Time) error {
	offered := false
	var rec Recommendation
	for _, candidate := range Recommendations(*state, siteID) {
		if candidate.ID == id {
			offered, rec = true, candidate
		}
	}
	if !offered {
		return ErrUnknownRecommendation
	}
	for index := range state.Sites {
		site := &state.Sites[index]
		if site.ID != siteID {
			continue
		}
		switch rec.Kind {
		case RecommendManageRF:
			country := site.Country
			domain, _ := regdomain.ForCountry(country)
			site.RF.Managed = true
			for _, band := range rfplan.Bands {
				profile := site.RF.Band(band)
				profile.Enabled = true
				if profile.Width == 0 {
					profile.Width = map[string]int{"2.4": 20, "5": 80}[band]
				}
				if len(profile.Channels) == 0 && domain != nil {
					profile.Channels = append([]int(nil), domain.DefaultAuto[band]...)
				}
				if profile.Power.Mode == "" {
					profile.Power = rfplan.DefaultPower[band]
				}
			}
			rfplan.ApplyInitial(state, now, siteID)
		case RecommendScanRadioCCRev:
			site.Expert = removeFromPatch(site.Expert, func(patch map[string]any) {
				if monitor, ok := patch["Monitor"].(map[string]any); ok {
					delete(monitor, "CCRev")
					if len(monitor) == 0 {
						delete(patch, "Monitor")
					}
				}
			})
		case RecommendTemplateQOS:
			site.Expert = removeFromPatch(site.Expert, func(patch map[string]any) {
				radios, _ := patch["Radios"].([]any)
				var kept []any
				for _, element := range radios {
					radio, ok := element.(map[string]any)
					if !ok {
						continue
					}
					for _, key := range []string{"QOS", "NonAggrRegulate", "ProbeControl", "DFSjumplist", "PreambleLength"} {
						delete(radio, key)
					}
					if len(radio) > 1 {
						kept = append(kept, radio)
					}
				}
				if len(kept) == 0 {
					delete(patch, "Radios")
				} else {
					patch["Radios"] = kept
				}
			})
		case RecommendPoEKeepRadios:
			site.APDefaults.PoEConstrained = model.PoEKeepRadios
		case RecommendDHCPHostname:
			site.APDefaults.HostnameFromName = true
		}
	}
	if rec.Kind == RecommendBandSteeringOff {
		for index := range state.Networks {
			if state.Networks[index].ID == rec.ObjectID && state.Networks[index].SiteID == siteID {
				state.Networks[index].BandSteering = false
			}
		}
	}
	return nil
}

func removeFromPatch(raw []byte, change func(map[string]any)) []byte {
	patch, err := decodeObject(raw)
	if err != nil {
		return raw
	}
	change(patch)
	encoded, err := EncodePatch(patch)
	if err != nil {
		return raw
	}
	return encoded
}
