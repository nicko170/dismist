package telemetry_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/nicko170/dismist/internal/telemetry"
	"github.com/nicko170/dismist/internal/telemetry/pbtest"
	"github.com/nicko170/dismist/tools/fakeap"
)

const apID = "02-aa-00-00-00-01"

func decodeStats(t *testing.T, topic string, stats *pbtest.Message) telemetry.Update {
	t.Helper()
	update, err := telemetry.Decode(pbtest.Frame(topic, apID, stats))
	if err != nil {
		t.Fatal(err)
	}
	return update
}

// TestDecodeEverySection decodes a fakeap full frame that carries every
// section dismist reads and checks one value from each.
func TestDecodeEverySection(t *testing.T) {
	snapshot := fakeap.DefaultSnapshot(2)
	snapshot.Inactive = []uint64{20}
	snapshot.ConfigTimestamp = 1794475905.25
	update, err := telemetry.Decode(pbtest.Gzip(pbtest.Frame(telemetry.TopicStatsFull, apID,
		snapshot.Stats(apID, "AP33-WW", "apfw-0.15.35064-noro-84df", true))))
	if err != nil {
		t.Fatal(err)
	}
	if len(update.DecodeErrors) != 0 {
		t.Fatalf("decode errors %v", update.DecodeErrors)
	}
	health := update.Health
	switch {
	case update.Topic != telemetry.TopicStatsFull || update.APID != apID || update.Model != "AP33-WW" || update.UptimeSeconds != 120:
		t.Fatalf("envelope %+v", update)
	case update.ConfigTimestamp != 1794475905.25:
		t.Fatalf("config timestamp %v", update.ConfigTimestamp)
	case health == nil || health.When.IsZero() || health.IntervalSeconds != 60 || !health.Configured || health.BootCount != 7 ||
		health.Country != "AU" || health.Hostname != "fake-ap" || health.CLMVersion != "clm_fake_1":
		t.Fatalf("health %+v", health)
	case health.Memory == nil || health.Memory.TotalKB != 1031800 || health.Load == nil || health.Load.Average[0] != 0.5 ||
		health.Load.Total != 100 || health.RW == nil || health.RW.AvailableKB != 7000 || health.CPU == nil ||
		health.CPU.Idle != 60000 || len(health.CPUs) != 1:
		t.Fatalf("proc %+v %+v %+v %+v", health.Memory, health.Load, health.RW, health.CPU)
	case health.Temperature == nil || health.Temperature.CPU == nil || math.Abs(*health.Temperature.CPU-52.5) > 0.01 ||
		health.Accel == nil || health.Agent == nil || health.Agent.Goroutines != 150:
		t.Fatalf("environment %+v %+v", health.Temperature, health.Agent)
	case health.Cloud == nil || health.Cloud.TCPRTTSeconds == nil || health.Cloud.PMTU != 1500 || health.Cloud.SendMSS != 1448:
		t.Fatalf("cloud %+v", health.Cloud)
	}
	if len(update.KMsg) != 1 || !strings.Contains(update.KMsg[0], "synthetic") {
		t.Fatalf("kmsg %v", update.KMsg)
	}
	if len(update.Radios) != 3 {
		t.Fatalf("radios %+v", update.Radios)
	}
	r0, r1, r2 := update.Radios[0], update.Radios[1], update.Radios[2]
	if r0.Band != telemetry.Band5 || r0.Role != telemetry.RoleAccess || r0.Channel != 36 || r0.WidthMHz != 80 ||
		r0.NoiseFloorDBm != -95 || r0.MaxTxPowerDBm != 10 || r0.Clients != 2 || r0.PHY != 9 || r0.DFS.CACState != 2 ||
		r0.ChanUtil == nil || math.Abs(r0.ChanUtil.Mean.All-0.05) > 0.001 || r0.Counters.Beacons != 1000 || r0.Interface != "r0" {
		t.Fatalf("r0 %+v", r0)
	}
	if r1.Band != telemetry.Band24 || r1.Role != telemetry.RoleAccess || r1.Channel != 11 {
		t.Fatalf("r1 %+v", r1)
	}
	if r2.Band != telemetry.BandScan || r2.Role != telemetry.RoleScan || r2.BandCode != 0 || r2.Channel != 165 {
		t.Fatalf("the scan radio must be labelled from the radio table, not its band code: %+v", r2)
	}
	if len(update.Probes) != 2 || update.Probes[0].Dev != "r0" || update.Probes[0].Channels[0] != 36 || update.Probes[1].ProbeRequests != 100 {
		t.Fatalf("probes (field 36) %+v", update.Probes)
	}
	if len(update.VAPs) != 2 || update.VAPs[0].Interface != "wl0.1" || update.VAPs[0].Band != telemetry.Band5 ||
		update.VAPs[0].WLANID != "00000000-0000-4000-8000-000000000001" || update.VAPs[1].Band != telemetry.Band24 ||
		update.VAPs[0].Radio != "r0" || update.VAPs[0].Clients != 2 {
		t.Fatalf("vaps %+v", update.VAPs)
	}
	if len(update.Clients) != 2 {
		t.Fatalf("clients %+v", update.Clients)
	}
	client := update.Clients[0]
	if client.MAC != "02:aa:00:00:20:01" || client.Name != "client-1" || client.TxMbps != 866 || client.Radio != "r0" ||
		client.Band != telemetry.Band5 || client.Channel != 36 || client.WidthMHz != 80 || client.SNR == nil || *client.SNR != 45 ||
		client.WLANID != "00000000-0000-4000-8000-000000000001" || len(client.PerAntennaRSSI) != 2 || client.Streams != 2 ||
		client.Auth != "WPA2-PSK" || client.IP != "192.0.2.100" {
		t.Fatalf("client %+v", client)
	}
	if len(update.Ports) != 2 || !update.Ports[0].Up || update.Ports[0].SpeedMbps != 1000 || update.Ports[0].RxBytes != 1_000_000 || update.Ports[1].Name != "eth1" {
		t.Fatalf("ports %+v", update.Ports)
	}
	if len(update.LLDP) != 1 || update.LLDP[0].SystemName != "example-switch" || update.LLDP[0].NativeVLAN != 1 ||
		len(update.LLDP[0].VLANs) != 2 || update.LLDP[0].APPort != "eth0" {
		t.Fatalf("lldp %+v", update.LLDP)
	}
	if len(update.LLDPPower) != 1 || update.LLDPPower[0].AllocatedMW != 19500 || update.LLDPPower[0].APPort != "eth0" {
		t.Fatalf("lldp power %+v", update.LLDPPower)
	}
	if update.Power == nil || update.Power.Source != "LLDP" || update.Power.AvailableMW != 19500 || update.Power.Constrained() {
		t.Fatalf("power %+v", update.Power)
	}
	ip := update.IP
	if ip == nil || len(ip.DHCP) != 1 || ip.DHCP[0].Address != "192.0.2.10" || ip.DHCP[0].Prefix != 24 ||
		ip.DHCP[0].Gateway != "192.0.2.1" || ip.DHCP[0].DNS[0] != "192.0.2.1" || ip.DHCP[0].LeaseSeconds != 86400 ||
		len(ip.DNSServers) != 1 || len(ip.Neighbors) != 1 {
		t.Fatalf("ip %+v", ip)
	}
	if gateway, mac := ip.DefaultGateway(); gateway != "192.0.2.1" || mac != "02:aa:00:00:fe:01" {
		t.Fatalf("default gateway %s %s", gateway, mac)
	}
	if len(update.Interfaces) != 1 || update.Interfaces[0].VLAN != 1 || update.Interfaces[0].Addresses[0] != "192.0.2.10/24" {
		t.Fatalf("interfaces %+v", update.Interfaces)
	}
	if len(update.InactiveVLANs) != 1 || update.InactiveVLANs[0] != 20 {
		t.Fatalf("inactive vlans %v", update.InactiveVLANs)
	}
	scan := update.Scan
	if scan == nil || len(scan.Band24) != 1 || scan.Band24[0].SSID != "Neighbour" || scan.Band24[0].RSSI != -70 ||
		scan.Band24[0].Channel != 6 || scan.Band24[0].QBSS == nil || scan.Band24[0].QBSS.Utilization != 40 ||
		len(scan.Band5) != 1 || !scan.Band5[0].Hidden || scan.Band5[0].WidthMHz != 80 || len(scan.Channels) != 2 ||
		scan.Channels[1].BandCode != 1 || scan.Channels[0].NoiseDBm != -93 {
		t.Fatalf("scan %+v", scan)
	}
	if len(update.BLE) != 1 || update.BLE[0].TxAdverts != 100 || update.Diag == nil || len(update.Diag.Wxlan) != 1 ||
		update.Diag.Dispatch["dhcp"] != 10 || update.Diag.LACP == nil || update.Diag.LACP.ActivePorts != 1 {
		t.Fatalf("diag %+v %+v", update.BLE, update.Diag)
	}
	for _, number := range []uint64{5, 18, 24, 31, 32, 33, 36, 69, 106} {
		if !update.Present.Has(number) {
			t.Fatalf("field %d not marked present", number)
		}
	}
}

