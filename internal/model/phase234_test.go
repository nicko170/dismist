package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConfigHistoryAppendListAndResults(t *testing.T) {
	store, _ := openTestStore(t)
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for revision := uint64(1); revision <= 5; revision++ {
		if err := store.UpdateTx(func(state *State, tx *Tx) error {
			_, err := tx.ConfigHistory().Append(ChangeRecord{Revision: state.Revision + 1, At: at, Summary: "change", Scope: ChangeScope{SiteID: "site-a"},
				Effective: map[string]EffectiveRef{"02-aa-00-00-00-01": {Hash: "e2:00", Revision: state.Revision + 1}}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// An action at the current revision does not replace the config record.
	if _, err := store.ConfigHistory().Append(ChangeRecord{Revision: 5, Kind: ChangeAction, Summary: "backup downloaded"}); err != nil {
		t.Fatal(err)
	}
	record, found, err := store.ConfigHistory().Get(5)
	if err != nil || !found || record.Kind != ChangeConfig {
		t.Fatalf("get %+v %v %v", record, found, err)
	}
	if err := store.ConfigHistory().SetResult(5, "02-aa-00-00-00-01", ApplyResult{State: "applied", At: at}); err != nil {
		t.Fatal(err)
	}
	record, _, _ = store.ConfigHistory().Get(5)
	if record.Results["02-aa-00-00-00-01"].State != "applied" {
		t.Fatalf("result %+v", record.Results)
	}
	page, next, err := store.ConfigHistory().List(HistoryQuery{SiteID: "site-a", Limit: 3})
	if err != nil || len(page) != 3 || page[0].Revision != 5 || next == 0 {
		t.Fatalf("page %+v next %d %v", page, next, err)
	}
	rest, end, err := store.ConfigHistory().List(HistoryQuery{SiteID: "site-a", Before: next, Limit: 10})
	if err != nil || end != 0 || len(page)+len(rest) != 6 {
		t.Fatalf("rest %+v %d %v", rest, end, err)
	}
	byAP, _, _ := store.ConfigHistory().List(HistoryQuery{APID: "02-aa-00-00-00-01"})
	if len(byAP) != 5 {
		t.Fatalf("by AP %d", len(byAP))
	}
	other, _, _ := store.ConfigHistory().List(HistoryQuery{SiteID: "site-b"})
	if len(other) != 1 { // the unscoped action
		t.Fatalf("other site %d", len(other))
	}
	// Retention keeps the newest revisions.
	policy := DefaultRetention()
	policy.Samples, policy.SamplesHourly, policy.Sessions, policy.Events, policy.ConfigHistory = 0, 0, 0, 0, 3
	if _, err := store.Prune(context.Background(), policy, at); err != nil {
		t.Fatal(err)
	}
	all, _, _ := store.ConfigHistory().List(HistoryQuery{Limit: 500})
	if len(all) != 3 || all[0].Revision != 5 {
		t.Fatalf("retained %+v", all)
	}
}

func TestScansStoreTrimsAndTagsBands(t *testing.T) {
	store, _ := openTestStore(t)
	var neighbours []ScanNeighbour
	for index := 0; index < 250; index++ {
		neighbours = append(neighbours, ScanNeighbour{BSSID: "02:BB:00:00:00:01", SSID: strings.Repeat("x", 30), Channel: 6, RSSI: -90 + index%60})
	}
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	if err := store.Scans().Put("site-a", "ap-1", "2.4", BandScan{At: at, Neighbours: neighbours}); err != nil {
		t.Fatal(err)
	}
	if err := store.Scans().Put("site-a", "ap-1", "5", BandScan{At: at, Channels: []ChannelStat{{Channel: 36, Util: 0.2, NoiseDBm: -95}}}); err != nil {
		t.Fatal(err)
	}
	scan, found, err := store.Scans().Get("ap-1")
	if err != nil || !found || len(scan.Bands) != 2 {
		t.Fatalf("scan %+v %v", scan, err)
	}
	got := scan.Bands["2.4"].Neighbours
	if len(got) > MaxScanNeighbours || got[0].RSSI < got[len(got)-1].RSSI || got[0].BSSID != "02:bb:00:00:00:01" {
		t.Fatalf("trim %d first %+v", len(got), got[0])
	}
	raw, _ := json.Marshal(scan)
	if len(raw) > MaxScanBytes {
		t.Fatalf("record %d bytes", len(raw))
	}
	site, _ := store.Scans().Site("site-a")
	if len(site) != 1 {
		t.Fatalf("site scans %d", len(site))
	}
	if err := store.Scans().Put("site-a", "ap-1", "6", BandScan{}); err == nil {
		t.Fatal("band 6 accepted")
	}
}

func TestHourlyRollups(t *testing.T) {
	store, _ := openTestStore(t)
	seedSite(t, store)
	apID := store.Snapshot().APs[0].ID
	siteID := store.Snapshot().APs[0].SiteID
	start := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for minute := 0; minute < 150; minute++ {
		at := start.Add(time.Duration(minute) * time.Minute)
		if err := store.RecordObservation(Observation{SiteID: siteID, APID: apID, At: at, Connected: true,
			Radios: []RadioSample{{Band: "5", Channel: 149, Width: 80, UtilAll: float64(minute%10) / 10, Clients: minute % 5}}}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.RollupSamples(context.Background(), start.Add(150*time.Minute), 1)
	if err != nil || result.Hours != 2 || result.Transactions < 2 {
		t.Fatalf("rollup %+v %v", result, err)
	}
	hours, err := store.HourlySamples(HourlyQuery{SiteID: siteID})
	if err != nil || len(hours) != 2 || hours[0].Minutes != 60 || hours[0].Radios[0].UtilMax != 0.9 || hours[0].Radios[0].ClientsMax != 4 {
		t.Fatalf("hours %+v %v", hours, err)
	}
	// Running again adds nothing; the next hour rolls up once complete.
	if again, _ := store.RollupSamples(context.Background(), start.Add(150*time.Minute), 0); again.Hours != 0 {
		t.Fatalf("rolled twice: %+v", again)
	}
	if later, _ := store.RollupSamples(context.Background(), start.Add(4*time.Hour), 0); later.Hours != 1 {
		t.Fatalf("third hour: %+v", later)
	}
}

func TestClientMeta(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.ClientMeta().Put("02-AA-00-00-00-01", ClientMeta{Alias: "TV", Notes: "lounge"}); err != nil {
		t.Fatal(err)
	}
	meta, found, err := store.ClientMeta().Get("02:aa:00:00:00:01")
	if err != nil || !found || meta.Alias != "TV" {
		t.Fatalf("meta %+v %v", meta, err)
	}
	if err := store.ClientMeta().Put("02:aa:00:00:00:01", ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := store.ClientMeta().Get("02:aa:00:00:00:01"); found {
		t.Fatal("empty meta kept")
	}
	if err := store.ClientMeta().Put("x", ClientMeta{Alias: strings.Repeat("a", 65)}); err == nil {
		t.Fatal("long alias accepted")
	}
}

func TestTypedRecordsRoundTripAndPSKRules(t *testing.T) {
	store, _ := openTestStore(t)
	seedSite(t, store)
	brightness := 10
	if err := store.Update(func(state *State) error {
		state.Sites[0].RF = RFProfile{Managed: true, Band5: BandProfile{Enabled: true, Width: 80, Channels: []int{36}}}
		state.APs[0].LEDBrightness = &brightness
		state.APs[0].RF = map[string]*RadioOverride{"5": {Channel: 149}}
		state.Networks[0].Security = SecurityWPA2Personal
		state.Networks[0].PSK = strings.Repeat("a", 64)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	snapshot.APs[0].RF["5"].Channel = 1 // a snapshot is a deep copy
	again := store.Snapshot()
	if again.APs[0].RF["5"].Channel != 149 || *again.APs[0].LEDBrightness != 10 || !again.Sites[0].RF.Managed {
		t.Fatalf("typed fields %+v", again.APs[0])
	}
	if err := store.Update(func(state *State) error {
		state.Networks[0].Security = SecurityWPA3Personal // 64-hex is not a SAE passphrase
		return nil
	}); err == nil {
		t.Fatal("64-hex PSK accepted for WPA3")
	}
	if err := store.Update(func(state *State) error {
		state.Networks[0].Security, state.Networks[0].PSK = SecurityOpen, ""
		return nil
	}); err != nil {
		t.Fatalf("open network without PSK refused: %v", err)
	}
}
