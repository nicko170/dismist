package metrics

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
	prommodel "github.com/prometheus/common/model"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

// fakeSession stands in for a registered AP session with a chosen status and
// telemetry, so tests cover connected APs without a WSS handshake.
type fakeSession struct {
	status controller.Status
	stats  controller.TelemetrySnapshot
}

func (f fakeSession) Status() controller.Status                       { return f.status }
func (f fakeSession) TelemetrySnapshot() controller.TelemetrySnapshot { return f.stats }

// fakeStore serves a fixed state without the model's validation, so tests
// can hold records the store would refuse (an AP without a secret, a site
// with an odd ID).
type fakeStore struct{ state model.State }

func (f *fakeStore) Snapshot() model.State {
	state := f.state
	state.Sites = append([]model.Site(nil), f.state.Sites...)
	state.APs = append([]model.APRecord(nil), f.state.APs...)
	return state
}

func (f *fakeStore) Update(update func(*model.State) error) error { return update(&f.state) }

func openStore(t *testing.T, state model.State) model.StateStore {
	t.Helper()
	return &fakeStore{state: state}
}

// fixture is a controller with connected, reconnected, disconnected and
// unregistered APs, a site with no APs, an AP whose site is gone and an
// unassigned AP. Names, addresses, SSIDs and MACs are all synthetic.
func fixture(t *testing.T) (model.StateStore, map[string]session) {
	t.Helper()
	connectedAt := time.Date(2026, 11, 14, 3, 4, 5, 0, time.UTC)
	state := model.State{
		Sites: []model.Site{
			{ID: "11111111-1111-4111-8111-111111111111", Name: "Example Home"},
			{ID: "22222222-2222-4222-8222-222222222222", Name: "Example Lab"},
			{ID: "x\"\\\ny", Name: "Odd ID"},
		},
		APs: []model.APRecord{
			{ID: "02-aa-00-00-00-01", Name: "Lounge AP", SiteID: "11111111-1111-4111-8111-111111111111", ManagementIP: "192.0.2.5", ConfigRevision: 7},
			{ID: "02-aa-00-00-00-02", Name: "Office AP", SiteID: "11111111-1111-4111-8111-111111111111", ConfigRevision: 9},
			{ID: "02-aa-00-00-00-03", SiteID: "11111111-1111-4111-8111-111111111111", ConfigRevision: 3},
			{ID: "02-aa-00-00-00-04", SiteID: "x\"\\\ny"},
			{ID: "02-aa-00-00-00-05", SiteID: "33333333-3333-4333-8333-333333333333", ConfigRevision: 1},
			{ID: "02-aa-00-00-00-06"},
		},
		Networks: []model.Network{{ID: "net-1", SiteID: "11111111-1111-4111-8111-111111111111", SSID: "Example", PSK: "correct horse battery staple", Enabled: true}},
	}
	clients := []telemetry.Client{
		{MAC: "02-aa-00-00-10-01", Name: "phone", IP: "198.51.100.20", SSID: "Example", BSSID: "02-aa-00-00-20-01"},
		{MAC: "02-aa-00-00-10-02", SSID: "Example"},
	}
	sessions := map[string]session{
		// Connected, telemetry since this connection.
		"02-aa-00-00-00-01": fakeSession{
			status: controller.Status{Connected: true, ConnectedAt: connectedAt, ConnectionCount: 6, AppliedRevision: 7, LastSeen: connectedAt.Add(90 * time.Second), Firmware: "apfw-0.15.0", Model: "AP33", RemoteAddress: "198.51.100.7:40000"},
			stats: controller.TelemetrySnapshot{
				FullFrames: 12, FastFrames: 345, LastFull: connectedAt.Add(60 * time.Second), UptimeSeconds: 218,
				AssociatedClients: clients,
				VAPs:              []telemetry.VAP{{Interface: "r1v0", SSID: "Example", BSSID: "02-aa-00-00-20-01"}},
			},
		},
		// Connected, but its telemetry is from before the reconnect.
		"02-aa-00-00-00-02": fakeSession{
			status: controller.Status{Connected: true, ConnectedAt: connectedAt, ConnectionCount: 1, AppliedRevision: 8, LastSeen: connectedAt},
			stats:  controller.TelemetrySnapshot{FullFrames: 1, LastFull: connectedAt.Add(-time.Minute), UptimeSeconds: 50, AssociatedClients: clients},
		},
		// Registered, never connected.
		"02-aa-00-00-00-03": fakeSession{},
		// 02-aa-00-00-00-04 has no session: no secret yet.
		"02-aa-00-00-00-05": fakeSession{status: controller.Status{ConnectionCount: 2, LastSeen: connectedAt.Add(-time.Hour)}},
		"02-aa-00-00-00-06": fakeSession{},
	}
	return openStore(t, state), sessions
}

