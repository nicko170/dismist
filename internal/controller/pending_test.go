package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/tools/fakeap"
)

func TestPendingMapIsBoundedAndExpires(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	pending := newPendingMap()
	pending.now = func() time.Time { return now }
	for index := range pendingMax + 10 {
		pending.record(Hello{ID: fmt.Sprintf("02-aa-00-00-01-%02x", index), Model: "AP33-WW"}, HelloExtras{}, "198.51.100.7:4000", PendingUnregistered)
		now = now.Add(time.Second)
	}
	list := pending.list()
	if len(list) != pendingMax {
		t.Fatalf("pending map holds %d entries, want %d", len(list), pendingMax)
	}
	if list[0].ID != fmt.Sprintf("02-aa-00-00-01-%02x", pendingMax+9) || list[len(list)-1].ID != "02-aa-00-00-01-0a" {
		t.Fatalf("oldest entries were not the ones evicted: first %s last %s", list[0].ID, list[len(list)-1].ID)
	}
	if list[0].RemoteAddress != "198.51.100.7" || list[0].Reason != PendingUnregistered || list[0].Attempts != 1 {
		t.Fatalf("pending entry: %+v", list[0])
	}
	revision := pending.currentRevision()
	pending.forget(list[0].ID)
	if pending.currentRevision() == revision || len(pending.list()) != pendingMax-1 {
		t.Fatal("forget did not remove the entry or change the revision")
	}
	now = now.Add(pendingTTL)
	if got := pending.list(); len(got) != 0 {
		t.Fatalf("%d entries survived the TTL", len(got))
	}
}

func TestPendingMapCleansHelloStrings(t *testing.T) {
	pending := newPendingMap()
	hostile := strings.Repeat("M", 500) + "\x00\x1b[31m"
	pending.record(Hello{ID: "02-aa-00-00-00-09", Model: hostile, Firmware: "fw\n1"}, HelloExtras{SKU: "AP33-WW"}, "bad-address", PendingUnregistered)
	entry := pending.list()[0]
	if len(entry.Model) != maxHelloString || strings.ContainsAny(entry.Model, "\x00\x1b") || entry.Firmware != "fw1" || entry.RemoteAddress != "bad-address" {
		t.Fatalf("hello strings not bounded and cleaned: %+v", entry)
	}
}

// pendingFixture is a Manager on a TLS test server.
type pendingFixture struct {
	manager *Manager
	url     string
	cert    []byte
}

func newPendingFixture(t *testing.T) pendingFixture {
	t.Helper()
	manager := NewManager()
	tlsServer := httptest.NewTLSServer(manager)
	t.Cleanup(tlsServer.Close)
	return pendingFixture{
		manager: manager, url: "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws",
		cert: tlsServer.Certificate().Raw,
	}
}

func secretOf(value byte) [32]byte {
	var secret [32]byte
	for index := range secret {
		secret[index] = value + byte(index)
	}
	return secret
}

