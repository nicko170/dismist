package dismist

import "embed"

// WebAssets contains the legacy React bundle (web/dist), served at / until
// M1 (design §2.4).
//
//go:embed web/dist
var WebAssets embed.FS

// UIAssets contains the v1 app (ui/dist): index.html plus everything under
// _ui/. Only a stub is committed; the Dockerfile and CI build the real app
// before go build (design §7.1). "all:" keeps the _ui directory, which a
// plain pattern would skip for its leading underscore.
//
//go:embed all:ui/dist
var UIAssets embed.FS
