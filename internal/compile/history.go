package compile

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/rfplan"
)

// Config history snapshots and restore (design §4.11).

// DigestPrefix starts a keyed secret digest in a snapshot.
const DigestPrefix = "hmac:"

// SecretDigest is the keyed digest of a secret at a path:
// hmac:<first 16 hex of HMAC-SHA256(key, "secret\x00" + path + "\x00" + value)>.
func SecretDigest(key []byte, path, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("secret\x00" + path + "\x00" + value))
	return DigestPrefix + hex.EncodeToString(mac.Sum(nil))[:16]
}

// IsDigest reports whether a string is a secret digest placeholder.
func IsDigest(value string) bool {
	return strings.HasPrefix(value, DigestPrefix) && len(value) == len(DigestPrefix)+16
}

// secretKeys are the expert-patch member names whose string values are
// secrets (the redactValue rules).
var secretKeys = map[string]bool{
	"psk": true, "secret": true, "keyencryptkey": true, "passphrase": true, "clientkey": true,
	"privatekeypassword": true, "udpsecret": true, "kek": true, "mack": true, "password": true,
}

// IsSecretKey reports whether a JSON member name holds a secret.
func IsSecretKey(name string) bool { return secretKeys[strings.ToLower(name)] }

// redactPatch replaces secret strings in a patch with digests (or with
// "[redacted]" when key is nil) and returns the result.
func redactPatch(raw json.RawMessage, key []byte, base string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	tree, err := decodeTree(raw)
	if err != nil {
		return json.RawMessage(`"[unreadable]"`)
	}
	var walk func(value any, pointer string, secret bool) any
	walk = func(value any, pointer string, secret bool) any {
		switch v := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(v))
			for name, member := range v {
				out[name] = walk(member, pointer+"/"+escapePointer(name), secret || IsSecretKey(name))
			}
			return out
		case []any:
			out := make([]any, len(v))
			for index, member := range v {
				out[index] = walk(member, pointer+"/"+strconv.Itoa(index), secret)
			}
			return out
		case string:
			if secret && v != "" && !IsDigest(v) {
				if key == nil {
					return "[redacted]"
				}
				return SecretDigest(key, base+pointer, v)
			}
		}
		return value
	}
	encoded, err := encodeTree(walk(tree, "", false))
	if err != nil {
		return json.RawMessage(`"[unreadable]"`)
	}
	return encoded
}

// RedactPatch replaces the secrets in an expert patch with "[redacted]"
// (for reports and logs).
func RedactPatch(raw json.RawMessage) json.RawMessage { return redactPatch(raw, nil, "") }

func digestOrEmpty(key []byte, path, value string) string {
	if value == "" {
		return ""
	}
	return SecretDigest(key, path, value)
}

// redactNetwork replaces a network's secrets with digests.
func redactNetwork(network model.Network, key []byte) model.Network {
	base := "networks/" + network.ID
	network.PSK = digestOrEmpty(key, base+"/psk", network.PSK)
	network.Advanced = redactPatch(network.Advanced, key, base+"/advanced")
	network.Expert = redactPatch(network.Expert, key, base+"/expert")
	if network.RADIUS != nil {
		radius := cloneValue(*network.RADIUS)
		for index := range radius.Auth {
			radius.Auth[index].Secret = digestOrEmpty(key, fmt.Sprintf("%s/radius/auth/%d/secret", base, index), radius.Auth[index].Secret)
		}
		for index := range radius.Acct {
			radius.Acct[index].Secret = digestOrEmpty(key, fmt.Sprintf("%s/radius/acct/%d/secret", base, index), radius.Acct[index].Secret)
		}
		if radius.CoA != nil {
			for index := range radius.CoA.Servers {
				radius.CoA.Servers[index].Secret = digestOrEmpty(key, fmt.Sprintf("%s/radius/coa/%d/secret", base, index), radius.CoA.Servers[index].Secret)
			}
		}
		network.RADIUS = &radius
	}
	if network.MPSK != nil {
		mpsk := cloneValue(*network.MPSK)
		for index := range mpsk.Keys {
			mpsk.Keys[index].Passphrase = digestOrEmpty(key, base+"/mpsk/keys/"+mpsk.Keys[index].ID+"/passphrase", mpsk.Keys[index].Passphrase)
		}
		network.MPSK = &mpsk
	}
	return network
}

func cloneValue[T any](value T) T {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out T
	if json.Unmarshal(raw, &out) != nil {
		return value
	}
	return out
}

