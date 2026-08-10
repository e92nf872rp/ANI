"""Embedding model singleton (US-013 / SPEC §5.1, §1.3).

The embedding model is served by the AI inference service via an OpenAI
compatible ``/v1/embeddings`` endpoint. rag-engine calls the remote service
through a LlamaIndex :class:`BaseEmbedding` subclass backed by the ``openai``
Python SDK, so write-side and query-side embeddings share the same model
endpoint (SPEC §1.3 "嵌入统一"). The model is constructed once at startup and
re-used by ``embed_service`` and ``milvus.py`` (via
``VectorStoreIndex.from_vector_store(..., embed_model=...)``).

We implement a custom :class:`OpenAICompatibleEmbedding` (subclass of
``llama_index.core.embeddings.BaseEmbedding``) rather than using
``llama_index.embeddings.openai.OpenAIEmbedding`` because the latter
validates ``model`` against a fixed OpenAI model enum and rejects custom
model names (e.g. ``Qwen3-Embedding-0.6B``). The inference service exposes
arbitrary model names, so we need a pass-through adapter.

The interim default endpoint (``http://10.10.20.197:8006/v1`` +
``Qwen3-Embedding-0.6B``) is a temporary embedding service; it will be
replaced by the formal inference-service address once that service deploys
an embedding model.
"""
from __future__ import annotations

from typing import Any

from app.core.config import settings

_model = None  # type: ignore[var-annotated]
# Cached BaseEmbedding wrapper around ``_model``. Built once on first
# ``get_embed_model()`` call so LlamaIndex's embedding cache is preserved
# across ``embed_and_write`` / ``as_retriever`` calls (SPEC §1.3 "嵌入统一").
_wrapped_model = None  # type: ignore[var-annotated]


def _make_openai_client(api_base: str, api_key: str, timeout: float):
    """Build an ``openai.OpenAI`` client (factory, kept module-level so it
    can be monkeypatched in tests without touching the pydantic model)."""
    from openai import OpenAI

    return OpenAI(
        base_url=api_base,
        # ``api_key`` must be non-empty for the SDK; "EMPTY" is the convention
        # for no-auth OpenAI-compatible servers (e.g. vLLM/sglang).
        api_key=api_key or "EMPTY",
        timeout=timeout,
    )


class OpenAICompatibleEmbedding:
    """Remote embedding adapter for an OpenAI-compatible ``/v1/embeddings``
    endpoint that serves arbitrary (non-OpenAI) model names.

    This adapter is framework-light (not a pydantic ``BaseEmbedding`` subclass)
    so it avoids the enum validation that ``OpenAIEmbedding`` performs on the
    model name. It exposes the ``get_text_embedding`` /
    ``get_text_embedding_batch`` / ``get_query_embedding`` surface that
    LlamaIndex's Index layer calls during ``insert_nodes`` and
    ``as_retriever``.

    LlamaIndex's ``resolve_embeddings`` enforces ``isinstance(embed_model,
    BaseEmbedding)``, so :func:`get_embed_model` wraps this adapter in a real
    ``BaseEmbedding`` subclass (via :func:`_as_base_embedding`) before handing
    it to ``VectorStoreIndex.from_vector_store``. The wrapper delegates every
    embedding call back to this adapter, which calls the remote endpoint.
    """

    def __init__(
        self,
        *,
        model: str,
        api_base: str,
        api_key: str = "",
        embed_batch_size: int = 100,
        timeout: float = 60.0,
    ) -> None:
        self._model = model
        self._batch_size = embed_batch_size
        self._client = _make_openai_client(api_base, api_key, timeout)

    # ── LlamaIndex embedding API surface ────────────────────────────────────

    def get_text_embedding(self, text: str) -> list[float]:
        return self.get_text_embedding_batch([text])[0]

    def get_query_embedding(self, text: str) -> list[float]:
        # Write and query share the same embedding endpoint (SPEC §1.3).
        return self.get_text_embedding(text)

    def get_text_embedding_batch(self, texts: list[str]) -> list[list[float]]:
        # The OpenAI SDK accepts a list of strings as ``input`` and returns a
        # list of embedding vectors in the same order.
        out: list[list[float]] = []
        for i in range(0, len(texts), self._batch_size):
            chunk = texts[i : i + self._batch_size]
            resp = self._client.embeddings.create(model=self._model, input=chunk)
            # Preserve input order (the API returns data in request order, but
            # we sort by index defensively).
            ordered = sorted(resp.data, key=lambda d: d.index)
            out.extend([list(map(float, e)) for e in (d.embedding for d in ordered)])
        return out

    # ── Metadata used by LlamaIndex for batching / async dispatch ────────────

    @property
    def model_name(self) -> str:
        return self._model


