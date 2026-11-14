// Command fakeap runs a software AP33 against a dismist controller: the
// real get-time and ep-auth v2 handshake, config fetch and apply report,
// synthetic stats frames and optional synthetic PACE events. It is for
// local runs, the Playwright e2e harness and protocol tests; it never
// talks to a real AP.
//
//	fakeap -url wss://127.0.0.1:8443/ws -id 02-aa-00-00-00-01 \
//	  -secret-file ap-secret.bin -ca controller-ca.pem
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nicko170/dismist/tools/fakeap"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("fakeap", flag.ContinueOnError)
	url := flags.String("url", "", "controller AP endpoint, for example wss://127.0.0.1:443/ws (required)")
	id := flags.String("id", "", "AP ID, for example 02-aa-00-00-00-01 (required)")
	secretText := flags.String("secret", "", "AP secret: 32 bytes as hex or base64")
	secretFile := flags.String("secret-file", "", "file holding the 32-byte AP secret (raw, hex or base64)")
	caFile := flags.String("ca", "", "PEM CA bundle that verifies the controller certificate")
	insecure := flags.Bool("insecure", false, "skip controller certificate verification (test controllers only)")
	serverName := flags.String("sni", "", "TLS server name to send and verify (default: the URL host)")
	model := flags.String("model", fakeap.DefaultModel, "model in the hello")
	firmware := flags.String("firmware", fakeap.DefaultFirmware, "firmware in the hello")
	serial := flags.String("serial", "EXAMPLE000001", "SerialNo in the hello")
	bootCount := flags.Uint64("boot-count", 7, "BootCount in the hello and stats")
	uptime := flags.Int64("uptime", 120, "uptime in seconds at the first connection")
	rebootReason := flags.String("reboot-reason", "unknown", "LastRebootReason in the hello")
	localIP := flags.String("local-ip", "192.0.2.10", "LocalIP in the hello")
	clients := flags.Int("clients", 3, "synthetic associated clients")
	fast := flags.Duration("fast", 2*time.Second, "fast stats interval (negative disables)")
	full := flags.Duration("full", 60*time.Second, "full stats interval (negative disables)")
	ping := flags.Duration("ping", 30*time.Second, "ping interval (negative disables)")
	events := flags.Duration("events", 0, "synthetic PACE event interval (0 disables)")
	reject := flags.String("reject-config", "", "report every config as rejected with this error")
	once := flags.Bool("once", false, "authenticate, fetch the config, print it and exit")
	reconnect := flags.Duration("reconnect", 2*time.Second, "delay before reconnecting after the session ends (0 exits instead)")
	duration := flags.Duration("duration", 0, "stop after this long (0 runs until interrupted)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *url == "" || *id == "" {
		flags.Usage()
		return errors.New("-url and -id are required")
	}
	secret, err := loadSecret(*secretText, *secretFile)
	if err != nil {
		return err
	}
	tlsConfig, err := clientTLS(*caFile, *insecure, *serverName)
	if err != nil {
		return err
	}
	extras := fakeap.DefaultExtras()
	extras.SerialNo, extras.BootCount, extras.LastRebootReason, extras.LocalIP = *serial, *bootCount, *rebootReason, *localIP
	extras.Uptime, extras.ConnectUptime = *uptime, *uptime
	config := fakeap.Config{
		URL: *url, ID: *id, Secret: secret, Model: *model, Firmware: *firmware,
		Extras: extras, TLS: tlsConfig,
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *duration > 0 {
		var stopTimer context.CancelFunc
		ctx, stopTimer = context.WithTimeout(ctx, *duration)
		defer stopTimer()
	}
	if *once {
		ap, err := fakeap.Connect(ctx, config)
		if err != nil {
			return err
		}
		defer ap.Close()
		body, err := ap.FetchConfig(ctx, fakeap.ConfigRequest{})
		if err != nil {
			return err
		}
		_, err = fmt.Println(string(body))
		return err
	}
	snapshot := fakeap.DefaultSnapshot(*clients)
	snapshot.BootCount = *bootCount
	snapshot.Uptime = uint64(max(*uptime, 0))
	started := time.Now()
	for {
		config.Extras.Uptime = *uptime + int64(time.Since(started)/time.Second)
		config.Extras.ConnectUptime = config.Extras.Uptime
		config.Counter = 0
		ap, err := fakeap.Connect(ctx, config)
		if err == nil {
			log.Printf("fakeap %s authenticated to %s (%s)", *id, *url, ap.Session().ServerName)
			snapshot.Uptime = uint64(max(config.Extras.Uptime, 0))
			err = ap.Run(ctx, fakeap.RunOptions{
				Snapshot: snapshot, FastInterval: *fast, FullInterval: *full,
				PingInterval: *ping, EventInterval: *events, RejectConfig: *reject,
				OnConfig: func(body []byte) { log.Printf("fakeap %s fetched a %d-byte config", *id, len(body)) },
			})
			_ = ap.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, fakeap.ErrServerProof) {
			return err
		}
		log.Printf("fakeap %s session ended: %v", *id, err)
		if *reconnect <= 0 {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*reconnect):
		}
	}
}

// loadSecret accepts the forms the dismist API accepts: 32 raw bytes in a
// file, or 64 hex characters, or base64 of 32 bytes.
func loadSecret(text, path string) ([32]byte, error) {
	var secret [32]byte
	switch {
	case text != "" && path != "":
		return secret, errors.New("use -secret or -secret-file, not both")
	case path != "":
		raw, err := os.ReadFile(path)
		if err != nil {
			return secret, err
		}
		if len(raw) == len(secret) {
			copy(secret[:], raw)
			return secret, nil
		}
		text = strings.TrimSpace(string(raw))
	case text == "":
		return secret, errors.New("-secret or -secret-file is required")
	}
	text = strings.TrimSpace(text)
	if decoded, err := hex.DecodeString(text); err == nil && len(decoded) == len(secret) {
		copy(secret[:], decoded)
		return secret, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(text); err == nil && len(decoded) == len(secret) {
		copy(secret[:], decoded)
		return secret, nil
	}
	return secret, errors.New("the AP secret must be 32 bytes (raw file, hex or base64)")
}

func clientTLS(caFile string, insecure bool, serverName string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	switch {
	case caFile != "" && insecure:
		return nil, errors.New("use -ca or -insecure, not both")
	case insecure:
		config.InsecureSkipVerify = true // Explicitly requested for test controllers.
	case caFile != "":
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificates", caFile)
		}
		config.RootCAs = pool
	default:
		return nil, errors.New("-ca or -insecure is required to connect")
	}
	return config, nil
}
