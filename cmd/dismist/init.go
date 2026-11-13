package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/nicko170/dismist/internal/aptemplate"
	"github.com/nicko170/dismist/internal/webgate"
)

const apHostname = "ep-terminator.mistsys.net"

var certificateFiles = []string{"ca.crt", "ca.key", "tls.crt", "tls.key"}

func initialize(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("data directory must be a private directory (mode 0700)")
	}
	existing, err := inspectCertificates(dir)
	if err != nil {
		return "", err
	}
	if existing {
		if err := verifyCertificates(dir); err != nil {
			return "", fmt.Errorf("existing certificates: %w", err)
		}
	}
	var generated map[string][]byte
	if !existing {
		generated, err = generateCertificates(time.Now())
		if err != nil {
			return "", err
		}
	}
	if err := ensureAPConfig(filepath.Join(dir, "ap-config.json")); err != nil {
		return "", fmt.Errorf("AP config: %w", err)
	}
	if !existing {
		if err := writeCertificates(dir, generated); err != nil {
			return "", err
		}
	}
	tokenPath := filepath.Join(dir, "admin.token")
	_, tokenStatErr := os.Lstat(tokenPath)
	if tokenStatErr != nil && !errors.Is(tokenStatErr, os.ErrNotExist) {
		return "", fmt.Errorf("administrator token: %w", tokenStatErr)
	}
	token, err := webgate.LoadOrCreateToken(tokenPath)
	if err != nil {
		return "", fmt.Errorf("administrator token: %w", err)
	}
	if errors.Is(tokenStatErr, os.ErrNotExist) {
		return token, nil
	}
	return "", nil
}

func ensureAPConfig(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("existing AP config must be a private regular file")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(aptemplate.Default()); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func inspectCertificates(dir string) (bool, error) {
	found := 0
	for _, name := range certificateFiles {
		info, err := os.Lstat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return false, fmt.Errorf("%s must be a private regular file", name)
		}
		found++
	}
	if found != 0 && found != len(certificateFiles) {
		return false, errors.New("partial certificate set exists; inspect it before retrying init")
	}
	return found == len(certificateFiles), nil
}

func generateCertificates(now time.Time) (map[string][]byte, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	caSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject:      pkix.Name{CommonName: "Dismist Local CA", Organization: []string{"Dismist"}},
		NotBefore:    now.Add(-10 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate WSS key: %w", err)
	}
	leafSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject:      pkix.Name{CommonName: apHostname},
		DNSNames:     []string{apHostname},
		NotBefore:    now.Add(-10 * time.Minute), NotAfter: now.AddDate(2, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("create WSS certificate: %w", err)
	}
	caPrivate, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return nil, err
	}
	leafPrivate, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"ca.crt":  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		"ca.key":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caPrivate}),
		"tls.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		"tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafPrivate}),
	}, nil
}

func randomSerial() (*big.Int, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	serial := new(big.Int).SetBytes(raw[:])
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, nil
}

func writeCertificates(dir string, generated map[string][]byte) error {
	var created []string
	defer func() {
		if created != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	for _, name := range certificateFiles {
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create %s without overwriting: %w", name, err)
		}
		created = append(created, path)
		_, err = file.Write(generated[name])
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	created = nil
	return nil
}

func verifyCertificates(dir string) error {
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return err
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil || caBlock.Type != "CERTIFICATE" {
		return errors.New("invalid CA PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return err
	}
	if !caCert.IsCA || caCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return errors.New("certificate authority cannot sign certificates")
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		return err
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil || caKeyBlock.Type != "PRIVATE KEY" {
		return errors.New("invalid CA key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return err
	}
	signer, ok := parsed.(crypto.Signer)
	if !ok {
		return errors.New("invalid CA signing key")
	}
	certPublic, err := x509.MarshalPKIXPublicKey(caCert.PublicKey)
	if err != nil {
		return err
	}
	keyPublic, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil || !bytes.Equal(certPublic, keyPublic) {
		return errors.New("CA certificate and key do not match")
	}
	leafPair, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		return err
	}
	leafCert, err := x509.ParseCertificate(leafPair.Certificate[0])
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := leafCert.Verify(x509.VerifyOptions{
		DNSName: apHostname, Roots: roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return err
	}
	return nil
}
