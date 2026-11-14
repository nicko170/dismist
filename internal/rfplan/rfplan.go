// Package rfplan chooses channels and power for Auto bands (design §3.10):
// the initial assignment written when an AP joins a site or the RF profile
// changes (Initial), the compile fallback that keeps an Auto band from ever
// compiling with no channel or power (Fallback), and the dismist-side
// channel planner (Plan). Everything here is pure and deterministic: the
// same inputs give the same result.
package rfplan

import (
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
)

// Bands are the access bands, in compile order.
var Bands = []string{regdomain.Band24, regdomain.Band5}

// Power limits (design §3.10 validation): never 0, at most 25 dBm.
const (
	MinPower = 1
	MaxPower = 25
)

// Default Auto power ranges for new sites (design §3.10).
var DefaultPower = map[string]model.PowerPolicy{
	regdomain.Band24: {Mode: model.PowerAuto, Min: 8, Max: 15},
	regdomain.Band5:  {Mode: model.PowerAuto, Min: 12, Max: 20},
}

// Domain returns the regulatory table of the site's country.
func Domain(site model.Site) (*regdomain.Domain, bool) {
	return regdomain.ForCountry(site.Country)
}

// Width is the band's width for an AP: the AP override, else the site
// profile, else 20 MHz.
func Width(site model.Site, ap model.APRecord, band string) int {
	if override := ap.RF[band]; override != nil && override.Width > 0 {
		return override.Width
	}
	if width := site.RF.Band(band).Width; width > 0 {
		return width
	}
	return 20
}

// Enabled reports whether a band's radio is on for an AP.
func Enabled(site model.Site, ap model.APRecord, band string) bool {
	if override := ap.RF[band]; override != nil && override.Enabled != nil {
		return *override.Enabled
	}
	return site.RF.Band(band).Enabled
}

// Static returns an AP's static channel for a band (0 = Auto).
func Static(ap model.APRecord, band string) int {
	if override := ap.RF[band]; override != nil {
		return override.Channel
	}
	return 0
}

// Auto reports whether the band of an AP is planned: RF managed, the radio
// enabled and no static channel.
func Auto(site model.Site, ap model.APRecord, band string) bool {
	return site.RF.Managed && Enabled(site, ap, band) && Static(ap, band) == 0
}

// AllowedChannels is the Auto channel list of a band at a width: the
// profile's channels (or the country's default Auto list) that are valid
// primaries at that width, without DFS channels unless the profile allows
// them, deduplicated per 40/80 MHz block (the first listed primary of each
// block), ascending.
func AllowedChannels(site model.Site, band string, width int) []int {
	domain, ok := Domain(site)
	if !ok {
		return nil
	}
	profile := site.RF.Band(band)
	channels := profile.Channels
	if len(channels) == 0 {
		channels = domain.DefaultAuto[band]
	}
	dfsAllowed := band == regdomain.Band5 && profile.DFS
	seenBlock := map[int]bool{}
	var result []int
	sorted := append([]int(nil), channels...)
	sort.Ints(sorted)
	for _, channel := range sorted {
		dfs, valid := domain.Valid(band, width, channel)
		if !valid || (dfs && !dfsAllowed) {
			continue
		}
		block := domain.Block(band, width, channel)
		if len(block) == 0 || seenBlock[block[0]] {
			continue
		}
		seenBlock[block[0]] = true
		result = append(result, channel)
	}
	return result
}

// PowerRange is the Auto power range of a band (fixed power gives min ==
// max).
func PowerRange(site model.Site, band string) (int, int) {
	policy := site.RF.Band(band).Power
	if policy.Mode == model.PowerFixed && policy.DBm > 0 {
		return policy.DBm, policy.DBm
	}
	low, high := policy.Min, policy.Max
	if low <= 0 && high <= 0 {
		low, high = DefaultPower[band].Min, DefaultPower[band].Max
	}
	if low < MinPower {
		low = MinPower
	}
	if high > MaxPower {
		high = MaxPower
	}
	if high < low {
		high = low
	}
	return low, high
}

// ValidPlan reports whether a stored plan still fits the profile: its
// channel is an allowed primary at the band's width (DFS only when allowed)
// and its power is inside the Auto range.
func ValidPlan(site model.Site, ap model.APRecord, band string, plan model.PlannedRadio) bool {
	width := Width(site, ap, band)
	if plan.Channel == 0 || plan.Width != width {
		return false
	}
	domain, ok := Domain(site)
	if !ok {
		return false
	}
	dfs, valid := domain.Valid(band, width, plan.Channel)
	if !valid || (dfs && !site.RF.Band5.DFS) {
		return false
	}
	allowedBlock := false
	block := domain.Block(band, width, plan.Channel)
	for _, channel := range AllowedChannels(site, band, width) {
		other := domain.Block(band, width, channel)
		if len(other) > 0 && len(block) > 0 && other[0] == block[0] {
			allowedBlock = true
		}
	}
	if !allowedBlock {
		return false
	}
	low, high := PowerRange(site, band)
	return plan.PowerDBm >= low && plan.PowerDBm <= high
}

