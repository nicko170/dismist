package model

import (
	"context"
	"sync"
	"time"
)

// Event writer defaults (design §5.8: flush every 2 s or 100 events).
const (
	DefaultEventFlushEvery = 2 * time.Second
	DefaultEventMaxBatch   = 100
	DefaultEventMaxQueue   = 10000
)

// EventWriterOptions configure an EventWriter.
type EventWriterOptions struct {
	// FlushEvery is the longest an event waits (default 2 s).
	FlushEvery time.Duration
	// MaxBatch queued events trigger a flush at once (default 100).
	MaxBatch int
	// MaxQueue bounds the queue while the database is slow or failing
	// (default 10000); when full the oldest queued event is dropped and
	// counted.
	MaxQueue int
	// OnWrite receives the result of every committed flush, for example
	// to publish SSE event summaries (WriteResult.Changed). It runs on the
	// flushing goroutine and must not block for long.
	OnWrite func(WriteResult)
	// OnError receives a failed flush and how many events it lost.
	OnError func(err error, lost int)
}

// EventWriterStats are the writer's counters since it was created.
type EventWriterStats struct {
	Queued   int    // events waiting now
	Written  uint64 // events in committed flushes (any outcome)
	Rejected uint64 // invalid events (EventRejected)
	Dropped  uint64 // dropped because the queue was full
	Lost     uint64 // lost in failed flushes
	Flushes  uint64 // committed flushes
}

// EventWriter batches event writes for the observer: callers Add events
// without touching the disk, and the writer stores them with
// EventStore.Write every FlushEvery or MaxBatch events, in one transaction
// per flush.
type EventWriter struct {
	store   *EventStore
	options EventWriterOptions
	wake    chan struct{}

	mu      sync.Mutex
	queue   []EventWrite
	stats   EventWriterStats
	flushMu sync.Mutex // one flush at a time, in queue order
}

// NewEventWriter returns a writer for store. Call Run to flush on a timer.
func NewEventWriter(store *EventStore, options EventWriterOptions) *EventWriter {
	if options.FlushEvery <= 0 {
		options.FlushEvery = DefaultEventFlushEvery
	}
	if options.MaxBatch <= 0 {
		options.MaxBatch = DefaultEventMaxBatch
	}
	if options.MaxQueue <= 0 {
		options.MaxQueue = DefaultEventMaxQueue
	}
	if options.MaxQueue < options.MaxBatch {
		options.MaxQueue = options.MaxBatch
	}
	return &EventWriter{store: store, options: options, wake: make(chan struct{}, 1)}
}

// Add queues events. It never blocks on the database.
func (w *EventWriter) Add(writes ...EventWrite) {
	if len(writes) == 0 {
		return
	}
	w.mu.Lock()
	w.queue = append(w.queue, writes...)
	if over := len(w.queue) - w.options.MaxQueue; over > 0 {
		w.queue = append([]EventWrite(nil), w.queue[over:]...)
		w.stats.Dropped += uint64(over)
	}
	full := len(w.queue) >= w.options.MaxBatch
	w.mu.Unlock()
	if full {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

// Flush stores everything queued now, in one transaction, and returns the
// result (empty when nothing was queued).
func (w *EventWriter) Flush() (WriteResult, error) {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()
	w.mu.Lock()
	batch := w.queue
	w.queue = nil
	w.mu.Unlock()
	if len(batch) == 0 {
		return WriteResult{}, nil
	}
	result, err := w.store.Write(batch)
	w.mu.Lock()
	if err != nil {
		w.stats.Lost += uint64(len(batch))
	} else {
		w.stats.Flushes++
		w.stats.Written += uint64(len(batch))
		for _, item := range result.Events {
			if item.Outcome == EventRejected {
				w.stats.Rejected++
			}
		}
	}
	w.mu.Unlock()
	if err != nil {
		if w.options.OnError != nil {
			w.options.OnError(err, len(batch))
		}
		return result, err
	}
	if w.options.OnWrite != nil {
		w.options.OnWrite(result)
	}
	return result, nil
}

// Run flushes every FlushEvery, and at once when MaxBatch events are
// queued, until ctx ends; then it flushes what is left and returns.
func (w *EventWriter) Run(ctx context.Context) {
	ticker := time.NewTicker(w.options.FlushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = w.Flush()
			return
		case <-ticker.C:
		case <-w.wake:
		}
		_, _ = w.Flush()
	}
}

// Stats returns the writer's counters.
func (w *EventWriter) Stats() EventWriterStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	stats := w.stats
	stats.Queued = len(w.queue)
	return stats
}
