"""Tests for the kb-service ParseOrchestrator (issue-032 / Plan step 5).

Covers all Acceptance Criteria:
- Full pipeline: pending → parsing → rag-engine.Parse → image upload →
  indexing → summary → Embed → Core vector insert → kb_chunks write → ready
- download_url from Core API passed to rag-engine.Parse (kb-service does not
  download document bytes)
- Image upload to Core API, markdown link [图片: 图片](object_id) embedded
  in parent content
- Embed RPC embeds child chunks + summary separately (summary NOT in
  child_chunks to avoid double-write)
- Core API insert with pre-computed vectors; metadata includes chunk_id,
  chunk_type, parent_content, parent_chunk_id, page_number, content_type,
  doc_id, file_name
- write_chunks receives parents + children + summaries separately
- State machine: pending → parsing → indexing → ready | failed
- _generate_summary: best-effort, first 3 parent blocks, Generate RPC with
  the same English prompt template as legacy SummaryService (instructs LLM
  to match content language); failure → None, no block
- _sanitize_error: redacts paths/tokens, truncates 500 chars
- Equivalence test: same input → same kb_chunks row count and content

Uses fakes for CoreClient, RagEngine client, asyncpg pool — no real services.
chunk_repo.write_chunks and doc_repo.update_parse_status are mocked.
"""
import json
import os
import sys
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock, patch

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.services import parse_orchestrator as po_module
from app.services.contracts import ParseOrchestratorProtocol
from app.services.parse_orchestrator import (
    ParseOrchestrator,
    SUMMARY_PARENT_COUNT,
    _sanitize_error,
)


TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"
OBJECT_ID = "44444444-4444-4444-4444-444444444444"
VECTOR_STORE_ID = "vs_kb_2222222222222222222222222"
PARENT_ID = "55555555-5555-5555-5555-555555555551"
CHILD_A = "66666666-6666-6666-6666-666666666661"
CHILD_B = "66666666-6666-6666-6666-666666666662"
IMAGE_ID = "77777777-7777-7777-7777-777777777771"
BUCKET_ID = "88888888-8888-8888-8888-888888888881"
DOWNLOAD_URL = "https://core.local/objects/444/download?token=secret"
IMAGE_OBJ_ID = "99999999-9999-9999-9999-999999999991"


# ── Fakes ────────────────────────────────────────────────────────────────────


