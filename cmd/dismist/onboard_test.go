package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
)

// Tests for `dismist ap33 onboard` and `restore` against the in-process
// SSH AP (fakeap_ssh_test.go) and an httptest controller that speaks the
// operator API contract (design §3.15, §4.8, §4.9).

const testAPIToken = "dsm_test_0123456789abcdef"

// syntheticSecret is the 32-byte secret the fake EEPROM holds at 0x400.
var syntheticSecret = func() []byte {
	secret := make([]byte, secretSize)
	for i := range secret {
		secret[i] = byte(0xa0 + i)
	}
	return secret
}()

func syntheticEEPROM() string {
	eeprom := bytes.Repeat([]byte{0xff}, eepromSize)
	copy(eeprom[secretOffset:], syntheticSecret)
	return string(eeprom)
}

// stockAP makes the fake a factory-state 0.15 AP: no /rw cloud.ini or
// bundle, the firmware's own bundle, and a provisioned EEPROM.
func stockAP(t *testing.T, ap *fakeAP) string {
	t.Helper()
	ap.removeFile(apEPControlRW + "/cloud.ini")
	ap.removeFile(apEPControlRW + "/cacerts-production.pem")
	stock := syntheticCABundle(t, "Synthetic Mist Root", "Synthetic Mist Intermediate")
	ap.setFile(stockBundlePath, stock)
	ap.setFile("/sys/bus/i2c/devices/0-0052/eeprom", syntheticEEPROM())
	return stock
}

// fakeController is the operator API as far as the CLI uses it.
type fakeController struct {
	t      *testing.T
	server *httptest.Server
	ap     *fakeAP

	mu         sync.Mutex
	scope      api.TokenScope
	view       api.OnboardingView
	sites      []api.SiteSummary
	record     *api.APDetail
	secret     string
	requests   []string
	posts      []api.APInput
	patches    []map[string]any
	ifMatches  []string
	staleTimes int
	conflict   bool
	// neverConnects keeps the AP offline whatever happens (timeout test).
	neverConnects bool
	reboots       int
	clock         time.Time
}

func newFakeController(t *testing.T, ap *fakeAP, wss *httptest.Server) *fakeController {
	t.Helper()
	c := &fakeController{
		t: t, ap: ap, scope: api.TokenScopeAdmin,
		sites: []api.SiteSummary{{ID: "site-home", Name: "Home", Country: "AU"}, {ID: "site-two", Name: "Garage"}},
		clock: inspectNow,
	}
	if wss != nil {
		c.view = api.OnboardingView{
			WSSURL: "wss://" + wss.Listener.Addr().String() + "/ws",
			CAPems: []api.CAPem{{Name: "Test endpoint CA", PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: wss.Certificate().Raw}))}},
		}
	}
	c.server = httptest.NewServer(http.HandlerFunc(c.handle))
	t.Cleanup(c.server.Close)
	return c
}

func (c *fakeController) fail(w http.ResponseWriter, status int, code api.ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.ErrorBody{Error: message, Code: code})
}

