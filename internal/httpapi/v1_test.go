package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/webgate"
)

func TestV1ListShapesAndRouting(t *testing.T) {
	f := newV1Fixture(t)
	sites := decodeInto[[]api.SiteSummary](t, f.call(t, http.MethodGet, "/api/sites", ""), http.StatusOK)
	if len(sites) != 2 || sites[0].ID != "home" || sites[0].Counts.APs != 1 || sites[0].Counts.WLANs != 1 {
		t.Fatalf("sites: %+v", sites)
	}
	aps := decodeInto[[]api.APView](t, f.call(t, http.MethodGet, "/api/aps?site=home", ""), http.StatusOK)
	if len(aps) != 1 || aps[0].ID != homeAP || aps[0].Status != api.APStatusNeverConnected || !aps[0].HasSecret || aps[0].Flags == nil {
		t.Fatalf("aps: %+v", aps)
	}
	all := decodeInto[[]api.APView](t, f.call(t, http.MethodGet, "/api/aps", ""), http.StatusOK)
	if len(all) != 2 {
		t.Fatalf("all aps: %d", len(all))
	}
	detail := decodeInto[api.APDetail](t, f.call(t, http.MethodGet, "/api/aps/"+homeAP, ""), http.StatusOK)
	if detail.Config.EffectiveHash == "" || detail.Commands.Reboot || !strings.Contains(detail.Commands.Fallback, "dismist ap33 ssh") {
		t.Fatalf("detail: %+v", detail)
	}
	if _, ok := detail.ProtocolFeatures[FeatureAPCmd]; !ok || detail.RFPlan == nil || detail.Overrides.RF == nil {
		t.Fatalf("detail features/overrides: %+v %+v", detail.ProtocolFeatures, detail.Overrides)
	}
	site := decodeInto[api.SiteView](t, f.call(t, http.MethodGet, "/api/sites/home", ""), http.StatusOK)
	if site.CCRev == nil || *site.CCRev != "AU/890" || site.Typed || site.Expert.Field != "advanced" || !site.Expert.HasPatch {
		t.Fatalf("site view: %+v", site)
	}
	if pending := decodeInto[[]api.PendingAP](t, f.call(t, http.MethodGet, "/api/aps/pending", ""), http.StatusOK); len(pending) != 0 {
		t.Fatalf("pending: %+v", pending)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/nope", ""), http.StatusNotFound, api.ErrorCodeNotFound)
	notAllowed := f.call(t, http.MethodPost, "/api/aps/"+homeAP, "{}")
	errorOf(t, notAllowed, http.StatusMethodNotAllowed, api.ErrorCodeInvalid)
	if allow := notAllowed.Header().Get("Allow"); !strings.Contains(allow, "PATCH") || !strings.Contains(allow, "DELETE") {
		t.Fatalf("Allow: %q", allow)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/aps/02-aa-00-00-00-99", ""), http.StatusNotFound, api.ErrorCodeNotFound)
	telemetry := decodeInto[api.APTelemetry](t, f.call(t, http.MethodGet, "/api/aps/"+homeAP+"/telemetry", ""), http.StatusOK)
	if telemetry.APID != homeAP || telemetry.Radios == nil || telemetry.LastFull != nil {
		t.Fatalf("telemetry: %+v", telemetry)
	}
	if clients := decodeInto[[]api.ClientView](t, f.call(t, http.MethodGet, "/api/sites/home/clients", ""), http.StatusOK); len(clients) != 0 {
		t.Fatalf("clients: %+v", clients)
	}
	if recent := decodeInto[[]api.SessionView](t, f.call(t, http.MethodGet, "/api/sites/home/clients?view=recent", ""), http.StatusOK); len(recent) != 0 {
		t.Fatalf("recent: %+v", recent)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/home/clients?view=bogus", ""), http.StatusBadRequest, api.ErrorCodeInvalid)
}

func TestAPStatusPrecedence(t *testing.T) {
	f := newV1Fixture(t)
	state := f.store.Snapshot()
	ap := *findAP(state, homeAP)
	now := time.Now().UTC()
	ctx := apContext{state: state, now: now}
	cases := []struct {
		name    string
		obs     apObs
		pending []controller.PendingAP
		want    api.APStatus
		flags   []string
	}{
		{"never", apObs{}, nil, api.APStatusNeverConnected, nil},
		{"offline", apObs{status: obsStatus{ConnectionCount: 1, LastSeen: now.Add(-time.Hour)}}, nil, api.APStatusOffline, nil},
		{"in sync", apObs{status: obsStatus{Connected: true, ConnectionCount: 1, AppliedRevision: 1}}, nil, api.APStatusInSync, nil},
		{"applying", apObs{status: obsStatus{Connected: true, ConnectionCount: 1}}, nil, api.APStatusPending, nil},
		{"rejected", apObs{status: obsStatus{Connected: true, ConnectionCount: 1, LastError: "bad"}}, nil, api.APStatusRejected, nil},
		{"offline and rejected", apObs{status: obsStatus{ConnectionCount: 1, LastError: "bad"}}, nil, api.APStatusOffline, []string{"rejected"}},
		{"config error", apObs{status: obsStatus{Connected: true, ConnectionCount: 1, AppliedRevision: 1, CompileError: "x", ConfigSource: "delivered"}}, nil, api.APStatusConfigError, nil},
		{"auth failing", apObs{status: obsStatus{ConnectionCount: 1}}, []controller.PendingAP{{ID: homeAP, Reason: controller.PendingAuthFailed, LastSeen: now.Add(-time.Minute)}}, api.APStatusAuthFailing, []string{"offline"}},
		{"old auth failure", apObs{status: obsStatus{ConnectionCount: 1}}, []controller.PendingAP{{ID: homeAP, Reason: controller.PendingAuthFailed, LastSeen: now.Add(-time.Hour)}}, api.APStatusOffline, nil},
		{"source mismatch", apObs{status: obsStatus{Connected: true, ConnectionCount: 1, AppliedRevision: 1}}, []controller.PendingAP{{ID: homeAP, Reason: controller.PendingSourceMismatch, LastSeen: now}}, api.APStatusSourceMismatch, nil},
	}
	for _, tc := range cases {
		ctx.pending = tc.pending
		got, flags := f.server.apStatus(ctx, ap, tc.obs)
		if got != tc.want {
			t.Errorf("%s: status %s, want %s (flags %v)", tc.name, got, tc.want, flags)
		}
		for _, flag := range tc.flags {
			if !containsString(flags, flag) {
				t.Errorf("%s: flags %v lack %s", tc.name, flags, flag)
			}
		}
	}
	// Applying within 2 minutes of the change.
	recent := ap
	recent.ConfigUpdatedAt = now.Add(-30 * time.Second)
	if got, _ := f.server.apStatus(apContext{state: state, now: now}, recent, apObs{status: obsStatus{Connected: true, ConnectionCount: 1}}); got != api.APStatusApplying {
		t.Fatalf("applying: %s", got)
	}
}

func TestPatchAPConcurrencyAndFeatures(t *testing.T) {
	f := newV1Fixture(t)
	// If-Match is required on PATCH /api/aps/$id.
	errorOf(t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP, `{"name":"Hall"}`), http.StatusPreconditionRequired, api.ErrorCodeInvalid)
	stale := f.call(t, http.MethodPatch, "/api/aps/"+homeAP, `{"name":"Hall"}`, "If-Match", `"999"`)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `"code":"stale"`) || !strings.Contains(stale.Body.String(), `"current":{`) {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body.String())
	}
	rev := f.updatedRev("ap", homeAP)
	out := decodeInto[api.MutationResponse[api.APDetail]](t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP,
		`{"protocol_features":{"pong2":true,"ap_cmd":true},"model":"ignored","status":"in_sync"}`, ifMatchHeader(rev)...), http.StatusOK)
	if !out.Object.ProtocolFeatures["pong2"] || !out.Object.ProtocolFeatures[FeatureAPCmd] {
		t.Fatalf("features: %+v", out.Object.ProtocolFeatures)
	}
	if out.Object.UpdatedRev != f.updatedRev("ap", homeAP) || out.Revision != f.store.Snapshot().Revision {
		t.Fatalf("response is not the committed record: updated_rev %d, stored %d", out.Object.UpdatedRev, f.updatedRev("ap", homeAP))
	}
	if !f.controls[homeAP].Features().Has(controller.FeaturePong2) {
		t.Fatal("feature not switched on the control session")
	}
	if findAP(f.store.Snapshot(), homeAP).Model != "" {
		t.Fatal("v1 PATCH wrote the read-only model")
	}
	out = decodeInto[api.MutationResponse[api.APDetail]](t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP,
		`{"protocol_features":{"pong2":null}}`, ifMatchHeader(f.updatedRev("ap", homeAP))...), http.StatusOK)
	if out.Object.ProtocolFeatures["pong2"] || f.controls[homeAP].Features().Has(controller.FeaturePong2) {
		t.Fatal("null did not reset the feature to the default")
	}
	bad := errorOf(t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP, `{"protocol_features":{"warp":true}}`, ifMatchHeader(f.updatedRev("ap", homeAP))...), http.StatusBadRequest, api.ErrorCodeInvalid)
	if bad.Fields["protocol_features.warp"] == "" {
		t.Fatalf("unknown feature: %+v", bad)
	}
	errorOf(t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP, `{"secret":"short"}`, ifMatchHeader(f.updatedRev("ap", homeAP))...), http.StatusBadRequest, api.ErrorCodeInvalid)
	errorOf(t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP, `{"bogus":1}`, ifMatchHeader(f.updatedRev("ap", homeAP))...), http.StatusBadRequest, api.ErrorCodeInvalid)
	// Moving the AP through PATCH compiles both sites; its config changes.
	moved := decodeInto[api.MutationResponse[api.APDetail]](t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP,
		`{"site_id":"other"}`, ifMatchHeader(f.updatedRev("ap", homeAP))...), http.StatusOK)
	if moved.Object.SiteID != "other" || len(moved.Impact.APs) != 1 || moved.Impact.APs[0].ID != homeAP {
		t.Fatalf("move: %+v", moved.Impact)
	}
}

