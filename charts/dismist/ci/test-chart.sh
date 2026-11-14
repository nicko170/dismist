#!/usr/bin/env bash
# Chart checks for CI (and local runs): render the chart over a matrix of
# values, assert what each case renders, and validate every render with
# kubeconform, using the datreeio CRD catalogue for the Prometheus Operator,
# cert-manager and Gateway API kinds.
#
# Needs helm, yq (mikefarah v4) and kubeconform. SKIP_KUBECONFORM=1 skips
# schema validation (for example offline).
set -euo pipefail

chart=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

crd_schemas='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'

fail() {
  echo "FAIL [$case_name] $*" >&2
  failures=$((failures + 1))
}

# render NAME HELM-ARGS...: render the chart into $work/NAME.yaml.
render() {
  case_name=$1
  shift
  if ! helm template dismist "$chart" --namespace dismist "$@" >"$work/$case_name.yaml" 2>"$work/$case_name.err"; then
    fail "helm template failed: $(cat "$work/$case_name.err")"
    : >"$work/$case_name.yaml"
    return
  fi
  if [ -z "${SKIP_KUBECONFORM:-}" ]; then
    if ! kubeconform -strict -summary -schema-location default -schema-location "$crd_schemas" \
      "$work/$case_name.yaml" >"$work/$case_name.kubeconform" 2>&1; then
      fail "kubeconform: $(cat "$work/$case_name.kubeconform")"
    fi
  fi
}

# render_fails NAME MESSAGE HELM-ARGS...: the chart must refuse these values.
render_fails() {
  case_name=$1
  local message=$2
  shift 2
  if helm template dismist "$chart" --namespace dismist "$@" >/dev/null 2>"$work/$case_name.err"; then
    fail "rendered, but should fail with: $message"
  elif ! grep -qF -- "$message" "$work/$case_name.err"; then
    fail "failed without \"$message\": $(cat "$work/$case_name.err")"
  fi
}

# q EXPR: evaluate a yq expression over every document of the current render.
q() {
  yq ea "$1" "$work/$case_name.yaml"
}

