package fakeap

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/telemetry/pbtest"
)

// Telemetry topics.
const (
	TopicFull = "ap-stats-full-"
	TopicFast = "ap-stats-fast-"
)

// Snapshot is the synthetic state an AP reports in its stats frames.
// Field numbers are the 0.15 pace APStats struct tags (design §5.2); every
// section dismist decodes can be filled. Zero values are omitted, as
// proto3 omits them on the wire.
type Snapshot struct {
	Uptime          uint64 // 18
	BootCount       uint64 // 71
	ConfigTimestamp float64
	Country         string // 75
	Hostname        string // 70
	CLMVersion      string // 93
	Radios          []Radio
	WLANs           []WLAN
	Clients         []Client
	Interfaces      []Interface
	Probes          []Probe // 36

	// Health (full frames unless noted).
	ClockOffset time.Duration // AP clock minus real time for field 5 (every frame)
	Interval    float64       // 23
	Memory      [2]uint64     // 24.2 total, available kB
	Load        [3]float32    // 24.4.1
	RW          [2]uint64     // 24.6.1 total, available kB
	CPU         *CPU          // 24.7.1
	KMsg        []string      // 24.1
	CPUTemp     float32       // 80.2.2
	Agent       [3]uint64     // 84: alloc, goroutines, RSS
	// Cloud (48, every frame): 0.15 layout unless LegacyCloud.
	TCPRTT, WebSockRTT float32
	LegacyCloud        bool // 0.8/0.14: 1 LastRTT = TCPRTT
	PMTU, SendMSS      uint32

	Ports      []Port       // 25
	LLDP       *LLDP        // 56
	LLDPPower  []LLDPPower  // 57 (0.15 sends one per port)
	Power      *Power       // 63
	DHCP       *DHCP        // 28
	Gateway    netip.Addr   // 29 default route
	Neighbors  []Neighbor   // 73
	DNS        []netip.Addr // 50
	DHCP6      *DHCP6       // 87
	Inactive   []uint64     // 79
	Scan       *Scan        // 69
	BLE        []BLE        // 38
	Wxlan      []Wxlan      // 97
	LACPFails  uint64       // 106
	ProbeExtra bool         // emit 104 PaceDot11RadioEvent etc. (ignored)
}

// Radio is one RadioStats entry (field 31).
type Radio struct {
	Dev        string // 31.1, "r0"
	Band       uint64 // 31.2: 0 = 2.4 GHz, 1 = 5 GHz (the scan radio also reports 0)
	Channel    uint64 // 31.3
	Bandwidth  uint64 // 31.5, MHz
	NoiseFloor int64  // 31.6, dBm
	MaxTxPower int64  // 31.8, dBm
	Clients    uint64 // 31.22
	PHY        uint64 // 31.26
	// UtilAll is the mean channel utilisation (31.30.1.1), 0 to 1.
	UtilAll       float32
	Radar         bool   // 31.17.1
	CACState      uint64 // 31.17.2
	TxRetries     uint64 // 31.18.23
	Beacons       uint64 // 31.31.5
	ProbeRequests uint64 // 31.27.1
}

// WLAN is one WLANStats entry (field 32): a VAP.
type WLAN struct {
	ID         string // 32.1, network UUID (sent as 16 raw bytes when it parses)
	RadioIndex uint64 // 32.2
	BSSID      net.HardwareAddr
	Dev        string // 32.4, "wl1.1"
	SSID       string // 32.5
	Clients    uint64 // 32.6
	McastPkts  uint64 // 32.7
	InvalidMIC uint64 // 32.22.11
}

