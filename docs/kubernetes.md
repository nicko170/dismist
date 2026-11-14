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
carry site and AP IDs only: no names, addresses, SSIDs, client MACs or
secrets.

`monitoring.enabled: true` ships everything for a cluster running the
Prometheus Operator and the Grafana dashboard sidecar:

- a `<release>-metrics` ClusterIP Service, and dismistd listening on
  `:9090` (`-metrics-listen=:9090 -metrics-allow-remote`);
- a ServiceMonitor scraping it every 30 s, with `instance` set to the
  release name so AP series continue across pod restarts;
- a PrometheusRule with the alerts in
  [monitoring/rules.yaml](../charts/dismist/monitoring/rules.yaml)
  (warning and info only);
- a ConfigMap of Grafana dashboards, labelled `grafana_dashboard: "1"` with
  a `grafana_folder` annotation (from the release that adds the dashboards).

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

A Prometheus that selects every ServiceMonitor and PrometheusRule (the
kube-prometheus-stack `*SelectorNilUsesHelmValues: false` setting) needs no
labels. If yours selects by label, add it under
`metrics.serviceMonitor.labels` and `metrics.prometheusRule.labels`, for
example `release: <kube-prometheus-stack release>`. The Grafana sidecar must
watch the ConfigMap's namespace (`metrics.dashboards.namespace`, default the
release namespace). Alerts can be dropped or re-graded per install with
`metrics.prometheusRule.disabledAlerts` and `severityOverrides`.

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
address while metrics are not exposed.

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
`backup.maxBytes` (`""`: 30 % of the volume). `apFeatures` lists protocol
features on by default for every AP (`-ap-features`; empty: all off).

Before upgrades, restores or file-level backups, stop the pod and wait for it
to exit. Copy the volume only after the process has stopped. Back up the
database together with the private Secret and any offline CA material. The
database holds WLAN PSKs and history, and the private files allow AP control.
The PVC carries `helm.sh/resource-policy: keep`, so uninstalling the release
leaves it in place. Admin sessions are held in memory and are lost on
restart.
