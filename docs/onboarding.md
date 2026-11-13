# Bring an AP into Dismist

This guide covers an AP that has already been prepared to trust Dismist.
Factory-offline preparation depends on the vendor and firmware. Follow a
vendor-specific guide for the device before attempting registration.

## What the server needs

For each AP, prepare these inputs on the server:

1. Its real controller ID (the MAC address in dashed form, for example
   `aa-bb-cc-00-00-01`) and the management **source IP seen by this server**.
2. Its own 32-byte authentication secret in a mode `0600` file. Each AP has
   a different secret. The claim QR/code and a Mist site SSH key do not
   contain or install it.
3. A full firmware-compatible AP config template, including the WLAN
   template. The controller changes site WLANs and typed identity fields on
   top of it, and preserves unknown advanced fields.
4. A WSS certificate/key for the endpoint hostname the AP uses, and a CA
   that the AP has been prepared to trust. A stock AP's Mist-only endpoint
   trust store will reject a fresh local or public-CA certificate.

Keep secrets, private keys, config templates with sensitive values, and the
bbolt database outside the Git checkout or in its ignored private `data/`
directory. Back up the AP's original `/rw` and EEPROM before preparation.
The controller's admin token protects the operator UI; it is separate from
the per-AP authentication secret.

## Register and configure

1. Start the server with the paths, listener addresses, and advertised IP in
   [deployment.md](deployment.md). Read `data/admin.token` locally and sign
   in to the LAN UI.
2. Create a site with its name, country, and timezone. Sites have no parent
   organization. A site may contain multiple APs.
3. In **Access Points**, register the AP using its ID, management IP, model,
   site, and absolute path to its private secret file on the server. The UI
   and list API do not return the secret file path or bytes. APs may later be
   moved between sites; the new site's WLANs and defaults apply to that AP.
4. In **Wireless Networks**, add the SSID, client VLAN, WPA2 passphrase, and
   enabled state. The WLAN is shared by the site's APs. Use the advanced
   editor only for firmware fields you understand; preview the effective
   config before applying.
5. Configure the AP management LAN's DNS to use this server and permit APs
   to reach it on TCP 443 and UDP/TCP 53. The controller returns its own
   advertised IP for `ep-terminator.mistsys.net` to registered AP source
   addresses and forwards their other DNS queries. The server does not edit
   the router or DHCP server. A client WLAN's VLAN and DHCP service remain
   your network's responsibility.
6. Once the prepared AP connects, check its connection, applied revision,
   WLAN broadcasts, associated clients, and events in the UI. An AP that
   shows a desired revision above its applied revision still needs to apply
   its config; do not infer success from the saved WLAN alone. The AP is
   *Dismisted* when it connects and applies its local configuration.
7. In **Site Settings**, paste any operator SSH *public* keys to push them to
   all authenticated APs in that site. A site key does not install the local
   CA or reveal the AP's EEPROM secret, so it cannot replace preparation.

## Vendor preparation

- [Juniper AP33 onboarding](../juniper/AP33.md)

An AP's identity, secret, and trust material must be prepared for that
specific unit before its WSS connection can begin. Do not copy credentials
or CA overlays from another AP. If its vendor/firmware has no verified
offline preparation path, stop before redirecting its DNS or resetting it.
