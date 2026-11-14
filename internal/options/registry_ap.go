package options

// AP options (scope ap), from the site-device.md catalogue and design §3.6.
// Per-AP radio overrides are in registry_rf.go (scope ap.rf).

func apOption(o Option) Option {
	o.Scope = ScopeAP
	if o.Disruption == "" {
		o.Disruption = DisruptionNone
	}
	if o.Phase == 0 {
		o.Phase = 2
	}
	return o
}

var apOptions = []Option{
	apOption(Option{
		Key: "ap.name", Label: "Name", Help: "Also the AP's hostname (sanitised when the site uses the AP name as hostname).",
		Control: "text", Type: "string", MaxBytes: 64, Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"Name", "Hostname", "SVIs[].DHCP.Hostname"}, Catalogue: "site-device.md §1",
	}),
	apOption(Option{
		Key: "ap.site_id", Label: "Site", Help: "Changing it moves the AP.", Control: "select", Type: "string",
		Tier: TierBasic, Confidence: Confirmed, DismistOnly: true, Catalogue: "design §3.6",
	}),
	apOption(Option{
		Key: "ap.management_ip", Label: "Pinned source IP", Help: "Only accept this AP from this address.",
		Control: "text", Type: "string", Tier: TierBasic, Confidence: Confirmed, DismistOnly: true, Catalogue: "design §3.6",
	}),
	apOption(Option{
		Key: "ap.led_brightness", Label: "LED brightness", Help: "Inherits the site default unless overridden.",
		Control: "slider", Type: "int", Min: Int(0), Max: Int(255), Tier: TierBasic, Confidence: Confirmed,
		FirmwarePaths: []string{"LED.Brightness"}, Catalogue: "site-device.md §5",
	}),
	apOption(Option{
		Key: "ap.ssh_enabled", Label: "SSH access", Help: "Inherit, On or Off; On needs a site key.",
		Control: "segmented", Type: "bool", Tier: TierAdvanced, Confidence: Confirmed,
		FirmwarePaths: []string{"SSH.Disabled", "SSH.External", "SSH.AuthorizedKeys"}, Catalogue: "site-device.md §4",
	}),
	apOption(Option{
		Key: "ap.management.ip_mode", Label: "IP mode", Control: "segmented", Type: "string", Default: "dhcp",
		Enum: []EnumValue{{Value: "dhcp", Label: "DHCP"}, {Value: "static", Label: "Static"}},
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Disruption: DisruptionStrandingRisk, Phase: 3,
		FirmwarePaths: []string{"SVIs[].DHCP.Enabled", "SVIs[].DHCP_Ena"}, Catalogue: "site-device.md §2",
	}),
	apOption(Option{
		Key: "ap.management.address", Label: "Address / prefix / gateway", Control: "text", Type: "object",
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Disruption: DisruptionStrandingRisk, Phase: 3,
		FirmwarePaths: []string{"SVIs[].IP", "SVIs[].Mask", "SVIs[].Gw"}, Catalogue: "site-device.md §2",
		VisibleWhen: []Condition{{Key: "ap.management.ip_mode", In: []string{"static"}}},
	}),
	apOption(Option{
		Key: "ap.management.dns", Label: "DNS servers", Control: "list", Type: "[]string",
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Disruption: DisruptionStrandingRisk, Phase: 3,
		FirmwarePaths: []string{"DNS"}, Catalogue: "site-device.md §2",
	}),
	apOption(Option{
		Key: "ap.management.vlan_id", Label: "Management VLAN", Control: "segmented", Type: "int", Min: Int(1), Max: Int(4094),
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Disruption: DisruptionStrandingRisk, Phase: 3,
		FirmwarePaths: []string{"SVIs[].Vlan_ID", "SwitchPorts[].Vlans"}, Catalogue: "site-device.md §2, §3a",
	}),
	apOption(Option{
		Key: "ap.management.ntp", Label: "NTP servers", Control: "list", Type: "[]string",
		Tier: TierAdvanced, Confidence: Likely, Phase: 3, FirmwarePaths: []string{"NTP"}, Catalogue: "site-device.md §2",
	}),
	apOption(Option{
		Key: "ap.eth1", Label: "eth1 mode", Control: "select", Type: "string",
		Enum: []EnumValue{
			{Value: "inherit", Label: "Inherit"}, {Value: "same_as_uplink", Label: "Same as uplink"},
			{Value: "access", Label: "Access VLAN"}, {Value: "trunk", Label: "Custom trunk"}, {Value: "disabled", Label: "Disabled"},
		},
		Tier: TierAdvanced, Confidence: NeedsLiveTest, Phase: 3,
		FirmwarePaths: []string{"SwitchPorts[].Vlans", "SwitchPorts[].PortVlanID", "SwitchPorts[].Phy.Shutdown"}, Catalogue: "site-device.md §3",
	}),
	apOption(Option{
		Key: "ap.wired_client_stats", Label: "Report wired clients on eth1", Control: "segmented", Type: "bool",
		Tier: TierAdvanced, Confidence: Likely, Phase: 3, FirmwarePaths: []string{"EnableWiredClientStats"}, Catalogue: "site-device.md §3",
	}),
	apOption(Option{
		Key: "ap.locate", Label: "Locate (blink LED)", Type: "bool", Tier: TierReadonly, Confidence: NeedsLiveTest, Phase: 3,
		FirmwarePaths: []string{"LED.Locator"}, Catalogue: "site-device.md §5, §17",
	}),
}
