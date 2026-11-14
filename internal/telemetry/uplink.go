package telemetry

// decodePort reads SwitchportStats (25).
func decodePort(r reader, f field) (Port, bool) {
	fields, ok := r.message(f)
	if !ok {
		return Port{}, false
	}
	var port Port
	for _, item := range fields {
		switch item.number {
		case 1:
			port.Name = r.str(item, 16)
		case 2:
			port.Up = r.bool(item)
		case 3:
			port.FullDuplex = r.bool(item)
		case 5:
			port.SpeedMbps = uint32(min(r.uint(item), 1<<20))
		case 6:
			port.RxBytes = r.uint(item)
		case 7:
			port.RxPackets = r.uint(item)
		case 8:
			port.TxBytes = r.uint(item)
		case 9:
			port.TxPackets = r.uint(item)
		case 10:
			port.PoweredDown = r.bool(item)
		case 16:
			port.Errors.RxPause = r.uint(item)
		case 17:
			port.Errors.RxErrors = r.uint(item)
		case 18:
			port.Errors.RxUndersize = r.uint(item)
		case 19:
			port.Errors.RxOversize = r.uint(item)
		case 20:
			port.Errors.RxJabber = r.uint(item)
		case 21:
			port.Errors.RxAlignment = r.uint(item)
		case 22:
			port.Errors.RxFCS = r.uint(item)
		case 23:
			port.Errors.RxFragment = r.uint(item)
		case 24:
			port.Errors.RxSymbol = r.uint(item)
		case 25:
			port.Errors.RxOverrun = r.uint(item)
		case 26:
			port.Errors.RxDiscards = r.uint(item)
		case 27:
			port.RxPeakBps = r.uint(item)
		case 28:
			port.TxPeakBps = r.uint(item)
		case 29:
			port.Errors.TxCollisions = r.uint(item)
		case 30:
			port.Errors.TxErrors = r.uint(item)
		case 31:
			port.Errors.TxExcessiveCollison = r.uint(item)
		case 32:
			port.Errors.TxUnderrun = r.uint(item)
		}
	}
	if port.Name == "" {
		r.fail()
		return Port{}, false
	}
	return port, true
}

// decodeLLDP reads LLDPneighbor (56). Descriptions are bounded to 256
// bytes and every other string to 64.
func decodeLLDP(r reader, f field) (LLDPNeighbor, bool) {
	fields, ok := r.message(f)
	if !ok {
		return LLDPNeighbor{}, false
	}
	var neighbor LLDPNeighbor
	for _, item := range fields {
		switch item.number {
		case 1:
			neighbor.PortDesc = r.str(item, 256)
		case 2:
			neighbor.SystemName = r.str(item, 64)
		case 3:
			neighbor.SystemDesc = r.str(item, 256)
		case 4:
			neighbor.MgmtAddress = r.str(item, 64)
		case 5:
			neighbor.PortID = r.str(item, 64)
		case 6:
			neighbor.AutonegSup = r.str(item, 64)
		case 7:
			neighbor.AutonegAdv = r.str(item, 64)
		case 8:
			neighbor.MTU = int(min(r.uint(item), 1<<16))
		case 9:
			neighbor.NativeVLAN = int(min(r.uint(item), 4095))
		case 10:
			sub, ok := r.message(item)
			if !ok || len(neighbor.VLANs) >= 64 {
				continue
			}
			var vlan LLDPVLAN
			for _, value := range sub {
				switch value.number {
				case 1:
					vlan.ID = int(min(r.uint(value), 4095))
				case 2:
					vlan.Name = r.str(value, 64)
				}
			}
			neighbor.VLANs = append(neighbor.VLANs, vlan)
		case 13:
			neighbor.SoftwareRev = r.str(item, 64)
		case 14:
			neighbor.Serial = r.str(item, 64)
		case 15:
			neighbor.Vendor = r.str(item, 64)
		case 16:
			neighbor.Model = r.str(item, 64)
		case 18:
			neighbor.ChassisID = r.str(item, 64)
		case 19:
			if address := r.str(item, 64); address != "" && len(neighbor.MgmtAddrs) < 8 {
				neighbor.MgmtAddrs = append(neighbor.MgmtAddrs, address)
			}
		case 20:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				if value.number == 1 && neighbor.Serial == "" {
					neighbor.Serial = r.str(value, 64)
				}
			}
		case 21:
			neighbor.APPort = r.str(item, 16)
		}
	}
	if neighbor.MgmtAddress == "" && len(neighbor.MgmtAddrs) != 0 {
		neighbor.MgmtAddress = neighbor.MgmtAddrs[0]
	}
	return neighbor, true
}

// decodeLLDPPower reads LLDPpwrStats (57): 1 Req_cnt, 2 Xtnd_sup,
// 3 Pse_avail, 4 Pd_req, 5 Pd_needs (mW), 6 AP_Port_name (0.15). 0.8 and
// 0.14 send one entry, 0.15 one per port; both arrive as repeated 57.
func decodeLLDPPower(r reader, f field) (LLDPPower, bool) {
	fields, ok := r.message(f)
	if !ok {
		return LLDPPower{}, false
	}
	var power LLDPPower
	for _, item := range fields {
		switch item.number {
		case 1:
			power.Requests = uint32(min(r.uint(item), 1<<31))
		case 2:
			power.Extended = r.bool(item)
		case 3:
			power.AllocatedMW = uint32(min(r.uint(item), 1<<20))
		case 4:
			power.RequestedMW = uint32(min(r.uint(item), 1<<20))
		case 5:
			power.NeededMW = uint32(min(r.uint(item), 1<<20))
		case 6:
			power.APPort = r.str(item, 16)
		}
	}
	return power, true
}

// decodePower reads PMGRStats (63): 1 Pwr_src, 2 Pwr_avail, 3 Pwr_needed,
// 5 Pwr_budget (zigzag), 6 ConstrainedMode, 7 PwrSrcs, 8 PwrMeasuredDraw.
func decodePower(r reader, f field) (*Power, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var power Power
	for _, item := range fields {
		switch item.number {
		case 1:
			power.Source = r.str(item, 32)
		case 2:
			power.AvailableMW = uint32(min(r.uint(item), 1<<20))
		case 3:
			power.NeededMW = uint32(min(r.uint(item), 1<<20))
		case 5:
			power.BudgetMW = int32(max(-1<<20, min(r.zigzag(item), 1<<20)))
		case 6:
			power.ConstrainedMode = r.str(item, 32)
		case 7:
			if source := r.str(item, 32); source != "" && len(power.Sources) < 8 {
				power.Sources = append(power.Sources, source)
			}
		case 8:
			power.MeasuredDrawMW = uint32(min(r.uint(item), 1<<20))
		}
	}
	return &power, true
}
