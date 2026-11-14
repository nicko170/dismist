package metrics

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nicko170/dismist/internal/model"
)

// legacyExposition is the hand-rolled exporter dismist shipped before the
// switch to client_golang (internal/metrics/handler.go on ap-identity), kept
// verbatim apart from taking its inputs as arguments. It is the oracle for
// "same metric names, types, labels and values".
func legacyExposition(state model.State, servers map[string]session) string {
	sort.Slice(state.Sites, func(i, j int) bool { return state.Sites[i].ID < state.Sites[j].ID })
	sort.Slice(state.APs, func(i, j int) bool { return state.APs[i].ID < state.APs[j].ID })

	var out bytes.Buffer
	legacyWriteMetric(&out, "dismist_sites", "Registered sites.", "gauge", strconv.Itoa(len(state.Sites)))
	legacyWriteMetric(&out, "dismist_aps", "Registered access points.", "gauge", strconv.Itoa(len(state.APs)))

	siteAPs := make(map[string]int, len(state.Sites))
	siteClients := make(map[string]int, len(state.Sites))
	aps := make([]apSample, 0, len(state.APs))
	for _, ap := range state.APs {
		siteAPs[ap.SiteID]++
		sample := apSample{site: ap.SiteID, id: ap.ID, desired: ap.ConfigRevision}
		if server := servers[ap.ID]; server != nil {
			status := server.Status()
			stats := server.TelemetrySnapshot()
			sample.connected = status.Connected
			sample.connections = status.ConnectionCount
			sample.applied = status.AppliedRevision
			sample.lastSeen = status.LastSeen.Unix()
			if status.LastSeen.IsZero() {
				sample.lastSeen = 0
			}
			sample.fullFrames = stats.FullFrames
			sample.fastFrames = stats.FastFrames
			sample.uptime = stats.UptimeSeconds
			sample.clients = liveClientCount(status, stats)
			siteClients[ap.SiteID] += sample.clients
		}
		aps = append(aps, sample)
	}
	legacyWriteSiteMetric(&out, "dismist_site_aps", "Registered access points per site.", state.Sites, siteAPs)
	legacyWriteSiteMetric(&out, "dismist_site_associated_clients", "Associated clients on connected access points per site.", state.Sites, siteClients)
	legacyWriteAPMetric(&out, "dismist_ap_connected", "Whether the access point is connected to the controller.", "gauge", aps, func(a apSample) string {
		if a.connected {
			return "1"
		}
		return "0"
	})
	legacyWriteAPMetric(&out, "dismist_ap_associated_clients", "Associated clients while the access point is connected.", "gauge", aps, func(a apSample) string { return strconv.Itoa(a.clients) })
	legacyWriteAPMetric(&out, "dismist_ap_connections_total", "Authenticated access point connections since controller start.", "counter", aps, func(a apSample) string { return strconv.FormatUint(a.connections, 10) })
	legacyWriteAPMetric(&out, "dismist_ap_full_frames_total", "Full telemetry frames received since controller start.", "counter", aps, func(a apSample) string { return strconv.FormatUint(a.fullFrames, 10) })
	legacyWriteAPMetric(&out, "dismist_ap_fast_frames_total", "Fast telemetry frames received since controller start.", "counter", aps, func(a apSample) string { return strconv.FormatUint(a.fastFrames, 10) })
	legacyWriteAPMetric(&out, "dismist_ap_uptime_seconds", "Access point uptime reported by its latest telemetry.", "gauge", aps, func(a apSample) string { return strconv.FormatUint(a.uptime, 10) })
	legacyWriteAPMetric(&out, "dismist_ap_desired_revision", "Desired access point configuration revision.", "gauge", aps, func(a apSample) string { return strconv.FormatUint(a.desired, 10) })
	legacyWriteAPMetric(&out, "dismist_ap_applied_revision", "Last access point configuration revision reported as applied.", "gauge", aps, func(a apSample) string { return strconv.FormatUint(a.applied, 10) })
	legacyWriteAPMetric(&out, "dismist_ap_last_seen_timestamp_seconds", "Unix time of the last access point message, or zero if never seen.", "gauge", aps, func(a apSample) string { return strconv.FormatInt(a.lastSeen, 10) })
	return out.String()
}

func legacyWriteSiteMetric(out *bytes.Buffer, name, help string, sites []model.Site, values map[string]int) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	for _, site := range sites {
		fmt.Fprintf(out, "%s{site=\"%s\"} %d\n", name, legacyEscapeLabel(site.ID), values[site.ID])
	}
}

func legacyWriteAPMetric(out *bytes.Buffer, name, help, kind string, aps []apSample, value func(apSample) string) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	for _, ap := range aps {
		fmt.Fprintf(out, "%s{site=\"%s\",ap=\"%s\"} %s\n", name, legacyEscapeLabel(ap.site), legacyEscapeLabel(ap.id), value(ap))
	}
}

func legacyWriteMetric(out *bytes.Buffer, name, help, kind, value string) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s %s\n%s %s\n", name, help, name, kind, name, value)
}

var legacyLabelEscaper = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")

func legacyEscapeLabel(value string) string { return legacyLabelEscaper.Replace(value) }
