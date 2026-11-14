package main

import (
	"bytes"
	"crypto/tls"
	"time"
)

// exitCertificateChanged is the exit status after the AP WSS certificate on
// disk changes. It is non-zero so systemd's Restart=on-failure and Kubernetes
// both start the daemon again with the new certificate.
const exitCertificateChanged = 75

// certificateChanged reports whether the files now hold a loadable pair whose
// leaf differs from the one being served. A missing, partial or invalid pair
// (for example mid-rotation) reports false so the current one keeps serving.
func certificateChanged(certPath, keyPath string, served []byte) bool {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	return !bytes.Equal(pair.Certificate[0], served)
}

// watchCertificate closes changed once the certificate files hold a new pair.
// AP authentication is bound to the certificate the AP saw during the TLS
// handshake, so the daemon restarts to serve a renewed certificate (from
// cert-manager or ACME, for example) rather than swapping it in place.
func watchCertificate(certPath, keyPath string, served []byte, interval time.Duration, changed chan<- struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if certificateChanged(certPath, keyPath, served) {
			close(changed)
			return
		}
	}
}
