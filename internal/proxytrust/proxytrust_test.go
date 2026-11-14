package proxytrust

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePrefixes(t *testing.T) {
	prefixes, err := ParsePrefixes(" 10.244.0.0/16, fd00:10:244::/48 ,192.0.2.7,, ::ffff:198.51.100.1 ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.244.0.0/16", "fd00:10:244::/48", "192.0.2.7/32", "198.51.100.1/32"}
	if len(prefixes) != len(want) {
		t.Fatalf("prefixes = %v, want %v", prefixes, want)
	}
	for i, prefix := range prefixes {
		if prefix.String() != want[i] {
			t.Fatalf("prefix %d = %s, want %s", i, prefix, want[i])
		}
	}
	if prefixes, err := ParsePrefixes(""); err != nil || len(prefixes) != 0 {
		t.Fatalf("empty list = %v, %v", prefixes, err)
	}
	if _, err := ParsePrefixes("10.0.0.0/8,not-an-ip"); err == nil {
		t.Fatal("invalid entry was accepted")
	}
}

func TestHTTPS(t *testing.T) {
	trusted, err := ParsePrefixes("10.244.0.0/16,fd00:10:244::/48")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote, proto string
		tls, want           bool
	}{
		{"direct TLS", "203.0.113.9:4000", "", true, true},
		{"plain HTTP", "10.244.3.199:4000", "", false, false},
		{"trusted v4 proxy", "10.244.3.199:4000", "https", false, true},
		{"trusted v6 proxy", "[fd00:10:244:3::ebbf]:4000", "https", false, true},
		{"trusted proxy reports http", "10.244.3.199:4000", "http", false, false},
		{"untrusted peer", "203.0.113.9:4000", "https", false, false},
		{"loopback is always trusted", "127.0.0.1:4000", "https", false, true},
		{"mapped trusted peer", "[::ffff:10.244.1.2]:4000", "https", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			handler := Middleware(trusted, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				got = HTTPS(request)
			}))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = tc.remote
			if tc.proto != "" {
				request.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			if tc.tls {
				request.TLS = &tls.ConnectionState{}
			}
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if got != tc.want {
				t.Fatalf("HTTPS = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMiddlewareWithoutPrefixesTrustsOnlyLoopback(t *testing.T) {
	var got bool
	handler := Middleware(nil, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		got = HTTPS(request)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.244.3.199:4000"
	request.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if got {
		t.Fatal("forwarded scheme trusted without a configured proxy")
	}
}
