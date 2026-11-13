package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

func apiFixture(t *testing.T, allowedHosts ...string) (http.Handler, *model.Store, *controller.Server) {
	t.Helper()
	store, err := model.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *model.State) error {
		state.APs = []model.APRecord{{ID: "ap-1", Name: "bench", SiteID: "lab", SecretFile: "private/secret.bin"}}
		state.Networks = []model.Network{{ID: "wlan-1", SSID: "TestSSID", PSK: "example-passphrase-11", VLANID: 123, Enabled: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"ap-1","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Enabled":true,"Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-04"}}]}`)
	control, err := controller.NewServer(controller.Options{
		AllowedIP: "127.0.0.1", ID: "ap-1", CertDER: []byte{1},
		Config: base, ServerName: "lab", Revision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(store, control, base, "ap-1", allowedHosts...), store, control
}

func request(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8088"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestListsRedactPasswordsAndSecretFiles(t *testing.T) {
	handler, _, _ := apiFixture(t)
	for _, path := range []string{"/api/networks", "/api/aps"} {
		response := request(t, handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("example-passphrase-11")) ||
			bytes.Contains(response.Body.Bytes(), []byte("private/secret.bin")) {
			t.Fatalf("secret leaked from %s: %s", path, response.Body.String())
		}
	}
}

func TestServesEmbeddedReactEntry(t *testing.T) {
	handler, _, _ := apiFixture(t)
	response := request(t, handler, http.MethodGet, "/", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("React entry missing: %d %s", response.Code, response.Body.String())
	}
}

func TestExplicitTailnetHostCanUseUIAndAPI(t *testing.T) {
	handler, _, _ := apiFixture(t, "nicks-macbook-pro.ison-beta.ts.net")
	req := httptest.NewRequest(http.MethodGet, "/api/networks", nil)
	req.Host = "nicks-macbook-pro.ison-beta.ts.net"
	req.Header.Set("Origin", "https://nicks-macbook-pro.ison-beta.ts.net")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("tailnet host rejected: %d", response.Code)
	}
	req.Host = "unlisted.ison-beta.ts.net"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unlisted host accepted: %d", response.Code)
	}
}

func TestTelemetryClientAndEventEndpoints(t *testing.T) {
	handler, _, _ := apiFixture(t)
	for _, path := range []string{"/api/telemetry", "/api/clients", "/api/events"} {
		response := request(t, handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, response.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body) == 0 {
			t.Fatalf("%s returned invalid JSON: %v", path, err)
		}
	}
	clients := request(t, handler, http.MethodGet, "/api/clients", "")
	var body map[string]json.RawMessage
	if err := json.Unmarshal(clients.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["recent_activity"]; !ok {
		t.Fatal("client API did not distinguish association from recent activity")
	}
}

func TestEditNetworkUpdatesPersistentStateAndAPConfig(t *testing.T) {
	handler, store, control := apiFixture(t)
	response := request(t, handler, http.MethodPut, "/api/networks/wlan-1",
		`{"ssid":"TestSSID-2","vlan_id":124,"enabled":true,"psk":"example-passphrase-01"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", response.Code, response.Body.String())
	}
	state := store.Snapshot()
	if state.Revision != 2 || state.Networks[0].VLANID != 124 || state.Networks[0].SSID != "TestSSID-2" {
		t.Fatalf("state was not updated: %+v", state)
	}
	config, revision := control.ConfigSnapshot()
	if revision != 2 {
		t.Fatalf("short AP config revision %d", revision)
	}
	var parsed struct {
		WLANs []struct {
			SSID  string   `json:"SSID"`
			Vlans []string `json:"Vlans"`
		} `json:"WLANs"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil || parsed.WLANs[0].SSID != "TestSSID-2" || parsed.WLANs[0].Vlans[0] != "124" {
		t.Fatalf("AP config was not rebuilt: %+v %v", parsed, err)
	}
	bad := request(t, handler, http.MethodPut, "/api/networks/wlan-1",
		`{"ssid":"invalid","vlan_id":4095,"enabled":true}`)
	if bad.Code != http.StatusBadRequest || store.Snapshot().Revision != 2 {
		t.Fatalf("invalid update changed state: %d %+v", bad.Code, store.Snapshot())
	}
}

func TestNetworkEditPersistsThroughBoltRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Lab A"}}
		state.APs = []model.APRecord{{ID: "ap-1", SiteID: "site-a", SecretFile: "secret.bin"}}
		state.Networks = []model.Network{{ID: "wlan-1", SiteID: "site-a", SSID: "TestSSID", PSK: "example-passphrase-11", VLANID: 123, Enabled: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"ap-1","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Enabled":true,"Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-04"}}]}`)
	control, err := controller.NewServer(controller.Options{AllowedIP: "127.0.0.1", ID: "ap-1", CertDER: []byte{1}, Config: base})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store, control, base, "ap-1")
	sites := request(t, handler, http.MethodGet, "/api/sites", "")
	if sites.Code != http.StatusOK || !bytes.Contains(sites.Body.Bytes(), []byte("Lab A")) {
		t.Fatalf("site listing missing: %d %s", sites.Code, sites.Body.String())
	}
	if err := store.RecordObservation(model.Observation{SiteID: "site-a", APID: "ap-1", At: time.Now().UTC(), Connected: true}); err != nil {
		t.Fatal(err)
	}
	history := request(t, handler, http.MethodGet, "/api/sites/site-a/aps/ap-1/samples", "")
	if history.Code != http.StatusOK || !bytes.Contains(history.Body.Bytes(), []byte("site-a")) {
		t.Fatalf("site history missing: %d %s", history.Code, history.Body.String())
	}
	shortSite := request(t, handler, http.MethodGet, "/api/sites/site-b/aps/ap-1/samples", "")
	if shortSite.Code != http.StatusNotFound {
		t.Fatalf("cross-site history accessible: %d", shortSite.Code)
	}
	response := request(t, handler, http.MethodPut, "/api/networks/wlan-1", `{"ssid":"TestSSID-2"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("edit failed: %d %s", response.Code, response.Body.String())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Snapshot(); got.Revision != 2 || got.Networks[0].SSID != "TestSSID-2" || got.Networks[0].SiteID != "site-a" {
		t.Fatalf("bbolt edit not persisted: %+v", got)
	}
}

func TestSecondSiteNetworkDoesNotChangeBenchAP(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Lab A"}}
		state.APs = []model.APRecord{{ID: "ap-1", SiteID: "site-a", SecretFile: "secret.bin"}}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "TestSSID", PSK: "example-passphrase-11", VLANID: 123}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"ap-1","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Enabled":true,"Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-04"}}]}`)
	control, err := controller.NewServer(controller.Options{AllowedIP: "127.0.0.1", ID: "ap-1", CertDER: []byte{1}, Config: base, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store, control, base, "ap-1")
	created := request(t, handler, http.MethodPost, "/api/sites", `{"name":"Lab B","country":"AU"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("site creation failed: %d %s", created.Code, created.Body.String())
	}
	var siteResult struct {
		Site struct {
			ID string `json:"id"`
		} `json:"site"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &siteResult); err != nil || siteResult.Site.ID == "" {
		t.Fatalf("new site missing ID: %v", err)
	}
	path := "/api/sites/" + siteResult.Site.ID + "/networks"
	response := request(t, handler, http.MethodPost, path, `{"ssid":"OTHER-LAB","psk":"example-passphrase-06","vlan_id":124,"enabled":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("second site WLAN failed: %d %s", response.Code, response.Body.String())
	}
	var createdNetwork struct {
		Networks []struct {
			ID string `json:"id"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &createdNetwork); err != nil || len(createdNetwork.Networks) != 1 {
		t.Fatalf("new WLAN ID missing: %v", err)
	}
	networkPath := path + "/" + createdNetwork.Networks[0].ID
	edited := request(t, handler, http.MethodPut, networkPath, `{"ssid":"OTHER-LAB-2"}`)
	if edited.Code != http.StatusOK || !bytes.Contains(edited.Body.Bytes(), []byte("OTHER-LAB-2")) {
		t.Fatalf("site WLAN edit failed: %d %s", edited.Code, edited.Body.String())
	}
	_, revision := control.ConfigSnapshot()
	if revision != 1 {
		t.Fatalf("site B edit changed site A AP revision: %d", revision)
	}
	labA := request(t, handler, http.MethodGet, "/api/networks", "")
	if bytes.Contains(labA.Body.Bytes(), []byte("OTHER-LAB")) {
		t.Fatalf("site B WLAN leaked into site A list: %s", labA.Body.String())
	}
	labB := request(t, handler, http.MethodGet, path, "")
	if labB.Code != http.StatusOK || !bytes.Contains(labB.Body.Bytes(), []byte("OTHER-LAB-2")) {
		t.Fatalf("site B WLAN missing: %d %s", labB.Code, labB.Body.String())
	}
	deleted := request(t, handler, http.MethodDelete, networkPath, "")
	if deleted.Code != http.StatusOK || bytes.Contains(deleted.Body.Bytes(), []byte("OTHER-LAB-2")) {
		t.Fatalf("site WLAN delete failed: %d %s", deleted.Code, deleted.Body.String())
	}
}

