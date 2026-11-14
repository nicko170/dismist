package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/webgate"
)

const gateAdminToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// gatedFixture is the operator stack as dismistd runs it: the web gate
// with the token store in front of the API.
func gatedFixture(t *testing.T) (http.Handler, *model.BoltStore) {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "a", Name: "A"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase-16"}}]}`)
	server := NewWithManager(store, controller.NewManager(), base, "", "controller.example")
	return webgate.New(gateAdminToken, server).WithTokens(store.Tokens()), store
}

type call struct {
	method, path, body string
	cookie             *http.Cookie
	bearer, origin     string
}

func (c call) do(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(c.method, "http://controller.example"+c.path, strings.NewReader(c.body))
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func signIn(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := call{method: http.MethodPost, path: "/auth/login", body: `{"token":"` + gateAdminToken + `"}`}.do(t, handler)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	return response.Result().Cookies()[0]
}

func createToken(t *testing.T, handler http.Handler, cookie *http.Cookie, name, scope string) api.TokenCreated {
	t.Helper()
	response := call{method: http.MethodPost, path: "/api/system/tokens", body: `{"name":"` + name + `","scope":"` + scope + `"}`, cookie: cookie}.do(t, handler)
	if response.Code != http.StatusCreated {
		t.Fatalf("create token: %d %s", response.Code, response.Body.String())
	}
	var created api.TokenCreated
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, created.ID+"_") || created.Scope != api.TokenScope(scope) || created.LastUsed != nil {
		t.Fatalf("created token %+v", created)
	}
	return created
}

func sessionOf(t *testing.T, response *httptest.ResponseRecorder) api.AuthSession {
	t.Helper()
	var session api.AuthSession
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatalf("%v: %s", err, response.Body.String())
	}
	return session
}

func TestSessionAuth(t *testing.T) {
	handler, _ := gatedFixture(t)
	if response := (call{method: http.MethodGet, path: "/auth/session"}).do(t, handler); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous session probe: %d", response.Code)
	}
	cookie := signIn(t, handler)
	response := call{method: http.MethodGet, path: "/auth/session", cookie: cookie}.do(t, handler)
	session := sessionOf(t, response)
	if response.Code != http.StatusOK || !session.Authenticated || session.Via != api.AuthViaSession ||
		session.Scope != api.TokenScopeAdmin || session.ExpiresAt == nil {
		t.Fatalf("session probe: %d %s", response.Code, response.Body.String())
	}
	if response := (call{method: http.MethodGet, path: "/api/sites", cookie: cookie}).do(t, handler); response.Code != http.StatusOK {
		t.Fatalf("session read: %d", response.Code)
	}
	// Sessions keep the same-origin rule for writes.
	cross := call{method: http.MethodPost, path: "/api/sites", body: `{"name":"X"}`, cookie: cookie, origin: "https://evil.example"}.do(t, handler)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-origin session write: %d", cross.Code)
	}
	if response := (call{method: http.MethodPost, path: "/api/sites", body: `{"name":"X"}`, cookie: cookie}).do(t, handler); response.Code != http.StatusCreated {
		t.Fatalf("session write: %d %s", response.Code, response.Body.String())
	}
	if response := (call{method: http.MethodPost, path: "/auth/logout", cookie: cookie}).do(t, handler); response.Code != http.StatusOK {
		t.Fatalf("logout: %d", response.Code)
	}
	if response := (call{method: http.MethodGet, path: "/api/sites", cookie: cookie}).do(t, handler); response.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", response.Code)
	}
}

