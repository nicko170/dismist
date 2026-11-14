package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

const reviewKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"

// List elements whose optional members are empty (a blocked client
// without a note, an SSH key without a comment, a syslog target without a
// tag) are stored, not refused as unsupported: records drop those members.
func TestTypedListsWithoutOptionalMembers(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	ok := func(name string, response interface{ Result() *http.Response }, body string) {
		t.Helper()
		if code := response.Result().StatusCode; code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	block := f.call(t, http.MethodPost, "/api/sites/home/clients/02:bb:00:00:00:09/block", "")
	ok("block without a note", block, block.Body.String())
	noted := f.call(t, http.MethodPost, "/api/sites/home/clients/02:bb:00:00:00:0a/block", `{"note":"kitchen"}`)
	ok("block with a note", noted, noted.Body.String())
	unblock := f.call(t, http.MethodPost, "/api/sites/home/clients/02:bb:00:00:00:0a/unblock", "")
	ok("unblock next to an entry without a note", unblock, unblock.Body.String())
	site := findSite(f.store.Snapshot(), "home")
	if len(site.Protection.BlockedClients) != 1 || site.Protection.BlockedClients[0].MAC != "02-bb-00-00-00-09" {
		t.Fatalf("blocked %+v", site.Protection.BlockedClients)
	}
	keys := f.call(t, http.MethodPatch, "/api/sites/home", `{"ssh":{"enabled":true,"keys":["`+reviewKey+`"]}}`)
	ok("SSH key without a comment", keys, keys.Body.String())
	legacyKeys := f.call(t, http.MethodPut, "/api/sites/home/ssh-keys", `{"keys":["`+reviewKey+`","`+reviewKey+` with-comment"]}`)
	ok("legacy ssh-keys alias", legacyKeys, legacyKeys.Body.String())
	experimental := f.call(t, http.MethodPatch, "/api/sites/home", `{"experimental":true}`)
	ok("experimental", experimental, experimental.Body.String())
	syslog := f.call(t, http.MethodPatch, "/api/sites/home", `{"syslog":[{"protocol":"udp","host":"192.0.2.5","port":514,"facility":1,"severity":6,"tag":""}]}`)
	ok("syslog without a tag", syslog, syslog.Body.String())
}

// Turning the scanning radio off (on by default) is the experimental
// action; the gate sees it although false is a zero value.
func TestScanningRadioOffIsExperimental(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	errorOf(t, f.call(t, http.MethodPatch, "/api/sites/home?dry_run=1", `{"rf":{"scanning_radio":false}}`), http.StatusUnprocessableEntity, api.ErrorCodeExperimentalDisabled)
	errorOf(t, f.call(t, http.MethodPut, "/api/sites/home/rf?dry_run=1", `{"scanning_radio":false}`), http.StatusUnprocessableEntity, api.ErrorCodeExperimentalDisabled)
	if response := f.call(t, http.MethodPatch, "/api/sites/home?dry_run=1", `{"rf":{"scanning_radio":true}}`); response.Code != http.StatusOK {
		t.Fatalf("scanning on (the default): %d %s", response.Code, response.Body.String())
	}
}

// The RF view shows the template's TWT (on) where the record holds
// nothing, and sending the form back unchanged changes no AP.
func TestRFFormRoundTripChangesNothing(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	siteID := newManagedSite(t, f)
	rf := decodeInto[api.RFProfileView](t, f.call(t, http.MethodGet, "/api/sites/"+siteID+"/rf", ""), http.StatusOK)
	for name, band := range map[string]api.BandProfileView{"2.4": rf.Band24, "5": rf.Band5} {
		if !band.Advanced.TWT || band.Advanced.TAF != "default" || band.Advanced.Antenna != "default" {
			t.Fatalf("%s GHz advanced %+v", name, band.Advanced)
		}
	}
	body, _ := json.Marshal(map[string]any{
		"band_24": map[string]any{"enabled": true, "advanced": rf.Band24.Advanced},
		"band_5":  map[string]any{"enabled": true, "advanced": rf.Band5.Advanced},
	})
	out := decodeInto[api.MutationResponse[api.RFProfileView]](t, f.call(t, http.MethodPut, "/api/sites/"+siteID+"/rf?dry_run=1", string(body)), http.StatusOK)
	if len(out.Impact.APs) != 0 || out.Impact.Summary != "No AP changes" {
		t.Fatalf("unchanged RF form changed APs: %+v", out.Impact)
	}
	// Turning TWT off is a real change.
	off := decodeInto[api.MutationResponse[api.RFProfileView]](t, f.call(t, http.MethodPut, "/api/sites/"+siteID+"/rf?dry_run=1", `{"band_5":{"advanced":{"twt":false}}}`), http.StatusOK)
	if len(off.Impact.APs) != 1 {
		t.Fatalf("TWT off: %+v", off.Impact)
	}
}

// The impact line counts APs, not diff paths: a power change writes Power
// and RRM.Power on one AP, which is one radio restart on one AP.
func TestImpactCountsEachAPOnce(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	siteID := newManagedSite(t, f)
	out := decodeInto[api.MutationResponse[api.RFProfileView]](t, f.call(t, http.MethodPut, "/api/sites/"+siteID+"/rf?dry_run=1", `{"band_24":{"power":{"mode":"fixed","dbm":10}}}`), http.StatusOK)
	if len(out.Impact.APs) != 1 || len(out.Impact.APs[0].Diff) < 2 {
		t.Fatalf("impact %+v", out.Impact)
	}
	if !strings.Contains(out.Impact.Summary, "2.4 GHz radio restarts on 1 AP") {
		t.Fatalf("summary %q", out.Impact.Summary)
	}
}

// compat_raised is reported by the change that raises compat, not by every
// later change.
func TestCompatWarningOnlyWhenRaised(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	hasCompat := func(warnings []api.Warning) bool {
		for _, warning := range warnings {
			if warning.Code == "compat_raised" {
				return true
			}
		}
		return false
	}
	created := decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPost, "/api/sites/home/networks", `{"ssid":"Example-3","psk":"example-passphrase-3","security":"wpa3_personal"}`), http.StatusCreated)
	if !hasCompat(created.Warnings) {
		t.Fatalf("WPA3 WLAN: %+v", created.Warnings)
	}
	if info, _ := f.store.Schema(); info.Compat != 2 {
		t.Fatalf("compat %+v", info)
	}
	renamed := decodeInto[api.MutationResponse[api.SiteView]](t, f.call(t, http.MethodPatch, "/api/sites/home", `{"name":"Home 2"}`), http.StatusOK)
	if hasCompat(renamed.Warnings) {
		t.Fatalf("a rename warned about compat: %+v", renamed.Warnings)
	}
	dry := decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home?dry_run=1", `{"hidden":true}`), http.StatusOK)
	if hasCompat(dry.Warnings) {
		t.Fatalf("an unrelated dry run warned about compat: %+v", dry.Warnings)
	}
}

