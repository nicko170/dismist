package httpapi

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
)

// StockAPURL is the endpoint a stock AP33 connects to. An AP still using it
// reaches dismist only through dismist's DNS (DNS mode).
const StockAPURL = "wss://ep-terminator.mistsys.net/ws"

// BackupStatus reports the scheduled snapshots (the System page's Backups
// card).
type BackupStatus interface {
	Status() api.BackupView
}

// SystemInfo is what dismistd tells the API about itself for /api/system and
// /api/onboarding. Zero values are reported as unknown.
type SystemInfo struct {
	Version   string
	Commit    string
	StartedAt time.Time
	// Ready reports readiness (the /readyz checks without the database
	// write).
	Ready func() bool
	// APURL is the WSS URL APs are configured with (-ap-url); "" means APs
	// keep the stock ep-terminator name (DNS mode).
	APURL     string
	WSSListen string
	// Chain is the AP endpoint's certificate chain as served, leaf first.
	Chain [][]byte
	// ExtraCAs are CA certificates to add to an AP's bundle besides the
	// served chain (-ap-ca-file): the local CA, or the public roots that
	// issue the served chain.
	ExtraCAs       [][]byte
	TrustedProxies []string
	DBPath         string
	Metrics        api.MetricsView
	Backups        BackupStatus
	Migration      func() api.MigrationView
	// BackupTempDir is where backup downloads are staged (an emptyDir in
	// the chart, never the database volume). "" means os.TempDir().
	BackupTempDir string
	// BackupTempMax is the most bytes a staged copy may use in
	// BackupTempDir (-backup-tmp-max: the chart's emptyDir sizeLimit,
	// which statfs does not report and which evicts the pod when
	// exceeded). 0 means only the free-space check applies.
	BackupTempMax int64
}

// UseSystem sets the daemon facts /api/system and /api/onboarding report.
func (s *Server) UseSystem(info SystemInfo) *Server {
	s.system = info
	return s
}

// DNSMode reports whether APs reach dismist through the stock Mist name.
func (info SystemInfo) DNSMode() bool {
	if info.APURL == "" {
		return true
	}
	parsed, err := url.Parse(info.APURL)
	return err == nil && strings.EqualFold(parsed.Hostname(), "ep-terminator.mistsys.net")
}

// wssURL is the URL an AP's cloud.ini points at.
func (info SystemInfo) wssURL() string {
	if info.APURL == "" {
		return StockAPURL
	}
	return info.APURL
}

type sizeStore interface{ Size() (int64, error) }
type schemaStore interface {
	Schema() (model.SchemaInfo, error)
}
type backupStore interface {
	WriteBackup(path string) (int64, error)
}

// systemResource serves /api/system and /api/system/backup.
func (s *Server) systemResource(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/api/system" && request.Method == http.MethodGet:
		respond(writer, http.StatusOK, s.systemView())
	case request.URL.Path == "/api/system/backup" && request.Method == http.MethodGet:
		s.downloadBackup(writer, request)
	default:
		routeNotFound(writer)
	}
}

