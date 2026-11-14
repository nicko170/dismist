package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
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
	parsed, _ := url.Parse(*endpoint)
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	bundle, err := os.ReadFile(*bundleFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		return fmt.Errorf("%s: no PEM certificates", *bundleFile)
	}

	dialer := &net.Dialer{Timeout: *timeout}
	// Verification is done below against the bundle only, so the chain can
	// be printed even when it fails.
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, port), &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // verified against the bundle below
	})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", *endpoint, err)
	}
	served := conn.ConnectionState().PeerCertificates
	_ = conn.Close()
	if len(served) == 0 {
		return fmt.Errorf("%s served no certificate", *endpoint)
	}
	fmt.Fprintf(env.stdout, "Chain served by %s:\n", net.JoinHostPort(host, port))
	for i, cert := range served {
		fmt.Fprintf(env.stdout, "  %d %s (issuer %s, expires %s)\n", i, cert.Subject, cert.Issuer, cert.NotAfter.UTC().Format("2006-01-02"))
	}
	intermediates := x509.NewCertPool()
	for _, cert := range served[1:] {
		intermediates.AddCert(cert)
	}
	chains, err := served[0].Verify(x509.VerifyOptions{
		DNSName: host, Roots: roots, Intermediates: intermediates,
		CurrentTime: env.now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("%w: %v", errTrustMismatch, err)
	}
	anchor := chains[0][len(chains[0])-1]
	fmt.Fprintf(env.stdout, "OK: verifies against %s, anchored at %s\n", *bundleFile, anchor.Subject)
	return nil
}
