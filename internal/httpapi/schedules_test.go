package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// typed switches the fixture to schema 2 for real (the typed compile); the
// fixture's own sites have non-UUID IDs, so typed tests use a new site.
func (f *v1Fixture) typed(t *testing.T) {
	t.Helper()
	f.committed()
	if err := f.store.UpdateTx(func(_ *model.State, tx *model.Tx) error { return tx.SetSchema(2) }); err != nil {
		t.Fatal(err)
	}
	if !f.store.Snapshot().Typed() {
		t.Fatal("state is not typed")
	}
}

// newManagedSite creates a v1 site (managed RF under AU/890) in typed mode
// and moves the fixture's second AP onto it.
func newManagedSite(t *testing.T, f *v1Fixture) string {
	t.Helper()
	f.typed(t)
	created := decodeInto[api.MutationResponse[api.SiteView]](t, f.call(t, http.MethodPost, "/api/sites", `{"name":"Lab","country":"AU","timezone":"Australia/Sydney"}`), http.StatusCreated)
	if response := f.call(t, http.MethodPut, "/api/aps/"+otherAP+"/site", `{"site_id":"`+created.Object.ID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("move AP: %d %s", response.Code, response.Body.String())
	}
	return created.Object.ID
}

// WLAN schedules are evaluated by the minute ticker, stored as off_air
// through a normal change (revision bump), and only for APs that take
// notify pushes; the compiled config never reads the clock.
func TestScheduleEvaluator(t *testing.T) {
	f := newV1Fixture(t)
	siteID := newManagedSite(t, f)
	sydney, _ := time.LoadLocation("Australia/Sydney")
	clock := time.Date(2026, 11, 16, 10, 0, 0, 0, sydney) // a Monday
	f.server.now = func() time.Time { return clock.UTC() }
	if response := f.call(t, http.MethodPatch, "/api/sites/"+siteID, `{"experimental":true}`); response.Code != http.StatusOK {
		t.Fatalf("experimental: %s", response.Body.String())
	}
	created := decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPost, "/api/sites/"+siteID+"/networks", `{"ssid":"Guest","psk":"example-passphrase-5"}`), http.StatusCreated)
	wlanPath := "/api/sites/" + siteID + "/networks/" + created.Object.ID
	response := f.call(t, http.MethodPatch, wlanPath,
		`{"schedule":{"enabled":true,"windows":[{"day":"mon","start":"09:00","end":"17:00"}],"off_air":true}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("schedule: %d %s", response.Code, response.Body.String())
	}
	network := func() model.Network {
		return *findNetwork(f.store.Snapshot(), siteID, created.Object.ID)
	}
	if network().Schedule.OffAir {
		t.Fatal("off_air is read-only")
	}
	revision := func() uint64 { return findAP(f.store.Snapshot(), otherAP).ConfigRevision }

	// Reconnect push mode: the schedule is not applied.
	clock = time.Date(2026, 11, 16, 17, 0, 0, 0, sydney)
	before := revision()
	f.server.evaluateSchedules()
	if network().Schedule.OffAir || revision() != before {
		t.Fatal("a schedule went off air for an AP without notify pushes")
	}

	// Notify: 17:00 takes the WLAN off air with a revision bump.
	f.controls[otherAP].SetFeatures(controller.Features(0).With(controller.FeatureConfigChangeNotify))
	f.server.evaluateSchedules()
	if !network().Schedule.OffAir || revision() == before {
		t.Fatalf("17:00: off_air %v revision %d -> %d", network().Schedule.OffAir, before, revision())
	}
	view := decodeInto[api.WLANView](t, f.call(t, http.MethodGet, wlanPath, ""), http.StatusOK)
	if view.Schedule == nil || !view.Schedule.OffAir {
		t.Fatalf("view schedule %+v", view.Schedule)
	}
	config := decodeInto[api.APConfigView](t, f.call(t, http.MethodGet, "/api/aps/"+otherAP+"/config", ""), http.StatusOK)
	if raw, _ := json.Marshal(config.Config); !strings.Contains(string(raw), `"Reason":8`) {
		t.Fatalf("compiled config does not disable the BSS: %s", config.Config)
	}
	// Nothing changes until the next window opens; a form save that sends
	// the schedule back does not clear the stored state.
	offRevision := revision()
	clock = time.Date(2026, 11, 17, 12, 0, 0, 0, sydney)
	f.server.evaluateSchedules()
	f.call(t, http.MethodPatch, wlanPath, `{"schedule":{"enabled":true,"windows":[{"day":"mon","start":"09:00","end":"17:00"}],"off_air":false}}`)
	if !network().Schedule.OffAir || revision() != offRevision {
		t.Fatal("the schedule changed outside a window edge")
	}
	clock = time.Date(2026, 11, 23, 9, 0, 0, 0, sydney)
	f.server.evaluateSchedules()
	if network().Schedule.OffAir || revision() == offRevision {
		t.Fatal("09:00 Monday did not bring the WLAN back on air")
	}
	// Switching the schedule off clears a stale state without an AP change.
	clock = time.Date(2026, 11, 23, 18, 0, 0, 0, sydney)
	f.server.evaluateSchedules()
	f.call(t, http.MethodPatch, wlanPath, `{"schedule":{"enabled":false}}`)
	disabled := revision()
	f.server.evaluateSchedules()
	if network().Schedule.OffAir {
		t.Fatal("a switched-off schedule kept its off-air state")
	}
	if revision() != disabled {
		t.Fatal("clearing a switched-off schedule's state changed the AP")
	}
}
