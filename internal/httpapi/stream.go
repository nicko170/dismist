package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

type streamAP struct {
	ID              string `json:"id"`
	Connected       bool   `json:"connected"`
	AppliedRevision uint64 `json:"applied_revision"`
	FullFrames      uint64 `json:"full_frames"`
	FastFrames      uint64 `json:"fast_frames"`
	ClientCount     int    `json:"client_count"`
}

type streamUpdate struct {
	SiteID     string     `json:"site_id"`
	Revision   uint64     `json:"revision"`
	ObservedAt time.Time  `json:"observed_at"`
	APs        []streamAP `json:"aps"`
}

func (s *Server) streamSite(writer http.ResponseWriter, request *http.Request, siteID string) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, internalError("streaming unavailable"))
		return
	}
	update, previous, found, err := s.siteStreamSnapshot(siteID, time.Now().UTC())
	if err != nil {
		writeAPIError(writer, internalError("stream unavailable"))
		return
	}
	if !found {
		writeAPIError(writer, notFound("site"))
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set("Connection", "keep-alive")
	if err := writeStreamUpdate(writer, flusher, update); err != nil {
		return
	}
	interval, heartbeat, maxAge := s.streamInterval, s.streamHeartbeat, s.streamMaxAge
	if interval <= 0 {
		interval = time.Second
	}
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	if maxAge <= 0 {
		maxAge = 30 * time.Minute
	}
	checkTicker := time.NewTicker(interval)
	heartbeatTicker := time.NewTicker(heartbeat)
	maxTimer := time.NewTimer(maxAge)
	defer checkTicker.Stop()
	defer heartbeatTicker.Stop()
	defer maxTimer.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-maxTimer.C:
			return
		case <-heartbeatTicker.C:
			if _, err := io.WriteString(writer, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-checkTicker.C:
			update, current, found, err := s.siteStreamSnapshot(siteID, time.Now().UTC())
			if err != nil || !found {
				return
			}
			if current == previous {
				continue
			}
			if err := writeStreamUpdate(writer, flusher, update); err != nil {
				return
			}
			previous = current
		}
	}
}

func writeStreamUpdate(writer http.ResponseWriter, flusher http.Flusher, update streamUpdate) error {
	raw, err := json.Marshal(update)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "event: update\ndata: %s\n\n", raw); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func (s *Server) siteStreamSnapshot(siteID string, at time.Time) (streamUpdate, [32]byte, bool, error) {
	state := s.store.Snapshot()
	update := streamUpdate{SiteID: siteID, Revision: state.Revision, ObservedAt: at, APs: make([]streamAP, 0)}
	var site model.Site
	found := false
	for _, candidate := range state.Sites {
		if candidate.ID == siteID {
			site, found = candidate, true
			break
		}
	}
	if !found {
		return streamUpdate{}, [32]byte{}, false, nil
	}
	aps := make([]model.APRecord, 0)
	statuses := make([]controller.Status, 0)
	for _, ap := range state.APs {
		if ap.SiteID != siteID {
			continue
		}
		aps = append(aps, ap)
		var status controller.Status
		var telemetry controller.TelemetrySnapshot
		if control := s.controlFor(ap.ID); control != nil {
			status = control.Status()
			telemetry = control.TelemetrySnapshot()
		}
		statuses = append(statuses, status)
		update.APs = append(update.APs, streamAP{
			ID: ap.ID, Connected: status.Connected, AppliedRevision: status.AppliedRevision,
			FullFrames: telemetry.FullFrames, FastFrames: telemetry.FastFrames,
			ClientCount: len(liveTelemetry(ap, status, telemetry).AssociatedClients),
		})
	}
	networks := make([]model.Network, 0)
	for _, network := range state.Networks {
		if network.SiteID == siteID {
			networks = append(networks, network)
		}
	}
	fingerprint, err := streamFingerprint(site, aps, networks, statuses, update.APs, at.Unix()/60)
	if err != nil {
		return streamUpdate{}, [32]byte{}, false, err
	}
	return update, fingerprint, true, nil
}

func streamFingerprint(site model.Site, aps []model.APRecord, networks []model.Network, statuses []controller.Status, summaries []streamAP, minute int64) ([32]byte, error) {
	fingerprintInput := struct {
		Site      model.Site
		APs       []model.APRecord
		Networks  []model.Network
		Statuses  []controller.Status
		Summaries []streamAP
		Minute    int64
	}{site, aps, networks, statuses, summaries, minute}
	raw, err := json.Marshal(fingerprintInput)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
