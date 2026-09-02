"""Embedding model singleton (US-013 / SPEC §5.1, §1.3).

The embedding model is served by the AI inference service via an OpenAI
compatible ``/v1/embeddings`` endpoint. rag-engine calls the remote service
through a lightweight adapter backed by the ``openai`` Python SDK, so
write-side and query-side embeddings share the same model endpoint
(SPEC §1.3 "嵌入统一"). The model is constructed once at startup and
re-used by the Embed RPC.

We implement a custom :class:`OpenAICompatibleEmbedding` rather than using
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

from app.core.config import settings

_model = None  # type: ignore[var-annotated]


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


async def init_embedding_model(model_name: str | None = None) -> None:
    """Initialise the remote embedding singleton.

    Called once at app startup (see ``main.py``). Connects to the AI
    inference service's OpenAI-compatible ``/v1/embeddings`` endpoint.
    """
    global _model
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


def get_embed_model() -> OpenAICompatibleEmbedding:
    """Return the initialised embedding singleton.

    Raises ``RuntimeError`` if ``init_embedding_model`` has not been called.
    """
    if _model is None:
        raise RuntimeError(
            "embedding model not initialised; call init_embedding_model() first"
        )
    return _model


def embed(texts: list[str]) -> list[list[float]]:
    """Embed a batch of texts using the unified remote embedding model.

    Returns a list of float vectors.
    """
    model = get_embed_model()
    return model.get_text_embedding_batch(texts)
