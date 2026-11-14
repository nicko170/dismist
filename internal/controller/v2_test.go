package controller

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/protocol"
	"github.com/nicko170/dismist/internal/telemetry"
	"github.com/nicko170/dismist/tools/fakeap"
)

// recorder collects what a Server hands to its callbacks.
type recorder struct {
	mu        sync.Mutex
	events    []Event
	pushes    []PushEvent
	verified  []Verification
	learned   []string
	vaps      [][]telemetry.VAP
	tunnel    string
	requests  []ConfigRequest
	hellos    int
	reboots   []Reboot
	stored    *HelloExtras
	storedAt  time.Time
	expired   int
	outputs   []telemetry.CommandOutput
	scans     int
	delivered []ConfigDelivery
}

func (r *recorder) options(options Options) Options {
	options.OnEvent = func(event Event) { r.mu.Lock(); r.events = append(r.events, event); r.mu.Unlock() }
	options.OnPush = func(event PushEvent) { r.mu.Lock(); r.pushes = append(r.pushes, event); r.mu.Unlock() }
	options.OnVerified = func(_ string, result Verification) {
		r.mu.Lock()
		r.verified = append(r.verified, result)
		r.mu.Unlock()
	}
	options.OnPushModeLearned = func(_ string, mode string) { r.mu.Lock(); r.learned = append(r.learned, mode); r.mu.Unlock() }
	options.OnLocateExpired = func(string) { r.mu.Lock(); r.expired++; r.mu.Unlock() }
	options.OnCommandOutput = func(_ string, output telemetry.CommandOutput) {
		r.mu.Lock()
		r.outputs = append(r.outputs, output)
		r.mu.Unlock()
	}
	options.OnScan = func(string, time.Time, telemetry.Scan) { r.mu.Lock(); r.scans++; r.mu.Unlock() }
	options.OnConfigDelivered = func(delivery ConfigDelivery) {
		r.mu.Lock()
		r.delivered = append(r.delivered, delivery)
		r.mu.Unlock()
	}
	options.Status = r
	return options
}

func (r *recorder) RecordHello(_ string, at time.Time, _ string, hello HelloExtras) (*HelloExtras, time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hellos++
	previous, previousAt := r.stored, r.storedAt
	stored := hello
	r.stored, r.storedAt = &stored, at
	return previous, previousAt, nil
}

func (r *recorder) RecordReboot(_ string, reboot Reboot) error {
	r.mu.Lock()
	r.reboots = append(r.reboots, reboot)
	r.mu.Unlock()
	return nil
}

func (r *recorder) SetVAPs(_ string, _ time.Time, vaps []telemetry.VAP) (bool, error) {
	r.mu.Lock()
	r.vaps = append(r.vaps, vaps)
	r.mu.Unlock()
	return true, nil
}

func (r *recorder) SetTunnelIPv6(_ string, _ time.Time, address string) error {
	r.mu.Lock()
	r.tunnel = address
	r.mu.Unlock()
	return nil
}

func (r *recorder) SetConfigRequest(_ string, request ConfigRequest) error {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()
	return nil
}

func (r *recorder) wait(t *testing.T, what string, ready func(*recorder) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		ok := ready(r)
		r.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			r.mu.Lock()
			t.Fatalf("%s never happened: events %+v pushes %+v verified %+v", what, r.events, r.pushes, r.verified)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *recorder) pushStates() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var states []string
	for _, push := range r.pushes {
		states = append(states, push.State)
	}
	return states
}

func (r *recorder) hasEvent(kind, title string) bool {
	for _, event := range r.events {
		if event.Kind == kind && strings.Contains(event.Title, title) {
			return true
		}
	}
	return false
}

func fastTimings() Timings {
	return Timings{PushFetch: 300 * time.Millisecond, ReconnectFetch: 3 * time.Second, PushApply: 3 * time.Second,
		PushCoalesce: 200 * time.Millisecond, Verify: 100 * time.Millisecond, Drift: 50 * time.Millisecond}
}

