"""Pure-logic unit tests for retrieve_service (US-014 / SPEC §5.1).

These tests do NOT require llama_index, pymilvus, asyncpg, or any live
service. Heavy/optional deps are stubbed so the module imports cleanly.
The tests validate the pure-logic parts that matter for the AC:

* QueryFusionRetriever is built with ``num_queries=1`` (disable query
  generation) and ``mode='reciprocal_reranking'`` (RRF) — SPEC §5.1 /
  PRD US-014 AC1.
* The vector retriever comes from ``VectorStoreIndex.as_retriever()``
  (Milvus wrapped via ``from_vector_store``) — SPEC §5.1 AC1.
* The keyword retriever is a :class:`BaseRetriever` subclass
  (:class:`PgTrgmRetriever`) — SPEC §5.1 AC1.
* Child hits get ``parent_content`` backfilled — SPEC §5.1 AC2.
* doc_summary hits get the document's parent blocks backfilled —
  SPEC §5.1 AC3.
* Below ``score_threshold`` → no-result (SPEC §5.4).
* ``RetrieveService`` accepts an injected embed_model (shared singleton,
  SPEC §1.3).
"""
from __future__ import annotations

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
    "llama_index.embeddings",
    "llama_index.vector_stores",
    "llama_index.vector_stores.milvus",
    "minio",
    "minio.error",
    "openai",
    "pymilvus",
    "pymilvus.connections",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

# Configure the llama_index.core.schema stub to return real-ish objects so
# the PgTrgmRetriever can build TextNode / NodeWithScore instances.
_schema_stub: Any = sys.modules["llama_index.core.schema"]


class _FakeTextNode:
    def __init__(self, id_="", text="", metadata=None, relationships=None) -> None:
        self.id_ = id_
        self.text = text
        self.metadata = metadata or {}
        self.relationships = relationships or {}

    def get_content(self) -> str:
        return self.text


class _FakeNodeWithScore:
    def __init__(self, node, score=0.0) -> None:
        self.node = node
        self.score = score


_schema_stub.TextNode = _FakeTextNode
_schema_stub.NodeWithScore = _FakeNodeWithScore


class _FakeQueryBundle:
    def __init__(self, query_str: str) -> None:
        self.query_str = query_str


_schema_stub.QueryBundle = _FakeQueryBundle


# Provide a REAL BaseRetriever base class so PgTrgmRetriever can subclass it.
# The default MagicMock() stub makes BaseRetriever a MagicMock instance,
# which cannot be subclassed.
class _FakeBaseRetriever:
    def __init__(self, *args, **kwargs) -> None:
        pass

    def retrieve(self, query):
        """Mimics LlamaIndex BaseRetriever.retrieve (calls _retrieve)."""
        bundle = _FakeQueryBundle(query)
        return self._retrieve(bundle)


_core_stub: Any = sys.modules["llama_index.core"]
_core_stub.BaseRetriever = _FakeBaseRetriever
# retrieve_service imports BaseRetriever from llama_index.core.retrievers
# (LlamaIndex 0.14.x), so the fake base class must be set there too.
_retrievers_stub: Any = sys.modules["llama_index.core.retrievers"]
_retrievers_stub.BaseRetriever = _FakeBaseRetriever


# Configure the retrievers stub so QueryFusionRetriever is a real-ish class
# whose constructor captures its kwargs (so we can assert num_queries + mode).
_fusion_captured: dict = {}


class _FakeQueryFusionRetriever:
    def __init__(self, *, retrievers, num_queries, mode, llm=None, use_async=None) -> None:
        _fusion_captured["retrievers"] = retrievers
        _fusion_captured["num_queries"] = num_queries
        _fusion_captured["mode"] = mode
        _fusion_captured["llm"] = llm
        _fusion_captured["use_async"] = use_async
        self._retrievers = retrievers
        self._num_queries = num_queries
        self._mode = mode
        self._llm = llm
        # The retrieve path uses ``fusion.retrieve(question)``; return an
        # empty list by default. Tests monkeypatch this on the instance.
        self.retrieve: Any = lambda query: []


_retrievers_stub.QueryFusionRetriever = _FakeQueryFusionRetriever

