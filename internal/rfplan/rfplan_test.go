package rfplan

import (
	"reflect"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

var at = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func managedSite() model.Site {
	return model.Site{ID: "site", Name: "Example", Country: "AU", RF: model.RFProfile{
		Managed: true,
		Band24:  model.BandProfile{Enabled: true, Width: 20, Channels: []int{1, 6, 11}, Power: model.PowerPolicy{Mode: model.PowerAuto, Min: 8, Max: 15}},
		Band5:   model.BandProfile{Enabled: true, Width: 80, Channels: []int{36, 40, 44, 48, 149, 153, 157, 161}, Power: model.PowerPolicy{Mode: model.PowerAuto, Min: 12, Max: 20}},
	}}
}

func TestAllowedChannelsPerBlockAndDFS(t *testing.T) {
	site := managedSite()
	if got := AllowedChannels(site, "5", 80); !reflect.DeepEqual(got, []int{36, 149}) {
		t.Fatalf("80 MHz %v", got)
	}
	if got := AllowedChannels(site, "5", 20); len(got) != 8 {
		t.Fatalf("20 MHz %v", got)
	}
	site.RF.Band5.Channels = []int{52, 100, 36}
	if got := AllowedChannels(site, "5", 80); !reflect.DeepEqual(got, []int{36}) {
		t.Fatalf("DFS off %v", got)
	}
	site.RF.Band5.DFS = true
	if got := AllowedChannels(site, "5", 80); !reflect.DeepEqual(got, []int{36, 52, 100}) {
		t.Fatalf("DFS on %v", got)
	}
	if got := AllowedChannels(site, "5", 160); got != nil {
		t.Fatalf("160 MHz is not available under AU/890: %v", got)
	}
}

func TestInitialSpreadsAndNeverMovesValidPlans(t *testing.T) {
	site := managedSite()
	// The live AP: static channels on both bands (lifted by the migration).
	live := model.APRecord{ID: "02-aa-00-00-00-37", SiteID: "site", RF: map[string]*model.RadioOverride{"5": {Channel: 149}, "2.4": {Channel: 1}}}
	second := model.APRecord{ID: "02-aa-00-00-00-38", SiteID: "site"}
	assignments := Initial(site, []model.APRecord{live, second}, at)
	if len(assignments) != 2 {
		t.Fatalf("assignments %+v", assignments)
	}
	byBand := map[string]model.PlannedRadio{}
	for _, a := range assignments {
		if a.APID != second.ID {
			t.Fatalf("static AP assigned: %+v", a)
		}
		byBand[a.Band] = a.Plan
	}
	if byBand["5"].Channel != 36 || byBand["5"].Width != 80 || byBand["5"].PowerDBm != 20 {
		t.Fatalf("5 GHz %+v (149 block is used by the live AP)", byBand["5"])
	}
	if byBand["2.4"].Channel != 6 || byBand["2.4"].PowerDBm != 15 {
		t.Fatalf("2.4 GHz %+v", byBand["2.4"])
	}
	if second.RFPlan != nil {
		t.Fatal("Initial mutated the caller's record")
	}
	// A third AP does not move the second.
	second.RFPlan = map[string]model.PlannedRadio{"5": byBand["5"], "2.4": byBand["2.4"]}
	third := model.APRecord{ID: "02-aa-00-00-00-01", SiteID: "site"}
	again := Initial(site, []model.APRecord{live, second, third}, at)
	for _, a := range again {
		if a.APID != third.ID {
			t.Fatalf("moved %s", a.APID)
		}
	}
	// An invalid plan (width changed) is replaced.
	site.RF.Band5.Width = 40
	replaced := Initial(site, []model.APRecord{live, second}, at)
	if len(replaced) != 1 || replaced[0].Band != "5" || replaced[0].Plan.Width != 40 {
		t.Fatalf("replaced %+v", replaced)
	}
}

func TestFallbackIsDeterministicAndNeverZero(t *testing.T) {
	site := managedSite()
	ap := model.APRecord{ID: "02-aa-00-00-00-99"}
	first, ok := Fallback(site, ap, "5")
	second, _ := Fallback(site, ap, "5")
	if !ok || first != second || first.Channel == 0 || first.PowerDBm != 20 {
		t.Fatalf("fallback %+v %+v", first, second)
	}
	site.RF.Band5.Power = model.PowerPolicy{Mode: model.PowerFixed, DBm: 17}
	if plan, _ := Fallback(site, ap, "5"); plan.PowerDBm != 17 {
		t.Fatalf("fixed power %+v", plan)
	}
}

func TestValidPlan(t *testing.T) {
	site := managedSite()
	ap := model.APRecord{ID: "x"}
	good := model.PlannedRadio{Channel: 153, Width: 80, PowerDBm: 15}
	if !ValidPlan(site, ap, "5", good) {
		t.Fatal("149 block plan refused")
	}
	for _, bad := range []model.PlannedRadio{
		{Channel: 52, Width: 80, PowerDBm: 15}, // DFS while off
		{Channel: 36, Width: 40, PowerDBm: 15}, // width
		{Channel: 36, Width: 80, PowerDBm: 0},  // power 0
		{Channel: 116, Width: 80, PowerDBm: 15},
	} {
		if ValidPlan(site, ap, "5", bad) {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func intp(v int) *int { return &v }

func scanWith(siteID string, band string, neighbours ...model.ScanNeighbour) model.APScan {
	return model.APScan{SiteID: siteID, Bands: map[string]model.BandScan{band: {At: at, Neighbours: neighbours}}}
}

func TestPlannerSingleAPNoScansRoundRobin(t *testing.T) {
	site := managedSite()
	aps := []model.APRecord{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	result := Plan(Inputs{Site: site, APs: aps, Now: at})
	if !result.NoScans {
		t.Fatal("expected no-scan mode")
	}
	channels := map[string]int{}
	for _, change := range result.Changes {
		if change.Band == "2.4" {
			channels[change.APID] = change.To.Channel
		}
	}
	if !reflect.DeepEqual(channels, map[string]int{"a": 1, "b": 6, "c": 11}) {
		t.Fatalf("round robin %v", channels)
	}
}

func TestPlannerAvoidsBusyNeighbourAndEachOther(t *testing.T) {
	site := managedSite()
	aps := []model.APRecord{{ID: "a"}, {ID: "b"}}
	own := map[string][]string{"a": {"02:aa:00:00:00:a1"}, "b": {"02:aa:00:00:00:b1"}}
	util := 0.6
	scans := map[string]model.APScan{
		"a": scanWith("site", "2.4",
			model.ScanNeighbour{BSSID: "02:aa:00:00:00:b1", Channel: 6, RSSI: -50},
			model.ScanNeighbour{BSSID: "02:bb:00:00:00:01", Channel: 1, RSSI: -45, Util: &util}),
		"b": scanWith("site", "2.4", model.ScanNeighbour{BSSID: "02:aa:00:00:00:a1", Channel: 1, RSSI: -52}),
	}
	result := Plan(Inputs{Site: site, APs: aps, Scans: scans, OwnBSSIDs: own, Now: at})
	got := map[string]int{}
	power := map[string]int{}
	for _, change := range result.Changes {
		if change.Band == "2.4" {
			got[change.APID] = change.To.Channel
			power[change.APID] = change.To.PowerDBm
		}
	}
	if got["a"] == 1 || got["a"] == got["b"] {
		t.Fatalf("channels %v (a must avoid the busy channel 1 and b)", got)
	}
	// They hear each other above -60 dBm: 3 dB below the max.
	if power["a"] != 12 || power["b"] != 12 {
		t.Fatalf("power %v", power)
	}
	// Determinism.
	if again := Plan(Inputs{Site: site, APs: aps, Scans: scans, OwnBSSIDs: own, Now: at}); !reflect.DeepEqual(again, result) {
		t.Fatal("plan is not deterministic")
	}
}

func TestPlannerKeepsStaticAndHysteresis(t *testing.T) {
	site := managedSite()
	static := model.APRecord{ID: "s", RF: map[string]*model.RadioOverride{"2.4": {Channel: 11}}}
	auto := model.APRecord{ID: "a", RFPlan: map[string]model.PlannedRadio{"2.4": {Channel: 1, Width: 20, PowerDBm: 15}}}
	// A weak neighbour on 1 and a slightly weaker one on 6: moving gains
	// less than 25 %, so the AP keeps channel 1.
	scans := map[string]model.APScan{"a": scanWith("site", "2.4",
		model.ScanNeighbour{BSSID: "02:bb:00:00:00:01", Channel: 1, RSSI: -88},
		model.ScanNeighbour{BSSID: "02:bb:00:00:00:02", Channel: 6, RSSI: -89})}
	result := Plan(Inputs{Site: site, APs: []model.APRecord{static, auto}, Scans: scans, Now: at})
	for _, change := range result.Changes {
		if change.APID == "s" && change.Band == "2.4" && (!change.Static || change.To.Channel != 11) {
			t.Fatalf("static moved %+v", change)
		}
		if change.APID == "a" && change.Band == "2.4" && (!change.Keep || change.To.Channel != 1 || change.Disruption != "none") {
			t.Fatalf("hysteresis %+v", change)
		}
	}
}

func TestPlannerDFSFallback(t *testing.T) {
	site := managedSite()
	site.RF.Band5.DFS = true
	site.RF.Band5.Channels = []int{52, 36}
	busy := 0.9
	scans := map[string]model.APScan{"a": scanWith("site", "5",
		model.ScanNeighbour{BSSID: "02:bb:00:00:00:01", Channel: 36, Width: 80, RSSI: -40, Util: &busy})}
	result := Plan(Inputs{Site: site, APs: []model.APRecord{{ID: "a"}}, Scans: scans, Now: at})
	for _, change := range result.Changes {
		if change.Band == "5" {
			if change.To.Channel != 52 || change.To.DFSFallback == 0 {
				t.Fatalf("DFS choice %+v", change)
			}
			if dfs, _ := (func() (bool, bool) { d, _ := Domain(site); return d.Valid("5", 80, change.To.DFSFallback) })(); dfs {
				t.Fatalf("fallback %d is DFS", change.To.DFSFallback)
			}
		}
	}
	// DFS off: never a DFS channel.
	site.RF.Band5.DFS = false
	result = Plan(Inputs{Site: site, APs: []model.APRecord{{ID: "a"}}, Scans: scans, Now: at})
	for _, change := range result.Changes {
		if change.Band == "5" && change.To.Channel != 36 {
			t.Fatalf("DFS off chose %d", change.To.Channel)
		}
	}
	_ = intp
}

func TestApplyWritesPlans(t *testing.T) {
	site := managedSite()
	state := model.State{Sites: []model.Site{site}, APs: []model.APRecord{{ID: "a", SiteID: "site"}}}
	result := Plan(Inputs{Site: site, APs: state.APs, Now: at})
	Apply(&state, result)
	if state.APs[0].RFPlan["5"].Channel == 0 || state.APs[0].RFPlan["2.4"].Source != model.PlanSourcePlanner {
		t.Fatalf("plans %+v", state.APs[0].RFPlan)
	}
	if got := ApplyInitial(&state, at); len(got) != 0 {
		t.Fatalf("valid plans re-assigned: %+v", got)
	}
}

func TestInitialWithScansAvoidsHeardAPs(t *testing.T) {
	site := managedSite()
	placed := model.APRecord{ID: "a", RFPlan: map[string]model.PlannedRadio{"2.4": {Channel: 1, Width: 20, PowerDBm: 15}, "5": {Channel: 36, Width: 80, PowerDBm: 20}}}
	joining := model.APRecord{ID: "b"}
	scans := map[string]model.APScan{"b": scanWith("site", "2.4",
		model.ScanNeighbour{BSSID: "02:aa:00:00:00:a1", Channel: 1, RSSI: -45},
		model.ScanNeighbour{BSSID: "02:bb:00:00:00:01", Channel: 6, RSSI: -50})}
	got := InitialWithScans(Inputs{Site: site, APs: []model.APRecord{placed, joining}, Scans: scans,
		OwnBSSIDs: map[string][]string{"a": {"02:aa:00:00:00:a1"}}, Now: at})
	for _, a := range got {
		if a.APID != "b" {
			t.Fatalf("moved %s", a.APID)
		}
		if a.Band == "2.4" && a.Plan.Channel != 11 {
			t.Fatalf("2.4 GHz %+v", a.Plan)
		}
	}
	if len(got) != 2 {
		t.Fatalf("assignments %+v", got)
	}
}
