package options

import "strings"

// neverPaths are the firmware paths no typed option may write: the
// never-tier fields of the catalogues ([SITE §16], [WLAN §5.18], design
// §1.2) and the paths design §4.4 forbids in expert patches for the same
// reason. A path matches when it equals an entry or lies below it; "*"
// ends a name prefix ("WLANs[].Wxlan.Portal*").
var neverPaths = []string{
	// top level
	"Ipsec", "Calea", "SiteString", "PAPIOpaque", "DeIdent", "AVS", "BootConfig",
	"Firmware", "MapID", "XYZ", "Default", "LicenseExpired", "RegulatoryOrg",
	"RadioSwap", "IndoorUse", "DirAntenna", "ManagementTunnels", "Monitor.PcapWLAN",
	// WLAN
	"WLANs[].WLCmds", "WLANs[].Wxlan.Portal*", "WLANs[].ISE", "WLANs[].AirWatch",
	"WLANs[].DPI", "WLANs[].COA.CloudEnabled", "WLANs[].BlackList.Cloud",
	"WLANs[].Auth.WEP", "WLANs[].Auth.SecondaryType", "WLANs[].Auth.AKM.WPA_PSK",
	"WLANs[].Auth.AKM.WPA_EAP", "WLANs[].Auth.Pairwise.TKIP", "WLANs[].Auth.Pairwise.WPA1",
	"WLANs[].Mesh", "WLANs[].ForMesh",
}

// NeverPaths returns the never-tier path list.
func NeverPaths() []string { return append([]string(nil), neverPaths...) }

// NeverPath reports whether path is at or below a never-tier path, and
// which one.
func NeverPath(path string) (string, bool) {
	for _, never := range neverPaths {
		if prefix, ok := strings.CutSuffix(never, "*"); ok {
			if strings.HasPrefix(path, prefix) {
				return never, true
			}
			continue
		}
		if path == never || strings.HasPrefix(path, never+".") || strings.HasPrefix(path, never+"[]") || strings.HasPrefix(path, never+"{}") {
			return never, true
		}
	}
	return "", false
}
