package model

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// WriteBackup writes a consistent copy of the database to path. The copy is
// made with tx.WriteTo into a temporary file in path's directory, synced,
// and then renamed onto path, so the bbolt read transaction lasts only as
// long as a local disk copy (design §4.1 "Backups") and path never holds a
// partial copy. The file is mode 0600. It returns the size written.
func (s *BoltStore) WriteBackup(path string) (int64, error) {
	return writeDBCopy(s.db, path)
}

// WriteTo streams a consistent copy of the database to w inside one read
// transaction. Prefer WriteBackup for anything slower than a local disk:
// a long read transaction blocks bbolt from growing its mmap and so stalls
// writers.
func (s *BoltStore) WriteTo(w io.Writer) (int64, error) {
	var written int64
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		written, err = tx.WriteTo(w)
		return err
	})
	return written, err
}

// Size is the size of the database file in bytes.
func (s *BoltStore) Size() (int64, error) {
	var size int64
	err := s.db.View(func(tx *bolt.Tx) error {
		size = tx.Size()
		return nil
	})
	return size, err
}

func writeDBCopy(db *bolt.DB, path string) (int64, error) {
	if path == "" {
		return 0, errors.New("backup path required")
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return 0, err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return 0, err
	}
	var written int64
	err = db.View(func(tx *bolt.Tx) error {
		var err error
		written, err = tx.WriteTo(temp)
		return err
	})
	if err != nil {
		_ = temp.Close()
		return 0, fmt.Errorf("copy database: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return 0, err
	}
	if err := temp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return 0, err
	}
	keep = true
	if handle, err := os.Open(dir); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
	return written, nil
}
