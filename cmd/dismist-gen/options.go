package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/nicko170/dismist/internal/options"
)

// writeOptions writes the generated TypeScript and Markdown files from the
// option registry. Files are rewritten only when their content changes.
func writeOptions(ts, md string) error {
	registry := options.Default()
	for _, target := range []struct {
		path string
		gen  func(io.Writer, *options.Registry) error
	}{{ts, options.GenerateTS}, {md, options.GenerateMarkdown}} {
		if target.path == "" {
			continue
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