// Client is one ClientStats entry (field 33).
type Client struct {
	MAC              net.HardwareAddr
	RadioIndex       uint64 // 33.2
	Band             uint64 // 33.3
	BSSID            net.HardwareAddr
	WLANID           string // 33.5.1
	SSID             string // 33.5.2
	ConnectedSeconds uint64 // 33.6
	RSSI             int64  // 33.8, dBm
	PerAntenna       []int64
	TxRate           uint64 // 33.11, 100 kb/s (full frames only)
	TxBytes          uint64 // 33.12
	RxRate           uint64 // 33.22 (full frames only)
	RxBytes          uint64 // 33.23
	Hostname         string // 33.44 (full frames only)
	UserAgent        string // 33.45 (full frames only)
	VLAN             uint64 // 33.47
	Protocol         string // 33.52
	Streams          uint64 // 33.53
	IP               netip.Addr
	AKM, Pairwise    string // 33.66, 33.67
}

// Interface is one SVIStats entry (field 26).
type Interface struct {
	Name      string // 26.1
	VLAN      uint64 // 26.2
	Addresses []netip.Prefix
}

// Probe is one ProbeStats entry (field 36).
type Probe struct {
	Dev      string
	Band     uint64
	Channels []uint64
	Requests uint64
}

// CPU is a /proc/stat line in jiffies.
type CPU struct{ User, System, Idle, SoftIRQ uint64 }

// Port is one SwitchportStats entry (field 25).
type Port struct {
	Name             string
	Up, FullDuplex   bool
	Mbps             uint64
	RxBytes, TxBytes uint64
	RxErrors         uint64
}

// LLDP is the LLDP neighbour (field 56).
type LLDP struct {
	SystemName, PortID, PortDesc, ChassisID, APPort string
	NativeVLAN                                      uint64
	VLANs                                           []uint64
}

// LLDPPower is one LLDP power entry (field 57), mW.
type LLDPPower struct {
	Allocated, Requested, Needed uint64
	Port                         string
}

// Power is the power manager (field 63), mW.
type Power struct {
	Source            string
	Available, Needed uint64
	Budget            int64
	Constrained       string
}

// DHCP is a DHCPv4 lease (field 28).
type DHCP struct {
	Dev      string
	State    string
	Address  netip.Prefix
	Server   netip.Addr
	Gateway  netip.Addr
	DNS      []netip.Addr
	Domain   string
	Lease    uint64
	Renewed  time.Time
	NTP      []netip.Addr
	ErrorMsg string
}

// DHCP6 is a DHCPv6 lease (field 87).
type DHCP6 struct {
	Dev, State string
	Addresses  []netip.Addr
}

// Neighbor is an ARP entry (field 73).
type Neighbor struct {
	IP  netip.Addr
	MAC net.HardwareAddr
}

// Scan is a neighbour scan (field 69).
type Scan struct {
	Band24, Band5 []Neighbour
	Channels      []ScanChannel
}

// Neighbour is one BSS the scan heard.
type Neighbour struct {
	SSID        string
	BSSID       net.HardwareAddr
	RSSI        int64
	Channel     uint64
	Width       uint64
	Stations    uint64
	Utilization uint64
}

// ScanChannel is one scanned channel.
type ScanChannel struct {
	Channel, Width, Band uint64
	Noise                int64
	Util                 float32
}

// BLE is one BTRadioStats entry (field 38).
type BLE struct {
	MAC                net.HardwareAddr
	TxAdverts, RxScans uint64
}

// Wxlan is one WxlanStats entry (field 97).
type Wxlan struct {
	Dev            string
	Rx, Tx, Drops  uint64
	ARPRx, ARPDrop uint64
}