func TestUnknownHelloFeedsPendingList(t *testing.T) {
	fixture := newPendingFixture(t)
	ctx := context.Background()
	config := fakeap.Config{URL: fixture.url, ID: "02-aa-00-00-00-21", Secret: secretOf(1), Extras: fakeap.DefaultExtras(), Timeout: time.Second}
	for range 2 {
		if _, err := fakeap.Connect(ctx, config); err == nil {
			t.Fatal("unregistered AP authenticated")
		}
	}
	pending := fixture.manager.Pending()
	if len(pending) != 1 {
		t.Fatalf("pending list: %+v", pending)
	}
	entry := pending[0]
	extras := fakeap.DefaultExtras()
	if entry.ID != config.ID || entry.Model != fakeap.DefaultModel || entry.Firmware != fakeap.DefaultFirmware ||
		entry.SKU != extras.SKU || entry.Serial != extras.SerialNo || entry.LocalIP != extras.LocalIP ||
		entry.RemoteAddress != "127.0.0.1" || entry.Attempts != 2 || entry.Reason != PendingUnregistered ||
		entry.FirstSeen.IsZero() || entry.LastSeen.Before(entry.FirstSeen) {
		t.Fatalf("pending entry: %+v", entry)
	}
	if routes := fixture.manager.RouteCounters(); routes[RouteUnknownAP] != 2 || routes[RouteRouted] != 0 {
		t.Fatalf("route counters: %v", routes)
	}
	revision := fixture.manager.PendingRevision()
	server, err := NewServer(Options{ID: config.ID, Secret: config.Secret, CertDER: fixture.cert, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Register(config.ID, "", server); err != nil {
		t.Fatal(err)
	}
	if len(fixture.manager.Pending()) != 0 || fixture.manager.PendingRevision() == revision {
		t.Fatal("registering the AP did not clear its pending entry")
	}
	ap, err := fakeap.Connect(ctx, config)
	if err != nil {
		t.Fatalf("registered AP: %v", err)
	}
	_ = ap.Close()
	if routes := fixture.manager.RouteCounters(); routes[RouteRouted] != 1 {
		t.Fatalf("route counters: %v", routes)
	}
}

func TestSourceMismatchAndAuthFailuresArePendingWithReason(t *testing.T) {
	fixture := newPendingFixture(t)
	ctx := context.Background()
	pinned, err := NewServer(Options{AllowedIP: "192.0.2.50", ID: "02-aa-00-00-00-31", Secret: secretOf(3), CertDER: fixture.cert, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Register("02-aa-00-00-00-31", "192.0.2.50", pinned); err != nil {
		t.Fatal(err)
	}
	open, err := NewServer(Options{ID: "02-aa-00-00-00-32", Secret: secretOf(4), CertDER: fixture.cert, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Register("02-aa-00-00-00-32", "", open); err != nil {
		t.Fatal(err)
	}
	if _, err := fakeap.Connect(ctx, fakeap.Config{URL: fixture.url, ID: "02-aa-00-00-00-31", Secret: secretOf(3)}); err == nil {
		t.Fatal("pinned AP authenticated from the wrong source")
	}
	// The wrong secret: the AP refuses the controller's proof and leaves.
	if _, err := fakeap.Connect(ctx, fakeap.Config{URL: fixture.url, ID: "02-aa-00-00-00-32", Secret: secretOf(9)}); !errors.Is(err, fakeap.ErrServerProof) {
		t.Fatalf("AP with another secret: %v", err)
	}
	reasons := map[string]string{}
	for _, entry := range fixture.manager.Pending() {
		reasons[entry.ID] = entry.Reason
	}
	if reasons["02-aa-00-00-00-31"] != PendingSourceMismatch {
		t.Fatalf("pending reasons: %v", reasons)
	}
	waitCounters(t, open, func(c Counters) bool { return c.AuthFailures[AuthStageProof] == 1 })
	waitPending(t, fixture.manager, "02-aa-00-00-00-32", PendingAuthFailed)
	if got := pinned.Counters().SourceMismatches; got != 1 {
		t.Fatalf("source mismatches = %d", got)
	}
	// A hello the session refuses (suite 2) is a hello-stage failure.
	hello := fmt.Sprintf(`{"version":2,"id":"02-aa-00-00-00-32","AuthType":"ap","model":"AP33-WW","firmware":"fw","suite":2,"counter":5,"QC":"%s","ENV":"production"}`,
		strings.Repeat("A", 43)+"=")
	if _, err := fakeap.Connect(ctx, fakeap.Config{URL: fixture.url, ID: "02-aa-00-00-00-32", Secret: secretOf(4), HelloJSON: []byte(hello)}); err == nil {
		t.Fatal("hello with suite 2 authenticated")
	}
	waitCounters(t, open, func(c Counters) bool { return c.AuthFailures[AuthStageHello] == 1 })
	// The right secret authenticates and clears the pending entry.
	ap, err := fakeap.Connect(ctx, fakeap.Config{URL: fixture.url, ID: "02-aa-00-00-00-32", Secret: secretOf(4)})
	if err != nil {
		t.Fatal(err)
	}
	defer ap.Close()
	waitPending(t, fixture.manager, "02-aa-00-00-00-32", "")
	if status := open.Status(); !status.Connected {
		t.Fatalf("AP not connected: %+v", status)
	}
}

// waitPending waits until the pending entry for id has reason, or is gone
// when reason is "".
func waitPending(t *testing.T, manager *Manager, id, reason string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := ""
		for _, entry := range manager.Pending() {
			if entry.ID == id {
				got = entry.Reason
			}
		}
		if got == reason {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %s = %q, want %q", id, got, reason)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
