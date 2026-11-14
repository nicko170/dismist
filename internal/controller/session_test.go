package controller

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/tools/fakeap"
)

// sessionFixture is one registered AP behind a Manager on a TLS server.
type sessionFixture struct {
	pendingFixture
	server *Server
	config fakeap.Config
}

func newSessionFixture(t *testing.T, options Options) sessionFixture {
	t.Helper()
	fixture := newPendingFixture(t)
	if options.ID == "" {
		options.ID = "02-aa-00-00-00-41"
	}
	options.Secret = secretOf(5)
	options.CertDER = fixture.cert
	server, err := NewServer(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Register(options.ID, "", server); err != nil {
		t.Fatal(err)
	}
	return sessionFixture{pendingFixture: fixture, server: server, config: fakeap.Config{
		URL: fixture.url, ID: options.ID, Secret: options.Secret, Extras: fakeap.DefaultExtras(),
	}}
}

func (f sessionFixture) connect(t *testing.T, extras fakeap.Extras) *fakeap.AP {
	t.Helper()
	config := f.config
	config.Extras = extras
	before := f.server.Counters().Connections
	ap, err := fakeap.Connect(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	waitCounters(t, f.server, func(c Counters) bool { return c.Connections > before })
	return ap
}

func lastEvent(t *testing.T, server *Server, kind string) ConnectionEvent {
	t.Helper()
	events := server.TelemetrySnapshot().Events
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind == kind {
			return events[index]
		}
	}
	t.Fatalf("no %s event in %+v", kind, events)
	return ConnectionEvent{}
}

func waitStatus(t *testing.T, server *Server, ready func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		status := server.Status()
		if ready(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never became ready: %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConnectionEventsTellRebootFromReconnect(t *testing.T) {
	fixture := newSessionFixture(t, Options{Config: []byte(`{"Timestamp":1}`)})
	extras := fakeap.DefaultExtras()
	ap := fixture.connect(t, extras)
	if event := lastEvent(t, fixture.server, "connection"); event.Title != "AP authenticated" ||
		!strings.Contains(event.Detail, "boot count 7") || !strings.Contains(event.Detail, fakeap.DefaultFirmware) {
		t.Fatalf("first connection event: %+v", event)
	}
	status := fixture.server.Status()
	if status.Hello.SerialNo != "EXAMPLE000001" || status.Hello.LocalIP != "192.0.2.10" || status.HelloAt.IsZero() {
		t.Fatalf("hello extras not in status: %+v", status.Hello)
	}
	_ = ap.Close()
	waitStatus(t, fixture.server, func(s Status) bool { return !s.Connected })

	extras.Uptime += 30
	ap = fixture.connect(t, extras)
	if event := lastEvent(t, fixture.server, "connection"); event.Title != "AP reconnected" {
		t.Fatalf("reconnect event: %+v", event)
	}
	_ = ap.Close()
	waitStatus(t, fixture.server, func(s Status) bool { return !s.Connected })

	extras.BootCount++
	extras.Uptime = 40
	extras.LastRebootReason = "reboot/unknown"
	ap = fixture.connect(t, extras)
	defer ap.Close()
	if event := lastEvent(t, fixture.server, "reboot"); event.Title != "AP rebooted (reboot/unknown, warm)" ||
		!strings.Contains(event.Detail, "boot count 8") {
		t.Fatalf("reboot event: %+v", event)
	}
}

func TestDisconnectCausesAreRecorded(t *testing.T) {
	fixture := newSessionFixture(t, Options{Config: []byte(`{"Timestamp":1}`)})
	extras := fakeap.DefaultExtras()

	ap := fixture.connect(t, extras)
	fixture.server.DisconnectBecause("config change")
	<-ap.Done()
	status := waitStatus(t, fixture.server, func(s Status) bool { return !s.Connected })
	if status.LastDisconnectCause != "config change" || lastEvent(t, fixture.server, "connection").Detail != "config change" {
		t.Fatalf("controller disconnect cause: %+v", status)
	}

	ap = fixture.connect(t, extras)
	_ = ap.Close()
	status = waitStatus(t, fixture.server, func(s Status) bool { return !s.Connected })
	if status.LastDisconnectCause != "closed by the AP (code 1000)" {
		t.Fatalf("AP close cause: %q", status.LastDisconnectCause)
	}

	ap = fixture.connect(t, extras)
	_ = ap.Drop()
	status = waitStatus(t, fixture.server, func(s Status) bool { return !s.Connected })
	if status.LastDisconnectCause != "connection lost without a close frame" {
		t.Fatalf("dropped connection cause: %q", status.LastDisconnectCause)
	}

	first := fixture.connect(t, extras)
	second := fixture.connect(t, extras)
	defer second.Close()
	<-first.Done()
	status = waitStatus(t, fixture.server, func(s Status) bool { return s.LastDisconnectCause == "replaced by a new connection" })
	if !status.Connected {
		t.Fatalf("replacing session not connected: %+v", status)
	}
	counters := fixture.server.Counters()
	if counters.Connections != 5 || counters.Disconnects != 4 {
		t.Fatalf("connections %d disconnects %d", counters.Connections, counters.Disconnects)
	}
	fixture.manager.Remove(fixture.config.ID)
	<-second.Done()
	status = waitStatus(t, fixture.server, func(s Status) bool { return !s.Connected })
	if status.LastDisconnectCause != "registration withdrawn" {
		t.Fatalf("withdrawal cause: %q", status.LastDisconnectCause)
	}
}

func TestConfigDeliveryIsReportedAndCounted(t *testing.T) {
	config := []byte(`{"ID":"02-aa-00-00-00-41","Timestamp":1794475905.5}`)
	var mu sync.Mutex
	var deliveries []ConfigDelivery
	fixture := newSessionFixture(t, Options{Config: config, Revision: 9, OnConfigDelivered: func(delivery ConfigDelivery) {
		mu.Lock()
		deliveries = append(deliveries, delivery)
		mu.Unlock()
	}})
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	ctx := context.Background()
	got, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{})
	if err != nil || !bytes.Equal(got, config) {
		t.Fatalf("config %s %v", got, err)
	}
	mu.Lock()
	if len(deliveries) != 1 || !bytes.Equal(deliveries[0].Config, config) || deliveries[0].Revision != 9 ||
		deliveries[0].Timestamp != 1794475905.5 || deliveries[0].APID != fixture.config.ID || deliveries[0].At.IsZero() {
		t.Fatalf("delivery callback: %+v", deliveries)
	}
	mu.Unlock()
	if err := ap.ReportApplied(1794475905.5, "Can't unmarshal config"); err != nil {
		t.Fatal(err)
	}
	counters := waitCounters(t, fixture.server, func(c Counters) bool { return c.ConfigRejections == 1 })
	if counters.ConfigDeliveries != 1 || counters.Frames["ep-config"] != 1 || counters.Frames["get-time"] != 0 ||
		counters.Frames["ep-auth"] != 2 || counters.Frames["ep-telemetry"] != 1 {
		t.Fatalf("counters %+v", counters)
	}
	if _, err := ap.Send(2, "wlc-site-rest", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ap.Send(2, "made-up-subject", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ap.Ping(ctx, []byte("x")); err != nil {
		t.Fatal(err)
	}
	counters = fixture.server.Counters()
	if counters.Frames["wlc-site-rest"] != 1 || counters.Frames[SubjectOther] != 1 || counters.Frames["ping"] != 1 {
		t.Fatalf("frame counters %+v", counters.Frames)
	}
}

func TestDeliveredFallbackIsServedAndCompileErrorShown(t *testing.T) {
	delivered := []byte(`{"ID":"02-aa-00-00-00-41","Timestamp":1700000000.25}`)
	fixture := newSessionFixture(t, Options{Config: delivered, Revision: 4, ConfigSource: ConfigSourceDelivered, CompileError: "advanced config patch must be a JSON object"})
	status := fixture.server.Status()
	if status.ConfigSource != ConfigSourceDelivered || status.CompileError == "" || fixture.server.Counters().CompileErrors != 1 {
		t.Fatalf("status %+v", status)
	}
	if event := lastEvent(t, fixture.server, "config"); !strings.Contains(event.Title, "serving the last delivered") {
		t.Fatalf("compile error event %+v", event)
	}
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	got, err := ap.FetchConfig(context.Background(), fakeap.ConfigRequest{})
	if err != nil || !bytes.Equal(got, delivered) {
		t.Fatalf("delivered fallback not served: %s %v", got, err)
	}
	fixed := []byte(`{"ID":"02-aa-00-00-00-41","Timestamp":1700000100}`)
	if changed, err := fixture.server.SetConfigIfNewer(fixed, 5); err != nil || !changed {
		t.Fatalf("fixed config: %t %v", changed, err)
	}
	if status := fixture.server.Status(); status.ConfigSource != ConfigSourceCompiled || status.CompileError != "" {
		t.Fatalf("compile error not cleared: %+v", status)
	}
}

func TestAPWithoutAnyConfigIsNotAnswered(t *testing.T) {
	fixture := newSessionFixture(t, Options{ConfigSource: ConfigSourceNone, CompileError: "AP 02-aa-00-00-00-41 has no site"})
	fixture.config.Timeout = 300 * time.Millisecond
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	ctx := context.Background()
	if _, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{}); err == nil {
		t.Fatal("an AP without a compiled or delivered config was sent a config")
	}
	if _, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{}); err == nil {
		t.Fatal("second request answered")
	}
	if _, err := ap.Ping(ctx, []byte("still here")); err != nil {
		t.Fatalf("session broken by the missing config: %v", err)
	}
	counters := fixture.server.Counters()
	if counters.ConfigUnavailable != 2 || counters.ConfigDeliveries != 0 {
		t.Fatalf("counters %+v", counters)
	}
	unavailable := 0
	for _, event := range fixture.server.TelemetrySnapshot().Events {
		if event.Title == "Configuration unavailable" {
			unavailable++
		}
	}
	if unavailable != 1 {
		t.Fatalf("%d unavailable events, want 1 per session", unavailable)
	}
	if config, _ := fixture.server.ConfigSnapshot(); config != nil {
		t.Fatalf("config snapshot %s", config)
	}
	config := []byte(`{"Timestamp":2}`)
	if changed, err := fixture.server.SetConfigIfNewer(config, 0); err != nil || !changed {
		t.Fatalf("first compiled config at revision 0: %t %v", changed, err)
	}
	got, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{})
	if err != nil || !bytes.Equal(got, config) {
		t.Fatalf("config after fix: %s %v", got, err)
	}
}

func TestNewServerConfigSourceValidation(t *testing.T) {
	cases := []Options{
		{ID: "a", CertDER: []byte{1}, ConfigSource: ConfigSourceNone, Config: []byte(`{}`)},
		{ID: "a", CertDER: []byte{1}, ConfigSource: "other", Config: []byte(`{}`)},
		{ID: "a", CertDER: []byte{1}, ConfigSource: ConfigSourceDelivered},
		{ID: "a", CertDER: []byte{1}},
	}
	for index, options := range cases {
		if _, err := NewServer(options); err == nil {
			t.Errorf("case %d accepted: %+v", index, options)
		}
	}
	if err := CheckUDPProbe("not-an-address", nil, false, 0); err == nil {
		t.Fatal("invalid UDP probe accepted")
	}
}
