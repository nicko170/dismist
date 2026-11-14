package main

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/protocol"
	"github.com/nicko170/dismist/internal/telemetry"
)

type registryOptions struct {
	DefaultID          string
	DefaultIP          string
	Base               []byte
	CertDER            []byte
	ServerName         string
	OnTelemetry        func([]byte)
	OnAPFrame          func(protocol.Message)
	UDPProbeAddress    string
	UDPProbeAAD        []byte
	UDPProbeSendTest   bool
	UDPProbeRekeyDelay time.Duration
	MutationLock       *sync.Mutex
	// APConfig is the ap_config store (BoltStore.APConfig()). An AP whose
	// config fails to compile is served its last delivered config from it,
	// and every ep-config reply is recorded in it (design §4.4). nil
	// disables both: a failing AP is then registered with no config.
	APConfig apConfigStore
	// Features are the daemon's -ap-features defaults; each AP's stored
	// protocol_features override them (design §5.7). Zero: all off.
	// -config-push=notify adds config_change_notify to them.
	Features controller.Features

	// Phase 1 to 4 protocol hooks (design §5). Each is optional.

	// Events receives every AP's typed events (eventSink.Event).
	Events func(controller.Event)
	// Status persists hellos, reboots, the VAP list, the tunnel address
	// and the ep-config request in ap_status (newAPStatusSink once the
	// model's APStatusStore is merged).
	Status controller.StatusSink
	// LastKnownVAPs returns an AP's persisted VAP list (ap_status), the
	// unchanged-config verification's baseline after a restart.
	LastKnownVAPs func(apID string) []telemetry.VAP
	// OnPush receives push lifecycle states (SSE "push").
	OnPush func(controller.PushEvent)
	// OnPushModeLearned stores a learned push mode (APRecord.PushMode).
	OnPushModeLearned func(apID, mode string)
	// PushMode returns an AP's stored push mode (APRecord.PushMode).
	PushMode func(ap model.APRecord) string
	// LocateUntil returns an AP's locate expiry (APRecord.LocateUntil);
	// OnLocateExpired clears it and pushes LED.Locator=false.
	LocateUntil     func(ap model.APRecord) time.Time
	OnLocateExpired func(apID string)
	// OnVerified stores verification results; nil stores them in
	// ap_config.delivered.verified through APConfig.
	OnVerified func(apID string, result controller.Verification)
	// OnScan stores neighbour scans (scans bucket, phase 3).
	OnScan func(apID string, at time.Time, scan telemetry.Scan)
	// OnCommandOutput receives ap-cmd-output- messages (phase 4).
	OnCommandOutput func(apID string, output telemetry.CommandOutput)
	// Timings overrides the protocol timers (tests).
	Timings controller.Timings
}

// verifiedStore is the optional part of the ap_config store that records
// verification results (*model.APConfigStore implements it).
type verifiedStore interface {
	SetVerified(apID string, verification model.Verification) error
}

// apConfigStore is the part of *model.APConfigStore the registry uses.
type apConfigStore interface {
	Delivered(apID string) (model.DeliveredConfig, bool, error)
	PutDelivered(apID string, delivered model.DeliveredConfig) error
}

// errNoSecret marks an AP that cannot be served until its secret is stored.
var errNoSecret = errors.New("AP has no secret; upload it in the UI")

// prepareAPs builds a control session for every AP assigned to a site,
// each on its own (design §4.4 "One AP never stops another"): an AP without
// a secret is skipped, an AP whose config fails to compile is served its
// last delivered config (or no config when it never received one), and an
// AP whose session cannot be built is logged and skipped. The error is
// only for options every AP shares.
func prepareAPs(state model.State, options registryOptions) (*controller.Manager, []string, error) {
	if err := controller.CheckUDPProbe(options.UDPProbeAddress, options.UDPProbeAAD, options.UDPProbeSendTest, options.UDPProbeRekeyDelay); err != nil {
		return nil, nil, err
	}
	manager := controller.NewManager()
	addresses := make([]string, 0, len(state.APs))
	records := make(map[string]model.APRecord, len(state.APs))
	for _, ap := range state.APs {
		records[ap.ID] = ap
	}
	var delivered compile.DeliveredLookup
	if options.APConfig != nil {
		delivered = options.APConfig
	}
	for _, served := range compile.Prepare(options.Base, state, delivered) {
		ap := records[served.APID]
		server, err := newAPServer(ap, served, options)
		if err != nil {
			log.Printf("AP %s is not served: %v", ap.ID, err)
			continue
		}
		if server == nil {
			continue
		}
		address := bindingAddress(ap, options)
		if err := manager.Register(ap.ID, address, server); err != nil {
			log.Printf("AP %s is not served: %v", ap.ID, err)
			continue
		}
		if address != "" {
			addresses = append(addresses, address)
		}
	}
	return manager, addresses, nil
}

