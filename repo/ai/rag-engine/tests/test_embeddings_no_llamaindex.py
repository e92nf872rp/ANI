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
    asyncio.run(embeddings.init_embedding_model("BAAI/bge-m3"))
    model = embeddings.get_embed_model()
    assert isinstance(model, OpenAICompatibleEmbedding)
    assert model.model_name == "BAAI/bge-m3"


def test_get_embed_model_no_wrapped_model_attribute():
    """STEP-2: the _wrapped_model global has been removed."""
    # _wrapped_model should not exist as a module attribute.
    assert not hasattr(embeddings, "_wrapped_model")
    # The legacy ``_model`` module alias has been removed too (M2 registry
    # replaced the single-model global).
    assert not hasattr(embeddings, "_model")


def test_get_embed_model_raises_before_init():
    """get_embed_model() raises RuntimeError when not initialised."""
    embeddings._models.clear()
    with pytest.raises(RuntimeError, match="not initialised"):
        embeddings.get_embed_model()


def test_init_embedding_model_registers_default():
    """init_embedding_model registers the default adapter in the registry
    (no LlamaIndex BaseEmbedding wrapper, STEP-2)."""
    asyncio.run(embeddings.init_embedding_model("test-model"))
    default = embeddings.get_embed_model()
    assert isinstance(default, OpenAICompatibleEmbedding)
    assert default.model_name == "test-model"
    # get_embed_model("") resolves to the same registered default.
    assert embeddings.get_embed_model("") is default


# ── M2: per-model registry ─────────────────────────────────────────────────


def test_get_embed_model_per_model_name_caches_adapters():
    """get_embed_model(name) returns one cached adapter per model name."""
    embeddings._models.clear()
    asyncio.run(embeddings.init_embedding_model("default-model"))
    a1 = embeddings.get_embed_model("model-a")
    a2 = embeddings.get_embed_model("model-a")
    b = embeddings.get_embed_model("model-b")
    assert a1 is a2  # cached
    assert a1 is not b  # distinct models → distinct adapters
    assert a1.model_name == "model-a"
    assert b.model_name == "model-b"
    # Default entry preserved after registering other models.
    assert embeddings.get_embed_model().model_name == "default-model"


def test_get_embed_model_empty_falls_back_to_default():
    """Empty model_name resolves to the default registered at init."""
    embeddings._models.clear()
    asyncio.run(embeddings.init_embedding_model("qwen-emb"))
    m = embeddings.get_embed_model("")
    assert m.model_name == "qwen-emb"
    assert m is embeddings.get_embed_model()


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
