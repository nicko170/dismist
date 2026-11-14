package httpapi

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

func (s *Server) registerAP(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Model        string `json:"model"`
		ManagementIP string `json:"management_ip"`
		SiteID       string `json:"site_id"`
		Secret       string `json:"secret"`
		// LocalFiles are the AP's local_* files the CLI found (names and
		// SHA-256 only); absent means never checked.
		LocalFiles []api.LocalFileView `json:"local_files"`
	}
	if err := decodeJSON(request, 64*1024, &input, "AP registration"); err != nil {
		writeError(writer, err)
		return
	}
	mac, err := net.ParseMAC(strings.ReplaceAll(strings.TrimSpace(input.ID), "-", ":"))
	if err != nil || len(mac) != 6 {
		writeAPIError(writer, invalidField("id", "invalid AP ID: use its Ethernet MAC address"))
		return
	}
	secret, err := parseAPSecret(input.Secret)
	if err != nil {
		writeAPIError(writer, invalidField("secret", err.Error()))
		return
	}
	// The management IP is optional: it pins the AP to one source address.
	managementIP := ""
	if address := strings.TrimSpace(input.ManagementIP); address != "" {
		ip := net.ParseIP(address)
		if ip == nil {
			writeAPIError(writer, invalidField("management_ip", "invalid AP management IP"))
			return
		}
		managementIP = ip.String()
	}
	localFiles, badFiles := parseLocalFiles(input.LocalFiles, time.Now().UTC())
	if badFiles != nil {
		writeAPIError(writer, badFiles)
		return
	}
	id := strings.ReplaceAll(mac.String(), ":", "-")
	_, err = s.mutate([]string{input.SiteID}, func(state *model.State, _ *model.Tx) error {
		foundSite := input.SiteID == ""
		for _, candidate := range state.Sites {
			if candidate.ID == input.SiteID {
				foundSite = true
				break
			}
		}
		if !foundSite {
			return notFound("site").withField("site_id", "no such site")
		}
		for _, candidate := range state.APs {
			if candidate.ID == id {
				return conflict("AP already registered").withField("id", "an AP with this ID is already registered")
			}
		}
		// A new AP in a site compiles as changed (it has no baseline), so
		// change detection sets its revision; an AP without a site starts
		// at the new revision too.
		state.APs = append(state.APs, model.APRecord{ID: id, Name: strings.TrimSpace(input.Name), Model: strings.TrimSpace(input.Model),
			ManagementIP: managementIP, SiteID: input.SiteID, Secret: secret, LocalFiles: localFiles,
			ConfigRevision: state.Revision + 1, ConfigUpdatedAt: state.UpdatedAt})
		return nil
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	state := s.store.Snapshot()
	for _, ap := range state.APs {
		if ap.ID == id {
			respond(writer, http.StatusCreated, map[string]any{"ap": s.apView(ap), "revision": state.Revision})
			return
		}
	}
	writeAPIError(writer, internalError("the registered AP was not stored"))
}

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