class _FakeRagEngine:
    """Fake RagEngineGRPCClient: parse / embed / generate."""

    def __init__(
        self,
        *,
        chunks=None,
        vectors=None,
        dimension=4,
        summary_answer="这是一个约两百字以上的摘要。" * 10,
    ):
        self._chunks = chunks or []
        self._vectors = vectors or [[0.1, 0.2, 0.3, 0.4]]
        self._dimension = dimension
        self._summary_answer = summary_answer
        self.parse_calls: list[dict] = []
        self.embed_calls: list[list[str]] = []
        self.generate_calls: list[dict] = []

    async def parse(
        self,
        *,
        download_url: str,
        file_name: str,
        file_type: str,
        chunk_size: int = 1024,
    ) -> list[dict]:
        self.parse_calls.append({
            "download_url": download_url,
            "file_name": file_name,
            "file_type": file_type,
            "chunk_size": chunk_size,
        })
        return [dict(c) for c in self._chunks]

    async def embed(self, *, texts: list[str]) -> tuple[list[list[float]], int]:
        self.embed_calls.append(list(texts))
        # Return one vector per text (cycle if fewer pre-built vectors).
        out = []
        for i in range(len(texts)):
            out.append(list(self._vectors[i % len(self._vectors)]))
        return out, self._dimension

    async def generate(
        self,
        *,
        question: str,
        session_id: str = "",
        context=None,
        history=None,
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> dict:
        self.generate_calls.append({
            "question": question,
            "session_id": session_id,
            "context": context or [],
            "history": history or [],
            "inference_service_name": inference_service_name,
            "max_tokens": max_tokens,
        })
        return {
            "answer": self._summary_answer,
            "input_tokens": 10,
            "output_tokens": 20,
            "session_id": session_id,
        }


class _FakeCoreClient:
    """Fake CoreClient for parse orchestrator."""

    def __init__(
        self,
        *,
        download_url=DOWNLOAD_URL,
        bucket_id=BUCKET_ID,
        image_object_id=IMAGE_OBJ_ID,
    ):
        self._download_url = download_url
        self._bucket_id = bucket_id
        self._image_object_id = image_object_id
        self.download_calls: list[str] = []
        self.bucket_calls: list[str] = []
        self.upload_calls: list[dict] = []
        self.insert_calls: list[dict] = []
        self.delete_vector_calls: list[dict] = []

    async def request_download_url(self, *, object_id, expires_seconds=3600):
        self.download_calls.append(object_id)
        return {"download_url": self._download_url}

    async def get_bucket_id_by_name(self, *, name):
        self.bucket_calls.append(name)
        return self._bucket_id

    async def upload_object(
        self,
        *,
        bucket_id,
        key,
        content_bytes,
        content_type=None,
        idempotency_key,
    ):
        self.upload_calls.append({
            "bucket_id": bucket_id,
            "key": key,
            "content_type": content_type,
            "idempotency_key": idempotency_key,
            "size": len(content_bytes),
        })
        return self._image_object_id

    async def insert_vector_documents(self, *, vector_store_id, documents, idempotency_key):
        self.insert_calls.append({
            "vector_store_id": vector_store_id,
            "documents": documents,
            "idempotency_key": idempotency_key,
        })
        return {"accepted": True}

    async def delete_vector_store_documents(self, *, vector_store_id, filter_expr):
        self.delete_vector_calls.append({
            "vector_store_id": vector_store_id,
            "filter_expr": filter_expr,
        })
        return {"deleted": True}

    async def aclose(self):
        pass


class _FakeConn:
    """Minimal fake asyncpg Connection for doc_repo + chunk_repo calls."""

    def __init__(self):
        self.executed: list[tuple] = []

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self
        return _tx()


class _FakePool:
    """Fake asyncpg Pool yielding a shared _FakeConn."""

    def __init__(self, conn=None):
        self._conn = conn or _FakeConn()

    @asynccontextmanager
    async def acquire(self):
        yield self._conn


# ── Builders ────────────────────────────────────────────────────────────────


def _parent(chunk_id=PARENT_ID, content="parent block text", page=1):
    return {
        "chunk_id": chunk_id,
        "content": content,
        "content_type": "text",
        "page_number": page,
        "parent_content": "",
        "parent_chunk_id": "",
        "chunk_type": "parent",
        "metadata_json": "{}",
        "image_bytes": b"",
        "image_format": "",
    }


def _child(chunk_id=CHILD_A, content="child chunk text", page=1,
           parent_content="parent block text", parent_chunk_id=PARENT_ID):
    return {
        "chunk_id": chunk_id,
        "content": content,
        "content_type": "text",
        "page_number": page,
        "parent_content": parent_content,
        "parent_chunk_id": parent_chunk_id,
        "chunk_type": "child",
        "metadata_json": "{}",
        "image_bytes": b"",
        "image_format": "",
    }


def _image_chunk(chunk_id=IMAGE_ID, content="[图片](placeholder)",
                 image_bytes=b"\x89PNG fake", image_format="png"):
    return {
        "chunk_id": chunk_id,
        "content": content,
        "content_type": "image",
        "page_number": 0,
        "parent_content": "",
        "parent_chunk_id": "",
        "chunk_type": "image",
        "metadata_json": "{}",
        "image_bytes": image_bytes,
        "image_format": image_format,
    }


def _make_service(*, rag_engine=None, core_client=None, pool=None):
    """Build a ParseOrchestrator with fakes and mocked repos."""
    rag = rag_engine or _FakeRagEngine()
    core = core_client or _FakeCoreClient()
    p = pool or _FakePool()
    factory = lambda tenant_id: core
    svc = ParseOrchestrator(
        db_pool=p,
        core_client_factory=factory,
        rag_engine_client=rag,
    )
    return svc, rag, core, p


def _patch_repos(*, existing_doc=None):
    """Patch doc_repo.update_parse_status, doc_repo.get_document,
    chunk_repo.write_chunks, and chunk_repo.delete_chunks_by_doc.

    Returns (patch_status, patch_write, patch_get_doc, patch_delete,
    mock_status, mock_write, mock_get_doc, mock_delete) for use in
    ``with`` blocks.
    """
    mock_status = AsyncMock(return_value=True)
    mock_write = AsyncMock(return_value=3)
    mock_get_doc = AsyncMock(return_value=existing_doc)
    mock_delete = AsyncMock(return_value=0)
    return (
        patch("app.services.parse_orchestrator.doc_repo.update_parse_status", mock_status),
        patch("app.services.parse_orchestrator.chunk_repo.write_chunks", mock_write),
        patch("app.services.parse_orchestrator.doc_repo.get_document", mock_get_doc),
        patch("app.services.parse_orchestrator.chunk_repo.delete_chunks_by_doc", mock_delete),
        mock_status,
        mock_write,
        mock_get_doc,
        mock_delete,
    )


# ── Tests ───────────────────────────────────────────────────────────────────


# AC: ParseOrchestrator satisfies ParseOrchestratorProtocol
def test_parse_orchestrator_satisfies_protocol():
    svc, _, _, _ = _make_service()
    assert isinstance(svc, ParseOrchestratorProtocol)


# AC: pending → parsing → indexing → ready
@pytest.mark.asyncio
async def test_full_pipeline_success_state_transitions():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID,
            kb_id=KB_ID,
            doc_id=DOC_ID,
            object_id=OBJECT_ID,
            file_name="a.pdf",
            file_type="pdf",
            chunk_size=1024,
            vector_store_id=VECTOR_STORE_ID,
        )

    # State transitions: parsing, indexing, ready (in order)
    status_calls = [c.kwargs["parse_status"] for c in mock_status.call_args_list]
    assert status_calls == ["parsing", "indexing", "ready"]
    # ready call includes chunk_count = write_chunks return value (total rows:
    # parents + children + summaries, matching legacy parse_worker line 499)
    ready_call = mock_status.call_args_list[-1]
    assert ready_call.kwargs["chunk_count"] == 3  # mock_write returns 3


