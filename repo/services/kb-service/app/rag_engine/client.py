"""rag-engine gRPC client for kb-service (SPEC §2.1, §2.4, §6.1; Plan §3.4).

``RagEngineGRPCClient`` is the stateless RPC client (Parse/Embed/
Generate/GenerateStream) used by kb-service's retrieve_service and
parse_orchestrator. Connects to rag-engine gRPC :50052.

The gRPC client deserializes the flattened EmbedResponse:
    vectors[i] = list(response.vectors_flat[i*dim:(i+1)*dim])
(see rag.proto EmbedResponse — flattening avoids nested repeated float
serialization complexity).
"""
from __future__ import annotations

from typing import Any, AsyncIterator

import grpc

from . import rag_pb2
from . import rag_pb2_grpc


class RagEngineError(Exception):
    """Error from the rag-engine gRPC call."""

    def __init__(self, message: str, *, status_code: int | None = None) -> None:
        super().__init__(message)
        self.status_code = status_code


class RagEngineGRPCClient:
    """gRPC client for rag-engine stateless RPCs (Plan §3.4).

    Connects to rag-engine gRPC (default localhost:50052) and exposes
    parse/embed/generate/generate_stream. Used by kb-service's
    retrieve_service (Embed) and parse_orchestrator (Parse/Generate).

    The gRPC channel is created lazily on first async call so it binds
    to the event loop of the caller (the gRPC server's dedicated loop),
    not the loop that was active when __init__ ran. This avoids the
    "Future attached to a different loop" error that occurs when the
    client is constructed on the uvicorn loop but used from the gRPC
    server's separate loop.
    """

    def __init__(
        self,
        addr: str = "localhost:50052",
        *,
        channel: grpc.aio.Channel | None = None,
        timeout: float = 120.0,
    ) -> None:
        self._addr = addr
        self._channel = channel  # may be None — created lazily in _ensure_channel
        self._stub = None
        # Per-RPC deadline (seconds). Matches the legacy REST client default.
        # GenerateStream is excluded — streaming calls use no deadline so the
        # iterator can yield tokens for as long as the model generates.
        self._timeout = timeout

    def _ensure_channel(self) -> None:
        """Lazily create the gRPC channel + stub on first use.

        This binds the channel to the event loop of the caller, not the
        loop active during __init__. Critical for the gRPC server's
        dedicated event loop architecture.
        """
        if self._channel is None:
            self._channel = grpc.aio.insecure_channel(self._addr)
        if self._stub is None:
            self._stub = rag_pb2_grpc.RagEngineStub(self._channel)

    async def aclose(self) -> None:
        if self._channel is not None:
            await self._channel.close()

    async def __aenter__(self) -> "RagEngineGRPCClient":
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        await self.aclose()

    async def parse(
        self,
        *,
        download_url: str,
        file_name: str,
        file_type: str,
        chunk_size: int = 1024,
    ) -> list[dict[str, Any]]:
        """Call Parse RPC (pass download_url; rag-engine downloads the file).

        Returns a list of chunk dicts: {chunk_id, content, content_type,
        page_number, parent_content, chunk_type, metadata_json, image_bytes,
        image_format, parent_chunk_id}.
        """
        request = rag_pb2.ParseRequest(
            download_url=download_url,
            file_name=file_name,
            file_type=file_type,
            chunk_size=chunk_size,
        )
        self._ensure_channel()
        resp: rag_pb2.ParseResponse = await self._stub.Parse(request, timeout=self._timeout)
        out: list[dict[str, Any]] = []
        for c in resp.chunks:
            out.append({
                "chunk_id": c.chunk_id,
                "content": c.content,
                "content_type": c.content_type,
                "page_number": c.page_number,
                "parent_content": c.parent_content,
                "chunk_type": c.chunk_type,
                "metadata_json": c.metadata_json,
                "image_bytes": c.image_bytes,
                "image_format": c.image_format,
                "parent_chunk_id": c.parent_chunk_id,
            })
        return out

    async def embed(self, *, texts: list[str]) -> tuple[list[list[float]], int]:
        """Call Embed RPC and deserialize the flattened vectors array.

        EmbedResponse stores a 1-D ``vectors_flat`` array + ``dimension`` +
        ``count``. This method reconstructs the per-text vectors:
            vectors[i] = list(vectors_flat[i*dim:(i+1)*dim])
        Returns (vectors, dimension).
        """
        request = rag_pb2.EmbedRequest(texts=texts)
        self._ensure_channel()
        resp: rag_pb2.EmbedResponse = await self._stub.Embed(request, timeout=self._timeout)
        dim = resp.dimension
        flat = list(resp.vectors_flat)
        if dim <= 0 or resp.count <= 0:
            return [], dim
        # Guard against a malformed response where the flat array length does
        # not match count * dimension (e.g. truncated transport / server bug).
        expected = resp.count * dim
        if len(flat) < expected:
            raise RagEngineError(
                f"embed: vectors_flat length {len(flat)} < count*dim {expected}"
            )
        vectors: list[list[float]] = []
        for i in range(resp.count):
            start = i * dim
            end = start + dim
            vectors.append(list(flat[start:end]))
        return vectors, dim

    async def generate(
        self,
        *,
        question: str,
        session_id: str = "",
        context: list[dict[str, Any]] | None = None,
        history: list[dict[str, str]] | None = None,
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> dict[str, Any]:
        """Call Generate RPC.

        ``history`` includes the current-turn user message (reproduces the
        legacy behavior where kb-service appends user to Redis before calling
        rag-engine). The Generate RPC appends ``question`` as the final USER
        message (reproduces the {query_str} template).

        Returns {answer, input_tokens, output_tokens, session_id}.
        """
        request = rag_pb2.GenerateRequest(
            question=question,
            session_id=session_id,
            context=_build_source_chunks(context or []),
            inference_service_name=inference_service_name,
            max_tokens=max_tokens,
            history=_build_chat_messages(history or []),
        )
        self._ensure_channel()
        resp: rag_pb2.GenerateResponse = await self._stub.Generate(request, timeout=self._timeout)
        return {
            "answer": resp.answer,
            "input_tokens": resp.input_tokens,
            "output_tokens": resp.output_tokens,
            "session_id": resp.session_id,
        }

    async def generate_stream(
        self,
        *,
        question: str,
        session_id: str = "",
        context: list[dict[str, Any]] | None = None,
        history: list[dict[str, str]] | None = None,
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> AsyncIterator[dict[str, Any]]:
        """Call GenerateStream RPC; async iterator of token dicts.

        Yields {content, done, input_tokens, output_tokens}. The final
        ``done=True`` item carries the token usage.
        """
        request = rag_pb2.GenerateRequest(
            question=question,
            session_id=session_id,
            context=_build_source_chunks(context or []),
            inference_service_name=inference_service_name,
            max_tokens=max_tokens,
            history=_build_chat_messages(history or []),
        )
        self._ensure_channel()
        async for tok in self._stub.GenerateStream(request):
            yield {
                "content": tok.content,
                "done": tok.done,
                "input_tokens": tok.input_tokens,
                "output_tokens": tok.output_tokens,
            }


def _build_source_chunks(context: list[dict[str, Any]]) -> list[rag_pb2.SourceChunk]:
    """Convert kb-service source dicts to proto SourceChunk messages."""
    out: list[rag_pb2.SourceChunk] = []
    for c in context:
        out.append(rag_pb2.SourceChunk(
            chunk_id=str(c.get("chunk_id", "")),
            doc_id=str(c.get("doc_id", "")),
            file_name=str(c.get("file_name", "")),
            page=int(c.get("page", 0) or 0),
            content=str(c.get("content", "")),
            score=float(c.get("score", 0.0) or 0.0),
        ))
    return out


def _build_chat_messages(history: list[dict[str, str]]) -> list[rag_pb2.ChatMessage]:
    """Convert role/content dicts to proto ChatMessage messages."""
    out: list[rag_pb2.ChatMessage] = []
    for m in history:
        out.append(rag_pb2.ChatMessage(
            role=str(m.get("role", "")),
            content=str(m.get("content", "")),
        ))
    return out