def _as_base_embedding(adapter: OpenAICompatibleEmbedding):
    """Wrap a duck-typed adapter in a real ``BaseEmbedding`` subclass when the
    installed LlamaIndex version enforces ``isinstance(..., BaseEmbedding)`` in
    ``resolve_embeddings``. We build a minimal subclass that delegates to the
    adapter so the remote endpoint is still used and no model enum is involved.
    """
    from llama_index.core.embeddings import BaseEmbedding

    class _RemoteEmbedding(BaseEmbedding):
        model_name: str = ""

        def __init__(self, **data: Any) -> None:
            super().__init__(**data)
            self._adapter = adapter

        def _get_query_embedding(self, text: str) -> list[float]:
            return self._adapter.get_query_embedding(text)

        def _get_text_embedding(self, text: str) -> list[float]:
            return self._adapter.get_text_embedding(text)

        async def _aget_query_embedding(self, text: str) -> list[float]:
            # Delegate to a thread so the sync HTTP call doesn't block the
            # event loop (LlamaIndex calls this from _aretrieve).
            import asyncio
            return await asyncio.to_thread(self._adapter.get_query_embedding, text)

        async def _aget_text_embedding(self, text: str) -> list[float]:
            import asyncio
            return await asyncio.to_thread(self._adapter.get_text_embedding, text)

        def _get_text_embeddings(self, texts: list[str]) -> list[list[float]]:
            return self._adapter.get_text_embedding_batch(texts)

    return _RemoteEmbedding(model_name=adapter.model_name)


async def init_embedding_model(model_name: str | None = None) -> None:
    """Initialise the remote embedding singleton.

    Called once at app startup (see ``main.py``). Connects to the AI
    inference service's OpenAI-compatible ``/v1/embeddings`` endpoint.
    """
    global _model, _wrapped_model
    # Close the previous OpenAI client (if any) to release its httpx
    # connection pool before replacing the singleton.
    if _model is not None:
        try:
            _model._client.close()
        except Exception:  # noqa: BLE001, S110 — best-effort close at shutdown
            pass
    name = model_name or settings.embedding_model
    _model = OpenAICompatibleEmbedding(
        model=name,
        api_base=settings.embedding_api_base,
        api_key=settings.embedding_api_key,
        embed_batch_size=100,
    )
    # Invalidate the cached wrapper so the next get_embed_model() rebuilds it.
    _wrapped_model = None


def get_embed_model():
    """Return the initialised embedding singleton wrapped as a LlamaIndex
    ``BaseEmbedding`` so ``VectorStoreIndex.from_vector_store`` accepts it.

    The wrapper is cached so LlamaIndex's internal embedding cache survives
    across calls (important for the write + query paths sharing the same
    cache, SPEC §1.3 "嵌入统一").

    Raises ``RuntimeError`` if ``init_embedding_model`` has not been called.
    """
    global _wrapped_model
    if _model is None:
        raise RuntimeError(
            "embedding model not initialised; call init_embedding_model() first"
        )
    if _wrapped_model is None:
        # Wrap in a BaseEmbedding subclass so LlamaIndex's resolve_embeddings
        # isinstance check passes. The wrapper delegates to the remote adapter.
        _wrapped_model = _as_base_embedding(_model)
    return _wrapped_model


def embed(texts: list[str]) -> list[list[float]]:
    """Embed a batch of texts using the unified remote embedding model.

    Returns a list of float vectors. Used by both the write path
    (``embed_service``) and the query path (``retrieve_service``) to guarantee
    embedding consistency (SPEC §1.3).
    """
    model = get_embed_model()
    return model.get_text_embedding_batch(texts)