func (s *Server) systemView() api.SystemView {
	info := s.system
	view := api.SystemView{
		Version: info.Version, Commit: info.Commit, Go: runtime.Version(), StartedAt: info.StartedAt,
		DNSMode: info.DNSMode(), TrustedProxies: nonNil(info.TrustedProxies), Metrics: info.Metrics,
		WSS:       api.WSSView{URL: info.APURL, Listen: info.WSSListen, Chain: []api.ChainCert{}, Cert: api.CertView{SANs: []string{}}},
		DB:        api.DBView{Path: info.DBPath, Schema: 1, Compat: 1, Retention: map[string]string{}},
		Backup:    api.BackupView{PreMigration: []api.PreMigrationCopy{}},
		Migration: api.MigrationView{State: api.MigrationStateNotRun},
	}
	if info.Ready != nil {
		view.Ready = info.Ready()
	}
	if view.Metrics.Clients == "" {
		view.Metrics.Clients = api.MetricsClientsAggregate
	}
	if len(info.Chain) > 0 {
		if leaf, err := x509.ParseCertificate(info.Chain[0]); err == nil {
			view.WSS.Cert = api.CertView{Subject: leaf.Subject.String(), SANs: certSANs(leaf), Issuer: leaf.Issuer.String(), NotAfter: leaf.NotAfter.UTC()}
		}
	}
	for _, ca := range s.caCertificates() {
		role := api.ChainCertRoleIntermediate
		if bytes.Equal(ca.RawIssuer, ca.RawSubject) {
			role = api.ChainCertRoleRoot
		}
		view.WSS.Chain = append(view.WSS.Chain, api.ChainCert{Subject: ca.Subject.String(), NotAfter: ca.NotAfter.UTC(), Role: role})
	}
	view.WSS.Sessions = s.connectedAPs()
	if sized, ok := s.store.(sizeStore); ok {
		if size, err := sized.Size(); err == nil {
			view.DB.SizeBytes = size
		}
	}
	if info.DBPath != "" {
		if space, err := model.DiskUsage(filepath.Dir(info.DBPath)); err == nil {
			view.DB.FreeBytes = space.Free
		}
		if copies, err := migrate.ListCopies(info.DBPath); err == nil {
			for _, copy := range copies {
				view.Backup.PreMigration = append(view.Backup.PreMigration, api.PreMigrationCopy{Name: copy.Name, SizeBytes: copy.SizeBytes})
			}
		}
	}
	if schemas, ok := s.store.(schemaStore); ok {
		if schema, err := schemas.Schema(); err == nil {
			view.DB.Schema, view.DB.Compat = schema.Schema, schema.Compat
		}
	}
	view.DB.RollbackWithoutRestore = view.DB.Compat <= 1
	if info.Backups != nil {
		preMigration := view.Backup.PreMigration
		view.Backup = info.Backups.Status()
		view.Backup.PreMigration = preMigration
	}
	if info.Migration != nil {
		view.Migration = info.Migration()
	}
	return view
}

func certSANs(cert *x509.Certificate) []string {
	sans := append([]string{}, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}
	for _, uri := range cert.URIs {
		sans = append(sans, uri.String())
	}
	return sans
}

// caCertificates are the CAs an AP needs to trust the endpoint: the served
// chain after the leaf (or the leaf itself when it is self-signed), then
// the extra CAs, without duplicates.
func (s *Server) caCertificates() []*x509.Certificate {
	var result []*x509.Certificate
	seen := map[string]bool{}
	add := func(der []byte) {
		cert, err := x509.ParseCertificate(der)
		if err != nil || seen[string(cert.Raw)] {
			return
		}
		seen[string(cert.Raw)] = true
		result = append(result, cert)
	}
	chain := s.system.Chain
	for index, der := range chain {
		if index == 0 {
			if leaf, err := x509.ParseCertificate(der); err != nil || !bytes.Equal(leaf.RawIssuer, leaf.RawSubject) {
				continue
			}
		}
		add(der)
	}
	for _, der := range s.system.ExtraCAs {
		add(der)
	}
	return result
}

// connectedAPs counts AP control sessions that are connected now.
func (s *Server) connectedAPs() int {
	count := 0
	if s.manager != nil {
		for _, control := range s.manager.Servers() {
			if control.Status().Connected {
				count++
			}
		}
		return count
	}
	if s.control != nil && s.control.Status().Connected {
		count++
	}
	return count
}

// onboarding serves GET /api/onboarding (design §3.15): what an AP needs to
// reach this controller.
func (s *Server) onboarding(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/api/onboarding" || request.Method != http.MethodGet {
		routeNotFound(writer)
		return
	}
	view := api.OnboardingView{WSSURL: s.system.wssURL(), DNSMode: s.system.DNSMode(), CAPems: []api.CAPem{}}
	for _, ca := range s.caCertificates() {
		name := ca.Subject.CommonName
		if name == "" {
			name = ca.Subject.String()
		}
		view.CAPems = append(view.CAPems, api.CAPem{Name: name, PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}))})
	}
	if ini, err := RenderCloudINI(view.WSSURL); err == nil {
		view.CloudINI = ini
	} else {
		log.Printf("onboarding: cannot render cloud.ini for %q: %v", view.WSSURL, err)
	}
	respond(writer, http.StatusOK, view)
}

// RWRootCerts is the CA bundle path cloud.ini points at: the stock bundle
// plus the controller's CAs, written to the AP's persistent /rw.
const RWRootCerts = "/rw/etc/mist/ep-control/cacerts-production.pem"

