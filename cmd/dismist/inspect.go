package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Paths on the AP33 (0.8 to 0.15). Everything inspect runs is a read: cat,
// ls, tail, wc, pidof, dmesg, logread and bootcfg without arguments.
const (
	apEPControlRW   = "/rw/etc/mist/ep-control"
	apEPControlRO   = "/etc/mist/ep-control"
	apVersionFile   = "/etc/mist/version"
	apEPControlLog  = "/var/log/ep-control.log"
	apSystemLog     = "/var/log/messages"
	apErrorReports  = "/var/log/errors"
	apEEPROMGlob    = "/sys/bus/i2c/devices/*/eeprom"
	apBootStateFile = "/proc/device-tree/chosen/bootstate"
	// eepromSize is the AP33 EEPROM size; the onboarding secret lives at
	// offset 0x400. inspect only measures the size and never reads it out.
	eepromSize = 32768
)

// apIDInterfaces are the interfaces whose address can be the AP's ID. The
// design derives the ID from eth0; ep-control itself logs aximac1 (0.8) or
// vlan1 (0.15). inspect reports eth0 and warns if another one differs.
var apIDInterfaces = []string{"eth0", "aximac1", "vlan1"}

// inspectLevel selects how much inspect collects.
type inspectLevel int

const (
	inspectStatus inspectLevel = iota // identity, uptime, boot, cloud, local file names
	inspectFull                       // plus CA bundles, EEPROM, MTD, error reports, logs
)

type inspectOptions struct {
	Level    inspectLevel
	LogLines int
}