# AC: download_url from Core API passed to rag-engine.Parse
@pytest.mark.asyncio
async def test_download_url_passed_to_parse_rpc():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, _, _, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    assert core.download_calls == [OBJECT_ID]
    assert rag.parse_calls[0]["download_url"] == DOWNLOAD_URL
    assert rag.parse_calls[0]["file_name"] == "a.pdf"
    assert rag.parse_calls[0]["file_type"] == "pdf"
    assert rag.parse_calls[0]["chunk_size"] == 1024


# AC: Image upload to Core API, markdown link embedded in parent content
@pytest.mark.asyncio
async def test_image_upload_embeds_link_in_parent():
    img = _image_chunk()
    chunks = [_parent(), _child(), img]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # Image uploaded to Core API
    assert len(core.upload_calls) == 1
    up = core.upload_calls[0]
    assert up["bucket_id"] == BUCKET_ID
    # Key follows legacy ImageUploader format: {kb_id}/{doc_id}/images/{uuid}.{ext}
    assert up["key"].startswith(f"{KB_ID}/{DOC_ID}/images/")
    assert up["key"].endswith(".png")
    assert up["content_type"] == "image/png"
    assert up["idempotency_key"] == f"parse-image-{DOC_ID}-{IMAGE_ID}"
    assert up["size"] > 0

    # The markdown link [图片: 图片](oss_url) should be appended to the
    # last parent's content (mirrors legacy parse_service._build_image_placeholder).
    # The URL is the OSS object path "{bucket}/{key}" (matches legacy
    # ImageUploader.upload() return value, minio_client.py line 57).
    write_call = mock_write.call_args
    parents_arg = write_call.kwargs["parents"]
    children_arg = write_call.kwargs["children"]
    # Image chunk should NOT be in parents or children
    parent_ids = [p["chunk_id"] for p in parents_arg]
    child_ids = [c["chunk_id"] for c in children_arg]
    assert IMAGE_ID not in parent_ids
    assert IMAGE_ID not in child_ids
    # The last parent's content should contain the image markdown link
    last_parent = parents_arg[-1]
    assert "[图片: 图片](" in last_parent["content"]
    # URL should be OSS path "kb-docs/{kb_id}/{doc_id}/images/{uuid}.png"
    # NOT the bare object_id (IMAGE_OBJ_ID)
    assert "kb-docs/" in last_parent["content"]
    assert f"{KB_ID}/{DOC_ID}/images/" in last_parent["content"]
    assert ".png" in last_parent["content"]
    # object_id should NOT appear as the URL (it's a UUID, not an OSS path)
    assert IMAGE_OBJ_ID not in last_parent["content"]


