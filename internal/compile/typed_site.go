package compile

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options/regdomain"
	"github.com/nicko170/dismist/internal/rfplan"
)

// Steps 1 and 3 (design §4.4): the site's and the AP's typed fields.

// preambleLengths map the preamble option to PreambleLength.
var preambleLengths = map[string]int{"auto": 0, "short": 1, "long": 2}

// tafModes map airtime fairness to TAFMode.
var tafModes = map[string]int{"default": 0, "off": 1, "on": 2}

// antennaMasks map the antenna mode to the chain mask.
var antennaMasks = map[string]int{"default": 0, "4x4": 15, "3x3": 7, "2x2": 3, "1x1": 1}

// radioElement returns the Radios element of a band, creating it.
func radioElement(root map[string]any, band string) map[string]any {
	radios, _ := root["Radios"].([]any)
	if element, at := keyedElement(radios, "Band", band); at >= 0 {
		return element
	}
	element := map[string]any{"Band": band}
	root["Radios"] = append(radios, element)
	return element
}

func bandOption(band, name string) string {
	if band == "2.4" {
		return "site.rf.band_24." + name
	}
	return "site.rf.band_5." + name
}

func apBandOption(band, name string) string {
	if band == "2.4" {
		return "ap.rf.band_24." + name
	}
	return "ap.rf.band_5." + name
}

// SSHKeyLine renders a typed key as an authorized_keys line.
func SSHKeyLine(key model.SSHKey) string {
	line := strings.TrimSpace(key.Key)
	if comment := strings.TrimSpace(key.Comment); comment != "" {
		line += " " + comment
	}
	return line
}

func stringList(values []string) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = value
	}
	return out
}

