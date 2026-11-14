package options

// WLAN options (scope wlan), from the wlans.md catalogue and design §3.9.
// Every WLAN field is classed wlan_restart until the Lab measures which
// fields really restart the BSS (§9 Q4); the live AP showed that a WLAN
// security change restarts it.

var personalModes = []string{"wpa2_personal", "wpa2_wpa3_personal", "wpa3_personal"}
var enterpriseModes = []string{"wpa2_enterprise", "wpa2_wpa3_enterprise", "wpa3_enterprise"}
var saeModes = []string{"wpa2_wpa3_personal", "wpa3_personal"}

func wlanOption(o Option) Option {
	o.Scope = ScopeWLAN
	if o.Disruption == "" {
		o.Disruption = DisruptionWLANRestart
	}
	if o.Phase == 0 {
		o.Phase = 2
	}
	return o
}

func boolWLAN(key, label, help string, tier Tier, confidence Confidence, def, legacy any, catalogue string, paths ...string) Option {
	return wlanOption(Option{
		Key: key, Label: label, Help: help, Control: "switch", Type: "bool", Default: def, LegacyDefault: legacy,
		Tier: tier, Confidence: confidence, FirmwarePaths: paths, Catalogue: catalogue,
	})
}

var wlanOptions = []Option{
	// Basic.
	wlanOption(Option{
		Key: "wlan.ssid", Label: "SSID", Help: "The network name, 1 to 32 bytes.",
		Control: "text", Type: "string", MaxBytes: 32, Default: "", Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].SSID"}, Catalogue: "wlans.md §5.1",
	}),
	boolWLAN("wlan.enabled", "Enabled", "", TierBasic, Confirmed, true, nil, "wlans.md §5.1", "WLANs[].Enabled"),
	wlanOption(Option{
		Key: "wlan.security", Label: "Security", Help: "How clients authenticate and encrypt.",
		Control: "select", Type: "string", Default: "wpa2_wpa3_personal", LegacyDefault: "wpa2_personal",
		Enum: []EnumValue{
			{Value: "wpa2_personal", Label: "WPA2-Personal", Confidence: Confirmed, Phase: 2},
			{Value: "wpa2_wpa3_personal", Label: "WPA2/WPA3-Personal (transition)", Confidence: Confirmed, Phase: 2},
			{Value: "wpa3_personal", Label: "WPA3-Personal", Confidence: Likely, Phase: 2},
			{Value: "open", Label: "Open (no encryption)", Confidence: Confirmed, Phase: 2},
			{Value: "owe", Label: "Enhanced Open (OWE)", Confidence: NeedsLiveTest, Phase: 2},
			{Value: "owe_transition", Label: "Enhanced Open + Open (transition)", Help: "Counts as 2 WLANs per band.", Confidence: NeedsLiveTest, Phase: 2},
			{Value: "wpa2_enterprise", Label: "WPA2-Enterprise", Confidence: Likely, Phase: 4},
			{Value: "wpa2_wpa3_enterprise", Label: "WPA2/WPA3-Enterprise", Confidence: Likely, Phase: 4},
			{Value: "wpa3_enterprise", Label: "WPA3-Enterprise", Confidence: Likely, Phase: 4},
		},
		Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].Auth.Type", "WLANs[].Auth.AKM", "WLANs[].Auth.Pairwise", "WLANs[].Auth.PSK",
			"WLANs[].Dot11w", "WLANs[].OWE", "WLANs[].Auth.TransitionDisableIndication", "WLANs[].Auth_servers"},
		Catalogue: "wlans.md §7.2",
	}),
	wlanOption(Option{
		Key: "wlan.psk", Label: "Passphrase",
		Help:    "WPA2-Personal: 8 to 63 printable characters or 64 hex digits. WPA3 and transition modes: 8 to 63 printable characters.",
		Control: "password", Type: "string", Secret: true, Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].Auth.PSK"}, Catalogue: "wlans.md §7.3",
		VisibleWhen: []Condition{{Key: "wlan.security", In: personalModes}},
	}),
	wlanOption(Option{
		Key: "wlan.bands", Label: "Bands", Control: "chips", Type: "[]string", Default: []string{"2.4", "5"},
		Enum: []EnumValue{{Value: "2.4", Label: "2.4 GHz"}, {Value: "5", Label: "5 GHz"}},
		Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].Bands", "WLANs[].RateSets", "WLANs[].BandConfigs"}, Catalogue: "wlans.md §5.1",
	}),
	wlanOption(Option{
		Key: "wlan.vlan_id", Label: "VLAN",
		Help:    "Untagged puts clients on the switch port's native VLAN. Tagged sends client traffic with an 802.1Q tag; the switch port must trunk that VLAN.",
		Control: "segmented", Type: "int", Min: Int(1), Max: Int(4094), Default: 1, Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].Vlans", "SwitchPorts[].Vlans"}, Catalogue: "wlans.md §5.10",
	}),
	boolWLAN("wlan.hidden", "Hidden SSID", "", TierBasic, Likely, false, nil, "wlans.md §5.1", "WLANs[].Hidden"),
	wlanOption(Option{
		Key: "wlan.isolation", Label: "Client isolation", Control: "select", Type: "string", Default: "none",
		Enum: []EnumValue{
			{Value: "none", Label: "None"},
			{Value: "clients", Label: "Isolate clients"},
			{Value: "clients_and_lan", Label: "Isolate clients and LAN"},
		},
		Tier: TierBasic, Confidence: Likely,
		FirmwarePaths: []string{"WLANs[].Isolation", "WLANs[].NetIsolation"}, Catalogue: "wlans.md §5.10",
	}),
	wlanOption(Option{
		Key: "wlan.radius.auth", Label: "RADIUS servers", Help: "1 or 2 servers {host, port, secret}; port 1812 by default.",
		Control: "list", Type: "object", Tier: TierBasic, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Auth_servers"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),

	// Advanced: security.
	wlanOption(Option{
		Key: "wlan.pmf", Label: "PMF (802.11w)", Control: "select", Type: "string", Default: "optional",
		Enum: []EnumValue{
			{Value: "disabled", Label: "Disabled"},
			{Value: "optional", Label: "Optional"},
			{Value: "required", Label: "Required"},
		},
		Tier: TierAdvanced, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].Dot11w", "WLANs[].Auth.AKM"}, Catalogue: "wlans.md §5.3",
		VisibleWhen: []Condition{{Key: "wlan.security", In: []string{"wpa2_personal", "wpa2_enterprise"}}},
	}),
	wlanOption(Option{
		Key: "wlan.sae_h2e_only", Label: "SAE H2E only", Help: "Older clients can't join.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"WLANs[].Auth.SAEHash2ElementOnly"}, Catalogue: "wlans.md §5.4",
		VisibleWhen: []Condition{{Key: "wlan.security", In: saeModes}},
	}),
	wlanOption(Option{
		Key: "wlan.transition_disable", Label: "WPA3 transition disable", Help: "Tells WPA3 clients never to fall back to WPA2 on this network.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"WLANs[].Auth.TransitionDisableIndication"}, Catalogue: "wlans.md §5.4",
		VisibleWhen: []Condition{{Key: "wlan.security", In: []string{"wpa2_wpa3_personal"}}},
	}),
	wlanOption(Option{
		Key: "wlan.group_rekey_sec", Label: "Group key rekey", Control: "number", Type: "int", Unit: "s",
		Min: Int(60), Max: Int(86400), Default: 86400, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"WLANs[].Auth.GroupRekeyIntervalSec", "WLANs[].Auth.GroupMasterRekeyIntervalSec"}, Catalogue: "wlans.md §5.2",
	}),
	boolWLAN("wlan.eapol_v1", "EAPOL v1", "For pre-2004 clients.", TierAdvanced, Likely, false, nil, "wlans.md §5.2", "WLANs[].Auth.EAPOLVersion"),
	wlanOption(Option{
		Key: "wlan.radius.mac_auth", Label: "MAC authentication", Control: "switch", Type: "bool", Default: false,
		Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].MacAuth.Enabled"}, Catalogue: "wlans.md §5.7",
	}),
	wlanOption(Option{
		Key: "wlan.owe_ssid", Label: "OWE companion SSID", Control: "text", Type: "string", MaxBytes: 32, Default: "",
		Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"WLANs[].OWE.SSID"}, Catalogue: "wlans.md §5.5",
		VisibleWhen: []Condition{{Key: "wlan.security", In: []string{"owe_transition"}}},
	}),

	// Advanced: roaming.
	wlanOption(Option{
		Key: "wlan.roaming.fast", Label: "Fast roaming", Control: "select", Type: "string", Default: "none",
		Enum: []EnumValue{
			{Value: "none", Label: "None", Confidence: Confirmed},
			{Value: "okc", Label: "OKC", Confidence: NeedsLiveTest},
			{Value: "11r", Label: "802.11r", Confidence: NeedsLiveTest},
		},
		Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"WLANs[].RoamMode", "WLANs[].MDID", "WLANs[].OverDS", "WLANs[].Auth.AKM"}, Catalogue: "wlans.md §5.8, §7.4",
	}),
	wlanOption(Option{
		Key: "wlan.roaming.ap_key_cache", Label: "AP-to-AP key caching",
		Help:    "Lets clients roam between your APs without the Mist cloud's key cache.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 3,
		FirmwarePaths: []string{"WLANs[].KeyCache.LocalAPkeyCache", "WLANs[].KeyCache.KeyEncryptKey"}, Catalogue: "wlans.md §5.8",
		VisibleWhen: []Condition{{Key: "wlan.roaming.fast", In: []string{"okc", "11r"}}},
	}),
	boolWLAN("wlan.roaming.neighbor_report", "802.11k neighbour reports", "", TierAdvanced, Confirmed, true, nil, "wlans.md §5.8", "WLANs[].WNM.NeighborReport"),
	boolWLAN("wlan.roaming.bss_transition", "802.11v BSS transition", "", TierAdvanced, Confirmed, true, nil, "wlans.md §5.8", "WLANs[].WNM.BSSTransitionManagement.Passive"),
	boolWLAN("wlan.roaming.ap_steering", "AP-initiated steering (BTM)", "", TierAdvanced, Likely, false, nil, "wlans.md §5.8", "WLANs[].WNM.BSSTransitionManagement.Active"),
	boolWLAN("wlan.roaming.bss_load", "BSS Load element", "", TierAdvanced, Confirmed, true, nil, "wlans.md §5.8", "WLANs[].WNM.BSSLoad"),

	// Advanced: radio and rates.
	wlanOption(Option{
		Key: "wlan.band_steering", Label: "Band steering, prefer 5 GHz", Help: "Needs both bands.",
		Control: "switch", Type: "bool", Default: false, LegacyDefault: true, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"WLANs[].BandConfigs[].Steer"}, Catalogue: "wlans.md §5.9",
	}),
	wlanOption(Option{
		Key: "wlan.rates", Label: "Data rate profile", Control: "select", Type: "string", Default: "compatible",
		Enum: []EnumValue{
			{Value: "compatible", Label: "Compatible", Confidence: Confirmed},
			{Value: "no_legacy", Label: "No legacy (no 802.11b)", Confidence: Likely},
			{Value: "high_density", Label: "High density", Confidence: NeedsLiveTest},
			{Value: "legacy_only", Label: "Legacy only", Confidence: NeedsLiveTest},
		},
		Tier: TierAdvanced, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].RateSets"}, Catalogue: "wlans.md §7.4",
	}),
	wlanOption(Option{
		Key: "wlan.min_rssi.band_24", Label: "Minimum client RSSI, 2.4 GHz", Help: "Empty = off.",
		Control: "number", Type: "int", Unit: "dBm", Min: Int(-95), Max: Int(-50), Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"WLANs[].BandConfigs[].GeoFence"}, Catalogue: "wlans.md §5.9",
	}),
	wlanOption(Option{
		Key: "wlan.min_rssi.band_5", Label: "Minimum client RSSI, 5 GHz", Help: "Empty = off.",
		Control: "number", Type: "int", Unit: "dBm", Min: Int(-95), Max: Int(-50), Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"WLANs[].BandConfigs[].GeoFence"}, Catalogue: "wlans.md §5.9",
	}),
	wlanOption(Option{
		Key: "wlan.dtim", Label: "DTIM", Control: "number", Type: "int", Min: Int(1), Max: Int(255), Default: 2,
		Tier: TierAdvanced, Confidence: Confirmed, FirmwarePaths: []string{"WLANs[].DTIM.Period"}, Catalogue: "wlans.md §5.9",
	}),
	boolWLAN("wlan.wmm_disabled", "Disable WMM", "Breaks 802.11n/ac/ax rates.", TierAdvanced, Likely, false, nil, "wlans.md §5.9", "WLANs[].WME.Disabled"),
	boolWLAN("wlan.uapsd_disabled", "Disable U-APSD", "", TierAdvanced, Likely, false, nil, "wlans.md §5.9", "WLANs[].WME.DisableApsd"),
	boolWLAN("wlan.disable_11ax", "Disable 802.11ax", "", TierAdvanced, Likely, false, nil, "wlans.md §5.12", "WLANs[].DisabledIEs.DisableHeIes"),
	boolWLAN("wlan.disable_11nac", "Disable 802.11n/ac", "", TierAdvanced, Likely, false, nil, "wlans.md §5.12", "WLANs[].DisabledIEs.DisableHTIes", "WLANs[].DisabledIEs.DisableVHTIes"),

	// Advanced: network.
	boolWLAN("wlan.broadcast.limit", "Limit broadcast and multicast", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].BcastFilter.Mcast"),
	boolWLAN("wlan.broadcast.allow_mdns", "Allow mDNS", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].BcastFilter.Mdns_allow"),
	boolWLAN("wlan.broadcast.allow_ssdp", "Allow SSDP", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].BcastFilter.Ssdp_allow"),
	boolWLAN("wlan.broadcast.allow_ipv6_nd", "Allow IPv6 ND", "", TierAdvanced, Likely, true, nil, "wlans.md §5.10", "WLANs[].BcastFilter.AllowICMPv6NeighborDiscovery"),
	boolWLAN("wlan.broadcast.arp_filter", "ARP filter", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].BcastFilter.Arp"),
	boolWLAN("wlan.broadcast.mcast_to_ucast", "Multicast to unicast", "", TierAdvanced, NeedsLiveTest, false, nil, "wlans.md §5.10", "WLANs[].Bcast2Ucast.ForcedEnabled"),
	boolWLAN("wlan.broadcast.wireless_bridge_dhcp", "DHCP for wireless bridges", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].BcastFilter.AllowDHCPtoWirelessBridge"),
	boolWLAN("wlan.dhcp_only", "Only DHCP-addressed clients", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].NoStaticIP"),
	boolWLAN("wlan.dhcp_dns_only", "Only DHCP-provided DNS", "", TierAdvanced, Likely, false, nil, "wlans.md §5.10", "WLANs[].RestrictDNS.FromDHCP"),
	wlanOption(Option{
		Key: "wlan.option82.enabled", Label: "DHCP Option 82", Control: "switch", Type: "bool", Default: false,
		Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].DhcpOption82"}, Catalogue: "wlans.md §5.10",
	}),
	wlanOption(Option{
		Key: "wlan.option82.circuit_id", Label: "Option 82 circuit ID", Help: "{SSID} and {AP_MAC} are expanded per AP.",
		Control: "text", Type: "string", MaxBytes: 64, Default: "", Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].DhcpOption82Data"}, Catalogue: "wlans.md §5.10",
	}),

	// Advanced: limits and clients.
	wlanOption(Option{
		Key: "wlan.max_clients", Label: "Max clients", Help: "Empty = no limit (512).",
		Control: "number", Type: "int", Min: Int(2), Max: Int(512), Tier: TierAdvanced, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].MaxStaAllowed"}, Catalogue: "wlans.md §5.11",
	}),
	wlanOption(Option{
		Key: "wlan.idle_timeout_sec", Label: "Idle timeout", Control: "number", Type: "int", Unit: "s",
		Min: Int(60), Max: Int(86400), Default: 1800, LegacyDefault: 3600, Tier: TierAdvanced, Confidence: Confirmed,
		FirmwarePaths: []string{"WLANs[].WCInactivity.TimeoutSec"}, Catalogue: "wlans.md §5.12",
	}),
	rateLimitOption("wlan.rate_limit.wlan_up", "WLAN rate limit, up", "WLANs[].RateLimit.WlanUp"),
	rateLimitOption("wlan.rate_limit.wlan_down", "WLAN rate limit, down", "WLANs[].RateLimit.WlanDown"),
	rateLimitOption("wlan.rate_limit.client_up", "Per-client rate limit, up", "WLANs[].RateLimit.ClientUp"),
	rateLimitOption("wlan.rate_limit.client_down", "Per-client rate limit, down", "WLANs[].RateLimit.ClientDown"),
	wlanOption(Option{
		Key: "wlan.qos_class", Label: "QoS override", Control: "select", Type: "string", Default: "none",
		Enum: []EnumValue{
			{Value: "none", Label: "None"}, {Value: "best_effort", Label: "Best effort"},
			{Value: "background", Label: "Background"}, {Value: "video", Label: "Video"}, {Value: "voice", Label: "Voice"},
		},
		Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"WLANs[].QOS"}, Catalogue: "wlans.md §5.11",
	}),
	boolWLAN("wlan.honour_blocklist", "Honour blocked clients", "", TierAdvanced, Likely, true, nil, "wlans.md §5.12", "WLANs[].BlackList.Enabled"),

	// Advanced: RADIUS (phase 4, Enterprise modes).
	wlanOption(Option{
		Key: "wlan.radius.acct", Label: "Accounting servers", Help: "0 to 2 servers {host, port, secret}; port 1813 by default.",
		Control: "list", Type: "object", Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Acct_servers"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.acct_interim_sec", Label: "Interim accounting", Help: "Empty = use the server's value.",
		Control: "number", Type: "int", Unit: "s", Min: Int(0), Max: Int(65535), Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Acct_Interim_Interval"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.nas_id", Label: "NAS-Identifier", Help: "Empty = generated by the AP.",
		Control: "text", Type: "string", MaxBytes: 48, Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Nas_ID"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.nas_ip", Label: "NAS-IP-Address", Control: "text", Type: "string", Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].NasIPAddress"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.dynamic_vlan", Label: "Dynamic VLAN", Help: "RADIUS-assigned VLANs; each is added to the eth0 trunk.",
		Control: "list", Type: "object", Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Dynamic_vlan", "SwitchPorts[].Vlans"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.reauth_sec", Label: "Periodic re-authentication", Help: "Empty = off.",
		Control: "number", Type: "int", Unit: "s", Min: Int(0), Max: Int(86400), Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Auth.EapReauthIntervalSec"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.reauth_on_session_timeout", Label: "Re-authenticate when Session-Timeout expires",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Auth.ReauthOnSessionTimeout"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),
	wlanOption(Option{
		Key: "wlan.radius.coa", Label: "Change of Authorisation", Help: "0 to 2 peers {host, secret, local port 3799}.",
		Control: "list", Type: "object", Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 4,
		FirmwarePaths: []string{"WLANs[].COA.Enabled", "WLANs[].COA.COAServers", "WLANs[].COA.EventTimeStampOptional"}, Catalogue: "wlans.md §5.7",
		VisibleWhen: []Condition{{Key: "wlan.security", In: enterpriseModes}},
	}),

	// Advanced: per-device passphrases (phase 4, experimental).
	wlanOption(Option{
		Key: "wlan.mpsk.enabled", Label: "Per-device passphrases", Help: "WPA2-Personal only.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Auth.MultiPSK.Enabled"}, Catalogue: "wlans.md §5.6",
		VisibleWhen: []Condition{{Key: "wlan.security", In: []string{"wpa2_personal"}}},
	}),
	wlanOption(Option{
		Key: "wlan.mpsk.keys", Label: "Keys", Help: "Up to 32 {name, passphrase, VLAN, client MAC, expiry}.",
		Control: "list", Type: "object", Secret: true, Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Auth.MultiPSK.PSKs", "SwitchPorts[].Vlans"}, Catalogue: "wlans.md §5.6",
		VisibleWhen: []Condition{{Key: "wlan.security", In: []string{"wpa2_personal"}}},
	}),
	wlanOption(Option{
		Key: "wlan.mpsk.private", Label: "Isolate each key's devices", Control: "switch", Type: "bool", Default: false,
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 4,
		FirmwarePaths: []string{"WLANs[].Auth.MultiPSK.PrivateWlan"}, Catalogue: "wlans.md §5.6",
		VisibleWhen: []Condition{{Key: "wlan.security", In: []string{"wpa2_personal"}}},
	}),

	// Schedule (phase 4, experimental): the controller pushes BSS.Disabled
	// with Reason 8 outside the windows.
	wlanOption(Option{
		Key: "wlan.schedule", Label: "Schedule", Help: "Weekday on-air windows in the site timezone.",
		Control: "list", Type: "object", Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 4,
		FirmwarePaths: []string{"WLANs[].BSS"}, Catalogue: "wlans.md §7.4",
	}),

	// Expert-tier defaults (no form control, design §3.9).
	wlanOption(Option{
		Key: "wlan.ucast_disassoc_on_bss_down", Type: "bool", Default: true, LegacyDefault: "absent",
		Tier: TierExpert, Confidence: Likely, FirmwarePaths: []string{"WLANs[].UcastDisassocOnBssDown"}, Catalogue: "wlans.md §5.10, §8",
	}),
	wlanOption(Option{
		Key: "wlan.reclaim_timeout_sec", Type: "int", Unit: "s", Default: 300, LegacyDefault: 600,
		Tier: TierExpert, Confidence: Likely, FirmwarePaths: []string{"WLANs[].WCInactivity.ReclaimTimeoutSec"}, Catalogue: "wlans.md §5.12, §8",
	}),

	// Read-only facts.
	wlanOption(Option{
		Key: "wlan.bssid_index", Label: "BSSID index", Type: "int", Min: Int(1), Max: Int(15), Tier: TierReadonly, Confidence: Confirmed,
		Disruption: DisruptionNone, FirmwarePaths: []string{"WLANs[].BandConfigs[].BSSIDIndex"}, Catalogue: "wlans.md §2",
	}),
	wlanOption(Option{
		Key: "wlan.mdid", Label: "Mobility domain (802.11r)", Type: "int", Tier: TierReadonly, Confidence: Confirmed,
		Disruption: DisruptionNone, FirmwarePaths: []string{"WLANs[].MDID"}, Catalogue: "wlans.md §5.8",
	}),
}

func rateLimitOption(key, label, path string) Option {
	return wlanOption(Option{
		Key: key, Label: label, Help: "Empty = unlimited.", Control: "number", Type: "int", Unit: "kbps",
		Min: Int(1), Max: Int(999000), Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{path}, Catalogue: "wlans.md §5.11",
	})
}
