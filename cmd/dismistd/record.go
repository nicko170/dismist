package main

import (
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// extendObservation adds the phase-1 observation fields (per-radio sample
// values, the AP model) once the model's v2 Observation is merged; see
// apstatus_sink.go.
var extendObservation func(*model.Observation, controller.Status, controller.TelemetrySnapshot)

func makeObservation(siteID, apID string, status controller.Status, stats controller.TelemetrySnapshot, at time.Time) model.Observation {
	observation := model.Observation{
		SiteID: siteID, APID: apID, At: at, Connected: status.Connected,
		Firmware: status.Firmware, AppliedRevision: status.AppliedRevision,
		UptimeSeconds: stats.UptimeSeconds,
		SkipClients:   !status.Connected || (!stats.LastFast.After(status.ConnectedAt) && !stats.LastFull.After(status.ConnectedAt)),
		Events:        make([]model.EventSnapshot, 0, len(stats.Events)),
	}
	if !observation.SkipClients {
		for _, client := range stats.AssociatedClients {
			observation.Clients = append(observation.Clients, model.ClientSnapshot{
				MAC: client.MAC, Name: client.Name, IP: client.IP,
				SSID: client.SSID, VLANID: client.VLANID, RSSI: client.RSSI,
				TxBytes: client.TxBytes, RxBytes: client.RxBytes,
				ConnectedSeconds: client.ConnectedSeconds,
			})
		}
	}
	for _, event := range stats.Events {
		observation.Events = append(observation.Events, model.EventSnapshot{
			When: event.When, Kind: event.Kind, Title: event.Title,
			Detail: event.Detail, ClientMAC: event.ClientMAC,
		})
	}
	if extendObservation != nil {
		extendObservation(&observation, status, stats)
	}
	return observation
}
