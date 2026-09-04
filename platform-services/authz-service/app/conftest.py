"""Root-level conftest: patches grpc.RpcError so test helpers can mock it.

WHY: grpc.RpcError is a plain exception class; concrete subclasses like
grpc._channel._InactiveRpcError mix in grpc.Call which provides .code()
and .details(). Test helpers use MagicMock(spec=grpc.RpcError) and then
set .code.return_value, which fails if grpc.RpcError has no 'code' attr.
Adding the methods here makes the spec match the real exception behaviour.
"""

from __future__ import annotations

import grpc


def _grpc_rpc_error_code(self: object) -> grpc.StatusCode:  # pragma: no cover
    return grpc.StatusCode.UNKNOWN


def _grpc_rpc_error_details(self: object) -> str:  # pragma: no cover
    return ""


if not hasattr(grpc.RpcError, "code"):
    grpc.RpcError.code = _grpc_rpc_error_code  # type: ignore[attr-defined]

if not hasattr(grpc.RpcError, "details"):
    grpc.RpcError.details = _grpc_rpc_error_details  # type: ignore[attr-defined]
