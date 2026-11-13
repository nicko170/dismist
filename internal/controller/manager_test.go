package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerRoutesOnlyRegisteredAPAddresses(t *testing.T) {
	first, _ := NewServer(Options{AllowedIP: "127.0.0.1", ID: "ap-a", CertDER: []byte{1}, Config: []byte(`{"WLANs":[]}`)})
	second, _ := NewServer(Options{AllowedIP: "127.0.0.2", ID: "ap-b", CertDER: []byte{1}, Config: []byte(`{"WLANs":[]}`)})
	manager := NewManager()
	if err := manager.Register("ap-a", "127.0.0.1", first); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("ap-b", "127.0.0.2", second); err != nil {
		t.Fatal(err)
	}
	if got, ok := manager.Server("ap-b"); !ok || got != second {
		t.Fatal("second AP did not get its own control session")
	}
	if err := manager.Register("ap-c", "127.0.0.1", second); err == nil {
		t.Fatal("duplicate source address accepted")
	}
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	request.RemoteAddr = "127.0.0.3:1234"
	response := httptest.NewRecorder()
	manager.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unregistered address accepted: %d", response.Code)
	}
	manager.Remove("ap-b")
	if _, ok := manager.Server("ap-b"); ok {
		t.Fatal("removed AP still registered")
	}
}
