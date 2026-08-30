"""Unit tests for parse_worker + gRPC server (US-015 / SPEC §5.1, §5.3, §4.1).

These tests do NOT require NATS, Milvus, PG, Core API, or vLLM. All
dependencies are injected as fakes/mocks. They validate:

* parse_worker: NATS subscription, parse pipeline orchestration, state
  transitions (pending→parsing→indexing→ready|failed), idempotency
  (skip if already ready), best-effort summary degradation.
* gRPC server: Query RPC delegates to QAService, validation (empty
  question, top_k bounds), error mapping (INVALID_ARGUMENT /
  DEADLINE_EXCEEDED / UNAVAILABLE / INTERNAL).
"""
from __future__ import annotations

import asyncio
import json
import os
import sys
from typing import Any
from unittest.mock import MagicMock

import pytest

# Stub heavy/optional deps so the modules import cleanly in the test env.
for _mod in (
    "docling",
    "docling.datamodel.base_models",
    "docling.datamodel.pipeline_options",
    "docling.document_converter",
    "docling_core",
    "docling_core.types.doc",
    "llama_index",
    "llama_index.readers",
    "llama_index.readers.docling",
    "llama_index.core",
    "llama_index.core.node_parser",
    "llama_index.core.schema",
    "llama_index.core.retrievers",
    "llama_index.core.chat_engine",
    "llama_index.core.memory",
    "llama_index.embeddings",
    "llama_index.llms.openai_like",
    "llama_index.vector_stores",
    "llama_index.vector_stores.milvus",
    "llama_index.storage.chat_store.redis",
    "minio",
    "minio.error",
    "openai",
    "pymilvus",
    "pymilvus.connections",
    "asyncpg",
    "nats",
    "nats.aio",
    "nats.aio.client",
    "fitz",
    "docx",
    "openpyxl",
    "pptx",
    "grpc",
    "grpc.aio",
    "grpc._utilities",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

# grpc.__version__ is read by the generated rag_pb2_grpc stub; the MagicMock
# default does not expose it, so set it explicitly. Also stub
# first_version_is_lower so the version check passes.
_grpc_stub: Any = sys.modules["grpc"]
_grpc_stub.__version__ = "1.83.0"
_grpc_utilities_stub: Any = sys.modules["grpc._utilities"]
_grpc_utilities_stub.first_version_is_lower = lambda a, b: False

# Make rag-engine root importable (app.* imports).
_HERE = os.path.dirname(os.path.abspath(__file__))
_REPO_ROOT = os.path.dirname(os.path.dirname(_HERE))
if _REPO_ROOT not in sys.path:
    sys.path.insert(0, os.path.join(_REPO_ROOT, "ai", "rag-engine"))

from app.grpc import rag_pb2 as rag_pb
from app.grpc.server import RagEngineServicer
from app.services.qa_service import QAResult
from app.services.retrieve_service import RetrievedSource
from app.workers.parse_worker import ParseWorker

# ── Fixtures ──────────────────────────────────────────────────────────────────


@pytest.fixture(autouse=True)
def _run_to_thread_inline(monkeypatch):
    """Keep these logic tests independent of the local thread executor."""

    async def _inline(func, /, *args, **kwargs):
        return func(*args, **kwargs)

    monkeypatch.setattr(asyncio, "to_thread", _inline)


class FakeStatusUpdater:
    """Records parse_status updates for assertions."""

    def __init__(self, current_status: str | None = None) -> None:
        self.updates: list[dict] = []
        self._current = current_status

    async def update(self, *, tenant_id, doc_id, parse_status,
                     error_message=None, chunk_count=None) -> bool:
        self.updates.append({
            "tenant_id": tenant_id,
            "doc_id": doc_id,
            "parse_status": parse_status,
            "error_message": error_message,
            "chunk_count": chunk_count,
        })
        return True

    async def current(self, *, tenant_id, doc_id):
        return self._current


class FakeCoreApi:
    """Returns a local temp file path for download_object."""

    def __init__(self, local_path: str = "/tmp/fake.docx") -> None:
        self._path = local_path
        self.downloads: list[Any] = []

    async def download_object(self, object_id, *, dest_dir=None, file_name=None):
        self.downloads.append(object_id)
        return self._path


class FakeNatsMsg:
    def __init__(self, payload: dict) -> None:
        self.data = json.dumps(payload).encode("utf-8")


class FakeNatsSubscription:
    def __init__(self) -> None:
        self.unsubscribed = False

    async def unsubscribe(self):
        self.unsubscribed = True


class FakeNatsClient:
    def __init__(self) -> None:
        self.subscriptions: list[tuple[str, object]] = []
        self.drained = False

    async def subscribe(self, subject, cb=None):
        sub = FakeNatsSubscription()
        self.subscriptions.append((subject, cb))
        return sub

    async def drain(self):
        self.drained = True


class FakeDbPool:
    """Fake asyncpg pool — ``async with pool.acquire() as conn`` yields a dummy conn."""

    class _Conn:
        async def execute(self, *args, **kwargs):
            return "UPDATE 1"

    class _AcquireCtx:
        async def __aenter__(self):
            return FakeDbPool._Conn()

        async def __aexit__(self, *exc):
            pass

    def acquire(self):
        return FakeDbPool._AcquireCtx()


def _make_worker(
    *,
    core_api=None,
    parse_service=None,
    chunk_service=None,
    summary_service=None,
    embed_service=None,
    chunks_write=None,
    db_pool=None,
    status_updater=None,
    nats_client=None,
) -> ParseWorker:
    """Build a ParseWorker with all deps injected (no live connections)."""
    async def _default_write(*a, **k):
        return 0
    return ParseWorker(
        nats_client=nats_client,
        core_api=core_api or FakeCoreApi(),  # type: ignore[arg-type]
        parse_service=parse_service or MagicMock(),
        chunk_service=chunk_service or MagicMock(),
        summary_service=summary_service or MagicMock(),
        embed_service=embed_service or MagicMock(),
        chunks_repo_write=chunks_write or _default_write,
        db_pool=db_pool or FakeDbPool(),
        status_updater=status_updater,
    )


# ── parse_worker tests ────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_parse_worker_subscribes_to_nats():
    """AC1: parse_worker subscribes to NATS ani.tasks.kb.parse."""
    nats = FakeNatsClient()
    worker = _make_worker(nats_client=nats)
    await worker.start()
    assert len(nats.subscriptions) == 1
    subject, _ = nats.subscriptions[0]
    assert subject == "ani.tasks.kb.parse"
    await worker.stop()


@pytest.mark.asyncio
async def test_parse_worker_state_transitions_ready():
    """AC3: parse_status transitions pending→parsing→indexing→ready."""
    updater = FakeStatusUpdater(current_status="pending")
    # parse_service.parse returns []; chunk_service.chunk returns ([], [])
    parse_service = MagicMock()
    parse_service.parse.return_value = []
    chunk_service = MagicMock()
    chunk_service.chunk.return_value = ([], [])
    summary_service = MagicMock()
    summary_service.summarize.return_value = None
    embed_service = MagicMock()
    embed_service.embed_and_write.return_value = MagicMock(nodes_written=0)

    async def fake_write(conn, **kwargs):
        return 0

    worker = _make_worker(
        parse_service=parse_service,
        chunk_service=chunk_service,
        summary_service=summary_service,
        embed_service=embed_service,
        chunks_write=fake_write,
        status_updater=updater,
    )
    payload = {
        "doc_id": "d1",
        "kb_id": "k1",
        "storage_path": "kb-docs/k1/d1/file.txt",
        "tenant_id": "t1",
        "file_name": "file.txt",
    }
    await worker.process_message(payload)

    statuses = [u["parse_status"] for u in updater.updates]
    assert "parsing" in statuses
    assert "indexing" in statuses
    assert "ready" in statuses
    # Order: parsing before indexing before ready.
    assert statuses.index("parsing") < statuses.index("indexing")
    assert statuses.index("indexing") < statuses.index("ready")


@pytest.mark.asyncio
async def test_parse_worker_failed_on_exception():
    """AC3: parse_status transitions to failed on error."""
    updater = FakeStatusUpdater(current_status="pending")
    core_api = MagicMock()
    core_api.download_object = MagicMock(side_effect=RuntimeError("download failed"))

    worker = _make_worker(core_api=core_api, status_updater=updater)
    payload = {
        "doc_id": "d1",
        "kb_id": "k1",
        "storage_path": "kb-docs/k1/d1/f.txt",
        "tenant_id": "t1",
    }
    await worker.process_message(payload)

    # First update is 'parsing', then 'failed'.
    assert updater.updates[0]["parse_status"] == "parsing"
    failed = [u for u in updater.updates if u["parse_status"] == "failed"]
    assert len(failed) == 1
    assert "download failed" in (failed[0]["error_message"] or "")


@pytest.mark.asyncio
async def test_parse_worker_idempotent_skip_if_ready():
    """SPEC §5.4: at-least-once; skip if already ready."""
    updater = FakeStatusUpdater(current_status="ready")
    parse_service = MagicMock()
    worker = _make_worker(parse_service=parse_service, status_updater=updater)
    payload = {
        "doc_id": "d1",
        "kb_id": "k1",
        "storage_path": "kb-docs/k1/d1/f.txt",
        "tenant_id": "t1",
    }
    await worker.process_message(payload)
    # No status updates — skipped.
    assert updater.updates == []
    parse_service.parse.assert_not_called()


@pytest.mark.asyncio
async def test_parse_worker_summary_degradation():
    """SPEC §5.4 / §6.3: summary failure degrades, does not block."""
    updater = FakeStatusUpdater(current_status="pending")
    parse_service = MagicMock()
    parse_service.parse.return_value = []
    chunk_service = MagicMock()
    chunk_service.chunk.return_value = ([], [])
    summary_service = MagicMock()
    summary_service.summarize.side_effect = RuntimeError("LLM timeout")
    embed_service = MagicMock()
    embed_service.embed_and_write.return_value = MagicMock(nodes_written=0)

    async def fake_write(conn, **kwargs):
        return 0

    worker = _make_worker(
        parse_service=parse_service,
        chunk_service=chunk_service,
        summary_service=summary_service,
        embed_service=embed_service,
        chunks_write=fake_write,
        status_updater=updater,
    )
    payload = {"doc_id": "d1", "kb_id": "k1", "storage_path": "p", "tenant_id": "t1"}
    await worker.process_message(payload)
    # Still reaches ready despite summary failure.
    statuses = [u["parse_status"] for u in updater.updates]
    assert "ready" in statuses


@pytest.mark.asyncio
async def test_parse_worker_missing_fields_skipped():
    """Invalid payload (missing doc_id/kb_id/storage_path) is skipped."""
    updater = FakeStatusUpdater()
    worker = _make_worker(status_updater=updater)
    await worker.process_message({"doc_id": "d1"})  # missing kb_id, storage_path
    assert updater.updates == []


# ── gRPC server tests ─────────────────────────────────────────────────────────


class FakeContext:
    """Mimics grpc.aio.ServicerContext for unit tests."""

    def __init__(self) -> None:
        self.aborted_code = None
        self.aborted_details = None

    async def abort(self, code, details):
        self.aborted_code = code
        self.aborted_details = details
        raise Exception(f"aborted: {code} {details}")  # noqa: TRY002


class FakeQAService:
    """Fake QAService returning a canned QAResult."""

    def __init__(self, result: QAResult | None = None, exc: Exception | None = None) -> None:
        self._result = result
        self._exc = exc
        self.calls: list[Any] = []

    def chat(self, **kwargs):
        self.calls.append(kwargs)
        if self._exc is not None:
            raise self._exc
        return self._result or QAResult(
            answer="answer",
            sources=[RetrievedSource(chunk_id="c1", doc_id="d1", file_name="f.txt",
                                     page=1, content="ctx", score=0.9,
                                     chunk_type="child", parent_content="p")],
            session_id="s1",
            input_tokens=10,
            output_tokens=20,
        )


def _make_request(**kwargs) -> rag_pb.QueryRequest:
    return rag_pb.QueryRequest(**kwargs)


@pytest.mark.asyncio
async def test_grpc_query_success():
    """AC4: gRPC server implements Query RPC (sync)."""
    qa = FakeQAService()
    servicer = RagEngineServicer(qa_service=qa)
    ctx = FakeContext()
    req = _make_request(
        tenant_id="t1", kb_id="k1", question="what is RAG?", top_k=0,
        score_threshold=0.0, session_id="", inference_service_name="",
    )
    resp = await servicer.Query(req, ctx)
    assert ctx.aborted_code is None
    assert resp.answer == "answer"
    assert resp.session_id == "s1"
    assert resp.input_tokens == 10
    assert resp.output_tokens == 20
    assert len(resp.sources) == 1
    assert resp.sources[0].doc_id == "d1"
    assert resp.sources[0].score == pytest.approx(0.9)
    # top_k=0 → default applied.
    assert qa.calls[0]["top_k"] == 5  # DEFAULT_TOP_K
    assert qa.calls[0]["score_threshold"] == 0.3  # DEFAULT_SCORE_THRESHOLD


@pytest.mark.asyncio
async def test_grpc_query_empty_question_invalid_argument():
    """SPEC §4.4: empty question → INVALID_ARGUMENT."""
    servicer = RagEngineServicer(qa_service=FakeQAService())
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None  # abort was called


@pytest.mark.asyncio
async def test_grpc_query_top_k_out_of_range():
    """SPEC §4.4 / §5.2: top_k out of [1,20] → INVALID_ARGUMENT."""
    servicer = RagEngineServicer(qa_service=FakeQAService())
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="q", top_k=100)
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_grpc_query_llm_timeout_deadline_exceeded():
    """SPEC §4.4: LLM timeout → DEADLINE_EXCEEDED."""
    qa = FakeQAService(exc=TimeoutError("llm timed out"))
    servicer = RagEngineServicer(qa_service=qa)
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="q")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_grpc_query_backend_unavailable():
    """SPEC §4.4: vLLM/Milvus unavailable → UNAVAILABLE."""
    qa = FakeQAService(exc=RuntimeError("vLLM unavailable"))
    servicer = RagEngineServicer(qa_service=qa)
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="q")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_grpc_query_question_too_long():
    """SPEC §7.2: question > 2000 chars → INVALID_ARGUMENT."""
    servicer = RagEngineServicer(qa_service=FakeQAService())
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="x" * 2001)
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_grpc_query_no_result_sources_empty():
    """SPEC §5.4: no-result returns answer with empty sources."""
    qa = FakeQAService(result=QAResult(
        answer="未检索到与问题相关的内容，无法回答。",
        sources=[], session_id="s2", input_tokens=0, output_tokens=0,
    ))
    servicer = RagEngineServicer(qa_service=qa)
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="q")
    resp = await servicer.Query(req, ctx)
    assert resp.answer == "未检索到与问题相关的内容，无法回答。"
    assert list(resp.sources) == []


@pytest.mark.asyncio
async def test_grpc_query_missing_tenant_id():
    """#8: missing tenant_id → INVALID_ARGUMENT."""
    servicer = RagEngineServicer(qa_service=FakeQAService())
    ctx = FakeContext()
    req = _make_request(tenant_id="", kb_id="k1", question="q")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_grpc_query_missing_kb_id():
    """#8: missing kb_id → INVALID_ARGUMENT."""
    servicer = RagEngineServicer(qa_service=FakeQAService())
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="", question="q")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Query(req, ctx)
    assert ctx.aborted_code is not None


@pytest.mark.asyncio
async def test_grpc_query_negative_score_threshold_disables_threshold():
    """#12: negative score_threshold is passed through (disables threshold)."""
    qa = FakeQAService()
    servicer = RagEngineServicer(qa_service=qa)
    ctx = FakeContext()
    req = _make_request(tenant_id="t1", kb_id="k1", question="q", score_threshold=-1.0)
    await servicer.Query(req, ctx)
    assert ctx.aborted_code is None
    # Negative threshold passed through as-is.
    assert qa.calls[0]["score_threshold"] == -1.0
