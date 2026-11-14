package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestHostKeyTrustOnFirstUseAndChangedKey(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	port := strconv.Itoa(ap.port())
	knownHostsFile := filepath.Join(home, "aps_known_hosts")
	host := "[127.0.0.1]:" + port

	// StrictHostKeyChecking=yes refuses an unknown host and records nothing.
	strict := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "--known-hosts", knownHostsFile, "-o", "StrictHostKeyChecking=yes", "127.0.0.1", "status")
	var unknown *HostKeyUnknownError
	if !errors.As(strict.err, &unknown) || unknown.Host != host {
		t.Fatalf("expected HostKeyUnknownError for %s, got %v", host, strict.err)
	}
	if _, err := os.Stat(knownHostsFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("strict mode wrote known_hosts")
	}
	if len(ap.recorded()) != 0 {
		t.Fatal("commands ran on an unverified host")
	}

	// The default (accept-new) records the key on first use.
	first := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "--known-hosts", knownHostsFile, "127.0.0.1", "status")
	if first.err != nil {
		t.Fatal(first.err)
	}
	data, err := os.ReadFile(knownHostsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := knownhosts.Line([]string{host}, ap.hostKey.PublicKey()) + "\n"
	if string(data) != want {
		t.Fatalf("known_hosts = %q, want %q", data, want)
	}
	// Now that it is known, strict mode connects.
	if result := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "--known-hosts", knownHostsFile, "-o", "StrictHostKeyChecking=yes", "127.0.0.1", "status"); result.err != nil {
		t.Fatal(result.err)
	}

	// A different key on the same address (a factory reset, or another
	// device) is refused in accept-new mode and the file is left alone.
	impostor := newTestKey(t, "", "ed25519")
	recorded := "# comment\nnot a valid line\n" + knownhosts.Line([]string{host}, impostor.signer.PublicKey()) + "\n"
	if err := os.WriteFile(knownHostsFile, []byte(recorded), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(ap.recorded())
	changed := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "--known-hosts", knownHostsFile, "127.0.0.1", "status")
	var mismatch *HostKeyChangedError
	if !errors.As(changed.err, &mismatch) || mismatch.Known[0].line != 3 {
		t.Fatalf("expected HostKeyChangedError at line 3, got %v", changed.err)
	}
	if !strings.Contains(changed.err.Error(), "CHANGED") || len(ap.recorded()) != before {
		t.Fatalf("changed key not refused before running commands: %v", changed.err)
	}
	// StrictHostKeyChecking=no warns and continues without recording.
	loose := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "--known-hosts", knownHostsFile, "-o", "StrictHostKeyChecking=no", "127.0.0.1", "status")
	if loose.err != nil || !strings.Contains(loose.stderr, "WARNING") {
		t.Fatalf("StrictHostKeyChecking=no: %v %s", loose.err, loose.stderr)
	}
	if after, _ := os.ReadFile(knownHostsFile); string(after) != recorded {
		t.Fatalf("StrictHostKeyChecking=no modified known_hosts: %q", after)
	}
}

func TestHostKeyPreferenceFollowsKnownKeyType(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	edHost := newTestKey(t, "", "ed25519")
	rsaHost := newTestKey(t, "", "rsa-fresh")
	// The server offers RSA and ed25519; x/crypto alone would pick
	// rsa-sha2-256, which would look like a changed key.
	ap := newFakeAP(t, fakeAPOptions{hostKey: edHost.signer, extraHostKeys: []ssh.Signer{rsaHost.signer}}, key.signer.PublicKey())
	port := strconv.Itoa(ap.port())
	knownHostsFile := filepath.Join(home, "known_hosts")
	if err := os.WriteFile(knownHostsFile, []byte(knownhosts.Line([]string{"127.0.0.1:" + port}, edHost.signer.PublicKey())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "-o", "UserKnownHostsFile="+knownHostsFile, "-o", "StrictHostKeyChecking=yes", "127.0.0.1", "status")
	if result.err != nil {
		t.Fatalf("known ed25519 key not preferred: %v", result.err)
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	if len(ap.authAlgos) != 1 || !strings.HasSuffix(ap.authAlgos[0], "/ssh-ed25519") {
		t.Fatalf("negotiated %v", ap.authAlgos)
	}
}

func TestKnownHostPatterns(t *testing.T) {
	hashed := knownhosts.HashHostname("[192.0.2.10]:2222")
	for _, tc := range []struct {
		patterns []string
		host     string
		want     bool
	}{
		{[]string{"192.0.2.10"}, "192.0.2.10", true},
		{[]string{"[192.0.2.10]:2222"}, "[192.0.2.10]:2222", true},
		{[]string{"[192.0.2.10]:2222"}, "192.0.2.10", false},
		{[]string{"192.0.2.*"}, "192.0.2.77", true},
		{[]string{"192.0.2.?"}, "192.0.2.77", false},
		{[]string{"192.0.2.*", "!192.0.2.13"}, "192.0.2.13", false},
		{[]string{"ap.example.net", "192.0.2.10"}, "192.0.2.10", true},
		{[]string{hashed}, "[192.0.2.10]:2222", true},
		{[]string{hashed}, "192.0.2.10", false},
		{[]string{"|1|bad|entry"}, "192.0.2.10", false},
	} {
		if got := knownHostMatches(tc.patterns, tc.host); got != tc.want {
			t.Errorf("knownHostMatches(%v, %q) = %v, want %v", tc.patterns, tc.host, got, tc.want)
		}
	}
}

func TestKnownHostsRevokedAndMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	key := newTestKey(t, "", "ed25519").signer.PublicKey()
	other := newTestKey(t, "", "ed25519").signer.PublicKey()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	if err := os.WriteFile(second, []byte(knownhosts.Line([]string{"192.0.2.10"}, key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	hosts := &knownHosts{files: []string{first, second}, mode: "accept-new", warn: &warn}
	if err := hosts.check("192.0.2.10:22", key); err != nil {
		t.Fatalf("key in the second file not accepted: %v", err)
	}
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a known key was written again")
	}
	// New hosts go to the first file.
	if err := hosts.check("192.0.2.11:22", other); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(first); !strings.HasPrefix(string(data), "192.0.2.11 ") {
		t.Fatalf("first file = %q", data)
	}
	revoked := "@revoked " + knownhosts.Line([]string{"192.0.2.12"}, key) + "\n"
	if err := os.WriteFile(second, []byte(revoked), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := hosts.check("192.0.2.12:22", key); err == nil || !strings.Contains(err.Error(), "@revoked") {
		t.Fatalf("revoked key accepted: %v", err)
	}
}
