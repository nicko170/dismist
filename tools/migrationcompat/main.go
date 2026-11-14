// Command migrationcompat supports the migration-compat CI job (design §4.1
// rule 6): it compares AP configs after normalisation and reads or sets
// the schema numbers of a stopped controller database. It never talks to an
// AP or a running controller.
//
//	migrationcompat equal OLD.json NEW.json   exit 0 when both configs normalise
//	                                          to the same effective config (§4.5)
//	migrationcompat state DB                  print meta.schema, meta.compat and
//	                                          meta.migration_state as JSON
//	migrationcompat set-compat DB N           write meta.compat = N (stop dismistd first)
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/model"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "migrationcompat:", err)
		os.Exit(1)
	}
}

const usage = "usage: migrationcompat equal OLD.json NEW.json | state DB | set-compat DB N"

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch {
	case args[0] == "equal" && len(args) == 3:
		return equal(args[1], args[2], out)
	case args[0] == "state" && len(args) == 2:
		return state(args[1], out)
	case args[0] == "set-compat" && len(args) == 3:
		compat, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil || compat == 0 {
			return fmt.Errorf("compat must be a positive integer, not %q", args[2])
		}
		return setCompat(args[1], compat)
	}
	return errors.New(usage)
}

// equal normalises both configs with the tree's normaliser (local files
// unknown, as for an AP never inspected) and compares the canonical forms.
func equal(oldPath, newPath string, out io.Writer) error {
	canonical := func(path string) ([]byte, error) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] != '{' {
			return nil, fmt.Errorf("%s: not a JSON config", path)
		}
		result, report, err := effective.Normalise(raw, effective.Context{LocalFiles: effective.LocalFilesUnknown})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(report.TypeErrors) > 0 {
			return nil, fmt.Errorf("%s: the AP would reject this config: %v", path, report.TypeErrors)
		}
		return result, nil
	}
	before, err := canonical(oldPath)
	if err != nil {
		return err
	}
	after, err := canonical(newPath)
	if err != nil {
		return err
	}
	if bytes.Equal(before, after) {
		fmt.Fprintf(out, "equal: %s and %s normalise to the same effective config (normaliser v%d)\n", oldPath, newPath, effective.Version)
		return nil
	}
	return fmt.Errorf("%s and %s differ after normalisation (normaliser v%d):\n%s", oldPath, newPath, effective.Version, firstDifference(before, after))
}

// firstDifference shows where two canonical documents part, without
// printing either in full (they hold passphrases).
func firstDifference(a, b []byte) string {
	index := 0
	for index < len(a) && index < len(b) && a[index] == b[index] {
		index++
	}
	start := max(0, index-60)
	excerpt := func(s []byte) string {
		end := min(len(s), index+60)
		if start >= end {
			return ""
		}
		return string(s[start:end])
	}
	return fmt.Sprintf("at byte %d:\n  old: …%s…\n  new: …%s…", index, excerpt(a), excerpt(b))
}

type stateView struct {
	Schema    uint64          `json:"schema"`
	Compat    uint64          `json:"compat"`
	Migration json.RawMessage `json:"migration_state,omitempty"`
}

func state(path string, out io.Writer) error {
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	defer db.Close()
	view := stateView{Schema: 1, Compat: 1}
	err = db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte(model.BucketMeta))
		if meta == nil {
			return nil
		}
		for key, target := range map[string]*uint64{model.MetaSchema: &view.Schema, model.MetaCompat: &view.Compat} {
			if raw := meta.Get([]byte(key)); raw != nil {
				if len(raw) != 8 {
					return fmt.Errorf("meta.%s is not an 8-byte integer", key)
				}
				*target = binary.BigEndian.Uint64(raw)
			}
		}
		if raw := meta.Get([]byte(model.MetaMigrationState)); raw != nil && json.Valid(raw) {
			view.Migration = append(json.RawMessage(nil), raw...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

func setCompat(path string, compat uint64) error {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists([]byte(model.BucketMeta))
		if err != nil {
			return err
		}
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], compat)
		return meta.Put([]byte(model.MetaCompat), raw[:])
	})
}
