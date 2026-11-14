#!/usr/bin/env bash
# Migration and rollback compatibility (design §4.1 rule 6), run by the
# migration-compat CI job and runnable locally with Docker:
#
#   NEW_IMAGE=dismist:ci tools/migrationcompat/run.sh
#
# For each previous release image it seeds a schema-1 database through that
# release's API with the shape of a real install (a WPA2/WPA3 network patch
# and a site patch replacing Radios[] with AU/890, static channels and
# powers), records the config a fake AP is served, then checks that:
#   1. the new image starts on that database (migrating it) and serves a
#      config that normalises to the same effective config;
#   2. the previous image still starts on the migrated database (compat 1)
#      and serves the same config as before (rollback by image tag);
#   3. after the previous image writes (an AP rename), the new image
#      re-migrates and again serves what the previous image served;
#   4. with meta.compat = 2 the M0 image refuses to start: exit 78 and the
#      restore message.
# Everything is synthetic (192.0.2.0/24, 02-aa-… IDs, SSID "Example"); no
# real AP, cluster or service is contacted.
#
# Environment: NEW_IMAGE (required), PREVIOUS_IMAGES (space separated,
# default the M0 release), M0_IMAGE (default the M0 release), FAKEAP and
# MIGRATIONCOMPAT (binaries; built from the tree when unset).
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
: "${NEW_IMAGE:?set NEW_IMAGE to the image under test}"
M0_IMAGE=${M0_IMAGE:-ghcr.io/nicko170/dismist:0.3.0}
PREVIOUS_IMAGES=${PREVIOUS_IMAGES:-$M0_IMAGE}
UI_PORT=${UI_PORT:-18088}
WSS_PORT=${WSS_PORT:-18443}
container=dismist-migration-compat

work=$(mktemp -d)
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

if [ -z "${FAKEAP:-}" ]; then
  FAKEAP=$work/bin/fakeap
  (cd "$root" && go build -o "$FAKEAP" ./tools/fakeap/cmd/fakeap)
fi
if [ -z "${MIGRATIONCOMPAT:-}" ]; then
  MIGRATIONCOMPAT=$work/bin/migrationcompat
  (cd "$root" && go build -o "$MIGRATIONCOMPAT" ./tools/migrationcompat)
fi

step() { printf '\n== %s\n' "$*"; }
fail() {
  echo "FAIL: $*" >&2
  docker logs "$container" 2>&1 | tail -n 40 >&2 || true
  exit 1
}

ap_id=02-aa-00-00-00-01
ap_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')

# start IMAGE: run dismistd from IMAGE on $data, detached, and wait until it
# is ready (the migration, if any, runs before /readyz answers 200).
start() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker run -d --name "$container" --user "$(id -u):$(id -g)" --workdir /data \
    -v "$data:/data" -p "127.0.0.1:$UI_PORT:8088" -p "127.0.0.1:$WSS_PORT:8443" "$1" \
    -db=/data/controller.db -admin-token-file=/data/admin.token \
    -tls-cert=/data/tls.crt -tls-key=/data/tls.key \
    -ui-listen=0.0.0.0:8088 -ws-listen=0.0.0.0:8443 -dns-listen=127.0.0.1:5353 \
    -controller-ip=192.0.2.1 -metrics-listen= >/dev/null
  for _ in $(seq 1 120); do
    if curl -fsS "http://127.0.0.1:$UI_PORT/readyz" >/dev/null 2>&1; then
      return 0
    fi
    if [ "$(docker inspect -f '{{.State.Running}}' "$container")" != true ]; then
      fail "$1 exited during startup"
    fi
    sleep 1
  done
  fail "$1 was not ready within 2 minutes"
}

