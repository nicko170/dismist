package main

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseCloudINI(t *testing.T) {
	config := parseCloudINI(syntheticCustomCloudINI)
	if len(config.Entries) != 2 || config.TLSRootCerts != apEPControlRW+"/cacerts-production.pem" || config.ProxyHost != "" {
		t.Fatalf("custom: %+v", config)
	}
	for i, entry := range config.Entries {
		if entry.URL != "wss://ap.example.net/ws" || entry.RootCerts != apEPControlRW+"/cacerts-production.pem" || entry.Insecure {
			t.Fatalf("entry %d: %+v", i, entry)
		}
	}
	if !config.Entries[0].Manufacturing || config.Entries[1].Manufacturing {
		t.Fatal("manufacturing flags")
	}

	tricky := `
[[cloud]]
  url = "wss://a.example.net/ws" # trailing comment
  [cloud.tls]
    insecure = true
    rootcerts = "/rw/x#y.pem"
[proxy]
  url = "http://user:secret@proxy.example.net:8080/"
[[other]]
  url = "wss://not-a-cloud.example.net"
`
	config = parseCloudINI(tricky)
	if len(config.Entries) != 1 || config.Entries[0].URL != "wss://a.example.net/ws" || !config.Entries[0].Insecure || config.Entries[0].RootCerts != "/rw/x#y.pem" {
		t.Fatalf("tricky: %+v", config)
	}
	if config.ProxyHost != "http://proxy.example.net:8080" {
		t.Fatalf("proxy credentials not stripped: %q", config.ProxyHost)
	}
}

func TestRootCertPathsExpandFIPS(t *testing.T) {
	report := &InspectReport{Cloud: &CloudConfig{Entries: []CloudEntry{
		{RootCerts: "/etc/mist/ep-control/cacerts-production${FIPS}.pem"},
		{RootCerts: "/etc/mist/ep-control/cacerts-production.pem"},
	}}}
	if got := report.rootCertPaths(); !slices.Equal(got, []string{"/etc/mist/ep-control/cacerts-production.pem"}) {
		t.Fatalf("non-FIPS: %v", got)
	}
	report.Firmware.FIPS = "1"
	if got := report.rootCertPaths(); len(got) != 2 || got[0] != "/etc/mist/ep-control/cacerts-production-fips.pem" {
		t.Fatalf("FIPS: %v", got)
	}
}

func TestBootFactsParsers(t *testing.T) {
	config, err := parseBootConfig("w0i2c3")
	if err != nil || config.Watchdog || config.Image != 2 || config.Counter != 3 {
		t.Fatalf("bootcfg: %+v %v", config, err)
	}
	for _, bad := range []string{"", "w1i3c7", "w1i1c8", "w1i1c7x", "unable to open"} {
		if _, err := parseBootConfig(bad); err == nil {
			t.Errorf("bootcfg %q accepted", bad)
		}
	}
	if seconds, ok := parseUptime("12.5 3.0\n"); !ok || seconds != 12.5 {
		t.Fatal("uptime")
	}
	if _, ok := parseUptime("garbage"); ok {
		t.Fatal("garbage uptime accepted")
	}
	if got := rootDevice("rootfs / rootfs rw 0 0\nubi0:rootfs2 / ubifs ro 0 0\n"); got != "ubi0:rootfs2" {
		t.Fatalf("root device %q", got)
	}
	info := parseRebootInfo("1794547930::reboot/a::b\n")
	if info.Time == nil || !info.Time.Equal(time.Unix(1794547930, 0)) || info.Reason != "reboot/a::b" {
		t.Fatalf("reboot info %+v", info)
	}
	if info := parseRebootInfo("unknown"); info.Time != nil || info.Reason != "unknown" {
		t.Fatalf("reboot info without time %+v", info)
	}
	values := parseShellAssignments("# c\nA=1\nB=\"two\"\nC='3'\nbad line\n")
	if values["A"] != "1" || values["B"] != "two" || values["C"] != "3" || len(values) != 3 {
		t.Fatalf("assignments %v", values)
	}
	if formatUptime(26*time.Hour+3*time.Minute) != "1d 2h 3m" || formatUptime(65*time.Second) != "1m 5s" {
		t.Fatal("formatUptime")
	}
}

func TestRedactLines(t *testing.T) {
	in := []string{
		"  PSK:synthetic-1",
		`  "PSK": "synthetic \"2\"",`,
		"  Key: synthetic-3",
		"  PAPIOpaque:synthetic-4}",
		"KeyEncryptKey=synthetic-5 next",
		"  AuthorizedKeys:[",
		"  Key:",
		"radius secret: synthetic-6",
		"connected to wss://ap.example.net/ws",
	}
	out := redactLines(slices.Clone(in))
	joined := strings.Join(out, "\n")
	for i := 1; i <= 6; i++ {
		if strings.Contains(joined, "synthetic-"+string(rune('0'+i))) || strings.Contains(joined, `synthetic \"2\"`) {
			t.Fatalf("secret %d survived:\n%s", i, joined)
		}
	}
	for _, keep := range []string{"  AuthorizedKeys:[", "  Key:", "connected to wss://ap.example.net/ws", "PAPIOpaque:<redacted>}", "<redacted> next"} {
		if !strings.Contains(joined, keep) {
			t.Fatalf("redaction removed %q:\n%s", keep, joined)
		}
	}
}

func TestNotableLines(t *testing.T) {
	for line, want := range map[string]bool{
		"started /usr/sbin/ep-control for the 1th time, pid 211":  false,
		"started /usr/sbin/ep-control for the 2th time, pid 211":  true,
		"started /usr/sbin/ep-control for the 12th time, pid 211": true,
		"procmon: restarting pace":                                true,
		"Out of memory: Killed process 1":                         true,
		"bloom filter refreshed":                                  false,
		"PoE budget 4300":                                         true,
		"Can't unmarshal config":                                  true,
		"LLDP neighbour changed":                                  true,
		"ordinary line":                                           false,
	} {
		if got := notableLine.MatchString(line); got != want {
			t.Errorf("notable(%q) = %v", line, got)
		}
	}
}

func TestDescribeLocalFileAndCABundle(t *testing.T) {
	file := describeLocalFile("local_radio_overrides.config", []byte(`{"2.4":{"Channel":1,"Width":40},"5":{"Channel":64},"Empty":{},"List":[]}`))
	if want := []string{"2.4.Channel", "2.4.Width", "5.Channel", "Empty", "List[]"}; !slices.Equal(file.Keys, want) {
		t.Fatalf("keys %v, want %v", file.Keys, want)
	}
	certs, problem := parseCABundle([]byte(syntheticCABundle(t, "Synthetic A") + "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"))
	if problem != "" || len(certs) != 2 || certs[0].Subject != "CN=Synthetic A,O=Example Trust" || certs[1].Error == "" {
		t.Fatalf("bundle %+v %q", certs, problem)
	}
	if _, problem := parseCABundle([]byte("nothing")); problem == "" {
		t.Fatal("empty bundle accepted")
	}
}
