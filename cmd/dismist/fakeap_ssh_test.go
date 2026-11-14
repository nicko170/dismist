package main

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakeAP is an in-process SSH server that behaves like an AP33's dropbear
// with a tiny read-only userland over an in-memory filesystem. It records
// every command so tests can prove inspect only reads.
type fakeAP struct {
	t        *testing.T
	addr     string
	hostKey  ssh.Signer
	listener net.Listener
	done     chan struct{}

	mu         sync.Mutex
	files      map[string]string
	commands   []string
	rebooted   int
	authAlgos  []string // negotiated "kex/hostkey" per authenticated connection
	authorized []ssh.PublicKey
}

type fakeAPOptions struct {
	// legacy emulates the 0.8 recovery image's dropbear 2017.75: an RSA
	// host key offered only as SHA-1 ssh-rsa, user signatures only as
	// ssh-rsa, and the older kex/cipher/MAC set.
	legacy bool
	// hostKey overrides the generated host key.
	hostKey ssh.Signer
	// extraHostKeys are offered as well (a server with several key types).
	extraHostKeys []ssh.Signer
}

func newFakeAP(t *testing.T, options fakeAPOptions, authorized ...ssh.PublicKey) *fakeAP {
	t.Helper()
	ap := &fakeAP{t: t, files: syntheticAPFiles(), authorized: authorized}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			ap.mu.Lock()
			defer ap.mu.Unlock()
			for _, allowed := range ap.authorized {
				if string(allowed.Marshal()) == string(key.Marshal()) {
					if algorithms, ok := meta.(ssh.AlgorithmsConnMetadata); ok {
						negotiated := algorithms.Algorithms()
						ap.authAlgos = append(ap.authAlgos, negotiated.KeyExchange+"/"+negotiated.HostKey)
					}
					return nil, nil
				}
			}
			return nil, errors.New("key not authorized")
		},
	}
	hostKey := options.hostKey
	if options.legacy {
		config.KeyExchanges = []string{"curve25519-sha256@libssh.org", ssh.InsecureKeyExchangeDH14SHA1}
		config.Ciphers = []string{ssh.CipherAES128CTR}
		config.MACs = []string{ssh.HMACSHA256}
		config.PublicKeyAuthAlgorithms = []string{ssh.KeyAlgoRSA}
		if hostKey == nil {
			hostKey = newTestKey(t, "", "rsa-fresh").signer
		}
		restricted, err := ssh.NewSignerWithAlgorithms(hostKey.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
		if err != nil {
			t.Fatal(err)
		}
		hostKey = restricted
	}
	if hostKey == nil {
		hostKey = newTestKey(t, "", "ed25519").signer
	}
	ap.hostKey = hostKey
	config.AddHostKey(hostKey)
	for _, extra := range options.extraHostKeys {
		config.AddHostKey(extra)
	}
	ap.done = make(chan struct{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap.listener = listener
	ap.addr = listener.Addr().String()
	t.Cleanup(func() {
		close(ap.done)
		_ = listener.Close()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go ap.handle(conn, config)
		}
	}()
	return ap
}

func (ap *fakeAP) port() int {
	_, port, _ := net.SplitHostPort(ap.addr)
	value, _ := strconv.Atoi(port)
	return value
}

func (ap *fakeAP) setFile(name, content string) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	ap.files[name] = content
}

func (ap *fakeAP) removeFile(name string) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	delete(ap.files, name)
}

func (ap *fakeAP) file(name string) (string, bool) {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	content, ok := ap.files[name]
	return content, ok
}

func (ap *fakeAP) recorded() []string {
	ap.mu.Lock()
	defer ap.mu.Unlock()
	return append([]string(nil), ap.commands...)
}

func (ap *fakeAP) handle(conn net.Conn, config *ssh.ServerConfig) {
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go ap.session(server, channel, channelRequests)
	}
}

