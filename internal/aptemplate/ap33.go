package aptemplate

import _ "embed"

//go:embed ap33.json
var ap33 []byte

// Default returns the AP33 base configuration without device, site, or WLAN identity.
func Default() []byte {
	return append([]byte(nil), ap33...)
}
