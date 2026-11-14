package legacyv0

import (
	"crypto/sha256"
	"encoding/hex"

	_ "embed"
)

//go:embed ap33.json
var bundledTemplate []byte

// TemplateSHA256 is the SHA-256 of the AP33 template bundled with the
// ap-identity release. internal/aptemplate/ap33.json must keep this hash.
const TemplateSHA256 = "ffc20a417fd992fd924150c7a5fbcaa5d74271cd1086f9adfc6fa0651727472d"

// BundledTemplate returns a copy of the frozen bundled template.
func BundledTemplate() []byte { return append([]byte(nil), bundledTemplate...) }

// Template returns the runtime template when one was loaded (dismistd
// -ap-config), else the frozen bundled copy.
func Template(runtime []byte) []byte {
	if len(runtime) != 0 {
		return runtime
	}
	return BundledTemplate()
}

// SHA256Hex is the hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