func (c *fakeController) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (c *fakeController) handle(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+testAPIToken {
		c.fail(w, http.StatusUnauthorized, api.ErrorCodeUnauthorized, "Sign in to continue.")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/aps/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/auth/session":
		c.reply(w, 200, api.AuthSession{Authenticated: true, Via: api.AuthViaToken, Scope: c.scope, TokenName: "test"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/onboarding":
		c.reply(w, 200, c.view)
	case r.Method == http.MethodGet && r.URL.Path == "/api/sites":
		c.reply(w, 200, c.sites)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/aps/"):
		if c.record == nil || c.record.ID != id {
			c.fail(w, http.StatusNotFound, api.ErrorCodeNotFound, "AP not found.")
			return
		}
		if rebooted := c.ap.rebootCount(); rebooted > c.reboots {
			c.reboots = rebooted
			if !c.neverConnects {
				c.clock = c.clock.Add(time.Minute)
				at := c.clock
				c.record.Connected, c.record.ConnectedAt = true, &at
				c.record.AppliedRevision = c.record.ConfigRevision
				c.record.Status = api.APStatusInSync
				c.record.Model, c.record.Firmware, c.record.LANIP, c.record.RemoteAddress = "AP33-WW", api.TestedFirmware, "192.0.2.21", "198.51.100.7"
			} else {
				c.record.Connected, c.record.Status = false, api.APStatusOffline
			}
		}
		c.reply(w, 200, c.record)
	case r.Method == http.MethodPost && r.URL.Path == "/api/aps":
		var input api.APInput
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			c.fail(w, http.StatusBadRequest, api.ErrorCodeInvalid, err.Error())
			return
		}
		c.posts = append(c.posts, input)
		if c.conflict {
			c.conflict = false
			c.record = &api.APDetail{APView: api.APView{ID: input.ID, Name: "Someone else", SiteID: "site-two", UpdatedRev: 4, Status: api.APStatusNeverConnected}}
			c.fail(w, http.StatusConflict, api.ErrorCodeConflict, "An AP with this ID is already registered.")
			return
		}
		c.secret = input.Secret
		c.record = &api.APDetail{APView: api.APView{
			ID: input.ID, Name: input.Name, SiteID: input.SiteID, PinnedIP: input.ManagementIP, HasSecret: true,
			Status: api.APStatusNeverConnected, ConfigRevision: 43, UpdatedRev: 1,
		}, LocalFiles: input.LocalFiles}
		c.reply(w, http.StatusCreated, api.MutationResponse[api.APView]{Revision: 43, Object: c.record.APView})
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/aps/"):
		c.ifMatches = append(c.ifMatches, r.Header.Get("If-Match"))
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			c.fail(w, http.StatusBadRequest, api.ErrorCodeInvalid, err.Error())
			return
		}
		c.patches = append(c.patches, patch)
		if c.record == nil || c.record.ID != id {
			c.fail(w, http.StatusNotFound, api.ErrorCodeNotFound, "AP not found.")
			return
		}
		if c.staleTimes > 0 {
			c.staleTimes--
			c.record.UpdatedRev++
			c.fail(w, http.StatusConflict, api.ErrorCodeStale, "The AP changed since you loaded it.")
			return
		}
		if r.Header.Get("If-Match") != strconv.Quote(strconv.FormatUint(c.record.UpdatedRev, 10)) {
			c.fail(w, http.StatusConflict, api.ErrorCodeStale, "The AP changed since you loaded it.")
			return
		}
		if name, ok := patch["name"].(string); ok {
			c.record.Name = name
		}
		if site, ok := patch["site_id"].(string); ok {
			c.record.SiteID = site
		}
		if secret, ok := patch["secret"].(string); ok {
			c.secret = secret
		}
		c.record.UpdatedRev++
		c.reply(w, 200, api.MutationResponse[api.APView]{Revision: 44, Object: c.record.APView})
	default:
		c.fail(w, http.StatusNotFound, api.ErrorCodeNotFound, "no such endpoint")
	}
}

func (c *fakeController) snapshot() (requests []string, posts []api.APInput, patches []map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.requests), slices.Clone(c.posts), slices.Clone(c.patches)
}

// onboardFixture is one AP, one WSS endpoint and one controller.
type onboardFixture struct {
	t          *testing.T
	ap         *fakeAP
	key        testKey
	wss        *httptest.Server
	controller *fakeController
	tokenFile  string
	backupDir  string
	stockCA    string
	answers    []bool
	asked      []string
}