func TestSiteAdvancedPatchIsAppliedAndPreviewRedactsSecrets(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Lab A"}}
		state.APs = []model.APRecord{{ID: "ap-1", SiteID: "site-a", SecretFile: "private/secret.bin"}}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "TestSSID", PSK: "example-passphrase-11", VLANID: 123}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"ap-1","Radio":{"Power":10},"WLANs":[{"ID":"old","SSID":"OLD","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-04"}}]}`)
	control, err := controller.NewServer(controller.Options{AllowedIP: "127.0.0.1", ID: "ap-1", CertDER: []byte{1}, Config: base, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store, control, base, "ap-1")
	response := request(t, handler, http.MethodPut, "/api/sites/site-a/advanced", `{"patch":{"Radio":{"Power":13},"UnknownFeature":{"enabled":true}}}`)
	if response.Code != http.StatusOK {
		t.Fatalf("site patch rejected: %d %s", response.Code, response.Body.String())
	}
	preview := request(t, handler, http.MethodGet, "/api/sites/site-a/aps/ap-1/config", "")
	if preview.Code != http.StatusOK || !bytes.Contains(preview.Body.Bytes(), []byte("UnknownFeature")) ||
		!bytes.Contains(preview.Body.Bytes(), []byte(`"Power":13`)) || bytes.Contains(preview.Body.Bytes(), []byte("example-passphrase-11")) ||
		bytes.Contains(preview.Body.Bytes(), []byte("example-passphrase-04")) {
		t.Fatalf("effective config preview short or leaked secret: %d %s", preview.Code, preview.Body.String())
	}
	apPatch := request(t, handler, http.MethodPut, "/api/sites/site-a/aps/ap-1/advanced", `{"patch":{"Radio":{"Power":14}}}`)
	if apPatch.Code != http.StatusOK {
		t.Fatalf("AP advanced patch failed: %d", apPatch.Code)
	}
	networkPatch := request(t, handler, http.MethodPut, "/api/sites/site-a/networks/wlan-a/advanced", `{"patch":{"RoamMode":"11r"}}`)
	if networkPatch.Code != http.StatusOK {
		t.Fatalf("WLAN advanced patch failed: %d", networkPatch.Code)
	}
	networkRead := request(t, handler, http.MethodGet, "/api/sites/site-a/networks/wlan-a/advanced", "")
	if networkRead.Code != http.StatusOK || !bytes.Contains(networkRead.Body.Bytes(), []byte("11r")) {
		t.Fatalf("WLAN advanced patch did not round-trip: %d %s", networkRead.Code, networkRead.Body.String())
	}
	preview = request(t, handler, http.MethodGet, "/api/sites/site-a/aps/ap-1/config", "")
	if !bytes.Contains(preview.Body.Bytes(), []byte(`"Power":14`)) || !bytes.Contains(preview.Body.Bytes(), []byte(`"RoamMode":"11r"`)) {
		t.Fatalf("effective AP/WLAN patches missing: %s", preview.Body.String())
	}
	bad := request(t, handler, http.MethodPut, "/api/sites/site-a/advanced", `{"patch":[1,2]}`)
	if bad.Code != http.StatusBadRequest || store.Snapshot().Revision != 4 {
		t.Fatalf("invalid patch changed state: %d %+v", bad.Code, store.Snapshot())
	}
}
