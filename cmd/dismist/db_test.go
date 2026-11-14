package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

const (
	dbSite    = "8e31882f-0000-4000-8000-0000000000aa"
	dbOther   = "8e31882f-0000-4000-8000-0000000000bb"
	dbAP      = "02-aa-00-00-00-37"
	dbLabAP   = "02-aa-00-00-00-40"
	dbNetwork = "6f2000aa-0000-4000-8000-000000000001"
)

func writeBackup(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.db")
	store, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{
			{ID: dbSite, Name: "Home", Country: "AU", Advanced: json.RawMessage(`{"Radios":[{"Band":"2.4","CCRev":"AU/890","CCRevSP":"AU/890","Channel":1,"Width":20,"Power":12,"RRM":{"Channel":1,"Width":20,"Power":12}},{"Band":"5","CCRev":"AU/890","CCRevSP":"AU/890","Channel":149,"Width":80,"Power":17,"RRM":{"Channel":149,"Width":80,"Power":17}}],"Monitor":{"CCRev":"AU/890"}}`)},
			{ID: dbOther, Name: "Office", Country: "AU"},
		}
		state.APs = []model.APRecord{{ID: dbAP, Name: "Hallway", SiteID: dbSite, Secret: bytes.Repeat([]byte{1}, 32), ManagementIP: "192.0.2.10",
			ConfigUpdatedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), LocalFiles: []model.LocalFile{}}}
		state.Networks = []model.Network{
			{ID: dbNetwork, SiteID: dbSite, SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true,
				Advanced: json.RawMessage(`{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}`)},
			{ID: "6f2000aa-0000-4000-8000-000000000002", SiteID: dbOther, SSID: "Office", PSK: "example-passphrase-02", VLANID: 1, Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.APStatus().SetVAPs(dbAP, time.Now(), []model.VAPRecord{{Dev: "wl0.1", WLANID: dbNetwork}, {Dev: "wl1.1", WLANID: dbNetwork, RadioIndex: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDBMigrateDryRun(t *testing.T) {
	backup := writeBackup(t)
	before, _ := os.ReadFile(backup)
	var out bytes.Buffer
	if err := runDB([]string{"migrate", "--dry-run", backup}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"dry run: PASS", "security wpa2_wpa3_personal, bssid_index 1 (measured)", "equal true", `rf_override {"2.4":{"channel":1},"5":{"channel":149}}`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "example-passphrase") {
		t.Fatal("report prints a passphrase")
	}
	after, _ := os.ReadFile(backup)
	if !bytes.Equal(before, after) {
		t.Fatal("dry run modified the backup")
	}
	out.Reset()
	if err := runDB([]string{"migrate", "--dry-run", "--json", backup}, &out); err != nil || !json.Valid(out.Bytes()) {
		t.Fatalf("json %v", err)
	}
	if err := runDB([]string{"migrate", backup}, &out); err == nil {
		t.Fatal("migrate without --dry-run accepted")
	}
}

// The CLI entry point routes `dismist db …` (design §4.7 rollout step 2 runs
// it on a production backup before the upgrade).
func TestRunDispatchesDB(t *testing.T) {
	backup := writeBackup(t)
	var out bytes.Buffer
	if err := run([]string{"db", "migrate", "--dry-run", backup}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "dry run: PASS") {
		t.Fatalf("report:\n%s", out.String())
	}
	if err := run([]string{"db"}, &out); err == nil || !strings.Contains(err.Error(), "dismist db migrate") {
		t.Fatalf("bare db: %v", err)
	}
	if err := run(nil, &out); err == nil || !strings.Contains(err.Error(), "dismist db") {
		t.Fatalf("usage does not mention db: %v", err)
	}
}

func TestDBRehearseCopy(t *testing.T) {
	backup := writeBackup(t)
	out := filepath.Join(t.TempDir(), "rehearsal.db")
	var log bytes.Buffer
	secret := strings.Repeat("ab", 32)
	if err := runDB([]string{"rehearse-copy", "--site", "Home", "--out", out, "--ap", dbAP + "=" + dbLabAP, "--secret", secret, backup}, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	store, err := model.OpenBolt(out, "")
	if err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if len(state.Sites) != 1 || state.Sites[0].ID != dbSite || len(state.Networks) != 1 || state.Networks[0].SSID != "Example-LAB" {
		t.Fatalf("copy %+v", state)
	}
	if len(state.APs) != 1 || state.APs[0].ID != dbLabAP || state.APs[0].ManagementIP != "" || state.APs[0].Secret[0] != 0xab {
		t.Fatalf("AP %+v", state.APs[0])
	}
	if len(state.Sites[0].Advanced) == 0 || len(state.Networks[0].Advanced) == 0 {
		t.Fatal("legacy patches lost")
	}
	status, err := store.APStatus().Get(dbLabAP)
	if err != nil || len(status.VAPs) != 2 {
		t.Fatalf("status moved %+v %v", status, err)
	}
	_ = store.Close()
	// The copy migrates like production would.
	var report bytes.Buffer
	if err := runDB([]string{"migrate", "--dry-run", out}, &report); err != nil {
		t.Fatalf("rehearsal copy does not migrate: %v\n%s", err, report.String())
	}
	if err := runDB([]string{"rehearse-copy", "--site", "Nope", "--out", filepath.Join(t.TempDir(), "x.db"), backup}, &log); err == nil {
		t.Fatal("unknown site accepted")
	}
}