// DefaultSnapshot is a plausible AP33 (radio table r0 access 5 GHz, r1
// access 2.4 GHz, r2 scan): one WLAN "Example" on both bands, clients on
// the 5 GHz VAP, a vlan1 address in 192.0.2.0/24, an LLDP switch, PoE
// over LLDP and a small neighbour scan. Everything is synthetic.
func DefaultSnapshot(clients int) Snapshot {
	bssid5 := net.HardwareAddr{0x02, 0xaa, 0, 0, 0x11, 0x01}
	bssid24 := net.HardwareAddr{0x02, 0xaa, 0, 0, 0x10, 0x01}
	wlanID := "00000000-0000-4000-8000-000000000001"
	snapshot := Snapshot{
		Uptime: 120, BootCount: 7, Country: "AU", Hostname: "fake-ap", CLMVersion: "clm_fake_1",
		Interval: 60, Memory: [2]uint64{1031800, 880000}, Load: [3]float32{0.5, 0.4, 0.3},
		RW: [2]uint64{7680, 7000}, CPU: &CPU{User: 5000, System: 1000, Idle: 60000, SoftIRQ: 500},
		CPUTemp: 52.5, Agent: [3]uint64{9 << 20, 150, 16 << 20},
		TCPRTT: 0.008, WebSockRTT: 0.04, PMTU: 1500, SendMSS: 1448,
		Radios: []Radio{
			{Dev: "r0", Band: 1, Channel: 36, Bandwidth: 80, NoiseFloor: -95, MaxTxPower: 10, PHY: 9, UtilAll: 0.05, CACState: 2, Beacons: 1000},
			{Dev: "r1", Band: 0, Channel: 11, Bandwidth: 20, NoiseFloor: -92, MaxTxPower: 6, PHY: 9, UtilAll: 0.2, Beacons: 1000},
			{Dev: "r2", Band: 0, Channel: 165, PHY: 9},
		},
		WLANs: []WLAN{
			{ID: wlanID, RadioIndex: 0, BSSID: bssid5, Dev: "wl0.1", SSID: "Example"},
			{ID: wlanID, RadioIndex: 1, BSSID: bssid24, Dev: "wl1.1", SSID: "Example"},
		},
		Interfaces: []Interface{
			{Name: "vlan1", VLAN: 1, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}},
		},
		Probes: []Probe{{Dev: "r0", Band: 1, Channels: []uint64{36}, Requests: 10}, {Dev: "r1", Channels: []uint64{11}, Requests: 100}},
		Ports: []Port{
			{Name: "eth0", Up: true, FullDuplex: true, Mbps: 1000, RxBytes: 1_000_000, TxBytes: 200_000},
			{Name: "eth1"},
		},
		LLDP: &LLDP{SystemName: "example-switch", PortID: "ge-0/0/1", PortDesc: "AP uplink", ChassisID: "02:aa:00:00:ff:01",
			APPort: "eth0", NativeVLAN: 1, VLANs: []uint64{1, 20}},
		LLDPPower: []LLDPPower{{Allocated: 19500, Requested: 19500, Needed: 19500, Port: "eth0"}},
		Power:     &Power{Source: "LLDP", Available: 19500, Needed: 19500, Budget: 0, Constrained: "None"},
		DHCP: &DHCP{Dev: "vlan1", State: "BOUND", Address: netip.MustParsePrefix("192.0.2.10/24"),
			Server: netip.MustParseAddr("192.0.2.1"), Gateway: netip.MustParseAddr("192.0.2.1"),
			DNS: []netip.Addr{netip.MustParseAddr("192.0.2.1")}, Domain: "example.net", Lease: 86400},
		Gateway:   netip.MustParseAddr("192.0.2.1"),
		Neighbors: []Neighbor{{IP: netip.MustParseAddr("192.0.2.1"), MAC: net.HardwareAddr{0x02, 0xaa, 0, 0, 0xfe, 0x01}}},
		DNS:       []netip.Addr{netip.MustParseAddr("192.0.2.1")},
		Scan: &Scan{
			Band24: []Neighbour{{SSID: "Neighbour", BSSID: net.HardwareAddr{0x02, 0xbb, 0, 0, 0, 1}, RSSI: -70, Channel: 6, Width: 20, Stations: 2, Utilization: 40}},
			Band5:  []Neighbour{{BSSID: net.HardwareAddr{0x02, 0xbb, 0, 0, 0, 2}, RSSI: -80, Channel: 149, Width: 80}},
			Channels: []ScanChannel{
				{Channel: 1, Width: 20, Band: 0, Noise: -93, Util: 0.3},
				{Channel: 36, Width: 20, Band: 1, Noise: -96, Util: 0.05},
			},
		},
		BLE:   []BLE{{MAC: net.HardwareAddr{0x02, 0xaa, 0, 0, 0x30, 0x01}, TxAdverts: 100, RxScans: 50}},
		Wxlan: []Wxlan{{Dev: "eth0", Rx: 1000, Tx: 900, ARPRx: 10}},
		KMsg:  []string{"6,1,1000000,-;fakeap: synthetic kernel log line"},
	}
	for index := range clients {
		snapshot.Clients = append(snapshot.Clients, Client{
			MAC:        net.HardwareAddr{0x02, 0xaa, 0, 0, 0x20, byte(index + 1)},
			RadioIndex: 0, Band: 1, BSSID: bssid5, WLANID: wlanID, SSID: "Example",
			ConnectedSeconds: uint64(60 * (index + 1)), RSSI: int64(-50 - index), PerAntenna: []int64{-50, -52},
			TxRate: 8660, RxRate: 7200, TxBytes: uint64(1000 * (index + 1)), RxBytes: uint64(2000 * (index + 1)),
			Hostname: fmt.Sprintf("client-%d", index+1), VLAN: 1, Protocol: "ax", Streams: 2,
			IP:  netip.AddrFrom4([4]byte{192, 0, 2, byte(100 + index)}),
			AKM: "WPA2-PSK", Pairwise: "CCMP",
		})
	}
	snapshot.Radios[0].Clients = uint64(clients)
	snapshot.WLANs[0].Clients = uint64(clients)
	return snapshot
}

