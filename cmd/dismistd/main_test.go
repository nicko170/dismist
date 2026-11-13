package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSecretRequiresExactly32PrivateBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, 31), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecret(path); err == nil {
		t.Fatal("accepted truncated AP secret")
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadSecret(path)
	if err != nil || !bytes.Equal(got[:], bytes.Repeat([]byte{7}, 32)) {
		t.Fatalf("failed to load exact secret: %v", err)
	}
}

func TestTelemetryCaptureIsPrivateAndBounded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "captures")
	capture, err := newTelemetryCapture(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	capture([]byte("first"))
	capture([]byte("second"))
	capture([]byte("third"))
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("capture limit failed: %d %v", len(files), err)
	}
	for _, file := range files {
		info, err := file.Info()
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("capture is not private: %v %v", info, err)
		}
	}
}
