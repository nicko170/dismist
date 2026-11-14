package httpapi

import (
	"sort"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// keyPaths lists the JSON key paths of a filled value ("a.b", lists as
// "a[]"), the shape its JSON has.
func keyPaths(value any, path string, out map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			child := child
			next := key
			if path != "" {
				next = path + "." + key
			}
			out[next] = true
			keyPaths(child, next, out)
		}
	case []any:
		if len(typed) > 0 {
			keyPaths(typed[0], path+"[]", out)
		}
	}
}

// TestTypedViewsMatchRecords checks the typed API views the API decodes
// straight from workstream A's records (viewFrom, patchRecord) against
// those records' JSON: a view key the record does not have reads as zero
// and cannot be written (design §4.3 keys are shared).
func TestTypedViewsMatchRecords(t *testing.T) {
	cases := []struct {
		name         string
		view, record any
		// viewOnly are view keys the API computes, not stored ones.
		viewOnly []string
	}{
		{"management", &api.ManagementView{}, &model.ManagementDefaults{}, nil},
		{"ap_defaults", &api.APDefaultsView{}, &model.APDefaults{}, nil},
		{"protection", &api.ProtectionView{}, &model.Protection{}, nil},
		{"syslog", &api.SyslogTarget{}, &model.SyslogTarget{}, nil},
		{"rf band", &api.BandProfileView{}, &model.BandProfile{}, []string{"valid_channels", "valid_channels[].width", "valid_channels[].primaries", "valid_channels[].primaries[].channel", "valid_channels[].primaries[].dfs", "valid_channels[].blocks"}},
		// "disabled" is the API's inverse of the record's "enabled" (apOverrides).
		{"ap rf override", &api.RadioOverride{}, &model.RadioOverride{}, []string{"disabled"}},
		{"wlan", &api.WLANView{}, &model.Network{}, []string{
			"experimental_in_use", "expert", "expert.field", "expert.has_patch", "expert.overridden_on", "expert.overridden_on.x",
			"expert.partly_shadowed", "expert.shadowed", "has_psk", "live", "live.aps_broadcasting", "live.aps_total", "live.clients",
			"mpsk.keys[].has_passphrase", "live_vaps", "live_vaps[].ap_id", "live_vaps[].ap_name", "live_vaps[].band",
			"live_vaps[].bssid", "live_vaps[].dev", "live_vaps[].clients", "mdid", "radius.acct[].has_secret", "radius.auth[].has_secret", "radius.coa.servers[].has_secret",
		}},
	}
	for _, tc := range cases {
		viewKeys, recordKeys := map[string]bool{}, map[string]bool{}
		keyPaths(producerJSON(t, tc.view), "", viewKeys)
		keyPaths(producerJSON(t, tc.record), "", recordKeys)
		skip := map[string]bool{}
		for _, key := range tc.viewOnly {
			skip[key] = true
		}
		var missing []string
		for key := range viewKeys {
			if !recordKeys[key] && !skip[key] {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: the API view has keys the record does not store:\n  %s", tc.name, strings.Join(missing, "\n  "))
		}
		var hidden []string
		for key := range recordKeys {
			if !viewKeys[key] {
				hidden = append(hidden, key)
			}
		}
		sort.Strings(hidden)
		if len(hidden) > 0 {
			t.Logf("%s: record keys the view does not show: %s", tc.name, strings.Join(hidden, ", "))
		}
	}
}
