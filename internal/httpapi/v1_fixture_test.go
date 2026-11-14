package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// v1Fixture is a bbolt store with the live-like Home site (synthetic
// names and addresses), a second site, and a control session per AP.
type v1Fixture struct {
	server   *Server
	store    *model.BoltStore
	controls map[string]*controller.Server
	manager  *controller.Manager
	base     []byte
}

const (
	homeAP  = "02-aa-00-00-00-01"
	otherAP = "02-aa-00-00-00-02"
)

// liveSitePatch is the shape of the production site's advanced patch: a
// full Radios[] replacement with AU/890, 5 GHz 149/80 at 17 dBm and
// 2.4 GHz 1/20 at 12 dBm, plus Monitor.CCRev AU/890.
const liveSitePatch = `{"Radios":[{"Band":"5","CCRev":"AU/890","CCRevSP":"AU/890","Channel":149,"Width":80,"Power":17,"RRM":{"Channel":149,"Width":80,"Power":17}},{"Band":"2.4","CCRev":"AU/890","CCRevSP":"AU/890","Channel":1,"Width":20,"Power":12,"RRM":{"Channel":1,"Width":20,"Power":12}}],"Monitor":{"CCRev":"AU/890"}}`

// liveNetworkPatch is the production network's advanced patch: WPA2/WPA3
// transition.
const liveNetworkPatch = `{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}`

