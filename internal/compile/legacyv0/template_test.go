package legacyv0

import (
	"testing"

	"github.com/nicko170/dismist/internal/aptemplate"
)

// TestTemplateHash is the template-hash check (design §4.2 check 6, run by
// CI): the frozen copy and the live bundled template both keep the
// ap-identity hash.
func TestTemplateHash(t *testing.T) {
	if got := SHA256Hex(BundledTemplate()); got != TemplateSHA256 {
		t.Fatalf("frozen legacyv0 template changed: %s", got)
	}
	if got := SHA256Hex(aptemplate.Default()); got != TemplateSHA256 {
		t.Fatalf("internal/aptemplate/ap33.json changed: %s; the template is frozen in v1", got)
	}
}
