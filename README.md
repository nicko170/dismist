# Dismist

Mist, dismissed.

`dismistd` is one Go server for AP WSS control, AP-scoped DNS, a LAN web UI/API,
and bbolt state. The React UI is embedded in the daemon and receives live
site-scoped updates over authenticated SSE. The `dismist` CLI
prepares local credentials. A site contains APs, WLANs, clients, events, and
history. See [deployment](docs/deployment.md) for installation, AP preparation,
service operation, and backups. For a new AP, follow
[AP onboarding](docs/onboarding.md); for AP33 hardware, use the
[Juniper AP33 walkthrough](docs/juniper/AP33.md).

Prometheus metrics are available at `http://127.0.0.1:9090/metrics` on the
controller host. Site Settings can send validated SSH public keys to all APs
in a site after they authenticate. Multiple APs per site are supported; mesh
backhaul is not yet validated. See [monitoring and SSH operations](docs/deployment.md).

## Quick start

On Kubernetes, use the Helm chart and image published with each release
(`oci://ghcr.io/nicko170/charts/dismist`, `ghcr.io/nicko170/dismist`); see
[Kubernetes deployment](docs/kubernetes.md). Otherwise, install both commands
with Go 1.27, or download the binaries from
[GitHub Releases](https://github.com/nicko170/dismist/releases):

```sh
go install github.com/nicko170/dismist/cmd/...@latest
export PATH="$(go env GOPATH)/bin:$PATH"
mkdir -p dismist && cd dismist
dismist init
sudo "$(go env GOPATH)/bin/dismistd"
```

`dismist init` creates a private `data/` directory, generates the local CA
and WSS certificate, writes the bundled AP33 base configuration, and prints
the new admin token. Open `http://SERVER_IP:8088/`, sign in, and create a site
with its country, then add a WLAN. Follow the
[AP33 rooting and onboarding guide](docs/juniper/AP33.md) to prepare and register
each physical AP. The site supplies the country, SSID, VLAN, and passphrase;
you do not need to write an AP config JSON file.

For a release archive, download the file matching your OS and CPU, verify it
against `SHA256SUMS`, and extract it. Run `./dismist init` and
`sudo ./dismistd` from the extracted directory; the archive also contains the
AP33 guide. The binary embeds the React UI and AP33 template, so no Node/npm
installation is needed on the server.

The daemon listens on UI `0.0.0.0:8088`, AP WSS `0.0.0.0:443`, and AP DNS
UDP/TCP `0.0.0.0:53`. Ports 443 and 53 need suitable OS privilege or service
capabilities. If the server has multiple usable IPv4 addresses, pass
`-controller-ip` with the address APs can reach. The example command uses
`sudo` for a quick test; [deployment](docs/deployment.md) covers a dedicated
service account, HTTPS for the UI, and backups.

The repository is currently private; downloading releases or installing a
version from GitHub requires access until it is made public. From a source
checkout, run `npm --prefix web ci`, `npm --prefix web run build`, then
`go test ./...` and `go build ./cmd/dismistd`.

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
- [Juniper AP33 onboarding](docs/juniper/AP33.md)
- [Sites and history](docs/sites-and-history.md)
- [Config field surface](docs/config-surface.md)
- [Kubernetes deployment](docs/kubernetes.md)

## License

MIT. See [LICENSE](LICENSE).
