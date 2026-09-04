"""Parity: exact ADR-2026-05-19 ctx.* attributes the SDK emits on spans and logs.

The Go cortex-otel-go package runs the mirror of these tests in
``libs/cortex-otel-go/parity_test.go``. Both suites load the same
``libs/cortex-otel-parity/request-context.json`` fixture, so key **and** value
drift between the two SDKs will fail loudly on either side.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from cortex_otel import _attributes as attrs
from cortex_otel._context import RequestContext, context_to_attributes

EXPECTED_REQUEST_CONTEXT_KEYS = {
    attrs.CTX_APP,
    attrs.CTX_TENANT,
    attrs.CTX_REQUEST_ID,
    attrs.CTX_AGENT_RUN_ID,
    attrs.CTX_WORKFLOW_ID,
    attrs.ENDUSER_ID_HASH,
}

_FIXTURE_PATH = Path(__file__).resolve().parents[2] / "cortex-otel-parity" / "request-context.json"


def _load_fixture() -> list[dict]:
    data = json.loads(_FIXTURE_PATH.read_text())
    cases = data["cases"]
    assert cases, f"fixture {_FIXTURE_PATH} has no cases"
    return cases


def _request_context_from_input(payload: dict[str, str]) -> RequestContext:
    return RequestContext(
        app=payload.get("app"),
        tenant=payload.get("tenant"),
        request_id=payload.get("request_id"),
        agent_run_id=payload.get("agent_run_id"),
        workflow_id=payload.get("workflow_id"),
        enduser_id_hash=payload.get("enduser_id_hash"),
    )


def test_attribute_constants_are_stable_strings() -> None:
    assert attrs.CTX_APP == "ctx.app"
    assert attrs.CTX_TENANT == "ctx.tenant"
    assert attrs.CTX_REQUEST_ID == "ctx.request_id"
    assert attrs.CTX_AGENT_RUN_ID == "ctx.agent_run_id"
    assert attrs.CTX_WORKFLOW_ID == "ctx.workflow_id"
    assert attrs.ENDUSER_ID_HASH == "enduser.id_hash"


def test_context_to_attributes_emits_adr_key_set() -> None:
    fully = _request_context_from_input(
        {
            "app": "insight",
            "tenant": "acme",
            "request_id": "req-1",
            "agent_run_id": "run-1",
            "workflow_id": "wf-1",
            "enduser_id_hash": "hash-1",
        }
    )
    got = set(context_to_attributes(fully).keys())
    assert got == EXPECTED_REQUEST_CONTEXT_KEYS, (
        f"key set drift:\n  got:  {sorted(got)}\n  want: {sorted(EXPECTED_REQUEST_CONTEXT_KEYS)}"
    )


@pytest.mark.parametrize("case", _load_fixture(), ids=lambda c: c["name"])
def test_shared_fixture_parity(case: dict) -> None:
    rc = _request_context_from_input(case["input"])
    got = context_to_attributes(rc)
    want = case["expected"]
    assert got == want, f"case {case['name']!r}: attribute drift\n  got:  {got}\n  want: {want}"