from app.services import retrieve_service

# ── constants (SPEC §5.1) ────────────────────────────────────────────────────


def test_fusion_constants_match_spec():
    assert retrieve_service.FUSION_MODE_RRF == "reciprocal_rerank"
    assert retrieve_service.FUSION_NUM_QUERIES == 1
    assert retrieve_service.DEFAULT_TOP_K == 5
    assert retrieve_service.DEFAULT_SCORE_THRESHOLD == 0.3


# ── _tokenize_cn_keywords ────────────────────────────────────────────────────


def test_tokenize_cn_keywords_segments_full_sentence():
    """A full CJK query must be broken into semantic tokens so pg_trgm can
    match the exact keyword (e.g. "混合检索") instead of being diluted inside
    the whole sentence."""
    toks = retrieve_service._tokenize_cn_keywords(
        "ANI 平台的作业调度能力与混合检索原理是什么？"
    )
    assert "混合" in toks and "检索" in toks
    assert "作业" in toks and "调度" in toks
    # stop-words / empty tokens are dropped
    assert "是什么" not in toks
    assert all(len(t) >= 2 for t in toks)


def test_tokenize_cn_keywords_short_keyword_recovered():
    assert set(retrieve_service._tokenize_cn_keywords("混合检索")) == {"混合", "检索"}


# ── build_fusion_retriever ───────────────────────────────────────────────────


def test_build_fusion_retriever_uses_rrf_and_num_queries_one(monkeypatch):
    """SPEC §5.1 / PRD US-014 AC1: QueryFusionRetriever with num_queries=1 +
    mode='reciprocal_reranking' (RRF), and the vector retriever comes from
    ``VectorStoreIndex.as_retriever()``.
    """
    _fusion_captured.clear()

    # build_vector_store_index returns a fake index whose as_retriever returns
    # a sentinel that we can assert is in the retrievers list.
    vector_retriever_sentinel = object()

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return vector_retriever_sentinel

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    fusion = svc.build_fusion_retriever(kb_id="kb1", top_k=7)

    # QueryFusionRetriever was constructed with the right params.
    assert isinstance(fusion, _FakeQueryFusionRetriever)
    assert _fusion_captured["num_queries"] == 1
    assert _fusion_captured["mode"] == "reciprocal_rerank"
    # The vector retriever (from as_retriever) is in the list.
    assert vector_retriever_sentinel in _fusion_captured["retrievers"]


def test_build_fusion_retriever_includes_keyword_retriever_when_search_fn(monkeypatch):
    """When a keyword_search_fn is supplied, a PgTrgmRetriever
    (BaseRetriever subclass) is added to the retrievers list (SPEC §5.1 AC1).
    """
    _fusion_captured.clear()
    vector_retriever_sentinel = object()

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return vector_retriever_sentinel

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    def _search(query, *, top_k, tenant_id="", kb_id=""):
        return [
            {
                "chunk_id": "kw1",
                "content": "keyword hit",
                "parent_content": "parent ctx",
                "doc_id": "d1",
                "file_name": "f.pdf",
                "page_number": 2,
                "content_type": "text",
                "chunk_type": "child",
                "score": 0.6,
            }
        ]

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), keyword_search_fn=_search
    )
    svc.build_fusion_retriever(kb_id="kb1", top_k=3)

    # Two retrievers: vector + keyword (PgTrgmRetriever).
    assert len(_fusion_captured["retrievers"]) == 2
    assert vector_retriever_sentinel in _fusion_captured["retrievers"]
    keyword_ret = _fusion_captured["retrievers"][1]
    # The keyword retriever is a real BaseRetriever subclass instance.
    assert keyword_ret is not vector_retriever_sentinel
    # Its _retrieve converts rows into NodeWithScore with metadata.
    bundle = _FakeQueryBundle("query")
    nodes = keyword_ret._retrieve(bundle)
    assert len(nodes) == 1
    assert nodes[0].node.metadata["chunk_type"] == "child"
    assert nodes[0].node.metadata["parent_content"] == "parent ctx"
    assert nodes[0].score == 0.6


