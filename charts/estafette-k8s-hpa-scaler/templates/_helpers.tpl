{{- define "labels" }}
labels:
  app: {{.Chart.Name | quote}}
  version: {{.Chart.Version | quote}}
  appVersion: {{.Chart.AppVersion | quote}}
  {{- if .Values.extraLabels }}
{{ toYaml .Values.extraLabels | indent 2 }}
  {{- end }}
{{- end }}