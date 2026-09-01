"""Tests for the kb-service RetrieveService (issue-031 / Plan §4.2).

Covers all Acceptance Criteria:
- 3 retrieval modes: hybrid / vector / keyword
- RRF fusion (delegates to app.services.rrf, tested separately)
- _build_sources_from_fusion: hybrid chunk_id in vector_results uses cosine
  score; keyword-only uses RRF score min-max normalization
- _process_vector_only: assembles sources from Core API results (with content)
- _process_keyword_only: assembles sources from PG results
- _return_parent_and_dedup: child with parent_content → replace content;
  same parent_chunk_id dedup keeping highest score; doc_summary not deduped
- parent backfill: empty parent_content → lookup parent_chunk_id (child) /
  all parent blocks (doc_summary)
- hybrid score_threshold normalization: max_score = max(vector cosine)
- Jaccard equivalence: same KB chunk_id set overlap > 90%

Uses fakes for CoreClient, RagEngine client, asyncpg pool, and parent
lookup — no real services required. chunk_repo.keyword_search is mocked
(it is unit-tested in test_chunk_repository.py).
"""
import os
import sys
from contextlib import asynccontextmanager

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.services import retrieve_service as retrieve_module
from app.services.contracts import RetrieveServiceProtocol
from app.services.retrieve_service import (
    DEFAULT_RRF_K,
    DEFAULT_TOP_K,
    RetrieveService,
)


TENANT_ID = "11111111-1111-1111-1111-111111111111"
KB_ID = "22222222-2222-2222-2222-222222222222"
DOC_ID = "33333333-3333-3333-3333-333333333333"
VECTOR_STORE_ID = "vs_kb_2222222222222222222222222"
PARENT_ID = "44444444-4444-4444-4444-444444444444"
PARENT_ID_2 = "44444444-4444-4444-4444-444444444445"
PARENT_ID_3 = "44444444-4444-4444-4444-444444444446"
CHILD_A = "55555555-5555-5555-5555-555555555551"
CHILD_B = "55555555-5555-5555-5555-555555555552"
CHILD_C = "55555555-5555-5555-5555-555555555553"
SUMMARY_ID = "66666666-6666-6666-6666-666666666666"


# ── Fakes ────────────────────────────────────────────────────────────────────


class _FakeRagEngine:
    """Fake RagEngineGRPCClient.embed."""

    def __init__(self, vectors=None, dimension=4):
        self._vectors = vectors or [[0.1, 0.2, 0.3, 0.4]]
        self._dimension = dimension
        self.embed_calls: list[list[str]] = []

    async def embed(self, *, texts):
        self.embed_calls.append(list(texts))
        return list(self._vectors), self._dimension


class _FakeCoreClient:
    """Fake CoreClient.search_vector_store."""

    def __init__(self, vector_results=None):
        self._vector_results = vector_results or []
        self.search_calls: list[dict] = []

    async def search_vector_store(self, *, vector_store_id, vector, top_k, filter_expr=None):
        self.search_calls.append({
            "vector_store_id": vector_store_id,
            "vector": vector,
            "top_k": top_k,
            "filter_expr": filter_expr,
        })
        return list(self._vector_results)


class _FakeParentLookup:
    """Fake _ParentLookup for backfill."""

    def __init__(self, parent_by_id=None, parents_by_doc=None):
        self._parent_by_id = parent_by_id or {}
        self._parents_by_doc = parents_by_doc or {}
        self.lookup_parent_calls: list[dict] = []
        self.lookup_parents_calls: list[dict] = []

    async def lookup_parent(self, *, tenant_id, parent_chunk_id):
        self.lookup_parent_calls.append({
            "tenant_id": tenant_id, "parent_chunk_id": parent_chunk_id,
        })
        return self._parent_by_id.get(parent_chunk_id)

    async def lookup_parents(self, *, tenant_id, doc_id):
        self.lookup_parents_calls.append({
            "tenant_id": tenant_id, "doc_id": doc_id,
        })
        return list(self._parents_by_doc.get(doc_id, []))


class _FakePool:
    """Fake asyncpg Pool (used only as a placeholder; keyword_search is mocked)."""

    def __init__(self, conn=None):
        self._conn = conn

    @asynccontextmanager
    async def acquire(self):
        yield self._conn


