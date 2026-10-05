{{/*
Expand the name of the chart.
*/}}
{{- define "protonpass-server.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "protonpass-server.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "protonpass-server.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "protonpass-server.labels" -}}
helm.sh/chart: {{ include "protonpass-server.chart" . }}
{{ include "protonpass-server.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "protonpass-server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "protonpass-server.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Images: the tag defaults to the chart appVersion.
*/}}
{{- define "protonpass-server.gatewayImage" -}}
{{ .Values.gateway.image.repository }}:{{ .Values.gateway.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "protonpass-server.brokerImage" -}}
{{ .Values.broker.image.repository }}:{{ .Values.broker.image.tag | default .Chart.AppVersion }}
{{- end }}
