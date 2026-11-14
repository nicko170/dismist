package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// Scheduled snapshots (design §4.1 "Backups"): every -backup-interval the
// database is copied with tx.WriteTo into -backup-dir, unless free space
// would drop below a quarter of the volume. Retention keeps -backup-keep
// snapshots and also deletes the oldest while all of them together exceed
// -backup-max-bytes (default 30 % of the volume).

const (
	snapshotPrefix = "controller-"
	snapshotSuffix = ".db"
	// backupFirstDelay is how long after startup an overdue snapshot runs.
	backupFirstDelay = time.Minute
	// backupMinFreeFraction is the share of the volume a snapshot must
	// leave free.
	backupMinFreeFraction = 4 // 1/4 = 25 %
	// backupDefaultMaxShare is the default -backup-max-bytes, in percent
	// of the volume.
	backupDefaultMaxShare = 30
)

// Failure reasons (for dismist_backup_failures_total{reason}).
const (
	backupFailSpace = "space"
	backupFailWrite = "write"
)

// byteSize is a flag value in bytes that also accepts K/M/G/T suffixes, in
// powers of 1024 with "i" (Ki, Mi, Gi, Ti) or 1000 without.
type byteSize int64

func (b *byteSize) String() string { return strconv.FormatInt(int64(*b), 10) }

func (b *byteSize) Set(value string) error {
	parsed, err := parseByteSize(value)
	if err != nil {
		return err
	}
	*b = byteSize(parsed)
	return nil
}

func parseByteSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	multipliers := []struct {
		suffix string
		factor int64
	}{
		{"Ti", 1 << 40}, {"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"T", 1e12}, {"G", 1e9}, {"M", 1e6}, {"K", 1e3}, {"k", 1e3},
	}
	factor := int64(1)
	for _, m := range multipliers {
		if trimmed, ok := strings.CutSuffix(value, m.suffix); ok {
			value, factor = trimmed, m.factor
			break
		}
	}
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid size %q: use bytes or a K/M/G/T(i) suffix", value)
	}
	return number * factor, nil
}

// backupConfig holds the -backup-* flags.
type backupConfig struct {
	dir      string
	interval time.Duration
	keep     int
	maxBytes byteSize
}

func registerBackupFlags(fs *flag.FlagSet) *backupConfig {
	c := &backupConfig{}
	fs.StringVar(&c.dir, "backup-dir", "", "directory for scheduled database snapshots (default: backups/ next to -db)")
	fs.DurationVar(&c.interval, "backup-interval", 24*time.Hour, "how often to snapshot the database into -backup-dir (0 disables)")
	fs.IntVar(&c.keep, "backup-keep", 7, "how many scheduled snapshots to keep")
	fs.Var(&c.maxBytes, "backup-max-bytes", "most bytes all snapshots may use together, e.g. 600Mi (default: 30% of the volume holding -backup-dir)")
	return c
}

// resolve fills defaults and checks the values.
func (c *backupConfig) resolve(dbPath string) error {
	if c.dir == "" {
		c.dir = filepath.Join(filepath.Dir(dbPath), "backups")
	}
	if c.interval < 0 {
		return errors.New("-backup-interval must not be negative")
	}
	if c.interval > 0 && c.interval < time.Minute {
		return errors.New("-backup-interval must be at least 1m (or 0 to disable)")
	}
	if c.keep < 1 {
		return errors.New("-backup-keep must be at least 1")
	}
	if c.maxBytes < 0 {
		return errors.New("-backup-max-bytes must not be negative")
	}
	return nil
}

// snapshotStore is what a snapshot needs from the database.
type snapshotStore interface {
	WriteBackup(path string) (int64, error)
	Size() (int64, error)
}

// backupScheduler takes scheduled snapshots and reports their state.
type backupScheduler struct {
	store  snapshotStore
	config backupConfig
	usage  func(string) (model.DiskSpace, error)
	now    func() time.Time
	// warn records a skipped or failed snapshot (a warning event).
	warn func(reason, message string)

	mu          sync.Mutex
	lastSuccess time.Time
	lastError   string
	next        time.Time
	failures    map[string]uint64
}

func newBackupScheduler(store snapshotStore, config backupConfig, warn func(reason, message string)) *backupScheduler {
	return &backupScheduler{
		store: store, config: config, usage: model.DiskUsage, now: time.Now, warn: warn,
		failures: map[string]uint64{},
	}
}

type snapshotFile struct {
	name    string
	size    int64
	modTime time.Time
}

// snapshots lists the scheduled snapshots, newest first.
func (b *backupScheduler) snapshots() ([]snapshotFile, error) {
	entries, err := os.ReadDir(b.config.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []snapshotFile
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || !strings.HasPrefix(name, snapshotPrefix) || !strings.HasSuffix(name, snapshotSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, snapshotFile{name: name, size: info.Size(), modTime: info.ModTime()})
	}
	// Names carry a sortable UTC timestamp.
	sort.Slice(files, func(i, j int) bool { return files[i].name > files[j].name })
	return files, nil
}

// maxBytes is -backup-max-bytes, or 30 % of the volume.
func (b *backupScheduler) maxBytes(space model.DiskSpace) int64 {
	if b.config.maxBytes > 0 {
		return int64(b.config.maxBytes)
	}
	return int64(space.Total / 100 * backupDefaultMaxShare)
}