// InspectReport is what `dismist ap33 inspect` found. --json prints it.
type InspectReport struct {
	Target        string        `json:"target"`
	HostKey       string        `json:"host_key,omitempty"`
	CollectedAt   time.Time     `json:"collected_at"`
	UID           *int          `json:"uid,omitempty"`
	ID            string        `json:"id,omitempty"`
	Interfaces    []APInterface `json:"interfaces,omitempty"`
	Firmware      FirmwareInfo  `json:"firmware"`
	UptimeSeconds *float64      `json:"uptime_seconds,omitempty"`
	BootCount     *int          `json:"boot_count,omitempty"`
	BootConfig    *BootConfig   `json:"bootcfg,omitempty"`
	RootFS        string        `json:"root_fs,omitempty"`
	BootState     string        `json:"boot_state,omitempty"`
	LastReboot    *RebootInfo   `json:"last_reboot,omitempty"`
	EPControlPIDs []int         `json:"ep_control_pids"`
	EEPROMs       []EEPROMInfo  `json:"eeproms,omitempty"`
	MTD           []string      `json:"mtd,omitempty"`
	RWFiles       []string      `json:"rw_ep_control_files,omitempty"`
	LocalFiles    []LocalFile   `json:"local_files"`
	Cloud         *CloudConfig  `json:"cloud,omitempty"`
	CABundles     []CABundle    `json:"ca_bundles,omitempty"`
	ErrorReports  []string      `json:"error_reports,omitempty"`
	Logs          []LogTail     `json:"logs,omitempty"`
	Notable       []string      `json:"notable_lines,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type APInterface struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
}

type FirmwareInfo struct {
	Model     string `json:"model,omitempty"`
	Version   string `json:"version,omitempty"`
	BuildTime string `json:"build_time,omitempty"`
	FIPS      string `json:"fips,omitempty"`
}

// BootConfig decodes `bootcfg` (the boot byte at the start of the EEPROM):
// w = watchdog at boot, i = image slot to boot, c = boot counter that flips
// the slot when it reaches 0.
type BootConfig struct {
	Raw      string `json:"raw"`
	Watchdog bool   `json:"watchdog"`
	Image    int    `json:"image"`
	Counter  int    `json:"counter"`
}

// RebootInfo is /rw/etc/mist/ep-control/last_reboot_info.txt ("<unix>::<reason>").
// ep-control renames it to .uploaded once it has reported it to the cloud.
type RebootInfo struct {
	Time     *time.Time `json:"time,omitempty"`
	Reason   string     `json:"reason"`
	File     string     `json:"file"`
	Uploaded bool       `json:"uploaded"`
}

type EEPROMInfo struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}

// LocalFile is one /rw/etc/mist/ep-control/local_* file. ep-control merges
// local_defaults before the controller's config and the others after it, so
// they change what the AP runs. Only key paths are listed, never values:
// local_wlan_overrides can hold passphrases.
type LocalFile struct {
	Name       string   `json:"name"`
	Size       int      `json:"size"`
	SHA256     string   `json:"sha256"`
	Keys       []string `json:"keys,omitempty"`
	ParseError string   `json:"parse_error,omitempty"`
}

type CloudConfig struct {
	Path         string       `json:"path"`
	RWOverride   bool         `json:"rw_override"`
	Entries      []CloudEntry `json:"entries"`
	TLSRootCerts string       `json:"tls_rootcerts,omitempty"`
	ProxyHost    string       `json:"proxy_host,omitempty"`
}

type CloudEntry struct {
	URL           string `json:"url"`
	Manufacturing bool   `json:"manufacturing,omitempty"`
	RootCerts     string `json:"rootcerts,omitempty"`
	Insecure      bool   `json:"insecure,omitempty"`
}

type CABundle struct {
	Path         string     `json:"path"`
	Certificates []CertInfo `json:"certificates,omitempty"`
	Error        string     `json:"error,omitempty"`
}

type CertInfo struct {
	Subject  string    `json:"subject,omitempty"`
	Issuer   string    `json:"issuer,omitempty"`
	NotAfter time.Time `json:"not_after,omitzero"`
	SHA256   string    `json:"sha256"`
	Error    string    `json:"error,omitempty"`
}

type LogTail struct {
	Name   string   `json:"name"`
	Source string   `json:"source"`
	Lines  []string `json:"lines,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// collectInspect gathers the report over runner. Only a transport failure
// (or a non-root login) stops it; anything missing on the AP is a warning.
func collectInspect(ctx context.Context, runner remoteRunner, options inspectOptions) (*InspectReport, error) {
	report := &InspectReport{CollectedAt: time.Now().UTC(), EPControlPIDs: []int{}, LocalFiles: []LocalFile{}}
	c := &collector{ctx: ctx, runner: runner, report: report}

	out, ok, err := c.run("id -u")
	if err != nil {
		return nil, err
	}
	if ok {
		if uid, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
			report.UID = &uid
			if uid != 0 {
				c.warn("logged in as uid %d, not root: some AP files may be unreadable", uid)
			}
		}
	}

	steps := []func() error{c.firmware, c.uptime, c.identity, c.bootConfig, c.rootFS, c.bootState, c.epControlFiles, c.epControlRunning, c.cloud}
	if options.Level >= inspectFull {
		steps = append(steps, c.eeproms, c.mtd, c.caBundles, c.errorReports)
		if options.LogLines > 0 {
			steps = append(steps, func() error { return c.logs(options.LogLines) })
		}
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return report, nil
}

type collector struct {
	ctx    context.Context
	runner remoteRunner
	report *InspectReport
}

func (c *collector) warn(format string, args ...any) {
	c.report.Warnings = append(c.report.Warnings, fmt.Sprintf(format, args...))
}

// run returns stdout and whether the command succeeded. A non-zero exit is
// not an error; a transport failure is.
func (c *collector) run(command string) (string, bool, error) {
	result, err := c.runner.Run(c.ctx, command)
	if err != nil {
		return "", false, err
	}
	return string(result.Stdout), result.ExitStatus == 0, nil
}

// readFile cats an absolute path. Every path inspect reads is absolute, so
// no "--" is needed (and old busybox builds need not support it).
func (c *collector) readFile(file string) ([]byte, bool, error) {
	if !strings.HasPrefix(file, "/") {
		c.warn("not reading %q: not an absolute path", file)
		return nil, false, nil
	}
	result, err := c.runner.Run(c.ctx, "cat "+shellQuote(file))
	if err != nil {
		return nil, false, err
	}
	return result.Stdout, result.ExitStatus == 0, nil
}

func (c *collector) firmware() error {
	data, ok, err := c.readFile(apVersionFile)
	if err != nil {
		return err
	}
	if !ok {
		c.warn("%s is missing: this may not be a Mist AP", apVersionFile)
		return nil
	}
	values := parseShellAssignments(string(data))
	c.report.Firmware = FirmwareInfo{
		Model: values["FIRMWARE_MODEL"], Version: values["FIRMWARE_VERSION"],
		BuildTime: values["FIRMWARE_BUILDTIME"], FIPS: values["FIRMWARE_FIPS"],
	}
	return nil
}

func (c *collector) uptime() error {
	data, ok, err := c.readFile("/proc/uptime")
	if err != nil || !ok {
		return err
	}
	if seconds, ok := parseUptime(string(data)); ok {
		c.report.UptimeSeconds = &seconds
	}
	return nil
}

func (c *collector) identity() error {
	for _, name := range apIDInterfaces {
		data, ok, err := c.readFile("/sys/class/net/" + name + "/address")
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		mac, err := net.ParseMAC(strings.TrimSpace(string(data)))
		if err != nil || len(mac) != 6 {
			continue
		}
		c.report.Interfaces = append(c.report.Interfaces, APInterface{Name: name, MAC: dashedMAC(mac)})
	}
	if len(c.report.Interfaces) == 0 {
		c.warn("no interface address found to derive the AP ID")
		return nil
	}
	c.report.ID = c.report.Interfaces[0].MAC
	for _, iface := range c.report.Interfaces[1:] {
		if iface.MAC != c.report.ID {
			c.warn("%s address %s differs from %s %s; ep-control may identify the AP by %s", iface.Name, iface.MAC, c.report.Interfaces[0].Name, c.report.ID, iface.Name)
		}
	}
	return nil
}

func dashedMAC(mac net.HardwareAddr) string {
	return strings.ReplaceAll(mac.String(), ":", "-")
}

func (c *collector) bootConfig() error {
	out, ok, err := c.run("/sbin/bootcfg")
	if err != nil {
		return err
	}
	if !ok {
		c.warn("/sbin/bootcfg failed")
		return nil
	}
	config, err := parseBootConfig(strings.TrimSpace(out))
	if err != nil {
		c.warn("bootcfg: %v", err)
		return nil
	}
	c.report.BootConfig = &config
	return nil
}

func (c *collector) rootFS() error {
	data, ok, err := c.readFile("/proc/mounts")
	if err != nil || !ok {
		return err
	}
	c.report.RootFS = rootDevice(string(data))
	return nil
}

func (c *collector) bootState() error {
	data, ok, err := c.readFile(apBootStateFile)
	if err != nil || !ok {
		return err
	}
	c.report.BootState = strings.TrimSpace(strings.Trim(string(data), "\x00"))
	return nil
}

func (c *collector) epControlFiles() error {
	out, ok, err := c.run("ls -1A " + apEPControlRW)
	if err != nil {
		return err
	}
	if !ok {
		c.warn("%s does not exist: /rw was wiped (factory reset?) and the AP runs its stock cloud config", apEPControlRW)
		return nil
	}
	names := nonEmptyLines(out)
	sort.Strings(names)
	c.report.RWFiles = names
	has := func(name string) bool { return slices.Contains(names, name) }

	if has("boot.count") {
		data, ok, err := c.readFile(apEPControlRW + "/boot.count")
		if err != nil {
			return err
		}
		if count, err := strconv.Atoi(strings.TrimSpace(string(data))); ok && err == nil {
			c.report.BootCount = &count
		}
	}
	for _, name := range []string{"last_reboot_info.txt", "last_reboot_info.txt.uploaded"} {
		if !has(name) {
			continue
		}
		data, ok, err := c.readFile(apEPControlRW + "/" + name)
		if err != nil {
			return err
		}
		if ok {
			info := parseRebootInfo(string(data))
			info.File = apEPControlRW + "/" + name
			info.Uploaded = strings.HasSuffix(name, ".uploaded")
			c.report.LastReboot = &info
			break
		}
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "local_") {
			continue
		}
		data, ok, err := c.readFile(apEPControlRW + "/" + name)
		if err != nil {
			return err
		}
		if !ok {
			c.warn("cannot read %s/%s", apEPControlRW, name)
			c.report.LocalFiles = append(c.report.LocalFiles, LocalFile{Name: name, ParseError: "unreadable"})
			continue
		}
		c.report.LocalFiles = append(c.report.LocalFiles, describeLocalFile(name, data))
	}
	if len(c.report.LocalFiles) > 0 {
		c.warn("%d local override file(s) in %s change what this AP runs; back them up and record them before onboarding or migrating it", len(c.report.LocalFiles), apEPControlRW)
	}
	return nil
}

