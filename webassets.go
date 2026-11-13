package dismist

import "embed"

// WebAssets contains the production React bundle served by the Go binary.
//
//go:embed web/dist
var WebAssets embed.FS
