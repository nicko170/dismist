package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// decodeErrorBody asserts that a response is the {error, code, fields}
// contract and returns it.
func decodeErrorBody(t *testing.T, label string, response *httptest.ResponseRecorder) api.ErrorBody {
	t.Helper()
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%s: content type %q, body %q", label, response.Header().Get("Content-Type"), response.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("%s: body is not a JSON object: %q", label, response.Body.String())
	}
	for _, key := range []string{"error", "code", "fields"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("%s: body lacks %q: %s", label, key, response.Body.String())
		}
	}
	var body api.ErrorBody
	decoder := json.NewDecoder(bytes.NewReader(response.Body.Bytes()))
	if err := decoder.Decode(&body); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if body.Error == "" || body.Code == "" || body.Fields == nil {
		t.Fatalf("%s: incomplete error body %s", label, response.Body.String())
	}
	return body
}

func TestErrorFormatOnEveryHandler(t *testing.T) {
	handler, _, _ := platformFixture(t)
	const secret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	cases := []struct {
		method, path, body string
		contentType        string
		status             int
		code               api.ErrorCode
		field              string
	}{
		// Router and UI
		{http.MethodGet, "/api/nothing", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPost, "/not-api", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodGet, "/missing.js", "", "", 404, api.ErrorCodeNotFound, ""},
		// AP registration (onboarding.go)
		{http.MethodPost, "/api/aps", `{"id":"x"}`, "text/plain", 400, api.ErrorCodeInvalid, ""},
		{http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-09","bogus":1}`, "", 400, api.ErrorCodeInvalid, "bogus"},
		{http.MethodPost, "/api/aps", `{"id":7}`, "", 400, api.ErrorCodeInvalid, "id"},
		{http.MethodPost, "/api/aps", `{"id":"not-a-mac","secret":"` + secret + `"}`, "", 400, api.ErrorCodeInvalid, "id"},
		{http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-09","secret":"short"}`, "", 400, api.ErrorCodeInvalid, "secret"},
		{http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-09","secret":"` + secret + `","management_ip":"nope"}`, "", 400, api.ErrorCodeInvalid, "management_ip"},
		{http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-09","secret":"` + secret + `","site_id":"zzz"}`, "", 404, api.ErrorCodeNotFound, "site_id"},
		{http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-09","secret":"` + secret + `"}{}`, "", 400, api.ErrorCodeInvalid, ""},
		// AP edit and move (aps.go)
		{http.MethodPut, "/api/aps/ap-a", `{}`, "", 400, api.ErrorCodeInvalid, ""},
		{http.MethodPut, "/api/aps/ap-a", `{"management_ip":"nope"}`, "", 400, api.ErrorCodeInvalid, "management_ip"},
		{http.MethodPut, "/api/aps/ap-a", `{"secret":"short"}`, "", 400, api.ErrorCodeInvalid, "secret"},
		{http.MethodPut, "/api/aps/missing", `{"name":"x"}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/aps/ap-a/site", `{"site_id":"zzz"}`, "", 404, api.ErrorCodeNotFound, "site_id"},
		{http.MethodPut, "/api/aps/ap-a/other", `{"site_id":"a"}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/aps/missing/site", `{"site_id":"b"}`, "", 404, api.ErrorCodeNotFound, ""},
		// Sites (sites.go)
		{http.MethodPost, "/api/sites", `{"name":""}`, "", 400, api.ErrorCodeInvalid, "name"},
		{http.MethodPost, "/api/sites", `{"name":"X","country":"Australia"}`, "", 400, api.ErrorCodeInvalid, "country"},
		{http.MethodPost, "/api/sites", `{"name":"X","timezone":"Mars/Base"}`, "", 400, api.ErrorCodeInvalid, "timezone"},
		{http.MethodPut, "/api/sites/a", `{}`, "", 400, api.ErrorCodeInvalid, ""},
		{http.MethodPut, "/api/sites/a", `{"name":" "}`, "", 400, api.ErrorCodeInvalid, "name"},
		{http.MethodPut, "/api/sites/zzz", `{"name":"x"}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/sites/a/ssh-keys", `{}`, "", 400, api.ErrorCodeInvalid, "keys"},
		{http.MethodPut, "/api/sites/a/ssh-keys", `{"keys":["-----BEGIN OPENSSH PRIVATE KEY-----"]}`, "", 400, api.ErrorCodeInvalid, "keys"},
		{http.MethodPut, "/api/sites/a/advanced", `{"patch":[1]}`, "", 400, api.ErrorCodeInvalid, "patch"},
		{http.MethodPut, "/api/sites/a/advanced", `{}`, "", 400, api.ErrorCodeInvalid, "patch"},
		{http.MethodPut, "/api/sites/a/aps/missing/advanced", `{"patch":{}}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/sites/a/networks/missing/advanced", `{"patch":{}}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/sites/a/advanced", `{"patch":{"x":"[redacted]"}}`, "", 400, api.ErrorCodeInvalid, "patch"},
		{http.MethodPut, "/api/sites/a/whatever", `{}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodGet, "/api/sites/zzz", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodGet, "/api/sites/a/aps/missing/status", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodGet, "/api/sites/a/aps/ap-a/nothing", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodGet, "/api/sites/a/networks/missing/advanced", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodDelete, "/api/sites/zzz/networks/wlan-a", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodDelete, "/api/sites/a", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPost, "/api/sites/a/other", `{}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPost, "/api/sites/zzz/networks", `{}`, "", 404, api.ErrorCodeNotFound, ""},
		// Networks (networks.go)
		{http.MethodPost, "/api/sites/a/networks", `{"ssid":"x"}`, "", 400, api.ErrorCodeInvalid, "psk"},
		{http.MethodPost, "/api/sites/a/networks", `{"ssid":"x","psk":"short","vlan_id":1}`, "", 400, api.ErrorCodeInvalid, "psk"},
		{http.MethodPost, "/api/sites/a/networks", `{"ssid":"x","psk":"long-enough-pass","vlan_id":0}`, "", 400, api.ErrorCodeInvalid, "vlan_id"},
		{http.MethodPost, "/api/sites/a/networks", `{"ssid":"","psk":"long-enough-pass","vlan_id":2}`, "", 400, api.ErrorCodeInvalid, "ssid"},
		{http.MethodPut, "/api/sites/a/networks/missing", `{"ssid":"x"}`, "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/sites/a/networks/wlan-a", `{"vlan_id":"x"}`, "", 400, api.ErrorCodeInvalid, "vlan_id"},
		{http.MethodDelete, "/api/sites/a/networks/missing", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodPut, "/api/networks/missing", `{"ssid":"x"}`, "", 404, api.ErrorCodeNotFound, ""},
		// Events and history (events.go)
		{http.MethodGet, "/api/sites/a/history/nothing", "", "", 404, api.ErrorCodeNotFound, ""},
		{http.MethodGet, "/api/sites/a/aps/missing/samples", "", "", 404, api.ErrorCodeNotFound, ""},
		// Stream (stream.go)
		{http.MethodGet, "/api/sites/zzz/stream", "", "", 404, api.ErrorCodeNotFound, ""},
	}
	for _, tc := range cases {
		label := tc.method + " " + tc.path + " " + tc.body
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Host = "127.0.0.1:8088"
		switch {
		case tc.contentType != "":
			req.Header.Set("Content-Type", tc.contentType)
		case tc.body != "":
			req.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Errorf("%s: status %d, want %d: %s", label, response.Code, tc.status, response.Body.String())
			continue
		}
		body := decodeErrorBody(t, label, response)
		if body.Code != tc.code {
			t.Errorf("%s: code %q, want %q", label, body.Code, tc.code)
		}
		if tc.field != "" && body.Fields[tc.field] == "" {
			t.Errorf("%s: no field error for %q: %s", label, tc.field, response.Body.String())
		}
	}
}

func TestForbiddenHostUsesErrorFormat(t *testing.T) {
	handler, _, _ := apiFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	req.Host = "evil.example"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status %d", response.Code)
	}
	if body := decodeErrorBody(t, "forbidden host", response); body.Code != api.ErrorCodeForbidden {
		t.Fatalf("code %q", body.Code)
	}
}

func TestConflictNamesTheField(t *testing.T) {
	handler, _, _ := platformFixture(t)
	const secret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	body := `{"id":"02-aa-00-00-00-0a","site_id":"a","secret":"` + secret + `"}`
	if response := request(t, handler, http.MethodPost, "/api/aps", body); response.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	response := request(t, handler, http.MethodPost, "/api/aps", body)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", response.Code, response.Body.String())
	}
	if got := decodeErrorBody(t, "duplicate", response); got.Code != api.ErrorCodeConflict || got.Fields["id"] == "" {
		t.Fatalf("duplicate body %+v", got)
	}
}

// fullDiskStore fails every commit the way bbolt does when the volume is
// full.
type fullDiskStore struct{ *model.BoltStore }

var errFullDisk = errors.New("write /state/controller.db: no space left on device")

func (fullDiskStore) Update(func(*model.State) error) error { return errFullDisk }
func (fullDiskStore) UpdateTx(func(*model.State, *model.Tx) error) error {
	return errFullDisk
}

func TestMutationFailureStatuses(t *testing.T) {
	// A storage failure is a 500 that does not carry the error text.
	_, store, _ := platformFixture(t)
	handler := NewWithManager(fullDiskStore{store}, controller.NewManager(), []byte(`{"WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase-16"}}]}`), "", "*")
	for _, mutation := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/sites/a", `{"name":"Renamed"}`},
		{http.MethodPost, "/api/sites", `{"name":"New"}`},
		{http.MethodPut, "/api/sites/a/networks/wlan-a", `{"ssid":"Other"}`},
	} {
		response := request(t, handler, mutation.method, mutation.path, mutation.body)
		label := mutation.method + " " + mutation.path
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s on a full disk: %d %s", label, response.Code, response.Body.String())
		}
		if body := decodeErrorBody(t, label, response); body.Code != api.ErrorCodeInternal || strings.Contains(response.Body.String(), "/state") || strings.Contains(response.Body.String(), "space") {
			t.Fatalf("%s: %s", label, response.Body.String())
		}
	}

	// A store rule the new state breaks stays a 400 with its message.
	recorder := httptest.NewRecorder()
	err := store.Update(func(state *model.State) error {
		state.Sites = append(state.Sites, state.Sites[0])
		return nil
	})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("duplicate site: %v, want model.ErrInvalid", err)
	}
	writeError(recorder, err)
	if body := decodeErrorBody(t, "store rule", recorder); recorder.Code != http.StatusBadRequest || body.Code != api.ErrorCodeInvalid || body.Error != err.Error() {
		t.Fatalf("store rule: %d %s", recorder.Code, recorder.Body.String())
	}

	// Compile failures of several APs: one 422 naming each.
	recorder = httptest.NewRecorder()
	writeError(recorder, errors.Join(
		&compile.APError{APID: "ap-1", Err: errors.New("WLAN template needs PSK auth")},
		&compile.APError{APID: "ap-2", Err: errors.New("bad patch")},
	))
	body := decodeErrorBody(t, "compile failures", recorder)
	if recorder.Code != http.StatusUnprocessableEntity || body.Code != api.ErrorCodeInvalid ||
		!strings.Contains(body.Error, "ap-1") || !strings.Contains(body.Error, "ap-2") ||
		!strings.Contains(body.Fields["aps.ap-1"], "PSK") || !strings.Contains(body.Fields["aps.ap-2"], "bad patch") {
		t.Fatalf("compile failures: %d %s", recorder.Code, recorder.Body.String())
	}
}
