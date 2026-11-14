package metrics

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// MinTokenLength is the shortest bearer token ReadTokenFile accepts.
const MinTokenLength = 16

// ReadTokenFile loads the scrape bearer token (-metrics-token-file). The file
// must be a private regular file (no group or other permission bits, as for
// every other dismist secret) holding one token of at least MinTokenLength
// printable characters; surrounding whitespace, such as a trailing newline,
// is ignored.
func ReadTokenFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("metrics token file %s must be a private regular file (mode 0600 or 0400)", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < MinTokenLength {
		return "", fmt.Errorf("metrics token file %s must hold a token of at least %d characters", path, MinTokenLength)
	}
	if strings.ContainsFunc(token, isTokenSpace) {
		return "", errors.New("metrics token must be one line without spaces or control characters")
	}
	return token, nil
}
