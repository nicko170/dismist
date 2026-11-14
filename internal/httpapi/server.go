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
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/proxytrust"
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
}

func New(store model.StateStore, control *controller.Server, base []byte, apID string, allowedHosts ...string) *Server {
	web, err := fs.Sub(webassets.WebAssets, "web/dist")
	if err != nil {
		panic(err)
	}
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true}
	for _, host := range allowedHosts {
		if host != "" {
			hosts[strings.ToLower(strings.TrimSuffix(host, "."))] = true
		}
	}
	return &Server{
		store: store, control: control, base: append([]byte(nil), base...),
		apID: apID, web: web, assets: http.FileServer(http.FS(web)), hosts: hosts,
		streamInterval: time.Second, streamHeartbeat: 15 * time.Second, streamMaxAge: 30 * time.Minute,
		mutationLock: new(sync.Mutex),
	}
}

// UseMutationLock coordinates API writes with AP registry activation. Call it
// before serving requests; the registry must receive the same mutex.
func (s *Server) UseMutationLock(lock *sync.Mutex) *Server {
	if lock != nil {
		s.mutationLock = lock
	}
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

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if !s.allowedHost(request.Host) || !s.allowedOrigin(request) {
		http.Error(writer, "local access only", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/") && request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
		s.mutationLock.Lock()
		defer s.mutationLock.Unlock()
	}
	switch {
	case request.URL.Path == "/api/aps" && request.Method == http.MethodGet:
		state := s.store.Snapshot()
		aps := make([]map[string]any, 0, len(state.APs))
		for _, ap := range state.APs {
			aps = append(aps, s.apView(ap))
		}
		respond(writer, http.StatusOK, map[string]any{"aps": aps})
	case request.URL.Path == "/api/aps" && request.Method == http.MethodPost:
		s.registerAP(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/aps/") && request.Method == http.MethodPut:
		if len(strings.Split(strings.Trim(request.URL.Path, "/"), "/")) == 3 {
			s.updateAP(writer, request)
		} else {
			s.moveAP(writer, request)
		}
	case request.URL.Path == "/api/sites" && request.Method == http.MethodGet:
		s.listSites(writer)
	case request.URL.Path == "/api/sites" && request.Method == http.MethodPost:
		s.createSite(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodGet:
		s.siteResource(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodPost:
		s.siteMutation(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodPut:
		s.siteUpdate(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/sites/") && request.Method == http.MethodDelete:
		s.siteDelete(writer, request)
	case request.URL.Path == "/api/status" && request.Method == http.MethodGet:
		s.legacyStatus(writer)
	case request.URL.Path == "/api/telemetry" && request.Method == http.MethodGet:
		s.legacyTelemetry(writer)
	case request.URL.Path == "/api/clients" && request.Method == http.MethodGet:
		s.legacyClients(writer)
	case request.URL.Path == "/api/events" && request.Method == http.MethodGet:
		s.legacyEvents(writer)
	case request.URL.Path == "/api/networks" && request.Method == http.MethodGet:
		s.listNetworks(writer, s.currentSiteID())
	case request.URL.Path == "/api/networks" && request.Method == http.MethodPost:
		s.createNetwork(writer, request, s.currentSiteID())
	case strings.HasPrefix(request.URL.Path, "/api/networks/") && request.Method == http.MethodPut:
		id := strings.TrimPrefix(request.URL.Path, "/api/networks/")
		s.editNetwork(writer, request, s.currentSiteID(), id)
	case strings.HasPrefix(request.URL.Path, "/api/networks/") && request.Method == http.MethodDelete:
		id := strings.TrimPrefix(request.URL.Path, "/api/networks/")
		s.removeNetwork(writer, s.currentSiteID(), id)
	default:
		if (request.Method == http.MethodGet || request.Method == http.MethodHead) && !strings.HasPrefix(request.URL.Path, "/api/") {
			s.serveUI(writer, request)
		} else {
			http.NotFound(writer, request)
		}
	}
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
