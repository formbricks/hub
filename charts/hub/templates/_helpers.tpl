{{/*
Expand the name of the chart.
*/}}
{{- define "hub.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "hub.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "hub.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "hub.labels" -}}
helm.sh/chart: {{ include "hub.chart" . }}
app.kubernetes.io/name: {{ include "hub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels
*/}}
{{- define "hub.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hub.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
ServiceAccount name
*/}}
{{- define "hub.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "hub.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
ConfigMap name
*/}}
{{- define "hub.configMapName" -}}
{{- if .Values.config.existingConfigMap -}}
{{- .Values.config.existingConfigMap -}}
{{- else -}}
{{- printf "%s-config" (include "hub.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Secret name
*/}}
{{- define "hub.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secret" (include "hub.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Hub worker resource name and selectors. Worker pods must not match the API
Service selector, because workers do not expose HTTP.
*/}}
{{- define "hub.workerName" -}}
{{- printf "%s-worker" (include "hub.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hub.workerSelectorLabels" -}}
app.kubernetes.io/name: {{ include "hub.workerName" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Hub embeddings runtime resource name and selectors.
*/}}
{{- define "hub.embeddingsName" -}}
{{- printf "%s-embeddings" (include "hub.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hub.embeddingsSelectorLabels" -}}
app.kubernetes.io/name: {{ include "hub.embeddingsName" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Secret used by Hub and the embeddings runtime for the embeddings API key.
*/}}
{{- define "hub.embeddingsSecretName" -}}
{{- default (printf "%s-secret" (include "hub.embeddingsName" .)) .Values.embeddings.auth.existingSecret -}}
{{- end -}}

{{/*
Secret used by the embeddings runtime for Hugging Face access.
*/}}
{{- define "hub.embeddingsHuggingFaceSecretName" -}}
{{- default (include "hub.embeddingsSecretName" .) .Values.embeddings.huggingFace.existingSecret -}}
{{- end -}}

{{/*
Model name Hub sends to the OpenAI-compatible embeddings endpoint.
*/}}
{{- define "hub.embeddingsServedModelName" -}}
{{- default .Values.embeddings.model .Values.embeddings.servedModelName -}}
{{- end -}}

{{/*
OpenAI-compatible embeddings base URL used by Hub.
*/}}
{{- define "hub.embeddingsBaseURL" -}}
{{- if .Values.embeddings.baseUrl -}}
{{- .Values.embeddings.baseUrl -}}
{{- else -}}
{{- printf "http://%s:%v/v1" (include "hub.embeddingsName" .) (.Values.embeddings.service.port | default .Values.embeddings.port) -}}
{{- end -}}
{{- end -}}

{{/*
Embedding API key value for the generated embeddings secret.
*/}}
{{- define "hub.embeddingsApiKey" -}}
{{- $secretName := include "hub.embeddingsSecretName" . }}
{{- $secretKey := .Values.embeddings.auth.secretKey | default "EMBEDDING_PROVIDER_API_KEY" }}
{{- $secret := (lookup "v1" "Secret" .Release.Namespace $secretName) }}
{{- if and $secret (index $secret.data $secretKey) }}
    {{- index $secret.data $secretKey | b64dec -}}
{{- else if .Values.embeddings.auth.apiKey }}
    {{- .Values.embeddings.auth.apiKey -}}
{{- else if .Values.secrets.stringData.EMBEDDING_PROVIDER_API_KEY }}
    {{- .Values.secrets.stringData.EMBEDDING_PROVIDER_API_KEY -}}
{{- else }}
    {{- randAlphaNum 32 -}}
{{- end -}}
{{- end }}

{{/*
Shared Hub embedding env. These values are managed from embeddings when the
self-hosted runtime is enabled so Hub API and Hub worker cannot drift.
*/}}
{{- define "hub.embeddingEnv" -}}
{{- if .Values.embeddings.enabled }}
- name: EMBEDDING_PROVIDER
  value: "openai"
- name: EMBEDDING_MODEL
  value: {{ include "hub.embeddingsServedModelName" . | quote }}
- name: EMBEDDING_BASE_URL
  value: {{ include "hub.embeddingsBaseURL" . | quote }}
- name: EMBEDDING_PROVIDER_API_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "hub.embeddingsSecretName" . }}
      key: {{ .Values.embeddings.auth.secretKey | default "EMBEDDING_PROVIDER_API_KEY" }}
- name: EMBEDDING_MAX_CONCURRENT
  value: {{ .Values.embeddings.maxConcurrent | quote }}
- name: EMBEDDING_NORMALIZE
  value: {{ .Values.embeddings.normalize | quote }}
{{- end }}
{{- end }}

{{/* External embedding client settings, independent of the optional bundled TEI runtime. */}}
{{- define "hub.embeddingClientEnv" -}}
{{- $root := .root -}}
{{- $cfg := $root.Values.embeddingClient -}}
{{- if $cfg.enabled -}}
{{- if $root.Values.embeddings.enabled -}}
{{- fail "embeddingClient.enabled and embeddings.enabled cannot both be true" -}}
{{- end -}}
{{- $provider := required "embeddingClient.provider is required" $cfg.provider -}}
{{- $model := required "embeddingClient.model is required" $cfg.model -}}
{{- if not (has $provider (list "openai" "google" "google-gemini")) -}}
{{- fail "embeddingClient.provider must be openai, google, or google-gemini" -}}
{{- end -}}
{{- $base := $cfg.baseUrl -}}
{{- if eq .role "worker" -}}
{{- $base = default $base $cfg.workerBaseUrl -}}
{{- else -}}
{{- $base = default $base $cfg.apiBaseUrl -}}
{{- end -}}
{{- if and (ne $provider "openai") $base -}}
{{- fail "embeddingClient base URL is supported only for openai" -}}
{{- end -}}
- name: EMBEDDING_PROVIDER
  value: {{ $provider | quote }}
- name: EMBEDDING_MODEL
  value: {{ $model | quote }}
{{- if $cfg.responseModel }}
- name: EMBEDDING_RESPONSE_MODEL
  value: {{ $cfg.responseModel | quote }}
{{- end }}
{{- if $base }}
- name: EMBEDDING_BASE_URL
  value: {{ $base | quote }}
{{- end }}
{{- if ne $provider "google-gemini" }}
- name: EMBEDDING_PROVIDER_API_KEY
  valueFrom:
    secretKeyRef:
      name: {{ required "embeddingClient.auth.existingSecret is required" $cfg.auth.existingSecret }}
      key: {{ $cfg.auth.secretKey | quote }}
{{- else }}
- name: EMBEDDING_GOOGLE_CLOUD_PROJECT
  value: {{ required "embeddingClient.googleCloudProject is required" $cfg.googleCloudProject | quote }}
- name: EMBEDDING_GOOGLE_CLOUD_LOCATION
  value: {{ required "embeddingClient.googleCloudLocation is required" $cfg.googleCloudLocation | quote }}
{{- if $cfg.auth.googleCredentialsSecret }}
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: /var/run/hub-google/credentials.json
{{- end }}
{{- end }}
{{- if eq .role "worker" }}
- name: EMBEDDING_MAX_CONCURRENT
  value: {{ $cfg.maxConcurrent | quote }}
- name: EMBEDDING_JOB_TIMEOUT_SECONDS
  value: {{ $cfg.jobTimeoutSeconds | quote }}
- name: EMBEDDING_BATCH_SIZE
  value: {{ $cfg.batchSize | quote }}
- name: EMBEDDING_BATCH_MAX_WAIT_MS
  value: {{ $cfg.batchMaxWaitMs | quote }}
- name: EMBEDDING_BATCH_MAX_IN_FLIGHT
  value: {{ $cfg.batchMaxInFlight | quote }}
{{- end }}
- name: EMBEDDING_MAX_INPUT_BYTES
  value: {{ $cfg.maxInputBytes | quote }}
- name: EMBEDDING_NORMALIZE
  value: {{ $cfg.normalize | quote }}
{{- end -}}
{{- end -}}

{{/*
Returns true when an env var is managed by embeddings and should not be rendered from extraEnv.
*/}}
{{- define "hub.embeddingEnvManaged" -}}
{{- $key := .key -}}
{{- $root := .root -}}
{{- $bundled := list "EMBEDDING_PROVIDER" "EMBEDDING_MODEL" "EMBEDDING_BASE_URL" "EMBEDDING_PROVIDER_API_KEY" "EMBEDDING_MAX_CONCURRENT" "EMBEDDING_NORMALIZE" -}}
{{- $external := list "EMBEDDING_PROVIDER" "EMBEDDING_MODEL" "EMBEDDING_MAX_INPUT_BYTES" "EMBEDDING_NORMALIZE" -}}
{{- $cfg := $root.Values.embeddingClient -}}
{{- $base := $cfg.baseUrl -}}
{{- if eq .role "worker" -}}
  {{- $base = default $base $cfg.workerBaseUrl -}}
  {{- $external = concat $external (list "EMBEDDING_MAX_CONCURRENT" "EMBEDDING_JOB_TIMEOUT_SECONDS" "EMBEDDING_BATCH_SIZE" "EMBEDDING_BATCH_MAX_WAIT_MS" "EMBEDDING_BATCH_MAX_IN_FLIGHT") -}}
{{- else -}}
  {{- $base = default $base $cfg.apiBaseUrl -}}
{{- end -}}
{{- if $base -}}{{- $external = append $external "EMBEDDING_BASE_URL" -}}{{- end -}}
{{- if $cfg.responseModel -}}{{- $external = append $external "EMBEDDING_RESPONSE_MODEL" -}}{{- end -}}
{{- if eq $cfg.provider "google-gemini" -}}
  {{- $external = concat $external (list "EMBEDDING_GOOGLE_CLOUD_PROJECT" "EMBEDDING_GOOGLE_CLOUD_LOCATION") -}}
  {{- if $cfg.auth.googleCredentialsSecret -}}{{- $external = append $external "GOOGLE_APPLICATION_CREDENTIALS" -}}{{- end -}}
{{- else -}}
  {{- $external = append $external "EMBEDDING_PROVIDER_API_KEY" -}}
{{- end -}}
{{- if has $key (ternary $external $bundled .external) -}}
true
{{- end -}}
{{- end }}

{{/*
Create the image reference
*/}}
{{- define "hub.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}

{{/*
Checksum annotations for config + secrets (rollout on change)
*/}}
{{- define "hub.checksumAnnotations" -}}
checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
checksum/secret: {{ include (print $.Template.BasePath "/secret.yaml") . | sha256sum }}
{{- end -}}