# AC: Embed RPC embeds child chunks + summary separately
#     (summary NOT in child_chunks to avoid double-write)
@pytest.mark.asyncio
async def test_embed_separates_summary_from_children():
    chunks = [_parent(), _child(CHILD_A), _child(CHILD_B)]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # embed called once with child texts + summary text
    assert len(rag.embed_calls) == 1
    embedded_texts = rag.embed_calls[0]
    # 2 children + 1 summary = 3 texts
    assert len(embedded_texts) == 3
    # First two are child contents
    assert embedded_texts[0] == "child chunk text"
    assert embedded_texts[1] == "child chunk text"
    # Third is the summary answer
    assert embedded_texts[2] == rag._summary_answer

    # write_chunks called with parents + children (no summary in children)
    write_call = mock_write.call_args
    children_arg = write_call.kwargs["children"]
    summaries_arg = write_call.kwargs["summaries"]
    assert len(children_arg) == 2  # only 2 children, no summary
    assert summaries_arg is not None
    assert len(summaries_arg) == 1  # summary passed separately


# AC: Core API insert with pre-computed vectors; metadata includes all fields
@pytest.mark.asyncio
async def test_core_insert_vector_metadata():
    chunks = [_parent(), _child(CHILD_A)]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, _, _, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    assert len(core.insert_calls) == 1
    ins = core.insert_calls[0]
    assert ins["vector_store_id"] == VECTOR_STORE_ID
    assert ins["idempotency_key"] == f"parse-{DOC_ID}"

    # 1 child + 1 summary = 2 documents inserted
    assert len(ins["documents"]) == 2

    # Check the child document metadata has all required fields
    child_doc = ins["documents"][0]
    assert child_doc["id"] == CHILD_A
    assert child_doc["content"] == "child chunk text"
    assert "vector" in child_doc
    assert len(child_doc["vector"]) == 4  # dimension from fake
    md = child_doc["metadata"]
    assert md["chunk_id"] == CHILD_A
    assert md["chunk_type"] == "child"
    assert md["parent_content"] == "parent block text"
    assert md["parent_chunk_id"] == PARENT_ID
    assert md["page_number"] == "1"
    assert md["content_type"] == "text"
    assert md["doc_id"] == DOC_ID
    assert md["file_name"] == "a.pdf"

    # Summary document metadata — chunk_type must be "doc_summary" (not "child")
    summary_doc = ins["documents"][1]
    assert summary_doc["metadata"]["chunk_type"] == "doc_summary"
    assert summary_doc["metadata"]["doc_id"] == DOC_ID