// TestMalformedSectionsNeverDropTheFrame breaks one radio sub-message and
// mistypes fields in others: each is skipped and counted, and the VAPs and
// clients still decode (design §5.1).
func TestMalformedSectionsNeverDropTheFrame(t *testing.T) {
	vap := pbtest.New().Raw(3, []byte{2, 0xaa, 0, 0, 0, 4}).String(4, "wl0.1").String(5, "Example")
	client := pbtest.New().Raw(1, []byte{2, 0xaa, 0, 0, 0, 5}).Zigzag(8, -60)
	stats := pbtest.New().
		Raw(31, []byte{0xff, 0xff}).                    // not a message
		Message(31, pbtest.New().Raw(1, []byte{0x80})). // Dev with an invalid byte
		Message(31, pbtest.New().String(1, "r1").String(3, "eleven")).
		Message(36, pbtest.New().String(1, "r0").Raw(5, []byte{1, 2, 3})). // radio MAC of 3 bytes
		Raw(48, []byte{0x0a}).
		Message(32, vap).
		Message(33, client).
		Message(33, pbtest.New().Raw(1, []byte{1, 2})). // client MAC of 2 bytes: dropped
		Varint(56, 7)                                   // LLDP as a varint
	update := decodeStats(t, telemetry.TopicStatsFull, stats)
	if len(update.VAPs) != 1 || len(update.Clients) != 1 || update.Clients[0].RSSI != -60 {
		t.Fatalf("VAPs and clients were dropped: %+v", update)
	}
	if update.DecodeErrors[telemetry.SectionRadio] < 2 || update.DecodeErrors[telemetry.SectionProbe] != 1 ||
		update.DecodeErrors[telemetry.SectionCloud] != 1 || update.DecodeErrors[telemetry.SectionClient] < 1 ||
		update.DecodeErrors[telemetry.SectionLLDP] != 1 {
		t.Fatalf("decode errors %v", update.DecodeErrors)
	}
	if len(update.Radios) != 1 || update.Radios[0].Dev != "r1" || update.Radios[0].Channel != 0 {
		t.Fatalf("a radio without a name is dropped; a mistyped channel reads as zero and the radio stays: %+v", update.Radios)
	}
	for _, section := range []string{telemetry.SectionRadio, telemetry.SectionClient} {
		found := false
		for _, known := range telemetry.DecodeSections() {
			found = found || known == section
		}
		if !found {
			t.Fatalf("section %s is not in the fixed list", section)
		}
	}
}

