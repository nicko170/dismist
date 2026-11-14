package main

import (
	"bytes"
	"crypto/x509"
	"embed"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"time"
)

// letsEncryptRootFiles are Let's Encrypt's current self-signed roots: ISRG
// Root X1 and X2 and the Generation Y roots Root YE and Root YR, as
// published at https://letsencrypt.org/certificates/ (test: their SHA-256
// fingerprints). An AP that trusts every root of the hierarchy keeps
// connecting when Let's Encrypt rotates intermediates or moves a chain to
// another root; trusting only the cross-signed copies a chain happens to
// carry today does not.
//
//go:embed roots/letsencrypt/*.crt
var letsEncryptRootFiles embed.FS

// letsEncryptRoots parses the embedded roots.
func letsEncryptRoots() ([]*x509.Certificate, error) {
	names, err := fs.Glob(letsEncryptRootFiles, "roots/letsencrypt/*.crt")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var roots []*x509.Certificate
	for _, name := range names {
		data, err := letsEncryptRootFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		certs, err := parsePEMCertificates(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path.Base(name), err)
		}
		roots = append(roots, certs...)
	}
	return roots, nil
}

// parsePEMCertificates parses every CERTIFICATE block in data. It fails on
// an unparseable certificate or when there is none.
func parsePEMCertificates(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no PEM certificates")
	}
	return certs, nil
}

// mergeCABundle appends to base every certificate in extras that base does
// not already hold (compared by DER). With nothing to add it returns base
// unchanged, byte for byte, so a re-run on an onboarded AP writes nothing.
func mergeCABundle(base []byte, extras []*x509.Certificate) ([]byte, []*x509.Certificate, error) {
	existing, err := parsePEMCertificates(base)
	if err != nil {
		return nil, nil, fmt.Errorf("the AP's CA bundle: %w", err)
	}
	have := make(map[string]bool, len(existing))
	for _, cert := range existing {
		have[string(cert.Raw)] = true
	}
	var added []*x509.Certificate
	for _, cert := range extras {
		if have[string(cert.Raw)] {
			continue
		}
		have[string(cert.Raw)] = true
		added = append(added, cert)
	}
	if len(added) == 0 {
		return base, nil, nil
	}
	merged := bytes.TrimRight(slices.Clone(base), " \t\r\n")
	merged = append(merged, '\n')
	for _, cert := range added {
		merged = append(merged, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}
	return merged, added, nil
}

// certPool builds a pool from certs.
func certPool(certs []*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}

// chainsToAny reports whether chain verifies against roots alone.
func (c servedChain) chainsToAny(roots []*x509.Certificate, now time.Time) bool {
	if len(c.Certs) == 0 || len(roots) == 0 {
		return false
	}
	_, err := c.verify(certPool(roots), now)
	return err == nil
}

// cloudINIEquivalent reports whether the AP's current cloud.ini already
// says what the desired one says: the same [[cloud]] entries (URL, factory
// flag, rootcerts, TLS verification) and the same legacy [tls] rootcerts.
// Entry order, comments, spacing and keys ep-control does not use for the
// connection do not count, so a file that is already right (such as one
// edited by hand at an earlier cutover) is left alone and the AP is not
// rebooted for nothing.
func cloudINIEquivalent(current, desired []byte) bool {
	a, b := parseCloudINI(string(current)), parseCloudINI(string(desired))
	if len(a.Entries) == 0 || len(a.Entries) != len(b.Entries) {
		return false
	}
	if a.TLSRootCerts != "" && a.TLSRootCerts != b.TLSRootCerts && a.TLSRootCerts != rwRootCertsPath {
		return false
	}
	if a.ProxyHost != b.ProxyHost {
		return false
	}
	remaining := slices.Clone(b.Entries)
	for _, entry := range a.Entries {
		index := slices.Index(remaining, entry)
		if index < 0 {
			return false
		}
		remaining = slices.Delete(remaining, index, index+1)
	}
	return true
}

// describeCert is a one-line label for a CA certificate.
func describeCert(cert *x509.Certificate) string {
	name := cert.Subject.CommonName
	if name == "" {
		name = cert.Subject.String()
	}
	return fmt.Sprintf("%s (expires %s)", name, cert.NotAfter.UTC().Format("2006-01-02"))
}
