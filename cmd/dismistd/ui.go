package main

import (
	"net/http"

	"github.com/nicko170/dismist/internal/webgate"
)

// gateOptions are the optional stores of the web gate.
type gateOptions struct {
	tokens   webgate.TokenVerifier
	sessions webgate.SessionStore
}

// operatorHandler puts the web gate in front of the operator API: the
// administrator token file signs in (break-glass) and, when tokens is not
// nil, API bearer tokens are accepted too.
func operatorHandler(tokenPath string, next http.Handler, tokens ...webgate.TokenVerifier) (http.Handler, error) {
	options := gateOptions{}
	if len(tokens) > 0 {
		options.tokens = tokens[0]
	}
	return operatorGate(tokenPath, next, options)
}

// operatorGate is operatorHandler with persisted sessions (design §4.1:
// sessions survive a pod restart from phase 1).
func operatorGate(tokenPath string, next http.Handler, options gateOptions) (http.Handler, error) {
	token, err := webgate.LoadOrCreateToken(tokenPath)
	if err != nil {
		return nil, err
	}
	gate := webgate.New(token, next)
	if options.tokens != nil {
		gate.WithTokens(options.tokens)
	}
	if options.sessions != nil {
		gate.WithSessions(options.sessions)
	}
	return gate, nil
}
