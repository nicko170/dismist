package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/controller"
)

func TestLoadSecretForms(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "secret.bin")
	if err := os.WriteFile(rawPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	hexPath := filepath.Join(dir, "secret.hex")
	if err := os.WriteFile(hexPath, []byte(hex.EncodeToString(raw)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ text, path string }{
		{hex.EncodeToString(raw), ""}, {base64.StdEncoding.EncodeToString(raw), ""}, {"", rawPath}, {"", hexPath},
	} {
		secret, err := loadSecret(c.text, c.path)
		if err != nil || !bytes.Equal(secret[:], raw) {
			t.Fatalf("%+v: %x %v", c, secret, err)
		}
	}
	for _, c := range []struct{ text, path string }{{"", ""}, {"abcd", ""}, {"x", rawPath}} {
		if _, err := loadSecret(c.text, c.path); err == nil {
			t.Fatalf("%+v accepted", c)
		}
	}
}

func TestClientTLSRequiresAnExplicitChoice(t *testing.T) {
	if _, err := clientTLS("", false, ""); err == nil {
		t.Fatal("no -ca or -insecure accepted")
	}
	if _, err := clientTLS("ca.pem", true, ""); err == nil {
		t.Fatal("-ca with -insecure accepted")
	}
	if config, err := clientTLS("", true, "controller.example"); err != nil || !config.InsecureSkipVerify || config.ServerName != "controller.example" {
		t.Fatalf("insecure config %+v %v", config, err)
	}
}

func TestOnceFetchesConfig(t *testing.T) {
	secret := bytes.Repeat([]byte{9}, 32)
	manager := controller.NewManager()
	tlsServer := httptest.NewTLSServer(manager)
	defer tlsServer.Close()
	var key [32]byte
	copy(key[:], secret)
	server, err := controller.NewServer(controller.Options{ID: "02-aa-00-00-00-71", Secret: key, CertDER: tlsServer.Certificate().Raw, Config: []byte(`{"Timestamp":5}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("02-aa-00-00-00-71", "", server); err != nil {
		t.Fatal(err)
	}
	url := "wss" + strings.TrimPrefix(tlsServer.URL, "https") + "/ws"
	if err := run([]string{"-url", url, "-id", "02-aa-00-00-00-71", "-secret", hex.EncodeToString(secret), "-insecure", "-once"}); err != nil {
		t.Fatal(err)
	}
	if server.Counters().ConfigDeliveries != 1 {
		t.Fatalf("config not fetched: %+v", server.Counters())
	}
	if err := run([]string{"-url", url}); err == nil {
		t.Fatal("missing -id accepted")
	}
}
