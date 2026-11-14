package compile

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/model"
)

// Step W (design §4.4): one WLAN per network from the template's WLANs[0],
// then the network's typed fields, then its expert patch, then the WLAN
// protected fields.

// AKMFlags are the 16 flags of Auth.AKM; the compiler always writes all of
// them (design §3.9, P5).
var AKMFlags = []string{
	"WPA_PSK", "WPA2_PSK", "WPA2_PSK_SHA256", "WPA2_PSK_FT", "WPA3_PSK", "WPA3_PSK_FT",
	"WPA3_PSK_SAE_EXT", "WPA3_PSK_FT_SAE_EXT", "WPA_EAP", "WPA2_EAP", "WPA2_EAP_SHA256",
	"WPA2_EAP_FT", "WPA3_EAP", "WPA3_EAP_FT", "WPA3_SUITEB", "WPA3_OWE",
}

// SecurityMapping is the device form of a security composite.
type SecurityMapping struct {
	AuthType   string
	AKM        map[string]bool
	CCMP       bool
	Dot11wOn   bool
	Dot11wReq  bool
	OWETransit bool
	Passphrase bool // the mode carries Auth.PSK
	Enterprise bool
}

// MapSecurity returns the exact device mapping of a security value with
// its PMF and roaming options (design §3.9 table). "" is the schema-1
// default (WPA2-Personal).
func MapSecurity(network model.Network) (SecurityMapping, error) {
	m := SecurityMapping{AKM: map[string]bool{}, CCMP: true}
	pmf := network.PMF
	if pmf == "" {
		pmf = "optional"
	}
	switch network.Security {
	case "", model.SecurityWPA2Personal:
		m.AuthType, m.Passphrase = "psk", true
		m.AKM["WPA2_PSK"] = true
		switch pmf {
		case "disabled":
		case "required":
			m.Dot11wOn, m.Dot11wReq = true, true
			m.AKM["WPA2_PSK_SHA256"] = true
		default:
			m.Dot11wOn = true
		}
	case model.SecurityWPA2WPA3Personal:
		m.AuthType, m.Passphrase = "psk", true
		m.AKM["WPA2_PSK"], m.AKM["WPA3_PSK"] = true, true
		m.Dot11wOn = true
	case model.SecurityWPA3Personal:
		m.AuthType, m.Passphrase = "psk", true
		m.AKM["WPA3_PSK"] = true
		m.Dot11wOn, m.Dot11wReq = true, true
	case model.SecurityOpen:
		m.AuthType, m.CCMP = "open", false
	case model.SecurityOWE:
		m.AuthType = "owe"
		m.AKM["WPA3_OWE"] = true
		m.Dot11wOn, m.Dot11wReq = true, true
	case model.SecurityOWETransition:
		m.AuthType, m.CCMP, m.OWETransit = "open", false, true
	case model.SecurityWPA2Enterprise:
		m.AuthType, m.Enterprise = "eap", true
		m.AKM["WPA2_EAP"] = true
		switch pmf {
		case "disabled":
		case "required":
			m.Dot11wOn, m.Dot11wReq = true, true
			m.AKM["WPA2_EAP_SHA256"] = true
		default:
			m.Dot11wOn = true
		}
	case model.SecurityWPA2WPA3Enterprise:
		m.AuthType, m.Enterprise = "eap", true
		m.AKM["WPA2_EAP"], m.AKM["WPA3_EAP"], m.AKM["WPA2_EAP_SHA256"] = true, true, true
		m.Dot11wOn = true
	case model.SecurityWPA3Enterprise:
		m.AuthType, m.Enterprise = "eap", true
		m.AKM["WPA3_EAP"], m.AKM["WPA2_EAP_SHA256"] = true, true
		m.Dot11wOn, m.Dot11wReq = true, true
	default:
		return m, fmt.Errorf("unknown security %q", network.Security)
	}
	if network.Roaming.Fast == "11r" {
		switch {
		case m.AKM["WPA2_PSK"] && m.AKM["WPA3_PSK"]:
			m.AKM["WPA2_PSK_FT"], m.AKM["WPA2_PSK_SHA256"], m.AKM["WPA3_PSK_FT"] = true, true, true
		case m.AKM["WPA3_PSK"]:
			m.AKM["WPA3_PSK_FT"] = true
		case m.AKM["WPA2_PSK"]:
			m.AKM["WPA2_PSK_FT"] = true
		}
		if m.AKM["WPA2_EAP"] {
			m.AKM["WPA2_EAP_FT"] = true
		}
		if m.AKM["WPA3_EAP"] {
			m.AKM["WPA3_EAP_FT"] = true
		}
	}
	return m, nil
}