// fail records a skipped or failed snapshot.
func (b *backupScheduler) fail(reason, message string) error {
	b.mu.Lock()
	b.failures[reason]++
	b.lastError = message
	b.mu.Unlock()
	log.Printf("scheduled backup: %s", message)
	if b.warn != nil {
		b.warn(reason, message)
	}
	return errors.New(message)
}

// RunOnce takes one snapshot now and applies retention.
func (b *backupScheduler) RunOnce() error {
	now := b.now().UTC()
	if err := os.MkdirAll(b.config.dir, 0o700); err != nil {
		return b.fail(backupFailWrite, fmt.Sprintf("cannot create %s: %v", b.config.dir, err))
	}
	size, err := b.store.Size()
	if err != nil {
		return b.fail(backupFailWrite, fmt.Sprintf("cannot read the database size: %v", err))
	}
	space, err := b.usage(b.config.dir)
	if err != nil {
		return b.fail(backupFailSpace, fmt.Sprintf("free-space check on %s failed: %v", b.config.dir, err))
	}
	if space.Free < uint64(size) || space.Free-uint64(size) < space.Total/backupMinFreeFraction {
		return b.fail(backupFailSpace, fmt.Sprintf("snapshot skipped: a %d-byte copy would leave less than 25%% of %s free (%d of %d bytes free)",
			size, b.config.dir, space.Free, space.Total))
	}
	name := snapshotPrefix + now.Format("20060102T150405Z") + snapshotSuffix
	path := filepath.Join(b.config.dir, name)
	if _, err := os.Stat(path); err == nil {
		return b.fail(backupFailWrite, fmt.Sprintf("snapshot %s already exists", name))
	}
	written, err := b.store.WriteBackup(path)
	if err != nil {
		return b.fail(backupFailWrite, fmt.Sprintf("snapshot %s failed: %v", name, err))
	}
	removed := b.prune(b.maxBytes(space))
	b.mu.Lock()
	b.lastSuccess, b.lastError = now, ""
	b.mu.Unlock()
	log.Printf("scheduled backup: wrote %s (%d bytes)%s", path, written, removed)
	return nil
}

// prune keeps the newest -backup-keep snapshots, then deletes the oldest
// while the rest exceed maxBytes. The newest snapshot is always kept.
func (b *backupScheduler) prune(maxBytes int64) string {
	files, err := b.snapshots()
	if err != nil {
		log.Printf("scheduled backup: list %s: %v", b.config.dir, err)
		return ""
	}
	var total int64
	for _, file := range files {
		total += file.size
	}
	var removed []string
	for len(files) > 1 && (len(files) > b.config.keep || (maxBytes > 0 && total > maxBytes)) {
		oldest := files[len(files)-1]
		if err := os.Remove(filepath.Join(b.config.dir, oldest.name)); err != nil {
			log.Printf("scheduled backup: remove %s: %v", oldest.name, err)
			break
		}
		total -= oldest.size
		files = files[:len(files)-1]
		removed = append(removed, oldest.name)
	}
	if len(removed) == 0 {
		return ""
	}
	return "; removed " + strings.Join(removed, ", ")
}

// firstRun is when the first scheduled snapshot runs: one interval after
// the newest existing snapshot, or shortly after startup when that is
// overdue, so a restart neither skips nor repeats a snapshot.
func (b *backupScheduler) firstRun() time.Time {
	now := b.now().UTC()
	soon := now.Add(backupFirstDelay)
	files, err := b.snapshots()
	if err != nil || len(files) == 0 {
		return soon
	}
	due := files[0].modTime.UTC().Add(b.config.interval)
	if due.Before(soon) {
		return soon
	}
	return due
}

// Run takes snapshots every interval until ctx ends. It returns at once
// when snapshots are disabled.
func (b *backupScheduler) Run(ctx context.Context) {
	if b.config.interval <= 0 {
		return
	}
	next := b.firstRun()
	for {
		b.mu.Lock()
		b.next = next
		b.mu.Unlock()
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_ = b.RunOnce()
		next = b.now().UTC().Add(b.config.interval)
	}
}

// Status implements httpapi.BackupStatus.
func (b *backupScheduler) Status() api.BackupView {
	view := api.BackupView{Keep: b.config.keep, Dir: b.config.dir, PreMigration: []api.PreMigrationCopy{}}
	b.mu.Lock()
	lastSuccess, next, lastError := b.lastSuccess, b.next, b.lastError
	b.mu.Unlock()
	view.LastError = lastError
	if b.config.interval > 0 {
		view.Interval = b.config.interval.String()
		if !next.IsZero() {
			next = next.UTC()
			view.Next = &next
		}
	}
	files, _ := b.snapshots()
	for _, file := range files {
		view.UsedBytes += file.size
	}
	view.Count = len(files)
	if lastSuccess.IsZero() && len(files) > 0 {
		lastSuccess = files[0].modTime
	}
	if !lastSuccess.IsZero() {
		lastSuccess = lastSuccess.UTC()
		view.LastSuccess = &lastSuccess
	}
	if space, err := b.usage(b.config.dir); err == nil {
		view.MaxBytes = b.maxBytes(space)
	} else {
		view.MaxBytes = int64(b.config.maxBytes)
	}
	return view
}

// Failures returns the skipped or failed snapshots by reason, for
// dismist_backup_failures_total{reason}.
func (b *backupScheduler) Failures() map[string]uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make(map[string]uint64, len(b.failures))
	for reason, count := range b.failures {
		result[reason] = count
	}
	return result
}
