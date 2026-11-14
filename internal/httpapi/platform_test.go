package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/proxytrust"
	"github.com/nicko170/dismist/internal/telemetry"
)

func platformFixture(t *testing.T) (*Server, *model.BoltStore, *controller.Server) {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
		state.APs = []model.APRecord{{ID: "ap-a", SiteID: "a", SecretFile: "secret"}}
		state.Networks = []model.Network{{ID: "wlan-a", SiteID: "a", SSID: "Lab", PSK: "example-passphrase-11", VLANID: 123, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"SiteID":"template-site","WLANs":[{"Auth":{"Type":"psk","PSK":"example-passphrase-16"}}]}`)
	control, err := controller.NewServer(controller.Options{AllowedIP: "127.0.0.1", ID: "ap-a", CertDER: []byte{1}, Config: base, Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	manager := controller.NewManager()
	if err := manager.Register("ap-a", "127.0.0.1", control); err != nil {
		t.Fatal(err)
	}
	return NewWithManager(store, manager, base, "ap-a", "*"), store, control
}

func TestMovedAPRetainsOldSiteHistoryWithoutLiveAccess(t *testing.T) {
	handler, store, _ := platformFixture(t)
	if err := store.RecordObservation(model.Observation{SiteID: "a", APID: "ap-a", At: time.Now().UTC(), Connected: true,
		Events: []model.EventSnapshot{{Kind: "client", Title: "Associated"}}}); err != nil {
		t.Fatal(err)
	}
	moved := request(t, handler, http.MethodPut, "/api/aps/ap-a/site", `{"site_id":"b"}`)
	if moved.Code != http.StatusOK {
		t.Fatalf("move: %d %s", moved.Code, moved.Body.String())
	}
	for _, path := range []string{"/api/sites/a/aps/ap-a/samples", "/api/sites/a/aps/ap-a/history/events"} {
		response := request(t, handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"site_id":"a"`)) {
			t.Fatalf("old history missing at %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if response := request(t, handler, http.MethodGet, "/api/sites/a/aps/ap-a/clients", ""); response.Code != http.StatusNotFound {
		t.Fatalf("old site exposes live AP: %d", response.Code)
	}
	if response := request(t, handler, http.MethodGet, "/api/sites/b/aps/ap-a/history/events", ""); response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("Associated")) {
		t.Fatalf("history crossed sites: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/sites/a/history/events", "/api/sites/a/history/samples"} {
		response := request(t, handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"site_id":"a"`)) {
			t.Fatalf("site history missing at %s: %d %s", path, response.Code, response.Body.String())
		}
	}
	if response := request(t, handler, http.MethodGet, "/api/sites/b/history/events", ""); response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("Associated")) {
		t.Fatalf("site B history crossed sites: %d %s", response.Code, response.Body.String())
	}
}

func TestSiteClientInventoryAndAPCountAreExplicitlyEmpty(t *testing.T) {
	handler, _, _ := platformFixture(t)
	response := request(t, handler, http.MethodGet, "/api/sites/a/clients", "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"clients":[]`)) {
		t.Fatalf("site clients: %d %s", response.Code, response.Body.String())
	}
	response = request(t, handler, http.MethodGet, "/api/aps", "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"client_count":0`)) {
		t.Fatalf("AP count missing: %d %s", response.Code, response.Body.String())
	}
}

