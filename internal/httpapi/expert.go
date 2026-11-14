package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/firmware"
)

// Forbidden expert-patch paths (design §4.4), matched case-insensitively
// on folded keys. "*" ends a prefix match.
var (
	forbiddenSiteAP = []string{
		"WLANs", "ID", "UUID", "SiteID", "OrgID", "Timestamp", "NotModified", "FailedRetryAfter",
		"Stats.Interval", "Stats.FullInterval", "Stats.MemInterval",
		"Ipsec", "Calea", "SiteString", "PAPIOpaque", "DeIdent", "AVS", "BootConfig", "Firmware", "MapID", "XYZ",
		"Default", "LicenseExpired", "RegulatoryOrg", "RadioSwap", "IndoorUse", "DirAntenna", "ManagementTunnels",
		"Monitor.PcapWLAN",
	}
	forbiddenWLAN = []string{
		"ID", "SSID", "Enabled", "Auth.PSK", "Auth.MultiPSK.PSKs", "KeyCache.KeyEncryptKey", "BandConfigs[].BSSIDIndex",
		"WLCmds", "Wxlan.Portal*", "ISE", "AirWatch", "DPI", "COA.CloudEnabled", "BlackList.Cloud", "Auth.WEP",
		"Auth.SecondaryType", "Auth.AKM.WPA_PSK", "Auth.AKM.WPA_EAP", "Auth.Pairwise.TKIP", "Auth.Pairwise.WPA1",
		"Mesh", "ForMesh",
	}
	// falseAllowed are forbidden flags a patch may still set to false.
	falseAllowed = []string{"Auth.AKM.WPA_PSK", "Auth.AKM.WPA_EAP", "Auth.Pairwise.TKIP"}
)

func forbiddenMatch(path string, list []string) (string, bool) {
	lower := strings.ToLower(path)
	for _, rule := range list {
		ruleLower := strings.ToLower(rule)
		if strings.HasSuffix(ruleLower, "*") {
			if strings.HasPrefix(lower, strings.TrimSuffix(ruleLower, "*")) {
				return rule, true
			}
			continue
		}
		if lower == ruleLower || strings.HasPrefix(lower, ruleLower+".") || strings.HasPrefix(lower, ruleLower+"[]") {
			return rule, true
		}
	}
	return "", false
}

// defaultExpertPolicy refuses duplicate keys (exact or case variants) and
// forbidden paths, case-insensitively. In legacy mode it stores the patch
// as written: the frozen v0 compiler does not fold the template's keys, so
// folding a stored patch could change what an AP runs. Otherwise keys are
// folded to their canonical 0.15 names (unknown keys are kept and listed
// as ignored).
type defaultExpertPolicy struct{}

func (defaultExpertPolicy) Check(scope api.ExpertScope, patch []byte, legacy bool) ([]byte, []string, error) {
	if err := duplicateKeys(patch); err != nil {
		return nil, nil, err
	}
	value, err := decodeAny(patch)
	if err != nil {
		return nil, nil, &ExpertError{Code: api.ErrorCodeInvalid, Pointer: "", Message: "the patch is not valid JSON"}
	}
	list := forbiddenSiteAP
	if scope == api.ExpertScopeWLAN {
		list = forbiddenWLAN
	}
	if err := walkForbidden(value, "", "", list); err != nil {
		return nil, nil, err
	}
	if legacy {
		return patch, nil, nil
	}
	schema := firmware.AP33()
	root := schema.Root()
	if scope == api.ExpertScopeWLAN {
		if node, ok := root.Field("WLANs"); ok && node.Elem != nil {
			root = node.Elem
		}
	}
	var ignored []string
	folded := foldValue(value, root, "", &ignored)
	raw, err := json.Marshal(folded)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(ignored)
	return raw, ignored, nil
}

