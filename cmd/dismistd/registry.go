package main

import (
	"fmt"
	"sync"
	"time"

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
}

func prepareAPs(state model.State, options registryOptions) (*controller.Manager, []string, error) {
	manager := controller.NewManager()
	addresses := make([]string, 0, len(state.APs))
	sites := make(map[string]model.Site, len(state.Sites))
	for _, site := range state.Sites {
		sites[site.ID] = site
	}
	for _, ap := range state.APs {
		if ap.SiteID == "" {
			continue
		}
		// The management IP is an optional source pin (and the AP DNS
		// allowlist); APs are identified by their authenticated ID.
		address := ap.ManagementIP
		if address == "" && ap.ID == options.DefaultID {
			address = options.DefaultIP
		}
		configTime := ap.ConfigUpdatedAt
		if configTime.IsZero() {
			configTime = state.UpdatedAt
		}
		configRevision := ap.ConfigRevision
		if configRevision == 0 {
			configRevision = state.Revision
		}
		site, exists := sites[ap.SiteID]
		if !exists {
			return nil, nil, fmt.Errorf("AP %s has no site", ap.ID)
		}
		if len(ap.Secret) != model.SecretSize {
			return nil, nil, fmt.Errorf("AP %s has no secret; upload it in the UI", ap.ID)
		}
		var secret [model.SecretSize]byte
		copy(secret[:], ap.Secret)
		networks := make([]model.Network, 0)
		for _, network := range state.Networks {
			if network.SiteID == ap.SiteID {
				networks = append(networks, network)
			}
		}
		config, err := controller.BuildSiteConfig(options.Base, site, ap, networks, configTime)
		if err != nil {
			return nil, nil, fmt.Errorf("AP %s config: %w", ap.ID, err)
		}
		server, err := controller.NewServer(controller.Options{
			AllowedIP: address, ID: ap.ID, Secret: secret,
			CertDER: options.CertDER, Config: config, Revision: configRevision,
			ServerName: options.ServerName, OnTelemetry: options.OnTelemetry,
			OnAPFrame:          options.OnAPFrame,
			UDPProbeAddress:    options.UDPProbeAddress,
			UDPProbeAAD:        options.UDPProbeAAD,
			UDPProbeSendTest:   options.UDPProbeSendTest,
			UDPProbeRekeyDelay: options.UDPProbeRekeyDelay,
		})
		if err != nil {
			return nil, nil, err
		}
		if err := manager.Register(ap.ID, address, server); err != nil {
			return nil, nil, err
		}
		if address != "" {
			addresses = append(addresses, address)
		}
	}
	return manager, addresses, nil
}
