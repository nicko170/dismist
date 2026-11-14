package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/migrate"
	"github.com/nicko170/dismist/internal/model"
)

// version and commit are set at build time with
// -ldflags "-X main.version=… -X main.commit=…".
var (
	version = "dev"
	commit  = ""
)

// buildVersion returns the version and commit, falling back to the Go
// build information when the linker flags were not set.
func buildVersion() (string, string) {
	v, c := version, commit
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c
	}
	if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	if c == "" {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				c = setting.Value
			}
		}
	}
	return v, c
}

// loadAPCAs reads the extra CA certificates for the onboarding bundle: the
// -ap-ca-file PEM bundle, or, when it is not set, a ca.crt next to the
// -tls-cert (the layout dismist init writes) if there is one.
func loadAPCAs(path, certPath string) ([][]byte, error) {
	explicit := path != ""
	if !explicit {
		path = filepath.Join(filepath.Dir(certPath), "ca.crt")
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var certs [][]byte
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !cert.IsCA {
			return nil, fmt.Errorf("%s: %q is not a CA certificate", path, cert.Subject.String())
		}
		certs = append(certs, block.Bytes)
	}
	if len(certs) == 0 && explicit {
		return nil, fmt.Errorf("%s holds no PEM certificates", path)
	}
	return certs, nil
}

// migrationView reads meta.migration_state for the System page.
func migrationView(store *model.BoltStore) api.MigrationView {
	state, err := migrate.ReadState(store)
	if err != nil {
		return api.MigrationView{State: api.MigrationStateLegacy, Reason: "cannot read meta.migration_state: " + err.Error()}
	}
	var report json.RawMessage
	found, _ := store.MetaJSON(model.MetaMigrationReport, &report)
	return api.MigrationView{State: api.MigrationState(state.State), Reason: state.Reason, ReportAvailable: found}
}

// backupWarning records a skipped or failed scheduled snapshot as a
// warning event on every site, so it shows in each site's Events.
func backupWarning(store *model.BoltStore) func(reason, message string) {
	return func(reason, message string) {
		now := time.Now().UTC()
		data, _ := json.Marshal(map[string]string{"reason": reason})
		for _, site := range store.Snapshot().Sites {
			event := model.EventV2{
				SiteID: site.ID, Kind: "backup", Severity: model.SeverityWarning,
				Title: "Scheduled backup skipped", Detail: message, FirstSeen: now, LastSeen: now, Data: data,
			}
			if _, err := store.Events2().AppendController(event); err != nil {
				log.Printf("backup warning event for site %s: %v", site.ID, err)
			}
		}
	}
}

// splitList splits a comma-separated flag value.
func splitList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

// systemMetricsView describes the metrics listener (the -metrics-* flags)
// for the System page.
func systemMetricsView(c *metricsConfig) api.MetricsView {
	return api.MetricsView{Listen: c.listen, Remote: c.allowRemote, Names: c.names, Clients: api.MetricsClients(c.clients)}
}
