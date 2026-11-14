package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// knownHostKey is one known_hosts entry that matched a host.
type knownHostKey struct {
	key     ssh.PublicKey
	file    string
	line    int
	revoked bool
}

// knownHosts implements trust-on-first-use host key checking over one or
// more OpenSSH known_hosts files. New keys are appended to the first file.
type knownHosts struct {
	files []string
	// mode is "accept-new" (record unknown keys, refuse changed ones),
	// "yes" (refuse unknown and changed keys) or "no" (record unknown
	// keys, warn about changed ones and continue).
	mode string
	warn io.Writer
}

// HostKeyChangedError reports a host key that differs from the recorded
// one. A factory reset regenerates an AP's host key; so does a different
// device answering on a reused address.
type HostKeyChangedError struct {
	Host        string
	Fingerprint string
	Known       []knownHostKey
}

func (e *HostKeyChangedError) Error() string {
	where := make([]string, 0, len(e.Known))
	for _, known := range e.Known {
		where = append(where, fmt.Sprintf("%s:%d (%s %s)", known.file, known.line, known.key.Type(), ssh.FingerprintSHA256(known.key)))
	}
	return fmt.Sprintf("host key for %s has CHANGED to %s; recorded in %s. If the AP was factory reset or replaced, confirm the new key on the AP (ssh-keygen -lf of its dropbear host key) and remove the old line; otherwise do not connect",
		e.Host, e.Fingerprint, strings.Join(where, ", "))
}

// HostKeyUnknownError reports a host with no recorded key in "yes" mode.
type HostKeyUnknownError struct {
	Host        string
	Fingerprint string
	File        string
}

func (e *HostKeyUnknownError) Error() string {
	return fmt.Sprintf("host key for %s is not known (%s) and StrictHostKeyChecking=yes; verify the fingerprint, then connect once with -o StrictHostKeyChecking=accept-new to record it in %s",
		e.Host, e.Fingerprint, e.File)
}

// lookup returns every entry for address ("host:port"), reading all files.
// Unparseable lines are skipped, as ssh(1) does.
func (k *knownHosts) lookup(address string) ([]knownHostKey, error) {
	host := knownhosts.Normalize(address)
	var found []knownHostKey
	for _, file := range k.files {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for index, line := range bytes.Split(data, []byte("\n")) {
			marker, hosts, key, _, _, err := ssh.ParseKnownHosts(line)
			if err != nil || key == nil || marker == "cert-authority" {
				continue
			}
			if !knownHostMatches(hosts, host) {
				continue
			}
			found = append(found, knownHostKey{key: key, file: file, line: index + 1, revoked: marker == "revoked"})
		}
	}
	return found, nil
}

// knownHostMatches applies known_hosts host patterns: hashed |1|salt|hash
// entries, wildcards, and negation.
func knownHostMatches(patterns []string, host string) bool {
	matched := false
	for _, pattern := range patterns {
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		var ok bool
		if strings.HasPrefix(pattern, "|1|") {
			ok = hashedHostMatches(pattern, host)
		} else {
			ok = hostPatternsMatch([]string{pattern}, host)
		}
		if !ok {
			continue
		}
		if negated {
			return false
		}
		matched = true
	}
	return matched
}

func hashedHostMatches(entry, host string) bool {
	parts := strings.Split(entry, "|")
	if len(parts) != 4 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(host))
	return hmac.Equal(mac.Sum(nil), want)
}

// preferredHostKeyAlgorithms puts the algorithms of keys already recorded for
// address first, so the server offers the key type we know (as ssh(1) does)
// instead of a different type that would look like a changed key.
func (k *knownHosts) preferredHostKeyAlgorithms(address string, configured []string) []string {
	known, err := k.lookup(address)
	if err != nil || len(known) == 0 {
		return configured
	}
	var first []string
	for _, entry := range known {
		if entry.revoked {
			continue
		}
		for _, algorithm := range hostKeyAlgorithmsForType(entry.key.Type()) {
			for _, candidate := range configured {
				if candidate == algorithm {
					first = appendUnique(first, algorithm)
				}
			}
		}
	}
	return appendUnique(first, configured...)
}

func hostKeyAlgorithmsForType(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

// callback returns the HostKeyCallback for a connection to address.
func (k *knownHosts) callback(address string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		return k.check(address, key)
	}
}

func (k *knownHosts) check(address string, key ssh.PublicKey) error {
	host := knownhosts.Normalize(address)
	fingerprint := ssh.FingerprintSHA256(key)
	known, err := k.lookup(address)
	if err != nil {
		return fmt.Errorf("read known_hosts: %w", err)
	}
	var others []knownHostKey
	for _, entry := range known {
		if !bytes.Equal(entry.key.Marshal(), key.Marshal()) {
			others = append(others, entry)
			continue
		}
		if entry.revoked {
			return fmt.Errorf("host key %s for %s is marked @revoked in %s:%d", fingerprint, host, entry.file, entry.line)
		}
		return nil
	}
	if len(others) > 0 {
		changed := &HostKeyChangedError{Host: host, Fingerprint: fingerprint, Known: others}
		if k.mode == "no" {
			fmt.Fprintf(k.warn, "WARNING: %v\nContinuing because StrictHostKeyChecking=no; the new key was not recorded.\n", changed)
			return nil
		}
		return changed
	}
	if k.mode == "yes" {
		return &HostKeyUnknownError{Host: host, Fingerprint: fingerprint, File: k.files[0]}
	}
	if err := appendKnownHost(k.files[0], host, key); err != nil {
		return fmt.Errorf("record host key: %w", err)
	}
	fmt.Fprintf(k.warn, "Recorded new host key for %s (%s %s) in %s\n", host, key.Type(), fingerprint, k.files[0])
	return nil
}

func appendKnownHost(file, host string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	handle, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(handle, knownhosts.Line([]string{host}, key)); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}
