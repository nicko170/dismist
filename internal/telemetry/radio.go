package telemetry

import "strings"

// RadioRole is one radio of a model's radio table.
type RadioRole struct {
	Band string // Band24, Band5 or BandScan
	Role string // RoleAccess or RoleScan
}

// RadioTable maps radio indexes (rN) to their fixed band and role. The
// scan radio reports band code 0 whatever channel it sits on, so its role
// and band label come from here, never from 31.2 (design §5.2, §3.5).
type RadioTable struct {
	Model  string
	Radios map[int]RadioRole
}

// radioTables are the known models. AP33: r0 access 5 GHz, r1 access
// 2.4 GHz, r2 scan.
var radioTables = []RadioTable{
	{Model: "AP33", Radios: map[int]RadioRole{
		0: {Band: Band5, Role: RoleAccess},
		1: {Band: Band24, Role: RoleAccess},
		2: {Band: BandScan, Role: RoleScan},
	}},
}

// RadioTableFor returns the radio table of a model ("AP33-WW" matches
// "AP33"). An unknown or empty model returns a table with no entries:
// bands then come from the band code and a radio is a scan radio only
// when it is r2 or above and carries no VAP.
func RadioTableFor(model string) RadioTable {
	model = strings.ToUpper(strings.TrimSpace(model))
	for _, table := range radioTables {
		if model == table.Model || strings.HasPrefix(model, table.Model+"-") {
			return table
		}
	}
	return RadioTable{Model: model}
}

// Known reports whether the table describes a model.
func (t RadioTable) Known() bool { return len(t.Radios) != 0 }

// BandLabel maps a 31.2 band code to its label.
func BandLabel(code int) string {
	switch code {
	case 0:
		return Band24
	case 1:
		return Band5
	case 2:
		return Band6
	}
	return ""
}

// ResolveRadios sets each radio's Index, Band and Role from the table,
// cross-checked against the radios that carry VAPs (32.2): a radio that
// broadcasts a WLAN is always an access radio.
func ResolveRadios(table RadioTable, radios []RadioStats, vaps []VAP) {
	carries := make(map[int]bool, len(vaps))
	for _, vap := range vaps {
		carries[vap.RadioIndex] = true
	}
	for index := range radios {
		radio := &radios[index]
		radio.Index = radioIndex(radio.Dev)
		radio.Interface = radio.Dev
		known, inTable := table.Radios[radio.Index]
		switch {
		case inTable && (known.Role == RoleAccess || !carries[radio.Index]):
			radio.Band, radio.Role = known.Band, known.Role
		case inTable:
			// The table says scan but the radio carries a VAP: trust the
			// VAP and label it from its band code.
			radio.Band, radio.Role = BandLabel(radio.BandCode), RoleAccess
		case !table.Known() && radio.Index >= 2 && !carries[radio.Index] && len(vaps) != 0:
			radio.Band, radio.Role = BandScan, RoleScan
		default:
			radio.Band, radio.Role = BandLabel(radio.BandCode), RoleAccess
		}
	}
}

// BandOf returns the band label of radio rN, from the radios when present
// and otherwise from the table.
func (t RadioTable) BandOf(index int, radios []RadioStats) string {
	for _, radio := range radios {
		if radio.Index == index && radio.Dev != "" {
			return radio.Band
		}
	}
	if role, ok := t.Radios[index]; ok {
		return role.Band
	}
	return ""
}

