"""Embedding model singleton."""
from __future__ import annotations

from sentence_transformers import SentenceTransformer

from app.core.config import settings

_model: SentenceTransformer | None = None


async def init_embedding_model(model_name: str | None = None) -> None:
    global _model
    name = model_name or settings.embedding_model
    # SentenceTransformer respects HF_ENDPOINT env var for mirror downloads
    _model = SentenceTransformer(name, trust_remote_code=True)


def get_model() -> SentenceTransformer:
    if _model is None:
        raise RuntimeError("embedding model not initialised; call init_embedding_model() first")
    return _model


def embed(texts: list[str]) -> list[list[float]]:
    """Embed a batch of texts. Returns list of float vectors."""
    return get_model().encode(texts, normalize_embeddings=True).tolist()
