package metrics

import (
	"encoding/json"
	"sort"
	"time"

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
	routes        func() map[string]uint64
	sessions      func() map[string]Session
	storeSchema   func() (schema, compat int, ok bool)
	compileStatus func() map[string]CompileStatus
	detail        func(apID string, session Session) APDetail
	storeUsage    func() (StoreUsage, bool)
	migration     func() (string, bool)
	backups       func() (BackupStatus, bool)
	build         BuildInfo
	certificates  []Certificate
	now           func() time.Time

	names       bool
	clients     ClientMode
	clientLimit int

	descs []*prometheus.Desc

	sites, aps, siteAPs, siteClients *prometheus.Desc
	siteInfo, apInfo, wlanInfo       *prometheus.Desc
	apMetrics                        []apMetric
	schema, compat                   *prometheus.Desc
	compileError, compileErrors      *prometheus.Desc

	status    statusFamilies
	telemetry telemetryFamilies
	radio     radioFamilies
	wlan      wlanFamilies
	client    clientFamilies
	control   controllerFamilies
}

// Session is the part of a registered AP session the collector reads;
// *controller.Server implements it. The collector also uses
// Counters(), Features() and ConfigSnapshot() when the session has them.
type Session interface {
	Status() controller.Status
	TelemetrySnapshot() controller.TelemetrySnapshot
}

type countersSession interface {
	Counters() controller.Counters
}

type featuresSession interface {
	Features() controller.Features
}

type configSession interface {
	ConfigSnapshot() ([]byte, uint64)
}