func testCollector(store model.StateStore, sessions map[string]session, options Options) *collector {
	options.Store, options.Manager = store, controller.NewManager()
	c := newCollector(options)
	c.sessions = func() map[string]session { return sessions }
	return c
}

var legacyFamilies = []string{
	"dismist_sites", "dismist_aps", "dismist_site_aps", "dismist_site_associated_clients",
	"dismist_ap_connected", "dismist_ap_associated_clients", "dismist_ap_connections_total",
	"dismist_ap_full_frames_total", "dismist_ap_fast_frames_total", "dismist_ap_uptime_seconds",
	"dismist_ap_desired_revision", "dismist_ap_applied_revision", "dismist_ap_last_seen_timestamp_seconds",
}

// TestCollectorMatchesLegacyExporter is the "same names and labels" check of
// the client_golang switch: for one fixture, the collector emits exactly the
// families, types, label sets and values of the hand-rolled exporter. Only the
// two frame counters' HELP changed ("decoded frames", design §6.2).
func TestCollectorMatchesLegacyExporter(t *testing.T) {
	store, sessions := fixture(t)
	want := legacyExposition(store.Snapshot(), sessions)
	want = strings.ReplaceAll(want, "frames received since", "frames decoded since")
	want = sortLabels(t, want)
	if got := strings.Count(want, "# TYPE "); got != len(legacyFamilies) {
		t.Fatalf("legacy exporter wrote %d families, want %d", got, len(legacyFamilies))
	}
	c := testCollector(store, sessions, Options{})
	// The legacy families must match exactly; families added since (the info
	// series) are checked by their own tests.
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), legacyFamilies...); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		name  string
		value float64
	}{
		{"dismist_ap_associated_clients", 2},
		{"dismist_ap_connected", 2},
		{"dismist_ap_connections_total", 9},
	} {
		got := sumFamily(t, c, check.name)
		if got != check.value {
			t.Errorf("sum(%s) = %v, want %v", check.name, got, check.value)
		}
	}
}

// sortLabels rewrites a text exposition with each sample's labels in name
// order, as client_golang writes them; the legacy exporter wrote {site, ap}.
func sortLabels(t *testing.T, exposition string) string {
	t.Helper()
	parser := expfmt.NewTextParser(prommodel.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(exposition))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	for _, name := range names {
		for _, metric := range families[name].Metric {
			sort.Slice(metric.Label, func(i, j int) bool { return metric.Label[i].GetName() < metric.Label[j].GetName() })
		}
		if _, err := expfmt.MetricFamilyToText(&out, families[name]); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}

func sumFamily(t *testing.T, c *collector, name string) float64 {
	t.Helper()
	out, err := testutil.CollectAndFormat(c, expfmt.TypeTextPlain, name)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		sum += value
	}
	return sum
}

