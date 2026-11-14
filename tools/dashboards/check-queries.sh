#!/usr/bin/env bash
# Loads the exporter's fixture exposition (internal/metrics
# TestWriteFixtureExposition) into a throwaway Prometheus and evaluates every
# dashboard query against it (check_queries.py). Needs Go, Python 3 and
# Docker. PROMETHEUS_IMAGE overrides the image (default: the version the
# production cluster runs). EXPOSITION_FILE checks the queries against a
# saved scrape instead (for example a live dismistd with fake APs).
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
image=${PROMETHEUS_IMAGE:-quay.io/prometheus/prometheus:v3.11.2}
port=${EXPOSITION_PORT:-18970}
prom_port=${PROMETHEUS_PORT:-18990}
work=$(mktemp -d)
name=dismist-dashboard-queries-$$
server=
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  if [ -n "$server" ]; then kill "$server" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work/srv" "$work/conf"
if [ -n "${EXPOSITION_FILE:-}" ]; then
  cp "$EXPOSITION_FILE" "$work/srv/metrics"
else
  (cd "$root" && DISMIST_FIXTURE_EXPOSITION="$work/srv/metrics" go test ./internal/metrics -run '^TestWriteFixtureExposition$' -count=1 >/dev/null)
fi
python3 -m http.server "$port" --bind 127.0.0.1 --directory "$work/srv" >/dev/null 2>&1 &
server=$!

# Linux: share the host network. Docker Desktop: reach the host by name.
if [ "$(uname -s)" = Linux ]; then
  network=(--network host)
  target=127.0.0.1:$port
  web=127.0.0.1:$prom_port
  listen=(--web.listen-address="$web")
else
  network=(-p "127.0.0.1:$prom_port:9090")
  target=host.docker.internal:$port
  web=127.0.0.1:$prom_port
  listen=()
fi
cat >"$work/conf/prometheus.yml" <<YAML
global:
  scrape_interval: 2s
scrape_configs:
  - job_name: dismist
    fallback_scrape_protocol: PrometheusText0.0.4
    static_configs:
      - targets: ['$target']
        labels: {namespace: dismist}
YAML
chmod -R a+rX "$work/conf"
docker run -d --name "$name" "${network[@]}" -v "$work/conf:/etc/prometheus:ro" "$image" \
  --config.file=/etc/prometheus/prometheus.yml "${listen[@]}" >/dev/null

# Wait for two scrapes, so rate() and changes() have samples.
for _ in $(seq 1 60); do
  up=$(curl -fsS "http://$web/api/v1/query?query=count_over_time(up%5B1m%5D)" 2>/dev/null |
    python3 -c 'import json,sys; r=json.load(sys.stdin)["data"]["result"]; print(r[0]["value"][1] if r else 0)' 2>/dev/null || echo 0)
  if [ "${up%.*}" -ge 3 ]; then break; fi
  sleep 1
done
curl -fsS "http://$web/api/v1/query?query=up" | grep -q '"1"\]' || { docker logs "$name" >&2; echo "Prometheus could not scrape the fixture" >&2; exit 1; }
python3 "$root/tools/dashboards/check_queries.py" "http://$web" "$root/charts/dismist/monitoring/dashboards"
