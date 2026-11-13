package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	legacyAuthPrefix = "sr7Krl7tkajVBowS"
	legacyAuthSuffix = "ZuSX01QGh8PJq0Na"
)

func runAP33(args []string, output io.Writer) error {
	if len(args) > 0 && args[0] == "prepare-trust" {
		return runPrepareTrust(args[1:], output)
	}
	if len(args) != 2 || args[0] != "serial-response" {
		return errors.New("usage: dismist ap33 serial-response <challenge>")
	}
	response, err := serialResponse(args[1], rand.Reader)
	if err != nil {
		return fmt.Errorf("cannot generate response: %w", err)
	}
	_, err = fmt.Fprintln(output, response)
	return err
}

// serialResponse answers the legacy AP33 0.8 A-mode root challenge.
func serialResponse(challenge string, randomness io.Reader) (string, error) {
	if !strings.HasPrefix(challenge, "B") {
		return "", errors.New("invalid challenge prefix (expected B)")
	}
	encoded := challenge[1:]
	for _, c := range encoded {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return "", errors.New("invalid challenge base64")
		}
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid challenge base64")
	}
	if len(raw) > 0 && raw[0] == 'C' {
		return "", errors.New("signed C-mode challenge is unsupported")
	}
	fields := strings.SplitN(string(raw), "|", 4)
	if len(fields) != 4 || fields[0] != "A" || fields[2] != "root" {
		return "", errors.New("invalid legacy A-mode root challenge")
	}
	if !validDashedMAC(fields[1]) {
		return "", errors.New("invalid challenge MAC (expected six dashed hex octets)")
	}
	serverNonce := []byte(fields[3])
	if len(serverNonce) != 32 {
		return "", errors.New("invalid challenge nonce (expected 32 bytes)")
	}
	clientNonce := make([]byte, 32)
	if _, err := io.ReadFull(randomness, clientNonce); err != nil {
		return "", fmt.Errorf("generate client nonce: %w", err)
	}
	first := hmac.New(sha256.New, serverNonce)
	_, _ = first.Write(clientNonce)
	auth := hmac.New(sha256.New, first.Sum(nil))
	_, _ = auth.Write([]byte(legacyAuthPrefix))
	_, _ = auth.Write(raw)
	_, _ = auth.Write([]byte(legacyAuthSuffix))
	response := append(clientNonce, auth.Sum(nil)...)
	return "B" + base64.StdEncoding.EncodeToString(response), nil
}

func validDashedMAC(mac string) bool {
	if len(mac) != 17 {
		return false
	}
	for i := range mac {
		if i%3 == 2 {
			if mac[i] != '-' {
				return false
			}
			continue
		}
		c := mac[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