// Fallback is the compile fallback for an Auto band without a valid plan
// (design §3.10 step 2): a channel chosen from the AP's own ID (FNV-1a
// modulo the allowed list, so it does not depend on other APs) and the
// range maximum. It is never expected to run; compile reports
// rf_plan_missing when it does.
func Fallback(site model.Site, ap model.APRecord, band string) (model.PlannedRadio, bool) {
	width := Width(site, ap, band)
	allowed := AllowedChannels(site, band, width)
	if len(allowed) == 0 {
		return model.PlannedRadio{}, false
	}
	hash := fnv.New32a()
	hash.Write([]byte(ap.ID))
	channel := allowed[int(hash.Sum32()%uint32(len(allowed)))]
	_, high := PowerRange(site, band)
	plan := model.PlannedRadio{Channel: channel, Width: width, PowerDBm: high, Source: "fallback", Reason: "compile fallback (no valid plan)"}
	plan.DFSFallback = dfsFallback(site, band, width, channel, nil)
	return plan, true
}

// dfsFallback picks the non-DFS fallback of a DFS channel: the lowest-cost
// non-DFS allowed primary at the same width (cost nil: the lowest).
func dfsFallback(site model.Site, band string, width, channel int, cost func(int) float64) int {
	domain, ok := Domain(site)
	if !ok || band != regdomain.Band5 {
		return 0
	}
	if dfs, valid := domain.Valid(band, width, channel); !valid || !dfs {
		return 0
	}
	if fixed := site.RF.Band5.DFSFallback; fixed != 0 {
		if dfs, valid := domain.Valid(band, width, fixed); valid && !dfs {
			return fixed
		}
	}
	candidates := domain.DefaultAuto[band]
	best, bestCost := 0, 0.0
	for _, candidate := range candidates {
		dfs, valid := domain.Valid(band, width, candidate)
		if !valid || dfs {
			continue
		}
		c := 0.0
		if cost != nil {
			c = cost(candidate)
		}
		if best == 0 || c < bestCost || (c == bestCost && candidate < best) {
			best, bestCost = candidate, c
		}
	}
	return best
}

// DefaultDFSFallback is the non-DFS fallback for a DFS channel when no
// plan names one: the first non-DFS default Auto channel at the width.
func DefaultDFSFallback(site model.Site, band string, width int) int {
	domain, ok := Domain(site)
	if !ok {
		return 0
	}
	for _, candidate := range domain.DefaultAuto[band] {
		if dfs, valid := domain.Valid(band, width, candidate); valid && !dfs {
			return candidate
		}
	}
	return 0
}

// Assignment is one band of one AP that Initial (or Plan) wrote.
type Assignment struct {
	APID string             `json:"ap_id"`
	Band string             `json:"band"`
	Plan model.PlannedRadio `json:"plan"`
}

// usage is the channel occupancy of the other APs of a site, by block.
func usageCount(site model.Site, aps []model.APRecord, skip string, band string, width int) map[int]int {
	domain, ok := Domain(site)
	counts := map[int]int{}
	if !ok {
		return counts
	}
	for _, ap := range aps {
		if ap.ID == skip || !Enabled(site, ap, band) {
			continue
		}
		channel := Static(ap, band)
		apWidth := Width(site, ap, band)
		if channel == 0 {
			if plan, ok := ap.RFPlan[band]; ok {
				channel = plan.Channel
				apWidth = plan.Width
			}
		}
		if channel == 0 {
			continue
		}
		for _, member := range blockOf(domain, band, apWidth, channel) {
			counts[member]++
		}
	}
	return counts
}

func blockOf(domain *regdomain.Domain, band string, width, channel int) []int {
	if block := domain.Block(band, width, channel); len(block) > 0 {
		return block
	}
	return []int{channel}
}

