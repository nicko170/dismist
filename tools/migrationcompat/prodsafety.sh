#!/usr/bin/env bash
# Production-safety check for the schema-2 upgrade (design §4.5, §4.7),
# with local binaries instead of images:
#
#   OLD_DISMISTD=… OLD_FAKEAP=… [NEW_DISMISTD=… NEW_FAKEAP=…] \
#     [FIXTURE_OUT=dir] tools/migrationcompat/prodsafety.sh
#
# OLD_* are v0.3.0 binaries (go build ./cmd/dismistd ./tools/fakeap/cmd/fakeap
# at tag v0.3.0); NEW_* default to builds of this tree. The script:
#   1. seeds a schema-1 database through v0.3.0's API with the production
#      AP's exact shape: site Home (AU), WLAN "Example" on VLAN 1 with the
#      WPA2/WPA3 network patch (Auth.AKM WPA2_PSK+WPA3_PSK, Dot11w
#      Enabled/not Required), the site patch replacing Radios[] (AU/890,
#      2.4 GHz 1/20/12 dBm, 5 GHz 149/80/17 dBm, Channel/Width/Power and
#      RRM) plus Monitor.CCRev AU/890, and an AP with a secret and a source
#      pin;
#   2. records the bytes v0.3.0 serves the AP and its config revision;
#   3. starts the new binary on that database and checks: the migration
#      committed (compat stays 1), the served config normalises to the same
#      effective config (any leaf whose raw bytes differ is listed), the
#      AP's config revision did not move, and the typed settings show
#      WPA2/WPA3 and the RF values.
# FIXTURE_OUT, when set, receives the v0.3.0 database, admin token and
# served config (cmd/dismistd/testdata/v030-production is made this way).
# Everything is synthetic (02-aa-… IDs, SSID "Example", loopback ports); no
# real AP, cluster or service is contacted.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
: "${OLD_DISMISTD:?set OLD_DISMISTD to a v0.3.0 dismistd binary}"
: "${OLD_FAKEAP:?set OLD_FAKEAP to a v0.3.0 fakeap binary}"
UI_PORT=${UI_PORT:-18188}
WSS_PORT=${WSS_PORT:-18543}
DNS_PORT=${DNS_PORT:-18153}

