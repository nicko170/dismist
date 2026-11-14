package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	bolt "go.etcd.io/bbolt"
)

// gatherV2 reads the migration inputs inside a transaction.
func gatherV2(btx *bolt.Tx, state model.State, tx *model.Tx, ctx *Context) (V2Input, error) {
	in := V2Input{Current: state, Template: ctx.Template, AcceptLocalFiles: ctx.AcceptLocalFiles, Now: ctx.Now, VAPs: map[string][]model.VAPRecord{}}
	if btx != nil {
		frozen, err := legacyv0.ReadState(btx)
		if err != nil {
			return in, fmt.Errorf("read legacy records: %w", err)
		}
		in.Frozen = frozen
	} else {
		in.Frozen = frozenFromModel(state)
	}
	if tx != nil {
		var err error
		if in.VAPs, err = tx.VAPs(); err != nil {
			return in, err
		}
		in.HashKey = tx.HashKey()
		if in.H0, err = tx.H0(); err != nil {
			return in, err
		}
	}
	return in, nil
}

// frozenFromModel converts model records to legacyv0's for the JSON store
// (tests); the bbolt store always reads the raw buckets.
func frozenFromModel(state model.State) legacyv0.State {
	frozen := legacyv0.State{Revision: state.Revision, UpdatedAt: state.UpdatedAt}
	for _, site := range state.Sites {
		frozen.Sites = append(frozen.Sites, legacyv0.Site{ID: site.ID, Name: site.Name, Country: site.Country,
			Timezone: site.Timezone, SSHPublicKeys: site.SSHPublicKeys, Advanced: site.Advanced})
	}
	for _, ap := range state.APs {
		frozen.APs = append(frozen.APs, legacyv0.APRecord{ID: ap.ID, Name: ap.Name, SiteID: ap.SiteID, ManagementIP: ap.ManagementIP,
			Model: ap.Model, Firmware: ap.Firmware, Secret: ap.Secret, SecretFile: ap.SecretFile, ConfigRevision: ap.ConfigRevision,
			ConfigUpdatedAt: ap.ConfigUpdatedAt, AppliedRevision: ap.AppliedRevision, Advanced: ap.Advanced})
	}
	for _, network := range state.Networks {
		frozen.Networks = append(frozen.Networks, legacyv0.Network{ID: network.ID, SiteID: network.SiteID, SSID: network.SSID,
			PSK: network.PSK, VLANID: network.VLANID, Enabled: network.Enabled, Advanced: network.Advanced})
	}
	return frozen
}

// applyTyped copies the typed fields of the planned state into the state
// being committed. Legacy and identity fields are never written.
func applyTyped(state *model.State, planned model.State) {
	sites := map[string]model.Site{}
	for _, site := range planned.Sites {
		sites[site.ID] = site
	}
	for index := range state.Sites {
		site := &state.Sites[index]
		if p, ok := sites[site.ID]; ok {
			site.SSH, site.Syslog, site.Management, site.APDefaults = p.SSH, p.Syslog, p.Management, p.APDefaults
			site.Protection, site.RF, site.Expert = p.Protection, p.RF, p.Expert
		}
	}
	aps := map[string]model.APRecord{}
	for _, ap := range planned.APs {
		aps[ap.ID] = ap
	}
	for index := range state.APs {
		ap := &state.APs[index]
		if p, ok := aps[ap.ID]; ok {
			ap.Expert, ap.LEDBrightness, ap.SSHEnabled, ap.RF, ap.RFPlan = p.Expert, p.LEDBrightness, p.SSHEnabled, p.RF, p.RFPlan
		}
	}
	networks := map[string]model.Network{}
	for _, network := range planned.Networks {
		networks[network.SiteID+"\x00"+network.ID] = network
	}
	for index := range state.Networks {
		n := &state.Networks[index]
		p, ok := networks[n.SiteID+"\x00"+n.ID]
		if !ok {
			continue
		}
		n.Security, n.PMF, n.RADIUS, n.MPSK, n.Bands, n.Hidden, n.Isolation = p.Security, p.PMF, p.RADIUS, p.MPSK, p.Bands, p.Hidden, p.Isolation
		n.BSSIDIndex, n.OWEIndex, n.BandSteering, n.Rates, n.MinRSSI, n.DTIM = p.BSSIDIndex, p.OWEIndex, p.BandSteering, p.Rates, p.MinRSSI, p.DTIM
		n.IdleTimeout, n.MaxClients, n.Roaming, n.Broadcast, n.DHCPOnly, n.DHCPDNSOnly = p.IdleTimeout, p.MaxClients, p.Roaming, p.Broadcast, p.DHCPOnly, p.DHCPDNSOnly
		n.WMMDisabled, n.UAPSDDisabled, n.Disable11ax, n.Disable11nac = p.WMMDisabled, p.UAPSDDisabled, p.Disable11ax, p.Disable11nac
		n.GroupRekeySec, n.EAPOLv1, n.SAEH2EOnly, n.TransitionDisable, n.OWESSID = p.GroupRekeySec, p.EAPOLv1, p.SAEH2EOnly, p.TransitionDisable, p.OWESSID
		n.RateLimit, n.QoSClass, n.HonourBlocklist, n.UcastDisassocOnBssDown = p.RateLimit, p.QoSClass, p.HonourBlocklist, p.UcastDisassocOnBssDown
		n.ReclaimTimeoutSec, n.Option82, n.Schedule, n.Expert = p.ReclaimTimeoutSec, p.Option82, p.Schedule, p.Expert
	}
}

