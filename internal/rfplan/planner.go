package rfplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
)

// The dismist-side channel planner (design §3.10, phase 3). The AP has no
// local ACS, so dismist plans: static APs are fixed constraints; every
// other AP's Auto band is assigned greedily by cost, most constrained
// first, then improved in two rounds, with hysteresis against the current
// channel. v1 never re-plans automatically; the operator previews
// (dry run) and applies.

// Hysteresis is the minimum relative cost improvement for a move: a
// proposal that improves the cost by less is kept as it is ("keep").
const Hysteresis = 0.25

// Inputs is everything the planner reads.
type Inputs struct {
	Site model.Site
	// APs are the site's APs.
	APs []model.APRecord
	// Scans are the latest neighbour scans by AP ID (model.ScanStore).
	Scans map[string]model.APScan
	// OwnBSSIDs lists each site AP's own BSSIDs (field 32.3), lower case.
	OwnBSSIDs map[string][]string
	// Utilisation is the current radio utilisation by AP ID and band
	// (31.30.1.1 All, 0 to 1), used when a scan has no channel stats.
	Utilisation map[string]map[string]float64
	// Current is the channel each AP's band runs now (telemetry), by AP
	// ID and band; absent falls back to the stored plan or static channel.
	Current map[string]map[string]int
	Now     time.Time
}

// Change is the proposal for one AP band (PlanProposal.changes).
type Change struct {
	APID        string             `json:"ap_id"`
	Band        string             `json:"band"`
	FromChannel int                `json:"from_channel"`
	FromPower   int                `json:"from_power_dbm"`
	To          model.PlannedRadio `json:"to"`
	CostBefore  float64            `json:"cost_before"`
	CostAfter   float64            `json:"cost_after"`
	// Keep is true when the proposal leaves the channel as it is (no
	// better channel, or within the hysteresis).
	Keep       bool   `json:"keep"`
	Reason     string `json:"reason"`
	Disruption string `json:"disruption"` // "radio_bounce" | "none"
	Static     bool   `json:"static,omitempty"`
}

// Result is a plan for a site.
type Result struct {
	Changes []Change `json:"changes"`
	// ScanAge is the age of each AP's newest scan in seconds (nil: none).
	ScanAge    map[string]*float64 `json:"scans_age_s"`
	InputsHash string              `json:"inputs_hash"`
	// NoScans reports that no AP had scan data, so APs were spread
	// round-robin over the allowed list.
	NoScans bool `json:"no_scans"`
}

// Assignments returns the planned radios of the changes that move or
// re-power a band (the ones Apply writes), sorted by AP ID then band.
func (r Result) Assignments() []Assignment {
	var result []Assignment
	for _, change := range r.Changes {
		if change.Static {
			continue
		}
		result = append(result, Assignment{APID: change.APID, Band: change.Band, Plan: change.To})
	}
	return result
}