// SnapshotSite returns the history snapshot of one site (design §4.11):
// the site record, its networks and each AP's config-bearing fields, with
// every secret replaced by a keyed digest. AP secrets, protocol features,
// pins and local files are not config and are left out.
func SnapshotSite(state model.State, siteID string, key []byte) model.ConfigSnapshot {
	snapshot := model.ConfigSnapshot{Sites: []model.Site{}, Networks: []model.Network{}, APs: []model.APRecord{}}
	for _, site := range state.Sites {
		if site.ID != siteID {
			continue
		}
		site = cloneValue(site)
		site.Advanced = redactPatch(site.Advanced, key, "sites/"+site.ID+"/advanced")
		site.Expert = redactPatch(site.Expert, key, "sites/"+site.ID+"/expert")
		site.Roaming.KeyEncryptKey = digestOrEmpty(key, "sites/"+site.ID+"/roaming/key_encrypt_key", site.Roaming.KeyEncryptKey)
		snapshot.Sites = append(snapshot.Sites, site)
	}
	for _, network := range state.Networks {
		if network.SiteID == siteID {
			snapshot.Networks = append(snapshot.Networks, redactNetwork(cloneValue(network), key))
		}
	}
	for _, ap := range state.APs {
		if ap.SiteID != siteID {
			continue
		}
		snapshot.APs = append(snapshot.APs, model.APRecord{
			ID: ap.ID, Name: ap.Name, SiteID: ap.SiteID,
			Advanced:      redactPatch(ap.Advanced, key, "aps/"+ap.ID+"/advanced"),
			Expert:        redactPatch(ap.Expert, key, "aps/"+ap.ID+"/expert"),
			LEDBrightness: ap.LEDBrightness, SSHEnabled: ap.SSHEnabled, RF: cloneValue(ap.RF), RFPlan: cloneValue(ap.RFPlan),
			Management: cloneValue(ap.Management), Eth1: cloneValue(ap.Eth1), WiredClientStats: ap.WiredClientStats,
		})
	}
	return snapshot
}

// ChangeOptions describes a committed config mutation for its history
// record.
type ChangeOptions struct {
	Revision uint64
	At       time.Time
	Actor    model.Actor
	Endpoint string
	Summary  string
	SiteID   string
	Objects  []model.ObjectRef
	// Applied is compile.Apply's result: its changed APs get a pending
	// result and an effective hash.
	Applied *Applied
}

// NewChangeRecord builds the history record of a committed mutation: the
// redacted snapshot of the site after the change, the effective hash of
// every AP that was pushed (pending until ep-config-applied).
func NewChangeRecord(state model.State, key []byte, opts ChangeOptions) model.ChangeRecord {
	record := model.ChangeRecord{
		Revision: opts.Revision, At: opts.At.UTC(), Kind: model.ChangeConfig, Actor: opts.Actor,
		Endpoint: opts.Endpoint, Summary: opts.Summary,
		Scope: model.ChangeScope{SiteID: opts.SiteID, Objects: opts.Objects},
	}
	if opts.SiteID != "" {
		snapshot := SnapshotSite(state, opts.SiteID, key)
		record.Snapshot = &snapshot
	}
	if opts.Applied != nil {
		for _, push := range opts.Applied.Pushes {
			if record.Effective == nil {
				record.Effective = map[string]model.EffectiveRef{}
				record.Results = map[string]model.ApplyResult{}
			}
			ref := model.EffectiveRef{Revision: push.Revision}
			for _, ap := range state.APs {
				if ap.ID == push.APID {
					if hash, _, _, err := hashConfig(key, push.Config, ap); err == nil {
						ref.Hash = hash
					}
				}
			}
			record.Effective[push.APID] = ref
			record.Results[push.APID] = model.ApplyResult{State: "pending"}
		}
	}
	return record
}

// SecretField names a secret restore needs (422 secret_required).
type SecretField struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// SecretRequiredError is returned by Restore when a secret of revision R
// differs from today's (or belongs to a deleted object) and was neither
// entered nor kept.
type SecretRequiredError struct{ Fields []SecretField }

func (e *SecretRequiredError) Error() string {
	var parts []string
	for _, field := range e.Fields {
		parts = append(parts, field.Message)
	}
	return strings.Join(parts, "; ")
}

// Code is the API error code.
func (e *SecretRequiredError) Code() string { return "secret_required" }

