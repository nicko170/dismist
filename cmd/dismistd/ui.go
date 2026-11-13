package main

import (
	"net/http"

	"github.com/nicko170/dismist/internal/webgate"
)

func operatorHandler(tokenPath string, next http.Handler) (http.Handler, error) {
	token, err := webgate.LoadOrCreateToken(tokenPath)
	if err != nil {
		return nil, err
	}
	return webgate.New(token, next), nil
}