// decodeRadio reads RadioStats (31).
func decodeRadio(r reader, f field) (RadioStats, bool) {
	fields, ok := r.message(f)
	if !ok {
		return RadioStats{}, false
	}
	var radio RadioStats
	for _, item := range fields {
		switch item.number {
		case 1:
			radio.Dev = r.str(item, 16)
		case 2:
			radio.BandCode = r.int(item)
		case 3:
			radio.Channel = r.int(item)
		case 4:
			radio.Secondary = r.int(item)
		case 5:
			radio.WidthMHz = min(r.uint(item), 320)
		case 6:
			radio.NoiseFloorDBm = int(clampDBm(r.zigzag(item)))
		case 7:
			radio.TxPowerDBm = int(clampDBm(r.zigzag(item)))
		case 8:
			radio.MaxTxPowerDBm = int(clampDBm(r.zigzag(item)))
		case 16:
			if breakdown, ok := decodeUtil64(r, item); ok {
				radio.Utilization = &breakdown
			}
		case 17:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				switch value.number {
				case 1:
					radio.DFS.RadarDetected = r.bool(value)
				case 2:
					radio.DFS.CACState = r.int(value)
				}
			}
		case 18:
			decodeRadioCounter(r, item, &radio.Counters)
		case 22:
			radio.Clients = r.int(item)
		case 23:
			radio.Missing = r.bool(item)
		case 26:
			radio.PHY = r.int(item)
		case 27:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				switch value.number {
				case 1, 2:
					radio.Counters.ProbeRequests += r.uint(value)
				case 5:
					radio.Counters.ProbeRequestsRand = r.uint(value)
				}
			}
		case 30:
			if util, ok := decodeChanUtil(r, item); ok {
				radio.ChanUtil = util
			}
		case 31:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				switch value.number {
				case 5:
					radio.Counters.Beacons = r.uint(value)
				case 16:
					radio.Counters.RxBadFCS = r.uint(value)
				case 17:
					radio.Counters.RxBadPLCP = r.uint(value)
				case 18:
					radio.Counters.RxGlitches = r.uint(value)
				}
			}
		case 38:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				switch value.number {
				case 1:
					radio.Counters.RxAuth = r.uint(value)
				case 3:
					radio.Counters.TxAuthFail = r.uint(value)
				case 4:
					radio.Counters.RxAssocReq = r.uint(value)
				case 6:
					radio.Counters.TxAssocRespFail = r.uint(value)
				case 7:
					radio.Counters.RxEAPOL = r.uint(value)
				case 8:
					radio.Counters.TxEAPOL = r.uint(value)
				case 9:
					radio.Counters.TxEAPOLFail = r.uint(value)
				}
			}
		}
	}
	if radio.Dev == "" {
		r.fail()
		return RadioStats{}, false
	}
	return radio, true
}

// decodeRadioCounter reads the radio Counter (31.18).
func decodeRadioCounter(r reader, f field, counters *RadioCounters) {
	fields, ok := r.message(f)
	if !ok {
		return
	}
	for _, item := range fields {
		switch item.number {
		case 1:
			counters.TxBytes = r.uint(item)
		case 2:
			counters.TxPackets = r.uint(item)
		case 3:
			counters.TxMgmt = r.uint(item)
		case 4:
			counters.TxErrors = r.uint(item)
		case 5:
			counters.RxBytes = r.uint(item)
		case 6:
			counters.RxPackets = r.uint(item)
		case 7:
			counters.RxMgmt = r.uint(item)
		case 8:
			counters.RxErrors = r.uint(item)
		case 22:
			counters.TxFailed = r.uint(item)
		case 23:
			counters.TxRetries = r.uint(item)
		case 24:
			counters.TxRetried = r.uint(item)
		case 25:
			counters.RxDups = r.uint(item)
		case 26:
			counters.RxRetried = r.uint(item)
		case 29:
			counters.ReInits = r.uint(item)
		case 46:
			counters.Down = r.uint(item)
		case 47:
			counters.Up = r.uint(item)
		case 48:
			counters.RxDecryptFailures = r.uint(item)
		case 53:
			counters.TxStalls = r.uint(item)
		}
	}
}

// decodeUtil64 reads the instantaneous Utilization (31.16): fixed64
// doubles 1 All to 7 UnDecodableWifi.
func decodeUtil64(r reader, f field) (UtilBreakdown, bool) {
	fields, ok := r.message(f)
	if !ok {
		return UtilBreakdown{}, false
	}
	var util UtilBreakdown
	for _, item := range fields {
		setUtil(&util, item.number, ratio(r.float64(item)))
	}
	return util, true
}

