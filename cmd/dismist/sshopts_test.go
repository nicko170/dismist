package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestLegacyDefaultsAllowAP33Dropbear(t *testing.T) {
	// dropbear 2017.75 on the 0.8 image: ssh-rsa host key and signatures,
	// curve25519/group14-sha1 kex, aes128-ctr, hmac-sha2-256.
	for name, list := range map[string][]string{
		"host key":  sshDefaultHostKey,
		"signature": sshDefaultPubkey,
	} {
		if !slices.Contains(list, ssh.KeyAlgoRSA) {
			t.Errorf("default %s algorithms lack ssh-rsa: %v", name, list)
		}
	}
	if !slices.Contains(sshDefaultKex, ssh.InsecureKeyExchangeDH14SHA1) || !slices.Contains(sshDefaultKex, ssh.KeyExchangeCurve25519) {
		t.Errorf("default kex: %v", sshDefaultKex)
	}
	if !slices.Contains(sshDefaultCiphers, ssh.CipherAES128CTR) || !slices.Contains(sshDefaultMACs, ssh.HMACSHA256) {
		t.Errorf("ciphers %v macs %v", sshDefaultCiphers, sshDefaultMACs)
	}
	// Nothing else insecure is enabled by default.
	for _, algorithm := range ssh.InsecureAlgorithms().Ciphers {
		if slices.Contains(sshDefaultCiphers, algorithm) {
			t.Errorf("insecure cipher %s enabled by default", algorithm)
		}
	}
	if slices.Contains(sshDefaultKex, ssh.InsecureKeyExchangeDH1SHA1) {
		t.Error("group1-sha1 enabled by default")
	}
}

func TestResolveAlgorithms(t *testing.T) {
	defaults := []string{"a", "b-sha1", "c-sha1"}
	known := []string{"a", "b-sha1", "c-sha1", "d", "e"}
	for _, tc := range []struct {
		value string
		want  []string
		err   bool
	}{
		{"d,a", []string{"d", "a"}, false},
		{"+d,a", []string{"a", "b-sha1", "c-sha1", "d"}, false},
		{"^e", []string{"e", "a", "b-sha1", "c-sha1"}, false},
		{"-*-sha1", []string{"a"}, false},
		{"-b-sha1", []string{"a", "c-sha1"}, false},
		{"-a,*sha1", nil, true}, // empty result
		{"unknown", nil, true},
		{"+unknown", nil, true},
		{"", nil, true},
		{"a,,d", nil, true},
	} {
		got, err := resolveAlgorithms(tc.value, defaults, known)
		if (err != nil) != tc.err || !slices.Equal(got, tc.want) {
			t.Errorf("resolveAlgorithms(%q) = %v, %v; want %v, err %v", tc.value, got, err, tc.want, tc.err)
		}
	}
}

