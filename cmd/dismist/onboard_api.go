package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
)

// controllerClient talks to dismist's operator API with a bearer token
// (design §4.8). The CLI runs on a laptop on the AP's LAN; the controller
// may be anywhere (a Kubernetes cluster cannot reach home LAN addresses).
type controllerClient struct {
	base  *url.URL
	token string
	http  *http.Client
}

// controllerAPIError is a non-2xx response in the {error, code, fields}
// contract, or a transport failure (Status 0).
type controllerAPIError struct {
	Method  string
	Path    string
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *controllerAPIError) Error() string {
	where := e.Method + " " + e.Path
	if e.Status == 0 {
		return fmt.Sprintf("%s: %s", where, e.Message)
	}
	message := e.Message
	for field, problem := range e.Fields {
		message += fmt.Sprintf(" (%s: %s)", field, problem)
	}
	return fmt.Sprintf("%s: %d %s: %s", where, e.Status, e.Code, message)
}

func isAPIStatus(err error, status int, code string) bool {
	var apiErr *controllerAPIError
	return errors.As(err, &apiErr) && apiErr.Status == status && (code == "" || apiErr.Code == code)
}

// newControllerClient checks the controller URL: https, or http only to a
// loopback address (a local dismistd), so the token never crosses a
// network in clear text.
func newControllerClient(raw, token string) (*controllerClient, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("invalid --controller %q: use https://HOST[:PORT]", raw)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		host := parsed.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("--controller %q: use https; plain http is allowed only to a loopback address", raw)
		}
	default:
		return nil, fmt.Errorf("invalid --controller %q: use https://HOST[:PORT]", raw)
	}
	parsed.Path = ""
	return &controllerClient{base: parsed, token: token, http: &http.Client{Timeout: 20 * time.Second}}, nil
}

// String is the controller's base URL.
func (c *controllerClient) String() string { return c.base.String() }

// readTokenFile reads an API token (dsm_<id>_<secret>). The 64-hex admin
// token only signs in to the UI; webgate accepts API tokens as bearer
// tokens.
func readTokenFile(file string, warn io.Writer) (string, error) {
	if file == "" {
		return "", errors.New("--token-file is required: a file holding an API token with admin scope (create one in System › API tokens)")
	}
	info, err := os.Stat(expandHome(file))
	if err != nil {
		return "", fmt.Errorf("--token-file: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(warn, "warning: %s is readable by other users; chmod 600 it\n", file)
	}
	data, err := os.ReadFile(expandHome(file))
	if err != nil {
		return "", fmt.Errorf("--token-file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if strings.ContainsAny(token, " \t\r\n") || token == "" {
		return "", fmt.Errorf("--token-file %s must hold one API token on one line", file)
	}
	if raw, err := hex.DecodeString(token); err == nil && len(raw) == 32 {
		return "", fmt.Errorf("--token-file %s holds the admin sign-in token, which the API does not accept as a bearer token; create an API token with admin scope in System › API tokens and use that", file)
	}
	if !strings.HasPrefix(token, "dsm_") {
		return "", fmt.Errorf("--token-file %s does not hold a dismist API token (they start with dsm_)", file)
	}
	return token, nil
}

// do sends one request. body is JSON-encoded when not nil; out receives the
// decoded 2xx body when not nil. ifMatch, when not empty, is sent as
// If-Match: "<rev>".
func (c *controllerClient) do(ctx context.Context, method, path, ifMatch string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "dismist-cli")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		request.Header.Set("If-Match", strconv.Quote(ifMatch))
	}
	response, err := c.http.Do(request)
	if err != nil {
		return &controllerAPIError{Method: method, Path: path, Message: err.Error()}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return &controllerAPIError{Method: method, Path: path, Status: response.StatusCode, Message: "reading the response: " + err.Error()}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		apiErr := &controllerAPIError{Method: method, Path: path, Status: response.StatusCode}
		var body api.ErrorBody
		if json.Unmarshal(data, &body) == nil && (body.Error != "" || body.Code != "") {
			apiErr.Code, apiErr.Message, apiErr.Fields = string(body.Code), body.Error, body.Fields
		} else {
			apiErr.Message = http.StatusText(response.StatusCode)
		}
		if apiErr.Code == "" {
			apiErr.Code = "http_" + strconv.Itoa(response.StatusCode)
		}
		return apiErr
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &controllerAPIError{Method: method, Path: path, Status: response.StatusCode, Message: "unexpected response: " + err.Error()}
	}
	return nil
}

