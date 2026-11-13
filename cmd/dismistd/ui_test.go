package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorHandlerBootstrapsTokenAndProtectsAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "admin.token")
	requests := 0
	underlying := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.WriteHeader(http.StatusOK)
	})
	handler, err := operatorHandler(path, underlying)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private admin token was not created: %v %+v", err, info)
	}
	request := httptest.NewRequest(http.MethodGet, "http://192.0.2.10:8088/api/sites", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || requests != 0 {
		t.Fatalf("unauthed API access: status=%d requests=%d", response.Code, requests)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	request = httptest.NewRequest(http.MethodPost, "http://192.0.2.10:8088/auth/login", strings.NewReader(`{"token":"`+token+`"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "http://192.0.2.10:8088/api/sites", nil)
	request.AddCookie(response.Result().Cookies()[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || requests != 1 {
		t.Fatalf("authed API access: status=%d requests=%d", response.Code, requests)
	}
}
