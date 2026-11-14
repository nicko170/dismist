package compile

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
)

var historyKey = []byte("0123456789abcdef0123456789abcdef")

func snapshotRecord(state model.State, revision uint64) model.ChangeRecord {
	return NewChangeRecord(state, historyKey, ChangeOptions{Revision: revision, At: typedTime, SiteID: typedSite, Summary: "test",
		Actor: model.Actor{Kind: model.ActorSession}})
}

func TestSnapshotRedactsWithKeyedDigests(t *testing.T) {
	state := newTypedState()
	state.Networks[0].Expert = json.RawMessage(`{"Auth_servers":[{"Secret":"radius-secret"}]}`)
	state.Sites[0].Roaming.KeyEncryptKey = "aa"
	record := snapshotRecord(state, 7)
	raw, _ := json.Marshal(record)
	for _, secret := range []string{"example-passphrase-01", "radius-secret", `"aa"`} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("snapshot leaks %s", secret)
		}
	}
	psk := record.Snapshot.Networks[0].PSK
	if !IsDigest(psk) || psk != SecretDigest(historyKey, "networks/"+typedNet+"/psk", "example-passphrase-01") {
		t.Fatalf("digest %q", psk)
	}
	if other := SecretDigest([]byte("another key 0123456789abcdef0123"), "networks/"+typedNet+"/psk", "example-passphrase-01"); other == psk {
		t.Fatal("digest is not keyed")
	}
	if len(record.Snapshot.APs) != 1 || record.Snapshot.APs[0].Secret != nil {
		t.Fatal("AP secret in snapshot")
	}
}

func TestRestoreSecretsAndObjects(t *testing.T) {
	state := newTypedState()
	r := snapshotRecord(state, 7)

	// Unchanged passphrase: kept; typed fields go back.
	changed := newTypedState()
	changed.Networks[0].Hidden = true
	changed.Sites[0].APDefaults.LEDBrightness = 3
	if _, err := Restore(&changed, r, historyKey, RestoreOptions{Now: typedTime}); err != nil {
		t.Fatal(err)
	}
	if changed.Networks[0].Hidden || changed.Sites[0].APDefaults.LEDBrightness != 255 || changed.Networks[0].PSK != "example-passphrase-01" {
		t.Fatalf("restore %+v", changed.Networks[0])
	}

	// Different passphrase today: secret_required unless entered or kept.
	differs := newTypedState()
	differs.Networks[0].PSK = "another-passphrase-01"
	_, err := Restore(&differs, r, historyKey, RestoreOptions{Now: typedTime})
	var required *SecretRequiredError
	if !errors.As(err, &required) || len(required.Fields) != 1 || required.Fields[0].Path != "networks/"+typedNet+"/psk" {
		t.Fatalf("want secret_required, got %v", err)
	}
	if _, err := Restore(&differs, r, historyKey, RestoreOptions{Now: typedTime, Secrets: map[string]string{"networks/" + typedNet + "/psk": "wrong-passphrase"}}); !errors.As(err, &required) {
		t.Fatalf("a wrong old value must be refused: %v", err)
	}
	ok := newTypedState()
	ok.Networks[0].PSK = "another-passphrase-01"
	if _, err := Restore(&ok, r, historyKey, RestoreOptions{Now: typedTime, Secrets: map[string]string{"networks/" + typedNet + "/psk": "example-passphrase-01"}}); err != nil || ok.Networks[0].PSK != "example-passphrase-01" {
		t.Fatalf("entered secret %v %q", err, ok.Networks[0].PSK)
	}
	keep := newTypedState()
	keep.Networks[0].PSK = "another-passphrase-01"
	if _, err := Restore(&keep, r, historyKey, RestoreOptions{Now: typedTime, KeepCurrent: map[string]bool{"networks/" + typedNet + "/psk": true}}); err != nil || keep.Networks[0].PSK != "another-passphrase-01" {
		t.Fatalf("keep current %v", err)
	}

	// Deleted since R: re-created (secret needed); created since R:
	// deleted unless kept.
	later := newTypedState()
	later.Networks[0].ID = "6f2000aa-0000-4000-8000-0000000000ff"
	result, err := Restore(&later, r, historyKey, RestoreOptions{Now: typedTime, Secrets: map[string]string{"networks/" + typedNet + "/psk": "example-passphrase-01"}})
	if err != nil || len(result.Recreated) != 1 || len(result.Deleted) != 1 || len(later.Networks) != 1 || later.Networks[0].ID != typedNet {
		t.Fatalf("objects %+v %v %+v", result, err, later.Networks)
	}
	kept := newTypedState()
	kept.Networks[0].ID = "6f2000aa-0000-4000-8000-0000000000ff"
	result, err = Restore(&kept, r, historyKey, RestoreOptions{Now: typedTime, KeepNewWLANs: true, Secrets: map[string]string{"networks/" + typedNet + "/psk": "example-passphrase-01"}})
	if err != nil || len(kept.Networks) != 2 {
		t.Fatalf("keep new WLANs %+v %v", result, err)
	}
	// The re-created network's index was taken by the kept one.
	for _, warning := range result.Warnings {
		if warning.Code == "bssid_changed" {
			return
		}
	}
	t.Fatalf("missing BSSID warning: %+v", result.Warnings)
}

