package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/nicko170/dismist/internal/metrics"
)

// metricsConfig holds the -metrics-* flags.
type metricsConfig struct {
	listen      string
	allowRemote bool
	tokenFile   string
	names       bool
	clients     string
	clientLimit int
}

// registerMetricsFlags defines the -metrics-* flags on fs.
func registerMetricsFlags(fs *flag.FlagSet) *metricsConfig {
	c := new(metricsConfig)
	fs.StringVar(&c.listen, "metrics-listen", "127.0.0.1:9090", "Prometheus metrics listen address: a numeric loopback IP and port unless -metrics-allow-remote is set (empty disables)")
	fs.BoolVar(&c.allowRemote, "metrics-allow-remote", false, "allow -metrics-listen on any numeric address, including \":port\", 0.0.0.0 and [::] (hostnames are always rejected); pair it with -metrics-token-file")
	fs.StringVar(&c.tokenFile, "metrics-token-file", "", "private file (mode 0600) holding a bearer token every scrape must present as \"Authorization: Bearer <token>\" (read at startup)")
	fs.BoolVar(&c.names, "metrics-names", false, "add site, AP and WLAN names as a name label on *_info series")
	fs.StringVar(&c.clients, "metrics-clients", string(metrics.ClientsAggregate), "client series: off, aggregate or per-client (per-client exports client MACs)")
	fs.IntVar(&c.clientLimit, "metrics-client-limit", 100, "most clients exported with -metrics-clients=per-client")
	return c
}

// startMetrics checks the -metrics-* flags, binds the listener and serves
// /metrics in the background. It returns a nil server when -metrics-listen is
// empty; otherwise the server's Addr is the bound address. options supplies
// the data sources; the flags fill in the rest.
func startMetrics(c *metricsConfig, options metrics.Options) (*http.Server, error) {
	if c.listen == "" {
		return nil, nil
	}
	mode, err := metrics.ParseClientMode(c.clients)
	if err != nil {
		return nil, fmt.Errorf("-metrics-clients: %w", err)
	}
	if c.clientLimit < 0 {
		return nil, errors.New("-metrics-client-limit must not be negative")
	}
	options.Names, options.Clients, options.ClientLimit = c.names, mode, c.clientLimit
	if c.tokenFile != "" {
		token, err := metrics.ReadTokenFile(c.tokenFile)
		if err != nil {
			return nil, fmt.Errorf("-metrics-token-file: %w", err)
		}
		options.Token = token
	}
	handler, err := metrics.NewHandler(options)
	if err != nil {
		return nil, err
	}
	listener, err := listenMetrics(c.listen, c.allowRemote)
	if err != nil {
		return nil, err
	}
	// Addr records the bound address (with the port chosen for ":0").
	server := &http.Server{Addr: listener.Addr().String(), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics listener stopped: %v", err)
		}
	}()
	auth := "no authentication"
	if options.Token != "" {
		auth = "bearer token required"
	}
	log.Printf("Prometheus metrics listening on %s (%s)", listener.Addr(), auth)
	if !isLoopbackListen(c.listen) && options.Token == "" {
		log.Printf("WARNING: /metrics on %s is readable without a token by anything that can reach it; set -metrics-token-file or restrict access to the port", listener.Addr())
	}
	return server, nil
}

// metricsListener binds a numeric loopback address only. main.go uses it
// until it switches to startMetrics.
func metricsListener(address string) (net.Listener, error) {
	return listenMetrics(address, false)
}

// listenMetrics binds the metrics listener. Without allowRemote the host
// must be a numeric loopback IP, so an unauthenticated scrape endpoint cannot
// be exposed by accident. With it, an empty host (":9090", both address
// families), the unspecified addresses and any other numeric IP are accepted.
// Hostnames, including localhost, are always rejected: what they resolve to
// is not under the operator's control.
func listenMetrics(address string, allowRemote bool) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	switch {
	case host == "" && allowRemote:
	case host == "":
		return nil, errors.New("metrics listen address must be a numeric loopback IP; set -metrics-allow-remote to listen on all addresses")
	default:
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return nil, fmt.Errorf("metrics listen address must use a numeric IP, not %q", host)
		}
		if !allowRemote && !ip.IsLoopback() {
			return nil, errors.New("metrics listen address must be a numeric loopback IP unless -metrics-allow-remote is set")
		}
	}
	return net.Listen("tcp", address)
}

func isLoopbackListen(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
