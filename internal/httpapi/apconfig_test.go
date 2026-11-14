package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/webgate"
)

func getConfigView(t *testing.T, handler http.Handler, path string, want int) api.APConfigView {
	t.Helper()
	response := request(t, handler, http.MethodGet, path, "")
	if response.Code != want {
		t.Fatalf("GET %s: %d %s", path, response.Code, response.Body.String())
	}
	var view api.APConfigView
	if want == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
	}
	return view
}

func TestNormalisedConfigViewWithKeyedHash(t *testing.T) {
	f := newChangeFixture(t)
	const apID = "02-aa-00-00-00-01"
	normalised := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK)
	hashPattern := regexp.MustCompile(`^e` + strconv.Itoa(effective.Version) + `:[0-9a-f]{64}$`)
	if !hashPattern.MatchString(normalised.EffectiveHash) || normalised.NormaliserVersion != effective.Version ||
		normalised.Report == nil || normalised.Report.Version != effective.Version || normalised.View != api.ConfigViewNormalised {
		t.Fatalf("normalised view: %+v", normalised)
	}
	// The hash is the keyed hash of the config the AP is served.
	served, revision := f.controls[apID].ConfigSnapshot()
	want, _, _, err := effective.HashConfig(f.store.HashKey(), served, compile.EffectiveContext(f.store.Snapshot().APs[0]))
	if err != nil || normalised.EffectiveHash != want || normalised.Revision != revision {
		t.Fatalf("hash %s revision %d, want %s %d (%v)", normalised.EffectiveHash, normalised.Revision, want, revision, err)
	}
	raw := request(t, f.handler, http.MethodGet, "/api/aps/"+apID+"/config?view=normalised", "").Body.String()
	if strings.Contains(raw, "example-passphrase") || !strings.Contains(raw, "[redacted]") || strings.Contains(raw, "Timestamp") {
		t.Fatalf("normalised view leaks a secret or keeps Timestamp: %s", raw)
	}
	// The effective view is the compiled config with the same hash.
	compiled := getConfigView(t, f.handler, "/api/aps/"+apID+"/config", http.StatusOK)
	if compiled.View != api.ConfigViewEffective || compiled.EffectiveHash != normalised.EffectiveHash {
		t.Fatalf("effective view %+v", compiled)
	}
	if body := request(t, f.handler, http.MethodGet, "/api/aps/"+apID+"/config?view=effective", "").Body.String(); !strings.Contains(body, "Timestamp") || strings.Contains(body, "example-passphrase") {
		t.Fatalf("effective view: %s", body)
	}
	// A dismist-only edit leaves the hash alone; a WLAN edit moves it.
	f.do(t, http.MethodPut, "/api/sites/a", `{"name":"Renamed"}`, http.StatusOK)
	if again := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK); again.EffectiveHash != normalised.EffectiveHash {
		t.Fatal("site rename changed the effective hash")
	}
	f.do(t, http.MethodPut, "/api/sites/a/networks/home", `{"ssid":"Example-new"}`, http.StatusOK)
	if again := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK); again.EffectiveHash == normalised.EffectiveHash {
		t.Fatal("SSID change kept the effective hash")
	}
}

func TestConfigViewShowsH0(t *testing.T) {
	f := newChangeFixture(t)
	const apID = "02-aa-00-00-00-01"
	if view := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK); view.H0 != nil {
		t.Fatalf("H0 before recording: %+v", view.H0)
	}
	server := f.handler.(*Server)
	if _, err := migrate.RecordH0(f.store, migrate.H0Options{Template: server.base, APs: []string{apID}}); err != nil {
		t.Fatal(err)
	}
	view := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK)
	if view.H0 == nil || view.H0.Matches == nil || !*view.H0.Matches || view.H0.EffectiveHash != view.EffectiveHash {
		t.Fatalf("H0 view %+v, hash %s", view.H0, view.EffectiveHash)
	}
}

