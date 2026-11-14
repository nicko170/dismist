package metrics

import (
	"github.com/nicko170/dismist/internal/controller"
)

// fixtureDetail is a full telemetry v2 decode for AP 02-aa-00-00-00-01,
// shaped like the 0.15 frames in the research (values synthetic): a 5 GHz
// and a 2.4 GHz access radio plus the scan radio, one WLAN on both access
// radios, clients on each, eth0 up at 1 Gb/s on LLDP power.
func fixtureDetail() APDetail {
	return APDetail{
		Telemetry: APTelemetry{
			IntervalSeconds:  59.9998,
			Configured:       true,
			ClockSkewSeconds: 1.5, HasClockSkew: true,
			ConfigTimestamp: 1794548005.856035,
			BootCount:       6,
			ClmVersion:      "apfw_clm_v15_53",
			MemoryTotalKB:   1031800, MemoryAvailableKB: 881252,
			LoadAverage: []float64{2.561, 2.626, 2.256},
			CPUJiffies:  map[string]uint64{"user": 55993, "nice": 0, "system": 13887, "idle": 579475, "softirq": 5557},
			RWTotalKB:   7680, RWAvailableKB: 7188,
			CPUTemperatureCelsius: 53.74,
			TCPRTTSeconds:         0.0034, WebSocketRTTSeconds: 0.012,
			AgentHeapBytes: 18 << 20, AgentRSSBytes: 42 << 20, AgentGoroutines: 61, HasAgent: true,
			Ports: []Port{
				{Name: "eth0", Up: true, FullDuplex: true, Mbps: 1000, RxBytes: 664467698, RxPackets: 508404, TxBytes: 416473, TxPackets: 1852, RxFCSErrors: 3},
				{Name: "eth1"},
				{Name: "not a port"},
			},
			LLDPPower:     []LLDPPower{{Port: "eth0", AllocatedMilliwatts: 19500, RequestedMilliwatts: 19500}},
			LLDPNeighbors: []LLDPNeighbor{{Port: "eth0", ChassisID: "02-aa-00-00-30-01", PortID: "ge-0/0/7", SystemName: "switch-1.example"}},
			Power:         &Power{Source: "LLDP", AvailableMilliwatts: 19500, NeededMilliwatts: 15200, BudgetMilliwatts: 4300, DrawMilliwatts: 11800},
			InactiveVLANs: []uint16{30, 0, 5000},
			Radios: []Radio{
				{Dev: "r0", Band: "5", Role: "access", PHY: "ax", Channel: 149, BandwidthMHz: 80, NoiseFloorDBm: -94, MaxTxPowerDBm: 17, Clients: 2,
					Utilization: map[string]float64{"all": 0.12, "tx": 0.05, "rx_in_bss": 0.03, "rx_other_bss": 0.02, "unknown_wifi": 0.01, "non_wifi": 0.005, "undecodable_wifi": 0.005}, UtilizationMax: 0.4, HasUtilization: true,
					Counters: RadioCounters{TxRetries: 120, Beacons: 9000, RxGlitches: 30, RxBadFCS: 12, ProbeRequests: 400}},
				{Dev: "r1", Band: "2.4", Role: "access", PHY: "ax", Channel: 1, BandwidthMHz: 20, NoiseFloorDBm: -90, MaxTxPowerDBm: 12, Clients: 1,
					Utilization: map[string]float64{"all": 0.25}, UtilizationMax: 0.6, HasUtilization: true,
					Counters: RadioCounters{TxRetries: 300, Beacons: 9000}},
				{Dev: "r2", Band: "2.4", Role: "scan", Channel: 165, BandwidthMHz: 20, NoiseFloorDBm: -95, Utilization: map[string]float64{"all": 0.9}, HasUtilization: true},
				{Dev: "wlan0"},
			},
			WLANs: []WLAN{
				{ID: "net-1", RadioIndex: 0, Clients: 2, HasClients: true, TxMulticastPackets: 50, TxMulticastBytes: 9000, EapolInvalidMIC: 4, EapolMsg1Retries: 1},
				{ID: "net-1", RadioIndex: 1, TxMulticastPackets: 20},
				{ID: "net-1", RadioIndex: 2},
				{ID: "bad id with spaces", RadioIndex: 0},
			},
			Clients: []Client{
				{MAC: "02:AA:00:00:10:01", RadioIndex: 0, WLANID: "net-1", RSSIDBm: -55, Protocol: "ax", TxBitsPerSecond: 1000, RxBitsPerSecond: 500, TxRateMbps: 8.1, RxRateMbps: 24, RxBytes: 1295567, TxBytes: 338, TxRetries: 7, ConnectedSeconds: 600, Hostname: "phone"},
				{MAC: "02-aa-00-00-10-02", RadioIndex: 0, WLANID: "net-1", RSSIDBm: -80, Protocol: "AC"},
				{MAC: "02-aa-00-00-10-03", RadioIndex: 1, WLANID: "net-1", RSSIDBm: -63, Protocol: "wifi7?"},
				{MAC: "02-aa-00-00-10-04", RadioIndex: 2, WLANID: "net-1", RSSIDBm: -40},
				{MAC: "not-a-mac", RadioIndex: 1, WLANID: "net-1", RSSIDBm: -70, Protocol: "n"},
			},
		},
		Verified: true, VerifyMismatch: false,
		Pushes: []PushCount{
			{Mode: "notify", Result: "applied", Count: 3},
			{Mode: "reconnect", Result: "fallback", Count: 1},
			{Mode: "weird", Result: "odd", Count: 2},
		},
		Topics:          map[string]uint64{"ap-stats-full-": 12, "ap-stats-fast-": 345, "ep-config-applied-": 1, "x-unknown-": 2},
		PlannedChannels: map[string]int{"5": 149, "2.4": 1, "60": 3},
	}
}

func fixtureCounters() controller.Counters {
	return controller.Counters{
		Connections: 6, Disconnects: 5, ConfigDeliveries: 6, ConfigRejections: 1, SourceMismatches: 2,
		AuthFailures: map[string]uint64{controller.AuthStageHello: 1, controller.AuthStageProof: 3},
		DecodeErrors: map[string]uint64{"hello": 1, "radio": 2, "Bad Section!": 1},
		Frames:       map[string]uint64{"ep-telemetry": 357, "ping": 10, "other": 1},
	}
}
