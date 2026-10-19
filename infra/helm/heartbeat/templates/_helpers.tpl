{{/*
Labels shared by every Heartbeat-owned object. No release revision or
timestamps: Argo CD renders the chart itself and diffs the result (ADR 0005).
*/}}
{{- define "heartbeat.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/part-of: heartbeat
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels for one component. Call with (dict "root" $ "component" "db-collector").
*/}}
{{- define "heartbeat.selectorLabels" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end }}

{{- define "heartbeat.componentLabels" -}}
{{ include "heartbeat.labels" .root }}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Image reference: a digest wins over a tag; the tag defaults to appVersion.
Call with (dict "root" $ "image" .Values.dbCollector.image).
*/}}
{{- define "heartbeat.image" -}}
{{- if .image.digest -}}
{{ .image.repository }}@{{ .image.digest }}
{{- else -}}
{{ .image.repository }}:{{ .image.tag | default .root.Chart.AppVersion }}
{{- end -}}
{{- end }}

{{/* Fixed name of the integrations ConfigMap. */}}
{{- define "heartbeat.integrationsConfigMap" -}}heartbeat-integrations{{- end }}

{{/* The integrations.yaml document as rendered into the ConfigMap. */}}
{{- define "heartbeat.integrationsYaml" -}}
{{ toYaml .Values.integrations }}
{{- end }}

{{/*
GOMEMLIMIT for a Go container: 80% of its memory limit, in whole MiB, so the
garbage collector works harder before the kernel OOM-kills the container; the
rest is for goroutine stacks, the runtime and memory outside the Go heap.
Empty when there is no limit. Call with (dict "limit" $c.resources.limits.memory
"name" "dbCollector").
*/}}
{{- define "heartbeat.goMemLimit" -}}
{{- with .limit -}}
{{- $bytes := 0.0 -}}
{{- if kindIs "string" . -}}
{{- $units := dict "" 1 "k" 1000 "M" 1000000 "G" 1000000000 "T" 1000000000000 "Ki" 1024 "Mi" 1048576 "Gi" 1073741824 "Ti" 1099511627776 -}}
{{- $number := regexFind "^[0-9]+(\\.[0-9]+)?" . -}}
{{- $suffix := trimPrefix $number . -}}
{{- if or (not $number) (not (hasKey $units $suffix)) -}}
{{- fail (printf "%s.resources.limits.memory %q: use a number with an optional k, M, G, T, Ki, Mi, Gi or Ti suffix, or set %s.goMemLimit" $.name . $.name) -}}
{{- end -}}
{{- $bytes = mulf (float64 $number) (get $units $suffix) -}}
{{- else -}}
{{- $bytes = float64 . -}}
{{- end -}}
{{- printf "%dMiB" (int64 (floor (divf (mulf $bytes 0.8) 1048576))) -}}
{{- end -}}
{{- end }}
