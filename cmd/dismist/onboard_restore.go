package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const restoreUsage = `usage: dismist ap33 restore [ssh flags] [user@]host --backup DIR [--no-reboot] [--dry-run] [--yes]`

const (
	restoreRebootReason = "dismist ap33 restore"
	// restoreAsideRoot is where restore moves files that were not in the
	// backup (it never deletes anything on the AP).
	restoreAsideRoot = "/rw/etc/mist/dismist-restore"
)

// restoreManaged are the /rw/etc/mist/ep-control files onboard changes and
// restore therefore puts back, in addition to every local_* file.
var restoreManaged = []string{"cloud.ini", "cacerts-production.pem"}

// restoreAction is one change restore makes to one file.
type restoreAction struct {
	Name string
	// Write is the backup's copy to put back; nil means the file was not in
	// the backup and is moved aside instead.
	Write []byte
}

// runRestore is `dismist ap33 restore` (design §3.15): put back the files
// onboard changes (cloud.ini, the CA bundle and the local_* files) from a
// backup that onboard took, then reboot so ep-control reads them. Files
// that were not in the backup are moved aside, never deleted. The EEPROM
// image in the backup is never written back.
func runRestore(args []string, env ap33Env) error {
	flags := flag.NewFlagSet("dismist ap33 restore", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	var ssh sshFlags
	ssh.register(flags)
	backupDir := flags.String("backup", "", "backup directory written by `dismist ap33 onboard` (<backup-dir>/<id>/<time>)")
	noReboot := flags.Bool("no-reboot", false, "put the files back but do not reboot the AP")
	dryRun := flags.Bool("dry-run", false, "print what would change; write nothing")
	yes := flags.Bool("yes", false, "do not ask before changing the AP and rebooting it")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 || strings.TrimSpace(*backupDir) == "" {
		return errors.New(restoreUsage)
	}
	backup, err := loadBackup(expandHome(*backupDir))
	if err != nil {
		return withExit(exitPreflight, err)
	}
	step := func(format string, args ...any) { fmt.Fprintf(env.stdout, format+"\n", args...) }
	step("Backup: %s of AP %s, taken %s (firmware %s)", backup.Dir, backup.Manifest.APID,
		backup.Manifest.CreatedAt.UTC().Format(time.RFC3339), orUnknown(backup.Manifest.Firmware))

	client, target, err := connectAP(env, &ssh, positional[0])
	if err != nil {
		return withExit(exitPreflight, err)
	}
	defer client.Close()
	report, err := collectInspect(env.ctx, client, inspectOptions{Level: inspectFull})
	if err != nil {
		return exitf(exitPreflight, "preflight %s: %w", target, err)
	}
	if report.UID == nil || *report.UID != 0 {
		return exitf(exitPreflight, "the SSH login on %s is not root (id -u is not 0); nothing was changed", target)
	}
	if report.ID != backup.Manifest.APID {
		return exitf(exitPreflight, "%s is AP %s, but the backup is of AP %s; nothing was changed", target, orUnknown(report.ID), backup.Manifest.APID)
	}

	actions, err := planRestore(env, client, report, backup)
	if err != nil {
		return withExit(exitPreflight, err)
	}
	if len(actions) == 0 {
		step("Nothing to restore: %s already matches the backup", apEPControlRW)
		return nil
	}
	aside := fmt.Sprintf("%s/%s", restoreAsideRoot, env.now().UTC().Format("20060102T150405Z"))
	for _, action := range actions {
		if action.Write != nil {
			step("Restore %s/%s (%d bytes from the backup)", apEPControlRW, action.Name, len(action.Write))
		} else {
			step("Move %s/%s to %s/ (it was not on the AP when the backup was taken)", apEPControlRW, action.Name, aside)
		}
	}
	if *dryRun {
		step("Dry run: nothing was written")
		return nil
	}
	if !*yes {
		question := fmt.Sprintf("Change %d file(s) on AP %s", len(actions), report.ID)
		if !*noReboot {
			question += " and reboot it (its clients lose Wi-Fi for about two minutes)"
		}
		ok, err := env.ask(question + "?")
		if err != nil {
			return withExit(exitPreflight, err)
		}
		if !ok {
			return exitf(exitPreflight, "not confirmed: re-run with --yes to restore %s; nothing was changed", report.ID)
		}
	}
	for _, action := range actions {
		dest := apEPControlRW + "/" + action.Name
		if action.Write == nil {
			if err := moveAPFile(env.ctx, client, dest, aside+"/"+action.Name); err != nil {
				return withExit(exitPreflight, err)
			}
			step("Moved %s aside", dest)
			continue
		}
		if err := writeAPFile(env.ctx, client, dest, action.Write); err != nil {
			return withExit(exitPreflight, err)
		}
		step("Restored %s (sha256 verified on the AP)", dest)
	}
	step("The AP's registration on the controller is unchanged; delete it there if the AP is leaving dismist.")
	if *noReboot {
		step("Reboot: skipped (--no-reboot). The AP reads the restored files at its next restart: `dismist ap33 ssh %s reboot --yes`", target.Host)
		return nil
	}
	if err := requestReboot(env.ctx, client, restoreRebootReason); err != nil {
		return exitf(exitPreflight, "reboot %s: %w", report.ID, err)
	}
	step("Reboot: requested (reason %q). The AP comes back on the endpoint and trust it had when the backup was taken.", restoreRebootReason)
	return nil
}

// planRestore compares the AP's current files with the backup. Order
// matters if the run is interrupted: local files first, then a cloud.ini
// that goes away (so the AP falls back to its stock endpoint and bundle),
// then the CA bundle, then a cloud.ini that is written.
func planRestore(env ap33Env, client apStreamer, report *InspectReport, backup *apBackup) ([]restoreAction, error) {
	names := map[string]bool{}
	for _, name := range restoreManaged {
		names[name] = true
	}
	for name := range backup.EPControl {
		if strings.HasPrefix(name, "local_") {
			names[name] = true
		}
	}
	for _, name := range report.RWFiles {
		if strings.HasPrefix(name, "local_") {
			names[name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	var locals, iniAside, bundle, iniWrite []restoreAction
	for _, name := range ordered {
		if strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
			return nil, fmt.Errorf("refusing file name %q from the backup", name)
		}
		saved, inBackup := backup.EPControl[name]
		present := slices.Contains(report.RWFiles, name)
		var current []byte
		if present {
			data, ok, err := readAPFile(env.ctx, client, apEPControlRW+"/"+name)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("cannot read %s/%s on the AP", apEPControlRW, name)
			}
			current = data
		}
		var action *restoreAction
		switch {
		case inBackup && (!present || !bytes.Equal(current, saved)):
			action = &restoreAction{Name: name, Write: nonNil(saved)}
		case !inBackup && present:
			action = &restoreAction{Name: name}
		}
		if action == nil {
			continue
		}
		switch {
		case strings.HasPrefix(name, "local_"):
			locals = append(locals, *action)
		case name == "cloud.ini" && action.Write == nil:
			iniAside = append(iniAside, *action)
		case name == "cloud.ini":
			iniWrite = append(iniWrite, *action)
		default:
			bundle = append(bundle, *action)
		}
	}
	return slices.Concat(locals, iniAside, bundle, iniWrite), nil
}

func nonNil(data []byte) []byte {
	if data == nil {
		return []byte{}
	}
	return data
}