// siteTyped is step 1: the site's typed fields and the derived ports.
func siteTyped(root map[string]any, site model.Site, wlans []map[string]any, networks []model.Network, rec *scopedRecorder, warnings *[]Warning) error {
	opt := func(option string, value any, keys ...string) {
		setAt(root, value, keys...)
		rec.note(option, strings.Join(keys, "."))
	}
	if country := strings.ToUpper(strings.TrimSpace(site.Country)); country != "" {
		opt("site.country", country, "Country")
	}

	// RF profile, only when managed.
	if site.RF.Managed {
		ccrev, ok := regdomain.CCRev(site.Country)
		if !ok {
			return fmt.Errorf("RF management needs a supported country (got %q)", site.Country)
		}
		for _, band := range rfplan.Bands {
			profile := site.RF.Band(band)
			radio := radioElement(root, band)
			prefix := "Radios[Band=" + band + "]."
			radio["CCRev"], radio["CCRevSP"] = ccrev, ccrev
			rec.note("site.country", prefix+"CCRev")
			rec.note("site.country", prefix+"CCRevSP")
			radio["Disabled"] = !profile.Enabled
			rec.note(bandOption(band, "enabled"), prefix+"Disabled")
			width := profile.Width
			if width == 0 {
				width = 20
			}
			radio["Width"] = num(width)
			child(radio, "RRM")["Width"] = num(width)
			rec.note(bandOption(band, "width"), prefix+"Width")
			rec.note(bandOption(band, "width"), prefix+"RRM.Width")
			if profile.Power.Mode == model.PowerFixed && profile.Power.DBm > 0 {
				radio["Power"] = num(profile.Power.DBm)
				child(radio, "RRM")["Power"] = num(profile.Power.DBm)
				rec.note(bandOption(band, "power"), prefix+"Power")
				rec.note(bandOption(band, "power"), prefix+"RRM.Power")
			}
			if profile.Preamble != "" {
				length, ok := preambleLengths[profile.Preamble]
				if !ok {
					return fmt.Errorf("%s: unknown preamble %q", bandOption(band, "preamble"), profile.Preamble)
				}
				radio["PreambleLength"] = num(length)
				rec.note(bandOption(band, "preamble"), prefix+"PreambleLength")
			}
			advanced := profile.Advanced
			if advanced.TAF != "" {
				mode, ok := tafModes[advanced.TAF]
				if !ok {
					return fmt.Errorf("%s: unknown value %q", bandOption(band, "taf"), advanced.TAF)
				}
				radio["TAFMode"] = num(mode)
				rec.note(bandOption(band, "taf"), prefix+"TAFMode")
			}
			if advanced.TWT != nil {
				radio["TWTDisabled"] = !*advanced.TWT
				rec.note(bandOption(band, "twt"), prefix+"TWTDisabled")
			}
			if advanced.Antenna != "" {
				mask, ok := antennaMasks[advanced.Antenna]
				if !ok || (band == "2.4" && mask > 3) {
					return fmt.Errorf("%s: invalid antenna mode %q", bandOption(band, "antenna"), advanced.Antenna)
				}
				chain := child(radio, "ChainSelectMode")
				chain["RxChain"], chain["TxChain"] = num(mask), num(mask)
				rec.note(bandOption(band, "antenna"), prefix+"ChainSelectMode.RxChain")
				rec.note(bandOption(band, "antenna"), prefix+"ChainSelectMode.TxChain")
			}
			if advanced.MinProbeRSSI != nil {
				probe := child(radio, "ProbeControl")
				probe["NoProbeRespRSSI"] = true
				probe["NoProbeRespRSSIThresh"] = num(*advanced.MinProbeRSSI)
				rec.note(bandOption(band, "min_probe_rssi"), prefix+"ProbeControl.NoProbeRespRSSI")
				rec.note(bandOption(band, "min_probe_rssi"), prefix+"ProbeControl.NoProbeRespRSSIThresh")
			}
			if advanced.MuMimo != nil {
				child(radio, "MuMimo")["Enabled"] = *advanced.MuMimo
				rec.note(bandOption(band, "mu_mimo"), prefix+"MuMimo.Enabled")
			}
			if advanced.TxBf != nil {
				child(radio, "TxBf")["Enabled"] = *advanced.TxBf
				rec.note(bandOption(band, "txbf"), prefix+"TxBf.Enabled")
			}
		}
		opt("site.country", ccrev, "Monitor", "CCRev")
		if site.RF.Scanning != nil {
			opt("site.rf.scanning_radio", *site.RF.Scanning, "Monitor", "Enabled")
		}
	}

	// SSH (root keys for 0.15 and RootAuthorizedSSHKeys for pre-0.15).
	if site.SSH.Enabled {
		keys := make([]string, 0, len(site.SSH.Keys))
		for _, key := range site.SSH.Keys {
			keys = append(keys, SSHKeyLine(key))
		}
		ssh := child(root, "SSH")
		ssh["Disabled"], ssh["External"], ssh["AuthorizedKeys"] = false, true, stringList(keys)
		root["RootAuthorizedSSHKeys"] = stringList(keys)
	} else {
		ssh := child(root, "SSH")
		ssh["Disabled"], ssh["External"], ssh["AuthorizedKeys"] = true, false, []any{}
	}
	rec.note("site.ssh.enabled", "SSH.Disabled")
	rec.note("site.ssh.enabled", "SSH.External")
	rec.note("site.ssh.keys", "SSH.AuthorizedKeys")
	if len(site.SSH.ReadonlyKeys) > 0 {
		var restricted []any
		for _, key := range site.SSH.ReadonlyKeys {
			restricted = append(restricted, map[string]any{"AuthorizedKey": SSHKeyLine(key)})
		}
		opt("site.ssh.readonly_keys", restricted, "SSH", "RestrictedKeys")
	}
	if len(site.Syslog) > 0 {
		var syslogs []any
		for _, target := range site.Syslog {
			port := target.Port
			if port == 0 {
				port = 514
			}
			network := target.Protocol
			if network == "" {
				network = "udp"
			}
			entry := map[string]any{
				"Network": network, "Address": net.JoinHostPort(target.Host, strconv.Itoa(port)),
				"Priority": num(target.Facility*8 + target.Severity),
			}
			if target.Tag != "" {
				entry["Tag"] = target.Tag
			}
			syslogs = append(syslogs, entry)
		}
		opt("site.syslog", syslogs, "Syslogs")
	}
	if len(site.Management.NTP) > 0 {
		opt("site.management.ntp", stringList(site.Management.NTP), "NTP")
	}
	if len(site.Management.DNS) > 0 {
		opt("site.management.dns", stringList(site.Management.DNS), "DNS")
		*warnings = append(*warnings, Warning{Code: "site_dns_override", Option: "site.management.dns",
			Message: "the site DNS servers override DNS from DHCP on every AP"})
	}
	if vlan := site.Management.VLANID; vlan > 1 {
		if svis, ok := root["SVIs"].([]any); ok && len(svis) > 0 {
			if svi, ok := svis[0].(map[string]any); ok {
				svi["Vlan_ID"] = strconv.Itoa(vlan)
				rec.note("site.management.vlan_id", "SVIs[Vlan_ID="+strconv.Itoa(vlan)+"].Vlan_ID")
			}
		}
	}
	defaults := site.APDefaults
	opt("site.ap_defaults.led_brightness", num(defaults.LEDBrightness), "LED", "Brightness")
	opt("site.ap_defaults.poe_constrained", defaults.PoEConstrained == model.PoEKeepRadios, "PoePwrConstrained", "EnableRadios")
	opt("site.ap_defaults.keep_wlans_up_on_uplink_down", !defaults.KeepWLANsUpOnUplinkDown, "Monitor", "EthUplink", "Enabled")
	opt("site.ap_defaults.wired_client_stats", defaults.WiredClientStats, "EnableWiredClientStats")
	if defaults.Recovery != nil {
		recovery := map[string]any{"Disabled": !defaults.Recovery.Enabled}
		if defaults.Recovery.Minutes > 0 {
			recovery["RecoveryTimeMin"] = num(defaults.Recovery.Minutes)
		}
		setAt(root, recovery, "APRecovery")
		rec.note("site.ap_defaults.config_recovery", "APRecovery.Disabled")
	}
	if defaults.BLEDisabled {
		opt("site.ap_defaults.ble_disabled", true, "BLE", "Disabled")
	}
	protection := site.Protection
	opt("site.protection.proxy_arp", protection.ProxyARP, "ProxyArp", "Enabled")
	opt("site.protection.arp_spoof_check", !protection.ARPSpoofCheck, "ProxyArp", "Disable_ReqSpoofCheck")
	opt("site.protection.arp_spoof_check", !protection.ARPSpoofCheck, "ProxyArp", "Disable_RespSpoofCheck")
	macs := make([]any, 0, len(protection.BlockedClients))
	for _, blocked := range protection.BlockedClients {
		macs = append(macs, macDashed(blocked.MAC))
	}
	opt("site.protection.blocked_clients", macs, "BlackList", "WCMacs")
	if box := protection.PenaltyBox; box != nil {
		opt("site.protection.penalty_box", []any{map[string]any{
			"Alarm": "PenaltyBox", "Enabled": true, "AlarmCounter": num(box.Threshold), "Interval": num(box.WindowSec),
		}}, "WIDS")
	}
	opt("site.protection.dhcp_fingerprinting", protection.DHCPFingerprint, "Fingerprint", "DHCPPayload")

	// Derived ports: every port's template VLANs, plus the tagged
	// management VLAN, plus every VLAN of the enabled WLANs (dynamic-VLAN
	// and Multi-PSK key VLANs included), in v0 order.
	var extra []string
	if vlan := site.Management.VLANID; vlan > 1 {
		extra = append(extra, strconv.Itoa(vlan))
	}
	for index, network := range networks {
		if !network.Enabled {
			continue
		}
		if vlans, ok := wlans[index]["Vlans"].([]any); ok {
			for _, vlan := range vlans {
				if text, ok := vlan.(string); ok {
					extra = append(extra, text)
				}
			}
		}
		if network.RADIUS != nil && network.RADIUS.DynamicVLAN != nil && network.RADIUS.DynamicVLAN.Enabled && model.EnterpriseSecurity(network.Security) {
			for _, vlan := range network.RADIUS.DynamicVLAN.VLANs {
				extra = append(extra, strconv.Itoa(vlan))
			}
		}
		if network.MPSK != nil && network.MPSK.Enabled {
			for _, key := range network.MPSK.Keys {
				if key.VLANID > 0 {
					extra = append(extra, strconv.Itoa(key.VLANID))
				}
			}
		}
	}
	if ports, ok := root["SwitchPorts"].([]any); ok {
		for _, element := range ports {
			port, ok := element.(map[string]any)
			if !ok {
				continue
			}
			name, _ := asString(port["Name"])
			eth1 := defaults.Eth1
			if name == "eth1" && eth1 != nil && eth1.Mode != "" && eth1.Mode != model.Eth1SameAsUplink {
				if err := applyEth1(port, *eth1); err != nil {
					return err
				}
				rec.note("site.ap_defaults.eth1", "SwitchPorts[Name=eth1].Vlans")
				continue
			}
			addPortVLANs(port, extra)
			rec.note("wlan.vlan_id", "SwitchPorts[Name="+name+"].Vlans")
		}
	}
	return nil
}