// RenderCloudINI renders /rw/etc/mist/ep-control/cloud.ini for an AP33 on
// 0.15: the stock file's structure with both [[cloud]] url keys set to
// wssURL and both rootcerts pointing at RWRootCerts.
func RenderCloudINI(wssURL string) (string, error) {
	if err := CheckAPURL(wssURL); err != nil {
		return "", err
	}
	return fmt.Sprintf(`# dismist: cloud.ini for /rw/etc/mist/ep-control (overrides /etc/mist/ep-control).
# A factory reset wipes /rw and with it this file and the CA bundle.

ENV = "production"

[[cloud]]
  ENV = "production"
  url = %[1]s
  priority = 2
  handshaketimeout = "2m"
    [cloud.tls]
      insecure = false
      rootcerts = %[2]s

[[cloud]]
  # Only for Factory Use (manufacturing)
  ENV = "production"
  url = %[1]s
  priority = 2
  handshaketimeout = "2m"
  manufacturing = true
    [cloud.tls]
      insecure = false
      rootcerts = %[2]s

[proxy]

[auth]
  timeout = "5m"

[udp]
  disabled = false
`, strconv.Quote(wssURL), strconv.Quote(RWRootCerts)), nil
}

// CheckAPURL accepts a WSS endpoint an AP can use: the wss scheme, a host,
// no credentials, query or fragment, and nothing that needs TOML escaping.
func CheckAPURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("%q is not a wss://host[:port]/path URL", raw)
	}
	if strings.ContainsAny(raw, "\"\\\n\r\t ") {
		return fmt.Errorf("%q contains characters cloud.ini cannot carry", raw)
	}
	return nil
}

// downloadBackup serves GET /api/system/backup (design §4.1, §4.9): the
// database is copied with tx.WriteTo into a private temporary file first
// and only then streamed, so no bbolt read transaction is open while a slow
// client downloads. One download runs at a time; admin scope only.
func (s *Server) downloadBackup(writer http.ResponseWriter, request *http.Request) {
	if !requireAdmin(writer, request) {
		return
	}
	backups, ok := s.store.(backupStore)
	if !ok {
		writeAPIError(writer, newError(http.StatusNotImplemented, api.ErrorCodeUnavailable, "backups need the bbolt database"))
		return
	}
	if !s.backupBusy.TryLock() {
		writer.Header().Set("Retry-After", "30")
		writeAPIError(writer, newError(http.StatusTooManyRequests, api.ErrorCodeRateLimited, "a backup download is already running; try again when it has finished"))
		return
	}
	defer s.backupBusy.Unlock()
	tempRoot := s.system.BackupTempDir
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	if sized, ok := s.store.(sizeStore); ok {
		if size, err := sized.Size(); err == nil {
			const margin = 1 << 20
			if limit := s.system.BackupTempMax; limit > 0 && size+margin > limit {
				writeAPIError(writer, newError(http.StatusInsufficientStorage, api.ErrorCodeUnavailable,
					fmt.Sprintf("the database (%d bytes) does not fit the %d-byte backup staging limit; raise -backup-tmp-max (the chart's backup.tmpSizeLimit)", size, limit)))
				return
			}
			if space, err := model.DiskUsage(tempRoot); err == nil && space.Free < uint64(size)+margin {
				writeAPIError(writer, newError(http.StatusInsufficientStorage, api.ErrorCodeUnavailable,
					fmt.Sprintf("not enough space in %s for a %d-byte copy of the database", tempRoot, size)))
				return
			}
		}
	}
	dir, err := os.MkdirTemp(tempRoot, "dismist-backup-")
	if err != nil {
		log.Printf("backup download: %v", err)
		writeAPIError(writer, internalError("cannot stage the backup"))
		return
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "controller.db")
	started := time.Now().UTC()
	if _, err := backups.WriteBackup(path); err != nil {
		log.Printf("backup download: copy database: %v", err)
		writeAPIError(writer, internalError("cannot copy the database"))
		return
	}
	file, err := os.Open(path)
	if err != nil {
		log.Printf("backup download: %v", err)
		writeAPIError(writer, internalError("cannot read the backup copy"))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeAPIError(writer, internalError("cannot read the backup copy"))
		return
	}
	name := "dismist-controller-" + started.Format("20060102T150405Z") + ".db"
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	writer.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	log.Printf("audit: database backup %s (%d bytes) downloaded by %s from %s", name, info.Size(), principal(request), request.RemoteAddr)
	if _, err := io.Copy(writer, file); err != nil {
		log.Printf("backup download %s: %v", name, err)
	}
}
