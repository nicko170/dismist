# Kubernetes deployment

Each release publishes a container image and a Helm chart to GHCR:

- `ghcr.io/nicko170/dismist:<version>` (linux/amd64 and linux/arm64)
- `oci://ghcr.io/nicko170/charts/dismist` (chart version = release version)

The chart runs one `dismistd` process with its embedded UI. It is a single
controller, not a high-availability deployment: one replica, `Recreate`
upgrades, and a `ReadWriteOnce` volume for the bbolt database. Never scale it
above one. See [values.yaml](../charts/dismist/values.yaml) for every option.

## How APs reach the controller

APs open a WSS connection and authenticate with their EEPROM secret. That
authentication is bound to the TLS certificate the AP sees and the cipher
suite it negotiated, so **TLS must end at dismistd**. Do not put the AP
endpoint behind an ingress, Gateway or other TLS-terminating proxy.

Dismist identifies each AP by the ID it authenticates with, not by its
source address, so any number of APs can sit behind one NAT address. The
source address still matters in three places: dismist shows it as the AP's
public address, an AP record can pin it (its management IP) so the AP is
accepted only from there, and dismist's AP DNS answers only registered
management addresses. Keep the path free of source NAT where you use a pin
or AP DNS:

- **LoadBalancer (default).** The `<release>-ap` Service exposes WSS on TCP
  443 with `externalTrafficPolicy: Local`, which keeps each AP's real source
  address.
- **hostNetwork.** Set `hostNetwork: true` to bind on a dedicated node
  instead, as on a bare server. The daemon runs unprivileged, so ports 443
  and 53 (`listen.wss`, `listen.dns`) need the node's
  `net.ipv4.ip_unprivileged_port_start` lowered. Otherwise keep the default
  ports and put the port in the AP's URL (`wss://host:8443/ws`).

Point the AP at the endpoint in one of two ways:

1. **Your own hostname (recommended).** Give the endpoint a DNS name with a
   public certificate. The chart can request one from cert-manager
   (`tls.certificate`), and dismistd restarts itself when cert-manager renews
   it. On the AP, set both `[[cloud]]` `url` values in
   `/rw/etc/mist/ep-control/cloud.ini` to `wss://<hostname>/ws`, and append
   the issuing root (for Let's Encrypt, ISRG Root X1 and X2) to the
   `rootcerts` bundle. Nothing else on the AP's network needs to change, and
   dismist's AP DNS is not used.
2. **The stock endpoint name.** Keep `wss://ep-terminator.mistsys.net/ws`,
   set `apService.exposeDNS: true` and `controller.advertisedIP` to the
   endpoint's address, and give the AP management network that address as its
   DNS server. The AP must trust a CA that issued a certificate for
   `ep-terminator.mistsys.net`. See [AP onboarding](onboarding.md).

Either way, the AP needs its own 32-byte EEPROM secret on the controller and a
prepared trust store. DNS or URL changes alone do not prepare a factory AP.

## Prepare the private Secret

Run `dismist init --data-dir DIR` in a private directory outside the Git
checkout. It creates an admin token, plus a local CA and certificate for the
stock endpoint name. Create one Secret holding the admin token. Include
`tls.crt` and `tls.key` only if the certificate does not come from
`tls.secretName` or `tls.certificate`:

```sh
kubectl create namespace dismist
kubectl -n dismist create secret generic dismist-private \
  --from-file=admin.token=DIR/admin.token
```

An init container copies the Secret into a memory volume at `/private`,
owned by the daemon user with mode `0600`, because dismistd rejects readable
secrets. Restart the pod after changing the Secret.

AP EEPROM secrets do not go in this Secret: they are stored in the database.
Paste each AP's 32-byte secret (hex or base64) or upload its `secret.bin`
when you register the AP in the UI or API. An AP record from an older release
that still points at a secret file (`/private/<name>.bin`) has that file
imported into the database once at startup, so keep the key in the Secret
until that has happened.

## Install

```sh
helm install dismist oci://ghcr.io/nicko170/charts/dismist --version VERSION \
  -n dismist -f values.yaml
```

A typical `values.yaml` with a cert-manager certificate, a pinned
load-balancer address, and the UI on a Gateway API listener:

```yaml
privateSecret:
  name: dismist-private
tls:
  certificate:
    enabled: true
    dnsNames: [ap.dismist.example.com]
    issuerRef: {kind: ClusterIssuer, name: letsencrypt-dns01}
apService:
  annotations:
    lbipam.cilium.io/ips: 192.0.2.10
controller:
  uiHost: dismist.example.com
  # The Gateway's source range, so sign-in works behind HTTPS.
  trustedProxies: [10.244.0.0/16]
httpRoute:
  enabled: true
  parentRefs: [{name: public, namespace: gateway, sectionName: https-dismist}]
  hostnames: [dismist.example.com]
```

## The operator UI

The UI is plain HTTP on the `<release>` ClusterIP Service, port 8088. Publish
it through an HTTPS proxy: `httpRoute` adds a Gateway API route. Set
`controller.uiHost` to the hostname operators use. Set
`controller.trustedProxies` to the proxy's source range: dismistd trusts
`X-Forwarded-Proto` only from those addresses or loopback. Without it,
sign-in through an HTTPS proxy is refused as cross-origin. The UI's live
updates are server-sent events that stay open for up to 30 minutes. The
route's request timeout defaults to `0s` (disabled) so the proxy doesn't cut
them off.

## Monitoring

dismistd serves Prometheus metrics at `/metrics` on its own port (9090). By
default it listens on the pod's loopback only, so nothing outside the pod can
read it (`kubectl port-forward deploy/<release> 9090` still can). Labels
carry site, AP and network IDs only: no names, addresses, SSIDs, host names,
client MACs or secrets, unless you opt in (below).

What it exports (the full catalogue is in the design, §6.3):

- **Controller:** build (`dismist_build_info`), the AP certificate's expiry,
  database size, free space, schema and migration state, backups, AP
  connection routing (including unknown APs), TLS handshake failures, AP DNS
  answers, operator UI requests and event streams, and Go and process
  metrics.
- **Per AP, always:** registered, connected and since when, last message,
  config revisions and timestamps, config errors and compile errors,
  protocol features, boots, disconnects, config deliveries and pushes,
  authentication failures, frames by subject, telemetry decode errors.
- **Per AP, while it reports:** CPU, load, memory, `/rw`, temperature,
  round trip to dismist, the telemetry agent's memory; uplink link, speed,
  duplex, traffic and errors; PoE (LLDP and the power manager); inactive
  VLANs; per radio its channel, width, noise, power, utilisation by type,
  DFS, clients and counters; per WLAN its clients, multicast and wrong
  passphrase attempts; per radio the clients by signal and protocol, the
  weakest and mean signal and the current throughput. These series stop as
  soon as the AP disconnects or misses three telemetry intervals, so a gap
  means no data rather than a stale value.

A fully reporting AP is about 150 to 250 series. `metrics.clients` controls
client data: `off` (counts only), `aggregate` (the default, no MACs) or
`per-client` (about 8 more series per client, labelled with its MAC, capped
by `metrics.clientLimit`). `metrics.includeNames` adds site, AP and WLAN
names (the SSID) to the `*_info` series, which the dashboards join to show
names instead of IDs, plus LLDP neighbours, the served certificate's name
and, in per-client mode, client host names. Prometheus keeps all of it for
its retention period.

`monitoring.enabled: true` ships everything for a cluster running the
Prometheus Operator and the Grafana dashboard sidecar:

- a `<release>-metrics` ClusterIP Service, and dismistd listening on
  `:9090` (`-metrics-listen=:9090 -metrics-allow-remote`);
- a ServiceMonitor scraping it every 30 s, with `instance` set to the
  release name so AP series continue across pod restarts;
- a PrometheusRule with the alerts in
  [monitoring/rules.yaml](../charts/dismist/monitoring/rules.yaml):
  warning and info only, covering disconnected, silent, flapping and
  rebooting APs, config not applied, drifting, rejected, failing to compile
  or not verifying, push fall-backs, authentication failures, unknown APs,
  TLS rejections, temperature, memory, `/rw`, uplink speed and errors, PoE,
  inactive VLANs, round trip, clock skew, missing or silent radios,
  congested or noisy channels, radar, radios off their planned channel,
  wrong passphrases, the AP certificate, decode errors, database write
  errors, stale backups and a migration left in legacy mode;
- a ConfigMap of Grafana dashboards, labelled `grafana_dashboard: "1"` with
  a `grafana_folder` annotation: **Dismist / Overview**, **Dismist / Access
  point** (one row per radio) and **Dismist / Clients & RF** (channel plan,
  interference, client quality, and a per-client row for per-client mode).

Each part has its own `enabled` under `metrics.*`: `null` follows
`monitoring.enabled`, `true` or `false` overrides it. For example, to scrape
and get dashboards but keep the alerts off until you have decided where they
should go:

```yaml
monitoring:
  enabled: true
metrics:
  includeNames: true          # names on *_info series, so dashboards show names
  serviceMonitor:
    sampleLimit: 20000        # a backstop: a runaway scrape fails instead
  prometheusRule:
    enabled: false
  dashboards:
    folder: Wi-Fi
```

Turn the rules on only once Alertmanager routes `namespace="dismist"`
somewhere you want (a personal receiver, or `null`): an AP that reboots on
its own fires `DismistAPRebooted` and `DismistAPFlapping` straight away.
Alerts can be dropped or re-graded per install with
`metrics.prometheusRule.disabledAlerts` and `severityOverrides`.

A Prometheus that selects every ServiceMonitor and PrometheusRule (the
kube-prometheus-stack `*SelectorNilUsesHelmValues: false` setting) needs no
labels. If yours selects by label, add it under
`metrics.serviceMonitor.labels` and `metrics.prometheusRule.labels`, for
example `release: <kube-prometheus-stack release>`. The Grafana sidecar must
watch the ConfigMap's namespace (`metrics.dashboards.namespace`, default the
release namespace). The dashboards use a `datasource` variable, so they work
with any Prometheus data source.

