package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	stockCert   = "-----BEGIN CERTIFICATE-----\nSTOCK\n-----END CERTIFICATE-----\n"
	localCert   = "-----BEGIN CERTIFICATE-----\nLOCAL\n-----END CERTIFICATE-----\n"
	stockConfig = "ENV = \"production\"\n[[cloud]]\nurl = \"wss://ep-terminator.mistsys.net/ws\"\ninsecure = false\nrootcerts = \"/etc/mist/ep-control/cacerts-production.pem\"\n"
)

func trustFixture(t *testing.T, config, stockCA, localCA string) (string, string, string) {
	t.Helper()
	base := t.TempDir()
	stockDir := filepath.Join(base, "stock")
	inputDir := filepath.Join(stockDir, "etc/mist/ep-control")
	if err := os.MkdirAll(inputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"cloud.ini": config, "cacerts-production.pem": stockCA} {
		if err := os.WriteFile(filepath.Join(inputDir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	caFile := filepath.Join(base, "local-ca.pem")
	if err := os.WriteFile(caFile, []byte(localCA), 0o600); err != nil {
		t.Fatal(err)
	}
	return stockDir, caFile, filepath.Join(base, "overlay")
}

func TestPrepareTrustCLIWritesPrivateCompatibleOverlay(t *testing.T) {
	stockDir, caFile, output := trustFixture(t, stockConfig, stockCert, localCert)
	var stdout bytes.Buffer
	if err := run([]string{"ap33", "prepare-trust", "--stock-dir", stockDir, "--controller-ca", caFile, "--output", output}, &stdout); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "CERTIFICATE") || !strings.Contains(stdout.String(), output) {
		t.Fatalf("unexpected CLI output: %q", stdout.String())
	}
	root := filepath.Join(output, "etc/mist/ep-control")
	config, err := os.ReadFile(filepath.Join(root, "cloud.ini"))
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := strings.Replace(stockConfig, "/etc/mist/ep-control/cacerts-production.pem", "/rw/etc/mist/ep-control/cacerts-production.pem", 1)
	if string(config) != wantConfig {
		t.Fatalf("cloud.ini changed beyond the trust path:\ngot %q\nwant %q", config, wantConfig)
	}
	bundle, err := os.ReadFile(filepath.Join(root, "cacerts-production.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bundle) != stockCert+localCert {
		t.Fatalf("CA bundle did not preserve stock then append local CA: %q", bundle)
	}
	for _, path := range []string{output, filepath.Join(output, "etc"), filepath.Join(output, "etc/mist"), root} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %s is not private: %v, %v", path, info, err)
		}
	}
	for _, path := range []string{filepath.Join(root, "cloud.ini"), filepath.Join(root, "cacerts-production.pem")} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("file %s is not private: %v, %v", path, info, err)
		}
	}
}

func TestPrepareTrustAcceptsStockFIPSAndExistingRWPath(t *testing.T) {
	config := "url = \"wss://ep-terminator${FIPS}.mistsys.net/ws\"\nrootcerts = \"/rw/etc/mist/ep-control/cacerts-production.pem\"\n"
	stockDir, caFile, output := trustFixture(t, config, stockCert, localCert)
	if err := run([]string{"ap33", "prepare-trust", "--stock-dir", stockDir, "--controller-ca", caFile, "--output", output}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(output, "etc/mist/ep-control/cloud.ini"))
	if err != nil || string(got) != config {
		t.Fatalf("unexpected FIPS config: %q, %v", got, err)
	}
}

func TestPrepareTrustPreservesStockBundleLeadingBytes(t *testing.T) {
	stock := " # Stock CA bundle\n" + stockCert
	stockDir, caFile, output := trustFixture(t, stockConfig, stock, localCert)
	if err := prepareTrustOverlay(stockDir, caFile, output); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(output, "etc/mist/ep-control/cacerts-production.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stock+localCert {
		t.Fatal("stock CA bundle leading bytes were changed")
	}
}

func TestPrepareTrustRejectsInvalidInputWithoutCreatingOutput(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		stockCA string
		localCA string
		message string
	}{
		{"unexpected endpoint", strings.Replace(stockConfig, "wss://ep-terminator.mistsys.net/ws", "wss://other.invalid/ws", 1), stockCert, localCert, "endpoint"},
		{"second unexpected endpoint", stockConfig + "url = \"wss://other.invalid/ws\"\n", stockCert, localCert, "endpoint"},
		{"insecure true", strings.Replace(stockConfig, "insecure = false", "insecure=true", 1), stockCert, localCert, "TLS"},
		{"unexpected CA path", strings.Replace(stockConfig, "/etc/mist/ep-control/cacerts-production.pem", "/tmp/other.pem", 1), stockCert, localCert, "root CA path"},
		{"missing rootcerts", strings.Replace(stockConfig, "rootcerts = \"/etc/mist/ep-control/cacerts-production.pem\"\n", "", 1), stockCert, localCert, "rootcerts"},
		{"invalid stock bundle", stockConfig, "not a certificate\n", localCert, "PEM"},
		{"invalid local bundle", stockConfig, stockCert, "-----BEGIN CERTIFICATE-----\nBROKEN\n", "PEM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stockDir, caFile, output := trustFixture(t, tc.config, tc.stockCA, tc.localCA)
			var stdout bytes.Buffer
			err := run([]string{"ap33", "prepare-trust", "--stock-dir", stockDir, "--controller-ca", caFile, "--output", output}, &stdout)
			if err == nil || !strings.Contains(err.Error(), tc.message) || stdout.Len() != 0 {
				t.Fatalf("expected %q error and no output, got %v and %q", tc.message, err, stdout.String())
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatalf("invalid input mutated output: %v", err)
			}
		})
	}
}

func TestPrepareTrustNeverOverwritesExistingOutput(t *testing.T) {
	stockDir, caFile, output := trustFixture(t, stockConfig, stockCert, localCert)
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(output, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"ap33", "prepare-trust", "--stock-dir", stockDir, "--controller-ca", caFile, "--output", output}, &bytes.Buffer{}); err == nil {
		t.Fatal("existing output was accepted")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "keep" {
		t.Fatalf("existing output mutated: %q, %v", got, err)
	}
}

func TestPrepareTrustMissingStockFileLeavesOutputAbsent(t *testing.T) {
	stockDir, caFile, output := trustFixture(t, stockConfig, stockCert, localCert)
	if err := os.Remove(filepath.Join(stockDir, "etc/mist/ep-control/cacerts-production.pem")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"ap33", "prepare-trust", "--stock-dir", stockDir, "--controller-ca", caFile, "--output", output}, &bytes.Buffer{}); err == nil {
		t.Fatal("missing stock CA was accepted")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("partial stock input mutated output: %v", err)
	}
}

func TestPrepareTrustRequiresAllFlagsAndNoPositionals(t *testing.T) {
	for _, args := range [][]string{
		{"ap33", "prepare-trust"},
		{"ap33", "prepare-trust", "--stock-dir", "stock", "--controller-ca", "ca"},
		{"ap33", "prepare-trust", "--stock-dir", "stock", "--controller-ca", "ca", "--output", "out", "extra"},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted invalid arguments %q", args)
		}
	}
}