// runV2 is the schema-2 step: plan, then commit (design §4.7 step 6). A
// refusal leaves everything as it was.
func runV2(state *model.State, tx *model.Tx, ctx *Context) error {
	in, err := gatherV2(tx.Bolt(), *state, tx, ctx)
	if err != nil {
		return err
	}
	result, err := PlanV2(in)
	report := result.Report
	if remigration := ctx.remigration; remigration != nil {
		report.Remigration = remigration
	}
	if raw, encodeErr := json.Marshal(report); encodeErr == nil {
		ctx.lastReport = raw
	}
	if err != nil {
		var refused *RefusedError
		if errors.As(err, &refused) {
			return err
		}
		return &RefusedError{Reason: err.Error()}
	}
	applyTyped(state, result.State)
	// Seed ap_config.desired with each AP's v2 compile (change detection
	// compares with it); ConfigRevision, ConfigUpdatedAt and so Timestamp
	// are unchanged, and nothing is pushed. The legacy compile is pinned:
	// the AP is served exactly the bytes the previous release served it
	// until a change touches it (equivalence is proven above, but a WLAN
	// whose raw bytes differ could still restart its BSS).
	apConfig := tx.APConfig()
	for _, ap := range state.APs {
		config, ok := result.Desired[ap.ID]
		if !ok {
			continue
		}
		desired := model.DesiredConfig{
			Revision:  compile.ConfigRevision(*state, ap),
			Timestamp: compile.ConfigTimestamp(compile.ConfigTime(*state, ap)),
			Bytes:     config,
			Pinned:    result.Legacy[ap.ID],
		}
		if err := apConfig.PutDesired(ap.ID, desired); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := tx.PutMetaRaw(model.MetaMigrationReport, raw); err != nil {
		return err
	}
	summary := "Migrated to schema 2"
	if report.Remigration != nil {
		summary = fmt.Sprintf("Re-migrated after running an older release; typed changes between rev %d and rev %d were replaced by the legacy config",
			report.Remigration.FromRevision, report.Remigration.ToRevision)
	}
	record := model.ChangeRecord{
		Revision: state.Revision + 1, At: ctx.Now.UTC(), Kind: model.ChangeMigration,
		Actor: model.Actor{Kind: model.ActorSystem, Name: "migration"}, Summary: summary, Report: raw,
	}
	if _, err := tx.ConfigHistory().Append(record); err != nil {
		return err
	}
	if err := tx.SetSchema(2); err != nil {
		return err
	}
	ctx.logf("migrate: %s; %d APs verified", summary, len(result.Desired))
	return nil
}

// DryRun plans the schema-2 migration of a store without writing anything
// (dismist db migrate --dry-run, /system/migration retry?dry_run=1).
func DryRun(store *model.BoltStore, opts Context) (V2Report, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	var report V2Report
	var planErr error
	err := store.View(func(btx *bolt.Tx) error {
		state := store.Snapshot()
		in, err := gatherV2(btx, state, nil, &opts)
		if err != nil {
			return err
		}
		in.HashKey = store.HashKey()
		if in.VAPs, err = readVAPs(btx); err != nil {
			return err
		}
		if in.H0, err = store.H0(); err != nil {
			in.H0 = nil
		}
		result, err := PlanV2(in)
		report, planErr = result.Report, err
		return nil
	})
	if err != nil {
		return report, err
	}
	return report, planErr
}

func readVAPs(btx *bolt.Tx) (map[string][]model.VAPRecord, error) {
	result := map[string][]model.VAPRecord{}
	bucket := btx.Bucket([]byte(model.BucketAPStatus))
	if bucket == nil {
		return result, nil
	}
	return result, bucket.ForEach(func(key, raw []byte) error {
		var record model.APStatusRecord
		if json.Unmarshal(raw, &record) == nil && record.VAPs != nil {
			result[string(key)] = record.VAPs
		}
		return nil
	})
}

// ReadReport returns the stored lift report.
func ReadReport(store *model.BoltStore) (V2Report, bool, error) {
	var report V2Report
	found, err := store.MetaJSON(model.MetaMigrationReport, &report)
	return report, found, err
}
