package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const ap33Usage = `usage:
  dismist ap33 inspect [ssh flags] [--json] [--log-lines N] [user@]host
  dismist ap33 ssh [ssh flags] [user@]host status [--json]
  dismist ap33 ssh [ssh flags] [user@]host reboot --yes [--reason TEXT | --no-reason]
  dismist ap33 serial-response <challenge>
  dismist ap33 prepare-trust --stock-dir DIR --controller-ca FILE --output DIR

ssh flags: -i KEY, -p PORT, -l USER, -o Key=Value (repeatable), -F ssh_config,
--known-hosts FILE. The user defaults to root; host keys are trusted on first
use and kept in ~/.config/dismist/known_hosts.`

// ap33Env carries the command's streams so tests can capture them.
type ap33Env struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	now    func() time.Time
}

func runAP33(args []string, output io.Writer) error {
	return runAP33With(args, ap33Env{ctx: context.Background(), stdout: output, stderr: os.Stderr, now: time.Now})
}

func runAP33With(args []string, env ap33Env) error {
	if len(args) == 0 {
		return errors.New(ap33Usage)
	}
	switch args[0] {
	case "inspect":
		return runInspect(args[1:], env)
	case "ssh":
		return runAPSSH(args[1:], env)
	case "serial-response":
		return runSerialResponse(args[1:], env.stdout)
	case "prepare-trust":
		return runPrepareTrust(args[1:], env.stdout)
	}
	return errors.New(ap33Usage)
}

// connectAP parses the shared SSH flags for destination and connects.
func connectAP(env ap33Env, sshFlags *sshFlags, destination string) (*apSSH, sshTarget, error) {
	options, target, err := sshFlags.apply(destination, env.stderr)
	if err != nil {
		return nil, sshTarget{}, err
	}
	client, err := dialAP(env.ctx, options, target, env.stderr)
	if err != nil {
		return nil, sshTarget{}, err
	}
	return client, target, nil
}

func runInspect(args []string, env ap33Env) error {
	const usage = "usage: dismist ap33 inspect [ssh flags] [--json] [--log-lines N] [user@]host"
	flags := flag.NewFlagSet("dismist ap33 inspect", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	var ssh sshFlags
	ssh.register(flags)
	jsonOutput := flags.Bool("json", false, "print the report as JSON")
	logLines := flags.Int("log-lines", 50, "lines to show from each AP log; 0 skips the logs")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(usage)
	}
	if *logLines < 0 || *logLines > 5000 {
		return errors.New("--log-lines must be between 0 and 5000")
	}
	client, target, err := connectAP(env, &ssh, positional[0])
	if err != nil {
		return err
	}
	defer client.Close()
	report, err := collectInspect(env.ctx, client, inspectOptions{Level: inspectFull, LogLines: *logLines})
	if err != nil {
		return fmt.Errorf("inspect %s: %w", target, err)
	}
	report.Target = target.String()
	report.HostKey = client.HostKeyFingerprint()
	return printReport(env, report, *jsonOutput)
}

