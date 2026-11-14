package webgate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/proxytrust"
)

const (
	cookieName = "dismist_session"
	sessionTTL = 24 * time.Hour
)

// LoadOrCreateToken keeps the administrator's credential out of the database
// and tracked configuration. Operators can read this private file locally.
func LoadOrCreateToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("admin token file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if token, err := readToken(path); err == nil {
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate admin token: %w", err)
	}
	token := hex.EncodeToString(random[:])
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return readToken(path)
	}
	if err != nil {
		return "", err
	}
	if _, err = file.WriteString(token + "\n"); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return token, nil
}

func readToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("admin token file must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(string(raw), "\n")
	if len(token) != 64 {
		return "", errors.New("admin token file must contain a 64-character hex token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", errors.New("admin token file must contain a 64-character hex token")
	}
	return token, nil
}

// TokenVerifier checks API bearer tokens (model.TokenStore implements it).
type TokenVerifier interface {
	// Verify returns the token's record when token is a valid
	// "dsm_<id>_<secret>" bearer token.
	Verify(token string) (model.TokenRecord, bool)
	// Touch records a use of the token.
	Touch(id string, at time.Time) error
}

// Principal is who a request to /api/ or /auth/session was authenticated
// as. Handlers read it with PrincipalFrom.
type Principal struct {
	Via   api.AuthVia
	Scope api.TokenScope
	// TokenID and TokenName identify a bearer token (Via "token").
	TokenID   string
	TokenName string
	// Expires is the session expiry (Via "session").
	Expires time.Time
}

// Admin reports whether the principal may write and read admin-only
// resources.
func (p Principal) Admin() bool { return p.Scope == api.TokenScopeAdmin }

// String names the principal for logs and audit entries (never a secret).
func (p Principal) String() string {
	if p.Via == api.AuthViaToken {
		return fmt.Sprintf("token %s (%s, %s)", p.TokenID, p.TokenName, p.Scope)
	}
	return "session"
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p. The gate sets it; tests may too.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal the gate authenticated, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Gate protects the operator API: a session cookie from /auth/login with the
// administrator token, or an API bearer token (design §4.8). Static UI
// files pass through so the login page can load.
type Gate struct {
	next      http.Handler
	tokenHash [32]byte
	tokens    TokenVerifier
	now       func() time.Time
	mu        sync.Mutex
	sessions  map[string]time.Time
	touched   map[string]time.Time // token ID -> last Touch call
}

// New protects the operator API while allowing the static login UI to load.
func New(token string, next http.Handler) *Gate {
	if len(token) != 64 || next == nil {
		panic("webgate requires a token and next handler")
	}
	return &Gate{
		next: next, tokenHash: sha256.Sum256([]byte(token)), now: time.Now,
		sessions: make(map[string]time.Time), touched: make(map[string]time.Time),
	}
}

// WithTokens enables API bearer tokens checked by verifier.
func (g *Gate) WithTokens(verifier TokenVerifier) *Gate {
	g.tokens = verifier
	return g
}

func (g *Gate) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	path := request.URL.Path
	if path == "/healthz" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, api.HealthView{Status: "ok"})
		return
	}
	if path == "/auth/session" && request.Method == http.MethodGet {
		principal, status := g.authenticate(request)
		if status != authOK {
			writeJSON(writer, http.StatusUnauthorized, unauthenticatedSession())
			return
		}
		writeJSON(writer, http.StatusOK, sessionView(principal))
		return
	}
	if path == "/auth/login" && request.Method == http.MethodPost {
		g.login(writer, request)
		return
	}
	if path == "/auth/logout" && request.Method == http.MethodPost {
		g.logout(writer, request)
		return
	}
	if strings.HasPrefix(path, "/api/") {
		principal, status := g.authenticate(request)
		switch status {
		case authBadToken:
			writeError(writer, http.StatusUnauthorized, api.ErrorCodeUnauthorized, "the API token is not valid or was revoked", nil)
			return
		case authNone:
			writeError(writer, http.StatusUnauthorized, api.ErrorCodeUnauthorized, "sign in to continue", nil)
			return
		}
		if isWrite(request.Method) {
			// Bearer requests skip the same-origin check: a browser never
			// attaches a bearer token on its own, so there is no CSRF.
			if principal.Via == api.AuthViaSession && !sameOrigin(request) {
				writeError(writer, http.StatusForbidden, api.ErrorCodeForbidden, "cross-origin request refused", nil)
				return
			}
			if !principal.Admin() {
				writeError(writer, http.StatusForbidden, api.ErrorCodeForbidden, "this API token is read-only", nil)
				return
			}
		}
		request = request.WithContext(WithPrincipal(request.Context(), principal))
	}
	g.next.ServeHTTP(writer, request)
}

type authStatus int

const (
	authNone     authStatus = iota // no credentials
	authOK                         // a valid session or token
	authBadToken                   // an Authorization header that is not a valid token
)

