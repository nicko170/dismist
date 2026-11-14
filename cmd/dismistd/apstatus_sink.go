//go:build apstatus

// This file adapts the model's APStatusStore (workstream A, phase 1) to
// controller.StatusSink. It builds with -tags apstatus until the
// integration merges A's store; then the build tag goes and main.go sets
// registryOptions.Status = newAPStatusSink(store.APStatus()) and
// registryOptions.LastKnownVAPs = lastKnownVAPs(store.APStatus()).

package main

import (
	"fmt"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/telemetry"
)

// apStatusSink stores controller facts in ap_status.
type apStatusSink struct{ store *model.APStatusStore }

func newAPStatusSink(store *model.APStatusStore) controller.StatusSink {
	return apStatusSink{store: store}
}

func helloFacts(hello controller.HelloExtras) model.HelloFacts {
	return model.HelloFacts{
		SKU: hello.SKU, SerialNo: hello.SerialNo, Developer: hello.Developer, Uptime: hello.Uptime,
		ConnectUptime: hello.ConnectUptime, BootCount: hello.BootCount, LastRebootTime: hello.LastRebootTime,
		LastRebootReason: hello.LastRebootReason, LastBootState: hello.LastBootState, LocalIP: hello.LocalIP,
		TimeoutSec: hello.TimeoutSec,
	}
}

func helloExtras(facts model.HelloFacts) controller.HelloExtras {
	return controller.HelloExtras{
		SKU: facts.SKU, SerialNo: facts.SerialNo, Developer: facts.Developer, Uptime: facts.Uptime,
		ConnectUptime: facts.ConnectUptime, BootCount: facts.BootCount, LastRebootTime: facts.LastRebootTime,
		LastRebootReason: facts.LastRebootReason, LastBootState: facts.LastBootState, LocalIP: facts.LocalIP,
		TimeoutSec: facts.TimeoutSec,
	}
}

func (s apStatusSink) RecordHello(apID string, at time.Time, model string, hello controller.HelloExtras) (*controller.HelloExtras, time.Time, error) {
	previous, previousAt, err := s.store.RecordHello(apID, at, model, helloFacts(hello))
	if err != nil || previous == nil {
		return nil, previousAt, err
	}
	extras := helloExtras(*previous)
	return &extras, previousAt, nil
}

func (s apStatusSink) RecordReboot(apID string, reboot controller.Reboot) error {
	return s.store.RecordReboot(apID, model.RebootRecord{
		DetectedAt: reboot.DetectedAt, Evidence: reboot.Evidence, BootCount: reboot.BootCount,
		PreviousBootCount: reboot.PreviousBootCount, Uptime: reboot.Uptime, LastRebootTime: reboot.LastRebootTime,
		Reason: reboot.Reason, BootState: reboot.BootState,
	})
}

func (s apStatusSink) SetVAPs(apID string, at time.Time, vaps []telemetry.VAP) (bool, error) {
	records := make([]model.VAPRecord, 0, len(vaps))
	for _, vap := range vaps {
		records = append(records, model.VAPRecord{
			Dev: vap.Interface, BSSID: vap.BSSID, SSID: vap.SSID, WLANID: vap.WLANID, RadioIndex: vap.RadioIndex,
		})
	}
	return s.store.SetVAPs(apID, at, records)
}

func (s apStatusSink) SetTunnelIPv6(apID string, at time.Time, address string) error {
	return s.store.SetTunnelIPv6(apID, at, address)
}

func (s apStatusSink) SetConfigRequest(apID string, request controller.ConfigRequest) error {
	return s.store.SetConfigRequest(apID, model.ConfigRequestFacts{
		At: request.At, AppliedConfigTimestamp: request.AppliedConfigTimestamp,
		RecoveredFromBadConfig: request.RecoveredFromBadConfig, Capabilities: request.Capabilities,
		MfgCountryCode: request.MfgCountryCode,
	})
}

// lastKnownVAPs loads an AP's persisted VAP list for the unchanged-config
// verification after a restart.
func lastKnownVAPs(store *model.APStatusStore) func(apID string) []telemetry.VAP {
	return func(apID string) []telemetry.VAP {
		record, err := store.Get(apID)
		if err != nil {
			return nil // model.ErrNotFound: never reported
		}
		table := telemetry.RadioTableFor(record.Model)
		vaps := make([]telemetry.VAP, 0, len(record.VAPs))
		for _, vap := range record.VAPs {
			vaps = append(vaps, telemetry.VAP{
				Interface: vap.Dev, BSSID: vap.BSSID, SSID: vap.SSID, WLANID: vap.WLANID, RadioIndex: vap.RadioIndex,
				Radio: fmt.Sprintf("r%d", vap.RadioIndex), Band: table.BandOf(vap.RadioIndex, nil),
			})
		}
		return vaps
	}
}