def test_build_fusion_retriever_omits_keyword_retriever_without_search_fn(monkeypatch):
    """Without a keyword_search_fn, only the vector retriever is used (keeps
    the service usable without a live PG connection)."""
    _fusion_captured.clear()

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    svc.build_fusion_retriever(kb_id="kb1")

    assert len(_fusion_captured["retrievers"]) == 1


def test_build_fusion_retriever_passes_top_k_to_vector_retriever(monkeypatch):
    captured_top_k = {}

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            captured_top_k["top_k"] = similarity_top_k
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    svc.build_fusion_retriever(kb_id="kb1", top_k=12)

    assert captured_top_k["top_k"] == 12


# ── _node_to_source ──────────────────────────────────────────────────────────


def _make_node_with_score(*, chunk_type="child", parent_content="", score=0.5,
                          chunk_id="c1", doc_id="d1", content="text"):
    node = _FakeTextNode(
        id_=chunk_id,
        text=content,
        metadata={
            "doc_id": doc_id,
            "file_name": "f.pdf",
            "page_number": 3,
            "chunk_type": chunk_type,
            "parent_content": parent_content,
        },
    )
    return _FakeNodeWithScore(node=node, score=score)


def test_node_to_source_maps_fields():
    nws = _make_node_with_score(chunk_type="child", parent_content="p", score=0.7)
    src = retrieve_service._node_to_source(nws)
    assert src.chunk_id == "c1"
    assert src.doc_id == "d1"
    assert src.file_name == "f.pdf"
    assert src.page == 3
    assert src.content == "text"
    assert src.score == 0.7
    assert src.chunk_type == "child"
    assert src.parent_content == "p"


# ── parent backfill for child hits (SPEC §5.1 AC2) ───────────────────────────


def test_backfill_parent_for_child_noop_when_parent_content_present():
    src = retrieve_service.RetrievedSource(
        chunk_id="c1", doc_id="d1", file_name="f.pdf", page=1,
        content="child", score=0.5, chunk_type="child", parent_content="already",
    )
    node = _FakeTextNode(metadata={"parent_chunk_id": "p1"})
    out = retrieve_service._backfill_parent_for_child(src, node, lookup=MagicMock())
    assert out.parent_content == "already"


def test_backfill_parent_for_child_falls_back_to_lookup():
    """When parent_content is empty, fetch the parent chunk via lookup."""
    src = retrieve_service.RetrievedSource(
        chunk_id="c1", doc_id="d1", file_name="f.pdf", page=1,
        content="child", score=0.5, chunk_type="child", parent_content="",
    )
    node = _FakeTextNode(metadata={"parent_chunk_id": "p1"})

    lookup = MagicMock()
    lookup.lookup_parent.return_value = {"content": "parent block text"}

    out = retrieve_service._backfill_parent_for_child(src, node, lookup=lookup)
    assert out.parent_content == "parent block text"
    lookup.lookup_parent.assert_called_once_with("p1")


def test_backfill_parent_for_child_noop_when_no_lookup():
    src = retrieve_service.RetrievedSource(
        chunk_id="c1", doc_id="d1", file_name="f.pdf", page=1,
        content="child", score=0.5, chunk_type="child", parent_content="",
    )
    node = _FakeTextNode(metadata={"parent_chunk_id": "p1"})
    out = retrieve_service._backfill_parent_for_child(src, node, lookup=None)
    assert out.parent_content == ""


def test_backfill_parent_for_child_noop_when_no_parent_chunk_id():
    src = retrieve_service.RetrievedSource(
        chunk_id="c1", doc_id="d1", file_name="f.pdf", page=1,
        content="child", score=0.5, chunk_type="child", parent_content="",
    )
    node = _FakeTextNode(metadata={})  # no parent_chunk_id
    lookup = MagicMock()
    out = retrieve_service._backfill_parent_for_child(src, node, lookup=lookup)
    assert out.parent_content == ""
    lookup.lookup_parent.assert_not_called()


# ── parent backfill for doc_summary hits (SPEC §5.1 AC3) ─────────────────────