func (c *collector) epControlRunning() error {
	out, ok, err := c.run("pidof ep-control")
	if err != nil {
		return err
	}
	if ok {
		for _, field := range strings.Fields(out) {
			if pid, err := strconv.Atoi(field); err == nil {
				c.report.EPControlPIDs = append(c.report.EPControlPIDs, pid)
			}
		}
	}
	if len(c.report.EPControlPIDs) == 0 {
		c.warn("ep-control is not running")
	}
	return nil
}

func (c *collector) cloud() error {
	file := apEPControlRO + "/cloud.ini"
	override := slices.Contains(c.report.RWFiles, "cloud.ini")
	if override {
		file = apEPControlRW + "/cloud.ini"
	}
	data, ok, err := c.readFile(file)
	if err != nil {
		return err
	}
	if !ok {
		c.warn("cannot read %s", file)
		return nil
	}
	config := parseCloudINI(string(data))
	config.Path = file
	config.RWOverride = override
	for _, entry := range config.Entries {
		if entry.Insecure {
			c.warn("%s disables TLS verification for %s", file, entry.URL)
		}
	}
	c.report.Cloud = &config
	return nil
}

// rootCertPaths lists the distinct CA bundle paths cloud.ini refers to, with
// ${FIPS} expanded the way the firmware's file names suggest.
func (r *InspectReport) rootCertPaths() []string {
	if r.Cloud == nil {
		return nil
	}
	fips := ""
	if r.Firmware.FIPS != "" && r.Firmware.FIPS != "0" {
		fips = "-fips"
	}
	var paths []string
	add := func(value string) {
		if value != "" {
			paths = appendUnique(paths, strings.ReplaceAll(value, "${FIPS}", fips))
		}
	}
	for _, entry := range r.Cloud.Entries {
		add(entry.RootCerts)
	}
	add(r.Cloud.TLSRootCerts)
	return paths
}

