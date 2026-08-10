"""Pure-logic unit tests for qa_service (US-014 / SPEC §5.1).

These tests do NOT require llama_index, pymilvus, Redis, or any live service.
Heavy/optional deps are stubbed so the module imports cleanly. The tests
validate the pure-logic parts that matter for the AC:

* ``qa_service`` uses ``ContextChatEngine.from_defaults`` with the fusion
  retriever + ``ChatMemoryBuffer(chat_store=RedisChatStore, session_id=...)``
  + ``OpenAILike(model=..., api_base=vllm_url, api_key="...",
  is_chat_model=True, context_window=...)`` (SPEC §5.1 qa_service / PRD
  US-014 AC4).
* ``qa_service.chat()`` returns ``answer + sources + session_id + tokens``
  synchronously (SPEC §5.1 / PRD US-014 AC5).
* A new ``session_id`` is generated when the caller does not supply one.
* No-result path: when retrieval yields no sources, returns a no-result
  answer with empty sources (SPEC §5.4 — do not hallucinate).
* Token extraction probes the LLM response usage object defensively.
"""
from __future__ import annotations

import sys
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
    "llama_index.vector_stores",
    "llama_index.vector_stores.milvus",
    "llama_index.llms",
    "llama_index.llms.openai_like",
    "llama_index.storage",
    "llama_index.storage.chat_store",
    "llama_index.storage.chat_store.redis",
    "minio",
    "minio.error",
    "openai",
    "pymilvus",
    "pymilvus.connections",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

# Capture ContextChatEngine.from_defaults kwargs so we can assert the wiring.
_chat_engine_captured: dict = {}


class _FakeContextChatEngine:
    def __init__(self) -> None:
        self.chat = lambda question: MagicMock(response="answer")

    @classmethod
    def from_defaults(cls, *, retriever, memory, llm, **kwargs):
        _chat_engine_captured["retriever"] = retriever
        _chat_engine_captured["memory"] = memory
        _chat_engine_captured["llm"] = llm
        _chat_engine_captured["kwargs"] = kwargs
        return cls()


sys.modules["llama_index.core.chat_engine"].ContextChatEngine = _FakeContextChatEngine


class _FakeChatMemoryBuffer:
    def __init__(self, *, chat_store, session_id, token_limit=None) -> None:
        self.chat_store = chat_store
        self.session_id = session_id
        self.token_limit = token_limit


sys.modules["llama_index.core.memory"].ChatMemoryBuffer = _FakeChatMemoryBuffer

from app.services import qa_service  # noqa: E402
from app.services.retrieve_service import RetrievedSource  # noqa: E402


# ── constants ───────────────────────────────────────────────────────────────


def test_no_result_answer_is_non_empty():
    assert qa_service.NO_RESULT_ANSWER


# ── _default_llm_factory ─────────────────────────────────────────────────────


def test_default_llm_factory_raises_when_settings_missing(monkeypatch):
    class _EmptySettings:
        vllm_model = ""
        vllm_api_base = ""
        vllm_api_key = ""
        vllm_context_window = 4096

    # Patch the settings reference on the qa_service module (it did
    # ``from app.core.config import settings`` at import time).
    monkeypatch.setattr(qa_service, "settings", _EmptySettings(), raising=False)

    with pytest.raises(RuntimeError, match="vLLM model/api_base not configured"):
        qa_service._default_llm_factory()


def test_default_llm_factory_builds_openai_like(monkeypatch):
    class _Settings:
        vllm_model = "qwen2"
        vllm_api_base = "http://vllm:8000/v1"
        vllm_api_key = "k"
        vllm_context_window = 8192

    monkeypatch.setattr(qa_service, "settings", _Settings(), raising=False)

    captured = {}

    class _FakeOpenAILike:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    monkeypatch.setattr(
        sys.modules["llama_index.llms.openai_like"], "OpenAILike", _FakeOpenAILike
    )

    llm = qa_service._default_llm_factory()
    assert isinstance(llm, _FakeOpenAILike)
    # SPEC §5.1 qa_service: OpenAILike(model=..., api_base=vllm_url, api_key="...",
    # is_chat_model=True, context_window=...)
    assert captured["model"] == "qwen2"
    assert captured["api_base"] == "http://vllm:8000/v1"
    assert captured["api_key"] == "k"
    assert captured["is_chat_model"] is True
    assert captured["context_window"] == 8192


