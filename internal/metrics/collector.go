package metrics

import (
	"sort"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// collector builds every dismist_* series from the controller's current
// state on each scrape: the store snapshot, the registered AP sessions and
// their telemetry. It keeps no state of its own, so a scrape always reflects
// what the controller knows at that moment.
type collector struct {
	store         model.StateStore
	sessions      func() map[string]session
	storeSchema   func() (schema, compat int, ok bool)
	compileStatus func() map[string]CompileStatus

	sites, aps, siteAPs, siteClients *prometheus.Desc
	apMetrics                        []apMetric
	schema, compat                   *prometheus.Desc
	compileError, compileErrors      *prometheus.Desc
}

// session is the part of a registered AP session the collector reads;
// *controller.Server implements it.
type session interface {
	Status() controller.Status
	TelemetrySnapshot() controller.TelemetrySnapshot
}

// managerSessions adapts the manager's registered servers.
func managerSessions(manager *controller.Manager) func() map[string]session {
	return func() map[string]session {
		servers := manager.Servers()
		sessions := make(map[string]session, len(servers))
		for id, server := range servers {
			if server != nil {
				sessions[id] = server
			}
		}
		return sessions
	}
}

// apMetric is one per-AP family labelled {site, ap}.
type apMetric struct {
	desc  *prometheus.Desc
	kind  prometheus.ValueType
	value func(apSample) float64
}

type apSample struct {
	site, id                                                      string
	connected                                                     bool
	clients                                                       int
	connections, fullFrames, fastFrames, uptime, desired, applied uint64
	lastSeen                                                      int64
}

var apLabels = []string{"site", "ap"}

func newCollector(options Options) *collector {
	c := &collector{
		store:         options.Store,
		sessions:      managerSessions(options.Manager),
		storeSchema:   options.StoreSchema,
		compileStatus: options.CompileStatus,
		sites:         prometheus.NewDesc("dismist_sites", "Registered sites.", nil, nil),
		aps:           prometheus.NewDesc("dismist_aps", "Registered access points.", nil, nil),
		siteAPs:       prometheus.NewDesc("dismist_site_aps", "Registered access points per site.", []string{"site"}, nil),
		siteClients:   prometheus.NewDesc("dismist_site_associated_clients", "Associated clients on connected access points per site.", []string{"site"}, nil),
		schema:        prometheus.NewDesc("dismist_store_schema", "Data format version of the controller database (meta.schema).", nil, nil),
		compat:        prometheus.NewDesc("dismist_store_compat", "Oldest database schema a dismist binary must support to run this database (meta.compat).", nil, nil),
		compileError:  prometheus.NewDesc("dismist_ap_config_compile_error", "Whether the access point's last config compile failed; it keeps its last delivered config.", apLabels, nil),
		compileErrors: prometheus.NewDesc("dismist_ap_config_compile_errors_total", "Access point config compile failures since controller start.", apLabels, nil),
	}
	gauge := func(name, help string, value func(apSample) float64) apMetric {
		return apMetric{prometheus.NewDesc(name, help, apLabels, nil), prometheus.GaugeValue, value}
	}
	counter := func(name, help string, value func(apSample) float64) apMetric {
		return apMetric{prometheus.NewDesc(name, help, apLabels, nil), prometheus.CounterValue, value}
	}
	c.apMetrics = []apMetric{
		gauge("dismist_ap_connected", "Whether the access point is connected to the controller.", func(a apSample) float64 { return boolValue(a.connected) }),
		gauge("dismist_ap_associated_clients", "Associated clients while the access point is connected.", func(a apSample) float64 { return float64(a.clients) }),
		counter("dismist_ap_connections_total", "Authenticated access point connections since controller start.", func(a apSample) float64 { return float64(a.connections) }),
		counter("dismist_ap_full_frames_total", "Full telemetry frames decoded since controller start.", func(a apSample) float64 { return float64(a.fullFrames) }),
		counter("dismist_ap_fast_frames_total", "Fast telemetry frames decoded since controller start.", func(a apSample) float64 { return float64(a.fastFrames) }),
		gauge("dismist_ap_uptime_seconds", "Access point uptime reported by its latest telemetry.", func(a apSample) float64 { return float64(a.uptime) }),
		gauge("dismist_ap_desired_revision", "Desired access point configuration revision.", func(a apSample) float64 { return float64(a.desired) }),
		gauge("dismist_ap_applied_revision", "Last access point configuration revision reported as applied.", func(a apSample) float64 { return float64(a.applied) }),
		gauge("dismist_ap_last_seen_timestamp_seconds", "Unix time of the last access point message, or zero if never seen.", func(a apSample) float64 { return float64(a.lastSeen) }),
	}
	return c
}

// Describe sends every descriptor, including those of optional sources that
// are not wired, so the registry checks each emitted series against them.
func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.sites, c.aps, c.siteAPs, c.siteClients, c.schema, c.compat, c.compileError, c.compileErrors} {
		ch <- desc
	}
	for _, metric := range c.apMetrics {
		ch <- metric.desc
	}
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	state := c.store.Snapshot()
	sessions := c.sessions()
	sort.Slice(state.Sites, func(i, j int) bool { return state.Sites[i].ID < state.Sites[j].ID })
	sort.Slice(state.APs, func(i, j int) bool { return state.APs[i].ID < state.APs[j].ID })

	emit(ch, c.sites, prometheus.GaugeValue, float64(len(state.Sites)))
	emit(ch, c.aps, prometheus.GaugeValue, float64(len(state.APs)))

	siteAPs := make(map[string]int, len(state.Sites))
	siteClients := make(map[string]int, len(state.Sites))
	samples := make([]apSample, 0, len(state.APs))
	for _, ap := range state.APs {
		siteAPs[ap.SiteID]++
		sample := apSample{site: ap.SiteID, id: ap.ID, desired: ap.ConfigRevision}
		if server, ok := sessions[ap.ID]; ok {
			status := server.Status()
			stats := server.TelemetrySnapshot()
			sample.connected = status.Connected
			sample.connections = status.ConnectionCount
			sample.applied = status.AppliedRevision
			if !status.LastSeen.IsZero() {
				sample.lastSeen = status.LastSeen.Unix()
			}
			sample.fullFrames = stats.FullFrames
			sample.fastFrames = stats.FastFrames
			sample.uptime = stats.UptimeSeconds
			sample.clients = liveClientCount(status, stats)
			siteClients[ap.SiteID] += sample.clients
		}
		samples = append(samples, sample)
	}
	for _, site := range state.Sites {
		emit(ch, c.siteAPs, prometheus.GaugeValue, float64(siteAPs[site.ID]), site.ID)
		emit(ch, c.siteClients, prometheus.GaugeValue, float64(siteClients[site.ID]), site.ID)
	}
	for _, metric := range c.apMetrics {
		for _, sample := range samples {
			emit(ch, metric.desc, metric.kind, metric.value(sample), sample.site, sample.id)
		}
	}

	if c.storeSchema != nil {
		if schema, compat, ok := c.storeSchema(); ok {
			emit(ch, c.schema, prometheus.GaugeValue, float64(schema))
			emit(ch, c.compat, prometheus.GaugeValue, float64(compat))
		}
	}
	if c.compileStatus != nil {
		status := c.compileStatus()
		for _, sample := range samples {
			ap := status[sample.id]
			emit(ch, c.compileError, prometheus.GaugeValue, boolValue(ap.Failing), sample.site, sample.id)
			emit(ch, c.compileErrors, prometheus.CounterValue, float64(ap.Failures), sample.site, sample.id)
		}
	}
}

// emit sends one sample. A label value the registry cannot accept (invalid
// UTF-8) becomes a gather error for that series instead of a panic, and the
// rest of the scrape is still served.
func emit(ch chan<- prometheus.Metric, desc *prometheus.Desc, kind prometheus.ValueType, value float64, labels ...string) {
	metric, err := prometheus.NewConstMetric(desc, kind, value, labels...)
	if err != nil {
		metric = prometheus.NewInvalidMetric(desc, err)
	}
	ch <- metric
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func liveClientCount(status controller.Status, stats controller.TelemetrySnapshot) int {
	if !status.Connected || (stats.LastFull.Before(status.ConnectedAt) && stats.LastFast.Before(status.ConnectedAt)) {
		return 0
	}
	return len(stats.AssociatedClients)
}
