{{/* Chart name and full release name (≤ 63 chars). */}}
{{- define "odb.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "odb.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "odb.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "odb.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Selector labels for one component: api, worker or all. Usage: include "odb.selector" (list $ "api") */}}
{{- define "odb.selector" -}}
{{- $ := index . 0 -}}
app.kubernetes.io/name: {{ include "odb.name" $ }}
app.kubernetes.io/instance: {{ $.Release.Name }}
app.kubernetes.io/component: {{ index . 1 }}
{{- end }}

{{/* The components this mode deploys. */}}
{{- define "odb.components" -}}
{{- if eq .Values.mode "all" }}all{{ else if eq .Values.mode "split" }}api worker{{ else }}{{ fail "mode must be split or all" }}{{ end }}
{{- end }}

{{/* The component serving HTTP traffic. */}}
{{- define "odb.webComponent" -}}
{{- if eq .Values.mode "all" }}all{{ else }}api{{ end }}
{{- end }}

{{- define "odb.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "odb.secretName" -}}
{{- default (include "odb.fullname" .) .Values.secrets.existingSecret }}
{{- end }}

{{- define "odb.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}{{ default (include "odb.fullname" .) .Values.serviceAccount.name }}{{ else }}{{ default "default" .Values.serviceAccount.name }}{{ end }}
{{- end }}

{{/* Settings shared by every pod and the migration hook. */}}
{{- define "odb.env" -}}
- name: OC_HTTP_ADDR
  value: ":8080"
- name: OC_METRICS_ADDR
  value: ":9090"
- name: OC_LOG_FORMAT
  value: json
- name: OC_LOG_LEVEL
  value: {{ .Values.config.logLevel | quote }}
- name: OC_PUBLIC_URL
  value: {{ required "publicUrl is required" .Values.publicUrl | quote }}
- name: OC_SIGNUP_ENABLED
  value: {{ .Values.config.signupEnabled | quote }}
- name: OC_SESSION_COOKIE_SECURE
  value: {{ hasPrefix "https://" .Values.publicUrl | quote }}
- name: OC_META_CONNSTR
  value: {{ required "metadata.connStr is required" .Values.metadata.connStr | quote }}
- name: OC_META_USERNAME
  value: {{ required "metadata.username is required" .Values.metadata.username | quote }}
- name: OC_META_BUCKET
  value: {{ .Values.metadata.bucket | quote }}
- name: OC_META_SCOPE
  value: {{ .Values.metadata.scope | quote }}
- name: OC_META_CAPELLA
  value: {{ .Values.metadata.capella | quote }}
- name: OC_ALLOW_PRIVATE_NETWORKS
  value: {{ .Values.config.egress.allowPrivateNetworks | quote }}
{{- with .Values.config.egress.allowCidrs }}
- name: OC_EGRESS_ALLOW_CIDRS
  value: {{ . | quote }}
{{- end }}
{{- with .Values.config.egress.denyCidrs }}
- name: OC_EGRESS_DENY_CIDRS
  value: {{ . | quote }}
{{- end }}
- name: OC_AUDIT_RETENTION_DAYS
  value: {{ .Values.config.auditRetentionDays | quote }}
- name: OC_MASTER_KEY_FILE
  value: /etc/odb/secrets/master-key
- name: OC_META_PASSWORD_FILE
  value: /etc/odb/secrets/meta-password
{{- with .Values.config.otel.endpoint }}
- name: OC_OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ . | quote }}
- name: OC_OTEL_SAMPLE_RATIO
  value: {{ $.Values.config.otel.sampleRatio | quote }}
{{- end }}
{{- range $k, $v := .Values.config.extraEnv }}
- name: {{ $k }}
  value: {{ $v | quote }}
{{- end }}
{{- end }}

{{/* The directory target's claim: an existing one, or the chart's own (pvc.yaml). */}}
{{- define "odb.outputClaim" -}}
{{- .Values.directoryTarget.persistence.existingClaim | default (printf "%s-output" (include "odb.fullname" .)) -}}
{{- end -}}

{{/* Several worker pods writing one folder need a ReadWriteMany claim (OC-038 AC5). */}}
{{- define "odb.checkOutput" -}}
{{- $d := .Values.directoryTarget -}}
{{- if and $d.enabled (eq .Values.mode "split") (not $d.persistence.existingClaim) (ne $d.persistence.accessMode "ReadWriteMany") -}}
{{- if or (gt (int .Values.worker.replicas) 1) .Values.worker.autoscaling.enabled -}}
{{- fail "directoryTarget: with mode split and more than one worker, set directoryTarget.persistence.accessMode: ReadWriteMany (or an existingClaim that is ReadWriteMany)" -}}
{{- end -}}
{{- end -}}
{{- end -}}
