package main

import (
	"net"
	"strings"
	"testing"
)

func TestSelectControllerIP(t *testing.T) {
	tests := []struct {
		name, explicit, want, errorText string
		found                           []net.IP
	}{
		{name: "explicit address", explicit: "192.0.2.12", found: []net.IP{net.ParseIP("192.0.2.2")}, want: "192.0.2.12"},
		{name: "one usable interface", found: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("169.254.1.2"), net.ParseIP("192.0.2.2")}, want: "192.0.2.2"},
		{name: "duplicate interface address", found: []net.IP{net.ParseIP("192.0.2.2"), net.ParseIP("192.0.2.2")}, want: "192.0.2.2"},
		{name: "ambiguous interfaces", found: []net.IP{net.ParseIP("192.0.2.2"), net.ParseIP("198.51.100.2")}, errorText: "-controller-ip"},
		{name: "no usable interface", found: []net.IP{net.ParseIP("127.0.0.1")}, errorText: "-controller-ip"},
		{name: "invalid explicit address", explicit: "0.0.0.0", errorText: "IPv4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectControllerIP(tt.explicit, tt.found)
			if tt.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errorText) {
					t.Fatalf("got %q, %v; want error containing %q", got, err, tt.errorText)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}