class _FakeConn:
    """Minimal fake asyncpg Connection (keyword_search is mocked, so unused)."""

    def transaction(self):
        @asynccontextmanager
        async def _tx():
            yield self
        return _tx()


# ── Builders ────────────────────────────────────────────────────────────────


def _vec_hit(chunk_id, score, *, content="chunk text", doc_id=DOC_ID,
             file_name="a.pdf", page=1, chunk_type="child",
             parent_content="", parent_chunk_id=PARENT_ID):
    """Build a Core API vector-search hit dict (§1.4 content + metadata)."""
    return {
        "id": chunk_id,
        "score": score,
        "content": content,
        "metadata": {
            "chunk_id": chunk_id,
            "doc_id": doc_id,
            "file_name": file_name,
            "page_number": str(page),
            "content_type": "text",
            "chunk_type": chunk_type,
            "parent_content": parent_content,
            "parent_chunk_id": parent_chunk_id,
        },
    }


def _kw_row(chunk_id, score, *, content="child text", parent_content="parent text",
            parent_chunk_id=PARENT_ID, doc_id=DOC_ID, file_name="a.pdf",
            page_number=1, chunk_type="child"):
    """Build a keyword_search result row (as returned by chunk_repo)."""
    return {
        "chunk_id": chunk_id,
        "content": content,
        "parent_content": parent_content,
        "parent_chunk_id": parent_chunk_id,
        "doc_id": doc_id,
        "file_name": file_name,
        "page_number": page_number,
        "content_type": "text",
        "chunk_type": chunk_type,
        "score": score,
    }


def _make_service(*, rag_engine=None, core_client=None, parent_lookup=None,
                  kw_results=None):
    """Build a RetrieveService with fakes.

    chunk_repo.keyword_search is mocked to return kw_results (bypassing
    jieba tokenization + pg_trgm SQL, which are unit-tested in
    test_chunk_repository.py).
    """
    rag = rag_engine or _FakeRagEngine()
    core = core_client or _FakeCoreClient()
    pool = _FakePool(_FakeConn())
    lookup = parent_lookup or _FakeParentLookup()
    factory = lambda tenant_id: core
    svc = RetrieveService(
        db_pool=pool,
        core_client_factory=factory,
        rag_engine_client=rag,
        parent_lookup=lookup,
    )
    # Mock keyword_search to return the canned rows, capturing the limit arg.
    _kw = list(kw_results or [])
    kw_calls: list[dict] = []

    async def _fake_keyword_search(conn, *, tenant_id, kb_id, query, limit=10):
        kw_calls.append({
            "tenant_id": tenant_id, "kb_id": kb_id, "query": query, "limit": limit,
        })
        return list(_kw)

    # Patch the module-level chunk_repo.keyword_search reference used by
    # retrieve_service. retrieve_service imports `chunk as chunk_repo` and
    # calls `chunk_repo.keyword_search(...)`, so patch the attribute on the
    # retrieve_service module's chunk_repo.
    retrieve_module.chunk_repo.keyword_search = _fake_keyword_search
    return svc, rag, core, lookup, kw_calls


@pytest.fixture(autouse=True)
def _restore_keyword_search():
    """Restore the real chunk_repo.keyword_search after each test."""
    orig = retrieve_module.chunk_repo.keyword_search
    yield
    retrieve_module.chunk_repo.keyword_search = orig


# ── Constants ────────────────────────────────────────────────────────────────


def test_default_constants_match_legacy():
    assert DEFAULT_TOP_K == 5
    assert DEFAULT_RRF_K == 60.0


def test_retrieve_service_satisfies_protocol():
    """AC: RetrieveService implements RetrieveServiceProtocol interface.

    ``runtime_checkable`` Protocols verify method names + call signatures
    structurally (duck typing). RetrieveService.retrieve has the same
    keyword-only signature as RetrieveServiceProtocol.retrieve.
    """
    svc, _, _, _, _ = _make_service()
    assert isinstance(svc, RetrieveServiceProtocol)


