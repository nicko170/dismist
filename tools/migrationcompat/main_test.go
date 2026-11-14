package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/model"
)

func TestEqualNormalisesBeforeComparing(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	template := aptemplate.Default()
	base := write("base.json", template)
	// The same config with a different Timestamp and trailing whitespace
	// is the same effective config (normaliser rule 7).
	stamped := write("stamped.json", append(bytes.Replace(template, []byte(`"Timestamp":`), []byte(`"Timestamp": 12345, "Ignored":`), 1), '\n'))
	changed := write("changed.json", bytes.Replace(template, []byte(`"Hostname":`), []byte(`"Hostname": "other", "Ignored":`), 1))
	var out bytes.Buffer
	if err := run([]string{"equal", base, base}, &out); err != nil || !strings.Contains(out.String(), "equal:") {
		t.Fatalf("identical configs: %v %s", err, out.String())
	}
	if !bytes.Contains(template, []byte(`"Timestamp":`)) {
		t.Skip("template has no Timestamp key to vary")
	}
	if err := run([]string{"equal", base, stamped}, &out); err != nil {
		t.Errorf("a new Timestamp made the configs differ: %v", err)
	}
	err := run([]string{"equal", base, changed}, &out)
	if err == nil || !strings.Contains(err.Error(), "differ after normalisation") {
		t.Errorf("a changed hostname compared equal: %v", err)
	}
	if err := run([]string{"equal", base, write("bad.json", []byte("not json"))}, &out); err == nil {
		t.Error("accepted a file that is not a config")
	}
}

func TestStateAndSetCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	store, err := model.OpenBolt(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"state", path}, &out); err != nil || !strings.Contains(out.String(), `"compat":1`) {
		t.Fatalf("state = %s, %v", out.String(), err)
	}
	if err := run([]string{"set-compat", path, "2"}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"state", path}, &out); err != nil || !strings.Contains(out.String(), `"compat":2`) {
		t.Fatalf("state after set-compat = %s, %v", out.String(), err)
	}
	// The binary's own guard now refuses the database.
	_, err = model.OpenBolt(path, "")
	var compat *model.CompatError
	if !errors.As(err, &compat) || compat.Compat != 2 {
		t.Fatalf("OpenBolt on compat 2 = %v", err)
	}
	for _, args := range [][]string{{}, {"set-compat", path, "zero"}, {"set-compat", path, "0"}, {"equal", path}, {"bogus"}} {
		if err := run(args, &out); err == nil {
			t.Errorf("%v: accepted", args)
		}
	}
}
