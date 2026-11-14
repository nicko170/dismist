package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/httpapi"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

func adapterStore(t *testing.T) *model.BoltStore {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Update(func(state *model.State) error {
		site := model.Site{ID: "home", Name: "Home", Country: "AU", Timezone: "Australia/Sydney"}
		compile.NewSiteDefaults(&site)
		state.Sites = []model.Site{site}
		state.Networks = []model.Network{{ID: "net-1", SiteID: "home", SSID: "Example", PSK: "example-passphrase-1", VLANID: 1, Enabled: true}}
		state.APs = []model.APRecord{{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "home", ConfigRevision: 3, Secret: make([]byte, model.SecretSize)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

// The history adapter stores the API's entries in config_history with the
// site snapshot, reads them back with names, and maps an AP's config
// revision to its entry when the result arrives.
func TestHistoryLogRoundTrip(t *testing.T) {
	store := adapterStore(t)
	history := historyLog{store: store}
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	for _, entry := range []httpapi.ChangeEntry{
		{Revision: 5, At: at, Summary: "Created WLAN Example", SiteID: "home", Actor: api.ChangeActor{Kind: api.ActorKindSession, Name: "admin"},
			Objects:   []api.ChangeObject{{Kind: "network", ID: "net-1"}},
			Effective: map[string]httpapi.EffectiveEntry{"02-aa-00-00-00-01": {Hash: "e2:aa", Revision: 3}},
			Results:   map[string]api.APState{"02-aa-00-00-00-01": {State: "pending"}}},
		{Revision: 6, At: at.Add(time.Minute), Summary: "Hidden on", SiteID: "home",
			Effective: map[string]httpapi.EffectiveEntry{"02-aa-00-00-00-01": {Hash: "e2:bb", Revision: 4}},
			Results:   map[string]api.APState{"02-aa-00-00-00-01": {State: "pending"}}},
	} {
		if err := store.UpdateTx(func(state *model.State, tx *model.Tx) error { return history.Append(tx, *state, entry) }); err != nil {
			t.Fatal(err)
		}
	}
	if err := history.AppendAction(httpapi.ChangeEntry{At: at, Summary: "Located AP Hallway", SiteID: "home"}); err != nil {
		t.Fatal(err)
	}
	entries, _, err := history.List("home", 0, 10)
	if err != nil || len(entries) != 3 {
		t.Fatalf("list %+v %v", entries, err)
	}
	entry, err := history.Get(5)
	if err != nil || len(entry.Snapshot) == 0 || len(entry.Objects) != 1 || entry.Objects[0].Name != "Example" {
		t.Fatalf("get %+v %v", entry, err)
	}
	if bytesContains(entry.Snapshot, "example-passphrase-1") {
		t.Fatal("the snapshot holds the passphrase")
	}
	if _, err := history.Get(99); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	// The AP applied config revision 4 (entry 6); revision 3 (entry 5) is
	// superseded.
	if err := history.SetResult(4, "02-aa-00-00-00-01", api.APState{State: "applied"}); err != nil {
		t.Fatal(err)
	}
	five, _ := history.Get(5)
	six, _ := history.Get(6)
	if five.Results["02-aa-00-00-00-01"].State != "superseded" || six.Results["02-aa-00-00-00-01"].State != "applied" {
		t.Fatalf("results %+v %+v", five.Results, six.Results)
	}
}

func bytesContains(raw json.RawMessage, text string) bool {
	return strings.Contains(string(raw), text)
}

// The planner adapter proposes from the stored scans and writes only the
// chosen changes; the expert adapter folds keys and refuses forbidden
// paths; neighbour scans land in the scans bucket.
func TestPlannerExpertAndScanAdapters(t *testing.T) {
	store := adapterStore(t)
	storeScan(store, func(string) string { return "home" })("02-aa-00-00-00-01", time.Now(), telemetry.Scan{
		Band5: []telemetry.ScanNeighbor{{SSID: "Neighbour", BSSID: "02:BB:00:00:00:01", RSSI: -60, Channel: 36, WidthMHz: 80}},
	})
	scan, found, err := store.Scans().Get("02-aa-00-00-00-01")
	if err != nil || !found || len(scan.Bands["5"].Neighbours) != 1 || scan.Bands["5"].Neighbours[0].BSSID != "02:bb:00:00:00:01" {
		t.Fatalf("scan %+v %v %v", scan, found, err)
	}
	p := planner{store: store}
	state := store.Snapshot()
	if err := p.Initial(&state, []string{"home"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	proposal, err := p.Propose(httpapi.PlanInput{State: state, SiteID: "home", Now: time.Now()})
	if err != nil || proposal.Inputs.APs != 1 || len(proposal.Changes) != 2 {
		t.Fatalf("proposal %+v %v", proposal, err)
	}
	var change api.PlanChange
	for _, candidate := range proposal.Changes {
		if candidate.Band == api.Band5 {
			change = candidate
		}
	}
	change.To.Channel = 149
	if err := p.Apply(&state, "home", []api.PlanChange{change}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if plan := state.APs[0].RFPlan["5"]; plan.Channel != 149 || plan.Source != model.PlanSourcePlanner {
		t.Fatalf("plan %+v", state.APs[0].RFPlan)
	}
	stored, ignored, err := expertPolicy{}.Check(api.ExpertScopeSite, []byte(`{"led":{"brightness":10}}`), false)
	if err != nil || string(stored) != `{"LED":{"Brightness":10}}` || len(ignored) != 0 {
		t.Fatalf("fold %s %v %v", stored, ignored, err)
	}
	if _, _, err := (expertPolicy{}).Check(api.ExpertScopeSite, []byte(`{"WLANs":[]}`), false); err == nil {
		t.Fatal("a forbidden path was accepted")
	} else {
		var expert *httpapi.ExpertError
		if !errors.As(err, &expert) || expert.Code != api.ErrorCodeForbiddenPath {
			t.Fatalf("forbidden: %v", err)
		}
	}
	legacy, _, err := expertPolicy{}.Check(api.ExpertScopeSite, []byte(`{"led":{"brightness":10}}`), true)
	if err != nil || string(legacy) != `{"led":{"brightness":10}}` {
		t.Fatalf("legacy patches are stored as written: %s %v", legacy, err)
	}
}
