package metrics

import (
	"crypto/x509"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nicko170/dismist/internal/controller"
)

// controllerFamilies are the controller's own series: build, certificates,
// database, migration, backups and AP connection routing.
type controllerFamilies struct {
	buildInfo, certNotAfter, certNotBefore         *prometheus.Desc
	storeSize, storeFree, migrationState           *prometheus.Desc
	backupLastSuccess, backupFailures, backupBytes *prometheus.Desc
	routes                                         *prometheus.Desc
}

// MigrationStates are the dismist_schema_migration_state states.
var MigrationStates = []string{"not_run", "committed", "legacy"}

// backupFailureReasons are always exported, so the counters exist before
// the first failure.
var backupFailureReasons = []string{"space", "io"}

func newControllerFamilies(c *collector) controllerFamilies {
	certLabels := []string{"listener", "role", "cn"}
	return controllerFamilies{
		buildInfo:         c.newDesc("dismist_build_info", "dismistd build, always 1.", "version", "revision", "goversion"),
		certNotAfter:      c.newDesc("dismist_tls_certificate_not_after_timestamp_seconds", "Expiry of a certificate dismist serves to access points (ap-wss) or bundles for onboarding (ap-onboarding).", certLabels...),
		certNotBefore:     c.newDesc("dismist_tls_certificate_not_before_timestamp_seconds", "Start of validity of a certificate dismist serves to access points or bundles for onboarding.", certLabels...),
		storeSize:         c.newDesc("dismist_store_size_bytes", "Size of the controller database."),
		storeFree:         c.newDesc("dismist_store_free_bytes", "Free space on the filesystem holding the controller database."),
		migrationState:    c.newDesc("dismist_schema_migration_state", "Database migration state, 1 for the current one: not_run, committed, or legacy (the migration failed verification and typed settings are paused).", "state"),
		backupLastSuccess: c.newDesc("dismist_backup_last_success_timestamp_seconds", "Unix time of the last successful scheduled database snapshot."),
		backupFailures:    c.newDesc("dismist_backup_failures_total", "Scheduled database snapshots skipped or failed, by reason (space, io).", "reason"),
		backupBytes:       c.newDesc("dismist_backup_bytes", "Space the kept database snapshots use together."),
		routes:            c.newDesc("dismist_ap_ws_routes_total", "AP WebSocket connections by routing result (routed, unknown_ap, bad_frame, timeout_or_closed, too_many_frames); unauthenticated, so no AP label.", "result"),
	}
}

func (c *collector) collectController(ch chan<- prometheus.Metric) {
	f := c.control
	gauge(ch, f.buildInfo, 1, labelText(c.build.Version, maxLabelTextRunes), labelText(c.build.Revision, maxLabelTextRunes), labelText(c.build.GoVersion, maxLabelTextRunes))
	for _, cert := range c.certificates {
		cn := labelText(cert.CN, maxLabelTextRunes)
		if cert.Role == "leaf" && !c.names {
			cn = "" // a host name: only with -metrics-names
		}
		l := []string{oneOf(cert.Listener, []string{"ap-wss", "ap-onboarding"}), oneOf(cert.Role, []string{"leaf", "intermediate", "root"}), cn}
		gauge(ch, f.certNotAfter, unixSeconds(cert.NotAfter), l...)
		gauge(ch, f.certNotBefore, unixSeconds(cert.NotBefore), l...)
	}
	if c.storeUsage != nil {
		if usage, ok := c.storeUsage(); ok {
			gauge(ch, f.storeSize, float64(usage.SizeBytes))
			if usage.HasFree {
				gauge(ch, f.storeFree, float64(usage.FreeBytes))
			}
		}
	}
	if c.migration != nil {
		if state, ok := c.migration(); ok {
			for _, known := range MigrationStates {
				gauge(ch, f.migrationState, boolValue(state == known), known)
			}
		}
	}
	if c.backups != nil {
		if status, ok := c.backups(); ok {
			if !status.LastSuccess.IsZero() {
				gauge(ch, f.backupLastSuccess, unixSeconds(status.LastSuccess))
			}
			gauge(ch, f.backupBytes, float64(status.Bytes))
			failures := countedKeys(status.Failures, keyword)
			for _, reason := range backupFailureReasons {
				if _, ok := failures[reason]; !ok {
					failures[reason] = 0
				}
			}
			for _, reason := range sortedKeys(failures) {
				counter(ch, f.backupFailures, float64(failures[reason]), reason)
			}
		}
	}
	if c.routes != nil {
		routes := c.routes()
		for _, result := range []string{controller.RouteRouted, controller.RouteUnknownAP, controller.RouteBadFrame, controller.RouteTimeoutClosed, controller.RouteTooManyFrames} {
			counter(ch, f.routes, float64(routes[result]), result)
		}
	}
}

// CertificateSeries describes the served chain (listener "ap-wss": the leaf
// first, then intermediates, a self-signed last certificate as the root) and
// the extra onboarding CAs (listener "ap-onboarding"), at most 16 in all.
// Certificates that do not parse are skipped.
func CertificateSeries(chain, onboarding [][]byte) []Certificate {
	var result []Certificate
	add := func(der []byte, listener string, role func(*x509.Certificate) string) {
		if len(result) >= maxCertificates {
			return
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return
		}
		result = append(result, Certificate{Listener: listener, Role: role(cert), CN: cert.Subject.CommonName, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter})
	}
	for index, der := range chain {
		add(der, "ap-wss", func(cert *x509.Certificate) string {
			switch {
			case index == 0:
				return "leaf"
			case selfSigned(cert):
				return "root"
			}
			return "intermediate"
		})
	}
	for _, der := range onboarding {
		add(der, "ap-onboarding", func(cert *x509.Certificate) string {
			if selfSigned(cert) {
				return "root"
			}
			return "intermediate"
		})
	}
	return result
}

func selfSigned(cert *x509.Certificate) bool {
	return cert.CheckSignatureFrom(cert) == nil
}
