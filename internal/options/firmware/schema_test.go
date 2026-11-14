package firmware

import (
	"strings"
	"testing"
)

func TestEmbeddedSchemaParses(t *testing.T) {
	schema := AP33()
	if len(schema.Paths()) < 1500 {
		t.Fatalf("only %d paths", len(schema.Paths()))
	}
	for path, want := range map[string]Kind{
		"WLANs":                                  KindSlice,
		"WLANs[]":                                KindStruct,
		"WLANs[].Auth.AKM.WPA3_PSK":              KindBool,
		"WLANs[].Auth.Type":                      KindCustom,
		"WLANs[].Auth.MultiPSK":                  KindStruct,
		"WLANs[].Vlans":                          KindSlice,
		"WLANs[].Vlans[]":                        KindString,
		"WLANs[].civic_location":                 KindStruct,
		"Radios[].DFSjumplist":                   KindStruct,
		"Radios[].Power":                         KindInt,
		"Radios[].Band":                          KindCustom,
		"SVIs[].DHCP_Ena":                        KindPointer,
		"APState":                                KindPointer,
		"VLANNames":                              KindMap,
		"VLANNames{}":                            KindInt,
		"XYZ":                                    KindArray,
		"BootConfig":                             KindRaw,
		"Timestamp":                              KindFloat,
		"WLANs[].Passpoint.MacAddress":           KindString,
		"SwitchPorts[].WiredAuth.DVlan.Vlan_IDs": KindSlice,
	} {
		node, ok := schema.Lookup(path)
		if !ok {
			t.Errorf("missing %s", path)
			continue
		}
		if node.Kind != want {
			t.Errorf("%s kind %s, want %s", path, node.Kind, want)
		}
	}
	for _, absent := range []string{"SiteName", "RootAuthorizedSSHKeys", "WLANs[].ForFTM", "WLANs[].AMPDU.RST", "SVIs[].dhcp_enabled", "WLANs[].vlans", "WLANs[].pvid"} {
		if _, ok := schema.Lookup(absent); ok {
			t.Errorf("%s should not be a 0.15 path", absent)
		}
	}
	if node, _ := schema.Lookup("APState"); node.Pointee != KindStruct || len(node.Fields) == 0 {
		t.Fatalf("pointer to struct: %+v", node)
	}
	if node, _ := schema.Lookup("SVIs[].DHCP_Ena"); node.Pointee != KindBool {
		t.Fatalf("pointer to bool: %+v", node)
	}
	if node, _ := schema.Lookup("XYZ"); node.ArrayLen != 3 {
		t.Fatalf("array length %d", node.ArrayLen)
	}
}

func TestFieldMatchingFollowsEncodingJSON(t *testing.T) {
	wlan, _ := AP33().Lookup("WLANs[]")
	for _, key := range []string{"RoamMode", "roamMode", "ROAMMODE", "roammode"} {
		if field, ok := wlan.Field(key); !ok || field.Name != "RoamMode" {
			t.Fatalf("%s did not fold to RoamMode", key)
		}
	}
	if _, ok := wlan.Field("ForFTM"); ok {
		t.Fatal("unknown key matched")
	}
	if FoldKey("K") != FoldKey("k") || FoldKey("ſ") != FoldKey("S") {
		t.Fatal("Unicode simple folding not applied like bytes.EqualFold")
	}
	got, ok := AP33().Canonical("wlans[].auth.akm.wpa3_psk")
	if !ok || got != "WLANs[].Auth.AKM.WPA3_PSK" {
		t.Fatalf("canonical %q %v", got, ok)
	}
	if got, ok := AP33().Canonical("vlannames{}"); !ok || got != "VLANNames{}" {
		t.Fatalf("map canonical %q %v", got, ok)
	}
	if _, ok := AP33().Canonical("WLANs[].ForFTM"); ok {
		t.Fatal("unknown path canonicalised")
	}
}

func TestParseRejectsBadLists(t *testing.T) {
	for _, bad := range []string{
		"A\tbool\n",                             // too few columns
		"A.B\tbool\tbool\n",                     // parent missing
		"A\tstruct {}\tstruct\nA\tbool\tbool\n", // duplicate
	} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