# AC: write_chunks receives parents + children + summaries separately
@pytest.mark.asyncio
async def test_write_chunks_separate_lists():
    chunks = [_parent(PARENT_ID, "parent text"), _child(CHILD_A), _child(CHILD_B)]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    write_call = mock_write.call_args
    assert write_call.kwargs["tenant_id"] == TENANT_ID
    assert write_call.kwargs["kb_id"] == KB_ID
    assert write_call.kwargs["doc_id"] == DOC_ID
    assert write_call.kwargs["file_name"] == "a.pdf"
    assert len(write_call.kwargs["parents"]) == 1
    assert len(write_call.kwargs["children"]) == 2
    assert write_call.kwargs["summaries"] is not None
    assert len(write_call.kwargs["summaries"]) == 1


# AC: State machine → failed on exception (with sanitized error_msg)
@pytest.mark.asyncio
async def test_failure_marks_doc_failed_with_sanitized_error():
    rag = _FakeRagEngine(chunks=[])

    # Override parse to raise
    original_parse = rag.parse

    async def failing_parse(**kwargs):
        raise RuntimeError(
            "download failed from /tmp/secret/path token=abc123 "
            + "x" * 600
        )

    rag.parse = failing_parse
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # First call: parsing; second call: failed
    status_calls = [c.kwargs["parse_status"] for c in mock_status.call_args_list]
    assert status_calls == ["parsing", "failed"]
    failed_call = mock_status.call_args_list[-1]
    error_msg = failed_call.kwargs["error_message"]
    # Sanitized: paths redacted, truncated to 500 chars
    assert "[redacted]" in error_msg, f"expected [redacted] in error_msg, got: {error_msg}"
    assert "/tmp/secret" not in error_msg, f"original path leaked: {error_msg}"
    assert "abc123" not in error_msg, f"token value leaked: {error_msg}"
    assert len(error_msg) <= 500


# AC: _generate_summary — best-effort, first 3 parents, prompt format
@pytest.mark.asyncio
async def test_summary_uses_first_3_parents_and_prompt():
    parents = [
        _parent("p1", "内容一"),
        _parent("p2", "内容二"),
        _parent("p3", "内容三"),
        _parent("p4", "内容四"),  # should be excluded (only first 3)
    ]
    rag = _FakeRagEngine()
    svc = ParseOrchestrator(
        db_pool=_FakePool(),
        core_client_factory=lambda tid: _FakeCoreClient(),
        rag_engine_client=rag,
    )

    result = await svc._generate_summary(parents)

    assert result is not None
    assert result["content"] == rag._summary_answer
    assert result["content_type"] == "text"
    assert result["page_number"] == 1
    assert result["parent_chunk_id"] is None
    assert result["parent_content"] is None
    assert result["token_count"] >= 1

    # Generate called with the correct prompt (first 3 parents joined).
    # The prompt uses the same English template as legacy SummaryService
    # (summary_service.py lines 53-56) that instructs the LLM to match the
    # content's language.
    assert len(rag.generate_calls) == 1
    gen_call = rag.generate_calls[0]
    prompt = gen_call["question"]
    assert "Summarize the following content in 200-500 characters" in prompt
    assert "Use the same language as the content" in prompt
    assert "内容一" in prompt
    assert "内容二" in prompt
    assert "内容三" in prompt
    assert "内容四" not in prompt  # 4th parent excluded
    assert gen_call["context"] == []
    assert gen_call["history"] == []
    assert gen_call["max_tokens"] == 500


# AC: _generate_summary — failure returns None, does not block
@pytest.mark.asyncio
async def test_summary_failure_returns_none():
    rag = _FakeRagEngine()
    # Override generate to raise
    async def failing_generate(**kwargs):
        raise RuntimeError("vLLM unavailable")

    rag.generate = failing_generate
    svc = ParseOrchestrator(
        db_pool=_FakePool(),
        core_client_factory=lambda tid: _FakeCoreClient(),
        rag_engine_client=rag,
    )

    result = await svc._generate_summary([_parent()])
    assert result is None


# AC: _generate_summary — no parents → None
@pytest.mark.asyncio
async def test_summary_no_parents_returns_none():
    rag = _FakeRagEngine()
    svc = ParseOrchestrator(
        db_pool=_FakePool(),
        core_client_factory=lambda tid: _FakeCoreClient(),
        rag_engine_client=rag,
    )
    result = await svc._generate_summary([])
    assert result is None
    # Generate should NOT have been called
    assert len(rag.generate_calls) == 0


