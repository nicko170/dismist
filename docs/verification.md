# Verification records

This file is the list of checks only real hardware can answer, and the
record of their results. There is one AP site (Home) and no separate lab AP:
everything that can be tested without hardware is an automated test
(`tools/fakeap`, synthetic frames, the in-process SSH AP and `httptest`
controllers in `cmd/dismist`), and everything else is a check below that the
operator runs on the production AP, one at a time, in a quiet window, under
the [production change rules](#production-change-rules). Options whose
catalogue confidence is `needs-live-test` stay experimental (the site's
experimental switch, enforced by the server) until a check here promotes
them.

Agents and automation never touch the AP, the cluster or Mist; every check
here is run by the operator.

## Rules for this file

This repository is public. Records here must not contain:

- MAC addresses, AP IDs, serial numbers, BSSIDs, SSIDs, IP addresses,
  hostnames, client names or any other client identity;
- passphrases, AP secrets, tokens, keys, or captured frames.

Refer to APs by role ("production AP", "second AP") and to clients by count.
Keep raw material (`dismist ap33 inspect --json` output, logs, captures,
backups) in private storage and cite it here by its private file name and
SHA-256 only. Effective hashes are keyed HMACs (design §4.5) and may be
recorded.

## Tools for the checks

From a machine on the AP's LAN:

```sh
umask 077
dismist ap33 inspect --json --log-lines 500 root@<ap> > <private-dir>/inspect-<date>.json
dismist ap33 inspect --log-lines 500 root@<ap>      # the same, for reading
dismist ap33 ssh root@<ap> status                   # short status: boot count, uptime, slot
```

`inspect` redacts known secret fields from the log lines it prints, but its
output still identifies the AP: keep it private and copy only the facts the
records ask for. Radio facts come from `wl` over SSH (read-only):

```sh
ssh root@<ap> 'for i in wl0.1 wl1.1; do wl -i $i chanspec; wl -i $i txpwr1; wl -i $i wpa_auth; wl -i $i mfp; done; wl country'
ssh root@<ap> 'wl -i wl1.1 chanspecs'               # the full channel list the driver allows
ssh root@<ap> 'wl -i wl1.1 assoclist | wc -l'       # associated clients on 5 GHz
ssh root@<ap> 'tail -f /var/log/ep-control.log'     # follow the agent while a change applies
```

Controller facts come from the API with a read-scope token
(`Authorization: Bearer dsm_…`): `GET /api/aps/<id>` (status, revisions,
identity, connection count), `GET /api/aps/<id>/config?view=normalised`
(effective hash) and `GET /api/system`.

---

## Recorded findings

### 2026-11-14: production AP, read-only (`apfw-0.15.35064`)

| Question (design §9) | Result | Evidence |
| --- | --- | --- |
| Q10 AU/890 chanspecs and 160 MHz | 5 GHz 80 MHz blocks 36-64, 100-112, 132-144, 149-161 (no 116-128 block); **no 160 MHz chanspec at all**; 2.4 GHz channels 1-13 at 20 MHz plus 40 MHz pairs. 160 MHz is not offered. | private `wl-readonly-20261114.txt` |
| Q11 power | `txpwr1` 17 on 5 GHz (ch149/80) and 12 on 2.4 GHz (ch1/20), equal to the configured `Power`/`RRM.Power`. | same |
| Q5 does losing the WSS session drop clients? | **No.** Over the v0.3.0 deploy the pod was gone about 21 s; the AP logged no deauth or disassociation, and clients per band were the same before and after. A pod restart or a reconnect-based push alone does not disturb clients; a config that restarts a WLAN or radio does. | private `events-around-deploy.json` |
| WPA2/WPA3 transition via `Auth.AKM {WPA2_PSK, WPA3_PSK}` + `Dot11w {Enabled: true, Required: false}` | `wpa_auth 0x40080 WPA2-PSK SAE`, `mfp 1`; iOS joins with SAE ("SAE Authentication Successful" events). | earlier session, same day |
| RF via the site `Radios[]` patch (`CCRev`/`CCRevSP` AU/890, channel, width, power) | All applied: `chanspec 149/80`, `country AU/890`, `txpwr1` 17 / 12; the agent logged "ccrev AU/890 from cloud" for r0, r1 and r2. | same |
| Q4 (part) does a WLAN security change disturb clients? | Yes: applying the security change dropped a laptop's Wi-Fi briefly. WLAN changes restart the BSS (`wlan_restart`). Whether a config with no WLAN or radio change does anything to clients is still open (check P2.4). | same |

### Known defects to verify after their fix

| Defect | Check after the fix |
| --- | --- |
| B: the hello's `LastRebootTime` is stored as the time of the hello ("now") | Reboot with `dismist ap33 ssh root@<ap> reboot --yes`; after the AP reconnects, `identity.last_reboot_time` on `GET /api/aps/<id>` equals the time `dismist ap33 inspect` shows on its "Last reboot" line (from `last_reboot_info.txt`), not the reconnect time, and `last_reboot_reason` is `reboot/dismist ap33 ssh reboot`. |

---

## Phase 0 baseline (H0)

Copy this section once, fill it in during the phase-0 deploy, and keep it.

### Record

| Field | Value |
| --- | --- |
| Date (UTC) | `<YYYY-MM-DD>` |
| Recorded by | `<name>` |
| Controller release before / after | `<image tag / chart version>` → `<image tag / chart version>` |
| Production AP firmware | `<apfw-...>` (from inspect) |
| Private evidence | `<file name>` `sha256:<...>` (one row per file) |

### Before the deploy

| Check | Value |
| --- | --- |
| Associated clients | `<n>` |
| BSSIDs and `Dev` names from `/api/telemetry` | recorded privately in `<file>`; count `<n>` |
| Applied revision / config revision | `<n>` / `<n>` |
| Boot count (hello or inspect) | `<n>` |
| Uptime | `<duration>` |

### `dismist ap33 inspect` on the production AP

| Fact | Value |
| --- | --- |
| Firmware version and build | `<...>` |
| Boot config (`bootcfg`) and root volume | `<wNiNcN>`, `<ubi0:rootfsN>` |
| Boot count, uptime | `<n>`, `<duration>` |
| Last reboot reason and time | `<reason>`, `<time>` (reported or pending) |
| ep-control running | yes / no |
| `/rw/etc/mist/ep-control/local_*` | none / `<name, size, sha256>` per file (design Q23) |
| Cloud config | `/rw` override yes / no; endpoint mode custom / DNS |
| CA bundle | `<n>` certificates; issuing roots of the served chain present yes / no |
| Error reports in `/var/log/errors` | `<names>` |

If any `local_*` file exists: back it up with the rest of `/rw`, record it with
`PATCH /api/aps/<id>` (`local_files`: names and SHA-256 only), and note here
that the migration will refuse to claim equivalence for this AP until it is
dealt with (design §4.7).

### Deploy

| Check | Result |
| --- | --- |
| AP reconnected within 2 minutes | yes / no (`<time>`) |
| Applied revision equals config revision | yes / no |
| Client count back within 5 minutes | yes / no (`<n>` at +5 min) |
| BSSIDs unchanged | yes / no |
| Wi-Fi during the pod restart (phone on the home WLAN pinging the gateway; design Q5) | `<lost n of m pings / no loss>` |

### H0

| Field | Value |
| --- | --- |
| `meta.h0` recorded | yes / no |
| Normaliser version | `<n>` |
| Effective hash (`GET /api/aps/<id>/config?view=normalised`) | `<hex>` |
| HMAC of the raw `legacyv0` bytes | `<hex>` |
| Phase-0 backup | `<private file name>` `sha256:<...>` |
| Rollback path checked (revert `ref.tag`; only new `meta` keys and buckets) | yes / no |

---

## Live AP stability investigation

Design §8.3 and Q21. Run with B (protocol) before any phase-1 protocol change
ships. The aim is to explain every reconnect and reboot so that existing
instability is neither blamed on v1 changes nor hides problems they cause.

### 1. Connections since the deploy

One row per AP session, from the stored hellos (tolerant hello pass) and the
controller's disconnect causes.

| # | Connected (UTC) | Disconnected (UTC) | Disconnect cause | BootCount | LastRebootReason | LastRebootTime | LastBootState | Uptime | ConnectUptime |
| - | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | | | read error / ping timeout / config save / pod restart / other | | | | | | |

### 2. What the AP logged

From `dismist ap33 inspect` (its "Notable log lines" list) and the full
ep-control and system logs it tails. Count and time each kind; quote only
sanitised fragments.

| Kind | Count | First / last (UTC) | Evidence (private file, line) |
| --- | --- | --- | --- |
| Reboots and their reasons | | | |
| procmon restarts; ep-control restarted by its wrapper ("for the Nth time", N > 1) | | | |
| OOM killer / out of memory | | | |
| Watchdog | | | |
| Panics and fatal errors (`/var/log/errors`) | | | |
| `Can't unmarshal` (config decode) | | | |
| PoE or LLDP power events | | | |
| Cloud disconnects seen by the AP | | | |

### 3. Classification

Every reconnect and reboot from steps 1 and 2:

| Event (UTC) | Kind | Class | Evidence |
| --- | --- | --- | --- |
| | reconnect / reboot | controller: config save / controller: pod restart / AP: ep-control restart / AP: reboot / network / unknown | |

### 4. 24-hour baseline

No dismist changes during the window.

| Measure | Value |
| --- | --- |
| Window (UTC) | `<start>` to `<end>` |
| Reconnects | `<total>`; per hour mean `<x>`, max `<y>` |
| Reboots | `<n>` (boot count `<a>` → `<b>`) |
| Uptime at the end | `<duration>` |
| Associated clients | min `<n>`, median `<n>`, max `<n>` |
| Notable log lines | `<n>` (see step 2) |

### 5. Findings

- Cause of the post-cutover reconnects and reboot: found / explained but not
  fixed / unexplained. `<one paragraph>`
- Thresholds derived from the baseline: Flapping `<n reconnects per window>`,
  Rebooted `<rule>`, rollback trigger "boot count increases unexpectedly" when
  `<rule>`.
- Decision: phase-1 subject replies may be switched on for the production AP
  yes / no, because `<reason>`. (If the AP still reboots on its own, the cause
  is fixed or explained first.)

---


## Phase 1 checks (observe)

Every protocol feature ships **off**, with a per-AP switch
(`PATCH /api/aps/<id> {"protocol_features": {"<name>": true}}`, or AP
settings › Diagnostics) and takes effect on the next frame. Turning a
feature off is its rollback; no deploy is needed.

**P1.1 Deploy with every feature off.** Backup first; check H0. Pass: the AP
reconnects within 2 minutes, `applied_revision == config_revision`, the
client count recovers within 5 minutes, and reconnects and boot count stay
within the phase-0 baseline for 24 hours.

**P1.2 Protocol features, one per day** (only after the phase-0 baseline
exists). For each of `subscribe_reply`, `file_discard`, `site_rest_404` and
`pong2`, in that order: switch it on for the production AP, follow the
ep-control log over SSH for the first 10 minutes, then compare reconnects and
boot count with the baseline for 24 hours.

| Feature | Pass |
| --- | --- |
| `subscribe_reply` | The once-a-minute `ep-subscribe` retries stop; no "Can't unmarshal" lines; no reconnect loop. |
| `file_discard` | The AP's `ep-file` offer is answered once per connection and not repeated; an info event "AP offered crash report" is stored. |
| `site_rest_404` | `wlc-site-rest` requests (every 5 minutes) are answered at once; no 10 s timeouts or errors about them in the log; no reconnect. |
| `pong2` | `connection.rtt_ms` is filled on AP detail; no reconnect. |

**P1.3 Radios tab against the radio.** AP detail › Radios shows 5 GHz ch149
80 MHz with max power 17, 2.4 GHz ch1 20 MHz with max power 12, and r2 as
"Scan". Compare with `wl -i wl1.1 chanspec`, `wl -i wl0.1 chanspec` and
`txpwr1`. Client count on the Clients page equals `wl assoclist` on both
bands (within the last full frame).

**P1.4 Monitoring.** The Prometheus target is up, fewer than 1,000 series,
the three Grafana dashboards render the production AP. Alert rules stay off
on au-syd-0 until the alert-routing question (Q14) is answered.

**P1.5 Second AP joins Home.** There is no Lab site: the second AP is
onboarded straight into Home with the CLI from a laptop on the LAN.

1. Backup; note the production AP's effective hash and client count.
2. Create an admin-scope API token (System › API tokens) and save it to
   `~/.config/dismist/token` (mode 600).
3. `dismist ap33 inspect root@<second-ap>`: root works, firmware is 0.15,
   one 32,768-byte EEPROM, and whether it has `local_*` files.
4. `dismist ap33 onboard root@<second-ap> --controller https://<controller> --token-file ~/.config/dismist/token --site Home --name <name> --dry-run`,
   then the same without `--dry-run`.
5. Pass: the command ends with "Done: … connected and in sync"; the Verify
   panel (`/onboard/register?step=verify&id=<id>`) shows every step done;
   both APs show the same public address and different LAN addresses; the
   **production AP's effective hash and `config_revision` did not change**
   (registering an AP is a dismist-only change for the other APs); clients
   on the production AP are undisturbed.
6. Expect: until the channel planner (phase 3) assigns channels, the second
   AP gets the site's radio settings. Before the schema-2 migration that is
   the Home site patch, so it runs the production AP's channels and power
   (co-channel). If that is a problem, give the second AP its own channels
   with an AP expert patch (`Radios` for that AP only) and check its
   `wl chanspec` afterwards.
7. Rollback: `dismist ap33 restore root@<second-ap> --backup <dir>` puts its
   original `cloud.ini` and CA bundle back and reboots it; delete its record
   in the UI.

| Record | Value |
| --- | --- |
| Date, firmware of the second AP | |
| Local files on the second AP | none / `<names and hashes>` |
| CLI result (exit code, time to "in sync") | |
| Production AP effective hash before / after | `<hex>` / `<hex>` (must match) |
| Client count on the production AP before / during / after | |

## Phase 2 checks (configure)

**P2.1 Migration dry run on a backup.** Download a backup and run
`dismist db migrate --dry-run backup.db` on the laptop. The automated
fixture `live situation` (`internal/migrate`) already proves the algorithm
on a copy of today's records; this check proves it on the real database.
Pass: verification PASS for every AP, and the lift report says:

| Object | Expected lift | Expected residual |
| --- | --- | --- |
| Home WLAN | security WPA2/WPA3-Personal (from `Auth.AKM {WPA2_PSK, WPA3_PSK}`), PMF optional (from `Dot11w {Enabled: true, Required: false}`), band steering on, idle timeout 3600 s, BSSID index measured (not predicted) | empty |
| Site Home | country AU, ccrev AU/890, RF managed; 5 GHz enabled, 80 MHz, fixed 17 dBm; 2.4 GHz enabled, 20 MHz, fixed 12 dBm | element-minimal leftovers of the `Radios[]` replacement only; `Monitor.CCRev` follows the patch (AU/890 lifts with no residual; an empty value stays as `{"Monitor":{"CCRev":""}}`) |
| Production AP | static channel override 149 on 5 GHz and 1 on 2.4 GHz | empty |
| Second AP (if onboarded) | the same static overrides (the site patch applied them to every AP) | empty |

Also: local-file status `none` for every AP; H0 recomputed from `meta.h0`
and from the phase-0 backup agree; the legacy `advanced` fields are
byte-identical (the report lists their digests before and after).

**P2.2 Deploy the migration.** Backup; deploy. Pass: the log says "migration
v2 committed" (legacy mode with a reason means nothing changed for the AP:
fix and Retry); `/system/migration` matches the dry run; the production AP's
normalised hash equals the recomputed H0; `config_revision` did not move; the
post-delivery verification on the next `ep-config` passes (BSSIDs, channels,
widths, max power, country and WLAN set unchanged); and before/after `wl`
output is identical:

