package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	backupTarName      = "rw.tar.gz"
	backupEEPROMName   = "eeprom.bin"
	backupManifestName = "manifest.json"
	backupSumsName     = "SHA256SUMS"
	// backupTarLimit bounds the /rw tarball; an AP33's /rw is a few
	// hundred KiB compressed.
	backupTarLimit   = 64 << 20
	backupTarTimeout = 5 * time.Minute
	// secretOffset and secretSize locate the AP's authentication secret in
	// its EEPROM (observed AP33 layout).
	secretOffset = 0x400
	secretSize   = 32
)

// backupManifest describes one backup directory. It holds no secret.
type backupManifest struct {
	Version    int         `json:"version"`
	Tool       string      `json:"tool"`
	APID       string      `json:"ap_id"`
	Target     string      `json:"target"`
	HostKey    string      `json:"host_key"`
	CreatedAt  time.Time   `json:"created_at"`
	Firmware   string      `json:"firmware"`
	BootConfig string      `json:"bootcfg,omitempty"`
	RootFS     string      `json:"root_fs,omitempty"`
	EEPROMPath string      `json:"eeprom_path"`
	Files      []string    `json:"files"`
	LocalFiles []LocalFile `json:"local_files"`
}

// apBackup is a backup taken (or loaded) for one AP.
type apBackup struct {
	Dir      string
	Manifest backupManifest
	TarSize  int64
	EEPROM   []byte
	// EPControl holds the regular files of /rw/etc/mist/ep-control in the
	// tarball, by name.
	EPControl map[string][]byte
}

// defaultBackupRoot is $XDG_DATA_HOME/dismist/backups, else
// ~/.local/share/dismist/backups. Backups hold the AP's EEPROM (with its
// secret), so they live in a private per-user directory, never a checkout.
func defaultBackupRoot() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "dismist", "backups"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the home directory for backups (pass --backup-dir): %w", err)
	}
	return filepath.Join(home, ".local", "share", "dismist", "backups"), nil
}

// mkdirPrivate creates dir and its missing parents with mode 0700.
func mkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// apStreamer is the SSH session onboard needs beyond Run.
type apStreamer interface {
	remoteRunner
	RunInput(ctx context.Context, command string, input []byte) (remoteResult, error)
	Stream(ctx context.Context, command string, w io.Writer, limit int64, timeout time.Duration) (remoteResult, error)
}

