"""Embedding model registry (US-013 / SPEC §5.1, §1.3, model switching M2).

The embedding model is served by the AI inference service via an OpenAI
compatible ``/v1/embeddings`` endpoint. rag-engine calls the remote service
through a lightweight adapter backed by the ``openai`` Python SDK, so
write-side and query-side embeddings share the same model endpoint
(SPEC §1.3 "嵌入统一").

Adapters are cached per model name (M2): the Embed RPC carries the KB's
``embedding_model`` in ``EmbedRequest.model``, and the registry lazily
creates one :class:`OpenAICompatibleEmbedding` per distinct model name.
The default model (``settings.embedding_model``) is pre-warmed at startup
so legacy callers that pass an empty ``model`` behave exactly as before.

We implement a custom :class:`OpenAICompatibleEmbedding` rather than using
``llama_index.embeddings.openai.OpenAIEmbedding`` because the latter
validates ``model`` against a fixed OpenAI model enum and rejects custom
model names (e.g. ``BAAI/bge-m3``). The inference service exposes
arbitrary model names, so we need a pass-through adapter. Note the remote
endpoint requires the FULL prefixed model name ("BAAI/bge-m3"); bare
aliases ("bge-m3") are NOT recognised.
"""
from __future__ import annotations

import threading

from app.core.config import settings

# Per-model adapter cache: model name -> OpenAICompatibleEmbedding.
# The entry registered by ``init_embedding_model`` is the default that
# legacy ``get_embed_model()`` (no-arg) resolves to.
_models: dict[str, OpenAICompatibleEmbedding] = {}
_default_name = ""
_lock = threading.Lock()


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
    model name. It exposes ``get_text_embedding`` /
    ``get_text_embedding_batch`` / ``get_query_embedding``.
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

    def get_text_embedding(self, text: str) -> list[float]:
        return self.get_text_embedding_batch([text])[0]

    def get_query_embedding(self, text: str) -> list[float]:
        # Write and query share the same embedding endpoint (SPEC §1.3).
        return self.get_text_embedding(text)

    def get_text_embedding_batch(self, texts: list[str]) -> list[list[float]]:
        out: list[list[float]] = []
        for i in range(0, len(texts), self._batch_size):
            chunk = texts[i : i + self._batch_size]
            resp = self._client.embeddings.create(model=self._model, input=chunk)
            ordered = sorted(resp.data, key=lambda d: d.index)
            out.extend([list(map(float, e)) for e in (d.embedding for d in ordered)])
        return out

    @property
    def model_name(self) -> str:
        return self._model


def _build_adapter(model_name: str) -> OpenAICompatibleEmbedding:
    """Construct one adapter for ``model_name`` (all connection params come
    from settings; only the model name varies)."""
    return OpenAICompatibleEmbedding(
        model=model_name,
        api_base=settings.embedding_api_base,
        api_key=settings.embedding_api_key,
        embed_batch_size=100,
    )


def get_embed_model(model_name: str = "") -> OpenAICompatibleEmbedding:
    """Return the embedding adapter for ``model_name``.

    Args:
        model_name: Per-KB embedding model name (from the KB row's
            ``embedding_model`` column). Empty falls back to the default
            registered by ``init_embedding_model`` (legacy single-model
            behavior).

    Adapters are cached per model name, so switching models per request
    costs nothing after the first call. ``init_embedding_model`` must
    have been called at startup (it pre-warms the default entry).
    """
    with _lock:
        if not _models:
            raise RuntimeError(
                "embedding model not initialised; call init_embedding_model() first"
            )
        name = model_name or _default_name or settings.embedding_model
        model = _models.get(name)
        if model is None:
            model = _build_adapter(name)
            _models[name] = model
    return model


async def init_embedding_model(model_name: str | None = None) -> None:
    """Initialise the default embedding adapter (and the registry).

    Called once at app startup (see ``main.py``). Connects to the AI
    inference service's OpenAI-compatible ``/v1/embeddings`` endpoint.
    """
    global _default_name
    name = model_name or settings.embedding_model
    with _lock:
        _models.clear()
        _models[name] = _build_adapter(name)
        _default_name = name
