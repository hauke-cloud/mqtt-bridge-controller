{{/*
Expand the name of the chart.
*/}}
{{- define "mqtt-bridge-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "mqtt-bridge-controller.fullname" -}}
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

{{/*
Create chart label.
*/}}
{{- define "mqtt-bridge-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "mqtt-bridge-controller.labels" -}}
helm.sh/chart: {{ include "mqtt-bridge-controller.chart" . }}
{{ include "mqtt-bridge-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "mqtt-bridge-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mqtt-bridge-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "mqtt-bridge-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "mqtt-bridge-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
TLS secret name.
*/}}
{{- define "mqtt-bridge-controller.tlsSecretName" -}}
{{- if .Values.tls.existingSecret }}
{{- .Values.tls.existingSecret }}
{{- else }}
{{- include "mqtt-bridge-controller.fullname" . }}-tls
{{- end }}
{{- end }}
