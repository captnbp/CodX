{{/*
Expand the name of the chart.
*/}}
{{- define "codx.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name (release name or overridden).
*/}}
{{- define "codx.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Instance name: defaults to release name, used in workspace object names.
*/}}
{{- define "codx.instanceName" -}}
{{- if .Values.instanceName -}}
{{- .Values.instanceName -}}
{{- else -}}
{{- .Release.Name -}}
{{- end -}}
{{- end -}}

{{/*
Namespace: defaults to the release namespace.
*/}}
{{- define "codx.namespace" -}}
{{- if .Values.namespace -}}
{{- .Values.namespace -}}
{{- else -}}
{{- .Release.Namespace -}}
{{- end -}}
{{- end -}}

{{/*
Service account name.
*/}}
{{- define "codx.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "codx.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "codx.labels" -}}
app.kubernetes.io/name: {{ include "codx.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: server
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{/*
Image reference.
*/}}
{{- define "codx.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
