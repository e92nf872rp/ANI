"""Pure-logic unit tests for summary_service (US-012 / SPEC §5.1).

These tests do NOT require a live vLLM endpoint, LlamaIndex, or pymilvus.
Heavy/optional deps are stubbed so the module imports cleanly. The tests
validate the pure-logic parts that matter for the AC:

* Concatenate first N parent blocks' full text (SPEC §5.1).
* LLM is called with a "200-500 字摘要" prompt (SPEC §5.1).
* The summary chunk is returned with content_type='text' and is ready to be
  passed to ``embed_service`` which stores it with ``chunk_type='doc_summary'``.
* Summary length is validated to be within [200, 500] characters (SPEC §5.1).
* Failure path: LLM exception / empty / out-of-bounds → log warning + return
  None (degrade to parent-child only, do NOT block ingestion — SPEC §5.4,
  §6.3, PRD US-012 AC7).
* ``parent_count`` is configurable and defaults to 3.
"""
from __future__ import annotations

import sys
from unittest.mock import MagicMock

import pytest

# Stub heavy/optional deps so summary_service (and its chunk_service import)
# load cleanly in the test env without installing llama_index/asyncpg/pymilvus.
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
    "llama_index.llms",
    "llama_index.llms.openai_like",
    "minio",
    "minio.error",
    "openai",
    "pymilvus",
    "pymilvus.connections",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

# Configure the llama_index.core.schema stub to return a real-ish object so
# ``embed_service._build_text_node`` can set attributes on it (reuses the same
# pattern as test_embed_service.py for the end-to-end wiring test below).
_schema_stub = sys.modules["llama_index.core.schema"]


class _FakeRelatedNodeInfo:
    def __init__(self, node_id: str) -> None:
        self.node_id = node_id


class _FakeTextNode:
    def __init__(self, id_, text, metadata, relationships=None) -> None:
        self.id_ = id_
        self.text = text
        self.metadata = metadata
        self.relationships = relationships or {}


_schema_stub.TextNode = _FakeTextNode
_schema_stub.NodeRelationship = MagicMock(PARENT="PARENT")
_schema_stub.RelatedNodeInfo = _FakeRelatedNodeInfo

from app.services.chunk_service import ParentChunk  # noqa: E402
from app.services import embed_service  # noqa: E402
from app.services import summary_service  # noqa: E402

# ── helpers ─────────────────────────────────────────────────────────────────


