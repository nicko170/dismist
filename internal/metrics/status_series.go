package metrics

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nicko170/dismist/internal/controller"
)

// statusFamilies are per-AP series exported whatever the telemetry's age:
// registration, connection and config state and the protocol counters
// (design §6.1 "Status series are always emitted").
type statusFamilies struct {
	registered, connectedSince, lastFull, lastFast *prometheus.Desc
	configError, desiredConfigTS, protocolFeature  *prometheus.Desc
	boots, disconnects, deliveries, rejections     *prometheus.Desc
	sourceMismatch, authFailures, frames           *prometheus.Desc
	decodeErrors, messages, pushes, verifyMismatch *prometheus.Desc
	plannedChannel                                 *prometheus.Desc
}

func newStatusFamilies(c *collector) statusFamilies {
	return statusFamilies{
		registered:      c.apDesc("dismist_ap_registered", "Whether the access point has a control session, so it can connect (it has a secret and its record compiles or has a delivered config)."),
		connectedSince:  c.apDesc("dismist_ap_connected_since_timestamp_seconds", "Unix time the access point's current connection authenticated; absent while it is disconnected."),
		lastFull:        c.apDesc("dismist_ap_last_full_telemetry_timestamp_seconds", "Unix time of the access point's last decoded full telemetry frame (about once a minute)."),
		lastFast:        c.apDesc("dismist_ap_last_fast_telemetry_timestamp_seconds", "Unix time of the access point's last decoded fast telemetry frame (about every 2 seconds)."),
		configError:     c.apDesc("dismist_ap_config_error", "Whether the access point reported an error applying its last config (it keeps running its previous config)."),
		desiredConfigTS: c.apDesc("dismist_ap_desired_config_timestamp_seconds", "Timestamp of the config dismist serves the access point; compare with dismist_ap_reported_config_timestamp_seconds."),
		protocolFeature: c.apDesc("dismist_ap_protocol_feature", "Whether a protocol feature (a reply or push dismist can send) is switched on for the access point.", "feature"),
		boots:           c.apDesc("dismist_ap_boots_total", "Boot count the access point reports (field 71, or its last hello); an increase is a reboot."),
		disconnects:     c.apDesc("dismist_ap_disconnects_total", "Authenticated access point sessions that ended since the session was registered."),
		deliveries:      c.apDesc("dismist_ap_config_deliveries_total", "Configs sent to the access point in reply to ep-config."),
		rejections:      c.apDesc("dismist_ap_config_rejections_total", "Configs the access point reported it could not apply."),
		sourceMismatch:  c.apDesc("dismist_ap_source_mismatch_total", "Connections naming this pinned access point from an address other than its pin."),
		authFailures:    c.apDesc("dismist_ap_auth_failures_total", "Failed authentications of this access point by stage (hello, proof); usually a wrong or rotated AP secret.", "stage"),
		frames:          c.apDesc("dismist_ap_frames_total", "Frames received from the access point by subject (fixed list plus other).", "subject"),
		decodeErrors:    c.apDesc("dismist_ap_telemetry_decode_errors_total", "Telemetry fields or sections skipped because they did not decode, by section; a rise usually means a firmware schema change.", "section"),
		messages:        c.apDesc("dismist_ap_telemetry_messages_total", "Telemetry messages received from the access point by topic (fixed list plus other).", "topic"),
		pushes:          c.apDesc("dismist_ap_config_pushes_total", "Config pushes to the access point by mode (notify, reconnect) and result (applied, rejected, fallback, timeout, stale).", "mode", "result"),
		verifyMismatch:  c.apDesc("dismist_ap_config_verify_mismatch", "Whether the access point's telemetry disagrees with the config last delivered to it (post-delivery verification)."),
		plannedChannel:  c.apDesc("dismist_radio_planned_channel", "Channel dismist wants the access radio on: the static per-AP channel or the site's channel plan.", "band"),
	}
}

