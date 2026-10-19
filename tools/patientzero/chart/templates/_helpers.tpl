{{- define "patientzero.labels" -}}
app.kubernetes.io/name: patientzero
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "patientzero.selectorLabels" -}}
app.kubernetes.io/name: patientzero
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