work=$(mktemp -d)
pid=
cleanup() {
  if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$work/bin"
if [ -z "${NEW_DISMISTD:-}" ]; then
  NEW_DISMISTD=$work/bin/dismistd
  (cd "$root" && go build -o "$NEW_DISMISTD" ./cmd/dismistd)
fi
if [ -z "${NEW_FAKEAP:-}" ]; then
  NEW_FAKEAP=$work/bin/fakeap
  (cd "$root" && go build -o "$NEW_FAKEAP" ./tools/fakeap/cmd/fakeap)
fi
MIGRATIONCOMPAT=$work/bin/migrationcompat
(cd "$root" && go build -o "$MIGRATIONCOMPAT" ./tools/migrationcompat)

step() { printf '\n== %s\n' "$*"; }
fail() {
  echo "FAIL: $*" >&2
  tail -n 40 "$work/daemon.log" >&2 || true
  exit 1
}

data=$work/data
mkdir -m 700 "$data"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 2 \
  -subj /CN=ap.example.test -addext "subjectAltName=DNS:ap.example.test,IP:127.0.0.1" \
  -keyout "$data/tls.key" -out "$data/tls.crt" >/dev/null 2>&1
chmod 600 "$data/tls.key"

ap_id=02-aa-00-00-00-01
ap_secret=000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f

start() {
  "$1" -db="$data/controller.db" -admin-token-file="$data/admin.token" \
    -tls-cert="$data/tls.crt" -tls-key="$data/tls.key" -tls-check-interval=0 \
    -ui-listen="127.0.0.1:$UI_PORT" -ws-listen="127.0.0.1:$WSS_PORT" -dns-listen="127.0.0.1:$DNS_PORT" \
    -controller-ip=127.0.0.1 -upstream-dns=127.0.0.1:9 -metrics-listen= -backup-interval=0 \
    >"$work/daemon.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 120); do
    if curl -fsS "http://127.0.0.1:$UI_PORT/readyz" >/dev/null 2>&1; then
      return 0
    fi
    kill -0 "$pid" 2>/dev/null || fail "$1 exited during startup"
    sleep 0.5
  done
  fail "$1 was not ready within a minute"
}

stop() {
  kill "$pid"
  wait "$pid" 2>/dev/null || true
  pid=
}

api() {
  local method=$1 path=$2 body=${3:-}
  local args=(-sS --fail-with-body -b "$work/cookies" -c "$work/cookies" -X "$method" -H 'Content-Type: application/json')
  if [ -n "$body" ]; then
    args+=(--data "$body")
  fi
  if ! curl "${args[@]}" -o "$work/response" "http://127.0.0.1:$UI_PORT$path"; then
    fail "$method $path: $(cat "$work/response" 2>/dev/null)"
  fi
  cat "$work/response"
}

login() {
  rm -f "$work/cookies"
  api POST /auth/login "{\"token\":\"$(tr -d '\n' <"$data/admin.token")\"}" >/dev/null
}

fetch() { # fetch FAKEAP NAME
  for _ in $(seq 1 40); do
    if "$1" -url "wss://127.0.0.1:$WSS_PORT/ws" -id "$ap_id" -secret "$ap_secret" -insecure -once >"$work/$2.json" 2>"$work/$2.err"; then
      return 0
    fi
    sleep 0.5
  done
  cat "$work/$2.err" >&2
  fail "fake AP could not fetch its config ($2)"
}

py() { python3 -c "$1"; }
# revision_of: the AP's config_revision from GET /api/aps (v0.3.0 has no
# GET /api/aps/$id; the list answers {"aps":[…]} or a bare list).
revision_of() {
  api GET /api/aps | py "
import json,sys
body=json.load(sys.stdin)
aps=body['aps'] if isinstance(body,dict) else body
print([a for a in aps if a['id']=='$ap_id'][0]['config_revision'])"
}

step "seed the production shape with v0.3.0 ($OLD_DISMISTD)"
start "$OLD_DISMISTD"
login
site=$(api POST /api/sites '{"name":"Home","country":"AU","timezone":"Australia/Sydney"}' | py "import json,sys; print(json.load(sys.stdin)['site']['id'])")
network=$(api POST "/api/sites/$site/networks" '{"ssid":"Example","psk":"example-passphrase-1","vlan_id":1,"enabled":true}' |
  py "import json,sys; print(json.load(sys.stdin)['networks'][0]['id'])")
api PUT "/api/sites/$site/networks/$network/advanced" \
  '{"patch":{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}}' >/dev/null
api PUT "/api/sites/$site/advanced" '{"patch":{"Radios":[
    {"Band":"2.4","CCRev":"AU/890","CCRevSP":"AU/890","Channel":1,"Width":20,"Power":12,"RRM":{"Channel":1,"Width":20,"Power":12}},
    {"Band":"5","CCRev":"AU/890","CCRevSP":"AU/890","Channel":149,"Width":80,"Power":17,"RRM":{"Channel":149,"Width":80,"Power":17}}],
  "Monitor":{"CCRev":"AU/890"}}}' >/dev/null
api POST /api/aps "{\"id\":\"$ap_id\",\"name\":\"Hallway\",\"site_id\":\"$site\",\"secret\":\"$ap_secret\",\"management_ip\":\"127.0.0.1\"}" >/dev/null
fetch "$OLD_FAKEAP" before
before_rev=$(revision_of)
echo "v0.3.0 serves revision $before_rev ($(wc -c <"$work/before.json") bytes)"
stop
"$MIGRATIONCOMPAT" state "$data/controller.db"
if [ -n "${FIXTURE_OUT:-}" ]; then
  mkdir -p "$FIXTURE_OUT"
  cp "$data/controller.db" "$FIXTURE_OUT/controller.db"
  cp "$data/admin.token" "$FIXTURE_OUT/admin.token"
  cp "$work/before.json" "$FIXTURE_OUT/served-v0.3.0.json"
  printf '{"site_id":"%s","network_id":"%s","ap_id":"%s","ap_secret":"%s","config_revision":%s}\n' \
    "$site" "$network" "$ap_id" "$ap_secret" "$before_rev" >"$FIXTURE_OUT/fixture.json"
  echo "fixture written to $FIXTURE_OUT"
fi