func newOnboardFixture(t *testing.T) *onboardFixture {
	t.Helper()
	home := isolateSSH(t)
	f := &onboardFixture{t: t, key: newTestKey(t, home, "ed25519")}
	f.ap = newFakeAP(t, fakeAPOptions{}, f.key.signer.PublicKey())
	f.stockCA = stockAP(t, f.ap)
	f.wss = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(f.wss.Close)
	f.controller = newFakeController(t, f.ap, f.wss)
	f.tokenFile = filepath.Join(home, "token")
	if err := os.WriteFile(f.tokenFile, []byte(testAPIToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.backupDir = filepath.Join(home, "backups")
	return f
}

func (f *onboardFixture) run(args ...string) cliResult {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	env := ap33Env{
		ctx: ctx, stdout: &stdout, stderr: &stderr, now: func() time.Time { return inspectNow },
		pollInterval: 5 * time.Millisecond,
		confirm: func(question string) (bool, error) {
			f.asked = append(f.asked, question)
			if len(f.answers) == 0 {
				return false, nil
			}
			answer := f.answers[0]
			f.answers = f.answers[1:]
			return answer, nil
		},
	}
	err := runAP33With(args, env)
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (f *onboardFixture) sshArgs() []string {
	return []string{"-i", f.key.file, "-p", strconv.Itoa(f.ap.port()), "root@127.0.0.1"}
}

func (f *onboardFixture) onboard(extra ...string) cliResult {
	f.t.Helper()
	args := append([]string{"onboard"}, f.sshArgs()...)
	args = append(args, "--controller", f.controller.server.URL, "--token-file", f.tokenFile, "--backup-dir", f.backupDir)
	return f.run(append(args, extra...)...)
}

func requireExit(t *testing.T, result cliResult, code int) {
	t.Helper()
	if got := exitCode(result.err); got != code {
		t.Fatalf("exit code %d, want %d (err %v)\nstdout:\n%s\nstderr:\n%s", got, code, result.err, result.stdout, result.stderr)
	}
}

func TestOnboardFreshAPEndToEnd(t *testing.T) {
	f := newOnboardFixture(t)
	result := f.onboard("--site", "home", "--name", "Hallway", "--public-roots", "letsencrypt")
	requireExit(t, result, exitDone)

	for _, want := range []string{
		"Controller: " + f.controller.server.URL + ", endpoint " + f.controller.view.WSSURL + ", 1 CA certificate(s) to add",
		"Preflight: AP 02-aa-00-00-00-01, apfw-0.15.35064-noro-84df (aplx) on ubi0:rootfs1",
		"Registration: 02-aa-00-00-00-01 is not registered at " + f.controller.server.URL + " yet",
		"Local files: none in /rw/etc/mist/ep-control",
		"TLS: the chain " + f.wss.Listener.Addr().String() + " serves verifies against the new bundle",
		"CA bundle: /etc/mist/ep-control/cacerts-production.pem plus 5 certificate(s): ",
		"ISRG Root X1", "ISRG Root X2", "Root YE", "Root YR",
		"cloud.ini: point both [[cloud]] entries at " + f.controller.view.WSSURL,
		"Secret: read 32 bytes at offset 0x400",
		"Installed /rw/etc/mist/ep-control/cacerts-production.pem (7 certificates, sha256 verified on the AP)",
		"Installed /rw/etc/mist/ep-control/cloud.ini (sha256 verified on the AP)",
		"Registration: 02-aa-00-00-00-01 registered in site Home (site-home)",
		"Reboot: requested",
		"Done: 02-aa-00-00-00-01 (AP33-WW, apfw-0.15.35064-noro-84df) is connected and in sync at rev 43; LAN address 192.0.2.21, public address 198.51.100.7",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if t.Failed() {
		t.Fatalf("stdout:\n%s", result.stdout)
	}
	secretHex := hex.EncodeToString(syntheticSecret)
	if strings.Contains(result.stdout+result.stderr, secretHex) {
		t.Fatal("the secret was printed")
	}

	// The AP: bundle = its own stock bundle + endpoint CA + LE roots; the
	// bundle went in before cloud.ini; then one reboot with a reason.
	if got := f.ap.written(); !slices.Equal(got, []string{rwRootCertsPath, rwCloudINIPath}) {
		t.Fatalf("write order %v", got)
	}
	bundle, _ := f.ap.file(rwRootCertsPath)
	if !strings.HasPrefix(bundle, strings.TrimRight(f.stockCA, "\n")) {
		t.Fatal("the new bundle does not start with the AP's own bundle")
	}
	certs, err := parsePEMCertificates([]byte(bundle))
	if err != nil || len(certs) != 7 {
		t.Fatalf("bundle has %d certificates (%v)", len(certs), err)
	}
	ini, _ := f.ap.file(rwCloudINIPath)
	parsed := parseCloudINI(ini)
	if len(parsed.Entries) != 2 {
		t.Fatalf("cloud.ini entries %+v", parsed.Entries)
	}
	for _, entry := range parsed.Entries {
		if entry.URL != f.controller.view.WSSURL || entry.RootCerts != rwRootCertsPath || entry.Insecure {
			t.Fatalf("cloud.ini entry %+v", entry)
		}
	}
	if f.ap.rebootCount() != 1 {
		t.Fatalf("rebooted %d times", f.ap.rebootCount())
	}
	if reason, _ := f.ap.file(apEPControlRW + "/last_reboot_info.txt"); !strings.HasSuffix(reason, "::reboot/"+onboardRebootReason) {
		t.Fatalf("reboot reason %q", reason)
	}

	// The controller: one POST with the hex secret, the site ID and an
	// empty (checked, none found) local file list.
	_, posts, patches := f.controller.snapshot()
	if len(posts) != 1 || len(patches) != 0 {
		t.Fatalf("posts %d patches %d", len(posts), len(patches))
	}
	post := posts[0]
	if post.ID != "02-aa-00-00-00-01" || post.Name != "Hallway" || post.SiteID != "site-home" || post.Secret != secretHex || post.ManagementIP != "" {
		t.Fatalf("POST %+v", post)
	}
	if post.LocalFiles == nil || len(post.LocalFiles) != 0 {
		t.Fatalf("local_files %#v, want an empty list", post.LocalFiles)
	}

	// The backup: private files, sums that verify, no secret in the
	// manifest, the original (stock) state of /rw.
	backup := onlyBackup(t, f.backupDir, "02-aa-00-00-00-01")
	for _, name := range []string{backupTarName, backupEEPROMName, backupManifestName, backupSumsName} {
		info, err := os.Stat(filepath.Join(backup, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, err, info)
		}
	}
	loaded, err := loadBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.EPControl["cloud.ini"]; ok {
		t.Fatal("the backup holds the new cloud.ini, not the original state")
	}
	if !bytes.Equal(loaded.EEPROM, []byte(syntheticEEPROM())) {
		t.Fatal("EEPROM backup differs")
	}
	manifest, _ := os.ReadFile(filepath.Join(backup, backupManifestName))
	if strings.Contains(string(manifest), secretHex) {
		t.Fatal("manifest holds the secret")
	}
	if info, err := os.Stat(filepath.Dir(backup)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("backup directory mode: %v %v", err, info)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(backup), "known_hosts")); err != nil {
		t.Fatalf("host key not kept with the backups: %v", err)
	}
}

func onlyBackup(t *testing.T, root, id string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, id))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(root, id, entry.Name()))
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("backups: %v", dirs)
	}
	return dirs[0]
}

