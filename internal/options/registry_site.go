package options

// Site options (scope site), from the site-device.md catalogue and design
// §3.11.

func siteOption(o Option) Option {
	o.Scope = ScopeSite
	if o.Disruption == "" {
		o.Disruption = DisruptionNone
	}
	if o.Phase == 0 {
		o.Phase = 2
	}
	return o
}

var siteOptions = []Option{
	// General.
	siteOption(Option{
		Key: "site.name", Label: "Name", Control: "text", Type: "string", MaxBytes: 64, Tier: TierBasic, Confidence: Confirmed,
		DismistOnly: true, Catalogue: "site-device.md §1 (SiteName is ignored by 0.15)",
	}),
	siteOption(Option{
		Key: "site.country", Label: "Country", Help: "Channels and power limits come from the ccrev, not from the country code.",
		Control: "select", Type: "string", Enum: []EnumValue{{Value: "AU", Label: "Australia (AU/890)", Confidence: Confirmed}},
		Tier: TierBasic, Confidence: Confirmed, Disruption: DisruptionAllRadios,
		FirmwarePaths: []string{"Country", "Radios[].CCRev", "Radios[].CCRevSP", "Monitor.CCRev"}, Catalogue: "radios-rf.md §5, §11",
	}),
	siteOption(Option{
		Key: "site.timezone", Label: "Timezone", Control: "text", Type: "string", Tier: TierBasic, Confidence: Confirmed,
		DismistOnly: true, Catalogue: "design §3.11",
	}),
	siteOption(Option{
		Key: "site.experimental", Label: "Experimental options",
		Help:    "Allows options not yet verified on AP33 0.15. Test on a lab AP first.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Confirmed,
		DismistOnly: true, Catalogue: "design P4",
	}),

	// Access.
	siteOption(Option{
		Key: "site.ssh.enabled", Label: "SSH access", Help: "Needs at least 1 key.",
		Control: "switch", Type: "bool", Default: false, Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"SSH.Disabled", "SSH.External", "SSH.AuthorizedKeys"}, Catalogue: "site-device.md §4",
	}),
	siteOption(Option{
		Key: "site.ssh.keys", Label: "Root SSH keys", Help: "OpenSSH public keys, at most 32; comments are kept.",
		Control: "ssh_keys", Type: "[]string", Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"SSH.AuthorizedKeys"}, Catalogue: "site-device.md §4",
	}),
	siteOption(Option{
		Key: "site.ssh.readonly_keys", Label: "Read-only stats keys", Control: "ssh_keys", Type: "[]string",
		Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"SSH.RestrictedKeys"}, Catalogue: "site-device.md §4",
	}),
	siteOption(Option{
		Key: "site.syslog", Label: "Remote syslog", Help: "Up to 16 {protocol, host, port, facility, severity, tag}.",
		Control: "list", Type: "object", Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"Syslogs"}, Catalogue: "site-device.md §9",
	}),

	// Network (management defaults).
	siteOption(Option{
		Key: "site.management.ntp", Label: "NTP servers", Help: "At most 6. Empty = DHCP, then pool.ntp.org.",
		Control: "list", Type: "[]string", Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"NTP"}, Catalogue: "site-device.md §2",
	}),
	siteOption(Option{
		Key: "site.management.dns", Label: "DNS servers", Help: "Overrides DNS from DHCP on every AP. At most 3.",
		Control: "list", Type: "[]string", Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"DNS"}, Catalogue: "site-device.md §2",
	}),
	siteOption(Option{
		Key: "site.management.vlan_id", Label: "Management VLAN", Help: "Needs config auto-recovery.",
		Control: "segmented", Type: "int", Min: Int(1), Max: Int(4094), Default: 1, Tier: TierAdvanced, Confidence: NeedsLiveTest,
		Disruption: DisruptionStrandingRisk, FirmwarePaths: []string{"SVIs[].Vlan_ID", "SwitchPorts[].Vlans"}, Catalogue: "site-device.md §2, §3a",
	}),

	// AP defaults.
	siteOption(Option{
		Key: "site.ap_defaults.led_brightness", Label: "LED brightness", Control: "slider", Type: "int",
		Min: Int(0), Max: Int(255), Default: 255, Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"LED.Brightness"}, Catalogue: "site-device.md §5",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.hostname_from_name", Label: "Use the AP name as hostname and DHCP hostname",
		Control: "switch", Type: "bool", Default: true, LegacyDefault: false, Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"Hostname", "SVIs[].DHCP.Hostname"}, Catalogue: "site-device.md §1",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.keep_wlans_up_on_uplink_down", Label: "Keep WLANs up when the uplink is down",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"Monitor.EthUplink.Enabled"}, Catalogue: "site-device.md §10",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.poe_constrained", Label: "When PoE power is insufficient", Control: "select", Type: "string",
		Enum:    []EnumValue{{Value: "keep_radios", Label: "Keep radios on"}, {Value: "disable_radios", Label: "Disable radios to fit"}},
		Default: "keep_radios", LegacyDefault: "disable_radios", Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"PoePwrConstrained.EnableRadios"}, Catalogue: "site-device.md §6",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.wired_client_stats", Label: "Report wired clients on eth1",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"EnableWiredClientStats"}, Catalogue: "site-device.md §3",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.eth1", Label: "eth1 default mode", Control: "select", Type: "string", Default: "same_as_uplink",
		Enum: []EnumValue{
			{Value: "same_as_uplink", Label: "Same as uplink"}, {Value: "access", Label: "Access VLAN"},
			{Value: "trunk", Label: "Custom trunk"}, {Value: "disabled", Label: "Disabled"},
		},
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 3,
		FirmwarePaths: []string{"SwitchPorts[].Vlans", "SwitchPorts[].PortVlanID", "SwitchPorts[].Phy.Shutdown"}, Catalogue: "site-device.md §3",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.config_recovery", Label: "Config auto-recovery", Help: "Minutes 1 to 60.",
		Control: "switch", Type: "object", Tier: TierAdvanced, Confidence: NeedsLiveTest,
		FirmwarePaths: []string{"APRecovery.Disabled", "APRecovery.RecoveryTimeMin"}, Catalogue: "site-device.md §17",
	}),
	siteOption(Option{
		Key: "site.ap_defaults.ble_disabled", Label: "Turn off the Bluetooth (BLE) radio",
		Help:    "dismist does not use Bluetooth; turning it off saves a little power.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"BLE.Disabled"}, Catalogue: "site-device.md §8",
	}),

	// Protection.
	siteOption(Option{
		Key: "site.protection.blocked_clients", Label: "Blocked clients", Control: "mac_list", Type: "object",
		Tier: TierAdvanced, Confidence: Likely, Phase: 3,
		FirmwarePaths: []string{"BlackList.WCMacs"}, Catalogue: "site-device.md §12",
	}),
	siteOption(Option{
		Key: "site.protection.proxy_arp", Label: "Proxy ARP", Control: "switch", Type: "bool", Default: true,
		Tier: TierAdvanced, Confidence: Likely, FirmwarePaths: []string{"ProxyArp.Enabled"}, Catalogue: "site-device.md §12",
	}),
	siteOption(Option{
		Key: "site.protection.arp_spoof_check", Label: "ARP spoof check", Help: "Needs proxy ARP.",
		Control: "switch", Type: "bool", Default: false, Tier: TierAdvanced, Confidence: Likely,
		FirmwarePaths: []string{"ProxyArp.Disable_ReqSpoofCheck", "ProxyArp.Disable_RespSpoofCheck"}, Catalogue: "site-device.md §12",
	}),
	siteOption(Option{
		Key: "site.protection.penalty_box", Label: "Repeated auth-failure penalty box", Control: "switch", Type: "object",
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 3,
		FirmwarePaths: []string{"WIDS"}, Catalogue: "site-device.md §12",
	}),
	siteOption(Option{
		Key: "site.protection.dhcp_fingerprinting", Label: "DHCP fingerprinting", Control: "switch", Type: "bool", Default: false,
		Tier: TierAdvanced, Confidence: Likely, FirmwarePaths: []string{"Fingerprint.DHCPPayload"}, Catalogue: "site-device.md §12",
	}),
}