// uuidBytes returns a text UUID as 16 raw bytes (as the AP sends
// uuid.UUID), or the text itself when it does not parse.
func uuidBytes(text string) []byte {
	raw, err := hex.DecodeString(strings.ReplaceAll(text, "-", ""))
	if err != nil || len(raw) != 16 {
		return []byte(text)
	}
	return raw
}

func timeMessage(at time.Time) *pbtest.Message {
	return pbtest.New().Int(1, at.Unix()).Int(2, int64(at.Nanosecond()))
}

// Stats encodes the snapshot as an APStats message. Fast frames carry
// what 0.15 fast frames carry (1, 5, 18, 19, 20, 21, 48, 60) and the
// client list without the full-only fields.
func (s Snapshot) Stats(id, model, firmware string, full bool) *pbtest.Message {
	stats := pbtest.New()
	if mac, err := net.ParseMAC(id); err == nil && len(mac) == 6 {
		stats.Raw(1, mac)
	}
	stats.Message(5, timeMessage(time.Now().Add(s.ClockOffset)))
	stats.Varint(18, s.Uptime)
	stats.String(19, model).String(20, firmware)
	stats.Bool(21, true)
	stats.Message(48, s.cloud())
	if s.ConfigTimestamp != 0 {
		stats.Double(60, s.ConfigTimestamp)
	}
	if full {
		s.fullSections(stats)
	}
	for _, client := range s.Clients {
		stats.Message(33, client.record(full))
	}
	return stats
}

func (s Snapshot) cloud() *pbtest.Message {
	cloud := pbtest.New()
	if s.LegacyCloud {
		if s.TCPRTT != 0 {
			cloud.Float(1, s.TCPRTT)
		}
	}
	if s.PMTU != 0 {
		cloud.Varint(3, uint64(s.PMTU))
	}
	if s.SendMSS != 0 {
		cloud.Varint(4, uint64(s.SendMSS))
	}
	if !s.LegacyCloud {
		if s.WebSockRTT != 0 {
			cloud.Float(6, s.WebSockRTT)
		}
		if s.TCPRTT != 0 {
			cloud.Float(8, s.TCPRTT)
		}
	}
	return cloud
}

