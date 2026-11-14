# Bring an AP into Dismist

The short version: from a laptop on the AP's LAN, with root SSH to the AP
and an API token for the controller, run

```sh
dismist ap33 onboard root@192.0.2.10 --controller https://dismist.example.net \
  --token-file ~/.config/dismist/token --site Home --name Hallway
```

It backs the AP up, reads its secret, points it at the controller, registers
it and reboots it, then waits until it is connected and in sync. The rest of
this page explains what that does, the UI side (**Add an AP**), and what to
do when an AP does not connect. Getting root on an AP in the first place
depends on the vendor and firmware: for AP33 see the
[Juniper AP33 walkthrough](juniper/AP33.md).

## How Dismist identifies an AP

Dismist identifies an AP by the **ID in its authenticated `ep-auth` hello**,
not by the address it connects from. The ID is the AP's base MAC address in
dashed form, for example `02-aa-00-00-00-01`. Each connection is matched to
the AP record with that ID and must then prove it holds that AP's own 32-byte
secret. Only after that does it become the AP's session, so an
unauthenticated client that sends another AP's ID cannot take its place.

Because the source address plays no part in identity, any number of APs can
reach the controller from the same address. Two APs behind one home NAT both
arrive from the router's public address and are told apart by their IDs. The
UI shows each AP's own LAN address (`lan_ip`, reported by the AP) and the
address its session comes from (`remote_address`, the NAT's public address
for an AP behind one).

An AP record can optionally carry a **pinned source IP**. When it is set,
Dismist only accepts that AP from that address. Leave it empty unless you use
DNS mode (below), where it is required.

## How the AP finds the controller

The AP connects to whatever WSS URL its `cloud.ini` names. There are two ways
to point it at Dismist:

1. **Custom endpoint (recommended).** The AP's `/rw` copy of `cloud.ini`
   names the controller's own hostname, for example
   `wss://ap.example.net/ws`, in both `[[cloud]]` `url` keys. The AP's CA
   bundle must contain the roots that issue the certificate the endpoint
   serves: the public roots for a public certificate (for Let's Encrypt, the
   ISRG roots), or `data/ca.crt` for the certificate `dismist init` creates.
   Dismist's DNS server is not used and no pin is needed. The Helm chart
   deployment works this way.
2. **DNS mode (stock endpoint).** The AP keeps the stock
   `ep-terminator.mistsys.net` name, the AP management LAN uses dismistd as
   its DNS server, and dismistd answers that name with its own address. It
   answers **only to pinned source addresses**, so in DNS mode every AP
   record needs a pinned source IP: the address dismistd sees the AP's DNS
   queries and WSS connection come from (after any NAT). An AP registered
   without a pin in DNS mode never resolves the controller and never
   connects. Several APs may share one pinned address. dismistd forwards the
   AP's other DNS questions to `-upstream-dns` and refuses DNS from
   addresses that are not pinned.

## Before you start

- **Root SSH to the AP** from a machine on its LAN (the controller, for
  example in a Kubernetes cluster, usually cannot reach home LAN addresses,
  which is why onboarding runs from a laptop). Check it with the read-only
  inspection, which also shows whether the AP has `local_*` override files:

  ```sh
  dismist ap33 inspect root@192.0.2.10
  ```

- **An API token with admin scope.** In the UI open System › API tokens,
  create one, and save it on one line in a private file:

  ```sh
  umask 077; mkdir -p ~/.config/dismist
  printf '%s\n' 'dsm_…' > ~/.config/dismist/token
  ```

  The admin sign-in token is not accepted here; the CLI says so if you try.
