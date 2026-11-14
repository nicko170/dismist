{{- define "dismist.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "dismist.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "dismist.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "dismist.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dismist.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "dismist.labels" -}}
helm.sh/chart: {{ include "dismist.chart" . }}
{{ include "dismist.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "dismist.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
{{- end }}

{{/* Secret holding the AP WSS certificate, or "" when it comes from privateSecret. */}}
{{- define "dismist.tlsSecretName" -}}
{{- if .Values.tls.secretName }}
{{- .Values.tls.secretName }}
{{- else if .Values.tls.certificate.enabled }}
{{- printf "%s-ap-tls" (include "dismist.fullname" .) }}
{{- end }}
{{- end }}

{{- define "dismist.pvcName" -}}
{{- default (printf "%s-state" (include "dismist.fullname" .)) .Values.persistence.existingClaim }}
{{- end }}

{{/*
Resolve one monitoring part (metrics.service, serviceMonitor, prometheusRule,
dashboards): null follows monitoring.enabled, true or false wins. Call with
(dict "value" <the part's enabled> "root" $). Renders "true" or nothing.
*/}}
{{- define "dismist.monitoringPart" -}}
{{- $value := .value -}}
{{- if kindIs "invalid" $value -}}
{{- $value = .root.Values.monitoring.enabled -}}
{{- end -}}
{{- if kindIs "string" $value -}}
{{- $value = eq (lower $value) "true" -}}
{{- end -}}
{{- if $value -}}true{{- end -}}
{{- end }}

{{/* The ServiceMonitor part. */}}
{{- define "dismist.serviceMonitorEnabled" -}}
{{- include "dismist.monitoringPart" (dict "value" .Values.metrics.serviceMonitor.enabled "root" .) -}}
{{- end }}

{{/* The metrics Service: its own switch, or implied by the ServiceMonitor. */}}
{{- define "dismist.metricsServiceEnabled" -}}
{{- if or (include "dismist.monitoringPart" (dict "value" .Values.metrics.service.enabled "root" .)) (include "dismist.serviceMonitorEnabled" .) -}}true{{- end -}}
{{- end }}

{{/*
Whether /metrics listens beyond the pod's loopback: a metrics Service, a
ServiceMonitor or scrape annotations need it.
*/}}
{{- define "dismist.metricsExposed" -}}
{{- if or (include "dismist.metricsServiceEnabled" .) .Values.metrics.scrapeAnnotations -}}true{{- end -}}
{{- end }}

{{/* The metrics bearer token is mounted when metrics.auth.existingSecret is set. */}}
{{- define "dismist.metricsToken" -}}
{{- if and .Values.metrics.enabled .Values.metrics.auth.existingSecret -}}true{{- end -}}
{{- end }}

{{/* dismistd -metrics-* arguments. */}}
{{- define "dismist.metricsArgs" -}}
{{- if not .Values.metrics.enabled }}
- -metrics-listen=
{{- else }}
{{- if include "dismist.metricsExposed" . }}
- -metrics-listen=:{{ .Values.metrics.port }}
- -metrics-allow-remote
{{- else if kindIs "invalid" .Values.controller.metricsListen }}
- -metrics-listen=127.0.0.1:{{ .Values.metrics.port }}
{{- else }}
- -metrics-listen={{ .Values.controller.metricsListen }}
{{- end }}
{{- if include "dismist.metricsToken" . }}
- -metrics-token-file=/private/metrics.token
{{- end }}
{{- if .Values.metrics.includeNames }}
- -metrics-names
{{- end }}
{{- if ne (toString .Values.metrics.clients) "aggregate" }}
- -metrics-clients={{ .Values.metrics.clients }}
{{- end }}
{{- if ne (int .Values.metrics.clientLimit) 100 }}
- -metrics-client-limit={{ int .Values.metrics.clientLimit }}
{{- end }}
{{- end }}
{{- end }}

{{/* Refuse metrics settings that cannot work or would expose more than asked. */}}
{{- define "dismist.validateMetrics" -}}
{{- $exposed := include "dismist.metricsExposed" . -}}
{{- if and $exposed (not .Values.metrics.enabled) -}}
{{- fail "metrics.service, metrics.serviceMonitor, metrics.scrapeAnnotations and monitoring.enabled need metrics.enabled=true" -}}
{{- end -}}
{{- if and $exposed .Values.hostNetwork (not .Values.metrics.allowHostNetwork) -}}
{{- fail "with hostNetwork, exposing metrics publishes /metrics on the node's addresses; set metrics.allowHostNetwork=true to accept that, or turn metrics exposure off" -}}
{{- end -}}
{{- if not (has (toString .Values.metrics.clients) (list "off" "aggregate" "per-client")) -}}
{{- fail "metrics.clients must be off, aggregate or per-client" -}}
{{- end -}}
{{- if and .Values.metrics.enabled (has (int .Values.metrics.port) (list (int .Values.listen.ui) (int .Values.listen.wss) (int .Values.listen.dns))) -}}
{{- fail "metrics.port must differ from listen.ui, listen.wss and listen.dns" -}}
{{- end -}}
{{- end }}

{{/*
dismist.byteSize NAME VALUE: VALUE as a -backup-* size flag value. dismistd
takes whole numbers with an optional K/M/G/T (1000) or Ki/Mi/Gi/Ti (1024)
suffix; anything else (a decimal, an E/P suffix) is refused here rather
than by the pod at start.
*/}}
{{- define "dismist.byteSize" -}}
{{- $name := index . 0 -}}
{{- $value := toString (index . 1) -}}
{{- if not (regexMatch "^[0-9]+(Ki|Mi|Gi|Ti|K|k|M|G|T)?$" $value) -}}
{{- fail (printf "%s must be a whole number of bytes with an optional K/M/G/T or Ki/Mi/Gi/Ti suffix, not %q" $name $value) -}}
{{- end -}}
{{- $value -}}
{{- end }}

{{/* The -backup-* flags (design §6.4). */}}
{{- define "dismist.backupArgs" -}}
{{- $interval := toString (.Values.backup.interval | default "0") }}
{{- if not (regexMatch `^(([0-9]+(\.[0-9]+)?(h|m|s|ms))+|0)$` $interval) }}
{{- fail (printf `backup.interval must be a Go duration such as 24h or 12h30m, or "" to disable snapshots, not %q` $interval) }}
{{- end }}
- -backup-interval={{ $interval }}
{{- $keep := .Values.backup.keep }}
{{- if not (and (or (kindIs "float64" $keep) (kindIs "int64" $keep) (kindIs "int" $keep)) (ge (float64 $keep) 1.0) (eq (float64 $keep) (float64 (int $keep)))) }}
{{- fail (printf "backup.keep must be a whole number of at least 1, not %v" $keep) }}
{{- end }}
- -backup-keep={{ int $keep }}
{{- with .Values.backup.maxBytes }}
- -backup-max-bytes={{ include "dismist.byteSize" (list "backup.maxBytes" .) }}
{{- end }}
{{- with .Values.backup.tmpSizeLimit }}
- -backup-tmp-max={{ include "dismist.byteSize" (list "backup.tmpSizeLimit" .) }}
{{- end }}
{{- end }}
