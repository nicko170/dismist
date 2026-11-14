package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/dnsserver"
	"github.com/nicko170/dismist/internal/model"
)

type apBinding struct {
	address    string
	secretFile string
}

type apRegistry struct {
	manager      *controller.Manager
	resolver     *dnsserver.Resolver
	options      registryOptions
	active       map[string]apBinding
	mutationLock *sync.Mutex
}

func newAPRegistry(manager *controller.Manager, resolver *dnsserver.Resolver, options registryOptions, initial model.State) *apRegistry {
	registry := &apRegistry{
		manager: manager, resolver: resolver, options: options,
		active: make(map[string]apBinding), mutationLock: options.MutationLock,
	}
	if registry.mutationLock == nil {
		registry.mutationLock = new(sync.Mutex)
	}
	for _, ap := range initial.APs {
		if _, ok := manager.Server(ap.ID); ok {
			registry.active[ap.ID] = bindingForAP(ap, options)
		}
	}
	return registry
}

// reconcileCurrent takes the shared mutation lock before reading state, so a
// concurrent site edit or AP move cannot commit between AP preparation and
// activation. The HTTP API uses the same lock for configuration mutations.
func (r *apRegistry) reconcileCurrent(store model.StateStore) error {
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	return r.reconcile(store.Snapshot())
}

func bindingForAP(ap model.APRecord, options registryOptions) apBinding {
	address := ap.ManagementIP
	if address == "" && ap.ID == options.DefaultID {
		address = options.DefaultIP
	}
	return apBinding{address: address, secretFile: ap.SecretFile}
}

// reconcile applies registration changes only. The HTTP API updates existing
// AP configs with SetConfig, so replacing their sessions here would disconnect
// APs for ordinary site or WLAN edits.
func (r *apRegistry) reconcile(state model.State) error {
	desired := make(map[string]apBinding, len(state.APs))
	for _, ap := range state.APs {
		binding := bindingForAP(ap, r.options)
		if ap.SiteID != "" {
			desired[ap.ID] = binding
		}
	}
	// Withdraw stale registrations before adding new ones. The DNS allowlist
	// is rebuilt from the active set afterwards, so APs may share addresses.
	defer r.syncDNS()
	for id, active := range r.active {
		if desired[id] == active {
			continue
		}
		r.manager.Remove(id)
		delete(r.active, id)
		log.Printf("AP %s registration withdrawn%s", id, fromAddress(active.address))
	}
	var failures []error
	for _, ap := range state.APs {
		binding, wanted := desired[ap.ID]
		if !wanted || r.active[ap.ID] == binding {
			continue
		}
		one := state
		one.APs = []model.APRecord{ap}
		prepared, _, err := prepareAPs(one, r.options)
		if err != nil {
			failures = append(failures, fmt.Errorf("AP %s: %w", ap.ID, err))
			continue
		}
		server, ok := prepared.Server(ap.ID)
		if !ok {
			failures = append(failures, fmt.Errorf("AP %s: no control session prepared", ap.ID))
			continue
		}
		if err := r.manager.Register(ap.ID, binding.address, server); err != nil {
			failures = append(failures, fmt.Errorf("AP %s: %w", ap.ID, err))
			continue
		}
		r.active[ap.ID] = binding
		log.Printf("AP %s registered%s", ap.ID, fromAddress(binding.address))
	}
	return errors.Join(failures...)
}

// syncDNS allows AP DNS for every active AP's pinned address.
func (r *apRegistry) syncDNS() {
	addresses := make([]string, 0, len(r.active))
	for _, binding := range r.active {
		if binding.address != "" {
			addresses = append(addresses, binding.address)
		}
	}
	if err := r.resolver.ReplaceAPs(addresses); err != nil {
		log.Printf("AP DNS allowlist: %v", err)
	}
}

func fromAddress(address string) string {
	if address == "" {
		return " (any source address)"
	}
	return " pinned to " + address
}

func runAPRegistry(ctx context.Context, store model.StateStore, registry *apRegistry, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := registry.reconcileCurrent(store)
			if err == nil {
				lastError = ""
			} else if err.Error() != lastError {
				lastError = err.Error()
				log.Printf("AP registry reconciliation: %v", err)
			}
		}
	}
}
