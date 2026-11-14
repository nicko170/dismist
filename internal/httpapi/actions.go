package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/model"
)

// FeatureAPCmd is the per-AP protocol feature that lets dismist send
// ap-cmd-rx commands (reboot, radio re-init, client disconnect). It is
// experimental: the body was recovered from the 0.15 ep-control binary but
// never captured on the wire (design §5.6, §9 Q2).
const FeatureAPCmd = "ap_cmd"

// commandSupport says which actions the AP detail page offers.
func (s *Server) commandSupport(state model.State, ap model.APRecord, obs apObs, features controller.Features) api.CommandSupport {
	commands := s.commands != nil && apCmdOn(ap, obs) && obs.status.Connected
	support := api.CommandSupport{Reboot: commands, RestartRadios: commands, Disconnect: commands}
	if site := findSite(state, ap.SiteID); site != nil {
		support.Locate = s.typedEnabled() && siteExperimental(*site)
	}
	host := firstNonEmpty(lanIP(obs, lastKnown{}), "<ap-lan-ip>")
	support.Fallback = "dismist ap33 ssh root@" + host + " reboot"
	return support
}

// apCmdOn reports whether the AP's ap_cmd feature is on (on the control
// session once the controller knows the feature, else in the record).
func apCmdOn(ap model.APRecord, obs apObs) bool {
	return obs.features[FeatureAPCmd] || ap.Features[FeatureAPCmd]
}

// sendCommand sends an ap-cmd-rx command when the AP has a verified (or
// explicitly switched on, experimental) command channel.
func (s *Server) sendCommand(request *http.Request, ap model.APRecord, command APCommand) (string, error) {
	if s.commands == nil {
		return "", ErrCommandUnsupported
	}
	if !apCmdOn(ap, s.observe(ap.ID)) {
		return "", ErrCommandUnsupported
	}
	command.Sender = "dismist"
	if command.Params == nil {
		command.Params = json.RawMessage("{}")
	}
	if command.Clients == nil {
		command.Clients = []string{}
	}
	id, err := s.commands.SendCommand(ap.ID, command)
	if err == nil {
		log.Printf("audit: ap-cmd-rx %s sent to AP %s by %s", command.Cmd, ap.ID, principal(request))
	}
	return id, err
}

// writeCommandError answers an AP command that cannot be sent: 501
// unverified_command with the LAN-side fallback (design §8.6 tests).
func writeCommandError(writer http.ResponseWriter, ap model.APRecord, err error) {
	if errors.Is(err, ErrCommandUnsupported) {
		e := newError(http.StatusNotImplemented, api.ErrorCodeUnverifiedCommand,
			"dismist has no verified command channel to this AP; from the LAN run: dismist ap33 ssh root@<ap> reboot (or switch on the experimental ap_cmd protocol feature for this AP)")
		writeAPIError(writer, e.withField("protocol_features.ap_cmd", "off"))
		return
	}
	log.Printf("AP %s: command failed: %v", ap.ID, err)
	writeAPIError(writer, newError(http.StatusBadGateway, api.ErrorCodeInternal, "the AP command could not be sent"))
}

// recordAction stores an action entry in config history (design §4.11).
func (s *Server) recordAction(request *http.Request, siteID, summary string) {
	if s.config.History == nil {
		return
	}
	entry := ChangeEntry{At: s.now(), Kind: "action", Actor: actorOf(request), Endpoint: endpointOf(request), Summary: summary, SiteID: siteID}
	if err := s.config.History.AppendAction(entry); err != nil {
		log.Printf("config history: record action %q: %v", summary, err)
	}
}

var locateMinutes = map[int]bool{1: true, 5: true, 15: true, 60: true}

// apAction serves POST /api/aps/$id/actions/$action (design §4.9, §5.6):
// push-config (re-send through the push lifecycle), locate and unlocate
// (LED.Locator through the config, experimental), reboot and
// restart-radios (ap-cmd-rx, experimental, 501 without a command channel).
func (s *Server) apAction(writer http.ResponseWriter, request *http.Request, p params) {
	state := s.store.Snapshot()
	ap := findAP(state, p["ap"])
	if ap == nil {
		writeAPIError(writer, notFound("AP"))
		return
	}
	var input api.ActionInput
	if request.ContentLength > 0 {
		if err := decodeJSON(request, 4096, &input, "action"); err != nil {
			writeError(writer, err)
			return
		}
	}
	name := firstNonEmpty(ap.Name, ap.ID)
	switch api.APAction(p["action"]) {
	case api.APActionPushConfig:
		control := s.controlFor(ap.ID)
		if control == nil {
			writeAPIError(writer, conflict("the AP has no control session; it gets its config when it connects"))
			return
		}
		via := "reconnect"
		_, revision := control.ConfigSnapshot()
		if s.pusher != nil {
			via = s.pusher.Push(ap.ID, revision)
		} else {
			control.DisconnectBecause("config re-sent by operator")
		}
		s.pushes.track(ap.ID, revision, s.now())
		s.recordAction(request, ap.SiteID, "Re-sent config to AP "+name)
		respond(writer, http.StatusOK, api.ActionResult{Action: string(api.APActionPushConfig), State: "accepted", Via: via, Revision: revision})
	case api.APActionLocate, api.APActionUnlocate:
		s.locate(writer, request, *ap, api.APAction(p["action"]) == api.APActionLocate, input.Minutes)
	case api.APActionReboot:
		id, err := s.sendCommand(request, *ap, APCommand{Cmd: "reboot", Reason: "dismist operator"})
		if err != nil {
			writeCommandError(writer, *ap, err)
			return
		}
		s.recordAction(request, ap.SiteID, "Rebooted AP "+name)
		respond(writer, http.StatusOK, api.ActionResult{Action: string(api.APActionReboot), State: "accepted", ActionID: id, Via: "ap-cmd-rx"})
	case api.APActionRestartRadios:
		if input.Band != "" && input.Band != "2.4" && input.Band != "5" {
			writeAPIError(writer, invalidField("band", `band must be "2.4", "5" or empty for both`))
			return
		}
		params, _ := json.Marshal(map[string]any{"band": input.Band})
		id, err := s.sendCommand(request, *ap, APCommand{Cmd: "radio_reinit", Params: params, Reason: "dismist operator"})
		if err != nil {
			writeCommandError(writer, *ap, err)
			return
		}
		s.recordAction(request, ap.SiteID, fmt.Sprintf("Restarted radios (%s) on AP %s", firstNonEmpty(input.Band, "both"), name))
		respond(writer, http.StatusOK, api.ActionResult{Action: string(api.APActionRestartRadios), State: "accepted", ActionID: id, Via: "ap-cmd-rx"})
	default:
		writeAPIError(writer, newError(http.StatusNotFound, api.ErrorCodeNotFound, "unknown action; use locate, unlocate, push-config, reboot or restart-radios"))
	}
}

