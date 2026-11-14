package model

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	bolt "go.etcd.io/bbolt"
)

// Schema-2 bookkeeping (design §4.1 rules 2 to 4):
//
//   - The compiler runs the typed pipeline only when the schema-2 migration
//     has committed and no older binary has written since (State.Schema).
//   - Every write by this binary while the state is typed records
//     meta.v2_revision (the revision it wrote) and meta.v2_legacy_digest
//     (an HMAC over the schema-1 projection of every record). An older
//     binary rewrites whole buckets with its own structs, dropping every
//     typed key, and moves the revision without updating either: at the
//     next start the state is treated as schema 1 until it is re-migrated.
//   - compat is raised to 2, in the same transaction, the first time a
//     typed state stores something a schema-1 binary would compile wrongly.

// migrationStateDoc is the part of meta.migration_state the store reads.
type migrationStateDoc struct {
	State string `json:"state"`
}

// SchemaStatus describes how the store derived State.Schema.
type SchemaStatus struct {
	// Meta is meta.schema (absent = 1).
	Meta uint64 `json:"meta"`
	// Config is the schema the compiler uses (State.Schema).
	Config uint64 `json:"config"`
	// OlderWriter reports that meta.schema is 2 but an older binary has
	// written since (revision or legacy digest moved): the state must be
	// re-migrated from the legacy keys.
	OlderWriter bool `json:"older_writer"`
	// Legacy reports that meta.migration_state is "legacy".
	Legacy bool `json:"legacy"`
}

// legacyProjection is the schema-1 view of the state that the legacy digest
// covers: the keys a schema-1 binary compiles from and identifies records
// by. System-maintained revision fields are left out (the revision check
// covers every write).
type legacyProjection struct {
	Sites []struct {
		ID, Name, Country, Timezone string
		SSHPublicKeys               []string
		Advanced                    json.RawMessage
	}
	APs []struct {
		ID, Name, SiteID, ManagementIP string
		Advanced                       json.RawMessage
	}
	Networks []struct {
		ID, SiteID, SSID, PSK string
		VLANID                int
		Enabled               bool
		Advanced              json.RawMessage
	}
}