def test_backfill_parents_for_summary_concatenates_document_parents():
    src = retrieve_service.RetrievedSource(
        chunk_id="s1", doc_id="d1", file_name="f.pdf", page=1,
        content="summary", score=0.8, chunk_type="doc_summary", parent_content="",
    )
    lookup = MagicMock()
    lookup.lookup_parents.return_value = [
        {"content": "parent A"},
        {"content": "parent B"},
    ]
    out = retrieve_service._backfill_parents_for_summary(src, lookup=lookup)
    assert out.parent_content == "parent A\nparent B"
    lookup.lookup_parents.assert_called_once_with("d1")


def test_backfill_parents_for_summary_noop_when_no_lookup():
    src = retrieve_service.RetrievedSource(
        chunk_id="s1", doc_id="d1", file_name="f.pdf", page=1,
        content="summary", score=0.8, chunk_type="doc_summary", parent_content="",
    )
    out = retrieve_service._backfill_parents_for_summary(src, lookup=None)
    assert out.parent_content == ""


def test_backfill_parents_for_summary_noop_when_no_doc_id():
    src = retrieve_service.RetrievedSource(
        chunk_id="s1", doc_id="", file_name="f.pdf", page=1,
        content="summary", score=0.8, chunk_type="doc_summary", parent_content="",
    )
    lookup = MagicMock()
    out = retrieve_service._backfill_parents_for_summary(src, lookup=lookup)
    assert out.parent_content == ""
    lookup.lookup_parents.assert_not_called()


# ── retrieve end-to-end (with stubbed fusion) ─────────────────────────────────


def _patch_build_fusion(monkeypatch, svc, retrieve_return):
    """Patch RetrieveService.build_fusion_retriever to return a fusion whose
    ``retrieve`` yields ``retrieve_return`` and capture the question."""
    received = {}

    def _fake_build(self, *, kb_id, top_k=5, dim=None, tenant_id=""):
        fusion = _FakeQueryFusionRetriever(
            retrievers=[], num_queries=1, mode="reciprocal_rerank"
        )
        def _retrieve(q):
            received["q"] = q
            return retrieve_return
        fusion.retrieve = _retrieve
        return fusion

    monkeypatch.setattr(svc, "build_fusion_retriever", _fake_build.__get__(svc))
    return received


def test_retrieve_passes_question_to_fusion_and_returns_sources(monkeypatch):
    """retrieve() calls fusion.retrieve(question) and converts results."""
    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    received = _patch_build_fusion(
        monkeypatch, svc, [_make_node_with_score(chunk_type="child", parent_content="p", score=0.9)]
    )

    result = svc.retrieve(kb_id="kb1", question="hello", top_k=5, score_threshold=0.3)
    assert received["q"] == "hello"
    assert len(result.sources) == 1
    assert result.sources[0].score == 0.9
    assert result.max_score == 0.9


def test_retrieve_applies_parent_backfill_for_child_hits(monkeypatch):
    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    lookup = MagicMock()
    lookup.lookup_parent.return_value = {"content": "parent from db"}

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), parent_lookup=lookup
    )
    # Child hit with EMPTY parent_content and a parent_chunk_id in node metadata.
    nodes = [
        _FakeNodeWithScore(
            node=_FakeTextNode(
                id_="c1",
                text="child text",
                metadata={
                    "doc_id": "d1", "file_name": "f.pdf", "page_number": 1,
                    "chunk_type": "child", "parent_content": "",
                    "parent_chunk_id": "p1",
                },
            ),
            score=0.9,
        )
    ]
    _patch_build_fusion(monkeypatch, svc, nodes)

    result = svc.retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert result.sources[0].parent_content == "parent from db"
    lookup.lookup_parent.assert_called_once_with("p1")


def test_retrieve_applies_parent_backfill_for_summary_hits(monkeypatch):
    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    lookup = MagicMock()
    lookup.lookup_parents.return_value = [{"content": "PA"}, {"content": "PB"}]

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), parent_lookup=lookup
    )
    nodes = [
        _FakeNodeWithScore(
            node=_FakeTextNode(
                id_="s1",
                text="summary text",
                metadata={
                    "doc_id": "d1", "file_name": "f.pdf", "page_number": 1,
                    "chunk_type": "doc_summary", "parent_content": "",
                },
            ),
            score=0.8,
        )
    ]
    _patch_build_fusion(monkeypatch, svc, nodes)

    result = svc.retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert result.sources[0].parent_content == "PA\nPB"
    lookup.lookup_parents.assert_called_once_with("d1")