func TestZigzagPackedFloatAndCloudLayouts(t *testing.T) {
	legacy := decodeStats(t, telemetry.TopicStatsFast, pbtest.New().Message(48, pbtest.New().Float(1, 0.02).Varint(3, 1400)))
	rtt, socket, ok := legacy.Health.Cloud.RTT()
	if !ok || socket != "tcp" || math.Abs(rtt-0.02) > 1e-6 || legacy.Health.Cloud.PMTU != 1400 {
		t.Fatalf("0.14 CloudStats %+v", legacy.Health.Cloud)
	}
	current := decodeStats(t, telemetry.TopicStatsFast, pbtest.New().Message(48, pbtest.New().Float(6, 0.04).Float(8, 0.008)))
	if rtt, socket, ok := current.Health.Cloud.RTT(); !ok || socket != "tcp" || math.Abs(rtt-0.008) > 1e-6 {
		t.Fatalf("0.15 CloudStats %+v", current.Health.Cloud)
	}
	websocketOnly := decodeStats(t, telemetry.TopicStatsFast, pbtest.New().Message(48, pbtest.New().Float(6, 0.04)))
	if _, socket, _ := websocketOnly.Health.Cloud.RTT(); socket != "websocket" {
		t.Fatalf("websocket RTT %+v", websocketOnly.Health.Cloud)
	}
	load := decodeStats(t, telemetry.TopicStatsFull, pbtest.New().Message(24, pbtest.New().Message(4,
		pbtest.New().PackedFloat(1, 1.5, 2.5, 3.5))))
	if load.Health.Load.Average != [3]float64{1.5, 2.5, 3.5} {
		t.Fatalf("load %+v", load.Health.Load)
	}
	power := decodeStats(t, telemetry.TopicStatsFull, pbtest.New().
		Message(63, pbtest.New().String(1, "LLDP").Zigzag(5, -2500).String(6, "Limited")).
		Message(57, pbtest.New().Varint(3, 15400)). // 0.14: one entry, no port
		Message(31, pbtest.New().String(1, "r0").Varint(2, 1).Zigzag(6, -96).Zigzag(8, 17)))
	if power.Power.BudgetMW != -2500 || !power.Power.Constrained() || len(power.LLDPPower) != 1 ||
		power.LLDPPower[0].AllocatedMW != 15400 || power.Radios[0].NoiseFloorDBm != -96 || power.Radios[0].MaxTxPowerDBm != 17 {
		t.Fatalf("zigzag fields %+v %+v %+v", power.Power, power.LLDPPower, power.Radios)
	}
	repeated := decodeStats(t, telemetry.TopicStatsFull, pbtest.New().
		Message(57, pbtest.New().Varint(3, 19500).String(6, "eth0")).
		Message(57, pbtest.New().Varint(3, 0).String(6, "eth1")))
	if len(repeated.LLDPPower) != 2 || repeated.LLDPPower[1].APPort != "eth1" {
		t.Fatalf("0.15 repeated LLDP power %+v", repeated.LLDPPower)
	}
	vlans := decodeStats(t, telemetry.TopicStatsFull, pbtest.New().Message(79, pbtest.New().Varint(1, 30).Varint(1, 40)))
	if len(vlans.InactiveVLANs) != 2 || vlans.InactiveVLANs[1] != 40 {
		t.Fatalf("unpacked inactive VLANs %v", vlans.InactiveVLANs)
	}
}