- **A site** to put the AP in (Home by default in the UI's command builder).
- The controller must know its public AP endpoint (`-ap-url`, or the Helm
  chart's AP hostname): `GET /api/onboarding` returns it with the CA
  certificates the AP needs.

## Onboard with the CLI

`dismist ap33 onboard` prints one line per step. Everything that can fail
without touching the AP is checked first; a failure before step 7 leaves
the AP exactly as it was.

1. **Controller.** Checks the token (admin scope), reads the AP endpoint and
   CA certificates from `GET /api/onboarding` and finds the site by ID or
   name. In DNS mode it insists on `--pin-ip`.
2. **Preflight** over SSH: root (`id -u` is 0), firmware from
   `/etc/mist/version` (FIPS images are refused), boot config, root volume,
   the AP ID from `eth0`, and exactly one 32,768-byte EEPROM. The host key is
   trusted on first use.
3. **Local files.** Lists `/rw/etc/mist/ep-control/local_*` with sizes and
   SHA-256. They change what the AP runs, so the command asks before going on
   (exit 6 if nobody confirms); `--yes` keeps them, `--remove-local-files`
   moves them aside on the AP (never deletes them).
4. **Trust plan.** Builds the new `cloud.ini` from the firmware's own stock
   file (both `[[cloud]]` URLs set to the endpoint, `rootcerts` pointed at
   `/rw/etc/mist/ep-control/cacerts-production.pem`, TLS verification kept)
   and the new CA bundle from the bundle the AP uses now plus the
   controller's CAs. With a Let's Encrypt certificate it also appends Let's
   Encrypt's four roots (ISRG Root X1 and X2, Root YE, Root YR; embedded and
   checked against their published fingerprints), so the AP keeps
   connecting when Let's Encrypt rotates its chain.
5. **TLS check.** Connects to the endpoint and verifies the chain it really
   serves against the new bundle alone, as the AP will. A mismatch stops here
   (exit 3), nothing changed.
6. **Backup** of `/rw` (a tarball) and the full EEPROM to
   `~/.local/share/dismist/backups/<id>/<UTC time>/`, mode 0600, with a
   manifest and `SHA256SUMS`, plus the AP's host key next to its backups.
   The backup holds the AP's secret: keep it private and out of any Git
   checkout.
7. **Secret.** Takes the 32 bytes at offset `0x400` of the EEPROM image, in
   memory only; it is never printed or written anywhere else.
8. **Install.** Writes the CA bundle first, then `cloud.ini` (each through a
   temporary file and `mv`, then compared by SHA-256 on the AP). An
   interrupted run leaves the AP on its old endpoint with a superset of its
   old trust.
9. **Register** through the API: `GET /api/aps/<id>`; on 404 `POST /api/aps`
   with the name, site, secret and the local-file list (names and hashes
   only); otherwise `PATCH /api/aps/<id>` with `If-Match` from that GET (a
   409 repeats the GET once). Because the AP is registered before it
   reboots, its first hello authenticates at once.
10. **Reboot and wait.** Reboots the AP (recording the reason, so its next
    hello does not report an unexplained reboot) and polls the controller
    until the AP is connected on a new session with its applied revision
    equal to its config revision, then prints model, firmware, LAN address
    and public address. An AP the controller sees connected is serving
    clients, so rebooting it needs `--yes` or a yes on the terminal.

Re-running on an onboarded AP checks the files, refreshes the registration
and skips the reboot when nothing changed. `--dry-run` runs steps 1 to 5 and
prints the plan without backing up, reading the secret or writing anything.

| Option | Meaning |
| --- | --- |
| `--controller URL` | The controller, `https://HOST[:PORT]` (plain `http` only to a loopback address). |
| `--token-file FILE` | The admin-scope API token. |
| `--site NAME\|ID`, `--name NAME` | Where to register the AP (default `Home`) and what to call it. |
| `--pin-ip` | Pin the AP to the address SSH reached it at (required in DNS mode only). |
| `--backup-dir DIR` | Backup root (default `~/.local/share/dismist/backups`). |
| `--no-reboot` | Install and register but leave the reboot to you. |
| `--remove-local-files` | Move `local_*` files aside on the AP. |
| `--ca FILE` | Extra CA certificates for the bundle (repeatable). |
| `--public-roots auto\|letsencrypt\|none` | Add Let's Encrypt's roots when the served chain is theirs (`auto`), always, or never. |
| `--timeout 5m` | How long to wait for the AP to come back in sync. |
| `--dry-run`, `--yes` | Plan only; answer yes to the questions. |

Exit codes: 0 done, 2 preflight failed (or a usage error), 3 TLS chain
mismatch, 4 API error, 5 timed out waiting for the AP, 6 local config files
present and not confirmed.

**Move an AP to another controller** (or after the endpoint's name
changes): `dismist ap33 onboard --retarget root@<ap> --controller … --token-file …`
rewrites `cloud.ini` and the bundle only; the AP must already be registered
on that controller.

**Undo**: `dismist ap33 restore root@<ap> --backup ~/.local/share/dismist/backups/<id>/<time>`
checks the backup against its `SHA256SUMS` and that it belongs to this AP,
puts back `cloud.ini`, the CA bundle and any `local_*` files as they were,
moves files that did not exist then into `/rw/etc/mist/dismist-restore/`
(nothing is deleted), and reboots (`--no-reboot`, `--dry-run`, `--yes`). It
never writes the EEPROM. The controller's record is left alone; delete it in
the UI if the AP is leaving.

**Reboot from the LAN**: `dismist ap33 ssh root@<ap> reboot --yes`. It writes
`<time>::reboot/<reason>` to `/rw/etc/mist/ep-control/last_reboot_info.txt`
first, which the AP reports as its last reboot reason on its next
connection (change it with `--reason`, skip it with `--no-reason`; an
earlier reason that the AP has not reported yet is left in place).
`dismist ap33 ssh root@<ap> status` prints a short status.

## Onboard from the UI

**Add an AP** (in the sidebar, or More on a phone) has four cards:

1. **Waiting to be adopted.** APs that said hello but are not served: new
   IDs, and registered APs whose attempts fail authentication or come from
   the wrong pinned address. Each row shows the ID, model, serial, firmware,
   the LAN address the AP reports and the public address it connects from.
   Two APs behind one NAT share the public address; the LAN address and
   serial tell them apart. **Adopt** opens the Register form with the ID
   filled in; **Dismiss** hides a row for 30 days (Show dismissed brings it
   back).
2. **Prepare an AP with the CLI.** Pick the site, type the AP's name and LAN
   address, and copy the exact `dismist ap33 onboard` command. If there is
   no admin-scope API token yet, it links to System › API tokens.
3. **Register manually**: the form below.
4. **Trust material**: the WSS URL, a **Download CA bundle** button
   (`GET /api/onboarding/ca-bundle.pem`) and the `cloud.ini` the CLI would
   install, for preparing an AP by hand
   ([appendix in the AP33 guide](juniper/AP33.md#appendix-manual-preparation)).

The **Register** form takes the AP ID in any MAC spelling (it shows the
dashed form it will save), a name, the site, the secret (paste hex or
base64, or upload `secret.bin`; it must be exactly 32 bytes) and an optional
pinned source IP (required only in DNS mode). There is no model field: the
AP reports it. After **Register** the page becomes the **Verify** panel
(`/onboard/register?step=verify&id=<id>`, so a refresh or a shared link
keeps it), which follows the AP live: waiting for hello → authenticated
(model, firmware) → config delivered → config applied → broadcasting the
site's WLANs, or the error that stops it. Authentication failures mean the
stored secret is not this AP's. TLS failures never reach the controller, so
after three minutes without a hello the panel points at the AP's `cloud.ini`
and CA bundle.

## If the AP does not connect

- Run `dismist ap33 inspect` again: the cloud URL must be the endpoint you
  expect, the CA bundle must list the roots that issue the endpoint's
  certificate (`dismist ap33 check-trust --url wss://… --bundle FILE`
  verifies a bundle against the live chain), and the ep-control log tail
  shows TLS and authentication errors.
- The AP appears under **Waiting to be adopted** with reason *Not
  registered*: register that ID, or fix a typo in the one you registered.
  *Source mismatch*: the AP's pinned source IP does not match; correct or
  clear the pin. *Auth failed*: the stored secret does not match the AP's
  EEPROM; upload it again from that AP's backup.
- dismistd logs `AP <id> authenticated from <address>` for each AP that
  authenticates, and `unregistered AP "<id>" ... connected from <address>`
  (at most once per ID per interval) for an ID it has no record for.
- In DNS mode, check the pin first: without one, the AP cannot resolve the
  controller.

## A second AP behind the same NAT

Nothing special is needed with a custom endpoint: both APs reach the
endpoint from the same public address and Dismist tells them apart by their
authenticated IDs. Onboard the second AP into the same site with the CLI and
leave both pins empty. In DNS mode, pin both APs; they may share the same
pinned address.

The second AP takes the site's radio settings. Until the channel planner
assigns channels (RF › Plan), a site whose radio settings name static
channels puts both APs on the same channels; plan, or give the second AP its
own channels, once it is connected.

## APs still claimed in Mist

An AP on 0.14 or 0.15 that is still claimed in a Mist organisation has no
root login of its own. The route usually suggested is to add the operator's
public key to the Mist site's `ssh_keys`, wait for the AP to take the
config, then `ssh root@<ap>` and run `dismist ap33 onboard`. **This is
unverified:** on the AP33s examined so far the Mist site `ssh_keys` were not
observed to grant root (design question Q16). Until someone confirms it on a
claimed AP (and records it in [verification](verification.md)), treat it as
a hypothesis, not a procedure. Release the AP from the Mist inventory only
after `dismist ap33 onboard` has finished. Unclaimed units use the 0.8
serial path in the [AP33 guide](juniper/AP33.md).

## Where secrets live

AP secrets are kept in the controller's bbolt database (`controller.db`,
mode `0600`), next to the WLAN passphrases it already holds. The API takes a
secret on registration and edit, never returns it, and reports only
`has_secret`. Leaving the secret blank when editing an AP keeps the stored
one. Treat the database and every backup of it as secret: together they
allow control of your APs and reveal your WLAN passphrases.

Records from older releases that still name a `secret_file` are migrated at
startup: dismistd imports the file into the database and clears the path. A
file that is missing, unreadable or not private is logged and left alone;
upload that AP's secret again from its EEPROM backup. An AP without a secret
is skipped with a log line and does not stop the other APs from being
served.

Keep the AP's original `/rw` backup, EEPROM backup and `secret.bin` outside
any Git checkout (the ignored `data/` directory is fine) or offline.

## Local override files

Files named `local_*` in `/rw/etc/mist/ep-control/` are merged by the AP's
ep-control into whatever config the controller sends (`local_defaults.config`
before it, the others after it), so they change what the AP runs. `inspect`
lists each one with its size, SHA-256 and key paths (never the values);
`onboard` reports them to the controller (names and hashes, shown on the
AP's page) and asks before continuing. An AP with local files cannot be
proven to run the config Dismist shows, so the schema-2 migration refuses to
claim equivalence for it until they are dealt with.

## SSH options

Every `dismist ap33` command that talks to an AP (`inspect`, `ssh`,
`onboard`, `restore`) uses its own SSH client, so you do not need OpenSSH
options for older AP firmware:

- The user defaults to `root`. Destinations are `[user@]host` or
  `ssh://[user@]host[:port]`.
- Keys come from `-i FILE` (repeatable), then `ssh-agent`, then
  `~/.ssh/id_ed25519`, `id_ecdsa` and `id_rsa` when no `-i` is given.
  Passphrase-protected keys must be loaded into the agent.
- The AP33 0.8 recovery image runs dropbear 2017.75, which only accepts RSA
  or ECDSA keys and SHA-1 `ssh-rsa` signatures. Dismist allows that SHA-1
  fallback (and the `ssh-rsa` host key and `diffie-hellman-group14-sha1`)
  by default; 0.14 and 0.15 negotiate modern algorithms.
- Host keys are trusted on first use and recorded in
  `~/.config/dismist/known_hosts` (or under `$XDG_CONFIG_HOME`). A changed
  key is refused. A factory reset regenerates the AP's host key; confirm the
  new one, then delete the old line.
- `-o Key=Value` takes the ssh_config(5) keywords `User`, `HostName`,
  `Port`, `IdentityFile`, `IdentitiesOnly`, `IdentityAgent`,
  `UserKnownHostsFile`, `StrictHostKeyChecking` (`yes`, `accept-new`, `no`),
  `ConnectTimeout`, `BatchMode`, `HostKeyAlgorithms`,
  `PubkeyAcceptedAlgorithms`, `KexAlgorithms`, `Ciphers` and `MACs`
  (algorithm lists accept OpenSSH's `+`, `-` and `^` forms). As in ssh(1),
  the first value given wins. `-p`, `-l` and `--known-hosts` are shorthands.
- `-F FILE` reads `Host` blocks from an ssh_config file. `~/.ssh/config` is
  not read unless you name it; unsupported keywords in it (such as
  `ProxyJump`) are skipped with a warning.

`dismist ap33 ssh root@192.0.2.101 status` prints a short status (ID,
firmware, uptime, boot count, slot, ep-control, cloud URL, local files).
`dismist ap33 ssh root@192.0.2.101 reboot --yes` reboots the AP. Before
rebooting it writes `<time>::reboot/<reason>` to
`/rw/etc/mist/ep-control/last_reboot_info.txt`, which the AP reports as its
last reboot reason on its next connection, so an operator reboot is not
mistaken for an unexplained one. It leaves an existing, not yet reported
reason in place. Change the text with `--reason`, or skip it with
`--no-reason`.

## Vendor preparation

- [Juniper AP33 onboarding](juniper/AP33.md)

An AP's identity, secret and trust material must be prepared for that
specific unit before its WSS connection can begin. Do not copy credentials or
CA overlays from another AP. If its vendor or firmware has no verified offline
preparation path, stop before redirecting its DNS or resetting it.
