package webgate

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/api"
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

type gate struct {
	next      http.Handler
	tokenHash [32]byte
	mu        sync.Mutex
	sessions  map[string]time.Time
}

// New protects the operator API while allowing the static login UI to load.
func New(token string, next http.Handler) http.Handler {
	if len(token) != 64 || next == nil {
		panic("webgate requires a token and next handler")
	}
	return &gate{next: next, tokenHash: sha256.Sum256([]byte(token)), sessions: make(map[string]time.Time)}
}

func (g *gate) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	path := request.URL.Path
	if path == "/healthz" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if path == "/auth/session" && request.Method == http.MethodGet {
		if !g.authorized(request) {
			writeJSON(writer, http.StatusUnauthorized, unauthenticatedSession())
			return
		}
		writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": true})
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
		if !g.authorized(request) {
			writeError(writer, http.StatusUnauthorized, api.ErrorCodeUnauthorized, "sign in to continue", nil)
			return
		}
		if isWrite(request.Method) && !sameOrigin(request) {
			writeError(writer, http.StatusForbidden, api.ErrorCodeForbidden, "cross-origin request refused", nil)
			return
		}
	}
	g.next.ServeHTTP(writer, request)
}

func (g *gate) login(writer http.ResponseWriter, request *http.Request) {
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
	now := time.Now()
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
	writeJSON(writer, http.StatusOK, map[string]bool{"authenticated": true})
}

func (g *gate) logout(writer http.ResponseWriter, request *http.Request) {
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

func (g *gate) authorized(request *http.Request) bool {
	cookie, err := request.Cookie(cookieName)
	if err != nil || len(cookie.Value) != 64 {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	expiry, ok := g.sessions[cookie.Value]
	if !ok {
		return false
	}
	if !expiry.After(time.Now()) {
		delete(g.sessions, cookie.Value)
		return false
	}
	return true
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
