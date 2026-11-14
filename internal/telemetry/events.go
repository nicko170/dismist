package telemetry

import (
	"bytes"
	"encoding/json"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// jsonFields is a JSON object decoded one field at a time, so a field with
// an unexpected type is skipped (and counted) without losing the others.
type jsonFields struct {
	fields map[string]json.RawMessage
	reader reader
}

// get finds a key the way encoding/json matches struct fields (and so the
// AP's Go code): an exact match wins, then the first case-insensitive
// match in sorted key order.
func (j jsonFields) get(name string) (json.RawMessage, bool) {
	if raw, ok := j.fields[name]; ok {
		return raw, !isNull(raw)
	}
	keys := make([]string, 0, len(j.fields))
	for key := range j.fields {
		if strings.EqualFold(key, name) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, false
	}
	sort.Strings(keys)
	raw := j.fields[keys[0]]
	return raw, !isNull(raw)
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func (j jsonFields) decode(name string, target any) bool {
	raw, ok := j.get(name)
	if !ok {
		return false
	}
	if err := json.Unmarshal(raw, target); err != nil {
		j.reader.fail()
		return false
	}
	return true
}

func (j jsonFields) str(name string, limit int) string {
	var value string
	if !j.decode(name, &value) {
		return ""
	}
	return cleanString([]byte(value), limit)
}

func (j jsonFields) number(name string) float64 {
	var value float64
	j.decode(name, &value)
	return finite(value)
}

func (j jsonFields) integer(name string) int {
	value := j.number(name)
	return int(max(-1<<31, min(value, 1<<31-1)))
}

func (j jsonFields) boolean(name string) bool {
	var value bool
	j.decode(name, &value)
	return value
}

// decodeJSON decodes a JSON ep-telemetry message: PACE events,
// ep-config-applied- and ap-cmd-output-. A message whose topic is not one
// of those still decodes, with its Topic and nothing else.
func decodeJSON(body []byte) (Update, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return Update{}, ErrMalformed
	}
	update := Update{DecodeErrors: map[string]int{}}
	object := jsonFields{fields: fields, reader: newReader(update.DecodeErrors, SectionEvent)}
	topic, err := jsonTopic(object)
	if err != nil {
		return Update{}, err
	}
	update.Topic = topic
	update.APID = object.str("HashKey", 64)
	switch topic {
	case TopicConfigApplied:
		object.reader = object.reader.in(SectionApplied)
		if update.APID == "" {
			update.APID = object.str("ID", 64)
		}
		applied := decodeApplied(object)
		update.Applied = &applied
		update.ConfigTimestamp, update.Error, update.Firmware = applied.ConfigTimestamp, applied.Error, applied.Firmware
	case TopicPaceEvent, TopicClientEvent:
		event := decodeEvent(object)
		update.Event = &event
	case TopicCommandOutput:
		var payload struct {
			Session string `json:"Session"`
			Output  string `json:"Output"`
		}
		if object.decode("Payload", &payload) {
			update.CommandOutput = &CommandOutput{
				Session: cleanString([]byte(payload.Session), 64),
				Output:  boundedText(payload.Output, MaxCommandOutput),
			}
		}
	}
	if len(update.DecodeErrors) == 0 {
		update.DecodeErrors = nil
	}
	return update, nil
}

// jsonTopic returns the message topic: Topic, or from the plural Topics
// list (0.15 PACE events) the PACE topic first. A Topics value that is not
// a list of strings makes the message malformed.
func jsonTopic(object jsonFields) (string, error) {
	if topic := object.str("Topic", 64); topic != "" {
		return topic, nil
	}
	raw, ok := object.get("Topics")
	if !ok {
		return "", nil
	}
	var topics []string
	if err := json.Unmarshal(raw, &topics); err != nil {
		return "", ErrMalformed
	}
	for _, preferred := range []string{TopicPaceEvent, TopicClientEvent} {
		for _, topic := range topics {
			if topic == preferred {
				return topic, nil
			}
		}
	}
	for _, topic := range topics {
		if cleaned := cleanString([]byte(topic), 64); cleaned != "" {
			return cleaned, nil
		}
	}
	return "", nil
}

// decodeApplied reads msgs.ConfigApplied (design §5.5).
func decodeApplied(object jsonFields) Applied {
	applied := Applied{
		ConfigTimestamp:        object.number("ConfigTimestamp"),
		Error:                  object.str("Error", 500),
		Firmware:               object.str("Firmware", 96),
		Good5Min:               object.boolean("Good5Min"),
		RecoveredFromBadConfig: object.boolean("RecoveredFromBadConfig"),
		PowerConstrained:       object.boolean("PowerConstrained"),
		Count:                  max(0, object.integer("Count")),
		AuditID:                object.str("AuditID", 64),
	}
	return applied
}

// PACE event types with their own kind (design §5.8).
const (
	PaceDHCPFailure   = 1
	PaceDNS           = 3
	PaceDisassociated = 6
	PaceDNSServer     = 11
	PaceDNSLatency    = 12
	PaceRSNConfig     = 28
	PacePCAP          = 42
	PaceAuthorized    = 49
	PaceGTKRekey      = 107
	PacePortUp        = 108
	PacePortDown      = 109
)

// paceKinds maps a PACE type to its kind and the keys copied into Data,
// as {AP key, data key}.
var paceKinds = map[int]struct {
	kind string
	data [][2]string
}{
	PaceDHCPFailure: {KindDHCPFailure, [][2]string{
		{"VLAN", "vlan"}, {"NumAttempts", "attempts"}, {"RequestedIP", "requested_ip"},
		{"LeasedIP", "leased_ip"}, {"DHCP_server_ip", "server"}, {"NACKed", "nacked"},
		{"Declined", "declined"}, {"Status_text", "status"}, {"DhcpMsgType", "message_type"},
		{"SSID", "ssid"}, {"BSSID", "bssid"}, {"Band", "band"},
	}},
	PaceDNS: {KindDNS, [][2]string{
		{"ServerInformation", "server"}, {"HostName", "hostname"}, {"WiredClient", "wired"},
		{"SSID", "ssid"}, {"BSSID", "bssid"}, {"Band", "band"},
	}},
	PaceDisassociated: {KindDisassociated, [][2]string{
		{"Reason_code", "reason_code"}, {"RSSI", "rssi"}, {"Time_since_association", "time_since_association"},
		{"SSID", "ssid"}, {"BSSID", "bssid"}, {"Band", "band"},
	}},
	PaceDNSServer: {KindDNSServer, [][2]string{
		{"Server_ip", "server"}, {"Vlan", "vlan"}, {"ServerInformation", "server_information"},
	}},
	PaceDNSLatency: {KindDNSLatency, [][2]string{
		{"Server_ip", "server"}, {"Vlan", "vlan"}, {"Median_old", "median_old_s"}, {"Median_new", "median_new_s"},
		{"Mad_old", "mad_old_s"}, {"Mad_new", "mad_new_s"}, {"Sample_size", "sample_size"},
	}},
	PaceRSNConfig: {KindRSNConfig, [][2]string{
		{"PrevChan24GHz", "prev_channel_24"}, {"CurrChan24GHz", "channel_24"},
		{"PrevChan5GHz", "prev_channel_5"}, {"CurrChan5GHz", "channel_5"},
		{"PrevChan6GHz", "prev_channel_6"}, {"CurrChan6GHz", "channel_6"},
		{"ReasonCode", "reason_code"}, {"Reason_txt", "reason"},
		{"TornDownWLANs", "torn_down_wlans"}, {"CurrWLANs", "wlans"}, {"PrevWLANs", "prev_wlans"},
	}},
	PacePCAP:       {KindPCAP, nil},
	PaceAuthorized: {KindAuthorized, authorizedData},
	PaceGTKRekey: {KindGTKRekey, [][2]string{
		{"SSID", "ssid"}, {"BSSID", "bssid"}, {"Band", "band"},
	}},
	PacePortUp:   {KindPort, nil},
	PacePortDown: {KindPort, nil},
}

var authorizedData = [][2]string{
	{"SSID", "ssid"}, {"BSSID", "bssid"}, {"Band", "band"}, {"AKM", "akm"}, {"Pairwise", "pairwise"},
	{"Auth_Type", "auth_type"}, {"PSK_Type", "psk_type"}, {"Username", "username"},
	{"AssocRSSI", "assoc_rssi"}, {"NumSpatialStreams", "streams"},
	{"AuthenticationWhen", "authentication_at"}, {"AssociationWhen", "association_at"},
	{"AuthorizationStartWhen", "authorization_start_at"}, {"AuthorizationSuccessWhen", "authorization_success_at"},
	{"Time_since_authentication", "time_since_authentication"}, {"Time_since_association", "time_since_association"},
	{"Time_since_authorization_start", "time_since_authorization_start"},
	{"Reassociation", "reassociation"}, {"Reauthorization", "reauthorization"},
	{"Dot11k", "dot11k"}, {"Dot11r", "dot11r"}, {"Dot11v", "dot11v"},
	{"Auth_server_addr", "auth_server"}, {"Auth_server_port", "auth_server_port"},
}

// commonEventKeys are the msgs.EPEvent fields; the raw fallback leaves
// them out of Data (they are Event fields already).
var commonEventKeys = map[string]bool{
	"topic": true, "hashkey": true, "topics": true, "hashkeys": true, "epid": true,
	"configtimestamp": true, "model": true, "firmware": true, "version": true, "id": true,
	"isupdate": true, "wc": true, "type": true, "title": true, "text": true, "when": true,
	"duration": true, "impacted_client_ip": true, "impacted_client_mac": true, "corrects": true,
}

// dropEventKey reports keys never copied into Data: captured packets and
// per-client stat dumps (large), and anything that could carry a secret.
func dropEventKey(key string) bool {
	lower := strings.ToLower(key)
	for _, word := range []string{"pcap", "wcstats", "password", "passphrase", "secret", "keycache", "pmk", "ptk", "gtk_key"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return lower == "psk" || lower == "key"
}

// decodeEvent reads a PACE event (msgs.EPEvent plus its type's fields)
// and types it (design §5.8).
func decodeEvent(object jsonFields) Event {
	event := Event{
		Type:     object.integer("Type"),
		Title:    object.str("Title", 120),
		Text:     boundedText(object.str("Text", 2000), 500),
		ID:       strings.ToLower(object.str("ID", 64)),
		IsUpdate: object.boolean("IsUpdate"),
		Duration: max(0, object.integer("Duration")),
		WLANID:   strings.ToLower(object.str("WLAN", 64)),
	}
	if when := object.str("When", 64); when != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, when); err == nil && parsed.Year() > 2000 {
			event.When = parsed.UTC()
		}
	}
	if !validUUIDText(event.ID) {
		event.ID = ""
	}
	if !validUUIDText(event.WLANID) {
		event.WLANID = ""
	}
	event.ClientMAC = normaliseMAC(object.str("impacted_client_mac", 32))
	event.ClientIP = normaliseIP(object.str("impacted_client_ip", 64))
	var corrects []string
	if object.decode("Corrects", &corrects) {
		for _, id := range corrects {
			id = strings.ToLower(strings.TrimSpace(id))
			if validUUIDText(id) && len(event.Corrects) < 32 {
				event.Corrects = append(event.Corrects, id)
			}
		}
	}
	event.Kind, event.Severity = KindPace, SeverityInfo
	known, typed := paceKinds[event.Type]
	if typed {
		event.Kind = known.kind
	}
	switch event.Type {
	case PaceDHCPFailure:
		event.Severity = SeverityWarning
	case PaceDNS:
		var server struct{ Failing bool }
		if raw, ok := object.get("ServerInformation"); ok && json.Unmarshal(raw, &server) == nil && server.Failing {
			event.Severity = SeverityWarning
		} else if !ok && strings.Contains(strings.ToLower(event.Title), "fail") {
			event.Severity = SeverityWarning
		}
	case PacePortDown:
		event.Severity = SeverityWarning
	case PacePCAP:
		if strings.Contains(event.Title+" "+event.Text, "No packets") {
			event.Noise = true
		}
	}
	if typed && known.data != nil {
		event.Data = eventData(object, known.data)
	} else {
		event.Data = rawEventData(object)
	}
	return event
}

