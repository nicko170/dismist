package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nicko170/dismist/internal/options"
)

// writeOptions writes the generated TypeScript and Markdown files from the
// option registry. Files are rewritten only when their content changes.
//
// The Markdown reference goes into the design tree (docs/design), which is
// not part of the public repository: when its directory does not exist the
// reference is skipped, so a clean checkout (CI's go generate job) gains no
// untracked file.
func writeOptions(ts, md string) error {
	registry := options.Default()
	for _, target := range []struct {
		path           string
		gen            func(io.Writer, *options.Registry) error
		needsDirectory bool
	}{{ts, options.GenerateTS, false}, {md, options.GenerateMarkdown, true}} {
		if target.path == "" {
			continue
		}
		if target.needsDirectory {
			if info, err := os.Stat(filepath.Dir(target.path)); err != nil || !info.IsDir() {
				fmt.Fprintf(os.Stderr, "dismist-gen: %s skipped: %s does not exist\n", target.path, filepath.Dir(target.path))
				continue
			}
		}
		var buf bytes.Buffer
		if err := target.gen(&buf, registry); err != nil {
			return err
		}
		if current, err := os.ReadFile(target.path); err == nil && bytes.Equal(current, buf.Bytes()) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target.path, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}
