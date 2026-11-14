package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/api"
)

// TestLiveSiteStaysUnchangedThroughTheV1API is the production-safety
// scenario from the API side: the Home site carries the live AP's two
// advanced patches (a full Radios[] replacement with AU/890 and the
// WPA2/WPA3 network patch). Before the schema-2 migration commits, no
// v1 operation may change what the AP runs: dismist-only edits keep every
// ConfigRevision and the effective hash, typed edits are refused with
// migration_pending, dry runs store nothing, and the legacy advanced
// fields stay byte-identical (design §4.1 rule 2, §4.7, §8.8).
func TestLiveSiteStaysUnchangedThroughTheV1API(t *testing.T) {
	f := newV1Fixture(t)
	beforeHash := f.hash(t, homeAP)
	beforeRecords := f.rawRecords(t)
	revision := f.store.Snapshot().Revision

	// Dismist-only edits: site name and timezone, pin, protocol features,
	// another AP's name. (An AP's own name is its config's Name.)
	edits := []struct{ method, path, body, kind, id string }{
		{http.MethodPatch, "/api/sites/home", `{"name":"Home sweet home","timezone":"Australia/Melbourne"}`, "site", "home"},
		{http.MethodPatch, "/api/aps/" + homeAP, `{"management_ip":"192.0.2.10"}`, "ap", homeAP},
		{http.MethodPatch, "/api/aps/" + homeAP, `{"protocol_features":{"pong2":false}}`, "ap", homeAP},
		{http.MethodPatch, "/api/aps/" + otherAP, `{"name":"Bench 2"}`, "ap", otherAP},
	}
	for _, edit := range edits {
		response := f.call(t, edit.method, edit.path, edit.body, ifMatchHeader(f.updatedRev(edit.kind, edit.id))...)
		out := decodeInto[api.MutationResponse[map[string]any]](t, response, http.StatusOK)
		for _, ap := range out.Impact.APs {
			if ap.ID == homeAP {
				t.Fatalf("%s %s changed the live AP's config: %+v", edit.method, edit.path, out.Impact)
			}
		}
		if edit.id != otherAP && out.Impact.Summary != "No AP changes" {
			t.Fatalf("%s %s: %+v", edit.method, edit.path, out.Impact)
		}
	}
	if got := f.hash(t, homeAP); got != beforeHash {
		t.Fatalf("effective hash moved: %s -> %s", beforeHash, got)
	}
	after := f.rawRecords(t)
	for key, value := range beforeRecords {
		if key == "ap/"+otherAP+"/rev" {
			continue
		}
		if after[key] != value {
			t.Fatalf("%s changed: %q -> %q", key, value, after[key])
		}
	}

	// Typed edits are refused while the migration has not committed.
	typed := []struct{ method, path, body string }{
		{http.MethodPatch, "/api/sites/home/networks/net-home", `{"security":"wpa3_personal"}`},
		{http.MethodPatch, "/api/sites/home/networks/net-home", `{"hidden":true}`},
		{http.MethodPatch, "/api/sites/home", `{"rf":{"band_5":{"width":40}}}`},
		{http.MethodPatch, "/api/sites/home", `{"ap_defaults":{"led_brightness":10}}`},
		{http.MethodPut, "/api/sites/home/rf", `{"band_5":{"width":40}}`},
		{http.MethodPatch, "/api/aps/" + homeAP, `{"led_brightness":0}`},
		{http.MethodPatch, "/api/aps/" + homeAP, `{"rf":{"5":{"channel":36}}}`},
	}
	for _, edit := range typed {
		headers := []string{}
		if strings.HasPrefix(edit.path, "/api/aps/") {
			headers = ifMatchHeader(f.updatedRev("ap", homeAP))
		}
		errorOf(t, f.call(t, edit.method, edit.path, edit.body, headers...), http.StatusServiceUnavailable, api.ErrorCodeMigrationPending)
	}

	// A dry run of a disruptive change shows the impact and stores nothing.
	before := f.store.Snapshot().Revision
	dry := decodeInto[api.MutationResponse[api.WLANView]](t,
		f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home?dry_run=1", `{"psk":"another-passphrase-9"}`), http.StatusOK)
	if !dry.DryRun || len(dry.Impact.APs) != 1 || dry.Impact.APs[0].ID != homeAP || dry.Impact.Disruption != api.DisruptionWLANRestart {
		t.Fatalf("dry-run impact: %+v", dry.Impact)
	}
	foundSecret := false
	for _, entry := range dry.Impact.APs[0].Diff {
		if strings.HasSuffix(entry.Path, "Auth.PSK") {
			foundSecret = entry.From == "[changed]" && entry.To == "[changed]"
		}
		if strings.Contains(entry.Path, "passphrase") {
			t.Fatalf("diff names a secret: %+v", entry)
		}
	}
	if !foundSecret || strings.Contains(f.call(t, http.MethodGet, "/api/sites/home/networks", "").Body.String(), "another-passphrase-9") {
		t.Fatalf("dry-run diff did not redact the passphrase: %+v", dry.Impact.APs[0].Diff)
	}
	if !strings.Contains(dry.Impact.Summary, "WLAN Example restarts on 1 AP") {
		t.Fatalf("impact summary: %q", dry.Impact.Summary)
	}
	if f.store.Snapshot().Revision != before || f.hash(t, homeAP) != beforeHash {
		t.Fatal("dry run stored the change")
	}
	if f.store.Snapshot().Revision != revision+uint64(len(edits)) {
		t.Fatalf("revision %d, want %d", f.store.Snapshot().Revision, revision+uint64(len(edits)))
	}

	// The WLAN view shows the live security lifted from the compiled config.
	wlans := decodeInto[[]api.WLANView](t, f.call(t, http.MethodGet, "/api/sites/home/networks", ""), http.StatusOK)
	if len(wlans) != 1 || wlans[0].Security != api.SecurityWPA2WPA3Personal || !wlans[0].HasPSK || wlans[0].PMF != nil {
		t.Fatalf("lifted WLAN view: %+v", wlans)
	}
	if !wlans[0].Expert.HasPatch || wlans[0].Expert.Field != "advanced" {
		t.Fatalf("legacy patch not reported: %+v", wlans[0].Expert)
	}

	// The RF view offers no 160 MHz under AU/890 (live finding) and the
	// AP33 80 MHz blocks.
	rf := decodeInto[api.RFProfileView](t, f.call(t, http.MethodGet, "/api/sites/home/rf", ""), http.StatusOK)
	if rf.CCRev == nil || *rf.CCRev != "AU/890" {
		t.Fatalf("ccrev: %+v", rf.CCRev)
	}
	widths := map[int]api.ValidChannels{}
	for _, entry := range rf.Band5.ValidChannels {
		widths[entry.Width] = entry
	}
	if _, offered := widths[160]; offered || len(widths) != 3 {
		t.Fatalf("5 GHz widths offered: %+v", rf.Band5.ValidChannels)
	}
	if blocks := widths[80].Blocks; len(blocks) != 5 || blocks[0][0] != 36 || blocks[4][0] != 149 {
		t.Fatalf("80 MHz blocks: %+v", blocks)
	}
	if len(rf.Band24.ValidChannels) != 1 || len(rf.Band24.ValidChannels[0].Primaries) != 13 {
		t.Fatalf("2.4 GHz channels: %+v", rf.Band24.ValidChannels)
	}
}

// TestLegacyModeStillEditsTheLegacyFields covers the edits legacy mode
// keeps (design §4.7 step 7): SSID, passphrase, enable, SSH keys and the
// expert editors, all writing the legacy fields.
func TestLegacyModeStillEditsTheLegacyFields(t *testing.T) {
	f := newV1Fixture(t)
	response := f.call(t, http.MethodPatch, "/api/sites/home/networks/net-home", `{"ssid":"Example-2","psk":"example-passphrase-77"}`)
	out := decodeInto[api.MutationResponse[api.WLANView]](t, response, http.StatusOK)
	if out.Object.SSID != "Example-2" || len(out.Impact.APs) != 1 {
		t.Fatalf("legacy WLAN edit: %+v", out)
	}
	if got := string(recordKey(f.store.Snapshot().Networks[0], "advanced")); got != liveNetworkPatch {
		t.Fatalf("network patch rewritten: %s", got)
	}
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl laptop"
	response = f.call(t, http.MethodPatch, "/api/sites/home", `{"ssh":{"keys":["`+key+`"]}}`)
	site := decodeInto[api.MutationResponse[api.SiteView]](t, response, http.StatusOK)
	if len(site.Object.SSH.Keys) != 1 || site.Object.SSH.Keys[0].Type != "ssh-ed25519" || !strings.HasPrefix(site.Object.SSH.Keys[0].Fingerprint, "SHA256:") {
		t.Fatalf("legacy SSH keys: %+v", site.Object.SSH)
	}
	// ssh.enabled is typed, so it is refused in legacy mode.
	errorOf(t, f.call(t, http.MethodPatch, "/api/sites/home", `{"ssh":{"enabled":true}}`), http.StatusServiceUnavailable, api.ErrorCodeMigrationPending)
	// The expert editor writes the legacy advanced field, as written.
	response = f.call(t, http.MethodPut, "/api/sites/home/networks/net-home/expert", `{"patch":{"roamMode":"11r","Auth":"[redacted]"}}`)
	expert := decodeInto[api.MutationResponse[api.ExpertPatchView]](t, response, http.StatusOK)
	if expert.Object.Field != "advanced" || expert.Object.Patch["roamMode"] != "11r" {
		t.Fatalf("legacy expert patch: %+v", expert.Object)
	}
	stored := string(recordKey(f.store.Snapshot().Networks[0], "advanced"))
	if !strings.Contains(stored, `"roamMode":"11r"`) || !strings.Contains(stored, `"WPA3_PSK":true`) {
		t.Fatalf("legacy patch not stored as written with restored secrets: %s", stored)
	}
}
