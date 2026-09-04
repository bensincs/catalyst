"""Cortex OTel attribute-key constants.

Central source of truth for the attribute keys defined in ADR-2026-05-19
(Full Platform Observability). Use these constants — never string literals —
when attaching attributes to spans, metrics, or logs, so a typo in one service
can't silently split dashboards.

Attributes are split into three groups:

* Resource attributes — set once at process start on the OTel Resource. Values
  are constant for the process's lifetime.
* Context attributes — set per-request via the request-context mechanism (see
  ``cortex_otel._context``). Values change on every request.
* Signal attributes — attached at the emit site on a specific log/span/metric.

Keys that map to a standard OpenTelemetry semantic convention keep the standard
key (``service.name``, ``deployment.environment``, ``http.route``,
``gen_ai.*``); only Cortex extensions live in the ``ctx.*`` namespace.
"""

# ---------------------------------------------------------------------------
# Resource — set once per process.
# ---------------------------------------------------------------------------

SERVICE_NAME = "service.name"
SERVICE_VERSION = "service.version"
DEPLOYMENT_ENVIRONMENT = "deployment.environment"
CTX_EMITTER_APP = "ctx.emitter_app"

# ---------------------------------------------------------------------------
# Context — set per-request.
# ---------------------------------------------------------------------------

CTX_APP = "ctx.app"
CTX_TENANT = "ctx.tenant"
CTX_REQUEST_ID = "ctx.request_id"
CTX_AGENT_RUN_ID = "ctx.agent_run_id"
CTX_WORKFLOW_ID = "ctx.workflow_id"
ENDUSER_ID_HASH = "enduser.id_hash"

# ---------------------------------------------------------------------------
# Collector-owned. Listed for documentation only — services must not emit
# these. The collector (Grafana Alloy) sets them from platform metadata.
# ---------------------------------------------------------------------------

OBS_TENANT = "obs.tenant"
CTX_CUSTOMER_ORG = "ctx.customer_org"
CTX_INSTALLATION = "ctx.installation"
CTX_DEPLOYMENT_MODE = "ctx.deployment_mode"

# ---------------------------------------------------------------------------
# Log signal — attached at the log-emit site.
# ---------------------------------------------------------------------------

EVENT_NAME = "event.name"
EVENT_OUTCOME = "event.outcome"
ERROR_TYPE = "error.type"
ERROR_CODE = "error.code"

# Event-outcome enum values.
OUTCOME_SUCCESS = "success"
OUTCOME_FAILURE = "failure"
OUTCOME_UNKNOWN = "unknown"

# ---------------------------------------------------------------------------
# HTTP — populated by auto-instrumentation. Listed so app code that stamps
# them manually uses consistent keys.
# ---------------------------------------------------------------------------

HTTP_REQUEST_METHOD = "http.request.method"
HTTP_ROUTE = "http.route"
HTTP_RESPONSE_STATUS_CODE = "http.response.status_code"

# ---------------------------------------------------------------------------
# GenAI — emitted by the AI Gateway (extproc) and by agent runtimes.
# ---------------------------------------------------------------------------

GEN_AI_SYSTEM = "gen_ai.system"
GEN_AI_OPERATION_NAME = "gen_ai.operation.name"
GEN_AI_REQUEST_MODEL = "gen_ai.request.model"
GEN_AI_RESPONSE_MODEL = "gen_ai.response.model"
GEN_AI_RESPONSE_ID = "gen_ai.response.id"
GEN_AI_USAGE_INPUT_TOKENS = "gen_ai.usage.input_tokens"
GEN_AI_USAGE_OUTPUT_TOKENS = "gen_ai.usage.output_tokens"
GEN_AI_USAGE_CACHED_INPUT_TOKENS = "gen_ai.usage.cached_input_tokens"
GEN_AI_TOOL_NAME = "gen_ai.tool.name"
GEN_AI_TOOL_CALL_ID = "gen_ai.tool.call.id"

# ---------------------------------------------------------------------------
# Cortex-specific agent/retrieval/eval attributes.
# ---------------------------------------------------------------------------

CTX_PROMPT_ID = "ctx.prompt.id"
CTX_PROMPT_VERSION = "ctx.prompt.version"
CTX_RETRIEVAL_INDEX = "ctx.retrieval.index"
CTX_EVAL_NAME = "ctx.eval.name"
CTX_EVAL_SCORE = "ctx.eval.score"
