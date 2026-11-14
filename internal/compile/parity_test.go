package compile_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

var updateGolden = flag.Bool("update", false, "rewrite the legacyv0 golden outputs")

// fixture is a synthetic controller.db: raw bucket JSON exactly as a v0
// binary stores it, plus the template the daemon runs with.
type fixture struct {
	Name      string            `json:"name"`
	Template  string            `json:"template"` // "bundled" or a path under testdata
	Revision  uint64            `json:"revision"`
	UpdatedAt string            `json:"updated_at"`
	Sites     []json.RawMessage `json:"sites"`
	APs       []json.RawMessage `json:"aps"`
	Networks  []json.RawMessage `json:"networks"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "fixtures", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	sort.Strings(paths)
	var result []fixture
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		result = append(result, f)
	}
	return result
}

// templates returns the bytes compile.AP and legacyv0 get for a fixture:
// the bundled template (aptemplate for the live compiler, the frozen copy
// for legacyv0) or the same custom -ap-config file for both.
func (f fixture) templates(t *testing.T) (live, frozen []byte) {
	t.Helper()
	if f.Template == "bundled" {
		return aptemplate.Default(), legacyv0.Template(nil)
	}
	custom, err := os.ReadFile(filepath.Join("testdata", f.Template))
	if err != nil {
		t.Fatal(err)
	}
	return custom, legacyv0.Template(custom)
}

func idOf(t *testing.T, raw json.RawMessage) (id, siteID string) {
	t.Helper()
	var record struct {
		ID     string `json:"id"`
		SiteID string `json:"site_id"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	return record.ID, record.SiteID
}

// writeDB stores the fixture's raw records with v0's bucket keys.
func (f fixture) writeDB(t *testing.T, path string) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Update(func(tx *bolt.Tx) error {
		buckets := map[string]*bolt.Bucket{}
		for _, name := range []string{"meta", "sites", "aps", "networks"} {
			bucket, err := tx.CreateBucketIfNotExists([]byte(name))
			if err != nil {
				return err
			}
			buckets[name] = bucket
		}
		for _, raw := range f.Sites {
			id, _ := idOf(t, raw)
			if err := buckets["sites"].Put([]byte(id), compact(t, raw)); err != nil {
				return err
			}
		}
		for _, raw := range f.APs {
			id, _ := idOf(t, raw)
			if err := buckets["aps"].Put([]byte(id), compact(t, raw)); err != nil {
				return err
			}
		}
		for _, raw := range f.Networks {
			id, siteID := idOf(t, raw)
			if err := buckets["networks"].Put([]byte(siteID+"\x00"+id), compact(t, raw)); err != nil {
				return err
			}
		}
		var revision [8]byte
		binary.BigEndian.PutUint64(revision[:], f.Revision)
		if err := buckets["meta"].Put([]byte("revision"), revision[:]); err != nil {
			return err
		}
		return buckets["meta"].Put([]byte("updated_at"), []byte(f.UpdatedAt))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func compact(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestCompileParityWithLegacyV0 is the phase-0 proof (design §8.3): on every
// fixture, compile.AP with no typed data is byte-identical to legacyv0,
// including fixtures that run on a custom -ap-config template. legacyv0
// reads the raw bucket JSON both before and after a v1 binary has opened
// (and backfilled) the database.
func TestCompileParityWithLegacyV0(t *testing.T) {
	if legacyv0.SHA256Hex(aptemplate.Default()) != legacyv0.TemplateSHA256 {
		t.Fatal("bundled template no longer matches the frozen legacyv0 template")
	}
	for _, f := range loadFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			live, frozen := f.templates(t)
			path := filepath.Join(t.TempDir(), "controller.db")
			f.writeDB(t, path)

			var before legacyv0.State
			db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			err = db.View(func(tx *bolt.Tx) error {
				before, err = legacyv0.ReadState(tx)
				return err
			})
			_ = db.Close()
			if err != nil {
				t.Fatal(err)
			}

			store, err := model.OpenBolt(path, "")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			var after legacyv0.State
			if err := store.View(func(tx *bolt.Tx) error {
				var err error
				after, err = legacyv0.ReadState(tx)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			state := store.Snapshot()
			compiled := 0
			for _, ap := range state.APs {
				input, err := compile.InputFor(live, state, ap.ID)
				var got []byte
				if err == nil {
					var output compile.Output
					output, err = compile.AP(input)
					got = output.Config
				}
				wantBefore, errBefore := legacyv0.CompileAP(frozen, before, ap.ID)
				wantAfter, errAfter := legacyv0.CompileAP(frozen, after, ap.ID)
				if ap.SiteID == "" {
					if !errors.Is(err, compile.ErrNoSite) || !errors.Is(errBefore, legacyv0.ErrNoSite) || !errors.Is(errAfter, legacyv0.ErrNoSite) {
						t.Fatalf("AP %s without a site: %v / %v / %v", ap.ID, err, errBefore, errAfter)
					}
					continue
				}
				if err != nil || errBefore != nil || errAfter != nil {
					t.Fatalf("AP %s: compile %v, legacyv0 %v / %v", ap.ID, err, errBefore, errAfter)
				}
				if !bytes.Equal(got, wantBefore) || !bytes.Equal(got, wantAfter) {
					t.Fatalf("AP %s: compile.AP differs from legacyv0\ncompile:  %s\nlegacyv0: %s", ap.ID, got, wantBefore)
				}
				checkGolden(t, f.Name+"-"+ap.ID, wantBefore)
				compiled++
			}
			if compiled == 0 {
				t.Fatal("fixture compiled no AP")
			}
		})
	}
}

// checkGolden pins legacyv0's output: the frozen compiler must never change
// what it produces. Run `go test ./internal/compile -update` only when a
// fixture is added.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", strings.ReplaceAll(name, "/", "_")+".json")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(append([]byte(nil), got...), '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update after adding a fixture): %v", path, err)
	}
	if !bytes.Equal(bytes.TrimSuffix(want, []byte("\n")), got) {
		t.Fatalf("legacyv0 output for %s changed; the frozen compiler must not change", name)
	}
}