# ── hybrid mode ─────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_hybrid_invokes_embed_and_vector_search_and_keyword_search():
    rag = _FakeRagEngine(vectors=[[0.5, 0.6, 0.7, 0.8]])
    core = _FakeCoreClient(vector_results=[_vec_hit(CHILD_A, 0.9)])
    svc, rag, core, _, kw_calls = _make_service(
        rag_engine=rag, core_client=core, kw_results=[_kw_row(CHILD_B, 0.5)],
    )

    sources, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )

    # embed called once with the question
    assert rag.embed_calls == [["查询"]]
    # search_vector_store called with the embedded vector, top_k*2
    assert len(core.search_calls) == 1
    assert core.search_calls[0]["vector"] == [0.5, 0.6, 0.7, 0.8]
    assert core.search_calls[0]["top_k"] == 10
    assert core.search_calls[0]["vector_store_id"] == VECTOR_STORE_ID
    # keyword_search called
    assert len(kw_calls) == 1
    assert kw_calls[0]["limit"] == 10


@pytest.mark.asyncio
async def test_hybrid_max_score_uses_vector_cosine_not_rrf():
    """AC: hybrid score_threshold normalization — max_score = max(vector cosine)."""
    rag = _FakeRagEngine()
    # Two vector hits with cosine scores 0.9 and 0.4
    core = _FakeCoreClient(vector_results=[
        _vec_hit(CHILD_A, 0.9, parent_chunk_id=PARENT_ID),
        _vec_hit(CHILD_B, 0.4, parent_chunk_id=PARENT_ID_2),
    ])
    svc, _, _, _, _ = _make_service(
        rag_engine=rag, core_client=core,
        kw_results=[_kw_row(CHILD_C, 0.5, parent_chunk_id=PARENT_ID_3)],
    )

    _, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )
    # max_score = max(0.9, 0.4) = 0.9 (NOT the tiny RRF score ~0.016)
    assert max_score == pytest.approx(0.9)


@pytest.mark.asyncio
async def test_hybrid_empty_vector_max_score_falls_back_to_zero():
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[])
    svc, _, _, _, _ = _make_service(
        core_client=core, kw_results=[_kw_row(CHILD_C, 0.5)],
    )

    _, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )
    # No vector hits → max_score = 0.0 (gate ② trips on threshold)
    assert max_score == 0.0


@pytest.mark.asyncio
async def test_hybrid_build_sources_uses_cosine_for_vector_hits():
    """AC: _build_sources_from_fusion — dual-leg hit scores above single-leg.

    Hybrid scores are RRF fused scores min-max normalized against the fused
    peak. CHILD_A is hit by BOTH legs (vector rank 0 + keyword rank 0 →
    fused 2/60), so it IS the peak → score 1.0. CHILD_B is keyword-only
    (fused 1/60) → score 0.5. This distinguishes hybrid output from
    vector-mode cosine output (regression test for the hybrid==vector
    score-identity bug).
    """
    rag = _FakeRagEngine()
    # CHILD_A in both vector + keyword; CHILD_B keyword-only
    core = _FakeCoreClient(vector_results=[_vec_hit(CHILD_A, 0.85)])
    svc, _, _, _, _ = _make_service(
        rag_engine=rag, core_client=core,
        kw_results=[_kw_row(CHILD_A, 0.3), _kw_row(CHILD_B, 0.4,
                                                    parent_chunk_id=PARENT_ID_2)],
    )

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )
    src_map = {s["chunk_id"]: s for s in sources}
    # CHILD_A: vector rank 0 (1/60) + keyword rank 1 (1/61) = peak
    # CHILD_B: keyword rank 0 (1/60, score 0.4 > 0.3)
    # CHILD_B score = (1/60) / (1/60 + 1/61)
    if CHILD_A in src_map:
        assert src_map[CHILD_A]["score"] == pytest.approx(1.0)
    if CHILD_B in src_map:
        assert src_map[CHILD_B]["score"] == pytest.approx(
            (1.0 / 60) / (1.0 / 60 + 1.0 / 61)
        )


@pytest.mark.asyncio
async def test_hybrid_keyword_only_hit_uses_rrf_min_max_normalization():
    """AC: _build_sources_from_fusion — keyword-only hit uses RRF score min-max."""
    rag = _FakeRagEngine()
    # CHILD_A in vector (cosine 0.9); CHILD_B keyword-only
    core = _FakeCoreClient(vector_results=[_vec_hit(CHILD_A, 0.9)])
    svc, _, _, _, _ = _make_service(
        rag_engine=rag, core_client=core,
        kw_results=[_kw_row(CHILD_B, 0.4, parent_chunk_id=PARENT_ID_2)],
    )

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )
    src_map = {s["chunk_id"]: s for s in sources}
    # CHILD_B is keyword-only → score = rrf_score / rrf_peak (min-max)
    # Both CHILD_A (rank 0 in vector) and CHILD_B (rank 0 in keyword) are
    # rank 0 in their respective lists → rrf scores 1/60 each.
    # rrf_peak = 1/60; CHILD_B score = (1/60) / (1/60) = 1.0
    if CHILD_B in src_map:
        assert src_map[CHILD_B]["score"] == pytest.approx(1.0)