func TestBearerTokenAuth(t *testing.T) {
	handler, store := gatedFixture(t)
	cookie := signIn(t, handler)
	admin := createToken(t, handler, cookie, "cli", "admin")
	reader := createToken(t, handler, cookie, "grafana", "read")

	// An admin token reads, and writes without an Origin (no CSRF check).
	response := call{method: http.MethodGet, path: "/auth/session", bearer: admin.Token}.do(t, handler)
	session := sessionOf(t, response)
	if response.Code != http.StatusOK || session.Via != api.AuthViaToken || session.Scope != api.TokenScopeAdmin ||
		session.ExpiresAt != nil || session.TokenName != "cli" {
		t.Fatalf("token session probe: %d %s", response.Code, response.Body.String())
	}
	if response := (call{method: http.MethodGet, path: "/api/sites", bearer: admin.Token}).do(t, handler); response.Code != http.StatusOK {
		t.Fatalf("admin token read: %d", response.Code)
	}
	write := call{method: http.MethodPost, path: "/api/sites", body: `{"name":"Lab"}`, bearer: admin.Token, origin: "https://elsewhere.example"}.do(t, handler)
	if write.Code != http.StatusCreated {
		t.Fatalf("admin token write: %d %s", write.Code, write.Body.String())
	}

	// A read token reads but never writes.
	if response := (call{method: http.MethodGet, path: "/api/sites", bearer: reader.Token}).do(t, handler); response.Code != http.StatusOK {
		t.Fatalf("read token read: %d", response.Code)
	}
	denied := call{method: http.MethodPost, path: "/api/sites", body: `{"name":"No"}`, bearer: reader.Token}.do(t, handler)
	if denied.Code != http.StatusForbidden || decodeErrorBody(t, "read token write", denied).Code != api.ErrorCodeForbidden {
		t.Fatalf("read token write: %d %s", denied.Code, denied.Body.String())
	}
	if response := (call{method: http.MethodPost, path: "/api/system/tokens", body: `{"name":"x","scope":"admin"}`, bearer: reader.Token}).do(t, handler); response.Code != http.StatusForbidden {
		t.Fatalf("read token created a token: %d", response.Code)
	}

	// Bad tokens are refused, and a bad bearer is not rescued by a cookie.
	for _, bearer := range []string{"junk", admin.ID + "_" + strings.Repeat("0", 64), admin.ID, "dsm__x"} {
		response := call{method: http.MethodGet, path: "/api/sites", bearer: bearer, cookie: cookie}.do(t, handler)
		if response.Code != http.StatusUnauthorized || decodeErrorBody(t, "bad bearer", response).Code != api.ErrorCodeUnauthorized {
			t.Fatalf("bad bearer %q: %d", bearer, response.Code)
		}
	}

	// The list shows both tokens without secrets and records use.
	listed := call{method: http.MethodGet, path: "/api/system/tokens", bearer: reader.Token}.do(t, handler)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), strings.TrimPrefix(admin.Token, admin.ID+"_")) {
		t.Fatalf("token list: %d %s", listed.Code, listed.Body.String())
	}
	var list api.TokenList
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || len(list.Tokens) != 2 {
		t.Fatalf("token list %s: %v", listed.Body.String(), err)
	}
	for _, token := range list.Tokens {
		if token.LastUsed == nil {
			t.Fatalf("token %s has no last_used after use", token.ID)
		}
	}

	// Revoking a token stops it at once.
	if response := (call{method: http.MethodDelete, path: "/api/system/tokens/" + admin.ID, bearer: admin.Token}).do(t, handler); response.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", response.Code, response.Body.String())
	}
	if response := (call{method: http.MethodGet, path: "/api/sites", bearer: admin.Token}).do(t, handler); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", response.Code)
	}
	if response := (call{method: http.MethodDelete, path: "/api/system/tokens/" + admin.ID, cookie: cookie}).do(t, handler); response.Code != http.StatusNotFound {
		t.Fatalf("revoking twice: %d", response.Code)
	}
	if records, _ := store.Tokens().List(); len(records) != 1 || records[0].ID != reader.ID {
		t.Fatalf("stored tokens after revoke: %+v", records)
	}

	// Validation errors name the field.
	bad := call{method: http.MethodPost, path: "/api/system/tokens", body: `{"name":"x","scope":"root"}`, cookie: cookie}.do(t, handler)
	if bad.Code != http.StatusBadRequest || decodeErrorBody(t, "bad scope", bad).Fields["scope"] == "" {
		t.Fatalf("bad scope: %d %s", bad.Code, bad.Body.String())
	}
}