func TestLegacyV0DecodesRawRecordsNotModelTypes(t *testing.T) {
	// A record with keys a later model might add or reinterpret decodes into
	// the frozen structs exactly as ap-identity did.
	raw := legacyv0.Raw{
		Revision:  []byte{0, 0, 0, 0, 0, 0, 0, 5},
		UpdatedAt: []byte("2026-11-01T00:00:00Z"),
		Sites:     [][]byte{[]byte(`{"id":"site-a","name":"Example","expert":{"LED":{"Brightness":1}},"rf":{"managed":true}}`)},
		APs:       [][]byte{[]byte(`{"id":"02-aa-00-00-00-01","site_id":"site-a","expert":{"Name":"x"},"led_brightness":3}`)},
		Networks:  [][]byte{[]byte(`{"id":"w","site_id":"site-a","ssid":"Example","psk":"example-passphrase-01","vlan_id":1,"enabled":true,"security":"wpa3_personal","expert":{"Hidden":true}}`)},
	}
	state, err := legacyv0.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := legacyv0.CompileAP(legacyv0.Template(nil), state, "02-aa-00-00-00-01")
	if err != nil {
		t.Fatal(err)
	}
	plain := raw
	plain.Sites = [][]byte{[]byte(`{"id":"site-a","name":"Example"}`)}
	plain.APs = [][]byte{[]byte(`{"id":"02-aa-00-00-00-01","site_id":"site-a"}`)}
	plain.Networks = [][]byte{[]byte(`{"id":"w","site_id":"site-a","ssid":"Example","psk":"example-passphrase-01","vlan_id":1,"enabled":true}`)}
	plainState, _ := legacyv0.Decode(plain)
	want, _ := legacyv0.CompileAP(legacyv0.Template(nil), plainState, "02-aa-00-00-00-01")
	if !bytes.Equal(got, want) {
		t.Fatal("schema-2 keys changed the v0 compile")
	}
}

func TestLegacyV0TemplateIsFrozen(t *testing.T) {
	if got := legacyv0.SHA256Hex(legacyv0.BundledTemplate()); got != legacyv0.TemplateSHA256 {
		t.Fatalf("frozen template hash %s", got)
	}
	custom := []byte(`{"WLANs":[{}]}`)
	if !bytes.Equal(legacyv0.Template(custom), custom) {
		t.Fatal("runtime template not honoured")
	}
}
