# Dismist self-hosted controller product design

## Purpose

**Dismist. Mist, dismissed.** A fully local controller for Juniper Mist access
points.

An operator runs one Go server for an office or set of sites. Registered APs
connect to its WSS endpoint, obtain shared site WLAN configuration with
optional AP overrides, and report clients and telemetry. Operators use the
embedded React UI from their LAN. bbolt is the system of record. There is no
organization layer and no dependency on Tailscale or Mist cloud at runtime.

## Operator workflows

- A site picker scopes the overview, WLANs, APs, clients, events, and history.
- The overview shows real AP/client counts, connected state, recent events,
  and time-series data already stored by the controller. Missing telemetry is
  labeled as unavailable; the UI does not synthesize application or RF data.
- AP inventory shows name, model, firmware, management IP, connection state,
  last seen, client count, and config revision. An operator can register an AP
  using its identity, management IP, and a private credential file already on
  the server, then move it between sites. The runtime activates new records
  without a restart.
- An AP is described as *Dismisted* only after it has connected to Dismist
  and applied its local configuration; registration alone is pending.
- AP detail shows radios, WLAN broadcasts, associated clients, status, and
  site/AP advanced configuration with a redacted effective preview.
- Client inventory and detail show association, signal, traffic, event and
  session history scoped to the selected site.
- Site WLANs are created and edited once, then applied to its APs. Typed
  SSID, PSK, VLAN and enabled fields sit alongside an advanced JSON patch.
- Site settings include name, country, timezone, and advanced defaults.

## Deployment

- The web UI listens on `0.0.0.0:8088` by default. The AP WSS and DNS
  listeners use standard `443` and `53` ports by default, with CLI overrides.
- An advertised controller address is explicit or derived from a usable
  network interface. Device identities, network addresses, and credentials
  come from explicit configuration and private files.
- Operators point the AP management LAN's DNS to the controller. It answers
  the AP endpoint hostname for registered AP addresses and forwards unrelated
  queries. The server does not change router DNS or NAT rules itself.
- A single-admin access token is stored in a private file and used to create
  HttpOnly browser sessions. LAN UI/API writes require authentication and
  same-origin requests. Operators may put HTTPS in front of the UI.
- The binary embeds the built React assets. Paths for bbolt, the AP config
  template, AP secret files, and the WSS certificate are explicit and
  documented. Startup errors identify missing inputs.

## Boundaries

- Tests use temporary stores and simulated APs.
- The screenshots guide information architecture, not branding or a pixel
  copy. The UI uses actual shadcn/ui components and a light neutral admin
  theme, with restrained accent color and standard tables, cards, dialogs,
  forms, and navigation.
- AP preparation is documented with its current requirements and limits.
- Existing bbolt data and working WLANs must survive migration and service
  restart.

## Acceptance

1. From another LAN device, an authenticated operator can use the UI at the
   server IP on port 8088 without a tailnet address.
2. Site/AP selection, AP moves, WLAN edits, and advanced patches persist in
   bbolt and affect only the intended site's APs.
3. Connected APs remain on their assigned sites and WLANs, with real status,
   client, and history data visible; empty data is explained honestly.
4. `npm run build`, `go test ./...`, and a production `go build` pass. The
   embedded UI and API work after a service restart.
5. Installation docs let a new operator configure listeners, DNS, AP trust,
   admin access, and data backup without developer-specific paths.