func newV1Fixture(t *testing.T) *v1Fixture {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{
			{ID: "home", Name: "Home", Country: "AU", Timezone: "Australia/Sydney", Advanced: json.RawMessage(liveSitePatch)},
			{ID: "other", Name: "Other", Country: "AU"},
		}
		state.APs = []model.APRecord{
			{ID: homeAP, Name: "Hallway", SiteID: "home", Secret: bytes.Repeat([]byte{1}, 32), ConfigRevision: 1, ConfigUpdatedAt: configTime},
			{ID: otherAP, Name: "Bench", SiteID: "other", Secret: bytes.Repeat([]byte{2}, 32), ConfigRevision: 1, ConfigUpdatedAt: configTime},
		}
		state.Networks = []model.Network{
			{ID: "net-home", SiteID: "home", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true, Advanced: json.RawMessage(liveNetworkPatch)},
			{ID: "net-other", SiteID: "other", SSID: "Example-Other", PSK: "example-passphrase-02", VLANID: 20, Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"x","Timestamp":1,"Country":"","SwitchPorts":[{"Name":"eth0","Vlans":["1"]}],` +
		`"Radios":[{"Band":"2.4","Channel":1,"Width":20,"Power":0},{"Band":"5","Channel":36,"Width":20,"Power":0}],` +
		`"WLANs":[{"ID":"t","SSID":"T","Enabled":true,"Vlans":["1"],"Bands":["2.4","5"],` +
		`"BandConfigs":[{"Band":"2.4","BSSIDIndex":0},{"Band":"5","BSSIDIndex":0}],"Auth":{"Type":"psk","PSK":"example-passphrase-00"}}]}`)
	f := &v1Fixture{store: store, controls: map[string]*controller.Server{}, manager: controller.NewManager(), base: base}
	for _, served := range compile.Prepare(base, store.Snapshot(), nil) {
		control, err := controller.NewServer(controller.Options{ID: served.APID, CertDER: []byte{1}, Config: served.Config, Revision: served.Revision})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Register(served.APID, "", control); err != nil {
			t.Fatal(err)
		}
		f.controls[served.APID] = control
	}
	f.server = NewWithManager(store, f.manager, base, homeAP, "*")
	return f
}

// call makes a v1 request (no legacy Referer).
func (f *v1Fixture) call(t *testing.T, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	return v1request(t, f.server, method, path, body, headers...)
}

func v1request(t *testing.T, handler http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8088"
	req.Header.Set("Accept", "application/json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for index := 0; index+1 < len(headers); index += 2 {
		req.Header.Set(headers[index], headers[index+1])
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func decodeInto[T any](t *testing.T, response *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status %d, want %d: %s", response.Code, want, response.Body.String())
	}
	var value T
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %T: %v: %s", value, err, response.Body.String())
	}
	return value
}

func errorOf(t *testing.T, response *httptest.ResponseRecorder, status int, code api.ErrorCode) api.ErrorBody {
	t.Helper()
	body := decodeInto[api.ErrorBody](t, response, status)
	if body.Code != code {
		t.Fatalf("code %q, want %q: %s", body.Code, code, response.Body.String())
	}
	return body
}

// rawRecords returns the stored JSON of every site, AP and network.
func (f *v1Fixture) rawRecords(t *testing.T) map[string]string {
	t.Helper()
	result := map[string]string{}
	state := f.store.Snapshot()
	for _, site := range state.Sites {
		result["site/"+site.ID] = string(recordKey(site, "advanced"))
	}
	for _, network := range state.Networks {
		result["network/"+network.ID] = string(recordKey(network, "advanced"))
	}
	for _, ap := range state.APs {
		result["ap/"+ap.ID+"/rev"] = fmt.Sprint(ap.ConfigRevision)
	}
	return result
}

func (f *v1Fixture) hash(t *testing.T, apID string) string {
	t.Helper()
	view := decodeInto[api.APConfigView](t, f.call(t, http.MethodGet, "/api/aps/"+apID+"/config?view=normalised", ""), http.StatusOK)
	if view.EffectiveHash == "" {
		t.Fatal("no effective hash")
	}
	return view.EffectiveHash
}

func (f *v1Fixture) updatedRev(kind, id string) uint64 {
	state := f.store.Snapshot()
	switch kind {
	case "site":
		if site := findSite(state, id); site != nil {
			return site.UpdatedRev
		}
	case "ap":
		if ap := findAP(state, id); ap != nil {
			return ap.UpdatedRev
		}
	case "network":
		for _, network := range state.Networks {
			if network.ID == id {
				return network.UpdatedRev
			}
		}
	}
	return 0
}

func ifMatchHeader(rev uint64) []string { return []string{"If-Match", fmt.Sprintf(`"%d"`, rev)} }

// fakeMigration reports a fixed migration state.
type fakeMigration struct {
	mu       sync.Mutex
	state    api.MigrationState
	retries  []bool
	finalise []bool
	at       time.Time
}

func (f *fakeMigration) Status() api.MigrationStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	status := api.MigrationStatus{MigrationView: api.MigrationView{State: f.state}, Paths: []string{}, Schema: 2, Compat: 1}
	if !f.at.IsZero() {
		at := f.at
		status.At = &at
		after := at.Add(finaliseAfter)
		status.FinaliseAfter = &after
		status.CanFinalise = time.Now().After(after)
	}
	return status
}

func (f *fakeMigration) Retry(dryRun bool) (api.MigrationStatus, error) {
	f.mu.Lock()
	f.retries = append(f.retries, dryRun)
	if !dryRun {
		f.state = api.MigrationStateCommitted
	}
	f.mu.Unlock()
	return f.Status(), nil
}

func (f *fakeMigration) Finalise(dryRun bool) (api.MigrationStatus, error) {
	f.mu.Lock()
	f.finalise = append(f.finalise, dryRun)
	f.mu.Unlock()
	return f.Status(), nil
}

// fakeHistory is an in-memory config_history.
type fakeHistory struct {
	mu      sync.Mutex
	entries []ChangeEntry
	next    uint64
}

func (h *fakeHistory) Append(_ *model.Tx, state model.State, entry ChangeEntry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	snapshot := map[string]any{"sites": []any{}, "networks": []any{}}
	for _, site := range state.Sites {
		if site.ID == entry.SiteID {
			snapshot["sites"] = append(snapshot["sites"].([]any), map[string]any{"id": site.ID, "name": site.Name})
		}
	}
	for _, network := range state.Networks {
		if network.SiteID == entry.SiteID {
			snapshot["networks"] = append(snapshot["networks"].([]any), map[string]any{"id": network.ID, "ssid": network.SSID, "psk": "hmac:0123456789abcdef"})
		}
	}
	entry.Snapshot, _ = json.Marshal(snapshot)
	h.entries = append(h.entries, entry)
	return nil
}

func (h *fakeHistory) List(siteID string, before uint64, limit int) ([]ChangeEntry, uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []ChangeEntry
	for index := len(h.entries) - 1; index >= 0; index-- {
		entry := h.entries[index]
		if entry.SiteID != siteID || (before != 0 && entry.Revision >= before) {
			continue
		}
		if len(result) == limit {
			return result, result[len(result)-1].Revision, nil
		}
		result = append(result, entry)
	}
	return result, 0, nil
}

func (h *fakeHistory) Get(revision uint64) (ChangeEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, entry := range h.entries {
		if entry.Revision == revision && entry.Kind != "action" {
			return entry, nil
		}
	}
	return ChangeEntry{}, model.ErrNotFound
}

func (h *fakeHistory) AppendAction(entry ChangeEntry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	entry.Revision = 1_000_000 + h.next
	h.entries = append(h.entries, entry)
	return nil
}

func (h *fakeHistory) SetResult(revision uint64, apID string, result api.APState) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index := range h.entries {
		if h.entries[index].Revision == revision {
			if h.entries[index].Results == nil {
				h.entries[index].Results = map[string]api.APState{}
			}
			h.entries[index].Results[apID] = result
			return nil
		}
	}
	return model.ErrNotFound
}

func (h *fakeHistory) summaries() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var result []string
	for _, entry := range h.entries {
		result = append(result, entry.Kind+": "+entry.Summary)
	}
	sort.Strings(result)
	return result
}

// fakeCommands records ap-cmd-rx commands.
type fakeCommands struct {
	mu   sync.Mutex
	sent []APCommand
}

func (c *fakeCommands) SendCommand(apID string, command APCommand) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, command)
	return fmt.Sprintf("cmd-%d", len(c.sent)), nil
}

// fakeClientMeta is an in-memory client_meta.
type fakeClientMeta struct {
	mu   sync.Mutex
	meta map[string]api.ClientMeta
}

func (m *fakeClientMeta) Get(mac string) (api.ClientMeta, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.meta[mac]
	return value, ok, nil
}

func (m *fakeClientMeta) Put(meta api.ClientMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.meta == nil {
		m.meta = map[string]api.ClientMeta{}
	}
	m.meta[meta.MAC] = meta
	return nil
}

func (m *fakeClientMeta) All() (map[string]api.ClientMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := map[string]api.ClientMeta{}
	for key, value := range m.meta {
		result[key] = value
	}
	return result, nil
}
