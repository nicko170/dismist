# Sites, advanced configuration, and history

## Objective

The local controller manages multiple APs grouped into sites. There is no
organization layer. Each AP belongs to one site or is unassigned. WLANs,
advanced defaults, client history, telemetry summaries, and events are
queried by site. Each registered AP has its own connection and configuration
state.

## State and history

bbolt is the system of record. The database file is mode `0600` in the
configured data directory. Buckets are `sites`, `aps`, `networks`, `templates`,
`ap_status`, `client_sessions`, `samples`, `events`, and `meta`. Entity buckets
store JSON values keyed by stable IDs. Site-scoped keys start with the site ID;
history keys then include AP ID and big-endian UTC time so prefix scans are
efficient. Every state change occurs in one bbolt update transaction.

Legacy JSON state can be imported once into a new database, then kept as a
backup. AP secrets remain separate mode `0600` files; the DB stores only
their paths. API list responses omit secrets and WLAN passphrases. The bbolt
database retains the passphrases needed to generate AP configuration.

Fast AP telemetry is aggregated into minute samples for future graphs. Full
telemetry refreshes AP radio, WLAN, and associated-client state. Client session
records open on association and close on disassociation, with site, AP, SSID,
VLAN, IP, signal, and traffic totals. Connection and AP events are append-only
and queryable by site and time range. Raw protocol captures belong in private
storage outside the database and repository.

## Configuration model

Each device model has a full JSON base template. The controller preserves
unknown fields from the model's base AP config. It applies site JSON Merge Patch,
then AP JSON Merge Patch, then generates site WLANs. Each WLAN has typed core
fields (SSID, WPA2 passphrase, VLAN, enabled) and an advanced JSON patch over
the model's WLAN template. Identity, site assignment, WLAN ID, SSID, VLAN,
passphrase, and the revision timestamp are set from typed state last.

This makes every observed advanced AP and WLAN field available in a JSON editor
without claiming that every field has a known meaning. The UI displays the
effective config beside the site and AP patches and validates a proposed
config before saving it. Site changes rebuild configs for only its APs. An AP
override affects only that AP. A rejected config leaves the last applied
revision and previous DB transaction intact.

Each AP has its own config revision and timestamp. A site metadata edit does
not bump the config revision of an AP in another site. Moving an AP to a new
site rebuilds only that AP's effective config and closes its old site's active
client-session records with a `site moved` reason.

## Control plane

One WSS listener accepts registered APs. It selects an AP record by its
management source address, then checks the ID and device secret in the AP auth
hello. Multiple APs in a site share WLAN definitions, while each has a
separate connection, config revision, status, and telemetry stream. The DNS
override answers the Mist hostname only for registered AP addresses;
unrelated queries are forwarded. The portable deployment serves the
management UI on the LAN; see [deployment](deployment.md) for listener and
admin access setup.

## Acceptance

1. Migrating existing state preserves each AP's site, WLAN, and credential
   references.
2. Creating a second site and simulated AP in tests does not change existing
   AP records.
3. Editing a site WLAN updates the effective config of its APs; an AP override
   changes one AP only.
4. Advanced JSON edits round-trip through bbolt and preserve unknown fields.
5. Associated clients, client sessions, minute samples, and events are stored
   under the correct site and AP and can be queried after restart.
6. The UI supports site selection, AP selection, typed WLAN edits, advanced
   config editing, client and connection views, and history queries.