stop() {
  docker stop -t 20 "$container" >/dev/null
  docker rm "$container" >/dev/null
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

# fetch NAME: the config the fake AP is served, retried while the
# controller registers the AP.
fetch() {
  for _ in $(seq 1 30); do
    if "$FAKEAP" -url "wss://127.0.0.1:$WSS_PORT/ws" -id "$ap_id" -secret "$ap_secret" -insecure -once >"$work/$1.json" 2>"$work/$1.err"; then
      return 0
    fi
    sleep 1
  done
  cat "$work/$1.err" >&2
  fail "fake AP could not fetch its config ($1)"
}

json_field() { python3 -c "import json,sys; print(json.load(sys.stdin)$1)"; }

for previous in $PREVIOUS_IMAGES; do
  step "previous release $previous against $NEW_IMAGE"
  data=$work/data-$(echo "$previous" | tr -c 'A-Za-z0-9' '_')
  mkdir -m 700 "$data"
  docker run --rm --user "$(id -u):$(id -g)" -v "$data:/data" --entrypoint /usr/local/bin/dismist "$previous" init --data-dir /data >/dev/null

  step "seed a schema-1 database with $previous"
  start "$previous"
  login
  site=$(api POST /api/sites '{"name":"Example Home","country":"AU","timezone":"Australia/Sydney"}' | json_field "['site']['id']")
  network=$(api POST "/api/sites/$site/networks" '{"ssid":"Example","psk":"example-passphrase-1","vlan_id":1,"enabled":true}' |
    json_field "['networks'][0]['id']")
  api PUT "/api/sites/$site/networks/$network/advanced" \
    '{"patch":{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}}' >/dev/null
  api PUT "/api/sites/$site/advanced" '{"patch":{"Radios":[
      {"Band":"2.4","CCRev":"AU/890","CCRevSP":"AU/890","Channel":1,"Width":20,"Power":12,"RRM":{"Channel":1,"Width":20,"Power":12}},
      {"Band":"5","CCRev":"AU/890","CCRevSP":"AU/890","Channel":149,"Width":80,"Power":17,"RRM":{"Channel":149,"Width":80,"Power":17}}],
    "Monitor":{"CCRev":"AU/890"}}}' >/dev/null
  api POST /api/aps "{\"id\":\"$ap_id\",\"name\":\"Lounge AP\",\"site_id\":\"$site\",\"secret\":\"$ap_secret\"}" >/dev/null
  fetch before
  stop
  "$MIGRATIONCOMPAT" state "$data/controller.db"

  step "1. $NEW_IMAGE migrates and serves the same effective config"
  start "$NEW_IMAGE"
  fetch migrated
  stop
  "$MIGRATIONCOMPAT" state "$data/controller.db" | tee "$work/state.json"
  compat=$(json_field "['compat']" <"$work/state.json")
  [ "$compat" = 1 ] || fail "the migration raised meta.compat to $compat; a rollback by image tag would refuse to start"
  "$MIGRATIONCOMPAT" equal "$work/before.json" "$work/migrated.json" || fail "the migrated config differs"
  # A binary with the schema 2 migration must commit it for this fixture
  # (the live situation, design §4.7); legacy mode would leave typed
  # settings paused on a real install.
  migration=$(python3 -c "import json,sys; print((json.load(sys.stdin).get('migration_state') or {}).get('state', 'not_run'))" <"$work/state.json")
  schema=$(json_field "['schema']" <"$work/state.json")
  if [ "$schema" -ge 2 ] && [ "$migration" != committed ]; then
    fail "schema $schema but migration state $migration"
  fi
  echo "schema $schema, migration $migration"

  step "2. $previous still runs the migrated database and serves what it did"
  start "$previous"
  fetch rolled-back
  "$MIGRATIONCOMPAT" equal "$work/before.json" "$work/rolled-back.json" || fail "the rolled-back config differs"

  step "3. after $previous writes, $NEW_IMAGE re-migrates"
  login
  api PUT "/api/aps/$ap_id" '{"name":"Lounge AP 2"}' >/dev/null
  fetch renamed
  stop
  if "$MIGRATIONCOMPAT" equal "$work/before.json" "$work/renamed.json" >/dev/null 2>&1; then
    fail "the rename did not change the served config, so this step would prove nothing"
  fi
  start "$NEW_IMAGE"
  fetch remigrated
  stop
  "$MIGRATIONCOMPAT" state "$data/controller.db"
  "$MIGRATIONCOMPAT" equal "$work/renamed.json" "$work/remigrated.json" || fail "the re-migrated config differs from what $previous served"

  step "4. with meta.compat = 2 the M0 image ($M0_IMAGE) refuses to start"
  "$MIGRATIONCOMPAT" set-compat "$data/controller.db" 2
  set +e
  docker run --rm --user "$(id -u):$(id -g)" --workdir /data -v "$data:/data" "$M0_IMAGE" \
    -db=/data/controller.db -admin-token-file=/data/admin.token \
    -tls-cert=/data/tls.crt -tls-key=/data/tls.key -ui-listen=127.0.0.1:8088 \
    -ws-listen=127.0.0.1:8443 -dns-listen=127.0.0.1:5353 -controller-ip=192.0.2.1 -metrics-listen= \
    >"$work/compat2.log" 2>&1
  status=$?
  set -e
  [ "$status" = 78 ] || { cat "$work/compat2.log" >&2; fail "$M0_IMAGE exited $status on a compat 2 database, want 78"; }
  grep -q "controller.db needs dismist schema 2" "$work/compat2.log" || { cat "$work/compat2.log" >&2; fail "no restore message"; }
  echo "M0 refused the compat 2 database with exit 78"
done

echo
echo "migration-compat passed"