func (c *collector) caBundles() error {
	for _, file := range c.report.rootCertPaths() {
		bundle := CABundle{Path: file}
		data, ok, err := c.readFile(file)
		if err != nil {
			return err
		}
		if !ok {
			bundle.Error = "missing or unreadable"
			c.warn("CA bundle %s named in cloud.ini is missing", file)
		} else {
			bundle.Certificates, bundle.Error = parseCABundle(data)
		}
		c.report.CABundles = append(c.report.CABundles, bundle)
	}
	return nil
}

func (c *collector) eeproms() error {
	out, ok, err := c.run("ls -1d " + apEEPROMGlob)
	if err != nil {
		return err
	}
	if !ok {
		c.warn("no EEPROM found under %s", path.Dir(path.Dir(apEEPROMGlob)))
		return nil
	}
	for _, file := range nonEmptyLines(out) {
		info := EEPROMInfo{Path: file}
		out, ok, err := c.run("wc -c " + shellQuote(file))
		if err != nil {
			return err
		}
		if fields := strings.Fields(out); ok && len(fields) > 0 {
			info.Size, _ = strconv.Atoi(fields[0])
		}
		c.report.EEPROMs = append(c.report.EEPROMs, info)
	}
	sized := 0
	for _, info := range c.report.EEPROMs {
		if info.Size == eepromSize {
			sized++
		}
	}
	if sized != 1 {
		c.warn("expected exactly one %d-byte EEPROM, found %d; onboarding would stop here", eepromSize, sized)
	}
	return nil
}

