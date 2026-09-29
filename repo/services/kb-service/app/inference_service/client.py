"""inference-service gRPC client for kb-service.

``InferenceServiceGRPCClient`` wraps the internal service-to-service RPC
``inference.internal.v1.InferenceEndpointResolver/ResolveInternalEndpoint``:
given ``(tenant_id, served_model_name)`` it returns the published inference
service's runtime address snapshot (``base_url``), the canonical
``served_model_name``, the ``task`` (generate | embed) and the ``status``.
kb-service then passes ``base_url`` + ``served_model_name`` through to
rag-engine (Embed/Generate) so the stateless RPCs connect directly to the
tenant's own inference service — never through the AI Gateway, never with a
tenant JWT or user API Key.

``base_url`` is a runtime snapshot: callers must NOT cache it permanently.
Resolution happens per call; on 404/503/endpoint errors the next call simply
re-resolves.

Error contract (gRPC code → exception):
    NOT_FOUND                → InferenceServiceNotFoundError
                                 (当前租户没有该 served_model_name)
    FAILED_PRECONDITION
      INFERENCE_SERVICE_NOT_READY → InferenceServiceNotReadyError
                                 (等待后重试；本客户端做一次短暂重试)
      RUNTIME_ENDPOINT_MISSING|INVALID → InferenceServiceEndpointError
                                 (重试并记录告警)
    INVALID_ARGUMENT         → InferenceServiceClientError
                                 (检查 tenant_id / served_model_name)

Channel binding follows ``RagEngineGRPCClient``: the channel is created
lazily on first async call so it binds to the event loop of the caller
(the gRPC server's dedicated loop), not the loop active during ``__init__``.
"""
from __future__ import annotations

import asyncio
from dataclasses import dataclass

import grpc

from app.generated.inference.resolver.v1 import (
    inference_endpoint_resolver_pb2 as _pb,
)
from app.generated.inference.resolver.v1 import (
    inference_endpoint_resolver_pb2_grpc as _pb_grpc,
)

_NOT_READY_RETRY_DELAY_SECONDS = 0.5


@dataclass(frozen=True)
class ResolvedEndpoint:
    """Result of a successful ResolveInternalEndpoint call."""

    base_url: str
    served_model_name: str
    task: str  # "generate" | "embed"
    status: str  # resolved services are always "running"


class InferenceServiceNotFoundError(Exception):
    """The (tenant_id, served_model_name) pair has no published service."""


class InferenceServiceNotReadyError(Exception):
    """The service exists but is not running yet (INFERENCE_SERVICE_NOT_READY)."""


class InferenceServiceEndpointError(Exception):
    """The service is running but its runtime endpoint is missing/invalid."""


class InferenceServiceClientError(Exception):
    """Unexpected error talking to inference-service."""

    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class InferenceServiceGRPCClient:
    """gRPC client for ``inference.internal.v1.InferenceEndpointResolver``.

    Single call: ``resolve_endpoint`` → :class:`ResolvedEndpoint`. The
    deprecated service_id lookup stays compatible server-side, but this
    client always sends ``served_model_name`` (preferred key).
    """

    def __init__(
        self,
        addr: str = "inference-service.ani-system.svc.cluster.local:9104",
        *,
        channel: grpc.aio.Channel | None = None,
        timeout: float = 10.0,
        not_ready_retries: int = 1,
    ) -> None:
        self._addr = addr
        self._channel = channel  # may be None — created lazily in _ensure_channel
        self._stub = None
        self._timeout = timeout
        self._not_ready_retries = not_ready_retries

    def _ensure_channel(self) -> None:
        """Lazily create the gRPC channel + stub on first use.

        Binds the channel to the caller's event loop, not the loop active
        during ``__init__`` (gRPC server dedicated-loop architecture).
        """
        if self._channel is None:
            self._channel = grpc.aio.insecure_channel(self._addr)
        if self._stub is None:
            self._stub = _pb_grpc.InferenceEndpointResolverStub(self._channel)

    async def aclose(self) -> None:
        if self._channel is not None:
            await self._channel.close()

    async def __aenter__(self) -> "InferenceServiceGRPCClient":
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        await self.aclose()

    async def resolve_endpoint(
        self, *, tenant_id: str, served_model_name: str
    ) -> ResolvedEndpoint:
        """Resolve a published ``served_model_name`` to its runtime snapshot.

        Args:
            tenant_id: KB owner tenant (model publication is per tenant).
            served_model_name: The KB's embedding/inference model name.

        Returns:
            A :class:`ResolvedEndpoint` whose ``base_url`` backs the OpenAI
            client (``{base_url}/chat/completions`` or ``{base_url}/embeddings``
            per ``task``) and whose ``served_model_name`` MUST be used as the
            ``model`` in the request body.

        Raises:
            InferenceServiceNotFoundError: no published service matches the
                (tenant_id, served_model_name) pair (NOT_FOUND).
            InferenceServiceNotReadyError: service exists but not running
                (INFERENCE_SERVICE_NOT_READY) — retried once, then raised.
            InferenceServiceEndpointError: runtime endpoint missing/invalid
                (RUNTIME_ENDPOINT_MISSING / RUNTIME_ENDPOINT_INVALID).
            InferenceServiceClientError: transport/unexpected failure.
        """
        request = _pb.ResolveInternalEndpointRequest(
            tenant_id=tenant_id,
            served_model_name=served_model_name,
        )
        self._ensure_channel()
        attempts = max(1, self._not_ready_retries + 1)
        for attempt in range(1, attempts + 1):
            try:
                resp = await self._stub.ResolveInternalEndpoint(
                    request, timeout=self._timeout
                )
                break
            except grpc.aio.AioRpcError as exc:
                code = exc.code()
                if code == grpc.StatusCode.NOT_FOUND:
                    raise InferenceServiceNotFoundError(
                        f"no published inference service for model "
                        f"{served_model_name!r} (tenant {tenant_id})"
                    ) from exc
                if (
                    code == grpc.StatusCode.FAILED_PRECONDITION
                    and "INFERENCE_SERVICE_NOT_READY" in (exc.details() or "")
                ):
                    if attempt < attempts:
                        # 等待后重试：服务可能在发布/启动中。
                        await asyncio.sleep(_NOT_READY_RETRY_DELAY_SECONDS)
                        continue
                    raise InferenceServiceNotReadyError(
                        f"inference service {served_model_name!r} "
                        f"(tenant {tenant_id}) is not ready: {exc.details()}"
                    ) from exc
                if (
                    code == grpc.StatusCode.FAILED_PRECONDITION
                    and "RUNTIME_ENDPOINT_" in (exc.details() or "")
                ):
                    # 重试并记录告警：调用方 logger.warning + 下次调用重解析。
                    raise InferenceServiceEndpointError(
                        f"inference service {served_model_name!r} "
                        f"(tenant {tenant_id}) runtime endpoint unusable: "
                        f"{exc.details()}"
                    ) from exc
                if code == grpc.StatusCode.INVALID_ARGUMENT:
                    raise InferenceServiceClientError(
                        f"ResolveInternalEndpoint rejected tenant_id="
                        f"{tenant_id!r} served_model_name="
                        f"{served_model_name!r}: {exc.details()}",
                        status_code=code.value[0],
                    ) from exc
                raise InferenceServiceClientError(
                    f"ResolveInternalEndpoint failed: {exc.details()}",
                    status_code=(
                        code.value[0] if code is not None else None
                    ),
                ) from exc
        return ResolvedEndpoint(
            base_url=resp.base_url,
            served_model_name=resp.served_model_name,
            task=resp.task,
            status=resp.status,
        )