# AC: _generate_summary — empty parent content → None
@pytest.mark.asyncio
async def test_summary_empty_parent_content_returns_none():
    rag = _FakeRagEngine()
    svc = ParseOrchestrator(
        db_pool=_FakePool(),
        core_client_factory=lambda tid: _FakeCoreClient(),
        rag_engine_client=rag,
    )
    result = await svc._generate_summary([_parent(PARENT_ID, "")])
    assert result is None


# AC: _generate_summary — empty answer → None
@pytest.mark.asyncio
async def test_summary_empty_answer_returns_none():
    rag = _FakeRagEngine(summary_answer="   ")
    svc = ParseOrchestrator(
        db_pool=_FakePool(),
        core_client_factory=lambda tid: _FakeCoreClient(),
        rag_engine_client=rag,
    )
    result = await svc._generate_summary([_parent()])
    assert result is None


# AC: _sanitize_error — redacts paths/tokens, truncates 500 chars
def test_sanitize_error_redacts_paths_and_tokens():
    msg = "failed at /tmp/secret/file.txt with token=abc123 bearer XYZ"
    result = _sanitize_error(msg)
    assert "/tmp/secret/file.txt" not in result
    assert "abc123" not in result
    assert "[redacted]" in result


def test_sanitize_error_redacts_windows_paths():
    msg = "failed at C:\\Users\\PC\\secrets\\key.pem with token=xyz"
    result = _sanitize_error(msg)
    assert "C:\\Users\\PC\\secrets\\key.pem" not in result
    assert "[redacted]" in result


def test_sanitize_error_redacts_presigned_url_params():
    msg = "download failed: X-Amz-Signature=abc123&X-Amz-Credential=AKIAxyz&expires=12345"
    result = _sanitize_error(msg)
    assert "abc123" not in result
    assert "AKIAxyz" not in result
    assert "[redacted]" in result


def test_sanitize_error_truncates_to_500():
    msg = "x" * 600
    result = _sanitize_error(msg)
    assert len(result) == 500


def test_sanitize_error_preserves_short_messages():
    msg = "parse error: invalid PDF"
    result = _sanitize_error(msg)
    assert result == "parse error: invalid PDF"


# AC: Equivalence — same input → same kb_chunks row count and content
# This test verifies that given the same Parse RPC output, the orchestrator
# produces the same write_chunks call (same parents, children, summaries).
@pytest.mark.asyncio
async def test_equivalence_same_input_same_output():
    chunks = [
        _parent(PARENT_ID, "parent content A"),
        _child(CHILD_A, "child content A", parent_content="parent content A"),
        _child(CHILD_B, "child content B", parent_content="parent content A"),
    ]
    rag = _FakeRagEngine(chunks=chunks)

    # Run twice with the same input
    results: list[dict] = []
    for _ in range(2):
        svc, _, _, _ = _make_service(rag_engine=_FakeRagEngine(chunks=chunks))
        p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()
        with p_status, p_write, p_get, p_delete:
            await svc.process_document(
                tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
                object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
                chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
            )
        wc = mock_write.call_args
        results.append({
            "parents": [(p["chunk_id"], p["content"]) for p in wc.kwargs["parents"]],
            "children": [(c["chunk_id"], c["content"]) for c in wc.kwargs["children"]],
            "summaries_len": len(wc.kwargs["summaries"]) if wc.kwargs["summaries"] else 0,
            "summaries_content": [s["content"] for s in (wc.kwargs["summaries"] or [])],
        })

    # Both runs produce identical kb_chunks content
    assert results[0] == results[1]
    # Row count: 1 parent + 2 children + 1 summary = 4
    total_rows_0 = len(results[0]["parents"]) + len(results[0]["children"]) + results[0]["summaries_len"]
    assert total_rows_0 == 4