Once exposed, any pod in the cluster can read `/metrics`. To limit that,
either require a bearer token:

```sh
kubectl -n dismist create secret generic dismist-metrics \
  --from-literal=token="$(openssl rand -hex 32)"
```

```yaml
metrics:
  auth:
    existingSecret: dismist-metrics
```

(dismistd reads it at startup, so restart the pod after changing it; the
ServiceMonitor sends it), or turn on `metrics.networkPolicy`, which leaves the
UI, WSS and DNS ports open and allows the metrics port only from the
Prometheus pods (`metrics.networkPolicy.from`). With `hostNetwork: true`,
exposing metrics would publish them on the node's addresses, so the chart
refuses unless `metrics.allowHostNetwork` is set. For a Prometheus without
the Operator, `metrics.service.enabled` gives it a Service to scrape and
`metrics.scrapeAnnotations` adds `prometheus.io/*` pod annotations.
`controller.metricsListen` is deprecated: it still overrides the loopback
address while metrics are not exposed. No sidecar is needed for any of this.

Checks after enabling monitoring:

```sh
kubectl -n dismist get servicemonitor,configmap -l grafana_dashboard=1
kubectl -n dismist port-forward deploy/dismist 9090 &
curl -s 127.0.0.1:9090/metrics | grep -c '^dismist_'   # well under 1000 for a home
```

In Prometheus, `up{namespace="dismist"}` is 1 and the target
`serviceMonitor/dismist/dismist/0` is up; Grafana shows the three dashboards
in the folder you chose.

## Operations

The startup and liveness probes use `GET /healthz` on the UI port, which
answers as soon as the UI listener is up. The startup probe allows 5 minutes
(`probes.startupFailureThreshold` × 5 s), enough for a database migration on
first start. The readiness probe uses `GET /readyz`, which answers 200 once
the database reads and writes, the AP WSS listener is bound and any
migration has finished; until then the AP Service sends APs nowhere. The
database write behind `/readyz` runs at most every 10 seconds, and the probe
times out after `probes.readinessTimeoutSeconds` (5). Neither probe tests AP
authentication, so check AP connections in the UI after a change.

The root filesystem is read-only. An `emptyDir` at `/tmp`
(`backup.tmpSizeLimit`, 1 GiB by default) holds the copy of the database a
backup download streams from; keep it larger than `controller.db`. dismistd
gets the limit as `-backup-tmp-max` and refuses (507) a download that would
not fit, because going over it evicts the pod.

Scheduled snapshots go to `/state/backups` on the database volume:
`backup.interval` (`24h`; `""` turns them off), `backup.keep` (7) and
`backup.maxBytes` (`""`: 30 % of the volume). A snapshot is skipped, with a
warning event and `dismist_backup_failures_total{reason="space"}`, when it
would leave less than a quarter of the volume free; `DismistBackupStale`
fires after two days without one.

Other controller settings:

- `apFeatures` lists protocol features on by default for every AP
  (`-ap-features`; empty: all off). Each feature makes dismist send the AP a
  frame it has not had before, so switch them on per AP first (AP settings,
  Diagnostics), one at a time, and watch reconnects and boots.
- `controller.configPush` (`-config-push`): `reconnect` drops an AP's
  session so it fetches a changed config; `notify` sends `ep-config-change`
  and falls back to a reconnect when the AP does not fetch within 15
  seconds. Empty keeps dismistd's default. Only APs whose compiled config
  changed are pushed either way.