func (c *collector) mtd() error {
	data, ok, err := c.readFile("/proc/mtd")
	if err != nil || !ok {
		return err
	}
	for _, line := range nonEmptyLines(string(data)) {
		if !strings.HasPrefix(line, "dev:") {
			c.report.MTD = append(c.report.MTD, line)
		}
	}
	return nil
}

func (c *collector) errorReports() error {
	out, ok, err := c.run("ls -1A " + apErrorReports)
	if err != nil || !ok {
		return err
	}
	c.report.ErrorReports = nonEmptyLines(out)
	sort.Strings(c.report.ErrorReports)
	return nil
}

func (c *collector) logs(lines int) error {
	epLog := LogTail{Name: "ep-control", Source: apEPControlLog}
	out, ok, err := c.run(fmt.Sprintf("tail -n %d %s", lines, apEPControlLog))
	if err != nil {
		return err
	}
	if ok {
		epLog.Lines = redactLines(nonEmptyLines(out))
	} else {
		epLog.Error = "missing or unreadable"
	}

	// 0.15 runs busybox syslogd writing /var/log/messages; older images may
	// only keep the in-memory buffer that logread prints.
	system := LogTail{Name: "system", Source: apSystemLog}
	out, ok, err = c.run(fmt.Sprintf("tail -n %d %s", lines, apSystemLog))
	if err != nil {
		return err
	}
	if !ok {
		system.Source = "logread"
		out, ok, err = c.run("logread")
		if err != nil {
			return err
		}
	}
	if ok {
		system.Lines = redactLines(lastLines(nonEmptyLines(out), lines))
	} else {
		system.Error = "no /var/log/messages and no logread"
	}

	kernel := LogTail{Name: "kernel", Source: "dmesg"}
	out, ok, err = c.run("dmesg")
	if err != nil {
		return err
	}
	if ok {
		kernel.Lines = redactLines(lastLines(nonEmptyLines(out), lines))
	} else {
		kernel.Error = "dmesg failed"
	}

	c.report.Logs = []LogTail{epLog, system, kernel}
	for _, tail := range c.report.Logs {
		for _, line := range tail.Lines {
			if notableLine.MatchString(line) && len(c.report.Notable) < maxNotableLines {
				c.report.Notable = append(c.report.Notable, tail.Name+": "+line)
			}
		}
	}
	return nil
}

const maxNotableLines = 100

// notableLine picks the lines the stability investigation (design §8.3)
// looks for: reboots, procmon and wrapper restarts of ep-control, OOM,
// watchdog, panics, config decode failures, PoE and LLDP power events, and
// cloud disconnects.
var notableLine = regexp.MustCompile(`(?i)reboot|procmon|\boom\b|oom-killer|out of memory|watchdog|panic|fatal|segfault|can'?t unmarshal|cannot unmarshal|\bpoe|lldp|for the ([2-9]|[0-9]{2,})th time|closing-because|disconnect`)

// secretField matches config fields ep-control prints in its config dumps
// (Go %+v and JSON forms) whose values are secrets.
var secretField = regexp.MustCompile(`(?i)("?\b(?:psk|passphrase|password|passwd|secret|sharedsecret|keyencryptkey|papiopaque|key|token|privatekey)"?\s*[:=]\s*)("(?:[^"\\]|\\.)*"|[^\s,}\]]+)`)

func redactLines(lines []string) []string {
	for i, line := range lines {
		lines[i] = secretField.ReplaceAllString(line, "${1}<redacted>")
	}
	return lines
}

