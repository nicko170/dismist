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
| `data/ap-config.json` | Bundled AP33 base config written by `dismist init` |
| `data/tls.crt`, `data/tls.key` | AP WSS leaf certificate and private key |
| `data/ca.crt`, `data/ca.key` | Local AP WSS certificate authority and private key |
| `data/admin.token` | Single-admin token created by the web gate |

Build and run `dismist init` before starting the daemon (or pass
`--data-dir DIR` for a different location).
Run it as the service user when using a system service. It generates a local
CA, an AP WSS certificate for `ep-terminator.mistsys.net`, and the admin token
using local randomness. It does not contact an AP or Mist cloud. Repeating
`init` verifies and reuses the existing files; it never replaces them. If a
certificate set is incomplete or invalid, inspect it and restore it from
backup before retrying. The generated WSS certificate lasts two years and
the CA lasts ten years; `init` does not renew either. Plan a replacement
and AP trust update before expiry.
Init also writes the bundled AP33 base configuration and prints a newly
created admin token once. If the token already exists, read it from the
private `data/admin.token` file.

Use absolute paths in a service unit. The database and token contain sensitive
data; restrict the data directory to the service user. `-db`, `-ap-config`,
`-tls-cert`, `-tls-key`, and `-admin-token-file` select other paths. The token
file is created with mode `0600` when absent; read it locally and enter its
64-character value in the UI login. The session is held in an HttpOnly cookie.
Operators may place HTTPS in front of the UI for access beyond a trusted LAN.
A proxy on the same host is trusted automatically. For one elsewhere, pass its
source addresses to `-trusted-proxies` (comma-separated CIDRs) so dismistd
accepts its `X-Forwarded-Proto: https` header. Otherwise sign-in is refused as
cross-origin.

dismistd checks `-tls-cert` and `-tls-key` every `-tls-check-interval`
(default one minute). AP authentication is bound to the certificate the AP
saw, so the daemon does not swap in a renewed certificate. When the files
hold a new valid pair, it shuts down cleanly and exits with status 75, and
its supervisor starts it again. The sample systemd unit restarts on failure,
which covers this. Set the interval to `0` to disable the check.

## AP preparation and initial state

`dismist init` writes a sanitized AP33 base configuration to
`data/ap-config.json`. The daemon also has an embedded copy if that default
file is absent. Operators set country, SSID, VLAN, and passphrase through site
setup; Dismist generates the AP's effective config. The shipped template has
no AP identity, site identifiers, credentials, fixed regional channel, or
transmit-power override. A custom `-ap-config` file remains available for a
different firmware/model or advanced deployment; Dismist will not silently
replace a custom file.

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

The first start may have zero APs. Sign in to the UI, create a site with its
country, then register APs and WLANs there. DNS refuses unregistered AP source
addresses. New AP records activate within a few seconds, without a server
restart. For a headless migration from an existing JSON state file, `-state`
can import it **once when creating a new database**; ordinary onboarding
does not need that file. See [AP onboarding](onboarding.md).

Point the AP management LAN's DNS setting at the controller IP. The
controller answers `ep-terminator.mistsys.net` only for registered AP source
addresses and forwards their other DNS queries. The AP then connects over
WSS. A routed network or NAT must preserve the source IP expected by the AP
record, or that record must use the translated source address.

## Running under systemd

[ops/dismistd.service](../ops/dismistd.service) is a sample unit. Create a
dedicated `dismist` user, place the binary at `/usr/local/bin/dismistd`, and
make `/var/lib/dismist` mode `0700` and owned by that user. Run
`dismist init --data-dir /var/lib/dismist` as that user. Init supplies the
AP33 template; place only per-AP secret files there with restrictive
permissions. Set `-controller-ip` in the unit only if address detection is
ambiguous; add `-state /var/lib/dismist/import.json` only for a one-time
headless import. The unit grants bind capability for ports below 1024. Start
it with `systemctl enable --now dismistd` after reviewing those values and AP
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

The program reports missing certificate paths at startup. An
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
