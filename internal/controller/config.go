package controller

import (
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

// BuildConfig forwards to compile.BuildConfig. Deprecated: use
// internal/compile; this wrapper is removed in phase 2 (design §4.4).
func BuildConfig(base []byte, ap model.APRecord, networks []model.Network, now time.Time) ([]byte, error) {
	return compile.BuildConfig(base, ap, networks, now)
}

// BuildSiteConfig forwards to compile.BuildSiteConfig. Deprecated: use
// compile.AP or compile.Site; this wrapper is removed in phase 2.
func BuildSiteConfig(base []byte, site model.Site, ap model.APRecord, networks []model.Network, now time.Time) ([]byte, error) {
	return compile.BuildSiteConfig(base, site, ap, networks, now)
}
