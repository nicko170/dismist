package httpapi

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// parseAPSecret reads an AP's 32-byte EEPROM secret given as hex (64
// digits, optionally separated by spaces or colons) or base64 (standard or
// URL alphabet, padded or not).
func parseAPSecret(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("AP secret required: the AP's 32-byte EEPROM secret as hex or base64")
	}
	compact := strings.NewReplacer(" ", "", ":", "", "\n", "", "\r", "", "\t", "").Replace(value)
	if decoded, err := hex.DecodeString(compact); err == nil && len(decoded) == model.SecretSize {
		return decoded, nil
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(compact); err == nil && len(decoded) == model.SecretSize {
			return decoded, nil
		}
	}
	return nil, errors.New("AP secret must be exactly 32 bytes, as hex or base64")
}

// localFileName matches the ep-control local_* files (design §3.15 step 3).
var localFileName = regexp.MustCompile(`^local_[A-Za-z0-9_.-]{1,64}$`)

// parseLocalFiles checks the local_files list of a registration or edit.
// nil stays nil (never checked); an empty list means checked, none found.
func parseLocalFiles(files []api.LocalFileView, now time.Time) ([]model.LocalFile, *apiError) {
	if files == nil {
		return nil, nil
	}
	if len(files) > 16 {
		return nil, invalidField("local_files", "at most 16 local files")
	}
	result := make([]model.LocalFile, 0, len(files))
	for index, file := range files {
		path := fmt.Sprintf("local_files[%d]", index)
		if !localFileName.MatchString(file.Name) {
			return nil, invalidField(path+".name", "a local file name starts with local_ and has no path")
		}
		digest := strings.ToLower(file.SHA256)
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != 32 {
			return nil, invalidField(path+".sha256", "sha256 must be 64 hex digits")
		}
		seen := file.SeenAt.UTC()
		if file.SeenAt.IsZero() {
			seen = now
		}
		result = append(result, model.LocalFile{Name: file.Name, SHA256: digest, SeenAt: seen})
	}
	return result, nil
}

// remarshal converts a decoded JSON value into a typed one.
func remarshal(value any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// asInt reads a whole JSON number.
func asInt(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i), true
		}
	case int:
		return typed, true
	case float64:
		if typed == float64(int(typed)) {
			return int(typed), true
		}
	}
	return 0, false
}

// onboardingView serves GET /api/onboarding (design §3.15): what an AP
// needs to reach this controller.
func (s *Server) onboardingView(writer http.ResponseWriter, _ *http.Request, _ params) {
	view := api.OnboardingView{WSSURL: s.system.wssURL(), DNSMode: s.system.DNSMode(), CAPems: []api.CAPem{}}
	for _, ca := range s.caCertificates() {
		name := ca.Subject.CommonName
		if name == "" {
			name = ca.Subject.String()
		}
		view.CAPems = append(view.CAPems, api.CAPem{Name: name, PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}))})
	}
	if ini, err := RenderCloudINI(view.WSSURL); err == nil {
		view.CloudINI = ini
	}
	respond(writer, http.StatusOK, view)
}

// caBundle serves GET /api/onboarding/ca-bundle.pem: the CAs an AP needs to
// trust the endpoint (the CLI adds them to the AP's stock Mist bundle).
func (s *Server) caBundle(writer http.ResponseWriter, request *http.Request, _ params) {
	var bundle strings.Builder
	for _, ca := range s.caCertificates() {
		name := ca.Subject.CommonName
		if name == "" {
			name = ca.Subject.String()
		}
		fmt.Fprintf(&bundle, "# %s (expires %s)\n", strings.ReplaceAll(name, "\n", " "), ca.NotAfter.UTC().Format("2006-01-02"))
		bundle.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}))
	}
	writer.Header().Set("Content-Type", "application/x-pem-file")
	writer.Header().Set("Content-Disposition", `attachment; filename="dismist-ca-bundle.pem"`)
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write([]byte(bundle.String()))
	}
}