// RestoreOptions are the operator's choices for a restore.
type RestoreOptions struct {
	// KeepNewWLANs keeps WLANs created after revision R unchanged.
	KeepNewWLANs bool
	// Secrets are old values entered by the operator, by digest path
	// ("networks/<id>/psk"); each is checked against R's digest.
	Secrets map[string]string
	// KeepCurrent keeps today's value of a secret, by digest path.
	KeepCurrent map[string]bool
	Now         time.Time
}

// RestoreResult reports what a restore did.
type RestoreResult struct {
	Warnings    []Warning `json:"warnings,omitempty"`
	Recreated   []string  `json:"recreated,omitempty"`    // network IDs
	Deleted     []string  `json:"deleted,omitempty"`      // network IDs
	NotRestored []string  `json:"not_restored,omitempty"` // AP IDs deleted since R
}

type secretResolver struct {
	key     []byte
	opts    RestoreOptions
	missing []SecretField
}

// resolve returns the value to store for a secret whose revision-R form is
// digest (or "" for none) and whose current value is current (exists
// reports whether the object still exists).
func (s *secretResolver) resolve(path, digest, current string, exists bool, label string) string {
	if digest == "" {
		return ""
	}
	if !IsDigest(digest) {
		return digest // stored in clear (a legacy snapshot): use it
	}
	if exists && current != "" && SecretDigest(s.key, path, current) == digest {
		return current
	}
	if entered, ok := s.opts.Secrets[path]; ok {
		if SecretDigest(s.key, path, entered) == digest {
			return entered
		}
		s.missing = append(s.missing, SecretField{Path: path, Message: label + " does not match the value at this revision"})
		return current
	}
	if exists && s.opts.KeepCurrent[path] {
		return current
	}
	if exists {
		s.missing = append(s.missing, SecretField{Path: path, Message: label + " differs from today's"})
	} else {
		s.missing = append(s.missing, SecretField{Path: path, Message: label + " must be entered to re-create the WLAN"})
	}
	return current
}

// restorePatch replaces digest placeholders in a restored expert patch
// with today's value at the same pointer (when it matches) or an entered
// value.
func (s *secretResolver) restorePatch(raw json.RawMessage, current json.RawMessage, base, label string) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	tree, err := decodeTree(raw)
	if err != nil {
		return raw
	}
	currentTree, _ := decodeTree(current)
	var walk func(value, now any, pointer string) any
	walk = func(value, now any, pointer string) any {
		switch v := value.(type) {
		case map[string]any:
			nowObject, _ := now.(map[string]any)
			out := make(map[string]any, len(v))
			for name, member := range v {
				out[name] = walk(member, nowObject[name], pointer+"/"+escapePointer(name))
			}
			return out
		case []any:
			nowArray, _ := now.([]any)
			out := make([]any, len(v))
			for index, member := range v {
				var nowMember any
				if index < len(nowArray) {
					nowMember = nowArray[index]
				}
				out[index] = walk(member, nowMember, pointer+"/"+strconv.Itoa(index))
			}
			return out
		case string:
			if IsDigest(v) {
				currentValue, _ := now.(string)
				return s.resolve(base+pointer, v, currentValue, currentValue != "", label+" "+pointer)
			}
		}
		return value
	}
	encoded, err := encodeTree(walk(tree, currentTree, ""))
	if err != nil {
		return raw
	}
	return encoded
}

