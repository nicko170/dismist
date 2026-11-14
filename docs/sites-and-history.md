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
backup. Each AP record holds that AP's 32-byte EEPROM authentication secret;
the controller reads secrets from the database, not from files. The API
accepts a secret as hex or base64 on registration and edit (a blank secret on
edit keeps the stored one), never returns it, and reports only `has_secret`.
API list responses also omit WLAN passphrases, which the database keeps to
generate AP configuration. Records written by older releases that still name
a `secret_file` are migrated at startup: the file is imported into the
record and the path cleared. A missing, unreadable or non-private file is
logged and left for the operator to upload again, and that AP is skipped
until it has a secret; other APs are unaffected. Because the database now
holds every AP secret as well as the passphrases, a copy of it (or of a
backup) grants control of the APs.

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

One WSS listener accepts every AP. It routes each connection by the AP ID in
its `ep-auth` hello to that AP's record, then authenticates it with that AP's
own secret. The connection becomes the AP's session only after it
authenticates, so an unauthenticated client that names an AP cannot displace
it, and only authenticated frames count as liveness. The source address is
not part of identity: any number of APs can connect from one NAT address,
and the API reports each AP's own LAN address (`lan_ip`, from the AP's
telemetry) beside the address its session comes from (`remote_address`).

An AP record may pin a source IP (`management_ip`); a pinned AP is accepted
only from that address. Pins also feed the optional DNS override: in DNS
mode the controller answers the Mist hostname `ep-terminator.mistsys.net`
only to pinned addresses and forwards their other queries, so every AP that
relies on it must be pinned. An AP whose `cloud.ini` names the controller's
own hostname needs neither the DNS override nor a pin.

Multiple APs in a site share WLAN definitions, while each has a separate
connection, config revision, status, and telemetry stream. The generated
config's `ID` is always the AP's authenticated ID. The portable deployment
serves the management UI on the LAN; see [deployment](deployment.md) for
listener and admin access setup.

## Acceptance

1. Migrating existing state preserves each AP's site, WLANs, and secret
   (imported from a legacy secret file when the record names one).
2. Creating a second site and simulated AP in tests does not change existing
   AP records, and two APs connecting from the same address are each
   identified by their own authenticated ID.
3. Editing a site WLAN updates the effective config of its APs; an AP override
   changes one AP only.
4. Advanced JSON edits round-trip through bbolt and preserve unknown fields.
5. Associated clients, client sessions, minute samples, and events are stored
   under the correct site and AP and can be queried after restart.
6. The UI supports site selection, AP selection, typed WLAN edits, advanced
   config editing, client and connection views, and history queries.
