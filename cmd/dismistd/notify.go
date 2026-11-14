package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/model"
)

// The webhook notifier is for installs without Kubernetes alerting (design
// §8.7): every interval it posts the site events of at least the chosen
// severity that were stored or updated since the last post. It sends event
// summaries only: no client MACs, configs or secrets.

type notifyConfig struct {
	url         string
	minSeverity string
	interval    time.Duration
}

func registerNotifyFlags(flags *flag.FlagSet) *notifyConfig {
	c := &notifyConfig{}
	flags.StringVar(&c.url, "notify-webhook", "", "URL that receives a JSON POST of new warning and error events (for installs without Prometheus alerting; empty: off)")
	flags.StringVar(&c.minSeverity, "notify-min-severity", "warning", "lowest event severity the webhook receives: info, warning or error")
	flags.DurationVar(&c.interval, "notify-interval", 30*time.Second, "how often the webhook notifier posts new events")
	return c
}

func (c *notifyConfig) validate() error {
	if c.url == "" {
		return nil
	}
	parsed, err := url.Parse(c.url)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("-notify-webhook must be an http(s) URL")
	}
	if severityRank(c.minSeverity) < 0 {
		return fmt.Errorf("-notify-min-severity must be info, warning or error")
	}
	if c.interval < 5*time.Second {
		return fmt.Errorf("-notify-interval must be at least 5s")
	}
	return nil
}

func severityRank(severity string) int {
	switch severity {
	case "info", "":
		return 0
	case "warning":
		return 1
	case "error":
		return 2
	}
	return -1
}

// notification is the webhook body.
type notification struct {
	Source string              `json:"source"`
	SentAt time.Time           `json:"sent_at"`
	Events []notificationEvent `json:"events"`
}

type notificationEvent struct {
	SiteID   string `json:"site_id"`
	SiteName string `json:"site_name"`
	APName   string `json:"ap_name"`
	api.EventSummary
}

type notifier struct {
	config notifyConfig
	store  *model.BoltStore
	client *http.Client
	since  time.Time
	seen   map[string]string
}

func newNotifier(config notifyConfig, store *model.BoltStore) *notifier {
	return &notifier{config: config, store: store, client: &http.Client{Timeout: 10 * time.Second},
		since: time.Now().UTC(), seen: map[string]string{}}
}

// collect returns the events to send since the last successful post.
func (n *notifier) collect() []notificationEvent {
	state := n.store.Snapshot()
	names := map[string]string{}
	for _, ap := range state.APs {
		names[ap.ID] = ap.Name
	}
	var result []notificationEvent
	for _, site := range state.Sites {
		events, _, err := n.store.Events2().List(model.EventQuery{SiteID: site.ID, Limit: 100})
		if err != nil {
			log.Printf("notifier: read events of site %s: %v", site.ID, err)
			continue
		}
		for _, event := range events {
			if event.LastSeen.Before(n.since) || severityRank(event.Severity) < severityRank(n.config.minSeverity) {
				continue
			}
			fingerprint := fmt.Sprintf("%d/%s", event.Count, event.LastSeen.Format(time.RFC3339Nano))
			if n.seen[event.ID] == fingerprint {
				continue
			}
			summary := api.EventSummary{ID: event.ID, APID: event.APID, Kind: event.Kind, Severity: api.Severity(event.Severity),
				Title: event.Title, FirstSeen: event.FirstSeen, LastSeen: event.LastSeen, Count: event.Count, HasClient: event.ClientMAC != ""}
			if event.ResolvedBy != "" {
				resolved := event.ResolvedBy
				summary.ResolvedBy = &resolved
			}
			result = append(result, notificationEvent{SiteID: site.ID, SiteName: site.Name, APName: names[event.APID], EventSummary: summary})
			n.seen[event.ID] = fingerprint
		}
	}
	return result
}

// post sends one batch.
func (n *notifier) post(ctx context.Context, events []notificationEvent) error {
	body, err := json.Marshal(notification{Source: "dismist", SentAt: time.Now().UTC(), Events: events})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, n.config.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "dismist-notifier")
	response, err := n.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("webhook answered %s", response.Status)
	}
	return nil
}

// run posts new events every interval until ctx ends. A failed post is
// retried with the same events at the next tick (at most 500 kept).
func (n *notifier) run(ctx context.Context) {
	ticker := time.NewTicker(n.config.interval)
	defer ticker.Stop()
	var queue []notificationEvent
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queue = append(queue, n.collect()...)
			if len(queue) > 500 {
				queue = queue[len(queue)-500:]
			}
			if len(queue) == 0 {
				continue
			}
			if err := n.post(ctx, queue); err != nil {
				log.Printf("notifier: %v (will retry)", err)
				continue
			}
			queue = nil
			if len(n.seen) > 5000 {
				n.seen = map[string]string{}
				n.since = time.Now().UTC()
			}
		}
	}
}
