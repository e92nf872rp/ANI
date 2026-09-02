"""Tests for embeddings.py LlamaIndex wrapper removal (RAG-REFACTOR-STEP-2 / Plan §2.3).

Validates that ``get_embed_model()`` returns the plain
``OpenAICompatibleEmbedding`` adapter directly (no LlamaIndex
``BaseEmbedding`` wrapper), so the new Embed RPC path does not depend on
LlamaIndex.

Stubs (including llama_index.core.embeddings.BaseEmbedding) are in conftest.py.
"""
from __future__ import annotations

import asyncio

import pytest
from app.core import embeddings
from app.core.embeddings import OpenAICompatibleEmbedding

# ── get_embed_model returns OpenAICompatibleEmbedding (no wrapper) ─────────


def test_get_embed_model_returns_openai_compatible_embedding():
    """STEP-2: get_embed_model() returns OpenAICompatibleEmbedding directly
    (no LlamaIndex BaseEmbedding wrapper)."""
    # Initialize the model so get_embed_model can return it.
    embeddings._model = None
    asyncio.run(embeddings.init_embedding_model("Qwen3-Embedding-0.6B"))
    model = embeddings.get_embed_model()
    assert isinstance(model, OpenAICompatibleEmbedding)
    assert model.model_name == "Qwen3-Embedding-0.6B"


def test_get_embed_model_no_wrapped_model_attribute():
    """STEP-2: the _wrapped_model global has been removed."""
    # _wrapped_model should not exist as a module attribute.
    assert not hasattr(embeddings, "_wrapped_model")


def test_get_embed_model_raises_before_init():
    """get_embed_model() raises RuntimeError when not initialised."""
    embeddings._model = None
    with pytest.raises(RuntimeError, match="not initialised"):
        embeddings.get_embed_model()


def test_init_embedding_model_does_not_set_wrapper():
    """init_embedding_model creates the adapter but does NOT build a
    LlamaIndex BaseEmbedding wrapper (STEP-2)."""
    embeddings._model = None
    asyncio.run(embeddings.init_embedding_model("test-model"))
    assert embeddings._model is not None
    assert isinstance(embeddings._model, OpenAICompatibleEmbedding)
    # get_embed_model returns the same object (no wrapping).
    assert embeddings.get_embed_model() is embeddings._model


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
