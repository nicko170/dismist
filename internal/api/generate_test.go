package api

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gzuidhof/tygo/tygo"
	"gopkg.in/yaml.v2"
)

// TestGeneratedTypesAreCurrent fails when ui/src/gen/api.gen.ts does not
// match the Go DTOs: run go generate ./internal/api.
func TestGeneratedTypesAreCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the package with go/packages")
	}
	raw, err := os.ReadFile("tygo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config tygo.Config
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Packages) != 1 {
		t.Fatalf("tygo.yaml has %d packages, want 1", len(config.Packages))
	}
	committed := config.Packages[0].OutputPath
	if !filepath.IsAbs(committed) {
		committed = filepath.Join(".", committed)
	}
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatalf("%s: %v (run go generate ./internal/api)", committed, err)
	}
	fresh := filepath.Join(t.TempDir(), "api.gen.ts")
	config.Packages[0].OutputPath = fresh
	if err := tygo.New(&config).Generate(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run go generate ./internal/api", committed)
	}
}