```sh
ssh root@<ap> 'for i in wl0.1 wl1.1; do wl -i $i chanspec; wl -i $i txpwr1; wl -i $i wpa_auth; wl -i $i mfp; done; wl country'
```

The Home WLAN shows WPA2/WPA3-Personal; RF shows 5 GHz 149/80 at 17 dBm and
2.4 GHz 1/20 at 12 dBm. Rollback: while `meta.compat` is 1, revert the
image tag (the untouched legacy fields reproduce the old config); with
`compat` 2, restore `controller.db.pre-v2-*` first, then revert the tag.

**P2.3 `ep-config-change` push (`config_change_notify`).** The feature is off
by default; with it off every change reconnects the AP, which Q5 showed does
not disturb clients. On the production AP: follow the ep-control log, switch
the feature on, change LED brightness 255 → 254, then back. Pass for each
change: the AP re-requests `ep-config` within 5 s ("refetching config"),
`ep-config-applied` reports the new `Timestamp`, `connection.connection_count`
does not change, the client count stays steady, and no "Can't unmarshal"
line appears. Repeat 10 times over a day and check boot count and agent
memory. If the AP does not re-fetch, the controller falls back to a
reconnect after 15 s; after two fallbacks it sets the AP back to
`reconnect` by itself. Record the body and service class that worked.