def test_default_llm_factory_uses_empty_key_when_unset(monkeypatch):
    class _Settings:
        vllm_model = "qwen2"
        vllm_api_base = "http://vllm:8000/v1"
        vllm_api_key = ""
        vllm_context_window = 4096

    monkeypatch.setattr(qa_service, "settings", _Settings(), raising=False)

    captured = {}

    class _FakeOpenAILike:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    monkeypatch.setattr(
        sys.modules["llama_index.llms.openai_like"], "OpenAILike", _FakeOpenAILike
    )

    qa_service._default_llm_factory()
    # Empty api_key falls back to "EMPTY" (vLLM/sglang convention).
    assert captured["api_key"] == "EMPTY"


# ── _default_chat_store_factory ──────────────────────────────────────────────


def test_default_chat_store_factory_builds_redis_chat_store(monkeypatch):
    captured = {}

    class _FakeRedisChatStore:
        def __init__(self, *, redis_url) -> None:
            captured["redis_url"] = redis_url

    monkeypatch.setattr(
        sys.modules["llama_index.storage.chat_store.redis"],
        "RedisChatStore",
        _FakeRedisChatStore,
    )

    class _Settings:
        redis_url = "redis://localhost:6379/0"

    monkeypatch.setattr(qa_service, "settings", _Settings(), raising=False)

    qa_service._default_chat_store_factory()
    assert captured["redis_url"] == "redis://localhost:6379/0"


# ── _extract_tokens ─────────────────────────────────────────────────────────


def test_extract_tokens_from_dict_usage():
    raw = MagicMock()
    raw.raw = {"usage": {"prompt_tokens": 10, "completion_tokens": 20}}
    response = MagicMock()
    response.raw = raw
    assert qa_service._extract_tokens(response) == (10, 20)


def test_extract_tokens_from_raw_attr_usage():
    class _Usage:
        prompt_tokens = 5
        completion_tokens = 7

    class _Raw:
        usage = _Usage()

    class _R:
        raw = _Raw()

    assert qa_service._extract_tokens(_R()) == (5, 7)


def test_extract_tokens_returns_zero_when_no_raw():
    class _R:
        raw = None
    assert qa_service._extract_tokens(_R()) == (0, 0)


def test_extract_tokens_returns_zero_when_no_usage():
    class _Raw:
        raw = {}

    class _R:
        raw = _Raw()

    assert qa_service._extract_tokens(_R()) == (0, 0)


# ── _extract_answer ─────────────────────────────────────────────────────────


def test_extract_answer_from_response_attr():
    class _R:
        response = "hello"
    assert qa_service._extract_answer(_R()) == "hello"


def test_extract_answer_from_answer_attr():
    class _R:
        answer = "via answer"
    assert qa_service._extract_answer(_R()) == "via answer"


def test_extract_answer_from_message_content():
    class _M:
        content = "msg content"
    class _R:
        message = _M()
    assert qa_service._extract_answer(_R()) == "msg content"


def test_extract_answer_falls_back_to_str():
    class _R:
        pass
    # No response/answer/message → str(response)
    assert qa_service._extract_answer(_R()) != ""


# ── _build_engine wiring (SPEC §5.1 qa_service AC4) ─────────────────────────


def test_build_engine_uses_context_chat_engine_from_defaults(monkeypatch):
    """The engine is built via ContextChatEngine.from_defaults with the fusion
    retriever, Redis-backed ChatMemoryBuffer, and OpenAILike LLM.
    """
    _chat_engine_captured.clear()
    fake_llm = MagicMock(name="llm")
    fake_store = MagicMock(name="store")
    fake_retriever = MagicMock(name="retriever")

    svc = qa_service.QAService(
        llm=fake_llm,
        chat_store=fake_store,
    )
    svc._build_engine(fusion_retriever=fake_retriever, session_id="s1")

    assert isinstance(
        _chat_engine_captured["memory"], _FakeChatMemoryBuffer
    )
    assert _chat_engine_captured["memory"].chat_store is fake_store
    assert _chat_engine_captured["memory"].session_id == "s1"
    assert _chat_engine_captured["retriever"] is fake_retriever
    assert _chat_engine_captured["llm"] is fake_llm