func (c *controllerClient) session(ctx context.Context) (api.AuthSession, error) {
	var session api.AuthSession
	err := c.do(ctx, http.MethodGet, "/auth/session", "", nil, &session)
	if isAPIStatus(err, http.StatusUnauthorized, "") {
		return session, fmt.Errorf("the controller refused the API token (revoked, expired or mistyped): %w", err)
	}
	return session, err
}

func (c *controllerClient) onboarding(ctx context.Context) (api.OnboardingView, error) {
	var view api.OnboardingView
	err := c.do(ctx, http.MethodGet, "/api/onboarding", "", nil, &view)
	return view, err
}

// sites accepts the v1 [SiteSummary] and the pre-v1 {"sites": [...]}.
func (c *controllerClient) sites(ctx context.Context) ([]api.SiteSummary, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/sites", "", nil, &raw); err != nil {
		return nil, err
	}
	var sites []api.SiteSummary
	if err := json.Unmarshal(raw, &sites); err == nil {
		return sites, nil
	}
	var wrapped struct {
		Sites []api.SiteSummary `json:"sites"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, &controllerAPIError{Method: http.MethodGet, Path: "/api/sites", Status: 200, Message: "unexpected response: " + err.Error()}
	}
	return wrapped.Sites, nil
}

// resolveSite finds the site named by --site: an exact ID, else a unique
// case-insensitive name.
func resolveSite(sites []api.SiteSummary, wanted string) (api.SiteSummary, error) {
	wanted = strings.TrimSpace(wanted)
	for _, site := range sites {
		if site.ID == wanted {
			return site, nil
		}
	}
	var matches []api.SiteSummary
	for _, site := range sites {
		if strings.EqualFold(strings.TrimSpace(site.Name), wanted) {
			matches = append(matches, site)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		var names []string
		for _, site := range sites {
			names = append(names, fmt.Sprintf("%q (%s)", site.Name, site.ID))
		}
		if len(names) == 0 {
			return api.SiteSummary{}, fmt.Errorf("--site %q: the controller has no sites yet; create one first", wanted)
		}
		return api.SiteSummary{}, fmt.Errorf("--site %q matches no site; sites: %s", wanted, strings.Join(names, ", "))
	default:
		return api.SiteSummary{}, fmt.Errorf("--site %q matches %d sites by name; pass the site ID instead", wanted, len(matches))
	}
}

// getAP returns the AP's detail, or nil when the controller has no record.
func (c *controllerClient) getAP(ctx context.Context, id string) (*api.APDetail, error) {
	var detail api.APDetail
	err := c.do(ctx, http.MethodGet, "/api/aps/"+url.PathEscape(id), "", nil, &detail)
	if isAPIStatus(err, http.StatusNotFound, "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

// apCreate is the POST /api/aps body: api.APInput, except that local_files
// is always sent. api.APInput drops an empty list (omitempty), but onboard
// has looked, and "none found" ([]) must not read as "never checked" (null,
// design §4.9 APDetail.local_files).
type apCreate struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	SiteID       string              `json:"site_id"`
	Secret       string              `json:"secret"`
	ManagementIP string              `json:"management_ip,omitempty"`
	LocalFiles   []api.LocalFileView `json:"local_files"`
}

func (c *controllerClient) createAP(ctx context.Context, input apCreate) error {
	if input.LocalFiles == nil {
		input.LocalFiles = []api.LocalFileView{}
	}
	return c.do(ctx, http.MethodPost, "/api/aps", "", input, nil)
}

// apPatch is the PATCH /api/aps/$id body the CLI sends (design §4.8 merge
// patch: absent fields are unchanged, the write-only secret is set by value).
type apPatch struct {
	Name         *string              `json:"name,omitempty"`
	SiteID       *string              `json:"site_id,omitempty"`
	ManagementIP *string              `json:"management_ip,omitempty"`
	Secret       string               `json:"secret,omitempty"`
	LocalFiles   *[]api.LocalFileView `json:"local_files,omitempty"`
}

func (c *controllerClient) patchAP(ctx context.Context, id string, rev uint64, patch apPatch) error {
	return c.do(ctx, http.MethodPatch, "/api/aps/"+url.PathEscape(id), strconv.FormatUint(rev, 10), patch, nil)
}

// registration is what onboard upserts for one AP.
type registration struct {
	ID     string
	Name   string
	SiteID string
	// Secret is the 32-byte EEPROM secret; it is sent as hex and never
	// printed.
	Secret []byte
	// PinnedIP is the source pin to set, "" to leave the pin alone.
	PinnedIP   string
	LocalFiles []api.LocalFileView
}

// upsertAP registers the AP (design §3.15 step 8): GET; on 404 POST; on 200
// PATCH with If-Match from that GET. A 409 stale on the PATCH, or a 409 on
// the POST (someone registered it meanwhile), repeats the GET once.
func upsertAP(ctx context.Context, client *controllerClient, reg registration, out io.Writer) (string, error) {
	secret := hex.EncodeToString(reg.Secret)
	localFiles := reg.LocalFiles
	if localFiles == nil {
		localFiles = []api.LocalFileView{}
	}
	for attempt := 0; ; attempt++ {
		current, err := client.getAP(ctx, reg.ID)
		if err != nil {
			return "", err
		}
		if current == nil {
			err = client.createAP(ctx, apCreate{ID: reg.ID, Name: reg.Name, SiteID: reg.SiteID, Secret: secret, ManagementIP: reg.PinnedIP, LocalFiles: localFiles})
			if err != nil && attempt == 0 && isAPIStatus(err, http.StatusConflict, "") {
				fmt.Fprintf(out, "Registration: %s was registered meanwhile; reading it again\n", reg.ID)
				continue
			}
			if err != nil {
				return "", err
			}
			return "registered", nil
		}
		patch := apPatch{Secret: secret, LocalFiles: &localFiles}
		var changes []string
		if reg.Name != "" && current.Name != reg.Name {
			patch.Name = &reg.Name
			changes = append(changes, fmt.Sprintf("name %q → %q", current.Name, reg.Name))
		}
		if reg.SiteID != "" && current.SiteID != reg.SiteID {
			patch.SiteID = &reg.SiteID
			changes = append(changes, fmt.Sprintf("site %s → %s", orNone(current.SiteID), reg.SiteID))
		}
		if reg.PinnedIP != "" && current.PinnedIP != reg.PinnedIP {
			patch.ManagementIP = &reg.PinnedIP
			changes = append(changes, fmt.Sprintf("pinned source IP %s → %s", orNone(current.PinnedIP), reg.PinnedIP))
		}
		err = client.patchAP(ctx, reg.ID, current.UpdatedRev, patch)
		if err != nil && attempt == 0 && isAPIStatus(err, http.StatusConflict, "stale") {
			fmt.Fprintf(out, "Registration: %s changed on the controller meanwhile (409 stale); reading it again\n", reg.ID)
			continue
		}
		if err != nil {
			return "", err
		}
		action := "refreshed (secret, local files)"
		if len(changes) > 0 {
			action = "updated: " + strings.Join(changes, ", ") + "; secret and local files refreshed"
		}
		return action, nil
	}
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}
