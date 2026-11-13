package metrics

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

func TestScrapeReportsRegisteredSitesAndAPsWithoutSensitiveData(t *testing.T) {
	store, err := model.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Private Site"}, {ID: "site-b", Name: "Empty Site"}}
		state.APs = []model.APRecord{{ID: "ap-a", SiteID: "site-a", ManagementIP: "192.0.2.5", SecretFile: "/private/secret", ConfigRevision: 7}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := controller.NewServer(controller.Options{ID: "ap-a", AllowedIP: "192.0.2.5", CertDER: []byte{1}, Config: []byte(`{}`), Revision: 7})
	if err != nil {
		t.Fatal(err)
	}
	manager := controller.NewManager()
	if err := manager.Register("ap-a", "192.0.2.5", control); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	New(store, manager).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	body := response.Body.String()
	for _, want := range []string{
		"dismist_sites 2\n", "dismist_aps 1\n",
		"dismist_site_aps{site=\"site-a\"} 1\n",
		"dismist_site_aps{site=\"site-b\"} 0\n",
		"dismist_ap_connected{site=\"site-a\",ap=\"ap-a\"} 0\n",
		"dismist_ap_desired_revision{site=\"site-a\",ap=\"ap-a\"} 7\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing metric %q in:\n%s", want, body)
		}
	}
	for _, sensitive := range []string{"Private Site", "192.0.2.5", "/private/secret", "client_mac", "ssid", "psk"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(sensitive)) {
			t.Errorf("scrape leaked %q", sensitive)
		}
	}
}

func TestScrapeEscapesLabelValuesAndRejectsOtherRoutes(t *testing.T) {
	store, err := model.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "x\"\\\ny", Name: "site"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store, controller.NewManager())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `dismist_site_aps{site="x\"\\\ny"} 0`) {
		t.Fatalf("unescaped scrape: %s", response.Body.String())
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{{http.MethodPost, "/metrics", http.StatusMethodNotAllowed}, {http.MethodGet, "/metrics/extra", http.StatusNotFound}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Errorf("%s %s: status = %d", test.method, test.path, response.Code)
		}
	}
}

func TestClientCountRequiresTelemetryFromCurrentConnection(t *testing.T) {
	connectedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	status := controller.Status{Connected: true, ConnectedAt: connectedAt}
	stats := controller.TelemetrySnapshot{
		LastFull:          connectedAt.Add(-time.Second),
		AssociatedClients: []telemetry.Client{{MAC: "client-a"}, {MAC: "client-b"}},
	}
	if got := liveClientCount(status, stats); got != 0 {
		t.Fatalf("counted stale clients after reconnect: %d", got)
	}
	stats.LastFast = connectedAt.Add(time.Second)
	if got := liveClientCount(status, stats); got != 2 {
		t.Fatalf("fresh clients = %d", got)
	}
	status.Connected = false
	if got := liveClientCount(status, stats); got != 0 {
		t.Fatalf("counted clients after disconnect: %d", got)
	}
}