// createBackup streams /rw as a tarball and the full EEPROM into
// root/<id>/<UTC time>/ (design §3.15 step 3), checks both, and writes the
// manifest and SHA256SUMS. A failed backup leaves no directory behind.
func createBackup(ctx context.Context, runner apStreamer, report *InspectReport, root, eepromPath string, hostKey ssh.PublicKey, now time.Time) (*apBackup, error) {
	apDir := filepath.Join(root, report.ID)
	if err := mkdirPrivate(apDir); err != nil {
		return nil, fmt.Errorf("backup directory: %w", err)
	}
	stamp := now.UTC().Format("20060102T150405Z")
	dir := filepath.Join(apDir, stamp)
	for suffix := 2; ; suffix++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || suffix > 50 {
			return nil, fmt.Errorf("backup directory: %w", err)
		}
		dir = filepath.Join(apDir, fmt.Sprintf("%s-%d", stamp, suffix))
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()

	backup := &apBackup{Dir: dir}
	tarFile, err := os.OpenFile(filepath.Join(dir, backupTarName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	writer := bufio.NewWriter(tarFile)
	result, err := runner.Stream(ctx, "tar -C /rw -czf - .", writer, backupTarLimit, backupTarTimeout)
	if flushErr := writer.Flush(); err == nil {
		err = flushErr
	}
	if closeErr := tarFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("back up /rw: %w", err)
	}
	if result.ExitStatus != 0 {
		return nil, fmt.Errorf("back up /rw: tar exited %d: %s", result.ExitStatus, strings.TrimSpace(string(result.Stderr)))
	}
	tarData, err := os.ReadFile(filepath.Join(dir, backupTarName))
	if err != nil {
		return nil, err
	}
	backup.TarSize = int64(len(tarData))
	if backup.EPControl, err = readEPControlFromTar(tarData); err != nil {
		return nil, fmt.Errorf("back up /rw: the tarball does not read back: %w", err)
	}
	for _, local := range report.LocalFiles {
		data, ok := backup.EPControl[local.Name]
		if !ok || sha256Hex(data) != local.SHA256 {
			return nil, fmt.Errorf("back up /rw: %s is missing from the tarball or changed while it was read", local.Name)
		}
	}

	eeprom, err := runner.Run(ctx, "cat "+shellQuote(eepromPath))
	if err != nil {
		return nil, fmt.Errorf("back up the EEPROM: %w", err)
	}
	if eeprom.ExitStatus != 0 || len(eeprom.Stdout) != eepromSize {
		return nil, fmt.Errorf("back up the EEPROM: read %d bytes from %s, expected %d", len(eeprom.Stdout), eepromPath, eepromSize)
	}
	backup.EEPROM = eeprom.Stdout
	if err := writePrivateFile(filepath.Join(dir, backupEEPROMName), backup.EEPROM); err != nil {
		return nil, err
	}

	backup.Manifest = backupManifest{
		Version: 1, Tool: "dismist ap33 onboard", APID: report.ID, Target: report.Target,
		CreatedAt: now.UTC(), Firmware: report.Firmware.Version, RootFS: report.RootFS,
		EEPROMPath: eepromPath, Files: []string{backupTarName, backupEEPROMName}, LocalFiles: report.LocalFiles,
	}
	if hostKey != nil {
		backup.Manifest.HostKey = ssh.FingerprintSHA256(hostKey)
	}
	if report.BootConfig != nil {
		backup.Manifest.BootConfig = report.BootConfig.Raw
	}
	manifest, err := json.MarshalIndent(backup.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writePrivateFile(filepath.Join(dir, backupManifestName), append(manifest, '\n')); err != nil {
		return nil, err
	}
	sums := fmt.Sprintf("%s  %s\n%s  %s\n%s  %s\n",
		sha256Hex(tarData), backupTarName, sha256Hex(backup.EEPROM), backupEEPROMName, sha256Hex(append(manifest, '\n')), backupManifestName)
	if err := writePrivateFile(filepath.Join(dir, backupSumsName), []byte(sums)); err != nil {
		return nil, err
	}
	if hostKey != nil {
		if err := recordBackupHostKey(filepath.Join(apDir, "known_hosts"), report.Target, hostKey); err != nil {
			return nil, err
		}
	}
	success = true
	return backup, nil
}

// recordBackupHostKey keeps the AP's host key next to its backups (design
// §3.15 step 1), so a restore after a factory reset can tell the old key.
func recordBackupHostKey(file, target string, key ssh.PublicKey) error {
	line := knownHostLine(target, key)
	existing, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if bytes.Contains(existing, []byte(line)) {
		return nil
	}
	handle, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := handle.WriteString(line); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}

func knownHostLine(target string, key ssh.PublicKey) string {
	host := target
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return fmt.Sprintf("%s %s", host, string(ssh.MarshalAuthorizedKey(key)))
}

// readEPControlFromTar reads every entry of a gzip tarball (so a truncated
// stream fails) and returns the regular files directly inside
// ./etc/mist/ep-control.
func readEPControlFromTar(data []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	reader := tar.NewReader(gz)
	files := map[string][]byte{}
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		entries++
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		content, err := io.ReadAll(io.LimitReader(reader, backupTarLimit))
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if dir, base := path.Split(name); dir == "etc/mist/ep-control/" && base != "" {
			files[base] = content
		}
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if entries == 0 {
		return nil, errors.New("empty tarball")
	}
	return files, nil
}

// loadBackup reads a backup directory for restore: the manifest, every file
// checked against SHA256SUMS, and the ep-control files from the tarball.
func loadBackup(dir string) (*apBackup, error) {
	sums, err := os.ReadFile(filepath.Join(dir, backupSumsName))
	if err != nil {
		return nil, fmt.Errorf("%s is not a dismist AP backup: %w", dir, err)
	}
	listed := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if !ok || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("%s: malformed line %q", backupSumsName, line)
		}
		listed[name] = sum
	}
	for _, name := range []string{backupTarName, backupManifestName} {
		if _, ok := listed[name]; !ok {
			return nil, fmt.Errorf("%s does not list %s", backupSumsName, name)
		}
	}
	contents := map[string][]byte{}
	names := make([]string, 0, len(listed))
	for name := range listed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if sha256Hex(data) != listed[name] {
			return nil, fmt.Errorf("%s does not match %s: the backup is damaged", name, backupSumsName)
		}
		contents[name] = data
	}
	backup := &apBackup{Dir: dir, EEPROM: contents[backupEEPROMName], TarSize: int64(len(contents[backupTarName]))}
	if err := json.Unmarshal(contents[backupManifestName], &backup.Manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", backupManifestName, err)
	}
	if backup.Manifest.APID == "" {
		return nil, fmt.Errorf("%s names no AP", backupManifestName)
	}
	if backup.EPControl, err = readEPControlFromTar(contents[backupTarName]); err != nil {
		return nil, fmt.Errorf("%s: %w", backupTarName, err)
	}
	return backup, nil
}

// apSecret returns the 32-byte authentication secret from an EEPROM image.
// An erased (all 0x00 or all 0xFF) secret means the EEPROM is not the AP's
// provisioned one.
func apSecret(eeprom []byte) ([]byte, error) {
	if len(eeprom) != eepromSize {
		return nil, fmt.Errorf("EEPROM image is %d bytes, expected %d", len(eeprom), eepromSize)
	}
	secret := bytes.Clone(eeprom[secretOffset : secretOffset+secretSize])
	if bytes.Count(secret, []byte{0}) == secretSize || bytes.Count(secret, []byte{0xff}) == secretSize {
		return nil, errors.New("the EEPROM holds no secret at offset 0x400 (all bytes equal): this AP was not provisioned the way AP33s are")
	}
	return secret, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