func lastLines(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// parseShellAssignments reads KEY=VALUE lines such as /etc/mist/version.
func parseShellAssignments(text string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		values[strings.TrimSpace(key)] = value
	}
	return values
}

func parseUptime(text string) (float64, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	return seconds, err == nil && seconds >= 0
}

var bootConfigPattern = regexp.MustCompile(`^w([01])i([12])c([0-7])$`)

func parseBootConfig(raw string) (BootConfig, error) {
	match := bootConfigPattern.FindStringSubmatch(raw)
	if match == nil {
		return BootConfig{}, fmt.Errorf("unexpected output %q (expected wNiNcN)", raw)
	}
	image, _ := strconv.Atoi(match[2])
	counter, _ := strconv.Atoi(match[3])
	return BootConfig{Raw: raw, Watchdog: match[1] == "1", Image: image, Counter: counter}, nil
}

// rootDevice returns the device mounted on / (ubi0:rootfs1 or ubi0:rootfs2),
// skipping the initramfs "rootfs" entry.
func rootDevice(mounts string) string {
	device := ""
	for _, line := range strings.Split(mounts, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "/" && fields[0] != "rootfs" {
			device = fields[0]
		}
	}
	return device
}

func parseRebootInfo(text string) RebootInfo {
	text = strings.TrimSpace(text)
	stamp, reason, ok := strings.Cut(text, "::")
	if !ok {
		return RebootInfo{Reason: text}
	}
	info := RebootInfo{Reason: reason}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64); err == nil && seconds > 0 {
		at := time.Unix(seconds, 0).UTC()
		info.Time = &at
	}
	return info
}

const maxLocalFileKeys = 200

func describeLocalFile(name string, data []byte) LocalFile {
	sum := sha256.Sum256(data)
	file := LocalFile{Name: name, Size: len(data), SHA256: hex.EncodeToString(sum[:])}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		file.ParseError = "not valid JSON (ep-control logs its own parse errors)"
		return file
	}
	var keys []string
	collectKeyPaths(value, "", &keys)
	sort.Strings(keys)
	if len(keys) > maxLocalFileKeys {
		keys = append(keys[:maxLocalFileKeys], fmt.Sprintf("... %d more", len(keys)-maxLocalFileKeys))
	}
	file.Keys = keys
	return file
}

// collectKeyPaths lists leaf paths (a.b, a[].c) without their values.
func collectKeyPaths(value any, prefix string, keys *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 && prefix != "" {
			*keys = appendUnique(*keys, prefix)
		}
		for key, child := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			collectKeyPaths(child, next, keys)
		}
	case []any:
		if len(typed) == 0 {
			*keys = appendUnique(*keys, prefix+"[]")
		}
		for _, child := range typed {
			collectKeyPaths(child, prefix+"[]", keys)
		}
	default:
		if prefix != "" {
			*keys = appendUnique(*keys, prefix)
		}
	}
}

// parseCloudINI reads the TOML subset ep-control's cloud.ini uses: [[cloud]]
// entries with url, manufacturing and a [cloud.tls] table, a legacy [tls]
// table, and [proxy]. Comments and unknown keys are ignored.
func parseCloudINI(text string) CloudConfig {
	var config CloudConfig
	section := ""
	current := -1
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			section = strings.TrimSpace(line[2 : len(line)-2])
			if section == "cloud" {
				config.Entries = append(config.Entries, CloudEntry{})
				current = len(config.Entries) - 1
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		text := unquoteTOML(value)
		switch {
		case section == "cloud" && current >= 0 && key == "url":
			config.Entries[current].URL = text
		case section == "cloud" && current >= 0 && key == "manufacturing":
			config.Entries[current].Manufacturing = value == "true"
		case section == "cloud.tls" && current >= 0 && key == "rootcerts":
			config.Entries[current].RootCerts = text
		case section == "cloud.tls" && current >= 0 && key == "insecure":
			config.Entries[current].Insecure = value == "true"
		case section == "tls" && key == "rootcerts":
			config.TLSRootCerts = text
		case section == "proxy" && key == "url":
			config.ProxyHost = proxyHost(text)
		}
	}
	return config
}

