# Deploying Dismist

## Server and ports

Build the setup CLI and daemon using the commands in [README](../README.md),
then place them on the server. One daemon process serves the embedded UI/API
on TCP 8088, AP WSS on TCP 443, and AP DNS on UDP and TCP 53. The default bind
address for each listener is `0.0.0.0`; use `-ui-listen`, `-ws-listen`, and
`-dns-listen` to override it. Permit admin LAN clients to reach 8088 and
registered APs to reach 443 and 53. The server does not edit DHCP, router
DNS, or NAT rules.

The server advertises an IPv4 address in AP DNS replies. With exactly one
usable, non-loopback IPv4 interface address, it detects that address. On a
multi-homed server, pass `-controller-ip` with the address APs can reach. If
admins open the UI through a DNS name, pass `-ui-host` with that name. The
default upstream DNS is `1.1.1.1:53`; use `-upstream-dns` to point at a
resolver reachable from the AP management network.

The UI opens one authenticated, site-scoped SSE connection for live AP,
client, event, and configuration changes. It keeps a slower refresh as a
fallback. If a reverse proxy fronts the UI, pass through `/api/sites/*/stream`
without response buffering and allow the 15-second heartbeat; streams close
after 30 minutes so the browser reconnects and the admin session is checked
again.

Prometheus metrics are served separately at `127.0.0.1:9090/metrics` by
default. Set `-metrics-listen` to another numeric loopback address, or to an
empty string to disable them. The endpoint is read-only and unauthenticated,
so Dismist refuses a non-loopback bind. A local Prometheus agent can scrape:

```yaml
scrape_configs:
  - job_name: dismist
    static_configs:
      - targets: ['127.0.0.1:9090']
```

A remote scraper needs a secured tunnel or a local scrape agent. Metrics cover
site/AP counts, connections, associated clients, telemetry frames, uptime,
configuration revisions, and last-seen time. Labels are limited to site and
AP IDs; client MACs, IP addresses, passphrases, and credentials are excluded.

The default private data paths are relative to the working directory:

| Path | Purpose |
| --- | --- |
| `data/controller.db` | bbolt sites, AP records, WLANs, and history |
| `data/ap-config.json` | AP firmware-compatible base config template |
| `data/tls.crt`, `data/tls.key` | AP WSS leaf certificate and private key |
| `data/ca.crt`, `data/ca.key` | Local AP WSS certificate authority and private key |
| `data/admin.token` | Single-admin token created by the web gate |

Build and run `dismist init --data-dir ./data` before starting the daemon.
Run it as the service user when using a system service. It generates a local
CA, an AP WSS certificate for `ep-terminator.mistsys.net`, and the admin token
using local randomness. It does not contact an AP or Mist cloud. Repeating
`init` verifies and reuses the existing files; it never replaces them. If a
certificate set is incomplete or invalid, inspect it and restore it from
backup before retrying. The generated WSS certificate lasts two years and
the CA lasts ten years; `init` does not renew either. Plan a replacement
and AP trust update before expiry.

Use absolute paths in a service unit. The database and token contain sensitive
data; restrict the data directory to the service user. `-db`, `-ap-config`,
`-tls-cert`, `-tls-key`, and `-admin-token-file` select other paths. The token
file is created with mode `0600` when absent; read it locally and enter its
64-character value in the UI login. The session is held in an HttpOnly cookie.
Operators may place HTTPS in front of the UI for access beyond a trusted LAN.

## AP preparation and initial state

Before starting, supply a full JSON base configuration compatible with the AP
firmware as `data/ap-config.json`. It must include a `WLANs` array with at
least one WLAN template containing `Auth` configured for PSK. The controller
uses this template to generate site WLANs and preserves other fields; a small
generic JSON fragment is not a proven replacement for a device config. Keep
secrets and passphrases in private files or the database, not in the source
tree.

When the base config includes `SwitchPorts`, Dismist adds every enabled site
WLAN's VLAN to each AP Ethernet port's `Vlans` allow list, preserving the port's
native `PortVlanID`. An explicit site or AP advanced `SwitchPorts` patch can
replace that generated list. The physical switch uplink must also carry those
VLANs; Dismist does not configure external switches or DHCP servers. If a
client associates but receives a `169.254.x.x` address, compare the effective
AP `SwitchPorts` and the upstream trunk before changing the WLAN passphrase.