# ── vector mode ─────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_vector_only_mode_skips_keyword_search():
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[
        _vec_hit(CHILD_A, 0.8, content="vec content A",
                 parent_chunk_id=PARENT_ID),
    ])
    svc, rag, core, _, kw_calls = _make_service(
        rag_engine=rag, core_client=core, kw_results=[],
    )

    sources, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="vector",
        vector_store_id=VECTOR_STORE_ID,
    )
    assert rag.embed_calls == [["查询"]]
    assert len(core.search_calls) == 1
    # No keyword_search
    assert kw_calls == []
    # max_score = max cosine
    assert max_score == pytest.approx(0.8)
    assert len(sources) == 1
    assert sources[0]["chunk_id"] == CHILD_A


def test_process_vector_only_assembles_content_from_core_api():
    """AC: _process_vector_only — assembles sources with content from Core API.

    Tests the helper directly (before _return_parent_and_dedup replaces
    child content with parent_content).
    """
    svc, _, _, _, _ = _make_service()
    vec_results = [
        _vec_hit(CHILD_A, 0.7, content="hello world",
                 doc_id=DOC_ID, file_name="b.pdf", page=3,
                 chunk_type="child", parent_content="parent blk",
                 parent_chunk_id=PARENT_ID),
    ]
    out = svc._process_vector_only(vec_results)
    assert len(out) == 1
    s = out[0]
    assert s["content"] == "hello world"
    assert s["doc_id"] == DOC_ID
    assert s["file_name"] == "b.pdf"
    assert s["page"] == 3
    assert s["parent_content"] == "parent blk"
    assert s["parent_chunk_id"] == PARENT_ID
    assert s["chunk_type"] == "child"
    assert s["score"] == pytest.approx(0.7)


@pytest.mark.asyncio
async def test_vector_only_empty_results():
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[])
    svc, _, _, _, _ = _make_service(rag_engine=rag, core_client=core)

    sources, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="vector",
        vector_store_id=VECTOR_STORE_ID,
    )
    assert sources == []
    assert max_score == 0.0


# ── keyword mode ────────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_keyword_only_mode_skips_embed_and_vector_search():
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[])
    svc, rag, core, _, kw_calls = _make_service(
        rag_engine=rag, core_client=core,
        kw_results=[_kw_row(CHILD_A, 0.6)],
    )

    sources, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=5, retrieval_mode="keyword",
        vector_store_id=VECTOR_STORE_ID,
    )
    # No embed, no vector search
    assert rag.embed_calls == []
    assert core.search_calls == []
    assert len(kw_calls) == 1
    assert len(sources) == 1
    assert sources[0]["chunk_id"] == CHILD_A
    assert max_score == pytest.approx(0.6)


def test_process_keyword_only_assembles_sources_from_pg():
    """AC: _process_keyword_only — assembles sources from PG results.

    Tests the helper directly (before dedup).
    """
    svc, _, _, _, _ = _make_service()
    kw_rows = [_kw_row(
        CHILD_A, 0.5, content="kw content", parent_content="parent",
        parent_chunk_id=PARENT_ID, doc_id=DOC_ID, file_name="c.pdf",
        page_number=2, chunk_type="child",
    )]
    out = svc._process_keyword_only(kw_rows)
    assert len(out) == 1
    s = out[0]
    assert s["content"] == "kw content"
    assert s["parent_content"] == "parent"
    assert s["parent_chunk_id"] == PARENT_ID
    assert s["file_name"] == "c.pdf"
    assert s["page"] == 2
    assert s["score"] == pytest.approx(0.5)


@pytest.mark.asyncio
async def test_keyword_only_empty_results():
    svc, _, _, _, _ = _make_service(kw_results=[])

    sources, max_score = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="keyword",
        vector_store_id=VECTOR_STORE_ID,
    )
    assert sources == []
    assert max_score == 0.0