func walkForbidden(value any, path, pointer string, list []string) error {
	object, ok := value.(map[string]any)
	if !ok {
		if items, isList := value.([]any); isList {
			for index, item := range items {
				if err := walkForbidden(item, path+"[]", fmt.Sprintf("%s/%d", pointer, index), list); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for key, child := range object {
		childPath := key
		if path != "" {
			childPath = path + "." + key
		}
		childPointer := pointer + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		if rule, hit := forbiddenMatch(childPath, list); hit {
			if !(isZeroJSON(child) && containsFold(falseAllowed, rule)) {
				return &ExpertError{Code: api.ErrorCodeForbiddenPath, Pointer: childPointer,
					Message: fmt.Sprintf("%s cannot be set by an expert patch (%s)", childPath, forbiddenReason(rule))}
			}
		}
		if err := walkForbidden(child, childPath, childPointer, list); err != nil {
			return err
		}
	}
	return nil
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func forbiddenReason(rule string) string {
	switch strings.ToLower(rule) {
	case "wlans":
		return "use the WLAN's own expert patch"
	case "id", "ssid", "enabled", "auth.psk", "auth.multipsk.psks", "keycache.keyencryptkey", "bandconfigs[].bssidindex":
		return "dismist sets it from the typed fields"
	case "uuid", "siteid", "orgid", "timestamp", "notmodified", "failedretryafter", "stats.interval", "stats.fullinterval", "stats.meminterval":
		return "dismist owns the config envelope"
	}
	return "not supported"
}

// duplicateKeys refuses an object with two keys that fold to one name.
func duplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	type frame struct {
		object  bool
		keys    map[string]string
		pointer string
		index   int
		expect  bool // next string token is a key
	}
	var stack []*frame
	var pendingKey string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return &ExpertError{Code: api.ErrorCodeInvalid, Message: "the patch is not valid JSON"}
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		childPointer := func() string {
			if top == nil {
				return ""
			}
			if top.object {
				return top.pointer + "/" + pendingKey
			}
			pointer := fmt.Sprintf("%s/%d", top.pointer, top.index)
			top.index++
			return pointer
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{', '[':
				pointer := childPointer()
				if top != nil && top.object {
					top.expect = true
				}
				stack = append(stack, &frame{object: value == '{', keys: map[string]string{}, pointer: pointer, expect: value == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].expect = true
				}
			}
		case string:
			if top != nil && top.object && top.expect {
				folded := firmware.FoldKey(value)
				if previous, seen := top.keys[folded]; seen {
					return &ExpertError{Code: api.ErrorCodeDuplicateKey, Pointer: top.pointer + "/" + value,
						Message: fmt.Sprintf("%q and %q are the same key to the AP; keep one", previous, value)}
				}
				top.keys[folded] = value
				pendingKey = value
				top.expect = false
				continue
			}
			childPointer()
			if top != nil && top.object {
				top.expect = true
			}
		default:
			childPointer()
			if top != nil && top.object {
				top.expect = true
			}
		}
	}
}

// foldValue folds every key to its canonical 0.15 spelling.
func foldValue(value any, node *firmware.Node, path string, ignored *[]string) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			var childNode *firmware.Node
			name := key
			if node != nil {
				if node.ValueKind() == firmware.KindMap && node.Elem != nil {
					childNode = node.Elem
				} else if field, ok := node.Field(key); ok {
					childNode, name = field, field.Name
				} else {
					*ignored = append(*ignored, childPath)
				}
			}
			result[name] = foldValue(child, childNode, childPath, ignored)
		}
		return result
	case []any:
		var elem *firmware.Node
		if node != nil {
			elem = node.Elem
		}
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = foldValue(child, elem, path+"[]", ignored)
		}
		return result
	}
	return value
}

func (s *Server) expertPolicy() ExpertPolicy {
	if s.config.Expert != nil {
		return s.config.Expert
	}
	return defaultExpertPolicy{}
}

// expertField is the record key an expert endpoint edits: "expert" once
// schema 2 has committed, the legacy "advanced" otherwise (design §4.9).
func (s *Server) expertField() string {
	if s.typedEnabled() {
		return "expert"
	}
	return "advanced"
}

