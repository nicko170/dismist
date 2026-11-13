package model

import (
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// NormalizeSSHPublicKeys accepts plain OpenSSH public keys and returns a
// canonical, deduplicated list. It never includes rejected input in errors.
func NormalizeSSHPublicKeys(keys []string) ([]string, error) {
	if len(keys) > 32 {
		return nil, errors.New("too many site SSH public keys")
	}
	result := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, raw := range keys {
		line := strings.TrimSpace(raw)
		if len(line) == 0 || len(line) > 8192 || strings.ContainsAny(line, "\r\n") {
			return nil, errors.New("invalid site SSH public key")
		}
		key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || len(options) != 0 || len(rest) != 0 {
			return nil, errors.New("invalid site SSH public key")
		}
		canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if !seen[canonical] {
			result = append(result, canonical)
			seen[canonical] = true
		}
	}
	return result, nil
}