step "start the new binary ($NEW_DISMISTD) on the v0.3.0 database"
start "$NEW_DISMISTD"
grep -E "migration|H0" "$work/daemon.log" || true
fetch "$NEW_FAKEAP" after
login

step "checks"
migration=$(api GET /api/system/migration)
echo "$migration" | py "import json,sys; m=json.load(sys.stdin); print('migration', m['state'], 'schema', m['schema'], 'compat', m['compat'])"
[ "$(echo "$migration" | py "import json,sys; print(json.load(sys.stdin)['state'])")" = committed ] || fail "migration did not commit: $migration"
[ "$(echo "$migration" | py "import json,sys; print(json.load(sys.stdin)['compat'])")" = 1 ] || fail "compat raised: $migration"
"$MIGRATIONCOMPAT" equal "$work/before.json" "$work/after.json" || fail "the normalised effective config changed"
echo "normalised effective config: identical"
if cmp -s "$work/before.json" "$work/after.json"; then
  echo "served bytes: identical ($(wc -c <"$work/after.json") bytes)"
else
  # Different bytes at the same revision and Timestamp are allowed only
  # because they normalise to the same effective config (checked above):
  # the typed compiler writes what 0.15 ep-control would have derived
  # (clamped Dot11w SA query values, the resolved BSSID index, folded key
  # case, explicit zero values) and leaves out keys 0.15 does not read.
  echo "served bytes differ in these leaves (each normalises away):"
  python3 - "$work/before.json" "$work/after.json" <<'PY'
import json, sys
def leaves(value, path=""):
    if isinstance(value, dict):
        for key, child in value.items():
            yield from leaves(child, f"{path}.{key}" if path else key)
    elif isinstance(value, list) and value and any(isinstance(v, (dict, list)) for v in value):
        for index, child in enumerate(value):
            yield from leaves(child, f"{path}[{index}]")
    else:
        yield path, value
before = dict(leaves(json.load(open(sys.argv[1]))))
after = dict(leaves(json.load(open(sys.argv[2]))))
for path in sorted(set(before) | set(after)):
    if path.endswith(("PSK", "Passphrase")):
        continue
    a, b = before.get(path, "<absent>"), after.get(path, "<absent>")
    if a != b and not (isinstance(a, (int, float)) and isinstance(b, (int, float)) and a == b):
        print(f"  {path}: {json.dumps(a)} -> {json.dumps(b)}")
PY
fi
after_rev=$(revision_of)
[ "$after_rev" = "$before_rev" ] || fail "config revision moved: $before_rev -> $after_rev"
echo "config revision: $after_rev (unchanged)"
api GET "/api/sites/$site/networks/$network" | py "
import json,sys
w=json.load(sys.stdin)
print('WLAN', w['ssid'], 'security', w['security'], 'pmf', w['pmf'], 'vlan', w['vlan_id'], 'bands', w['bands'])
assert w['security']=='wpa2_wpa3_personal', w['security']
assert w['pmf'] in (None,'optional'), w['pmf']
assert w['vlan_id']==1
assert w['expert']['has_patch'] is False, w['expert']
" || fail "typed WLAN settings"
api GET "/api/sites/$site/rf" | py "
import json,sys
r=json.load(sys.stdin)
b5,b24=r['band_5'],r['band_24']
print('RF managed', r['managed'], 'ccrev', r.get('ccrev'), '| 5 GHz width', b5['width'], 'power', b5['power'], '| 2.4 GHz width', b24['width'], 'power', b24['power'])
assert r['managed'] and b5['width']==80 and b24['width']==20
assert b5['power']['mode']=='fixed' and b5['power']['dbm']==17, b5['power']
assert b24['power']['mode']=='fixed' and b24['power']['dbm']==12, b24['power']
" || fail "typed RF settings"
api GET "/api/aps/$ap_id" | py "
import json,sys
a=json.load(sys.stdin)
rf=a['overrides']['rf']
print('AP static channels', '5 GHz', rf['5']['channel'], '2.4 GHz', rf['2.4']['channel'], 'pinned', a['pinned_ip'])
assert rf['5']['channel']==149 and rf['2.4']['channel']==1
assert a['pinned_ip']=='127.0.0.1'
" || fail "typed AP settings"
stop
"$MIGRATIONCOMPAT" state "$data/controller.db"
echo
echo "production-safety check passed"