// labConfig returns a compiled-looking AP33 config: the bundled template
// with AU/890, 5 GHz 36/80 at 10 dBm, 2.4 GHz 11/20 at 6 dBm and one WLAN
// "Example" (BSSID index 1) on both bands.
func labConfig(t *testing.T, timestamp float64) []byte {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal(aptemplate.Default(), &config); err != nil {
		t.Fatal(err)
	}
	config["Timestamp"] = timestamp
	config["Country"] = "AU"
	for _, raw := range config["Radios"].([]any) {
		radio := raw.(map[string]any)
		radio["CCRev"] = "AU/890"
		rrm := radio["RRM"].(map[string]any)
		switch radio["Band"] {
		case "5":
			radio["Channel"], radio["Width"], radio["Power"] = 36, 80, 10
			rrm["Channel"], rrm["Width"], rrm["Power"] = 36, 80, 10
		case "2.4":
			radio["Channel"], radio["Width"], radio["Power"] = 11, 20, 6
			rrm["Channel"], rrm["Width"], rrm["Power"] = 11, 20, 6
		}
	}
	wlans := config["WLANs"].([]any)
	wlan := wlans[0].(map[string]any)
	wlan["ID"] = "00000000-0000-4000-8000-000000000001"
	wlan["SSID"] = "Example"
	wlan["Enabled"] = true
	wlan["Vlans"] = []any{"1"}
	wlan["Auth"].(map[string]any)["PSK"] = "example-passphrase"
	for _, raw := range wlan["BandConfigs"].([]any) {
		raw.(map[string]any)["BSSIDIndex"] = 1
	}
	config["WLANs"] = []any{wlan}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// runAP runs fakeap until it ends and returns Run's error on a channel.
func runAP(ap *fakeap.AP, options fakeap.RunOptions) <-chan error {
	done := make(chan error, 1)
	go func() { done <- ap.Run(context.Background(), options) }()
	return done
}

func quickRun(snapshot fakeap.Snapshot) fakeap.RunOptions {
	return fakeap.RunOptions{Snapshot: snapshot, FastInterval: 20 * time.Millisecond, FullInterval: 40 * time.Millisecond, PingInterval: -1}
}

func TestSubjectRepliesAreFeaturesOffByDefault(t *testing.T) {
	rec := &recorder{}
	fixture := newSessionFixture(t, rec.options(Options{Config: []byte(`{"Timestamp":1}`), Revision: 1}))
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := ap.Subscribe(short, "rogue-clients-request-"); err == nil {
		t.Fatal("ep-subscribe was answered with subscribe_reply off")
	}
	reply, err := ap.Ping2(context.Background(), time.UnixMicro(1234))
	if err != nil || !bytes.Equal(reply, binary.AppendUvarint([]byte{8}, 1234)) {
		t.Fatalf("ping2 without pong2 is an echo: %x %v", reply, err)
	}
	fixture.server.SetFeatures(Features(0).With(FeatureSubscribeReply).With(FeatureFileDiscard).With(FeatureSiteRest404).With(FeaturePong2))
	ctx := context.Background()
	if body, err := ap.Subscribe(ctx, "rogue-clients-request-"); err != nil || string(body) != `{"Partition":0,"Err":""}` {
		t.Fatalf("subscribe reply %s %v", body, err)
	}
	if body, err := ap.OfferFile(ctx, "/rw/var/log/errors/ep-control.report.1", 57159); err != nil ||
		string(body) != `{"SendIt":false,"Discard":true,"ErrorMsg":""}` {
		t.Fatalf("file reply %s %v", body, err)
	}
	if body, err := ap.SiteRest(ctx); err != nil || !strings.Contains(string(body), `"StatusCode":404`) {
		t.Fatalf("site rest reply %s %v", body, err)
	}
	reply, err = ap.Ping2(ctx, time.UnixMicro(1234))
	if err != nil || len(reply) < 4 || reply[0] != 8 || ping2Sent(reply) != 1234 || !bytes.Contains(reply, []byte{0x10}) {
		t.Fatalf("pong2 %x %v", reply, err)
	}
	if err := ap.RegisterIP([16]byte{0xfd, 0x28, 15: 1}); err != nil {
		t.Fatal(err)
	}
	counters := waitCounters(t, fixture.server, func(c Counters) bool { return c.IPRegistrations == 1 })
	if counters.Replies["subscribe_reply"] != 1 || counters.Replies["file_discard"] != 1 || counters.Replies["site_rest_404"] != 1 ||
		counters.Replies["pong2"] != 1 || counters.FileOffers != 1 {
		t.Fatalf("reply counters %+v", counters)
	}
	status := fixture.server.Status()
	if status.TunnelIPv6 != "fd28::1" || status.LastFileOffer == nil || !status.LastFileOffer.Discarded ||
		status.LastFileOffer.Size != 57159 || !status.Capabilities.Pong2 {
		t.Fatalf("status %+v", status)
	}
	rec.wait(t, "tunnel recorded and feature events", func(r *recorder) bool {
		return r.tunnel == "fd28::1" && r.hasEvent(EventFeature, "pong2 switched on") && r.hasEvent(EventFile, "crash report")
	})
}

// TestPushReconnectByDefault: with config_change_notify off a push closes
// the session; the AP reconnects, fetches and applies.
func TestPushReconnectByDefault(t *testing.T) {
	rec := &recorder{}
	options := rec.options(Options{Config: labConfig(t, 100), Revision: 1, Timings: fastTimings()})
	fixture := newSessionFixture(t, options)
	ap := fixture.connect(t, fakeap.DefaultExtras())
	done := runAP(ap, quickRun(fakeap.DefaultSnapshot(1)))
	waitStatus(t, fixture.server, func(s Status) bool { return s.AppliedRevision == 1 })
	handle, err := fixture.server.Push(labConfig(t, 200), 2)
	if err != nil || handle.Mode != PushModeReconnect || handle.State != PushStateReconnecting || !handle.Changed {
		t.Fatalf("push %+v %v", handle, err)
	}
	if err := <-done; !errors.Is(err, fakeap.ErrClosed) {
		t.Fatalf("reconnect push did not close the session: %v", err)
	}
	ap = fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	runAP(ap, quickRun(fakeap.DefaultSnapshot(1)))
	rec.wait(t, "applied", func(r *recorder) bool {
		return len(r.pushes) != 0 && r.pushes[len(r.pushes)-1].State == PushStateApplied
	})
	if states := rec.pushStates(); strings.Join(states, ",") != "reconnecting,fetched,applied" {
		t.Fatalf("push states %v", states)
	}
	if counters := fixture.server.Counters(); counters.Pushes[PushModeReconnect][PushResultApplied] != 1 {
		t.Fatalf("push counters %+v", counters.Pushes)
	}
	if fixture.server.Status().LastDisconnectCause != "config change" {
		t.Fatalf("cause %q", fixture.server.Status().LastDisconnectCause)
	}
	unchanged, _ := fixture.server.Push(labConfig(t, 200), 2)
	if unchanged.State != PushStateUnchanged {
		t.Fatalf("an equal config pushes nothing: %+v", unchanged)
	}
}

// TestPushNotifyFetchesWithoutReconnect: ep-config-change with handle 0
// and an AuditID body; the AP re-fetches on the same session.
func TestPushNotifyFetchesWithoutReconnect(t *testing.T) {
	rec := &recorder{}
	options := rec.options(Options{Config: labConfig(t, 100), Revision: 1, Timings: fastTimings(),
		Features: Features(0).With(FeatureConfigChangeNotify)})
	fixture := newSessionFixture(t, options)
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	var notified sync.Mutex
	var frames []string
	run := quickRun(fakeap.DefaultSnapshot(1))
	run.OnConfigChange = func(message protocol.Message) {
		notified.Lock()
		frames = append(frames, fmt.Sprintf("%s/%d/%d", message.Body, message.Handle, message.ServiceClass))
		notified.Unlock()
	}
	runAP(ap, run)
	waitStatus(t, fixture.server, func(s Status) bool { return s.AppliedRevision == 1 })
	connections := fixture.server.Status().ConnectionCount
	handle, err := fixture.server.Push(labConfig(t, 200), 2)
	if err != nil || handle.Mode != PushModeNotify || handle.State != PushStateNotified {
		t.Fatalf("push %+v %v", handle, err)
	}
	rec.wait(t, "applied", func(r *recorder) bool {
		return len(r.pushes) != 0 && r.pushes[len(r.pushes)-1].State == PushStateApplied
	})
	status := fixture.server.Status()
	if status.ConnectionCount != connections || status.AppliedRevision != 2 || status.PushMode != PushModeNotify ||
		status.Capabilities.ConfigChangeNotify == nil || !*status.Capabilities.ConfigChangeNotify {
		t.Fatalf("status after notify push %+v", status)
	}
	notified.Lock()
	if len(frames) != 1 || frames[0] != `{"AuditID":"dismist-r2"}/0/2` {
		t.Fatalf("ep-config-change frames %v", frames)
	}
	notified.Unlock()
	if states := rec.pushStates(); strings.Join(states, ",") != "notified,fetched,applied" {
		t.Fatalf("push states %v", states)
	}
	rec.wait(t, "learned notify", func(r *recorder) bool { return len(r.learned) == 1 && r.learned[0] == PushModeNotify })
}

// TestPushFallbackLearnsReconnect: an AP that ignores ep-config-change is
// reconnected after the fetch timeout; two fallbacks in a row learn
// "reconnect", and switching the feature on again forgets it.
func TestPushFallbackLearnsReconnect(t *testing.T) {
	rec := &recorder{}
	options := rec.options(Options{Config: labConfig(t, 100), Revision: 1, Timings: fastTimings(),
		Features: Features(0).With(FeatureConfigChangeNotify)})
	fixture := newSessionFixture(t, options)
	run := quickRun(fakeap.DefaultSnapshot(1))
	run.IgnoreConfigChange = true
	for round := range 2 {
		ap := fixture.connect(t, fakeap.DefaultExtras())
		done := runAP(ap, run)
		waitStatus(t, fixture.server, func(s Status) bool { return s.AppliedRevision == uint64(round+1) })
		if _, err := fixture.server.Push(labConfig(t, float64(200+round)), uint64(round+2)); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, fakeap.ErrClosed) {
			t.Fatalf("round %d: no fallback reconnect: %v", round, err)
		}
	}
	rec.wait(t, "learned reconnect", func(r *recorder) bool { return len(r.learned) == 1 && r.learned[0] == PushModeReconnect })
	status := fixture.server.Status()
	if status.PushMode != PushModeReconnect || status.Capabilities.ConfigChangeNotify == nil || *status.Capabilities.ConfigChangeNotify {
		t.Fatalf("status %+v", status)
	}
	if counters := fixture.server.Counters(); counters.Pushes[PushModeNotify][PushResultFallback] != 2 {
		t.Fatalf("fallback counters %+v", counters.Pushes)
	}
	if !rec.hasEvent(EventPush, "push mode is now reconnect") {
		t.Fatal("no push fallback event")
	}
	fixture.server.SetFeatures(0)
	fixture.server.SetFeatures(Features(0).With(FeatureConfigChangeNotify))
	if fixture.server.PushMode() != PushModeNotify {
		t.Fatal("switching config_change_notify on again did not forget the learned reconnect")
	}
}