// MPSKPMK derives the PMK the firmware wants for a Multi-PSK key (design
// §3.9): hex(PBKDF2-HMAC-SHA1(passphrase, SSID, 4096, 32)), 64 hex digits.
func MPSKPMK(passphrase, ssid string) (string, error) {
	key, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// MDID is the 802.11r mobility domain of a WLAN: the first two bytes of
// its UUID, non-zero, the same on every AP.
func MDID(networkID string) int {
	clean := strings.ReplaceAll(networkID, "-", "")
	if len(clean) >= 4 {
		if value, err := strconv.ParseUint(clean[:4], 16, 16); err == nil && value != 0 {
			return int(value)
		}
	}
	sum := 0
	for _, c := range networkID {
		sum = (sum*31 + int(c)) & 0xffff
	}
	if sum == 0 {
		sum = 1
	}
	return sum
}

// rateProfiles are the RateSets per profile and band [WLAN §7.4]; nil
// masks keep the template's.
type rateSet struct {
	basic, legacy []string
	zeroMasks     bool
}

var rateProfiles = map[string]map[string]rateSet{
	"compatible": {
		"2.4": {basic: []string{"1", "2", "5.5", "11"}, legacy: []string{"6", "9", "12", "18", "24", "36", "48", "54"}},
		"5":   {basic: []string{"6", "12", "24"}, legacy: []string{"9", "18", "36", "48", "54"}},
	},
	"no_legacy": {
		"2.4": {basic: []string{"6", "12", "24"}, legacy: []string{"9", "18", "36", "48", "54"}},
		"5":   {basic: []string{"6", "12", "24"}, legacy: []string{"9", "18", "36", "48", "54"}},
	},
	"high_density": {
		"2.4": {basic: []string{"12", "24"}, legacy: []string{"18", "36", "48", "54"}},
		"5":   {basic: []string{"12", "24"}, legacy: []string{"18", "36", "48", "54"}},
	},
	"legacy_only": {
		"2.4": {basic: []string{"1", "2", "5.5", "11"}, legacy: []string{"6", "9", "12", "18", "24", "36", "48", "54"}, zeroMasks: true},
		"5":   {basic: []string{"6", "12", "24"}, legacy: []string{"9", "18", "36", "48", "54"}, zeroMasks: true},
	},
}

func numbers(values []string) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = jsonNumber(value)
	}
	return out
}

// steerBlock is the template's 2.4 GHz band-steering block (design §3.9).
func steerBlock() map[string]any {
	return map[string]any{
		"NoProbeRespAll": true, "DenyDot11Auth": true, "DenyDot11AuthDualBandOnly": true,
		"DenyDot11AuthRSSIThresh": num(-78), "DenyDot11AuthConsecNum": num(5),
	}
}

// qosClasses map the QoS override to QOS.AccessClass (4 = no override).
var qosClasses = map[string]int{"": 4, "none": 4, "best_effort": 0, "background": 1, "video": 2, "voice": 3}

func radiusServers(servers []model.RADIUSServer, defaultPort int) []any {
	out := make([]any, 0, len(servers))
	for _, server := range servers {
		entry := map[string]any{"Secret": server.Secret}
		port := server.Port
		if port == 0 {
			port = defaultPort
		}
		entry["Port"] = num(port)
		if net.ParseIP(server.Host) != nil {
			entry["IP"] = server.Host
		} else {
			entry["Hostname"] = server.Host
		}
		out = append(out, entry)
	}
	return out
}

// macDashed renders a MAC as the firmware's ethernet.MAC text
// (aa-bb-cc-dd-ee-ff).
func macDashed(mac string) string {
	return strings.ToLower(strings.NewReplacer(":", "-", ".", "").Replace(strings.TrimSpace(mac)))
}

// wlanInput is what one WLAN compile reads.
type wlanInput struct {
	template map[string]any
	site     model.Site
	ap       model.APRecord
	network  model.Network
	rec      *recorder
	warnings *[]Warning
}