# AC: No children → no embed, no vector insert, but summary still attempted
@pytest.mark.asyncio
async def test_no_children_still_generates_summary():
    chunks = [_parent()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # embed called with just the summary text (0 children + 1 summary)
    assert len(rag.embed_calls) == 1
    assert len(rag.embed_calls[0]) == 1  # only summary

    # Core insert called with 1 document (the summary)
    assert len(core.insert_calls) == 1
    assert len(core.insert_calls[0]["documents"]) == 1

    # write_chunks: 1 parent, 0 children, 1 summary
    wc = mock_write.call_args
    assert len(wc.kwargs["parents"]) == 1
    assert len(wc.kwargs["children"]) == 0
    assert len(wc.kwargs["summaries"]) == 1


# AC: Summary disabled when generate fails → embed only children, no summary
@pytest.mark.asyncio
async def test_summary_failure_no_summary_in_write_or_embed():
    chunks = [_parent(), _child(CHILD_A)]
    rag = _FakeRagEngine(chunks=chunks)

    async def failing_generate(**kwargs):
        raise RuntimeError("vLLM down")

    rag.generate = failing_generate
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # embed called with only child text (no summary)
    assert len(rag.embed_calls) == 1
    assert len(rag.embed_calls[0]) == 1  # only 1 child

    # Core insert: 1 document (child only)
    assert len(core.insert_calls) == 1
    assert len(core.insert_calls[0]["documents"]) == 1

    # write_chunks: no summaries
    wc = mock_write.call_args
    assert wc.kwargs["summaries"] is None or len(wc.kwargs["summaries"]) == 0


# AC: Bucket not found → failed status
@pytest.mark.asyncio
async def test_bucket_not_found_marks_failed():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    core = _FakeCoreClient(bucket_id=None)
    svc, rag, core, pool = _make_service(rag_engine=rag, core_client=core)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    status_calls = [c.kwargs["parse_status"] for c in mock_status.call_args_list]
    assert "failed" in status_calls


# AC: download_url missing → failed
@pytest.mark.asyncio
async def test_missing_download_url_marks_failed():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    core = _FakeCoreClient(download_url="")
    svc, rag, core, pool = _make_service(rag_engine=rag, core_client=core)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    status_calls = [c.kwargs["parse_status"] for c in mock_status.call_args_list]
    assert "failed" in status_calls


# AC: Image with no image_bytes is skipped (no upload)
@pytest.mark.asyncio
async def test_no_image_bytes_skips_upload():
    # image chunk with empty bytes
    img = _image_chunk(image_bytes=b"")
    chunks = [_parent(), _child(), img]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, _, _, _, _ = _patch_repos()

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # No upload calls (image_bytes was empty)
    assert len(core.upload_calls) == 0


# AC: SUMMARY_PARENT_COUNT is 3 (matches rag-engine SummaryService)
def test_summary_parent_count_is_3():
    assert SUMMARY_PARENT_COUNT == 3


# ── Additional robustness tests (review fixes) ──────────────────────────────


# tenant_id guard: empty tenant_id → ValueError before any DB call
@pytest.mark.asyncio
async def test_empty_tenant_id_raises():
    svc, _, _, _ = _make_service()
    with pytest.raises(ValueError, match="tenant_id must not be empty"):
        await svc.process_document(
            tenant_id="", kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )


# Idempotency: already-ready document → skip (no parse, no write)
@pytest.mark.asyncio
async def test_idempotency_skips_already_ready():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    # existing doc with parse_status="ready"
    existing_doc = {"parse_status": "ready", "id": DOC_ID}
    p_status, p_write, p_get, p_delete, mock_status, mock_write, mock_get, _ = _patch_repos(
        existing_doc=existing_doc,
    )

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # get_document was called to check status
    assert mock_get.call_count == 1
    # No parse, no embed, no write, no vector insert
    assert len(rag.parse_calls) == 0
    assert len(rag.embed_calls) == 0
    assert len(core.insert_calls) == 0
    assert mock_write.call_count == 0
    # No status update (no parsing/indexing/ready transitions)
    assert mock_status.call_count == 0


# Idempotency: non-ready document → proceeds with pipeline
@pytest.mark.asyncio
async def test_idempotency_proceeds_when_not_ready():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    # existing doc with parse_status="pending"
    existing_doc = {"parse_status": "pending", "id": DOC_ID}
    p_status, p_write, p_get, p_delete, mock_status, mock_write, mock_get, _ = _patch_repos(
        existing_doc=existing_doc,
    )

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # Pipeline proceeded: parse called, status transitions happened
    assert len(rag.parse_calls) == 1
    status_calls = [c.kwargs["parse_status"] for c in mock_status.call_args_list]
    assert status_calls == ["parsing", "indexing", "ready"]


# Reparse idempotency: prior kb_chunks must be deleted before re-writing
@pytest.mark.asyncio
async def test_reparse_deletes_prior_chunks():
    """Reparse of a failed doc must delete prior chunks before writing new ones."""
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    existing_doc = {"parse_status": "failed", "id": DOC_ID}
    p_status, p_write, p_get, p_delete, mock_status, mock_write, mock_get, mock_delete = _patch_repos(
        existing_doc=existing_doc,
    )

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # delete_chunks_by_doc was called before write_chunks
    assert mock_delete.called, "expected delete_chunks_by_doc to be called"
    delete_call = mock_delete.call_args
    assert delete_call.kwargs["doc_id"] == DOC_ID
    assert delete_call.kwargs["tenant_id"] == TENANT_ID
    # Core vector delete also called (best-effort cleanup on reparse)
    assert len(core.delete_vector_calls) == 1
    assert core.delete_vector_calls[0]["vector_store_id"] == VECTOR_STORE_ID
    assert DOC_ID in core.delete_vector_calls[0]["filter_expr"]
    # write_chunks still called (new chunks written after delete)
    assert mock_write.called
    # Pipeline proceeded to ready
    status_calls = [c.kwargs["parse_status"] for c in mock_status.call_args_list]
    assert status_calls == ["parsing", "indexing", "ready"]


# Summary chunk_type must be "doc_summary" in the chunk dict returned by
# _generate_summary (so Core vector metadata picks it up correctly)
@pytest.mark.asyncio
async def test_summary_chunk_has_doc_summary_type():
    svc = ParseOrchestrator(
        db_pool=_FakePool(),
        core_client_factory=lambda tid: _FakeCoreClient(),
        rag_engine_client=_FakeRagEngine(),
    )
    result = await svc._generate_summary([_parent()])
    assert result is not None
    assert result["chunk_type"] == "doc_summary"


# update_parse_status returns False → doc not found/deleted → skip pipeline
# (mirrors rag-engine parse_worker #r5)
@pytest.mark.asyncio
async def test_update_returns_false_skips_pipeline():
    chunks = [_parent(), _child()]
    rag = _FakeRagEngine(chunks=chunks)
    svc, rag, core, pool = _make_service(rag_engine=rag)
    p_status, p_write, p_get, p_delete, mock_status, mock_write, mock_get, _ = _patch_repos()
    # Make update_parse_status return False (no row matched)
    mock_status.return_value = False

    with p_status, p_write, p_get, p_delete:
        await svc.process_document(
            tenant_id=TENANT_ID, kb_id=KB_ID, doc_id=DOC_ID,
            object_id=OBJECT_ID, file_name="a.pdf", file_type="pdf",
            chunk_size=1024, vector_store_id=VECTOR_STORE_ID,
        )

    # Pipeline aborted: no parse, no embed, no write, no vector insert
    assert len(rag.parse_calls) == 0
    assert len(rag.embed_calls) == 0
    assert len(core.insert_calls) == 0
    assert mock_write.call_count == 0
    # Only one status update attempted (parsing), no indexing/ready
    assert mock_status.call_count == 1
    assert mock_status.call_args.kwargs["parse_status"] == "parsing"
