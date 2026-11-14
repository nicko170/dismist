package httpapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/webgate"
	bolt "go.etcd.io/bbolt"
)

// testChain returns a CA and a leaf it signed, as DER.
func testChain(t *testing.T) (caDER, leafDER []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Example Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err = x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "ap.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		DNSNames: []string{"ap.example.test"}, IPAddresses: []net.IP{net.ParseIP("192.0.2.44")},
	}
	leafDER, err = x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return caDER, leafDER
}

type fakeBackups struct{ view api.BackupView }

func (f fakeBackups) Status() api.BackupView { return f.view }

func TestSystemView(t *testing.T) {
	handler, store, _ := platformFixture(t)
	caDER, leafDER := testChain(t)
	last := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	handler.UseSystem(SystemInfo{
		Version: "1.2.3", Commit: "abc123", StartedAt: last, Ready: func() bool { return true },
		APURL: "wss://ap.example.test/ws", WSSListen: "0.0.0.0:8443",
		Chain: [][]byte{leafDER}, ExtraCAs: [][]byte{caDER}, TrustedProxies: []string{"10.244.0.0/16"},
		DBPath:  store.Path(),
		Metrics: api.MetricsView{Listen: "127.0.0.1:9090"},
		Backups: fakeBackups{api.BackupView{LastSuccess: &last, Keep: 7, Dir: "/state/backups"}},
		Migration: func() api.MigrationView {
			return api.MigrationView{State: api.MigrationStateNotRun}
		},
	})
	response := request(t, handler, http.MethodGet, "/api/system", "")
	if response.Code != http.StatusOK {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var view api.SystemView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Version != "1.2.3" || view.Commit != "abc123" || !view.Ready || view.Go == "" || view.DNSMode ||
		view.WSS.URL != "wss://ap.example.test/ws" || view.WSS.Cert.Subject != "CN=ap.example.test" ||
		len(view.WSS.Cert.SANs) != 2 || view.WSS.Cert.Issuer != "CN=Example Test CA" ||
		len(view.WSS.Chain) != 1 || view.WSS.Chain[0].Role != api.ChainCertRoleRoot {
		t.Fatalf("system view %+v", view)
	}
	if view.DB.Path != store.Path() || view.DB.SizeBytes <= 0 || view.DB.FreeBytes == 0 || view.DB.Schema != 1 ||
		view.DB.Compat != 1 || !view.DB.RollbackWithoutRestore {
		t.Fatalf("db view %+v", view.DB)
	}
	if view.Backup.Keep != 7 || view.Backup.LastSuccess == nil || view.Backup.PreMigration == nil || view.Migration.State != api.MigrationStateNotRun ||
		view.Metrics.Clients != api.MetricsClientsAggregate {
		t.Fatalf("backup/migration/metrics view %+v %+v %+v", view.Backup, view.Migration, view.Metrics)
	}
}

func TestOnboardingView(t *testing.T) {
	handler, _, _ := platformFixture(t)
	caDER, leafDER := testChain(t)
	handler.UseSystem(SystemInfo{APURL: "wss://ap.example.test/ws", Chain: [][]byte{leafDER}, ExtraCAs: [][]byte{caDER}})
	response := request(t, handler, http.MethodGet, "/api/onboarding", "")
	var view api.OnboardingView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || response.Code != http.StatusOK {
		t.Fatalf("%d %s %v", response.Code, response.Body.String(), err)
	}
	if view.WSSURL != "wss://ap.example.test/ws" || view.DNSMode || len(view.CAPems) != 1 || view.CAPems[0].Name != "Example Test CA" {
		t.Fatalf("onboarding %+v", view)
	}
	block, _ := pem.Decode([]byte(view.CAPems[0].PEM))
	if block == nil || !bytes.Equal(block.Bytes, caDER) {
		t.Fatal("CA PEM does not round-trip")
	}
	if strings.Count(view.CloudINI, `url = "wss://ap.example.test/ws"`) != 2 || strings.Count(view.CloudINI, `rootcerts = "`+RWRootCerts+`"`) != 2 ||
		strings.Contains(view.CloudINI, "insecure = true") {
		t.Fatalf("cloud.ini:\n%s", view.CloudINI)
	}
	// Without -ap-url the AP keeps the stock name: DNS mode.
	handler.UseSystem(SystemInfo{})
	response = request(t, handler, http.MethodGet, "/api/onboarding", "")
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || !view.DNSMode || view.WSSURL != StockAPURL || len(view.CAPems) != 0 {
		t.Fatalf("DNS mode onboarding %+v %v", view, err)
	}
	for _, bad := range []string{"https://ap.example.test/ws", "wss://user:pw@ap.example.test/ws", "wss://ap.example.test/ws?x=1", `wss://ap.example.test/"ws`, "wss:///ws"} {
		if CheckAPURL(bad) == nil {
			t.Fatalf("CheckAPURL accepted %q", bad)
		}
	}
}

// openBackup checks a downloaded database: it opens, and its meta revision
// and state buckets are readable.
func openBackup(t *testing.T, raw []byte) uint64 {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		t.Fatalf("backup does not open: %v", err)
	}
	defer db.Close()
	var revision uint64
	if err := db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("meta"))
		if meta == nil || len(meta.Get([]byte("revision"))) != 8 || tx.Bucket([]byte("sites")) == nil {
			t.Fatal("backup lacks meta.revision or sites")
		}
		revision = binary.BigEndian.Uint64(meta.Get([]byte("revision")))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return revision
}

