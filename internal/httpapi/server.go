package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	webassets "github.com/nicko170/dismist"
	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options"
	"github.com/nicko170/dismist/internal/proxytrust"
	"github.com/nicko170/dismist/internal/webgate"
)

type Server struct {
	store           model.StateStore
	control         *controller.Server
	manager         *controller.Manager
	base            []byte
	apID            string
	assets          http.Handler
	web             fs.FS
	hosts           map[string]bool
	streamInterval  time.Duration
	streamHeartbeat time.Duration
	streamMaxAge    time.Duration
	mutationLock    *sync.Mutex
	tokens          TokenManager
	next            fs.FS
	system          SystemInfo
	backupBusy      sync.Mutex

	routes   router
	config   Config
	commands Commands
	pusher   Pusher
	sessions SessionManager
	hub      *streamHub
	pushes   *pushTracker
	now      func() time.Time
	// defaultFeatures are the -ap-features defaults.
	defaultFeatures controller.Features
	// legacyUI serves the legacy app at /legacy/ (until the release that
	// deletes it, design §2.4).
	legacyUI bool
}

func New(store model.StateStore, control *controller.Server, base []byte, apID string, allowedHosts ...string) *Server {
	web, err := fs.Sub(webassets.WebAssets, "web/dist")
	if err != nil {
		panic(err)
	}
	next, err := fs.Sub(webassets.UIAssets, "ui/dist")
	if err != nil {
		panic(err)
	}
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true}
	for _, host := range allowedHosts {
		if host != "" {
			hosts[strings.ToLower(strings.TrimSuffix(host, "."))] = true
		}
	}
	server := &Server{
		store: store, control: control, base: append([]byte(nil), base...),
		apID: apID, web: web, assets: http.FileServer(http.FS(web)), hosts: hosts,
		streamInterval: time.Second, streamHeartbeat: 15 * time.Second, streamMaxAge: 30 * time.Minute,
		mutationLock: new(sync.Mutex), next: next, hub: newStreamHub(), pushes: newPushTracker(),
		now: func() time.Time { return time.Now().UTC() }, legacyUI: true,
	}
	if withTokens, ok := store.(interface{ Tokens() *model.TokenStore }); ok {
		server.tokens = withTokens.Tokens()
	}
	if withSessions, ok := store.(interface{ Sessions() *model.SessionStore }); ok {
		server.sessions = withSessions.Sessions()
	}
	server.registerRoutes()
	return server
}

// UseMutationLock coordinates API writes with AP registry activation. Call it
// before serving requests; the registry must receive the same mutex.
func (s *Server) UseMutationLock(lock *sync.Mutex) *Server {
	if lock != nil {
		s.mutationLock = lock
	}
	return s
}

// UseConfig sets workstream A's typed-config layer.
func (s *Server) UseConfig(config Config) *Server {
	s.config = config
	return s
}

// UseCommands sets the AP command channel (ap-cmd-rx, workstream B).
func (s *Server) UseCommands(commands Commands) *Server {
	s.commands = commands
	return s
}

// UsePusher sets the config push lifecycle (workstream B).
func (s *Server) UsePusher(pusher Pusher) *Server {
	s.pusher = pusher
	return s
}

// UseSessions sets the persisted web-session store for
// /api/system/sessions. New picks the store's own Sessions() when it has
// one.
func (s *Server) UseSessions(sessions SessionManager) *Server {
	s.sessions = sessions
	return s
}

// UseDefaultFeatures sets the -ap-features defaults shown on AP detail.
func (s *Server) UseDefaultFeatures(features controller.Features) *Server {
	s.defaultFeatures = features
	return s
}

// UseLegacyUI turns serving the legacy app at /legacy/ on or off.
func (s *Server) UseLegacyUI(on bool) *Server {
	s.legacyUI = on
	return s
}

func NewWithManager(store model.StateStore, manager *controller.Manager, base []byte, defaultAPID string, allowedHosts ...string) *Server {
	control, _ := manager.Server(defaultAPID)
	server := New(store, control, base, defaultAPID, allowedHosts...)
	server.manager = manager
	return server
}

func (s *Server) controlFor(apID string) *controller.Server {
	if s.manager != nil {
		control, _ := s.manager.Server(apID)
		return control
	}
	if apID == s.apID {
		return s.control
	}
	return nil
}

func (s *Server) registry() *options.Registry {
	if s.config.Registry != nil {
		return s.config.Registry
	}
	return options.Default()
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	// Bearer-token requests skip the origin check: browsers never attach a
	// bearer token on their own, so there is no CSRF exposure (§4.8).
	viaToken := false
	if p, ok := webgate.PrincipalFrom(request.Context()); ok && p.Via == api.AuthViaToken {
		viaToken = true
	}
	if !s.allowedHost(request.Host) || (!viaToken && !s.allowedOrigin(request)) {
		writeAPIError(writer, forbidden("this host or origin is not allowed; use the controller's own address"))
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/") {
		if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
			s.mutationLock.Lock()
			defer s.mutationLock.Unlock()
		}
		if !s.routes.serve(writer, request) {
			routeNotFound(writer)
		}
		return
	}
	if (request.Method == http.MethodGet || request.Method == http.MethodHead) && !strings.HasPrefix(request.URL.Path, "/auth/") {
		s.serveUI(writer, request)
		return
	}
	routeNotFound(writer)
}

func (s *Server) allowedHost(host string) bool {
	if s.hosts["*"] {
		return true
	}
	hostname := host
	if parsed, err := url.Parse("http://" + host); err == nil {
		hostname = parsed.Hostname()
	}
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	return s.hosts[hostname]
}

func (s *Server) allowedOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return s.allowedHost(parsed.Host)
	}
	scheme := "http"
	if proxytrust.HTTPS(request) {
		scheme = "https"
	}
	requested, err := url.Parse(scheme + "://" + request.Host)
	if err != nil || requested.User != nil || parsed.Scheme != scheme || !strings.EqualFold(parsed.Hostname(), requested.Hostname()) {
		return false
	}
	originPort, requestPort := parsed.Port(), requested.Port()
	if originPort == "" {
		if scheme == "https" {
			originPort = "443"
		} else {
			originPort = "80"
		}
	}
	if requestPort == "" {
		if scheme == "https" {
			requestPort = "443"
		} else {
			requestPort = "80"
		}
	}
	return originPort == requestPort
}

func randomID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// legacyClient reports whether the request comes from the legacy app served
// at /legacy/ (its fetches and its EventSource carry the page as Referer).
// The endpoints it uses answer in their phase-0 shapes for it, so the
// legacy UI keeps working until the release that deletes it.
func legacyClient(request *http.Request) bool {
	referer := request.Header.Get("Referer")
	if referer == "" {
		return false
	}
	parsed, err := url.Parse(referer)
	if err != nil {
		return false
	}
	return parsed.Path == "/legacy" || strings.HasPrefix(parsed.Path, "/legacy/")
}
