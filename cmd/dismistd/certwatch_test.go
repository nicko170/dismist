package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestPair(t *testing.T, dir string, serial int64) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "ap.example"},
		DNSNames: []string{"ap.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return der
}

func TestCertificateChanged(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	served := writeTestPair(t, dir, 1)
	if certificateChanged(certPath, keyPath, served) {
		t.Fatal("unchanged certificate reported as changed")
	}
	renewed := writeTestPair(t, dir, 2)
	if !certificateChanged(certPath, keyPath, served) {
		t.Fatal("renewed certificate not detected")
	}
	if certificateChanged(certPath, keyPath, renewed) {
		t.Fatal("served renewal reported as changed")
	}
	// A half-written rotation (new certificate, old key) must not trigger a restart.
	other := t.TempDir()
	writeTestPair(t, other, 3)
	raw, err := os.ReadFile(filepath.Join(other, "tls.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if certificateChanged(certPath, keyPath, renewed) {
		t.Fatal("mismatched certificate and key reported as a usable change")
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if certificateChanged(certPath, keyPath, renewed) {
		t.Fatal("missing key reported as a usable change")
	}
}

func TestWatchCertificateSignalsOnce(t *testing.T) {
	dir := t.TempDir()
	served := writeTestPair(t, dir, 1)
	changed := make(chan struct{})
	go watchCertificate(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), served, 10*time.Millisecond, changed)
	select {
	case <-changed:
		t.Fatal("watch fired without a change")
	case <-time.After(50 * time.Millisecond):
	}
	writeTestPair(t, dir, 2)
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not report the renewed certificate")
	}
}
