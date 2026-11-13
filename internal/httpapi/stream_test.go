package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

func readSSEFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		if line == "\n" {
			return frame.String()
		}
		frame.WriteString(line)
	}
}

func TestSiteStreamEmitsInitialAndSiteChangeWithoutSecrets(t *testing.T) {
	handler, store, _ := platformFixture(t)
	handler.streamInterval = 20 * time.Millisecond
	handler.streamHeartbeat = time.Second
	handler.streamMaxAge = time.Second
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sites/a/stream", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream response: %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	initial := readSSEFrame(t, reader)
	if !strings.HasPrefix(initial, "event: update\ndata: ") || bytes.Contains([]byte(initial), []byte("example-passphrase-11")) || bytes.Contains([]byte(initial), []byte("secret")) || bytes.Contains([]byte(initial), []byte("mac")) {
		t.Fatalf("initial SSE exposed secret or short event: %s", initial)
	}
	var first struct {
		SiteID     string    `json:"site_id"`
		Revision   uint64    `json:"revision"`
		ObservedAt time.Time `json:"observed_at"`
		APs        []struct {
			ID              string `json:"id"`
			Connected       bool   `json:"connected"`
			AppliedRevision uint64 `json:"applied_revision"`
			FullFrames      uint64 `json:"full_frames"`
			FastFrames      uint64 `json:"fast_frames"`
			ClientCount     int    `json:"client_count"`
		} `json:"aps"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(strings.SplitN(initial, "\n", 2)[1], "data: "))), &first); err != nil {
		t.Fatal(err)
	}
	if first.SiteID != "a" || first.Revision != store.Snapshot().Revision || first.ObservedAt.IsZero() || len(first.APs) != 1 || first.APs[0].ID != "ap-a" {
		t.Fatalf("initial SSE payload: %+v", first)
	}
	if err := store.Update(func(state *model.State) error { state.Sites[0].Name = "Updated"; return nil }); err != nil {
		t.Fatal(err)
	}
	for {
		changed := readSSEFrame(t, reader)
		if strings.HasPrefix(changed, "event: update\n") && strings.Contains(changed, `"revision":2`) {
			break
		}
	}
}

func TestSiteStreamRejectsUnknownSite(t *testing.T) {
	handler, _, _ := platformFixture(t)
	response := request(t, handler, http.MethodGet, "/api/sites/missing/stream", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown site stream: %d %s", response.Code, response.Body.String())
	}
}

func TestSiteStreamClosesOnCancellation(t *testing.T) {
	handler, _, _ := platformFixture(t)
	handler.streamInterval = 20 * time.Millisecond
	handler.streamHeartbeat = time.Second
	handler.streamMaxAge = time.Second
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(done)
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/sites/a/stream", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = readSSEFrame(t, bufio.NewReader(response.Body))
	cancel()
	_ = response.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after request cancellation")
	}
}

func TestSiteStreamHeartbeatAndMaximumAge(t *testing.T) {
	handler, _, _ := platformFixture(t)
	handler.streamInterval = 20 * time.Millisecond
	handler.streamHeartbeat = 25 * time.Millisecond
	handler.streamMaxAge = 150 * time.Millisecond
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	response, err := client.Get(server.URL + "/api/sites/a/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	_ = readSSEFrame(t, reader)
	for {
		frame := readSSEFrame(t, reader)
		if frame == ": heartbeat\n" {
			break
		}
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
}

func TestStreamFingerprintTracksCountersAndMinuteBoundary(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Minute)
	base, err := streamFingerprint(model.Site{ID: "a"}, nil, nil, nil,
		[]streamAP{{ID: "ap-a", FullFrames: 6, FastFrames: 204}}, at.Unix()/60)
	if err != nil {
		t.Fatal(err)
	}
	changedCounter, err := streamFingerprint(model.Site{ID: "a"}, nil, nil, nil,
		[]streamAP{{ID: "ap-a", FullFrames: 6, FastFrames: 205}}, at.Unix()/60)
	if err != nil {
		t.Fatal(err)
	}
	changedMinute, err := streamFingerprint(model.Site{ID: "a"}, nil, nil, nil,
		[]streamAP{{ID: "ap-a", FullFrames: 6, FastFrames: 204}}, at.Add(time.Minute).Unix()/60)
	if err != nil {
		t.Fatal(err)
	}
	if base == changedCounter || base == changedMinute {
		t.Fatal("counter or minute boundary did not request a refresh")
	}
}

func TestSiteStreamFingerprintIgnoresOtherSiteEdit(t *testing.T) {
	handler, store, _ := platformFixture(t)
	if err := store.Update(func(state *model.State) error {
		state.APs[0].ConfigRevision = state.Revision + 1
		state.APs[0].ConfigUpdatedAt = state.UpdatedAt
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1794475905, 0).UTC()
	_, first, found, err := handler.siteStreamSnapshot("a", at)
	if err != nil || !found {
		t.Fatalf("initial fingerprint: %v", err)
	}
	if err := store.Update(func(state *model.State) error { state.Sites[1].Name = "Other site changed"; return nil }); err != nil {
		t.Fatal(err)
	}
	_, second, found, err := handler.siteStreamSnapshot("a", at)
	if err != nil || !found || first != second {
		t.Fatalf("other site changed site A stream: found=%v err=%v", found, err)
	}
}
