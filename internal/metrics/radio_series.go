package metrics

import (
	"math"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// radioFamilies are the per-radio series (labels site, ap, radio, band).
type radioFamilies struct {
	info, channel, bandwidth, noise, maxTxPower, txPower *prometheus.Desc
	clients, missing, utilization, utilizationMax        *prometheus.Desc
	radar, cacState                                      *prometheus.Desc
	counters                                             []radioCounter
}

type radioCounter struct {
	desc  *prometheus.Desc
	value func(RadioCounters) uint64
}

// radioLabels are one exported radio's label values.
type radioLabels struct {
	radio, band string
	access      bool
	noise       int64
}

func newRadioFamilies(c *collector) radioFamilies {
	labels := []string{"radio", "band"}
	desc := func(name, help string, extra ...string) *prometheus.Desc {
		return c.apDesc(name, help, append(append([]string(nil), labels...), extra...)...)
	}
	count := func(name, help string, value func(RadioCounters) uint64) radioCounter {
		return radioCounter{desc(name, help), value}
	}
	return radioFamilies{
		info:           c.apDesc("dismist_radio_info", "Radio of the access point, always 1, with its PHY and role (access radios serve clients; the scan radio has band=\"scan\").", "radio", "band", "phy", "role"),
		channel:        desc("dismist_radio_channel", "Primary channel the radio is on."),
		bandwidth:      desc("dismist_radio_bandwidth_mhz", "Channel width the radio uses."),
		noise:          desc("dismist_radio_noise_floor_dbm", "Noise floor the radio measures."),
		maxTxPower:     desc("dismist_radio_max_tx_power_dbm", "Transmit power the radio runs at (its configured maximum)."),
		txPower:        desc("dismist_radio_tx_power_dbm", "Current transmit power, when the access point reports one."),
		clients:        desc("dismist_radio_clients", "Clients associated on the radio."),
		missing:        desc("dismist_radio_missing", "Whether the access point reports the radio missing."),
		utilization:    desc("dismist_radio_channel_utilization_ratio", "Mean channel utilisation over the access point's last measurement period, by type (all, tx, rx_in_bss, rx_other_bss, unknown_wifi, non_wifi, undecodable_wifi).", "type"),
		utilizationMax: desc("dismist_radio_channel_utilization_max_ratio", "Peak channel utilisation over the access point's last measurement period.", "type"),
		radar:          desc("dismist_radio_dfs_radar_detected", "Whether the radio has detected radar (DFS)."),
		cacState:       desc("dismist_radio_dfs_cac_state", "The radio's DFS channel availability check state as the access point reports it (0 when none)."),
		counters: []radioCounter{
			count("dismist_radio_tx_retries_total", "Frames the radio retransmitted.", func(r RadioCounters) uint64 { return r.TxRetries }),
			count("dismist_radio_tx_failed_total", "Frames the radio failed to transmit.", func(r RadioCounters) uint64 { return r.TxFailed }),
			count("dismist_radio_rx_decrypt_failures_total", "Received frames the radio could not decrypt.", func(r RadioCounters) uint64 { return r.RxDecryptFailures }),
			count("dismist_radio_reinits_total", "Radio re-initialisations.", func(r RadioCounters) uint64 { return r.Reinits }),
			count("dismist_radio_tx_stalls_total", "Transmit stalls on the radio.", func(r RadioCounters) uint64 { return r.TxStalls }),
			count("dismist_radio_beacons_total", "Beacons the radio sent.", func(r RadioCounters) uint64 { return r.Beacons }),
			count("dismist_radio_rx_bad_fcs_total", "Received frames with a bad checksum.", func(r RadioCounters) uint64 { return r.RxBadFCS }),
			count("dismist_radio_rx_bad_plcp_total", "Received frames with a bad PLCP header.", func(r RadioCounters) uint64 { return r.RxBadPLCP }),
			count("dismist_radio_rx_glitches_total", "Receive glitches (energy the radio could not decode).", func(r RadioCounters) uint64 { return r.RxGlitches }),
			count("dismist_radio_probe_requests_total", "Probe requests the radio received; a rough measure of devices nearby.", func(r RadioCounters) uint64 { return r.ProbeRequests }),
		},
	}
}

// exportedRadio returns the labels a radio is exported with, or false when
// its name is not a radio name.
func exportedRadio(radio Radio) (radioLabels, bool) {
	name := strings.ToLower(radio.Dev)
	if !radioPattern.MatchString(name) {
		return radioLabels{}, false
	}
	labels := radioLabels{radio: name, access: true, noise: radio.NoiseFloorDBm}
	switch {
	case strings.EqualFold(radio.Role, "scan"):
		labels.band, labels.access = "scan", false
	case radio.Band == "2.4" || radio.Band == "5" || radio.Band == "6":
		labels.band = radio.Band
	default:
		labels.band = "unknown"
	}
	return labels, true
}

// collectRadios exports an AP's radios and returns them by name.
func (c *collector) collectRadios(ch chan<- prometheus.Metric, ap apState) map[string]radioLabels {
	f := c.radio
	site, id := ap.record.SiteID, ap.record.ID
	radios := map[string]radioLabels{}
	for _, radio := range ap.detail.Telemetry.Radios {
		labels, ok := exportedRadio(radio)
		if !ok || len(radios) >= maxRadiosPerAP {
			continue
		}
		if _, seen := radios[labels.radio]; seen {
			continue
		}
		radios[labels.radio] = labels
		role := "access"
		if !labels.access {
			role = "scan"
		}
		phy := "unknown"
		if radio.PHY != "" {
			phy = keyword(radio.PHY)
		}
		gauge(ch, f.info, 1, site, id, labels.radio, labels.band, phy, role)
		l := []string{site, id, labels.radio, labels.band}
		gauge(ch, f.channel, float64(radio.Channel), l...)
		gauge(ch, f.bandwidth, float64(radio.BandwidthMHz), l...)
		if radio.NoiseFloorDBm != 0 {
			gauge(ch, f.noise, float64(radio.NoiseFloorDBm), l...)
		}
		if radio.MaxTxPowerDBm != 0 {
			gauge(ch, f.maxTxPower, float64(radio.MaxTxPowerDBm), l...)
		}
		if radio.TxPowerDBm != 0 {
			gauge(ch, f.txPower, float64(radio.TxPowerDBm), l...)
		}
		gauge(ch, f.clients, float64(radio.Clients), l...)
		gauge(ch, f.missing, boolValue(radio.Missing), l...)
		if radio.HasUtilization {
			for _, kind := range UtilizationTypes {
				gauge(ch, f.utilization, ratio(radio.Utilization[kind]), append(l, kind)...)
			}
			gauge(ch, f.utilizationMax, ratio(radio.UtilizationMax), append(l, "all")...)
		}
		gauge(ch, f.radar, boolValue(radio.RadarDetected), l...)
		gauge(ch, f.cacState, float64(radio.CACState), l...)
		for _, metric := range f.counters {
			counter(ch, metric.desc, float64(metric.value(radio.Counters)), l...)
		}
	}
	return radios
}

// ratio clamps a utilisation to 0..1.
func ratio(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	return math.Min(value, 1)
}

// wlanFamilies are the per-WLAN (VAP) series (labels site, ap, wlan, radio).
type wlanFamilies struct {
	clients, multicastPackets, multicastBytes, invalidMIC, handshakeRetries *prometheus.Desc
}

func newWLANFamilies(c *collector) wlanFamilies {
	desc := func(name, help string, extra ...string) *prometheus.Desc {
		return c.apDesc(name, help, append([]string{"wlan", "radio"}, extra...)...)
	}
	return wlanFamilies{
		clients:          desc("dismist_wlan_clients", "Clients associated to the WLAN on the radio; the wlan label is the network ID."),
		multicastPackets: desc("dismist_wlan_tx_multicast_packets_total", "Multicast packets the WLAN sent on the radio."),
		multicastBytes:   desc("dismist_wlan_tx_multicast_bytes_total", "Multicast bytes the WLAN sent on the radio."),
		invalidMIC:       desc("dismist_wlan_eapol_invalid_mic_total", "WPA handshakes that failed the MIC check: a device with the wrong passphrase, or someone guessing it."),
		handshakeRetries: desc("dismist_wlan_eapol_handshake_retries_total", "WPA handshake message retransmissions by message (1, 3).", "message"),
	}
}

func (c *collector) collectWLANs(ch chan<- prometheus.Metric, ap apState, radios map[string]radioLabels) {
	f := c.wlan
	site, id := ap.record.SiteID, ap.record.ID
	t := ap.detail.Telemetry
	counted := map[[2]string]uint64{}
	for _, client := range t.Clients {
		counted[[2]string{client.WLANID, radioName(client.RadioIndex)}]++
	}
	seen := map[[2]string]bool{}
	for _, wlan := range t.WLANs {
		radio := radioName(wlan.RadioIndex)
		if radio == "" || !wlanIDPattern.MatchString(wlan.ID) || len(seen) >= maxWLANsPerAP {
			continue
		}
		if labels, ok := radios[radio]; ok && !labels.access {
			continue
		}
		key := [2]string{wlan.ID, radio}
		if seen[key] {
			continue
		}
		seen[key] = true
		clients := wlan.Clients
		if !wlan.HasClients {
			clients = counted[key]
		}
		l := []string{site, id, wlan.ID, radio}
		gauge(ch, f.clients, float64(clients), l...)
		counter(ch, f.multicastPackets, float64(wlan.TxMulticastPackets), l...)
		counter(ch, f.multicastBytes, float64(wlan.TxMulticastBytes), l...)
		counter(ch, f.invalidMIC, float64(wlan.EapolInvalidMIC), l...)
		counter(ch, f.handshakeRetries, float64(wlan.EapolMsg1Retries), append(l, "1")...)
		counter(ch, f.handshakeRetries, float64(wlan.EapolMsg3Retries), append(l, "3")...)
	}
}

// clientFamilies are the aggregate client series per access radio and the
// opt-in per-client series.
type clientFamilies struct {
	bySignal, rssi, byProtocol, throughput *prometheus.Desc

	clientRSSI, clientSNR, clientTxRate, clientRxRate *prometheus.Desc
	clientRxBytes, clientTxBytes, clientTxRetries     *prometheus.Desc
	clientConnected, clientInfo, omitted              *prometheus.Desc
}

func newClientFamilies(c *collector) clientFamilies {
	radio := func(name, help string, extra ...string) *prometheus.Desc {
		return c.apDesc(name, help, append([]string{"radio", "band"}, extra...)...)
	}
	client := func(name, help string, extra ...string) *prometheus.Desc {
		return c.apDesc(name, help, append([]string{"client"}, extra...)...)
	}
	infoLabels := []string{"radio", "wlan", "protocol"}
	if c.names {
		infoLabels = append(infoLabels, "name")
	}
	return clientFamilies{
		bySignal:        radio("dismist_radio_clients_by_signal", "Clients on the access radio by signal: excellent (-60 dBm or better), good (to -67), fair (to -75), poor (below -75).", "signal"),
		rssi:            radio("dismist_radio_client_rssi_dbm", "Client signal strength on the access radio: the weakest (min) and the mean.", "stat"),
		byProtocol:      radio("dismist_radio_clients_by_protocol", "Clients on the access radio by Wi-Fi generation (be, ax, ac, n, a, g, b, other).", "protocol"),
		throughput:      radio("dismist_radio_client_throughput_bits_per_second", "Current client throughput on the access radio, from the access point's side: tx to clients, rx from clients (units unverified, design §9 Q7).", "direction"),
		clientRSSI:      client("dismist_client_rssi_dbm", "Client signal strength (per-client mode)."),
		clientSNR:       client("dismist_client_snr_db", "Client signal-to-noise ratio: its RSSI minus its radio's noise floor (per-client mode)."),
		clientTxRate:    client("dismist_client_tx_bitrate_bps", "Link rate to the client (per-client mode; units unverified, design §9 Q7)."),
		clientRxRate:    client("dismist_client_rx_bitrate_bps", "Link rate from the client (per-client mode; units unverified, design §9 Q7)."),
		clientRxBytes:   client("dismist_client_receive_bytes_total", "Bytes received from the client this association (per-client mode)."),
		clientTxBytes:   client("dismist_client_transmit_bytes_total", "Unicast bytes sent to the client this association (per-client mode; may stay flat, design §9 Q7)."),
		clientTxRetries: client("dismist_client_tx_retries_total", "Retransmissions to the client this association (per-client mode)."),
		clientConnected: client("dismist_client_connected_seconds", "How long the client has been associated (per-client mode)."),
		clientInfo:      client("dismist_client_info", "Associated client, always 1, with its radio, WLAN and protocol; the name label (its host name) is present with -metrics-names (per-client mode).", infoLabels...),
		omitted:         c.newDesc("dismist_metrics_clients_omitted", "Associated clients left out of the per-client series by -metrics-client-limit."),
	}
}

// clientSample is one client kept for the per-client series.
type clientSample struct {
	site, ap, mac string
	radio         radioLabels
	client        Client
}

// collectClientAggregates exports the per-radio client aggregates and
// returns the clients for the per-client series.
func (c *collector) collectClientAggregates(ch chan<- prometheus.Metric, ap apState, radios map[string]radioLabels) []clientSample {
	if c.clients == ClientsOff {
		return nil
	}
	f := c.client
	site, id := ap.record.SiteID, ap.record.ID
	type aggregate struct {
		signal     map[string]int
		protocol   map[string]int
		rssiMin    int64
		rssiSum    int64
		rssiCount  int
		tx, rx     uint64
		hasClients bool
	}
	byRadio := map[string]*aggregate{}
	for name, labels := range radios {
		if labels.access {
			byRadio[name] = &aggregate{signal: map[string]int{}, protocol: map[string]int{}}
		}
	}
	var samples []clientSample
	for _, client := range ap.detail.Telemetry.Clients {
		name := radioName(client.RadioIndex)
		agg, ok := byRadio[name]
		if !ok {
			continue // unknown or scan radio: no join (design §5.3)
		}
		agg.hasClients = true
		agg.protocol[oneOf(strings.ToLower(client.Protocol), Protocols)]++
		agg.tx += client.TxBitsPerSecond
		agg.rx += client.RxBitsPerSecond
		if rssi := client.RSSIDBm; rssi < 0 {
			agg.signal[signalBucket(rssi)]++
			if agg.rssiCount == 0 || rssi < agg.rssiMin {
				agg.rssiMin = rssi
			}
			agg.rssiSum += rssi
			agg.rssiCount++
		}
		if c.clients == ClientsPerClient {
			if mac := clientLabel(client.MAC); mac != "" {
				samples = append(samples, clientSample{site: site, ap: id, mac: mac, radio: radios[name], client: client})
			}
		}
	}
	for _, name := range sortedKeys(byRadio) {
		agg := byRadio[name]
		l := []string{site, id, name, radios[name].band}
		for _, bucket := range SignalBuckets {
			gauge(ch, f.bySignal, float64(agg.signal[bucket]), append(l, bucket)...)
		}
		for _, protocol := range Protocols {
			gauge(ch, f.byProtocol, float64(agg.protocol[protocol]), append(l, protocol)...)
		}
		if agg.rssiCount > 0 {
			gauge(ch, f.rssi, float64(agg.rssiMin), append(l, "min")...)
			gauge(ch, f.rssi, float64(agg.rssiSum)/float64(agg.rssiCount), append(l, "mean")...)
		}
		gauge(ch, f.throughput, float64(agg.tx), append(l, "tx")...)
		gauge(ch, f.throughput, float64(agg.rx), append(l, "rx")...)
	}
	return samples
}

// signalBucket maps an RSSI to its dismist_radio_clients_by_signal bucket.
func signalBucket(rssi int64) string {
	switch {
	case rssi >= -60:
		return "excellent"
	case rssi >= -67:
		return "good"
	case rssi >= -75:
		return "fair"
	}
	return "poor"
}

// collectPerClient exports the per-client series (per-client mode only),
// at most clientLimit clients in MAC order across every AP.
func (c *collector) collectPerClient(ch chan<- prometheus.Metric, samples []clientSample) {
	if c.clients != ClientsPerClient {
		return
	}
	f := c.client
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].mac != samples[j].mac {
			return samples[i].mac < samples[j].mac
		}
		return samples[i].ap < samples[j].ap
	})
	// A client seen on two APs during a roam is kept once, on the first.
	unique := samples[:0]
	seen := map[string]bool{}
	for _, sample := range samples {
		if !seen[sample.mac] {
			seen[sample.mac] = true
			unique = append(unique, sample)
		}
	}
	limit := c.clientLimit
	omitted := 0
	if len(unique) > limit {
		omitted = len(unique) - limit
		unique = unique[:limit]
	}
	gauge(ch, f.omitted, float64(omitted))
	for _, sample := range unique {
		client := sample.client
		l := []string{sample.site, sample.ap, sample.mac}
		if client.RSSIDBm < 0 {
			gauge(ch, f.clientRSSI, float64(client.RSSIDBm), l...)
			if sample.radio.noise < 0 && client.RSSIDBm > sample.radio.noise {
				gauge(ch, f.clientSNR, float64(client.RSSIDBm-sample.radio.noise), l...)
			}
		}
		gauge(ch, f.clientTxRate, client.TxRateMbps*1e6, l...)
		gauge(ch, f.clientRxRate, client.RxRateMbps*1e6, l...)
		counter(ch, f.clientRxBytes, float64(client.RxBytes), l...)
		counter(ch, f.clientTxBytes, float64(client.TxBytes), l...)
		counter(ch, f.clientTxRetries, float64(client.TxRetries), l...)
		gauge(ch, f.clientConnected, float64(client.ConnectedSeconds), l...)
		wlan := ""
		if wlanIDPattern.MatchString(client.WLANID) {
			wlan = client.WLANID
		}
		info := append(l, sample.radio.radio, wlan, oneOf(strings.ToLower(client.Protocol), Protocols))
		if c.names {
			info = append(info, labelText(client.Hostname, maxLabelTextRunes))
		}
		gauge(ch, f.clientInfo, 1, info...)
	}
}