# ── _return_parent_and_dedup ───────────────────────────────────────────────


def test_return_parent_and_dedup_replaces_child_content_with_parent():
    """AC: child with parent_content → content replaced with parent_content."""
    svc, _, _, _, _ = _make_service()
    sources = [{
        "chunk_id": CHILD_A, "doc_id": DOC_ID, "file_name": "a.pdf",
        "page": 1, "content": "child text", "parent_content": "parent text",
        "parent_chunk_id": PARENT_ID, "chunk_type": "child", "score": 0.9,
    }]
    out = svc._return_parent_and_dedup(sources)
    assert out[0]["content"] == "parent text"


def test_return_parent_and_dedup_same_parent_dedups_highest_score():
    """AC: same parent_chunk_id dedup keeping highest score."""
    svc, _, _, _, _ = _make_service()
    sources = [
        {"chunk_id": CHILD_A, "content": "c1", "parent_content": "p1",
         "parent_chunk_id": PARENT_ID, "chunk_type": "child", "score": 0.5,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
        {"chunk_id": CHILD_B, "content": "c2", "parent_content": "p1",
         "parent_chunk_id": PARENT_ID, "chunk_type": "child", "score": 0.9,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
        {"chunk_id": CHILD_C, "content": "c3", "parent_content": "p2",
         "parent_chunk_id": PARENT_ID_2, "chunk_type": "child", "score": 0.3,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
    ]
    out = svc._return_parent_and_dedup(sources)
    # Two unique parents → 2 results
    assert len(out) == 2
    out_by_parent = {s["parent_chunk_id"]: s for s in out}
    # PARENT_ID: keep CHILD_B (highest score 0.9)
    assert out_by_parent[PARENT_ID]["chunk_id"] == CHILD_B
    assert out_by_parent[PARENT_ID]["score"] == pytest.approx(0.9)
    # PARENT_ID_2: only CHILD_C
    assert out_by_parent[PARENT_ID_2]["chunk_id"] == CHILD_C


def test_return_parent_and_dedup_doc_summary_not_deduped():
    """AC: doc_summary does NOT participate in dedup — kept as-is."""
    svc, _, _, _, _ = _make_service()
    sources = [
        {"chunk_id": SUMMARY_ID, "content": "summary", "parent_content": "",
         "parent_chunk_id": "", "chunk_type": "doc_summary", "score": 0.7,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
        {"chunk_id": CHILD_A, "content": "c1", "parent_content": "p1",
         "parent_chunk_id": PARENT_ID, "chunk_type": "child", "score": 0.5,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
    ]
    out = svc._return_parent_and_dedup(sources)
    # doc_summary kept as-is + 1 deduped child = 2
    assert len(out) == 2
    summary = [s for s in out if s["chunk_type"] == "doc_summary"][0]
    assert summary["chunk_id"] == SUMMARY_ID
    # doc_summary content NOT replaced (no parent replacement for summary)
    assert summary["content"] == "summary"


def test_return_parent_and_dedup_child_without_parent_key_kept():
    """Child without parent_chunk_id is kept as-is (not deduped)."""
    svc, _, _, _, _ = _make_service()
    sources = [
        {"chunk_id": CHILD_A, "content": "orphan", "parent_content": "",
         "parent_chunk_id": "", "chunk_type": "child", "score": 0.4,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
    ]
    out = svc._return_parent_and_dedup(sources)
    assert len(out) == 1
    # No parent_content → content not replaced
    assert out[0]["content"] == "orphan"


def test_return_parent_and_dedup_parent_chunk_type_kept_as_is():
    """parent chunk_type is not deduped (no parent_chunk_id key)."""
    svc, _, _, _, _ = _make_service()
    sources = [
        {"chunk_id": PARENT_ID, "content": "parent block",
         "parent_content": "", "parent_chunk_id": "",
         "chunk_type": "parent", "score": 0.8,
         "doc_id": DOC_ID, "file_name": "a.pdf", "page": 1},
    ]
    out = svc._return_parent_and_dedup(sources)
    assert len(out) == 1
    assert out[0]["content"] == "parent block"


# ── parent backfill ─────────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_backfill_parent_for_child_with_empty_parent_content():
    """AC: child parent_content empty → lookup parent_chunk_id → backfill."""
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[
        _vec_hit(CHILD_A, 0.9, content="child", parent_content="",
                 parent_chunk_id=PARENT_ID, chunk_type="child"),
    ])
    parent_lookup = _FakeParentLookup(
        parent_by_id={PARENT_ID: {"chunk_id": PARENT_ID, "content": "parent block text"}},
    )
    svc, _, _, lookup, _ = _make_service(
        rag_engine=rag, core_client=core, parent_lookup=parent_lookup,
    )

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="vector",
        vector_store_id=VECTOR_STORE_ID,
    )
    # parent_content was empty → lookup called → backfilled
    assert lookup.lookup_parent_calls
    assert lookup.lookup_parent_calls[0]["parent_chunk_id"] == PARENT_ID
    assert sources[0]["parent_content"] == "parent block text"
    # After dedup, child content is replaced with the backfilled parent_content
    assert sources[0]["content"] == "parent block text"


@pytest.mark.asyncio
async def test_backfill_skipped_when_parent_content_already_present():
    """Common path: parent_content denormalized → backfill is a no-op."""
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[
        _vec_hit(CHILD_A, 0.9, content="child",
                 parent_content="already denormalized",
                 parent_chunk_id=PARENT_ID, chunk_type="child"),
    ])
    parent_lookup = _FakeParentLookup()
    svc, _, _, lookup, _ = _make_service(
        rag_engine=rag, core_client=core, parent_lookup=parent_lookup,
    )

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="vector",
        vector_store_id=VECTOR_STORE_ID,
    )
    # parent_content was present → lookup NOT called
    assert lookup.lookup_parent_calls == []
    assert sources[0]["parent_content"] == "already denormalized"


@pytest.mark.asyncio
async def test_backfill_doc_summary_concatenates_all_parent_blocks():
    """AC: doc_summary → backfill all parent blocks of the document."""
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[
        _vec_hit(SUMMARY_ID, 0.8, content="summary text",
                 parent_content="", parent_chunk_id="",
                 chunk_type="doc_summary", doc_id=DOC_ID),
    ])
    parent_lookup = _FakeParentLookup(
        parents_by_doc={
            DOC_ID: [
                {"chunk_id": PARENT_ID, "content": "parent block 1"},
                {"chunk_id": PARENT_ID_2, "content": "parent block 2"},
            ],
        },
    )
    svc, _, _, lookup, _ = _make_service(
        rag_engine=rag, core_client=core, parent_lookup=parent_lookup,
    )

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="vector",
        vector_store_id=VECTOR_STORE_ID,
    )
    assert lookup.lookup_parents_calls
    assert lookup.lookup_parents_calls[0]["doc_id"] == DOC_ID
    # All parent blocks concatenated with \n
    assert sources[0]["parent_content"] == "parent block 1\nparent block 2"
    # doc_summary content NOT replaced by dedup (summary kept as-is)
    assert sources[0]["content"] == "summary text"


