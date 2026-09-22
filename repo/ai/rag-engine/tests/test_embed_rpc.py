"""Tests for Embed RPC (RAG-REFACTOR-STEP-2 / Plan Â§2.3).

Validates: texts â†?vectors_flat + dimension + count. Does NOT require live
vLLM/Milvus â€?embedding model is mocked. Stubs in conftest.py.
"""
from __future__ import annotations

from unittest.mock import MagicMock

import pytest
from app.grpc import rag_pb2 as rag_pb
from app.grpc.server import RagEngineServicer
from app.services.embed_rpc_service import EmbedRPCService


class FakeContext:
    def __init__(self) -> None:
        self.aborted_code = None
        self.aborted_details = None

    async def abort(self, code, details):
        self.aborted_code = code
        self.aborted_details = details
        raise Exception(f"aborted: {code} {details}")  # noqa: TRY002


# â”€â”€ EmbedRPCService â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_embed_rpc_service_empty():
    svc = EmbedRPCService()
    vectors, dim = svc.embed([])
    assert vectors == []
    assert dim == 0


def test_embed_rpc_service_with_texts(monkeypatch):
    """EmbedRPCService.embed calls get_embed_model().get_text_embedding_batch."""
    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [
        [0.1, 0.2, 0.3],
        [0.4, 0.5, 0.6],
    ]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    svc = EmbedRPCService()
    vectors, dim = svc.embed(["hello", "world"])
    assert len(vectors) == 2
    assert dim == 3
    assert vectors[0] == [0.1, 0.2, 0.3]
    assert vectors[1] == [0.4, 0.5, 0.6]
    fake_model.get_text_embedding_batch.assert_called_once_with(["hello", "world"])


# â”€â”€ Embed RPC via gRPC servicer â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


@pytest.mark.asyncio
async def test_embed_rpc_empty_texts():
    """Empty texts â†?empty response with dimension=0, count=0."""
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.EmbedRequest(texts=[])
    resp = await servicer.Embed(req, ctx)
    assert ctx.aborted_code is None
    assert list(resp.vectors_flat) == []
    assert resp.dimension == 0
    assert resp.count == 0


@pytest.mark.asyncio
async def test_embed_rpc_returns_flat_vectors(monkeypatch):
    """texts â†?vectors_flat + dimension + count."""
    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [
        [1.0, 2.0, 3.0],
        [4.0, 5.0, 6.0],
    ]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.EmbedRequest(texts=["a", "b"])
    resp = await servicer.Embed(req, ctx)
    assert ctx.aborted_code is None
    # Flattened: [1,2,3,4,5,6]
    assert list(resp.vectors_flat) == [1.0, 2.0, 3.0, 4.0, 5.0, 6.0]
    assert resp.dimension == 3
    assert resp.count == 2


@pytest.mark.asyncio
async def test_embed_rpc_single_text(monkeypatch):
    """Single text â†?one vector, correct dimension."""
    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [[0.5, 0.6]]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.EmbedRequest(texts=["single"])
    resp = await servicer.Embed(req, ctx)
    assert list(resp.vectors_flat) == pytest.approx([0.5, 0.6])
    assert resp.dimension == 2
    assert resp.count == 1


@pytest.mark.asyncio
async def test_embed_rpc_error_handling(monkeypatch):
    """Embed RPC error → INTERNAL."""
    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.side_effect = RuntimeError("connection failed")
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.EmbedRequest(texts=["test"])
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Embed(req, ctx)
    assert ctx.aborted_code is not None


# ── M2: per-model Embed RPC (EmbedRequest.model) ────────────────────────────


def test_embed_rpc_service_routes_model_param(monkeypatch):
    """EmbedRPCService.embed passes model through to get_embed_model."""
    captured = {}

    def _fake_get(model_name=""):
        captured["model_name"] = model_name
        return fake_model

    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [[0.1, 0.2]]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", _fake_get
    )
    svc = EmbedRPCService()
    svc.embed(["text"], "bge-m3")
    assert captured["model_name"] == "bge-m3"


@pytest.mark.asyncio
async def test_embed_rpc_model_field_routed(monkeypatch):
    """gRPC Embed RPC: EmbedRequest.model → get_embed_model(model)."""
    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [[1.0, 2.0]]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.EmbedRequest(texts=["a"], model="bge-m3")
    resp = await servicer.Embed(req, ctx)
    assert ctx.aborted_code is None
    assert resp.count == 1
    assert resp.dimension == 2


@pytest.mark.asyncio
async def test_embed_rpc_empty_model_uses_default(monkeypatch):
    """gRPC Embed RPC: empty model → empty string passed (server default)."""
    captured = {}

    def _fake_get(model_name=""):
        captured["model_name"] = model_name
        return fake_model

    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [[1.0, 2.0]]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", _fake_get
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.EmbedRequest(texts=["a"], model="")
    resp = await servicer.Embed(req, ctx)
    assert ctx.aborted_code is None
    assert captured["model_name"] == ""
    assert resp.count == 1


# ── Bug B fix, layer 2: Embed RPC clamps texts to EMBED_SAFE_CHARS ──────────


@pytest.mark.asyncio
async def test_embed_rpc_truncates_oversized_text(monkeypatch):
    """Every text is clamped to EMBED_SAFE_CHARS before embedding."""
    from app.core.config import EMBED_SAFE_CHARS

    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [
        [1.0, 2.0],
        [3.0, 4.0],
    ]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    long_text = "x" * (EMBED_SAFE_CHARS + 100)
    req = rag_pb.EmbedRequest(texts=[long_text, "short"])
    resp = await servicer.Embed(req, ctx)
    assert ctx.aborted_code is None
    assert resp.count == 2
    sent = fake_model.get_text_embedding_batch.call_args[0][0]
    assert len(sent[0]) == EMBED_SAFE_CHARS
    assert sent[1] == "short"


@pytest.mark.asyncio
async def test_embed_rpc_short_text_untouched(monkeypatch):
    """Texts within the cap pass through unmodified."""
    from app.core.config import EMBED_SAFE_CHARS

    fake_model = MagicMock()
    fake_model.get_text_embedding_batch.return_value = [[1.0, 2.0]]
    monkeypatch.setattr(
        "app.services.embed_rpc_service.get_embed_model", lambda model="": fake_model
    )
    servicer = RagEngineServicer()
    ctx = FakeContext()
    text = "a" * EMBED_SAFE_CHARS  # exactly at the cap — not truncated
    req = rag_pb.EmbedRequest(texts=[text])
    await servicer.Embed(req, ctx)
    assert ctx.aborted_code is None
    sent = fake_model.get_text_embedding_batch.call_args[0][0]
    assert sent[0] == text


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