func TestCollectorPassesLint(t *testing.T) {
	store, sessions := fixture(t)
	c := testCollector(store, sessions, Options{
		StoreSchema:   func() (int, int, bool) { return 2, 1, true },
		CompileStatus: func() map[string]CompileStatus { return nil },
	})
	problems, err := testutil.CollectAndLint(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range problems {
		t.Errorf("%s: %s", problem.Metric, problem.Text)
	}
}

func TestPhaseZeroSourcesAreOptional(t *testing.T) {
	store, sessions := fixture(t)
	optional := []string{"dismist_store_schema", "dismist_store_compat", "dismist_ap_config_compile_error", "dismist_ap_config_compile_errors_total"}
	if n := testutil.CollectAndCount(testCollector(store, sessions, Options{}), optional...); n != 0 {
		t.Fatalf("unwired sources emitted %d series", n)
	}
	unreadable := testCollector(store, sessions, Options{StoreSchema: func() (int, int, bool) { return 0, 0, false }})
	if n := testutil.CollectAndCount(unreadable, optional...); n != 0 {
		t.Fatalf("unreadable schema emitted %d series", n)
	}

	c := testCollector(store, sessions, Options{
		StoreSchema: func() (int, int, bool) { return 2, 1, true },
		CompileStatus: func() map[string]CompileStatus {
			return map[string]CompileStatus{"02-aa-00-00-00-02": {Failing: true, Failures: 3}, "not-an-ap": {Failing: true, Failures: 1}}
		},
	})
	want := `# HELP dismist_store_schema Data format version of the controller database (meta.schema).
# TYPE dismist_store_schema gauge
dismist_store_schema 2
# HELP dismist_store_compat Oldest database schema a dismist binary must support to run this database (meta.compat).
# TYPE dismist_store_compat gauge
dismist_store_compat 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "dismist_store_schema", "dismist_store_compat"); err != nil {
		t.Fatal(err)
	}
	// Every AP in the store gets both series; an ID the store does not know is ignored.
	if n := testutil.CollectAndCount(c, "dismist_ap_config_compile_error"); n != len(store.Snapshot().APs) {
		t.Fatalf("compile_error series = %d", n)
	}
	out, err := testutil.CollectAndFormat(c, expfmt.TypeTextPlain, "dismist_ap_config_compile_error", "dismist_ap_config_compile_errors_total")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`dismist_ap_config_compile_error{ap="02-aa-00-00-00-02",site="11111111-1111-4111-8111-111111111111"} 1`,
		`dismist_ap_config_compile_errors_total{ap="02-aa-00-00-00-02",site="11111111-1111-4111-8111-111111111111"} 3`,
		`dismist_ap_config_compile_error{ap="02-aa-00-00-00-01",site="11111111-1111-4111-8111-111111111111"} 0`,
		`dismist_ap_config_compile_errors_total{ap="02-aa-00-00-00-01",site="11111111-1111-4111-8111-111111111111"} 0`,
	} {
		if !strings.Contains(string(out), line+"\n") {
			t.Errorf("missing %s in\n%s", line, out)
		}
	}
	if strings.Contains(string(out), "not-an-ap") {
		t.Error("exported an AP the store does not know")
	}
}

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
	if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain; version=0.0.4; charset=utf-8") {
		t.Fatalf("content type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache control = %q", got)
	}
	body := response.Body.String()
	for _, want := range []string{
		"dismist_sites 2\n", "dismist_aps 1\n",
		"dismist_site_aps{site=\"site-a\"} 1\n",
		"dismist_site_aps{site=\"site-b\"} 0\n",
		"dismist_ap_connected{ap=\"ap-a\",site=\"site-a\"} 0\n",
		"dismist_ap_desired_revision{ap=\"ap-a\",site=\"site-a\"} 7\n",
		"go_goroutines ", "process_",
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

// TestDefaultModeLeaksNothing scrapes connected APs with clients and WLANs
// and checks no name, address, SSID, BSSID, MAC, firmware or PSK appears.
func TestDefaultModeLeaksNothing(t *testing.T) {
	store, sessions := fixture(t)
	c := testCollector(store, sessions, Options{})
	out, err := testutil.CollectAndFormat(c, expfmt.TypeTextPlain)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToLower(string(out))
	for _, sensitive := range []string{
		"example home", "lounge ap", "office ap", "192.0.2.5", "198.51.100.", "example",
		"02-aa-00-00-10-01", "02-aa-00-00-10-02", "02-aa-00-00-20-01", "phone", "apfw", "ap33",
		"correct horse", "net-1", "ssid", "psk", "mac",
	} {
		if strings.Contains(body, sensitive) {
			t.Errorf("scrape leaked %q", sensitive)
		}
	}
}

func TestScrapeEscapesLabelValuesAndRejectsOtherRoutes(t *testing.T) {
	store := openStore(t, model.State{Sites: []model.Site{{ID: "x\"\\\ny", Name: "site"}}})
	handler := New(store, controller.NewManager())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `dismist_site_aps{site="x\"\\\ny"} 0`) {
		t.Fatalf("unescaped scrape: %s", response.Body.String())
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{{http.MethodPost, "/metrics", http.StatusMethodNotAllowed}, {http.MethodGet, "/metrics/extra", http.StatusNotFound}, {http.MethodGet, "/", http.StatusNotFound}} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Errorf("%s %s: status = %d", test.method, test.path, response.Code)
		}
	}
}

// TestInvalidLabelValueKeepsTheScrape: a label value Prometheus cannot carry
// (invalid UTF-8) drops that series with a logged error, not the scrape.
func TestInvalidLabelValueKeepsTheScrape(t *testing.T) {
	store := openStore(t, model.State{
		Sites: []model.Site{{ID: "ok-site"}, {ID: "bad\xffsite"}},
		APs:   []model.APRecord{{ID: "ap-a", SiteID: "ok-site"}},
	})
	var logged bytes.Buffer
	handler, err := NewHandler(Options{Store: store, Manager: controller.NewManager(), ErrorLog: log.New(&logged, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{"dismist_sites 2\n", `dismist_site_aps{site="ok-site"} 1`, `dismist_ap_connected{ap="ap-a",site="ok-site"} 0`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if logged.Len() == 0 {
		t.Error("the invalid series was not logged")
	}
}

func TestScrapeRequiresBearerTokenWhenSet(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	store := openStore(t, model.State{Sites: []model.Site{{ID: "site-a"}}})
	handler, err := NewHandler(Options{Store: store, Manager: controller.NewManager(), Token: token})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		authorization string
		status        int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer", http.StatusUnauthorized},
		{"Bearer ", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Bearer " + token + "x", http.StatusUnauthorized},
		{"Bearer " + token[:len(token)-1], http.StatusUnauthorized},
		{"Basic " + token, http.StatusUnauthorized},
		{token, http.StatusUnauthorized},
		{"Bearer " + token, http.StatusOK},
		{"bearer " + token, http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if test.authorization != "" {
			request.Header.Set("Authorization", test.authorization)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("Authorization %q: status = %d, want %d", test.authorization, response.Code, test.status)
			continue
		}
		body := response.Body.String()
		if test.status == http.StatusUnauthorized {
			if response.Header().Get("WWW-Authenticate") == "" {
				t.Errorf("Authorization %q: no WWW-Authenticate challenge", test.authorization)
			}
			if strings.Contains(body, "dismist_") {
				t.Errorf("Authorization %q: unauthorized response carried metrics", test.authorization)
			}
		} else if !strings.Contains(body, "dismist_sites 1\n") {
			t.Errorf("Authorization %q: body = %s", test.authorization, body)
		}
	}
	// Other routes stay 404 without a token, so probing reveals nothing more.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/other", nil))
	if response.Code != http.StatusNotFound {
		t.Errorf("GET /other: status = %d", response.Code)
	}
}

func TestNewHandlerRejectsBadOptions(t *testing.T) {
	store := openStore(t, model.State{})
	manager := controller.NewManager()
	for name, options := range map[string]Options{
		"no store":       {Manager: manager},
		"no manager":     {Store: store},
		"unknown mode":   {Store: store, Manager: manager, Clients: "all"},
		"negative limit": {Store: store, Manager: manager, ClientLimit: -1},
		"token space":    {Store: store, Manager: manager, Token: "abcdefgh ijklmnop"},
	} {
		if _, err := NewHandler(options); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, mode := range []ClientMode{"", ClientsOff, ClientsAggregate, ClientsPerClient} {
		if _, err := NewHandler(Options{Store: store, Manager: manager, Clients: mode, Names: true, ClientLimit: 100}); err != nil {
			t.Errorf("mode %q: %v", mode, err)
		}
	}
}

func TestReadTokenFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good", "0123456789abcdef0123456789abcdef\n", 0o600)
	token, err := ReadTokenFile(good)
	if err != nil || token != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("ReadTokenFile = %q, %v", token, err)
	}
	if _, err := ReadTokenFile(write("readonly", "0123456789abcdef", 0o400)); err != nil {
		t.Errorf("0400 file: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"group readable": write("group", "0123456789abcdef0123456789abcdef", 0o640),
		"world readable": write("world", "0123456789abcdef0123456789abcdef", 0o604),
		"short":          write("short", "abc\n", 0o600),
		"empty":          write("empty", "\n", 0o600),
		"two words":      write("words", "0123456789abcdef 0123456789abcdef", 0o600),
		"two lines":      write("lines", "0123456789abcdef\n0123456789abcdef\n", 0o600),
		"symlink":        link,
		"directory":      dir,
		"missing":        filepath.Join(dir, "missing"),
	} {
		if _, err := ReadTokenFile(path); err == nil {
			t.Errorf("%s: accepted", name)
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
