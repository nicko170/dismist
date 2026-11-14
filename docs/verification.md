# Verification records

This file records what was verified on real hardware: the phase-0 baseline of
the production AP (its stability before any v1 protocol change), every Lab
test that promotes an experimental option, and every production change. The
v1 design (§8.3 and §8.8) defines what each record must contain; the
templates below follow it. Later judgements (the Flapping and Rebooted alert
thresholds, and the "boot count increases unexpectedly" rollback trigger) are
measured against the baseline recorded here.

## Rules for this file

This repository is public. Records here must not contain:

- MAC addresses, AP IDs, serial numbers, BSSIDs, SSIDs, IP addresses,
  hostnames, client names or any other client identity;
- passphrases, AP secrets, tokens, keys, or captured frames.

Refer to APs by role ("production AP", "Lab AP") and to clients by count. Keep
raw material (`dismist ap33 inspect --json` output, logs, captures, backups)
in private storage and cite it here by its private file name and SHA-256 only.
Effective hashes are keyed HMACs (design §4.5) and may be recorded.

Collect AP-side facts with the read-only inspection, from a machine on the
AP's LAN:

```sh
umask 077
dismist ap33 inspect --json --log-lines 500 root@<ap> > <private-dir>/inspect-<date>.json
dismist ap33 inspect --log-lines 500 root@<ap>      # the same, for reading
```

`inspect` redacts known secret fields from the log lines it prints, but its
output still identifies the AP: keep it private and copy only the facts the
templates ask for.

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

## Lab promotion record

Design §8.8. One record per option or enum value promoted from
`needs-live-test` to `likely` or `confirmed`; the registry PR cites it. Lab AP
only, never the production AP first.

| Field | Value |
| --- | --- |
| Option (registry key) and value | `<key>` = `<value>` |
| Catalogue reference | `<[WLAN §n] / [RF §n] / [SITE §n]>` |
| Date (UTC), tester | |
| Lab AP firmware | `<apfw-...>` |
| Compiled fragment (sanitised: no SSIDs, PSKs, IDs) | inline JSON, or a fenced block below the table |
| Steps | `<numbered>` |
| What the AP did | ep-control log lines; `wl` output (`wl wpa_auth`, `wl -i r0 chanspec`, `txpwr1`, ...) |
| Client join on the latest iOS | yes / no, `<details>` |
| Clients on other WLANs disturbed | yes / no |
| Post-delivery verification (design §5.9) | pass / fail |
| Result | promote to `likely` / `confirmed` / keep experimental / reject |
| Registry PR | `<link>` |

---

## Production change record

Design §8.8: one change at a time, in a quiet window, watched for 10 minutes.

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
