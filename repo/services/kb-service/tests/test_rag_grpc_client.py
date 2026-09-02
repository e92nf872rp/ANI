"""Tests for the rag-engine gRPC client (issue-030 / Plan §3.4).

Verifies:
- RagEngineGRPCClient.embed() deserializes the flattened EmbedResponse:
    vectors[i] = list(vectors_flat[i*dim:(i+1)*dim])
- parse() / generate() / generate_stream() map proto responses to dicts.
- _build_source_chunks / _build_chat_messages convert dicts to proto.

Uses a FakeStub that returns pre-built proto responses, so no real gRPC
server is required.
"""
import os
import sys

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.rag_engine import rag_pb2
from app.rag_engine.client import (
    RagEngineGRPCClient,
    _build_chat_messages,
    _build_source_chunks,
)


# ── Fake gRPC stub ───────────────────────────────────────────────────────────


class _FakeStream:
    """Async iterator over pre-built GenerateToken messages."""

    def __init__(self, tokens):
        self._tokens = tokens
        self._i = 0

    def __aiter__(self):
        return self

    async def __anext__(self):
        if self._i >= len(self._tokens):
            raise StopAsyncIteration
        tok = self._tokens[self._i]
        self._i += 1
        return tok


class FakeStub:
    """Fake RagEngineStub returning canned proto responses."""

    def __init__(self):
        self.parse_resp = None
        self.embed_resp = None
        self.generate_resp = None
        self.generate_stream_resp = None
        self.last_parse_req = None
        self.last_embed_req = None
        self.last_generate_req = None

    async def Parse(self, request, **kwargs):
        self.last_parse_req = request
        return self.parse_resp

    async def Embed(self, request, **kwargs):
        self.last_embed_req = request
        return self.embed_resp

    async def Generate(self, request, **kwargs):
        self.last_generate_req = request
        return self.generate_resp

    def GenerateStream(self, request, **kwargs):
        self.last_generate_req = request
        return _FakeStream(self.generate_stream_resp)


def _make_client(stub):
    """Build a RagEngineGRPCClient with an injected fake stub."""
    # Bypass the real channel by constructing the client then swapping the stub.
    client = RagEngineGRPCClient.__new__(RagEngineGRPCClient)
    client._addr = "fake"
    client._channel = None
    client._stub = stub
    client._timeout = 120.0
    return client


# ── embed: flattened array deserialization ───────────────────────────────────


@pytest.mark.asyncio
async def test_embed_deserializes_flattened_vectors():
    """vectors[i] = vectors_flat[i*dim:(i+1)*dim]."""
    stub = FakeStub()
    # 3 texts, dim=4 → 12 floats flattened.
    flat = [0.1, 0.2, 0.3, 0.4,  0.5, 0.6, 0.7, 0.8,  0.9, 1.0, 1.1, 1.2]
    stub.embed_resp = rag_pb2.EmbedResponse(
        vectors_flat=flat, dimension=4, count=3
    )
    client = _make_client(stub)

    vectors, dim = await client.embed(texts=["a", "b", "c"])

    assert dim == 4
    assert len(vectors) == 3
    # Float32 proto round-trip loses precision; compare with approx.
    assert vectors[0] == pytest.approx([0.1, 0.2, 0.3, 0.4])
    assert vectors[1] == pytest.approx([0.5, 0.6, 0.7, 0.8])
    assert vectors[2] == pytest.approx([0.9, 1.0, 1.1, 1.2])
    # Stub received the texts.
    assert list(stub.last_embed_req.texts) == ["a", "b", "c"]


@pytest.mark.asyncio
async def test_embed_single_text():
    stub = FakeStub()
    stub.embed_resp = rag_pb2.EmbedResponse(
        vectors_flat=[1.0, 2.0, 3.0], dimension=3, count=1
    )
    client = _make_client(stub)

    vectors, dim = await client.embed(texts=["only"])

    assert dim == 3
    assert vectors == [[1.0, 2.0, 3.0]]


@pytest.mark.asyncio
async def test_embed_empty_when_count_zero():
    stub = FakeStub()
    stub.embed_resp = rag_pb2.EmbedResponse(
        vectors_flat=[], dimension=0, count=0
    )
    client = _make_client(stub)

    vectors, dim = await client.embed(texts=[])

    assert vectors == []
    assert dim == 0


@pytest.mark.asyncio
async def test_embed_raises_on_truncated_flat_array():
    """A flat array shorter than count*dim indicates a malformed response."""
    from app.rag_engine.client import RagEngineError

    stub = FakeStub()
    # count=3, dim=4 → expect 12 floats, but only provide 8 (truncated).
    stub.embed_resp = rag_pb2.EmbedResponse(
        vectors_flat=[0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8], dimension=4, count=3
    )
    client = _make_client(stub)

    with pytest.raises(RagEngineError, match="vectors_flat length"):
        await client.embed(texts=["a", "b", "c"])