// eventData copies the listed keys into a JSON object of at most
// MaxEventData bytes.
func eventData(object jsonFields, keys [][2]string) json.RawMessage {
	data := make(map[string]json.RawMessage, len(keys))
	for _, key := range keys {
		if raw, ok := object.get(key[0]); ok && json.Valid(raw) {
			data[key[1]] = raw
		}
	}
	return boundData(data)
}

// rawEventData keeps the event's own fields (not the common EPEvent ones,
// nor packets, stat dumps or secrets) for types without a typed mapping.
func rawEventData(object jsonFields) json.RawMessage {
	data := make(map[string]json.RawMessage)
	for key, raw := range object.fields {
		if commonEventKeys[strings.ToLower(key)] || dropEventKey(key) || isNull(raw) || !json.Valid(raw) {
			continue
		}
		data[key] = raw
	}
	return boundData(data)
}

// boundData marshals data, dropping the largest values until it fits in
// MaxEventData (then "truncated" is set).
func boundData(data map[string]json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	truncated := false
	for {
		encoded, err := json.Marshal(data)
		if err == nil && len(encoded) <= MaxEventData {
			return encoded
		}
		largest, size := "", -1
		for key, raw := range data {
			if len(raw) > size && key != "truncated" {
				largest, size = key, len(raw)
			}
		}
		if largest == "" {
			return json.RawMessage(`{"truncated":true}`)
		}
		delete(data, largest)
		if !truncated {
			truncated = true
			data["truncated"] = json.RawMessage("true")
		}
	}
}

// boundedText cuts text to limit bytes on a rune boundary.
func boundedText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && text[cut]&0xC0 == 0x80 {
		cut--
	}
	return text[:cut]
}

// normaliseMAC returns a MAC as "02:aa:…", or "" for an all-zero or
// unparseable one.
func normaliseMAC(mac string) string {
	parsed, err := net.ParseMAC(strings.ToLower(strings.ReplaceAll(mac, "-", ":")))
	if err != nil || len(parsed) != 6 || bytes.Equal(parsed, make([]byte, 6)) {
		return ""
	}
	return parsed.String()
}

// normaliseIP returns an address in canonical form, or "" for an
// unspecified or unparseable one.
func normaliseIP(ip string) string {
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || address.IsUnspecified() {
		return ""
	}
	return address.Unmap().String()
}
