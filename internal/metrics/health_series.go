package metrics

import (
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// telemetryFamilies are the AP health and uplink series, exported only while
// the AP's telemetry is fresh (design §6.1).
type telemetryFamilies struct {
	configured, clockSkew, reportedConfigTS                    *prometheus.Desc
	memoryTotal, memoryAvailable, load, cpu                    *prometheus.Desc
	rwSize, rwAvailable, temperature, rtt                      *prometheus.Desc
	agentHeap, agentRSS, agentGoroutines                       *prometheus.Desc
	portUp, portFullDuplex, portSpeed                          *prometheus.Desc
	portRxBytes, portRxPackets, portTxBytes, portTxPackets     *prometheus.Desc
	portRxErrors, portRxFCSErrors, portRxDiscards, portTxError *prometheus.Desc
	lldpAllocated, lldpRequested, lldpNeighbor                 *prometheus.Desc
	powerAvailable, powerNeeded, powerBudget, powerDraw        *prometheus.Desc
	powerConstrained, powerSource, inactiveVLAN                *prometheus.Desc
}

func newTelemetryFamilies(c *collector) telemetryFamilies {
	neighborLabels := []string{"port", "chassis_id", "port_id", "system_name"}
	return telemetryFamilies{
		configured:       c.apDesc("dismist_ap_configured", "Whether the access point reports itself configured (field 21)."),
		clockSkew:        c.apDesc("dismist_ap_clock_skew_seconds", "Controller receive time minus the access point's clock; positive when the AP clock is behind."),
		reportedConfigTS: c.apDesc("dismist_ap_reported_config_timestamp_seconds", "Timestamp of the config the access point reports it runs (field 60)."),
		memoryTotal:      c.apDesc("dismist_ap_memory_total_bytes", "Access point memory."),
		memoryAvailable:  c.apDesc("dismist_ap_memory_available_bytes", "Access point memory available to programs."),
		load:             c.apDesc("dismist_ap_load_average", "Access point load average.", "period"),
		cpu:              c.apDesc("dismist_ap_cpu_seconds_total", "Access point CPU time by mode, summed over its cores.", "mode"),
		rwSize:           c.apDesc("dismist_ap_rw_filesystem_size_bytes", "Size of the access point's writable /rw filesystem."),
		rwAvailable:      c.apDesc("dismist_ap_rw_filesystem_available_bytes", "Free space on the access point's /rw filesystem."),
		temperature:      c.apDesc("dismist_ap_temperature_celsius", "Access point temperature by sensor (cpu, ambient).", "sensor"),
		rtt:              c.apDesc("dismist_ap_controller_rtt_seconds", "Round-trip time the access point measures to the controller, by socket (tcp, websocket).", "socket"),
		agentHeap:        c.apDesc("dismist_ap_agent_heap_bytes", "Go heap of the access point's telemetry agent (pace)."),
		agentRSS:         c.apDesc("dismist_ap_agent_rss_bytes", "Resident memory of the access point's telemetry agent (pace)."),
		agentGoroutines:  c.apDesc("dismist_ap_agent_goroutines", "Goroutines in the access point's telemetry agent (pace)."),
		portUp:           c.apDesc("dismist_ap_port_up", "Whether the access point's Ethernet port has link.", "port"),
		portFullDuplex:   c.apDesc("dismist_ap_port_full_duplex", "Whether the access point's Ethernet port runs full duplex.", "port"),
		portSpeed:        c.apDesc("dismist_ap_port_speed_bits_per_second", "Negotiated speed of the access point's Ethernet port.", "port"),
		portRxBytes:      c.apDesc("dismist_ap_port_receive_bytes_total", "Bytes received on the access point's Ethernet port.", "port"),
		portRxPackets:    c.apDesc("dismist_ap_port_receive_packets_total", "Packets received on the access point's Ethernet port.", "port"),
		portTxBytes:      c.apDesc("dismist_ap_port_transmit_bytes_total", "Bytes transmitted on the access point's Ethernet port (switch-port counter; may exclude CPU traffic).", "port"),
		portTxPackets:    c.apDesc("dismist_ap_port_transmit_packets_total", "Packets transmitted on the access point's Ethernet port.", "port"),
		portRxErrors:     c.apDesc("dismist_ap_port_receive_errors_total", "Receive errors on the access point's Ethernet port.", "port"),
		portRxFCSErrors:  c.apDesc("dismist_ap_port_receive_fcs_errors_total", "Frames with a bad checksum received on the access point's Ethernet port; a rise points at the cable.", "port"),
		portRxDiscards:   c.apDesc("dismist_ap_port_receive_discards_total", "Received frames the access point's Ethernet port discarded.", "port"),
		portTxError:      c.apDesc("dismist_ap_port_transmit_errors_total", "Transmit errors on the access point's Ethernet port.", "port"),
		lldpAllocated:    c.apDesc("dismist_ap_lldp_power_allocated_watts", "PoE power the switch allocates to the access point over LLDP.", "port"),
		lldpRequested:    c.apDesc("dismist_ap_lldp_power_requested_watts", "PoE power the access point requests over LLDP.", "port"),
		lldpNeighbor:     c.apDesc("dismist_ap_lldp_neighbor_info", "LLDP neighbour on the access point's port, always 1 (exported with -metrics-names only).", neighborLabels...),
		powerAvailable:   c.apDesc("dismist_ap_power_available_watts", "Power available to the access point (power manager)."),
		powerNeeded:      c.apDesc("dismist_ap_power_needed_watts", "Power the access point needs for full operation."),
		powerBudget:      c.apDesc("dismist_ap_power_budget_watts", "Power budget left; negative when the access point is short of power."),
		powerDraw:        c.apDesc("dismist_ap_power_draw_watts", "Power the access point measures it draws."),
		powerConstrained: c.apDesc("dismist_ap_power_constrained", "Whether the access point runs in a power-constrained mode."),
		powerSource:      c.apDesc("dismist_ap_power_source_info", "Power source the access point uses (for example lldp), always 1.", "source"),
		inactiveVLAN:     c.apDesc("dismist_ap_inactive_vlan", "A VLAN the access point is configured for but sees no traffic on, always 1.", "vlan"),
	}
}

func (c *collector) collectHealth(ch chan<- prometheus.Metric, ap apState) {
	f := c.telemetry
	t := ap.detail.Telemetry
	site, id := ap.record.SiteID, ap.record.ID
	gauge(ch, f.configured, boolValue(t.Configured), site, id)
	if t.HasClockSkew {
		gauge(ch, f.clockSkew, t.ClockSkewSeconds, site, id)
	}
	if t.ConfigTimestamp > 0 {
		gauge(ch, f.reportedConfigTS, t.ConfigTimestamp, site, id)
	}
	if t.MemoryTotalKB > 0 {
		gauge(ch, f.memoryTotal, float64(t.MemoryTotalKB)*1024, site, id)
		gauge(ch, f.memoryAvailable, float64(t.MemoryAvailableKB)*1024, site, id)
	}
	for index, value := range t.LoadAverage {
		if index >= len(loadPeriods) {
			break
		}
		gauge(ch, f.load, value, site, id, loadPeriods[index])
	}
	if len(t.CPUJiffies) > 0 {
		for _, mode := range CPUModes {
			counter(ch, f.cpu, float64(t.CPUJiffies[mode])/100, site, id, mode)
		}
	}
	if t.RWTotalKB > 0 {
		gauge(ch, f.rwSize, float64(t.RWTotalKB)*1024, site, id)
		gauge(ch, f.rwAvailable, float64(t.RWAvailableKB)*1024, site, id)
	}
	if t.CPUTemperatureCelsius != 0 {
		gauge(ch, f.temperature, t.CPUTemperatureCelsius, site, id, "cpu")
	}
	if t.AmbientTemperatureCelsius != 0 {
		gauge(ch, f.temperature, t.AmbientTemperatureCelsius, site, id, "ambient")
	}
	if t.TCPRTTSeconds > 0 {
		gauge(ch, f.rtt, t.TCPRTTSeconds, site, id, "tcp")
	}
	if t.WebSocketRTTSeconds > 0 {
		gauge(ch, f.rtt, t.WebSocketRTTSeconds, site, id, "websocket")
	}
	if t.HasAgent {
		gauge(ch, f.agentHeap, float64(t.AgentHeapBytes), site, id)
		gauge(ch, f.agentRSS, float64(t.AgentRSSBytes), site, id)
		gauge(ch, f.agentGoroutines, float64(t.AgentGoroutines), site, id)
	}
}

func (c *collector) collectUplink(ch chan<- prometheus.Metric, ap apState) {
	f := c.telemetry
	t := ap.detail.Telemetry
	site, id := ap.record.SiteID, ap.record.ID
	seen := map[string]bool{}
	for _, port := range t.Ports {
		name := strings.ToLower(port.Name)
		if !portPattern.MatchString(name) || seen[name] || len(seen) >= maxPortsPerAP {
			continue
		}
		seen[name] = true
		gauge(ch, f.portUp, boolValue(port.Up), site, id, name)
		gauge(ch, f.portFullDuplex, boolValue(port.FullDuplex), site, id, name)
		gauge(ch, f.portSpeed, float64(port.Mbps)*1e6, site, id, name)
		counter(ch, f.portRxBytes, float64(port.RxBytes), site, id, name)
		counter(ch, f.portRxPackets, float64(port.RxPackets), site, id, name)
		counter(ch, f.portTxBytes, float64(port.TxBytes), site, id, name)
		counter(ch, f.portTxPackets, float64(port.TxPackets), site, id, name)
		counter(ch, f.portRxErrors, float64(port.RxErrors), site, id, name)
		counter(ch, f.portRxFCSErrors, float64(port.RxFCSErrors), site, id, name)
		counter(ch, f.portRxDiscards, float64(port.RxDiscards), site, id, name)
		counter(ch, f.portTxError, float64(port.TxErrors), site, id, name)
	}
	lldp := map[string]bool{}
	for _, power := range t.LLDPPower {
		name := strings.ToLower(power.Port)
		if name == "" {
			name = "eth0" // 0.14 sends one LLDP_pwr without a port name
		}
		if !portPattern.MatchString(name) || lldp[name] || len(lldp) >= maxPortsPerAP {
			continue
		}
		lldp[name] = true
		gauge(ch, f.lldpAllocated, float64(power.AllocatedMilliwatts)/1000, site, id, name)
		gauge(ch, f.lldpRequested, float64(power.RequestedMilliwatts)/1000, site, id, name)
	}
	if c.names {
		neighbors := map[[4]string]bool{}
		for _, neighbor := range t.LLDPNeighbors {
			port := strings.ToLower(neighbor.Port)
			if !portPattern.MatchString(port) || len(neighbors) >= maxNeighborsPerAP {
				continue
			}
			key := [4]string{port, labelText(neighbor.ChassisID, maxLabelTextRunes), labelText(neighbor.PortID, maxLabelTextRunes), labelText(neighbor.SystemName, maxLabelTextRunes)}
			if neighbors[key] {
				continue
			}
			neighbors[key] = true
			gauge(ch, f.lldpNeighbor, 1, append([]string{site, id}, key[:]...)...)
		}
	}
	if power := t.Power; power != nil {
		gauge(ch, f.powerAvailable, float64(power.AvailableMilliwatts)/1000, site, id)
		gauge(ch, f.powerNeeded, float64(power.NeededMilliwatts)/1000, site, id)
		gauge(ch, f.powerBudget, float64(power.BudgetMilliwatts)/1000, site, id)
		gauge(ch, f.powerConstrained, boolValue(power.Constrained), site, id)
		if power.DrawMilliwatts > 0 {
			gauge(ch, f.powerDraw, float64(power.DrawMilliwatts)/1000, site, id)
		}
		if power.Source != "" {
			gauge(ch, f.powerSource, 1, site, id, keyword(power.Source))
		}
	}
	vlans := map[uint16]bool{}
	for _, vlan := range t.InactiveVLANs {
		if vlan == 0 || vlan > 4094 || vlans[vlan] || len(vlans) >= maxVLANsPerAP {
			continue
		}
		vlans[vlan] = true
		gauge(ch, f.inactiveVLAN, 1, site, id, strconv.Itoa(int(vlan)))
	}
}

func unixSeconds(at time.Time) float64 {
	return float64(at.UnixNano()) / 1e9
}
