package httpapi

import (
	"context"
	"errors"
	"log"
	"sort"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// WLAN schedules (design §3.9, phase 4, experimental): every minute the
// evaluator works out, in the site timezone, whether each scheduled WLAN
// is inside one of its windows. A WLAN goes off air only when every AP of
// its site takes config pushes by notification (a reconnect per window
// edge is not worth it). When the result differs from the stored
// Schedule.OffAir the evaluator stores it through the normal mutation
// path, so change detection bumps the revision, the history records it
// ("Schedule: WLAN Guest off air") and the push lifecycle delivers it.
// The compiled config never reads the clock, so a restart serves the same
// bytes at the same revision.

// RunSchedules evaluates WLAN schedules every interval until ctx ends.
func (s *Server) RunSchedules(ctx context.Context, interval time.Duration) {
	s.evaluateSchedules()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.evaluateSchedules()
		}
	}
}

// scheduleChange is one WLAN whose stored schedule state must change.
type scheduleChange struct {
	siteID, networkID, ssid string
	offAir                  bool
	// effective is false when the schedule is switched off and only the
	// stale stored state is cleared (no AP change, no history entry).
	effective bool
}

// scheduleChanges lists the WLANs whose stored schedule state differs from
// an evaluation at now.
func (s *Server) scheduleChanges(state model.State, now time.Time) []scheduleChange {
	sites := map[string]model.Site{}
	for _, site := range state.Sites {
		sites[site.ID] = site
	}
	notify := map[string]bool{}
	var changes []scheduleChange
	for _, network := range state.Networks {
		schedule := network.Schedule
		if schedule == nil || (!schedule.Enabled && !schedule.OffAir) {
			continue
		}
		want := false
		if schedule.Enabled {
			site, ok := sites[network.SiteID]
			if !ok {
				continue
			}
			allowed, seen := notify[site.ID]
			if !seen {
				allowed = s.siteTakesNotify(state, site.ID)
				notify[site.ID] = allowed
			}
			on, err := compile.ScheduleOn(*schedule, site.Timezone, now)
			// An invalid schedule (unknown timezone, bad window) keeps
			// the WLAN on air.
			want = allowed && err == nil && !on
		}
		if want != schedule.OffAir {
			changes = append(changes, scheduleChange{siteID: network.SiteID, networkID: network.ID, ssid: network.SSID,
				offAir: want, effective: schedule.Enabled})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].siteID != changes[j].siteID {
			return changes[i].siteID < changes[j].siteID
		}
		return changes[i].networkID < changes[j].networkID
	})
	return changes
}

// siteTakesNotify reports whether every AP of a site takes config pushes
// by ep-config-change notification (its live session's learned mode, or
// its protocol features while it is offline). A site without APs does.
func (s *Server) siteTakesNotify(state model.State, siteID string) bool {
	for _, ap := range siteAPs(state, siteID) {
		if control := s.controlFor(ap.ID); control != nil {
			if control.PushMode() != controller.PushModeNotify {
				return false
			}
			continue
		}
		if ap.PushMode == controller.PushModeReconnect ||
			!controller.ResolveFeatures(s.defaultFeatures, ap.Features).Has(controller.FeatureConfigChangeNotify) {
			return false
		}
	}
	return true
}

// evaluateSchedules stores every changed schedule state (one mutation per
// WLAN, under the API's mutation lock).
func (s *Server) evaluateSchedules() {
	if !s.typedEnabled() {
		return
	}
	now := s.now()
	for _, change := range s.scheduleChanges(s.store.Snapshot(), now) {
		change := change
		state := "back on air"
		if change.offAir {
			state = "off air"
		}
		historySite := change.siteID
		if !change.effective {
			historySite = ""
		}
		s.mutationLock.Lock()
		_, err := s.run(mutation{
			sites: []string{change.siteID}, kind: "wlan.updated", objectID: change.networkID, historySite: historySite,
			summary: "Schedule: WLAN " + change.ssid + " " + state,
			objects: []api.ChangeObject{{Kind: "network", ID: change.networkID, Name: change.ssid}},
			change: func(state *model.State, _ *model.Tx) error {
				network := findNetwork(*state, change.siteID, change.networkID)
				if network == nil || network.Schedule == nil || network.Schedule.OffAir == change.offAir {
					return errNoChange
				}
				schedule := *network.Schedule
				schedule.OffAir = change.offAir
				network.Schedule = &schedule
				return nil
			},
		})
		s.mutationLock.Unlock()
		if err != nil && !errors.Is(err, errNoChange) {
			log.Printf("WLAN %s: store schedule state: %v", change.networkID, err)
		}
	}
}
