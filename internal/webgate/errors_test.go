package webgate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/api"
)

func checkErrorBody(t *testing.T, label string, response *httptest.ResponseRecorder, status int, code api.ErrorCode) map[string]json.RawMessage {
	t.Helper()
	if response.Code != status {
		t.Fatalf("%s: status %d, want %d: %s", label, response.Code, status, response.Body.String())
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%s: content type %q", label, response.Header().Get("Content-Type"))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("%s: %v: %s", label, err, response.Body.String())
	}
	var body api.ErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error == "" || body.Code != code || body.Fields == nil {
		t.Fatalf("%s: body %s, want code %q", label, response.Body.String(), code)
	}
	return raw
}

func TestGateErrorsUseTheErrorContract(t *testing.T) {
	token := strings.Repeat("b", 64)
	gate := New(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	serve := func(method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, req)
		return response
	}
	checkErrorBody(t, "api without session", serve(http.MethodGet, "http://controller.local/api/sites", "", nil), http.StatusUnauthorized, api.ErrorCodeUnauthorized)
	raw := checkErrorBody(t, "session probe", serve(http.MethodGet, "http://controller.local/auth/session", "", nil), http.StatusUnauthorized, api.ErrorCodeUnauthorized)
	if string(raw["authenticated"]) != "false" {
		t.Fatalf("session probe lacks authenticated:false: %s", raw["authenticated"])
	}
	checkErrorBody(t, "bad login body", serve(http.MethodPost, "http://controller.local/auth/login", `{"token":1}`, nil), http.StatusBadRequest, api.ErrorCodeInvalid)
	wrong := checkErrorBody(t, "wrong token", serve(http.MethodPost, "http://controller.local/auth/login", `{"token":"`+strings.Repeat("c", 64)+`"}`, nil), http.StatusUnauthorized, api.ErrorCodeUnauthorized)
	if !strings.Contains(string(wrong["fields"]), "token") {
		t.Fatalf("wrong token has no field error: %s", wrong["fields"])
	}
	checkErrorBody(t, "cross-origin login", serve(http.MethodPost, "http://controller.local/auth/login", `{"token":"`+token+`"}`,
		map[string]string{"Origin": "https://evil.example"}), http.StatusForbidden, api.ErrorCodeForbidden)
	checkErrorBody(t, "cross-origin logout", serve(http.MethodPost, "http://controller.local/auth/logout", ``,
		map[string]string{"Sec-Fetch-Site": "cross-site"}), http.StatusForbidden, api.ErrorCodeForbidden)
}