func TestOnboardRerunIsIdempotentAndPatchesWithIfMatch(t *testing.T) {
	f := newOnboardFixture(t)
	requireExit(t, f.onboard("--site", "Home", "--name", "Hallway"), exitDone)
	writes := len(f.ap.written())

	// Second run: files already right, AP connected: nothing written, no
	// reboot, and the registration refreshed with a PATCH.
	result := f.onboard("--site", "Home", "--name", "Hallway")
	requireExit(t, result, exitDone)
	if len(f.ap.written()) != writes {
		t.Fatalf("re-run wrote %v", f.ap.written()[writes:])
	}
	if f.ap.rebootCount() != 1 {
		t.Fatalf("re-run rebooted (%d)", f.ap.rebootCount())
	}
	for _, want := range []string{"CA bundle: /rw/etc/mist/ep-control/cacerts-production.pem already holds every CA", "unchanged", "refreshed (secret, local files)", "Reboot: not needed"} {
		if !strings.Contains(result.stdout, want) {
			t.Errorf("output lacks %q\n%s", want, result.stdout)
		}
	}
	_, posts, patches := f.controller.snapshot()
	if len(posts) != 1 || len(patches) != 1 {
		t.Fatalf("posts %d patches %d", len(posts), len(patches))
	}
	if patches[0]["secret"] != hex.EncodeToString(syntheticSecret) {
		t.Fatal("PATCH did not refresh the secret")
	}
	if _, ok := patches[0]["name"]; ok {
		t.Fatal("PATCH sent an unchanged name")
	}
	if got := f.controller.ifMatches; len(got) != 1 || got[0] != `"1"` {
		t.Fatalf("If-Match %v", got)
	}
}