func TestSSHOptionsFirstValueWins(t *testing.T) {
	options := newSSHOptions()
	for _, option := range []string{
		"Port=2222", "port 2200", "user=admin", "IdentityFile=/k1", "identityfile /k2",
		"StrictHostKeyChecking=accept-new", "StrictHostKeyChecking=no",
		"ConnectTimeout=3", "PubkeyAcceptedKeyTypes=+ssh-rsa", "PubkeyAcceptedAlgorithms=ssh-ed25519",
		"KexAlgorithms=-diffie-hellman-group14-sha1", `HostName="192.0.2.20"`,
		"UserKnownHostsFile=/a /b", "IdentityAgent=none", "BatchMode=yes",
	} {
		if err := options.setOptionString(option); err != nil {
			t.Fatalf("%s: %v", option, err)
		}
	}
	if options.Port != 2222 || options.User != "admin" || options.strictMode() != "accept-new" || options.connectTimeout() != 3*time.Second {
		t.Fatalf("options: %+v", options)
	}
	if !slices.Equal(options.IdentityFiles, []string{"/k1", "/k2"}) || !slices.Equal(options.KnownHostsFiles, []string{"/a", "/b"}) {
		t.Fatalf("files: %v %v", options.IdentityFiles, options.KnownHostsFiles)
	}
	// The alias and its new name share one "first value".
	if !slices.Contains(options.PubkeyAcceptedAlgorithms, ssh.KeyAlgoRSA) || slices.Equal(options.PubkeyAcceptedAlgorithms, []string{ssh.KeyAlgoED25519}) {
		t.Fatalf("pubkey algorithms: %v", options.PubkeyAcceptedAlgorithms)
	}
	if slices.Contains(options.KexAlgorithms, ssh.InsecureKeyExchangeDH14SHA1) || len(options.KexAlgorithms) == 0 {
		t.Fatalf("kex: %v", options.KexAlgorithms)
	}
	if options.IdentityAgent != "none" || agentSocket(options) != "" {
		t.Fatal("IdentityAgent=none not honoured")
	}
	target, err := options.resolve("ap33")
	if err != nil || target.Addr != "192.0.2.20:2222" || target.User != "admin" || target.Host != "ap33" {
		t.Fatalf("target %+v %v", target, err)
	}

	for _, bad := range []string{"ProxyJump=bastion", "Port=0", "Port=x", "StrictHostKeyChecking=maybe", "ConnectTimeout=-1", "Ciphers=rot13", "noequals", "=value", "IdentitiesOnly=perhaps"} {
		if err := newSSHOptions().setOptionString(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	defaults := newSSHOptions()
	if defaults.strictMode() != "accept-new" || defaults.connectTimeout() != defaultConnectTimeout {
		t.Fatal("defaults changed")
	}
	if err := defaults.setOptionString("ConnectTimeout=0"); err != nil || defaults.connectTimeout() != 0 {
		t.Fatal("ConnectTimeout=0 must disable the timeout")
	}
}

func TestParseDestination(t *testing.T) {
	for _, tc := range []struct {
		in   string
		user string
		host string
		port int
		err  bool
	}{
		{"192.0.2.10", "", "192.0.2.10", 0, false},
		{"root@192.0.2.10", "root", "192.0.2.10", 0, false},
		{"admin@ap.example.net", "admin", "ap.example.net", 0, false},
		{"root@[2001:db8::1]", "root", "2001:db8::1", 0, false},
		{"2001:db8::1", "", "2001:db8::1", 0, false},
		{"ssh://root@192.0.2.10:2222", "root", "192.0.2.10", 2222, false},
		{"ssh://[2001:db8::1]:2200", "", "2001:db8::1", 2200, false},
		{"ssh://192.0.2.10/path", "", "", 0, true},
		{"192.0.2.10:2222", "", "", 0, true},
		{"", "", "", 0, true},
		{"@192.0.2.10", "", "", 0, true},
		{"root@", "", "", 0, true},
		{"bad host", "", "", 0, true},
		{"ssh://192.0.2.10:99999", "", "", 0, true},
	} {
		user, host, port, err := parseDestination(tc.in)
		if (err != nil) != tc.err || user != tc.user || host != tc.host || port != tc.port {
			t.Errorf("parseDestination(%q) = %q %q %d %v", tc.in, user, host, port, err)
		}
	}
	options := newSSHOptions()
	_ = options.setOption("Port", "2200")
	_ = options.setOption("User", "admin")
	target, err := options.resolve("ssh://root@192.0.2.10:2222")
	if err != nil || target.String() != "root@192.0.2.10:2222" {
		t.Fatalf("destination user/port must beat options: %v %v", target, err)
	}
	target, _ = newSSHOptions().resolve("2001:db8::1")
	if target.String() != "root@[2001:db8::1]:22" {
		t.Fatalf("default target %s", target)
	}
}

func TestReadSSHConfigHostBlocks(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config")
	content := `# synthetic
StrictHostKeyChecking yes

Host ap-* !ap-lab
  HostName 192.0.2.30
  Port 2222
  IdentityFile ~/.ssh/ap_rsa
  ProxyJump bastion.example.net
  HostKeyAlgorithms +ssh-rsa

Match host ap-hall
  User nobody

Host ap-lab
  HostName 192.0.2.40

Host *
  User operator
  Port 22
`
	if err := os.WriteFile(config, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	home := isolateSSH(t)

	var warn bytes.Buffer
	options := newSSHOptions()
	if err := options.setOption("Port", "2022"); err != nil { // command line wins
		t.Fatal(err)
	}
	if err := options.readSSHConfig(config, "ap-hall", &warn); err != nil {
		t.Fatal(err)
	}
	target, err := options.resolve("ap-hall")
	if err != nil {
		t.Fatal(err)
	}
	if target.Addr != "192.0.2.30:2022" || target.User != "operator" || options.strictMode() != "yes" {
		t.Fatalf("target %+v strict %s", target, options.strictMode())
	}
	if !slices.Equal(options.IdentityFiles, []string{filepath.Join(home, ".ssh", "ap_rsa")}) {
		t.Fatalf("identity files %v", options.IdentityFiles)
	}
	if !strings.Contains(warn.String(), "ProxyJump") || !strings.Contains(warn.String(), "Match") {
		t.Fatalf("warnings: %s", warn.String())
	}

	lab := newSSHOptions()
	if err := lab.readSSHConfig(config, "ap-lab", io.Discard); err != nil {
		t.Fatal(err)
	}
	target, _ = lab.resolve("ap-lab")
	if target.Addr != "192.0.2.40:22" || len(lab.IdentityFiles) != 0 {
		t.Fatalf("negated pattern ignored: %+v", target)
	}

	if err := newSSHOptions().readSSHConfig(filepath.Join(dir, "missing"), "x", io.Discard); err == nil {
		t.Fatal("missing -F file accepted")
	}
	broken := filepath.Join(dir, "broken")
	_ = os.WriteFile(broken, []byte("Host x\n  Port nope\n"), 0o600)
	if err := newSSHOptions().readSSHConfig(broken, "x", io.Discard); err == nil || !strings.Contains(err.Error(), "broken:2") {
		t.Fatalf("bad value not reported with its line: %v", err)
	}
}

func TestWildcardMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, value string
		want           bool
	}{
		{"*", "", true},
		{"a*c", "abbbc", true},
		{"a*c", "abbbd", false},
		{"a?c", "abc", true},
		{"[192.0.2.1]:22", "[192.0.2.1]:22", true},
		{"[192.0.2.1]:22", "1", false},
		{"*-sha1", "diffie-hellman-group14-sha1", true},
		{"**x", "abx", true},
	} {
		if got := wildcardMatch(tc.pattern, tc.value); got != tc.want {
			t.Errorf("wildcardMatch(%q, %q) = %v", tc.pattern, tc.value, got)
		}
	}
}

func TestParseInterspersed(t *testing.T) {
	flags := flag.NewFlagSet("t", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonFlag := flags.Bool("json", false, "")
	var ssh sshFlags
	ssh.register(flags)
	positional, err := parseInterspersed(flags, []string{"-i", "/k", "root@192.0.2.10", "--json", "status", "-o", "Port=2", "--", "-x"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(positional, []string{"root@192.0.2.10", "status", "-x"}) || !*jsonFlag || !slices.Equal(ssh.identities, []string{"/k"}) || !slices.Equal(ssh.options, []string{"Port=2"}) {
		t.Fatalf("positional %v json %v ssh %+v", positional, *jsonFlag, ssh)
	}
	if _, err := parseInterspersed(flags, []string{"--nope"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestLoadSignersOrderAndRestrictions(t *testing.T) {
	home := isolateSSH(t)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ed := newTestKey(t, sshDir, "ed25519")
	rsaKey := newTestKey(t, sshDir, "rsa")
	if err := os.Rename(ed.file, filepath.Join(sshDir, "id_ed25519")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rsaKey.file, filepath.Join(sshDir, "id_rsa")); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	// No -i: the default files are used, ed25519 first.
	signers, _, err := loadSigners(newSSHOptions(), &warn)
	if err != nil || len(signers) != 2 || signers[0].PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("default identities: %v %v", signers, err)
	}
	// RSA keys may use SHA-1 by default and not once it is removed.
	if algorithms := signers[1].(ssh.MultiAlgorithmSigner).Algorithms(); !slices.Contains(algorithms, ssh.KeyAlgoRSA) {
		t.Fatalf("RSA signer algorithms %v", algorithms)
	}
	options := newSSHOptions()
	_ = options.setOption("PubkeyAcceptedAlgorithms", "ssh-ed25519")
	_ = options.setOption("IdentityFile", filepath.Join(sshDir, "id_rsa"))
	signers, _, err = loadSigners(options, &warn)
	if err != nil || len(signers) != 0 || !strings.Contains(warn.String(), "no algorithm allowed") {
		t.Fatalf("restricted RSA: %v %v %s", signers, err, warn.String())
	}
	// An explicit identity that cannot be read is an error, not a warning.
	missing := newSSHOptions()
	_ = missing.setOption("IdentityFile", filepath.Join(sshDir, "nope"))
	if _, _, err := loadSigners(missing, io.Discard); err == nil {
		t.Fatal("missing -i accepted")
	}
	// A passphrase-protected key without an agent is explained.
	encrypted := filepath.Join(sshDir, "id_encrypted")
	block, err := ssh.MarshalPrivateKeyWithPassphrase(cachedRSAKeyMust(t), "", []byte("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(encrypted, pemBytes(block), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := newSSHOptions()
	_ = locked.setOption("IdentityFile", encrypted)
	if _, _, err := loadSigners(locked, io.Discard); err == nil || !strings.Contains(err.Error(), "ssh-agent") {
		t.Fatalf("encrypted key: %v", err)
	}
}
