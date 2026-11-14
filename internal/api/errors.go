package api

// ErrorCode is the machine-readable code of an error response (design §4.8).
type ErrorCode string

const (
	// ErrorCodeInvalid: the request is malformed or a field is invalid (400,
	// or 422 for a config the firmware rules refuse).
	ErrorCodeInvalid ErrorCode = "invalid"
	// ErrorCodeNotFound: the object or route does not exist (404).
	ErrorCodeNotFound ErrorCode = "not_found"
	// ErrorCodeConflict: the change conflicts with stored state (409).
	ErrorCodeConflict ErrorCode = "conflict"
	// ErrorCodeStale: If-Match did not match updated_rev (409, phase 2).
	ErrorCodeStale ErrorCode = "stale"
	// ErrorCodeForbiddenPath: an expert patch writes a forbidden path (422).
	ErrorCodeForbiddenPath ErrorCode = "forbidden_path"
	// ErrorCodeDuplicateKey: two keys of a patch fold to one firmware key (422).
	ErrorCodeDuplicateKey ErrorCode = "duplicate_key"
	// ErrorCodeExperimentalDisabled: an experimental value on a site without
	// experimental: true (422).
	ErrorCodeExperimentalDisabled ErrorCode = "experimental_disabled"
	// ErrorCodeFirmwareRule: the compiled config breaks a firmware rule (422).
	ErrorCodeFirmwareRule ErrorCode = "firmware_rule"
	// ErrorCodeSecretRequired: a restore needs a secret that differs (422).
	ErrorCodeSecretRequired ErrorCode = "secret_required"
	// ErrorCodeMigrationPending: typed editing while in legacy mode (503).
	ErrorCodeMigrationPending ErrorCode = "migration_pending"
	// ErrorCodeUnverifiedCommand: an AP command the firmware is not known to
	// support (501).
	ErrorCodeUnverifiedCommand ErrorCode = "unverified_command"
	// ErrorCodeUnauthorized: no valid session or token (401).
	ErrorCodeUnauthorized ErrorCode = "unauthorized"
	// ErrorCodeRateLimited: too many requests, or one already running (429).
	ErrorCodeRateLimited ErrorCode = "rate_limited"

	// The codes below are additions to the §4.8 list.

	// ErrorCodeForbidden: authenticated but not allowed: a read-scope token
	// writing or reading an admin-only resource, a cross-origin write, or a
	// host that is not allowed (403).
	ErrorCodeForbidden ErrorCode = "forbidden"
	// ErrorCodeNotReady: the daemon is starting, migrating, or a readiness check
	// failed (503).
	ErrorCodeNotReady ErrorCode = "not_ready"
	// ErrorCodeUnavailable: the feature is not available on this store or build
	// (501, 503).
	ErrorCodeUnavailable ErrorCode = "unavailable"
	// ErrorCodeInternal: the server failed (500). The message never carries an
	// internal error string.
	ErrorCodeInternal ErrorCode = "internal"
)

// ErrorBody is every non-2xx response, including webgate's (design §4.8).
// Fields maps a JSON path of the request (or an option key) to a message
// for that field; it is always present, empty when no field is at fault.
type ErrorBody struct {
	// Error is one sentence for humans.
	Error  string            `json:"error"`
	Code   ErrorCode         `json:"code"`
	Fields map[string]string `json:"fields"`
}