// expertSummary reports an object's patch and the typed options it
// shadows (compile.Output.Shadowed of the site's APs).
func (s *Server) expertSummary(state model.State, scope api.ExpertScope, objectID string, record any) api.ExpertSummary {
	summary := api.ExpertSummary{Field: s.expertField(), Shadowed: []string{}, PartlyShadowed: []string{}, OverriddenOn: map[string][]string{}}
	summary.HasPatch = len(recordKey(record, summary.Field)) > 2
	siteID := objectID
	switch typed := record.(type) {
	case model.Network:
		siteID = typed.SiteID
	case model.APRecord:
		siteID = typed.SiteID
	}
	shadowed, partly := map[string]bool{}, map[string]bool{}
	for _, ap := range siteAPs(state, siteID) {
		input, err := compile.InputFor(s.base, state, ap.ID)
		if err != nil {
			continue
		}
		output, err := compile.AP(input)
		if err != nil {
			continue
		}
		for _, shadow := range output.Shadowed {
			switch {
			case string(scope) == shadow.Scope && shadow.By == shadow.Scope && (shadow.ObjectID == "" || shadow.ObjectID == objectID):
				if scope == api.ExpertScopeAP && ap.ID != objectID {
					continue
				}
				if shadow.Partly {
					partly[shadow.Option] = true
				} else {
					shadowed[shadow.Option] = true
				}
			case scope == api.ExpertScopeSite && shadow.Scope == "site" && shadow.By == "ap":
				if !containsString(summary.OverriddenOn[shadow.Option], ap.ID) {
					summary.OverriddenOn[shadow.Option] = append(summary.OverriddenOn[shadow.Option], ap.ID)
				}
			}
		}
	}
	for option := range shadowed {
		summary.Shadowed = append(summary.Shadowed, option)
	}
	for option := range partly {
		summary.PartlyShadowed = append(summary.PartlyShadowed, option)
	}
	sort.Strings(summary.Shadowed)
	sort.Strings(summary.PartlyShadowed)
	return summary
}

// expertTarget finds the record an expert endpoint addresses.
type expertTarget struct {
	scope    api.ExpertScope
	objectID string
	siteID   string
	name     string
}

func (s *Server) expertTargetFor(state model.State, p params, scope api.ExpertScope) (expertTarget, any, *apiError) {
	switch scope {
	case api.ExpertScopeSite:
		site := findSite(state, p["site"])
		if site == nil {
			return expertTarget{}, nil, notFound("site")
		}
		return expertTarget{scope: scope, objectID: site.ID, siteID: site.ID, name: "site " + site.Name}, *site, nil
	case api.ExpertScopeWLAN:
		network := findNetwork(state, p["site"], p["nid"])
		if network == nil {
			return expertTarget{}, nil, notFound("network")
		}
		return expertTarget{scope: scope, objectID: network.ID, siteID: network.SiteID, name: "WLAN " + network.SSID}, *network, nil
	default:
		ap := findAP(state, p["ap"])
		if ap == nil || (p["site"] != "" && ap.SiteID != p["site"]) {
			return expertTarget{}, nil, notFound("AP")
		}
		return expertTarget{scope: scope, objectID: ap.ID, siteID: ap.SiteID, name: "AP " + firstNonEmpty(ap.Name, ap.ID)}, *ap, nil
	}
}

func (s *Server) expertView(state model.State, target expertTarget, record any) api.ExpertPatchView {
	field := s.expertField()
	summary := s.expertSummary(state, target.scope, target.objectID, record)
	view := api.ExpertPatchView{Scope: target.scope, ObjectID: target.objectID, Field: field,
		Shadowed: summary.Shadowed, PartlyShadowed: summary.PartlyShadowed, IgnoredKeys: []string{}}
	view.Patch, _ = redactPatch(recordKey(record, field)).(map[string]any)
	if view.Patch == nil {
		view.Patch = map[string]any{}
	}
	switch typed := record.(type) {
	case model.Site:
		view.UpdatedRev = typed.UpdatedRev
	case model.Network:
		view.UpdatedRev = typed.UpdatedRev
	case model.APRecord:
		view.UpdatedRev = typed.UpdatedRev
	}
	if raw := recordKey(record, field); len(raw) > 0 && field == "expert" {
		if _, ignored, err := (defaultExpertPolicy{}).Check(target.scope, raw, false); err == nil && ignored != nil {
			view.IgnoredKeys = ignored
		}
	}
	return view
}

