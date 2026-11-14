package main

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/protocol"
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
	Features controller.Features
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
	}
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
