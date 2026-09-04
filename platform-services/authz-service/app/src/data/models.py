from __future__ import annotations

from datetime import datetime

from pydantic import BaseModel, ConfigDict, Field


class CheckPermissionResponse(BaseModel):
    allowed: bool = Field(description="Whether the subject has the requested permission")
    checked_at: str = Field(
        description="ZedToken representing the snapshot at which this check was performed"
    )


class GrantPermissionResponse(BaseModel):
    granted_at: str = Field(
        description="ZedToken representing the revision at which the relationship was written"
    )


class RelationshipItem(BaseModel):
    resource: str = Field(description="Resource in the form '<type>:<id>'")
    relation: str = Field(description="Relation name")
    subject: str = Field(description="Subject in the form '<type>:<id>'")


class ListRelationshipsResponse(BaseModel):
    relationships: list[RelationshipItem]
    read_at: str = Field(description="ZedToken at which this read was performed")


class LookupSubjectsResponse(BaseModel):
    subjects: list[str] = Field(description="Subject IDs returned by LookupSubjects")
    looked_up_at: str = Field(description="ZedToken at which this lookup was performed")


class RevokePermissionResponse(BaseModel):
    revoked_at: str = Field(
        description="ZedToken representing the revision at which the relationship was deleted"
    )


class CreateRoleRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    name: str
    display_name: str
    description: str | None = None
    parent_role_id: str | None = None
    metadata: dict[str, str] | None = None


class RoleResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    role_id: str
    name: str
    display_name: str
    description: str | None
    parent_role_id: str | None
    tenant_id: str
    created_at: datetime
    created_by: str
    consistency_token: str


class RoleListResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    roles: list[RoleResponse]
    next_page_token: str | None


class AddMemberRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    user_id: str


class MemberResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    role_id: str
    user_id: str
    granted_at: str
    granted_by: str


class MemberListResponse(BaseModel):
    model_config = ConfigDict(extra="forbid")

    members: list[str]
    next_page_token: str | None
    total_count: int