# ── Jaccard equivalence ─────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_hybrid_jaccard_overlap_with_reference_chunk_ids():
    """AC: equivalence — same KB, sources chunk_id set Jaccard > 90%.

    Build a realistic scenario where vector + keyword both return overlapping
    chunk_ids with DISTINCT parents (so dedup doesn't collapse them); the
    fused output should overlap heavily with the union of the two input sets
    (the legacy rag-engine produces the same fused set via
    QueryFusionRetriever).
    """
    rag = _FakeRagEngine()
    # 4 shared chunk_ids + 2 vector-only + 2 keyword-only, each with a
    # distinct parent_chunk_id so _return_parent_and_dedup doesn't collapse.
    shared = [CHILD_A, CHILD_B, CHILD_C, "c7"]
    vec_only = ["c5", "c6"]
    kw_only = ["c8", "c9"]
    all_ids = shared + vec_only + kw_only
    # Distinct parent per chunk.
    parents = {cid: f"44444444-4444-4444-4444-{i:012d}" for i, cid in enumerate(all_ids)}
    vec_hits = [
        _vec_hit(cid, 0.9 - i * 0.05, parent_content=f"p_{cid}",
                 parent_chunk_id=parents[cid])
        for i, cid in enumerate(shared + vec_only)
    ]
    kw_rows = [
        _kw_row(cid, 0.8 - i * 0.05, parent_content=f"p_{cid}",
                parent_chunk_id=parents[cid])
        for i, cid in enumerate(shared + kw_only)
    ]
    core = _FakeCoreClient(vector_results=vec_hits)
    svc, _, _, _, _ = _make_service(
        rag_engine=rag, core_client=core, kw_results=kw_rows,
        parent_lookup=_FakeParentLookup(),
    )

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="查询",
        top_k=10, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )
    # Reference set: chunk_ids that the legacy rag-engine would have surfaced
    # (the union of vector + keyword results, matching what RRF fuses).
    reference = set(shared + vec_only + kw_only)
    fused = {s["chunk_id"] for s in sources}
    # Jaccard similarity
    intersection = reference & fused
    union = reference | fused
    jaccard = len(intersection) / len(union) if union else 1.0
    assert jaccard > 0.9, f"Jaccard {jaccard:.3f} <= 0.9 (fused={fused}, ref={reference})"


