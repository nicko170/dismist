package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

func TestDefaultAPConfigUsesBundledTemplateWhenFileIsMissing(t *testing.T) {
	base, err := loadAPConfig(filepath.Join(t.TempDir(), "ap-config.json"), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.BuildSiteConfig(base, model.Site{}, model.APRecord{ID: "example-ap"}, []model.Network{{ID: "example-wlan", SSID: "ExampleSSID", PSK: "example-passphrase-04", VLANID: 123, Enabled: true}}, time.Unix(1, 0)); err != nil {
		t.Fatalf("bundled fallback is not a valid AP33 template: %v", err)
	}
}

func TestExplicitAPConfigPathMustExist(t *testing.T) {
	_, err := loadAPConfig(filepath.Join(t.TempDir(), "missing.json"), false)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicitly selected AP config: %v", err)
	}
}
