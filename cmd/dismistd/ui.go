package main

import (
	"net/http"

	"github.com/nicko170/dismist/internal/webgate"
)

// operatorHandler puts the web gate in front of the operator API: the
// administrator token file signs in (break-glass) and, when tokens is not
// nil, API bearer tokens are accepted too.
func operatorHandler(tokenPath string, next http.Handler, tokens ...webgate.TokenVerifier) (http.Handler, error) {
	token, err := webgate.LoadOrCreateToken(tokenPath)
	if err != nil {
		return nil, err
	}
	gate := webgate.New(token, next)
	if len(tokens) > 0 && tokens[0] != nil {
		gate.WithTokens(tokens[0])
	}
	return gate, nil
}
