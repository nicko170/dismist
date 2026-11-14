package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"

	"github.com/nicko170/dismist/internal/controller"
)

const (
	home = "11111111-1111-4111-8111-111111111111"
	ap1  = "02-aa-00-00-00-01"
	ap2  = "02-aa-00-00-00-02"
)

// scrape gathers every family a collector emits as text exposition.
func scrape(t *testing.T, collectors ...prometheus.Collector) string {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	for _, c := range collectors {
		registry.MustRegister(c)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(&out, family); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}

// samples returns the sample lines (no HELP or TYPE) of an exposition.
func samples(exposition string) []string {
	var lines []string
	for _, line := range strings.Split(exposition, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines
}

func expectLines(t *testing.T, exposition string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(exposition, line+"\n") {
			t.Errorf("missing %s", line)
		}
	}
}

func expectNoSeries(t *testing.T, exposition string, fragments ...string) {
	t.Helper()
	for _, line := range samples(exposition) {
		for _, fragment := range fragments {
			if strings.Contains(line, fragment) {
				t.Errorf("unexpected series %s", line)
			}
		}
	}
}

func lbl(name, labels string) string {
	return name + "{" + labels + "}"
}

func TestTelemetrySeriesFromDetail(t *testing.T) {
	store, sessions := fixture(t)
	out := scrape(t, testCollector(store, sessions, Options{}))
	a := `ap="` + ap1 + `",`
	s := `site="` + home + `"`
	expectLines(t, out,
		lbl("dismist_ap_registered", a+s)+" 1",
		lbl("dismist_ap_registered", `ap="02-aa-00-00-00-04",site="x\"\\\ny"`)+" 0",
		lbl("dismist_ap_connected_since_timestamp_seconds", a+s)+" 1.794625445e+09",
		lbl("dismist_ap_last_full_telemetry_timestamp_seconds", a+s)+" 1.794625565e+09",
		lbl("dismist_ap_config_error", a+s)+" 1",
		lbl("dismist_ap_desired_config_timestamp_seconds", a+s)+" 1.794548005856035e+09",
		lbl("dismist_ap_protocol_feature", a+`feature="pong2",`+s)+" 1",
		lbl("dismist_ap_protocol_feature", a+`feature="config_change_notify",`+s)+" 0",
		lbl("dismist_ap_boots_total", a+s)+" 6",
		lbl("dismist_ap_disconnects_total", a+s)+" 5",
		lbl("dismist_ap_config_deliveries_total", a+s)+" 6",
		lbl("dismist_ap_config_rejections_total", a+s)+" 1",
		lbl("dismist_ap_source_mismatch_total", a+s)+" 2",
		lbl("dismist_ap_auth_failures_total", a+s+`,stage="proof"`)+" 3",
		lbl("dismist_ap_frames_total", a+s+`,subject="ep-telemetry"`)+" 357",
		lbl("dismist_ap_frames_total", a+s+`,subject="get-time"`)+" 0",
		lbl("dismist_ap_telemetry_decode_errors_total", a+`section="radio",`+s)+" 2",
		lbl("dismist_ap_telemetry_decode_errors_total", a+`section="other",`+s)+" 1",
		lbl("dismist_ap_telemetry_messages_total", a+s+`,topic="ap-stats-fast"`)+" 345",
		lbl("dismist_ap_telemetry_messages_total", a+s+`,topic="other"`)+" 2",
		lbl("dismist_ap_config_pushes_total", a+`mode="notify",result="applied",`+s)+" 3",
		lbl("dismist_ap_config_pushes_total", a+`mode="other",result="other",`+s)+" 2",
		lbl("dismist_ap_config_verify_mismatch", a+s)+" 0",
		lbl("dismist_radio_planned_channel", a+`band="5",`+s)+" 149",
		// Health.
		lbl("dismist_ap_configured", a+s)+" 1",
		lbl("dismist_ap_clock_skew_seconds", a+s)+" 1.5",
		lbl("dismist_ap_reported_config_timestamp_seconds", a+s)+" 1.794548005856035e+09",
		lbl("dismist_ap_memory_total_bytes", a+s)+" 1.0565632e+09",
		lbl("dismist_ap_load_average", a+`period="5m",`+s)+" 2.626",
		lbl("dismist_ap_cpu_seconds_total", a+`mode="idle",`+s)+" 5794.75",
		lbl("dismist_ap_cpu_seconds_total", a+`mode="steal",`+s)+" 0",
		lbl("dismist_ap_rw_filesystem_available_bytes", a+s)+" 7.360512e+06",
		lbl("dismist_ap_temperature_celsius", a+`sensor="cpu",`+s)+" 53.74",
		lbl("dismist_ap_controller_rtt_seconds", a+s+`,socket="tcp"`)+" 0.0034",
		lbl("dismist_ap_agent_goroutines", a+s)+" 61",
		// Uplink and power.
		lbl("dismist_ap_port_up", a+`port="eth0",`+s)+" 1",
		lbl("dismist_ap_port_speed_bits_per_second", a+`port="eth0",`+s)+" 1e+09",
		lbl("dismist_ap_port_receive_fcs_errors_total", a+`port="eth0",`+s)+" 3",
		lbl("dismist_ap_port_up", a+`port="eth1",`+s)+" 0",
		lbl("dismist_ap_lldp_power_allocated_watts", a+`port="eth0",`+s)+" 19.5",
		lbl("dismist_ap_power_budget_watts", a+s)+" 4.3",
		lbl("dismist_ap_power_draw_watts", a+s)+" 11.8",
		lbl("dismist_ap_power_constrained", a+s)+" 0",
		lbl("dismist_ap_power_source_info", a+s+`,source="lldp"`)+" 1",
		lbl("dismist_ap_inactive_vlan", a+s+`,vlan="30"`)+" 1",
		// Radios.
		lbl("dismist_radio_info", a+`band="5",phy="ax",radio="r0",role="access",`+s)+" 1",
		lbl("dismist_radio_info", a+`band="scan",phy="unknown",radio="r2",role="scan",`+s)+" 1",
		lbl("dismist_radio_channel", a+`band="5",radio="r0",`+s)+" 149",
		lbl("dismist_radio_bandwidth_mhz", a+`band="2.4",radio="r1",`+s)+" 20",
		lbl("dismist_radio_noise_floor_dbm", a+`band="5",radio="r0",`+s)+" -94",
		lbl("dismist_radio_max_tx_power_dbm", a+`band="5",radio="r0",`+s)+" 17",
		lbl("dismist_radio_channel_utilization_ratio", a+`band="5",radio="r0",`+s+`,type="rx_other_bss"`)+" 0.02",
		lbl("dismist_radio_channel_utilization_ratio", a+`band="scan",radio="r2",`+s+`,type="all"`)+" 0.9",
		lbl("dismist_radio_channel_utilization_max_ratio", a+`band="2.4",radio="r1",`+s+`,type="all"`)+" 0.6",
		lbl("dismist_radio_beacons_total", a+`band="5",radio="r0",`+s)+" 9000",
		lbl("dismist_radio_tx_retries_total", a+`band="2.4",radio="r1",`+s)+" 300",
		// WLANs: r2 (scan) and a bad ID are skipped; r1 counts clients.
		lbl("dismist_wlan_clients", a+`radio="r0",`+s+`,wlan="net-1"`)+" 2",
		lbl("dismist_wlan_clients", a+`radio="r1",`+s+`,wlan="net-1"`)+" 2",
		lbl("dismist_wlan_eapol_invalid_mic_total", a+`radio="r0",`+s+`,wlan="net-1"`)+" 4",
		lbl("dismist_wlan_eapol_handshake_retries_total", a+`message="1",radio="r0",`+s+`,wlan="net-1"`)+" 1",
		lbl("dismist_wlan_info", s+`,wlan="net-1"`)+" 1",
		// Client aggregates: the scan radio's client is not joined.
		lbl("dismist_radio_clients_by_signal", a+`band="5",radio="r0",signal="excellent",`+s)+" 1",
		lbl("dismist_radio_clients_by_signal", a+`band="5",radio="r0",signal="poor",`+s)+" 1",
		lbl("dismist_radio_clients_by_signal", a+`band="2.4",radio="r1",signal="good",`+s)+" 1",
		lbl("dismist_radio_clients_by_signal", a+`band="2.4",radio="r1",signal="fair",`+s)+" 1",
		lbl("dismist_radio_client_rssi_dbm", a+`band="5",radio="r0",`+s+`,stat="min"`)+" -80",
		lbl("dismist_radio_client_rssi_dbm", a+`band="5",radio="r0",`+s+`,stat="mean"`)+" -67.5",
		lbl("dismist_radio_clients_by_protocol", a+`band="5",protocol="ac",radio="r0",`+s)+" 1",
		lbl("dismist_radio_clients_by_protocol", a+`band="2.4",protocol="other",radio="r1",`+s)+" 1",
		lbl("dismist_radio_client_throughput_bits_per_second", a+`band="5",direction="tx",radio="r0",`+s)+" 1000",
	)
	expectNoSeries(t, out,
		`radio="r2",site="`+home+`",signal=`, `wlan="bad id`, `radio="wlan0"`, `port="not a port"`, `vlan="0"`, `vlan="5000"`,
		`band="60"`, "dismist_client_", "dismist_ap_lldp_neighbor_info", "dismist_metrics_clients_omitted",
		`ap="`+ap2+`",band=`, // AP 2's telemetry is from before its reconnect
	)
	if strings.Contains(out, `dismist_radio_client_rssi_dbm{ap="`+ap1+`",band="scan"`) {
		t.Error("scan radio has client aggregates")
	}
}

// TestTelemetryGoesStale: three missed full frames remove every telemetry
// series, while the status series stay.
func TestTelemetryGoesStale(t *testing.T) {
	store, sessions := fixture(t)
	late := fixtureNow.Add(3 * time.Minute)
	out := scrape(t, testCollector(store, sessions, Options{Now: func() time.Time { return late }}))
	expectNoSeries(t, out, "dismist_radio_info", "dismist_radio_channel", "dismist_radio_clients", "dismist_ap_port_", "dismist_ap_temperature", "dismist_wlan_clients", "dismist_ap_configured")
	expectLines(t, out,
		lbl("dismist_ap_registered", `ap="`+ap1+`",site="`+home+`"`)+" 1",
		lbl("dismist_ap_boots_total", `ap="`+ap1+`",site="`+home+`"`)+" 6",
		lbl("dismist_ap_connected", `ap="`+ap1+`",site="`+home+`"`)+" 1",
	)
	// A disconnected AP exports none either.
	disconnected := sessions[ap1].(fakeSession)
	disconnected.status.Connected = false
	sessions[ap1] = disconnected
	out = scrape(t, testCollector(store, sessions, Options{}))
	expectNoSeries(t, out, "dismist_radio_info", "dismist_radio_channel", "dismist_ap_port_", `dismist_ap_connected_since_timestamp_seconds{ap="`+ap1)
	// The planned channel is config, not telemetry: it stays.
	expectLines(t, out, lbl("dismist_radio_planned_channel", `ap="`+ap1+`",band="5",site="`+home+`"`)+" 149")
}

func TestTelemetryFresh(t *testing.T) {
	connected := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := connected.Add(10 * time.Minute)
	for _, test := range []struct {
		name     string
		lastFull time.Time
		interval float64
		want     bool
	}{
		{"recent", now.Add(-time.Minute), 60, true},
		{"just under three intervals", now.Add(-179 * time.Second), 60, true},
		{"three intervals", now.Add(-180 * time.Second), 60, false},
		{"short interval", now.Add(-40 * time.Second), 10, false},
		{"absent interval is 60 s", now.Add(-2 * time.Minute), 0, true},
		{"absurd interval is 60 s", now.Add(-4 * time.Minute), 1e9, false},
		{"before the connection", connected.Add(-time.Second), 60, false},
		{"never", time.Time{}, 60, false},
	} {
		got := telemetryFresh(statusAt(connected), statsAt(test.lastFull), test.interval, now)
		if got != test.want {
			t.Errorf("%s: fresh = %v", test.name, got)
		}
	}
}

func TestClientModes(t *testing.T) {
	store, sessions := fixture(t)
	off := scrape(t, testCollector(store, sessions, Options{Clients: ClientsOff}))
	expectNoSeries(t, off, "dismist_radio_clients_by_", "dismist_radio_client_", "dismist_client_")
	expectLines(t, off, lbl("dismist_wlan_clients", `ap="`+ap1+`",radio="r0",site="`+home+`",wlan="net-1"`)+" 2")

	a := `ap="` + ap1 + `",`
	s := `site="` + home + `"`
	perClient := scrape(t, testCollector(store, sessions, Options{Clients: ClientsPerClient, ClientLimit: 2}))
	expectLines(t, perClient,
		"dismist_metrics_clients_omitted 1",
		lbl("dismist_client_rssi_dbm", a+`client="02-aa-00-00-10-01",`+s)+" -55",
		lbl("dismist_client_snr_db", a+`client="02-aa-00-00-10-01",`+s)+" 39",
		lbl("dismist_client_tx_bitrate_bps", a+`client="02-aa-00-00-10-01",`+s)+" 8.1e+06",
		lbl("dismist_client_receive_bytes_total", a+`client="02-aa-00-00-10-01",`+s)+" 1.295567e+06",
		lbl("dismist_client_connected_seconds", a+`client="02-aa-00-00-10-01",`+s)+" 600",
		lbl("dismist_client_info", a+`client="02-aa-00-00-10-01",protocol="ax",radio="r0",`+s+`,wlan="net-1"`)+" 1",
		lbl("dismist_client_info", a+`client="02-aa-00-00-10-02",protocol="ac",radio="r0",`+s+`,wlan="net-1"`)+" 1",
	)
	// Clients are cut in MAC order: 10-03 is the third; the scan radio's
	// client and the bad MAC are never counted.
	expectNoSeries(t, perClient, `client="02-aa-00-00-10-03"`, `client="02-aa-00-00-10-04"`, `not-a-mac`, `name="phone"`)

	named := scrape(t, testCollector(store, sessions, Options{Clients: ClientsPerClient, ClientLimit: 100, Names: true}))
	expectLines(t, named,
		"dismist_metrics_clients_omitted 0",
		lbl("dismist_client_info", a+`client="02-aa-00-00-10-01",name="phone",protocol="ax",radio="r0",`+s+`,wlan="net-1"`)+" 1",
		lbl("dismist_client_info", a+`client="02-aa-00-00-10-03",name="",protocol="other",radio="r1",`+s+`,wlan="net-1"`)+" 1",
	)
}

func TestNamesModeAddsOnlyNames(t *testing.T) {
	store, sessions := fixture(t)
	plain := scrape(t, testCollector(store, sessions, Options{}))
	named := scrape(t, testCollector(store, sessions, Options{Names: true}))
	expectNoSeries(t, plain, "dismist_ap_lldp_neighbor_info", "switch-1")
	expectLines(t, named,
		lbl("dismist_ap_lldp_neighbor_info", `ap="`+ap1+`",chassis_id="02-aa-00-00-30-01",port="eth0",port_id="ge-0/0/7",site="`+home+`",system_name="switch-1.example"`)+" 1",
	)
}

// TestDefaultModeLeaksNothing scrapes connected APs with clients and WLANs
// and checks no name, address, SSID, BSSID, client MAC, host name or PSK
// appears in any series.
func TestDefaultModeLeaksNothing(t *testing.T) {
	store, sessions := fixture(t)
	c := testCollector(store, sessions, Options{
		Certificates: []Certificate{{Listener: "ap-wss", Role: "leaf", CN: "ap.example.com", NotAfter: fixtureNow}},
	})
	body := strings.ToLower(strings.Join(samples(scrape(t, c)), "\n"))
	if !strings.Contains(body, "dismist_radio_channel{") {
		t.Fatal("the fixture exported no telemetry; the check would prove nothing")
	}
	for _, sensitive := range []string{
		"example home", "lounge ap", "office ap", "192.0.2.5", "198.51.100.", "example",
		"02-aa-00-00-10-0", "02:aa:00:00:10", "02-aa-00-00-20-01", "02-aa-00-00-30-01", "phone", "switch-1",
		"correct horse", "ssid", "psk", "mac", "ap.example.com",
	} {
		if strings.Contains(body, sensitive) {
			t.Errorf("scrape leaked %q", sensitive)
		}
	}
}

func TestControllerSeries(t *testing.T) {
	store, sessions := fixture(t)
	success := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	c := testCollector(store, sessions, Options{
		Build: BuildInfo{Version: "1.2.3", Revision: "abcdef", GoVersion: "go1.27.1"},
		StoreUsage: func() (StoreUsage, bool) {
			return StoreUsage{SizeBytes: 65536, FreeBytes: 1 << 30, HasFree: true}, true
		},
		MigrationState: func() (string, bool) { return "legacy", true },
		Backups: func() (BackupStatus, bool) {
			return BackupStatus{LastSuccess: success, Bytes: 4096, Failures: map[string]uint64{"space": 2}}, true
		},
		Certificates: []Certificate{
			{Listener: "ap-wss", Role: "leaf", CN: "ap.example.com", NotBefore: success, NotAfter: success.Add(90 * 24 * time.Hour)},
			{Listener: "ap-wss", Role: "intermediate", CN: "Example CA", NotAfter: success.Add(365 * 24 * time.Hour)},
		},
	})
	out := scrape(t, c)
	expectLines(t, out,
		`dismist_build_info{goversion="go1.27.1",revision="abcdef",version="1.2.3"} 1`,
		"dismist_store_size_bytes 65536",
		"dismist_store_free_bytes 1.073741824e+09",
		`dismist_schema_migration_state{state="legacy"} 1`,
		`dismist_schema_migration_state{state="committed"} 0`,
		"dismist_backup_last_success_timestamp_seconds 1.794528e+09",
		"dismist_backup_bytes 4096",
		`dismist_backup_failures_total{reason="space"} 2`,
		`dismist_backup_failures_total{reason="io"} 0`,
		`dismist_tls_certificate_not_after_timestamp_seconds{cn="",listener="ap-wss",role="leaf"} 1.802304e+09`,
		`dismist_tls_certificate_not_after_timestamp_seconds{cn="Example CA",listener="ap-wss",role="intermediate"} 1.826064e+09`,
		`dismist_ap_ws_routes_total{result="unknown_ap"} 0`,
	)
	named := scrape(t, testCollector(store, sessions, Options{Names: true, Certificates: []Certificate{{Listener: "ap-wss", Role: "leaf", CN: "ap.example.com", NotAfter: success}}}))
	expectLines(t, named, `dismist_tls_certificate_not_after_timestamp_seconds{cn="ap.example.com",listener="ap-wss",role="leaf"} 1.794528e+09`)
}

func statusAt(connected time.Time) controller.Status {
	return controller.Status{Connected: true, ConnectedAt: connected}
}

func statsAt(lastFull time.Time) controller.TelemetrySnapshot {
	return controller.TelemetrySnapshot{LastFull: lastFull}
}