func expertHandlers(scope api.ExpertScope) (func(*Server) handlerFunc, func(*Server) handlerFunc) {
	get := func(s *Server) handlerFunc {
		return func(writer http.ResponseWriter, request *http.Request, p params) {
			state := s.store.Snapshot()
			target, record, bad := s.expertTargetFor(state, p, scope)
			if bad != nil {
				writeAPIError(writer, bad)
				return
			}
			respond(writer, http.StatusOK, s.expertView(state, target, record))
		}
	}
	put := func(s *Server) handlerFunc {
		return func(writer http.ResponseWriter, request *http.Request, p params) {
			s.putExpert(writer, request, p, scope)
		}
	}
	return get, put
}

// putExpert serves PUT …/expert (alias …/advanced): stores the scope's
// expert patch, with the "[redacted]" round trip restoring stored secrets.
func (s *Server) putExpert(writer http.ResponseWriter, request *http.Request, p params, scope api.ExpertScope) {
	var input struct {
		Patch   json.RawMessage `json:"patch"`
		Confirm bool            `json:"confirm"`
	}
	if err := decodeJSON(request, 1<<20, &input, "expert patch"); err != nil {
		writeError(writer, err)
		return
	}
	if len(input.Patch) == 0 {
		writeAPIError(writer, invalidField("patch", "patch is required"))
		return
	}
	var patch json.RawMessage
	if string(input.Patch) != "null" {
		if input.Patch[0] != '{' || !json.Valid(input.Patch) {
			writeAPIError(writer, invalidField("patch", "patch must be a JSON object"))
			return
		}
		patch = append(json.RawMessage(nil), input.Patch...)
	}
	state := s.store.Snapshot()
	target, record, bad := s.expertTargetFor(state, p, scope)
	if bad != nil {
		writeAPIError(writer, bad)
		return
	}
	field := s.expertField()
	var updatedRev uint64
	switch typed := record.(type) {
	case model.Site:
		updatedRev = typed.UpdatedRev
	case model.Network:
		updatedRev = typed.UpdatedRev
	case model.APRecord:
		updatedRev = typed.UpdatedRev
	}
	if bad := ifMatch(request, updatedRev, false); bad != nil {
		staleWith(writer, bad, s.expertView(state, target, record))
		return
	}
	var stored json.RawMessage
	var ignored []string
	if patch != nil {
		restored, err := restorePatch(recordKey(record, field), patch)
		if err != nil {
			writeError(writer, err)
			return
		}
		stored, ignored, err = s.expertPolicy().Check(scope, restored, field == "advanced")
		var expertErr *ExpertError
		if errors.As(err, &expertErr) {
			e := newError(http.StatusUnprocessableEntity, expertErr.Code, expertErr.Message)
			writeAPIError(writer, e.withField("patch"+expertErr.Pointer, expertErr.Message))
			return
		}
		if err != nil {
			writeError(writer, err)
			return
		}
	}
	m := mutation{
		sites: []string{target.siteID}, dryRun: dryRun(request), request: request,
		kind: string(scope) + ".updated", objectID: target.objectID, historySite: target.siteID,
		summary: "Expert patch of " + target.name + " changed",
		objects: []api.ChangeObject{{Kind: map[api.ExpertScope]string{api.ExpertScopeSite: "site", api.ExpertScopeWLAN: "network", api.ExpertScopeAP: "ap"}[scope], ID: target.objectID}},
		change: func(state *model.State, _ *model.Tx) error {
			var current any
			switch scope {
			case api.ExpertScopeSite:
				site := findSite(*state, target.objectID)
				if site == nil {
					return notFound("site")
				}
				current = site
			case api.ExpertScopeWLAN:
				network := findNetwork(*state, target.siteID, target.objectID)
				if network == nil {
					return notFound("network")
				}
				current = network
			default:
				ap := findAP(*state, target.objectID)
				if ap == nil {
					return notFound("AP")
				}
				current = ap
			}
			if err := setRecordKey(current, field, stored); err != nil {
				var unsupported *errUnsupported
				if errors.As(err, &unsupported) {
					return unsupportedField(field)
				}
				return err
			}
			return nil
		},
	}
	// Saving a patch that shadows typed fields needs a confirmation that
	// lists them (design §4.4): a dry run first, then the real save.
	if field == "expert" && !input.Confirm && !m.dryRun {
		preview := m
		preview.dryRun = true
		out, err := s.run(preview)
		if err != nil {
			writeChangeError(writer, err)
			return
		}
		if updated, ok := findRecord(out.state, scope, target); ok {
			summary := s.expertSummary(out.state, scope, target.objectID, updated)
			if len(summary.Shadowed)+len(summary.PartlyShadowed) > 0 {
				e := newError(http.StatusUnprocessableEntity, api.ErrorCodeConflict,
					"this patch overrides typed settings: "+strings.Join(append(summary.Shadowed, summary.PartlyShadowed...), ", ")+"; send confirm: true to save it")
				for _, option := range append(summary.Shadowed, summary.PartlyShadowed...) {
					e.withField(option, "overridden by the expert patch")
				}
				writeAPIError(writer, e)
				return
			}
		}
	}
	out, err := s.run(m)
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	if !out.dryRun {
		log.Printf("audit: expert patch of %s changed by %s", target.name, principal(request))
	}
	if legacyClient(request) {
		respond(writer, http.StatusOK, map[string]any{"revision": out.revision})
		return
	}
	updated, ok := findRecord(out.state, scope, target)
	if !ok {
		writeAPIError(writer, notFound(string(scope)))
		return
	}
	view := s.expertView(out.state, target, updated)
	if ignored != nil {
		view.IgnoredKeys = ignored
		for _, key := range ignored {
			out.warnings = append(out.warnings, api.Warning{Code: "ignored_key", Message: key + " is not a 0.15 config key; the AP ignores it"})
		}
	}
	if bytes.Contains(bytes.ToLower(stored), []byte(`"proxy"`)) && bytes.Contains(bytes.ToLower(stored), []byte(`"cloud"`)) {
		out.warnings = append(out.warnings, api.Warning{Code: "cloud_proxy", Message: "Cloud.Proxy.URL can redirect the AP's controller connection"})
	}
	respondMutation(writer, http.StatusOK, out, view)
}

