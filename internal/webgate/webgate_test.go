package webgate

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A reverse proxy with HTTP Basic auth forwards the browser's
// Authorization header on every request; only a Bearer header is a token
// attempt, so the session cookie still signs those requests in.
func TestGateIgnoresNonBearerAuthorization(t *testing.T) {
	token := strings.Repeat("d", 64)
	gate := New(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://controller.local:8088/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	request.Header.Set("Authorization", "Basic b3BlcmF0b3I6c2VjcmV0")
	gate.ServeHTTP(login, request)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	send := func(path, authorization string, withCookie bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://controller.local:8088"+path, nil)
		request.Header.Set("Authorization", authorization)
		if withCookie {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, request)
		return response
	}
	for _, authorization := range []string{"Basic b3BlcmF0b3I6c2VjcmV0", "Digest username=\"operator\"", "basic"} {
		if response := send("/api/sites", authorization, true); response.Code != http.StatusOK {
			t.Fatalf("%q with a session cookie: %d %s", authorization, response.Code, response.Body.String())
		}
		if response := send("/auth/session", authorization, true); response.Code != http.StatusOK {
			t.Fatalf("/auth/session with %q and a session cookie: %d %s", authorization, response.Code, response.Body.String())
		}
		response := send("/api/sites", authorization, false)
		body := checkErrorBody(t, authorization+" alone", response, http.StatusUnauthorized, "unauthorized")
		if !strings.Contains(string(body["error"]), "sign in") {
			t.Fatalf("%q without a cookie: %s, want the sign-in message", authorization, response.Body.String())
		}
	}
	// A Bearer header is a token attempt: a bad one is refused even with a
	// valid cookie (tokens are not configured here, so every one is bad).
	for _, authorization := range []string{"Bearer dsm_x_y", "bearer dsm_x_y", "Bearer"} {
		response := send("/api/sites", authorization, true)
		body := checkErrorBody(t, authorization, response, http.StatusUnauthorized, "unauthorized")
		if !strings.Contains(string(body["error"]), "token") {
			t.Fatalf("%q: %s, want the bad-token message", authorization, response.Body.String())
		}
	}
}

func TestGateLoginRateLimit(t *testing.T) {
	token := strings.Repeat("e", 64)
	trusted, err := proxytrust.ParsePrefixes("10.244.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	gate := New(token, http.NotFoundHandler())
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	gate.now = func() time.Time { return now }
	handler := proxytrust.Middleware(trusted, gate)
	login := func(remote, forwarded, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://controller.local:8088/auth/login", strings.NewReader(body))
		request.RemoteAddr = remote
		if forwarded != "" {
			request.Header.Set("X-Forwarded-For", forwarded)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	wrong := `{"token":"` + strings.Repeat("0", 64) + `"}`
	right := `{"token":"` + token + `"}`
	for attempt := 1; attempt <= 10; attempt++ {
		if response := login("192.0.2.5:4000", "", wrong); response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", attempt, response.Code, response.Body.String())
		}
	}
	// The 11th attempt in the minute is refused, even with the right
	// token, so it reveals nothing.
	limited := login("192.0.2.5:4000", "", right)
	checkErrorBody(t, "11th attempt", limited, http.StatusTooManyRequests, "rate_limited")
	if retry := limited.Header().Get("Retry-After"); retry != "6" {
		t.Fatalf("Retry-After %q, want 6", retry)
	}
	if len(limited.Result().Cookies()) != 0 {
		t.Fatal("a rate-limited login set a cookie")
	}
	// Other clients are not affected; a forged X-Forwarded-For from an
	// untrusted peer does not get a fresh bucket.
	if response := login("192.0.2.6:4000", "", right); response.Code != http.StatusOK {
		t.Fatalf("another client: %d %s", response.Code, response.Body.String())
	}
	if response := login("192.0.2.5:4000", "198.51.100.1", right); response.Code != http.StatusTooManyRequests {
		t.Fatalf("forged X-Forwarded-For: %d", response.Code)
	}
	// Behind a trusted proxy, each forwarded client has its own bucket.
	for attempt := 1; attempt <= 10; attempt++ {
		if response := login("10.244.3.199:4000", "198.51.100.7", wrong); response.Code != http.StatusUnauthorized {
			t.Fatalf("proxied attempt %d: %d", attempt, response.Code)
		}
	}
	if response := login("10.244.3.199:4000", "198.51.100.7", right); response.Code != http.StatusTooManyRequests {
		t.Fatalf("proxied 11th attempt: %d", response.Code)
	}
	if response := login("10.244.3.199:4000", "198.51.100.8", right); response.Code != http.StatusOK {
		t.Fatalf("another proxied client: %d %s", response.Code, response.Body.String())
	}
	// One attempt comes back every 6 seconds.
	now = now.Add(6 * time.Second)
	if response := login("192.0.2.5:4000", "", right); response.Code != http.StatusOK {
		t.Fatalf("after 6 s: %d %s", response.Code, response.Body.String())
	}
	if response := login("192.0.2.5:4000", "", right); response.Code != http.StatusTooManyRequests {
		t.Fatalf("second attempt after 6 s: %d", response.Code)
	}
	// IPv6 clients share their /64.
	for attempt := 1; attempt <= 10; attempt++ {
		login(fmt.Sprintf("[2001:db8::%x]:4000", attempt), "", wrong)
	}
	if response := login("[2001:db8::ffff]:4000", "", right); response.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64: %d", response.Code)
	}
	if response := login("[2001:db8:0:1::1]:4000", "", right); response.Code != http.StatusOK {
		t.Fatalf("another /64: %d", response.Code)
	}
}

func TestLoginLimiterDropsIdleClients(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for index := 0; index < loginLimiterSweep; index++ {
		limiter.allow(netip.AddrFrom4([4]byte{10, 0, byte(index >> 8), byte(index)}), now)
	}
	limiter.allow(netip.MustParseAddr("192.0.2.1"), now.Add(loginWindow))
	if len(limiter.buckets) != 1 {
		t.Fatalf("%d buckets after a sweep, want 1", len(limiter.buckets))
	}
}
