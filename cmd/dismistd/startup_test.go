package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
)

// lockedLog collects the daemon's log lines for failure messages.
type lockedLog struct {
	mu    sync.Mutex
	lines strings.Builder
}

func (l *lockedLog) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines.WriteString(line + "\n")
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lines.String()
}

func writeTestTLS(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "ap.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"ap.example.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// TestDaemonStartsHealthzFirstAndServesTheAPI builds dismistd and runs it
// on loopback ports with a fresh database.
func TestDaemonStartsHealthzFirstAndServesTheAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the daemon")
	}
	work := t.TempDir()
	binary := filepath.Join(work, "dismistd")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	data := filepath.Join(work, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := writeTestTLS(t, data)
	tokenPath := filepath.Join(data, "admin.token")
	daemon := exec.Command(binary,
		"-db", filepath.Join(data, "controller.db"), "-admin-token-file", tokenPath,
		"-tls-cert", certPath, "-tls-key", keyPath, "-tls-check-interval", "0",
		"-ui-listen", "127.0.0.1:0", "-ws-listen", "127.0.0.1:0", "-dns-listen", "127.0.0.1:0",
		"-metrics-listen", "", "-controller-ip", "127.0.0.1", "-upstream-dns", "127.0.0.1:9",
		"-ap-url", "wss://ap.example.test/ws", "-backup-interval", "0",
	)
	daemon.Dir = work
	stderr, err := daemon.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	t.Cleanup(func() {
		_ = daemon.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = daemon.Process.Kill()
		}
	})
	listening := regexp.MustCompile(`UI listener on (127\.0\.0\.1:\d+)`)
	address := make(chan string, 1)
	logs := &lockedLog{}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logs.add(line)
			if match := listening.FindStringSubmatch(line); match != nil {
				address <- match[1]
			}
		}
		exited <- daemon.Wait()
	}()
	var base string
	select {
	case addr := <-address:
		base = "http://" + addr
	case err := <-exited:
		t.Fatalf("dismistd exited: %v\n%s", err, logs.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("dismistd did not open its UI listener\n%s", logs.String())
	}
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string, header map[string]string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		for key, value := range header {
			req.Header.Set(key, value)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, body
	}
	if status, body := get("/healthz", nil); status != http.StatusOK {
		t.Fatalf("/healthz: %d %s", status, body)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		status, _ := get("/readyz", nil)
		if status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/readyz never became ready\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status, body := get("/next/sites", nil); status != http.StatusOK || !strings.Contains(string(body), "<html") {
		t.Fatalf("/next/sites: %d %s", status, body)
	}
	status, body := get("/api/system", nil)
	if status != http.StatusUnauthorized || !strings.Contains(string(body), `"code":"unauthorized"`) {
		t.Fatalf("anonymous /api/system: %d %s", status, body)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	login, err := client.Post(base+"/auth/login", "application/json", strings.NewReader(`{"token":"`+strings.TrimSpace(string(raw))+`"}`))
	if err != nil || login.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", login, err)
	}
	cookie := login.Cookies()[0]
	login.Body.Close()
	status, body = get("/api/system", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
	var system api.SystemView
	if status != http.StatusOK || json.Unmarshal(body, &system) != nil {
		t.Fatalf("/api/system: %d %s", status, body)
	}
	if !system.Ready || system.WSS.URL != "wss://ap.example.test/ws" || system.WSS.Cert.Subject != "CN=ap.example.test" ||
		system.DB.Schema != 1 || system.DB.Compat != 1 || system.Migration.State != api.MigrationStateNotRun {
		t.Fatalf("system view %+v", system)
	}
	status, body = get("/api/system/backup", map[string]string{"Cookie": cookie.Name + "=" + cookie.Value})
	if status != http.StatusOK || len(body) == 0 {
		t.Fatalf("backup download: %d (%d bytes)", status, len(body))
	}
}
