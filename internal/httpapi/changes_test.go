package httpapi

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// changeFixture has two APs in site "a" and one in site "b", each with a
// control session, like a running controller.
type changeFixture struct {
	handler  http.Handler
	store    *model.BoltStore
	controls map[string]*controller.Server
}

func newChangeFixture(t *testing.T) *changeFixture {
	t.Helper()
	store, err := model.OpenBolt(filepath.Join(t.TempDir(), "controller.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "a", Name: "Home", Country: "AU", Timezone: "Australia/Sydney"}, {ID: "b", Name: "Lab"}}
		state.APs = []model.APRecord{
			{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "a", Secret: bytes.Repeat([]byte{1}, 32), ConfigRevision: 1, ConfigUpdatedAt: configTime},
			{ID: "02-aa-00-00-00-02", Name: "Office", SiteID: "a", Secret: bytes.Repeat([]byte{2}, 32), ConfigRevision: 1, ConfigUpdatedAt: configTime},
			{ID: "02-aa-00-00-00-03", Name: "Bench", SiteID: "b", Secret: bytes.Repeat([]byte{3}, 32), ConfigRevision: 1, ConfigUpdatedAt: configTime},
		}
		state.Networks = []model.Network{
			{ID: "home", SiteID: "a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true},
			{ID: "lab", SiteID: "b", SSID: "Example-Lab", PSK: "example-passphrase-02", VLANID: 20, Enabled: true},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"x","Timestamp":1,"SwitchPorts":[{"Name":"eth0","Vlans":["1"]}],"WLANs":[{"ID":"t","SSID":"T","Enabled":true,"Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-00"}}]}`)
	manager := controller.NewManager()
	fixture := &changeFixture{store: store, controls: map[string]*controller.Server{}}
	state := store.Snapshot()
	// Serve each AP what dismistd would serve it at startup.
	for _, served := range compile.Prepare(base, state, nil) {
		control, err := controller.NewServer(controller.Options{ID: served.APID, CertDER: []byte{1}, Config: served.Config, Revision: served.Revision})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Register(served.APID, "", control); err != nil {
			t.Fatal(err)
		}
		fixture.controls[served.APID] = control
	}
	fixture.handler = NewWithManager(store, manager, base, "02-aa-00-00-00-01", "*")
	return fixture
}

// revisions returns each AP's stored ConfigRevision and the revision its
// control session serves.
func (f *changeFixture) revisions() map[string][2]uint64 {
	result := map[string][2]uint64{}
	for _, ap := range f.store.Snapshot().APs {
		var served uint64
		if control := f.controls[ap.ID]; control != nil {
			_, served = control.ConfigSnapshot()
		}
		result[ap.ID] = [2]uint64{ap.ConfigRevision, served}
	}
	return result
}

func (f *changeFixture) do(t *testing.T, method, path, body string, want int) {
	t.Helper()
	if response := request(t, f.handler, method, path, body); response.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
	}
}

// changed lists the APs whose stored or served revision moved.
func changedAPs(before, after map[string][2]uint64) []string {
	var changed []string
	for _, id := range []string{"02-aa-00-00-00-01", "02-aa-00-00-00-02", "02-aa-00-00-00-03"} {
		if before[id] != after[id] {
			changed = append(changed, id)
		}
	}
	return changed
}

func TestDismistOnlyEditsChangeNoConfigRevision(t *testing.T) {
	f := newChangeFixture(t)
	secret := hex.EncodeToString(bytes.Repeat([]byte{9}, 32))
	edits := []struct{ method, path, body string }{
		{http.MethodPut, "/api/sites/a", `{"name":"Home renamed"}`},
		{http.MethodPut, "/api/sites/a", `{"timezone":"Australia/Brisbane"}`},
		{http.MethodPut, "/api/aps/02-aa-00-00-00-01", `{"management_ip":"192.0.2.10"}`},
		{http.MethodPut, "/api/aps/02-aa-00-00-00-01", `{"management_ip":""}`},
		{http.MethodPut, "/api/aps/02-aa-00-00-00-01", `{"secret":"` + secret + `"}`},
		{http.MethodPut, "/api/aps/02-aa-00-00-00-01", `{"model":"AP33"}`},
		{http.MethodPut, "/api/aps/02-aa-00-00-00-01", `{"local_files":[]}`},
		{http.MethodPost, "/api/sites", `{"name":"Garage"}`},
		{http.MethodPut, "/api/sites/b/networks/lab", `{"psk":"example-passphrase-02"}`},
	}
	for _, edit := range edits {
		before := f.revisions()
		storeBefore := f.store.Snapshot().Revision
		want := http.StatusOK
		if edit.method == http.MethodPost {
			want = http.StatusCreated
		}
		f.do(t, edit.method, edit.path, edit.body, want)
		if f.store.Snapshot().Revision == storeBefore && edit.path != "/api/sites/b/networks/lab" {
			t.Fatalf("%s %s did not commit", edit.method, edit.path)
		}
		if changed := changedAPs(before, f.revisions()); len(changed) != 0 {
			t.Fatalf("%s %s %s bumped %v", edit.method, edit.path, edit.body, changed)
		}
	}
}

func TestConfigEditsBumpOnlyChangedAPs(t *testing.T) {
	f := newChangeFixture(t)
	steps := []struct {
		method, path, body string
		want               []string
	}{
		// A WLAN edit changes every AP of its site, and no other.
		{http.MethodPut, "/api/sites/a/networks/home", `{"ssid":"Example-2"}`, []string{"02-aa-00-00-00-01", "02-aa-00-00-00-02"}},
		// An AP's name is in its own config only.
		{http.MethodPut, "/api/aps/02-aa-00-00-00-02", `{"name":"Study"}`, []string{"02-aa-00-00-00-02"}},
		// An AP patch changes that AP only.
		{http.MethodPut, "/api/sites/a/aps/02-aa-00-00-00-01/advanced", `{"patch":{"LED":{"Brightness":200}}}`, []string{"02-aa-00-00-00-01"}},
		// A site patch changes the site's APs.
		{http.MethodPut, "/api/sites/b/advanced", `{"patch":{"LED":{"Brightness":10}}}`, []string{"02-aa-00-00-00-03"}},
		// The country is in the config.
		{http.MethodPut, "/api/sites/b", `{"country":"NZ"}`, []string{"02-aa-00-00-00-03"}},
		// Site SSH keys reach the APs.
		{http.MethodPut, "/api/sites/b/ssh-keys", `{"keys":["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB key"]}`, []string{"02-aa-00-00-00-03"}},
		// Moving an AP changes it; the other APs of both sites keep theirs.
		{http.MethodPut, "/api/aps/02-aa-00-00-00-02/site", `{"site_id":"b"}`, []string{"02-aa-00-00-00-02"}},
		// A new WLAN in site b reaches both of its APs now.
		{http.MethodPost, "/api/sites/b/networks", `{"ssid":"Example-IoT","psk":"example-passphrase-03","vlan_id":30}`, []string{"02-aa-00-00-00-02", "02-aa-00-00-00-03"}},
		// Deleting a WLAN in site a changes the AP left there.
		{http.MethodDelete, "/api/sites/a/networks/home", ``, []string{"02-aa-00-00-00-01"}},
	}
	for _, step := range steps {
		before := f.revisions()
		f.do(t, step.method, step.path, step.body, http.StatusOK)
		after := f.revisions()
		changed := changedAPs(before, after)
		if len(changed) != len(step.want) {
			t.Fatalf("%s %s: changed %v, want %v", step.method, step.path, changed, step.want)
		}
		state := f.store.Snapshot()
		for index, id := range step.want {
			if changed[index] != id {
				t.Fatalf("%s %s: changed %v, want %v", step.method, step.path, changed, step.want)
			}
			// The changed AP is at the new state revision, serves it and
			// has it stored as its desired config.
			if after[id][0] != state.Revision || after[id][1] != state.Revision {
				t.Fatalf("%s %s: AP %s at %v, state revision %d", step.method, step.path, id, after[id], state.Revision)
			}
			desired, found, err := f.store.APConfig().Desired(id)
			if err != nil || !found || desired.Revision != state.Revision {
				t.Fatalf("%s %s: AP %s desired %+v found=%v err=%v", step.method, step.path, id, desired.Revision, found, err)
			}
			served, _ := f.controls[id].ConfigSnapshot()
			if !bytes.Equal(served, desired.Bytes) {
				t.Fatalf("%s %s: AP %s serves a config other than its desired one", step.method, step.path, id)
			}
		}
	}
}

func TestRegisterAPBumpsOnlyTheNewAP(t *testing.T) {
	f := newChangeFixture(t)
	before := f.revisions()
	secret := hex.EncodeToString(bytes.Repeat([]byte{4}, 32))
	f.do(t, http.MethodPost, "/api/aps", `{"id":"02-aa-00-00-00-04","name":"Shed","site_id":"a","secret":"`+secret+`"}`, http.StatusCreated)
	if changed := changedAPs(before, f.revisions()); len(changed) != 0 {
		t.Fatalf("registering an AP bumped %v", changed)
	}
	state := f.store.Snapshot()
	for _, ap := range state.APs {
		if ap.ID == "02-aa-00-00-00-04" && ap.ConfigRevision != state.Revision {
			t.Fatalf("new AP at revision %d, state %d", ap.ConfigRevision, state.Revision)
		}
	}
	if _, found, _ := f.store.APConfig().Desired("02-aa-00-00-00-04"); !found {
		t.Fatal("new AP has no desired config")
	}
}

// serveStore builds a handler over store the way dismistd does at startup:
// each AP's control session serves what compile.Prepare gives it.
func serveStore(t *testing.T, store *model.BoltStore, base []byte) (http.Handler, map[string]*controller.Server) {
	t.Helper()
	manager := controller.NewManager()
	controls := map[string]*controller.Server{}
	for _, served := range compile.Prepare(base, store.Snapshot(), nil) {
		if served.Config == nil {
			continue
		}
		control, err := controller.NewServer(controller.Options{ID: served.APID, CertDER: []byte{1}, Config: served.Config, Revision: served.Revision})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Register(served.APID, "", control); err != nil {
			t.Fatal(err)
		}
		controls[served.APID] = control
	}
	return NewWithManager(store, manager, base, "02-aa-00-00-00-01", "*"), controls
}

// A network created before a restart is read back in bucket order after
// it; the compiled WLAN order (and so change detection) must not depend on
// which of the two the state holds.
func TestNetworkOrderSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	configTime := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Update(func(state *model.State) error {
		state.Sites = []model.Site{{ID: "a", Name: "Home"}}
		state.APs = []model.APRecord{{ID: "02-aa-00-00-00-01", Name: "Hallway", SiteID: "a", Secret: bytes.Repeat([]byte{1}, 32), ConfigRevision: 1, ConfigUpdatedAt: configTime}}
		// "zzzz" sorts after any generated network ID.
		state.Networks = []model.Network{{ID: "zzzz", SiteID: "a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base := []byte(`{"ID":"x","Timestamp":1,"WLANs":[{"ID":"t","SSID":"T","Enabled":true,"Vlans":["1"],"Auth":{"Type":"psk","PSK":"example-passphrase-00"}}]}`)
	handler, _ := serveStore(t, store, base)
	if response := request(t, handler, http.MethodPost, "/api/sites/a/networks", `{"ssid":"Example-IoT","psk":"example-passphrase-02","vlan_id":30}`); response.Code != http.StatusOK {
		t.Fatalf("create network: %d %s", response.Code, response.Body.String())
	}
	created := store.Snapshot().APs[0].ConfigRevision
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, controls := serveStore(t, store, base)
	served, revision := controls["02-aa-00-00-00-01"].ConfigSnapshot()
	desired, found, err := store.APConfig().Desired("02-aa-00-00-00-01")
	if err != nil || !found {
		t.Fatalf("desired: found=%v err=%v", found, err)
	}
	if revision != created || !bytes.Equal(served, desired.Bytes) {
		t.Fatalf("after restart the AP is served revision %d (want %d) and a config other than its desired one:\nserved  %s\ndesired %s", revision, created, served, desired.Bytes)
	}
	if response := request(t, handler, http.MethodPut, "/api/sites/a", `{"name":"Renamed"}`); response.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", response.Code, response.Body.String())
	}
	if got := store.Snapshot().APs[0].ConfigRevision; got != created {
		t.Fatalf("a site rename after a restart moved the AP from revision %d to %d", created, got)
	}
	if _, after := controls["02-aa-00-00-00-01"].ConfigSnapshot(); after != created {
		t.Fatalf("a site rename after a restart pushed revision %d", after)
	}
}