// compileWLAN builds one WLAN object (step W).
func compileWLAN(in wlanInput) (map[string]any, []Shadow, error) {
	n := in.network
	w := deepCopy(in.template).(map[string]any)
	rec := in.rec.scoped(ScopeWLAN, n.ID, "wlan")
	opt := func(option string, value any, keys ...string) {
		setAt(w, value, keys...)
		rec.note(option, strings.Join(keys, "."))
	}

	// Security composite (design §3.9).
	mapping, err := MapSecurity(n)
	if err != nil {
		return nil, nil, err
	}
	auth := child(w, "Auth")
	auth["Type"] = mapping.AuthType
	akm := map[string]any{}
	for _, flag := range AKMFlags {
		akm[flag] = mapping.AKM[flag]
	}
	auth["AKM"] = akm
	auth["Pairwise"] = map[string]any{"TKIP": false, "CCMP": mapping.CCMP, "GCMP256": false,
		"WPA1": map[string]any{"CCMP": false, "TKIP": false}}
	auth["WEP"] = nil
	auth["TransitionDisableIndication"] = n.TransitionDisable
	if n.Security == model.SecurityWPA2WPA3Personal || n.Security == model.SecurityWPA3Personal || n.SAEH2EOnly {
		auth["SAEHash2ElementOnly"] = n.SAEH2EOnly
	}
	eapol := 0
	if n.EAPOLv1 {
		eapol = 1
	}
	auth["EAPOLVersion"] = num(eapol)
	rekey := n.GroupRekeySec
	if rekey == 0 {
		rekey = 86400
	}
	auth["GroupRekeyIntervalSec"], auth["GroupMasterRekeyIntervalSec"] = num(rekey), num(rekey)
	for _, path := range []string{"Auth.Type", "Auth.AKM", "Auth.Pairwise", "Auth.TransitionDisableIndication"} {
		rec.note("wlan.security", path)
	}
	rec.note("wlan.eapol_v1", "Auth.EAPOLVersion")
	rec.note("wlan.group_rekey_sec", "Auth.GroupRekeyIntervalSec")
	rec.note("wlan.group_rekey_sec", "Auth.GroupMasterRekeyIntervalSec")
	if _, ok := auth["SAEHash2ElementOnly"]; ok {
		rec.note("wlan.sae_h2e_only", "Auth.SAEHash2ElementOnly")
	}
	opt("wlan.pmf", map[string]any{"Enabled": mapping.Dot11wOn, "Required": mapping.Dot11wReq,
		"SA_Query_Attempts": num(3), "SA_Query_Timeout": num(200)}, "Dot11w")
	owe := child(w, "OWE")
	owe["Transition"] = mapping.OWETransit
	rec.note("wlan.security", "OWE.Transition")
	if mapping.OWETransit {
		owe["SSID"] = n.OWESSID
		rec.note("wlan.owe_ssid", "OWE.SSID")
	}

	// Multi-PSK (phase 4, WPA2-Personal only).
	var psks []any
	if n.MPSK != nil && n.MPSK.Enabled {
		for _, key := range n.MPSK.Keys {
			pmk, err := MPSKPMK(key.Passphrase, n.SSID)
			if err != nil {
				return nil, nil, fmt.Errorf("network %s key %s: %w", n.ID, key.Name, err)
			}
			entry := map[string]any{"Name": key.ID, "KeyName": key.Name, "PSK": pmk, "Vlan_ID": num(key.VLANID)}
			if key.MAC != "" {
				entry["WC"] = macDashed(key.MAC)
			}
			if !key.Expires.IsZero() {
				entry["Expire_Time"] = num(int(key.Expires.Unix()))
			}
			psks = append(psks, entry)
		}
		if psks == nil {
			psks = []any{}
		}
		auth["MultiPSK"] = map[string]any{"Enabled": true, "PrivateWlan": n.MPSK.Private, "PSKs": psks}
		rec.note("wlan.mpsk.enabled", "Auth.MultiPSK.Enabled")
		rec.note("wlan.mpsk.private", "Auth.MultiPSK.PrivateWlan")
	} else {
		auth["MultiPSK"] = nil
	}

	// RADIUS (Enterprise, or MAC authentication).
	radius := n.RADIUS
	if radius == nil {
		radius = &model.RADIUSConfig{}
	}
	opt("wlan.radius.mac_auth", radius.MACAuth, "MacAuth", "Enabled")
	if mapping.Enterprise || radius.MACAuth {
		opt("wlan.radius.auth", radiusServers(radius.Auth, 1812), "Auth_servers")
		if len(radius.Acct) > 0 {
			opt("wlan.radius.acct", radiusServers(radius.Acct, 1813), "Acct_servers")
		}
		opt("wlan.radius.acct_interim_sec", num(radius.AcctInterimSec), "Acct_Interim_Interval")
		if radius.NASID != "" {
			opt("wlan.radius.nas_id", radius.NASID, "Nas_ID")
		}
		if radius.NASIP != "" {
			opt("wlan.radius.nas_ip", radius.NASIP, "NasIPAddress")
		}
	}
	if mapping.Enterprise {
		opt("wlan.radius.reauth_sec", num(radius.ReauthSec), "Auth", "EapReauthIntervalSec")
		opt("wlan.radius.reauth_on_session_timeout", radius.ReauthOnSessionTimeout, "Auth", "ReauthOnSessionTimeout")
		if dv := radius.DynamicVLAN; dv != nil && dv.Enabled {
			vlans := make([]any, 0, len(dv.VLANs))
			for _, vlan := range dv.VLANs {
				vlans = append(vlans, num(vlan))
			}
			kind := dv.Type
			if kind == "" {
				kind = "standard"
			}
			opt("wlan.radius.dynamic_vlan", map[string]any{"Enabled": true, "Type": kind, "Vlan_IDs": vlans}, "Dynamic_vlan")
		}
		if coa := radius.CoA; coa != nil && coa.Enabled {
			var servers []any
			for _, server := range coa.Servers {
				entry := map[string]any{"Secret": server.Secret}
				port := server.Port
				if port == 0 {
					port = 3799
				}
				entry["LocalPort"] = num(port)
				if net.ParseIP(server.Host) != nil {
					entry["Server"] = server.Host
				} else {
					entry["Hostname"] = server.Host
				}
				servers = append(servers, entry)
			}
			coaObject := child(w, "COA")
			coaObject["Enabled"] = true
			coaObject["COAServers"] = servers
			coaObject["EventTimeStampOptional"] = coa.EventTimestampOptional
			for _, path := range []string{"COA.Enabled", "COA.COAServers", "COA.EventTimeStampOptional"} {
				rec.note("wlan.radius.coa", path)
			}
		}
	}

	// Bands, rate sets and per-band configs.
	bands := n.Bands
	if len(bands) == 0 {
		bands = []string{"2.4", "5"}
	}
	bandList := make([]any, len(bands))
	for index, band := range bands {
		bandList[index] = band
	}
	opt("wlan.bands", bandList, "Bands")
	profile := n.Rates
	if profile == "" {
		profile = "compatible"
	}
	rates, ok := rateProfiles[profile]
	if !ok {
		return nil, nil, fmt.Errorf("network %s: unknown rate profile %q", n.ID, profile)
	}
	templateRates, _ := w["RateSets"].([]any)
	templateConfigs, _ := w["BandConfigs"].([]any)
	var rateSets, bandConfigs []any
	for _, band := range bands {
		rs := map[string]any{"Band": band}
		if existing, at := keyedElement(templateRates, "Band", band); at >= 0 {
			rs = deepCopy(existing).(map[string]any)
		}
		profileBand := rates[band]
		rs["BasicRates"], rs["LegacyRates"] = numbers(profileBand.basic), numbers(profileBand.legacy)
		if profileBand.zeroMasks {
			rs["HTMCSRateMask"], rs["VHTMCSRateMask"], rs["HEMCSRateMask"] = "0000000000000000", "0000000000000000", "0000000000000000"
		}
		rateSets = append(rateSets, rs)
		rec.note("wlan.rates", "RateSets[Band="+band+"].BasicRates")
		rec.note("wlan.rates", "RateSets[Band="+band+"].LegacyRates")

		bc := map[string]any{"Band": band}
		if existing, at := keyedElement(templateConfigs, "Band", band); at >= 0 {
			bc = deepCopy(existing).(map[string]any)
		}
		delete(bc, "Steer")
		if band == "2.4" && n.BandSteering {
			bc["Steer"] = steerBlock()
		}
		rec.note("wlan.band_steering", "BandConfigs[Band="+band+"].Steer")
		delete(bc, "GeoFence")
		if threshold, ok := n.MinRSSI[band]; ok && threshold != 0 {
			bc["GeoFence"] = map[string]any{"DenyDot11Auth": true, "DenyDot11AuthRSSIThresh": num(threshold)}
		}
		key := "wlan.min_rssi.band_24"
		if band == "5" {
			key = "wlan.min_rssi.band_5"
		}
		rec.note(key, "BandConfigs[Band="+band+"].GeoFence")
		bandConfigs = append(bandConfigs, bc)
	}
	w["RateSets"], w["BandConfigs"] = rateSets, bandConfigs

	// Basic and advanced fields.
	vlan := n.VLANID
	if vlan == 0 {
		vlan = 1
	}
	opt("wlan.vlan_id", []any{strconv.Itoa(vlan)}, "Vlans")
	opt("wlan.hidden", n.Hidden, "Hidden")
	isolation := n.Isolation
	opt("wlan.isolation", isolation == "clients" || isolation == "clients_and_lan", "Isolation")
	opt("wlan.isolation", isolation == "clients_and_lan", "NetIsolation")
	roam := n.Roaming.Fast
	if roam == "" {
		roam = "none"
	}
	opt("wlan.roaming.fast", roam, "RoamMode")
	if roam == "11r" {
		opt("wlan.roaming.fast", num(MDID(n.ID)), "MDID")
		opt("wlan.roaming.fast", false, "OverDS")
	}
	keyCache := child(w, "KeyCache")
	keyCache["WLCkeyCache"] = true
	keyCache["LocalAPkeyCache"] = n.Roaming.APKeyCache
	rec.note("wlan.roaming.ap_key_cache", "KeyCache.LocalAPkeyCache")
	opt("wlan.roaming.neighbor_report", n.Roaming.NeighborReport, "WNM", "NeighborReport")
	opt("wlan.roaming.bss_load", n.Roaming.BSSLoad, "WNM", "BSSLoad")
	opt("wlan.roaming.bss_transition", n.Roaming.BSSTransition, "WNM", "BSSTransitionManagement", "Passive")
	opt("wlan.roaming.ap_steering", n.Roaming.APSteering, "WNM", "BSSTransitionManagement", "Active")
	opt("wlan.broadcast.limit", n.Broadcast.Limit, "BcastFilter", "Mcast")
	opt("wlan.broadcast.allow_mdns", n.Broadcast.AllowMDNS, "BcastFilter", "Mdns_allow")
	opt("wlan.broadcast.allow_ssdp", n.Broadcast.AllowSSDP, "BcastFilter", "Ssdp_allow")
	opt("wlan.broadcast.allow_ipv6_nd", n.Broadcast.AllowIPv6ND, "BcastFilter", "AllowICMPv6NeighborDiscovery")
	opt("wlan.broadcast.arp_filter", n.Broadcast.ARPFilter, "BcastFilter", "Arp")
	opt("wlan.broadcast.wireless_bridge_dhcp", n.Broadcast.WirelessBridgeDHCP, "BcastFilter", "AllowDHCPtoWirelessBridge")
	opt("wlan.broadcast.mcast_to_ucast", n.Broadcast.McastToUcast, "Bcast2Ucast", "ForcedEnabled")
	opt("wlan.dhcp_only", n.DHCPOnly, "NoStaticIP")
	opt("wlan.dhcp_dns_only", n.DHCPDNSOnly, "RestrictDNS", "FromDHCP")
	idle := n.IdleTimeout
	if idle == 0 {
		idle = 1800
	}
	opt("wlan.idle_timeout_sec", num(idle), "WCInactivity", "TimeoutSec")
	if n.ReclaimTimeoutSec > 0 {
		opt("wlan.reclaim_timeout_sec", num(n.ReclaimTimeoutSec), "WCInactivity", "ReclaimTimeoutSec")
	}
	opt("wlan.max_clients", num(n.MaxClients), "MaxStaAllowed")
	limit := n.RateLimit
	if limit == nil {
		limit = &model.RateLimit{}
	}
	opt("wlan.rate_limit.wlan_up", num(limit.WLANUp), "RateLimit", "WlanUp")
	opt("wlan.rate_limit.wlan_down", num(limit.WLANDown), "RateLimit", "WlanDown")
	opt("wlan.rate_limit.client_up", num(limit.ClientUp), "RateLimit", "ClientUp")
	opt("wlan.rate_limit.client_down", num(limit.ClientDown), "RateLimit", "ClientDown")
	class, ok := qosClasses[n.QoSClass]
	if !ok {
		return nil, nil, fmt.Errorf("network %s: unknown QoS class %q", n.ID, n.QoSClass)
	}
	opt("wlan.qos_class", map[string]any{"Enabled": true, "AccessClass": num(class)}, "QOS")
	dtim := n.DTIM
	if dtim == 0 {
		dtim = 2
	}
	opt("wlan.dtim", num(dtim), "DTIM", "Period")
	opt("wlan.wmm_disabled", n.WMMDisabled, "WME", "Disabled")
	opt("wlan.uapsd_disabled", n.UAPSDDisabled, "WME", "DisableApsd")
	opt("wlan.disable_11ax", n.Disable11ax, "DisabledIEs", "DisableHeIes")
	opt("wlan.disable_11nac", n.Disable11nac, "DisabledIEs", "DisableHTIes")
	opt("wlan.disable_11nac", n.Disable11nac, "DisabledIEs", "DisableVHTIes")
	opt("wlan.honour_blocklist", n.HonourBlocklist, "BlackList", "Enabled")
	if n.UcastDisassocOnBssDown != nil {
		opt("wlan.ucast_disassoc_on_bss_down", *n.UcastDisassocOnBssDown, "UcastDisassocOnBssDown")
	}
	if o82 := n.Option82; o82 != nil {
		opt("wlan.option82.enabled", o82.Enabled, "DhcpOption82")
		if o82.Enabled {
			circuit := strings.NewReplacer("{SSID}", n.SSID, "{AP_MAC}", macDashed(in.ap.ID)).Replace(o82.CircuitID)
			opt("wlan.option82.circuit_id", circuit, "DhcpOption82Data")
		}
	}
	// The schedule's on/off state is stored (Schedule.OffAir, set by the
	// API's evaluator), never computed from the clock here.
	if sched := n.Schedule; sched != nil && sched.Enabled && sched.OffAir {
		opt("wlan.schedule", map[string]any{"Disabled": true, "Reason": num(8)}, "BSS")
	}

	// Expert patch, then the protected fields.
	var shadows []Shadow
	if len(n.Expert) != 0 {
		folded, err := FoldPatch(ScopeWLAN, n.Expert)
		if err != nil {
			return nil, nil, fmt.Errorf("network %s expert patch: %w", n.ID, err)
		}
		patch, err := decodeObject(folded.Patch)
		if err != nil {
			return nil, nil, err
		}
		before := rec.snapshot(w)
		merged, err := KeyedMerge(w, patch, "WLANs[]")
		if err != nil {
			return nil, nil, fmt.Errorf("network %s expert patch: %w", n.ID, err)
		}
		w = merged.(map[string]any)
		shadows = rec.shadows(before, w, ScopeWLAN, n.ID)
	}
	w["ID"] = n.ID
	w["SSID"] = n.SSID
	w["Enabled"] = n.Enabled
	auth = child(w, "Auth")
	if mapping.Passphrase {
		auth["PSK"] = n.PSK
	} else {
		auth["PSK"] = ""
	}
	if n.MPSK != nil && n.MPSK.Enabled {
		// The keys are protected: a patch cannot change or remove them.
		multi, ok := auth["MultiPSK"].(map[string]any)
		if !ok {
			multi = map[string]any{"Enabled": true}
			auth["MultiPSK"] = multi
		}
		multi["PSKs"] = psks
	}
	if n.Roaming.APKeyCache {
		child(w, "KeyCache")["KeyEncryptKey"] = in.site.Roaming.KeyEncryptKey
	} else if kc, ok := w["KeyCache"].(map[string]any); ok {
		kc["KeyEncryptKey"] = ""
	}
	if configs, ok := w["BandConfigs"].([]any); ok {
		for _, element := range configs {
			if bc, ok := element.(map[string]any); ok {
				bc["BSSIDIndex"] = num(n.BSSIDIndex)
			}
		}
	}
	return w, shadows, nil
}

// ScheduleOn reports whether a WLAN schedule is on air at now in the site
// timezone ("" = UTC).
func ScheduleOn(schedule model.Schedule, timezone string, now time.Time) (bool, error) {
	location := time.UTC
	if timezone != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			return true, fmt.Errorf("unknown timezone %q", timezone)
		}
		location = loaded
	}
	local := now.In(location)
	day := strings.ToLower(local.Weekday().String()[:3])
	minute := local.Hour()*60 + local.Minute()
	parse := func(text string) (int, error) {
		hour, rest, ok := strings.Cut(text, ":")
		if !ok {
			return 0, fmt.Errorf("invalid time %q", text)
		}
		h, err1 := strconv.Atoi(hour)
		m, err2 := strconv.Atoi(rest)
		if err1 != nil || err2 != nil || h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
			return 0, fmt.Errorf("invalid time %q", text)
		}
		return h*60 + m, nil
	}
	for _, window := range schedule.Windows {
		start, err := parse(window.Start)
		if err != nil {
			return true, err
		}
		end, err := parse(window.End)
		if err != nil {
			return true, err
		}
		if strings.ToLower(window.Day) == day && minute >= start && minute < end {
			return true, nil
		}
	}
	return false, nil
}