func (ap *fakeAP) session(server *ssh.ServerConn, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		stdout, stderr, status, reboot := ap.exec(payload.Command)
		_, _ = channel.Write([]byte(stdout))
		_, _ = channel.Stderr().Write([]byte(stderr))
		if reboot {
			// A rebooting AP drops the connection without an exit status.
			_ = server.Close()
			return
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
		return
	}
}

// exec runs one command against the in-memory filesystem.
func (ap *fakeAP) exec(command string) (stdout, stderr string, status int, reboot bool) {
	ap.mu.Lock()
	ap.commands = append(ap.commands, command)
	ap.mu.Unlock()

	if command == "reboot" || strings.HasSuffix(command, "; reboot") {
		if command != "reboot" {
			if out, err := ap.runRebootPrelude(strings.TrimSuffix(command, "; reboot")); err != nil {
				return "", out + err.Error(), 1, false
			}
		}
		ap.mu.Lock()
		ap.rebooted++
		ap.mu.Unlock()
		return "", "", 0, true
	}

	if command == "hang" {
		<-ap.done
		return "", "", 0, false
	}
	if command == "flood" {
		return strings.Repeat("x", 1<<20), "", 0, false
	}

	words, metachar := shellWords(command)
	if metachar || len(words) == 0 {
		return "", "fake AP: unsupported shell syntax: " + command, 127, false
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	missing := func(name string) (string, string, int, bool) {
		return "", fmt.Sprintf("%s: can't open '%s': No such file or directory\n", words[0], name), 1, false
	}
	switch words[0] {
	case "id":
		if len(words) == 2 && words[1] == "-u" {
			return "0\n", "", 0, false
		}
	case "/sbin/bootcfg":
		if len(words) == 1 {
			return ap.files["!bootcfg"], "", 0, false
		}
	case "pidof":
		if len(words) == 2 && words[1] == "ep-control" {
			if pids := ap.files["!pidof"]; pids != "" {
				return pids, "", 0, false
			}
			return "", "", 1, false
		}
	case "dmesg":
		return ap.files["!dmesg"], "", 0, false
	case "logread":
		if content, ok := ap.files["!logread"]; ok {
			return content, "", 0, false
		}
		return "", "sh: logread: not found\n", 127, false
	case "cat":
		args := words[1:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		var out strings.Builder
		for _, name := range args {
			content, ok := ap.files[name]
			if !ok {
				return missing(name)
			}
			out.WriteString(content)
		}
		return out.String(), "", 0, false
	case "ls":
		if len(words) != 3 {
			break
		}
		switch words[1] {
		case "-1A":
			children := ap.children(words[2])
			if children == nil {
				return missing(words[2])
			}
			return strings.Join(children, "\n") + "\n", "", 0, false
		case "-1d":
			var matches []string
			for name := range ap.files {
				if ok, _ := path.Match(words[2], name); ok {
					matches = append(matches, name)
				}
			}
			if len(matches) == 0 {
				return missing(words[2])
			}
			sort.Strings(matches)
			return strings.Join(matches, "\n") + "\n", "", 0, false
		}
	case "wc":
		if len(words) == 3 && words[1] == "-c" {
			content, ok := ap.files[words[2]]
			if !ok {
				return missing(words[2])
			}
			return fmt.Sprintf("%d %s\n", len(content), words[2]), "", 0, false
		}
	case "tail":
		if len(words) == 4 && words[1] == "-n" {
			n, err := strconv.Atoi(words[2])
			content, ok := ap.files[words[3]]
			if err != nil || !ok {
				return missing(words[3])
			}
			lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
			if len(lines) > n {
				lines = lines[len(lines)-n:]
			}
			return strings.Join(lines, "\n") + "\n", "", 0, false
		}
	}
	return "", "fake AP: unsupported command: " + command, 127, false
}

// children lists the direct children of dir, or nil when it does not exist.
func (ap *fakeAP) children(dir string) []string {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	seen := map[string]bool{}
	for name := range ap.files {
		if rest, ok := strings.CutPrefix(name, prefix); ok && rest != "" {
			seen[strings.SplitN(rest, "/", 2)[0]] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// runRebootPrelude runs the reason-recording part of the reboot command in a
// real POSIX shell against a scratch copy of /rw/etc/mist/ep-control, so the
// test checks the shell syntax the AP will see.
func (ap *fakeAP) runRebootPrelude(script string) (string, error) {
	root := ap.t.TempDir()
	scratch := filepath.Join(root, "ep-control")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return "", err
	}
	ap.mu.Lock()
	for name, content := range ap.files {
		if rest, ok := strings.CutPrefix(name, apEPControlRW+"/"); ok && !strings.Contains(rest, "/") {
			if err := os.WriteFile(filepath.Join(scratch, rest), []byte(content), 0o600); err != nil {
				ap.mu.Unlock()
				return "", err
			}
		}
	}
	ap.mu.Unlock()
	out, err := exec.Command("/bin/sh", "-c", strings.ReplaceAll(script, apEPControlRW, scratch)).CombinedOutput()
	if err != nil {
		return string(out), err
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		return "", err
	}
	ap.mu.Lock()
	defer ap.mu.Unlock()
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(scratch, entry.Name()))
		if err != nil {
			return "", err
		}
		ap.files[apEPControlRW+"/"+entry.Name()] = string(data)
	}
	return string(out), nil
}

// shellWords splits a command the way sh would for the simple commands
// inspect sends, and reports any unquoted shell metacharacter (redirection,
// pipes, command separators, substitution, globbing excepted).
func shellWords(command string) (words []string, metachar bool) {
	var current strings.Builder
	inWord := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				return nil, true
			}
			current.WriteString(command[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"' || c == '\\' || c == '`' || c == '$':
			return nil, true
		case strings.IndexByte("><|;&(){}", c) >= 0:
			return nil, true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, current.String())
	}
	return words, false
}

// syntheticAPFiles is a 0.15 AP with a custom endpoint, as after the k8s
// cutover. Every value is synthetic.
func syntheticAPFiles() map[string]string {
	return map[string]string{
		"/etc/mist/version":                              "# version information about this firmware image\nFIRMWARE_MODEL=aplx\nFIRMWARE_VERSION=apfw-0.15.35064-noro-84df\nFIRMWARE_BUILDTIME=2026-09-12_02:34:06_UTC\nFIRMWARE_FIPS=0\n",
		"/proc/uptime":                                   "7384.52 14020.11\n",
		"/proc/mounts":                                   "rootfs / rootfs rw 0 0\nubi0:rootfs1 / ubifs ro,relatime 0 0\nproc /proc proc rw 0 0\n",
		"/proc/mtd":                                      "dev:    size   erasesize  name\nmtd0: 00100000 00010000 \"u-boot\"\nmtd4: 0f000000 00020000 \"ubi\"\n",
		apBootStateFile:                                  "normal\x00",
		"/sys/class/net/eth0/address":                    "02:aa:00:00:00:01\n",
		"/sys/class/net/vlan1/address":                   "02:aa:00:00:00:01\n",
		"/sys/bus/i2c/devices/0-0052/eeprom":             strings.Repeat("\x00", eepromSize),
		apEPControlRW + "/boot.count":                    "7",
		apEPControlRW + "/last_reboot_info.txt.uploaded": "1794547930::reboot/synthetic test",
		apEPControlRW + "/cloud.ini":                     syntheticCustomCloudINI,
		apEPControlRW + "/cacerts-production.pem":        "",
		apEPControlRW + "/authorized_keys":               "ssh-ed25519 AAAA synthetic\n",
		apEPControlRO + "/cloud.ini":                     syntheticStockCloudINI,
		apEPControlLog: "wrapper 1970-01-01T00:00:06Z.350508: started /usr/sbin/ep-control for the 1th time, pid 211\n" +
			"DBG> 1970-01-01T00:00:06.685883Z G1 main.go:118: boot count 7\n" +
			"  PSK:synthetic-passphrase-1\n" +
			"  \"PSK\": \"synthetic-passphrase-2\",\n" +
			"wrapper 1970-01-01T01:00:06Z.350508: started /usr/sbin/ep-control for the 2th time, pid 977\n" +
			"DBG> 2026-11-14T03:00:00Z G1 cloud.go:1: connected to wss://ap.example.net/ws\n",
		apSystemLog: "Nov 14 03:00:00 ap daemon.info procmon: ep-control exited, restarting\nNov 14 03:00:01 ap user.info kernel: eth0: link up\n",
		apErrorReports + "/ep-control.report.last": "x",
		"!bootcfg": "w1i1c7\n",
		"!pidof":   "977\n",
		"!dmesg":   "[    0.000000] Booting Linux\n[ 4000.000000] Out of memory: Killed process 123 (pace)\n",
	}
}

const syntheticCustomCloudINI = `ENV = "production"

[[cloud]]
	ENV = "production"
	# Only for Factory Use (manufacturing)
	url = "wss://ap.example.net/ws"
	handshaketimeout = "2m"
	manufacturing = true
	[cloud.tls]
		rootcerts = "/rw/etc/mist/ep-control/cacerts-production.pem"

[[cloud]]
	ENV = "production"
	url = "wss://ap.example.net/ws"
	handshaketimeout = "2m"
	[cloud.tls]
		rootcerts = "/rw/etc/mist/ep-control/cacerts-production.pem"

#for older APs
[tls]
	rootcerts = "/rw/etc/mist/ep-control/cacerts-production.pem"

[proxy]
	#url = "http://username:password@proxy.example.net:8080/"

[auth]
	timeout = "5m"
`

const syntheticStockCloudINI = `ENV = "production"

[[cloud]]
  ENV = "production"
  url = "wss://ep-terminator${FIPS}.mistsys.net/ws"
  priority = 2
    [cloud.tls]
      # use 'true' ONLY for testing, not in production!
      insecure = false
      rootcerts = "/etc/mist/ep-control/cacerts-production${FIPS}.pem"

[[cloud]]
  ENV = "production"
  url = "wss://ep-terminator.mistsys.net/ws"
  manufacturing = true
    [cloud.tls]
      insecure = false
      rootcerts = "/etc/mist/ep-control/cacerts-production.pem"

[proxy]
  #url = "http://username:password@proxy.com:8080/"
`

// testKey is a user or host key with its private key file.
type testKey struct {
	file   string
	signer ssh.Signer
}

var cachedRSAKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

// newTestKey creates an "ed25519", "rsa" (cached per test binary, since RSA
// generation is slow) or "rsa-fresh" key and writes it to dir.
func newTestKey(t *testing.T, dir, kind string) testKey {
	t.Helper()
	var private crypto.Signer
	var err error
	switch kind {
	case "ed25519":
		_, private, err = ed25519.GenerateKey(rand.Reader)
	case "rsa":
		private, err = cachedRSAKey()
	case "rsa-fresh":
		private, err = rsa.GenerateKey(rand.Reader, 2048)
	default:
		t.Fatalf("unknown key kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := testKey{signer: signer}
	if dir != "" {
		block, err := ssh.MarshalPrivateKey(private, "")
		if err != nil {
			t.Fatal(err)
		}
		key.file = filepath.Join(dir, "id_"+kind)
		if err := os.WriteFile(key.file, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return key
}

// isolateSSH points HOME, XDG_CONFIG_HOME and SSH_AUTH_SOCK at an empty
// scratch directory so tests never read the developer's keys or agent.
func isolateSSH(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("SSH_AUTH_SOCK", "")
	return home
}

func cachedRSAKeyMust(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := cachedRSAKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func pemBytes(block *pem.Block) []byte {
	return pem.EncodeToMemory(block)
}
