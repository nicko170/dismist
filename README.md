# Dismist

Mist, dismissed.

`dismistd` is one Go server for AP WSS control, AP-scoped DNS, a LAN web UI/API,
and bbolt state. The React UI is embedded in the daemon and receives live
site-scoped updates over authenticated SSE. The `dismist` CLI
prepares local credentials. A site contains APs, WLANs, clients, events, and
history. See [deployment](docs/deployment.md) for installation, AP preparation,
service operation, and backups. For a new AP, follow
[AP onboarding](docs/onboarding.md); for AP33 hardware, use the
[Juniper AP33 walkthrough](juniper/AP33.md).

Prometheus metrics are available at `http://127.0.0.1:9090/metrics` on the
controller host. Site Settings can send validated SSH public keys to all APs
in a site after they authenticate. Multiple APs per site are supported; mesh
backhaul is not yet validated. See [monitoring and SSH operations](docs/deployment.md).

## Build and run

Requires Go 1.27 and Node/npm to build the embedded UI:

```sh
cd web
npm ci
npm run build
cd ..
go test ./...
go build -o dismist ./cmd/dismist
go build -o dismistd ./cmd/dismistd
./dismist init --data-dir ./data
# Place a firmware-compatible AP config template at data/ap-config.json.
./dismistd -controller-ip 192.0.2.10
```

Replace the example address with the server's IPv4 address reachable from APs.
The address can be omitted if the server has exactly one usable IPv4 address.
The default listeners are UI `0.0.0.0:8088`, AP WSS `0.0.0.0:443`, and AP
DNS UDP/TCP `0.0.0.0:53`. Ports 443 and 53 require appropriate OS privilege
or service capabilities. `-h` lists listener and file overrides. Before the
first start, supply `data/ap-config.json` as described in the deployment guide.
`dismist init` creates `data/tls.crt`, `data/tls.key`, `data/ca.crt`,
`data/ca.key`, and `data/admin.token` with private permissions. It does not
contact an AP or Mist cloud. State is kept in `data/controller.db`.

The AP management LAN must use the controller as DNS. For registered AP
source addresses, it maps `ep-terminator.mistsys.net` to the advertised
controller IP and forwards other questions to `-upstream-dns`. It refuses DNS
from unregistered addresses. APs also need a compatible controller certificate
in their trust store and their existing device secrets in private files. The
locally generated `data/ca.crt` must be installed into the AP trust store;
initializing the server does not prepare an AP. DNS redirection alone cannot
provision an unprepared factory AP.

## Reference

- [Deployment and backups](docs/deployment.md)
- [AP onboarding](docs/onboarding.md)
- [Juniper AP33 onboarding](juniper/AP33.md)
- [Sites and history](docs/sites-and-history.md)
- [Config field surface](docs/config-surface.md)
- [Kubernetes deployment](docs/kubernetes.md)

## License

MIT. See [LICENSE](LICENSE).
