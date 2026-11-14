package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/dnsserver"
	"github.com/nicko170/dismist/internal/httpapi"
	"github.com/nicko170/dismist/internal/model"
)

func TestRegistryActivationAndSiteMoveKeepNewestSiteConfig(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "delayed-secret.bin")
	if err := syscall.Mkfifo(secretPath, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := model.OpenBolt(filepath.Join(dir, "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "A"}, {ID: "site-b", Name: "B"}}
		state.APs = []model.APRecord{{ID: "ap-a", SiteID: "site-a", ManagementIP: "127.0.0.2", SecretFile: secretPath}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	manager := controller.NewManager()
	resolver, err := dnsserver.NewResolver("127.0.0.254", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	resolver.RemoveAP("127.0.0.254")
	base := []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`)
	mutationLock := new(sync.Mutex)
	registry := newAPRegistry(manager, resolver, registryOptions{
		Base: base, CertDER: []byte{1}, MutationLock: mutationLock,
	}, model.State{})
	handler := httpapi.NewWithManager(store, manager, base, "ap-a", "*").UseMutationLock(mutationLock)
	registered := make(chan error, 1)
	go func() { registered <- registry.reconcileCurrent(store) }()
	var writer *os.File
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fd, openErr := syscall.Open(secretPath, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if openErr == nil {
			writer = os.NewFile(uintptr(fd), secretPath)
			break
		}
		if openErr != syscall.ENXIO {
			t.Fatal(openErr)
		}
		time.Sleep(time.Millisecond)
	}
	if writer == nil {
		t.Fatal("registry did not begin preparing the AP")
	}
	moved := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPut, "/api/aps/ap-a/site", strings.NewReader(`{"site_id":"site-b"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		moved <- response
	}()
	earlyMove := false
	select {
	case <-moved:
		earlyMove = true
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := writer.Write(bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-registered; err != nil {
		t.Fatal(err)
	}
	if earlyMove {
		t.Fatal("site move committed while stale AP registration was in progress")
	}
	response := <-moved
	if response.Code != http.StatusOK {
		t.Fatalf("site move: %d %s", response.Code, response.Body.String())
	}
	server, ok := manager.Server("ap-a")
	if !ok {
		t.Fatal("AP was not registered")
	}
	config, revision := server.ConfigSnapshot()
	var applied struct {
		SiteID string `json:"SiteID"`
	}
	if err := json.Unmarshal(config, &applied); err != nil {
		t.Fatal(err)
	}
	if applied.SiteID != "site-b" || revision != store.Snapshot().APs[0].ConfigRevision {
		t.Fatalf("registered AP retained stale site config: site=%q revision=%d", applied.SiteID, revision)
	}
}

func TestReconcileActivatesPersistedAPAndWithdrawsOldIP(t *testing.T) {
	dir := t.TempDir()
	store, err := model.OpenBolt(filepath.Join(dir, "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secretPath := filepath.Join(dir, "ap-secret.bin")
	if err := os.WriteFile(secretPath, bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := controller.NewManager()
	resolver, err := dnsserver.NewResolver("127.0.0.254", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	resolver.RemoveAP("127.0.0.254")
	registry := newAPRegistry(manager, resolver, registryOptions{
		Base: []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`), CertDER: []byte{1},
	}, store.Snapshot())
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "office", Name: "Office"}}
		state.APs = []model.APRecord{{ID: "ap-one", SiteID: "office", ManagementIP: "127.0.0.2", SecretFile: secretPath}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.reconcile(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	first, ok := manager.Server("ap-one")
	if !ok || first == nil {
		t.Fatal("newly persisted AP was not registered")
	}
	assertAPAccess(t, manager, resolver, "ap-one", "127.0.0.2", true)

	if err := store.Update(func(state *model.State) error {
		state.APs[0].Name = "Renamed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.reconcile(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if current, _ := manager.Server("ap-one"); current != first {
		t.Fatal("config-only change replaced the AP session")
	}

	if err := store.Update(func(state *model.State) error {
		state.APs[0].ManagementIP = "127.0.0.3"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.reconcile(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if current, _ := manager.Server("ap-one"); current == first {
		t.Fatal("changed source IP kept the old AP session")
	}
	assertAPAccess(t, manager, resolver, "ap-one", "127.0.0.2", false)
	assertAPAccess(t, manager, resolver, "ap-one", "127.0.0.3", true)

	if err := store.Update(func(state *model.State) error {
		state.APs = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.reconcile(store.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Server("ap-one"); ok {
		t.Fatal("removed AP retained a control session")
	}
	assertAPAccess(t, manager, resolver, "ap-one", "127.0.0.3", false)
}

func TestAPRegistryPollsPersistedAPs(t *testing.T) {
	dir := t.TempDir()
	store, err := model.OpenBolt(filepath.Join(dir, "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secretPath := filepath.Join(dir, "secret.bin")
	if err := os.WriteFile(secretPath, bytes.Repeat([]byte{5}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := controller.NewManager()
	resolver, err := dnsserver.NewResolver("127.0.0.254", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	resolver.RemoveAP("127.0.0.254")
	registry := newAPRegistry(manager, resolver, registryOptions{
		Base: []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`), CertDER: []byte{1},
	}, store.Snapshot())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAPRegistry(ctx, store, registry, 10*time.Millisecond)
	}()
	t.Cleanup(func() { cancel(); <-done })
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "office", Name: "Office"}}
		state.APs = []model.APRecord{{ID: "ap-new", SiteID: "office", ManagementIP: "127.0.0.5", SecretFile: secretPath}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		if _, ok := manager.Server("ap-new"); ok {
			assertAPAccess(t, manager, resolver, "ap-new", "127.0.0.5", true)
			return
		}
		select {
		case <-deadline:
			t.Fatal("persisted AP was not registered by periodic reconciliation")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestReconcileWaitsForSiteAssignment(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret.bin")
	if err := os.WriteFile(secretPath, bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := controller.NewManager()
	resolver, err := dnsserver.NewResolver("127.0.0.254", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	resolver.RemoveAP("127.0.0.254")
	registry := newAPRegistry(manager, resolver, registryOptions{
		Base: []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`), CertDER: []byte{1},
	}, model.State{})
	state := model.State{
		Sites: []model.Site{{ID: "office", Name: "Office"}},
		APs:   []model.APRecord{{ID: "ap-pending", ManagementIP: "127.0.0.4", SecretFile: secretPath}},
	}
	if err := registry.reconcile(state); err != nil {
		t.Fatalf("unassigned AP caused an error: %v", err)
	}
	assertAPAccess(t, manager, resolver, "ap-pending", "127.0.0.4", false)
	state.APs[0].SiteID = "office"
	if err := registry.reconcile(state); err != nil {
		t.Fatal(err)
	}
	assertAPAccess(t, manager, resolver, "ap-pending", "127.0.0.4", true)
}

func TestReconcileInvalidNewAPKeepsExistingSession(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "good-secret.bin")
	if err := os.WriteFile(secretPath, bytes.Repeat([]byte{4}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	state := model.State{
		Sites: []model.Site{{ID: "office", Name: "Office"}},
		APs:   []model.APRecord{{ID: "ap-good", SiteID: "office", ManagementIP: "127.0.0.7", SecretFile: secretPath}},
	}
	options := registryOptions{Base: []byte(`{"WLANs":[{"Auth":{"Type":"psk"}}]}`), CertDER: []byte{1}}
	manager, _, err := prepareAPs(state, options)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := dnsserver.NewResolver("127.0.0.7", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	registry := newAPRegistry(manager, resolver, options, state)
	good, _ := manager.Server("ap-good")
	state.APs = append(state.APs, model.APRecord{
		ID: "ap-bad", SiteID: "office", ManagementIP: "127.0.0.8", SecretFile: filepath.Join(dir, "missing.bin"),
	})
	if err := registry.reconcile(state); err == nil {
		t.Fatal("missing secret did not report a registration failure")
	}
	if current, _ := manager.Server("ap-good"); current != good {
		t.Fatal("unrelated invalid AP replaced the existing session")
	}
	assertAPAccess(t, manager, resolver, "ap-good", "127.0.0.7", true)
	assertAPAccess(t, manager, resolver, "ap-bad", "127.0.0.8", false)
}

// assertAPAccess checks an AP's pinned address against AP DNS and its control
// session. WSS routing is by authenticated AP ID, so a withdrawn or re-pinned
// AP must no longer have a session pinned to the old address.
func assertAPAccess(t *testing.T, manager *controller.Manager, resolver *dnsserver.Resolver, id, address string, allowed bool) {
	t.Helper()
	query := new(dns.Msg)
	query.SetQuestion("ep-terminator.mistsys.net.", dns.TypeA)
	answer := resolver.Resolve(query, net.ParseIP(address))
	server, ok := manager.Server(id)
	pinned := ok && server.AllowedManagementIP() == address
	if allowed {
		if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 || !pinned {
			t.Fatalf("registered AP %s at %s lacks DNS or its session: DNS %d/%d, session %t", id, address, answer.Rcode, len(answer.Answer), pinned)
		}
	} else if answer.Rcode != dns.RcodeRefused || pinned {
		t.Fatalf("withdrawn AP %s at %s retains DNS or its session: DNS %d, session %t", id, address, answer.Rcode, pinned)
	}
}