func TestPushRejectedAndCoalesced(t *testing.T) {
	rec := &recorder{}
	options := rec.options(Options{Config: labConfig(t, 100), Revision: 1, Timings: fastTimings(),
		Features: Features(0).With(FeatureConfigChangeNotify)})
	fixture := newSessionFixture(t, options)
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	var mu sync.Mutex
	notifies := 0
	run := quickRun(fakeap.DefaultSnapshot(1))
	run.OnConfigChange = func(protocol.Message) { mu.Lock(); notifies++; mu.Unlock() }
	runAP(ap, run)
	waitStatus(t, fixture.server, func(s Status) bool { return s.AppliedRevision == 1 })
	for revision := uint64(2); revision <= 4; revision++ {
		if _, err := fixture.server.Push(labConfig(t, float64(100+revision)), revision); err != nil {
			t.Fatal(err)
		}
	}
	waitStatus(t, fixture.server, func(s Status) bool { return s.AppliedRevision == 4 })
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if notifies > 2 {
		t.Fatalf("three pushes within the coalescing window sent %d notifies", notifies)
	}
	mu.Unlock()
	if push := fixture.server.Status().Push; push == nil || push.State != PushStateApplied || push.Revision != 4 {
		t.Fatalf("last push %+v", push)
	}

	rejecting := &recorder{}
	rejectFixture := newSessionFixture(t, rejecting.options(Options{ID: "02-aa-00-00-00-42", Config: labConfig(t, 100), Revision: 1,
		Timings: fastTimings(), Features: Features(0).With(FeatureConfigChangeNotify)}))
	other := rejectFixture.connect(t, fakeap.DefaultExtras())
	defer other.Close()
	rejectRun := quickRun(fakeap.DefaultSnapshot(1))
	rejectRun.RejectConfig = "Can't unmarshal config"
	runAP(other, rejectRun)
	waitCounters(t, rejectFixture.server, func(c Counters) bool { return c.ConfigRejections >= 1 })
	if _, err := rejectFixture.server.Push(labConfig(t, 300), 2); err != nil {
		t.Fatal(err)
	}
	rejecting.wait(t, "rejected", func(r *recorder) bool {
		return len(r.pushes) != 0 && r.pushes[len(r.pushes)-1].State == PushStateRejected && r.pushes[len(r.pushes)-1].Error != ""
	})
	if counters := rejectFixture.server.Counters(); counters.Pushes[PushModeNotify][PushResultRejected] != 1 {
		t.Fatalf("rejected counters %+v", counters.Pushes)
	}
}

