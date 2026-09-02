"""Tests for Parse RPC (RAG-REFACTOR-STEP-2 / Plan Â§2.2).

Validates: download_url â†?chunks + image bytes. Does NOT require live
httpx/Milvus/PG/vLLM â€?all deps are stubbed in conftest.py.
"""
from __future__ import annotations

from pathlib import Path

import pytest
from app.grpc import rag_pb2 as rag_pb
from app.grpc.server import RagEngineServicer, _build_parse_chunks, _extract_image_bytes
from app.services.chunk_service import ChildChunk, ParentChunk


class FakeContext:
    def __init__(self) -> None:
        self.aborted_code = None
        self.aborted_details = None

    async def abort(self, code, details):
        self.aborted_code = code
        self.aborted_details = details
        raise Exception(f"aborted: {code} {details}")  # noqa: TRY002


# â”€â”€ _build_parse_chunks â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_build_parse_chunks_parents():
    parents = [ParentChunk(chunk_id="p1", content="parent text", content_type="text", token_count=10, page_number=1)]
    chunks = _build_parse_chunks(parents, [], [])
    assert len(chunks) == 1
    assert chunks[0].chunk_id == "p1"
    assert chunks[0].content == "parent text"
    assert chunks[0].chunk_type == "parent"
    assert chunks[0].parent_content == ""
    assert chunks[0].parent_chunk_id == ""


def test_build_parse_chunks_children_carry_parent_info():
    parents = [ParentChunk(chunk_id="p1", content="parent text", content_type="text", token_count=10, page_number=1)]
    children = [ChildChunk(
        chunk_id="c1", content="child text", content_type="text",
        page_number=1, token_count=5, parent_chunk_id="p1", parent_content="parent text",
    )]
    chunks = _build_parse_chunks(parents, children, [])
    # 1 parent + 1 child
    assert len(chunks) == 2
    child = next(c for c in chunks if c.chunk_type == "child")
    assert child.chunk_id == "c1"
    assert child.parent_content == "parent text"
    assert child.parent_chunk_id == "p1"


def test_build_parse_chunks_images():
    image_chunks = [{"image_bytes": b"\x89PNG", "image_format": "png", "placeholder": "[å›¾ç‰‡](placeholder)"}]
    chunks = _build_parse_chunks([], [], image_chunks)
    assert len(chunks) == 1
    assert chunks[0].chunk_type == "image"
    assert chunks[0].content == "[å›¾ç‰‡](placeholder)"
    assert chunks[0].image_bytes == b"\x89PNG"
    assert chunks[0].image_format == "png"
    assert chunks[0].chunk_id  # uuid generated


def test_build_parse_chunks_empty():
    chunks = _build_parse_chunks([], [], [])
    assert chunks == []


def test_build_parse_chunks_mixed():
    parents = [ParentChunk(chunk_id="p1", content="p", content_type="text", token_count=1, page_number=1)]
    children = [ChildChunk(chunk_id="c1", content="c", content_type="text", page_number=1, token_count=1,
                           parent_chunk_id="p1", parent_content="p")]
    images = [{"image_bytes": b"x", "image_format": "png", "placeholder": "[å›¾ç‰‡](placeholder)"}]
    chunks = _build_parse_chunks(parents, children, images)
    assert len(chunks) == 3
    types = {c.chunk_type for c in chunks}
    assert types == {"parent", "child", "image"}


# â”€â”€ _extract_image_bytes â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_extract_image_bytes_empty(tmp_path: Path):
    """Non-PDF/Office file â†?empty list."""
    f = tmp_path / "test.txt"
    f.write_text("hello")
    images = _extract_image_bytes(str(f), "txt")
    assert images == []


def test_extract_image_bytes_unknown_format(tmp_path: Path):
    f = tmp_path / "test.unknown"
    f.write_text("hello")
    images = _extract_image_bytes(str(f), "unknown")
    assert images == []


# â”€â”€ Parse RPC â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


@pytest.mark.asyncio
async def test_parse_rpc_missing_download_url():
    """Missing download_url â†?INVALID_ARGUMENT."""
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.ParseRequest(download_url="", file_name="test.txt")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Parse(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_parse_rpc_missing_file_name():
    """Missing file_name â†?INVALID_ARGUMENT."""
    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.ParseRequest(download_url="http://example.com/f.txt", file_name="")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Parse(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_parse_rpc_success(tmp_path: Path, monkeypatch):
    """Parse RPC: download_url â†?chunks."""
    # Create a temp file that will be "downloaded".
    doc_path = tmp_path / "test.txt"
    doc_path.write_text("hello world\n\nsecond paragraph")

    # Stub httpx.AsyncClient to write the local file content.
    class _FakeResponse:
        def raise_for_status(self):
            pass

        async def aiter_bytes(self):
            yield doc_path.read_bytes()

    class _FakeStream:
        async def __aenter__(self):
            return _FakeResponse()

        async def __aexit__(self, *exc):
            pass

    class _FakeClient:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *exc):
            pass

        def stream(self, method, url):
            return _FakeStream()

    import httpx

    monkeypatch.setattr(httpx, "AsyncClient", lambda **kw: _FakeClient())

    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.ParseRequest(
        download_url="http://example.com/test.txt",
        file_name="test.txt",
        file_type="txt",
        chunk_size=1024,
    )
    resp = await servicer.Parse(req, ctx)
    assert ctx.aborted_code is None
    # Should have parent + child chunks (text file parsed).
    assert len(resp.chunks) > 0
    # At least one child chunk with content.
    child_chunks = [c for c in resp.chunks if c.chunk_type == "child"]
    assert len(child_chunks) > 0
    assert any("hello" in c.content for c in child_chunks)


@pytest.mark.asyncio
async def test_parse_rpc_download_failure(tmp_path: Path, monkeypatch):
    """Download failure â†?INTERNAL error."""

    class _FakeResponse:
        def raise_for_status(self):
            raise RuntimeError("404 not found")

        async def aiter_bytes(self):
            yield b""

    class _FakeStream:
        async def __aenter__(self):
            return _FakeResponse()

        async def __aexit__(self, *exc):
            pass

    class _FakeClient:
        async def __aenter__(self):
            return self

        async def __aexit__(self, *exc):
            pass

        def stream(self, method, url):
            return _FakeStream()

    import httpx

    monkeypatch.setattr(httpx, "AsyncClient", lambda **kw: _FakeClient())

    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.ParseRequest(
        download_url="http://example.com/bad.txt",
        file_name="bad.txt",
        file_type="txt",
    )
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Parse(req, ctx)
    assert ctx.aborted_code is not None


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
