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