// TestClientMergeCarriesFullOnlyFields: a fast frame keeps the link rates,
// hostname and user agent of the last full frame; a full frame that omits
// a field means zero (design §5.3).
func TestClientMergeCarriesFullOnlyFields(t *testing.T) {
	snapshot := fakeap.DefaultSnapshot(1)
	snapshot.Clients[0].UserAgent = "Example/1.0"
	full := decodeStats(t, telemetry.TopicStatsFull, snapshot.Stats(apID, "AP33-WW", "fw", true))
	merged := telemetry.MergeClients(nil, full.Clients, false)
	snapshot.Clients[0].RSSI = -70
	snapshot.Clients[0].TxBytes = 9999
	fast := decodeStats(t, telemetry.TopicStatsFast, snapshot.Stats(apID, "AP33-WW", "fw", false))
	if fast.Clients[0].TxMbps != 0 || fast.Clients[0].Name != "" {
		t.Fatalf("fast frames carry no rates or hostname: %+v", fast.Clients[0])
	}
	merged = telemetry.MergeClients(merged, fast.Clients, true)
	client := merged["02:aa:00:00:20:01"]
	if client.TxMbps != 866 || client.RxMbps != 720 || client.Name != "client-1" || client.UserAgent != "Example/1.0" ||
		client.RSSI != -70 || client.TxBytes != 9999 {
		t.Fatalf("fast merge %+v", client)
	}
	stripped := pbtest.New().Message(33, pbtest.New().Raw(1, []byte{2, 0xaa, 0, 0, 0x20, 1}).Varint(3, 1))
	fullAgain := decodeStats(t, telemetry.TopicStatsFull, stripped)
	merged = telemetry.MergeClients(merged, fullAgain.Clients, false)
	if merged["02:aa:00:00:20:01"].TxMbps != 0 || merged["02:aa:00:00:20:01"].Name != "" {
		t.Fatalf("a full frame without the rates means zero: %+v", merged["02:aa:00:00:20:01"])
	}
	if gone := telemetry.MergeClients(merged, nil, true); len(gone) != 0 {
		t.Fatal("a client missing from the newest frame is gone")
	}
}