// Apply writes a result's plans into state (every non-static band, so
// "keep" refreshes the plan's reason and inputs hash without moving it).
// Change detection then pushes only APs whose compiled config changed.
func Apply(state *model.State, result Result) {
	for _, assignment := range result.Assignments() {
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
}

func inputsHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// weight doubles every 6 dB and is 1 at -90 dBm.
func weight(rssi int) float64 { return math.Pow(2, float64(rssi+90)/6) }

// overlap reports whether two channels' spans intersect: on 2.4 GHz when
// |c1 - c2| < 5; on 5 GHz when either primary falls in the other's
// 20/40/80 MHz block.
func overlap(domain *regdomain.Domain, band string, c1, w1, c2, w2 int) bool {
	if band == regdomain.Band24 {
		d := c1 - c2
		if d < 0 {
			d = -d
		}
		return d < 5
	}
	b1 := blockOf(domain, band, w1, c1)
	b2 := blockOf(domain, band, w2, c2)
	for _, x := range b1 {
		for _, y := range b2 {
			if x == y {
				return true
			}
		}
	}
	return false
}

type planner struct {
	in      Inputs
	domain  *regdomain.Domain
	band    string
	ownerOf map[string]string         // BSSID → AP ID
	edges   map[string]map[string]int // AP → AP → strongest RSSI either way
	chosen  map[string]int            // AP → channel
	widths  map[string]int
	current map[string]int
}

func (p *planner) neighbours(apID string) []model.ScanNeighbour {
	scan, ok := p.in.Scans[apID]
	if !ok {
		return nil
	}
	return scan.Bands[p.band].Neighbours
}

func (p *planner) channelStat(apID string, channel int) (util float64, noise int, ok bool) {
	scan, found := p.in.Scans[apID]
	if !found {
		return 0, 0, false
	}
	for _, stat := range scan.Bands[p.band].Channels {
		if stat.Channel == channel {
			return stat.Util, stat.NoiseDBm, true
		}
	}
	return 0, 0, false
}

// cost is the design §3.10 cost of channel c for AP a (change penalty
// optional).
func (p *planner) cost(apID string, c int, penalty bool) float64 {
	width := p.widths[apID]
	total := 0.0
	for other, rssi := range p.edges[apID] {
		otherChannel, ok := p.chosen[other]
		if !ok || otherChannel == 0 {
			continue
		}
		if overlap(p.domain, p.band, c, width, otherChannel, p.widths[other]) {
			total += 2 * weight(rssi)
		}
	}
	for _, neighbour := range p.neighbours(apID) {
		if neighbour.Own || p.ownerOf[neighbour.BSSID] != "" {
			continue
		}
		nWidth := neighbour.Width
		if nWidth == 0 {
			nWidth = 20
		}
		if !overlap(p.domain, p.band, c, width, neighbour.Channel, nWidth) {
			continue
		}
		util := 0.0
		if neighbour.Util != nil {
			util = *neighbour.Util
		}
		total += weight(neighbour.RSSI) * (1 + util)
	}
	if util, noise, ok := p.channelStat(apID, c); ok {
		total += 4 * util
		if extra := float64(noise + 90); extra > 0 {
			total += extra / 3
		}
	}
	if dfs, _ := p.domain.Valid(p.band, width, c); dfs {
		total += 1.5
	}
	if penalty && c != p.current[apID] {
		total += 1.0
	}
	return total
}

// Plan plans one site (design §3.10). Bands that are not Auto on an AP
// (static channel, radio disabled, RF unmanaged) are reported as static
// and never moved.
func Plan(in Inputs) Result {
	result := Result{ScanAge: map[string]*float64{}}
	domain, ok := Domain(in.Site)
	aps := append([]model.APRecord(nil), in.APs...)
	sort.Slice(aps, func(i, j int) bool { return aps[i].ID < aps[j].ID })
	anyScan := false
	for _, ap := range aps {
		scan, found := in.Scans[ap.ID]
		var newest time.Time
		if found {
			for _, band := range scan.Bands {
				if band.At.After(newest) {
					newest = band.At
				}
				if len(band.Neighbours) > 0 || len(band.Channels) > 0 {
					anyScan = true
				}
			}
		}
		if newest.IsZero() {
			result.ScanAge[ap.ID] = nil
		} else {
			age := in.Now.Sub(newest).Seconds()
			result.ScanAge[ap.ID] = &age
		}
	}
	result.NoScans = !anyScan
	hashInput, _ := json.Marshal(struct {
		Site  model.RFProfile
		APs   []model.APRecord
		Scans map[string]model.APScan
		Own   map[string][]string
		Util  map[string]map[string]float64
		Cur   map[string]map[string]int
	}{in.Site.RF, aps, in.Scans, in.OwnBSSIDs, in.Utilisation, in.Current})
	result.InputsHash = inputsHash(string(hashInput))
	if !ok || !in.Site.RF.Managed {
		return result
	}
	for _, band := range Bands {
		result.Changes = append(result.Changes, planBand(in, domain, aps, band, anyScan, result.InputsHash)...)
	}
	return result
}

func planBand(in Inputs, domain *regdomain.Domain, aps []model.APRecord, band string, anyScan bool, hash string) []Change {
	p := &planner{in: in, domain: domain, band: band, ownerOf: map[string]string{},
		edges: map[string]map[string]int{}, chosen: map[string]int{}, widths: map[string]int{}, current: map[string]int{}}
	for apID, bssids := range in.OwnBSSIDs {
		for _, bssid := range bssids {
			p.ownerOf[strings.ToLower(bssid)] = apID
		}
	}
	var auto []model.APRecord
	var changes []Change
	for _, ap := range aps {
		if !Enabled(in.Site, ap, band) {
			continue
		}
		p.widths[ap.ID] = Width(in.Site, ap, band)
		current := in.Current[ap.ID][band]
		if current == 0 {
			if static := Static(ap, band); static != 0 {
				current = static
			} else if plan, ok := ap.RFPlan[band]; ok {
				current = plan.Channel
			}
		}
		p.current[ap.ID] = current
		if static := Static(ap, band); static != 0 {
			p.chosen[ap.ID] = static
			changes = append(changes, Change{APID: ap.ID, Band: band, FromChannel: static, Static: true, Keep: true,
				To: model.PlannedRadio{Channel: static, Width: p.widths[ap.ID]}, Reason: "static channel (AP override)", Disruption: "none"})
			continue
		}
		auto = append(auto, ap)
	}
	// AP-to-AP edges, from either side's scan.
	for _, ap := range aps {
		for _, neighbour := range p.neighbours(ap.ID) {
			owner := p.ownerOf[strings.ToLower(neighbour.BSSID)]
			if owner == "" && neighbour.Own {
				owner = neighbour.OwnAPID
			}
			if owner == "" || owner == ap.ID {
				continue
			}
			for _, pair := range [][2]string{{ap.ID, owner}, {owner, ap.ID}} {
				if p.edges[pair[0]] == nil {
					p.edges[pair[0]] = map[string]int{}
				}
				if existing, ok := p.edges[pair[0]][pair[1]]; !ok || neighbour.RSSI > existing {
					p.edges[pair[0]][pair[1]] = neighbour.RSSI
				}
			}
		}
	}
	if len(auto) == 0 {
		return changes
	}
	allowedFor := func(ap model.APRecord) []int { return AllowedChannels(in.Site, band, p.widths[ap.ID]) }
	if !anyScan {
		// No scan data anywhere: spread round-robin in AP-ID order,
		// counting static APs as already placed.
		for index, ap := range auto {
			allowed := allowedFor(ap)
			if len(allowed) == 0 {
				continue
			}
			used := map[int]int{}
			for other, channel := range p.chosen {
				for _, member := range blockOf(domain, band, p.widths[other], channel) {
					used[member]++
				}
			}
			best := allowed[index%len(allowed)]
			bestUse := used[best]
			for offset := range allowed {
				candidate := allowed[(index+offset)%len(allowed)]
				if used[candidate] < bestUse {
					best, bestUse = candidate, used[candidate]
				}
			}
			p.chosen[ap.ID] = best
		}
	} else {
		// Most constrained first: neighbours above -75 dBm, then AP ID.
		strong := func(ap model.APRecord) int {
			count := 0
			for _, neighbour := range p.neighbours(ap.ID) {
				if neighbour.RSSI > -75 {
					count++
				}
			}
			return count
		}
		sort.SliceStable(auto, func(i, j int) bool {
			si, sj := strong(auto[i]), strong(auto[j])
			if si != sj {
				return si > sj
			}
			return auto[i].ID < auto[j].ID
		})
		choose := func(ap model.APRecord) {
			best, bestCost := 0, 0.0
			for _, channel := range allowedFor(ap) {
				c := p.cost(ap.ID, channel, false)
				if best == 0 || c < bestCost-1e-9 {
					best, bestCost = channel, c
				}
			}
			if best != 0 {
				p.chosen[ap.ID] = best
			}
		}
		for _, ap := range auto {
			choose(ap)
		}
		for round := 0; round < 2; round++ {
			for _, ap := range auto {
				delete(p.chosen, ap.ID)
				choose(ap)
			}
		}
	}
	sort.Slice(auto, func(i, j int) bool { return auto[i].ID < auto[j].ID })
	for _, ap := range auto {
		proposed, ok := p.chosen[ap.ID]
		if !ok {
			continue
		}
		current := p.current[ap.ID]
		change := Change{APID: ap.ID, Band: band, FromChannel: current}
		if plan, ok := ap.RFPlan[band]; ok {
			change.FromPower = plan.PowerDBm
		}
		change.CostAfter = p.cost(ap.ID, proposed, true)
		change.CostBefore = change.CostAfter
		valid := false
		if current != 0 {
			for _, channel := range allowedFor(ap) {
				if sameBlock(domain, band, p.widths[ap.ID], channel, current) {
					valid = true
				}
			}
		}
		if valid {
			p.chosen[ap.ID] = current
			change.CostBefore = p.cost(ap.ID, current, true)
			p.chosen[ap.ID] = proposed
		}
		final := proposed
		switch {
		case valid && sameBlock(domain, band, p.widths[ap.ID], proposed, current):
			final, change.Keep = current, true
			change.Reason = "already on the best channel"
		case valid && change.CostBefore > 0 && (change.CostBefore-change.CostAfter)/change.CostBefore < Hysteresis:
			final, change.Keep = current, true
			change.Reason = fmt.Sprintf("keep: channel %d is within %d%% of the best", current, int(Hysteresis*100))
			change.CostAfter = change.CostBefore
		case valid && change.CostBefore == 0:
			final, change.Keep = current, true
			change.Reason = "keep: no interference measured"
			change.CostAfter = 0
		default:
			change.Reason = reasonFor(p, ap.ID, final, anyScan, current)
		}
		p.chosen[ap.ID] = final
		power := planPower(p, ap.ID)
		change.To = model.PlannedRadio{
			Channel: final, Width: p.widths[ap.ID], PowerDBm: power, Source: model.PlanSourcePlanner,
			Reason: change.Reason, InputsHash: hash, PlannedAt: in.Now.UTC(),
		}
		change.To.DFSFallback = dfsFallback(in.Site, band, p.widths[ap.ID], final, func(c int) float64 { return p.cost(ap.ID, c, false) })
		change.Disruption = "none"
		if final != current || power != change.FromPower {
			change.Disruption = "radio_bounce"
		}
		changes = append(changes, change)
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].APID < changes[j].APID })
	return changes
}

