package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var inspectNow = time.Date(2026, 11, 14, 5, 0, 0, 0, time.UTC)

// syntheticCABundle returns PEM certificates for the given common names.
func syntheticCABundle(t *testing.T, names ...string) string {
	t.Helper()
	var bundle strings.Builder
	for i, name := range names {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber:          big.NewInt(int64(i + 1)),
			Subject:               pkix.Name{CommonName: name, Organization: []string{"Example Trust"}},
			NotBefore:             inspectNow.Add(-time.Hour),
			NotAfter:              inspectNow.AddDate(10, 0, 0),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	return bundle.String()
}

type cliResult struct {
	stdout, stderr string
	err            error
}

func runAP33Test(t *testing.T, args ...string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := runAP33With(args, ap33Env{ctx: ctx, stdout: &stdout, stderr: &stderr, now: func() time.Time { return inspectNow }})
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

// readOnlyCommands are the only programs inspect and status may run.
var readOnlyCommands = []string{"id", "cat", "ls", "tail", "wc", "pidof", "dmesg", "logread", "/sbin/bootcfg"}

func assertReadOnly(t *testing.T, commands []string) {
	t.Helper()
	if len(commands) == 0 {
		t.Fatal("no commands ran")
	}
	for _, command := range commands {
		words, metachar := shellWords(command)
		if metachar || len(words) == 0 || !slices.Contains(readOnlyCommands, words[0]) {
			t.Fatalf("inspect ran a command outside the read-only set: %q", command)
		}
		if strings.Contains(command, "eeprom") && words[0] != "wc" && words[0] != "ls" {
			t.Fatalf("inspect read EEPROM contents: %q", command)
		}
	}
}

func TestInspectReportsAPStateReadOnly(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	ap.setFile(apEPControlRW+"/cacerts-production.pem", syntheticCABundle(t, "Synthetic Mist Root", "Synthetic Root X1", "Synthetic Root X2"))

	result := runAP33Test(t, "inspect", "-i", key.file, "-p", strconv.Itoa(ap.port()), "root@127.0.0.1")
	if result.err != nil {
		t.Fatalf("inspect: %v\nstderr: %s", result.err, result.stderr)
	}
	out := result.stdout
	for _, want := range []string{
		"AP root@127.0.0.1:" + strconv.Itoa(ap.port()),
		"Host key       SHA256:",
		"AP ID          02-aa-00-00-00-01",
		"Firmware       apfw-0.15.35064-noro-84df (aplx, built 2026-09-12_02:34:06_UTC)",
		"Uptime         2h 3m 5s (booted 2026-11-14T02:56:55Z)",
		"Boot count     7",
		"Boot config    w1i1c7 (image 1, watchdog on, counter 7)",
		"Root FS        ubi0:rootfs1",
		"Boot state     normal",
		"Last reboot    2026-11-13T05:32:10Z reboot/synthetic test",
		"ep-control     running (pid 977)",
		"EEPROM         /sys/bus/i2c/devices/0-0052/eeprom (32768 bytes)",
		"Local files    none (/rw/etc/mist/ep-control/local_* absent)",
		"Cloud config   /rw/etc/mist/ep-control/cloud.ini (overrides the firmware copy)",
		"  url (factory)   wss://ap.example.net/ws",
		"  rootcerts       /rw/etc/mist/ep-control/cacerts-production.pem",
		"  [tls] rootcerts /rw/etc/mist/ep-control/cacerts-production.pem",
		"CA bundle      /rw/etc/mist/ep-control/cacerts-production.pem (3 certificates)",
		"  CN=Synthetic Root X1,O=Example Trust (expires 2036-11-14)",
		"Error reports  /var/log/errors: ep-control.report.last",
		"ep-control: wrapper 1970-01-01T01:00:06Z.350508: started /usr/sbin/ep-control for the 2th time, pid 977",
		"system: Nov 14 03:00:00 ap daemon.info procmon: ep-control exited, restarting",
		"kernel: [ 4000.000000] Out of memory: Killed process 123 (pace)",
		"ep-control log (last 6 lines of /var/log/ep-control.log, secrets redacted):",
		"  PSK:<redacted>",
		`  "PSK": <redacted>,`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "synthetic-passphrase") {
		t.Fatal("inspect printed a passphrase from the ep-control log")
	}
	if notable := out[strings.Index(out, "Notable"):strings.Index(out, "ep-control log (")]; strings.Contains(notable, "1th time") {
		t.Fatal("the first ep-control start is listed as a restart")
	}
	assertReadOnly(t, ap.recorded())

	if !strings.Contains(result.stderr, "Recorded new host key for [127.0.0.1]:") {
		t.Fatalf("first connection did not record the host key: %s", result.stderr)
	}
	knownHostsFile := filepath.Join(home, ".config", "dismist", "known_hosts")
	info, err := os.Stat(knownHostsFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("known_hosts not private: %v %v", info, err)
	}

	again := runAP33Test(t, "inspect", "--log-lines", "0", "root@127.0.0.1", "-i", key.file, "-p", strconv.Itoa(ap.port()))
	if again.err != nil {
		t.Fatalf("second inspect: %v", again.err)
	}
	if strings.Contains(again.stderr, "Recorded") || strings.Contains(again.stdout, "ep-control log (") {
		t.Fatalf("second run re-recorded the key or printed logs with --log-lines 0:\n%s\n%s", again.stderr, again.stdout)
	}
	data, _ := os.ReadFile(knownHostsFile)
	if lines := strings.Count(string(data), "\n"); lines != 1 {
		t.Fatalf("known_hosts has %d lines, want 1", lines)
	}
}

func TestInspectJSONListsLocalFilesWithoutValues(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	overrides := `{"Monitor":{"Enabled":false,"PcapWLAN":{"ClientFirstBkgrdEnabled":false}},"Radios":[{"Band":"5"}]}`
	wlan := `{"Example":{"Auth":{"PSK":"synthetic-local-psk"},"WNM":{"BSSLoad":false}}}`
	ap.setFile(apEPControlRW+"/local_overrides.config", overrides)
	ap.setFile(apEPControlRW+"/local_wlan_overrides.config", wlan)
	ap.setFile(apEPControlRW+"/local_defaults.config", "{not json")

	result := runAP33Test(t, "inspect", "--json", "-i", key.file, "-p", strconv.Itoa(ap.port()), "127.0.0.1")
	if result.err != nil {
		t.Fatalf("inspect: %v\n%s", result.err, result.stderr)
	}
	if strings.Contains(result.stdout, "synthetic-local-psk") {
		t.Fatal("JSON report leaked a local file value")
	}
	var report InspectReport
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, result.stdout)
	}
	if report.Target != "root@127.0.0.1:"+strconv.Itoa(ap.port()) || report.ID != "02-aa-00-00-00-01" {
		t.Fatalf("target %q id %q", report.Target, report.ID)
	}
	if len(report.LocalFiles) != 3 {
		t.Fatalf("local files: %+v", report.LocalFiles)
	}
	byName := map[string]LocalFile{}
	for _, file := range report.LocalFiles {
		byName[file.Name] = file
	}
	sum := sha256.Sum256([]byte(overrides))
	got := byName["local_overrides.config"]
	if got.SHA256 != hex.EncodeToString(sum[:]) || got.Size != len(overrides) {
		t.Fatalf("local_overrides digest/size: %+v", got)
	}
	if want := []string{"Monitor.Enabled", "Monitor.PcapWLAN.ClientFirstBkgrdEnabled", "Radios[].Band"}; !slices.Equal(got.Keys, want) {
		t.Fatalf("keys = %v, want %v", got.Keys, want)
	}
	if keys := byName["local_wlan_overrides.config"].Keys; !slices.Equal(keys, []string{"Example.Auth.PSK", "Example.WNM.BSSLoad"}) {
		t.Fatalf("wlan keys = %v", keys)
	}
	if byName["local_defaults.config"].ParseError == "" {
		t.Fatal("invalid JSON local file not flagged")
	}
	found := false
	for _, warning := range report.Warnings {
		found = found || strings.Contains(warning, "3 local override file(s)")
	}
	if !found {
		t.Fatalf("no local file warning: %v", report.Warnings)
	}
	if report.BootConfig == nil || report.BootConfig.Image != 1 || report.UptimeSeconds == nil || *report.UptimeSeconds != 7384.52 {
		t.Fatalf("boot facts: %+v %v", report.BootConfig, report.UptimeSeconds)
	}
}