// bindingAddress is the AP's optional source pin: its management IP, or
// the -ap-ip fallback for the -ap-id AP.
func bindingAddress(ap model.APRecord, options registryOptions) string {
	if ap.ManagementIP == "" && ap.ID == options.DefaultID {
		return options.DefaultIP
	}
	return ap.ManagementIP
}

// newAPServer builds the control session for one prepared AP. It returns
// nil and no error for an AP that is not assigned to a site, and
// errNoSecret for an AP without a secret.
func newAPServer(ap model.APRecord, served compile.Served, options registryOptions) (*controller.Server, error) {
	switch served.Skip {
	case compile.SkipNoSite:
		return nil, nil
	case compile.SkipNoSecret:
		return nil, errNoSecret
	}
	var secret [model.SecretSize]byte
	copy(secret[:], ap.Secret)
	serverOptions := controller.Options{
		AllowedIP: bindingAddress(ap, options), ID: ap.ID, Secret: secret,
		CertDER: options.CertDER, Config: served.Config, Revision: served.Revision,
		ServerName:         options.ServerName,
		Features:           controller.ResolveFeatures(options.Features, ap.Features),
		OnTelemetry:        options.OnTelemetry,
		OnAPFrame:          options.OnAPFrame,
		UDPProbeAddress:    options.UDPProbeAddress,
		UDPProbeAAD:        options.UDPProbeAAD,
		UDPProbeSendTest:   options.UDPProbeSendTest,
		UDPProbeRekeyDelay: options.UDPProbeRekeyDelay,
	}
	if options.APConfig != nil {
		serverOptions.OnConfigDelivered = recordDelivered(options.APConfig)
		if delivered, ok, err := options.APConfig.Delivered(ap.ID); err == nil && ok {
			serverOptions.LastDelivered = delivered.Bytes
		}
		if store, ok := options.APConfig.(verifiedStore); ok && options.OnVerified == nil {
			serverOptions.OnVerified = verificationRecorder(store)
		}
	}
	applyProtocolOptions(&serverOptions, ap, options)
	switch served.Source {
	case compile.SourceCompiled:
	case compile.SourceDelivered:
		serverOptions.ConfigSource = controller.ConfigSourceDelivered
		serverOptions.CompileError = errorText(served.Err)
		log.Printf("AP %s config does not compile (%v); serving its last delivered config (revision %d)", ap.ID, served.Err, served.Revision)
	default:
		serverOptions.ConfigSource = controller.ConfigSourceNone
		serverOptions.Config = nil
		serverOptions.CompileError = errorText(served.Err)
		log.Printf("AP %s config does not compile (%v) and it was never delivered one; it is not served a config", ap.ID, served.Err)
	}
	server, err := controller.NewServer(serverOptions)
	if err != nil {
		return nil, fmt.Errorf("control session: %w", err)
	}
	return server, nil
}

// applyProtocolOptions sets an AP's phase 1 to 4 protocol hooks.
func applyProtocolOptions(serverOptions *controller.Options, ap model.APRecord, options registryOptions) {
	serverOptions.OnEvent = options.Events
	serverOptions.Status = options.Status
	serverOptions.OnPush = options.OnPush
	serverOptions.OnPushModeLearned = options.OnPushModeLearned
	serverOptions.OnScan = options.OnScan
	serverOptions.OnCommandOutput = options.OnCommandOutput
	serverOptions.OnLocateExpired = options.OnLocateExpired
	serverOptions.Timings = options.Timings
	serverOptions.LocalFiles = effective.LocalFilesState(ap.LocalFiles)
	if options.OnVerified != nil {
		serverOptions.OnVerified = options.OnVerified
	}
	if options.LastKnownVAPs != nil {
		serverOptions.LastKnownVAPs = options.LastKnownVAPs(ap.ID)
	}
	if options.PushMode != nil {
		switch mode := options.PushMode(ap); mode {
		case controller.PushModeNotify, controller.PushModeReconnect:
			serverOptions.PushMode = mode
		}
	}
	if options.LocateUntil != nil {
		serverOptions.LocateUntil = options.LocateUntil(ap)
	}
}

func errorText(err error) string {
	if err == nil {
		return "config does not compile"
	}
	return err.Error()
}

// recordDelivered writes ap_config.delivered after every ep-config reply,
// so a later compile failure can fall back to exactly these bytes.
func recordDelivered(store apConfigStore) func(controller.ConfigDelivery) {
	return func(delivery controller.ConfigDelivery) {
		err := store.PutDelivered(delivery.APID, model.DeliveredConfig{
			Revision: delivery.Revision, Timestamp: delivery.Timestamp,
			Bytes: delivery.Config, At: delivery.At.UTC(),
		})
		if err != nil {
			log.Printf("AP %s: cannot record the delivered config: %v", delivery.APID, err)
		}
	}
}
