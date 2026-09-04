{{- define "agentgateway.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
The resource name.

Kept equal to the chart name in the common case rather than the usual
"<release>-<chart>": the platform's registration names the Service to route to,
and a name that changes with the release is a gateway 500 with healthy pods.
*/}}
{{- define "agentgateway.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- include "agentgateway.name" . -}}
{{- end -}}
{{- end -}}

{{- define "agentgateway.labels" -}}
app.kubernetes.io/name: {{ include "agentgateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "agentgateway.selectorLabels" -}}
app.kubernetes.io/name: {{ include "agentgateway.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
