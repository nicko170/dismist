package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// Protocol and telemetry wiring (workstream B): the sinks that carry the
// controller's typed events, verification results and protocol facts into
// the store. main.go starts them and hands the hooks to registryOptions.

// eventStore is the part of *model.EventStore the event sink writes to.
type eventStore interface {
	Upsert(events []model.EventV2) ([]model.EventV2, error)
	AppendController(event model.EventV2) (model.EventV2, error)
	Resolve(ids []string, by string) error
}

// Event sink limits (design §5.8: flush every 2 s or 100 events).
const (
	eventFlushInterval = 2 * time.Second
	eventFlushSize     = 100
	eventQueueSize     = 4096
)

// eventSink batches the controller's typed events into events2. Events
// with an ID (PACE events, and controller events a later one resolves)
// are upserted by that ID; the others are controller events that the
// store gives a UUIDv7 and folds within 60 s. Corrects resolves earlier
// events. A full queue drops events (counted) rather than block an AP's
// session.
type eventSink struct {
	store   eventStore
	siteOf  func(apID string) string
	queue   chan controller.Event
	dropped sync.Mutex
	drops   uint64
	onWrite func([]model.EventV2)
}

// newEventSink returns a sink; siteOf maps an AP to its site at write
// time. onWrite, when set, receives the stored events (the SSE "event"
// summary).
func newEventSink(store eventStore, siteOf func(apID string) string, onWrite func([]model.EventV2)) *eventSink {
	return &eventSink{store: store, siteOf: siteOf, queue: make(chan controller.Event, eventQueueSize), onWrite: onWrite}
}

// Event queues one event (registryOptions.Events, controller
// Options.OnEvent).
func (s *eventSink) Event(event controller.Event) {
	select {
	case s.queue <- event:
	default:
		s.dropped.Lock()
		s.drops++
		drops := s.drops
		s.dropped.Unlock()
		if drops == 1 || drops%1000 == 0 {
			log.Printf("event sink queue full: %d events dropped", drops)
		}
	}
}

// Dropped returns how many events the full queue dropped.
func (s *eventSink) Dropped() uint64 {
	s.dropped.Lock()
	defer s.dropped.Unlock()
	return s.drops
}

// Run writes queued events until ctx ends, then flushes what is queued.
func (s *eventSink) Run(ctx context.Context) {
	ticker := time.NewTicker(eventFlushInterval)
	defer ticker.Stop()
	batch := make([]controller.Event, 0, eventFlushSize)
	flush := func() {
		if len(batch) != 0 {
			s.write(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case event := <-s.queue:
					batch = append(batch, event)
				default:
					flush()
					return
				}
			}
		case event := <-s.queue:
			batch = append(batch, event)
			if len(batch) >= eventFlushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// write stores one batch: PACE upserts in one transaction, then
// controller events, then the events they resolve (Corrects).
func (s *eventSink) write(batch []controller.Event) {
	var upserts, stored []model.EventV2
	type pending struct {
		record   model.EventV2
		corrects []string
	}
	var controllerEvents []pending
	resolutions := map[string][]string{} // resolving event ID -> resolved IDs
	for _, event := range batch {
		record := toEventRecord(event, s.siteOf(event.APID))
		if record.SiteID == "" {
			continue // an AP without a site has no event history
		}
		if record.ID == "" {
			controllerEvents = append(controllerEvents, pending{record: record, corrects: event.Corrects})
			continue
		}
		upserts = append(upserts, record)
		if len(event.Corrects) != 0 {
			resolutions[record.ID] = append(resolutions[record.ID], event.Corrects...)
		}
	}
	if len(upserts) != 0 {
		written, err := s.store.Upsert(upserts)
		if err != nil {
			log.Printf("cannot store %d AP events: %v", len(upserts), err)
		}
		stored = append(stored, written...)
	}
	for _, item := range controllerEvents {
		written, err := s.store.AppendController(item.record)
		if err != nil {
			log.Printf("cannot store an AP event: %v", err)
			continue
		}
		stored = append(stored, written)
		if len(item.corrects) != 0 {
			resolutions[written.ID] = append(resolutions[written.ID], item.corrects...)
		}
	}
	for by, ids := range resolutions {
		if err := s.store.Resolve(ids, by); err != nil {
			log.Printf("cannot resolve AP events: %v", err)
		}
	}
	if s.onWrite != nil && len(stored) != 0 {
		s.onWrite(stored)
	}
}

// toEventRecord maps a controller event to the stored record.
func toEventRecord(event controller.Event, siteID string) model.EventV2 {
	at := event.At.UTC()
	return model.EventV2{
		ID: event.ID, SiteID: siteID, APID: event.APID, Kind: event.Kind, PaceType: event.PaceType,
		Severity: event.Severity, Title: event.Title, Detail: event.Detail,
		ClientMAC: strings.ToLower(event.ClientMAC), WLANID: event.WLANID,
		FirstSeen: at, LastSeen: at, Count: 1, Data: event.Data,
	}
}

// siteOfAP returns a lookup of an AP's site from the live state.
func siteOfAP(store model.StateStore) func(apID string) string {
	return func(apID string) string {
		for _, ap := range store.Snapshot().APs {
			if ap.ID == apID {
				return ap.SiteID
			}
		}
		return ""
	}
}

// verificationRecorder stores post-delivery verification results in
// ap_config.delivered.verified (design §5.9).
func verificationRecorder(store interface {
	SetVerified(apID string, verification model.Verification) error
}) func(apID string, result controller.Verification) {
	return func(apID string, result controller.Verification) {
		mismatches := append([]string(nil), result.Mismatches...)
		if err := store.SetVerified(apID, model.Verification{At: result.At.UTC(), OK: result.OK, Mismatches: mismatches}); err != nil {
			log.Printf("AP %s: cannot store the verification result: %v", apID, err)
		}
	}
}

// configPushFeatures applies -config-push to the -ap-features defaults:
// "notify" switches config_change_notify on for every AP that does not
// override it; "reconnect" (the default) leaves the defaults as they are.
func configPushFeatures(defaults controller.Features, mode string) (controller.Features, error) {
	parsed, err := controller.ParsePushMode(mode)
	if err != nil {
		return defaults, err
	}
	if parsed == controller.PushModeNotify {
		return defaults.With(controller.FeatureConfigChangeNotify), nil
	}
	return defaults, nil
}