func (s Snapshot) fullSections(stats *pbtest.Message) {
	if s.Interval != 0 {
		stats.Double(23, s.Interval)
	}
	proc := pbtest.New()
	for _, line := range s.KMsg {
		proc.String(1, line)
	}
	if s.Memory != [2]uint64{} {
		proc.Message(2, pbtest.New().Varint(1, s.Memory[0]).Varint(2, s.Memory[1]))
	}
	if s.Load != [3]float32{} {
		proc.Message(4, pbtest.New().PackedFloat(1, s.Load[:]...).Varint(3, 100))
	}
	if s.RW != [2]uint64{} {
		proc.Message(6, pbtest.New().Message(1, pbtest.New().Varint(1, s.RW[0]).Varint(2, s.RW[1])))
	}
	if s.CPU != nil {
		line := func(name string) *pbtest.Message {
			return pbtest.New().String(1, name).Varint(2, s.CPU.User).Varint(4, s.CPU.System).
				Varint(5, s.CPU.Idle).Varint(8, s.CPU.SoftIRQ)
		}
		proc.Message(7, pbtest.New().Message(1, line("cpu")).Message(2, line("cpu0")))
	}
	if proc.Len() != 0 {
		stats.Message(24, proc)
	}
	for _, port := range s.Ports {
		record := pbtest.New().String(1, port.Name)
		if port.Up {
			record.Bool(2, true)
		}
		if port.FullDuplex {
			record.Bool(3, true)
		}
		if port.Mbps != 0 {
			record.Varint(5, port.Mbps)
		}
		if port.RxBytes != 0 {
			record.Varint(6, port.RxBytes)
		}
		if port.TxBytes != 0 {
			record.Varint(8, port.TxBytes)
		}
		if port.RxErrors != 0 {
			record.Varint(17, port.RxErrors)
		}
		stats.Message(25, record)
	}
	for _, iface := range s.Interfaces {
		svi := pbtest.New().String(1, iface.Name)
		if iface.VLAN != 0 {
			svi.Varint(2, iface.VLAN)
		}
		for _, prefix := range iface.Addresses {
			svi.Message(3, pbtest.New().Varint(2, uint64(prefix.Bits())).Raw(3, prefix.Addr().AsSlice()))
		}
		stats.Message(26, svi)
	}
	if s.DHCP != nil {
		lease := pbtest.New().String(1, s.DHCP.Dev).String(2, s.DHCP.State)
		if !s.DHCP.Renewed.IsZero() {
			lease.Message(3, timeMessage(s.DHCP.Renewed))
		}
		if s.DHCP.Lease != 0 {
			lease.Varint(4, s.DHCP.Lease)
		}
		if s.DHCP.Address.IsValid() {
			lease.Message(6, pbtest.New().Varint(2, uint64(s.DHCP.Address.Bits())).Raw(3, s.DHCP.Address.Addr().AsSlice()))
		}
		if s.DHCP.ErrorMsg != "" {
			lease.String(9, s.DHCP.ErrorMsg)
		}
		if s.DHCP.Server.IsValid() {
			lease.Raw(10, s.DHCP.Server.AsSlice())
		}
		if s.DHCP.Gateway.IsValid() {
			lease.Raw(11, s.DHCP.Gateway.AsSlice())
		}
		for _, server := range s.DHCP.DNS {
			lease.Raw(12, server.AsSlice())
		}
		if s.DHCP.Domain != "" {
			lease.String(13, s.DHCP.Domain)
		}
		for _, server := range s.DHCP.NTP {
			lease.Raw(14, server.AsSlice())
		}
		stats.Message(28, lease)
	}
	if s.Gateway.IsValid() {
		stats.Message(29, pbtest.New().Raw(3, s.Gateway.AsSlice()))
	}
	for _, radio := range s.Radios {
		record := pbtest.New().String(1, radio.Dev)
		if radio.Band != 0 {
			record.Varint(2, radio.Band)
		}
		record.Varint(3, radio.Channel)
		if radio.Bandwidth != 0 {
			record.Varint(5, radio.Bandwidth)
		}
		if radio.NoiseFloor != 0 {
			record.Zigzag(6, radio.NoiseFloor)
		}
		if radio.MaxTxPower != 0 {
			record.Zigzag(8, radio.MaxTxPower)
		}
		if radio.Radar || radio.CACState != 0 {
			dfs := pbtest.New()
			if radio.Radar {
				dfs.Bool(1, true)
			}
			if radio.CACState != 0 {
				dfs.Varint(2, radio.CACState)
			}
			record.Message(17, dfs)
		}
		counter := pbtest.New().Varint(2, 100)
		if radio.TxRetries != 0 {
			counter.Varint(23, radio.TxRetries)
		}
		record.Message(18, counter)
		if radio.Clients != 0 {
			record.Varint(22, radio.Clients)
		}
		if radio.PHY != 0 {
			record.Varint(26, radio.PHY)
		}
		if radio.ProbeRequests != 0 {
			record.Message(27, pbtest.New().Varint(1, radio.ProbeRequests))
		}
		if radio.UtilAll != 0 {
			mean := pbtest.New().Float(1, radio.UtilAll).Float(2, radio.UtilAll/2)
			record.Message(30, pbtest.New().Message(1, mean).Message(3, mean).Varint(4, 55).Varint(5, 29))
		}
		if radio.Beacons != 0 {
			record.Message(31, pbtest.New().Varint(5, radio.Beacons))
		}
		stats.Message(31, record)
	}
	for _, wlan := range s.WLANs {
		record := pbtest.New().Raw(1, uuidBytes(wlan.ID))
		if wlan.RadioIndex != 0 {
			record.Varint(2, wlan.RadioIndex)
		}
		record.Raw(3, wlan.BSSID).String(4, wlan.Dev).String(5, wlan.SSID)
		if wlan.Clients != 0 {
			record.Varint(6, wlan.Clients)
		}
		if wlan.McastPkts != 0 {
			record.Varint(7, wlan.McastPkts)
		}
		if wlan.InvalidMIC != 0 {
			record.Message(22, pbtest.New().Varint(11, wlan.InvalidMIC))
		}
		stats.Message(32, record)
	}
	for _, probe := range s.Probes {
		record := pbtest.New().String(1, probe.Dev)
		if probe.Band != 0 {
			record.Varint(2, probe.Band)
		}
		record.PackedVarint(3, probe.Channels...)
		if probe.Requests != 0 {
			record.Varint(6, probe.Requests)
		}
		stats.Message(36, record)
	}
	for _, ble := range s.BLE {
		stats.Message(38, pbtest.New().Message(1, pbtest.New().Raw(1, ble.MAC).Varint(7, ble.TxAdverts).Varint(10, ble.RxScans)))
	}
	for _, server := range s.DNS {
		stats.Raw(50, server.AsSlice())
	}
	if s.LLDP != nil {
		lldp := pbtest.New().String(1, s.LLDP.PortDesc).String(2, s.LLDP.SystemName).String(5, s.LLDP.PortID)
		if s.LLDP.NativeVLAN != 0 {
			lldp.Varint(9, s.LLDP.NativeVLAN)
		}
		for _, vlan := range s.LLDP.VLANs {
			lldp.Message(10, pbtest.New().Varint(1, vlan).String(2, fmt.Sprintf("vlan-%d", vlan)))
		}
		lldp.String(18, s.LLDP.ChassisID).String(21, s.LLDP.APPort)
		stats.Message(56, lldp)
	}
	for _, power := range s.LLDPPower {
		record := pbtest.New().Varint(1, 2).Bool(2, true).Varint(3, power.Allocated).Varint(4, power.Requested).Varint(5, power.Needed)
		if power.Port != "" {
			record.String(6, power.Port)
		}
		stats.Message(57, record)
	}
	if s.Power != nil {
		power := pbtest.New().String(1, s.Power.Source).Varint(2, s.Power.Available).Varint(3, s.Power.Needed)
		if s.Power.Budget != 0 {
			power.Zigzag(5, s.Power.Budget)
		}
		if s.Power.Constrained != "" {
			power.String(6, s.Power.Constrained)
		}
		power.String(7, s.Power.Source)
		stats.Message(63, power)
	}
	if s.Scan != nil {
		scan := pbtest.New()
		neighbour := func(n Neighbour) *pbtest.Message {
			record := pbtest.New()
			if n.SSID != "" {
				record.String(1, n.SSID)
			}
			record.Raw(2, n.BSSID).Zigzag(3, n.RSSI).Message(4, pbtest.New().Varint(1, n.Channel).Varint(2, n.Width))
			if n.Stations != 0 || n.Utilization != 0 {
				record.Message(12, pbtest.New().Bool(1, true).Varint(2, n.Stations).Varint(3, n.Utilization))
			}
			return record
		}
		for _, n := range s.Scan.Band24 {
			scan.Message(7, neighbour(n))
		}
		for _, n := range s.Scan.Band5 {
			scan.Message(8, neighbour(n))
		}
		for _, channel := range s.Scan.Channels {
			record := pbtest.New().Message(1, pbtest.New().Varint(1, channel.Channel).Varint(2, channel.Width)).
				Zigzag(2, channel.Noise).Message(4, pbtest.New().Float(1, channel.Util))
			if channel.Band != 0 {
				record.Varint(7, channel.Band)
			}
			scan.Message(9, record)
		}
		scan.Varint(10, s.Uptime).Varint(12, 14)
		stats.Message(69, scan)
	}
	if s.Hostname != "" {
		stats.String(70, s.Hostname)
	}
	if s.BootCount != 0 {
		stats.Varint(71, s.BootCount)
	}
	for _, neighbor := range s.Neighbors {
		stats.Message(73, pbtest.New().Raw(1, neighbor.IP.AsSlice()).Raw(2, neighbor.MAC))
	}
	if s.Country != "" {
		stats.String(75, s.Country)
	}
	if len(s.Inactive) != 0 {
		stats.Message(79, pbtest.New().PackedVarint(1, s.Inactive...))
	}
	if s.CPUTemp != 0 {
		stats.Message(80, pbtest.New().Message(1, pbtest.New().Message(1, pbtest.New().Float(3, 1))).
			Message(2, pbtest.New().Float(2, s.CPUTemp)))
	}
	if s.Agent != [3]uint64{} {
		stats.Message(84, pbtest.New().Varint(1, s.Agent[0]).Varint(2, s.Agent[1]).Varint(3, s.Agent[2]))
	}
	if s.DHCP6 != nil {
		lease := pbtest.New().String(1, s.DHCP6.Dev).String(2, s.DHCP6.State)
		for _, address := range s.DHCP6.Addresses {
			lease.Raw(6, address.AsSlice())
		}
		stats.Message(87, lease)
	}
	if s.CLMVersion != "" {
		stats.String(93, s.CLMVersion)
	}
	for _, counters := range s.Wxlan {
		stats.Message(97, pbtest.New().String(1, counters.Dev).Varint(2, counters.Rx).Varint(3, counters.Tx).
			Varint(4, counters.ARPRx).Varint(6, counters.ARPDrop).Varint(8, counters.Drops))
	}
	stats.Message(103, pbtest.New().Varint(4, 10).Varint(6, 20))
	if s.ProbeExtra {
		stats.Message(104, pbtest.New().Varint(1, 1))
	}
	stats.Message(106, pbtest.New().Varint(1, s.LACPFails).Varint(2, 1))
}