func addPortVLANs(port map[string]any, extra []string) {
	existing, _ := port["Vlans"].([]any)
	vlans := append([]any(nil), existing...)
	seen := map[string]bool{}
	for _, vlan := range vlans {
		if text, ok := vlan.(string); ok {
			seen[text] = true
		}
	}
	for _, vlan := range extra {
		if !seen[vlan] {
			vlans = append(vlans, vlan)
			seen[vlan] = true
		}
	}
	if vlans == nil {
		vlans = []any{}
	}
	port["Vlans"] = vlans
}

// applyEth1 writes a non-default eth1 mode (experimental).
func applyEth1(port map[string]any, eth1 model.Eth1) error {
	switch eth1.Mode {
	case model.Eth1Access:
		if eth1.VLANID < 1 || eth1.VLANID > 4094 {
			return fmt.Errorf("eth1 access VLAN %d out of range", eth1.VLANID)
		}
		port["Vlans"] = []any{strconv.Itoa(eth1.VLANID)}
		port["PortVlanID"] = strconv.Itoa(eth1.VLANID)
	case model.Eth1Trunk:
		var vlans []any
		for _, vlan := range eth1.VLANs {
			vlans = append(vlans, strconv.Itoa(vlan))
		}
		if vlans == nil {
			vlans = []any{}
		}
		port["Vlans"] = vlans
		pvid := ""
		if eth1.VLANID > 0 {
			pvid = strconv.Itoa(eth1.VLANID)
		}
		port["PortVlanID"] = pvid
	case model.Eth1Disabled:
		child(port, "Phy")["Shutdown"] = true
	default:
		return fmt.Errorf("unknown eth1 mode %q", eth1.Mode)
	}
	return nil
}