def test_build_engine_lazily_builds_llm(monkeypatch):
    _chat_engine_captured.clear()
    fake_llm = MagicMock(name="factory_llm")
    svc = qa_service.QAService(llm_factory=lambda: fake_llm, chat_store=MagicMock())
    svc._build_engine(fusion_retriever=MagicMock(), session_id="s1")
    assert _chat_engine_captured["llm"] is fake_llm


def test_build_engine_lazily_builds_chat_store(monkeypatch):
    _chat_engine_captured.clear()
    fake_store = MagicMock(name="factory_store")
    svc = qa_service.QAService(
        llm=MagicMock(), chat_store_factory=lambda: fake_store
    )
    svc._build_engine(fusion_retriever=MagicMock(), session_id="s1")
    assert _chat_engine_captured["memory"].chat_store is fake_store


def test_build_engine_caches_llm_and_chat_store():
    """The factory-built LLM + chat store are cached on self so subsequent
    calls reuse the same client (avoids rebuilding OpenAILike/RedisChatStore
    per query).
    """
    llm_calls = 0
    store_calls = 0

    def _llm_factory():
        nonlocal llm_calls
        llm_calls += 1
        return MagicMock()

    def _store_factory():
        nonlocal store_calls
        store_calls += 1
        return MagicMock()

    svc = qa_service.QAService(
        llm_factory=_llm_factory, chat_store_factory=_store_factory
    )
    svc._build_engine(fusion_retriever=MagicMock(), session_id="s1")
    svc._build_engine(fusion_retriever=MagicMock(), session_id="s2")
    assert llm_calls == 1
    assert store_calls == 1


# ── chat() return shape (SPEC §5.1 / PRD US-014 AC5) ──────────────────────────


def _make_retrieve_service_stub(*, pre_nodes=None):
    """Build a RetrieveService stub whose build_fusion_retriever returns a
    mock fusion retriever with a .retrieve() method for the pre-check.
    chat() calls build_fusion_retriever → fusion.retrieve() (pre-check) then
    engine.chat(); sources come from response.source_nodes.
    """
    svc = MagicMock()
    svc._llm = None
    svc._parent_lookup = None
    svc.has_llm = False
    fusion = MagicMock(name="fusion")
    # Pre-check: fusion_retriever.retrieve(question) returns a list of nodes.
    # Default to a single high-score node so the threshold passes.
    if pre_nodes is None:
        pre_nodes = [_make_source_node(score=0.9)]
    fusion.retrieve.return_value = pre_nodes
    svc.build_fusion_retriever.return_value = fusion
    # Hybrid chat() calls vector_similarity_map to produce readable 0~1
    # scores. Default to reflecting each pre-check node's own score so the
    # threshold gate and score expectations stay consistent.
    svc.vector_similarity_map.return_value = {
        getattr(n.node, "id_", "c1"): float(getattr(n, "score", 0.0) or 0.0)
        for n in pre_nodes
    }
    return svc


def _make_source_node(*, chunk_id="c1", doc_id="d1", content="child",
                      chunk_type="child", parent_content="parent", score=0.9):
    """Build a fake NodeWithScore for response.source_nodes."""
    from tests.test_retrieve_service import _FakeTextNode, _FakeNodeWithScore
    node = _FakeTextNode(
        id_=chunk_id,
        text=content,
        metadata={
            "doc_id": doc_id,
            "file_name": "f.pdf",
            "page_number": 1,
            "chunk_type": chunk_type,
            "parent_content": parent_content,
        },
    )
    return _FakeNodeWithScore(node=node, score=score)


def test_chat_returns_answer_sources_session_id_tokens(monkeypatch):
    """chat() synchronously returns answer + sources + session_id + tokens
    (SPEC §5.1 / PRD US-014 AC5).
    """
    _chat_engine_captured.clear()
    retrieve_svc = _make_retrieve_service_stub()

    # Build a fake engine whose chat returns a response with usage + sources.
    class _Usage:
        prompt_tokens = 12
        completion_tokens = 34

    class _Raw:
        usage = _Usage()

    class _Response:
        response = "the answer"
        raw = _Raw()
        source_nodes = [_make_source_node()]

    fake_engine = MagicMock()
    fake_engine.chat = lambda question: _Response()

    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    svc._build_engine = lambda *, fusion_retriever, session_id: fake_engine

    result = svc.chat(kb_id="kb1", question="hello", session_id="s1")

    assert result.answer == "the answer"
    assert len(result.sources) == 1
    assert result.sources[0].chunk_id == "c1"
    assert result.session_id == "s1"
    assert result.input_tokens == 12
    assert result.output_tokens == 34