// record encodes one client. Fast frames leave out the full-only fields
// (11, 22, 44, 45, ...), as the AP does.
func (c Client) record(full bool) *pbtest.Message {
	record := pbtest.New().Raw(1, c.MAC)
	if c.RadioIndex != 0 {
		record.Varint(2, c.RadioIndex)
	}
	if c.Band != 0 {
		record.Varint(3, c.Band)
	}
	record.Raw(4, c.BSSID)
	wlan := pbtest.New()
	if c.WLANID != "" {
		wlan.Raw(1, uuidBytes(c.WLANID))
	}
	record.Message(5, wlan.String(2, c.SSID)).
		Varint(6, c.ConnectedSeconds).Zigzag(8, c.RSSI)
	if len(c.PerAntenna) != 0 {
		record.PackedZigzag(9, c.PerAntenna...)
	}
	record.Zigzag(10, c.RSSI).Varint(12, c.TxBytes).Varint(23, c.RxBytes).Varint(47, c.VLAN)
	if full {
		record.Varint(11, c.TxRate).Varint(22, c.RxRate)
		if c.Hostname != "" {
			record.String(44, c.Hostname)
		}
		if c.UserAgent != "" {
			record.String(45, c.UserAgent)
		}
	}
	if c.Protocol != "" {
		record.String(52, c.Protocol)
	}
	if c.Streams != 0 {
		record.Varint(53, c.Streams)
	}
	if c.IP.Is4() {
		record.Raw(55, c.IP.AsSlice())
	}
	if c.AKM != "" {
		record.String(66, c.AKM)
	}
	if c.Pairwise != "" {
		record.String(67, c.Pairwise)
	}
	return record
}

