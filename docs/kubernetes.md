# Kubernetes deployment

The manifests in [ops/kubernetes](../ops/kubernetes/) run one `dismistd` process
with its embedded React UI. This is a single-controller deployment, not a
high-availability configuration. The controller holds its bbolt database on a
10 Gi `ReadWriteOnce` persistent volume. Its DNS, WSS, and UI listeners use
the selected node's network directly: UDP/TCP 53, TCP 443, and TCP 8088.

## Prerequisites and network plan

Choose one dedicated Linux node with a stable IPv4 address reachable from the
AP management network. Label it `dismist.io/controller=true`. Ensure nothing
else on the node binds 53, 443, or 8088, including a local DNS stub or ingress.
The deployment uses `hostNetwork` so the daemon sees each AP's original source
IP. Route AP traffic directly to the node; a load balancer, proxy, or NAT that
changes the source address breaks the AP DNS allowlist and WSS registration.
The `-controller-ip` argument defaults to the Kubernetes node's `status.hostIP`.
If the AP-facing address differs, edit that argument to the reachable IPv4
address before applying the manifests. Keep the AP-facing address stable across
rescheduling and restore. The PVC's storage class must support `ReadWriteOnce`
mounts on the selected node and Unix ownership changes by the init container.
Only one node should carry the selector label unless all eligible nodes have
the same AP-facing endpoint and compatible storage.

Set the AP management DHCP DNS server to that AP-facing address. Allow only
registered AP management addresses to reach UDP/TCP 53 and TCP 443, and
operator clients to reach TCP 8088. `hostNetwork` exposes 8088 on the node as
well as through the `dismist-ui` ClusterIP service; protect it with node or
upstream firewall rules. Kubernetes NetworkPolicy does not provide a reliable
boundary for this host-networked pod. The UI is HTTP; use a trusted LAN or an
HTTPS reverse proxy for operators. A proxy must allow unbuffered
`/api/sites/*/stream` SSE responses and the 15-second heartbeat. Set `-ui-host`
if operators use a DNS hostname. The AP WSS endpoint is separate from the UI;
do not terminate AP TLS at an ingress. Permit the daemon to reach its upstream
DNS resolver. The default is `1.1.1.1:53`; set `-upstream-dns` in the args if
your network requires another resolver.

The AP must trust the CA that issued the WSS certificate for
`ep-terminator.mistsys.net` and retain its existing 32-byte EEPROM
authentication secret. DNS redirection alone does not prepare a factory AP.
Follow [AP onboarding](onboarding.md) before enabling an AP.

## Build the image and prepare credentials

Build from the repository root so the Dockerfile can compile the React assets
and both Go binaries. Its Dockerfile-specific ignore file limits the build
context to the listed sources; private `evidence/`, local `data/`, and
`node_modules` are not sent to the builder. Publish the image to a registry
the selected node can pull, then
replace `registry.example.invalid/dismist:REPLACE_ME` in `deployment.yaml`
with an immutable digest reference.

```sh
docker build -f ops/kubernetes/Dockerfile -t REGISTRY/dismist:VERSION .
docker push REGISTRY/dismist:VERSION
kubectl label node NODE_NAME dismist.io/controller=true
kubectl create namespace dismist
```

Prepare a private working directory off the Git checkout. Run `dismist init
--data-dir DIR` there to create the CA, WSS leaf pair, and admin token, then
place a full firmware-compatible `ap-config.json` alongside them. The template
may contain WLAN secrets and belongs in the Kubernetes Secret. Keep `ca.key`
and `ca.crt` in an encrypted backup outside the cluster; only the WSS leaf
pair is needed by the daemon. Never generate a new EEPROM secret to replace an
existing AP's secret.

Create a namespace Secret from the prepared files. Supply one
`--from-file=AP_NAME.bin=/private/path/to/raw-secret` per AP. Key names must be
unique and the raw files must each be exactly 32 bytes. The paths shown here
are placeholders; do not put credential values in YAML or shell arguments.

```sh
kubectl -n dismist create secret generic dismist-private \
  --from-file=admin.token=DIR/admin.token \
  --from-file=tls.crt=DIR/tls.crt \
  --from-file=tls.key=DIR/tls.key \
  --from-file=ap-config.json=DIR/ap-config.json \
  --from-file=AP_NAME.bin=/private/path/to/raw-secret
kubectl -n dismist apply -k ops/kubernetes
kubectl -n dismist rollout status deployment/dismist
```

The init container copies Secret entries to a memory-backed volume, changes
their owner to the non-root daemon UID, and sets file mode `0600`. This is
necessary because the daemon rejects group-readable admin tokens and AP
secrets. The same init container owns the PVC mount directory for bbolt. The
main container has a read-only root filesystem and only the bind capability
needed for ports below 1024. Kubernetes Secret encryption at rest and restricted
access to Secret read permissions should be enabled by the cluster operator.

For a new database, create the site and AP in the UI after the pod is ready.
Use `/private/AP_NAME.bin` as the AP's `secret_file`, its MAC-derived controller
ID, and the source management IP actually seen at the node. An AP without a
registered source IP receives no controller DNS answer. To add or replace a
Secret entry later, update `dismist-private` and restart the deployment: the
in-memory private volume is populated only when a pod starts. Coordinate a
credential rotation with AP trust changes. A restart interrupts AP WSS
sessions until they reconnect.

## Operations

`GET /healthz` on port 8088 is the current startup, readiness, and liveness
probe. It confirms that the UI handler is answering; it does not test AP DNS,
WSS authentication, upstream DNS, or bbolt writes. Check these paths separately
after deployment. Prometheus metrics are on a separate loopback listener,
`127.0.0.1:9090/metrics` by default. With `hostNetwork`, that loopback address
belongs to the selected node. Scrape it using a same-pod sidecar or a secured
node-local collector; an ordinary Prometheus pod cannot scrape it through the
pod IP or `dismist-ui` Service. Do not expose port 9090 on the AP or operator
network. Ensure the node has no competing port 9090 listener, or set
`-metrics-listen` to another loopback port. Setting it to empty disables metrics.

The Deployment uses one replica and `Recreate` upgrades. Never scale it above
one or run another process against `controller.db`. Before upgrades, restores,
or file-level backups, scale it to zero and wait for the pod to exit. Copy or
snapshot the PVC only after the process has stopped. Back up the database
together with the Kubernetes Secret contents, the offline CA material, and any
initial import data. Protect those backups: the database contains WLAN PSKs
and history, and the private files allow AP control. Keep the PVC when deleting
or replacing the Deployment. After restoring, verify file ownership and mode
`0600`, bring the deployment back to one replica, and verify AP reconnection.
The controller's in-memory admin sessions are lost on restart.

Before a production apply, the operator must select the registry digest,
dedicated node and its stable AP-facing IPv4 address, PVC storage class and
backup method, upstream DNS resolver, AP management routing/firewall rules,
and UI HTTPS access path. The sample uses the cluster's default storage class;
set `storageClassName` in `pvc.yaml` if that is unsuitable. It creates no
external load balancer or ingress.
