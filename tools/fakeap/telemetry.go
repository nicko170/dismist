package fakeap

import (
	"crypto/rand"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/nicko170/dismist/internal/telemetry/pbtest"
)

// Telemetry topics.
const (
	TopicFull = "ap-stats-full-"
	TopicFast = "ap-stats-fast-"
)

// Snapshot is the synthetic state an AP reports in its stats frames.
// Field numbers are the 0.15 pace APStats struct tags (design §5.2).
type Snapshot struct {
	Uptime          uint64 // 18
	BootCount       uint64 // 71
	ConfigTimestamp float64
	Country         string // 75
	Radios          []Radio
	WLANs           []WLAN
	Clients         []Client
	Interfaces      []Interface
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
}

// WLAN is one WLANStats entry (field 32): a VAP.
type WLAN struct {
	ID         string // 32.1, network UUID as text bytes
	RadioIndex uint64 // 32.2
	BSSID      net.HardwareAddr
	Dev        string // 32.4, "wl1.1"
	SSID       string // 32.5
	Clients    uint64 // 32.6
}

// Client is one ClientStats entry (field 33).
type Client struct {
	MAC              net.HardwareAddr
	RadioIndex       uint64 // 33.2
	Band             uint64 // 33.3
	BSSID            net.HardwareAddr
	SSID             string // 33.5.2
	ConnectedSeconds uint64 // 33.6
	RSSI             int64  // 33.8, dBm
	TxRate           uint64 // 33.11, 100 kb/s (full frames only)
	TxBytes          uint64 // 33.12
	RxRate           uint64 // 33.22 (full frames only)
	RxBytes          uint64 // 33.23
	Hostname         string // 33.44 (full frames only)
	VLAN             uint64 // 33.47
	IP               netip.Addr
}

// Interface is one SVIStats entry (field 26).
type Interface struct {
	Name      string // 26.1
	VLAN      uint64 // 26.2
	Addresses []netip.Prefix
}

// DefaultSnapshot is a plausible AP33: two access radios and the scan
// radio, one WLAN "Example" on both bands, clients on the 5 GHz VAP and a
// vlan1 address in 192.0.2.0/24. Everything is synthetic.
func DefaultSnapshot(clients int) Snapshot {
	bssid24 := net.HardwareAddr{0x02, 0xaa, 0, 0, 0x10, 0x01}
	bssid5 := net.HardwareAddr{0x02, 0xaa, 0, 0, 0x11, 0x01}
	snapshot := Snapshot{
		Uptime: 120, BootCount: 7, Country: "AU",
		Radios: []Radio{
			{Dev: "r0", Band: 0, Channel: 11, Bandwidth: 20, NoiseFloor: -92, MaxTxPower: 6},
			{Dev: "r1", Band: 1, Channel: 36, Bandwidth: 80, NoiseFloor: -95, MaxTxPower: 10},
			{Dev: "r2", Band: 0, Channel: 1, Bandwidth: 20, NoiseFloor: -93},
		},
		WLANs: []WLAN{
			{ID: "00000000-0000-4000-8000-000000000001", RadioIndex: 0, BSSID: bssid24, Dev: "wl0.1", SSID: "Example"},
			{ID: "00000000-0000-4000-8000-000000000001", RadioIndex: 1, BSSID: bssid5, Dev: "wl1.1", SSID: "Example"},
		},
		Interfaces: []Interface{
			{Name: "vlan1", VLAN: 1, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}},
			{Name: "eth0"},
		},
	}
	for index := range clients {
		snapshot.Clients = append(snapshot.Clients, Client{
			MAC:        net.HardwareAddr{0x02, 0xaa, 0, 0, 0x20, byte(index + 1)},
			RadioIndex: 1, Band: 1, BSSID: bssid5, SSID: "Example",
			ConnectedSeconds: uint64(60 * (index + 1)), RSSI: int64(-50 - index),
			TxRate: 8660, RxRate: 7200, TxBytes: uint64(1000 * (index + 1)), RxBytes: uint64(2000 * (index + 1)),
			Hostname: fmt.Sprintf("client-%d", index+1), VLAN: 1,
			IP: netip.AddrFrom4([4]byte{192, 0, 2, byte(100 + index)}),
		})
	}
	snapshot.Radios[1].Clients = uint64(clients)
	snapshot.WLANs[1].Clients = uint64(clients)
	return snapshot
}