// transliterate maps common Latin letters with diacritics to ASCII.
var transliterate = func() map[rune]string {
	table := map[rune]string{'ß': "ss", 'æ': "ae", 'Æ': "ae", 'ø': "o", 'Ø': "o", 'œ': "oe", 'Œ': "oe", 'ð': "d", 'þ': "th", 'ł': "l", 'Ł': "l"}
	groups := map[string]string{
		"a": "àáâãäåāăą", "c": "çćĉċč", "d": "ďđ", "e": "èéêëēĕėęě", "g": "ĝğġģ", "h": "ĥħ",
		"i": "ìíîïĩīĭįı", "j": "ĵ", "k": "ķ", "l": "ĺļľŀ", "n": "ñńņňŉ", "o": "òóôõöōŏő",
		"r": "ŕŗř", "s": "śŝşš", "t": "ţťŧ", "u": "ùúûüũūŭůűų", "w": "ŵ", "y": "ýÿŷ", "z": "źżž",
	}
	for ascii, letters := range groups {
		for _, r := range letters {
			table[r] = ascii
			table[[]rune(strings.ToUpper(string(r)))[0]] = ascii
		}
	}
	return table
}()

// SanitiseHostname turns an AP name into a hostname (design §4.4):
// ASCII transliteration, lower case, characters outside [a-z0-9-] become
// "-", repeats collapsed, leading and trailing "-" trimmed, at most 63
// characters; empty becomes ap-<mac12>.
func SanitiseHostname(name, apID string) string {
	var b strings.Builder
	for _, r := range name {
		if ascii, ok := transliterate[r]; ok {
			b.WriteString(ascii)
			continue
		}
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	if out == "" {
		out = "ap-" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(apID, "-", ""), ":", ""))
	}
	return out
}

