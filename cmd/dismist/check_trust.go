package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"time"
)

// errTrustMismatch marks a served chain that the bundle does not verify.
var errTrustMismatch = errors.New("the served certificate chain does not verify against the bundle")

// runCheckTrust verifies the chain a WSS endpoint serves against a CA bundle
// alone, as the AP will: no system roots, the URL's host as the name.
// openssl s_client is not a substitute on macOS, where LibreSSL also loads
// the system roots and passes a bundle that would fail on the AP.
func runCheckTrust(args []string, env ap33Env) error {
	const usage = "usage: dismist ap33 check-trust --url wss://HOST[:PORT]/ws --bundle FILE"
	flags := flag.NewFlagSet("dismist ap33 check-trust", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	endpoint := flags.String("url", "", "the WSS URL in the AP's cloud.ini")
	bundleFile := flags.String("bundle", "", "the CA bundle the AP will use (cacerts-production.pem)")
	timeout := flags.Duration("timeout", 10*time.Second, "connection timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *endpoint == "" || *bundleFile == "" || flags.NArg() != 0 {
		return errors.New(usage)
	}
	if err := checkCloudURL(*endpoint); err != nil {
		return err
	}
	bundle, err := os.ReadFile(*bundleFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		return fmt.Errorf("%s: no PEM certificates", *bundleFile)
	}
	chain, err := fetchServedChain(env.ctx, *endpoint, *timeout)
	if err != nil {
		return err
	}
	printServedChain(env.stdout, chain)
	anchor, err := chain.verify(roots, env.now())
	if err != nil {
		return err
	}
	fmt.Fprintf(env.stdout, "OK: verifies against %s, anchored at %s\n", *bundleFile, anchor.Subject)
	return nil
}

// servedChain is what a WSS endpoint presented in its TLS handshake.
type servedChain struct {
	Addr  string
	Host  string
	Certs []*x509.Certificate
}

// fetchServedChain connects to the WSS URL's host and returns the chain it
// serves, without verifying it: verification happens against a bundle
// alone, so the chain can be printed even when it fails.
func fetchServedChain(ctx context.Context, endpoint string, timeout time.Duration) (servedChain, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return servedChain{}, err
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config: &tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, //nolint:gosec // verified against the bundle by the caller
		},
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return servedChain{}, fmt.Errorf("connect to %s: %w", endpoint, err)
	}
	served := conn.(*tls.Conn).ConnectionState().PeerCertificates
	_ = conn.Close()
	if len(served) == 0 {
		return servedChain{}, fmt.Errorf("%s served no certificate", endpoint)
	}
	return servedChain{Addr: addr, Host: host, Certs: served}, nil
}

// verify checks the served chain against roots alone, as the AP does, and
// returns the trust anchor it chains to.
func (c servedChain) verify(roots *x509.CertPool, now time.Time) (*x509.Certificate, error) {
	intermediates := x509.NewCertPool()
	for _, cert := range c.Certs[1:] {
		intermediates.AddCert(cert)
	}
	chains, err := c.Certs[0].Verify(x509.VerifyOptions{
		DNSName: c.Host, Roots: roots, Intermediates: intermediates,
		CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errTrustMismatch, err)
	}
	return chains[0][len(chains[0])-1], nil
}

func printServedChain(w io.Writer, chain servedChain) {
	fmt.Fprintf(w, "Chain served by %s:\n", chain.Addr)
	for i, cert := range chain.Certs {
		fmt.Fprintf(w, "  %d %s (issuer %s, expires %s)\n", i, cert.Subject, cert.Issuer, cert.NotAfter.UTC().Format("2006-01-02"))
	}
}