**P2.4 Does a no-radio change disturb clients? (Q4).** In the same session
as P2.3: `wl -i wl1.1 assoclist` and `wl -i wl0.1 assoclist` before and 30 s
after the LED-only change. Pass: the same clients, no deauth or
disassociation events. If clients drop, LED/SSH/name changes get the
`wlan_restart` disruption class instead of `none`.

**P2.5 Experimental WLAN and RF values.** Each `needs-live-test` value that
someone wants to use (WPA3-only Personal, OWE, 11r, rate limits, per-WLAN
DTIM, other ccrevs, …) is tried once on the production AP using the
[promotion record](#promotion-record), on a **separate test WLAN** where
possible (a second SSID on its own VLAN, so the home WLAN's clients are not
restarted), one value per window. Values that fail stay experimental.

## Phase 3 checks (operate)

**P3.1 Channel plan with two APs.** `POST /api/sites/<home>/rf/plan?dry_run=1`
(RF › Plan › Preview). Pass: the proposal moves at most the second AP off the
production AP's channels, keeps DFS off, and its disruption line names only
the radios that change. Apply in a quiet window; only those radios bounce;
clients recover within 2 minutes; `wl chanspec` matches the plan on both
APs; the post-delivery verification passes. Rollback: restore the previous
revision from Changes.

**P3.2 Locate (`LED.Locator`, experimental, Q19).** On the production AP:
Locate for 2 minutes from AP detail. Pass: the LED shows a locate pattern
while it runs, returns to normal when the timer clears it (and when Stop is
pressed early), and the client count is steady (LED changes are pushed like
any config change: see P2.3/P2.4). Record whether the AP needs the config
re-sent to stop.

**P3.3 Reboot.** Two routes:

- From the LAN, always available: `dismist ap33 ssh root@<ap> reboot --yes`.
  It records the reason in `last_reboot_info.txt`, so the AP's next hello
  reports `reboot/<reason>` instead of an unexplained reboot.
- From the controller over `ap-cmd-rx` (experimental, off until this check
  passes; the wire format below was recovered from the 0.15 agent, not
  captured). With the ep-control log followed over SSH, send the reboot
  action. Pass: the log shows "Rx reboot command" and "Reboot reason: <the
  reason sent>"; the AP reboots once; the next hello's `LastRebootReason`
  carries the reason. If the log shows nothing, repeat with service class 0
  instead of 2. A "Can't unmarshal ap_cmd_rx" line means the body is wrong:
  record it and keep the action disabled. Record which subject, class and
  body worked; only then is the firmware version added to the verified list.

**P3.4 Restart radios and disconnect client** (`ap-cmd-rx` `radio_reinit`,
`disconnect`; experimental). Only after P3.3 passed, one at a time: pass when
the log shows "Rx radio reinit command" / "Rx disconnect command to
clients" and the radio (or the one client) comes back within a minute, with
other clients undisturbed. The radio parameter format (`Radio`) is unknown:
try `r0`, then `wl0`, watching the log; record what the agent accepts.

## Phase 4 checks (polish)

Each of these is experimental until its record here passes, and each runs
on a separate test WLAN where it can:

| Check | Pass |
| --- | --- |
| Guest preset with rate limit and schedule | The WLAN appears and disappears on schedule (`wl -i <dev> bss`); a client's throughput is capped near the configured rate (units are unverified: record measured against configured). |
| WPA2/WPA3-Enterprise with RADIUS | A client authenticates against the RADIUS server; `wl wpa_auth` shows the expected AKMs; accounting records arrive when configured. |
| Per-device passphrases (Multi-PSK) | Two clients with different passphrases join the same SSID and land on their configured VLANs. |
| NotModified reply (Q6) | After a controller restart the AP does not re-apply an unchanged config (no "applying config" in the log) and stays in sync. |
| Reveal passphrase and Wi-Fi QR | The revealed passphrase joins a phone; the QR joins an iPhone; both reveals appear in the audit log. |

---

## Production change rules

There is only the production AP (and, from P1.5, the second AP in the same
site), so every check above is a production change:

1. Backup first, and check the effective hash against the last recorded value
   (same normaliser version; otherwise recompute both).
2. One change at a time, in a quiet window, watched for 10 minutes, with the
   ep-control log followed over SSH.
3. Roll back if the AP is disconnected for more than 2 minutes, the client
   count drops by more than half for more than 5 minutes, the config is
   rejected, the post-delivery verification fails, or reconnects or reboots
   exceed the phase-0 baseline.
4. Rollback ladder: a protocol feature → switch it off; a config change →
   restore the previous revision (Changes); a release → revert the image tag
   (restore `controller.db.pre-v2-*` first once `compat` is 2). The AP keeps
   its last good config while the controller is away, and its root SSH keys
   stay in both the legacy and typed fields, so the LAN path
   (`dismist ap33 ssh`, `dismist ap33 restore`) stays open.
5. Management network options (management VLAN, static IP, `APRecovery`)
   are never tried on the production AP in v1.

## Promotion record

One record per option or enum value promoted from `needs-live-test` to
`likely` or `confirmed`; the registry PR cites it.

| Field | Value |
| --- | --- |
| Option (registry key) and value | `<key>` = `<value>` |
| Catalogue reference | `<[WLAN §n] / [RF §n] / [SITE §n]>` |
| Date (UTC), tester | |
| AP firmware | `<apfw-...>` |
| WLAN used | home WLAN / separate test WLAN |
| Compiled fragment (sanitised: no SSIDs, PSKs, IDs) | inline JSON, or a fenced block below the table |
| Steps | `<numbered>` |
| What the AP did | ep-control log lines; `wl` output (`wl wpa_auth`, `wl -i <dev> chanspec`, `txpwr1`, …) |
| Client join on the latest iOS | yes / no, `<details>` |
| Clients on other WLANs disturbed | yes / no |
| Post-delivery verification | pass / fail |
| Result | promote to `likely` / `confirmed` / keep experimental / reject |
| Registry PR | `<link>` |

## Production change record

One row per change made under the rules above.

| Field | Value |
| --- | --- |
| Date and window (UTC) | |
| Change | `<what, which scope>`; dry-run impact `<disruption classes>` |
| Backup taken first | `<private file name>` `sha256:<...>` |
| Effective hash before matches the last recorded value (same normaliser version) | yes / no (`<hex>`) |
| Effective hash after | `<hex>` |
| Watched 10 minutes | yes |
| Rollback triggers (any → roll back) | disconnected > 2 min: no; clients fell by more than half for > 5 min: no; config rejected: no; post-delivery verification failed: no; reconnects or reboots above the phase-0 baseline: no |
| Outcome | kept / rolled back via `<ladder step>` |

---

## Appendix: the `ap-cmd-rx` command format (static analysis)

The command channel was never captured from the Mist cloud. The format below
was recovered from the AP33 0.15 agent binary (`ep-control`,
`apfw-0.15.35064`) by reading its type descriptors and the string table of
its command handler; it is what check P3.3 tests.

- The agent registers the subject `ap-cmd-rx` at start-up and hands each
  message body to its command handler. The body is JSON for this Go struct
  (no JSON tags, so the keys are the field names, matched
  case-insensitively):

  ```go
  struct {
      Cmd     string
      Params  json.RawMessage
      Clients []ethernet.MAC
      Sender  string
      Reason  string
  }
  ```

  A body that does not decode is logged as "Can't unmarshal ap_cmd_rx" and
  dropped. Every command is logged as "<Cmd> cmd from <Sender> because
  <Reason>".
- `Cmd` values the handler accepts: `reboot`, `radio_reinit`,
  `radio_reload`, `radio_control`, `radio_reinit_psmwd`,
  `radio_assert_type`, `radio-cmd-generic`, `disconnect`, `deauth`,
  `deauth_clients_on_rogue`, `ping`, `traceroute`, `arp`, `pcap`,
  `get_wlan_map`, `spectrum`, `ftm_mode`, `synthetic-test`, `marvis-mini`,
  `purge-minis`, `pprof-report`, `set-extio-outputs`, `set-flowqos-learn`,
  `l2tp-disconnect`, `radius-server-refresh`, `select-int-ext-antenna`,
  `esl-cmd`, `uwb-cmd`, `zigbee-cmd`, `robo_reset`, `switch-cloud`,
  `fips-zeroize`.
- `Params` by command, where recovered:

  | Cmd | Params |
  | --- | --- |
  | `reboot` | `{"Reason": string}`; the reason defaults to `unknown`. Params that do not decode are logged ("Can't unmarshal reboot params") and the AP **still reboots**. |
  | `radio_reinit`, `radio_reload` | `{"Radio": string}` (value format unknown) |
  | `radio_control` | `{"Action": string, "Radio": string}` |
  | `radio_assert_type` | `{"Radio": string, "AssertType": string, "AfterSec": string}` |
  | `disconnect` | `{"Clients": [MAC], "Reason": int}` |
  | `get_wlan_map` | `{"Dev_name": string}` |

- Command output (ping, traceroute, ARP) is published on the telemetry topic
  `ap-cmd-output-` as `{Session, Output}`.
- Not recovered: the service class the agent expects (try 2, then 0), and
  whether the agent acknowledges a command.
- Never sent by dismist: `robo_reset`, `switch-cloud`, `fips-zeroize`
  (destructive or cloud-switching).

The candidate reboot message is therefore subject `ap-cmd-rx` with body
`{"Cmd":"reboot","Params":{"Reason":"<reason>"},"Clients":[],"Sender":"dismist","Reason":"<reason>"}`.
Until P3.3 passes, reboot from the controller stays disabled (501
`unverified_command`) and `dismist ap33 ssh reboot` is the supported route.