func TestBackupDownload(t *testing.T) {
	handler, store, _ := platformFixture(t)
	staging := t.TempDir()
	handler.UseSystem(SystemInfo{BackupTempDir: staging})
	response := request(t, handler, http.MethodGet, "/api/system/backup", "")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(response.Header().Get("Content-Disposition"), `attachment; filename="dismist-controller-`) {
		t.Fatalf("backup: %d %v", response.Code, response.Header())
	}
	if revision := openBackup(t, response.Body.Bytes()); revision != store.Snapshot().Revision {
		t.Fatalf("backup revision %d, store %d", revision, store.Snapshot().Revision)
	}
	if left, _ := os.ReadDir(staging); len(left) != 0 {
		t.Fatalf("staging directory not cleaned: %v", left)
	}
	// Read tokens never download backups (they contain PSKs and AP secrets).
	read := webgate.Principal{Via: api.AuthViaToken, Scope: api.TokenScopeRead, TokenID: "dsm_r"}
	req := httptest.NewRequest(http.MethodGet, "/api/system/backup", nil).WithContext(webgate.WithPrincipal(t.Context(), read))
	req.Host = "127.0.0.1:8088"
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read token backup: %d", denied.Code)
	}
	// One download at a time.
	handler.backupBusy.Lock()
	busy := request(t, handler, http.MethodGet, "/api/system/backup", "")
	handler.backupBusy.Unlock()
	if busy.Code != http.StatusTooManyRequests || busy.Header().Get("Retry-After") == "" ||
		decodeErrorBody(t, "busy backup", busy).Code != api.ErrorCodeRateLimited {
		t.Fatalf("concurrent backup: %d %s", busy.Code, busy.Body.String())
	}
}

// stallingWriter blocks on its first body write until released, like a
// client on a slow link.
type stallingWriter struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
	finished chan struct{}
}

func (w *stallingWriter) Header() http.Header { return w.header }
func (w *stallingWriter) WriteHeader(status int) {
	w.status = status
}
func (w *stallingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.started)
		<-w.release
	})
	return w.body.Write(p)
}

func TestBackupHoldsNoReadTransactionDuringASlowDownload(t *testing.T) {
	handler, store, _ := platformFixture(t)
	handler.UseSystem(SystemInfo{BackupTempDir: t.TempDir()})
	// Writers keep changing state while the copy is made.
	stop := make(chan struct{})
	writersDone := make(chan struct{})
	go func() {
		defer close(writersDone)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = store.Update(func(state *model.State) error {
				state.Sites[1].Name = "B" + strings.Repeat("x", i%7)
				return nil
			})
		}
	}()
	writer := &stallingWriter{header: http.Header{}, started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/api/system/backup", nil)
	req.Host = "127.0.0.1:8088"
	go func() {
		defer close(writer.finished)
		handler.ServeHTTP(writer, req)
	}()
	select {
	case <-writer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("download never started")
	}
	close(stop)
	<-writersDone
	// The client is stalled mid-download. A write that grows the database
	// past its current mmap must still complete: with a read transaction
	// open for the download it would block until the client finished.
	grown := make(chan error, 1)
	go func() {
		grown <- store.PutMetaJSON("test_growth", strings.Repeat("y", 24<<20))
	}()
	select {
	case err := <-grown:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		close(writer.release)
		t.Fatal("a database write blocked behind a stalled backup download")
	}
	close(writer.release)
	<-writer.finished
	if writer.status != http.StatusOK {
		t.Fatalf("status %d", writer.status)
	}
	openBackup(t, writer.body.Bytes())
}

// In the chart /tmp is an emptyDir with a sizeLimit: statfs reports the
// node's free space, and going over the limit evicts the pod. A database
// larger than the staging limit is refused before anything is copied.
func TestBackupDownloadRespectsTheStagingLimit(t *testing.T) {
	handler, store, _ := platformFixture(t)
	size, err := store.Size()
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	handler.UseSystem(SystemInfo{BackupTempDir: staging, BackupTempMax: size})
	response := request(t, handler, http.MethodGet, "/api/system/backup", "")
	if response.Code != http.StatusInsufficientStorage || decodeErrorBody(t, "over the limit", response).Code != api.ErrorCodeUnavailable {
		t.Fatalf("database of %d bytes with a %d-byte staging limit: %d %s", size, size, response.Code, response.Body.String())
	}
	if left, _ := os.ReadDir(staging); len(left) != 0 {
		t.Fatalf("staged %v over the limit", left)
	}
	handler.UseSystem(SystemInfo{BackupTempDir: staging, BackupTempMax: size + 2<<20})
	if response := request(t, handler, http.MethodGet, "/api/system/backup", ""); response.Code != http.StatusOK {
		t.Fatalf("under the limit: %d %s", response.Code, response.Body.String())
	}
}
