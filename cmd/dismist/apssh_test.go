package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAPSSHStatusIsReadOnlySubset(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	result := runAP33Test(t, "ssh", "-i", key.file, "-p", strconv.Itoa(ap.port()), "root@127.0.0.1", "status", "--json")
	if result.err != nil {
		t.Fatal(result.err)
	}
	var report InspectReport
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatal(err)
	}
	if report.ID != "02-aa-00-00-00-01" || report.BootCount == nil || *report.BootCount != 7 || report.Cloud == nil {
		t.Fatalf("status report: %+v", report)
	}
	if len(report.Logs) != 0 || len(report.CABundles) != 0 || len(report.EEPROMs) != 0 {
		t.Fatal("status collected the full inspect set")
	}
	assertReadOnly(t, ap.recorded())
}

func TestAPSSHRebootRequiresConfirmation(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	result := runAP33Test(t, "ssh", "-i", key.file, "-p", strconv.Itoa(ap.port()), "root@127.0.0.1", "reboot")
	if result.err == nil || !strings.Contains(result.err.Error(), "--yes") {
		t.Fatalf("reboot without --yes: %v", result.err)
	}
	if len(ap.recorded()) != 0 {
		t.Fatal("reboot without --yes connected to the AP")
	}
	for _, reason := range []string{"two\nlines", strings.Repeat("x", 121), "café"} {
		if result := runAP33Test(t, "ssh", "127.0.0.1", "reboot", "--yes", "--reason", reason); result.err == nil {
			t.Errorf("reason %q accepted", reason)
		}
	}
}

func TestAPSSHRebootRecordsReason(t *testing.T) {
	home := isolateSSH(t)
	key := newTestKey(t, home, "ed25519")
	ap := newFakeAP(t, fakeAPOptions{}, key.signer.PublicKey())
	port := strconv.Itoa(ap.port())
	before := time.Now().Unix()

	result := runAP33Test(t, "ssh", "-i", key.file, "-p", port, "root@127.0.0.1", "reboot", "--yes", "--reason", "it's a test")
	if result.err != nil {
		t.Fatalf("reboot: %v\n%s", result.err, result.stderr)
	}
	if !strings.Contains(result.stdout, "Before reboot: AP 02-aa-00-00-00-01, boot count 7, uptime 2h 3m 5s") || !strings.Contains(result.stdout, "Reboot requested") {
		t.Fatalf("reboot output:\n%s", result.stdout)
	}
	ap.mu.Lock()
	rebooted := ap.rebooted
	ap.mu.Unlock()
	if rebooted != 1 {
		t.Fatalf("rebooted %d times", rebooted)
	}
	content, ok := ap.file(apEPControlRW + "/last_reboot_info.txt")
	match := regexp.MustCompile(`^(\d+)::reboot/it's a test$`).FindStringSubmatch(content)
	if !ok || match == nil {
		t.Fatalf("last_reboot_info.txt = %q", content)
	}
	if stamp, _ := strconv.ParseInt(match[1], 10, 64); stamp < before || stamp > time.Now().Unix()+1 {
		t.Fatalf("timestamp %s outside the run", match[1])
	}
	if _, ok := ap.file(apEPControlRW + "/last_reboot_info.txt.dismist"); ok {
		t.Fatal("temporary file left behind")
	}

	// An unreported reason from an earlier reboot is evidence; keep it.
	ap.setFile(apEPControlRW+"/last_reboot_info.txt", "1794547930::watchdog")
	result = runAP33Test(t, "ssh", "-i", key.file, "-p", port, "root@127.0.0.1", "reboot", "--yes")
	if result.err != nil {
		t.Fatal(result.err)
	}
	if content, _ := ap.file(apEPControlRW + "/last_reboot_info.txt"); content != "1794547930::watchdog" {
		t.Fatalf("pending reason overwritten: %q", content)
	}
	if !strings.Contains(result.stderr, `unreported reason "watchdog"`) {
		t.Fatalf("no warning about the pending reason: %s", result.stderr)
	}

	// --no-reason sends a bare reboot.
	ap.removeFile(apEPControlRW + "/last_reboot_info.txt")
	result = runAP33Test(t, "ssh", "-i", key.file, "-p", port, "root@127.0.0.1", "reboot", "--yes", "--no-reason")
	if result.err != nil {
		t.Fatal(result.err)
	}
	commands := ap.recorded()
	if commands[len(commands)-1] != "reboot" {
		t.Fatalf("last command %q", commands[len(commands)-1])
	}
	if _, ok := ap.file(apEPControlRW + "/last_reboot_info.txt"); ok {
		t.Fatal("--no-reason wrote a reason")
	}
}

func TestRebootCommandQuoting(t *testing.T) {
	if got := rebootCommand(""); got != "reboot" {
		t.Fatalf("bare reboot = %q", got)
	}
	got := rebootCommand(`a'b; rm -rf /`)
	if !strings.Contains(got, `'reboot/a'\''b; rm -rf /'`) || !strings.HasSuffix(got, "; reboot") {
		t.Fatalf("reason not quoted: %s", got)
	}
}