func stripTOMLComment(line string) string {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if quoted {
				i++
			}
		case '"':
			quoted = !quoted
		case '#':
			if !quoted {
				return line[:i]
			}
		}
	}
	return line
}

func unquoteTOML(value string) string {
	if unquoted, err := strconv.Unquote(value); err == nil {
		return unquoted
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}

// proxyHost keeps only the proxy's scheme and host: the URL may carry a
// username and password.
func proxyHost(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		rest, scheme = raw, ""
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	rest, _, _ = strings.Cut(rest, "/")
	if scheme != "" {
		return scheme + "://" + rest
	}
	return rest
}

func parseCABundle(data []byte) ([]CertInfo, string) {
	var certs []CertInfo
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		sum := sha256.Sum256(block.Bytes)
		info := CertInfo{SHA256: hex.EncodeToString(sum[:])}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			info.Error = err.Error()
		} else {
			info.Subject = cert.Subject.String()
			info.Issuer = cert.Issuer.String()
			info.NotAfter = cert.NotAfter.UTC()
		}
		certs = append(certs, info)
	}
	if len(certs) == 0 {
		return nil, "no PEM certificates"
	}
	return certs, ""
}

// writeInspectText prints the report for a terminal.
func writeInspectText(w io.Writer, report *InspectReport, now time.Time) error {
	p := &textPrinter{w: w}
	p.line("AP %s", report.Target)
	if report.HostKey != "" {
		p.field("Host key", report.HostKey)
	}
	p.field("AP ID", orUnknown(report.ID))
	firmware := orUnknown(report.Firmware.Version)
	var details []string
	if report.Firmware.Model != "" {
		details = append(details, report.Firmware.Model)
	}
	if report.Firmware.BuildTime != "" {
		details = append(details, "built "+report.Firmware.BuildTime)
	}
	if report.Firmware.FIPS != "" && report.Firmware.FIPS != "0" {
		details = append(details, "FIPS")
	}
	if len(details) > 0 {
		firmware += " (" + strings.Join(details, ", ") + ")"
	}
	p.field("Firmware", firmware)
	if report.UptimeSeconds != nil {
		up := time.Duration(*report.UptimeSeconds * float64(time.Second))
		p.field("Uptime", fmt.Sprintf("%s (booted %s)", formatUptime(up), now.Add(-up).UTC().Format(time.RFC3339)))
	} else {
		p.field("Uptime", "unknown")
	}
	if report.BootCount != nil {
		p.field("Boot count", strconv.Itoa(*report.BootCount))
	} else {
		p.field("Boot count", "unknown")
	}
	if report.BootConfig != nil {
		watchdog := "off"
		if report.BootConfig.Watchdog {
			watchdog = "on"
		}
		p.field("Boot config", fmt.Sprintf("%s (image %d, watchdog %s, counter %d)", report.BootConfig.Raw, report.BootConfig.Image, watchdog, report.BootConfig.Counter))
	}
	if report.RootFS != "" {
		p.field("Root FS", report.RootFS)
	}
	if report.BootState != "" {
		p.field("Boot state", report.BootState)
	}
	if report.LastReboot != nil {
		reboot := report.LastReboot.Reason
		if report.LastReboot.Time != nil {
			reboot = report.LastReboot.Time.Format(time.RFC3339) + " " + reboot
		}
		if !report.LastReboot.Uploaded {
			reboot += " (not yet reported to the controller)"
		}
		p.field("Last reboot", reboot)
	}
	if len(report.EPControlPIDs) > 0 {
		pids := make([]string, len(report.EPControlPIDs))
		for i, pid := range report.EPControlPIDs {
			pids[i] = strconv.Itoa(pid)
		}
		p.field("ep-control", "running (pid "+strings.Join(pids, ", ")+")")
	} else {
		p.field("ep-control", "NOT running")
	}
	for _, info := range report.EEPROMs {
		p.field("EEPROM", fmt.Sprintf("%s (%d bytes)", info.Path, info.Size))
	}
	if len(report.MTD) > 0 {
		p.field("MTD", fmt.Sprintf("%d partitions", len(report.MTD)))
	}

	if len(report.LocalFiles) == 0 {
		p.field("Local files", "none ("+apEPControlRW+"/local_* absent)")
	} else {
		p.field("Local files", fmt.Sprintf("%d PRESENT in %s", len(report.LocalFiles), apEPControlRW))
		for _, file := range report.LocalFiles {
			p.line("  %s  %d bytes  sha256 %s", file.Name, file.Size, file.SHA256)
			if file.ParseError != "" {
				p.line("    %s", file.ParseError)
			}
			for _, key := range file.Keys {
				p.line("    %s", key)
			}
		}
	}

	if report.Cloud != nil {
		origin := "firmware copy"
		if report.Cloud.RWOverride {
			origin = "overrides the firmware copy"
		}
		p.field("Cloud config", fmt.Sprintf("%s (%s)", report.Cloud.Path, origin))
		for _, entry := range report.Cloud.Entries {
			label := "url"
			if entry.Manufacturing {
				label = "url (factory)"
			}
			p.line("  %-15s %s", label, orUnknown(entry.URL))
			if entry.RootCerts != "" {
				p.line("  %-15s %s", "rootcerts", entry.RootCerts)
			}
			if entry.Insecure {
				p.line("  %-15s %s", "insecure", "TLS verification DISABLED")
			}
		}
		if report.Cloud.TLSRootCerts != "" {
			p.line("  %-15s %s", "[tls] rootcerts", report.Cloud.TLSRootCerts)
		}
		if report.Cloud.ProxyHost != "" {
			p.line("  %-15s %s", "proxy", report.Cloud.ProxyHost)
		}
	}
	for _, bundle := range report.CABundles {
		if bundle.Error != "" {
			p.field("CA bundle", bundle.Path+": "+bundle.Error)
			continue
		}
		p.field("CA bundle", fmt.Sprintf("%s (%d certificates)", bundle.Path, len(bundle.Certificates)))
		for _, cert := range bundle.Certificates {
			if cert.Error != "" {
				p.line("  unparseable certificate sha256 %s: %s", cert.SHA256, cert.Error)
				continue
			}
			expiry := "expires " + cert.NotAfter.Format("2006-01-02")
			if cert.NotAfter.Before(now) {
				expiry = "EXPIRED " + cert.NotAfter.Format("2006-01-02")
			}
			p.line("  %s (%s)", cert.Subject, expiry)
		}
	}
	if len(report.ErrorReports) > 0 {
		p.field("Error reports", apErrorReports+": "+strings.Join(report.ErrorReports, ", "))
	}
	if len(report.Warnings) > 0 {
		p.line("")
		p.line("Warnings:")
		for _, warning := range report.Warnings {
			p.line("  - %s", warning)
		}
	}
	if len(report.Notable) > 0 {
		p.line("")
		p.line("Notable log lines (reboots, restarts, OOM, watchdog, decode errors, power):")
		for _, line := range report.Notable {
			p.line("  %s", line)
		}
	}
	for _, tail := range report.Logs {
		p.line("")
		if tail.Error != "" {
			p.line("%s log (%s): %s", tail.Name, tail.Source, tail.Error)
			continue
		}
		p.line("%s log (last %d lines of %s, secrets redacted):", tail.Name, len(tail.Lines), tail.Source)
		for _, line := range tail.Lines {
			p.line("  %s", line)
		}
	}
	return p.err
}

type textPrinter struct {
	w   io.Writer
	err error
}

func (p *textPrinter) line(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format+"\n", args...)
	}
}

func (p *textPrinter) field(name, value string) {
	p.line("%-14s %s", name, value)
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	seconds := (d - minutes*time.Minute) / time.Second
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	default:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
}
