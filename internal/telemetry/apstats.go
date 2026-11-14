package telemetry

// decodeAPStats decodes one stats.APStats message into update. Every
// section is decoded on its own: a malformed sub-message is skipped and
// counted under its section, and the rest of the frame still decodes. Only
// an APStats body that does not scan at all is counted under SectionFrame,
// and then nothing from it is used.
func decodeAPStats(raw []byte, update *Update) {
	fields, err := scan(raw)
	if err != nil {
		update.DecodeErrors[SectionFrame]++
		return
	}
	errs := update.DecodeErrors
	health := newReader(errs, SectionHealth)
	var h Health
	healthSeen := false
	var ip IP
	ipSeen := false
	var diag Diag
	diagSeen := false
	for _, item := range fields {
		update.Present.add(item.number)
		switch item.number {
		case 5:
			h.When, healthSeen = health.time(item), true
		case 18:
			// int32 seconds; a negative value is nonsense and reads as 0.
			if value := health.int64(item); value > 0 {
				update.UptimeSeconds = uint64(value)
			}
		case 19:
			update.Model = health.str(item, 64)
		case 20:
			update.Firmware = health.str(item, 96)
		case 21:
			h.Configured, healthSeen = health.bool(item), true
		case 23:
			h.IntervalSeconds, healthSeen = health.float64(item), true
		case 24:
			if decodeProc(health, item, &h, update) {
				healthSeen = true
			}
		case 25:
			if port, ok := decodePort(newReader(errs, SectionPort), item); ok {
				update.Ports = append(update.Ports, port)
			}
		case 26:
			if iface, ok := decodeInterface(newReader(errs, SectionInterface), item); ok && len(update.Interfaces) < 64 {
				update.Interfaces = append(update.Interfaces, iface)
			}
		case 28:
			if lease, ok := decodeDHCP(newReader(errs, SectionIP), item); ok && len(ip.DHCP) < 16 {
				ip.DHCP, ipSeen = append(ip.DHCP, lease), true
			}
		case 29:
			if route, ok := decodeRoute(newReader(errs, SectionIP), item); ok && len(ip.Routes) < 64 {
				ip.Routes, ipSeen = append(ip.Routes, route), true
			}
		case 31:
			if radio, ok := decodeRadio(newReader(errs, SectionRadio), item); ok && len(update.Radios) < 16 {
				update.Radios = append(update.Radios, radio)
			}
		case 32:
			if vap, ok := decodeVAP(newReader(errs, SectionWLAN), item); ok && len(update.VAPs) < 64 {
				update.VAPs = append(update.VAPs, vap)
			}
		case 33:
			if client, ok := decodeClient(newReader(errs, SectionClient), item); ok && len(update.Clients) < MaxClients {
				update.Clients = append(update.Clients, client)
			}
		case 36:
			if probe, ok := decodeProbe(newReader(errs, SectionProbe), item); ok && len(update.Probes) < 16 {
				update.Probes = append(update.Probes, probe)
			}
		case 38:
			if radio, ok := decodeBLE(newReader(errs, SectionDiag), item); ok && len(update.BLE) < 4 {
				update.BLE = append(update.BLE, radio)
			}
		case 48:
			if cloud, ok := decodeCloud(newReader(errs, SectionCloud), item); ok {
				h.Cloud, healthSeen = cloud, true
			}
		case 50:
			ipReader := newReader(errs, SectionIP)
			for _, address := range ipReader.addrs(item) {
				if len(ip.DNSServers) < 16 {
					ip.DNSServers, ipSeen = append(ip.DNSServers, address), true
				}
			}
		case 56:
			if neighbor, ok := decodeLLDP(newReader(errs, SectionLLDP), item); ok && len(update.LLDP) < 8 {
				update.LLDP = append(update.LLDP, neighbor)
			}
		case 57:
			if power, ok := decodeLLDPPower(newReader(errs, SectionLLDP), item); ok && len(update.LLDPPower) < 8 {
				update.LLDPPower = append(update.LLDPPower, power)
			}
		case 60:
			update.ConfigTimestamp = health.float64(item)
		case 63:
			if power, ok := decodePower(newReader(errs, SectionPower), item); ok {
				update.Power = power
			}
		case 69:
			if result, ok := decodeScan(newReader(errs, SectionScan), item); ok {
				update.Scan = result
			}
		case 70:
			h.Hostname, healthSeen = health.str(item, 64), true
		case 71:
			h.BootCount, healthSeen = health.uint(item), true
		case 73:
			if neighbor, ok := decodeNeighbor(newReader(errs, SectionIP), item); ok && len(ip.Neighbors) < 64 {
				ip.Neighbors, ipSeen = append(ip.Neighbors, neighbor), true
			}
		case 75:
			h.Country, healthSeen = health.str(item, 8), true
		case 79:
			update.InactiveVLANs = append(update.InactiveVLANs, decodeVLANs(newReader(errs, SectionVLANs), item)...)
		case 80:
			if decodeEnvironment(health, item, &h) {
				healthSeen = true
			}
		case 84:
			if agent, ok := decodeAgent(health, item); ok {
				h.Agent, healthSeen = agent, true
			}
		case 87:
			if lease, ok := decodeDHCP6(newReader(errs, SectionIP), item); ok && len(ip.DHCP6) < 16 {
				ip.DHCP6, ipSeen = append(ip.DHCP6, lease), true
			}
		case 93:
			h.CLMVersion, healthSeen = health.str(item, 64), true
		case 97:
			if counters, ok := decodeWxlan(newReader(errs, SectionDiag), item); ok && len(diag.Wxlan) < 32 {
				diag.Wxlan, diagSeen = append(diag.Wxlan, counters), true
			}
		case 103:
			if dispatch, ok := decodeDispatch(newReader(errs, SectionDiag), item); ok {
				diag.Dispatch, diagSeen = dispatch, true
			}
		case 106:
			if lacp, ok := decodeLACP(newReader(errs, SectionDiag), item); ok {
				diag.LACP, diagSeen = lacp, true
			}
		}
	}
	if healthSeen {
		update.Health = &h
	}
	if ipSeen {
		update.IP = &ip
	}
	if diagSeen {
		update.Diag = &diag
	}
	// Bands and roles come from the model's radio table (design §5.2),
	// cross-checked against the radios that carry VAPs.
	table := RadioTableFor(update.Model)
	ResolveRadios(table, update.Radios, update.VAPs)
	for index := range update.VAPs {
		update.VAPs[index].Band = table.BandOf(update.VAPs[index].RadioIndex, update.Radios)
	}
	JoinClients(update.Clients, update.Radios)
}

// MaxClients bounds the clients decoded from one frame.
const MaxClients = 512
