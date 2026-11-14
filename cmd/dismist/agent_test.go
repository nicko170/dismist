package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// serveAgent runs an in-process ssh-agent holding keys and points
// SSH_AUTH_SOCK at it.
func serveAgent(t *testing.T, keys ...any) {
	t.Helper()
	// Unix socket paths are short on macOS; t.TempDir can exceed the limit.
	dir, err := os.MkdirTemp("", "dagent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	keyring := agent.NewKeyring()
	for _, key := range keys {
		if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = agent.ServeAgent(keyring, conn)
			}()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", socket)
}

func TestAgentKeysAuthenticateIncludingLegacyRSA(t *testing.T) {
	home := isolateSSH(t)
	rsaKey := cachedRSAKeyMust(t)
	signer, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	serveAgent(t, rsaKey)
	// The agent's RSA key must fall back to SHA-1 for dropbear 2017.75.
	ap := newFakeAP(t, fakeAPOptions{legacy: true}, signer.PublicKey())
	if result := runAP33Test(t, "ssh", "-p", strconv.Itoa(ap.port()), "127.0.0.1", "status"); result.err != nil {
		t.Fatalf("agent RSA key on legacy dropbear: %v\n%s", result.err, result.stderr)
	}

	// An encrypted key file whose key is in the agent uses the agent copy.
	encrypted := filepath.Join(home, "id_locked")
	block, err := ssh.MarshalPrivateKeyWithPassphrase(rsaKey, "", []byte("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(encrypted, pemBytes(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := runAP33Test(t, "ssh", "-i", encrypted, "-o", "IdentitiesOnly=yes", "-p", strconv.Itoa(ap.port()), "127.0.0.1", "status"); result.err != nil {
		t.Fatalf("encrypted key via agent: %v", result.err)
	}
	// IdentityAgent=none with no other identity has nothing to offer.
	result := runAP33Test(t, "ssh", "-o", "IdentityAgent=none", "-p", strconv.Itoa(ap.port()), "127.0.0.1", "status")
	if result.err == nil {
		t.Fatal("connected without any identity")
	}
}
