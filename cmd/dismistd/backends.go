package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/httpapi"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/options"
	"github.com/nicko170/dismist/internal/rfplan"
	"github.com/nicko170/dismist/internal/telemetry"
)

// The API (workstream C) declares what it needs from the other workstreams
// as interfaces (internal/httpapi/backends.go). This file implements them
// over workstream A's stores, compile pipeline and channel planner, and
// workstream B's controller sessions.

// apiBackends returns the API's typed-config layer over store.
func apiBackends(store *model.BoltStore, migrations httpapi.MigrationRunner) httpapi.Config {
	return httpapi.Config{
		History:         historyLog{store: store},
		Restorer:        restorer{},
		Recommendations: recommender{},
		Migration:       migrations,
		Expert:          expertPolicy{},
		Planner:         planner{store: store},
		ClientMeta:      clientMeta{store: store.ClientMeta()},
		LastKnown: func(apID string) (any, bool) {
			record, err := store.APStatus().Get(apID)
			if err != nil {
				return nil, false
			}
			return record, true
		},
	}
}

// historyLog is httpapi.ChangeLog over config_history (design §4.11).
type historyLog struct{ store *model.BoltStore }

func modelActor(actor api.ChangeActor) model.Actor {
	return model.Actor{Kind: string(actor.Kind), ID: actor.ID, Name: actor.Name, Remote: actor.Remote}
}

func apiActor(actor model.Actor) api.ChangeActor {
	return api.ChangeActor{Kind: api.ActorKind(actor.Kind), ID: actor.ID, Name: actor.Name, Remote: actor.Remote}
}

func (h historyLog) record(entry httpapi.ChangeEntry) model.ChangeRecord {
	kind := entry.Kind
	if kind == "" {
		kind = model.ChangeConfig
	}
	record := model.ChangeRecord{
		Revision: entry.Revision, At: entry.At.UTC(), Kind: kind, Actor: modelActor(entry.Actor),
		Endpoint: entry.Endpoint, Summary: entry.Summary, Scope: model.ChangeScope{SiteID: entry.SiteID},
	}
	for _, object := range entry.Objects {
		record.Scope.Objects = append(record.Scope.Objects, model.ObjectRef{Kind: object.Kind, ID: object.ID})
	}
	for apID, effective := range entry.Effective {
		if record.Effective == nil {
			record.Effective = map[string]model.EffectiveRef{}
		}
		record.Effective[apID] = model.EffectiveRef{Hash: effective.Hash, Revision: effective.Revision}
	}
	for apID, result := range entry.Results {
		if record.Results == nil {
			record.Results = map[string]model.ApplyResult{}
		}
		applied := model.ApplyResult{State: result.State, Error: result.Error}
		if result.At != nil {
			applied.At = result.At.UTC()
		}
		record.Results[apID] = applied
	}
	return record
}

// Append stores a committed mutation with the redacted snapshot of its
// site after the change (compile.SnapshotSite).
func (h historyLog) Append(tx *model.Tx, state model.State, entry httpapi.ChangeEntry) error {
	record := h.record(entry)
	if entry.SiteID != "" {
		snapshot := compile.SnapshotSite(state, entry.SiteID, tx.HashKey())
		record.Snapshot = &snapshot
	}
	_, err := tx.ConfigHistory().Append(record)
	return err
}

// AppendAction stores an action at the current state revision, so it
// sorts among the config changes made around it.
func (h historyLog) AppendAction(entry httpapi.ChangeEntry) error {
	record := h.record(entry)
	record.Kind = model.ChangeAction
	if record.Revision == 0 {
		record.Revision = h.store.Snapshot().Revision
	}
	_, err := h.store.ConfigHistory().Append(record)
	return err
}

func (h historyLog) List(siteID string, before uint64, limit int) ([]httpapi.ChangeEntry, uint64, error) {
	records, next, err := h.store.ConfigHistory().List(model.HistoryQuery{SiteID: siteID, Before: before, Limit: limit})
	if err != nil {
		return nil, 0, err
	}
	entries := make([]httpapi.ChangeEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, changeEntry(record))
	}
	return entries, next, nil
}

func (h historyLog) Get(revision uint64) (httpapi.ChangeEntry, error) {
	record, found, err := h.store.ConfigHistory().Get(revision)
	if err != nil {
		return httpapi.ChangeEntry{}, err
	}
	if !found {
		return httpapi.ChangeEntry{}, model.ErrNotFound
	}
	return changeEntry(record), nil
}

