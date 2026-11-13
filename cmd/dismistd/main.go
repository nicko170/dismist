package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nicko170/dismist/internal/dnsserver"
	"github.com/nicko170/dismist/internal/httpapi"
	"github.com/nicko170/dismist/internal/metrics"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/protocol"
)

func main() {
	dnsListen := flag.String("dns-listen", "0.0.0.0:53", "AP DNS UDP/TCP listen address")
	wsListen := flag.String("ws-listen", "0.0.0.0:443", "AP WSS listen address")
	apIP := flag.String("ap-ip", "", "management IP fallback for -ap-id (legacy AP records)")
	apID := flag.String("ap-id", "", "default AP ID for the UI and legacy AP records")
	controllerIP := flag.String("controller-ip", "", "IPv4 address advertised by AP DNS (auto-detected when one usable address exists)")
	upstream := flag.String("upstream-dns", "1.1.1.1:53", "upstream DNS resolver")
	configPath := flag.String("ap-config", "data/ap-config.json", "AP config template JSON path")
	statePath := flag.String("state", "", "optional legacy state JSON for one-time migration into a new database")
	dbPath := flag.String("db", "data/controller.db", "private bbolt controller database path")
	certPath := flag.String("tls-cert", "data/tls.crt", "AP WSS TLS certificate path")
	keyPath := flag.String("tls-key", "data/tls.key", "AP WSS TLS private key path")
	uiListen := flag.String("ui-listen", "0.0.0.0:8088", "LAN UI and API listen address")
	metricsListen := flag.String("metrics-listen", "127.0.0.1:9090", "loopback-only Prometheus metrics listen address (empty disables)")
	uiHost := flag.String("ui-host", "", "optional DNS hostname allowed for the UI and API")
	adminTokenFile := flag.String("admin-token-file", "data/admin.token", "private administrator token file (created if missing)")
	serverName := flag.String("server-name", "mistlocal", "server name in the AP authentication response")
	captureDir := flag.String("capture-telemetry-dir", "", "private directory for a bounded telemetry research capture")
	captureAPFramesDir := flag.String("capture-ap-frames-dir", "", "private directory for bounded authenticated AP frame captures")
	udpProbeAddress := flag.String("udp-probe-address", "", "optional UDP destination advertised to the AP for protocol diagnostics")
	udpProbeSetupFile := flag.String("udp-probe-setup-file", "", "private Mist UDP setup JSON supplying the probe AAD")
	udpProbeSendTest := flag.Bool("udp-probe-send-test", false, "send a padded UDP test message during the local protocol probe")
	udpProbeRekeyDelay := flag.Duration("udp-probe-rekey-delay", 0, "delay AP UDP test until upstream rekey can be relayed")
	flag.Parse()
	var interfaceAddresses []net.IP
	if *controllerIP == "" {
		var err error
		interfaceAddresses, err = interfaceIPv4s()
		if err != nil {
			log.Fatalf("discover controller address: %v; set -controller-ip", err)
		}
	}
	advertisedIP, err := selectControllerIP(*controllerIP, interfaceAddresses)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("AP DNS advertises controller address %s", advertisedIP)
	var udpProbeAAD []byte
	if *udpProbeSetupFile != "" {
		raw, err := os.ReadFile(*udpProbeSetupFile)
		if err != nil {
			log.Fatal(err)
		}
		var setup struct {
			AAD []byte `json:"aad"`
		}
		if err := json.Unmarshal(raw, &setup); err != nil || len(setup.AAD) != 8 {
			log.Fatal("invalid UDP probe setup file")
		}
		udpProbeAAD = setup.AAD
	}
	var captureTelemetry func([]byte)
	if *captureDir != "" {
		var err error
		captureTelemetry, err = newTelemetryCapture(*captureDir, 500)
		if err != nil {
			log.Fatal(err)
		}
	}
	var captureAPFrame func(protocol.Message)
	if *captureAPFramesDir != "" {
		saveFrame, err := newTelemetryCapture(*captureAPFramesDir, 1500)
		if err != nil {
			log.Fatal(err)
		}
		captureAPFrame = func(message protocol.Message) {
			encoded, err := protocol.Encode(message)
			if err == nil {
				saveFrame(encoded)
			}
		}
	}
	base, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("read AP config template %q (-ap-config): %v", *configPath, err)
	}
	certificate, err := tls.LoadX509KeyPair(*certPath, *keyPath)
	if err != nil || len(certificate.Certificate) == 0 {
		log.Fatalf("load AP WSS certificate (-tls-cert %q, -tls-key %q): %v", *certPath, *keyPath, err)
	}
	store, err := model.OpenBolt(*dbPath, *statePath)
	if err != nil {
		log.Fatalf("open controller database %q (-db): %v", *dbPath, err)
	}
	defer store.Close()
	state := store.Snapshot()
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	mutationLock := new(sync.Mutex)
	registryConfig := registryOptions{
		DefaultID: *apID, DefaultIP: *apIP, Base: base,
		CertDER: certificate.Certificate[0], ServerName: *serverName,
		OnTelemetry: captureTelemetry, OnAPFrame: captureAPFrame,
		MutationLock:       mutationLock,
		UDPProbeAddress:    *udpProbeAddress,
		UDPProbeAAD:        udpProbeAAD,
		UDPProbeSendTest:   *udpProbeSendTest,
		UDPProbeRekeyDelay: *udpProbeRekeyDelay,
	}
	manager, addresses, err := prepareAPs(state, registryConfig)
	if err != nil {
		log.Fatal(err)
	}
	if _, ok := manager.Server(*apID); !ok {
		for _, ap := range state.APs {
			if _, ok := manager.Server(ap.ID); ok {
				*apID = ap.ID
				break
			}
		}
	}
	resolver, err := newAPDNSResolver(addresses, "ep-terminator.mistsys.net", advertisedIP, *upstream)
	if err != nil {
		log.Fatal(err)
	}
	registry := newAPRegistry(manager, resolver, registryConfig, state)
	service, err := dnsserver.Start(*dnsListen, resolver)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()
	log.Printf("AP-scoped DNS listening on %s", service.Addr())
	listener, err := net.Listen("tcp", *wsListen)
	if err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{
		Handler: manager, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}},
	}
	go func() {
		err := httpServer.Serve(tls.NewListener(listener, httpServer.TLSConfig))
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("AP WSS listener stopped: %v", err)
		}
	}()
	log.Printf("AP WSS listening on %s", listener.Addr())
	uiListener, err := net.Listen("tcp", *uiListen)
	if err != nil {
		log.Fatal(err)
	}
	uiHandler, err := operatorHandler(*adminTokenFile, httpapi.NewWithManager(store, manager, base, *apID, advertisedIP, *uiHost).UseMutationLock(mutationLock))
	if err != nil {
		log.Fatalf("initialize administrator access: %v", err)
	}
	uiServer := &http.Server{
		Handler:           uiHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		err := uiServer.Serve(uiListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("UI API listener stopped: %v", err)
		}
	}()
	log.Printf("UI API listening on %s", uiListener.Addr())
	var metricsServer *http.Server
	if *metricsListen != "" {
		metricsListener, err := metricsListener(*metricsListen)
		if err != nil {
			log.Fatalf("start metrics listener: %v", err)
		}
		metricsServer = &http.Server{
			Handler: metrics.New(store, manager), ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			err := metricsServer.Serve(metricsListener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("metrics listener stopped: %v", err)
			}
		}()
		log.Printf("Prometheus metrics listening on %s", metricsListener.Addr())
	}
	log.Printf("Administrator token file: %s", *adminTokenFile)
	registryCtx, stopRegistry := context.WithCancel(context.Background())
	registryDone := make(chan struct{})
	go func() {
		defer close(registryDone)
		runAPRegistry(registryCtx, store, registry, 2*time.Second)
	}()
	recordStop := make(chan struct{})
	recordDone := make(chan struct{})
	go func() {
		defer close(recordDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		record := func() {
			current := store.Snapshot()
			for id, control := range manager.Servers() {
				for _, ap := range current.APs {
					if ap.ID != id {
						continue
					}
					observation := makeObservation(ap.SiteID, ap.ID, control.Status(), control.TelemetrySnapshot(), time.Now().UTC())
					if err := store.RecordObservation(observation); err != nil {
						log.Printf("cannot save AP observation: %v", err)
					}
					break
				}
			}
		}
		record()
		for {
			select {
			case <-ticker.C:
				record()
			case <-recordStop:
				return
			}
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	stopRegistry()
	<-registryDone
	close(recordStop)
	<-recordDone
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	_ = uiServer.Shutdown(ctx)
	if metricsServer != nil {
		_ = metricsServer.Shutdown(ctx)
	}
}

func loadSecret(path string) ([32]byte, error) {
	var secret [32]byte
	info, err := os.Stat(path)
	if err != nil {
		return secret, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return secret, errors.New("AP secret file must be private")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return secret, err
	}
	if len(raw) != len(secret) {
		return secret, errors.New("AP secret must be exactly 32 bytes")
	}
	copy(secret[:], raw)
	return secret, nil
}

func newTelemetryCapture(dir string, limit uint64) (func([]byte), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	var count atomic.Uint64
	return func(body []byte) {
		number := count.Add(1)
		if number > limit {
			return
		}
		name := fmt.Sprintf("%d-%06d.bin", time.Now().UnixNano(), number)
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			log.Printf("telemetry capture failed: %v", err)
		}
	}, nil
}