// Restore makes the site's config look like revision R through the normal
// mutation path (design §4.11 table). It changes state only; the caller
// runs change detection, appends the history record and pushes. It returns
// *SecretRequiredError when secrets need the operator.
func Restore(state *model.State, record model.ChangeRecord, key []byte, opts RestoreOptions) (RestoreResult, error) {
	var result RestoreResult
	if record.Snapshot == nil || len(record.Snapshot.Sites) == 0 {
		return result, fmt.Errorf("revision %d has no site snapshot to restore", record.Revision)
	}
	resolver := &secretResolver{key: key, opts: opts}
	old := record.Snapshot.Sites[0]
	siteIndex := -1
	for index := range state.Sites {
		if state.Sites[index].ID == old.ID {
			siteIndex = index
		}
	}
	if siteIndex < 0 {
		return result, fmt.Errorf("site %s no longer exists", old.ID)
	}
	site := &state.Sites[siteIndex]
	site.Country, site.SSH, site.Syslog, site.Management = old.Country, old.SSH, old.Syslog, old.Management
	site.APDefaults, site.Protection, site.RF, site.Experimental = old.APDefaults, old.Protection, old.RF, old.Experimental
	site.Expert = resolver.restorePatch(old.Expert, site.Expert, "sites/"+site.ID+"/expert", "Site expert patch secret")

	// Networks.
	oldNetworks := map[string]model.Network{}
	for _, network := range record.Snapshot.Networks {
		oldNetworks[network.ID] = network
	}
	var kept []model.Network
	present := map[string]bool{}
	for _, network := range state.Networks {
		if network.SiteID != site.ID {
			kept = append(kept, network)
			continue
		}
		previous, existed := oldNetworks[network.ID]
		if !existed {
			if opts.KeepNewWLANs {
				kept = append(kept, network)
				present[network.ID] = true
			} else {
				result.Deleted = append(result.Deleted, network.ID)
			}
			continue
		}
		present[network.ID] = true
		kept = append(kept, restoreNetwork(network, previous, true, resolver))
	}
	used := map[int]bool{}
	for _, network := range kept {
		if network.SiteID == site.ID {
			used[network.BSSIDIndex] = true
			if network.OWEIndex > 0 {
				used[network.OWEIndex] = true
			}
		}
	}
	var recreated []model.Network
	for _, previous := range record.Snapshot.Networks {
		if present[previous.ID] {
			continue
		}
		network := restoreNetwork(model.Network{ID: previous.ID, SiteID: site.ID}, previous, false, resolver)
		if network.BSSIDIndex < 1 || used[network.BSSIDIndex] {
			free := FreeBSSIDIndex(used)
			result.Warnings = append(result.Warnings, Warning{Code: "bssid_changed", Option: "wlan.bssid_index",
				Message: fmt.Sprintf("BSSIDs for %s will change (index %d is taken; using %d)", network.SSID, network.BSSIDIndex, free)})
			network.BSSIDIndex = free
		}
		used[network.BSSIDIndex] = true
		recreated = append(recreated, network)
		result.Recreated = append(result.Recreated, network.ID)
	}
	state.Networks = append(kept, recreated...)

	// APs: typed overrides, expert patch and rf_plan; never identity or
	// operations fields.
	oldAPs := map[string]model.APRecord{}
	for _, ap := range record.Snapshot.APs {
		oldAPs[ap.ID] = ap
	}
	current := map[string]bool{}
	for index := range state.APs {
		ap := &state.APs[index]
		current[ap.ID] = true
		previous, existed := oldAPs[ap.ID]
		if ap.SiteID != site.ID || !existed {
			continue
		}
		ap.LEDBrightness, ap.SSHEnabled, ap.RF, ap.RFPlan = previous.LEDBrightness, previous.SSHEnabled, previous.RF, previous.RFPlan
		ap.Management, ap.Eth1, ap.WiredClientStats = previous.Management, previous.Eth1, previous.WiredClientStats
		ap.Expert = resolver.restorePatch(previous.Expert, ap.Expert, "aps/"+ap.ID+"/expert", "AP expert patch secret")
		for _, band := range rfplan.Bands {
			if static := rfplan.Static(*ap, band); static != 0 && site.RF.Managed {
				width := rfplan.Width(*site, *ap, band)
				if domain, ok := rfplan.Domain(*site); ok {
					if _, valid := domain.Valid(band, width, static); !valid {
						ap.RF[band].Channel = 0
						result.Warnings = append(result.Warnings, Warning{Code: "rf_plan_replaced", Option: apBandOption(band, "channel"),
							Message: fmt.Sprintf("%s: static channel %d is no longer valid; using an initial assignment", ap.ID, static)})
					}
				}
			}
			if plan, ok := ap.RFPlan[band]; ok && rfplan.Auto(*site, *ap, band) && !rfplan.ValidPlan(*site, *ap, band, plan) {
				delete(ap.RFPlan, band)
				result.Warnings = append(result.Warnings, Warning{Code: "rf_plan_replaced", Option: apBandOption(band, "channel"),
					Message: fmt.Sprintf("%s: the %s GHz plan of this revision no longer fits the RF profile; using an initial assignment", ap.ID, band)})
			}
		}
	}
	for _, previous := range record.Snapshot.APs {
		if !current[previous.ID] {
			result.NotRestored = append(result.NotRestored, previous.ID)
		}
	}
	sort.Strings(result.NotRestored)
	rfplan.ApplyInitial(state, opts.Now, site.ID)
	if len(resolver.missing) > 0 {
		return result, &SecretRequiredError{Fields: resolver.missing}
	}
	return result, nil
}

