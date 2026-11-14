# Bring an AP into Dismist

This guide covers an AP that has already been prepared to trust Dismist.
Preparing a factory AP depends on the vendor and firmware, so follow the
vendor guide for the device first (for AP33, the
[Juniper AP33 walkthrough](juniper/AP33.md)).

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

## What the server needs for each AP

1. Its **ID** (base MAC). The Register form accepts `aa:bb:cc:dd:ee:ff`,
   `AA-BB-CC-DD-EE-FF` or `aabbccddeeff`.
2. Its own **32-byte authentication secret**, read from that AP's EEPROM.
   Each AP has a different secret; a claim code, QR code or SSH key does not
   contain it, and a random replacement will not authenticate the AP. You
   upload it when you register the AP: paste it as hex or base64, or upload
   the `secret.bin` file. It is stored in the controller database.
3. A **base config** for its model. Dismist bundles one for AP33 and
   `dismist init` writes it. Other models need a validated template.
4. **Trust material**: a WSS certificate for the hostname the AP uses, issued
   by a CA in the AP's bundle (see above). A stock AP's Mist-only bundle
   rejects any other certificate.

The model and firmware come from the AP itself once it authenticates; you do
not enter them.

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

## Before you start: inspect the AP

From a machine on the AP's LAN with root SSH access to it, run the read-only
inspection:

```sh
dismist ap33 inspect root@192.0.2.101
```

It prints the AP ID, firmware, uptime, boot count and boot slot, the last
reboot reason, the `cloud.ini` URLs and CA bundle subjects, and the tail of
the ep-control, system and kernel logs (with known secret fields
redacted). It writes nothing to the AP. Add `--json` for a machine-readable
report. Treat the output as private: it contains the AP's MAC and addresses.

Check the **Local files** line. Files named `local_*` in
`/rw/etc/mist/ep-control/` are merged by the AP's ep-control into whatever
config the controller sends (`local_defaults.config` before it, the others
after it), so they change what the AP runs. inspect lists each one with its
size, SHA-256 and key paths (never the values). If an AP has any, back them
up with the rest of `/rw` before you change anything, and expect the AP's
behaviour to differ from the config Dismist shows.

`dismist ap33 inspect` speaks SSH itself: see [SSH options](#ssh-options).

## Register and configure

1. Start the server with the paths, listener addresses and advertised IP in
   [deployment.md](deployment.md). Read `data/admin.token` locally and sign
   in to the UI.
2. Create a site with its name, country and timezone. The site country is
   sent to AP33s for regulatory configuration. Sites have no parent
   organization, and a site may contain several APs.
3. In **Access Points**, register the AP with its ID, a name, its site and
   its secret (paste the hex or base64, or upload `secret.bin`). Leave
   **Pinned source IP** (labelled *Management IP* in the legacy UI) empty
   for a custom endpoint; in DNS mode enter the address the controller sees. The UI and API never return the secret. APs
   can later move between sites; the new site's WLANs and defaults then
   apply to that AP.
4. In **Wireless Networks**, add the SSID, client VLAN, WPA2 passphrase and
   enabled state. A WLAN is shared by all of the site's APs. Use the
   advanced editor only for firmware fields you understand, and preview the
   effective config before applying.
5. Network: with a custom endpoint, the AP only needs to reach the endpoint
   on TCP 443; leave its DNS alone. In DNS mode, set the AP management LAN's
   DNS server to the controller and allow TCP 443 and UDP/TCP 53 to it.
   Dismist does not edit your router or DHCP server; the client WLAN's VLAN
   and DHCP service remain your network's responsibility.
6. Once the prepared AP connects, check that it shows **Connected**, that
   its applied revision equals its config revision, and that its WLANs
   broadcast and clients associate. An AP whose config revision is above its
   applied revision has not applied its config yet; a saved WLAN alone does
   not prove success.
7. In **Site Settings**, paste operator SSH *public* keys to push them to
   every authenticated AP in the site. A site key does not install trust
   material or reveal the AP's EEPROM secret, so it cannot replace
   preparation.

### If the AP does not connect

- Run `dismist ap33 inspect` again: the cloud URL must be the endpoint you
  expect, the CA bundle must list the roots that issue the endpoint's
  certificate, and the ep-control log tail shows TLS and authentication
  errors.
- dismistd logs `AP <id> authenticated from <address>` for each AP that
  authenticates. For a hello naming an ID it has no record for, it logs
  `unregistered AP "<id>" ... connected from <address>` (at most once per
  ID per interval): register that ID, or fix a typo in the one you
  registered. `... which isn't its registered source address` means the
  AP's pinned source IP does not match; correct or clear the pin.
- If the ID is registered but no `authenticated` line appears, the AP failed
  TLS or authentication. Check the cloud URL and CA bundle with inspect; if
  they are right, the stored secret does not match the AP's EEPROM, so
  upload it again from that AP's backup.
- In DNS mode, check the pin first: without one, the AP cannot resolve the
  controller.

## A second AP behind the same NAT

Nothing special is needed with a custom endpoint: both APs reach the
endpoint from the same public address and Dismist tells them apart by their
authenticated IDs. Register each with its own ID and secret and leave both
pins empty. In DNS mode, pin both APs; they may share the same pinned
address.

## SSH options

`dismist ap33 inspect` and `dismist ap33 ssh` use their own SSH client, so
you do not need OpenSSH options for older AP firmware:

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