func TestOnboardRepeatsTheGETOnceAfterA409(t *testing.T) {
	f := newOnboardFixture(t)
	f.controller.record = &api.APDetail{APView: api.APView{ID: "02-aa-00-00-00-01", Name: "Old", SiteID: "site-two", UpdatedRev: 7, ConfigRevision: 40}}
	f.controller.staleTimes = 1
	result := f.onboard("--site", "Home", "--name", "Hallway", "--no-reboot")
	requireExit(t, result, exitDone)
	if !strings.Contains(result.stdout, "changed on the controller meanwhile (409 stale); reading it again") {
		t.Fatalf("no retry line:\n%s", result.stdout)
	}
	if got := f.controller.ifMatches; !slices.Equal(got, []string{`"7"`, `"8"`}) {
		t.Fatalf("If-Match %v, want the first GET's then the second GET's revision", got)
	}
	_, _, patches := f.controller.snapshot()
	last := patches[len(patches)-1]
	if last["name"] != "Hallway" || last["site_id"] != "site-home" {
		t.Fatalf("PATCH %v", last)
	}
	if !strings.Contains(result.stdout, `name "Old" → "Hallway", site site-two → site-home`) {
		t.Fatalf("change summary missing:\n%s", result.stdout)
	}
	if f.ap.rebootCount() != 0 || !strings.Contains(result.stdout, "Reboot: skipped (--no-reboot)") {
		t.Fatal("--no-reboot rebooted")
	}

	// A second 409 is not retried again: the error reaches the operator.
	g := newOnboardFixture(t)
	g.controller.record = &api.APDetail{APView: api.APView{ID: "02-aa-00-00-00-01", UpdatedRev: 7}}
	g.controller.staleTimes = 2
	client, err := newControllerClient(g.controller.server.URL, testAPIToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upsertAP(context.Background(), client, registration{ID: "02-aa-00-00-00-01", Secret: syntheticSecret}, io.Discard); !isAPIStatus(err, http.StatusConflict, "stale") {
		t.Fatalf("two stale replies: %v", err)
	}
	if _, _, patches := g.controller.snapshot(); len(patches) != 2 {
		t.Fatalf("%d PATCHes, want 2", len(patches))
	}
}

func TestUpsertHandlesAPOSTConflict(t *testing.T) {
	f := newOnboardFixture(t)
	f.controller.conflict = true
	client, err := newControllerClient(f.controller.server.URL, testAPIToken)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	action, err := upsertAP(context.Background(), client, registration{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "site-home", Secret: syntheticSecret}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "registered meanwhile") || !strings.HasPrefix(action, "updated:") {
		t.Fatalf("action %q, output %q", action, out.String())
	}
	requests, posts, patches := f.controller.snapshot()
	if len(posts) != 1 || len(patches) != 1 {
		t.Fatalf("requests %v", requests)
	}
}

func TestOnboardLocalFiles(t *testing.T) {
	t.Run("not confirmed", func(t *testing.T) {
		f := newOnboardFixture(t)
		f.ap.setFile(apEPControlRW+"/local_overrides", `{"LED":{"Brightness":10}}`)
		result := f.onboard("--site", "Home", "--name", "Hallway")
		requireExit(t, result, exitLocalFilesSet)
		if len(f.ap.written()) != 0 || f.ap.rebootCount() != 0 {
			t.Fatal("changed the AP without confirmation")
		}
		if _, err := os.Stat(filepath.Join(f.backupDir, "02-aa-00-00-00-01")); !os.IsNotExist(err) {
			t.Fatalf("a refused run left a backup: %v", err)
		}
		if len(f.asked) != 1 || !strings.Contains(f.asked[0], "local_* file(s) change what this AP runs") {
			t.Fatalf("asked %v", f.asked)
		}
		if requests, posts, _ := f.controller.snapshot(); len(posts) != 0 {
			t.Fatalf("registered anyway: %v", requests)
		}
	})
	t.Run("kept with --yes and reported", func(t *testing.T) {
		f := newOnboardFixture(t)
		content := `{"LED":{"Brightness":10}}`
		f.ap.setFile(apEPControlRW+"/local_overrides", content)
		result := f.onboard("--site", "Home", "--name", "Hallway", "--yes")
		requireExit(t, result, exitDone)
		_, posts, _ := f.controller.snapshot()
		files := posts[0].LocalFiles
		if len(files) != 1 || files[0].Name != "local_overrides" || files[0].SHA256 != sha256Hex([]byte(content)) || !files[0].SeenAt.Equal(inspectNow) {
			t.Fatalf("local_files %+v", files)
		}
		if _, ok := f.ap.file(apEPControlRW + "/local_overrides"); !ok {
			t.Fatal("--yes removed the local file")
		}
		backup, err := loadBackup(onlyBackup(t, f.backupDir, "02-aa-00-00-00-01"))
		if err != nil || string(backup.EPControl["local_overrides"]) != content {
			t.Fatalf("local file not in the backup: %v", err)
		}
	})
	t.Run("moved aside", func(t *testing.T) {
		f := newOnboardFixture(t)
		f.ap.setFile(apEPControlRW+"/local_radio_overrides", `{"Radios":[]}`)
		result := f.onboard("--site", "Home", "--name", "Hallway", "--remove-local-files")
		requireExit(t, result, exitDone)
		if _, ok := f.ap.file(apEPControlRW + "/local_radio_overrides"); ok {
			t.Fatal("the local file is still in use")
		}
		moved := false
		for _, name := range f.ap.written() {
			if strings.HasPrefix(name, localFilesAsideRoot+"/") && strings.HasSuffix(name, "/local_radio_overrides") {
				moved = true
			}
		}
		if !moved {
			t.Fatalf("not moved aside: %v", f.ap.written())
		}
		_, posts, _ := f.controller.snapshot()
		if len(posts[0].LocalFiles) != 0 {
			t.Fatalf("reported %v after moving them", posts[0].LocalFiles)
		}
	})
}

func TestOnboardTLSMismatchChangesNothing(t *testing.T) {
	f := newOnboardFixture(t)
	f.controller.view.CAPems = []api.CAPem{{Name: "Wrong CA", PEM: syntheticCABundle(t, "Not The Issuer")}}
	result := f.onboard("--site", "Home", "--name", "Hallway", "--public-roots", "none")
	requireExit(t, result, exitTLSMismatch)
	if len(f.ap.written()) != 0 || f.ap.rebootCount() != 0 {
		t.Fatal("wrote to the AP after a TLS mismatch")
	}
	if _, posts, _ := f.controller.snapshot(); len(posts) != 0 {
		t.Fatal("registered after a TLS mismatch")
	}
	if !strings.Contains(result.stderr, "Chain served by") || !strings.Contains(result.err.Error(), "nothing was changed") {
		t.Fatalf("stderr %q err %v", result.stderr, result.err)
	}
}

func TestOnboardDryRunWritesNothing(t *testing.T) {
	f := newOnboardFixture(t)
	result := f.onboard("--site", "Home", "--name", "Hallway", "--dry-run")
	requireExit(t, result, exitDone)
	if len(f.ap.written()) != 0 || f.ap.rebootCount() != 0 {
		t.Fatal("dry run changed the AP")
	}
	if _, err := os.Stat(f.backupDir); !os.IsNotExist(err) {
		t.Fatal("dry run wrote a backup")
	}
	if requests, posts, patches := f.controller.snapshot(); len(posts)+len(patches) != 0 {
		t.Fatalf("dry run wrote to the controller: %v", requests)
	}
	for _, want := range []string{"Dry run: nothing was written", "write /rw/etc/mist/ep-control/cacerts-production.pem", "register 02-aa-00-00-00-01 as \"Hallway\" in site Home (POST /api/aps)", "reboot the AP"} {
		if !strings.Contains(result.stdout, want) {
			t.Errorf("dry run lacks %q\n%s", want, result.stdout)
		}
	}
	for _, command := range f.ap.recorded() {
		if strings.Contains(command, "eeprom") && strings.HasPrefix(command, "cat ") {
			t.Fatalf("dry run read the EEPROM: %q", command)
		}
	}
}

func TestOnboardRetargetOnlyRewritesTrust(t *testing.T) {
	f := newOnboardFixture(t)
	f.controller.record = &api.APDetail{APView: api.APView{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "site-home", UpdatedRev: 3, ConfigRevision: 42, AppliedRevision: 42}}
	result := f.onboard("--retarget", "--no-reboot")
	requireExit(t, result, exitDone)
	if got := f.ap.written(); !slices.Equal(got, []string{rwRootCertsPath, rwCloudINIPath}) {
		t.Fatalf("retarget wrote %v", got)
	}
	requests, posts, patches := f.controller.snapshot()
	if len(posts)+len(patches) != 0 || slices.Contains(requests, "GET /api/sites") {
		t.Fatalf("retarget touched the registration: %v", requests)
	}
	if strings.Contains(result.stdout, "Secret:") {
		t.Fatal("retarget read the secret")
	}

	// --retarget of an AP the controller does not know is an API error.
	g := newOnboardFixture(t)
	requireExit(t, g.onboard("--retarget"), exitAPIError)
	if len(g.ap.written()) != 0 {
		t.Fatal("retarget of an unknown AP wrote files")
	}
}

func TestOnboardPreflightRefusals(t *testing.T) {
	t.Run("read-only token", func(t *testing.T) {
		f := newOnboardFixture(t)
		f.controller.scope = api.TokenScopeRead
		requireExit(t, f.onboard("--site", "Home", "--name", "Hallway"), exitAPIError)
	})
	t.Run("DNS mode needs a pin", func(t *testing.T) {
		f := newOnboardFixture(t)
		f.controller.view.DNSMode = true
		result := f.onboard("--site", "Home", "--name", "Hallway")
		requireExit(t, result, exitPreflight)
		if !strings.Contains(result.err.Error(), "--pin-ip") {
			t.Fatal(result.err)
		}
	})
	t.Run("unknown site", func(t *testing.T) {
		f := newOnboardFixture(t)
		result := f.onboard("--site", "Lab", "--name", "Hallway")
		requireExit(t, result, exitPreflight)
		if !strings.Contains(result.err.Error(), `"Home" (site-home)`) {
			t.Fatal(result.err)
		}
	})
	t.Run("erased EEPROM", func(t *testing.T) {
		f := newOnboardFixture(t)
		f.ap.setFile("/sys/bus/i2c/devices/0-0052/eeprom", strings.Repeat("\xff", eepromSize))
		result := f.onboard("--site", "Home", "--name", "Hallway")
		requireExit(t, result, exitPreflight)
		if len(f.ap.written()) != 0 {
			t.Fatal("wrote files without a secret")
		}
		// The backup was taken first and is kept for the record.
		onlyBackup(t, f.backupDir, "02-aa-00-00-00-01")
	})
	t.Run("admin sign-in token", func(t *testing.T) {
		f := newOnboardFixture(t)
		if err := os.WriteFile(f.tokenFile, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
			t.Fatal(err)
		}
		result := f.onboard("--site", "Home", "--name", "Hallway")
		requireExit(t, result, exitPreflight)
		if !strings.Contains(result.err.Error(), "admin sign-in token") {
			t.Fatal(result.err)
		}
	})
	t.Run("plain http to a remote controller", func(t *testing.T) {
		if _, err := newControllerClient("http://dismist.example.net", testAPIToken); err == nil {
			t.Fatal("accepted plain http to a non-loopback controller")
		}
		if _, err := newControllerClient("https://dismist.example.net/api", testAPIToken); err == nil {
			t.Fatal("accepted a controller URL with a path")
		}
	})
}

func TestOnboardPinIPUsesTheSSHAddressAndSiteDefaultsToHome(t *testing.T) {
	f := newOnboardFixture(t)
	requireExit(t, f.onboard("--name", "Hallway", "--pin-ip", "--no-reboot"), exitDone)
	_, posts, _ := f.controller.snapshot()
	if posts[0].ManagementIP != "127.0.0.1" || posts[0].SiteID != "site-home" {
		t.Fatalf("pin %q site %q", posts[0].ManagementIP, posts[0].SiteID)
	}
}

func TestOnboardAsksBeforeRebootingAConnectedAP(t *testing.T) {
	f := newOnboardFixture(t)
	at := inspectNow
	f.controller.record = &api.APDetail{APView: api.APView{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "site-home", UpdatedRev: 3, Connected: true, ConnectedAt: &at, ConfigRevision: 42, AppliedRevision: 42}}
	result := f.onboard("--site", "Home", "--name", "Hallway")
	requireExit(t, result, exitDone)
	if f.ap.rebootCount() != 0 || !strings.Contains(result.stdout, "Reboot: skipped (not confirmed)") {
		t.Fatalf("rebooted a serving AP without asking:\n%s", result.stdout)
	}
	if len(f.asked) != 1 || f.asked[0] != onboardConfirmReboot {
		t.Fatalf("asked %v", f.asked)
	}
}

func TestOnboardTimesOutWaitingForTheAP(t *testing.T) {
	f := newOnboardFixture(t)
	f.controller.neverConnects = true
	result := f.onboard("--site", "Home", "--name", "Hallway", "--timeout", "200ms")
	requireExit(t, result, exitTimedOut)
	if !strings.Contains(result.err.Error(), "did not come back in sync within 200ms") {
		t.Fatal(result.err)
	}
}

func TestRestorePutsTheOriginalFilesBack(t *testing.T) {
	f := newOnboardFixture(t)
	f.ap.setFile(apEPControlRW+"/local_overrides", `{"LED":{"Brightness":10}}`)
	requireExit(t, f.onboard("--site", "Home", "--name", "Hallway", "--remove-local-files"), exitDone)
	backup := onlyBackup(t, f.backupDir, "02-aa-00-00-00-01")
	before := len(f.ap.written())

	args := append([]string{"restore"}, f.sshArgs()...)
	dry := f.run(append(args, "--backup", backup, "--dry-run")...)
	requireExit(t, dry, exitDone)
	if len(f.ap.written()) != before {
		t.Fatal("restore --dry-run changed the AP")
	}
	refused := f.run(append(args, "--backup", backup)...)
	requireExit(t, refused, exitPreflight)
	if len(f.ap.written()) != before {
		t.Fatal("unconfirmed restore changed the AP")
	}

	result := f.run(append(args, "--backup", backup, "--yes")...)
	requireExit(t, result, exitDone)
	if content, ok := f.ap.file(apEPControlRW + "/local_overrides"); !ok || content != `{"LED":{"Brightness":10}}` {
		t.Fatalf("local file not restored: %q", content)
	}
	for _, name := range []string{rwCloudINIPath, rwRootCertsPath} {
		if _, ok := f.ap.file(name); ok {
			t.Fatalf("%s still in use after restoring a stock AP", name)
		}
	}
	changes := f.ap.written()[before:]
	want := []string{apEPControlRW + "/local_overrides", restoreAsideRoot + "/20261114T050000Z/cloud.ini", restoreAsideRoot + "/20261114T050000Z/cacerts-production.pem"}
	if !slices.Equal(changes, want) {
		t.Fatalf("restore changes %v, want %v", changes, want)
	}
	if f.ap.rebootCount() != 2 {
		t.Fatalf("reboots %d", f.ap.rebootCount())
	}
	if again := f.run(append(args, "--backup", backup, "--yes")...); !strings.Contains(again.stdout, "Nothing to restore") || f.ap.rebootCount() != 2 {
		t.Fatalf("second restore:\n%s", again.stdout)
	}
}

func TestRestoreRefusesAnotherAPsBackupAndDamage(t *testing.T) {
	f := newOnboardFixture(t)
	requireExit(t, f.onboard("--site", "Home", "--name", "Hallway", "--no-reboot"), exitDone)
	backup := onlyBackup(t, f.backupDir, "02-aa-00-00-00-01")
	f.ap.setFile("/sys/class/net/eth0/address", "02:aa:00:00:00:99\n")
	args := append([]string{"restore"}, f.sshArgs()...)
	result := f.run(append(args, "--backup", backup, "--yes")...)
	requireExit(t, result, exitPreflight)
	if !strings.Contains(result.err.Error(), "but the backup is of AP 02-aa-00-00-00-01") {
		t.Fatal(result.err)
	}

	if err := os.WriteFile(filepath.Join(backup, backupTarName), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	result = f.run(append(args, "--backup", backup, "--yes")...)
	requireExit(t, result, exitPreflight)
	if !strings.Contains(result.err.Error(), "damaged") {
		t.Fatal(result.err)
	}
}

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{fmt.Errorf("plain"), 2},
		{exitf(exitTLSMismatch, "tls"), 3},
		{fmt.Errorf("wrapped: %w", withExit(exitAPIError, fmt.Errorf("api"))), 4},
		{withExit(exitTimedOut, withExit(exitAPIError, fmt.Errorf("first wins"))), 4},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestMergeCABundleKeepsTheBaseAndSkipsDuplicates(t *testing.T) {
	base := syntheticCABundle(t, "A", "B")
	extra, _ := parsePEMCertificates([]byte(syntheticCABundle(t, "C")))
	baseCerts, _ := parsePEMCertificates([]byte(base))
	merged, added, err := mergeCABundle([]byte(base), append([]*x509.Certificate{baseCerts[0]}, extra...))
	if err != nil || len(added) != 1 || !bytes.HasPrefix(merged, []byte(strings.TrimRight(base, "\n"))) {
		t.Fatalf("added %d err %v", len(added), err)
	}
	same, none, err := mergeCABundle([]byte(base), baseCerts)
	if err != nil || len(none) != 0 || string(same) != base {
		t.Fatal("a bundle that already holds every CA changed")
	}
}

func TestLetsEncryptRootsAreTheExpectedCertificates(t *testing.T) {
	roots, err := letsEncryptRoots()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ISRG Root X1": "96bcec06264976f37460779acf28c5a7cfe8a3c0aae11a8ffcee05c0bddf08c6",
		"ISRG Root X2": "69729b8e15a86efc177a57afb7171dfc64add28c2fca8cf1507e34453ccb1470",
		"Root YE":      "e14ffcad5b0025731006caa43a121a22d8e9700f4fb9cf852f02a708aa5d5666",
		"Root YR":      "e57b7e6f150c419102e8d5c055729ff967b9d1a829bf00cec89ca604ebf4a86f",
	}
	if len(roots) != len(want) {
		t.Fatalf("%d roots", len(roots))
	}
	for _, root := range roots {
		if got := sha256Hex(root.Raw); want[root.Subject.CommonName] != got {
			t.Errorf("%s fingerprint %s", root.Subject.CommonName, got)
		}
		if root.CheckSignatureFrom(root) != nil {
			t.Errorf("%s is not self-signed", root.Subject.CommonName)
		}
	}
}

func TestCloudINIEquivalentIgnoresFormatting(t *testing.T) {
	desired, err := rewriteCloudINI([]byte(syntheticStockCloudINI), "wss://ap.example.net/ws")
	if err != nil {
		t.Fatal(err)
	}
	if !cloudINIEquivalent([]byte(syntheticCustomCloudINI), []byte(desired)) {
		t.Fatal("a hand-written cloud.ini with the same entries is not equivalent")
	}
	other, _ := rewriteCloudINI([]byte(syntheticStockCloudINI), "wss://other.example.net/ws")
	if cloudINIEquivalent([]byte(syntheticCustomCloudINI), []byte(other)) {
		t.Fatal("a different endpoint is equivalent")
	}
}
