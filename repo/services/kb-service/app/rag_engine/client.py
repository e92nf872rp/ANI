"""rag-engine client for kb-service Query (SPEC §2.1, §2.4, §6.1).

SPEC §2.1 specifies that kb-service calls rag-engine Query over gRPC. As of
this batch the rag-engine exposes a REST endpoint
(`POST /api/v1/kb/{kb_id}/query`) and no rag.proto/gRPC server exists yet.
This client implements the Query call over the available REST endpoint while
preserving a gRPC-style interface (async `query(...)` returning a
QueryResponse-like dict) so the gRPC servicer and tests can be written against
a stable contract. When a rag.proto is introduced, swap the transport to
grpcio without changing call sites.

The gRPC intent is preserved in the interface; only the transport is REST
until the rag-engine gRPC server lands.
"""
from __future__ import annotations

from typing import Any

import httpx


class RagEngineError(Exception):
    """Error from the rag-engine Query call."""

    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class RagEngineClient:
    """Async client for the rag-engine Query RPC.

    The base URL is the rag-engine HTTP address (settings.rag_engine_addr
    with http:// scheme). When a gRPC server is added, this class will switch
    to a grpc.aio channel while keeping the `query()` signature.
    """

    def __init__(
        self,
        *,
        base_url: str,
        timeout: float = 120.0,
        client: httpx.AsyncClient | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._client = client or httpx.AsyncClient(
            base_url=self._base_url, timeout=timeout
        )
        self._owns_client = client is None

    async def aclose(self) -> None:
        if self._owns_client:
            await self._client.aclose()

    async def __aenter__(self) -> "RagEngineClient":
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        await self.aclose()

    async def query(
        self,
        *,
        kb_id: str,
        tenant_id: str,
        question: str,
        session_id: str | None = None,
        top_k: int = 5,
        score_threshold: float = 0.3,
        retrieval_mode: str | None = None,
        inference_service_name: str = "default",
    ) -> dict[str, Any]:
        """Call the rag-engine Query RPC (currently over REST, gRPC-intent).

        Returns a dict matching the kb_service.proto QueryResponse shape:
            {answer, sources: [{doc_id, file_name, page, content, score}],
             session_id, input_tokens, output_tokens}
        """
        body: dict[str, Any] = {
            "kb_id": kb_id,
            "tenant_id": tenant_id,
            "question": question,
            "top_k": top_k,
            "score_threshold": score_threshold,
            "inference_service_name": inference_service_name,
        }
        if session_id:
            body["session_id"] = session_id
        if retrieval_mode:
            body["retrieval_mode"] = retrieval_mode
        resp = await self._client.post(f"/api/v1/kb/{kb_id}/query", json=body)
        if resp.status_code != 200:
            try:
                detail = resp.json().get("detail", resp.text)
            except Exception:
                detail = resp.text
            raise RagEngineError(
                f"rag-engine query failed: {detail}",
                status_code=resp.status_code,
            )
        return resp.json()
