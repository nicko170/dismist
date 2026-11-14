package controller

import (
	"reflect"
	"strings"
	"testing"
)

// TestFeatureNamesAreTheFixedList pins the §5.7 names: they are API and
// metric label values, so they never change.
func TestFeatureNamesAreTheFixedList(t *testing.T) {
	want := []string{"pong2", "subscribe_reply", "file_discard", "site_rest_404", "config_change_notify"}
	if got := FeatureNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("feature names %v", got)
	}
	for _, name := range want {
		if !ValidFeature(name) {
			t.Fatalf("%s not valid", name)
		}
	}
	if ValidFeature("ep_ip_register") || ValidFeature("") {
		t.Fatal("unknown names accepted")
	}
}

func TestParseAndResolveFeatures(t *testing.T) {
	none, err := ParseFeatures("")
	if err != nil || none != 0 || none.String() != "" {
		t.Fatalf("empty -ap-features: %v %v", none, err)
	}
	parsed, err := ParseFeatures(" pong2 , file_discard,")
	if err != nil || !parsed.Has(FeaturePong2) || !parsed.Has(FeatureFileDiscard) || parsed.Has(FeatureSiteRest404) {
		t.Fatalf("parsed %v %v", parsed, err)
	}
	if parsed.String() != "pong2,file_discard" {
		t.Fatalf("String = %q", parsed.String())
	}
	if _, err := ParseFeatures("pong2,pong3"); err == nil || !strings.Contains(err.Error(), "pong3") {
		t.Fatalf("unknown feature accepted: %v", err)
	}
	resolved := ResolveFeatures(parsed, map[string]bool{"pong2": false, "subscribe_reply": true, "bogus": true})
	if resolved.Has(FeaturePong2) || !resolved.Has(FeatureSubscribeReply) || !resolved.Has(FeatureFileDiscard) {
		t.Fatalf("resolved %v", resolved)
	}
	if unknown := UnknownFeatures(map[string]bool{"bogus": true, "pong2": true, "also": false}); !reflect.DeepEqual(unknown, []string{"also", "bogus"}) {
		t.Fatalf("unknown %v", unknown)
	}
	states := resolved.Map()
	if len(states) != len(FeatureNames()) || !states["subscribe_reply"] || states["pong2"] {
		t.Fatalf("map %v", states)
	}
	if got := resolved.Without(FeatureFileDiscard).With(FeatureConfigChangeNotify).List(); !reflect.DeepEqual(got, []Feature{FeatureSubscribeReply, FeatureConfigChangeNotify}) {
		t.Fatalf("list %v", got)
	}
}

func TestServerFeaturesDefaultOffAndSwitchAtRuntime(t *testing.T) {
	server, err := NewServer(Options{ID: "ap-f", CertDER: []byte{1}, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range AllFeatures() {
		if server.FeatureEnabled(feature) {
			t.Fatalf("%s on by default", feature)
		}
	}
	server.SetFeatures(Features(0).With(FeatureSiteRest404))
	if !server.FeatureEnabled(FeatureSiteRest404) || server.Features().String() != "site_rest_404" {
		t.Fatal("feature switch not applied")
	}
	configured, err := NewServer(Options{ID: "ap-g", CertDER: []byte{1}, Config: []byte(`{}`), Features: Features(0).With(FeaturePong2)})
	if err != nil || !configured.FeatureEnabled(FeaturePong2) {
		t.Fatalf("Options.Features not applied: %v", err)
	}
}