func sameBlock(domain *regdomain.Domain, band string, width, a, b int) bool {
	if a == b {
		return true
	}
	ba, bb := domain.Block(band, width, a), domain.Block(band, width, b)
	return len(ba) > 0 && len(bb) > 0 && ba[0] == bb[0]
}

func reasonFor(p *planner, apID string, channel int, anyScan bool, current int) string {
	if !anyScan {
		return "no scan data: spread over the allowed channels"
	}
	coChannel, strongest := 0, -200
	for _, neighbour := range p.neighbours(apID) {
		if overlap(p.domain, p.band, current, p.widths[apID], neighbour.Channel, max(neighbour.Width, 20)) {
			coChannel++
			if neighbour.RSSI > strongest {
				strongest = neighbour.RSSI
			}
		}
	}
	if current != 0 && coChannel > 0 {
		return fmt.Sprintf("co-channel with %d neighbours at %d dBm on %d; %d is clearer", coChannel, strongest, current, channel)
	}
	return fmt.Sprintf("lowest-cost channel %d", channel)
}

// planPower is the Auto power (design §3.10): one AP on the band → the
// range max; several → the max minus 3 dB per other site AP heard above
// -60 dBm, clamped to the min. Fixed mode always wins.
func planPower(p *planner, apID string) int {
	low, high := PowerRange(p.in.Site, p.band)
	if p.in.Site.RF.Band(p.band).Power.Mode == model.PowerFixed {
		return high
	}
	power := high
	for other, rssi := range p.edges[apID] {
		if _, active := p.widths[other]; active && rssi > -60 {
			power -= 3
		}
	}
	if power < low {
		power = low
	}
	return power
}