The AP must trust the CA that issued the WSS certificate. For the certificate
created by `dismist init`, install `ca.crt` in the AP trust store through a
verified preparation path. A factory AP with its original Mist-only trust
store will reject this local certificate. The controller also needs each AP's
existing 32-byte EEPROM authentication secret as a raw mode `0600` file. A
random replacement secret will not authenticate an existing AP. See
[AP onboarding](onboarding.md) for preparation requirements and registration.

After an AP is authenticated, Site Settings can send OpenSSH public keys to
every AP in that site. Enter one `ssh-ed25519` or `ssh-rsa` public key per line;
the same operation is available as `GET`/`PUT /api/sites/{site-id}/ssh-keys`
with `{"keys":["ssh-ed25519 ..."]}`. Dismist validates and deduplicates up
to 32 keys, appends them to both AP SSH key fields, and preserves keys in the
firmware-compatible base config. A site key change reconnects its APs to apply
the new config. Never paste a private key. This path requires an already
authenticated AP and does not prepare a factory AP to trust the controller.

The first start with a new database may have zero APs; the UI can create
sites while AP DNS refuses all unregistered clients. To register an AP at
initial installation, prepare a private JSON import file and pass `-state`
**only when creating a new database**. Example structure:

```json
{
  "sites": [{"id": "office", "name": "Office"}],
  "aps": [{
    "id": "aa-bb-cc-00-00-01",
    "name": "Hallway",
    "site_id": "office",
    "management_ip": "192.0.2.101",
    "secret_file": "/var/lib/dismist/ap-secrets/hallway.bin"
  }],
  "networks": []
}
```

Replace both example IPs and identity with real values. The AP ID is its
actual MAC-derived controller ID; `management_ip` is the source address seen
by this server. The import runs once, only for a new bbolt database. Keep the
import file as a private backup; edits to it after import do not alter the
database. Site WLANs can then be created in the UI. New AP records may also
be created through `POST /api/aps` without restarting the server; the running
registry applies them within a few seconds. See [AP onboarding](onboarding.md).

Point the AP management LAN's DNS setting at the controller IP. The
controller answers `ep-terminator.mistsys.net` only for registered AP source
addresses and forwards their other DNS queries. The AP then connects over
WSS. A routed network or NAT must preserve the source IP expected by the AP
record, or that record must use the translated source address.

## Running under systemd

[ops/dismistd.service](../ops/dismistd.service) is a sample unit. Create a
dedicated `dismist` user, place the binary at `/usr/local/bin/dismistd`, and
make `/var/lib/dismist` mode `0700` and owned by that user. Run
`dismist init --data-dir /var/lib/dismist` as that user, then place the AP
template, import file if used, and AP secret files under that directory with
restrictive permissions. Edit the unit's `-controller-ip`
value and add `-state /var/lib/dismist/import.json` for a one-time initial
AP import. The unit grants bind capability for ports below 1024. Start it with
`systemctl enable --now dismistd` after reviewing those values and AP
management LAN routing. Use `journalctl -u dismistd` for logs.

For a manual launch, use the same explicit paths:

```sh
./dismistd -controller-ip 192.0.2.10 \
  -db /var/lib/dismist/controller.db \
  -ap-config /var/lib/dismist/ap-config.json \
  -tls-cert /var/lib/dismist/tls.crt \
  -tls-key /var/lib/dismist/tls.key \
  -admin-token-file /var/lib/dismist/admin.token
```

The program reports missing template and certificate paths at startup. An
unreadable or non-private AP secret prevents its registered AP from starting.
Use `-ap-id` and `-ap-ip` only to preserve a legacy AP record that lacks a
management address, and `-server-name` to change the AP authentication reply
name for a known firmware requirement.

## Backup and restore

Stop the service before copying `controller.db`. Back up that file together
with AP secret files, CA key and certificate, TLS key and certificate, AP config
template, admin token, and the initial import file if used. Keep copies
encrypted and access limited: the database stores WLAN PSKs and history, and
the secrets permit AP control.
To restore, stop the service, replace the files with owner `dismist` and
private permissions, then start the service. Restore the complete set so the
database's `secret_file` paths still resolve. Never run two server processes
against the same database.
