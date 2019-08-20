{{- define "commonLabels" -}}
app: {{ .Chart.Name | quote }}
release: {{ .Release.Name }}
chart: {{ .Chart.Name }}-{{ .Chart.Version }}
heritage: {{ .Release.Service }}
{{- if .Values.extraLabels }}
{{ toYaml .Values.extraLabels }}
{{- end }}
{{- end -}}

{{- define "completeLabels" -}}
{{ include "commonLabels" . }}
appVersion: {{.Chart.AppVersion | quote}}
{{- end -}}
