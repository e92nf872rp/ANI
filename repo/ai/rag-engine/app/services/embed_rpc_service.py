"""Embed RPC service (RAG-REFACTOR-STEP-2 / Plan §2.3, model switching M2).

Stateless embedding execution: calls ``OpenAICompatibleEmbedding`` directly
(no LlamaIndex dependency). The Embed RPC uses this service to compute
embedding vectors for a batch of texts.

``get_embed_model(model)`` returns the per-model
:class:`OpenAICompatibleEmbedding` adapter from the registry (no
``BaseEmbedding`` wrapper), so this service calls
``get_text_embedding_batch`` directly.
"""
from __future__ import annotations

from app.core.embeddings import get_embed_model


class EmbedRPCService:
    """Stateless embedding service for the Embed RPC (Plan §2.3).

    Calls the remote OpenAI-compatible ``/v1/embeddings`` endpoint via the
    per-model :class:`OpenAICompatibleEmbedding` adapter from the registry.
    Does NOT depend on LlamaIndex.
    """

    def embed(
        self, texts: list[str], model: str = ""
    ) -> tuple[list[list[float]], int]:
        """Embed a batch of texts.

        Args:
            texts: List of text strings to embed.
            model: Per-KB embedding model name (``EmbedRequest.model``);
                empty falls back to the server default.

        Returns:
            ``(vectors, dimension)`` where ``vectors`` is a list of float
            lists (one per input text, in order) and ``dimension`` is the
            embedding dimension (0 when ``texts`` is empty).
        """
        if not texts:
            return [], 0
        emb_model = get_embed_model(model)
        vectors = emb_model.get_text_embedding_batch(texts)
        dim = len(vectors[0]) if vectors else 0
        return vectors, dim