// Stats encodes the snapshot as an APStats message. Fast frames carry
// uptime and the client list without the full-only fields.
func (s Snapshot) Stats(id, model, firmware string, full bool) *pbtest.Message {
	stats := pbtest.New()
	if mac, err := net.ParseMAC(id); err == nil && len(mac) == 6 {
		stats.Raw(1, mac)
	}
	stats.Varint(18, s.Uptime)
	if full {
		stats.String(19, model).String(20, firmware)
		if s.ConfigTimestamp != 0 {
			stats.Double(60, s.ConfigTimestamp)
		}
		if s.BootCount != 0 {
			stats.Varint(71, s.BootCount)
		}
		if s.Country != "" {
			stats.String(75, s.Country)
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
		for _, radio := range s.Radios {
			stats.Message(31, pbtest.New().String(1, radio.Dev).Varint(2, radio.Band).
				Varint(3, radio.Channel).Varint(5, radio.Bandwidth).Zigzag(6, radio.NoiseFloor).
				Zigzag(8, radio.MaxTxPower).Varint(22, radio.Clients))
		}
		for _, wlan := range s.WLANs {
			stats.Message(32, pbtest.New().String(1, wlan.ID).Varint(2, wlan.RadioIndex).
				Raw(3, wlan.BSSID).String(4, wlan.Dev).String(5, wlan.SSID).Varint(6, wlan.Clients))
		}
	}
	for _, client := range s.Clients {
		record := pbtest.New().Raw(1, client.MAC).Varint(2, client.RadioIndex).Varint(3, client.Band).
			Raw(4, client.BSSID).Message(5, pbtest.New().String(2, client.SSID)).
			Varint(6, client.ConnectedSeconds).Zigzag(8, client.RSSI).Varint(12, client.TxBytes).
			Varint(23, client.RxBytes).Varint(47, client.VLAN)
		if full {
			record.Varint(11, client.TxRate).Varint(22, client.RxRate)
			if client.Hostname != "" {
				record.String(44, client.Hostname)
			}
		}
		if client.IP.Is4() {
			record.Raw(55, client.IP.AsSlice())
		}
		stats.Message(33, record)
	}
	return stats
}

// SendStats sends a full or fast stats frame for the snapshot.
func (a *AP) SendStats(snapshot Snapshot, full bool) error {
	topic := TopicFast
	if full {
		topic = TopicFull
	}
	stats := snapshot.Stats(a.config.ID, a.config.Model, a.config.Firmware, full)
	return a.SendTelemetry(pbtest.Frame(topic, a.config.ID, stats))
}

// PaceEvent is a synthetic PACE event (msgs.EPEvent).
type PaceEvent struct {
	Type      int
	Title     string
	Text      string
	When      time.Time
	ClientMAC string // "02-aa-…"; empty for AP events
	ClientIP  string
}

// SendPaceEvent sends a PACE event the way 0.15 does: client events carry
// the plural Topics list, AP events a single Topic.
func (a *AP) SendPaceEvent(event PaceEvent) error {
	when := event.When
	if when.IsZero() {
		when = time.Now().UTC()
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	message := map[string]any{
		"ID":    fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]),
		"Type":  event.Type,
		"Title": event.Title,
		"Text":  event.Text,
		"When":  when.Format(time.RFC3339Nano),
		"Model": a.config.Model, "Firmware": a.config.Firmware,
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