// SetResult records an AP's outcome for one of its config revisions (the
// controller reports the AP's ConfigRevision, not the state revision the
// history is keyed by): the entry whose effective revision for the AP
// matches gets the result, and an applied revision supersedes the AP's
// older entries still pending.
func (h historyLog) SetResult(configRevision uint64, apID string, result api.APState) error {
	outcome := model.ApplyResult{State: result.State, Error: result.Error, At: time.Now().UTC()}
	if result.At != nil {
		outcome.At = result.At.UTC()
	}
	records, _, err := h.store.ConfigHistory().List(model.HistoryQuery{APID: apID, Limit: 200})
	if err != nil {
		return err
	}
	for _, record := range records {
		ref, ok := record.Effective[apID]
		if !ok {
			continue
		}
		if current, ok := record.Results[apID]; ok && current.State != "" && current.State != "pending" {
			continue
		}
		switch {
		case ref.Revision == configRevision:
			err = h.store.ConfigHistory().SetResult(record.Revision, apID, outcome)
		case ref.Revision < configRevision && result.State == "applied":
			err = h.store.ConfigHistory().SetResult(record.Revision, apID, model.ApplyResult{State: "superseded", At: outcome.At})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// changeEntry is the API's form of a history record. Object names come
// from the record's own snapshot (the names at that revision).
func changeEntry(record model.ChangeRecord) httpapi.ChangeEntry {
	entry := httpapi.ChangeEntry{
		Revision: record.Revision, At: record.At, Kind: record.Kind, Actor: apiActor(record.Actor),
		Endpoint: record.Endpoint, Summary: record.Summary, SiteID: record.Scope.SiteID,
		Effective: map[string]httpapi.EffectiveEntry{}, Results: map[string]api.APState{},
	}
	if entry.Kind == model.ChangeMigration {
		entry.Kind = model.ChangeAction
	}
	names := map[string]string{}
	if record.Snapshot != nil {
		for _, site := range record.Snapshot.Sites {
			names["site/"+site.ID] = site.Name
		}
		for _, network := range record.Snapshot.Networks {
			names["network/"+network.ID] = network.SSID
		}
		for _, ap := range record.Snapshot.APs {
			names["ap/"+ap.ID] = ap.Name
		}
		if raw, err := json.Marshal(record.Snapshot); err == nil {
			entry.Snapshot = raw
		}
	}
	for _, object := range record.Scope.Objects {
		entry.Objects = append(entry.Objects, api.ChangeObject{Kind: object.Kind, ID: object.ID, Name: names[object.Kind+"/"+object.ID]})
	}
	for apID, effective := range record.Effective {
		entry.Effective[apID] = httpapi.EffectiveEntry{Hash: effective.Hash, Revision: effective.Revision}
	}
	for apID, result := range record.Results {
		state := api.APState{State: result.State, Error: result.Error}
		if !result.At.IsZero() {
			at := result.At
			state.At = &at
		}
		entry.Results[apID] = state
	}
	return entry
}

// restorer is httpapi.Restorer over compile.Restore (design §4.11).
type restorer struct{}

func (restorer) Restore(state *model.State, tx *model.Tx, siteID string, entry httpapi.ChangeEntry, input api.RestoreInput) (api.RestorePlan, error) {
	var plan api.RestorePlan
	if tx == nil {
		return plan, errors.New("restore needs the bbolt database")
	}
	var snapshot model.ConfigSnapshot
	if err := json.Unmarshal(entry.Snapshot, &snapshot); err != nil {
		return plan, fmt.Errorf("revision %d snapshot: %w", entry.Revision, err)
	}
	record := model.ChangeRecord{Revision: entry.Revision, Kind: entry.Kind, Scope: model.ChangeScope{SiteID: siteID}, Snapshot: &snapshot}
	keep := map[string]bool{}
	for _, path := range input.KeepCurrent {
		keep[path] = true
	}
	result, err := compile.Restore(state, record, tx.HashKey(), compile.RestoreOptions{
		KeepNewWLANs: input.KeepNewerWLANs, Secrets: input.Secrets, KeepCurrent: keep, Now: state.UpdatedAt,
	})
	var secrets *compile.SecretRequiredError
	if errors.As(err, &secrets) {
		fields := map[string]string{}
		for _, field := range secrets.Fields {
			fields[field.Path] = field.Message
		}
		return plan, &httpapi.SecretsRequiredError{Fields: fields}
	}
	if err != nil {
		return plan, err
	}
	networkNames := map[string]string{}
	for _, network := range snapshot.Networks {
		networkNames[network.ID] = network.SSID
	}
	for _, network := range state.Networks {
		if _, ok := networkNames[network.ID]; !ok {
			networkNames[network.ID] = network.SSID
		}
	}
	apNames := map[string]string{}
	for _, ap := range snapshot.APs {
		apNames[ap.ID] = ap.Name
	}
	recreated, deleted := map[string]bool{}, map[string]bool{}
	for _, id := range result.Recreated {
		recreated[id] = true
		plan.Recreated = append(plan.Recreated, api.ChangeObject{Kind: "network", ID: id, Name: networkNames[id]})
	}
	for _, id := range result.Deleted {
		deleted[id] = true
		plan.Removed = append(plan.Removed, api.ChangeObject{Kind: "network", ID: id, Name: networkNames[id]})
	}
	notRestored := map[string]bool{}
	for _, id := range result.NotRestored {
		notRestored[id] = true
		plan.NotRestored = append(plan.NotRestored, api.ChangeObject{Kind: "ap", ID: id, Name: apNames[id]})
	}
	for _, site := range snapshot.Sites {
		plan.Changed = append(plan.Changed, api.ChangeObject{Kind: "site", ID: site.ID, Name: site.Name})
	}
	for _, network := range snapshot.Networks {
		if !recreated[network.ID] && !deleted[network.ID] {
			plan.Changed = append(plan.Changed, api.ChangeObject{Kind: "network", ID: network.ID, Name: network.SSID})
		}
	}
	for _, ap := range snapshot.APs {
		if !notRestored[ap.ID] {
			plan.Changed = append(plan.Changed, api.ChangeObject{Kind: "ap", ID: ap.ID, Name: ap.Name})
		}
	}
	for path := range input.Secrets {
		plan.SecretFields = append(plan.SecretFields, path)
	}
	sort.Strings(plan.SecretFields)
	return plan, nil
}

// recommender is httpapi.Recommender over compile's recommendations
// (design §4.6).
type recommender struct{}

func (recommender) List(state model.State, siteID string) []api.Recommendation {
	result := []api.Recommendation{}
	for _, item := range compile.Recommendations(state, siteID) {
		result = append(result, api.Recommendation{ID: item.ID, Title: item.Title, Detail: item.Detail, Scope: item.Scope,
			ObjectID: item.ObjectID, Disruption: api.Disruption(item.Disruption)})
	}
	return result
}

func (recommender) Apply(state *model.State, siteID, id string) (string, error) {
	title := id
	for _, item := range compile.Recommendations(*state, siteID) {
		if item.ID == id {
			title = item.Title
		}
	}
	if err := compile.ApplyRecommendation(state, siteID, id, state.UpdatedAt); err != nil {
		return "", err
	}
	return "Applied recommendation: " + title, nil
}

// expertPolicy is httpapi.ExpertPolicy over compile.FoldPatch, the folding
// the compiler itself applies (design §4.4). In legacy mode the patch is
// checked the same way but stored as written: the frozen v0 compiler
// reads it.
type expertPolicy struct{}

func (expertPolicy) Check(scope api.ExpertScope, patch []byte, legacy bool) ([]byte, []string, error) {
	result, err := compile.FoldPatch(string(scope), patch)
	var problems compile.PatchErrors
	if errors.As(err, &problems) && len(problems) > 0 {
		problem := problems[0]
		code := api.ErrorCode(problem.Code)
		switch code {
		case api.ErrorCodeForbiddenPath, api.ErrorCodeDuplicateKey:
		default:
			code = api.ErrorCodeInvalid
		}
		return nil, nil, &httpapi.ExpertError{Code: code, Pointer: problem.Pointer, Message: problem.Message}
	}
	if err != nil {
		return nil, nil, &httpapi.ExpertError{Code: api.ErrorCodeInvalid, Message: err.Error()}
	}
	if legacy {
		return patch, nil, nil
	}
	return result.Patch, result.Ignored, nil
}

// planner is httpapi.Planner over internal/rfplan (design §3.10).
type planner struct{ store *model.BoltStore }

func (planner) Initial(state *model.State, siteIDs []string, now time.Time) error {
	rfplan.ApplyInitial(state, now, siteIDs...)
	return nil
}

func (p planner) Propose(input httpapi.PlanInput) (api.PlanProposal, error) {
	scans, err := p.store.Scans().Site(input.SiteID)
	if err != nil {
		return api.PlanProposal{}, err
	}
	status, err := p.store.APStatus().All()
	if err != nil {
		return api.PlanProposal{}, err
	}
	utilisation := map[string]map[string]float64{}
	current := map[string]map[string]int{}
	widths := map[string]map[string]int{}
	for apID, radios := range input.Radios {
		for _, radio := range radios {
			if radio.Band == nil || radio.Role == "scan" {
				continue
			}
			band := string(*radio.Band)
			if utilisation[apID] == nil {
				utilisation[apID], current[apID], widths[apID] = map[string]float64{}, map[string]int{}, map[string]int{}
			}
			utilisation[apID][band] = radio.Util
			current[apID][band] = radio.Channel
			widths[apID][band] = radio.Width
		}
	}
	in, found := rfplan.SiteInputs(input.State, input.SiteID, scans, status, utilisation, current, input.Now)
	if !found {
		return api.PlanProposal{}, model.ErrNotFound
	}
	for bssid, apID := range input.OwnBSSIDs {
		in.OwnBSSIDs[apID] = append(in.OwnBSSIDs[apID], strings.ToLower(bssid))
	}
	result := rfplan.Plan(in)
	names := map[string]string{}
	for _, ap := range in.APs {
		names[ap.ID] = ap.Name
	}
	proposal := api.PlanProposal{
		Inputs:  api.PlanInputs{APs: len(in.APs), ScansAgeSeconds: map[string]*float64{}},
		Changes: []api.PlanChange{},
	}
	for apID, age := range result.ScanAge {
		proposal.Inputs.ScansAgeSeconds[apID] = age
	}
	for _, change := range result.Changes {
		fromWidth := widths[change.APID][change.Band]
		if fromWidth == 0 {
			fromWidth = change.To.Width
		}
		planned := api.PlanChange{
			APID: change.APID, APName: names[change.APID], Band: api.Band(change.Band),
			From:       api.PlanChannel{Channel: change.FromChannel, Width: fromWidth, PowerDBm: change.FromPower},
			To:         api.PlanChannel{Channel: change.To.Channel, Width: change.To.Width, PowerDBm: change.To.PowerDBm},
			CostBefore: change.CostBefore, CostAfter: change.CostAfter, Keep: change.Keep || change.Static,
			Reason: change.Reason, Disruption: api.Disruption(change.Disruption),
		}
		if change.To.DFSFallback != 0 {
			fallback := change.To.DFSFallback
			planned.To.DFSFallback = &fallback
		}
		if change.Static && planned.Reason == "" {
			planned.Reason = "static channel"
		}
		if planned.Disruption == "" {
			planned.Disruption = api.DisruptionNone
		}
		proposal.Changes = append(proposal.Changes, planned)
	}
	return proposal, nil
}

func (planner) Apply(state *model.State, siteID string, changes []api.PlanChange, now time.Time) error {
	for _, change := range changes {
		for index := range state.APs {
			ap := &state.APs[index]
			if ap.ID != change.APID || ap.SiteID != siteID {
				continue
			}
			if rfplan.Static(*ap, string(change.Band)) != 0 {
				continue // a static channel is the operator's, never the planner's
			}
			if ap.RFPlan == nil {
				ap.RFPlan = map[string]model.PlannedRadio{}
			}
			plan := model.PlannedRadio{Channel: change.To.Channel, Width: change.To.Width, PowerDBm: change.To.PowerDBm,
				Source: model.PlanSourcePlanner, Reason: change.Reason, PlannedAt: now.UTC()}
			if change.To.DFSFallback != nil {
				plan.DFSFallback = *change.To.DFSFallback
			}
			ap.RFPlan[string(change.Band)] = plan
		}
	}
	return nil
}

// clientMeta is httpapi.ClientMetaStore over client_meta (phase 4).
type clientMeta struct{ store *model.ClientMetaStore }

func (c clientMeta) Get(mac string) (api.ClientMeta, bool, error) {
	meta, found, err := c.store.Get(mac)
	return api.ClientMeta{MAC: mac, Alias: meta.Alias, Notes: meta.Notes}, found, err
}

func (c clientMeta) Put(meta api.ClientMeta) error {
	return c.store.Put(meta.MAC, model.ClientMeta{Alias: meta.Alias, Notes: meta.Notes, UpdatedAt: time.Now().UTC()})
}

func (c clientMeta) All() (map[string]api.ClientMeta, error) {
	all, err := c.store.All()
	if err != nil {
		return nil, err
	}
	result := make(map[string]api.ClientMeta, len(all))
	for mac, meta := range all {
		result[mac] = api.ClientMeta{MAC: mac, Alias: meta.Alias, Notes: meta.Notes}
	}
	return result, nil
}

// apCommands is httpapi.Commands over the controller's ap-cmd-rx actions
// (design §5.6). Each command still needs the AP's ap_cmd feature, which
// the controller checks.
type apCommands struct{ manager *controller.Manager }

func (c apCommands) SendCommand(apID string, command httpapi.APCommand) (string, error) {
	control, ok := c.manager.Server(apID)
	if !ok {
		return "", httpapi.ErrCommandUnsupported
	}
	var result controller.CommandResult
	var err error
	switch command.Cmd {
	case controller.CommandReboot:
		result, err = control.Reboot(command.Reason)
	case controller.CommandRadioReinit:
		var params struct {
			Band string `json:"band"`
		}
		_ = json.Unmarshal(command.Params, &params)
		bands := []string{params.Band}
		if params.Band == "" || params.Band == "both" {
			bands = []string{telemetry.Band24, telemetry.Band5}
		}
		var sessions []string
		for _, band := range bands {
			result, err = control.RestartRadio(band, command.Reason)
			if err != nil {
				break
			}
			sessions = append(sessions, result.Session)
		}
		result.Session = strings.Join(sessions, ",")
	case controller.CommandDisconnect:
		// 802.11 reason 1 (unspecified): the operator kicked the client.
		result, err = control.DisconnectClients(command.Clients, 1, command.Reason)
	default:
		return "", fmt.Errorf("%w: %s", controller.ErrInvalidCommand, command.Cmd)
	}
	if errors.Is(err, controller.ErrUnverifiedCommand) || errors.Is(err, controller.ErrNotConnected) {
		return "", httpapi.ErrCommandUnsupported
	}
	if err != nil {
		return "", err
	}
	if result.Session != "" {
		return result.Session, nil
	}
	return fmt.Sprintf("%s-%d", command.Cmd, result.At.UnixNano()), nil
}

// storeScan keeps an AP's neighbour scan in the scans bucket (design
// §5.10) for the channel planner.
func storeScan(store *model.BoltStore, siteOf func(string) string) func(apID string, at time.Time, scan telemetry.Scan) {
	return func(apID string, at time.Time, scan telemetry.Scan) {
		siteID := siteOf(apID)
		if siteID == "" {
			return
		}
		bandChannels := map[string][]model.ChannelStat{}
		for _, channel := range scan.Channels {
			band := telemetry.Band5
			if channel.BandCode == 0 && channel.Channel <= 14 {
				band = telemetry.Band24
			}
			bandChannels[band] = append(bandChannels[band], model.ChannelStat{Channel: channel.Channel, Util: channel.Utilization.All, NoiseDBm: channel.NoiseDBm})
		}
		for band, neighbours := range map[string][]telemetry.ScanNeighbor{telemetry.Band24: scan.Band24, telemetry.Band5: scan.Band5} {
			if len(neighbours) == 0 && len(bandChannels[band]) == 0 {
				continue
			}
			record := model.BandScan{At: at.UTC(), Channels: bandChannels[band]}
			for _, neighbour := range neighbours {
				entry := model.ScanNeighbour{BSSID: neighbour.BSSID, SSID: neighbour.SSID, Channel: neighbour.Channel,
					Width: neighbour.WidthMHz, RSSI: neighbour.RSSI}
				if neighbour.QBSS != nil {
					stations, util := neighbour.QBSS.Stations, float64(neighbour.QBSS.Utilization)/255
					entry.StationCount, entry.Util = &stations, &util
				}
				record.Neighbours = append(record.Neighbours, entry)
			}
			if err := store.Scans().Put(siteID, apID, band, record); err != nil {
				log.Printf("AP %s: cannot store the %s GHz scan: %v", apID, band, err)
			}
		}
	}
}

// withRegistry sets the option registry the API validates typed settings
// against.
func withRegistry(config httpapi.Config) httpapi.Config {
	config.Registry = options.Default()
	return config
}