// Client notes are returned with the client (and survive a later alias
// edit in the UI, which sends both).
func TestClientDetailCarriesMeta(t *testing.T) {
	f := newV1Fixture(t)
	meta := &fakeClientMeta{}
	f.server.UseConfig(Config{ClientMeta: meta})
	decodeInto[api.ClientMeta](t, f.call(t, http.MethodPut, "/api/clients/02:bb:00:00:00:01/meta", `{"alias":"Kid iPad","notes":"blocked after 9pm"}`), http.StatusOK)
	detail := decodeInto[api.ClientDetail](t, f.call(t, http.MethodGet, "/api/clients/02-bb-00-00-00-01", ""), http.StatusOK)
	if detail.Meta == nil || detail.Meta.Alias != "Kid iPad" || detail.Meta.Notes != "blocked after 9pm" {
		t.Fatalf("meta %+v", detail.Meta)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/clients/02-bb-00-00-00-02", ""), http.StatusNotFound, api.ErrorCodeNotFound)
}

// ?ap= limits the neighbour page and the change history to one AP.
func TestNeighboursAndChangesFilterByAP(t *testing.T) {
	f := newV1Fixture(t)
	const thirdAP = "02-aa-00-00-00-03"
	if err := f.store.Update(func(state *model.State) error {
		state.APs = append(state.APs, model.APRecord{ID: thirdAP, Name: "Study", SiteID: "home", Secret: []byte(strings.Repeat("\x03", 32))})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	all := decodeInto[api.NeighbourPage](t, f.call(t, http.MethodGet, "/api/sites/home/rf/neighbours", ""), http.StatusOK)
	one := decodeInto[api.NeighbourPage](t, f.call(t, http.MethodGet, "/api/sites/home/rf/neighbours?ap=02:AA:00:00:00:03", ""), http.StatusOK)
	if len(all.Scans) != 4 || len(one.Scans) != 2 || one.Scans[0].APID != thirdAP || one.Scans[1].APID != thirdAP {
		t.Fatalf("scans all %+v one %+v", all.Scans, one.Scans)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/home/rf/neighbours?ap="+otherAP, ""), http.StatusNotFound, api.ErrorCodeNotFound)

	history := &fakeHistory{}
	f.server.UseConfig(Config{History: history})
	for revision, effective := range []map[string]EffectiveEntry{
		{homeAP: {Hash: "a", Revision: 1}},
		{thirdAP: {Hash: "b", Revision: 2}},
		{homeAP: {Hash: "c", Revision: 3}, thirdAP: {Hash: "d", Revision: 3}},
		nil,
	} {
		history.entries = append(history.entries, ChangeEntry{Revision: uint64(revision + 1), Kind: "config", SiteID: "home", Effective: effective})
	}
	history.entries = append(history.entries, ChangeEntry{Revision: 5, Kind: "config", SiteID: "home",
		Objects: []api.ChangeObject{{Kind: "ap", ID: homeAP, Name: "Hallway"}}})
	revisions := func(page api.ChangePage) []uint64 {
		var result []uint64
		for _, change := range page.Changes {
			result = append(result, change.Revision)
		}
		return result
	}
	page := decodeInto[api.ChangePage](t, f.call(t, http.MethodGet, "/api/sites/home/changes?ap="+homeAP, ""), http.StatusOK)
	if got := revisions(page); len(got) != 3 || got[0] != 5 || got[1] != 3 || got[2] != 1 || page.NextBefore != nil {
		t.Fatalf("AP history %v next %v", got, page.NextBefore)
	}
	first := decodeInto[api.ChangePage](t, f.call(t, http.MethodGet, "/api/sites/home/changes?ap="+thirdAP+"&limit=1", ""), http.StatusOK)
	if got := revisions(first); len(got) != 1 || got[0] != 3 || first.NextBefore == nil || *first.NextBefore != 3 {
		t.Fatalf("first page %v next %v", got, first.NextBefore)
	}
	second := decodeInto[api.ChangePage](t, f.call(t, http.MethodGet, "/api/sites/home/changes?ap="+thirdAP+"&limit=1&before=3", ""), http.StatusOK)
	if got := revisions(second); len(got) != 1 || got[0] != 2 {
		t.Fatalf("second page %v", got)
	}
	if everything := decodeInto[api.ChangePage](t, f.call(t, http.MethodGet, "/api/sites/home/changes", ""), http.StatusOK); len(everything.Changes) != 5 {
		t.Fatalf("site history %v", revisions(everything))
	}
}

// The legacy app is not served once typed settings are on.
func TestLegacyUIOnlyInLegacyMode(t *testing.T) {
	f := newV1Fixture(t)
	if response := f.call(t, http.MethodGet, "/legacy/", ""); !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("legacy mode: %d", response.Code)
	}
	f.committed()
	if response := f.call(t, http.MethodGet, "/legacy/", ""); strings.Contains(response.Body.String(), `<div id="root"></div>`) && !strings.Contains(response.Body.String(), "_ui") {
		t.Fatalf("typed mode still serves the legacy app: %s", response.Body.String())
	}
}

// Global search finds offline clients by alias and APs by serial.
func TestSearchOfflineAliasesAndSerials(t *testing.T) {
	f := newV1Fixture(t)
	meta := &fakeClientMeta{}
	f.server.UseConfig(Config{ClientMeta: meta, LastKnown: func(apID string) (any, bool) {
		if apID != homeAP {
			return nil, false
		}
		return map[string]any{"hello": map[string]any{"serial": "A0700EXAMPLE1"}}, true
	}})
	if err := meta.Put(api.ClientMeta{MAC: "02-bb-00-00-00-07", Alias: "Work MBP"}); err != nil {
		t.Fatal(err)
	}
	hits := decodeInto[api.SearchResult](t, f.call(t, http.MethodGet, "/api/search?q=work", ""), http.StatusOK).Hits
	if len(hits) != 1 || hits[0].Kind != "client" || hits[0].ID != "02-bb-00-00-00-07" || hits[0].Title != "Work MBP" || hits[0].Detail != "offline" {
		t.Fatalf("alias hits %+v", hits)
	}
	hits = decodeInto[api.SearchResult](t, f.call(t, http.MethodGet, "/api/search?q=a0700example", ""), http.StatusOK).Hits
	if len(hits) != 1 || hits[0].Kind != "ap" || hits[0].ID != homeAP {
		t.Fatalf("serial hits %+v", hits)
	}
}

// System › Database shows record counts and the revision from which a
// rollback needs a restore.
func TestSystemDatabaseCountsAndCompatRevision(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	view := decodeInto[api.SystemView](t, f.call(t, http.MethodGet, "/api/system", ""), http.StatusOK)
	if view.DB.Records["sites"] != 2 || view.DB.Records["aps"] != 2 || view.DB.CompatRaisedAtRevision != nil {
		t.Fatalf("db %+v", view.DB)
	}
	created := decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPost, "/api/sites/home/networks", `{"ssid":"Example-3","psk":"example-passphrase-3","security":"wpa3_personal"}`), http.StatusCreated)
	view = decodeInto[api.SystemView](t, f.call(t, http.MethodGet, "/api/system", ""), http.StatusOK)
	if view.DB.CompatRaisedAtRevision == nil || *view.DB.CompatRaisedAtRevision != created.Revision || view.DB.RollbackWithoutRestore {
		t.Fatalf("compat raised at %v, want %d", view.DB.CompatRaisedAtRevision, created.Revision)
	}
}

// The BLE indicator: null without field 38, alive when the counters show
// activity.
func TestBLEIndicator(t *testing.T) {
	if bleView(apObs{}) != nil {
		t.Fatal("no BLE radio reported")
	}
	quiet := bleView(apObs{telemetry: obsTelemetry{BLE: []obsBLE{{}}}})
	if quiet == nil || quiet.Alive {
		t.Fatalf("quiet radio %+v", quiet)
	}
	alive := bleView(apObs{telemetry: obsTelemetry{BLE: []obsBLE{{TxAdverts: 40, Scans: 2, TxErrors: 1}}}})
	if alive == nil || !alive.Alive || alive.TxAdverts != 40 || alive.Errors != 1 {
		t.Fatalf("alive radio %+v", alive)
	}
}

// Ranges longer than the 30-day minute retention read the hourly rollups.
func TestLongSampleRangesReadHourlyRollups(t *testing.T) {
	f := newV1Fixture(t)
	now := time.Now().UTC()
	old := now.Add(-60 * 24 * time.Hour).Truncate(time.Hour)
	for minute := 0; minute < 3; minute++ {
		observation := model.Observation{SiteID: "home", APID: homeAP, At: old.Add(time.Duration(minute) * time.Minute), Connected: true,
			Clients: []model.ClientSnapshot{{MAC: "02:bb:00:00:00:01", RSSI: -50}, {MAC: "02:bb:00:00:00:02", RSSI: -60}, {MAC: "02:bb:00:00:00:03", RSSI: -55}},
			Radios:  []model.RadioSample{{Band: "5", Channel: 149, Width: 80, UtilAll: 0.25, Clients: 3}}}
		if err := f.store.RecordObservation(observation); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.RollupSamples(t.Context(), now, 0); err != nil {
		t.Fatal(err)
	}
	page := decodeInto[api.SamplePage](t, f.call(t, http.MethodGet, "/api/sites/home/history/samples?range=90d&step=24h", ""), http.StatusOK)
	if page.Step != "24h" || len(page.Samples) != 1 || page.Samples[0].Clients != 3 || len(page.Samples[0].Radios) != 1 ||
		page.Samples[0].Radios[0].Channel != 149 || page.Samples[0].Radios[0].Util != 0.25 {
		t.Fatalf("hourly page %+v", page)
	}
	defaults := decodeInto[api.SamplePage](t, f.call(t, http.MethodGet, "/api/sites/home/history/samples?range=90d", ""), http.StatusOK)
	if defaults.Step != "1h0m0s" || len(defaults.Samples) != 1 {
		t.Fatalf("default step %+v", defaults)
	}
}

// The expert editor's vocabulary comes from the firmware path list and the
// server's forbidden patterns.
func TestExpertVocabulary(t *testing.T) {
	f := newV1Fixture(t)
	wlan := decodeInto[api.ExpertVocabulary](t, f.call(t, http.MethodGet, "/api/options/expert?scope=wlan", ""), http.StatusOK)
	has := func(list []string, want string) bool {
		for _, item := range list {
			if item == want {
				return true
			}
		}
		return false
	}
	if !has(wlan.Paths, "Hidden") || !has(wlan.Paths, "Auth.AKM.WPA3_PSK") || has(wlan.Paths, "Radios") || !has(wlan.Forbidden, "SSID") || !has(wlan.Forbidden, "Wxlan.Portal*") {
		t.Fatalf("wlan vocabulary: %d paths, forbidden %v", len(wlan.Paths), wlan.Forbidden)
	}
	site := decodeInto[api.ExpertVocabulary](t, f.call(t, http.MethodGet, "/api/options/expert?scope=site", ""), http.StatusOK)
	if !has(site.Paths, "Monitor.NbrScan") && !has(site.Paths, "Monitor.Enabled") || has(site.Paths, "Hidden") || !has(site.Forbidden, "WLANs") {
		t.Fatalf("site vocabulary: %d paths, forbidden %v", len(site.Paths), site.Forbidden)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/options/expert?scope=nope", ""), http.StatusBadRequest, api.ErrorCodeInvalid)
}
