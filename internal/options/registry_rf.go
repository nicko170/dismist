package options

import "strings"

// RF options (scopes site.rf and ap.rf), from the radios-rf.md catalogue
// and design §3.6 and §3.10. Channel widths and primaries come from the
// AU/890 table (regdomain); AP33 has no 160 MHz chanspec under AU/890
// (live finding), so 160 MHz is not offered.

func rfOption(o Option) Option {
	if o.Scope == "" {
		o.Scope = ScopeSiteRF
	}
	if o.Disruption == "" {
		o.Disruption = DisruptionRadioBounce
	}
	if o.Phase == 0 {
		o.Phase = 2
	}
	return o
}

func bandOptions(band, label string) []Option {
	prefix := "site.rf." + band + "."
	twoFour := band == "band_24"
	widths := []EnumValue{{Value: "20", Label: "20 MHz"}}
	defaultWidth := 20
	channelsDefault := []int{1, 6, 11}
	powerDefault := map[string]any{"mode": "auto", "min": 8, "max": 15}
	antennas := []EnumValue{{Value: "default", Label: "Default"}, {Value: "2x2", Label: "2x2"}, {Value: "1x1", Label: "1x1"}}
	if !twoFour {
		widths = []EnumValue{{Value: "20", Label: "20 MHz"}, {Value: "40", Label: "40 MHz"}, {Value: "80", Label: "80 MHz"}}
		defaultWidth = 80
		channelsDefault = []int{36, 40, 44, 48, 149, 153, 157, 161}
		powerDefault = map[string]any{"mode": "auto", "min": 12, "max": 20}
		antennas = []EnumValue{
			{Value: "default", Label: "Default"}, {Value: "4x4", Label: "4x4"}, {Value: "3x3", Label: "3x3"},
			{Value: "2x2", Label: "2x2"}, {Value: "1x1", Label: "1x1"},
		}
	}
	options := []Option{
		rfOption(Option{
			Key: prefix + "enabled", Label: label + " enabled", Help: "Warns when no enabled WLAN uses the band.",
			Control: "switch", Type: "bool", Default: true, Tier: TierBasic, Confidence: Confirmed,
			FirmwarePaths: []string{"Radios[].Disabled"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "width", Label: label + " width", Control: "select", Type: "int", Unit: "MHz", Enum: widths,
			Default: defaultWidth, Tier: TierBasic, Confidence: Confirmed,
			FirmwarePaths: []string{"Radios[].Width", "Radios[].RRM.Width"}, Catalogue: "radios-rf.md §8, §10",
		}),
		rfOption(Option{
			Key: prefix + "channels", Label: label + " channels for Auto", Help: "Planner input only; never sent to the AP.",
			Control: "chips", Type: "[]int", Default: channelsDefault, Tier: TierBasic, Confidence: Confirmed,
			Disruption: DisruptionNone, DismistOnly: true, Catalogue: "radios-rf.md §5; design §3.10",
		}),
		rfOption(Option{
			Key: prefix + "power", Label: label + " transmit power", Help: "Fixed 1 to 25 dBm, or an Auto range. Warns above 20 dBm.",
			Control: "segmented", Type: "object", Unit: "dBm", Default: powerDefault, Tier: TierBasic, Confidence: Likely,
			FirmwarePaths: []string{"Radios[].Power", "Radios[].RRM.Power"}, Catalogue: "radios-rf.md §8, §10",
		}),
		rfOption(Option{
			Key: prefix + "preamble", Label: label + " preamble", Control: "select", Type: "string", Default: "short",
			Enum: []EnumValue{{Value: "auto", Label: "Auto"}, {Value: "short", Label: "Short"}, {Value: "long", Label: "Long"}},
			Tier: TierAdvanced, Confidence: Confirmed,
			FirmwarePaths: []string{"Radios[].PreambleLength"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "taf", Label: label + " airtime fairness", Control: "select", Type: "string", Default: "default",
			Enum: []EnumValue{{Value: "default", Label: "Default"}, {Value: "off", Label: "Off"}, {Value: "on", Label: "On"}},
			Tier: TierAdvanced, Confidence: Confirmed, FirmwarePaths: []string{"Radios[].TAFMode"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "twt", Label: label + " target wake time (TWT)", Control: "switch", Type: "bool", Default: true,
			Tier: TierAdvanced, Confidence: Likely, FirmwarePaths: []string{"Radios[].TWTDisabled"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "antenna", Label: label + " antenna mode", Control: "select", Type: "string", Default: "default",
			Enum: antennas, Tier: TierAdvanced, Confidence: Likely,
			FirmwarePaths: []string{"Radios[].ChainSelectMode.RxChain", "Radios[].ChainSelectMode.TxChain"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "min_probe_rssi", Label: label + " minimum probe-response RSSI", Help: "Empty = off.",
			Control: "number", Type: "int", Unit: "dBm", Min: Int(-95), Max: Int(-50), Tier: TierAdvanced, Confidence: Likely,
			FirmwarePaths: []string{"Radios[].ProbeControl.NoProbeRespRSSI", "Radios[].ProbeControl.NoProbeRespRSSIThresh"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "mu_mimo", Label: label + " MU-MIMO", Control: "switch", Type: "bool",
			Tier: TierAdvanced, Confidence: NeedsLiveTest, FirmwarePaths: []string{"Radios[].MuMimo.Enabled"}, Catalogue: "radios-rf.md §8",
		}),
		rfOption(Option{
			Key: prefix + "txbf", Label: label + " transmit beamforming", Control: "switch", Type: "bool",
			Tier: TierAdvanced, Confidence: NeedsLiveTest, FirmwarePaths: []string{"Radios[].TxBf.Enabled"}, Catalogue: "radios-rf.md §8",
		}),
	}
	if !twoFour {
		options = append(options,
			rfOption(Option{
				Key: prefix + "dfs", Label: "Use DFS channels",
				Help:    "DFS channels need a 60 s radar check (CAC) when the radio comes up and may move on radar.",
				Control: "switch", Type: "bool", Default: false, Tier: TierBasic, Confidence: Likely,
				FirmwarePaths: []string{"Radios[].DFSjumplist"}, Catalogue: "radios-rf.md §5, §9",
			}),
			rfOption(Option{
				Key: prefix + "dfs_fallback", Label: "DFS fallback", Help: "Auto = the best-cost non-DFS channel at the same width.",
				Control: "select", Type: "int", Default: 0, Tier: TierAdvanced, Confidence: Confirmed,
				FirmwarePaths: []string{"Radios[].DFSjumplist.ChanSpecs"}, Catalogue: "radios-rf.md §9",
			}),
		)
	}
	return options
}

func apBandOptions(band, label string) []Option {
	prefix := "ap.rf." + band + "."
	widths := []EnumValue{{Value: "20", Label: "20 MHz"}}
	if band == "band_5" {
		widths = append(widths, EnumValue{Value: "40", Label: "40 MHz"}, EnumValue{Value: "80", Label: "80 MHz"})
	}
	ap := func(o Option) Option {
		o.Scope = ScopeAPRF
		return rfOption(o)
	}
	return []Option{
		ap(Option{
			Key: prefix + "enabled", Label: label + " enabled", Control: "segmented", Type: "bool", Tier: TierAdvanced, Confidence: Confirmed,
			FirmwarePaths: []string{"Radios[].Disabled"}, Catalogue: "radios-rf.md §9",
		}),
		ap(Option{
			Key: prefix + "channel", Label: label + " channel", Help: "Auto uses the channel plan; Static pins a channel on this AP.",
			Control: "select", Type: "int", Default: 0, Tier: TierAdvanced, Confidence: Confirmed,
			FirmwarePaths: []string{"Radios[].Channel", "Radios[].RRM.Channel", "Radios[].DFSjumplist"}, Catalogue: "radios-rf.md §4, §9",
		}),
		ap(Option{
			Key: prefix + "width", Label: label + " width", Control: "select", Type: "int", Unit: "MHz", Enum: widths,
			Tier: TierAdvanced, Confidence: Confirmed,
			FirmwarePaths: []string{"Radios[].Width", "Radios[].RRM.Width"}, Catalogue: "radios-rf.md §9",
		}),
		ap(Option{
			Key: prefix + "power_dbm", Label: label + " transmit power", Help: "1 to 25 dBm; warns above 20.",
			Control: "number", Type: "int", Unit: "dBm", Min: Int(1), Max: Int(25), Tier: TierAdvanced, Confidence: Likely,
			FirmwarePaths: []string{"Radios[].Power", "Radios[].RRM.Power"}, Catalogue: "radios-rf.md §9",
		}),
	}
}

var rfOptions = func() []Option {
	options := []Option{
		rfOption(Option{
			Key: "site.rf.managed", Label: "Manage RF",
			Help:    "When off, APs use the firmware defaults: 36/20 and 1/20, US/0 rules and a 0 dBm power target.",
			Control: "switch", Type: "bool", Default: true, LegacyDefault: "lifted", Tier: TierBasic, Confidence: Confirmed,
			Disruption: DisruptionAllRadios, FirmwarePaths: []string{"Radios"}, Catalogue: "radios-rf.md §7; design §3.10",
		}),
		rfOption(Option{
			Key: "site.rf.scanning_radio", Label: "Scanning radio", Help: "Turning it off removes planner input.",
			Control: "switch", Type: "bool", Default: true, Tier: TierAdvanced, Confidence: NeedsLiveTest,
			FirmwarePaths: []string{"Monitor.Enabled"}, Catalogue: "site-device.md §10",
		}),
	}
	options = append(options, bandOptions("band_24", "2.4 GHz")...)
	options = append(options, bandOptions("band_5", "5 GHz")...)
	options = append(options, apBandOptions("band_24", "2.4 GHz")...)
	options = append(options, apBandOptions("band_5", "5 GHz")...)
	return options
}()

// BandKey maps a firmware band ("2.4", "5") to its option key segment.
func BandKey(band string) string {
	if strings.HasPrefix(band, "2") {
		return "band_24"
	}
	return "band_5"
}
