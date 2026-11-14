// Package api is the HTTP API contract of dismistd (design §4.8 to §4.10):
// the Go DTOs every handler encodes, and through tygo the TypeScript types
// in ui/src/gen/api.gen.ts. Nobody writes a second copy of these shapes.
//
// Rules (design §8.1, §8.2):
//   - Workstream C owns this package; A, B, E, F and H review changes.
//   - Within a phase the contract changes only additively: new types and
//     new fields, never a renamed or retyped field.
//   - The same change regenerates ui/src/gen/api.gen.ts (go generate
//     ./internal/api); the file is never edited by hand.
//
// TypeScript mapping conventions:
//   - time.Time is an RFC 3339 string.
//   - A pointer field without omitempty is always present and may be null;
//     it carries `tstype:"T | null,required"` so the TS type says so.
//   - omitempty and omitzero fields are optional (`field?: T`).
//   - String enums are named string types with a const group; the TS output
//     is a union of the values.
package api

//go:generate go tool tygo generate --config tygo.yaml