def test_chat_generates_new_session_id_when_not_supplied(monkeypatch):
    retrieve_svc = _make_retrieve_service_stub()
    fake_engine = MagicMock()
    fake_engine.chat = lambda question: MagicMock(
        response="a", source_nodes=[_make_source_node()]
    )

    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    svc._build_engine = lambda *, fusion_retriever, session_id: fake_engine

    result = svc.chat(kb_id="kb1", question="q")
    assert result.session_id  # generated UUID, non-empty


def test_chat_no_result_returns_no_result_answer(monkeypatch):
    """When retrieval yields no sources, returns NO_RESULT_ANSWER + empty
    sources (SPEC §5.4 — do not hallucinate).
    """
    retrieve_svc = _make_retrieve_service_stub(pre_nodes=[])

    class _Response:
        response = ""
        raw = None
        source_nodes = []  # no sources

    fake_engine = MagicMock()
    fake_engine.chat = lambda question: _Response()

    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    svc._build_engine = lambda *, fusion_retriever, session_id: fake_engine

    result = svc.chat(kb_id="kb1", question="q")

    assert result.answer == qa_service.NO_RESULT_ANSWER
    assert result.sources == []


def test_chat_below_score_threshold_returns_no_result(monkeypatch):
    """SPEC §5.4: when max source score is below score_threshold, return
    NO_RESULT_ANSWER + empty sources (do not hallucinate). The LLM is NOT
    called — the pre-check skips it.
    """
    # Pre-check returns a low-score node → threshold fails → LLM skipped.
    retrieve_svc = _make_retrieve_service_stub(
        pre_nodes=[_make_source_node(score=0.1)]
    )

    fake_engine = MagicMock()
    fake_engine.chat.return_value = MagicMock(response="should not reach")

    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    svc._build_engine = lambda *, fusion_retriever, session_id: fake_engine

    result = svc.chat(kb_id="kb1", question="q", score_threshold=0.3)

    assert result.answer == qa_service.NO_RESULT_ANSWER
    assert result.sources == []
    assert result.input_tokens == 0  # LLM was not called
    assert result.output_tokens == 0
    # Verify the LLM was NOT called (engine.chat never invoked).
    fake_engine.chat.assert_not_called()


def test_chat_above_score_threshold_returns_answer(monkeypatch):
    """When max score is above score_threshold, return the real answer."""
    retrieve_svc = _make_retrieve_service_stub(
        pre_nodes=[_make_source_node(score=0.9)]
    )

    class _Response:
        response = "real answer"
        raw = None
        source_nodes = [_make_source_node(score=0.9)]

    fake_engine = MagicMock()
    fake_engine.chat = lambda question: _Response()

    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    svc._build_engine = lambda *, fusion_retriever, session_id: fake_engine

    result = svc.chat(kb_id="kb1", question="q", score_threshold=0.3)

    assert result.answer == "real answer"
    assert len(result.sources) == 1


def test_chat_passes_kb_id_top_k_to_build_fusion_retriever(monkeypatch):
    retrieve_svc = _make_retrieve_service_stub()
    fake_engine = MagicMock()
    fake_engine.chat = lambda question: MagicMock(
        response="a", source_nodes=[_make_source_node()]
    )

    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    svc._build_engine = lambda *, fusion_retriever, session_id: fake_engine

    svc.chat(
        kb_id="kb1", question="hello", session_id="s1",
        top_k=8, score_threshold=0.5,
    )

    retrieve_svc.build_fusion_retriever.assert_called_once_with(
        kb_id="kb1", top_k=8, dim=None, tenant_id=""
    )


def test_chat_passes_fusion_retriever_to_engine(monkeypatch):
    """The fusion retriever from build_fusion_retriever is wired into the chat engine."""
    _chat_engine_captured.clear()
    retrieve_svc = _make_retrieve_service_stub()
    svc = qa_service.QAService(
        retrieve_service=retrieve_svc,
        llm=MagicMock(),
        chat_store=MagicMock(),
    )
    # Use the real _build_engine so we capture the fusion retriever.
    svc._build_engine = qa_service.QAService._build_engine.__get__(svc, qa_service.QAService)

    svc.chat(kb_id="kb1", question="q", session_id="s1")

    assert _chat_engine_captured["retriever"] is retrieve_svc.build_fusion_retriever.return_value


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
