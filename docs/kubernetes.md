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
endpoint behind an ingress, Gateway or other TLS-terminating proxy. Dismist
also identifies each AP by its source IP, so the path must keep it:

- **LoadBalancer (default).** The `<release>-ap` Service exposes WSS on TCP
  443 with `externalTrafficPolicy: Local`. Register each AP with the address
  it arrives from. For an AP behind NAT, that is the NAT's public address. One
  AP per source address.
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
stock endpoint name. Create one Secret holding the admin token and one
`<name>.bin` per AP (each exactly 32 bytes). Include `tls.crt` and `tls.key`
only if the certificate does not come from `tls.secretName` or
`tls.certificate`:

```sh
kubectl create namespace dismist
kubectl -n dismist create secret generic dismist-private \
  --from-file=admin.token=DIR/admin.token \
  --from-file=AP_NAME.bin=/private/path/to/secret.bin
```

An init container copies the Secret into a memory volume at `/private`,
owned by the daemon user with mode `0600`, because dismistd rejects readable
secrets. Restart the pod after changing the Secret. Register each AP with
secret file `/private/AP_NAME.bin`.

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

## Operations

`GET /healthz` on the UI port is the startup, readiness and liveness probe.
It confirms the UI handler answers. It does not test AP WSS authentication
or bbolt writes, so check AP connections in the UI after a change.
Prometheus metrics stay on the pod's loopback (`controller.metricsListen`);
scrape them with a sidecar if you need them.

Before upgrades, restores or file-level backups, stop the pod and wait for it
to exit. Copy the volume only after the process has stopped. Back up the
database together with the private Secret and any offline CA material. The
database holds WLAN PSKs and history, and the private files allow AP control.
The PVC carries `helm.sh/resource-policy: keep`, so uninstalling the release
leaves it in place. Admin sessions are held in memory and are lost on
restart.
