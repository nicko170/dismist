package model

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func openTestStore(t *testing.T) (*BoltStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func seedSite(t *testing.T, store *BoltStore) {
	t.Helper()
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "Example"}}
		state.APs = []APRecord{
			{ID: "02-aa-00-00-00-01", SiteID: "site-a", Secret: []byte(testSecret)},
			{ID: "02-aa-00-00-00-02", SiteID: "site-a", Secret: []byte(testSecret)},
		}
		state.Networks = []Network{{ID: "wlan-a", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func writeMetaUint(t *testing.T, path, key string, value uint64) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists([]byte(BucketMeta))
		if err != nil {
			return err
		}
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], value)
		return meta.Put([]byte(key), raw[:])
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEveryDesignBucketExists(t *testing.T) {
	store, _ := openTestStore(t)
	want := []string{"meta", "sites", "aps", "networks", "ap_config", "pending_dismissed", "config_history",
		"tokens", "sessions", "events2", "events2_id", "events2_ap", "sessions_t", "samples",
		"samples_hourly", "scans", "client_meta", "events", "client_active", "ap_status", "templates", "client_sessions"}
	if err := store.View(func(tx *bolt.Tx) error {
		for _, name := range want {
			if tx.Bucket([]byte(name)) == nil {
				return fmt.Errorf("missing bucket %s", name)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHashKeyIsCreatedOnceAndKept(t *testing.T) {
	store, path := openTestStore(t)
	key := store.HashKey()
	if len(key) != HashKeySize || bytes.Equal(key, make([]byte, HashKeySize)) {
		t.Fatalf("hash key %x", key)
	}
	_ = store.Close()
	reopened, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !bytes.Equal(reopened.HashKey(), key) {
		t.Fatal("hash key changed across restarts")
	}
	other, _ := openTestStore(t)
	if bytes.Equal(other.HashKey(), key) {
		t.Fatal("two databases share a hash key")
	}
}

func TestSchemaAbsentMeansOneAndIsNotWrittenByM0(t *testing.T) {
	store, _ := openTestStore(t)
	seedSite(t, store)
	info, err := store.Schema()
	if err != nil || info.Schema != 1 || info.Compat != 1 {
		t.Fatalf("schema %+v %v", info, err)
	}
	if err := store.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(BucketMeta))
		if meta.Get([]byte(MetaSchema)) != nil || meta.Get([]byte(MetaCompat)) != nil {
			return errors.New("M0 wrote schema or compat")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCompatGuardRefusesNewerDatabaseWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	seedSite(t, store)
	_ = store.Close()
	writeMetaUint(t, path, MetaCompat, 2)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenBolt(path, "")
	var compat *CompatError
	if !errors.As(err, &compat) || compat.Compat != 2 || compat.ExitCode() != 78 {
		t.Fatalf("compat 2 accepted or wrong error: %v", err)
	}
	want := "controller.db needs dismist schema 2; restore controller.db.pre-v2-* to run this version (docs/kubernetes.md#rollback)"
	if err.Error() != want {
		t.Fatalf("message %q", err.Error())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("refused database was modified")
	}
	// compat equal to the supported schema runs.
	writeMetaUint(t, path, MetaCompat, SupportedSchema)
	ok, err := OpenBolt(path, "")
	if err != nil {
		t.Fatalf("compat %d refused: %v", SupportedSchema, err)
	}
	_ = ok.Close()
}

// TestCompatGuardExits78 runs the guard in a child process: OpenBolt on a
// compat-2 database followed by ExitIfCompat, which is what dismistd does.
func TestCompatGuardExits78(t *testing.T) {
	if path := os.Getenv("DISMIST_COMPAT_CHILD_DB"); path != "" {
		_, err := OpenBolt(path, "")
		ExitIfCompat(err)
		fmt.Fprintln(os.Stderr, "guard did not exit:", err)
		os.Exit(3)
	}
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	writeMetaUint(t, path, MetaCompat, 2)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCompatGuardExits78$")
	cmd.Env = append(os.Environ(), "DISMIST_COMPAT_CHILD_DB="+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 78 {
		t.Fatalf("child exit %v, stderr %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "controller.db needs dismist schema 2; restore controller.db.pre-v2-*") {
		t.Fatalf("missing message: %s", stderr.String())
	}
}

func TestUnreadableCompatIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	db, _ := bolt.Open(path, 0o600, nil)
	_ = db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(BucketMeta)).Put([]byte(MetaCompat), []byte("2")) })
	_ = db.Close()
	var compat *CompatError
	if _, err := OpenBolt(path, ""); !errors.As(err, &compat) {
		t.Fatalf("garbled compat accepted: %v", err)
	}
}

func TestAPConfigCommitsWithStateAndRollsBackWithIt(t *testing.T) {
	store, _ := openTestStore(t)
	seedSite(t, store)
	apID := "02-aa-00-00-00-01"
	if err := store.UpdateTx(func(state *State, tx *Tx) error {
		state.Sites[0].Name = "Renamed"
		return tx.APConfig().PutDesired(apID, DesiredConfig{Revision: 7, Timestamp: 1.5, Bytes: []byte(`{"a":1}`)})
	}); err != nil {
		t.Fatal(err)
	}
	desired, ok, err := store.APConfig().Desired(apID)
	if err != nil || !ok || desired.Revision != 7 || string(desired.Bytes) != `{"a":1}` || desired.SHA == "" {
		t.Fatalf("desired %+v %v %v", desired, ok, err)
	}
	if desired.SHA != store.APConfig().Digest([]byte(`{"a":1}`)) {
		t.Fatal("digest mismatch")
	}
	failing := errors.New("refused")
	if err := store.UpdateTx(func(state *State, tx *Tx) error {
		if err := tx.APConfig().PutDesired(apID, DesiredConfig{Revision: 8, Bytes: []byte(`{"a":2}`)}); err != nil {
			return err
		}
		return failing
	}); !errors.Is(err, failing) {
		t.Fatal(err)
	}
	if desired, _, _ := store.APConfig().Desired(apID); desired.Revision != 7 {
		t.Fatalf("failed mutation kept its ap_config write: %+v", desired)
	}
	// A validation failure after the change also rolls ap_config back.
	if err := store.UpdateTx(func(state *State, tx *Tx) error {
		_ = tx.APConfig().PutDesired(apID, DesiredConfig{Revision: 9, Bytes: []byte(`{}`)})
		state.APs[0].SiteID = "missing"
		return nil
	}); err == nil {
		t.Fatal("invalid state accepted")
	}
	if desired, _, _ := store.APConfig().Desired(apID); desired.Revision != 7 {
		t.Fatalf("invalid mutation kept its ap_config write: %+v", desired)
	}
	if got := store.Snapshot(); got.Sites[0].Name != "Renamed" || got.Revision != 2 {
		t.Fatalf("state %+v", got)
	}
	// Delivered is kept separately from desired.
	if err := store.APConfig().PutDelivered(apID, DeliveredConfig{Revision: 6, Bytes: []byte(`{"d":1}`), At: time.Unix(10, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := store.APConfig().SetVerified(apID, Verification{At: time.Unix(20, 0), OK: true}); err != nil {
		t.Fatal(err)
	}
	record, _, _ := store.APConfig().Get(apID)
	if record.Desired.Revision != 7 || record.Delivered.Revision != 6 || string(record.Delivered.Bytes) != `{"d":1}` || !record.Delivered.Verified.OK {
		t.Fatalf("record %+v", record)
	}
	// Removing the AP removes its ap_config record.
	if err := store.Update(func(state *State) error {
		state.APs = state.APs[1:]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.APConfig().Get(apID); ok {
		t.Fatal("ap_config of a deleted AP survived")
	}
}

func TestJSONStoreSupportsUpdateTx(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var _ TxStateStore = store
	if err := store.UpdateTx(func(state *State, tx *Tx) error {
		state.Sites = []Site{{ID: "site-a", Name: "A"}}
		state.APs = []APRecord{{ID: "ap-a", SiteID: "site-a", Secret: []byte(testSecret)}}
		return tx.APConfig().PutDesired("ap-a", DesiredConfig{Revision: 1, Bytes: []byte(`{}`)})
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTx(func(state *State, tx *Tx) error {
		desired, ok, err := tx.APConfig().Desired("ap-a")
		if err != nil || !ok || desired.Revision != 1 {
			return fmt.Errorf("desired lost: %+v %v %v", desired, ok, err)
		}
		_ = tx.APConfig().PutDesired("ap-a", DesiredConfig{Revision: 2})
		return errors.New("rollback")
	}); err == nil || err.Error() != "rollback" {
		t.Fatal(err)
	}
	if err := store.UpdateTx(func(state *State, tx *Tx) error {
		if desired, _, _ := tx.APConfig().Desired("ap-a"); desired.Revision != 1 {
			return fmt.Errorf("rolled-back write visible: %+v", desired)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var _ TxStateStore = (*BoltStore)(nil)
}

func TestUpdatedRevTracksChangedRecordsOnly(t *testing.T) {
	store, _ := openTestStore(t)
	seedSite(t, store)
	first := store.Snapshot()
	if first.Sites[0].UpdatedRev != 1 || first.APs[0].UpdatedRev != 1 || first.Networks[0].UpdatedRev != 1 {
		t.Fatalf("new records not stamped: %+v", first)
	}
	if err := store.Update(func(state *State) error {
		state.APs[1].Name = "renamed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot()
	if got.APs[1].UpdatedRev != 2 || got.APs[0].UpdatedRev != 1 || got.Sites[0].UpdatedRev != 1 || got.Networks[0].UpdatedRev != 1 {
		t.Fatalf("updated_rev: %+v", got)
	}
}

func TestLocalFilesKeepsNeverCheckedApartFromNoneFound(t *testing.T) {
	store, path := openTestStore(t)
	seedSite(t, store)
	if err := store.Update(func(state *State) error {
		state.APs[0].LocalFiles = []LocalFile{}
		state.APs[1].Features = map[string]bool{"pong2": false}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	reopened, err := OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.Snapshot()
	if got.APs[0].LocalFiles == nil || len(got.APs[0].LocalFiles) != 0 {
		t.Fatalf("checked-and-empty became %#v", got.APs[0].LocalFiles)
	}
	if got.APs[1].LocalFiles != nil {
		t.Fatalf("never-checked became %#v", got.APs[1].LocalFiles)
	}
	if on, present := got.APs[1].Features["pong2"]; !present || on {
		t.Fatalf("features %#v", got.APs[1].Features)
	}
}

func TestTokensStoreOnlyADigest(t *testing.T) {
	store, _ := openTestStore(t)
	tokens := store.Tokens()
	record, bearer, err := tokens.Create("laptop", TokenScopeAdmin, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	id, secret, ok := SplitToken(bearer)
	if !ok || id != record.ID || !strings.HasPrefix(id, "dsm_") || len(secret) != 64 {
		t.Fatalf("token %q record %+v", bearer, record)
	}
	if err := store.View(func(tx *bolt.Tx) error {
		if bytes.Contains(tx.Bucket([]byte(BucketTokens)).Get([]byte(id)), []byte(secret)) {
			return errors.New("secret stored")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := tokens.Verify(bearer); !ok || got.Scope != TokenScopeAdmin {
		t.Fatal("valid token rejected")
	}
	for _, bad := range []string{bearer + "x", id + "_" + strings.Repeat("0", 64), "dsm_", "nope", id} {
		if _, ok := tokens.Verify(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, _, err := tokens.Create("x", "root", time.Now()); err == nil {
		t.Fatal("accepted unknown scope")
	}
	if err := tokens.Touch(id, time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	if err := tokens.Touch(id, time.Unix(210, 0)); err != nil {
		t.Fatal(err)
	}
	if got, _ := tokens.Get(id); !got.LastUsed.Equal(time.Unix(200, 0)) {
		t.Fatalf("touch not throttled: %v", got.LastUsed)
	}
	list, err := tokens.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if err := tokens.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := tokens.Verify(bearer); ok {
		t.Fatal("revoked token accepted")
	}
	if err := tokens.Delete(id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSessionsAndPendingDismissals(t *testing.T) {
	store, _ := openTestStore(t)
	sessions := store.Sessions()
	now := time.Unix(1000, 0)
	keyA, keyB := SessionKey("cookie-a"), SessionKey("cookie-b")
	if keyA == "cookie-a" || len(keyA) != 64 {
		t.Fatal("session key is not a digest")
	}
	_ = sessions.Put(keyA, SessionRecord{Created: now, Expires: now.Add(time.Hour), Remote: "192.0.2.10"})
	_ = sessions.Put(keyB, SessionRecord{Created: now.Add(time.Second), Expires: now.Add(time.Minute)})
	if _, err := sessions.Get(keyA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Get(keyB, now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session returned")
	}
	if list, _ := sessions.List(now); len(list) != 2 || list[0].Key != keyB {
		t.Fatalf("list %+v", list)
	}
	if removed, _ := sessions.DeleteExpired(now.Add(2 * time.Minute)); removed != 1 {
		t.Fatalf("expired removed %d", removed)
	}
	if err := sessions.Touch(keyA, now.Add(time.Minute), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := sessions.Get(keyA, now); !got.Expires.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("touch %+v", got)
	}
	if removed, _ := sessions.DeleteAll(""); removed != 1 {
		t.Fatalf("revoke-all removed %d", removed)
	}

	pending := store.PendingDismissed()
	_ = pending.Dismiss("02-aa-00-00-00-09", PendingDismissal{Until: now.Add(time.Hour), At: now, Actor: "admin"})
	_ = pending.Dismiss("02-aa-00-00-00-0a", PendingDismissal{Until: now.Add(-time.Second), At: now})
	active, err := pending.Active(now)
	if err != nil || len(active) != 1 || active["02-aa-00-00-00-09"].Actor != "admin" {
		t.Fatalf("active %+v %v", active, err)
	}
	if removed, _ := pending.Prune(now); removed != 1 {
		t.Fatal("prune")
	}
	_ = pending.Undismiss("02-aa-00-00-00-09")
	if active, _ := pending.Active(now); len(active) != 0 {
		t.Fatal("undismiss")
	}
}

func TestProbeAndH0(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.Probe(); err != nil {
		t.Fatal(err)
	}
	record := H0Record{APID: "02-aa-00-00-00-01", Raw: []byte(`{"x":1}`), RawHMAC: "abc", NormaliserVersion: 1, EffectiveHash: "e1:00"}
	if err := store.PutH0(record); err != nil {
		t.Fatal(err)
	}
	if err := store.PutH0(H0Record{APID: "02-aa-00-00-00-02", EffectiveHash: "e1:01"}); err != nil {
		t.Fatal(err)
	}
	all, err := store.H0()
	if err != nil || len(all) != 2 || string(all[record.APID].Raw) != `{"x":1}` || all[record.APID].EffectiveHash != "e1:00" {
		t.Fatalf("h0 %+v %v", all, err)
	}
	if err := store.PutMetaJSON(MetaCompat, 1); err == nil {
		t.Fatal("compat written as JSON")
	}
}

// TestBackupIsConsistentUnderConcurrentWrites checks that every backup taken
// while writers run is a self-consistent bbolt snapshot: the stored revision
// always equals the number of networks, because both change in the same
// transaction.
func TestBackupIsConsistentUnderConcurrentWrites(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.Update(func(state *State) error {
		state.Sites = []Site{{ID: "site-a", Name: "Example"}}
		state.Networks = []Network{{ID: "n-0000", SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := store.Update(func(state *State) error {
				state.Networks = append(state.Networks, Network{ID: fmt.Sprintf("n-%04d", i), SiteID: "site-a", SSID: "Example", PSK: "example-passphrase-01", VLANID: 1 + i%4000})
				return nil
			}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		path := filepath.Join(dir, fmt.Sprintf("backup-%d.db", i))
		size, err := store.WriteBackup(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() != size || info.Mode().Perm() != 0o600 {
			t.Fatalf("backup file %v %v", info, err)
		}
		copyDB, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		err = copyDB.View(func(tx *bolt.Tx) error {
			state, err := readBoltState(tx)
			if err != nil {
				return err
			}
			if int(state.Revision) != len(state.Networks) {
				return fmt.Errorf("torn backup: revision %d with %d networks", state.Revision, len(state.Networks))
			}
			return nil
		})
		_ = copyDB.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".tmp-") {
				t.Fatalf("temporary file left behind: %s", entry.Name())
			}
		}
	}
	close(stop)
	writers.Wait()
}

func TestDiskUsage(t *testing.T) {
	space, err := DiskUsage(t.TempDir())
	if err != nil {
		t.Skip(err)
	}
	if space.Total == 0 || space.Free > space.Total {
		t.Fatalf("disk usage %+v", space)
	}
}
