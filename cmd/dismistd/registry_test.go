package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/dnsserver"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/tools/fakeap"
)

func TestPreparedAPsShareSiteConfigAndKeepSeparateSecrets(t *testing.T) {
	secret := func(value byte) []byte {
		return bytes.Repeat([]byte{value}, 32)
	}
	state := model.State{
		Revision: 4, UpdatedAt: time.Unix(1794475905, 0),
		Sites: []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}},
		APs: []model.APRecord{
			{ID: "ap-a", SiteID: "site-a", Secret: secret(1)},
			{ID: "ap-c", SiteID: "site-a", ManagementIP: "127.0.0.3", Secret: secret(3)},
			{ID: "ap-b", SiteID: "site-b", ManagementIP: "127.0.0.2", Secret: secret(2)},
		},
		Networks: []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "A-WLAN", PSK: "example-passphrase-09", VLANID: 123}, {ID: "wlan-b", SiteID: "site-b", SSID: "B-WLAN", PSK: "example-passphrase-10", VLANID: 124}},
	}
	base := []byte(`{"ID":"old","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-05"}}]}`)
	manager, addresses, err := prepareAPs(state, registryOptions{DefaultID: "ap-a", DefaultIP: "127.0.0.1", Base: base, CertDER: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 3 {
		t.Fatalf("short AP address count %d", len(addresses))
	}
	for _, id := range []string{"ap-a", "ap-c"} {
		control, ok := manager.Server(id)
		if !ok {
			t.Fatalf("site A AP %s missing", id)
		}
		config, revision := control.ConfigSnapshot()
		if revision != 4 || !bytes.Contains(config, []byte("A-WLAN")) || bytes.Contains(config, []byte("B-WLAN")) {
			t.Fatalf("site A AP %s received short config", id)
		}
	}
	controlB, ok := manager.Server("ap-b")
	if !ok {
		t.Fatal("site B AP missing")
	}
	configB, _ := controlB.ConfigSnapshot()
	if !bytes.Contains(configB, []byte("B-WLAN")) || bytes.Contains(configB, []byte("A-WLAN")) {
		t.Fatal("site B AP received site A config")
	}
}

func TestUnrelatedSiteChangeDoesNotBumpAPConfigOnRestart(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret.bin")
	if err := os.WriteFile(secretPath, bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	configTime := time.Unix(1794475905, 0)
	state := model.State{
		Revision: 4, UpdatedAt: configTime.Add(time.Hour),
		Sites:    []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}},
		APs:      []model.APRecord{{ID: "ap-a", SiteID: "site-a", Secret: bytes.Repeat([]byte{1}, 32), ConfigRevision: 3, ConfigUpdatedAt: configTime}},
		Networks: []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "A-WLAN", PSK: "example-passphrase-09", VLANID: 123}},
	}
	base := []byte(`{"ID":"old","Timestamp":1,"WLANs":[{"ID":"old","SSID":"OLD","Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-05"}}]}`)
	manager, _, err := prepareAPs(state, registryOptions{DefaultID: "ap-a", DefaultIP: "127.0.0.1", Base: base, CertDER: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	control, _ := manager.Server("ap-a")
	config, revision := control.ConfigSnapshot()
	if revision != 3 || !bytes.Contains(config, []byte(`"Timestamp":1794475905`)) {
		t.Fatalf("unrelated site edit changed AP config revision or timestamp: %d %s", revision, config)
	}
}

func TestPrepareAPsAllowsFreshEmptyInventory(t *testing.T) {
	manager, addresses, err := prepareAPs(model.State{}, registryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || len(manager.Servers()) != 0 || len(addresses) != 0 {
		t.Fatalf("fresh inventory should have no AP servers or DNS addresses: %v %v", manager, addresses)
	}
}

func TestPrepareAPsSkipsUnassignedAP(t *testing.T) {
	state := model.State{
		Sites: []model.Site{{ID: "office", Name: "Office"}},
		APs:   []model.APRecord{{ID: "ap-pending", ManagementIP: "127.0.0.4", SecretFile: "/not/ready/secret.bin"}},
	}
	manager, addresses, err := prepareAPs(state, registryOptions{})
	if err != nil || len(addresses) != 0 || len(manager.Servers()) != 0 {
		t.Fatalf("unassigned AP should remain inactive: addresses %v, err %v", addresses, err)
	}
}

// fakeAPConfig is an in-memory ap_config delivered store.
type fakeAPConfig struct {
	mu        sync.Mutex
	delivered map[string]model.DeliveredConfig
	puts      int
}

func (f *fakeAPConfig) Delivered(apID string) (model.DeliveredConfig, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delivered, found := f.delivered[apID]
	return delivered, found, nil
}

func (f *fakeAPConfig) PutDelivered(apID string, delivered model.DeliveredConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delivered == nil {
		f.delivered = map[string]model.DeliveredConfig{}
	}
	f.delivered[apID] = delivered
	f.puts++
	return nil
}

// brokenPatch makes an AP's compile fail (the merge patch is not an object).
var brokenPatch = json.RawMessage(`[1]`)

// TestPrepareAPsIsolatesEachAP is design §4.4 "One AP never stops
// another": no secret, a broken patch, a missing site or a bad pin affects
// only that AP.
func TestPrepareAPsIsolatesEachAP(t *testing.T) {
	secret := bytes.Repeat([]byte{5}, 32)
	state := model.State{
		Revision: 6, UpdatedAt: time.Unix(1794475905, 0),
		Sites: []model.Site{{ID: "site-a", Name: "A"}},
		APs: []model.APRecord{
			{ID: "ap-good", SiteID: "site-a", ManagementIP: "127.0.0.2", Secret: secret},
			{ID: "ap-no-secret", SiteID: "site-a", ManagementIP: "127.0.0.3"},
			{ID: "ap-broken-delivered", SiteID: "site-a", Secret: secret, Advanced: brokenPatch},
			{ID: "ap-broken-never", SiteID: "site-a", Secret: secret, Advanced: brokenPatch},
			{ID: "ap-lost-site", SiteID: "site-gone", Secret: secret},
			{ID: "ap-bad-pin", SiteID: "site-a", ManagementIP: "not-an-ip", Secret: secret},
			{ID: "ap-unassigned", Secret: secret},
		},
		Networks: []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}},
	}
	lastDelivered := []byte(`{"ID":"ap-broken-delivered","Timestamp":1700000000.5,"WLANs":[]}`)
	store := &fakeAPConfig{delivered: map[string]model.DeliveredConfig{
		"ap-broken-delivered": {Revision: 3, Timestamp: 1700000000.5, Bytes: lastDelivered},
	}}
	base := []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`)
	manager, addresses, err := prepareAPs(state, registryOptions{Base: base, CertDER: []byte{1}, APConfig: store})
	if err != nil {
		t.Fatalf("one bad AP failed the registry: %v", err)
	}
	if len(addresses) != 1 || addresses[0] != "127.0.0.2" {
		t.Fatalf("addresses %v", addresses)
	}
	for _, id := range []string{"ap-no-secret", "ap-bad-pin", "ap-unassigned"} {
		if _, ok := manager.Server(id); ok {
			t.Fatalf("%s should not be served", id)
		}
	}
	good, ok := manager.Server("ap-good")
	if !ok {
		t.Fatal("good AP not served next to broken ones")
	}
	if config, revision := good.ConfigSnapshot(); revision != 6 || !bytes.Contains(config, []byte("Example")) || good.Status().ConfigSource != controller.ConfigSourceCompiled {
		t.Fatalf("good AP config rev %d source %q", revision, good.Status().ConfigSource)
	}
	fallback, ok := manager.Server("ap-broken-delivered")
	if !ok {
		t.Fatal("broken AP with a delivered config not served")
	}
	if config, revision := fallback.ConfigSnapshot(); revision != 3 || !bytes.Equal(config, lastDelivered) {
		t.Fatalf("broken AP not served its delivered bytes: rev %d %s", revision, config)
	}
	if status := fallback.Status(); status.ConfigSource != controller.ConfigSourceDelivered || status.CompileError == "" {
		t.Fatalf("fallback status %+v", status)
	}
	for _, id := range []string{"ap-broken-never", "ap-lost-site"} {
		server, ok := manager.Server(id)
		if !ok {
			t.Fatalf("%s should be registered so its error shows", id)
		}
		if config, _ := server.ConfigSnapshot(); config != nil || server.Status().ConfigSource != controller.ConfigSourceNone || server.Counters().CompileErrors != 1 {
			t.Fatalf("%s served %s (%+v)", id, config, server.Status())
		}
	}
}

// TestCompileFailureServesDeliveredBytesOverTheWire records a delivery
// through the real ap_config store, breaks the AP's config, restarts the
// registry and checks the AP is sent exactly the bytes it had before.
func TestCompileFailureServesDeliveredBytesOverTheWire(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var handler http.Handler
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(tlsServer.Close)
	var secret [32]byte
	for index := range secret {
		secret[index] = byte(index + 1)
	}
	state := model.State{
		Revision: 2, UpdatedAt: time.Unix(1794475905, 0),
		Sites:    []model.Site{{ID: "site-a", Name: "A"}},
		APs:      []model.APRecord{{ID: "02-aa-00-00-00-51", SiteID: "site-a", Secret: secret[:], ConfigRevision: 2, ConfigUpdatedAt: time.Unix(1794475905, 0)}},
		Networks: []model.Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}},
	}
	options := registryOptions{Base: []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`), CertDER: tlsServer.Certificate().Raw, APConfig: store.APConfig()}
	manager, _, err := prepareAPs(state, options)
	if err != nil {
		t.Fatal(err)
	}
	handler = manager
	ap := fakeap.Config{URL: "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws", ID: "02-aa-00-00-00-51", Secret: secret}
	ctx := context.Background()
	connected, err := fakeap.Connect(ctx, ap)
	if err != nil {
		t.Fatal(err)
	}
	first, err := connected.FetchConfig(ctx, fakeap.ConfigRequest{})
	_ = connected.Close()
	if err != nil {
		t.Fatal(err)
	}
	delivered, found, err := store.APConfig().Delivered("02-aa-00-00-00-51")
	if err != nil || !found || !bytes.Equal(delivered.Bytes, first) || delivered.Revision != 2 || delivered.Timestamp != 1794475905 || delivered.At.IsZero() {
		t.Fatalf("delivery not recorded: %+v %t %v", delivered, found, err)
	}

	state.APs[0].Advanced = brokenPatch
	state.Revision = 3
	manager, _, err = prepareAPs(state, options)
	if err != nil {
		t.Fatal(err)
	}
	handler = manager
	connected, err = fakeap.Connect(ctx, ap)
	if err != nil {
		t.Fatal(err)
	}
	defer connected.Close()
	again, err := connected.FetchConfig(ctx, fakeap.ConfigRequest{})
	if err != nil || !bytes.Equal(again, first) {
		t.Fatalf("compile failure did not serve the delivered bytes:\n got %s\nwant %s (%v)", again, first, err)
	}
	server, _ := manager.Server("02-aa-00-00-00-51")
	if status := server.Status(); status.ConfigSource != controller.ConfigSourceDelivered || !strings.Contains(status.CompileError, "JSON object") {
		t.Fatalf("status %+v", status)
	}
}

