package controller

import "testing"

func TestAllowedManagementIPReportsRegisteredAddress(t *testing.T) {
	server, err := NewServer(Options{AllowedIP: "192.0.2.19", ID: "ap-test", CertDER: []byte{1}, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := server.AllowedManagementIP(); got != "192.0.2.19" {
		t.Fatalf("management IP = %q", got)
	}
}

func TestAllowedManagementIPIsOptional(t *testing.T) {
	server, err := NewServer(Options{ID: "ap-test", CertDER: []byte{1}, Config: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := server.AllowedManagementIP(); got != "" {
		t.Fatalf("unpinned AP management IP = %q", got)
	}
	if _, err := NewServer(Options{AllowedIP: "not-an-ip", ID: "ap-test", CertDER: []byte{1}, Config: []byte(`{}`)}); err == nil {
		t.Fatal("invalid source pin accepted")
	}
}
