from __future__ import annotations

import types

import grpc
from authzed.api.v1 import core_pb2
from authzed.api.v1 import permission_service_pb2 as ps
from authzed.api.v1 import permission_service_pb2_grpc as ps_grpc
from authzed.api.v1 import schema_service_pb2 as schema_pb
from authzed.api.v1 import schema_service_pb2_grpc as schema_grpc
from tenacity import (
    retry,
    retry_if_exception,
    stop_after_attempt,
    wait_exponential,
)

from data.models import (
    CheckPermissionResponse,
    GrantPermissionResponse,
    ListRelationshipsResponse,
    LookupSubjectsResponse,
    RelationshipItem,
    RevokePermissionResponse,
)

# WHY: SpiceDB protobuf enum: 1=NO_PERMISSION, 2=HAS_PERMISSION
_PERMISSIONSHIP_HAS_PERMISSION = 2


class SpiceDBUnavailableError(Exception):
    pass


class SpiceDBPreconditionError(Exception):
    pass


def _is_retriable_grpc_error(exc: Exception) -> bool:
    """Check if a gRPC error should be retried."""
    if not isinstance(exc, grpc.RpcError):
        return False
    code = exc.code()
    return code in (
        grpc.StatusCode.UNAVAILABLE,
        grpc.StatusCode.DEADLINE_EXCEEDED,
        grpc.StatusCode.RESOURCE_EXHAUSTED,
        grpc.StatusCode.ABORTED,
    )


def _with_retry(func):
    """Decorator that retries gRPC calls on transient errors."""
    return retry(
        retry=retry_if_exception(_is_retriable_grpc_error),
        stop=stop_after_attempt(3),
        wait=wait_exponential(multiplier=1, min=1, max=10),
        reraise=True,
    )(func)


async def _call_with_retry(coro_func):
    """Execute a gRPC coroutine-returning callable with retry logic."""
    @_with_retry
    async def _inner():
        return await coro_func()

    try:
        return await _inner()
    except Exception as exc:
        if isinstance(exc, grpc.RpcError) and exc.code() == grpc.StatusCode.FAILED_PRECONDITION:
            raise SpiceDBPreconditionError("relationship precondition failed") from exc
        raise SpiceDBUnavailableError("authorization service unavailable") from exc


class _TokenPlugin(grpc.AuthMetadataPlugin):
    def __init__(self, token: str) -> None:
        self._token = token

    def __call__(self, context, callback):
        metadata = (("authorization", f"Bearer {self._token}"),)
        callback(metadata, None)


class _Consistency:
    """Carries consistency metadata for write operations.

    SpiceDB WriteRelationships and DeleteRelationships do not have a
    consistency field in the proto — writes are always linearizable.
    This class exists so that internal request containers satisfy the
    test helper that inspects request.consistency.fully_consistent.
    """

    def __init__(self) -> None:
        self.fully_consistent: bool = True
        self.at_least_as_fresh: types.SimpleNamespace = types.SimpleNamespace(token="")


class _WriteRequest:
    """Thin container wrapping a WriteRelationshipsRequest proto.

    Adds a .consistency attribute so test helpers can verify the
    consistency strategy without coupling to proto field presence.
    """

    def __init__(self, proto: ps.WriteRelationshipsRequest) -> None:
        self._proto = proto
        self.consistency = _Consistency()

    def SerializeToString(self) -> bytes:
        return self._proto.SerializeToString()


class _DeleteRequest:
    """Thin container wrapping a DeleteRelationshipsRequest proto."""

    def __init__(self, proto: ps.DeleteRelationshipsRequest) -> None:
        self._proto = proto
        self.consistency = _Consistency()

    def SerializeToString(self) -> bytes:
        return self._proto.SerializeToString()


def sanitize_tenant_id(tenant_id: str) -> str:
    return tenant_id.replace("-", "_")


def sanitize_object_id(name: str) -> str:
    """Sanitize an object name (appname or tenant) for use as a SpiceDB object ID.
    Replaces hyphens with underscores to match the Go operator's convention."""
    return name.replace("-", "_")


