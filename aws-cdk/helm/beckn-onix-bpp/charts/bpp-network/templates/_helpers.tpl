{{/*
Expand the name of the chart or use a provided override.
*/}}
{{- define "common.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name, with truncation to 63 characters.
*/}}
{{- define "common.fullname" -}}
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
Generate a chart name and version label.
*/}}
{{- define "common.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels to be used in all charts.
*/}}
{{- define "common.labels" -}}
helm.sh/chart: {{ include "common.chart" . }}
{{ include "common.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/release: {{ .Release.Revision | quote }}
{{- end }}

{{/*
Common selector labels.
*/}}
{{- define "common.selectorLabels" -}}
app.kubernetes.io/name: {{ include "common.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Helper for creating service account names.
*/}}
{{- define "common.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "common.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Helper for image names and tags.
*/}}
{{- define "common.image" -}}
{{ printf "%s:%s" .Values.image.repository .Values.image.tag }}
{{- end }}

{{/*
Helper for constructing resource names with prefixes or suffixes.
*/}}
{{- define "common.resourceName" -}}
{{- printf "%s-%s" (include "common.fullname" .) .Values.suffix | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "getSecretValue" -}}
{{- $secretName := .secretName -}}
{{- $namespace := .namespace -}}
{{- $key := .key -}}
{{- $secret := (lookup "v1" "Secret" $namespace $secretName) -}}
{{- if $secret -}}
{{- $data := $secret.data -}}
{{- if $data -}}
{{- $value := index $data $key | b64dec -}}
{{- $value -}}
{{- else -}}
{{- printf "Error: Secret data for %s not found" $key -}}
{{- end -}}
{{- else -}}
{{- printf "Error: Secret %s not found in namespace %s" $secretName $namespace -}}
{{- end -}}
{{- end -}}