func (c *collector) collectStatus(ch chan<- prometheus.Metric, ap apState) {
	f := c.status
	site, id := ap.record.SiteID, ap.record.ID
	if ap.session == nil {
		gauge(ch, f.registered, 0, site, id)
		return
	}
	gauge(ch, f.registered, 1, site, id)
	status, stats, detail := ap.status, ap.stats, ap.detail
	if status.Connected && !status.ConnectedAt.IsZero() {
		gauge(ch, f.connectedSince, unixSeconds(status.ConnectedAt), site, id)
	}
	if !stats.LastFull.IsZero() {
		gauge(ch, f.lastFull, unixSeconds(stats.LastFull), site, id)
	}
	if !stats.LastFast.IsZero() {
		gauge(ch, f.lastFast, unixSeconds(stats.LastFast), site, id)
	}
	gauge(ch, f.configError, boolValue(status.LastError != ""), site, id)
	if timestamp := desiredConfigTimestamp(ap.session); timestamp > 0 {
		gauge(ch, f.desiredConfigTS, timestamp, site, id)
	}
	if source, ok := ap.session.(featuresSession); ok {
		features := source.Features()
		for _, feature := range controller.AllFeatures() {
			gauge(ch, f.protocolFeature, boolValue(features.Has(feature)), site, id, string(feature))
		}
	}
	if boots := max(status.Hello.BootCount, detail.Telemetry.BootCount); boots > 0 {
		counter(ch, f.boots, float64(boots), site, id)
	}
	if source, ok := ap.session.(countersSession); ok {
		counters := source.Counters()
		counter(ch, f.disconnects, float64(counters.Disconnects), site, id)
		counter(ch, f.deliveries, float64(counters.ConfigDeliveries), site, id)
		counter(ch, f.rejections, float64(counters.ConfigRejections), site, id)
		counter(ch, f.sourceMismatch, float64(counters.SourceMismatches), site, id)
		for _, stage := range []string{controller.AuthStageHello, controller.AuthStageProof} {
			counter(ch, f.authFailures, float64(counters.AuthFailures[stage]), site, id, stage)
		}
		subjects := controller.FrameSubjects()
		for _, subject := range subjects {
			counter(ch, f.frames, float64(counters.Frames[subject]), site, id, subject)
		}
		decode := countedKeys(counters.DecodeErrors, keyword)
		// The hello section is always present, so dashboards and rate() see
		// the family before the first error.
		if _, ok := decode["hello"]; !ok {
			decode["hello"] = 0
		}
		for _, section := range sortedKeys(decode) {
			counter(ch, f.decodeErrors, float64(decode[section]), site, id, section)
		}
	}
	topics := countedKeys(detail.Topics, func(topic string) string {
		return oneOf(trimTopic(topic), TelemetryTopics)
	})
	for _, topic := range sortedKeys(topics) {
		counter(ch, f.messages, float64(topics[topic]), site, id, topic)
	}
	pushes := map[[2]string]uint64{}
	for _, push := range detail.Pushes {
		pushes[[2]string{oneOf(push.Mode, pushModes), oneOf(push.Result, pushResults)}] += push.Count
	}
	for _, key := range sortedPairs(pushes) {
		counter(ch, f.pushes, float64(pushes[key]), site, id, key[0], key[1])
	}
	if detail.Verified {
		gauge(ch, f.verifyMismatch, boolValue(detail.VerifyMismatch), site, id)
	}
	for _, band := range sortedKeys(detail.PlannedChannels) {
		if channel := detail.PlannedChannels[band]; channel > 0 && slices.Contains([]string{"2.4", "5", "6"}, band) {
			gauge(ch, f.plannedChannel, float64(channel), site, id, band)
		}
	}
}

// trimTopic strips the trailing "-" of PACE topic names
// ("ep-config-applied-").
func trimTopic(topic string) string {
	for len(topic) > 0 && topic[len(topic)-1] == '-' {
		topic = topic[:len(topic)-1]
	}
	return topic
}

func sortedPairs(values map[[2]string]uint64) [][2]string {
	keys := make([][2]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b [2]string) int {
		if a[0] != b[0] {
			if a[0] < b[0] {
				return -1
			}
			return 1
		}
		switch {
		case a[1] < b[1]:
			return -1
		case a[1] > b[1]:
			return 1
		}
		return 0
	})
	return keys
}