@pytest.mark.asyncio
async def test_vector_mode_jaccard_one_to_one_with_vector_hits():
    """vector mode with distinct parents: sources chunk_ids = vector chunk_ids."""
    rag = _FakeRagEngine()
    vec_hits = [
        _vec_hit(CHILD_A, 0.9, parent_chunk_id=PARENT_ID, parent_content="p1"),
        _vec_hit(CHILD_B, 0.8, parent_chunk_id=PARENT_ID_2, parent_content="p2"),
        _vec_hit(CHILD_C, 0.7, parent_chunk_id=PARENT_ID_3, parent_content="p3"),
    ]
    core = _FakeCoreClient(vector_results=vec_hits)
    svc, _, _, _, _ = _make_service(rag_engine=rag, core_client=core)

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="vector",
        vector_store_id=VECTOR_STORE_ID,
    )
    fused = {s["chunk_id"] for s in sources}
    reference = {CHILD_A, CHILD_B, CHILD_C}
    assert fused == reference  # Jaccard = 1.0


@pytest.mark.asyncio
async def test_keyword_mode_jaccard_one_to_one_with_kw_hits():
    """keyword mode with distinct parents: sources = kw chunk_ids."""
    kw_rows = [
        _kw_row(CHILD_A, 0.6, parent_chunk_id=PARENT_ID, parent_content="p1"),
        _kw_row(CHILD_B, 0.5, parent_chunk_id=PARENT_ID_2, parent_content="p2"),
    ]
    svc, _, _, _, _ = _make_service(kw_results=kw_rows)

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="keyword",
        vector_store_id=VECTOR_STORE_ID,
    )
    fused = {s["chunk_id"] for s in sources}
    reference = {CHILD_A, CHILD_B}
    assert fused == reference  # Jaccard = 1.0


# ── error / edge cases ──────────────────────────────────────────────────────


@pytest.mark.asyncio
async def test_retrieve_raises_when_vector_store_id_missing():
    svc, _, _, _, _ = _make_service()
    with pytest.raises(ValueError, match="vector_store_id"):
        await svc.retrieve(
            tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
            retrieval_mode="hybrid", vector_store_id=None,
        )


@pytest.mark.asyncio
async def test_hybrid_top_k_multiplier_for_leg_searches():
    """Verify vector + keyword searches fetch top_k*2 (for fusion headroom)."""
    rag = _FakeRagEngine()
    core = _FakeCoreClient(vector_results=[])
    svc, _, core, _, kw_calls = _make_service(
        rag_engine=rag, core_client=core, kw_results=[],
    )

    await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="hybrid",
        vector_store_id=VECTOR_STORE_ID,
    )
    assert core.search_calls[0]["top_k"] == 10  # 5 * 2
    # keyword_search limit = top_k*2 = 10
    assert kw_calls[0]["limit"] == 10


@pytest.mark.asyncio
async def test_keyword_mode_does_not_require_vector_store_id():
    """keyword mode skips vector leg, so no vector_store_id needed."""
    svc, _, _, _, _ = _make_service(kw_results=[_kw_row(CHILD_A, 0.5)])

    sources, _ = await svc.retrieve(
        tenant_id=TENANT_ID, kb_id=KB_ID, question="q",
        top_k=5, retrieval_mode="keyword",
        vector_store_id=None,
    )
    assert len(sources) == 1
    assert sources[0]["chunk_id"] == CHILD_A
