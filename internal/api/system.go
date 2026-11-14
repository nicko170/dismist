package api

import "time"

// CertView describes the AP-facing leaf certificate.
type CertView struct {
	Subject  string    `json:"subject"`
	SANs     []string  `json:"sans"`
	Issuer   string    `json:"issuer"`
	NotAfter time.Time `json:"not_after"`
}

// ChainCertRole is the role of a CA certificate in the served chain or the
// onboarding bundle.
type ChainCertRole string

const (
	ChainCertRoleIntermediate ChainCertRole = "intermediate"
	ChainCertRoleRoot         ChainCertRole = "root"
)

// ChainCert is one CA certificate in the served chain or onboarding bundle.
type ChainCert struct {
	Subject  string        `json:"subject"`
	NotAfter time.Time     `json:"not_after"`
	Role     ChainCertRole `json:"role"`
}

// WSSView is the AP endpoint card.
type WSSView struct {
	// URL is the WSS URL APs are configured with ("" when -ap-url is not
	// set).
	URL    string      `json:"url"`
	Listen string      `json:"listen"`
	Cert   CertView    `json:"cert"`
	Chain  []ChainCert `json:"chain"`
	// Sessions is the number of connected AP sessions.
	Sessions int `json:"sessions"`
}

// DBView is the database card.
type DBView struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	FreeBytes uint64 `json:"free_bytes"`
	Schema    uint64 `json:"schema"`
	Compat    uint64 `json:"compat"`
	// RollbackWithoutRestore is true while meta.compat is 1: an older
	// release can run this database without restoring a backup first.
	RollbackWithoutRestore bool              `json:"rollback_without_restore"`
	Retention              map[string]string `json:"retention"`
}

// PreMigrationCopy is one controller.db.pre-v<N>-<ts> file.
type PreMigrationCopy struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// BackupView is the backups card.
type BackupView struct {
	LastSuccess *time.Time `json:"last_success" tstype:"string | null,required"`
	// Next is the next scheduled snapshot; null when snapshots are off.
	Next *time.Time `json:"next" tstype:"string | null,required"`
	// Interval is the snapshot interval as a Go duration ("24h0m0s"), ""
	// when snapshots are off.
	Interval  string `json:"interval"`
	Keep      int    `json:"keep"`
	MaxBytes  int64  `json:"max_bytes"`
	UsedBytes int64  `json:"used_bytes"`
	Count     int    `json:"count"`
	Dir       string `json:"dir"`
	// LastError is the reason the last scheduled snapshot was skipped or
	// failed; "" after a success.
	LastError    string             `json:"last_error"`
	PreMigration []PreMigrationCopy `json:"pre_migration"`
}

// MetricsClients is the client metric mode.
type MetricsClients string

const (
	MetricsClientsOff       MetricsClients = "off"
	MetricsClientsAggregate MetricsClients = "aggregate"
	MetricsClientsPerClient MetricsClients = "per-client"
)

// MetricsView is the metrics card.
type MetricsView struct {
	Listen  string         `json:"listen"`
	Remote  bool           `json:"remote"`
	Names   bool           `json:"names"`
	Clients MetricsClients `json:"clients"`
}

// MigrationState is meta.migration_state.state.
type MigrationState string

const (
	MigrationStateNotRun    MigrationState = "not_run"
	MigrationStateCommitted MigrationState = "committed"
	MigrationStateLegacy    MigrationState = "legacy"
)

// MigrationView is the migration card.
type MigrationView struct {
	State           MigrationState `json:"state"`
	Reason          string         `json:"reason"`
	ReportAvailable bool           `json:"report_available"`
}

// SystemView is GET /api/system (design §3.14).
type SystemView struct {
	Version   string    `json:"version"`
	Commit    string    `json:"commit"`
	Go        string    `json:"go"`
	StartedAt time.Time `json:"started_at"`
	Ready     bool      `json:"ready"`
	WSS       WSSView   `json:"wss"`
	// DNSMode is true when dismist serves AP DNS for the stock Mist name.
	DNSMode        bool          `json:"dns_mode"`
	TrustedProxies []string      `json:"trusted_proxies"`
	DB             DBView        `json:"db"`
	Backup         BackupView    `json:"backup"`
	Metrics        MetricsView   `json:"metrics"`
	Migration      MigrationView `json:"migration"`
}

// CAPem is one CA certificate for an AP's trust bundle.
type CAPem struct {
	Name string `json:"name"`
	PEM  string `json:"pem"`
}

// OnboardingView is GET /api/onboarding: what an AP needs to reach this
// controller. CAPems are the CA certificates of the served chain (the
// issuing roots and intermediates, or the controller's own CA); the CLI adds
// them to the AP's stock Mist bundle. CloudINI is a cloud.ini for
// /rw/etc/mist/ep-control, "" when the WSS URL is not known.
type OnboardingView struct {
	WSSURL   string  `json:"wss_url"`
	DNSMode  bool    `json:"dns_mode"`
	CAPems   []CAPem `json:"ca_pems"`
	CloudINI string  `json:"cloud_ini"`
}

// HealthView is the body of /healthz and of a passing /readyz.
type HealthView struct {
	Status string `json:"status"`
}