func findRecord(state model.State, scope api.ExpertScope, target expertTarget) (any, bool) {
	switch scope {
	case api.ExpertScopeSite:
		if site := findSite(state, target.objectID); site != nil {
			return *site, true
		}
	case api.ExpertScopeWLAN:
		if network := findNetwork(state, target.siteID, target.objectID); network != nil {
			return *network, true
		}
	default:
		if ap := findAP(state, target.objectID); ap != nil {
			return *ap, true
		}
	}
	return nil, false
}

// restorePatch puts stored secrets back where the incoming patch says
// "[redacted]" (the round trip of a redacted view).
func restorePatch(existing json.RawMessage, patch json.RawMessage) (json.RawMessage, error) {
	var original any
	if len(existing) != 0 {
		decoded, err := decodeAny(existing)
		if err != nil {
			return nil, err
		}
		original = decoded
	}
	incoming, err := decodeAny(patch)
	if err != nil {
		return nil, invalidField("patch", "patch must be a JSON object")
	}
	restored, err := restoreRedacted(original, incoming)
	if err != nil {
		return nil, err
	}
	return json.Marshal(restored)
}

func restoreRedacted(original, incoming any) (any, error) {
	if value, ok := incoming.(string); ok && value == "[redacted]" {
		if original == nil {
			return nil, invalidField("patch", "a [redacted] value has no stored secret to keep")
		}
		return original, nil
	}
	switch value := incoming.(type) {
	case map[string]any:
		previous, _ := original.(map[string]any)
		result := make(map[string]any, len(value))
		for key, child := range value {
			restored, err := restoreRedacted(previous[key], child)
			if err != nil {
				return nil, err
			}
			result[key] = restored
		}
		return result, nil
	case []any:
		previous, _ := original.([]any)
		result := make([]any, len(value))
		for index, child := range value {
			var prior any
			if index < len(previous) {
				prior = previous[index]
			}
			restored, err := restoreRedacted(prior, child)
			if err != nil {
				return nil, err
			}
			result[index] = restored
		}
		return result, nil
	default:
		return incoming, nil
	}
}