func TestDeleteAPAndSite(t *testing.T) {
	f := newV1Fixture(t)
	errorOf(t, f.call(t, http.MethodDelete, "/api/sites/home", ""), http.StatusConflict, api.ErrorCodeConflict)
	deleted := decodeInto[api.MutationResponse[api.APDeleted]](t, f.call(t, http.MethodDelete, "/api/aps/"+homeAP+"?purge_history=1", ""), http.StatusOK)
	if deleted.Object.ID != homeAP || findAP(f.store.Snapshot(), homeAP) != nil {
		t.Fatalf("AP delete: %+v", deleted)
	}
	dry := decodeInto[api.MutationResponse[api.SiteDeleted]](t, f.call(t, http.MethodDelete, "/api/sites/home?dry_run=1", ""), http.StatusOK)
	if !dry.DryRun || findSite(f.store.Snapshot(), "home") == nil {
		t.Fatal("dry-run site delete removed the site")
	}
	decodeInto[api.MutationResponse[api.SiteDeleted]](t, f.call(t, http.MethodDelete, "/api/sites/home", ""), http.StatusOK)
	state := f.store.Snapshot()
	if findSite(state, "home") != nil || findNetwork(state, "home", "net-home") != nil {
		t.Fatal("site or its WLANs remain")
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/home", ""), http.StatusNotFound, api.ErrorCodeNotFound)
}

func TestCreateSiteAndWLANInLegacyMode(t *testing.T) {
	f := newV1Fixture(t)
	created := decodeInto[api.MutationResponse[api.SiteView]](t, f.call(t, http.MethodPost, "/api/sites", `{"name":"Garage","country":"au","timezone":"Australia/Sydney"}`), http.StatusCreated)
	if created.Object.Country != "AU" || created.Object.Typed {
		t.Fatalf("site: %+v", created.Object)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/sites", `{"name":"garage"}`), http.StatusConflict, api.ErrorCodeConflict)
	missing := errorOf(t, f.call(t, http.MethodPost, "/api/sites/"+created.Object.ID+"/networks", `{"ssid":"Example-G"}`), http.StatusBadRequest, api.ErrorCodeInvalid)
	if missing.Fields["psk"] == "" || missing.Fields["vlan_id"] == "" {
		t.Fatalf("missing fields: %+v", missing)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/sites/"+created.Object.ID+"/networks", `{"ssid":"Example-G","psk":"example-passphrase-55","vlan_id":1,"hidden":true}`), http.StatusServiceUnavailable, api.ErrorCodeMigrationPending)
	wlan := decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPost, "/api/sites/"+created.Object.ID+"/networks", `{"ssid":"Example-G","psk":"example-passphrase-55","vlan_id":30}`), http.StatusCreated)
	if wlan.Object.SSID != "Example-G" || !wlan.Object.Enabled || wlan.Object.VLANID != 30 || !wlan.Object.HasPSK {
		t.Fatalf("wlan: %+v", wlan.Object)
	}
	// Duplicate SSID on overlapping bands is refused.
	errorOf(t, f.call(t, http.MethodPost, "/api/sites/"+created.Object.ID+"/networks", `{"ssid":"Example-G","psk":"example-passphrase-56","vlan_id":31}`), http.StatusUnprocessableEntity, api.ErrorCodeInvalid)
	one := decodeInto[api.WLANView](t, f.call(t, http.MethodGet, "/api/sites/"+created.Object.ID+"/networks/"+wlan.Object.ID, ""), http.StatusOK)
	if one.ID != wlan.Object.ID {
		t.Fatalf("get wlan: %+v", one)
	}
	gone := decodeInto[api.MutationResponse[api.WLANDeleted]](t, f.call(t, http.MethodDelete, "/api/sites/"+created.Object.ID+"/networks/"+wlan.Object.ID, "", ifMatchHeader(1)...), http.StatusConflict)
	_ = gone
	decodeInto[api.MutationResponse[api.WLANDeleted]](t, f.call(t, http.MethodDelete, "/api/sites/"+created.Object.ID+"/networks/"+wlan.Object.ID, ""), http.StatusOK)
}

// committed switches the fixture to typed mode (a fake migration that has
// committed).
func (f *v1Fixture) committed() *fakeMigration {
	migration := &fakeMigration{state: api.MigrationStateCommitted, at: time.Now().Add(-40 * 24 * time.Hour)}
	f.server.UseConfig(Config{Migration: migration})
	return migration
}

func TestTypedEditsInCommittedMode(t *testing.T) {
	f := newV1Fixture(t)
	f.committed()
	cases := []struct {
		name, method, path, body string
		status                   int
		code                     api.ErrorCode
		field                    string
	}{
		{"experimental security", http.MethodPatch, "/api/sites/home/networks/net-home", `{"security":"owe"}`, 422, api.ErrorCodeExperimentalDisabled, "wlan.security"},
		{"experimental roaming", http.MethodPatch, "/api/sites/home/networks/net-home", `{"roaming":{"fast":"11r"}}`, 422, api.ErrorCodeExperimentalDisabled, "wlan.roaming.fast"},
		{"hex with SAE", http.MethodPatch, "/api/sites/home/networks/net-home", `{"psk":"` + strings.Repeat("ab", 32) + `"}`, 400, api.ErrorCodeInvalid, "psk"},
		{"no bands", http.MethodPatch, "/api/sites/home/networks/net-home", `{"bands":[]}`, 400, api.ErrorCodeInvalid, "bands"},
		{"bad isolation", http.MethodPatch, "/api/sites/home/networks/net-home", `{"isolation":"everyone"}`, 400, api.ErrorCodeInvalid, "isolation"},
		{"pmf on wpa3", http.MethodPatch, "/api/sites/home/networks/net-home", `{"security":"wpa3_personal","pmf":"optional"}`, 400, api.ErrorCodeInvalid, "pmf"},
		{"steering on one band", http.MethodPatch, "/api/sites/home/networks/net-home", `{"bands":["5"],"band_steering":true}`, 400, api.ErrorCodeInvalid, "band_steering"},
		{"enterprise without radius", http.MethodPatch, "/api/sites/home/networks/net-home", `{"security":"wpa2_enterprise"}`, 400, api.ErrorCodeInvalid, "radius.auth"},
		{"not stored yet", http.MethodPatch, "/api/sites/home/networks/net-home", `{"hidden":true}`, 501, api.ErrorCodeUnavailable, "hidden"},
		{"160 MHz", http.MethodPatch, "/api/sites/home", `{"rf":{"band_5":{"width":160}}}`, 422, api.ErrorCodeInvalid, "rf.band_5.width"},
		{"power 0", http.MethodPatch, "/api/sites/home", `{"rf":{"band_5":{"power":{"mode":"fixed","dbm":0}}}}`, 422, api.ErrorCodeInvalid, "rf.band_5.power.dbm"},
		{"DFS channel without DFS", http.MethodPatch, "/api/sites/home", `{"rf":{"band_5":{"channels":[52]}}}`, 422, api.ErrorCodeInvalid, "rf.band_5.channels"},
		{"bad LED", http.MethodPatch, "/api/sites/home", `{"ap_defaults":{"led_brightness":300}}`, 422, api.ErrorCodeInvalid, "ap_defaults.led_brightness"},
		{"experimental syslog", http.MethodPatch, "/api/sites/home", `{"syslog":[{"protocol":"udp","host":"192.0.2.5","port":514,"facility":1,"severity":6,"tag":"ap"}]}`, 422, api.ErrorCodeExperimentalDisabled, "site.syslog"},
		{"tagged management VLAN", http.MethodPatch, "/api/sites/home", `{"management":{"vlan_id":30}}`, 422, api.ErrorCodeExperimentalDisabled, "site.management.vlan_id"},
		{"bad blocked MAC", http.MethodPatch, "/api/sites/home", `{"protection":{"blocked_clients":[{"mac":"nope"}]}}`, 400, api.ErrorCodeInvalid, "protection.blocked_clients[0].mac"},
	}
	for _, tc := range cases {
		response := f.call(t, tc.method, tc.path, tc.body)
		body := decodeInto[api.ErrorBody](t, response, tc.status)
		if body.Code != tc.code || (tc.field != "" && body.Fields[tc.field] == "") {
			t.Errorf("%s: %d %s", tc.name, response.Code, response.Body.String())
		}
	}
	apCases := []struct {
		name, body string
		status     int
		field      string
	}{
		{"DFS static channel", `{"rf":{"5":{"channel":52}}}`, 422, "rf.5.channel"},
		{"invalid static channel", `{"rf":{"5":{"channel":165,"width":80}}}`, 422, "rf.5.channel"},
		{"160 MHz override", `{"rf":{"5":{"width":160}}}`, 422, "rf.5.width"},
		{"power out of range", `{"rf":{"2.4":{"power_dbm":30}}}`, 422, "rf.2.4.power_dbm"},
		{"SSH without keys", `{"ssh_enabled":true}`, 422, "ssh_enabled"},
		{"experimental management", `{"management":{"vlan_id":30}}`, 422, "ap.management"},
		{"valid override not stored yet", `{"rf":{"5":{"channel":149,"width":80,"power_dbm":17}}}`, 501, "rf.5"},
	}
	for _, tc := range apCases {
		response := f.call(t, http.MethodPatch, "/api/aps/"+homeAP, tc.body, ifMatchHeader(f.updatedRev("ap", homeAP))...)
		if response.Code != tc.status || (tc.field != "" && !strings.Contains(response.Body.String(), tc.field)) {
			t.Errorf("%s: %d %s", tc.name, response.Code, response.Body.String())
		}
	}
	// Typed mode reads the expert field, not the legacy advanced one.
	view := decodeInto[api.ExpertPatchView](t, f.call(t, http.MethodGet, "/api/sites/home/expert", ""), http.StatusOK)
	if view.Field != "expert" || len(view.Patch) != 0 {
		t.Fatalf("typed expert view: %+v", view)
	}
	// Legacy keys still work in typed mode; a dry run reports the impact.
	dry := decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home?dry_run=1", `{"enabled":false}`), http.StatusOK)
	if !dry.DryRun || dry.Impact.Disruption != api.DisruptionWLANRestart {
		t.Fatalf("dry run: %+v", dry.Impact)
	}
}

func TestExpertPolicy(t *testing.T) {
	policy := defaultExpertPolicy{}
	refused := []struct {
		scope api.ExpertScope
		patch string
		code  api.ErrorCode
	}{
		{api.ExpertScopeSite, `{"WLANs":[]}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeSite, `{"wlans":[{"SSID":"x"}]}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeAP, `{"timestamp":1}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeAP, `{"Stats":{"interval":5}}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeSite, `{"monitor":{"pcapwlan":{}}}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeWLAN, `{"ssid":"x"}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeWLAN, `{"auth":{"psk":"x"}}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeWLAN, `{"BandConfigs":[{"Band":"5","bssidindex":2}]}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeWLAN, `{"Wxlan":{"PortalURL":"x"}}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeWLAN, `{"Auth":{"AKM":{"WPA_PSK":true}}}`, api.ErrorCodeForbiddenPath},
		{api.ExpertScopeWLAN, `{"SSID":"a","ssid":"b"}`, api.ErrorCodeDuplicateKey},
		{api.ExpertScopeSite, `{"Radios":[{"Band":"5","Power":1,"power":2}]}`, api.ErrorCodeDuplicateKey},
		{api.ExpertScopeSite, `{"LED":{"Brightness":1},"LED":{"Brightness":2}}`, api.ErrorCodeDuplicateKey},
	}
	for _, tc := range refused {
		_, _, err := policy.Check(tc.scope, []byte(tc.patch), true)
		expertErr, ok := err.(*ExpertError)
		if !ok || expertErr.Code != tc.code || (tc.code == api.ErrorCodeForbiddenPath && expertErr.Pointer == "") {
			t.Errorf("%s %s: %v", tc.scope, tc.patch, err)
		}
	}
	allowed := []struct {
		scope api.ExpertScope
		patch string
	}{
		{api.ExpertScopeWLAN, `{"Auth":{"AKM":{"WPA_PSK":false,"WPA3_PSK":true}},"Dot11w":{"Enabled":true}}`},
		{api.ExpertScopeSite, liveSitePatch},
		{api.ExpertScopeWLAN, liveNetworkPatch},
		{api.ExpertScopeAP, `{"LED":{"Brightness":10},"Radios":[{"Band":"5","Channel":36}]}`},
	}
	for _, tc := range allowed {
		stored, _, err := policy.Check(tc.scope, []byte(tc.patch), true)
		if err != nil || string(stored) != tc.patch {
			t.Errorf("%s %s: %v %s", tc.scope, tc.patch, err, stored)
		}
	}
	// Outside legacy mode keys are folded to their canonical names and
	// unknown keys are kept and reported.
	stored, ignored, err := policy.Check(api.ExpertScopeWLAN, []byte(`{"roammode":"11r","dot11w":{"enabled":true},"NotAKey":1}`), false)
	if err != nil || !strings.Contains(string(stored), `"RoamMode":"11r"`) || !strings.Contains(string(stored), `"Dot11w":{"Enabled":true}`) ||
		len(ignored) != 1 || ignored[0] != "NotAKey" {
		t.Fatalf("fold: %v %s %v", err, stored, ignored)
	}
}

func TestExpertEndpointsRefuseForbiddenPaths(t *testing.T) {
	f := newV1Fixture(t)
	body := errorOf(t, f.call(t, http.MethodPut, "/api/sites/home/expert", `{"patch":{"wlans":[]}}`), http.StatusUnprocessableEntity, api.ErrorCodeForbiddenPath)
	if body.Fields["patch/wlans"] == "" {
		t.Fatalf("pointer: %+v", body)
	}
	errorOf(t, f.call(t, http.MethodPut, "/api/aps/"+homeAP+"/expert", `{"patch":{"ID":"x"}}`), http.StatusUnprocessableEntity, api.ErrorCodeForbiddenPath)
	errorOf(t, f.call(t, http.MethodPut, "/api/sites/home/networks/net-home/expert", `{"patch":{"SSID":"a","ssid":"b"}}`), http.StatusUnprocessableEntity, api.ErrorCodeDuplicateKey)
	view := decodeInto[api.ExpertPatchView](t, f.call(t, http.MethodGet, "/api/sites/home/networks/net-home/advanced", ""), http.StatusOK)
	if view.Scope != api.ExpertScopeWLAN || view.Patch["Auth"] == nil {
		t.Fatalf("view: %+v", view)
	}
	// Removing the AP patch with null.
	decodeInto[api.MutationResponse[api.ExpertPatchView]](t, f.call(t, http.MethodPut, "/api/aps/"+homeAP+"/expert", `{"patch":{"LED":{"Brightness":5}}}`), http.StatusOK)
	cleared := decodeInto[api.MutationResponse[api.ExpertPatchView]](t, f.call(t, http.MethodPut, "/api/aps/"+homeAP+"/expert", `{"patch":null}`), http.StatusOK)
	if len(cleared.Object.Patch) != 0 || len(recordKey(*findAP(f.store.Snapshot(), homeAP), "advanced")) != 0 {
		t.Fatalf("clear: %+v", cleared.Object)
	}
}

// typedRecord stands in for a schema-2 record (design §4.3 keys).
type typedRecord struct {
	ID      string   `json:"id"`
	Hidden  bool     `json:"hidden,omitempty"`
	Bands   []string `json:"bands"`
	Roaming struct {
		Fast           string `json:"fast"`
		NeighborReport bool   `json:"neighbor_report"`
	} `json:"roaming"`
	MaxClients int             `json:"max_clients,omitempty"`
	Advanced   json.RawMessage `json:"advanced,omitempty"`
}

func TestPatchRecordCodec(t *testing.T) {
	record := typedRecord{ID: "n", Bands: []string{"2.4", "5"}}
	record.Roaming.NeighborReport = true
	patch, _ := decodeObject([]byte(`{"hidden":true,"roaming":{"fast":"okc"},"max_clients":64}`))
	if err := patchRecord(&record, patch); err != nil {
		t.Fatal(err)
	}
	if !record.Hidden || record.Roaming.Fast != "okc" || !record.Roaming.NeighborReport || record.MaxClients != 64 || len(record.Bands) != 2 {
		t.Fatalf("merge: %+v", record)
	}
	reset, _ := decodeObject([]byte(`{"max_clients":null,"hidden":false}`))
	if err := patchRecord(&record, reset); err != nil || record.MaxClients != 0 || record.Hidden {
		t.Fatalf("reset: %v %+v", err, record)
	}
	unknown, _ := decodeObject([]byte(`{"isolation":"clients"}`))
	err := patchRecord(&record, unknown)
	if unsupported, ok := err.(*errUnsupported); !ok || unsupported.path != "isolation" {
		t.Fatalf("unsupported: %v", err)
	}
	if err := setRecordKey(&record, "advanced", json.RawMessage(`{"Auth":null}`)); err != nil || string(record.Advanced) != `{"Auth":null}` {
		t.Fatalf("raw key: %v %s", err, record.Advanced)
	}
	if err := setRecordKey(&record, "expert", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a key the record lacks was accepted")
	}
}

func TestDiffAndDisruption(t *testing.T) {
	before := []byte(`{"Timestamp":1,"SiteName":"a","Radios":[{"Band":"5","Channel":36},{"Band":"2.4","Channel":1}],"WLANs":[{"ID":"w","SSID":"Example","Hidden":false,"Auth":{"PSK":"old-passphrase"}}],"LED":{"Brightness":255}}`)
	after := []byte(`{"Timestamp":2,"SiteName":"b","Radios":[{"Band":"2.4","Channel":1},{"Band":"5","Channel":149}],"WLANs":[{"ID":"w","SSID":"Example","Hidden":true,"Auth":{"PSK":"new-passphrase"}}],"LED":{"Brightness":10},"Country":"AU"}`)
	diff := diffConfigs(before, after)
	paths := map[string]api.DiffEntry{}
	for _, entry := range diff {
		paths[entry.Path] = entry
	}
	if len(diff) != 5 || paths["Radios[5].Channel"].Path == "" || paths["WLANs[Example].Hidden"].Path == "" || paths["LED.Brightness"].Path == "" || paths["Country"].Path == "" {
		t.Fatalf("diff: %+v", diff)
	}
	if secret := paths["WLANs[Example].Auth.PSK"]; secret.From != "[changed]" || secret.To != "[changed]" {
		t.Fatalf("secret diff: %+v", secret)
	}
	classes := map[string]api.Disruption{
		"WLANs[Example].Hidden": api.DisruptionWLANRestart, "Radios[5].Channel": api.DisruptionRadioBounce,
		"Radios[5].CCRev": api.DisruptionAllRadiosBounce, "Country": api.DisruptionAllRadiosBounce,
		"LED.Brightness": api.DisruptionNone, "SVIs[1].Vlan_ID": api.DisruptionStrandingRisk, "Monitor.CCRev": api.DisruptionRadioBounce,
	}
	for path, want := range classes {
		if got := disruptionOf(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
	summary := impactSummary(1, map[string]int{"Example": 1}, map[string]int{"5": 1}, 0, 0, 0, map[string]int{"Example": 19})
	if summary != "WLAN Example restarts on 1 AP; about 19 clients reconnect; 5 GHz radio restarts on 1 AP; clients on 5 GHz reconnect" {
		t.Fatalf("summary: %q", summary)
	}
	if impactSummary(0, nil, nil, 0, 0, 0, nil) != "No AP changes" {
		t.Fatal("empty summary")
	}
}

func TestHistoryChangesAndRestore(t *testing.T) {
	f := newV1Fixture(t)
	history := &fakeHistory{}
	f.server.UseConfig(Config{History: history})
	decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home", `{"ssid":"Example-2"}`), http.StatusOK)
	decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home?dry_run=1", `{"ssid":"Example-3"}`), http.StatusOK)
	decodeInto[api.MutationResponse[api.SiteView]](t, f.call(t, http.MethodPatch, "/api/sites/home", `{"timezone":"Australia/Perth"}`), http.StatusOK)
	summaries := history.summaries()
	if len(summaries) != 2 || !strings.Contains(strings.Join(summaries, "|"), "WLAN Example: renamed to Example-2") {
		t.Fatalf("history: %v", summaries)
	}
	page := decodeInto[api.ChangePage](t, f.call(t, http.MethodGet, "/api/sites/home/changes", ""), http.StatusOK)
	if len(page.Changes) != 2 || page.Changes[0].Revision <= page.Changes[1].Revision {
		t.Fatalf("changes: %+v", page)
	}
	wlanChange := page.Changes[1]
	if len(wlanChange.AffectedAPs) != 1 || wlanChange.AffectedAPs[0].ID != homeAP || wlanChange.Actor.Kind != api.ActorKindSession {
		t.Fatalf("WLAN change: %+v", wlanChange)
	}
	detail := decodeInto[api.ChangeDetail](t, f.call(t, http.MethodGet, "/api/sites/home/changes/"+itoa(page.Changes[0].Revision), ""), http.StatusOK)
	if !detail.Restorable || detail.Snapshot == nil || strings.Contains(f.call(t, http.MethodGet, "/api/sites/home/changes/"+itoa(page.Changes[0].Revision), "").Body.String(), "hmac:0123456789abcdef") {
		t.Fatalf("detail: %+v", detail)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/other/changes/"+itoa(page.Changes[0].Revision), ""), http.StatusNotFound, api.ErrorCodeNotFound)
	// Restore needs typed mode and a restorer.
	errorOf(t, f.call(t, http.MethodPost, "/api/sites/home/changes/"+itoa(wlanChange.Revision)+"/restore?dry_run=1", ""), http.StatusNotImplemented, api.ErrorCodeUnavailable)
	migration := &fakeMigration{state: api.MigrationStateCommitted}
	restorer := &fakeRestorer{}
	f.server.UseConfig(Config{History: history, Migration: migration, Restorer: restorer})
	restorer.secrets = map[string]string{"networks.net-home.psk": "Passphrase for Example-2 at this revision differs from today's"}
	body := errorOf(t, f.call(t, http.MethodPost, "/api/sites/home/changes/"+itoa(wlanChange.Revision)+"/restore?dry_run=1", ""), http.StatusUnprocessableEntity, api.ErrorCodeSecretRequired)
	if body.Fields["networks.net-home.psk"] == "" {
		t.Fatalf("secret fields: %+v", body)
	}
	restorer.secrets = nil
	out := decodeInto[api.MutationResponse[api.RestorePlan]](t, f.call(t, http.MethodPost, "/api/sites/home/changes/"+itoa(wlanChange.Revision)+"/restore", `{"keep_current":["networks.net-home.psk"]}`), http.StatusOK)
	if out.Object.Revision != wlanChange.Revision || len(out.Object.Changed) != 1 || findNetwork(f.store.Snapshot(), "home", "net-home").SSID != "Example" {
		t.Fatalf("restore: %+v", out)
	}
	// Actions are recorded without a snapshot.
	f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/push-config", "")
	if !strings.Contains(strings.Join(history.summaries(), "|"), "action: Re-sent config to AP Hallway") {
		t.Fatalf("action not recorded: %v", history.summaries())
	}
}

type fakeRestorer struct{ secrets map[string]string }

func (r *fakeRestorer) Restore(state *model.State, _ *model.Tx, siteID string, entry ChangeEntry, input api.RestoreInput) (api.RestorePlan, error) {
	if r.secrets != nil {
		return api.RestorePlan{}, &SecretsRequiredError{Fields: r.secrets}
	}
	for index := range state.Networks {
		if state.Networks[index].SiteID == siteID {
			state.Networks[index].SSID = "Example"
		}
	}
	return api.RestorePlan{Changed: []api.ChangeObject{{Kind: "network", ID: "net-home", Name: "Example"}}}, nil
}

func itoa(value uint64) string { raw, _ := json.Marshal(value); return string(raw) }

func TestMigrationEndpoints(t *testing.T) {
	f := newV1Fixture(t)
	status := decodeInto[api.MigrationStatus](t, f.call(t, http.MethodGet, "/api/system/migration", ""), http.StatusOK)
	if status.State != api.MigrationStateNotRun || status.Paths == nil {
		t.Fatalf("status: %+v", status)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/system/migration/retry", ""), http.StatusNotImplemented, api.ErrorCodeUnavailable)
	migration := &fakeMigration{state: api.MigrationStateLegacy}
	f.server.UseConfig(Config{Migration: migration})
	run := decodeInto[api.MigrationRun](t, f.call(t, http.MethodPost, "/api/system/migration/retry?dry_run=1", ""), http.StatusOK)
	if !run.DryRun || run.Status.State != api.MigrationStateLegacy {
		t.Fatalf("dry run: %+v", run)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/system/migration/finalise", ""), http.StatusConflict, api.ErrorCodeConflict)
	run = decodeInto[api.MigrationRun](t, f.call(t, http.MethodPost, "/api/system/migration/retry", ""), http.StatusOK)
	if run.DryRun || run.Status.State != api.MigrationStateCommitted || len(migration.retries) != 2 || migration.retries[0] != true {
		t.Fatalf("retry: %+v %v", run, migration.retries)
	}
	if system := decodeInto[api.SystemView](t, f.call(t, http.MethodGet, "/api/system", ""), http.StatusOK); system.Migration.State != api.MigrationStateCommitted {
		t.Fatalf("system migration: %+v", system.Migration)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/system/migration/retry", ""), http.StatusConflict, api.ErrorCodeConflict)
	migration.at = time.Now()
	errorOf(t, f.call(t, http.MethodPost, "/api/system/migration/finalise", ""), http.StatusConflict, api.ErrorCodeConflict)
	migration.at = time.Now().Add(-31 * 24 * time.Hour)
	decodeInto[api.MigrationRun](t, f.call(t, http.MethodPost, "/api/system/migration/finalise?dry_run=1", ""), http.StatusOK)
	if len(migration.finalise) != 1 || !migration.finalise[0] {
		t.Fatalf("finalise: %v", migration.finalise)
	}
}

func TestPersistedSessionsListAndRevoke(t *testing.T) {
	f := newV1Fixture(t)
	gate := webgate.New(gateAdminToken, f.server).WithTokens(f.store.Tokens()).WithSessions(f.store.Sessions())
	do := func(method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8088"+path, nil)
		req.Header.Set("User-Agent", "test-browser")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, req)
		return response
	}
	login := func() *http.Cookie {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8088/auth/login", strings.NewReader(`{"token":"`+gateAdminToken+`"}`))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("login: %d %s", response.Code, response.Body.String())
		}
		return response.Result().Cookies()[0]
	}
	first, second := login(), login()
	list := decodeInto[api.AuthSessionList](t, do(http.MethodGet, "/api/system/sessions", first), http.StatusOK)
	if len(list.Sessions) != 2 {
		t.Fatalf("sessions: %+v", list)
	}
	current := 0
	for _, session := range list.Sessions {
		if session.Current {
			current++
		}
		if strings.Contains(session.ID, first.Value) || strings.Contains(session.ID, second.Value) {
			t.Fatal("a session ID is the cookie")
		}
	}
	if current != 1 {
		t.Fatalf("current marker: %+v", list)
	}
	// A new gate on the same store (a restart) still knows the session.
	restarted := webgate.New(gateAdminToken, f.server).WithSessions(f.store.Sessions())
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8088/api/sites", nil)
	req.AddCookie(second)
	response := httptest.NewRecorder()
	restarted.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("session lost on restart: %d", response.Code)
	}
	revoked := decodeInto[api.RevokedSessions](t, do(http.MethodPost, "/api/system/sessions/revoke-all", first), http.StatusOK)
	if revoked.Revoked != 1 || do(http.MethodGet, "/api/sites", second).Code != http.StatusUnauthorized || do(http.MethodGet, "/api/sites", first).Code != http.StatusOK {
		t.Fatalf("revoke-all: %+v", revoked)
	}
	list = decodeInto[api.AuthSessionList](t, do(http.MethodGet, "/api/system/sessions", first), http.StatusOK)
	decodeInto[api.RevokedSessions](t, do(http.MethodDelete, "/api/system/sessions/"+list.Sessions[0].ID, first), http.StatusOK)
	if do(http.MethodGet, "/api/sites", first).Code != http.StatusUnauthorized {
		t.Fatal("revoked session still works")
	}
}

func TestAPActions(t *testing.T) {
	f := newV1Fixture(t)
	body := errorOf(t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/reboot", ""), http.StatusNotImplemented, api.ErrorCodeUnverifiedCommand)
	if !strings.Contains(body.Error, "dismist ap33 ssh") {
		t.Fatalf("fallback missing: %+v", body)
	}
	commands := &fakeCommands{}
	f.server.UseCommands(commands)
	errorOf(t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/reboot", ""), http.StatusNotImplemented, api.ErrorCodeUnverifiedCommand)
	decodeInto[api.MutationResponse[api.APDetail]](t, f.call(t, http.MethodPatch, "/api/aps/"+homeAP, `{"protocol_features":{"ap_cmd":true}}`, ifMatchHeader(f.updatedRev("ap", homeAP))...), http.StatusOK)
	result := decodeInto[api.ActionResult](t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/reboot", ""), http.StatusOK)
	if result.Via != "ap-cmd-rx" || len(commands.sent) != 1 || commands.sent[0].Cmd != "reboot" || commands.sent[0].Sender != "dismist" || string(commands.sent[0].Params) != "{}" {
		t.Fatalf("reboot: %+v %+v", result, commands.sent)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/restart-radios", `{"band":"6"}`), http.StatusBadRequest, api.ErrorCodeInvalid)
	decodeInto[api.ActionResult](t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/restart-radios", `{"band":"5"}`), http.StatusOK)
	if commands.sent[1].Cmd != "radio_reinit" || !strings.Contains(string(commands.sent[1].Params), `"5"`) {
		t.Fatalf("radio reinit: %+v", commands.sent[1])
	}
	push := decodeInto[api.ActionResult](t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/push-config", ""), http.StatusOK)
	if push.Via != "reconnect" || push.Revision == 0 {
		t.Fatalf("push-config: %+v", push)
	}
	errorOf(t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/locate", `{"minutes":5}`), http.StatusServiceUnavailable, api.ErrorCodeMigrationPending)
	f.committed()
	errorOf(t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/locate", `{"minutes":5}`), http.StatusUnprocessableEntity, api.ErrorCodeExperimentalDisabled)
	errorOf(t, f.call(t, http.MethodPost, "/api/aps/"+homeAP+"/actions/warp", ""), http.StatusNotFound, api.ErrorCodeNotFound)
	errorOf(t, f.call(t, http.MethodPost, "/api/sites/home/clients/02-bb-00-00-00-01/disconnect", ""), http.StatusNotFound, api.ErrorCodeNotFound)
}

func TestEventsAndClients(t *testing.T) {
	f := newV1Fixture(t)
	now := time.Now().UTC()
	for index, event := range []model.EventV2{
		{SiteID: "home", APID: homeAP, Kind: "connection", Severity: "info", Title: "AP connected", FirstSeen: now.Add(-3 * time.Hour), LastSeen: now.Add(-3 * time.Hour)},
		{SiteID: "home", APID: homeAP, Kind: "dhcp_failure", Severity: "warning", Title: "DHCP failed", ClientMAC: "02-bb-00-00-00-01", FirstSeen: now.Add(-time.Minute), LastSeen: now.Add(-time.Minute), Data: json.RawMessage(`{"vlan":30}`)},
		{SiteID: "other", APID: otherAP, Kind: "connection", Severity: "info", Title: "AP connected", FirstSeen: now, LastSeen: now},
	} {
		if _, err := f.store.Events2().AppendController(event); err != nil {
			t.Fatalf("event %d: %v", index, err)
		}
	}
	page := decodeInto[api.EventPage](t, f.call(t, http.MethodGet, "/api/sites/home/events", ""), http.StatusOK)
	if len(page.Events) != 2 || page.Events[0].Kind != "dhcp_failure" || page.Events[0].APName != "Hallway" || page.Events[0].Data["vlan"] == nil {
		t.Fatalf("events: %+v", page)
	}
	if recent := decodeInto[api.EventPage](t, f.call(t, http.MethodGet, "/api/sites/home/events?range=1h", ""), http.StatusOK); len(recent.Events) != 1 {
		t.Fatalf("range: %+v", recent)
	}
	if warn := decodeInto[api.EventPage](t, f.call(t, http.MethodGet, "/api/sites/home/events?severity=warning&client=02:BB:00:00:00:01", ""), http.StatusOK); len(warn.Events) != 1 {
		t.Fatalf("filters: %+v", warn)
	}
	paged := decodeInto[api.EventPage](t, f.call(t, http.MethodGet, "/api/sites/home/events?limit=1", ""), http.StatusOK)
	if len(paged.Events) != 1 || paged.NextBefore == nil {
		t.Fatalf("paging: %+v", paged)
	}
	one := decodeInto[api.EventView](t, f.call(t, http.MethodGet, "/api/sites/home/events/"+page.Events[0].ID, ""), http.StatusOK)
	if one.ClientMAC != "02-bb-00-00-00-01" {
		t.Fatalf("event: %+v", one)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/other/events/"+page.Events[0].ID, ""), http.StatusNotFound, api.ErrorCodeNotFound)
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/home/events?range=forever", ""), http.StatusBadRequest, api.ErrorCodeInvalid)
	apEvents := decodeInto[api.EventPage](t, f.call(t, http.MethodGet, "/api/aps/"+homeAP+"/events", ""), http.StatusOK)
	if len(apEvents.Events) != 2 {
		t.Fatalf("AP events: %+v", apEvents)
	}
	// The site-less client lookup finds where the client was last seen.
	client := decodeInto[api.ClientDetail](t, f.call(t, http.MethodGet, "/api/clients/02:BB:00:00:00:01", ""), http.StatusOK)
	if client.SiteID != "home" || client.Live != nil || len(client.Events) != 1 {
		t.Fatalf("client: %+v", client)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/clients/02-bb-00-00-00-99", ""), http.StatusNotFound, api.ErrorCodeNotFound)
	errorOf(t, f.call(t, http.MethodGet, "/api/clients/nope", ""), http.StatusBadRequest, api.ErrorCodeInvalid)
	errorOf(t, f.call(t, http.MethodPut, "/api/clients/02-bb-00-00-00-01/meta", `{"alias":"Phone"}`), http.StatusNotImplemented, api.ErrorCodeUnavailable)
	meta := &fakeClientMeta{}
	f.server.UseConfig(Config{ClientMeta: meta})
	saved := decodeInto[api.ClientMeta](t, f.call(t, http.MethodPut, "/api/clients/02:bb:00:00:00:01/meta", `{"alias":" Phone ","notes":"kitchen"}`), http.StatusOK)
	if saved.MAC != "02-bb-00-00-00-01" || saved.Alias != "Phone" {
		t.Fatalf("meta: %+v", saved)
	}
}

func TestSearchRevealAndBundle(t *testing.T) {
	f := newV1Fixture(t)
	result := decodeInto[api.SearchResult](t, f.call(t, http.MethodGet, "/api/search?q=hall", ""), http.StatusOK)
	if len(result.Hits) != 1 || result.Hits[0].Kind != "ap" || result.Hits[0].ID != homeAP {
		t.Fatalf("search AP: %+v", result)
	}
	result = decodeInto[api.SearchResult](t, f.call(t, http.MethodGet, "/api/search?q=example-other", ""), http.StatusOK)
	if len(result.Hits) != 1 || result.Hits[0].Kind != "wlan" {
		t.Fatalf("search WLAN: %+v", result)
	}
	reveal := decodeInto[api.RevealView](t, f.call(t, http.MethodPost, "/api/sites/home/networks/net-home/reveal", ""), http.StatusOK)
	if reveal.PSK != "example-passphrase-01" || reveal.WiFiQR != "WIFI:T:WPA;S:Example;P:example-passphrase-01;;" {
		t.Fatalf("reveal: %+v", reveal)
	}
	if got := wifiQR(`a;b`, `p:q`, "wpa3_personal", true); got != `WIFI:T:SAE;S:a\;b;P:p\:q;H:true;;` {
		t.Fatalf("qr escaping: %s", got)
	}
	// A read-scope token may not reveal.
	req := httptest.NewRequest(http.MethodPost, "/api/sites/home/networks/net-home/reveal", nil)
	req.Host = "127.0.0.1:8088"
	req = req.WithContext(webgate.WithPrincipal(req.Context(), webgate.Principal{Via: api.AuthViaToken, Scope: api.TokenScopeRead}))
	response := httptest.NewRecorder()
	f.server.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read token revealed: %d", response.Code)
	}
	bundle := f.call(t, http.MethodGet, "/api/onboarding/ca-bundle.pem", "")
	if bundle.Code != http.StatusOK || bundle.Header().Get("Content-Type") != "application/x-pem-file" {
		t.Fatalf("bundle: %d %v", bundle.Code, bundle.Header())
	}
}

func TestSamplesAggregateTheSite(t *testing.T) {
	f := newV1Fixture(t)
	now := time.Now().UTC().Truncate(time.Minute)
	for minute := 0; minute < 3; minute++ {
		at := now.Add(-time.Duration(minute) * time.Minute)
		observation := model.Observation{SiteID: "home", APID: homeAP, At: at, Connected: true,
			Clients: []model.ClientSnapshot{{MAC: "02:bb:00:00:00:01", RSSI: -50}, {MAC: "02:bb:00:00:00:02", RSSI: -60}},
			Radios:  []model.RadioSample{{Band: "5", Channel: 149, Width: 80, UtilAll: 0.2, Clients: 2}}}
		if err := f.store.RecordObservation(observation); err != nil {
			t.Fatal(err)
		}
	}
	page := decodeInto[api.SamplePage](t, f.call(t, http.MethodGet, "/api/sites/home/history/samples?range=1h&step=1h", ""), http.StatusOK)
	if len(page.Samples) == 0 || page.Samples[len(page.Samples)-1].Clients != 2 || page.Samples[len(page.Samples)-1].APsConnected != 1 ||
		len(page.Samples[len(page.Samples)-1].Radios) != 1 || page.Samples[len(page.Samples)-1].Radios[0].Channel != 149 {
		t.Fatalf("samples: %+v", page)
	}
	errorOf(t, f.call(t, http.MethodGet, "/api/sites/home/history/samples?step=5s", ""), http.StatusBadRequest, api.ErrorCodeInvalid)
}

func readEvent(t *testing.T, reader *bufio.Reader) (string, string) {
	t.Helper()
	for {
		frame := readSSEFrame(t, reader)
		if strings.HasPrefix(frame, ":") {
			continue
		}
		lines := strings.SplitN(strings.TrimSpace(frame), "\n", 2)
		if len(lines) != 2 {
			t.Fatalf("frame: %q", frame)
		}
		return strings.TrimPrefix(lines[0], "event: "), strings.TrimPrefix(lines[1], "data: ")
	}
}

func TestStreamV2Deltas(t *testing.T) {
	f := newV1Fixture(t)
	f.server.streamInterval = 20 * time.Millisecond
	f.server.streamHeartbeat = 50 * time.Millisecond
	f.server.streamMaxAge = 5 * time.Second
	server := httptest.NewServer(f.server)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sites/home/stream", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	event, data := readEvent(t, reader)
	var snapshot api.StreamSnapshot
	if event != api.StreamEventSnapshot || json.Unmarshal([]byte(data), &snapshot) != nil || len(snapshot.APs) != 1 || snapshot.APs[0].ID != homeAP {
		t.Fatalf("snapshot: %s %s", event, data)
	}
	if strings.Contains(data, "passphrase") || strings.Contains(data, "02:bb") {
		t.Fatalf("stream leaks: %s", data)
	}
	// A committed mutation sends config, then a fresh snapshot.
	decodeInto[api.MutationResponse[api.WLANView]](t, f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home", `{"ssid":"Example-2"}`), http.StatusOK)
	event, data = readEvent(t, reader)
	var config api.ConfigEvent
	if event != api.StreamEventConfig || json.Unmarshal([]byte(data), &config) != nil || config.Kind != "wlan.updated" || len(config.AffectedAPs) != 1 {
		t.Fatalf("config event: %s %s", event, data)
	}
	if event, _ = readEvent(t, reader); event != api.StreamEventSnapshot {
		t.Fatalf("after config: %s", event)
	}
	// A stored event arrives as a summary without the client MAC.
	now := time.Now().UTC().Add(time.Second)
	if _, err := f.store.Events2().AppendController(model.EventV2{SiteID: "home", APID: homeAP, Kind: "dhcp_failure", Severity: "warning",
		Title: "DHCP failed", ClientMAC: "02-bb-00-00-00-01", FirstSeen: now, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	for {
		event, data = readEvent(t, reader)
		if event == api.StreamEventEvent {
			break
		}
	}
	var summary api.EventSummary
	if json.Unmarshal([]byte(data), &summary) != nil || !summary.HasClient || strings.Contains(data, "02-bb") || summary.Kind != "dhcp_failure" {
		t.Fatalf("event summary: %s", data)
	}
	// Push lifecycle events from the controller reach the site.
	f.server.PublishPush(api.PushEvent{APID: homeAP, Revision: 3, State: api.PushStateNotified})
	for {
		event, data = readEvent(t, reader)
		if event == api.StreamEventPush {
			break
		}
	}
	if !strings.Contains(data, `"state":"notified"`) {
		t.Fatalf("push: %s", data)
	}
}

func TestReconnectPushIsReportedOncePerStream(t *testing.T) {
	f := newV1Fixture(t)
	recorder := httptest.NewRecorder()
	reported := map[string]uint64{}
	f.server.pushes.track(homeAP, 5, time.Now())
	live := api.APLive{ID: homeAP, ConfigRevision: 5, AppliedRevision: 4}
	if err := f.server.reportPush(recorder, recorder, live, reported); err != nil || recorder.Body.Len() != 0 {
		t.Fatalf("reported before applied: %q", recorder.Body.String())
	}
	live.AppliedRevision = 5
	if err := f.server.reportPush(recorder, recorder, live, reported); err != nil || !strings.Contains(recorder.Body.String(), `"state":"applied"`) {
		t.Fatalf("applied: %q", recorder.Body.String())
	}
	size := recorder.Body.Len()
	_ = f.server.reportPush(recorder, recorder, live, reported)
	if recorder.Body.Len() != size {
		t.Fatal("reported twice")
	}
	f.server.pushes.track(homeAP, 6, time.Now())
	live = api.APLive{ID: homeAP, ConfigRevision: 6, AppliedRevision: 5, LastError: "bad config"}
	_ = f.server.reportPush(recorder, recorder, live, reported)
	if !strings.Contains(recorder.Body.String(), `"state":"rejected"`) || !strings.Contains(recorder.Body.String(), "bad config") {
		t.Fatalf("rejected: %q", recorder.Body.String())
	}
	f.server.pushes.track(otherAP, 2, time.Now().Add(-10*time.Minute))
	_ = f.server.reportPush(recorder, recorder, api.APLive{ID: otherAP, ConfigRevision: 2}, reported)
	if !strings.Contains(recorder.Body.String(), `"state":"timeout"`) {
		t.Fatalf("timeout: %q", recorder.Body.String())
	}
	if material(api.APLive{ClientCount: 1}) == material(api.APLive{ClientCount: 2}) || material(api.APLive{UptimeSeconds: 1}) != material(api.APLive{UptimeSeconds: 2}) {
		t.Fatal("material fields")
	}
}
