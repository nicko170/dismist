package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Writes to the AP go through these helpers only. Each file is written to a
// temporary name in the same directory with mode 0600, renamed over the
// destination (so ep-control never reads half a file), then read back.

// writeAPFile installs data at dest on the AP and verifies it there.
func writeAPFile(ctx context.Context, runner apStreamer, dest string, data []byte) error {
	if !strings.HasPrefix(dest, "/rw/") {
		return fmt.Errorf("refusing to write %s: dismist only writes under /rw", dest)
	}
	temp := dest + ".dismist-new"
	command := fmt.Sprintf("umask 077 && mkdir -p %s && cat > %s && chmod 600 %s && mv %s %s",
		shellQuote(path.Dir(dest)), shellQuote(temp), shellQuote(temp), shellQuote(temp), shellQuote(dest))
	result, err := runner.RunInput(ctx, command, data)
	if err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if result.ExitStatus != 0 {
		return fmt.Errorf("write %s: exit status %d: %s", dest, result.ExitStatus, strings.TrimSpace(string(result.Stderr)))
	}
	return verifyAPFile(ctx, runner, dest, data)
}

// verifyAPFile compares dest's SHA-256 on the AP with want's. Without a
// sha256sum applet it reads the file back instead.
func verifyAPFile(ctx context.Context, runner apStreamer, dest string, want []byte) error {
	result, err := runner.Run(ctx, "sha256sum "+shellQuote(dest))
	if err != nil {
		return fmt.Errorf("verify %s: %w", dest, err)
	}
	if result.ExitStatus == 0 {
		fields := strings.Fields(string(result.Stdout))
		if len(fields) == 0 || fields[0] != sha256Hex(want) {
			return fmt.Errorf("verify %s: the AP's copy does not match what was sent", dest)
		}
		return nil
	}
	if result.ExitStatus != 127 {
		return fmt.Errorf("verify %s: sha256sum exited %d: %s", dest, result.ExitStatus, strings.TrimSpace(string(result.Stderr)))
	}
	readBack, err := runner.Run(ctx, "cat "+shellQuote(dest))
	if err != nil {
		return fmt.Errorf("verify %s: %w", dest, err)
	}
	if readBack.ExitStatus != 0 || !bytes.Equal(readBack.Stdout, want) {
		return fmt.Errorf("verify %s: the AP's copy does not match what was sent", dest)
	}
	return nil
}

// moveAPFile renames src to dst on the AP (creating dst's directory). It is
// how dismist takes a file out of use without deleting it.
func moveAPFile(ctx context.Context, runner apStreamer, src, dst string) error {
	if !strings.HasPrefix(src, "/rw/") || !strings.HasPrefix(dst, "/rw/") {
		return fmt.Errorf("refusing to move %s to %s: dismist only changes /rw", src, dst)
	}
	command := fmt.Sprintf("mkdir -p %s && mv %s %s", shellQuote(path.Dir(dst)), shellQuote(src), shellQuote(dst))
	result, err := runner.Run(ctx, command)
	if err != nil {
		return fmt.Errorf("move %s: %w", src, err)
	}
	if result.ExitStatus != 0 {
		return fmt.Errorf("move %s to %s: exit status %d: %s", src, dst, result.ExitStatus, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}

// readAPFile cats an absolute path; ok is false when it is missing.
func readAPFile(ctx context.Context, runner remoteRunner, file string) ([]byte, bool, error) {
	result, err := runner.Run(ctx, "cat "+shellQuote(file))
	if err != nil {
		return nil, false, err
	}
	return result.Stdout, result.ExitStatus == 0, nil
}

// requestReboot records reason (see rebootCommand) and reboots. The AP
// drops the session, which is the expected result.
func requestReboot(ctx context.Context, runner remoteRunner, reason string) error {
	result, err := runner.Run(ctx, rebootCommand(reason))
	switch {
	case errors.Is(err, errConnectionLost):
		return nil
	case err != nil:
		return err
	case result.ExitStatus != 0:
		return fmt.Errorf("reboot: exit status %d: %s", result.ExitStatus, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