// InitialWithScans is the phase-3 initial assignment (design §3.10
// "Triggers"): for every Auto band without a valid plan, the channel of
// least cost by the planner's cost function with no change penalty,
// holding every other AP fixed. Without any scan data it is Initial.
func InitialWithScans(in Inputs) []Assignment {
	anyScan := false
	for _, scan := range in.Scans {
		for _, band := range scan.Bands {
			anyScan = anyScan || len(band.Neighbours) > 0 || len(band.Channels) > 0
		}
	}
	if !anyScan {
		return Initial(in.Site, in.APs, in.Now)
	}
	domain, ok := Domain(in.Site)
	if !ok || !in.Site.RF.Managed {
		return nil
	}
	aps := append([]model.APRecord(nil), in.APs...)
	sort.Slice(aps, func(i, j int) bool { return aps[i].ID < aps[j].ID })
	var result []Assignment
	for _, band := range Bands {
		p := &planner{in: in, domain: domain, band: band, ownerOf: map[string]string{},
			edges: map[string]map[string]int{}, chosen: map[string]int{}, widths: map[string]int{}, current: map[string]int{}}
		for apID, bssids := range in.OwnBSSIDs {
			for _, bssid := range bssids {
				p.ownerOf[strings.ToLower(bssid)] = apID
			}
		}
		var pending []model.APRecord
		for _, ap := range aps {
			if !Enabled(in.Site, ap, band) {
				continue
			}
			p.widths[ap.ID] = Width(in.Site, ap, band)
			if static := Static(ap, band); static != 0 {
				p.chosen[ap.ID] = static
				continue
			}
			if plan, ok := ap.RFPlan[band]; ok && ValidPlan(in.Site, ap, band, plan) {
				p.chosen[ap.ID] = plan.Channel
				continue
			}
			pending = append(pending, ap)
		}
		for _, ap := range aps {
			for _, neighbour := range p.neighbours(ap.ID) {
				owner := p.ownerOf[strings.ToLower(neighbour.BSSID)]
				if owner == "" || owner == ap.ID {
					continue
				}
				for _, pair := range [][2]string{{ap.ID, owner}, {owner, ap.ID}} {
					if p.edges[pair[0]] == nil {
						p.edges[pair[0]] = map[string]int{}
					}
					if existing, ok := p.edges[pair[0]][pair[1]]; !ok || neighbour.RSSI > existing {
						p.edges[pair[0]][pair[1]] = neighbour.RSSI
					}
				}
			}
		}
		for _, ap := range pending {
			best, bestCost := 0, 0.0
			for _, channel := range AllowedChannels(in.Site, band, p.widths[ap.ID]) {
				c := p.cost(ap.ID, channel, false)
				if best == 0 || c < bestCost-1e-9 {
					best, bestCost = channel, c
				}
			}
			if best == 0 {
				continue
			}
			p.chosen[ap.ID] = best
			plan := model.PlannedRadio{Channel: best, Width: p.widths[ap.ID], PowerDBm: planPower(p, ap.ID),
				Source: model.PlanSourceInitial, Reason: "initial assignment (lowest cost)", PlannedAt: in.Now.UTC()}
			plan.DFSFallback = dfsFallback(in.Site, band, p.widths[ap.ID], best, func(c int) float64 { return p.cost(ap.ID, c, false) })
			result = append(result, Assignment{APID: ap.ID, Band: band, Plan: plan})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].APID != result[j].APID {
			return result[i].APID < result[j].APID
		}
		return result[i].Band < result[j].Band
	})
	return result
}
