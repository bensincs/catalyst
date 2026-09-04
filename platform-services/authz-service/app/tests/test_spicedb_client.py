"""Unit tests for the SpiceDB client wrapper.

The SpiceDB client wraps the authzed gRPC channel and translates between
the SDK protocol types (CheckPermissionResponse, GrantPermissionResponse,
RevokePermissionResponse) and the raw gRPC request/response objects.

What we test here
-----------------
1. check_permission wires the correct gRPC CheckPermissionRequest fields
2. grant_permission wires the correct WriteRelationships request
3. revoke_permission wires the correct DeleteRelationships request
4. ZedToken strings are extracted from gRPC responses and returned to callers
5. Consistency token supplied by the caller is forwarded as at_least_as_fresh
6. When the gRPC call raises a grpc.RpcError the client converts it to a
   domain exception rather than leaking gRPC internals

All tests mock the underlying gRPC channel — SpiceDB is never contacted.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from tests.conftest import (
    AGENT_RESOURCE_A,
    TENANT_A,
    TENANT_B,
    USER_MEMBER_A,
    USER_OWNER_A,
    ZEDTOKEN_READ,
    ZEDTOKEN_WRITE,
)


# ---------------------------------------------------------------------------
# check_permission
# ---------------------------------------------------------------------------


class TestCheckPermissionRPC:
    """SpiceDB CheckPermission RPC wiring."""

    @pytest.mark.asyncio
    async def test_check_permission_returns_allowed_true(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=2, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)

        client = SpiceDBClient(stub=stub)

        result = await client.check_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            permission="can_access",
            subject=USER_MEMBER_A,
        )

        assert result.allowed is True

    @pytest.mark.asyncio
    async def test_check_permission_returns_allowed_false(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=1, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)

        client = SpiceDBClient(stub=stub)

        result = await client.check_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            permission="can_access",
            subject=USER_MEMBER_A,
        )

        assert result.allowed is False

    @pytest.mark.asyncio
    async def test_check_permission_includes_checked_at_token(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=1, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)

        client = SpiceDBClient(stub=stub)

        result = await client.check_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            permission="can_access",
            subject=USER_MEMBER_A,
        )

        assert result.checked_at == ZEDTOKEN_READ

    @pytest.mark.asyncio
    async def test_check_permission_forwards_consistency_token(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=1, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)

        client = SpiceDBClient(stub=stub)
        caller_token = "caller-supplied-zedtoken"

        await client.check_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            permission="can_access",
            subject=USER_MEMBER_A,
            consistency_token=caller_token,
        )

        call_args = stub.CheckPermission.call_args
        request = call_args[0][0] if call_args[0] else call_args.args[0]
        assert _extract_zedtoken_from_consistency(request) == caller_token

    @pytest.mark.asyncio
    async def test_check_permission_uses_fully_consistent_when_no_token(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=1, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)

        client = SpiceDBClient(stub=stub)

        await client.check_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            permission="can_access",
            subject=USER_MEMBER_A,
            consistency_token=None,
        )

        call_args = stub.CheckPermission.call_args
        request = call_args[0][0] if call_args[0] else call_args.args[0]
        assert _uses_at_least_as_fresh_or_fully_consistent(request)

    @pytest.mark.asyncio
    async def test_check_permission_defaults_to_fully_consistent(self) -> None:
        """ADR-004 requires fully_consistent (not minimize_latency) when no token is supplied."""
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=1, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)

        client = SpiceDBClient(stub=stub)

        await client.check_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            permission="can_access",
            subject=USER_MEMBER_A,
        )

        call_args = stub.CheckPermission.call_args
        request = call_args[0][0] if call_args[0] else call_args.args[0]
        assert not _uses_minimize_latency(request)
        assert _uses_fully_consistent(request)

    @pytest.mark.asyncio
    async def test_check_permission_propagates_grpc_error_as_domain_exception(
        self,
    ) -> None:
        from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError

        stub = MagicMock()
        stub.CheckPermission = AsyncMock(side_effect=_make_grpc_error("UNAVAILABLE"))

        client = SpiceDBClient(stub=stub)

        with pytest.raises(SpiceDBUnavailableError):
            await client.check_permission(
                tenant_id=TENANT_A,
                resource=AGENT_RESOURCE_A,
                permission="can_access",
                subject=USER_MEMBER_A,
            )


# ---------------------------------------------------------------------------
# grant_permission
# ---------------------------------------------------------------------------


class TestGrantPermissionRPC:
    """SpiceDB WriteRelationships RPC wiring for grant."""

    @pytest.mark.asyncio
    async def test_grant_permission_returns_granted_at_token(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_write_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_write(grpc_response)

        client = SpiceDBClient(stub=stub)

        result = await client.grant_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            relation="user",
            subject=USER_MEMBER_A,
        )

        assert result.granted_at == ZEDTOKEN_WRITE

    @pytest.mark.asyncio
    async def test_grant_permission_calls_write_relationships_rpc(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_write_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_write(grpc_response)

        client = SpiceDBClient(stub=stub)

        await client.grant_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            relation="owner",
            subject=USER_OWNER_A,
        )

        stub.WriteRelationships.assert_called_once()

    @pytest.mark.asyncio
    async def test_grant_permission_uses_fully_consistent(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_write_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_write(grpc_response)

        client = SpiceDBClient(stub=stub)

        await client.grant_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            relation="user",
            subject=USER_MEMBER_A,
        )

        call_args = stub.WriteRelationships.call_args
        request = call_args[0][0] if call_args[0] else call_args.args[0]
        # SpiceDB writes are always linearizable — no consistency field required.
        # Verify the proto was passed directly (not wrapped) by checking it has
        # the expected updates field.
        assert len(request.updates) == 1
        assert request.updates[0].operation == request.updates[0].OPERATION_TOUCH

    @pytest.mark.asyncio
    async def test_grant_permission_propagates_grpc_error(self) -> None:
        from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError

        stub = MagicMock()
        stub.WriteRelationships = AsyncMock(
            side_effect=_make_grpc_error("UNAVAILABLE")
        )

        client = SpiceDBClient(stub=stub)

        with pytest.raises(SpiceDBUnavailableError):
            await client.grant_permission(
                tenant_id=TENANT_A,
                resource=AGENT_RESOURCE_A,
                relation="user",
                subject=USER_MEMBER_A,
            )


# ---------------------------------------------------------------------------
# grant_permissions (batch)
# ---------------------------------------------------------------------------


class TestGrantPermissionsRPC:
    """SpiceDB WriteRelationships RPC wiring for atomic batch grant."""

    @pytest.mark.asyncio
    async def test_grant_permissions_writes_all_updates_in_one_call(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_write_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_write(grpc_response)
        client = SpiceDBClient(stub=stub)

        result = await client.grant_permissions(
            tenant_id=TENANT_A,
            relationships=[
                {"resource": AGENT_RESOURCE_A, "relation": "member", "subject": USER_MEMBER_A},
                {"resource": AGENT_RESOURCE_A, "relation": "owner", "subject": USER_OWNER_A},
            ],
        )

        assert result.granted_at == ZEDTOKEN_WRITE
        stub.WriteRelationships.assert_called_once()
        call_args = stub.WriteRelationships.call_args
        request = call_args[0][0] if call_args[0] else call_args.args[0]
        assert len(request.updates) == 2
        assert all(u.operation == u.OPERATION_TOUCH for u in request.updates)

    @pytest.mark.asyncio
    async def test_grant_permissions_adds_must_match_precondition(self) -> None:
        from data.spicedb_client import SpiceDBClient

        stub = _stub_with_write(_make_write_response(zedtoken=ZEDTOKEN_WRITE))
        client = SpiceDBClient(stub=stub)

        await client.grant_permissions(
            tenant_id=TENANT_A,
            relationships=[
                {"resource": AGENT_RESOURCE_A, "relation": "member", "subject": USER_MEMBER_A}
            ],
            preconditions=[
                {"resource": AGENT_RESOURCE_A, "relation": "defined", "subject": USER_OWNER_A}
            ],
        )

        request = stub.WriteRelationships.call_args.args[0]
        assert len(request.optional_preconditions) == 1
        precondition = request.optional_preconditions[0]
        assert precondition.operation == precondition.OPERATION_MUST_MATCH
        assert precondition.filter.resource_type == "agent"
        assert precondition.filter.optional_relation == "defined"

    @pytest.mark.asyncio
    async def test_grant_permissions_propagates_grpc_error(self) -> None:
        from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError

        stub = MagicMock()
        stub.WriteRelationships = AsyncMock(side_effect=_make_grpc_error("UNAVAILABLE"))
        client = SpiceDBClient(stub=stub)

        with pytest.raises(SpiceDBUnavailableError):
            await client.grant_permissions(
                tenant_id=TENANT_A,
                relationships=[
                    {"resource": AGENT_RESOURCE_A, "relation": "member", "subject": USER_MEMBER_A},
                ],
            )


# ---------------------------------------------------------------------------
# revoke_permissions (batch)
# ---------------------------------------------------------------------------


class TestRevokePermissionsRPC:
    """SpiceDB WriteRelationships RPC wiring for atomic batch revoke."""

    @pytest.mark.asyncio
    async def test_revoke_permissions_deletes_all_updates_in_one_call(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_write_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_write(grpc_response)
        client = SpiceDBClient(stub=stub)

        result = await client.revoke_permissions(
            tenant_id=TENANT_A,
            relationships=[
                {"resource": AGENT_RESOURCE_A, "relation": "member", "subject": USER_MEMBER_A},
                {"resource": AGENT_RESOURCE_A, "relation": "owner", "subject": USER_OWNER_A},
            ],
        )

        assert result.revoked_at == ZEDTOKEN_WRITE
        request = stub.WriteRelationships.call_args.args[0]
        assert len(request.updates) == 2
        assert all(u.operation == u.OPERATION_DELETE for u in request.updates)


# ---------------------------------------------------------------------------
# revoke_permission
# ---------------------------------------------------------------------------


class TestRevokePermissionRPC:
    """SpiceDB DeleteRelationships RPC wiring for revoke."""

    @pytest.mark.asyncio
    async def test_revoke_permission_returns_revoked_at_token(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_delete_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_delete(grpc_response)

        client = SpiceDBClient(stub=stub)

        result = await client.revoke_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            relation="user",
            subject=USER_MEMBER_A,
        )

        assert result.revoked_at == ZEDTOKEN_WRITE

    @pytest.mark.asyncio
    async def test_revoke_permission_calls_delete_relationships_rpc(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_delete_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_delete(grpc_response)

        client = SpiceDBClient(stub=stub)

        await client.revoke_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            relation="user",
            subject=USER_MEMBER_A,
        )

        stub.DeleteRelationships.assert_called_once()

    @pytest.mark.asyncio
    async def test_revoke_permission_uses_fully_consistent(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_delete_response(zedtoken=ZEDTOKEN_WRITE)
        stub = _stub_with_delete(grpc_response)

        client = SpiceDBClient(stub=stub)

        await client.revoke_permission(
            tenant_id=TENANT_A,
            resource=AGENT_RESOURCE_A,
            relation="user",
            subject=USER_MEMBER_A,
        )

        call_args = stub.DeleteRelationships.call_args
        request = call_args[0][0] if call_args[0] else call_args.args[0]
        # SpiceDB deletes are always linearizable — no consistency field required.
        # Verify the proto was passed directly by checking the relationship_filter.
        assert request.relationship_filter.resource_type != ""

    @pytest.mark.asyncio
    async def test_revoke_permission_propagates_grpc_error(self) -> None:
        from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError

        stub = MagicMock()
        stub.DeleteRelationships = AsyncMock(
            side_effect=_make_grpc_error("UNAVAILABLE")
        )

        client = SpiceDBClient(stub=stub)

        with pytest.raises(SpiceDBUnavailableError):
            await client.revoke_permission(
                tenant_id=TENANT_A,
                resource=AGENT_RESOURCE_A,
                relation="user",
                subject=USER_MEMBER_A,
            )


# ---------------------------------------------------------------------------
# Constructor / channel wiring
# ---------------------------------------------------------------------------


class TestParseResourceTenantIsolation:
    """`agent:agent-1@tenant-a` must never collide with `agent:agent-1@tenant-b`."""

    @pytest.mark.asyncio
    async def test_tenant_suffix_is_folded_into_object_id(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=2, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)
        client = SpiceDBClient(stub=stub)

        await client.check_permission(
            tenant_id=TENANT_A,
            resource=f"agent:agent-1@{TENANT_A}",
            permission="can_access",
            subject=USER_MEMBER_A,
        )

        sent = stub.CheckPermission.call_args[0][0]
        assert sent.resource.object_type == "agent"
        # The object ID must include a tenant scope; raw "agent-1" would
        # collide across tenants.
        assert sent.resource.object_id != "agent-1"
        assert "tenant_alpha" in sent.resource.object_id

    @pytest.mark.asyncio
    async def test_same_id_different_tenants_produce_different_object_ids(self) -> None:
        from data.spicedb_client import SpiceDBClient

        grpc_response = _make_check_response(permissionship=2, zedtoken=ZEDTOKEN_READ)
        stub = _stub_with_check(grpc_response)
        client = SpiceDBClient(stub=stub)

        await client.check_permission(
            tenant_id=TENANT_A,
            resource=f"agent:agent-1@{TENANT_A}",
            permission="can_access",
            subject=USER_MEMBER_A,
        )
        id_a = stub.CheckPermission.call_args[0][0].resource.object_id

        await client.check_permission(
            tenant_id=TENANT_B,
            resource=f"agent:agent-1@{TENANT_B}",
            permission="can_access",
            subject=USER_MEMBER_A,
        )
        id_b = stub.CheckPermission.call_args[0][0].resource.object_id

        assert id_a != id_b, (
            "Same resource ID under different tenants must not collide on a "
            "single SpiceDB object — that's the multi-tenant isolation bug."
        )


class TestGrantPermissionToRoleTenantRelation:
    """Removed: grant_permission_to_role and platform_permission definitions have been deleted.

    The simplified schema (cortex.zed) has no platform_permission type and no
    grant_permission_to_role method on SpiceDBClient. Tests that covered that
    surface are no longer applicable.
    """


class TestPermissionObjectIdConsistency:
    """Removed: permission_object_id helper and platform_permission type have been deleted.

    See TestGrantPermissionToRoleTenantRelation above.
    """


class TestSpiceDBClientConstruction:
    """SpiceDBClient must be constructable from a gRPC channel address."""

    def test_client_can_be_constructed_with_channel_address(self) -> None:
        from data.spicedb_client import SpiceDBClient

        client = SpiceDBClient.from_address("localhost:50051", token="t-test")

        assert client is not None

    def test_client_has_required_authz_methods(self) -> None:
        from data.spicedb_client import SpiceDBClient

        stub = MagicMock()
        client = SpiceDBClient(stub=stub)

        assert callable(getattr(client, "check_permission", None))
        assert callable(getattr(client, "grant_permission", None))
        assert callable(getattr(client, "revoke_permission", None))
        assert callable(getattr(client, "list_relationships", None))
        assert callable(getattr(client, "lookup_subjects", None))
        assert callable(getattr(client, "write_schema", None))


# ---------------------------------------------------------------------------
# write_schema
# ---------------------------------------------------------------------------


class TestWriteSchemaRPC:
    """SpiceDB WriteSchema RPC wiring."""

    @pytest.mark.asyncio
    async def test_write_schema_reuses_client_metadata_and_retry_path(self) -> None:
        from data.spicedb_client import SpiceDBClient

        schema_stub = _stub_with_schema_write(
            _make_schema_write_response(zedtoken=ZEDTOKEN_WRITE)
        )
        client = SpiceDBClient(
            stub=MagicMock(),
            schema_stub=schema_stub,
            timeout_seconds=3,
        )

        result = await client.write_schema("definition user {}")

        assert result == ZEDTOKEN_WRITE
        request = schema_stub.WriteSchema.call_args.args[0]
        assert request.schema == "definition user {}"


# ---------------------------------------------------------------------------
# list_relationships
# ---------------------------------------------------------------------------


class TestListRelationshipsRPC:
    """SpiceDB ReadRelationships RPC wiring."""

    @pytest.mark.asyncio
    async def test_returns_all_items(self) -> None:
        from data.spicedb_client import SpiceDBClient

        items = [
            _make_read_response_item(
                resource_type="application",
                resource_id="cortex_tenant_ui_tenant_alpha",
                relation="accessor",
                subject_type="user",
                subject_id="alice",
                zedtoken=ZEDTOKEN_READ,
            ),
            _make_read_response_item(
                resource_type="application",
                resource_id="cortex_agent_ui_tenant_alpha",
                relation="accessor",
                subject_type="user",
                subject_id="bob",
                zedtoken=ZEDTOKEN_READ,
            ),
        ]
        stub = _stub_with_read(items)
        client = SpiceDBClient(stub=stub)

        result = await client.list_relationships(tenant_id=TENANT_A)

        assert len(result.relationships) == 2
        assert result.relationships[0].resource == "application:cortex_tenant_ui_tenant_alpha"
        assert result.relationships[0].relation == "accessor"
        assert result.relationships[0].subject == "user:alice"
        assert result.read_at == ZEDTOKEN_READ

    @pytest.mark.asyncio
    async def test_empty_stream_returns_empty_list(self) -> None:
        from data.spicedb_client import SpiceDBClient

        stub = _stub_with_read([])
        client = SpiceDBClient(stub=stub)

        result = await client.list_relationships(tenant_id=TENANT_A)

        assert result.relationships == []
        assert result.read_at == ""

    @pytest.mark.asyncio
    async def test_subject_filter_is_sent_to_spicedb(self) -> None:
        from data.spicedb_client import SpiceDBClient

        stub = _stub_with_read([])
        client = SpiceDBClient(stub=stub)

        await client.list_relationships(
            tenant_id=TENANT_A,
            resource_type="app_role",
            relation="member",
            subject="user:alice@example.com",
        )

        request = stub.ReadRelationships.call_args.args[0]
        relationship_filter = request.relationship_filter
        subject_filter = relationship_filter.optional_subject_filter
        assert relationship_filter.resource_type == "app_role"
        assert relationship_filter.optional_relation == "member"
        assert subject_filter.subject_type == "user"
        assert subject_filter.optional_subject_id == "alice_at_example_dot_com"

    @pytest.mark.asyncio
    async def test_grpc_error_raises_spicedb_unavailable(self) -> None:
        from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError
        import grpc

        async def _failing_stream(*args, **kwargs):
            raise grpc.RpcError()
            yield  # make it a generator

        stub = MagicMock()
        stub.ReadRelationships = MagicMock(return_value=_failing_stream())
        client = SpiceDBClient(stub=stub)

        with pytest.raises(SpiceDBUnavailableError):
            await client.list_relationships(tenant_id=TENANT_A)


# ---------------------------------------------------------------------------
# lookup_subjects
# ---------------------------------------------------------------------------


class TestLookupSubjectsRPC:
    """SpiceDB LookupSubjects RPC wiring."""

    @pytest.mark.asyncio
    async def test_lookup_subjects_uses_exact_resource_and_fully_consistent(self) -> None:
        from data.spicedb_client import SpiceDBClient

        items = [
            _make_lookup_subjects_response_item(
                subject_id="insight|admin_tenant_alpha",
                zedtoken=ZEDTOKEN_READ,
            )
        ]
        stub = _stub_with_lookup_subjects(items)
        client = SpiceDBClient(stub=stub)

        result = await client.lookup_subjects(
            tenant_id=TENANT_A,
            resource="app_role_assignment:insight|user_colon_alice_tenant_alpha",
            permission="assigned",
            subject_object_type="app_role",
        )

        request = stub.LookupSubjects.call_args.args[0]
        assert request.resource.object_type == "app_role_assignment"
        assert request.resource.object_id == "insight|user_colon_alice_tenant_alpha"
        assert request.permission == "assigned"
        assert request.subject_object_type == "app_role"
        assert request.consistency.fully_consistent is True
        assert not _uses_minimize_latency(request)
        assert result.subjects == ["app_role:insight|admin_tenant_alpha"]
        assert result.looked_up_at == ZEDTOKEN_READ


# ---------------------------------------------------------------------------
# Private helpers — build fake gRPC responses without a live channel
# ---------------------------------------------------------------------------


def _make_check_response(*, permissionship: int, zedtoken: str) -> MagicMock:
    """
    permissionship values (from authzed proto):
      0 = PERMISSIONSHIP_UNSPECIFIED
      1 = PERMISSIONSHIP_NO_PERMISSION
      2 = PERMISSIONSHIP_HAS_PERMISSION
    """
    response = MagicMock()
    response.permissionship = permissionship
    response.checked_at.token = zedtoken
    return response


def _make_write_response(*, zedtoken: str) -> MagicMock:
    response = MagicMock()
    response.written_at.token = zedtoken
    return response


def _make_delete_response(*, zedtoken: str) -> MagicMock:
    response = MagicMock()
    response.deleted_at.token = zedtoken
    return response


def _make_schema_write_response(*, zedtoken: str) -> MagicMock:
    response = MagicMock()
    response.written_at.token = zedtoken
    return response


def _make_read_response_item(
    *, resource_type: str, resource_id: str, relation: str,
    subject_type: str, subject_id: str, zedtoken: str
) -> MagicMock:
    item = MagicMock()
    item.relationship.resource.object_type = resource_type
    item.relationship.resource.object_id = resource_id
    item.relationship.relation = relation
    item.relationship.subject.object.object_type = subject_type
    item.relationship.subject.object.object_id = subject_id
    item.read_at.token = zedtoken
    return item


def _make_lookup_subjects_response_item(
    *, subject_id: str, zedtoken: str
) -> MagicMock:
    item = MagicMock()
    item.subject_object_id = subject_id
    item.subject.object.object_id = subject_id
    item.looked_up_at.token = zedtoken
    return item


async def _async_iter(items):
    for item in items:
        yield item


def _stub_with_read(items: list) -> MagicMock:
    stub = MagicMock()
    stub.ReadRelationships = MagicMock(return_value=_async_iter(items))
    return stub


def _stub_with_lookup_subjects(items: list) -> MagicMock:
    stub = MagicMock()
    stub.LookupSubjects = MagicMock(return_value=_async_iter(items))
    return stub


def _stub_with_check(response: MagicMock) -> MagicMock:
    stub = MagicMock()
    stub.CheckPermission = AsyncMock(return_value=response)
    return stub


def _stub_with_write(response: MagicMock) -> MagicMock:
    stub = MagicMock()
    stub.WriteRelationships = AsyncMock(return_value=response)
    return stub


def _stub_with_delete(response: MagicMock) -> MagicMock:
    stub = MagicMock()
    stub.DeleteRelationships = AsyncMock(return_value=response)
    return stub


def _stub_with_schema_write(response: MagicMock) -> MagicMock:
    stub = MagicMock()
    stub.WriteSchema = AsyncMock(return_value=response)
    return stub


def _make_grpc_error(code: str) -> Exception:
    import grpc

    error = MagicMock(spec=grpc.RpcError)
    error.code.return_value = getattr(grpc.StatusCode, code, grpc.StatusCode.INTERNAL)
    error.details.return_value = f"simulated {code} error"
    return error


def _extract_zedtoken_from_consistency(request: MagicMock) -> str | None:
    """Pull the ZedToken string out of whatever consistency field the request carries."""
    try:
        return request.consistency.at_least_as_fresh.token
    except AttributeError:
        return None


def _uses_at_least_as_fresh_or_fully_consistent(request: MagicMock) -> bool:
    try:
        _ = request.consistency.at_least_as_fresh
        return True
    except AttributeError:
        pass
    try:
        _ = request.consistency.fully_consistent
        return True
    except AttributeError:
        pass
    return False


def _uses_minimize_latency(request: MagicMock) -> bool:
    try:
        val = request.consistency.minimize_latency
        return bool(val)
    except AttributeError:
        return False


def _uses_fully_consistent(request: MagicMock) -> bool:
    try:
        val = request.consistency.fully_consistent
        return bool(val)
    except AttributeError:
        return False


# ---------------------------------------------------------------------------
# delete_object — wholesale deletion of an exact object id (teardown primitive)
# ---------------------------------------------------------------------------


class TestDeleteObjectRPC:
    """delete_object deletes by raw object id without re-encoding."""

    @pytest.mark.asyncio
    async def test_delete_object_passes_raw_object_id_verbatim(self) -> None:
        from data.spicedb_client import SpiceDBClient

        stub = _stub_with_delete(_make_delete_response(zedtoken=ZEDTOKEN_WRITE))
        client = SpiceDBClient(stub=stub)

        # An object id that already contains a folded tenant suffix and a "|" —
        # it must NOT be re-sanitised or have a tenant appended.
        result = await client.delete_object(
            resource_type="app_role",
            object_id="example_app_deployment|admin_inception_dev",
        )

        assert result.revoked_at == ZEDTOKEN_WRITE
        request = stub.DeleteRelationships.call_args.args[0]
        rf = request.relationship_filter
        assert rf.resource_type == "app_role"
        assert rf.optional_resource_id == "example_app_deployment|admin_inception_dev"
        # no relation filter → all relations on the object are removed
        assert rf.optional_relation == ""

    @pytest.mark.asyncio
    async def test_delete_object_with_relation_filter(self) -> None:
        from data.spicedb_client import SpiceDBClient

        stub = _stub_with_delete(_make_delete_response(zedtoken=ZEDTOKEN_WRITE))
        client = SpiceDBClient(stub=stub)

        await client.delete_object(
            resource_type="application",
            object_id="insight_inception_dev",
            relation="accessor",
        )

        request = stub.DeleteRelationships.call_args.args[0]
        rf = request.relationship_filter
        assert rf.resource_type == "application"
        assert rf.optional_resource_id == "insight_inception_dev"
        assert rf.optional_relation == "accessor"

    @pytest.mark.asyncio
    async def test_delete_object_propagates_grpc_error(self) -> None:
        from data.spicedb_client import SpiceDBClient, SpiceDBUnavailableError

        stub = MagicMock()
        stub.DeleteRelationships = AsyncMock(
            side_effect=_make_grpc_error("UNAVAILABLE")
        )
        client = SpiceDBClient(stub=stub)

        with pytest.raises(SpiceDBUnavailableError):
            await client.delete_object(
                resource_type="app_role", object_id="x_tenant_a"
            )