// locate sets or clears the AP's locate_until, which makes the compiler
// emit LED.Locator (design §5.6). It is experimental (likely, never seen on
// an AP33), so the site must be experimental.
func (s *Server) locate(writer http.ResponseWriter, request *http.Request, ap model.APRecord, on bool, minutes int) {
	if !s.typedEnabled() {
		writeAPIError(writer, migrationPending([]string{"locate_until"}))
		return
	}
	site := findSite(s.store.Snapshot(), ap.SiteID)
	if site == nil {
		writeAPIError(writer, conflict("the AP is not in a site"))
		return
	}
	if on && !siteExperimental(*site) {
		writeAPIError(writer, experimentalDisabled([]string{"ap.locate"}))
		return
	}
	if minutes == 0 {
		minutes = 5
	}
	if on && !locateMinutes[minutes] {
		writeAPIError(writer, invalidField("minutes", "locate for 1, 5, 15 or 60 minutes"))
		return
	}
	var until any
	var untilTime *time.Time
	if on {
		at := s.now().Add(time.Duration(minutes) * time.Minute).UTC()
		until, untilTime = at.Format(time.RFC3339Nano), &at
	}
	action := api.APActionUnlocate
	summary := "Stopped locating AP " + firstNonEmpty(ap.Name, ap.ID)
	if on {
		action = api.APActionLocate
		summary = fmt.Sprintf("Locating AP %s for %d min", firstNonEmpty(ap.Name, ap.ID), minutes)
	}
	out, err := s.run(mutation{
		sites: []string{ap.SiteID}, dryRun: dryRun(request), request: request, kind: "ap.updated", objectID: ap.ID,
		historySite: ap.SiteID, summary: summary, objects: []api.ChangeObject{{Kind: "ap", ID: ap.ID, Name: ap.Name}},
		change: func(state *model.State, _ *model.Tx) error {
			current := findAP(*state, ap.ID)
			if current == nil {
				return notFound("AP")
			}
			if err := patchRecord(current, map[string]any{"locate_until": until}); err != nil {
				var unsupported *errUnsupported
				if errors.As(err, &unsupported) {
					return unsupportedField("locate_until")
				}
				return err
			}
			return nil
		},
	})
	if err != nil {
		writeChangeError(writer, err)
		return
	}
	respondMutation(writer, http.StatusOK, out, api.ActionResult{Action: string(action), State: "done", Until: untilTime, Via: "config", Revision: out.revision})
}

// RunLocateExpiry clears expired locate_until values every interval, so a
// forgotten locate stops on its own (design §5.6: "a controller timer
// re-pushes false at expiry").
func (s *Server) RunLocateExpiry(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.expireLocates()
		}
	}
}

func (s *Server) expireLocates() {
	if !s.typedEnabled() {
		return
	}
	now := s.now()
	state := s.store.Snapshot()
	for _, ap := range state.APs {
		_, _, until := apOverrides(ap)
		if until == nil || until.After(now) {
			continue
		}
		id := ap.ID
		s.mutationLock.Lock()
		_, err := s.run(mutation{
			sites: []string{ap.SiteID}, kind: "ap.updated", objectID: id, historySite: ap.SiteID,
			summary: "Locate of AP " + firstNonEmpty(ap.Name, id) + " expired",
			change: func(state *model.State, _ *model.Tx) error {
				current := findAP(*state, id)
				if current == nil {
					return errNoChange
				}
				return patchRecord(current, map[string]any{"locate_until": nil})
			},
		})
		s.mutationLock.Unlock()
		if err != nil && !errors.Is(err, errNoChange) && !strings.Contains(err.Error(), "unsupported") {
			log.Printf("AP %s: clear expired locate: %v", id, err)
		}
	}
}