# count KIND [NAME]: how many resources of KIND (named NAME) were rendered.
count() {
  if [ $# -eq 2 ]; then
    q "[select(.kind == \"$1\" and .metadata.name == \"$2\")] | length"
  else
    q "[select(.kind == \"$1\")] | length"
  fi
}

expect_count() {
  local want=$1
  shift
  local got
  got=$(count "$@")
  [ "$got" = "$want" ] || fail "$* rendered $got times, want $want"
}

args() {
  q 'select(.kind == "Deployment") | .spec.template.spec.containers[0].args[]'
}

# The args are captured first: grep -q can exit before yq finishes writing,
# and pipefail would then report the match as a failure.
expect_arg() {
  local all
  all=$(args)
  grep -qxF -- "$1" <<<"$all" || fail "dismistd args lack $1: $(tr '\n' ' ' <<<"$all")"
}

expect_no_arg() {
  local all
  all=$(args)
  if grep -q -- "^$1" <<<"$all"; then fail "dismistd args have $1: $(tr '\n' ' ' <<<"$all")"; fi
}

expect_eq() {
  local got
  got=$(q "$1")
  [ "$got" = "$2" ] || fail "$1 = $got, want $2"
}

dashboards=0
if compgen -G "$chart/monitoring/dashboards/*.json" >/dev/null; then dashboards=1; fi

helm lint "$chart" --set privateSecret.name=private >/dev/null || { case_name=lint; fail "helm lint"; }
for values in "$chart"/ci/*-values.yaml; do
  helm lint "$chart" -f "$values" >/dev/null || { case_name=lint; fail "helm lint -f $values"; }
done

# Defaults: no monitoring objects, metrics on the pod loopback, /readyz
# readiness, a 5-minute startup budget and a /tmp emptyDir.
render default --set privateSecret.name=private
expect_count 0 Service dismist-metrics
expect_count 0 ServiceMonitor
expect_count 0 PrometheusRule
expect_count 0 ConfigMap
expect_count 0 NetworkPolicy
expect_arg -metrics-listen=127.0.0.1:9090
expect_no_arg -metrics-allow-remote
expect_no_arg -metrics-token-file
expect_no_arg -metrics-names
expect_no_arg -metrics-clients
expect_no_arg -metrics-client-limit
expect_no_arg -ap-features
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].ports[] | select(.name == "metrics") | .name' ""
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].readinessProbe.httpGet.path' /readyz
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].startupProbe.httpGet.path' /healthz
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].startupProbe.periodSeconds * .spec.template.spec.containers[0].startupProbe.failureThreshold' 300
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].livenessProbe.httpGet.path' /healthz
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.volumes[] | select(.name == "tmp") | .emptyDir.sizeLimit' 1Gi
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].volumeMounts[] | select(.name == "tmp") | .mountPath' /tmp
expect_eq 'select(.kind == "Deployment") | .spec.template.metadata.annotations | has("prometheus.io/scrape")' false

# monitoring.enabled turns on every part.
render monitoring --set privateSecret.name=private --set monitoring.enabled=true
expect_count 1 Service dismist-metrics
expect_count 1 ServiceMonitor dismist
expect_count 1 PrometheusRule dismist
expect_count "$dashboards" ConfigMap dismist-dashboards
expect_count 0 NetworkPolicy
expect_arg -metrics-listen=:9090
expect_arg -metrics-allow-remote
expect_no_arg -metrics-token-file
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].ports[] | select(.name == "metrics") | .containerPort' 9090
expect_eq 'select(.kind == "Service" and .metadata.name == "dismist-metrics") | .spec.ports[0].targetPort' metrics
expect_eq 'select(.kind == "Service" and .metadata.name == "dismist-metrics") | .metadata.labels["app.kubernetes.io/component"]' metrics
expect_eq 'select(.kind == "ServiceMonitor") | .spec.selector.matchLabels["app.kubernetes.io/component"]' metrics
expect_eq 'select(.kind == "ServiceMonitor") | .spec.namespaceSelector.matchNames[0]' dismist
expect_eq 'select(.kind == "ServiceMonitor") | .spec.endpoints[0].port' metrics
expect_eq 'select(.kind == "ServiceMonitor") | .spec.endpoints[0].relabelings[0].replacement' dismist
expect_eq 'select(.kind == "ServiceMonitor") | .spec.endpoints[0] | has("authorization")' false
expect_eq 'select(.kind == "ServiceMonitor") | .spec | has("sampleLimit")' false
expect_eq 'select(.kind == "ServiceMonitor") | .metadata.labels | has("release")' false
# The ServiceMonitor must select only the metrics Service, never the UI or AP one.
expect_eq '[select(.kind == "Service" and .metadata.labels["app.kubernetes.io/component"] == "metrics")] | length' 1
# shellcheck disable=SC2016 # a literal $labels template, not a shell variable
expect_eq 'select(.kind == "PrometheusRule") | .spec.groups[0].rules[0].annotations.summary | test("\{\{ \$labels.ap \}\}")' true
if [ "$dashboards" = 1 ]; then
  expect_eq 'select(.kind == "ConfigMap" and .metadata.name == "dismist-dashboards") | .metadata.labels.grafana_dashboard' 1
  expect_eq 'select(.kind == "ConfigMap" and .metadata.name == "dismist-dashboards") | .metadata.annotations.grafana_folder' Dismist
fi

# Monitoring on with alerts off: everything except the PrometheusRule.
render no-alerts -f "$chart/ci/monitoring-no-alerts-values.yaml"
expect_count 1 Service dismist-metrics
expect_count 1 ServiceMonitor dismist
expect_count 0 PrometheusRule
expect_count "$dashboards" ConfigMap dismist-dashboards
expect_arg -metrics-names
expect_eq 'select(.kind == "ServiceMonitor") | .spec.sampleLimit' 20000

# Each part can be switched on alone; a ServiceMonitor implies its Service.
render servicemonitor-only --set privateSecret.name=private --set metrics.serviceMonitor.enabled=true --set metrics.service.enabled=false
expect_count 1 Service dismist-metrics
expect_count 1 ServiceMonitor
expect_count 0 PrometheusRule
expect_count 0 ConfigMap
expect_arg -metrics-allow-remote
render service-only --set privateSecret.name=private --set metrics.service.enabled=true
expect_count 1 Service dismist-metrics
expect_count 0 ServiceMonitor
expect_arg -metrics-listen=:9090
render rules-only --set privateSecret.name=private --set metrics.prometheusRule.enabled=true
expect_count 0 Service dismist-metrics
expect_count 1 PrometheusRule
expect_arg -metrics-listen=127.0.0.1:9090
render parts-off --set privateSecret.name=private --set monitoring.enabled=true \
  --set metrics.service.enabled=false --set metrics.serviceMonitor.enabled=false \
  --set metrics.prometheusRule.enabled=false --set metrics.dashboards.enabled=false
expect_count 0 Service dismist-metrics
expect_count 0 ServiceMonitor
expect_count 0 PrometheusRule
expect_count 0 ConfigMap
expect_arg -metrics-listen=127.0.0.1:9090

# A bearer token: copied into /private by the init container, passed to
# dismistd and sent by the ServiceMonitor.
render token --set privateSecret.name=private --set monitoring.enabled=true --set metrics.auth.existingSecret=dismist-metrics --set metrics.auth.key=scrape
expect_arg -metrics-token-file=/private/metrics.token
expect_eq 'select(.kind == "ServiceMonitor") | .spec.endpoints[0].authorization.credentials.name' dismist-metrics
expect_eq 'select(.kind == "ServiceMonitor") | .spec.endpoints[0].authorization.credentials.key' scrape
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.volumes[] | select(.name == "metrics-auth") | .secret.items[0].key' scrape
expect_eq 'select(.kind == "Deployment") | .spec.template.spec.initContainers[0].command[2] | test("cp -L /bootstrap-metrics/token /private/metrics.token")' true

# The deprecated controller.metricsListen applies only while metrics are not exposed.
render legacy-listen --set privateSecret.name=private --set controller.metricsListen=127.0.0.1:9191
expect_arg -metrics-listen=127.0.0.1:9191
render legacy-disable --set privateSecret.name=private --set controller.metricsListen=
expect_arg -metrics-listen=
render legacy-ignored --set privateSecret.name=private --set controller.metricsListen=127.0.0.1:9191 --set monitoring.enabled=true
expect_arg -metrics-listen=:9090

# metrics.enabled=false disables the listener; exposure then cannot work.
render metrics-off --set privateSecret.name=private --set metrics.enabled=false
expect_arg -metrics-listen=
expect_count 0 Service dismist-metrics
render_fails metrics-off-exposed "need metrics.enabled=true" --set privateSecret.name=private --set metrics.enabled=false --set monitoring.enabled=true

# hostNetwork would publish /metrics on the node: refused unless allowed.
render_fails hostnetwork "metrics.allowHostNetwork" --set privateSecret.name=private --set hostNetwork=true --set monitoring.enabled=true
render hostnetwork-allowed --set privateSecret.name=private --set hostNetwork=true --set monitoring.enabled=true --set metrics.allowHostNetwork=true
expect_arg -metrics-allow-remote
render hostnetwork-loopback --set privateSecret.name=private --set hostNetwork=true
expect_arg -metrics-listen=127.0.0.1:9090

# Other refusals.
render_fails port-clash "metrics.port must differ" --set privateSecret.name=private --set metrics.port=8088
render_fails client-mode "metrics.clients must be" --set privateSecret.name=private --set metrics.clients=all

# Scrape annotations expose the listener without a Service.
render annotations --set privateSecret.name=private --set metrics.scrapeAnnotations=true
expect_count 0 Service dismist-metrics
expect_arg -metrics-allow-remote
expect_eq 'select(.kind == "Deployment") | .spec.template.metadata.annotations["prometheus.io/port"]' 9090

# NetworkPolicy: one open rule for the service ports, plus the metrics rule
# only when metrics are exposed.
render netpol --set privateSecret.name=private --set metrics.networkPolicy.enabled=true
expect_eq 'select(.kind == "NetworkPolicy") | .spec.ingress | length' 1
render netpol-exposed --set privateSecret.name=private --set metrics.networkPolicy.enabled=true --set monitoring.enabled=true
expect_eq 'select(.kind == "NetworkPolicy") | .spec.ingress | length' 2
expect_eq 'select(.kind == "NetworkPolicy") | .spec.ingress[1].ports[0].port' metrics

# Everything on: every resource kind validated against its schema.
render full -f "$chart/ci/full-values.yaml"
expect_count 1 ServiceMonitor
expect_count 1 PrometheusRule
expect_count 1 NetworkPolicy
expect_count 1 Certificate
expect_count 1 HTTPRoute
expect_arg -metrics-clients=per-client
expect_arg -metrics-client-limit=50
expect_arg -ap-features=example-feature
expect_eq 'select(.kind == "PrometheusRule") | [.spec.groups[].rules[] | select(.alert == "DismistAPFlapping")] | length' 0
expect_eq 'select(.kind == "PrometheusRule") | .spec.groups[].rules[] | select(.alert == "DismistAPSilent") | .labels.severity' info
expect_eq 'select(.kind == "PrometheusRule") | .spec.groups[-1].name' dismist.extra

if [ "$failures" -gt 0 ]; then
  echo "$failures chart check(s) failed" >&2
  exit 1
fi
echo "chart checks passed"