// decodeUtil32 reads UtilizationStats: fixed32 floats 1 All to 7
// UnDecodableWifi.
func decodeUtil32(r reader, f field) (UtilBreakdown, bool) {
	fields, ok := r.message(f)
	if !ok {
		return UtilBreakdown{}, false
	}
	var util UtilBreakdown
	for _, item := range fields {
		setUtil(&util, item.number, ratio(r.float32(item)))
	}
	return util, true
}

func setUtil(util *UtilBreakdown, number uint64, value float64) {
	switch number {
	case 1:
		util.All = value
	case 2:
		util.Tx = value
	case 3:
		util.RxInBSS = value
	case 4:
		util.RxOtherBSS = value
	case 5:
		util.UnknownWifi = value
	case 6:
		util.NonWifi = value
	case 7:
		util.UnDecodableWifi = value
	}
}

// ratio bounds a utilisation fraction to [0, 1].
func ratio(value float64) float64 { return max(0, min(value, 1)) }

// decodeChanUtil reads UtilizationResults (31.30).
func decodeChanUtil(r reader, f field) (*ChannelUtilization, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var util ChannelUtilization
	for _, item := range fields {
		switch item.number {
		case 1:
			util.Mean, _ = decodeUtil32(r, item)
		case 2:
			util.Min, _ = decodeUtil32(r, item)
		case 3:
			util.Max, _ = decodeUtil32(r, item)
		case 4:
			util.PeriodSec = r.int(item)
		case 5:
			util.Count = r.int(item)
		case 6:
			util.Version = r.int(item)
		case 8:
			util.ChanImMean = decodeChanIm(r, item)
		case 9:
			util.ChanImMin = decodeChanIm(r, item)
		case 10:
			util.ChanImMax = decodeChanIm(r, item)
		}
	}
	return &util, true
}

// decodeChanIm reads ChanImStats: integer percentages.
func decodeChanIm(r reader, f field) *ChanIm {
	fields, ok := r.message(f)
	if !ok {
		return nil
	}
	var im ChanIm
	for _, item := range fields {
		value := int(min(r.uint(item), 100))
		switch item.number {
		case 1:
			im.Tx = value
		case 2:
			im.RxInBSS = value
		case 3:
			im.RxOtherBSS = value
		case 4:
			im.UnknownWifi = value
		case 5:
			im.NonWifi = value
		case 6:
			im.Idle = value
		case 7:
			im.TxOp = value
		case 8:
			im.TxGood = value
		case 9:
			im.TxBad = value
		}
	}
	return &im
}

// decodeProbe reads ProbeStats (36).
func decodeProbe(r reader, f field) (Probe, bool) {
	fields, ok := r.message(f)
	if !ok {
		return Probe{}, false
	}
	var probe Probe
	for _, item := range fields {
		switch item.number {
		case 1:
			probe.Dev = r.str(item, 16)
		case 2:
			probe.BandCode = r.int(item)
		case 3:
			for _, channel := range r.varints(item) {
				if len(probe.Channels) < 64 {
					probe.Channels = append(probe.Channels, int(min(channel, 1<<16)))
				}
			}
		case 4:
			probe.WidthMHz = int(min(r.uint(item), 320))
		case 5:
			probe.RadioMAC = r.mac(item)
		case 6:
			probe.ProbeRequests = r.uint(item)
		case 7:
			probe.ProbeRequestsRandom = r.uint(item)
		case 8:
			probe.ProbeRequestsBcast = r.uint(item)
		case 9:
			probe.ProbeRequestsOffChann = r.uint(item)
		}
	}
	return probe, true
}

// clampDBm bounds a dBm value to a plausible range.
func clampDBm(value int64) int64 { return max(-200, min(value, 100)) }
