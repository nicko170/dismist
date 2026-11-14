package controller

import (
	"fmt"
	"sort"
	"strings"
)

// Feature is the fixed name of a per-AP protocol feature (design §5.7):
// a new frame dismist sends to the AP that can be switched on and off at
// runtime, per AP, without a restart. Recording-only changes (no frame sent
// to the AP) are not features.
type Feature string

// The fixed feature names. Every one is off unless switched on with
// -ap-features or APRecord.Features (protocol_features). Phase 0 ships the
// framework only: no handler consults a feature yet.
const (
	// FeaturePong2 answers ping2 with a protobuf Pong2 instead of an echo.
	FeaturePong2 Feature = "pong2"
	// FeatureSubscribeReply answers ep-subscribe with the cloud's reply.
	FeatureSubscribeReply Feature = "subscribe_reply"
	// FeatureFileDiscard answers ep-file offers with "discard".
	FeatureFileDiscard Feature = "file_discard"
	// FeatureSiteRest404 answers wlc-site-rest GETs with a 404 at once.
	FeatureSiteRest404 Feature = "site_rest_404"
	// FeatureConfigChangeNotify pushes config with ep-config-change instead
	// of a reconnect (§5.6).
	FeatureConfigChangeNotify Feature = "config_change_notify"
)

// allFeatures is the fixed list, in display order. Bit i of Features is
// allFeatures[i]; append only, never reorder.
var allFeatures = []Feature{
	FeaturePong2,
	FeatureSubscribeReply,
	FeatureFileDiscard,
	FeatureSiteRest404,
	FeatureConfigChangeNotify,
}

// AllFeatures returns every feature name in display order.
func AllFeatures() []Feature { return append([]Feature(nil), allFeatures...) }

// FeatureNames returns every feature name as strings, in display order.
func FeatureNames() []string {
	names := make([]string, len(allFeatures))
	for index, feature := range allFeatures {
		names[index] = string(feature)
	}
	return names
}

// ValidFeature reports whether name is one of the fixed feature names.
func ValidFeature(name string) bool { return featureBit(Feature(name)) != 0 }

func featureBit(feature Feature) Features {
	for index, known := range allFeatures {
		if known == feature {
			return 1 << index
		}
	}
	return 0
}

// Features is a set of switched-on protocol features. The zero value has
// every feature off. It is a small value type, so a Server can swap it
// atomically and a change takes effect on the next frame.
type Features uint32

// Has reports whether feature is on.
func (f Features) Has(feature Feature) bool {
	bit := featureBit(feature)
	return bit != 0 && f&bit != 0
}

// With returns f with feature switched on. Unknown names are ignored.
func (f Features) With(feature Feature) Features { return f | featureBit(feature) }

// Without returns f with feature switched off.
func (f Features) Without(feature Feature) Features { return f &^ featureBit(feature) }

// List returns the switched-on features in display order.
func (f Features) List() []Feature {
	var result []Feature
	for _, feature := range allFeatures {
		if f.Has(feature) {
			result = append(result, feature)
		}
	}
	return result
}

// Map returns every fixed feature with its state, for views and metrics.
func (f Features) Map() map[string]bool {
	result := make(map[string]bool, len(allFeatures))
	for _, feature := range allFeatures {
		result[string(feature)] = f.Has(feature)
	}
	return result
}

// String returns the switched-on names, comma-separated (the -ap-features
// syntax).
func (f Features) String() string {
	names := make([]string, 0, len(allFeatures))
	for _, feature := range f.List() {
		names = append(names, string(feature))
	}
	return strings.Join(names, ",")
}

// ParseFeatures parses the -ap-features value: comma-separated fixed
// names, with surrounding space ignored. An empty value switches nothing
// on. An unknown name is an error, so a typo cannot silently do nothing.
func ParseFeatures(value string) (Features, error) {
	var result Features
	var unknown []string
	for _, part := range strings.Split(value, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		bit := featureBit(Feature(name))
		if bit == 0 {
			unknown = append(unknown, name)
			continue
		}
		result |= bit
	}
	if len(unknown) != 0 {
		return 0, fmt.Errorf("unknown protocol feature %s (known: %s)",
			strings.Join(unknown, ", "), strings.Join(FeatureNames(), ", "))
	}
	return result, nil
}

// ResolveFeatures applies an AP's stored overrides (APRecord.Features) to
// the daemon default: true switches a feature on, false off, absent keeps
// the default. Unknown override names are ignored here; UnknownFeatures
// reports them for validation.
func ResolveFeatures(defaults Features, overrides map[string]bool) Features {
	result := defaults
	for name, on := range overrides {
		bit := featureBit(Feature(name))
		if bit == 0 {
			continue
		}
		if on {
			result |= bit
		} else {
			result &^= bit
		}
	}
	return result
}

// UnknownFeatures returns the override names that are not fixed feature
// names, sorted. An API that stores overrides rejects these.
func UnknownFeatures(overrides map[string]bool) []string {
	var unknown []string
	for name := range overrides {
		if !ValidFeature(name) {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}
