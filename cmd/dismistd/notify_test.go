package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

func TestNotifierPostsNewWarningsOnce(t *testing.T) {
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "home", Name: "Home"}}
		state.APs = []model.APRecord{{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "home", SecretFile: "secret.bin"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies []string
	fail := true
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		mu.Lock()
		defer mu.Unlock()
		if fail {
			fail = false
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		bodies = append(bodies, string(raw))
	}))
	defer receiver.Close()
	config := notifyConfig{url: receiver.URL, minSeverity: "warning", interval: 5 * time.Second}
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	n := newNotifier(config, store)
	n.since = time.Now().Add(-time.Minute)
	now := time.Now().UTC()
	for _, event := range []model.EventV2{
		{SiteID: "home", APID: "02-aa-00-00-00-01", Kind: "connection", Severity: "info", Title: "AP connected", FirstSeen: now, LastSeen: now},
		{SiteID: "home", APID: "02-aa-00-00-00-01", Kind: "dhcp_failure", Severity: "warning", Title: "DHCP failed", ClientMAC: "02-bb-00-00-00-01", FirstSeen: now, LastSeen: now},
	} {
		if _, err := store.Events2().AppendController(event); err != nil {
			t.Fatal(err)
		}
	}
	events := n.collect()
	if len(events) != 1 || events[0].Kind != "dhcp_failure" || events[0].APName != "Hallway" || !events[0].HasClient {
		t.Fatalf("collected: %+v", events)
	}
	if err := n.post(context.Background(), events); err == nil {
		t.Fatal("a 502 was not an error")
	}
	if err := n.post(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if again := n.collect(); len(again) != 0 {
		t.Fatalf("event sent twice: %+v", again)
	}
	mu.Lock()
	defer mu.Unlock()
	var body notification
	if len(bodies) != 1 || json.Unmarshal([]byte(bodies[0]), &body) != nil || len(body.Events) != 1 || strings.Contains(bodies[0], "02-bb") {
		t.Fatalf("webhook body: %v", bodies)
	}
	for _, bad := range []notifyConfig{{url: "ftp://x"}, {url: "https://x", minSeverity: "loud", interval: time.Minute}, {url: "https://x", minSeverity: "info", interval: time.Second}} {
		if bad.validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
