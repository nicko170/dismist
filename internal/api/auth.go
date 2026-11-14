package api

import "time"

// TokenScope is an API token scope (design §4.8).
type TokenScope string

const (
	// TokenScopeRead allows GET and HEAD, except admin-only resources (backups,
	// the normalised config view).
	TokenScopeRead TokenScope = "read"
	// TokenScopeAdmin allows everything a signed-in session can do.
	TokenScopeAdmin TokenScope = "admin"
)

// AuthVia says how a request was authenticated.
type AuthVia string

const (
	AuthViaSession AuthVia = "session"
	AuthViaToken   AuthVia = "token"
)

// AuthSession is GET /auth/session. Unauthenticated requests get 401 with
// an ErrorBody that also carries "authenticated": false.
type AuthSession struct {
	Authenticated bool `json:"authenticated"`
	// ExpiresAt is the session expiry; null for a bearer token.
	ExpiresAt *time.Time `json:"expires_at" tstype:"string | null,required"`
	Via       AuthVia    `json:"via,omitempty"`
	Scope     TokenScope `json:"scope,omitempty"`
	// TokenName is the API token's name when Via is "token".
	TokenName string `json:"token_name,omitempty"`
}

// LoginRequest is POST /auth/login. Token is the administrator token from
// the admin token file.
type LoginRequest struct {
	Token string `json:"token"`
}

// TokenView is one API token (GET /api/system/tokens). The secret is never
// returned after creation.
type TokenView struct {
	// ID is "dsm_<16 hex>"; the bearer token is "<id>_<secret>".
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Scope    TokenScope `json:"scope"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used" tstype:"string | null,required"`
}

// TokenCreated is the response to POST /api/system/tokens: the token view
// plus the bearer token, shown once.
type TokenCreated struct {
	TokenView `tstype:",extends"`
	Token     string `json:"token"`
}

// TokenInput is POST /api/system/tokens.
type TokenInput struct {
	Name  string     `json:"name"`
	Scope TokenScope `json:"scope"`
}

// TokenList is GET /api/system/tokens.
type TokenList struct {
	Tokens []TokenView `json:"tokens"`
}

// AuthSessionView is one signed-in web session (GET /api/system/sessions,
// phase 1). ID is the stored key (a hash of the cookie value).
type AuthSessionView struct {
	ID        string     `json:"id"`
	Created   time.Time  `json:"created"`
	Expires   time.Time  `json:"expires"`
	LastUsed  *time.Time `json:"last_used" tstype:"string | null,required"`
	Remote    string     `json:"remote"`
	UserAgent string     `json:"user_agent"`
	Current   bool       `json:"current"`
}
