{{/* Chart name. */}}
{{- define "xi.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Full name: release name, plus the chart name unless the release already contains it. */}}
{{- define "xi.fullname" -}}
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

{{- define "xi.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xi.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "xi.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "xi.selectorLabels" . }}
app.kubernetes.io/component: {{ .Values.role }}
app.kubernetes.io/version: {{ include "xi.imageTag" . | trunc 63 | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: xtandard-image
{{- end -}}

{{- define "xi.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "xi.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "xi.imageTag" -}}
{{- default (printf "%s-%s" .Chart.AppVersion .Values.image.flavor) .Values.image.tag -}}
{{- end -}}

{{- define "xi.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (include "xi.imageTag" .) -}}
{{- end -}}
{{- end -}}

{{/* "true" when this release runs video transformations. */}}
{{- define "xi.videoEnabled" -}}
{{- if eq .Values.role "video" -}}true
{{- else if and (eq .Values.role "all") .Values.video.enabled -}}true
{{- end -}}
{{- end -}}

{{- define "xi.shutdownTimeout" -}}
{{- if .Values.shutdown.timeout -}}{{ .Values.shutdown.timeout }}
{{- else if include "xi.videoEnabled" . -}}150s
{{- else -}}30s
{{- end -}}
{{- end -}}

{{/* Seconds of a Go duration made of h/m/s parts ("1m30s", "150s"). */}}
{{- define "xi.durationSeconds" -}}
{{- $d := . -}}
{{- $total := 0 -}}
{{- range $part := regexFindAll "[0-9]+[hms]" $d -1 -}}
{{- $n := int (trimSuffix "h" (trimSuffix "m" (trimSuffix "s" $part))) -}}
{{- if hasSuffix "h" $part -}}{{- $total = add $total (mul $n 3600) -}}
{{- else if hasSuffix "m" $part -}}{{- $total = add $total (mul $n 60) -}}
{{- else -}}{{- $total = add $total $n -}}
{{- end -}}
{{- end -}}
{{- $total -}}
{{- end -}}

{{- define "xi.terminationGracePeriod" -}}
{{- if not (kindIs "invalid" .Values.terminationGracePeriodSeconds) -}}
{{- .Values.terminationGracePeriodSeconds -}}
{{- else -}}
{{- add (include "xi.durationSeconds" .Values.shutdown.delay | int) (include "xi.durationSeconds" (include "xi.shutdownTimeout" .) | int) 10 -}}
{{- end -}}
{{- end -}}

{{/*
GOMEMLIMIT override: goMemLimit, or with "auto" 30 % of
resources.limits.memory when it is written in Mi or Gi. Empty (the default)
sets none and the server derives it from the cgroup limit
(XI_PROCESSING_GO_MEMORY_FRACTION, docs/OPERATIONS.md §1.3).
*/}}
{{- define "xi.goMemLimit" -}}
{{- $v := .Values.goMemLimit | toString -}}
{{- if ne $v "auto" -}}{{ $v }}
{{- else -}}
{{- $m := "" -}}
{{- with .Values.resources.limits }}{{ $m = (get . "memory") | toString }}{{ end -}}
{{- if regexMatch "^[0-9]+Gi$" $m -}}{{ div (mul (trimSuffix "Gi" $m | int) 1024 3) 10 }}MiB
{{- else if regexMatch "^[0-9]+Mi$" $m -}}{{ div (mul (trimSuffix "Mi" $m | int) 3) 10 }}MiB
{{- end -}}
{{- end -}}
{{- end -}}

{{/* PromQL matchers selecting this release's series. */}}
{{- define "xi.promSelector" -}}
{{- default (printf "namespace=%q,service=%q" .Release.Namespace (include "xi.fullname" .)) .Values.autoscaling.keda.selector -}}
{{- end -}}
