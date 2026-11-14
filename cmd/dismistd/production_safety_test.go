package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/tools/fakeap"
)

// The v0.3.0 production fixture (testdata/v030-production) was written by
// the v0.3.0 binary itself, through its API, with the production AP's
// exact shape (tools/migrationcompat/prodsafety.sh with FIXTURE_OUT): site
// Home (AU), WLAN "Example" on VLAN 1 with the WPA2/WPA3 network patch,
// the site Radios[] replacement plus Monitor.CCRev AU/890, and an AP with
// a secret and a source pin. served-v0.3.0.json is the config v0.3.0
// served that AP. Every value is synthetic.
type v030Fixture struct {
	SiteID         string `json:"site_id"`
	NetworkID      string `json:"network_id"`
	APID           string `json:"ap_id"`
	APSecret       string `json:"ap_secret"`
	ConfigRevision uint64 `json:"config_revision"`
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// legacyFields reads the legacy advanced patches the v0.3.0 binary compiles
// (a rollback by image tag must find them byte-identical).
func legacyFields(t *testing.T, path string) map[string]string {
	t.Helper()
	store, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := store.Snapshot()
	result := map[string]string{}
	for _, site := range state.Sites {
		result["site/"+site.ID] = string(site.Advanced)
	}
	for _, network := range state.Networks {
		result["network/"+network.ID] = string(network.Advanced) + "|" + network.SSID + "|" + network.PSK
	}
	for _, ap := range state.APs {
		result["ap/"+ap.ID] = string(ap.Advanced) + "|" + ap.ManagementIP
	}
	return result
}

// TestProductionDatabaseFromV030 starts this build's dismistd on the
// database v0.3.0 wrote for the production AP and proves the upgrade does
// not change what the AP runs (design §4.5, §4.7): the schema-2 migration
// commits with compat 1, the AP is served a config with the same
// normalised effective config at the same revision, the legacy fields stay
// byte-identical, and the typed settings show the lifted WPA2/WPA3 and RF
// values.
func TestProductionDatabaseFromV030(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the daemon")
	}
	fixtureDir := filepath.Join("testdata", "v030-production")
	var fixture v030Fixture
	raw, err := os.ReadFile(filepath.Join(fixtureDir, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	served, err := os.ReadFile(filepath.Join(fixtureDir, "served-v0.3.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	data := filepath.Join(work, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(data, "controller.db")
	copyFile(t, filepath.Join(fixtureDir, "controller.db"), dbPath)
	copyFile(t, filepath.Join(fixtureDir, "admin.token"), filepath.Join(data, "admin.token"))
	legacyBefore := legacyFields(t, dbPath)
	certPath, keyPath := writeTestTLS(t, data)

	binary := filepath.Join(work, "dismistd")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	daemon := exec.Command(binary,
		"-db", dbPath, "-admin-token-file", filepath.Join(data, "admin.token"),
		"-tls-cert", certPath, "-tls-key", keyPath, "-tls-check-interval", "0",
		"-ui-listen", "127.0.0.1:0", "-ws-listen", "127.0.0.1:0", "-dns-listen", "127.0.0.1:0",
		"-metrics-listen", "", "-controller-ip", "127.0.0.1", "-upstream-dns", "127.0.0.1:9", "-backup-interval", "0",
	)
	daemon.Dir = work
	stderr, err := daemon.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = daemon.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = daemon.Process.Kill()
			<-exited
		}
	}
	t.Cleanup(stop)
	uiRE, wssRE := regexp.MustCompile(`UI API listening on (127\.0\.0\.1:\d+)`), regexp.MustCompile(`AP WSS listening on (127\.0\.0\.1:\d+)`)
	uiAddr, wssAddr := make(chan string, 1), make(chan string, 1)
	logs := &lockedLog{}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			logs.add(line)
			if match := uiRE.FindStringSubmatch(line); match != nil {
				uiAddr <- match[1]
			}
			if match := wssRE.FindStringSubmatch(line); match != nil {
				wssAddr <- match[1]
			}
		}
		exited <- daemon.Wait()
	}()
	var base, wss string
	for base == "" || wss == "" {
		select {
		case addr := <-uiAddr:
			base = "http://" + addr
		case addr := <-wssAddr:
			wss = "wss://" + addr + "/ws"
		case err := <-exited:
			t.Fatalf("dismistd exited: %v\n%s", err, logs.String())
		case <-time.After(60 * time.Second):
			t.Fatalf("dismistd did not open its listeners\n%s", logs.String())
		}
	}
	if !strings.Contains(logs.String(), "database migration committed") {
		t.Fatalf("the schema-2 migration did not commit\n%s", logs.String())
	}

	// What the AP is served now.
	secretBytes, err := hex.DecodeString(fixture.APSecret)
	if err != nil || len(secretBytes) != 32 {
		t.Fatalf("fixture secret: %v", err)
	}
	var secret [32]byte
	copy(secret[:], secretBytes)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var config []byte
	for attempt := 0; ; attempt++ {
		ap, err := fakeap.Connect(ctx, fakeap.Config{URL: wss, ID: fixture.APID, Secret: secret,
			TLS: &tls.Config{InsecureSkipVerify: true}}) //nolint:gosec // test controller
		if err == nil {
			config, err = ap.FetchConfig(ctx, fakeap.ConfigRequest{})
			_ = ap.Close()
		}
		if err == nil {
			break
		}
		if attempt > 40 {
			t.Fatalf("fake AP could not fetch its config: %v\n%s", err, logs.String())
		}
		time.Sleep(250 * time.Millisecond)
	}
	normalise := func(name string, raw []byte) []byte {
		t.Helper()
		canonical, report, err := effective.Normalise(bytes.TrimSpace(raw), effective.Context{LocalFiles: effective.LocalFilesUnknown})
		if err != nil || len(report.TypeErrors) > 0 {
			t.Fatalf("%s: %v %v", name, err, report.TypeErrors)
		}
		return canonical
	}
	if before, after := normalise("v0.3.0", served), normalise("migrated", config); !bytes.Equal(before, after) {
		t.Fatalf("the AP's normalised effective config changed:\nbefore %s\nafter  %s", before, after)
	}
	var servedConfig, migratedConfig struct {
		Timestamp float64
	}
	_ = json.Unmarshal(served, &servedConfig)
	_ = json.Unmarshal(config, &migratedConfig)
	if servedConfig.Timestamp == 0 || servedConfig.Timestamp != migratedConfig.Timestamp {
		t.Fatalf("config Timestamp moved: %v -> %v", servedConfig.Timestamp, migratedConfig.Timestamp)
	}

	// The API: migration state, revision and the typed lift.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 10 * time.Second, Jar: jar}
	token, err := os.ReadFile(filepath.Join(data, "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	login, err := client.Post(base+"/auth/login", "application/json", strings.NewReader(`{"token":"`+strings.TrimSpace(string(token))+`"}`))
	if err != nil || login.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", login, err)
	}
	login.Body.Close()
	get := func(path string, out any) {
		t.Helper()
		response, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK || json.Unmarshal(body, out) != nil {
			t.Fatalf("GET %s: %d %s", path, response.StatusCode, body)
		}
	}
	var migration api.MigrationStatus
	get("/api/system/migration", &migration)
	if migration.State != api.MigrationStateCommitted || migration.Schema != 2 || migration.Compat != 1 {
		t.Fatalf("migration %+v", migration)
	}
	var ap api.APDetail
	get("/api/aps/"+fixture.APID, &ap)
	if ap.ConfigRevision != fixture.ConfigRevision {
		t.Fatalf("the AP's config revision moved: %d -> %d", fixture.ConfigRevision, ap.ConfigRevision)
	}
	if ap.PinnedIP != "127.0.0.1" || ap.Overrides.RF["5"] == nil || ap.Overrides.RF["5"].Channel == nil || *ap.Overrides.RF["5"].Channel != 149 ||
		ap.Overrides.RF["2.4"] == nil || ap.Overrides.RF["2.4"].Channel == nil || *ap.Overrides.RF["2.4"].Channel != 1 {
		t.Fatalf("AP lift: pin %q overrides %+v", ap.PinnedIP, ap.Overrides.RF)
	}
	var wlan api.WLANView
	get("/api/sites/"+fixture.SiteID+"/networks/"+fixture.NetworkID, &wlan)
	if wlan.SSID != "Example" || wlan.Security != api.SecurityWPA2WPA3Personal || wlan.VLANID != 1 || wlan.Expert.HasPatch ||
		(wlan.PMF != nil && *wlan.PMF != "optional") {
		t.Fatalf("WLAN lift %+v", wlan)
	}
	var rf api.RFProfileView
	get("/api/sites/"+fixture.SiteID+"/rf", &rf)
	if !rf.Managed || rf.Band5.Width != 80 || rf.Band24.Width != 20 || rf.Band5.Power != (api.PowerPolicy{Mode: "fixed", DBm: 17}) ||
		rf.Band24.Power != (api.PowerPolicy{Mode: "fixed", DBm: 12}) {
		t.Fatalf("RF lift %+v", rf)
	}
	for _, valid := range append(rf.Band5.ValidChannels, rf.Band24.ValidChannels...) {
		if valid.Width == 160 {
			t.Fatal("AU/890 on the AP33 has no 160 MHz chanspec; the RF profile must not offer it")
		}
	}

	// Rollback by image tag: the legacy fields are byte-identical.
	stop()
	legacyAfter := legacyFields(t, dbPath)
	for key, before := range legacyBefore {
		if legacyAfter[key] != before {
			t.Errorf("legacy field %s changed:\n%s\n%s", key, before, legacyAfter[key])
		}
	}
}