func TestSiteClientEntryIncludesAPIdentity(t *testing.T) {
	raw, err := json.Marshal(siteClient{Client: telemetry.Client{MAC: "02:aa:00:00:00:06"}, APID: "ap-a", APName: "Office AP"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"ap_id":"ap-a"`)) || !bytes.Contains(raw, []byte(`"ap_name":"Office AP"`)) {
		t.Fatalf("site client lacks AP identity: %s", raw)
	}
}

func TestSiteReadRedactsAdvancedSecrets(t *testing.T) {
	handler, store, _ := platformFixture(t)
	if err := store.Update(func(state *model.State) error {
		state.Sites[0].Advanced = json.RawMessage(`{"Unknown":{"passphrase":"example-passphrase-11","api_key":"private-key","shared_key":"shared-private","credential":"private-credential","mode":"custom"}}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := request(t, handler, http.MethodGet, "/api/sites/a", "")
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("example-passphrase-11")) || bytes.Contains(response.Body.Bytes(), []byte("private-key")) || bytes.Contains(response.Body.Bytes(), []byte("shared-private")) || bytes.Contains(response.Body.Bytes(), []byte("private-credential")) || !bytes.Contains(response.Body.Bytes(), []byte("custom")) {
		t.Fatalf("site advanced read: %d %s", response.Code, response.Body.String())
	}
}

func TestSavingRedactedAdvancedPatchPreservesStoredSecret(t *testing.T) {
	handler, store, _ := platformFixture(t)
	if err := store.Update(func(state *model.State) error {
		state.Sites[0].Advanced = json.RawMessage(`{"Unknown":{"passphrase":"example-passphrase-11","mode":"old"}}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := request(t, handler, http.MethodGet, "/api/sites/a", "")
	var result struct {
		Site struct {
			Advanced map[string]any `json:"advanced"`
		} `json:"site"`
	}
	if err := json.Unmarshal(read.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	unknown := result.Site.Advanced["Unknown"].(map[string]any)
	unknown["mode"] = "new"
	body, _ := json.Marshal(map[string]any{"patch": result.Site.Advanced})
	response := request(t, handler, http.MethodPut, "/api/sites/a/advanced", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("redacted patch save: %d %s", response.Code, response.Body.String())
	}
	stored := store.Snapshot().Sites[0].Advanced
	if !bytes.Contains(stored, []byte("example-passphrase-11")) || !bytes.Contains(stored, []byte(`"mode":"new"`)) || bytes.Contains(stored, []byte("[redacted]")) {
		t.Fatalf("stored secret changed: %s", stored)
	}
}

func TestEmptyInventoryLegacyEndpointsReturnEmptyData(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "empty.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := NewWithManager(store, controller.NewManager(), []byte(`{"WLANs":[{}]}`), "", "*")
	for _, path := range []string{"/api/status", "/api/telemetry", "/api/clients", "/api/events"} {
		response := request(t, handler, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		if !json.Valid(response.Body.Bytes()) {
			t.Fatalf("%s returned invalid JSON", path)
		}
	}
}

func TestSiteMetadataUpdatesWithoutChangingOtherSite(t *testing.T) {
	handler, store, control := platformFixture(t)
	response := request(t, handler, http.MethodPut, "/api/sites/a", `{"name":"Lab A","country":"AU","timezone":"Australia/Sydney"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update: %d %s", response.Code, response.Body.String())
	}
	state := store.Snapshot()
	if state.Sites[0].Name != "Lab A" || state.Sites[0].Country != "AU" || state.Sites[0].Timezone != "Australia/Sydney" || state.Sites[1].Name != "B" {
		t.Fatalf("sites: %+v", state.Sites)
	}
	if _, revision := control.ConfigSnapshot(); revision != state.APs[0].ConfigRevision {
		t.Fatalf("config revision mismatch: %d %d", revision, state.APs[0].ConfigRevision)
	}
	bad := request(t, handler, http.MethodPut, "/api/sites/a", `{"name":" "}`)
	if bad.Code != http.StatusBadRequest || store.Snapshot().Revision != state.Revision {
		t.Fatalf("invalid edit committed: %d", bad.Code)
	}
	for _, body := range []string{`{"country":"A1"}`, `{"timezone":"Not/A_Zone"}`} {
		bad = request(t, handler, http.MethodPut, "/api/sites/a", body)
		if bad.Code != http.StatusBadRequest || store.Snapshot().Revision != state.Revision {
			t.Fatalf("invalid metadata accepted: %d %s", bad.Code, body)
		}
	}
}

func TestSiteCreationRejectsInvalidMetadata(t *testing.T) {
	handler, store, _ := platformFixture(t)
	before := store.Snapshot().Revision
	for _, body := range []string{`{"name":"Bad","country":"A1"}`, `{"name":"Bad","timezone":"Not/A_Zone"}`, `{"name":"Bad"}{"name":"Extra"}`} {
		response := request(t, handler, http.MethodPost, "/api/sites", body)
		if response.Code != http.StatusBadRequest || store.Snapshot().Revision != before {
			t.Fatalf("invalid site created: %d %s", response.Code, body)
		}
	}
}

func TestMovedAPLiveSnapshotExcludesPreviousSiteData(t *testing.T) {
	movedAt := time.Now().UTC()
	ap := model.APRecord{ConfigUpdatedAt: movedAt}
	old := controller.TelemetrySnapshot{LastFull: movedAt.Add(-time.Minute),
		Events: []controller.ConnectionEvent{{When: movedAt.Add(-time.Minute), Title: "old"}, {When: movedAt.Add(time.Second), Title: "new"}}}
	if liveTelemetry(ap, controller.Status{Connected: true}, old).LastFull.IsZero() == false {
		t.Fatal("old telemetry remained visible")
	}
	events := liveEvents(ap, old.Events)
	if len(events) != 1 || events[0].Title != "new" {
		t.Fatalf("old site event leaked: %+v", events)
	}
	old.LastFull = movedAt.Add(time.Second)
	if liveTelemetry(ap, controller.Status{Connected: true}, old).LastFull.IsZero() {
		t.Fatal("new telemetry hidden")
	}
}

func TestLiveSnapshotWaitsForTelemetryAfterAPReconnect(t *testing.T) {
	now := time.Now().UTC()
	ap := model.APRecord{ConfigUpdatedAt: now.Add(-time.Hour)}
	status := controller.Status{Connected: true, ConnectedAt: now}
	previous := controller.TelemetrySnapshot{LastFast: now.Add(-time.Minute),
		AssociatedClients: []telemetry.Client{{MAC: "02:aa:00:00:00:02"}}}
	if got := liveTelemetry(ap, status, previous); len(got.AssociatedClients) != 0 {
		t.Fatalf("old client remained live after reconnect: %+v", got.AssociatedClients)
	}
	previous.LastFast = now.Add(time.Second)
	if got := liveTelemetry(ap, status, previous); len(got.AssociatedClients) != 1 {
		t.Fatalf("fresh client telemetry was hidden: %+v", got.AssociatedClients)
	}
}

func TestUnassignAPClearsSiteConfigAndKeepsInventory(t *testing.T) {
	handler, store, control := platformFixture(t)
	response := request(t, handler, http.MethodPut, "/api/aps/ap-a/site", `{"site_id":""}`)
	if response.Code != http.StatusOK {
		t.Fatalf("unassign: %d %s", response.Code, response.Body.String())
	}
	if store.Snapshot().APs[0].SiteID != "" {
		t.Fatal("AP still assigned")
	}
	config, revision := control.ConfigSnapshot()
	if !bytes.Contains(config, []byte(`"SiteID":""`)) || !bytes.Contains(config, []byte(`"WLANs":[]`)) || revision != store.Snapshot().APs[0].ConfigRevision {
		t.Fatalf("stale unassigned config: %s rev %d", config, revision)
	}
	if got := request(t, handler, http.MethodGet, "/api/aps", ""); got.Code != http.StatusOK || !bytes.Contains(got.Body.Bytes(), []byte(`"site_id":""`)) {
		t.Fatalf("unassigned AP missing: %d %s", got.Code, got.Body.String())
	}
	if got := request(t, handler, http.MethodGet, "/api/sites/a/aps/ap-a/status", ""); got.Code != http.StatusNotFound {
		t.Fatalf("old site still sees AP: %d", got.Code)
	}
}

func TestOfflineAPConfigValidationLeavesStateUnchanged(t *testing.T) {
	handler, store, _ := platformFixture(t)
	handler.manager.Remove("ap-a")
	before := store.Snapshot()
	response := request(t, handler, http.MethodPut, "/api/sites/a/networks/wlan-a/advanced", `{"patch":{"Auth":null}}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid patch accepted: %d %s", response.Code, response.Body.String())
	}
	after := store.Snapshot()
	if after.Revision != before.Revision || len(after.Networks[0].Advanced) != 0 || after.APs[0].ConfigRevision != before.APs[0].ConfigRevision {
		t.Fatalf("invalid offline config committed: %+v", after)
	}
}

func TestMutationBodiesRejectTrailingJSON(t *testing.T) {
	handler, store, _ := platformFixture(t)
	before := store.Snapshot().Revision
	for _, mutation := range []struct{ path, body string }{
		{"/api/aps/ap-a/site", `{"site_id":"b"}{"site_id":"a"}`},
		{"/api/sites/a/advanced", `{"patch":{"Radio":{"Power":1}}}{"patch":null}`},
	} {
		response := request(t, handler, http.MethodPut, mutation.path, mutation.body)
		if response.Code != http.StatusBadRequest || store.Snapshot().Revision != before {
			t.Fatalf("trailing JSON accepted at %s: %d %s", mutation.path, response.Code, response.Body.String())
		}
	}
}

func TestRegisterAPStoresPrivateRecordAndReturnsRedactedView(t *testing.T) {
	handler, store, _ := platformFixture(t)
	secretBytes := bytes.Repeat([]byte{7}, 32)
	secret := hex.EncodeToString(secretBytes)
	input, _ := json.Marshal(map[string]string{"id": "02:AA:00:00:00:06", "name": "Office AP", "model": "AP33", "management_ip": "192.0.2.10", "site_id": "b", "secret": secret})
	response := request(t, handler, http.MethodPost, "/api/aps", string(input))
	if response.Code != http.StatusCreated || bytes.Contains(response.Body.Bytes(), []byte(secret)) || bytes.Contains(response.Body.Bytes(), []byte(base64.StdEncoding.EncodeToString(secretBytes))) || !bytes.Contains(response.Body.Bytes(), []byte(`"config_revision":2`)) {
		t.Fatalf("registration response: %d %s", response.Code, response.Body.String())
	}
	state := store.Snapshot()
	if len(state.APs) != 2 || state.APs[1].ID != "02-aa-00-00-00-06" || state.APs[1].SiteID != "b" || !bytes.Equal(state.APs[1].Secret, secretBytes) || state.APs[1].ConfigRevision != state.Revision {
		t.Fatalf("AP not stored: %+v", state.APs)
	}
	listed := request(t, handler, http.MethodGet, "/api/aps", "")
	if listed.Code != http.StatusOK || bytes.Contains(listed.Body.Bytes(), []byte(secret)) ||
		bytes.Contains(listed.Body.Bytes(), []byte(base64.StdEncoding.EncodeToString(secretBytes))) || !bytes.Contains(listed.Body.Bytes(), []byte(`"has_secret":true`)) {
		t.Fatalf("AP list leaked the secret or lacks has_secret: %d %s", listed.Code, listed.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"id": "02-aa-00-00-00-06", "management_ip": "192.0.2.11", "site_id": "b", "secret": secret})
	duplicate := request(t, handler, http.MethodPost, "/api/aps", string(body))
	if duplicate.Code != http.StatusConflict || store.Snapshot().Revision != state.Revision {
		t.Fatalf("duplicate AP ID accepted: %d %s", duplicate.Code, duplicate.Body.String())
	}
	// APs are identified by ID, so a second AP may share another's address
	// (several APs behind one NAT).
	body, _ = json.Marshal(map[string]string{"id": "02-aa-00-00-00-05", "management_ip": "192.0.2.10", "site_id": "b", "secret": secret})
	if shared := request(t, handler, http.MethodPost, "/api/aps", string(body)); shared.Code != http.StatusCreated {
		t.Fatalf("AP sharing an address rejected: %d %s", shared.Code, shared.Body.String())
	}
}

func TestRegisterAPRejectsDuplicateOrInvalidInputs(t *testing.T) {
	handler, store, _ := platformFixture(t)
	secretBytes := bytes.Repeat([]byte{7}, 32)
	secret := hex.EncodeToString(secretBytes)
	before := store.Snapshot().Revision
	for _, input := range []map[string]string{
		{"id": "invalid", "management_ip": "192.0.2.10", "site_id": "b", "secret": secret},
		{"id": "02-aa-00-00-00-06", "management_ip": "invalid", "site_id": "b", "secret": secret},
		{"id": "02-aa-00-00-00-06", "management_ip": "192.0.2.10", "site_id": "missing", "secret": secret},
		{"id": "ap-a", "management_ip": "192.0.2.10", "site_id": "b", "secret": secret},
	} {
		body, _ := json.Marshal(input)
		response := request(t, handler, http.MethodPost, "/api/aps", string(body))
		if response.Code < 400 || store.Snapshot().Revision != before {
			t.Fatalf("invalid AP registered: %d %s", response.Code, body)
		}
	}
	for _, bad := range []string{"", "zz", hex.EncodeToString(bytes.Repeat([]byte{7}, 33)), "/var/lib/dismist/ap.bin"} {
		body, _ := json.Marshal(map[string]string{"id": "02-aa-00-00-00-06", "site_id": "b", "secret": bad})
		if response := request(t, handler, http.MethodPost, "/api/aps", string(body)); response.Code != http.StatusBadRequest || store.Snapshot().Revision != before {
			t.Fatalf("bad secret %q accepted: %d", bad, response.Code)
		}
	}
	legacy, _ := json.Marshal(map[string]string{"id": "02-aa-00-00-00-06", "site_id": "b", "secret_file": "/var/lib/dismist/ap.bin"})
	if response := request(t, handler, http.MethodPost, "/api/aps", string(legacy)); response.Code != http.StatusBadRequest {
		t.Fatalf("legacy secret_file registration accepted: %d", response.Code)
	}
	encoded, _ := json.Marshal(map[string]string{"id": "02-aa-00-00-00-06", "site_id": "b", "secret": base64.StdEncoding.EncodeToString(secretBytes)})
	if response := request(t, handler, http.MethodPost, "/api/aps", string(encoded)); response.Code != http.StatusCreated {
		t.Fatalf("base64 secret rejected: %d %s", response.Code, response.Body.String())
	}
	for _, ap := range store.Snapshot().APs {
		if ap.ID == "02-aa-00-00-00-06" && !bytes.Equal(ap.Secret, secretBytes) {
			t.Fatalf("base64 secret stored as %x", ap.Secret)
		}
	}
}

func TestRegisterAPRejectsShortPrivateSecretWithoutChangingDatabase(t *testing.T) {
	handler, store, _ := platformFixture(t)
	secretBytes := bytes.Repeat([]byte{7}, 31)
	secret := hex.EncodeToString(secretBytes)
	before := store.Snapshot()
	body, _ := json.Marshal(map[string]string{"id": "02-aa-00-00-00-06", "management_ip": "192.0.2.10", "site_id": "b", "secret": secret})
	response := request(t, handler, http.MethodPost, "/api/aps", string(body))
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte("exactly 32 bytes")) {
		t.Fatalf("short secret accepted or unclear error: %d %s", response.Code, response.Body.String())
	}
	after := store.Snapshot()
	if after.Revision != before.Revision || len(after.APs) != len(before.APs) {
		t.Fatalf("short secret changed database: before=%+v after=%+v", before, after)
	}
}

func TestAPInventoryUsesControllerIPAndLiveFirmwarePreference(t *testing.T) {
	handler, _, _ := platformFixture(t)
	response := request(t, handler, http.MethodGet, "/api/aps", "")
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"management_ip":"127.0.0.1"`)) {
		t.Fatalf("legacy AP controller IP missing: %d %s", response.Code, response.Body.String())
	}
	if got := preferredFirmware("stored-firmware", controller.Status{Firmware: "live-firmware"}); got != "live-firmware" {
		t.Fatalf("stale firmware won: %q", got)
	}
	if got := preferredFirmware("stored-firmware", controller.Status{}); got != "stored-firmware" {
		t.Fatalf("stored fallback lost: %q", got)
	}
}

func TestEditAPFieldsRebuildsOnlyItsConfigAndRedactsSecretPath(t *testing.T) {
	handler, store, control := platformFixture(t)
	if err := store.Update(func(state *model.State) error {
		state.APs = append(state.APs, model.APRecord{ID: "ap-b", SiteID: "b", SecretFile: "other-secret", ConfigRevision: 2})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secretBytes := bytes.Repeat([]byte{9}, 32)
	secret := hex.EncodeToString(secretBytes)
	body, _ := json.Marshal(map[string]string{"name": "Renamed", "model": "AP34", "management_ip": "192.0.2.25", "secret": secret})
	response := request(t, handler, http.MethodPut, "/api/aps/ap-a", string(body))
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(secret)) || !bytes.Contains(response.Body.Bytes(), []byte(`"management_ip":"192.0.2.25"`)) {
		t.Fatalf("AP edit: %d %s", response.Code, response.Body.String())
	}
	state := store.Snapshot()
	if state.APs[0].Name != "Renamed" || state.APs[0].Model != "AP34" || state.APs[0].ManagementIP != "192.0.2.25" || !bytes.Equal(state.APs[0].Secret, secretBytes) || state.APs[0].ConfigRevision != state.Revision || state.APs[1].ConfigRevision != 2 {
		t.Fatalf("AP edit state: %+v", state.APs)
	}
	config, revision := control.ConfigSnapshot()
	if revision != state.APs[0].ConfigRevision || !bytes.Contains(config, []byte(`"Name":"Renamed"`)) {
		t.Fatalf("AP config not rebuilt: rev %d config %s", revision, config)
	}
	retained := request(t, handler, http.MethodPut, "/api/aps/ap-a", `{"model":"AP35","secret":""}`)
	if retained.Code != http.StatusOK || !bytes.Equal(store.Snapshot().APs[0].Secret, secretBytes) {
		t.Fatalf("blank secret did not retain existing credential: %d %s", retained.Code, retained.Body.String())
	}
	state = store.Snapshot()
	noChange := request(t, handler, http.MethodPut, "/api/aps/ap-a", `{"name":"Renamed","secret":""}`)
	if noChange.Code != http.StatusOK || store.Snapshot().Revision != state.Revision {
		t.Fatalf("no-op edit bumped state: %d %s", noChange.Code, noChange.Body.String())
	}
}

func TestEditAPRejectsInvalidBindingsWithoutChangingState(t *testing.T) {
	handler, store, _ := platformFixture(t)
	secretBytes := bytes.Repeat([]byte{4}, 31)
	secret := hex.EncodeToString(secretBytes)
	otherIP := "192.0.2.40"
	if err := store.Update(func(state *model.State) error {
		state.APs = append(state.APs, model.APRecord{ID: "ap-b", SiteID: "b", SecretFile: "other-secret", ManagementIP: otherIP})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	for _, input := range []map[string]string{
		{"management_ip": "invalid"}, {"secret": secret},
	} {
		body, _ := json.Marshal(input)
		response := request(t, handler, http.MethodPut, "/api/aps/ap-a", string(body))
		if response.Code < 400 || store.Snapshot().Revision != before.Revision {
			t.Fatalf("invalid AP edit committed: %d %s", response.Code, body)
		}
	}
	shared := request(t, handler, http.MethodPut, "/api/aps/ap-a", `{"management_ip":"`+otherIP+`"}`)
	if shared.Code != http.StatusOK {
		t.Fatalf("pinning an AP to a shared address rejected: %d %s", shared.Code, shared.Body.String())
	}
}

func TestEditAPConfigFailureLeavesRecordAndDesiredRevisionUnchanged(t *testing.T) {
	handler, store, control := platformFixture(t)
	if err := store.Update(func(state *model.State) error {
		state.Networks[0].Advanced = json.RawMessage(`{"Auth":null}`)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	_, desired := control.ConfigSnapshot()
	response := request(t, handler, http.MethodPut, "/api/aps/ap-a", `{"name":"Would fail"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid config accepted: %d %s", response.Code, response.Body.String())
	}
	after := store.Snapshot()
	_, gotDesired := control.ConfigSnapshot()
	if after.Revision != before.Revision || after.APs[0].Name != before.APs[0].Name || gotDesired != desired {
		t.Fatalf("failed config changed state or desired revision: %+v -> %+v, %d -> %d", before.APs[0], after.APs[0], desired, gotDesired)
	}
}

func TestAPNoOpEditReturnsPersistedRevision(t *testing.T) {
	handler, store, _ := platformFixture(t)
	before := store.Snapshot()
	response := request(t, handler, http.MethodPut, "/api/aps/ap-a", `{"name":""}`)
	if response.Code != http.StatusOK || store.Snapshot().Revision != before.Revision {
		t.Fatalf("no-op edit changed state: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		AP struct {
			ConfigRevision uint64 `json:"config_revision"`
		} `json:"ap"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.AP.ConfigRevision != before.APs[0].ConfigRevision {
		t.Fatalf("response revision %d differs from stored %d", result.AP.ConfigRevision, before.APs[0].ConfigRevision)
	}
}

func TestRegisterAPWithSharedOrNoSourceAddress(t *testing.T) {
	handler, store, _ := platformFixture(t)
	secretBytes := bytes.Repeat([]byte{1}, 32)
	secret := hex.EncodeToString(secretBytes)
	body, _ := json.Marshal(map[string]string{"id": "02-aa-00-00-00-06", "management_ip": "127.0.0.1", "site_id": "b", "secret": secret})
	if response := request(t, handler, http.MethodPost, "/api/aps", string(body)); response.Code != http.StatusCreated {
		t.Fatalf("AP sharing the legacy AP's address rejected: %d %s", response.Code, response.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"id": "02-aa-00-00-00-07", "site_id": "b", "secret": secret})
	response := request(t, handler, http.MethodPost, "/api/aps", string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("AP without a management IP rejected: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		AP map[string]any `json:"ap"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.AP["management_ip"] != "" {
		t.Fatalf("unpinned AP view: %s %v", response.Body.String(), err)
	}
	for _, ap := range store.Snapshot().APs {
		if ap.ID == "02-aa-00-00-00-07" && ap.ManagementIP != "" {
			t.Fatalf("unpinned AP stored with address %q", ap.ManagementIP)
		}
	}
	body, _ = json.Marshal(map[string]string{"id": "02-aa-00-00-00-08", "management_ip": "nope", "site_id": "b", "secret": secret})
	if response := request(t, handler, http.MethodPost, "/api/aps", string(body)); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid management IP accepted: %d", response.Code)
	}
}

func TestWildcardLANHostRequiresExactMutationOrigin(t *testing.T) {
	handler, store, _ := platformFixture(t)
	for _, origin := range []string{"http://other.lan:8088", "http://box.lan:9090", "https://box.lan:8088"} {
		req := httptest.NewRequest(http.MethodPut, "/api/sites/a", bytes.NewBufferString(`{"name":"Changed"}`))
		req.Host = "box.lan:8088"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("accepted %s: %d", origin, response.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPut, "/api/sites/a", bytes.NewBufferString(`{"name":"Changed"}`))
	req.Host = "box.lan:8088"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://box.lan:8088")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || store.Snapshot().Sites[0].Name != "Changed" {
		t.Fatalf("same-origin LAN edit failed: %d %s", response.Code, response.Body.String())
	}
}

func TestLoopbackHTTPSProxyOrigin(t *testing.T) {
	handler, store, _ := platformFixture(t)
	req := httptest.NewRequest(http.MethodPut, "/api/sites/a", bytes.NewBufferString(`{"name":"Via proxy"}`))
	req.Host = "office.example:443"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://office.example:443")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || store.Snapshot().Sites[0].Name != "Via proxy" {
		t.Fatalf("trusted HTTPS proxy rejected: %d %s", response.Code, response.Body.String())
	}
	req = httptest.NewRequest(http.MethodPut, "/api/sites/a", bytes.NewBufferString(`{"name":"Spoofed"}`))
	req.Host = "office.example:443"
	req.RemoteAddr = "192.0.2.5:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://office.example:443")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden || store.Snapshot().Sites[0].Name != "Via proxy" {
		t.Fatalf("untrusted proxy accepted: %d", response.Code)
	}
}

func TestConfiguredHTTPSProxyOrigin(t *testing.T) {
	inner, store, _ := platformFixture(t)
	trusted, err := proxytrust.ParsePrefixes("10.244.0.0/16")
	if err != nil {
		t.Fatal(err)
	}
	handler := proxytrust.Middleware(trusted, inner)
	edit := func(remote, name string) int {
		req := httptest.NewRequest(http.MethodPut, "/api/sites/a", bytes.NewBufferString(`{"name":"`+name+`"}`))
		req.Host = "dismist.example"
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Origin", "https://dismist.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response.Code
	}
	if code := edit("10.244.3.199:41000", "Via gateway"); code != http.StatusOK || store.Snapshot().Sites[0].Name != "Via gateway" {
		t.Fatalf("configured HTTPS proxy rejected: %d", code)
	}
	if code := edit("192.0.2.5:41000", "Spoofed"); code != http.StatusForbidden || store.Snapshot().Sites[0].Name != "Via gateway" {
		t.Fatalf("peer outside the trusted proxies accepted: %d", code)
	}
}