func TestRestoreAPOverridesNotIdentity(t *testing.T) {
	state := newTypedState()
	brightness := 40
	state.APs[0].LEDBrightness = &brightness
	r := snapshotRecord(state, 7)
	now := newTypedState()
	now.APs[0].Name = "Renamed"
	now.APs[0].ManagementIP = "192.0.2.5"
	now.APs[0].Features = map[string]bool{"pong2": true}
	if _, err := Restore(&now, r, historyKey, RestoreOptions{Now: typedTime}); err != nil {
		t.Fatal(err)
	}
	ap := now.APs[0]
	if ap.LEDBrightness == nil || *ap.LEDBrightness != 40 || ap.Name != "Renamed" || ap.ManagementIP != "192.0.2.5" || !ap.Features["pong2"] {
		t.Fatalf("AP restore %+v", ap)
	}
	// Restoring a digest placeholder in an expert patch needs the value.
	state.APs[0].Expert = json.RawMessage(`{"Cloud":{"Proxy":{"URL":"https://example.test"}},"WiredSecret":{"Secret":"x-secret"}}`)
	r = snapshotRecord(state, 8)
	again := newTypedState()
	_, err := Restore(&again, r, historyKey, RestoreOptions{Now: typedTime})
	var required *SecretRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("expert digest needs the secret: %v", err)
	}
	if strings.Contains(string(again.APs[0].Expert), DigestPrefix) {
		t.Fatal("restored patch contains a digest")
	}
}

func TestRestoreReplacesInvalidPlan(t *testing.T) {
	state := newTypedState()
	r := snapshotRecord(state, 7)
	now := newTypedState()
	now.Sites[0].RF.Band5.Width = 40
	// R's profile comes back (80 MHz), so the 80 MHz plan stays valid.
	if _, err := Restore(&now, r, historyKey, RestoreOptions{Now: typedTime}); err != nil {
		t.Fatal(err)
	}
	if now.APs[0].RFPlan["5"].Width != 80 {
		t.Fatalf("plan %+v", now.APs[0].RFPlan["5"])
	}
}

func TestRecommendations(t *testing.T) {
	state := newTypedState()
	state.Sites[0].RF.Managed = false
	state.Sites[0].APDefaults.PoEConstrained = model.PoEDisableRadios
	state.Sites[0].APDefaults.HostnameFromName = false
	state.Sites[0].Expert = json.RawMessage(`{"Monitor":{"CCRev":""},"Radios":[{"Band":"5","QOS":{"Dscp2Ac":null}}]}`)
	state.Networks[0].BandSteering = true
	recs := Recommendations(state, typedSite)
	kinds := map[string]Recommendation{}
	for _, rec := range recs {
		kinds[rec.Kind] = rec
	}
	for _, kind := range []string{RecommendManageRF, RecommendScanRadioCCRev, RecommendTemplateQOS, RecommendPoEKeepRadios, RecommendDHCPHostname, RecommendBandSteeringOff} {
		if _, ok := kinds[kind]; !ok {
			t.Fatalf("missing %s in %+v", kind, recs)
		}
	}
	if err := ApplyRecommendation(&state, typedSite, kinds[RecommendScanRadioCCRev].ID, typedTime); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state.Sites[0].Expert), "CCRev") {
		t.Fatalf("CCRev kept: %s", state.Sites[0].Expert)
	}
	if err := ApplyRecommendation(&state, typedSite, kinds[RecommendTemplateQOS].ID, typedTime); err != nil || len(state.Sites[0].Expert) != 0 {
		t.Fatalf("QOS residual %s %v", state.Sites[0].Expert, err)
	}
	if err := ApplyRecommendation(&state, typedSite, kinds[RecommendManageRF].ID, typedTime); err != nil || !state.Sites[0].RF.Managed {
		t.Fatal("manage RF")
	}
	if err := ApplyRecommendation(&state, typedSite, "nope:1", typedTime); !errors.Is(err, ErrUnknownRecommendation) {
		t.Fatal("unknown id")
	}
	// The result compiles.
	input, _ := InputFor(legacyv0.BundledTemplate(), state, typedAP)
	input.Now = time.Time{}
	if _, err := AP(input); err != nil {
		t.Fatal(err)
	}
}
