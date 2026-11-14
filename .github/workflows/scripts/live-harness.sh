#!/usr/bin/env bash
# Runs a command against a real dismistd with fake APs (CI and local use):
#
#   .github/workflows/scripts/live-harness.sh -- npx playwright test --grep @live
#
# It initialises a private data directory, starts dismistd on loopback
# ports, creates a site ("Example Home", AU) with one WLAN ("Example"),
# registers two fake APs (02-aa-00-00-00-01 and -02) and connects them
# with tools/fakeap (synthetic telemetry and clients), checks the
# /metrics pipeline end to end, then runs the command with:
#   E2E_BASE_URL     the operator UI and API (http://127.0.0.1:18088)
#   E2E_ADMIN_TOKEN  the administrator token (sign in with it)
#   E2E_SITE_ID      the site's ID
#   E2E_AP_IDS       the fake APs' IDs, space separated
#   E2E_METRICS_URL  the scrape endpoint
# Everything is synthetic and on loopback; no real AP is contacted.
#
# Environment: DISMISTD_BIN, DISMIST_BIN, FAKEAP_BIN (built from the tree
# when unset).
set -euo pipefail

root=$(cd "$(dirname "$0")/../../.." && pwd)
if [ "${1:-}" = "--" ]; then shift; fi
work=$(mktemp -d)
pids=()
cleanup() {
  for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

build() {
  local var=$1 package=$2
  if [ -z "${!var:-}" ]; then
    printf -v "$var" '%s' "$work/bin/$(basename "$package")"
    (cd "$root" && go build -o "${!var}" "$package")
  fi
}
build DISMISTD_BIN ./cmd/dismistd
build DISMIST_BIN ./cmd/dismist
build FAKEAP_BIN ./tools/fakeap/cmd/fakeap

ui=127.0.0.1:18088
wss=127.0.0.1:18443
metrics=127.0.0.1:19190
data=$work/data
"$DISMIST_BIN" init --data-dir "$data" >/dev/null
"$DISMISTD_BIN" -db="$data/controller.db" -admin-token-file="$data/admin.token" \
  -tls-cert="$data/tls.crt" -tls-key="$data/tls.key" -ap-config="$data/ap-config.json" \
  -ui-listen="$ui" -ws-listen="$wss" -dns-listen=127.0.0.1:15353 -controller-ip=192.0.2.1 \
  -metrics-listen="$metrics" -backup-interval=0 >"$work/dismistd.log" 2>&1 &
pids+=($!)

for _ in $(seq 1 60); do
  curl -fsS "http://$ui/readyz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "http://$ui/readyz" >/dev/null || { cat "$work/dismistd.log" >&2; exit 1; }

token=$(tr -d '\n' <"$data/admin.token")
api() {
  curl -sS --fail-with-body -b "$work/cookies" -c "$work/cookies" -X "$1" -H 'Content-Type: application/json' \
    ${3:+--data "$3"} "http://$ui$2"
}
api POST /auth/login "{\"token\":\"$token\"}" >/dev/null
# v1 answers a MutationResponse {object}; releases before it {site}.
site=$(api POST /api/sites '{"name":"Example Home","country":"AU","timezone":"UTC"}' |
  python3 -c "import json,sys; b=json.load(sys.stdin); print((b.get('object') or b.get('site') or b)['id'])")
api POST "/api/sites/$site/networks" '{"ssid":"Example","psk":"example-passphrase-1","vlan_id":1,"enabled":true}' >/dev/null

ap_ids=()
for n in 1 2; do
  id=02-aa-00-00-00-0$n
  secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  api POST /api/aps "{\"id\":\"$id\",\"name\":\"Example AP $n\",\"site_id\":\"$site\",\"secret\":\"$secret\"}" >/dev/null
  "$FAKEAP_BIN" -url "wss://$wss/ws" -id "$id" -secret "$secret" -insecure -clients $((n + 1)) \
    -full 5s -fast 2s -local-ip "192.0.2.1$n" >"$work/fakeap-$n.log" 2>&1 &
  pids+=($!)
  ap_ids+=("$id")
done

# The metrics pipeline end to end: both APs connected and reporting.
for _ in $(seq 1 30); do
  scrape=$(curl -fsS "http://$metrics/metrics" || true)
  if [ "$(grep -c '^dismist_ap_connected{.*} 1$' <<<"$scrape")" = 2 ]; then break; fi
  sleep 1
done
for want in '^dismist_ap_connected\{.*\} 1$' '^dismist_ap_registered\{.*\} 1$' '^dismist_build_info\{' \
  '^dismist_ap_protocol_feature\{' '^dismist_store_size_bytes ' '^dismist_ap_ws_routes_total\{.*result="routed".*\} [1-9]'; do
  if ! grep -Eq "$want" <<<"$scrape"; then
    echo "metrics: no series matching $want" >&2
    grep '^dismist_' <<<"$scrape" | head -50 >&2
    tail -n 30 "$work/dismistd.log" "$work"/fakeap-*.log >&2
    exit 1
  fi
done
echo "live harness: dismistd on http://$ui with ${#ap_ids[@]} fake APs connected"

if [ $# -eq 0 ]; then exit 0; fi
E2E_BASE_URL="http://$ui" E2E_ADMIN_TOKEN="$token" E2E_SITE_ID="$site" E2E_AP_IDS="${ap_ids[*]}" \
  E2E_METRICS_URL="http://$metrics/metrics" "$@"
