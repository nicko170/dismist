package fakeap_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/telemetry"
	"github.com/nicko170/dismist/internal/telemetry/pbtest"
	"github.com/nicko170/dismist/tools/fakeap"
)

func controllerFixture(t *testing.T, id string, secret [32]byte, config []byte) (*controller.Server, string) {
	t.Helper()
	manager := controller.NewManager()
	tlsServer := httptest.NewTLSServer(manager)
	t.Cleanup(tlsServer.Close)
	server, err := controller.NewServer(controller.Options{
		ID: id, Secret: secret, CertDER: tlsServer.Certificate().Raw, Config: config, Revision: 3, ServerName: "example-controller",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register(id, "", server); err != nil {
		t.Fatal(err)
	}
	return server, "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws"
}

func TestHandshakeConfigAndTelemetryAgainstController(t *testing.T) {
	var secret [32]byte
	secret[0] = 42
	config := []byte(`{"ID":"02-aa-00-00-00-61","Timestamp":1794475905.25}`)
	server, url := controllerFixture(t, "02-aa-00-00-00-61", secret, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ap, err := fakeap.Connect(ctx, fakeap.Config{URL: url, ID: "02-aa-00-00-00-61", Secret: secret, Extras: fakeap.DefaultExtras(), Counter: 99})
	if err != nil {
		t.Fatal(err)
	}
	session := ap.Session()
	if session.ServerName != "example-controller" || session.Reply3.NextCounter != 100 || session.ServerTime.IsZero() {
		t.Fatalf("session %+v", session)
	}
	var fetched []byte
	done := make(chan error, 1)
	go func() {
		done <- ap.Run(ctx, fakeap.RunOptions{
			Snapshot: fakeap.DefaultSnapshot(2), FastInterval: 20 * time.Millisecond, FullInterval: 50 * time.Millisecond,
			PingInterval: 30 * time.Millisecond, EventInterval: 40 * time.Millisecond,
			OnConfig: func(body []byte) { fetched = body },
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, stats := server.Status(), server.TelemetrySnapshot()
		if status.AppliedRevision == 3 && stats.FullFrames >= 2 && stats.FastFrames >= 2 && len(stats.RecentClients) != 0 {
			if len(stats.VAPs) != 2 || stats.VAPs[1].SSID != "Example" || stats.VAPs[1].Interface != "wl1.1" ||
				len(stats.AssociatedClients) != 2 || stats.AssociatedClients[0].IP != "192.0.2.100" ||
				len(stats.Interfaces) != 2 || stats.Interfaces[0].Addresses[0] != "192.0.2.10/24" {
				t.Fatalf("telemetry %+v", stats)
			}
			if status.Hello.SerialNo != "EXAMPLE000001" || status.Model != fakeap.DefaultModel {
				t.Fatalf("status %+v", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("AP never applied and reported: %+v %+v", status, stats)
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.Disconnect()
	select {
	case err := <-done:
		if !errors.Is(err, fakeap.ErrClosed) {
			t.Fatalf("Run after controller disconnect: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not end when the controller disconnected")
	}
	if !bytes.Equal(fetched, config) {
		t.Fatalf("fetched %s", fetched)
	}
}

func TestWrongSecretIsDetectedByTheAP(t *testing.T) {
	var secret, other [32]byte
	secret[0], other[0] = 1, 2
	_, url := controllerFixture(t, "02-aa-00-00-00-62", secret, []byte(`{}`))
	if _, err := fakeap.Connect(context.Background(), fakeap.Config{URL: url, ID: "02-aa-00-00-00-62", Secret: other}); !errors.Is(err, fakeap.ErrServerProof) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := fakeap.Connect(context.Background(), fakeap.Config{URL: url, ID: "02-aa-00-00-00-63", Secret: secret}); !errors.Is(err, fakeap.ErrNotAuthenticated) {
		t.Fatalf("unknown ID: %v", err)
	}
}

func TestSyntheticStatsDecode(t *testing.T) {
	snapshot := fakeap.DefaultSnapshot(2)
	full, err := telemetry.Decode(pbtest.Frame(fakeap.TopicFull, "02-aa-00-00-00-64", snapshot.Stats("02-aa-00-00-00-64", "AP33-WW", "fw-1", true)))
	if err != nil {
		t.Fatal(err)
	}
	if full.Topic != fakeap.TopicFull || full.Model != "AP33-WW" || full.Firmware != "fw-1" || full.UptimeSeconds != 120 ||
		len(full.VAPs) != 2 || len(full.Clients) != 2 || full.Clients[0].Name != "client-1" || full.Clients[0].RSSI != -50 ||
		full.Clients[0].TxMbps != 866 || full.Clients[1].MAC != "02:aa:00:00:20:02" {
		t.Fatalf("full frame %+v", full)
	}
	fast, err := telemetry.Decode(pbtest.Gzip(pbtest.Frame(fakeap.TopicFast, "02-aa-00-00-00-64", snapshot.Stats("02-aa-00-00-00-64", "AP33-WW", "fw-1", false))))
	if err != nil {
		t.Fatal(err)
	}
	if fast.Topic != fakeap.TopicFast || len(fast.VAPs) != 0 || len(fast.Clients) != 2 || fast.Clients[0].Name != "" ||
		fast.Clients[0].TxMbps != 0 || fast.Clients[0].SSID != "Example" {
		t.Fatalf("fast frame %+v", fast)
	}
}
