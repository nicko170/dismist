package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesPrivateVerifiableCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	var output bytes.Buffer
	if err := run([]string{"init", "--data-dir", dir}, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "PRIVATE KEY") {
		t.Fatal("CLI printed private key")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("data directory is not private: %v %v", info, err)
	}
	for _, name := range []string{"admin.token", "ap-config.json", "ca.crt", "ca.key", "tls.crt", "tls.key"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s is not a private regular file: %v %v", name, info, err)
		}
	}
	token, err := os.ReadFile(filepath.Join(dir, "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := hex.DecodeString(strings.TrimSpace(string(token))); err != nil || len(raw) != 32 {
		t.Fatal("admin token is not 32 random bytes in hex")
	}
	if !strings.Contains(output.String(), strings.TrimSpace(string(token))) {
		t.Fatal("CLI did not print the newly generated administrator token")
	}
	var config struct{ WLANs []json.RawMessage }
	configRaw, err := os.ReadFile(filepath.Join(dir, "ap-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(configRaw, &config); err != nil || len(config.WLANs) != 1 {
		t.Fatalf("init did not write a usable AP33 config: %v", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		t.Fatal("CA is not PEM")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !ca.IsCA || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("invalid CA certificate: %v", err)
	}
	leafPEM, err := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if err != nil {
		t.Fatal(err)
	}
	leafBlock, _ := pem.Decode(leafPEM)
	if leafBlock == nil {
		t.Fatal("leaf is not PEM")
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "ep-terminator.mistsys.net", Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("AP WSS certificate does not verify: %v", err)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")); err != nil {
		t.Fatalf("daemon cannot load generated certificate: %v", err)
	}
}

func TestInitIsIdempotentAndNeverOverwritesCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := run([]string{"init", "--data-dir", dir}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	before := make(map[string][]byte)
	for _, name := range []string{"admin.token", "ap-config.json", "ca.crt", "ca.key", "tls.crt", "tls.key"} {
		var err error
		before[name], err = os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	var repeatOutput bytes.Buffer
	if err := run([]string{"init", "--data-dir", dir}, &repeatOutput); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(repeatOutput.String(), strings.TrimSpace(string(before["admin.token"]))) {
		t.Fatal("repeat init reprinted an existing administrator token")
	}
	for name, original := range before {
		current, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(current, original) {
			t.Fatalf("%s changed on repeat init: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"init", "--data-dir", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid existing TLS key was silently replaced")
	}
	current, err := os.ReadFile(filepath.Join(dir, "tls.key"))
	if err != nil || string(current) != "invalid" {
		t.Fatalf("invalid existing key was overwritten: %v", err)
	}
}

func TestInitDefaultsToDataDirectory(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := run([]string{"init"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("data", "ap-config.json")); err != nil {
		t.Fatalf("zero-argument init did not create the default AP config: %v", err)
	}
}

func TestInitRejectsPartialExistingCredentials(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"init", "--data-dir", dir}, &bytes.Buffer{}); err == nil {
		t.Fatal("partial credentials were accepted")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != "existing" {
		t.Fatalf("existing key was overwritten: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "admin.token")); !os.IsNotExist(err) {
		t.Fatalf("partial init created a token: %v", err)
	}
}
