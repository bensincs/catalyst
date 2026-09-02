{{/*
LLM Gateway API-key env var.

The llm-gateway (LiteLLM) enforces LITELLM_MASTER_KEY whenever that key is
sourced from the insight-secrets Secret — i.e. in the App Config or ESO
(externalSecrets / useEsoSecrets) modes. Any component that calls the gateway
must therefore present the SAME key, otherwise LiteLLM rejects the request with
"Malformed API Key".

This helper renders the LLM_GATEWAY_API_KEY env entry consistently:
  - ESO / App Config active -> secretKeyRef into insight--llm-gateway--master-key
  - otherwise (local/dev)    -> the plain fallback value

Usage (indent to the env list level, usually 12):
  {{- include "insight.llmGatewayApiKeyEnv" (dict "ctx" $ "fallback" .Values.<component>.env.LLM_GATEWAY_API_KEY) | nindent 12 }}
*/}}
{{- define "insight.llmGatewayApiKeyEnv" -}}
- name: LLM_GATEWAY_API_KEY
{{- if or .ctx.Values.appConfig.enabled .ctx.Values.llmGateway.useEsoSecrets .ctx.Values.appInfra.externalSecrets.enabled }}
  valueFrom:
    secretKeyRef:
      name: {{ .ctx.Values.appConfig.secrets.secretName | default "insight-secrets" }}
      key: insight--llm-gateway--master-key
      optional: true
{{- else }}
  value: {{ .fallback | default "" | quote }}
{{- end }}
{{- end -}}

{{/*
Pick a single storage container name from the per-tenant infra output.

The insight Terraform emits appInfra.storageContainers as a comma-joined,
sorted list (e.g. "insight-audio-brief,insight-user-documents"), projected by
the tenant-operator. Individual env vars want one specific container, so this
helper returns the first entry whose name contains `match` (e.g.
"user-documents" or "audio-brief"). When appInfra provides nothing (dev/qa,
which drive storage via App Config / values instead), it returns `fallback`.

Usage:
  {{ include "insight.pickStorageContainer" (dict "ctx" $ "match" "user-documents" "fallback" .Values.backend.env.AZURE_STORAGE_CONTAINER) | quote }}
*/}}
{{- define "insight.pickStorageContainer" -}}
{{- $match := .match -}}
{{- $fallback := .fallback -}}
{{- $picked := "" -}}
{{- range $c := splitList "," (.ctx.Values.appInfra.storageContainers | default "") -}}
{{- if and (eq $picked "") (contains $match $c) -}}{{- $picked = $c -}}{{- end -}}
{{- end -}}
{{- $picked | default $fallback -}}
{{- end -}}

{{/*
OpenTelemetry SDK env for InSight Python services (cortex-otel).

Emits the OTEL_* env the cortex-otel SDK reads so a service exports traces and
metrics via OTLP to the node-local otel-agent DaemonSet (cortex-observability
chart) on hostPort 4318. Logs travel separately: services write structured JSON
to stdout, which the same agent scrapes into Log Analytics (ADR-2026-05-19,
Azure-native stack; issue #1377).

The block renders only when global.observability.enabled is true and an endpoint
is set; otherwise it is absent and the SDK is a no-op. enabled is read via
toString so a boolean and the quoted-string form ("true"/"false") behave the
same.

Attribution:
  - service.instance.id is set here from POD_NAME. service.name and
    service.version are owned by the app's setup_telemetry(...).
  - ctx.emitter_app is owned by the app's setup_telemetry(emitter_app="insight")
    call (#2241); the SDK resource attribute is the single source of truth.
  - ctx.app is per-request, stamped by the SDK middleware from the
    X-Ctx-App / X-Cortex-App header (#1731).

NODE_IP (the pod's host IP) and POD_NAME are declared before the values that
reference them: Kubernetes expands $(NODE_IP) in the OTLP endpoint and
$(POD_NAME) in service.instance.id, and $(VAR) resolves only against env vars
declared earlier in the same container.

OTEL_EXPORTER_OTLP_PROTOCOL is always http/protobuf: cortex-otel wires
opentelemetry-exporter-otlp-proto-http only. The values key documents that
contract; changing it does not switch the SDK to gRPC.

OTEL_PYTHON_EXCLUDED_URLS keeps probe paths out of FastAPI auto-instrumentation
spans. Both instrumentation paths read it: the SDK's instrument_fastapi
(observability) and the operator-injected agent (otelPythonInstrumentation, via
the inject-python annotation), so it is emitted when either is on while the OTLP
exporter env stays gated on observability. Defaults match
global.otelPythonInstrumentation.excludedUrls.

Usage (indent to the env list level, usually 12):
  {{- include "insight.otelEnv" $ | nindent 12 }}
*/}}
{{- define "insight.otelEnv" -}}
{{- $obs := .Values.global.observability | default dict -}}
{{- $instr := .Values.global.otelPythonInstrumentation | default dict -}}
{{- $obsOn := and $obs.endpoint (eq (toString $obs.enabled) "true") -}}
{{- $instrOn := eq (toString $instr.enabled) "true" -}}
{{- if $obsOn }}
- name: NODE_IP
  valueFrom:
    fieldRef:
      fieldPath: status.hostIP
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ $obs.endpoint | quote }}
- name: OTEL_EXPORTER_OTLP_PROTOCOL
  value: {{ $obs.protocol | default "http/protobuf" | quote }}
- name: OTEL_RESOURCE_ATTRIBUTES
  value: "service.instance.id=$(POD_NAME)"
{{- end }}
{{- /* Single source of OTEL_PYTHON_EXCLUDED_URLS: emitted for either the SDK
  (observability) or the operator-injection (otelPythonInstrumentation) path so
  probe routes stay out of spans regardless of which is active, and so it can
  never be duplicated. Templates that include this helper must NOT emit it
  separately. */}}
{{- if or $obsOn $instrOn }}
- name: OTEL_PYTHON_EXCLUDED_URLS
  value: {{ $instr.excludedUrls | default "health,ready" | quote }}
{{- end }}
{{- end -}}

{{/*
Pod Security Standards — restricted profile (pod-level).

Applied to all Insight workloads so the namespace can enforce
pod-security.kubernetes.io/enforce=restricted. Complements
insight.containerSecurityContext on every container/initContainer.

Usage (indent under spec:):
  {{- include "insight.podSecurityContext" . | nindent 6 }}
*/}}
{{- define "insight.podSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  seccompProfile:
    type: RuntimeDefault
{{- end -}}

{{/*
Pod Security Standards — restricted profile (container-level).

Usage (indent under container:):
  {{- include "insight.containerSecurityContext" . | nindent 10 }}
*/}}
{{- define "insight.containerSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  allowPrivilegeEscalation: false
  seccompProfile:
    type: RuntimeDefault
  capabilities:
    drop:
      - ALL
{{- end -}}

{{/*
Env for UID 1000 with --no-create-home images / SDK caches.

Python Azure/MSAL and many SDKs honour $HOME / $TMPDIR / $XDG_CACHE_HOME.
Point them at /tmp so restricted pods do not fail on missing /home/appuser.

Note: prisma-python ignores $HOME and writes under the passwd home
(/home/appuser). llm-gateway mounts an emptyDir there for that case.

Usage (indent under env:):
  {{- include "insight.nonRootEnv" . | nindent 12 }}
*/}}
{{- define "insight.nonRootEnv" -}}
- name: HOME
  value: /tmp
- name: TMPDIR
  value: /tmp
- name: XDG_CACHE_HOME
  value: /tmp
{{- end -}}