// SendStats sends a full or fast stats frame for the snapshot.
func (a *AP) SendStats(snapshot Snapshot, full bool) error {
	topic := TopicFast
	if full {
		topic = TopicFull
	}
	stats := snapshot.Stats(a.config.ID, a.config.Model, a.config.Firmware, full)
	return a.SendTelemetry(pbtest.Gzip(pbtest.Frame(topic, a.config.ID, stats)))
}

// PaceEvent is a synthetic PACE event (msgs.EPEvent plus type fields).
type PaceEvent struct {
	Type      int
	Title     string
	Text      string
	When      time.Time
	ClientMAC string // "02-aa-…"; empty for AP events
	ClientIP  string
	// ID is the event's time UUID (random when empty); IsUpdate re-sends
	// an event with the same ID; Corrects lists event IDs it resolves.
	ID       string
	IsUpdate bool
	Corrects []string
	// Fields are added to the top level (the type's own fields).
	Fields map[string]any
}

// NewEventID returns a random UUID for a PACE event.
func NewEventID() string {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	id[6] = 0x10 | id[6]&0x0f
	id[8] = 0x80 | id[8]&0x3f
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

// SendPaceEvent sends a PACE event the way 0.15 does: client events carry
// the plural Topics list, AP events a single Topic.
func (a *AP) SendPaceEvent(event PaceEvent) error {
	when := event.When
	if when.IsZero() {
		when = time.Now().UTC()
	}
	id := event.ID
	if id == "" {
		id = NewEventID()
	}
	message := map[string]any{
		"ID":       id,
		"IsUpdate": event.IsUpdate,
		"Type":     event.Type,
		"Title":    event.Title,
		"Text":     event.Text,
		"When":     when.Format(time.RFC3339Nano),
		"Model":    a.config.Model, "Firmware": a.config.Firmware,
		"HashKey": a.config.ID,
	}
	if len(event.Corrects) != 0 {
		message["Corrects"] = event.Corrects
	}
	for key, value := range event.Fields {
		message[key] = value
	}
	if event.ClientMAC != "" {
		message["Topics"] = []string{"ep-pace-event-", "client-pace-event-"}
		message["impacted_client_mac"] = event.ClientMAC
		if event.ClientIP != "" {
			message["impacted_client_ip"] = event.ClientIP
		}
	} else {
		message["Topic"] = "ep-pace-event-"
	}
	return a.SendJSON(message)
}

// SendCommandOutput sends an ap-cmd-output- message for a diagnostic
// session.
func (a *AP) SendCommandOutput(session, output string) error {
	return a.SendJSON(map[string]any{
		"Topic": "ap-cmd-output-", "HashKey": a.config.ID,
		"Payload": map[string]string{"Session": session, "Output": output},
	})
}