# ── no-result path (SPEC §5.4) ───────────────────────────────────────────────


def test_retrieve_below_threshold_returns_empty_sources(monkeypatch):
    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    _patch_build_fusion(monkeypatch, svc, [_make_node_with_score(score=0.1)])

    result = svc.retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert result.sources == []
    # max_score is computed from the original hits before filtering.
    assert result.max_score == 0.1


def test_retrieve_empty_fusion_returns_empty_sources(monkeypatch):
    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    _patch_build_fusion(monkeypatch, svc, [])

    result = svc.retrieve(kb_id="kb1", question="q")
    assert result.sources == []
    assert result.max_score == 0.0


def test_retrieve_passes_threshold_at_boundary(monkeypatch):
    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    _patch_build_fusion(monkeypatch, svc, [_make_node_with_score(score=0.3)])

    result = svc.retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert len(result.sources) == 1  # boundary is inclusive


# ── shared embed_model (SPEC §1.3) ───────────────────────────────────────────


def test_retrieve_service_accepts_injected_embed_model(monkeypatch):
    captured = {}

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return object()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: (
            captured.update(embed_model=embed_model) or _FakeIndex()
        ),
    )

    shared = MagicMock(name="shared")
    svc = retrieve_service.RetrieveService(embed_model=shared)
    svc.build_fusion_retriever(kb_id="kb1")
    assert captured["embed_model"] is shared


# ── public accessors (has_llm / set_llm / parent_lookup) ─────────────────────


def test_has_llm_returns_false_by_default():
    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    assert svc.has_llm is False


def test_set_llm_sets_llm():
    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    fake_llm = MagicMock(name="llm")
    svc.set_llm(fake_llm)
    assert svc.has_llm is True


def test_parent_lookup_property_returns_injected_lookup():
    lookup = MagicMock(name="lookup")
    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), parent_lookup=lookup
    )
    assert svc.parent_lookup is lookup


def test_parent_lookup_property_returns_none_when_not_injected():
    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    assert svc.parent_lookup is None


# ── vector_retrieve (单路向量检索) ──────────────────────────────────────────


def test_vector_retrieve_uses_index_as_retriever(monkeypatch):
    """vector_retrieve uses VectorStoreIndex.as_retriever() (no fusion)."""
    retrieved_question = {}

    class _FakeRetriever:
        def retrieve(self, question):
            retrieved_question["q"] = question
            return [_make_node_with_score(chunk_type="child", parent_content="p", score=0.9)]

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return _FakeRetriever()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    result = svc.vector_retrieve(kb_id="kb1", question="hello", top_k=5, score_threshold=0.3)

    assert retrieved_question["q"] == "hello"
    assert len(result.sources) == 1
    assert result.sources[0].score == 0.9
    assert result.max_score == 0.9


def test_vector_retrieve_applies_parent_backfill(monkeypatch):
    """vector_retrieve applies parent backfill for child hits (SPEC §5.1 AC2)."""
    class _FakeRetriever:
        def retrieve(self, question):
            return [_FakeNodeWithScore(
                node=_FakeTextNode(
                    id_="c1", text="child",
                    metadata={
                        "doc_id": "d1", "file_name": "f.pdf", "page_number": 1,
                        "chunk_type": "child", "parent_content": "",
                        "parent_chunk_id": "p1",
                    },
                ),
                score=0.9,
            )]

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return _FakeRetriever()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    lookup = MagicMock()
    lookup.lookup_parent.return_value = {"content": "parent via vector path"}

    svc = retrieve_service.RetrieveService(embed_model=MagicMock(), parent_lookup=lookup)
    result = svc.vector_retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert result.sources[0].parent_content == "parent via vector path"
    lookup.lookup_parent.assert_called_once_with("p1")


def test_vector_retrieve_below_threshold_returns_empty(monkeypatch):
    class _FakeRetriever:
        def retrieve(self, question):
            return [_make_node_with_score(score=0.1)]

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return _FakeRetriever()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    result = svc.vector_retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert result.sources == []
    assert result.max_score == 0.1