- `retention.samples`, `retention.sessions` and `retention.events`
  (`-retain-*`, Go durations such as `720h`) override how long history is
  kept (30, 180 and 90 days by default).
- `migration.acceptLocalFiles` (`-migrate-accept-local-files`) lists AP IDs
  whose `local_*` files on the AP you have reviewed, so the schema 2
  migration may claim equivalence for them. Leave it empty unless the
  migration report names an AP.

Before upgrades, restores or file-level backups, stop the pod and wait for it
to exit. Copy the volume only after the process has stopped. Back up the
database together with the private Secret and any offline CA material. The
database holds WLAN PSKs and history, and the private files allow AP control.
The PVC carries `helm.sh/resource-policy: keep`, so uninstalling the release
leaves it in place. Admin sessions are held in memory and are lost on
restart.

## Rollback

dismist's database carries two numbers in `meta`: `schema`, the format the
newest writer used, and `compat`, the oldest schema a binary must support to
run it safely. Every release from 0.3.0 refuses to start, with exit 78 and
the message "controller.db needs dismist schema N; restore
controller.db.pre-v2-* to run this version", when `compat` is newer than it
supports. While the database refuses to start, the APs keep running their
last config.

The schema 2 migration runs at the first start of a release that has it. It
writes a copy of the database first (`/state/controller.db.pre-v2-<UTC
time>`), adds typed settings next to the old fields and keeps `compat` at 1,
so the previous release still runs the migrated database and compiles exactly
the config it compiled before. If the migration cannot prove that every AP's
effective config is unchanged, nothing is written: dismist keeps running in
legacy mode (System, Migration shows why; `DismistMigrationPending` fires)
and nothing changes for the APs.

Before any upgrade, download a backup (System, or `GET /api/system/backup`)
and note the AP's effective hash (`GET /api/aps/<id>/config?view=normalised`).
Then, depending on what went wrong:

1. **A protocol feature misbehaves:** switch it off for the AP (AP settings,
   Diagnostics). No deploy needed.
2. **A config change misbehaves:** restore the previous revision from the
   site's Changes page.
3. **The release misbehaves and `compat` is 1** (`dismist_store_compat` is 1,
   or System shows it): roll the image back. With Helm,
   `helm -n dismist history dismist` and `helm -n dismist rollback dismist
   <revision>`; with Flux, revert the chart or image tag in Git. The older
   release compiles the untouched legacy fields. Rolling forward again
   re-migrates anything the older release wrote.
4. **`compat` is 2** (after storing something a schema 1 release cannot
   compile, or after finalising the migration): restore the pre-migration
   copy **first**, then roll the image back. Never roll back the image alone,
   and never to a release older than 0.3.0, which has no `compat` check.

Restoring the pre-migration copy (changes made since the migration are
lost; the release and PVC names below are the defaults):

```sh
# Stop the controller. With Flux, suspend the HelmRelease first so it does
# not scale it back up.
flux -n dismist suspend helmrelease dismist        # Flux only
kubectl -n dismist scale deployment/dismist --replicas=0
kubectl -n dismist wait --for=delete pod -l app.kubernetes.io/instance=dismist --timeout=120s

# List the copies, keep the current database aside, and put the copy back
# as the daemon user with mode 0600.
kubectl -n dismist run dismist-restore -i --rm --restart=Never --image=busybox:1.37.0 \
  --overrides='{"spec":{"securityContext":{"runAsUser":65532,"runAsGroup":65532,"runAsNonRoot":true},
    "containers":[{"name":"restore","image":"busybox:1.37.0","stdin":true,
      "command":["sh","-ec","ls -l /state; cp -p /state/controller.db /state/controller.db.before-restore; cp /state/controller.db.pre-v2-<UTC time> /state/controller.db; chmod 0600 /state/controller.db; ls -l /state"],
      "volumeMounts":[{"name":"state","mountPath":"/state"}]}],
    "volumes":[{"name":"state","persistentVolumeClaim":{"claimName":"dismist-state"}}]}}'

# Roll the image back (Helm), or revert the tag in Git and resume (Flux).
helm -n dismist rollback dismist <revision>
flux -n dismist resume helmrelease dismist         # Flux only
kubectl -n dismist scale deployment/dismist --replicas=1
```

After any rollback, check that the AP reconnects within two minutes, its
applied revision matches the desired one, its effective hash matches the
value you noted, and the client count recovers.