// TestClientRadioJoinIgnoresScanRadioBand: a 2.4 GHz client on r1 joins
// r1 even though the scan radio r2 also reports band 0.
func TestClientRadioJoinIgnoresScanRadioBand(t *testing.T) {
	stats := pbtest.New().String(19, "AP33-WW").
		Message(31, pbtest.New().String(1, "r1").Varint(3, 6).Varint(5, 20).Zigzag(6, -90)).
		Message(31, pbtest.New().String(1, "r2").Varint(3, 165).Zigzag(6, -80)).
		Message(32, pbtest.New().Varint(2, 1).Raw(3, []byte{2, 0xaa, 0, 0, 0, 9}).String(4, "wl1.1").String(5, "Example")).
		Message(33, pbtest.New().Raw(1, []byte{2, 0xaa, 0, 0, 0, 7}).Varint(2, 1).Zigzag(8, -60)).
		Message(33, pbtest.New().Raw(1, []byte{2, 0xaa, 0, 0, 0, 8}).Varint(2, 2).Zigzag(8, -60))
	update := decodeStats(t, telemetry.TopicStatsFull, stats)
	onR1, onScan := update.Clients[0], update.Clients[1]
	if onR1.Radio != "r1" || onR1.Band != telemetry.Band24 || onR1.Channel != 6 || onR1.SNR == nil || *onR1.SNR != 30 {
		t.Fatalf("client on r1 %+v", onR1)
	}
	if onScan.Channel != 0 || onScan.SNR != nil {
		t.Fatalf("the scan radio is excluded from client joins: %+v", onScan)
	}
	if update.Radios[1].Role != telemetry.RoleScan || update.VAPs[0].Band != telemetry.Band24 {
		t.Fatalf("roles %+v %+v", update.Radios, update.VAPs)
	}
	unknown := decodeStats(t, telemetry.TopicStatsFull, pbtest.New().String(19, "AP99").
		Message(31, pbtest.New().String(1, "r0").Varint(2, 1)).
		Message(31, pbtest.New().String(1, "r2")).
		Message(32, pbtest.New().Raw(3, []byte{2, 0xaa, 0, 0, 0, 9}).String(4, "wl0.1")))
	if unknown.Radios[0].Band != telemetry.Band5 || unknown.Radios[1].Role != telemetry.RoleScan {
		t.Fatalf("unknown model roles %+v", unknown.Radios)
	}
}

func TestScanKeepsTheStrongestNeighbours(t *testing.T) {
	scan := pbtest.New()
	for index := range 250 {
		scan.Message(7, pbtest.New().Raw(2, []byte{2, 0xbb, 0, 0, byte(index >> 8), byte(index)}).Zigzag(3, int64(-40-index%60)))
	}
	update := decodeStats(t, telemetry.TopicStatsFull, pbtest.New().Message(69, scan))
	if len(update.Scan.Band24) != telemetry.MaxScanNeighbors || update.Scan.Band24[0].RSSI != -40 ||
		update.Scan.Band24[len(update.Scan.Band24)-1].RSSI < -99 {
		t.Fatalf("scan bound and order: %d %+v", len(update.Scan.Band24), update.Scan.Band24[0])
	}
}