func printReport(env ap33Env, report *InspectReport, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(env.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return writeInspectText(env.stdout, report, env.now())
}

const defaultRebootReason = "dismist ap33 ssh reboot"

func runAPSSH(args []string, env ap33Env) error {
	const usage = "usage: dismist ap33 ssh [ssh flags] [user@]host status [--json] | reboot --yes [--reason TEXT | --no-reason]"
	flags := flag.NewFlagSet("dismist ap33 ssh", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	var ssh sshFlags
	ssh.register(flags)
	jsonOutput := flags.Bool("json", false, "status: print the report as JSON")
	yes := flags.Bool("yes", false, "reboot: confirm the reboot")
	reason := flags.String("reason", defaultRebootReason, "reboot: reason recorded for the AP to report as LastRebootReason")
	noReason := flags.Bool("no-reason", false, "reboot: do not record a reboot reason")
	positional, err := parseInterspersed(flags, args)
	if err != nil {
		return err
	}
	if len(positional) != 2 {
		return errors.New(usage)
	}
	destination, action := positional[0], positional[1]
	switch action {
	case "status":
		client, target, err := connectAP(env, &ssh, destination)
		if err != nil {
			return err
		}
		defer client.Close()
		report, err := collectInspect(env.ctx, client, inspectOptions{Level: inspectStatus})
		if err != nil {
			return fmt.Errorf("status %s: %w", target, err)
		}
		report.Target = target.String()
		report.HostKey = client.HostKeyFingerprint()
		return printReport(env, report, *jsonOutput)
	case "reboot":
		recorded := strings.TrimSpace(*reason)
		if *noReason {
			recorded = ""
		}
		if err := validRebootReason(recorded); err != nil {
			return err
		}
		if !*yes {
			return errors.New("reboot disconnects every client on the AP for about two minutes; re-run with --yes to confirm")
		}
		client, target, err := connectAP(env, &ssh, destination)
		if err != nil {
			return err
		}
		defer client.Close()
		return rebootAP(env, client, target, recorded)
	}
	return errors.New(usage)
}

func validRebootReason(reason string) error {
	if len(reason) > 120 {
		return errors.New("--reason must be at most 120 characters")
	}
	for _, r := range reason {
		if r < 0x20 || r > 0x7e {
			return errors.New("--reason must be printable ASCII on one line")
		}
	}
	return nil
}

// rebootAP records the reason (unless the AP still holds an unreported one)
// and reboots. The firmware creates last_reboot_info.txt with "unknown" at
// boot when it is missing, so without this an operator reboot would look
// like an unexplained one in the AP's next hello.
func rebootAP(env ap33Env, client *apSSH, target sshTarget, reason string) error {
	before, err := collectInspect(env.ctx, client, inspectOptions{Level: inspectStatus})
	if err != nil {
		return fmt.Errorf("read AP state before reboot: %w", err)
	}
	state := "AP " + orUnknown(before.ID)
	if before.BootCount != nil {
		state += fmt.Sprintf(", boot count %d", *before.BootCount)
	}
	if before.UptimeSeconds != nil {
		state += ", uptime " + formatUptime(time.Duration(*before.UptimeSeconds*float64(time.Second)))
	}
	fmt.Fprintf(env.stdout, "Before reboot: %s\n", state)
	if reason != "" && before.LastReboot != nil && !before.LastReboot.Uploaded {
		fmt.Fprintf(env.stderr, "warning: %s still holds the unreported reason %q of the previous reboot; leaving it in place\n", before.LastReboot.File, before.LastReboot.Reason)
	}
	result, err := client.Run(env.ctx, rebootCommand(reason))
	switch {
	case errors.Is(err, errConnectionLost):
	case err != nil:
		return fmt.Errorf("reboot %s: %w", target, err)
	case result.ExitStatus != 0:
		return fmt.Errorf("reboot %s: exit status %d: %s", target, result.ExitStatus, strings.TrimSpace(string(result.Stderr)))
	}
	fmt.Fprintf(env.stdout, "Reboot requested for %s. The AP is usually back on the controller within two minutes; check its boot count with `dismist ap33 ssh %s status`.\n", target, target.Host)
	return nil
}

// rebootCommand writes "<unix>::reboot/<reason>" (the format ep-control
// reports as LastRebootReason) only when no unreported reason is pending,
// then reboots whether or not the write worked.
func rebootCommand(reason string) string {
	if reason == "" {
		return "reboot"
	}
	file := apEPControlRW + "/last_reboot_info.txt"
	temp := file + ".dismist"
	return fmt.Sprintf(`[ -e %[1]s ] || { printf '%%s::%%s' "$(date +%%s)" %[3]s > %[2]s && mv %[2]s %[1]s; }; reboot`,
		shellQuote(file), shellQuote(temp), shellQuote("reboot/"+reason))
}
