package main

import (
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckTrustVerifiesAgainstTheBundleOnly(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	endpoint := "wss://" + strings.TrimPrefix(server.URL, "https://") + "/ws"
	dir := t.TempDir()

	good := filepath.Join(dir, "good.pem")
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runAP33Test(t, "check-trust", "--url", endpoint, "--bundle", good)
	if result.err != nil || !strings.Contains(result.stdout, "OK: verifies against") || !strings.Contains(result.stdout, "Chain served by 127.0.0.1:") {
		t.Fatalf("good bundle: %v\n%s", result.err, result.stdout)
	}

	other := filepath.Join(dir, "other.pem")
	if err := os.WriteFile(other, []byte(syntheticCABundle(t, "Synthetic Unrelated Root")), 0o600); err != nil {
		t.Fatal(err)
	}
	result = runAP33Test(t, "check-trust", "--url", endpoint, "--bundle", other)
	if !errors.Is(result.err, errTrustMismatch) || !strings.Contains(result.stdout, "Chain served by") {
		t.Fatalf("unrelated bundle: %v\n%s", result.err, result.stdout)
	}

	for _, args := range [][]string{
		{"check-trust"},
		{"check-trust", "--url", endpoint},
		{"check-trust", "--url", "https://127.0.0.1/ws", "--bundle", good},
		{"check-trust", "--url", endpoint, "--bundle", filepath.Join(dir, "missing.pem")},
		{"check-trust", "--url", endpoint, "--bundle", good, "extra"},
	} {
		if result := runAP33Test(t, args...); result.err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