# ── parse ────────────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_parse_maps_chunks_to_dicts():
    stub = FakeStub()
    stub.parse_resp = rag_pb2.ParseResponse(chunks=[
        rag_pb2.ParsedChunk(
            chunk_id="c1", content="hello", content_type="text",
            page_number=1, parent_content="parent", chunk_type="child",
            metadata_json='{"k":"v"}', parent_chunk_id="p1",
        ),
        rag_pb2.ParsedChunk(
            chunk_id="img1", content="[img]", content_type="image",
            chunk_type="image", image_bytes=b"\x89PNG", image_format="png",
        ),
    ])
    client = _make_client(stub)

    chunks = await client.parse(
        download_url="http://dl", file_name="a.pdf", file_type="pdf", chunk_size=512
    )

    assert len(chunks) == 2
    assert chunks[0]["chunk_id"] == "c1"
    assert chunks[0]["content"] == "hello"
    assert chunks[0]["parent_chunk_id"] == "p1"
    assert chunks[1]["image_bytes"] == b"\x89PNG"
    assert chunks[1]["image_format"] == "png"
    # Request fields propagated.
    assert stub.last_parse_req.download_url == "http://dl"
    assert stub.last_parse_req.file_name == "a.pdf"
    assert stub.last_parse_req.file_type == "pdf"
    assert stub.last_parse_req.chunk_size == 512


# ── generate ────────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_generate_returns_answer_and_tokens():
    stub = FakeStub()
    stub.generate_resp = rag_pb2.GenerateResponse(
        answer="hi", input_tokens=10, output_tokens=5, session_id="sess-1"
    )
    client = _make_client(stub)

    result = await client.generate(
        question="q", session_id="sess-1",
        context=[{"chunk_id": "c1", "doc_id": "d1", "content": "ctx", "score": 0.9}],
        history=[{"role": "user", "content": "q"}],
        max_tokens=2048,
    )

    assert result["answer"] == "hi"
    assert result["input_tokens"] == 10
    assert result["output_tokens"] == 5
    assert result["session_id"] == "sess-1"
    # Request built correctly.
    req = stub.last_generate_req
    assert req.question == "q"
    assert req.session_id == "sess-1"
    assert req.max_tokens == 2048
    assert len(req.context) == 1
    assert req.context[0].chunk_id == "c1"
    # Float32 proto round-trip loses precision; compare with approx.
    assert req.context[0].score == pytest.approx(0.9)
    assert len(req.history) == 1
    assert req.history[0].role == "user"


@pytest.mark.asyncio
async def test_generate_defaults_none_context_history():
    stub = FakeStub()
    stub.generate_resp = rag_pb2.GenerateResponse(answer="ok")
    client = _make_client(stub)

    await client.generate(question="q")

    req = stub.last_generate_req
    assert list(req.context) == []
    assert list(req.history) == []


# ── generate_stream ─────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_generate_stream_yields_tokens_then_done():
    stub = FakeStub()
    stub.generate_stream_resp = [
        rag_pb2.GenerateToken(content="hel", done=False),
        rag_pb2.GenerateToken(content="lo", done=False),
        rag_pb2.GenerateToken(content="", done=True, input_tokens=8, output_tokens=2),
    ]
    client = _make_client(stub)

    out = []
    async for tok in client.generate_stream(question="q"):
        out.append(tok)

    assert len(out) == 3
    assert out[0]["content"] == "hel"
    assert out[0]["done"] is False
    assert out[2]["done"] is True
    assert out[2]["input_tokens"] == 8
    assert out[2]["output_tokens"] == 2


# ── helpers ─────────────────────────────────────────────────────────────────


def test_build_source_chunks_maps_fields():
    chunks = _build_source_chunks([
        {"chunk_id": "c1", "doc_id": "d1", "file_name": "a.pdf",
         "page": 3, "content": "text", "score": 0.5},
    ])
    assert len(chunks) == 1
    assert chunks[0].chunk_id == "c1"
    assert chunks[0].doc_id == "d1"
    assert chunks[0].page == 3
    assert chunks[0].score == 0.5


def test_build_source_chunks_defaults_missing_fields():
    chunks = _build_source_chunks([{}])
    assert chunks[0].chunk_id == ""
    assert chunks[0].page == 0
    assert chunks[0].score == 0.0


def test_build_chat_messages():
    msgs = _build_chat_messages([
        {"role": "user", "content": "hi"},
        {"role": "assistant", "content": "hello"},
    ])
    assert len(msgs) == 2
    assert msgs[0].role == "user"
    assert msgs[1].content == "hello"


def test_build_chat_messages_empty():
    assert _build_chat_messages([]) == []