func TestConfigViewErrorsAndScopes(t *testing.T) {
	f := newChangeFixture(t)
	const apID = "02-aa-00-00-00-01"
	for _, tc := range []struct {
		path   string
		status int
		code   api.ErrorCode
	}{
		{"/api/aps/" + apID + "/config?view=raw", http.StatusBadRequest, api.ErrorCodeInvalid},
		{"/api/aps/02-aa-00-00-00-99/config", http.StatusNotFound, api.ErrorCodeNotFound},
		{"/api/aps/" + apID + "/config?view=applied", http.StatusNotFound, api.ErrorCodeNotFound},
		{"/api/aps/" + apID + "/other", http.StatusNotFound, api.ErrorCodeNotFound},
	} {
		response := request(t, f.handler, http.MethodGet, tc.path, "")
		if response.Code != tc.status || decodeErrorBody(t, tc.path, response).Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
	// The applied view shows what was delivered.
	served, revision := f.controls[apID].ConfigSnapshot()
	if err := f.store.APConfig().PutDelivered(apID, model.DeliveredConfig{Revision: revision, Bytes: served, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	applied := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=applied", http.StatusOK)
	compiled := getConfigView(t, f.handler, "/api/aps/"+apID+"/config", http.StatusOK)
	if applied.Revision != revision || applied.EffectiveHash != compiled.EffectiveHash {
		t.Fatalf("applied view %+v", applied)
	}
	// A read-scope token sees the effective view, not the normalised one.
	read := webgate.Principal{Via: api.AuthViaToken, Scope: api.TokenScopeRead, TokenID: "dsm_x"}
	for path, want := range map[string]int{
		"/api/aps/" + apID + "/config?view=normalised": http.StatusForbidden,
		"/api/aps/" + apID + "/config?view=effective":  http.StatusOK,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(webgate.WithPrincipal(t.Context(), read))
		req.Host = "127.0.0.1:8088"
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("read token %s: %d", path, response.Code)
		}
	}
	// An AP without a site has no compiled config.
	f.do(t, http.MethodPut, "/api/aps/"+apID+"/site", `{"site_id":""}`, http.StatusOK)
	if response := request(t, f.handler, http.MethodGet, "/api/aps/"+apID+"/config", ""); response.Code != http.StatusNotFound {
		t.Fatalf("unassigned AP config: %d", response.Code)
	}
}

func TestLocalFilesAreRecordedAndReachTheNormaliser(t *testing.T) {
	f := newChangeFixture(t)
	const apID = "02-aa-00-00-00-01"
	if view := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK); view.Report.LocalFiles != effective.LocalFilesUnknown {
		t.Fatalf("never checked: %q", view.Report.LocalFiles)
	}
	f.do(t, http.MethodPut, "/api/aps/"+apID, `{"local_files":[]}`, http.StatusOK)
	if view := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK); view.Report.LocalFiles != effective.LocalFilesNone || !view.Report.ZeroRule {
		t.Fatalf("checked, none: %+v", view.Report)
	}
	digest := strings.Repeat("ab", 32)
	f.do(t, http.MethodPut, "/api/aps/"+apID, `{"local_files":[{"name":"local_overrides","sha256":"`+digest+`"}]}`, http.StatusOK)
	view := getConfigView(t, f.handler, "/api/aps/"+apID+"/config?view=normalised", http.StatusOK)
	if view.Report.LocalFiles != effective.LocalFilesPresent || view.Report.ZeroRule {
		t.Fatalf("present: %+v", view.Report)
	}
	for _, ap := range f.store.Snapshot().APs {
		if ap.ID == apID && (len(ap.LocalFiles) != 1 || ap.LocalFiles[0].SHA256 != digest || ap.LocalFiles[0].SeenAt.IsZero()) {
			t.Fatalf("stored local files %+v", ap.LocalFiles)
		}
	}
	for _, bad := range []string{
		`{"local_files":[{"name":"../etc/passwd","sha256":"` + digest + `"}]}`,
		`{"local_files":[{"name":"local_overrides","sha256":"xyz"}]}`,
	} {
		response := request(t, f.handler, http.MethodPut, "/api/aps/"+apID, bad)
		if response.Code != http.StatusBadRequest || len(decodeErrorBody(t, bad, response).Fields) != 1 {
			t.Fatalf("%s: %d %s", bad, response.Code, response.Body.String())
		}
	}
	secret := strings.Repeat("0a", 32)
	f.do(t, http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-05","site_id":"a","secret":"`+secret+`","local_files":[]}`, http.StatusCreated)
	for _, ap := range f.store.Snapshot().APs {
		if ap.ID == "02-aa-00-00-00-05" && (ap.LocalFiles == nil || len(ap.LocalFiles) != 0) {
			t.Fatalf("registered local files %#v", ap.LocalFiles)
		}
	}
}
