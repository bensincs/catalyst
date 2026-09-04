"""Resource attribute construction for the OTel SDK."""

from collections.abc import Mapping

from opentelemetry.sdk.resources import Resource

from cortex_otel import _attributes as attrs


def build_resource(
    *,
    service_name: str,
    service_version: str | None = None,
    emitter_app: str | None = None,
    deployment_environment: str | None = None,
    service_namespace: str | None = None,
    extra: Mapping[str, str] | None = None,
) -> Resource:
    """Build an OTel Resource with the ADR-2026-05-19 standard attributes.

    ``emitter_app`` sets ``ctx.emitter_app`` — the Cortex application emitting
    the signal (e.g. ``cortex``, ``insight``, ``inalpha``). This is a
    process-level constant.

    ``deployment_environment`` sets the standard OTel ``deployment.environment``
    attribute (e.g. ``local``, ``dev``, ``qa``, ``uat``, ``prod``). Falls back
    to ``OTEL_RESOURCE_ATTRIBUTES`` env when omitted.

    ``service_namespace`` is intentionally unset by default: per the ADR,
    OpenTelemetry's ``service.namespace`` is reserved for team/product logical
    grouping, not emitter identity — ``ctx.emitter_app`` covers the latter.

    Caller-supplied attributes win over the SDK's environment-merged defaults
    only when explicitly passed via ``extra``; ``OTEL_RESOURCE_ATTRIBUTES`` is
    still merged in by the SDK.
    """

    resource_attrs: dict[str, str] = {attrs.SERVICE_NAME: service_name}
    if service_version:
        resource_attrs[attrs.SERVICE_VERSION] = service_version
    if emitter_app:
        resource_attrs[attrs.CTX_EMITTER_APP] = emitter_app
    if deployment_environment:
        resource_attrs[attrs.DEPLOYMENT_ENVIRONMENT] = deployment_environment
    if service_namespace:
        resource_attrs["service.namespace"] = service_namespace
    if extra:
        resource_attrs.update(extra)
    return Resource.create(resource_attrs)
