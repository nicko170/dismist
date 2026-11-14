# Sites, configuration and history

## Model

One controller manages any number of APs grouped into sites; there is no
organisation layer. Each AP belongs to one site or is unassigned. A site
owns its WLANs, radio profile, operator SSH keys and defaults; clients,
events and history are queried by site. Each AP has its own connection,
config revision and status.

## Control plane

One WSS listener accepts every AP. It routes each connection by the AP ID in
its `ep-auth` hello to that AP's record and authenticates it with that AP's
own 32-byte secret. The connection becomes the AP's session only after it
authenticates, so an unauthenticated client that names an AP cannot displace
it, and only authenticated frames count as liveness. **The source address is
not part of identity**: any number of APs can connect from one NAT address,
and every view shows each AP's own LAN address (`lan_ip`, reported by the
AP) beside the address its session comes from (`remote_address`).

An AP record may pin a source IP; a pinned AP is accepted only from that
address. Pins also feed the optional DNS mode, where the controller answers
the Mist hostname only to pinned addresses, so every AP that relies on it
must be pinned. An AP whose `cloud.ini` names the controller's own hostname
needs neither.

An ID that says hello without a record, and a registered AP whose attempts
fail authentication or come from the wrong pinned address, appear in the
**pending list** (Add an AP › Waiting to be adopted). A dismissed entry stays
hidden for 30 days; its attempts keep counting.

The controller only sends the AP frames it has been told to: every reply
beyond the original handshake and config (`ep-subscribe`, `ep-file`,
`wlc-site-rest`, `ping2`, and the `ep-config-change` push) is a **protocol
feature**, off by default and switched per AP at runtime (AP settings ›
Diagnostics). Turning one off is its rollback.

## How config reaches an AP

The controller compiles one config per AP: the model template, then the
site's typed settings, then the site expert patch, then the AP's typed
overrides, then the AP expert patch; WLANs are compiled separately and
inserted. Fields dismist always sets (ID, name, site, timestamp, WLAN
identity and security) are written last. See
[config surface](config-surface.md) for every typed option and what it
writes.

A change recompiles every AP in the affected site in the same transaction
and pushes **only the APs whose compiled config changed** (ignoring the
timestamp). Renaming a site, changing its timezone or pinning an AP pushes
nothing. A push reconnects the AP by default (losing the session does not
disturb Wi-Fi clients); the `config_change_notify` feature instead sends
`ep-config-change` and falls back to a reconnect if the AP has not fetched
the new config within 15 s. Every config mutation accepts `?dry_run=1`,
which returns the per-AP diff and the disruption it causes ("5 GHz radio
restarts on 1 AP") without storing anything.

After every delivery the controller checks, about two minutes later, that
the AP reports what it was sent: each enabled WLAN on each band with the
right BSSID index, channels and widths, maximum power, country and the
config timestamp. A mismatch is flagged on the AP and raised as an event.

One AP that fails to compile never stops another from being served: it
keeps receiving the last config it was delivered, and the error shows on
its page.

## Effective config

The *effective config* is the compiled JSON as the firmware reads it:
keys matched case-insensitively, unknown keys ignored, radios matched by
band, the resolved channel, width and power. Its canonical form has a keyed
hash (HMAC-SHA256 with a per-database key, so it reveals nothing about the
passphrases inside), shown on the AP's Config page. Upgrades and migrations
must not change any AP's effective hash; only a previewed operator change
may.

## Schema 2: typed settings without changing what runs

Before v1, typed fields covered little and anything else (WPA2/WPA3
transition, radio settings) lived in JSON "advanced" patches. The schema-2
migration turns what each AP runs into typed settings **without changing
it**:

1. It computes every AP's current effective config with the frozen pre-v1
   compiler.
2. It lifts typed values from that effective config (not by parsing the
   patches): for example a network patch setting `Auth.AKM {WPA2_PSK,
   WPA3_PSK}` with `Dot11w {Enabled: true, Required: false}` becomes
   *WPA2/WPA3-Personal, PMF optional*; a site patch replacing `Radios[]` with
   AU/890, 5 GHz channel 149 at 80 MHz and 17 dBm, and 2.4 GHz channel 1 at
   20 MHz and 12 dBm becomes a managed AU radio profile with fixed power and
   static channel overrides on the site's APs.
3. Whatever the typed settings cannot express stays in a minimal residual
   expert patch.
4. It commits only if every AP's normalised effective config is
   byte-for-byte what it was. Otherwise it changes nothing and dismist runs
   on the legacy compiler with typed editing paused ("legacy mode", with the
   reason and a Retry on System › Migration).

The migration is additive: the legacy patch fields are left byte-identical
and `meta.compat` stays 1, so rolling the image back runs exactly the old
config. A copy of the database (`controller.db.pre-v2-<time>`) is written
first. An AP with `local_*` override files cannot be proven unchanged, so it
blocks the migration until they are dealt with. `dismist db migrate
--dry-run backup.db` runs the same steps on a backup and prints the report.

## State and history

bbolt is the system of record (`controller.db`, mode 0600). Configuration
records (`sites`, `aps`, `networks`) gain new keys only; `meta` holds the
schema and compatibility numbers, the hash key and the phase-0 baseline.
Other buckets keep the last compiled and delivered config per AP, persisted
web sessions and API tokens (only a hash of each token), dismissed pending
APs, config history, events (newest first per site, deduplicated by event
ID), client sessions, minute samples with per-radio data, and the latest
neighbour scan per AP.

Each AP record holds that AP's 32-byte EEPROM authentication secret. The API
accepts it as hex or base64 on registration and edit (absent on edit keeps
the stored one), never returns it, and reports only `has_secret`. Views also
omit WLAN passphrases, which the database keeps to generate AP config.
Together they grant control of the APs and the WLANs, so the database and
every backup of it are secret.

**Config history.** Every committed config change appends a record in the
same transaction: who (session or API token), when, a one-line summary, the
typed records of the affected site with every secret replaced by a keyed
digest, each affected AP's effective hash, and later whether each AP applied
it. Actions (reboot, locate, backup download, passphrase reveal, token
creation, migration) are recorded too. The last 500 revisions are kept.

**Restore** (Changes › a revision › Restore, always previewed first) makes
the site's config look like that revision through the normal change path:
typed settings and expert patches return to that revision's values, WLANs
deleted since are re-created (with their old BSSID index when it is free)
and WLANs created since are removed (or kept, if you choose). AP identity
(name, pin, secret, site, protocol features) is not restored. A passphrase
that differs from today's must be entered again or kept as it is; dismist
keeps no copies of old secrets.

**Retention**: samples 30 days, hourly rollups 400 days, client sessions 180
days, events 90 days. **Backups**: System › Backups downloads a consistent
copy, and scheduled snapshots are kept on the data volume within a size cap;
see [deployment](deployment.md).

## Acceptance

1. Upgrading keeps every AP's effective hash; the schema-2 migration of the
   live situation (WPA2/WPA3 network patch, `Radios[]` site patch) lifts the
   values above, leaves the legacy fields byte-identical and verifies every
   AP, and refuses otherwise.
2. Two APs connecting from the same address are each identified by their
   own authenticated ID; registering a second AP does not change the first
   AP's effective hash or config revision.
3. Editing a site WLAN pushes only the APs whose compiled config changed; an
   AP override changes that AP only; a dismist-only edit pushes nothing.
4. One AP with a broken patch or no secret does not stop the others from
   being served.
5. Events, client sessions, samples and config history are stored under the
   right site and AP and survive a restart; a restore previews its impact and
   secrets before it applies.