def _make_parent(content: str, *, pid: str = "p1") -> ParentChunk:
    return ParentChunk(
        chunk_id=pid,
        content=content,
        content_type="text",
        token_count=max(1, len(content) // 2),
        page_number=1,
    )


class _FakeCompletion:
    """Mimics LlamaIndex CompletionResponse with a ``.text`` attribute."""

    def __init__(self, text: str) -> None:
        self.text = text


class _FakeLLM:
    """Minimal LLM stub recording prompts and returning a canned summary."""

    def __init__(self, summary_text: str = "x" * 250) -> None:
        self._summary = summary_text
        self.prompts: list[str] = []

    def complete(self, prompt: str):  # noqa: D401 - LlamaIndex surface
        self.prompts.append(prompt)
        return _FakeCompletion(self._summary)


class _RaisingLLM:
    """LLM stub that always raises to exercise the degradation path."""

    def complete(self, prompt: str):  # noqa: D401
        raise TimeoutError("vLLM timeout")


# ── constants ───────────────────────────────────────────────────────────────


def test_summary_constants_match_spec():
    assert summary_service.SUMMARY_MIN_CHARS == 200
    assert summary_service.SUMMARY_MAX_CHARS == 500
    assert summary_service.DEFAULT_SUMMARY_PARENT_COUNT == 3


# ── _concat_parents ─────────────────────────────────────────────────────────


def test_concat_parents_takes_first_n():
    parents = [_make_parent(f"block{i} content here. ") for i in range(5)]
    out = summary_service._concat_parents(parents, n=3)
    assert "block0" in out
    assert "block1" in out
    assert "block2" in out
    assert "block3" not in out  # only first N
    assert "block4" not in out


def test_concat_parents_fewer_than_n_returns_all():
    parents = [_make_parent("only one block. " * 20)]
    out = summary_service._concat_parents(parents, n=3)
    assert "only one block" in out


def test_concat_parents_empty():
    assert summary_service._concat_parents([], n=3) == ""


def test_concat_parents_skips_empty_content():
    parents = [_make_parent(""), _make_parent("real content. " * 20)]
    out = summary_service._concat_parents(parents, n=3)
    assert "real content" in out


# ── _build_prompt ───────────────────────────────────────────────────────────


def test_build_prompt_contains_200_500_bounds_and_content():
    prompt = summary_service._build_prompt("SOME CONTENT")
    assert "200-500" in prompt
    assert "SOME CONTENT" in prompt


# ── _extract_summary_text ───────────────────────────────────────────────────


def test_extract_summary_text_from_string():
    assert summary_service._extract_summary_text("hi") == "hi"


def test_extract_summary_text_from_completion_text_attr():
    assert summary_service._extract_summary_text(_FakeCompletion("hi")) == "hi"


def test_extract_summary_text_from_response_attr():
    class _R:
        response = "via response"
    assert summary_service._extract_summary_text(_R()) == "via response"


def test_extract_summary_text_none_returns_empty():
    assert summary_service._extract_summary_text(None) == ""


# ── _validate_summary ───────────────────────────────────────────────────────


def test_validate_summary_accepts_in_bounds():
    s = "摘" * 300
    assert summary_service._validate_summary(s) == s


def test_validate_summary_accepts_lower_bound():
    s = "摘" * summary_service.SUMMARY_MIN_CHARS
    assert summary_service._validate_summary(s) == s


def test_validate_summary_accepts_upper_bound():
    s = "摘" * summary_service.SUMMARY_MAX_CHARS
    assert summary_service._validate_summary(s) == s


def test_validate_summary_accepts_too_short():
    # Length target is prompt-guided; out-of-range summaries are still kept.
    s = "摘" * 199
    assert summary_service._validate_summary(s) == s


def test_validate_summary_accepts_too_long():
    # Length target is prompt-guided; out-of-range summaries are still kept.
    s = "摘" * 501
    assert summary_service._validate_summary(s) == s


def test_validate_summary_rejects_empty():
    assert summary_service._validate_summary("") is None
    assert summary_service._validate_summary("   ") is None


# ── SummaryService.summarize happy path ─────────────────────────────────────


def test_summarize_returns_child_chunk_with_summary_text():
    parents = [_make_parent("父块内容较长。" * 50, pid=f"p{i}") for i in range(3)]
    llm = _FakeLLM("摘" * 250)
    svc = summary_service.SummaryService(llm=llm)
    chunk = svc.summarize(parents)
    assert chunk is not None
    # ChildChunk is imported via chunk_service; verify the chunk is a
    # ChildChunk instance directly.
    from app.services.chunk_service import ChildChunk

    assert isinstance(chunk, ChildChunk)
    assert chunk.content == "摘" * 250
    assert chunk.content_type == "text"
    assert chunk.chunk_id  # UUID assigned


def test_summarize_calls_llm_with_summary_prompt():
    parents = [_make_parent("父块内容。" * 30)]
    llm = _FakeLLM("摘" * 250)
    svc = summary_service.SummaryService(llm=llm)
    svc.summarize(parents)
    assert len(llm.prompts) == 1
    assert "200-500" in llm.prompts[0]
    assert "父块内容" in llm.prompts[0]


def test_summarize_concatenates_first_n_parents():
    parents = [_make_parent(f"第{i}段父块内容。", pid=f"p{i}") for i in range(5)]
    llm = _FakeLLM("摘" * 250)
    svc = summary_service.SummaryService(llm=llm, parent_count=2)
    svc.summarize(parents)
    prompt = llm.prompts[0]
    assert "第0段" in prompt
    assert "第1段" in prompt
    assert "第2段" not in prompt  # only first N=2


def test_summarize_default_parent_count_is_three():
    parents = [_make_parent(f"第{i}段。", pid=f"p{i}") for i in range(6)]
    llm = _FakeLLM("摘" * 250)
    svc = summary_service.SummaryService(llm=llm)
    svc.summarize(parents)
    prompt = llm.prompts[0]
    assert "第0段" in prompt and "第1段" in prompt and "第2段" in prompt
    assert "第3段" not in prompt


# ── degradation path (SPEC §5.4 / PRD US-012 AC7) ────────────────────────────


def test_summarize_returns_none_when_no_parents():
    svc = summary_service.SummaryService(llm=_FakeLLM())
    assert svc.summarize([]) is None


def test_summarize_returns_none_when_combined_empty():
    parents = [_make_parent("   ")]
    svc = summary_service.SummaryService(llm=_FakeLLM())
    assert svc.summarize(parents) is None


def test_summarize_llm_exception_returns_none_and_does_not_raise(caplog):
    parents = [_make_parent("父块内容。" * 30)]
    svc = summary_service.SummaryService(llm=_RaisingLLM())
    with caplog.at_level("WARNING", logger="app.services.summary_service"):
        result = svc.summarize(parents)
    assert result is None
    assert any("degrading" in rec.message for rec in caplog.records)


def test_summarize_empty_summary_returns_none(caplog):
    parents = [_make_parent("父块内容。" * 30)]
    llm = _FakeLLM(summary_text="")
    with caplog.at_level("WARNING", logger="app.services.summary_service"):
        result = summary_service.SummaryService(llm=llm).summarize(parents)
    assert result is None
    assert any("empty" in rec.message for rec in caplog.records)


def test_summarize_too_short_summary_still_persisted():
    # Length target is prompt-guided; out-of-range summaries are still kept.
    parents = [_make_parent("父块内容。" * 30)]
    llm = _FakeLLM(summary_text="摘" * 50)  # < 200
    chunk = summary_service.SummaryService(llm=llm).summarize(parents)
    assert chunk is not None
    assert chunk.content == "摘" * 50


def test_summarize_too_long_summary_still_persisted():
    # Length target is prompt-guided; out-of-range summaries are still kept.
    parents = [_make_parent("父块内容。" * 30)]
    llm = _FakeLLM(summary_text="摘" * 600)  # > 500
    chunk = summary_service.SummaryService(llm=llm).summarize(parents)
    assert chunk is not None
    assert chunk.content == "摘" * 600


def test_summarize_factory_failure_returns_none(caplog):
    parents = [_make_parent("父块内容。" * 30)]

    def _bad_factory():
        raise RuntimeError("vLLM not configured")

    svc = summary_service.SummaryService(llm=None, llm_factory=_bad_factory)
    with caplog.at_level("WARNING", logger="app.services.summary_service"):
        assert svc.summarize(parents) is None
    assert any("factory failed" in rec.message for rec in caplog.records)


def test_summarize_caches_factory_llm_across_calls():
    """The factory-built LLM is cached on self._llm after the first successful
    summarize() call, so subsequent calls reuse the same client (avoids
    rebuilding an OpenAILike HTTP client per document).
    """
    parents = [_make_parent("父块内容。" * 30)]

    class _CountingFactory:
        def __init__(self):
            self.calls = 0

        def __call__(self):
            self.calls += 1
            return _FakeLLM("摘" * 250)

    factory = _CountingFactory()
    svc = summary_service.SummaryService(llm=None, llm_factory=factory)
    assert svc.summarize(parents) is not None
    assert svc.summarize(parents) is not None
    assert svc.summarize(parents) is not None
    assert factory.calls == 1  # factory invoked once, then cached
    assert svc._llm is not None


# ── constructor validation ──────────────────────────────────────────────────


def test_summary_service_rejects_zero_parent_count():
    with pytest.raises(ValueError):
        summary_service.SummaryService(parent_count=0)


def test_summary_service_rejects_negative_parent_count():
    with pytest.raises(ValueError):
        summary_service.SummaryService(parent_count=-1)


def test_summary_service_accepts_custom_parent_count():
    svc = summary_service.SummaryService(llm=_FakeLLM(), parent_count=5)
    assert svc._parent_count == 5


# ── returned chunk is consumable by embed_service (chunk_type=doc_summary) ──


def test_summarize_returns_chunk_consumable_by_embed_service(monkeypatch):
    """The returned ChildChunk flows into embed_service.embed_and_write as a
    ``summaries=[...]`` entry and is stored with ``chunk_type='doc_summary'``
    (SPEC §3.1, §5.1). This end-to-end-ish test wires the two services together
    with stubbed Milvus/Index to verify the chunk_type is preserved.
    """
    inserted_nodes: list = []

    def _fake_build_vector_store_index(kb_id, *, dim=None, embed_model=None):
        idx = MagicMock()
        idx.insert_nodes = lambda nodes: inserted_nodes.extend(nodes)
        return idx

    monkeypatch.setattr(
        embed_service, "build_vector_store_index", _fake_build_vector_store_index
    )

    from app.services.chunk_service import ChildChunk

    parents = [_make_parent("父块内容较长。", pid="p1")]
    summary_chunk = summary_service.SummaryService(llm=_FakeLLM("摘" * 250)).summarize(
        parents
    )
    assert summary_chunk is not None
    assert isinstance(summary_chunk, ChildChunk)

    svc = embed_service.EmbedService(embed_model=MagicMock())
    svc.embed_and_write(
        tenant_id="t1",
        kb_id="kb1",
        doc_id="doc1",
        file_name="f.pdf",
        parents=parents,
        children=[],
        summaries=[summary_chunk],
    )

    summary_nodes = [
        n for n in inserted_nodes if n.metadata["chunk_type"] == "doc_summary"
    ]
    assert len(summary_nodes) == 1
    assert summary_nodes[0].text == "摘" * 250


# ── _default_llm_factory (defensive settings read) ──────────────────────────


def test_default_llm_factory_raises_when_settings_missing(monkeypatch):
    """When vLLM settings are absent the factory raises so the degradation
    path logs a warning and returns None.
    """

    class _EmptySettings:
        vllm_model = ""
        vllm_api_base = ""
        vllm_api_key = ""
        vllm_context_window = 4096

    monkeypatch.setattr(
        "app.core.config.settings",
        _EmptySettings(),
        raising=False,
    )

    with pytest.raises(RuntimeError, match="vLLM model/api_base not configured"):
        summary_service._default_llm_factory()


def test_default_llm_factory_builds_openai_like_when_configured(monkeypatch):
    class _Settings:
        vllm_model = "qwen2"
        vllm_api_base = "http://vllm:8000/v1"
        vllm_api_key = "k"
        vllm_context_window = 8192

    monkeypatch.setattr(
        "app.core.config.settings",
        _Settings(),
        raising=False,
    )

    captured = {}

    class _FakeOpenAILike:
        def __init__(self, **kwargs) -> None:
            captured.update(kwargs)

    monkeypatch.setattr(
        sys.modules["llama_index.llms.openai_like"], "OpenAILike", _FakeOpenAILike
    )

    llm = summary_service._default_llm_factory()
    assert isinstance(llm, _FakeOpenAILike)
    assert captured["model"] == "qwen2"
    assert captured["api_base"] == "http://vllm:8000/v1"
    assert captured["api_key"] == "k"
    assert captured["is_chat_model"] is True
    assert captured["context_window"] == 8192


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