// apTyped is step 3: the AP's typed fields (name, hostname, overrides,
// radios).
func apTyped(root map[string]any, site model.Site, ap model.APRecord, now time.Time, rec *scopedRecorder, warnings *[]Warning) error {
	opt := func(option string, value any, keys ...string) {
		setAt(root, value, keys...)
		rec.note(option, strings.Join(keys, "."))
	}
	if ap.Name != "" {
		opt("ap.name", ap.Name, "Name")
	}
	if site.APDefaults.HostnameFromName {
		hostname := SanitiseHostname(ap.Name, ap.ID)
		opt("ap.name", hostname, "Hostname")
		if svis, ok := root["SVIs"].([]any); ok {
			for _, element := range svis {
				if svi, ok := element.(map[string]any); ok {
					child(svi, "DHCP")["Hostname"] = hostname
					rec.note("ap.name", "SVIs[Vlan_ID="+keyString(svi["Vlan_ID"])+"].DHCP.Hostname")
				}
			}
		}
	} else if ap.Name != "" {
		opt("ap.name", ap.Name, "Hostname")
	}
	if ap.LEDBrightness != nil {
		opt("ap.led_brightness", num(*ap.LEDBrightness), "LED", "Brightness")
	}
	if ap.SSHEnabled != nil {
		ssh := child(root, "SSH")
		if *ap.SSHEnabled {
			if !site.SSH.Enabled {
				keys := make([]string, 0, len(site.SSH.Keys))
				for _, key := range site.SSH.Keys {
					keys = append(keys, SSHKeyLine(key))
				}
				ssh["AuthorizedKeys"] = stringList(keys)
			}
			ssh["Disabled"], ssh["External"] = false, true
		} else {
			ssh["Disabled"], ssh["External"], ssh["AuthorizedKeys"] = true, false, []any{}
		}
		rec.note("ap.ssh_enabled", "SSH.Disabled")
		rec.note("ap.ssh_enabled", "SSH.External")
	}
	if ap.WiredClientStats != nil {
		opt("ap.wired_client_stats", *ap.WiredClientStats, "EnableWiredClientStats")
	}
	if ap.Eth1 != nil && ap.Eth1.Mode != "" {
		if ports, ok := root["SwitchPorts"].([]any); ok {
			if port, at := keyedElement(ports, "Name", "eth1"); at >= 0 {
				if ap.Eth1.Mode == model.Eth1SameAsUplink {
					if eth0, at0 := keyedElement(ports, "Name", "eth0"); at0 >= 0 {
						port["Vlans"] = deepCopy(eth0["Vlans"])
					}
				} else if err := applyEth1(port, *ap.Eth1); err != nil {
					return err
				}
				rec.note("ap.eth1", "SwitchPorts[Name=eth1].Vlans")
			}
		}
	}
	if m := ap.Management; m != nil {
		if svis, ok := root["SVIs"].([]any); ok && len(svis) > 0 {
			if svi, ok := svis[0].(map[string]any); ok {
				if m.VLANID != nil && *m.VLANID > 1 {
					svi["Vlan_ID"] = strconv.Itoa(*m.VLANID)
					rec.note("ap.management.vlan_id", "SVIs[Vlan_ID="+strconv.Itoa(*m.VLANID)+"].Vlan_ID")
					if ports, ok := root["SwitchPorts"].([]any); ok {
						if eth0, at := keyedElement(ports, "Name", "eth0"); at >= 0 {
							addPortVLANs(eth0, []string{strconv.Itoa(*m.VLANID)})
						}
					}
				}
				prefix := "SVIs[Vlan_ID=" + keyString(svi["Vlan_ID"]) + "]."
				child(svi, "DHCP")["Enabled"] = !m.Static
				svi["DHCP_Ena"] = !m.Static
				rec.note("ap.management.ip_mode", prefix+"DHCP.Enabled")
				if m.Static {
					svi["IP"], svi["Mask"], svi["Gw"] = m.Address, "/"+strconv.Itoa(m.Prefix), m.Gateway
					rec.note("ap.management.address", prefix+"IP")
				}
			}
		}
		if len(m.DNS) > 0 {
			opt("ap.management.dns", stringList(m.DNS), "DNS")
		}
		if len(m.NTP) > 0 {
			opt("ap.management.ntp", stringList(m.NTP), "NTP")
		}
	}
	if !ap.LocateUntil.IsZero() && !now.IsZero() && now.Before(ap.LocateUntil) {
		opt("ap.locate", true, "LED", "Locator")
	}

	// Radios: per band, channel and power from the AP's static override,
	// else its rf_plan, else the compile fallback (design §3.10).
	if !site.RF.Managed {
		return nil
	}
	domain, _ := rfplan.Domain(site)
	for _, band := range rfplan.Bands {
		radio := radioElement(root, band)
		prefix := "Radios[Band=" + band + "]."
		override := ap.RF[band]
		if override != nil && override.Enabled != nil {
			radio["Disabled"] = !*override.Enabled
			rec.note(apBandOption(band, "enabled"), prefix+"Disabled")
		}
		if override != nil && override.Width > 0 {
			radio["Width"] = num(override.Width)
			child(radio, "RRM")["Width"] = num(override.Width)
			rec.note(apBandOption(band, "width"), prefix+"Width")
			rec.note(apBandOption(band, "width"), prefix+"RRM.Width")
		}
		if !rfplan.Enabled(site, ap, band) {
			continue
		}
		width := rfplan.Width(site, ap, band)
		rrm := child(radio, "RRM")
		channel := rfplan.Static(ap, band)
		power := 0
		if channel != 0 {
			radio["Channel"], rrm["Channel"] = num(channel), num(channel)
		} else {
			plan, ok := ap.RFPlan[band]
			if !ok || !rfplan.ValidPlan(site, ap, band, plan) {
				fallback, found := rfplan.Fallback(site, ap, band)
				if !found {
					return fmt.Errorf("%s: no allowed channel for Auto at %d MHz", bandOption(band, "channels"), width)
				}
				plan = fallback
				*warnings = append(*warnings, Warning{Code: "rf_plan_missing", Option: apBandOption(band, "channel"),
					Message: fmt.Sprintf("%s GHz has no valid channel plan; compiled channel %d from the fallback", band, plan.Channel)})
			}
			channel, power = plan.Channel, plan.PowerDBm
			radio["Channel"], rrm["Channel"] = num(0), num(channel)
		}
		rec.note(apBandOption(band, "channel"), prefix+"Channel")
		rec.note(apBandOption(band, "channel"), prefix+"RRM.Channel")
		if override != nil && override.PowerDBm > 0 {
			power = override.PowerDBm
		} else if policy := site.RF.Band(band).Power; policy.Mode == model.PowerFixed && policy.DBm > 0 {
			power = 0 // the site's fixed power, written in step 1
		} else if power == 0 {
			_, power = rfplan.PowerRange(site, band)
		}
		if power > 0 {
			radio["Power"], rrm["Power"] = num(power), num(power)
			rec.note(apBandOption(band, "power_dbm"), prefix+"Power")
			rec.note(apBandOption(band, "power_dbm"), prefix+"RRM.Power")
		}
		if domain != nil {
			if dfs, _ := domain.Valid(band, width, channel); dfs {
				fallback := site.RF.Band5.DFSFallback
				if plan, ok := ap.RFPlan[band]; ok && plan.DFSFallback != 0 && rfplan.Static(ap, band) == 0 {
					fallback = plan.DFSFallback
				}
				if fallback == 0 {
					fallback = rfplan.DefaultDFSFallback(site, band, width)
				}
				radio["DFSjumplist"] = map[string]any{"ChanSpecs": []any{map[string]any{"Channel": num(fallback), "Width": num(width)}}}
				rec.note(apBandOption(band, "channel"), prefix+"DFSjumplist")
			}
		}
	}
	return nil
}
