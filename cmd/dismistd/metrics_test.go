package main

import (
	"context"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/metrics"
	"github.com/nicko170/dismist/internal/model"
)

func TestMetricsListenerAcceptsOnlyNumericLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "192.0.2.5:0", ":0", "localhost:0", "[::]:0", "example.com:0", "127.0.0.1"} {
		listener, err := metricsListener(address)
		if err == nil {
			listener.Close()
			t.Errorf("accepted %q", address)
		}
	}
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		listener, err := metricsListener(address)
		if err != nil {
			t.Errorf("rejected %q: %v", address, err)
			continue
		}
		listener.Close()
	}
}

func TestMetricsListenerWithAllowRemote(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0", ":0", "0.0.0.0:0", "[::]:0"} {
		listener, err := listenMetrics(address, true)
		if err != nil {
			t.Errorf("rejected %q: %v", address, err)
			continue
		}
		listener.Close()
	}
	// Hostnames are never accepted: what they resolve to can change.
	for _, address := range []string{"localhost:0", "example.com:0", "ip6-localhost:0", "127.0.0.1", "[::1]"} {
		listener, err := listenMetrics(address, true)
		if err == nil {
			listener.Close()
			t.Errorf("accepted %q", address)
		}
	}
	// A non-loopback numeric IP passes validation; binding then depends on
	// the host owning it, so check validation alone with a documentation IP.
	if _, err := listenMetrics("192.0.2.5:0", true); err != nil && strings.Contains(err.Error(), "numeric") {
		t.Errorf("rejected a numeric IP with allow-remote: %v", err)
	}
}

func TestMetricsFlagsDefaults(t *testing.T) {
	fs := flag.NewFlagSet("dismistd", flag.ContinueOnError)
	config := registerMetricsFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	want := metricsConfig{listen: "127.0.0.1:9090", clients: "aggregate", clientLimit: 100}
	if *config != want {
		t.Fatalf("defaults = %+v, want %+v", *config, want)
	}
	if err := fs.Parse([]string{"-metrics-listen=:9090", "-metrics-allow-remote", "-metrics-token-file=/private/metrics.token", "-metrics-names", "-metrics-clients=per-client", "-metrics-client-limit=5"}); err != nil {
		t.Fatal(err)
	}
	want = metricsConfig{listen: ":9090", allowRemote: true, tokenFile: "/private/metrics.token", names: true, clients: "per-client", clientLimit: 5}
	if *config != want {
		t.Fatalf("parsed = %+v, want %+v", *config, want)
	}
}

func testMetricsOptions(t *testing.T) metrics.Options {
	t.Helper()
	store, err := model.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "site-a", Name: "Example"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return metrics.Options{Store: store, Manager: controller.NewManager()}
}

func TestStartMetricsDisabledWhenListenEmpty(t *testing.T) {
	server, err := startMetrics(&metricsConfig{listen: "", tokenFile: "/does/not/exist", clients: "bogus"}, testMetricsOptions(t))
	if err != nil || server != nil {
		t.Fatalf("startMetrics = %v, %v; want disabled", server, err)
	}
}

func TestStartMetricsRejectsBadFlags(t *testing.T) {
	dir := t.TempDir()
	readable := filepath.Join(dir, "readable.token")
	if err := os.WriteFile(readable, []byte("0123456789abcdef0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readable, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]metricsConfig{
		"remote without allow":  {listen: ":0", clients: "aggregate"},
		"hostname":              {listen: "localhost:0", allowRemote: true, clients: "aggregate"},
		"bad client mode":       {listen: "127.0.0.1:0", clients: "everything"},
		"negative client limit": {listen: "127.0.0.1:0", clients: "aggregate", clientLimit: -1},
		"missing token file":    {listen: "127.0.0.1:0", clients: "aggregate", tokenFile: filepath.Join(dir, "missing")},
		"readable token file":   {listen: "127.0.0.1:0", clients: "aggregate", tokenFile: readable},
	} {
		server, err := startMetrics(&config, testMetricsOptions(t))
		if err == nil {
			_ = server.Close()
			t.Errorf("%s: started", name)
		}
	}
}

// TestStartMetricsServesWithToken runs the listener end to end: remote bind
// allowed explicitly, 401 without the token, 200 with it.
func TestStartMetricsServesWithToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	tokenFile := filepath.Join(t.TempDir(), "metrics.token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("dismistd", flag.ContinueOnError)
	config := registerMetricsFlags(fs)
	if err := fs.Parse([]string{"-metrics-listen=:0", "-metrics-allow-remote", "-metrics-token-file=" + tokenFile}); err != nil {
		t.Fatal(err)
	}
	server, err := startMetrics(config, testMetricsOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	_, port, err := net.SplitHostPort(server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("127.0.0.1", port)

	get := func(authorization string) (int, string) {
		request, err := http.NewRequest(http.MethodGet, "http://"+address+"/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(body)
	}
	if status, _ := get(""); status != http.StatusUnauthorized {
		t.Errorf("no token: status = %d", status)
	}
	if status, _ := get("Bearer not-the-token-at-all"); status != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d", status)
	}
	status, body := get("Bearer " + token)
	if status != http.StatusOK || !strings.Contains(body, "dismist_sites 1\n") {
		t.Errorf("token: status = %d, body = %s", status, body)
	}
}
