package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

// `dismist db …` (design §4.7, §8.5): offline tools for controller.db
// backups. They never modify the file they are given.
//
//	dismist db migrate --dry-run [--ap-config FILE] [--accept-local-files IDS] [--json] BACKUP
//	dismist db rehearse-copy --site NAME|ID --out FILE [--ssid-suffix -LAB] [--ap OLD=NEW --secret HEX|BASE64] BACKUP
//
// main.go dispatches "db" to runDB.

const dbUsage = "usage: dismist db migrate --dry-run [--ap-config FILE] [--accept-local-files IDS] [--json] BACKUP | " +
	"dismist db rehearse-copy --site NAME --out FILE [--ssid-suffix -LAB] [--ap OLD=NEW --secret HEX|BASE64] BACKUP"

func runDB(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New(dbUsage)
	}
	switch args[0] {
	case "migrate":
		return dbMigrate(args[1:], output)
	case "rehearse-copy":
		return dbRehearseCopy(args[1:], output)
	}
	return errors.New(dbUsage)
}

// copyFile copies src to a new private file dst.
func dbCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func dbMigrate(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("dismist db migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	dryRun := flags.Bool("dry-run", false, "plan the schema-2 migration of a copy and print the report (required)")
	apConfig := flags.String("ap-config", "", "runtime AP template (the daemon's -ap-config); default: the bundled template")
	accept := flags.String("accept-local-files", "", "comma-separated AP IDs whose local_* files are accepted")
	asJSON := flags.Bool("json", false, "print the full JSON report")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*dryRun || flags.NArg() != 1 {
		return errors.New("usage: dismist db migrate --dry-run [--ap-config FILE] [--accept-local-files IDS] [--json] BACKUP (the migration itself runs in dismistd)")
	}
	var template []byte
	if *apConfig != "" {
		var err error
		if template, err = os.ReadFile(*apConfig); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp("", "dismist-dry-run-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "controller.db")
	if err := dbCopyFile(flags.Arg(0), path); err != nil {
		return err
	}
	store, err := model.OpenBolt(path, "")
	if err != nil {
		return err
	}
	defer store.Close()
	var acceptList []string
	for _, id := range strings.Split(*accept, ",") {
		if id = strings.TrimSpace(id); id != "" {
			acceptList = append(acceptList, id)
		}
	}
	report, planErr := migrate.DryRun(store, migrate.Context{Template: template, AcceptLocalFiles: acceptList, Now: time.Now().UTC()})
	if *asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else {
		dbPrintReport(output, report)
	}
	if planErr != nil {
		return fmt.Errorf("FAIL: %v", planErr)
	}
	return nil
}

func dbPrintReport(w io.Writer, report migrate.V2Report) {
	result := "PASS"
	if !report.Verified {
		result = "FAIL"
	}
	fmt.Fprintf(w, "schema-2 migration dry run: %s\n", result)
	if report.Reason != "" {
		fmt.Fprintf(w, "  reason: %s (%s)\n", report.Reason, report.Code)
		for _, path := range report.Paths {
			fmt.Fprintf(w, "    differs: %s\n", path)
		}
	}
	for _, site := range report.Sites {
		fmt.Fprintf(w, "site %s (probe %s): rf managed %v %s\n", site.ID, site.Probe, site.RFManaged, site.RFReason)
		fmt.Fprintf(w, "  typed: %s\n", site.Typed)
		for _, path := range site.ResidualPaths {
			fmt.Fprintf(w, "  residual: %s\n", path)
		}
	}
	for _, network := range report.Networks {
		fmt.Fprintf(w, "network %s (%s): security %s, bssid_index %d (%s)\n", network.ID, network.SSID, network.Security, network.BSSIDIndex, network.BSSIDSource)
		for _, path := range network.ResidualPaths {
			fmt.Fprintf(w, "  residual: %s\n", path)
		}
	}
	for _, ap := range report.APs {
		if !ap.Compiled {
			fmt.Fprintf(w, "AP %s: no site, not compiled\n", ap.ID)
			continue
		}
		fmt.Fprintf(w, "AP %s: equal %v, local files %s, rf_override %s\n  old %s\n  new %s\n", ap.ID, ap.Equal, ap.LocalFiles, ap.RFOverride, ap.OldHash, ap.NewHash)
		for _, path := range ap.ResidualPaths {
			fmt.Fprintf(w, "  residual: %s\n", path)
		}
		for _, path := range ap.Mismatch {
			fmt.Fprintf(w, "  mismatch: %s\n", path)
		}
	}
	for _, check := range report.H0 {
		fmt.Fprintf(w, "H0 %s: recorded %s (v%d), recomputed %s, current %s, matches %v\n",
			check.APID, check.Recorded, check.RecordedVer, check.Recomputed, check.Current, check.MatchesNow)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

func dbDecodeSecret(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if raw, err := hex.DecodeString(text); err == nil && len(raw) == model.SecretSize {
		return raw, nil
	}
	if raw, err := base64.StdEncoding.DecodeString(text); err == nil && len(raw) == model.SecretSize {
		return raw, nil
	}
	return nil, fmt.Errorf("the AP secret must be %d bytes, hex or base64", model.SecretSize)
}

// dbRehearseCopy writes a copy of a production backup for the Lab
// rehearsal (design §8.5): only one site is kept, its SSIDs get a suffix,
// and the production AP's record is replaced by the Lab AP's. Records are
// edited member by member in the raw bucket JSON, so every other byte of
// the legacy records stays as it was.
func dbRehearseCopy(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("dismist db rehearse-copy", flag.ContinueOnError)
	flags.SetOutput(output)
	siteName := flags.String("site", "", "the site to keep (name or ID)")
	out := flags.String("out", "", "the rehearsal database to write (must not exist)")
	suffix := flags.String("ssid-suffix", "-LAB", "appended to every SSID of the site")
	apMap := flags.String("ap", "", "OLD=NEW: replace the production AP's ID with the Lab AP's")
	secretArg := flags.String("secret", "", "the Lab AP's 32-byte secret, hex or base64 (with --ap)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *siteName == "" || *out == "" || flags.NArg() != 1 {
		return errors.New("usage: dismist db rehearse-copy --site NAME --out FILE [--ssid-suffix -LAB] [--ap OLD=NEW --secret HEX|BASE64] BACKUP")
	}
	var oldAP, newAP string
	var secret []byte
	if *apMap != "" {
		var ok bool
		oldAP, newAP, ok = strings.Cut(*apMap, "=")
		if !ok || oldAP == "" || newAP == "" {
			return errors.New("--ap must be OLD=NEW")
		}
		var err error
		if secret, err = dbDecodeSecret(*secretArg); err != nil {
			return err
		}
	}
	if err := dbCopyFile(flags.Arg(0), *out); err != nil {
		return err
	}
	db, err := bolt.Open(*out, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		_ = os.Remove(*out)
		return err
	}
	defer db.Close()
	var summary []string
	err = db.Update(func(tx *bolt.Tx) error {
		sites := tx.Bucket([]byte(model.BucketSites))
		aps := tx.Bucket([]byte(model.BucketAPs))
		networks := tx.Bucket([]byte(model.BucketNetworks))
		if sites == nil || aps == nil || networks == nil {
			return errors.New("not a dismist database")
		}
		keep := ""
		var deleteSites [][]byte
		if err := sites.ForEach(func(key, raw []byte) error {
			var record map[string]json.RawMessage
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			var id, name string
			_ = json.Unmarshal(record["id"], &id)
			_ = json.Unmarshal(record["name"], &name)
			if id == *siteName || name == *siteName {
				keep = id
				return nil
			}
			deleteSites = append(deleteSites, append([]byte(nil), key...))
			return nil
		}); err != nil {
			return err
		}
		if keep == "" {
			return fmt.Errorf("site %q not found", *siteName)
		}
		for _, key := range deleteSites {
			if err := sites.Delete(key); err != nil {
				return err
			}
		}
		summary = append(summary, fmt.Sprintf("kept site %s, removed %d other sites", keep, len(deleteSites)))
		// Networks: drop other sites', rename this site's SSIDs.
		var dropNetworks [][]byte
		renamed := map[string][]byte{}
		if err := networks.ForEach(func(key, raw []byte) error {
			var record map[string]json.RawMessage
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			var siteID, ssid string
			_ = json.Unmarshal(record["site_id"], &siteID)
			_ = json.Unmarshal(record["ssid"], &ssid)
			if siteID != keep {
				dropNetworks = append(dropNetworks, append([]byte(nil), key...))
				return nil
			}
			ssid += *suffix
			if len(ssid) > 32 {
				return fmt.Errorf("SSID %q is longer than 32 bytes with the suffix", ssid)
			}
			record["ssid"], _ = json.Marshal(ssid)
			encoded, err := json.Marshal(record)
			if err != nil {
				return err
			}
			renamed[string(key)] = encoded
			return nil
		}); err != nil {
			return err
		}
		for _, key := range dropNetworks {
			if err := networks.Delete(key); err != nil {
				return err
			}
		}
		for key, raw := range renamed {
			if err := networks.Put([]byte(key), raw); err != nil {
				return err
			}
		}
		summary = append(summary, fmt.Sprintf("renamed %d SSIDs with %q, removed %d networks", len(renamed), *suffix, len(dropNetworks)))
		// APs: keep only the mapped AP of the kept site.
		var dropAPs [][]byte
		var moved []byte
		if err := aps.ForEach(func(key, raw []byte) error {
			var record map[string]json.RawMessage
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			var id, siteID string
			_ = json.Unmarshal(record["id"], &id)
			_ = json.Unmarshal(record["site_id"], &siteID)
			if oldAP != "" && id == oldAP && siteID == keep {
				record["id"], _ = json.Marshal(newAP)
				record["secret"], _ = json.Marshal(secret)
				delete(record, "management_ip")
				delete(record, "secret_file")
				encoded, err := json.Marshal(record)
				if err != nil {
					return err
				}
				moved = encoded
			}
			dropAPs = append(dropAPs, append([]byte(nil), key...))
			return nil
		}); err != nil {
			return err
		}
		for _, key := range dropAPs {
			if err := aps.Delete(key); err != nil {
				return err
			}
		}
		if oldAP != "" && moved == nil {
			return fmt.Errorf("AP %s is not in site %s", oldAP, keep)
		}
		if moved != nil {
			if err := aps.Put([]byte(newAP), moved); err != nil {
				return err
			}
			summary = append(summary, fmt.Sprintf("AP %s replaced by %s (secret set, pin cleared)", oldAP, newAP))
		}
		// Per-AP buckets: move the production AP's last-known status (its
		// field-32 VAP list measures the BSSID indexes), drop the rest.
		if status := tx.Bucket([]byte(model.BucketAPStatus)); status != nil {
			var record map[string]json.RawMessage
			raw := status.Get([]byte(oldAP))
			if oldAP != "" && raw != nil && json.Unmarshal(raw, &record) == nil {
				record["ap_id"], _ = json.Marshal(newAP)
				encoded, _ := json.Marshal(record)
				if err := dbClearBucket(tx, model.BucketAPStatus); err != nil {
					return err
				}
				if err := tx.Bucket([]byte(model.BucketAPStatus)).Put([]byte(newAP), encoded); err != nil {
					return err
				}
			} else if err := dbClearBucket(tx, model.BucketAPStatus); err != nil {
				return err
			}
		}
		for _, name := range []string{model.BucketAPConfig, model.BucketSessions, model.BucketScans} {
			if err := dbClearBucket(tx, name); err != nil {
				return err
			}
		}
		if meta := tx.Bucket([]byte(model.BucketMeta)); meta != nil {
			if err := meta.Delete([]byte(model.MetaH0)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		_ = os.Remove(*out)
		return err
	}
	for _, line := range summary {
		fmt.Fprintln(output, line)
	}
	fmt.Fprintf(output, "rehearsal copy written to %s\n", *out)
	return nil
}

func dbClearBucket(tx *bolt.Tx, name string) error {
	if tx.Bucket([]byte(name)) == nil {
		return nil
	}
	if err := tx.DeleteBucket([]byte(name)); err != nil {
		return err
	}
	_, err := tx.CreateBucket([]byte(name))
	return err
}
