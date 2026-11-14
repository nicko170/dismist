package fakeap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RunOptions control an AP's steady-state behaviour.
type RunOptions struct {
	// Snapshot is the state reported; its Uptime grows while running.
	Snapshot Snapshot
	// FastInterval and FullInterval pace the stats frames (default 2 s and
	// 60 s, as the AP). Negative disables that kind.
	FastInterval time.Duration
	FullInterval time.Duration
	// PingInterval paces pings (default 30 s); negative disables them.
	PingInterval time.Duration
	// EventInterval, when positive, sends a synthetic client PACE event
	// that often.
	EventInterval time.Duration
	// RejectConfig makes the AP report every config as rejected with this
	// error instead of applied.
	RejectConfig string
	// OnConfig is called with every config the AP fetches.
	OnConfig func([]byte)
}

func (o RunOptions) withDefaults() RunOptions {
	if o.FastInterval == 0 {
		o.FastInterval = 2 * time.Second
	}
	if o.FullInterval == 0 {
		o.FullInterval = 60 * time.Second
	}
	if o.PingInterval == 0 {
		o.PingInterval = 30 * time.Second
	}
	return o
}

// Run behaves like a connected AP until ctx ends or the connection
// closes: it fetches its config and reports it applied, sends a full
// stats frame, then fast and full frames, pings and optional PACE events
// on their intervals, and refetches config when the controller sends
// ep-config-change. It returns ErrClosed when the controller ends the
// session and ctx.Err() when ctx ends.
func (a *AP) Run(ctx context.Context, options RunOptions) error {
	options = options.withDefaults()
	started := time.Now()
	snapshot := options.Snapshot
	baseUptime := snapshot.Uptime
	applied := 0.0
	fetch := func() error {
		config, err := a.FetchConfig(ctx, ConfigRequest{AppliedConfigTimestamp: applied})
		if err != nil {
			return err
		}
		if options.OnConfig != nil {
			options.OnConfig(config)
		}
		var metadata struct {
			Timestamp float64 `json:"Timestamp"`
		}
		_ = json.Unmarshal(config, &metadata)
		if err := a.ReportApplied(metadata.Timestamp, options.RejectConfig); err != nil {
			return err
		}
		if options.RejectConfig == "" {
			applied = metadata.Timestamp
			snapshot.ConfigTimestamp = metadata.Timestamp
		}
		return nil
	}
	send := func(full bool) error {
		snapshot.Uptime = baseUptime + uint64(time.Since(started)/time.Second)
		return a.SendStats(snapshot, full)
	}
	// An unanswered ep-config is retried with the next full frame, as the
	// AP keeps asking when the controller has no config for it.
	needConfig := false
	tryFetch := func() error {
		err := fetch()
		if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			needConfig = true
			return nil
		}
		needConfig = false
		return err
	}
	if err := tryFetch(); err != nil {
		return a.endError(ctx, err)
	}
	if err := send(true); err != nil {
		return a.endError(ctx, err)
	}
	fast := ticker(options.FastInterval)
	full := ticker(options.FullInterval)
	pings := ticker(options.PingInterval)
	events := ticker(options.EventInterval)
	defer stop(fast, full, pings, events)
	eventNumber := 0
	for {
		var err error
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.done:
			return ErrClosed
		case <-tick(fast):
			err = send(false)
		case <-tick(full):
			err = send(true)
			if err == nil && needConfig {
				err = tryFetch()
			}
		case <-tick(pings):
			_, err = a.Ping(ctx, []byte("fakeap"))
		case <-tick(events):
			eventNumber++
			if len(snapshot.Clients) != 0 {
				client := snapshot.Clients[eventNumber%len(snapshot.Clients)]
				err = a.SendPaceEvent(PaceEvent{
					Type: 49, Title: "Client authorized", Text: fmt.Sprintf("Synthetic event %d", eventNumber),
					ClientMAC: dashedMAC(client.MAC.String()), ClientIP: client.IP.String(),
				})
			} else {
				err = a.SendPaceEvent(PaceEvent{Type: 3, Title: "DNS query succeeded", Text: fmt.Sprintf("Synthetic event %d", eventNumber)})
			}
		case message := <-a.inbox:
			if message.Subject == "ep-config-change" {
				err = tryFetch()
			}
		}
		if err != nil {
			return a.endError(ctx, err)
		}
	}
}

func (a *AP) endError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case <-a.done:
		return ErrClosed
	default:
	}
	if errors.Is(err, ErrClosed) {
		return ErrClosed
	}
	return err
}

func ticker(interval time.Duration) *time.Ticker {
	if interval <= 0 {
		return nil
	}
	return time.NewTicker(interval)
}

func tick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

func stop(tickers ...*time.Ticker) {
	for _, t := range tickers {
		if t != nil {
			t.Stop()
		}
	}
}

func dashedMAC(mac string) string {
	out := []byte(mac)
	for index, b := range out {
		if b == ':' {
			out[index] = '-'
		}
	}
	return string(out)
}