func restoreNetwork(current, previous model.Network, exists bool, resolver *secretResolver) model.Network {
	restored := cloneValue(previous)
	restored.SiteID = current.SiteID
	restored.Advanced = current.Advanced // legacy field: never restored
	restored.UpdatedRev = current.UpdatedRev
	base := "networks/" + previous.ID
	label := "Passphrase for " + previous.SSID
	if model.PersonalSecurity(previous.Security) {
		restored.PSK = resolver.resolve(base+"/psk", previous.PSK, current.PSK, exists, label)
	} else {
		restored.PSK = ""
	}
	if previous.RADIUS != nil {
		for index := range restored.RADIUS.Auth {
			var now string
			if current.RADIUS != nil && index < len(current.RADIUS.Auth) {
				now = current.RADIUS.Auth[index].Secret
			}
			restored.RADIUS.Auth[index].Secret = resolver.resolve(fmt.Sprintf("%s/radius/auth/%d/secret", base, index),
				previous.RADIUS.Auth[index].Secret, now, exists, fmt.Sprintf("RADIUS secret %d for %s", index+1, previous.SSID))
		}
		for index := range restored.RADIUS.Acct {
			var now string
			if current.RADIUS != nil && index < len(current.RADIUS.Acct) {
				now = current.RADIUS.Acct[index].Secret
			}
			restored.RADIUS.Acct[index].Secret = resolver.resolve(fmt.Sprintf("%s/radius/acct/%d/secret", base, index),
				previous.RADIUS.Acct[index].Secret, now, exists, fmt.Sprintf("Accounting secret %d for %s", index+1, previous.SSID))
		}
		if restored.RADIUS.CoA != nil {
			for index := range restored.RADIUS.CoA.Servers {
				var now string
				if current.RADIUS != nil && current.RADIUS.CoA != nil && index < len(current.RADIUS.CoA.Servers) {
					now = current.RADIUS.CoA.Servers[index].Secret
				}
				restored.RADIUS.CoA.Servers[index].Secret = resolver.resolve(fmt.Sprintf("%s/radius/coa/%d/secret", base, index),
					previous.RADIUS.CoA.Servers[index].Secret, now, exists, fmt.Sprintf("CoA secret %d for %s", index+1, previous.SSID))
			}
		}
	}
	if previous.MPSK != nil {
		currentKeys := map[string]string{}
		if current.MPSK != nil {
			for _, key := range current.MPSK.Keys {
				currentKeys[key.ID] = key.Passphrase
			}
		}
		for index, key := range restored.MPSK.Keys {
			now, ok := currentKeys[key.ID]
			restored.MPSK.Keys[index].Passphrase = resolver.resolve(base+"/mpsk/keys/"+key.ID+"/passphrase",
				previous.MPSK.Keys[index].Passphrase, now, exists && ok, "Passphrase of key "+key.Name)
		}
	}
	restored.Expert = resolver.restorePatch(previous.Expert, current.Expert, base+"/expert", "WLAN expert patch secret for "+previous.SSID)
	return restored
}

// FreeBSSIDIndex returns the lowest free BSSID index from 1 (design
// §4.4), or 0 when all 15 are taken.
func FreeBSSIDIndex(used map[int]bool) int {
	for index := 1; index <= 15; index++ {
		if !used[index] {
			return index
		}
	}
	return 0
}

// AssignBSSIDIndex picks the BSSID index of a new network of a site: the
// lowest free index from 1; an OWE transition network also reserves the
// next free index for its companion. Indexes are never renumbered.
func AssignBSSIDIndex(state model.State, siteID string, oweTransition bool) (index, owe int, err error) {
	used := map[int]bool{}
	for _, network := range state.Networks {
		if network.SiteID != siteID {
			continue
		}
		used[network.BSSIDIndex] = true
		if network.OWEIndex > 0 {
			used[network.OWEIndex] = true
		}
	}
	index = FreeBSSIDIndex(used)
	if index == 0 {
		return 0, 0, fmt.Errorf("every BSSID index (1 to 15) of the site is in use")
	}
	if oweTransition {
		used[index] = true
		owe = FreeBSSIDIndex(used)
		if owe == 0 {
			return 0, 0, fmt.Errorf("an OWE transition WLAN needs two free BSSID indexes")
		}
	}
	return index, owe, nil
}