func TestInspectStockAndFactoryResetAP(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	ap.removeFile(apEPControlRW + "/cloud.ini")
	ap.setFile(apEPControlRO+"/cacerts-production.pem", syntheticCABundle(t, "Synthetic Mist Root"))

	result := runAP33Test(t, "inspect", "--json", "--log-lines", "0", "-i", key.file, "-p", strconv.Itoa(ap.port()), "127.0.0.1")
	if result.err != nil {
		t.Fatal(result.err)
	}
	var report InspectReport
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.Cloud == nil || report.Cloud.RWOverride || report.Cloud.Path != apEPControlRO+"/cloud.ini" || len(report.Cloud.Entries) != 2 {
		t.Fatalf("stock cloud config: %+v", report.Cloud)
	}
	if report.Cloud.Entries[0].URL != "wss://ep-terminator${FIPS}.mistsys.net/ws" || report.Cloud.ProxyHost != "" {
		t.Fatalf("stock entries: %+v", report.Cloud)
	}
	// ${FIPS} expands to "" on a non-FIPS image, so both entries share one bundle.
	if len(report.CABundles) != 1 || report.CABundles[0].Path != apEPControlRO+"/cacerts-production.pem" || len(report.CABundles[0].Certificates) != 1 {
		t.Fatalf("bundles: %+v", report.CABundles)
	}

	for name := range syntheticAPFiles() {
		if strings.HasPrefix(name, apEPControlRW+"/") {
			ap.removeFile(name)
		}
	}
	reset := runAP33Test(t, "ssh", "-i", key.file, "-p", strconv.Itoa(ap.port()), "127.0.0.1", "status")
	if reset.err != nil {
		t.Fatal(reset.err)
	}
	if !strings.Contains(reset.stdout, "/rw/etc/mist/ep-control does not exist") || !strings.Contains(reset.stdout, "Boot count     unknown") {
		t.Fatalf("factory-reset AP not reported:\n%s", reset.stdout)
	}
}

