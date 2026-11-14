package telemetry

// Phase-4 diagnostics (design §5.2): 38 BTRadios, 97 WxlanPkts,
// 103 PaceDispatchPkt and 106 LACP. The kernel log (24.1) is read with
// Proc in health.go.

// decodeBLE reads BTRadioStats (38): 1 BLE BLEStats.
func decodeBLE(r reader, f field) (BLERadio, bool) {
	fields, ok := r.message(f)
	if !ok {
		return BLERadio{}, false
	}
	var radio BLERadio
	found := false
	for _, item := range fields {
		if item.number != 1 {
			continue
		}
		stats, ok := r.message(item)
		if !ok {
			continue
		}
		found = true
		for _, value := range stats {
			switch value.number {
			case 1:
				// ethernet.MAC; the AP sends it byte-reversed on some
				// firmware, so it is shown as reported.
				radio.MAC = r.mac(value)
			case 3:
				radio.TxBytes = r.uint(value)
			case 4:
				radio.TxCommands = r.uint(value)
			case 5:
				radio.RxBytes = r.uint(value)
			case 6:
				radio.RxEvents = r.uint(value)
			case 7:
				radio.TxAdverts = r.uint(value)
			case 9:
				radio.Scans = r.uint(value)
			case 10:
				radio.RxScanPkts = r.uint(value)
			case 13:
				radio.RxAdverts = r.uint(value)
			case 16:
				radio.TxErrors = r.uint(value)
			case 17:
				radio.RxErrors = r.uint(value)
			case 18:
				radio.ResetsHung = r.uint(value)
			case 19:
				radio.ResetsHungRx = r.uint(value)
			}
		}
	}
	return radio, found
}

// decodeWxlan reads WxlanStats (97).
func decodeWxlan(r reader, f field) (WxlanCounters, bool) {
	fields, ok := r.message(f)
	if !ok {
		return WxlanCounters{}, false
	}
	var counters WxlanCounters
	for _, item := range fields {
		switch item.number {
		case 1:
			counters.Dev = r.str(item, 32)
		case 2:
			counters.RxPackets = r.uint(item)
		case 3:
			counters.TxPackets = r.uint(item)
		case 4:
			counters.ARPRx = r.uint(item)
		case 5:
			counters.ARPReply = r.uint(item)
		case 6:
			counters.ARPDrop = r.uint(item)
		case 7:
			counters.BCMCDrop = r.uint(item)
		case 8:
			counters.PktDrop = r.uint(item)
		}
	}
	return counters, counters.Dev != ""
}

// dispatchNames are the PaceDispatchPktStats (103) counters by field.
var dispatchNames = map[uint64]string{
	1: "http_get", 2: "dot11_rd_event", 3: "eapol", 4: "dhcp", 5: "dhcpv6", 6: "dns",
	7: "mdns", 8: "ignored", 9: "dot11_rd_event_lag", 10: "dot11_rd_event_lag_last_sec",
	11: "igmp", 12: "http_get_short_frame",
}

// decodeDispatch reads PaceDispatchPktStats (103).
func decodeDispatch(r reader, f field) (map[string]uint64, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	result := make(map[string]uint64)
	for _, item := range fields {
		if name, known := dispatchNames[item.number]; known {
			if value := r.int64(item); value > 0 {
				result[name] = uint64(value)
			}
		}
	}
	return result, true
}

// decodeLACP reads LACPStats (106): 1 FailCount, 2 ActivePorts.
func decodeLACP(r reader, f field) (*LACP, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var lacp LACP
	for _, item := range fields {
		switch item.number {
		case 1:
			lacp.FailCount = r.uint(item)
		case 2:
			lacp.ActivePorts = r.uint(item)
		}
	}
	return &lacp, true
}
