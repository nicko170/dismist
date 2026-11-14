package telemetry

import "sort"

// decodeScan reads WifiRadioScanResults (69): 7 Scan24GHz, 8 Scan5GHz,
// 14 Scan6GHz []ScanResults, 9 ScanChannelStats []WifiChannelStats,
// 10 Uptime, 11 ReportAll, 12 ChannelStatsVersion. Each band keeps at
// most MaxScanNeighbors neighbours, strongest first (design §5.10).
func decodeScan(r reader, f field) (*Scan, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var result Scan
	for _, item := range fields {
		switch item.number {
		case 7:
			if neighbor, ok := decodeScanResult(r, item); ok && len(result.Band24) < 4*MaxScanNeighbors {
				result.Band24 = append(result.Band24, neighbor)
			}
		case 8:
			if neighbor, ok := decodeScanResult(r, item); ok && len(result.Band5) < 4*MaxScanNeighbors {
				result.Band5 = append(result.Band5, neighbor)
			}
		case 14:
			if neighbor, ok := decodeScanResult(r, item); ok && len(result.Band6) < 4*MaxScanNeighbors {
				result.Band6 = append(result.Band6, neighbor)
			}
		case 9:
			if channel, ok := decodeScanChannel(r, item); ok && len(result.Channels) < 256 {
				result.Channels = append(result.Channels, channel)
			}
		case 10:
			result.Uptime = r.int(item)
		case 11:
			result.ReportAll = r.bool(item)
		case 12:
			result.StatsVersion = r.int(item)
		}
	}
	result.Band24 = strongest(result.Band24)
	result.Band5 = strongest(result.Band5)
	result.Band6 = strongest(result.Band6)
	return &result, true
}

// strongest sorts neighbours by RSSI, strongest first (then by BSSID), and
// keeps MaxScanNeighbors.
func strongest(neighbors []ScanNeighbor) []ScanNeighbor {
	sort.SliceStable(neighbors, func(i, j int) bool {
		if neighbors[i].RSSI != neighbors[j].RSSI {
			return neighbors[i].RSSI > neighbors[j].RSSI
		}
		return neighbors[i].BSSID < neighbors[j].BSSID
	})
	if len(neighbors) > MaxScanNeighbors {
		neighbors = neighbors[:MaxScanNeighbors]
	}
	return neighbors
}

// decodeScanResult reads ScanResults: 1 SSID, 2 BSSID, 3 RSSI (zigzag),
// 4 Channel ChannelSet {1 Primary, 2 Width, 3 Secondary}, 7 ListenChannel,
// 12 QBSS {1 IsValid, 2 StationCount, 3 ChannelUtilization,
// 4 AvailAdmissionCapacity}.
func decodeScanResult(r reader, f field) (ScanNeighbor, bool) {
	fields, ok := r.message(f)
	if !ok {
		return ScanNeighbor{}, false
	}
	var neighbor ScanNeighbor
	for _, item := range fields {
		switch item.number {
		case 1:
			neighbor.SSID = r.str(item, 32)
		case 2:
			neighbor.BSSID = r.mac(item)
		case 3:
			neighbor.RSSI = int(clampDBm(r.zigzag(item)))
		case 4:
			neighbor.Channel, neighbor.WidthMHz, neighbor.Secondary = decodeChannelSet(r, item)
		case 7:
			neighbor.ListenChannel = int(min(r.uint(item), 1<<16))
		case 12:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			var qbss QBSS
			valid := false
			for _, value := range sub {
				switch value.number {
				case 1:
					valid = r.bool(value)
				case 2:
					qbss.Stations = int(min(r.uint(value), 1<<16))
				case 3:
					qbss.Utilization = int(min(r.uint(value), 255))
				case 4:
					qbss.Capacity = int(min(r.uint(value), 1<<16))
				}
			}
			if valid {
				neighbor.QBSS = &qbss
			}
		}
	}
	if neighbor.BSSID == "" {
		r.fail()
		return ScanNeighbor{}, false
	}
	neighbor.Hidden = neighbor.SSID == ""
	return neighbor, true
}

// decodeChannelSet reads wifi.ChannelSet: 1 Primary, 2 Width, 3 Secondary.
func decodeChannelSet(r reader, f field) (primary, width, secondary int) {
	fields, ok := r.message(f)
	if !ok {
		return 0, 0, 0
	}
	for _, item := range fields {
		value := int(min(r.uint(item), 1<<16))
		switch item.number {
		case 1:
			primary = value
		case 2:
			width = value
		case 3:
			secondary = value
		}
	}
	return primary, width, secondary
}

// decodeScanChannel reads WifiChannelStats: 1 Channel, 7 Band,
// 2 NoiseFloor, 9 NoiseFloorMin, 10 NoiseFloorMax (zigzag), 4 Utilization
// (float32 fractions), 5 When.
func decodeScanChannel(r reader, f field) (ScanChannel, bool) {
	fields, ok := r.message(f)
	if !ok {
		return ScanChannel{}, false
	}
	var channel ScanChannel
	for _, item := range fields {
		switch item.number {
		case 1:
			channel.Channel, channel.WidthMHz, _ = decodeChannelSet(r, item)
		case 7:
			channel.BandCode = r.int(item)
		case 2:
			channel.NoiseDBm = int(clampDBm(r.zigzag(item)))
		case 9:
			channel.NoiseMinDBm = int(clampDBm(r.zigzag(item)))
		case 10:
			channel.NoiseMaxDBm = int(clampDBm(r.zigzag(item)))
		case 4:
			channel.Utilization, _ = decodeUtil32(r, item)
		case 5:
			channel.When = r.time(item)
		}
	}
	return channel, true
}