func TestInspectLegacyDropbearNeedsSHA1RSA(t *testing.T) {
	home := isolateSSH(t)
	rsaKey := newTestKey(t, home, "rsa")
	edKey := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{legacy: true}, rsaKey.signer.PublicKey(), edKey.signer.PublicKey())
	port := strconv.Itoa(ap.port())

	result := runAP33Test(t, "ssh", "-i", rsaKey.file, "-p", port, "root@127.0.0.1", "status")
	if result.err != nil {
		t.Fatalf("legacy dropbear with an RSA key: %v\n%s", result.err, result.stderr)
	}
	if !strings.Contains(result.stdout, "apfw-0.15.35064") {
		t.Fatalf("status output:\n%s", result.stdout)
	}
	ap.mu.Lock()
	negotiated := slices.Clone(ap.authAlgos)
	ap.mu.Unlock()
	if len(negotiated) != 1 || !strings.HasSuffix(negotiated[0], "/ssh-rsa") {
		t.Fatalf("host key algorithm was not SHA-1 ssh-rsa: %v", negotiated)
	}

	// Without the SHA-1 fallback the RSA key cannot sign for this server.
	strict := runAP33Test(t, "ssh", "-i", rsaKey.file, "-p", port, "-o", "PubkeyAcceptedAlgorithms=-ssh-rsa", "root@127.0.0.1", "status")
	if strict.err == nil || !strings.Contains(strict.err.Error(), "RSA or ECDSA") {
		t.Fatalf("expected an authentication failure with a hint, got %v", strict.err)
	}
	// dropbear 2017.75 has no ed25519.
	ed := runAP33Test(t, "ssh", "-i", edKey.file, "-p", port, "root@127.0.0.1", "status")
	if ed.err == nil || !strings.Contains(ed.err.Error(), "not ed25519") {
		t.Fatalf("expected the ed25519 hint, got %v", ed.err)
	}
	// Refusing the legacy host key algorithm leaves nothing in common.
	noRSAHost := runAP33Test(t, "ssh", "-i", rsaKey.file, "-p", port, "-o", "HostKeyAlgorithms=ssh-ed25519", "root@127.0.0.1", "status")
	if noRSAHost.err == nil || !strings.Contains(noRSAHost.err.Error(), "HostKeyAlgorithms") {
		t.Fatalf("expected a no-common-algorithm hint, got %v", noRSAHost.err)
	}
}

func TestInspectRejectsBadArguments(t *testing.T) {
	isolateSSH(t)
	for _, args := range [][]string{
		{"inspect"},
		{"inspect", "a", "b"},
		{"inspect", "--log-lines", "-1", "root@192.0.2.10"},
		{"inspect", "-o", "ProxyJump=bastion", "root@192.0.2.10"},
		{"inspect", "root@"},
		{"ssh", "root@192.0.2.10"},
		{"ssh", "root@192.0.2.10", "format"},
		{"bogus"},
		{},
	} {
		if result := runAP33Test(t, args...); result.err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

func TestRunnerBoundsOutputAndTime(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	options := newSSHOptions()
	if err := options.setOption("IdentityFile", key.file); err != nil {
		t.Fatal(err)
	}
	target, err := options.resolve("ssh://root@127.0.0.1:" + strconv.Itoa(ap.port()))
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	client, err := dialAP(context.Background(), options, target, &warn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	client.maxOutput = 1024
	if _, err := client.Run(context.Background(), "flood"); err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("flood: %v", err)
	}
	client.maxOutput = defaultMaxOutput
	client.commandTimeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := client.Run(context.Background(), "hang"); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("hang: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("command timeout not enforced")
	}
	client.commandTimeout = defaultCommandTimeout
	result, err := client.Run(context.Background(), "cat -- '/no such file'")
	if err != nil || result.ExitStatus != 1 || !strings.Contains(string(result.Stderr), "No such file") {
		t.Fatalf("missing file: %+v %v", result, err)
	}
}