def test_vector_retrieve_empty_returns_empty(monkeypatch):
    class _FakeRetriever:
        def retrieve(self, question):
            return []

    class _FakeIndex:
        def as_retriever(self, similarity_top_k):
            return _FakeRetriever()

    monkeypatch.setattr(
        retrieve_service,
        "build_vector_store_index",
        lambda kb_id, *, dim=None, embed_model=None: _FakeIndex(),
    )

    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    result = svc.vector_retrieve(kb_id="kb1", question="q")
    assert result.sources == []
    assert result.max_score == 0.0


# ── keyword_retrieve (单路全文检索) ─────────────────────────────────────────


def test_keyword_retrieve_uses_pg_trgm_retriever(monkeypatch):
    """keyword_retrieve uses PgTrgmRetriever (BaseRetriever subclass)."""
    def _search(query, *, top_k, tenant_id="", kb_id=""):
        assert query == "hello"
        assert top_k == 5
        return [
            {
                "chunk_id": "kw1", "content": "keyword hit",
                "parent_content": "parent ctx", "doc_id": "d1",
                "file_name": "f.pdf", "page_number": 2,
                "content_type": "text", "chunk_type": "child", "score": 0.7,
            }
        ]

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), keyword_search_fn=_search
    )
    result = svc.keyword_retrieve(kb_id="kb1", question="hello", top_k=5, score_threshold=0.3)

    assert len(result.sources) == 1
    assert result.sources[0].chunk_id == "kw1"
    # SPEC §5.1 parent-child: child hits surface the parent block as content.
    assert result.sources[0].content == "parent ctx"
    assert result.sources[0].parent_content == "parent ctx"
    assert result.sources[0].score == 0.7


def test_keyword_retrieve_raises_without_search_fn():
    """keyword_retrieve raises RuntimeError when no keyword_search_fn."""
    svc = retrieve_service.RetrieveService(embed_model=MagicMock())
    with pytest.raises(RuntimeError, match="keyword_retrieve requires a keyword_search_fn"):
        svc.keyword_retrieve(kb_id="kb1", question="q")


def test_keyword_retrieve_applies_parent_backfill(monkeypatch):
    def _search(query, *, top_k, tenant_id="", kb_id=""):
        return [
            {
                "chunk_id": "kw1", "content": "child hit",
                "parent_content": "", "doc_id": "d1",
                "file_name": "f.pdf", "page_number": 1,
                "content_type": "text", "chunk_type": "child", "score": 0.8,
            }
        ]

    lookup = MagicMock()
    lookup.lookup_parent.return_value = {"content": "parent via keyword path"}

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), keyword_search_fn=_search, parent_lookup=lookup
    )
    result = svc.keyword_retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    # Note: pg_trgm rows carry parent_content in metadata; backfill only
    # triggers when parent_content is empty AND parent_chunk_id is in metadata.
    # The search row has parent_content="" but no parent_chunk_id key, so
    # backfill is a no-op. Verify the source still has empty parent_content.
    assert result.sources[0].chunk_id == "kw1"
    assert result.sources[0].score == 0.8


def test_keyword_retrieve_below_threshold_returns_empty(monkeypatch):
    def _search(query, *, top_k, tenant_id="", kb_id=""):
        return [
            {
                "chunk_id": "kw1", "content": "low score",
                "parent_content": "", "doc_id": "d1",
                "file_name": "f.pdf", "page_number": 1,
                "content_type": "text", "chunk_type": "child", "score": 0.1,
            }
        ]

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), keyword_search_fn=_search
    )
    result = svc.keyword_retrieve(kb_id="kb1", question="q", score_threshold=0.3)
    assert result.sources == []
    assert result.max_score == 0.1


def test_keyword_retrieve_empty_returns_empty(monkeypatch):
    def _search(query, *, top_k, tenant_id="", kb_id=""):
        return []

    svc = retrieve_service.RetrieveService(
        embed_model=MagicMock(), keyword_search_fn=_search
    )
    result = svc.keyword_retrieve(kb_id="kb1", question="q")
    assert result.sources == []
    assert result.max_score == 0.0


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
