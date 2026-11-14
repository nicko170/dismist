package metrics

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestRoute(t *testing.T) {
	for path, want := range map[string]string{
		"/healthz":                          "/healthz",
		"/auth/login":                       "/auth/login",
		"/auth/whatever":                    "/auth/other",
		"/api/aps":                          "/api/aps",
		"/api/aps/pending":                  "/api/aps/pending",
		"/api/aps/02-aa-00-00-00-01":        "/api/aps/{id}",
		"/api/aps/02-aa-00-00-00-01/config": "/api/aps/{id}/config",
		"/api/aps/02-aa-00-00-00-01/actions/reboot": "/api/aps/{id}/actions/{id}",
		"/api/sites/abc/stream":                     "/api/sites/{id}/stream",
		"/api/sites/abc/networks/def/expert":        "/api/sites/{id}/networks/{id}/expert",
		"/api/sites/abc/clients/02-aa-00-00-10-01":  "/api/sites/{id}/clients/{id}",
		"/api/system/sessions/revoke-all":           "/api/system/sessions/revoke-all",
		"/api/system/sessions/s1":                   "/api/system/sessions/{id}",
		"/api/sites/abc/made/up/path":               "/api/other",
		"/api/sites//networks":                      "/api/other",
		"/api/" + strings.Repeat("x/", 50):          "/api/other",
		"/_ui/index-abc123.js":                      "static",
		"/legacy/":                                  "legacy",
		"/sites/abc/overview":                       "ui",
		"/":                                         "ui",
	} {
		if got := Route(path); got != want {
			t.Errorf("Route(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestHTTPInstrumentation(t *testing.T) {
	instruments := NewInstruments()
	streamOpen := make(chan struct{})
	streamDone := make(chan struct{})
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			http.Error(w, "nope", http.StatusUnauthorized)
		case "/api/sites/s1/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: snapshot\ndata: {}\n\n")
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			_, _ = io.WriteString(w, "event: ap\ndata: {\"a\":")
			_, _ = io.WriteString(w, "1}\n\n")
			w.(http.Flusher).Flush()
			close(streamOpen)
			<-streamDone
		default:
			_, _ = io.WriteString(w, "ok")
		}
	})
	server := httptest.NewServer(instruments.HTTP(app))
	defer server.Close()
	for _, path := range []string{"/api/aps", "/api/aps", "/sites/x"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	response, err := http.Post(server.URL+"/auth/login", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	go func() {
		if response, err := http.Get(server.URL + "/api/sites/s1/stream"); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	<-streamOpen
	out := scrape(t, instruments)
	expectLines(t, out,
		`dismist_http_requests_total{code="200",route="/api/aps"} 2`,
		`dismist_http_requests_total{code="200",route="ui"} 1`,
		`dismist_http_requests_total{code="401",route="/auth/login"} 1`,
		"dismist_ui_login_failures_total 1",
		"dismist_ui_streams 1",
		"dismist_ui_stream_updates_total 2",
		`dismist_http_request_duration_seconds_count{route="/api/aps"} 2`,
	)
	close(streamDone)
	deadline := time.Now().Add(5 * time.Second)
	for instruments.streams.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out = scrape(t, instruments)
	expectLines(t, out, "dismist_ui_streams 0", `dismist_http_requests_total{code="200",route="/api/sites/{id}/stream"} 1`)
	if strings.Contains(out, `dismist_http_request_duration_seconds_count{route="/api/sites/{id}/stream"}`) {
		t.Error("the stream route was timed")
	}
}

func TestTLSErrorLog(t *testing.T) {
	instruments := NewInstruments()
	var forwarded bytes.Buffer
	logger := instruments.TLSErrorLog(&forwarded)
	logger.Printf("http: TLS handshake error from 192.0.2.9:4242: remote error: tls: unknown certificate authority")
	logger.Printf("http: TLS handshake error from 192.0.2.9:4243: EOF")
	logger.Printf("http: TLS handshake error from 192.0.2.9:4244: read tcp 10.0.0.1:8443->192.0.2.9:4244: read: connection reset by peer")
	logger.Printf("http: TLS handshake error from 192.0.2.9:4245: tls: first record does not look like a TLS handshake")
	logger.Printf("http: something else")
	expectLines(t, scrape(t, instruments),
		`dismist_ap_tls_handshake_errors_total{reason="client_rejected"} 1`,
		`dismist_ap_tls_handshake_errors_total{reason="closed"} 2`,
		`dismist_ap_tls_handshake_errors_total{reason="protocol"} 1`,
	)
	if !strings.Contains(forwarded.String(), "something else") || !strings.Contains(forwarded.String(), "TLS handshake error") {
		t.Fatalf("lines not forwarded: %q", forwarded.String())
	}
	var nilInstruments *Instruments
	nilInstruments.TLSErrorLog(io.Discard).Printf("TLS handshake error")
	nilInstruments.ObservationError()
	if nilInstruments.HTTP(http.NotFoundHandler()) == nil || nilInstruments.DNS(dns.HandlerFunc(func(dns.ResponseWriter, *dns.Msg) {})) == nil {
		t.Fatal("nil instruments must pass handlers through")
	}
}

type fakeDNSWriter struct {
	dns.ResponseWriter
	written *dns.Msg
}

func (f *fakeDNSWriter) WriteMsg(m *dns.Msg) error { f.written = m; return nil }

func TestDNSInstrumentation(t *testing.T) {
	instruments := NewInstruments()
	for _, rcode := range []struct {
		rcode         int
		authoritative bool
	}{
		{dns.RcodeSuccess, true}, {dns.RcodeSuccess, false}, {dns.RcodeNameError, false},
		{dns.RcodeRefused, false}, {dns.RcodeServerFailure, false},
	} {
		handler := instruments.DNS(dns.HandlerFunc(func(w dns.ResponseWriter, query *dns.Msg) {
			answer := new(dns.Msg)
			answer.SetReply(query)
			answer.Rcode, answer.Authoritative = rcode.rcode, rcode.authoritative
			_ = w.WriteMsg(answer)
		}))
		query := new(dns.Msg)
		query.SetQuestion("example.com.", dns.TypeA)
		writer := &fakeDNSWriter{}
		handler.ServeDNS(writer, query)
		if writer.written == nil {
			t.Fatal("answer not passed through")
		}
	}
	instruments.ObservationError()
	expectLines(t, scrape(t, instruments),
		`dismist_dns_queries_total{result="override"} 1`,
		`dismist_dns_queries_total{result="forwarded"} 2`,
		`dismist_dns_queries_total{result="refused"} 1`,
		`dismist_dns_queries_total{result="error"} 1`,
		"dismist_store_observation_errors_total 1",
		`dismist_ap_tls_handshake_errors_total{reason="client_rejected"} 0`,
	)
}

func TestCertificateSeries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example Root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(10 * 365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(rootDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "ap.example.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(13 * 24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("192.0.2.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, root, &leafKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	certs := CertificateSeries([][]byte{leafDER, rootDER}, [][]byte{rootDER, []byte("not a certificate")})
	if len(certs) != 3 {
		t.Fatalf("certificates = %+v", certs)
	}
	want := []Certificate{
		{Listener: "ap-wss", Role: "leaf", CN: "ap.example.com"},
		{Listener: "ap-wss", Role: "root", CN: "Example Root"},
		{Listener: "ap-onboarding", Role: "root", CN: "Example Root"},
	}
	for index, cert := range certs {
		if cert.Listener != want[index].Listener || cert.Role != want[index].Role || cert.CN != want[index].CN {
			t.Errorf("certificate %d = %+v, want %+v", index, cert, want[index])
		}
	}
	if !certs[0].NotAfter.Equal(now.Add(13 * 24 * time.Hour)) {
		t.Errorf("leaf not after = %v", certs[0].NotAfter)
	}
	many := make([][]byte, 40)
	for index := range many {
		many[index] = rootDER
	}
	if got := len(CertificateSeries(many, many)); got != maxCertificates {
		t.Errorf("certificates not capped: %d", got)
	}
}