func TestTypedPaceEvents(t *testing.T) {
	decode := func(body string) telemetry.Event {
		t.Helper()
		update, err := telemetry.Decode([]byte(body))
		if err != nil || update.Event == nil {
			t.Fatalf("%s: %+v %v", body, update, err)
		}
		return *update.Event
	}
	dhcp := decode(`{"Topics":["ep-pace-event-","client-pace-event-"],"ID":"0E4A2B90-1d2c-11ef-8000-0242ac120002","IsUpdate":true,"Type":1,"Title":"DHCP failure","When":"2026-11-14T03:00:00Z","impacted_client_mac":"02-aa-00-00-00-02","VLAN":20,"NumAttempts":3,"NACKed":false,"PcapFile":"AAAA","WLAN":"00000000-0000-4000-8000-000000000001"}`)
	var data map[string]any
	_ = json.Unmarshal(dhcp.Data, &data)
	if dhcp.Kind != telemetry.KindDHCPFailure || dhcp.Severity != telemetry.SeverityWarning || !dhcp.IsUpdate ||
		dhcp.ID != "0e4a2b90-1d2c-11ef-8000-0242ac120002" || dhcp.ClientMAC != "02:aa:00:00:00:02" ||
		data["vlan"] != float64(20) || data["attempts"] != float64(3) || data["PcapFile"] != nil ||
		dhcp.WLANID != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("dhcp event %+v %v", dhcp, data)
	}
	dns := decode(`{"Topic":"ep-pace-event-","Type":3,"Title":"Succeeding DNS query","ServerInformation":{"IP":"192.0.2.1","Failing":false},"Corrects":["0e4a2b90-1d2c-11ef-8000-0242ac120002","bogus"]}`)
	if dns.Kind != telemetry.KindDNS || dns.Severity != telemetry.SeverityInfo || len(dns.Corrects) != 1 {
		t.Fatalf("dns event %+v", dns)
	}
	failing := decode(`{"Topic":"ep-pace-event-","Type":3,"Title":"Failing DNS query","ServerInformation":{"Failing":true}}`)
	if failing.Severity != telemetry.SeverityWarning {
		t.Fatalf("failing dns %+v", failing)
	}
	noise := decode(`{"Topic":"ep-pace-event-","Type":42,"Title":"PCAP File","Text":"No packets."}`)
	if !noise.Noise || noise.Kind != telemetry.KindPCAP {
		t.Fatalf("pcap noise %+v", noise)
	}
	down := decode(`{"Topic":"ep-pace-event-","Type":109,"Title":"Port DOWN","Port":"eth1"}`)
	if down.Kind != telemetry.KindPort || down.Severity != telemetry.SeverityWarning || !strings.Contains(string(down.Data), "eth1") {
		t.Fatalf("port event %+v", down)
	}
	other := decode(`{"Topic":"ep-pace-event-","Type":77,"Title":"Something","Password":"secret","Huge":"` + strings.Repeat("x", 5000) + `","Small":1}`)
	if other.Kind != telemetry.KindPace || strings.Contains(string(other.Data), "secret") || len(other.Data) > telemetry.MaxEventData ||
		!strings.Contains(string(other.Data), `"truncated":true`) || !strings.Contains(string(other.Data), `"Small":1`) {
		t.Fatalf("raw fallback %s", other.Data)
	}
	mistyped := decode(`{"Topic":"ep-pace-event-","Type":6,"Title":7,"Reason_code":8}`)
	if mistyped.Kind != telemetry.KindDisassociated || mistyped.Title != "" {
		t.Fatalf("mistyped title %+v", mistyped)
	}
}

func TestAppliedAndCommandOutput(t *testing.T) {
	update, err := telemetry.Decode([]byte(`{"Topic":"ep-config-applied-","HashKey":"02-aa-00-00-00-01","ConfigTimestamp":1794475705.5,"Firmware":"fw","Good5Min":true,"Error":null,"PowerConstrained":true,"AuditID":"dismist-r3","RecoveredFromBadConfig":true,"Count":4}`))
	if err != nil || update.Applied == nil {
		t.Fatal(err)
	}
	applied := *update.Applied
	if applied.ConfigTimestamp != 1794475705.5 || !applied.Good5Min || applied.Error != "" || !applied.PowerConstrained ||
		applied.AuditID != "dismist-r3" || !applied.RecoveredFromBadConfig || applied.Count != 4 || update.APID != apID {
		t.Fatalf("applied %+v", applied)
	}
	output, err := telemetry.Decode([]byte(`{"Topic":"ap-cmd-output-","HashKey":"02-aa-00-00-00-01","Payload":{"Session":"dismist-1","Output":"64 bytes from 192.0.2.1"}}`))
	if err != nil || output.CommandOutput == nil || output.CommandOutput.Session != "dismist-1" {
		t.Fatalf("command output %+v %v", output, err)
	}
}