func TestReconcileAppliesProtocolFeaturesWithoutReregistering(t *testing.T) {
	secret := bytes.Repeat([]byte{6}, 32)
	state := model.State{
		Sites: []model.Site{{ID: "site-a", Name: "A"}},
		APs:   []model.APRecord{{ID: "ap-f", SiteID: "site-a", ManagementIP: "127.0.0.6", Secret: secret}},
	}
	defaults, err := controller.ParseFeatures("subscribe_reply")
	if err != nil {
		t.Fatal(err)
	}
	options := registryOptions{Base: []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`), CertDER: []byte{1}, Features: defaults}
	manager, _, err := prepareAPs(state, options)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := dnsserver.NewResolver("127.0.0.6", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	registry := newAPRegistry(manager, resolver, options, state)
	server, _ := manager.Server("ap-f")
	if !server.FeatureEnabled(controller.FeatureSubscribeReply) || server.FeatureEnabled(controller.FeaturePong2) {
		t.Fatalf("default features not applied: %s", server.Features())
	}
	state.APs[0].Features = map[string]bool{"pong2": true, "subscribe_reply": false}
	if err := registry.reconcile(state); err != nil {
		t.Fatal(err)
	}
	if current, _ := manager.Server("ap-f"); current != server {
		t.Fatal("a feature switch re-registered the AP")
	}
	if !server.FeatureEnabled(controller.FeaturePong2) || server.FeatureEnabled(controller.FeatureSubscribeReply) {
		t.Fatalf("per-AP features not applied: %s", server.Features())
	}
	state.APs[0].Features = nil
	if err := registry.reconcile(state); err != nil {
		t.Fatal(err)
	}
	if server.Features() != defaults {
		t.Fatalf("clearing overrides did not restore the defaults: %s", server.Features())
	}
}

func TestPrepareAPsRejectsSharedProbeMisconfiguration(t *testing.T) {
	if _, _, err := prepareAPs(model.State{}, registryOptions{UDPProbeSendTest: true}); err == nil {
		t.Fatal("UDP test without a probe address accepted")
	}
}
