package httpapi

import (
	"log"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/model"
)

// applyChange runs a config mutation on one site's state. See mutate.
func (s *Server) applyChange(siteID string, change func(*model.State) error) error {
	_, err := s.mutate([]string{siteID}, func(state *model.State, _ *model.Tx) error { return change(state) })
	return err
}

// mutate runs a config mutation with change detection (design §4.4): inside
// the store transaction, after change has edited the state, every AP of the
// affected sites is compiled. Only the APs whose compiled config differs
// from ap_config.desired (ignoring Timestamp and SiteName) get a new
// ConfigRevision, ConfigUpdatedAt and desired config; after the commit only
// they are sent the new config and reconnected. A change that touches only
// dismist-side fields (a site name, a timezone, a pin, an AP secret,
// another AP's name) therefore bumps and disconnects nothing.
//
// A compile failure of any affected AP refuses the whole mutation (a 400
// naming the AP); nothing is stored.
func (s *Server) mutate(sites []string, change func(*model.State, *model.Tx) error) (compile.Applied, error) {
	var applied compile.Applied
	update := func(state *model.State, tx *model.Tx) error {
		previous := copyState(*state)
		if err := change(state, tx); err != nil {
			return err
		}
		var desired compile.DesiredStore
		if tx != nil {
			desired = tx.APConfig()
		}
		result, err := compile.Apply(state, desired, compile.ApplyOptions{
			Template: s.base, Sites: sites, Previous: &previous, Now: state.UpdatedAt,
		})
		if err != nil {
			return err
		}
		applied = result
		return nil
	}
	var err error
	if store, ok := s.store.(model.TxStateStore); ok {
		err = store.UpdateTx(update)
	} else {
		err = s.store.Update(func(state *model.State) error { return update(state, nil) })
	}
	if err != nil {
		return compile.Applied{}, err
	}
	return applied, s.push(applied.Pushes)
}

// push hands committed configs to the APs' control sessions. In phase 0 the
// only push mode is a reconnect: the AP fetches the new config when it
// comes back (design §5.6).
func (s *Server) push(pushes []compile.Push) error {
	var failed error
	for _, push := range pushes {
		control := s.controlFor(push.APID)
		if control == nil {
			continue
		}
		changed, err := control.SetConfigIfNewer(push.Config, push.Revision)
		if err != nil {
			log.Printf("AP %s: set config revision %d: %v", push.APID, push.Revision, err)
			failed = internalError("AP config update failed")
			continue
		}
		if changed {
			control.Disconnect()
		}
	}
	return failed
}

// copyState copies the parts of state a mutation may change, so the
// pre-mutation state can serve as the change-detection baseline.
func copyState(state model.State) model.State {
	state.Sites = append([]model.Site(nil), state.Sites...)
	state.APs = append([]model.APRecord(nil), state.APs...)
	state.Networks = append([]model.Network(nil), state.Networks...)
	for index := range state.Sites {
		site := &state.Sites[index]
		site.Advanced = append([]byte(nil), site.Advanced...)
		site.SSHPublicKeys = append([]string(nil), site.SSHPublicKeys...)
	}
	for index := range state.APs {
		state.APs[index].Advanced = append([]byte(nil), state.APs[index].Advanced...)
	}
	for index := range state.Networks {
		state.Networks[index].Advanced = append([]byte(nil), state.Networks[index].Advanced...)
	}
	return state
}