// LegacyDigest is the keyed digest of the schema-1 projection of state
// (meta.v2_legacy_digest).
func LegacyDigest(key []byte, state State) string {
	var projection legacyProjection
	sites := append([]Site(nil), state.Sites...)
	sort.Slice(sites, func(i, j int) bool { return sites[i].ID < sites[j].ID })
	for _, site := range sites {
		// nil and empty lists encode the same on disk (omitempty), so the
		// digest must not tell them apart.
		keys := site.SSHPublicKeys
		if len(keys) == 0 {
			keys = nil
		}
		projection.Sites = append(projection.Sites, struct {
			ID, Name, Country, Timezone string
			SSHPublicKeys               []string
			Advanced                    json.RawMessage
		}{site.ID, site.Name, site.Country, site.Timezone, keys, compactRaw(site.Advanced)})
	}
	aps := append([]APRecord(nil), state.APs...)
	sort.Slice(aps, func(i, j int) bool { return aps[i].ID < aps[j].ID })
	for _, ap := range aps {
		projection.APs = append(projection.APs, struct {
			ID, Name, SiteID, ManagementIP string
			Advanced                       json.RawMessage
		}{ap.ID, ap.Name, ap.SiteID, ap.ManagementIP, compactRaw(ap.Advanced)})
	}
	networks := append([]Network(nil), state.Networks...)
	sort.Slice(networks, func(i, j int) bool {
		return networks[i].SiteID+"\x00"+networks[i].ID < networks[j].SiteID+"\x00"+networks[j].ID
	})
	for _, network := range networks {
		projection.Networks = append(projection.Networks, struct {
			ID, SiteID, SSID, PSK string
			VLANID                int
			Enabled               bool
			Advanced              json.RawMessage
		}{network.ID, network.SiteID, network.SSID, network.PSK, network.VLANID, network.Enabled, compactRaw(network.Advanced)})
	}
	raw, _ := json.Marshal(projection)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("v2-legacy\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func compactRaw(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}

// readSchemaStatus derives the config schema of a state read from tx.
func readSchemaStatus(tx *bolt.Tx, key []byte, state State) (SchemaStatus, error) {
	status := SchemaStatus{Meta: 1, Config: 1}
	meta := tx.Bucket([]byte(BucketMeta))
	if meta == nil {
		return status, nil
	}
	schema, err := readUint(meta, MetaSchema, 1)
	if err != nil {
		return status, err
	}
	status.Meta = schema
	var migration migrationStateDoc
	if raw := meta.Get([]byte(MetaMigrationState)); raw != nil {
		if err := json.Unmarshal(raw, &migration); err != nil {
			return status, fmt.Errorf("meta.%s: %w", MetaMigrationState, err)
		}
	}
	status.Legacy = migration.State == "legacy"
	if schema < 2 {
		return status, nil
	}
	v2Revision, err := readUint(meta, MetaV2Revision, 0)
	if err != nil {
		return status, err
	}
	digest := string(meta.Get([]byte(MetaV2LegacyDigest)))
	if v2Revision != state.Revision || digest == "" || len(key) == 0 || digest != LegacyDigest(key, state) {
		status.OlderWriter = true
	}
	if !status.OlderWriter && !status.Legacy {
		status.Config = schema
	}
	return status, nil
}

// SchemaStatus reports how State.Schema was derived at the last read.
func (s *BoltStore) SchemaStatus() SchemaStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.schemaStatus
}

// RefreshSchema re-derives State.Schema from meta (after migrate records
// meta.migration_state outside a state write).
func (s *BoltStore) RefreshSchema() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.View(func(tx *bolt.Tx) error {
		status, err := readSchemaStatus(tx, s.hashKey, s.state)
		if err != nil {
			return err
		}
		s.schemaStatus = status
		s.state.Schema = status.Config
		return nil
	})
}

// RequiredCompat returns the compat a typed state needs (design §4.1 rule
// 4): 2 when any network stores something a schema-1 binary would compile
// wrongly or refuse (a security other than WPA2-Personal or WPA2/WPA3-
// Personal, a 64-hex PSK, or Multi-PSK keys), with the reasons; 1
// otherwise.
func RequiredCompat(state State) (uint64, []string) {
	var reasons []string
	for _, network := range state.Networks {
		switch {
		case network.Security != "" && network.Security != SecurityWPA2Personal && network.Security != SecurityWPA2WPA3Personal:
			reasons = append(reasons, fmt.Sprintf("network %s uses %s", network.ID, network.Security))
		case IsHexPMK(network.PSK):
			reasons = append(reasons, fmt.Sprintf("network %s has a 64-hex PSK", network.ID))
		case network.MPSK != nil && len(network.MPSK.Keys) > 0:
			reasons = append(reasons, fmt.Sprintf("network %s has per-device passphrases", network.ID))
		}
	}
	if len(reasons) > 0 {
		return 2, reasons
	}
	return 1, nil
}

// commitSchema2 records meta.v2_revision and meta.v2_legacy_digest for a
// typed state and raises compat when the state needs it.
func commitSchema2(tx *bolt.Tx, key []byte, next State) error {
	if !next.Typed() {
		return nil
	}
	meta := tx.Bucket([]byte(BucketMeta))
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], next.Revision)
	if err := meta.Put([]byte(MetaV2Revision), revision[:]); err != nil {
		return err
	}
	if err := meta.Put([]byte(MetaV2LegacyDigest), []byte(LegacyDigest(key, next))); err != nil {
		return err
	}
	required, _ := RequiredCompat(next)
	compat, err := readUint(meta, MetaCompat, 1)
	if err != nil {
		return err
	}
	if required > compat {
		return putUint(meta, MetaCompat, required)
	}
	return nil
}