// authenticate checks the bearer token when an Authorization header is
// present (a bad token is not retried as a cookie), else the session
// cookie.
func (g *Gate) authenticate(request *http.Request) (Principal, authStatus) {
	if header := request.Header.Get("Authorization"); header != "" {
		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || g.tokens == nil {
			return Principal{}, authBadToken
		}
		record, ok := g.tokens.Verify(strings.TrimSpace(token))
		if !ok {
			return Principal{}, authBadToken
		}
		g.touch(record.ID)
		return Principal{Via: api.AuthViaToken, Scope: api.TokenScope(record.Scope), TokenID: record.ID, TokenName: record.Name}, authOK
	}
	if expires, ok := g.session(request); ok {
		return Principal{Via: api.AuthViaSession, Scope: api.TokenScopeAdmin, Expires: expires}, authOK
	}
	return Principal{}, authNone
}

// touch records a token use at most once a minute per token, so busy
// clients do not cause a database write per request.
func (g *Gate) touch(id string) {
	now := g.now()
	g.mu.Lock()
	last, seen := g.touched[id]
	if seen && now.Sub(last) < model.TokenTouchInterval {
		g.mu.Unlock()
		return
	}
	g.touched[id] = now
	g.mu.Unlock()
	if err := g.tokens.Touch(id, now); err != nil && !errors.Is(err, model.ErrNotFound) {
		log.Printf("API token %s: record last use: %v", id, err)
	}
}

func sessionView(p Principal) api.AuthSession {
	view := api.AuthSession{Authenticated: true, Via: p.Via, Scope: p.Scope}
	if p.Via == api.AuthViaToken {
		view.TokenName = p.TokenName
	} else if !p.Expires.IsZero() {
		expires := p.Expires.UTC()
		view.ExpiresAt = &expires
	}
	return view
}

func (g *Gate) login(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeError(writer, http.StatusForbidden, api.ErrorCodeForbidden, "cross-origin request refused", nil)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(writer, http.StatusBadRequest, api.ErrorCodeInvalid, "invalid login request", nil)
		return
	}
	provided := sha256.Sum256([]byte(body.Token))
	if len(body.Token) != 64 || subtle.ConstantTimeCompare(provided[:], g.tokenHash[:]) != 1 {
		writeError(writer, http.StatusUnauthorized, api.ErrorCodeUnauthorized, "invalid administrator token", map[string]string{"token": "that token is not valid"})
		return
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		writeError(writer, http.StatusInternalServerError, api.ErrorCodeInternal, "cannot create session", nil)
		return
	}
	id := hex.EncodeToString(random[:])
	now := g.now()
	g.mu.Lock()
	for value, expiry := range g.sessions {
		if !expiry.After(now) {
			delete(g.sessions, value)
		}
	}
	g.sessions[id] = now.Add(sessionTTL)
	g.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name: cookieName, Value: id, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: secureRequest(request), SameSite: http.SameSiteStrictMode,
	})
	writeJSON(writer, http.StatusOK, sessionView(Principal{Via: api.AuthViaSession, Scope: api.TokenScopeAdmin, Expires: now.Add(sessionTTL)}))
}

func (g *Gate) logout(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeError(writer, http.StatusForbidden, api.ErrorCodeForbidden, "cross-origin request refused", nil)
		return
	}
	if cookie, err := request.Cookie(cookieName); err == nil {
		g.mu.Lock()
		delete(g.sessions, cookie.Value)
		g.mu.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{
		Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: secureRequest(request), SameSite: http.SameSiteStrictMode,
	})
	writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": false})
}

// session returns the expiry of the request's session cookie, if valid.
func (g *Gate) session(request *http.Request) (time.Time, bool) {
	cookie, err := request.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 64 {
		return time.Time{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	expiry, ok := g.sessions[cookie.Value]
	if !ok {
		return time.Time{}, false
	}
	if !expiry.After(g.now()) {
		delete(g.sessions, cookie.Value)
		return time.Time{}, false
	}
	return expiry, true
}

func isWrite(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func sameOrigin(request *http.Request) bool {
	if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme := "http"
	if secureRequest(request) {
		scheme = "https"
	}
	return parsed.Scheme == scheme && strings.EqualFold(parsed.Host, request.Host)
}

func secureRequest(request *http.Request) bool {
	return proxytrust.HTTPS(request)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// writeError writes the {error, code, fields} error contract (design §4.8).
func writeError(writer http.ResponseWriter, status int, code api.ErrorCode, message string, fields map[string]string) {
	if fields == nil {
		fields = map[string]string{}
	}
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(writer, status, api.ErrorBody{Error: message, Code: code, Fields: fields})
}

// unauthenticatedBody is the 401 of GET /auth/session: the error contract
// plus "authenticated": false, so clients that read either shape work.
type unauthenticatedBody struct {
	api.ErrorBody
	Authenticated bool `json:"authenticated"`
}

func unauthenticatedSession() unauthenticatedBody {
	return unauthenticatedBody{
		ErrorBody: api.ErrorBody{Error: "not signed in", Code: api.ErrorCodeUnauthorized, Fields: map[string]string{}},
	}
}