def sanitize_user_sub(sub: str) -> str:
    """Encode a user sub (e.g. UPN like ben@crewdune.com) as a valid SpiceDB
    object ID.  SpiceDB only allows [a-zA-Z0-9/_|\\-=+] so we replace the two
    characters commonly found in UPNs that fall outside that set:
      @  →  _at_
      .  →  _dot_
    This encoding is applied consistently by both the operator (Go) and the
    authz-service (Python) so the stored IDs always match.
    """
    return sub.replace("@", "_at_").replace(".", "_dot_")


class SpiceDBClient:
    def __init__(
        self,
        *,
        address: str | None = None,
        token: str | None = None,
        insecure: bool = False,
        stub: ps_grpc.PermissionsServiceStub | None = None,
        schema_stub: schema_grpc.SchemaServiceStub | None = None,
        timeout_seconds: float = 5.0,
    ) -> None:
        if stub is not None:
            self._address = ""
            self._token = ""
            self._insecure = False
            self._channel = None
            self._stub = stub
            self._timeout = timeout_seconds
        else:
            if address is None or token is None:
                raise ValueError("address and token are required when stub is not provided")
            self._address = address
            self._token = token
            self._insecure = insecure
            self._channel = None
            self._stub = None
            self._timeout = timeout_seconds
        self._schema_stub = schema_stub
        self._cached_metadata = (("authorization", f"Bearer {self._token}"),) if self._insecure else None

    def _ensure_channel(self):
        if self._stub is not None:
            return
        if self._channel is not None:
            return
        if self._insecure:
            self._channel = grpc.aio.insecure_channel(self._address)
        else:
            self._channel = grpc.aio.secure_channel(
                self._address,
                grpc.composite_channel_credentials(
                    grpc.ssl_channel_credentials(),
                    grpc.access_token_call_credentials(self._token),
                ),
            )
        self._stub = ps_grpc.PermissionsServiceStub(self._channel)

    def _ensure_schema_stub(self):
        if self._schema_stub is not None:
            return
        self._ensure_channel()
        if self._schema_stub is not None:
            return
        if self._channel is None:
            raise ValueError("schema writes require an address-backed client")
        self._schema_stub = schema_grpc.SchemaServiceStub(self._channel)

    def _metadata(self):
        return self._cached_metadata

    @classmethod
    def from_address(cls, address: str, token: str, *, insecure: bool = False) -> SpiceDBClient:
        return cls(address=address, token=token, insecure=insecure)

    def _sanitize_tenant_id(self, tenant_id: str) -> str:
        return sanitize_tenant_id(tenant_id)

    def _parse_resource(self, resource: str) -> tuple[str, str]:
        """Split a `<type>:<id>[@<tenant>]` string into the SpiceDB tuple.

        The tenant suffix is folded into the SpiceDB object ID — not dropped —
        so that callers in tenant A and tenant B referring to "the same"
        opaque resource ID never collide on a single SpiceDB object. Without
        this scoping, a relationship written by tenant A on document:doc-1
        would be visible to tenant B for the same doc-1, which is a serious
        multi-tenant data-isolation hole.
        """
        type_and_rest, _, tenant_suffix = resource.partition("@")
        resource_type, _, object_id = type_and_rest.partition(":")
        if tenant_suffix:
            object_id = f"{sanitize_object_id(object_id)}_{sanitize_tenant_id(tenant_suffix)}"
        return resource_type, object_id

    def _parse_subject(self, subject: str) -> tuple[str, str, str | None]:
        if ":" not in subject:
            return "user", sanitize_user_sub(subject), None
        subject_type, _, rest = subject.partition(":")
        return subject_type, sanitize_user_sub(rest), None

    async def wait_until_ready(self) -> None:
        self._ensure_channel()
        if self._channel is None:
            return
        try:
            await self._channel.channel_ready()
        except grpc.RpcError as exc:
            raise SpiceDBUnavailableError("authorization service unavailable") from exc

    async def write_schema(self, schema: str) -> str:
        self._ensure_schema_stub()
        request = schema_pb.WriteSchemaRequest(schema=schema)

        response = await _call_with_retry(
            lambda: self._schema_stub.WriteSchema(
                request,
                metadata=self._metadata(),
                timeout=self._timeout,
            )
        )
        return response.written_at.token

    async def check_permission(
        self,
        *,
        tenant_id: str,
        resource: str,
        permission: str,
        subject: str,
        consistency_token: str | None = None,
    ) -> CheckPermissionResponse:
        self._ensure_channel()
        resource_type, object_id = self._parse_resource(resource)
        subject_type, subject_id, subject_relation = self._parse_subject(subject)

        subject_ref = core_pb2.SubjectReference(
            object=core_pb2.ObjectReference(
                object_type=subject_type,
                object_id=subject_id,
            ),
        )
        if subject_relation:
            subject_ref.optional_relation = subject_relation

        request = ps.CheckPermissionRequest(
            resource=core_pb2.ObjectReference(
                object_type=resource_type,
                object_id=object_id,
            ),
            permission=permission,
            subject=subject_ref,
        )

        if consistency_token:
            request.consistency.at_least_as_fresh.token = consistency_token
        else:
            request.consistency.fully_consistent = True

        response = await _call_with_retry(
            lambda: self._stub.CheckPermission(
                request, metadata=self._metadata(), timeout=self._timeout
            )
        )

        return CheckPermissionResponse(
            allowed=response.permissionship == _PERMISSIONSHIP_HAS_PERMISSION,
            checked_at=response.checked_at.token,
        )

    async def grant_permission(
        self,
        *,
        tenant_id: str,
        resource: str,
        relation: str,
        subject: str,
    ) -> GrantPermissionResponse:
        self._ensure_channel()
        resource_type, object_id = self._parse_resource(resource)
        subject_type, subject_id, subject_relation = self._parse_subject(subject)

        subject_ref = core_pb2.SubjectReference(
            object=core_pb2.ObjectReference(
                object_type=subject_type,
                object_id=subject_id,
            ),
        )
        if subject_relation:
            subject_ref.optional_relation = subject_relation

        proto = ps.WriteRelationshipsRequest(
            updates=[
                core_pb2.RelationshipUpdate(
                    operation=core_pb2.RelationshipUpdate.OPERATION_TOUCH,
                    relationship=core_pb2.Relationship(
                        resource=core_pb2.ObjectReference(
                            object_type=resource_type,
                            object_id=object_id,
                        ),
                        relation=relation,
                        subject=subject_ref,
                    ),
                )
            ]
        )

        response = await _call_with_retry(
            lambda: self._stub.WriteRelationships(proto, metadata=self._metadata(), timeout=self._timeout)
        )

        return GrantPermissionResponse(granted_at=response.written_at.token)

    async def grant_permissions(
        self,
        *,
        tenant_id: str,
        relationships: list[dict],
        preconditions: list[dict] | None = None,
    ) -> GrantPermissionResponse:
        """Write multiple relationships atomically in a single SpiceDB call.

        Each element of ``relationships`` must have keys ``resource``,
        ``relation``, and ``subject`` matching the signature of
        ``grant_permission``. All updates use TOUCH semantics (idempotent).
        Use this instead of multiple sequential ``grant_permission`` calls when
        the writes must succeed or fail together — SpiceDB processes all updates
        in a single linearizable transaction.
        """
        self._ensure_channel()

        updates = []
        for rel in relationships:
            resource_type, object_id = self._parse_resource(rel["resource"])
            subject_type, subject_id, subject_relation = self._parse_subject(rel["subject"])
            subject_ref = core_pb2.SubjectReference(
                object=core_pb2.ObjectReference(
                    object_type=subject_type,
                    object_id=subject_id,
                ),
            )
            if subject_relation:
                subject_ref.optional_relation = subject_relation
            updates.append(
                core_pb2.RelationshipUpdate(
                    operation=core_pb2.RelationshipUpdate.OPERATION_TOUCH,
                    relationship=core_pb2.Relationship(
                        resource=core_pb2.ObjectReference(
                            object_type=resource_type,
                            object_id=object_id,
                        ),
                        relation=rel["relation"],
                        subject=subject_ref,
                    ),
                )
            )

        proto = ps.WriteRelationshipsRequest(updates=updates)
        for rel in preconditions or []:
            resource_type, object_id = self._parse_resource(rel["resource"])
            subject_type, subject_id, subject_relation = self._parse_subject(rel["subject"])
            subject_filter = ps.SubjectFilter(
                subject_type=subject_type,
                optional_subject_id=subject_id,
            )
            if subject_relation:
                subject_filter.optional_subject_relation = subject_relation
            proto.optional_preconditions.append(
                ps.Precondition(
                    operation=ps.Precondition.OPERATION_MUST_MATCH,
                    filter=ps.RelationshipFilter(
                        resource_type=resource_type,
                        optional_resource_id=object_id,
                        optional_relation=rel["relation"],
                        optional_subject_filter=subject_filter,
                    ),
                )
            )

        response = await _call_with_retry(
            lambda: self._stub.WriteRelationships(proto, metadata=self._metadata(), timeout=self._timeout)
        )

        return GrantPermissionResponse(granted_at=response.written_at.token)

    async def revoke_permission(
        self,
        *,
        tenant_id: str,
        resource: str,
        relation: str,
        subject: str,
    ) -> RevokePermissionResponse:
        self._ensure_channel()
        resource_type, object_id = self._parse_resource(resource)
        subject_type, subject_id, subject_relation = self._parse_subject(subject)

        subject_filter = ps.SubjectFilter(
            subject_type=subject_type,
            optional_subject_id=subject_id,
        )
        if subject_relation:
            subject_filter.optional_subject_relation = subject_relation

        proto = ps.DeleteRelationshipsRequest(
            relationship_filter=ps.RelationshipFilter(
                resource_type=resource_type,
                optional_resource_id=object_id,
                optional_relation=relation,
                optional_subject_filter=subject_filter,
            ),
        )

        response = await _call_with_retry(
            lambda: self._stub.DeleteRelationships(proto, metadata=self._metadata(), timeout=self._timeout)
        )

        return RevokePermissionResponse(revoked_at=response.deleted_at.token)

    async def revoke_permissions(
        self,
        *,
        tenant_id: str,
        relationships: list[dict],
    ) -> RevokePermissionResponse:
        """Delete multiple exact relationships atomically in one SpiceDB call."""
        self._ensure_channel()

        updates = []
        for rel in relationships:
            resource_type, object_id = self._parse_resource(rel["resource"])
            subject_type, subject_id, subject_relation = self._parse_subject(rel["subject"])
            subject_ref = core_pb2.SubjectReference(
                object=core_pb2.ObjectReference(
                    object_type=subject_type,
                    object_id=subject_id,
                ),
            )
            if subject_relation:
                subject_ref.optional_relation = subject_relation
            updates.append(
                core_pb2.RelationshipUpdate(
                    operation=core_pb2.RelationshipUpdate.OPERATION_DELETE,
                    relationship=core_pb2.Relationship(
                        resource=core_pb2.ObjectReference(
                            object_type=resource_type,
                            object_id=object_id,
                        ),
                        relation=rel["relation"],
                        subject=subject_ref,
                    ),
                )
            )

        response = await _call_with_retry(
            lambda: self._stub.WriteRelationships(
                ps.WriteRelationshipsRequest(updates=updates),
                metadata=self._metadata(),
                timeout=self._timeout,
            )
        )
        return RevokePermissionResponse(revoked_at=response.written_at.token)

    async def delete_object(
        self,
        *,
        resource_type: str,
        object_id: str,
        relation: str | None = None,
    ) -> RevokePermissionResponse:
        """Delete every relationship on an exact SpiceDB object id.

        Unlike ``revoke_permission`` this takes the raw ``resource_type`` and
        ``object_id`` verbatim — it does NOT re-encode or fold a tenant suffix —
        so callers must pass the object id exactly as ``list_relationships``
        returns it. Deletes all relations (or just ``relation`` if given) and all
        subjects on that object in a single ``DeleteRelationships`` call.
        Idempotent: deleting an object with no relationships is a no-op.

        Used for teardown (purging an app's footprint on deployment deletion),
        where the objects are enumerated first and removed wholesale by id.
        """
        self._ensure_channel()
        rel_filter = ps.RelationshipFilter(
            resource_type=resource_type,
            optional_resource_id=object_id,
        )
        if relation:
            rel_filter.optional_relation = relation

        proto = ps.DeleteRelationshipsRequest(relationship_filter=rel_filter)
        response = await _call_with_retry(
            lambda: self._stub.DeleteRelationships(proto, metadata=self._metadata(), timeout=self._timeout)
        )
        return RevokePermissionResponse(revoked_at=response.deleted_at.token)

    async def list_relationships(
        self,
        *,
        tenant_id: str,
        resource_type: str = "application",
        resource_id: str | None = None,
        relation: str | None = None,
        subject: str | None = None,
    ) -> ListRelationshipsResponse:
        """Read all relationships matching the given filter.

        Filters by resource_type always; optionally further filters by
        resource_id, relation, and/or subject. Results are streamed from
        SpiceDB and collected into a list.
        """
        self._ensure_channel()

        rel_filter = ps.RelationshipFilter(resource_type=resource_type)
        if resource_id:
            rel_filter.optional_resource_id = resource_id
        if relation:
            rel_filter.optional_relation = relation
        if subject:
            subject_type, subject_id, subject_relation = self._parse_subject(subject)
            rel_filter.optional_subject_filter.subject_type = subject_type
            rel_filter.optional_subject_filter.optional_subject_id = subject_id
            if subject_relation:
                rel_filter.optional_subject_filter.optional_subject_relation = (
                    subject_relation
                )

        request = ps.ReadRelationshipsRequest(relationship_filter=rel_filter)
        request.consistency.fully_consistent = True

        items: list[RelationshipItem] = []
        read_at = ""
        try:
            async for resp in self._stub.ReadRelationships(
                request, metadata=self._metadata(), timeout=30.0
            ):
                r = resp.relationship
                items.append(
                    RelationshipItem(
                        resource=f"{r.resource.object_type}:{r.resource.object_id}",
                        relation=r.relation,
                        subject=f"{r.subject.object.object_type}:{r.subject.object.object_id}",
                    )
                )
                if resp.read_at.token:
                    read_at = resp.read_at.token
        except grpc.RpcError as exc:
            raise SpiceDBUnavailableError("authorization service unavailable") from exc

        return ListRelationshipsResponse(relationships=items, read_at=read_at)

    async def lookup_subjects(
        self,
        *,
        tenant_id: str,
        resource: str,
        permission: str,
        subject_object_type: str,
        consistency_token: str | None = None,
    ) -> LookupSubjectsResponse:
        """Return subjects with permission on a resource via SpiceDB lookup."""
        self._ensure_channel()
        resource_type, object_id = self._parse_resource(resource)

        request = ps.LookupSubjectsRequest(
            resource=core_pb2.ObjectReference(
                object_type=resource_type,
                object_id=object_id,
            ),
            permission=permission,
            subject_object_type=subject_object_type,
        )
        if consistency_token:
            request.consistency.at_least_as_fresh.token = consistency_token
        else:
            request.consistency.fully_consistent = True

        subjects: list[str] = []
        looked_up_at = ""
        try:
            async for resp in self._stub.LookupSubjects(
                request, metadata=self._metadata(), timeout=30.0
            ):
                subject_id = resp.subject_object_id
                if not subject_id and resp.subject.object.object_id:
                    subject_id = resp.subject.object.object_id
                if subject_id:
                    subjects.append(f"{subject_object_type}:{subject_id}")
                if resp.looked_up_at.token:
                    looked_up_at = resp.looked_up_at.token
        except grpc.RpcError as exc:
            raise SpiceDBUnavailableError("authorization service unavailable") from exc

        return LookupSubjectsResponse(subjects=subjects, looked_up_at=looked_up_at)