func TestPushToOfflineAPIsDeliveredOnConnect(t *testing.T) {
	fixture := newSessionFixture(t, Options{Config: labConfig(t, 100), Revision: 1})
	handle, err := fixture.server.Push(labConfig(t, 200), 2)
	if err != nil || handle.State != PushStateOffline {
		t.Fatalf("offline push %+v %v", handle, err)
	}
	if _, err := fixture.server.Resend(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePushMode("sometimes"); err == nil {
		t.Fatal("bad -config-push accepted")
	}
	if mode, err := ParsePushMode(""); err != nil || mode != PushModeReconnect {
		t.Fatalf("default push mode %q %v", mode, err)
	}
}

// TestVerificationPassesAndCatchesMismatches runs the post-delivery
// verification against fakeap telemetry that matches the delivered
// config, then against telemetry on the wrong channel.
func TestVerificationPassesAndCatchesMismatches(t *testing.T) {
	rec := &recorder{}
	config := labConfig(t, 100)
	fixture := newSessionFixture(t, rec.options(Options{Config: config, Revision: 1, Timings: fastTimings(), LocalFiles: "none"}))
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	runAP(ap, quickRun(fakeap.DefaultSnapshot(1)))
	rec.wait(t, "verified", func(r *recorder) bool { return len(r.verified) != 0 })
	rec.mu.Lock()
	first := rec.verified[0]
	rec.mu.Unlock()
	if !first.OK || first.Revision != 1 || first.Unchanged {
		t.Fatalf("matching telemetry: %+v", first)
	}

	wrong := &recorder{}
	wrongFixture := newSessionFixture(t, wrong.options(Options{ID: "02-aa-00-00-00-43", Config: config, Revision: 1,
		Timings: fastTimings(), LocalFiles: "none", LastDelivered: config}))
	snapshot := fakeap.DefaultSnapshot(1)
	snapshot.Radios[1].Channel = 6
	snapshot.WLANs[1].Dev = "wl1.2"
	snapshot.Country = "US"
	other := wrongFixture.connect(t, fakeap.DefaultExtras())
	defer other.Close()
	runAP(other, quickRun(snapshot))
	wrong.wait(t, "verified", func(r *recorder) bool { return len(r.verified) != 0 })
	wrong.mu.Lock()
	result := wrong.verified[0]
	wrong.mu.Unlock()
	joined := strings.Join(result.Mismatches, "; ")
	if result.OK || !result.Unchanged || !strings.Contains(joined, "2.4 GHz radio on channel 6, configured 11") ||
		!strings.Contains(joined, "expected BSSID index 1") || !strings.Contains(joined, "country US") {
		t.Fatalf("mismatches %+v", result)
	}
	status := wrongFixture.server.Status()
	if !status.VerifyMismatch || status.Verification == nil || status.Verification.OK {
		t.Fatalf("status %+v", status)
	}
	if counters := wrongFixture.server.Counters(); counters.VerifyMismatches != 1 {
		t.Fatalf("counters %+v", counters)
	}
	wrong.wait(t, "mismatch event", func(r *recorder) bool { return r.hasEvent(EventConfig, "verification failed") })
}

// TestTelemetryEventsAndStatusSink checks the typed events and status
// facts the v2 merge produces: PACE upserts and noise, reboot from the
// boot count, clock skew, inactive VLAN raise and resolve, drift, the VAP
// list and the ep-config request.
func TestTelemetryEventsAndStatusSink(t *testing.T) {
	rec := &recorder{}
	config := labConfig(t, 100)
	fixture := newSessionFixture(t, rec.options(Options{Config: config, Revision: 1, Timings: fastTimings()}))
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	ctx := context.Background()
	if _, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{Tags: "emptydns,wpa1pairwise,allwlans,owe", MfgCountryCode: "WW", AppliedConfigTimestamp: 50}); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeap.DefaultSnapshot(1)
	snapshot.ClockOffset = -45 * time.Second
	snapshot.Inactive = []uint64{1}
	snapshot.ConfigTimestamp = 50
	if err := ap.SendStats(snapshot, true); err != nil {
		t.Fatal(err)
	}
	id := fakeap.NewEventID()
	for _, update := range []bool{false, true} {
		if err := ap.SendPaceEvent(fakeap.PaceEvent{ID: id, IsUpdate: update, Type: 1, Title: "DHCP failure",
			ClientMAC: "02-aa-00-00-20-01", Fields: map[string]any{"NumAttempts": 3}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ap.SendPaceEvent(fakeap.PaceEvent{Type: 42, Title: "PCAP File", Text: "No packets."}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	snapshot.Inactive = nil
	snapshot.BootCount = 8
	if err := ap.SendStats(snapshot, true); err != nil {
		t.Fatal(err)
	}
	counters := waitCounters(t, fixture.server, func(c Counters) bool { return c.PaceNoise == 1 && c.Reboots == 1 })
	if counters.Telemetry["ap-stats-full-"] != 2 || counters.ClockSkewWarnings != 1 {
		t.Fatalf("counters %+v", counters)
	}
	rec.wait(t, "events", func(r *recorder) bool {
		return r.hasEvent(EventClock, "AP clock is") && r.hasEvent(EventInactiveVLAN, "VLAN 1 is not active") &&
			r.hasEvent(EventInactiveVLAN, "VLAN 1 is active") && r.hasEvent(EventReboot, "AP rebooted") &&
			r.hasEvent(EventConfig, "different configuration") && len(r.vaps) == 2 && len(r.reboots) == 1 && len(r.requests) == 1 && r.scans == 2
	})
	rec.mu.Lock()
	var pace []Event
	raisedID, resolved := "", []string(nil)
	for _, event := range rec.events {
		switch {
		case event.PaceType != nil:
			pace = append(pace, event)
		case event.Kind == EventInactiveVLAN && event.ID != "":
			raisedID = event.ID
		case event.Kind == EventInactiveVLAN:
			resolved = event.Corrects
		}
	}
	request := rec.requests[0]
	rec.mu.Unlock()
	if len(pace) != 2 || pace[0].ID != id || !pace[1].IsUpdate || pace[0].Kind != telemetry.KindDHCPFailure || pace[0].ClientMAC != "02:aa:00:00:20:01" {
		t.Fatalf("PACE events %+v", pace)
	}
	if raisedID == "" || len(resolved) != 1 || resolved[0] != raisedID {
		t.Fatalf("inactive VLAN raise %q resolve %v", raisedID, resolved)
	}
	if strings.Join(request.Capabilities, ",") != "allwlans,emptydns,owe,wpa1pairwise" || request.MfgCountryCode != "WW" ||
		!fixture.server.Status().ConfigRequest.HasCapability("OWE") {
		t.Fatalf("config request %+v", request)
	}
	buffered := 0
	for _, event := range fixture.server.TelemetrySnapshot().Events {
		if event.ID == id {
			buffered++
		}
	}
	if buffered != 1 {
		t.Fatalf("an upserted PACE event is buffered once, got %d", buffered)
	}
	stats := fixture.server.TelemetrySnapshot()
	if stats.ClockSkewSeconds == nil || *stats.ClockSkewSeconds < 40 || len(stats.LastKnownVAPs) != 2 || stats.Scan == nil ||
		len(stats.KMsg) != 1 || stats.CPUBusy != nil || !fixture.server.Status().ConfigDrift {
		t.Fatalf("snapshot %+v", stats)
	}
}

func TestAPCommandsNeedTheFeature(t *testing.T) {
	rec := &recorder{}
	fixture := newSessionFixture(t, rec.options(Options{Config: labConfig(t, 100), Revision: 1, Timings: fastTimings()}))
	if _, err := fixture.server.Reboot("test"); !errors.Is(err, ErrUnverifiedCommand) {
		t.Fatalf("reboot without ap_cmd: %v", err)
	}
	fixture.server.SetFeatures(Features(0).With(FeatureAPCmd))
	if _, err := fixture.server.Reboot("test"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("reboot while offline: %v", err)
	}
	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	var mu sync.Mutex
	var commands []fakeap.Command
	run := quickRun(fakeap.DefaultSnapshot(1))
	run.OnCommand = func(command fakeap.Command) { mu.Lock(); commands = append(commands, command); mu.Unlock() }
	done := runAP(ap, run)
	waitStatus(t, fixture.server, func(s Status) bool { return s.AppliedRevision == 1 })
	ping, err := fixture.server.Ping("192.0.2.1", 3)
	if err != nil || ping.Session == "" {
		t.Fatalf("ping %+v %v", ping, err)
	}
	rec.wait(t, "ping output", func(r *recorder) bool { return len(r.outputs) == 1 })
	if output, ok := fixture.server.CommandOutput(ping.Session); !ok || !strings.Contains(output, "fakeap ping 192.0.2.1") {
		t.Fatalf("output %q %v", output, ok)
	}
	if _, err := fixture.server.DisconnectClients([]string{"02:aa:00:00:20:01"}, 8, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.RestartRadio("5", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.Reboot("operator request"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, fakeap.ErrRebooted) {
		t.Fatalf("fakeap did not reboot: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(commands) != 4 {
		t.Fatalf("commands %+v", commands)
	}
	disconnect, radio, reboot := commands[1], commands[2], commands[3]
	if disconnect.Cmd != CommandDisconnect || disconnect.Clients[0] != "02-aa-00-00-20-01" || string(disconnect.Params) != `{"Clients":["02-aa-00-00-20-01"],"Reason":8}` ||
		radio.Cmd != CommandRadioReinit || string(radio.Params) != `{"Radio":"r0"}` ||
		reboot.Cmd != CommandReboot || string(reboot.Params) != `{"Reason":"operator request"}` || reboot.Sender != "dismist" ||
		reboot.Handle != 0 || reboot.Class != 2 {
		t.Fatalf("command bodies %+v", commands)
	}
	if counters := fixture.server.Counters(); counters.Commands[CommandReboot] != 1 || counters.Commands[CommandPing] != 1 {
		t.Fatalf("command counters %+v", counters.Commands)
	}
	if _, err := fixture.server.Ping("bad host!", 1); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("invalid ping target: %v", err)
	}
}

func TestLocateTimerAndNotModified(t *testing.T) {
	rec := &recorder{}
	fixture := newSessionFixture(t, rec.options(Options{Config: labConfig(t, 100), Revision: 1, LocateUntil: time.Now().Add(50 * time.Millisecond)}))
	if fixture.server.Status().LocateUntil.IsZero() {
		t.Fatal("locate not armed")
	}
	rec.wait(t, "locate expiry", func(r *recorder) bool { return r.expired == 1 })
	if !fixture.server.LocateUntil().IsZero() {
		t.Fatal("locate still set after expiry")
	}
	fixture.server.SetLocateUntil(time.Now().Add(time.Hour))
	fixture.server.SetLocateUntil(time.Time{})
	time.Sleep(20 * time.Millisecond)
	rec.mu.Lock()
	if rec.expired != 1 {
		t.Fatal("a cleared locate fired")
	}
	rec.mu.Unlock()

	ap := fixture.connect(t, fakeap.DefaultExtras())
	defer ap.Close()
	ctx := context.Background()
	body, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{AppliedConfigTimestamp: 100})
	if err != nil || bytes.Equal(body, notModifiedReply) {
		t.Fatalf("not_modified is off by default: %s %v", body, err)
	}
	fixture.server.SetFeatures(Features(0).With(FeatureNotModified))
	body, err = ap.FetchConfig(ctx, fakeap.ConfigRequest{AppliedConfigTimestamp: 100})
	if err != nil || !bytes.Equal(body, notModifiedReply) {
		t.Fatalf("not_modified reply %s %v", body, err)
	}
	body, err = ap.FetchConfig(ctx, fakeap.ConfigRequest{AppliedConfigTimestamp: 99})
	if err != nil || bytes.Equal(body, notModifiedReply) {
		t.Fatalf("an older applied config gets the config: %s %v", body, err)
	}
}

func TestHelloRebootTimeNormalisation(t *testing.T) {
	now := time.Date(2026, 11, 14, 7, 56, 2, 0, time.UTC)
	reportedNow := HelloExtras{Uptime: 16827, LastRebootTime: now}
	reportedNow.normaliseRebootTime(now)
	want := now.Add(-16827 * time.Second)
	if !reportedNow.LastRebootTimeEstimated || !reportedNow.LastRebootTime.Equal(want) {
		t.Fatalf("a hello-time LastRebootTime becomes the boot time: %+v", reportedNow)
	}
	real := HelloExtras{Uptime: 400, LastRebootTime: now.Add(-410 * time.Second)}
	real.normaliseRebootTime(now)
	if real.LastRebootTimeEstimated || !real.LastRebootTime.Equal(now.Add(-410*time.Second)) {
		t.Fatalf("a plausible reboot time is kept: %+v", real)
	}
	noUptime := HelloExtras{LastRebootTime: now}
	noUptime.normaliseRebootTime(now)
	if !noUptime.LastRebootTime.IsZero() {
		t.Fatalf("a hello-time LastRebootTime without uptime is dropped: %+v", noUptime)
	}
}

// TestRebootDetectedAcrossControllerRestart: a new session object (a
// daemon restart) still sees the reboot through the persisted hello.
func TestRebootDetectedAcrossControllerRestart(t *testing.T) {
	rec := &recorder{}
	first := newSessionFixture(t, rec.options(Options{Config: labConfig(t, 100), Revision: 1}))
	ap := first.connect(t, fakeap.DefaultExtras())
	_ = ap.Close()
	second := newSessionFixture(t, rec.options(Options{Config: labConfig(t, 100), Revision: 1}))
	extras := fakeap.DefaultExtras()
	extras.BootCount++
	extras.Uptime = 30
	again := second.connect(t, extras)
	defer again.Close()
	rec.wait(t, "reboot recorded", func(r *recorder) bool { return len(r.reboots) == 1 && r.reboots[0].Evidence == RebootEvidenceBootCount })
	if second.server.Counters().Reboots != 1 || second.server.Status().LastRebootDetected.IsZero() {
		t.Fatalf("reboot not counted: %+v", second.server.Counters())
	}
}