// managerSessions adapts the manager's registered servers.
func managerSessions(manager *controller.Manager) func() map[string]Session {
	return func() map[string]Session {
		servers := manager.Servers()
		sessions := make(map[string]Session, len(servers))
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

// newDesc creates a family descriptor and records it for Describe.
func (c *collector) newDesc(name, help string, labels ...string) *prometheus.Desc {
	desc := prometheus.NewDesc(name, help, labels, nil)
	c.descs = append(c.descs, desc)
	return desc
}

func (c *collector) apDesc(name, help string, extra ...string) *prometheus.Desc {
	return c.newDesc(name, help, append([]string{"site", "ap"}, extra...)...)
}

func newCollector(options Options) *collector {
	c := &collector{
		store:         options.Store,
		sessions:      managerSessions(options.Manager),
		routes:        options.Manager.RouteCounters,
		storeSchema:   options.StoreSchema,
		compileStatus: options.CompileStatus,
		detail:        options.Detail,
		storeUsage:    options.StoreUsage,
		migration:     options.MigrationState,
		backups:       options.Backups,
		build:         options.Build,
		certificates:  options.Certificates,
		now:           options.Now,
		names:         options.Names,
		clients:       options.Clients,
		clientLimit:   options.ClientLimit,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.clients == "" {
		c.clients = ClientsAggregate
	}
	c.sites = c.newDesc("dismist_sites", "Registered sites.")
	c.aps = c.newDesc("dismist_aps", "Registered access points.")
	c.siteAPs = c.newDesc("dismist_site_aps", "Registered access points per site.", "site")
	c.siteClients = c.newDesc("dismist_site_associated_clients", "Associated clients on connected access points per site.", "site")
	c.schema = c.newDesc("dismist_store_schema", "Data format version of the controller database (meta.schema).")
	c.compat = c.newDesc("dismist_store_compat", "Oldest database schema a dismist binary must support to run this database (meta.compat).")
	c.compileError = c.apDesc("dismist_ap_config_compile_error", "Whether the access point's last config compile failed; it keeps its last delivered config.")
	c.compileErrors = c.apDesc("dismist_ap_config_compile_errors_total", "Access point config compile failures since controller start.")

	// Info series let dashboards join names and AP facts onto the ID-labelled
	// series. Names are opt-in (-metrics-names); model, firmware and the
	// regulatory (CLM) version are bounded per AP and always present.
	siteInfoLabels := []string{"site"}
	apInfoLabels := []string{"site", "ap", "model", "firmware", "clm"}
	wlanInfoLabels := []string{"site", "wlan"}
	if options.Names {
		siteInfoLabels = append(siteInfoLabels, "name")
		apInfoLabels = append(apInfoLabels, "name")
		wlanInfoLabels = append(wlanInfoLabels, "name")
	}
	c.siteInfo = c.newDesc("dismist_site_info", "Registered site, always 1; the name label is present with -metrics-names.", siteInfoLabels...)
	c.apInfo = c.newDesc("dismist_ap_info", "Registered access point, always 1, with the model, firmware and regulatory (CLM) version it reports; the name label is present with -metrics-names.", apInfoLabels...)
	c.wlanInfo = c.newDesc("dismist_wlan_info", "Configured WLAN (network), always 1; the wlan label is the network ID and the name label (its SSID) is present with -metrics-names.", wlanInfoLabels...)

	gauge := func(name, help string, value func(apSample) float64) apMetric {
		return apMetric{c.apDesc(name, help), prometheus.GaugeValue, value}
	}
	counter := func(name, help string, value func(apSample) float64) apMetric {
		return apMetric{c.apDesc(name, help), prometheus.CounterValue, value}
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
	c.status = newStatusFamilies(c)
	c.telemetry = newTelemetryFamilies(c)
	c.radio = newRadioFamilies(c)
	c.wlan = newWLANFamilies(c)
	c.client = newClientFamilies(c)
	c.control = newControllerFamilies(c)
	return c
}

// Describe sends every descriptor, including those of optional sources that
// are not wired, so the registry checks each emitted series against them.
func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs {
		ch <- desc
	}
}

// apState is one registered AP during a scrape.
type apState struct {
	record  model.APRecord
	session Session
	status  controller.Status
	stats   controller.TelemetrySnapshot
	detail  APDetail
	// fresh is true while telemetry series may be exported (design §6.1).
	fresh bool
}

func (c *collector) Collect(ch chan<- prometheus.Metric) {
	state := c.store.Snapshot()
	sessions := c.sessions()
	now := c.now()
	sort.Slice(state.Sites, func(i, j int) bool { return state.Sites[i].ID < state.Sites[j].ID })
	sort.Slice(state.APs, func(i, j int) bool { return state.APs[i].ID < state.APs[j].ID })

	emit(ch, c.sites, prometheus.GaugeValue, float64(len(state.Sites)))
	emit(ch, c.aps, prometheus.GaugeValue, float64(len(state.APs)))

	siteAPs := make(map[string]int, len(state.Sites))
	siteClients := make(map[string]int, len(state.Sites))
	samples := make([]apSample, 0, len(state.APs))
	aps := make([]apState, 0, len(state.APs))
	for _, ap := range state.APs {
		siteAPs[ap.SiteID]++
		sample := apSample{site: ap.SiteID, id: ap.ID, desired: ap.ConfigRevision}
		current := apState{record: ap}
		model, firmware := ap.Model, ap.Firmware
		if server, ok := sessions[ap.ID]; ok {
			current.session = server
			current.status = server.Status()
			current.stats = server.TelemetrySnapshot()
			if c.detail != nil {
				current.detail = c.detail(ap.ID, server)
			}
			current.fresh = telemetryFresh(current.status, current.stats, current.detail.Telemetry.IntervalSeconds, now)
			if current.status.Model != "" {
				model = current.status.Model
			}
			if current.status.Firmware != "" {
				firmware = current.status.Firmware
			}
			sample.connected = current.status.Connected
			sample.connections = current.status.ConnectionCount
			sample.applied = current.status.AppliedRevision
			if !current.status.LastSeen.IsZero() {
				sample.lastSeen = current.status.LastSeen.Unix()
			}
			sample.fullFrames = current.stats.FullFrames
			sample.fastFrames = current.stats.FastFrames
			sample.uptime = current.stats.UptimeSeconds
			sample.clients = liveClientCount(current.status, current.stats)
			siteClients[ap.SiteID] += sample.clients
		}
		samples = append(samples, sample)
		aps = append(aps, current)
		infoLabels := []string{ap.SiteID, ap.ID, model, firmware, labelText(current.detail.Telemetry.ClmVersion, 64)}
		if c.names {
			infoLabels = append(infoLabels, ap.Name)
		}
		emit(ch, c.apInfo, prometheus.GaugeValue, 1, infoLabels...)
	}
	for _, site := range state.Sites {
		if c.names {
			emit(ch, c.siteInfo, prometheus.GaugeValue, 1, site.ID, site.Name)
		} else {
			emit(ch, c.siteInfo, prometheus.GaugeValue, 1, site.ID)
		}
		emit(ch, c.siteAPs, prometheus.GaugeValue, float64(siteAPs[site.ID]), site.ID)
		emit(ch, c.siteClients, prometheus.GaugeValue, float64(siteClients[site.ID]), site.ID)
	}
	for _, network := range state.Networks {
		if c.names {
			emit(ch, c.wlanInfo, prometheus.GaugeValue, 1, network.SiteID, network.ID, network.SSID)
		} else {
			emit(ch, c.wlanInfo, prometheus.GaugeValue, 1, network.SiteID, network.ID)
		}
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

	var perClient []clientSample
	for _, ap := range aps {
		c.collectStatus(ch, ap)
		if !ap.fresh {
			continue
		}
		c.collectHealth(ch, ap)
		c.collectUplink(ch, ap)
		radios := c.collectRadios(ch, ap)
		c.collectWLANs(ch, ap, radios)
		perClient = append(perClient, c.collectClientAggregates(ch, ap, radios)...)
	}
	c.collectPerClient(ch, perClient)
	c.collectController(ch)
}

// telemetryFresh reports whether an AP's telemetry series may be exported:
// it is connected, its last full frame is from this connection and younger
// than three reporting intervals (design §6.1).
func telemetryFresh(status controller.Status, stats controller.TelemetrySnapshot, intervalSeconds float64, now time.Time) bool {
	if !status.Connected || stats.LastFull.IsZero() || stats.LastFull.Before(status.ConnectedAt) {
		return false
	}
	interval := time.Duration(intervalSeconds * float64(time.Second))
	if interval < time.Second || interval > time.Hour {
		interval = defaultTelemetryInterval
	}
	return now.Sub(stats.LastFull) < 3*interval
}

// defaultTelemetryInterval is the AP's full-frame interval when it reports
// none (field 23 is 60 s on every firmware seen).
const defaultTelemetryInterval = 60 * time.Second

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

func gauge(ch chan<- prometheus.Metric, desc *prometheus.Desc, value float64, labels ...string) {
	emit(ch, desc, prometheus.GaugeValue, value, labels...)
}

func counter(ch chan<- prometheus.Metric, desc *prometheus.Desc, value float64, labels ...string) {
	emit(ch, desc, prometheus.CounterValue, value, labels...)
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

// desiredConfigTimestamp reads the Timestamp of the config the session
// serves, or 0.
func desiredConfigTimestamp(session Session) float64 {
	source, ok := session.(configSession)
	if !ok {
		return 0
	}
	config, _ := source.ConfigSnapshot()
	if len(config) == 0 {
		return 0
	}
	var envelope struct {
		Timestamp float64
	}
	if json.Unmarshal(config, &envelope) != nil {
		return 0
	}
	return envelope.Timestamp
}