// Initial assigns every Auto band of the given APs that has no valid plan
// (design §3.10 "Auto before the planner"): the allowed channel used by
// the fewest other APs of the site (their plans or static channels), ties
// to the lowest channel, and the maximum of the Auto range. APs with a
// valid plan are never moved, so adding an AP does not bounce the others.
// aps is every AP of the site; the result lists only new assignments,
// sorted by AP ID then band.
func Initial(site model.Site, aps []model.APRecord, now time.Time) []Assignment {
	sorted := append([]model.APRecord(nil), aps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for index := range sorted {
		// Work on copies: the caller's records keep their maps.
		plans := make(map[string]model.PlannedRadio, len(sorted[index].RFPlan))
		for band, plan := range sorted[index].RFPlan {
			plans[band] = plan
		}
		sorted[index].RFPlan = plans
	}
	var result []Assignment
	for index := range sorted {
		ap := &sorted[index]
		for _, band := range Bands {
			if !Auto(site, *ap, band) {
				continue
			}
			if plan, ok := ap.RFPlan[band]; ok && ValidPlan(site, *ap, band, plan) {
				continue
			}
			width := Width(site, *ap, band)
			allowed := AllowedChannels(site, band, width)
			if len(allowed) == 0 {
				continue
			}
			counts := usageCount(site, sorted, ap.ID, band, width)
			best, bestCount := 0, 0
			for _, channel := range allowed {
				if best == 0 || counts[channel] < bestCount {
					best, bestCount = channel, counts[channel]
				}
			}
			_, high := PowerRange(site, band)
			plan := model.PlannedRadio{
				Channel: best, Width: width, PowerDBm: high, Source: model.PlanSourceInitial,
				Reason: "initial assignment", PlannedAt: now.UTC(),
			}
			plan.DFSFallback = dfsFallback(site, band, width, best, nil)
			plan.InputsHash = inputsHash(fmt.Sprintf("initial|%s|%s|%d|%v|%d", ap.ID, band, width, allowed, counts[best]))
			if ap.RFPlan == nil {
				ap.RFPlan = map[string]model.PlannedRadio{}
			}
			ap.RFPlan[band] = plan // later APs see this one
			result = append(result, Assignment{APID: ap.ID, Band: band, Plan: plan})
		}
	}
	return result
}

// ApplyInitial runs Initial for the given sites (every site when none are
// given) and writes the assignments into state. The mutation that creates,
// registers or moves an AP, or changes a site's RF profile, calls it in
// the same transaction. It returns the assignments written.
func ApplyInitial(state *model.State, now time.Time, siteIDs ...string) []Assignment {
	want := map[string]bool{}
	for _, id := range siteIDs {
		want[id] = true
	}
	var all []Assignment
	for _, site := range state.Sites {
		if len(want) > 0 && !want[site.ID] {
			continue
		}
		var aps []model.APRecord
		for _, ap := range state.APs {
			if ap.SiteID == site.ID {
				aps = append(aps, ap)
			}
		}
		assignments := Initial(site, aps, now)
		for _, assignment := range assignments {
			for index := range state.APs {
				ap := &state.APs[index]
				if ap.ID != assignment.APID {
					continue
				}
				if ap.RFPlan == nil {
					ap.RFPlan = map[string]model.PlannedRadio{}
				}
				ap.RFPlan[assignment.Band] = assignment.Plan
			}
		}
		all = append(all, assignments...)
	}
	return all
}

// OwnBSSIDs returns each AP's own BSSIDs (field 32.3) from the stored AP
// status, for Inputs.OwnBSSIDs.
func OwnBSSIDs(status map[string]model.APStatusRecord) map[string][]string {
	result := map[string][]string{}
	for apID, record := range status {
		for _, vap := range record.VAPs {
			if vap.BSSID != "" {
				result[apID] = append(result[apID], vap.BSSID)
			}
		}
		sort.Strings(result[apID])
	}
	return result
}

// ScanMap indexes a site's stored scans by AP ID, for Inputs.Scans.
func ScanMap(scans []model.APScan) map[string]model.APScan {
	result := make(map[string]model.APScan, len(scans))
	for _, scan := range scans {
		result[scan.APID] = scan
	}
	return result
}

// SiteInputs gathers the planner inputs of one site from state and the
// stores' data (telemetry-derived maps may be nil).
func SiteInputs(state model.State, siteID string, scans []model.APScan, status map[string]model.APStatusRecord,
	utilisation map[string]map[string]float64, current map[string]map[string]int, now time.Time) (Inputs, bool) {
	in := Inputs{Scans: ScanMap(scans), OwnBSSIDs: OwnBSSIDs(status), Utilisation: utilisation, Current: current, Now: now}
	found := false
	for _, site := range state.Sites {
		if site.ID == siteID {
			in.Site, found = site, true
		}
	}
	for _, ap := range state.APs {
		if ap.SiteID == siteID {
			in.APs = append(in.APs, ap)
		}
	}
	return in, found
}
