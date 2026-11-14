package webgate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/proxytrust"
)

func TestLoadOrCreateTokenIsPrivateAndStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller", "admin.token")
	token, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64", len(token))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token file permissions = %o", info.Mode().Perm())
	}
	again, err := LoadOrCreateToken(path)
	if err != nil || again != token {
		t.Fatalf("existing token changed: %q, %v", again, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(path); err == nil {
		t.Fatal("world-readable admin token was accepted")
	}
}

func TestGateLoginSessionLogout(t *testing.T) {
	token := strings.Repeat("a", 64)
	requests := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	})
	gate := New(token, next)

	unauthorized := httptest.NewRecorder()
	gate.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "http://controller.local:8088/api/sites", nil))
	if unauthorized.Code != http.StatusUnauthorized || requests != 0 {
		t.Fatalf("unauthorized API request reached handler: status=%d requests=%d", unauthorized.Code, requests)
	}

	static := httptest.NewRecorder()
	gate.ServeHTTP(static, httptest.NewRequest(http.MethodGet, "http://controller.local:8088/", nil))
	if static.Code != http.StatusOK || requests != 1 {
		t.Fatalf("login page assets unavailable: status=%d requests=%d", static.Code, requests)
	}

	short := httptest.NewRecorder()
	gate.ServeHTTP(short, httptest.NewRequest(http.MethodPost, "http://controller.local:8088/auth/login", strings.NewReader(`{"token":"short"}`)))
	if short.Code != http.StatusUnauthorized {
		t.Fatalf("short token status = %d", short.Code)
	}

	login := httptest.NewRecorder()
	gate.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "http://controller.local:8088/auth/login", strings.NewReader(`{"token":"`+token+`"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %+v", cookies)
	}

	api := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://controller.local:8088/api/sites", nil)
	request.AddCookie(cookies[0])
	gate.ServeHTTP(api, request)
	if api.Code != http.StatusOK || requests != 2 {
		t.Fatalf("valid session failed: status=%d requests=%d", api.Code, requests)
	}

	logout := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "http://controller.local:8088/auth/logout", nil)
	request.AddCookie(cookies[0])
	gate.ServeHTTP(logout, request)
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status = %d", logout.Code)
	}
	after := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "http://controller.local:8088/api/sites", nil)
	request.AddCookie(cookies[0])
	gate.ServeHTTP(after, request)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out session remained valid: %d", after.Code)
	}
}

func TestGateRejectsCrossOriginWrite(t *testing.T) {
	token := strings.Repeat("b", 64)
	requests := 0
	gate := New(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	login := httptest.NewRecorder()
	gate.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "http://controller.local:8088/auth/login", strings.NewReader(`{"token":"`+token+`"}`)))
	if login.Code != http.StatusOK {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for _, origin := range []string{"http://other.local:8088", "https://controller.local:8088"} {
		request := httptest.NewRequest(http.MethodPost, "http://controller.local:8088/api/sites", strings.NewReader(`{}`))
		request.Header.Set("Origin", origin)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || requests != 0 {
			t.Fatalf("cross-origin %q reached API: status=%d requests=%d", origin, response.Code, requests)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "http://controller.local:8088/api/sites", strings.NewReader(`{}`))
	request.Header.Set("Origin", "http://controller.local:8088")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	gate.ServeHTTP(response, request)
	if response.Code != http.StatusOK || requests != 1 {
		t.Fatalf("same-origin write failed: status=%d requests=%d", response.Code, requests)
	}
}

func TestGateLoginThroughTrustedHTTPSProxy(t *testing.T) {
	token := strings.Repeat("c", 64)
	trusted, err := proxytrust.ParsePrefixes("10.244.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	gate := proxytrust.Middleware(trusted, New(token, http.NotFoundHandler()))
	login := func(remote string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://dismist.example/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
		request.RemoteAddr = remote
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("Origin", "https://dismist.example")
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, request)
		return response
	}
	proxied := login("10.244.3.199:41000")
	if proxied.Code != http.StatusOK {
		t.Fatalf("login through trusted proxy = %d: %s", proxied.Code, proxied.Body.String())
	}
	if cookies := proxied.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("session cookie behind HTTPS proxy is not Secure: %+v", cookies)
	}
	if direct := login("192.0.2.5:41000"); direct.Code != http.StatusForbidden {
		t.Fatalf("login with untrusted forwarded scheme = %d, want 403", direct.Code)
	}
}
