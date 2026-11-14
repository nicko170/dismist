// Package legacyv0 is the frozen v0 config compiler: it reproduces exactly
// what the ap-identity release of dismistd sends to an AP, whatever changes
// later (design §4.4 "The frozen v0 compiler").
//
//   - config.go and patch.go are verbatim copies of ap-identity's
//     internal/controller/config.go and patch.go, with the model types
//     replaced by this package's frozen copies.
//   - records.go holds frozen copies of the ap-identity record structs and
//     decodes them from the raw sites, aps and networks bucket JSON, never
//     through live internal/model types, so new fields or changed semantics
//     in model cannot change the output.
//   - ap33.json is a frozen copy of internal/aptemplate/ap33.json as of
//     ap-identity; TemplateSHA256 is its hash. The live template must keep
//     that hash (internal/options registry check 6).
//   - A runtime template (dismistd -ap-config) replaces the bundled one,
//     exactly as the running binary does: callers pass the template bytes
//     the daemon loaded, and Template(nil) is the frozen bundled copy.
//
// Do not edit config.go, patch.go or ap33.json. legacyv0 is used by the
// schema-2 migration, by legacy mode and by the compile parity tests.
package legacyv0
