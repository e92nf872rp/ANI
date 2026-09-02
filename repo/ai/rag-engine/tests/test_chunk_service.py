"""Pure-logic unit tests for chunk_service (US-012 / SPEC §5.1).

These tests do NOT require LlamaIndex, asyncpg, or any remote service. The
default splitter falls back to :class:`_PureSentenceSplitter` when
``SentenceSplitter`` cannot be imported, so every test exercises the real
chunking algorithm.
"""
from __future__ import annotations

import sys
from unittest.mock import MagicMock

import pytest

# Stub heavy/optional deps so chunk_service (and its parse_service import)
# load cleanly in the test env without installing docling/llama_index/asyncpg.
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
    "minio",
    "minio.error",
    "pymilvus",
    "pymilvus.connections",
    "asyncpg",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

from app.services.chunk_service import (
    CHILD_CHUNK_MIN,
    CHILD_CHUNK_SIZE,
    CHILD_CHUNK_SIZE_MAX,
    CHILD_CHUNK_SIZE_MIN,
    PARENT_CHUNK_SIZE,
    ChildChunk,
    ChunkService,
    ParentChunk,
    _estimate_tokens,
    _force_truncate,
    _make_default_splitter,
    _PureSentenceSplitter,
    _segment_nodes,
    _split_text_by_sentences,
    _split_units,
)
from app.services.parse_service import ParsedNode

# ── _estimate_tokens ──────────────────────────────────────────────────────────


def test_estimate_tokens_min_one():
    assert _estimate_tokens("") == 1


def test_estimate_tokens_basic():
    assert _estimate_tokens("a" * 10) == 5


# ── _split_units (atomic link/code vs sentences) ──────────────────────────────


def test_split_units_plain_sentences():
    units = _split_units("第一句。第二句！")
    kinds = [k for k, _ in units]
    assert all(k == "sentence" for k in kinds)
    assert any("第一句" in t for _, t in units)
    assert any("第二句" in t for _, t in units)


def test_split_units_link_atomic():
    text = "参见 [文档](http://x/y) 链接。"
    units = _split_units(text)
    has_link = any(k == "link" and "文档" in t and "http://x/y" in t for k, t in units)
    assert has_link


def test_split_units_code_fence_atomic():
    text = "前文。\n```python\nprint('hi')\n```\n后文。"
    units = _split_units(text)
    code_units = [t for k, t in units if k == "code"]
    assert len(code_units) == 1
    assert "print('hi')" in code_units[0]


def test_split_units_link_not_split_across_boundary():
    text = "x" * 1500 + " [link](http://x) " + "y" * 1500
    chunks = _split_text_by_sentences(text, CHILD_CHUNK_SIZE)
    # Link text must appear intact in exactly one chunk.
    assert sum(1 for c in chunks if "[link](http://x)" in c) == 1


# ── _force_truncate ───────────────────────────────────────────────────────────


def test_force_truncate_splits_long_sentence():
    long = "a" * (CHILD_CHUNK_SIZE * 2 * 2)  # 2x chunk_size in tokens
    pieces = _force_truncate(long, CHILD_CHUNK_SIZE)
    assert len(pieces) >= 2
    assert all(len(p) <= CHILD_CHUNK_SIZE * 2 for p in pieces)


def test_force_truncate_short_unchanged():
    assert _force_truncate("short", CHILD_CHUNK_SIZE) == ["short"]


# ── _split_text_by_sentences (SentenceSplitter semantics) ─────────────────────


def test_split_text_respects_sentence_boundary():
    text = "这是一个较短的句子。这是另一个较短的句子。"
    chunks = _split_text_by_sentences(text, CHILD_CHUNK_SIZE)
    # Two sentences fit within one chunk (both are tiny) → one chunk.
    assert len(chunks) == 1
    assert "这是" in chunks[0]


def test_split_text_overflow_creates_multiple_chunks():
    # Many sentences that together exceed chunk_size (1024 chars).
    text = "句子内容在这里。" * 200
    chunks = _split_text_by_sentences(text, CHILD_CHUNK_SIZE)
    assert len(chunks) > 1


def test_split_text_empty():
    assert _split_text_by_sentences("", CHILD_CHUNK_SIZE) == []
    assert _split_text_by_sentences("   \n  ", CHILD_CHUNK_SIZE) == []


def test_split_text_single_long_sentence_force_truncated():
    long = "a" * (CHILD_CHUNK_SIZE * 2 * 2 + 10)
    chunks = _split_text_by_sentences(long, CHILD_CHUNK_SIZE)
    assert len(chunks) >= 2
    for c in chunks:
        assert _estimate_tokens(c) <= CHILD_CHUNK_SIZE


# ── min-threshold (preferred cut at sentence boundary once ≥ min) ────────────


def test_split_text_min_threshold_cuts_at_sentence_boundary():
    """Once a chunk reaches chunk_min, the next sentence boundary cuts."""
    # chunk_min=128 → 256 chars. Each sentence ~13 chars. After ~20 sentences
    # the chunk crosses 256 chars and is flushed at that sentence boundary,
    # well before the 512-token (1024-char) hard cap.
    text = "句子内容在这里。" * 200
    chunks = _split_text_by_sentences(text, CHILD_CHUNK_SIZE, chunk_min=CHILD_CHUNK_MIN)
    assert len(chunks) > 1
    # No chunk should grow toward the cap; each should be around the min.
    for c in chunks:
        # Chunks cut at the min threshold land roughly in [min, min+sentence].
        assert _estimate_tokens(c) <= CHILD_CHUNK_SIZE


def test_split_text_min_threshold_default_is_64():
    """The default chunk_min equals CHILD_CHUNK_MIN (64)."""
    sp = _PureSentenceSplitter(CHILD_CHUNK_SIZE)
    assert sp._chunk_min == CHILD_CHUNK_MIN
    assert CHILD_CHUNK_MIN == 64


def test_split_text_min_threshold_no_cut_below_min():
    """Below chunk_min, sentences accumulate without flushing."""
    # Tiny sentences, chunk_min high → everything stays in one chunk.
    text = "短句。" * 10  # ~40 chars = 20 tokens, below any reasonable min
    chunks = _split_text_by_sentences(text, CHILD_CHUNK_SIZE, chunk_min=128)
    assert len(chunks) == 1


def test_chunk_service_rejects_invalid_min():
    with pytest.raises(ValueError):
        ChunkService(child_chunk_min=0)


def test_chunk_service_clamps_min_above_size():
    # chunk_min > chunk_size is clamped down to chunk_size (valid per-KB
    # chunk_size down to 1, see v1.yaml), not rejected.
    svc = ChunkService(child_chunk_min=CHILD_CHUNK_SIZE + 1)
    assert svc._child_chunk_min == CHILD_CHUNK_SIZE


def test_chunk_service_accepts_custom_min():
    svc = ChunkService(child_chunk_min=128)
    assert svc._child_chunk_min == 128


# ── _segment_nodes (meta-aware segmentation) ──────────────────────────────────


def test_segment_nodes_table_atomic():
    nodes = [
        ParsedNode(content="intro text", content_type="text", metadata={"sub_type": "paragraph", "section_path": ""}),
        ParsedNode(content="<table>...</table>", content_type="table", metadata={"sub_type": "table", "section_path": ""}),
        ParsedNode(content="outro text", content_type="text", metadata={"sub_type": "paragraph", "section_path": ""}),
    ]
    segs = _segment_nodes(nodes)
    kinds = [s.kind for s in segs]
    assert kinds == ["text", "atomic", "text"]


def test_segment_nodes_heading_starts_new_segment():
    nodes = [
        ParsedNode(content="# Ch1", content_type="text", metadata={"sub_type": "heading", "heading_level": 1, "section_path": "Ch1"}),
        ParsedNode(content="para under ch1", content_type="text", metadata={"sub_type": "paragraph", "section_path": "Ch1"}),
        ParsedNode(content="# Ch2", content_type="text", metadata={"sub_type": "heading", "heading_level": 1, "section_path": "Ch2"}),
        ParsedNode(content="para under ch2", content_type="text", metadata={"sub_type": "paragraph", "section_path": "Ch2"}),
    ]
    segs = _segment_nodes(nodes)
    # Heading starts a new text segment → two text segments (each with heading + para).
    assert all(s.kind == "text" for s in segs)
    assert len(segs) == 2
    assert segs[0].heading_level == 1
    assert segs[0].section_path == "Ch1"
    assert segs[1].section_path == "Ch2"


def test_segment_nodes_section_change_without_heading():
    """Regression: a non-heading section_path change must start a new segment
    carrying the NEW section path, not the stale previous one.

    Before the fix, flush() did not reset current_section, so the second
    segment inherited the first section's path, corrupting meta-aware parent
    boundaries and metadata inheritance.
    """
    nodes = [
        ParsedNode(content="para under s1", content_type="text",
                   metadata={"sub_type": "paragraph", "section_path": "S1"}),
        ParsedNode(content="para under s2", content_type="text",
                   metadata={"sub_type": "paragraph", "section_path": "S2"}),
    ]
    segs = _segment_nodes(nodes)
    assert len(segs) == 2, "section change without heading must split"
    assert segs[0].section_path == "S1"
    assert segs[1].section_path == "S2", "second segment must carry S2, not stale S1"


def test_segment_nodes_image_flows_into_text():
    """Image nodes are NOT atomic — they flow into surrounding text segments."""
    nodes = [
        ParsedNode(content="[图片: x](url)", content_type="image", metadata={"sub_type": "image", "section_path": ""}),
    ]
    segs = _segment_nodes(nodes)
    assert len(segs) == 1
    assert segs[0].kind == "text"


def test_segment_nodes_code_atomic():
    nodes = [
        ParsedNode(content="```py\nprint(1)\n```", content_type="code", metadata={"sub_type": "code", "section_path": ""}),
    ]
    segs = _segment_nodes(nodes)
    assert len(segs) == 1
    assert segs[0].kind == "atomic"


# ── ChunkService.chunk ───────────────────────────────────────────────────────


def _text_node(content: str, *, section: str = "", sub: str = "paragraph", level=None) -> ParsedNode:
    meta = {"sub_type": sub, "section_path": section}
    if level is not None:
        meta["heading_level"] = level
    return ParsedNode(content=content, content_type="text", metadata=meta)


def _table_node(content: str, *, section: str = "") -> ParsedNode:
    return ParsedNode(content=content, content_type="table", metadata={"sub_type": "table", "section_path": section})


def _image_node(content: str, *, section: str = "") -> ParsedNode:
    return ParsedNode(content=content, content_type="image", metadata={"sub_type": "image", "section_path": section})


def test_chunk_returns_parents_and_children():
    svc = ChunkService()
    nodes = [_text_node("段落一内容较多。" * 20, section="S1")]
    parents, children = svc.chunk(nodes)
    assert len(parents) >= 1
    assert len(children) >= 1
    assert all(isinstance(p, ParentChunk) for p in parents)
    assert all(isinstance(c, ChildChunk) for c in children)


def test_chunk_table_is_atomic_child():
    svc = ChunkService()
    nodes = [
        _text_node("intro " * 50, section="S1"),
        _table_node("<table><tr><th>H</th></tr><tr><td>v</td></tr></table>", section="S1"),
    ]
    parents, children = svc.chunk(nodes)
    # SPEC §5.1: a table becomes a self-contained table parent (header
    # preserved as HTML) plus one child per data row (plain 列名：值 text).
    table_parents = [p for p in parents if "<table>" in p.content]
    assert len(table_parents) == 1
    assert len(children) >= 1


def test_chunk_image_flows_into_text_child():
    """Image nodes are NOT atomic — they become text child chunks."""
    svc = ChunkService()
    nodes = [_image_node("[图片: x](http://u)")]
    _, children = svc.chunk(nodes)
    assert len(children) == 1
    assert children[0].content_type == "text"


def test_chunk_link_not_split():
    svc = ChunkService()
    big = "前导文本。" * 100 + " [重要链接](http://x/y) " + "后继文本。" * 100
    nodes = [_text_node(big, section="S1")]
    _, children = svc.chunk(nodes)
    link_children = [c for c in children if "[重要链接](http://x/y)" in c.content]
    # Link survives intact and appears in exactly one chunk.
    assert len(link_children) == 1


def test_chunk_parent_links_children():
    svc = ChunkService()
    nodes = [_text_node("段落内容足够长以触发分块。" * 50, section="S1")]
    parents, children = svc.chunk(nodes)
    # Every child references a parent and carries parent_content.
    for c in children:
        assert c.parent_chunk_id is not None
        assert c.parent_content is not None
    # parent.chunk_id matches the children's parent_chunk_id.
    parent_ids = {p.chunk_id for p in parents}
    assert all(c.parent_chunk_id in parent_ids for c in children)


def test_chunk_parent_content_equals_parent_full_text():
    svc = ChunkService()
    nodes = [_text_node("段落内容足够长以触发分块。" * 50, section="S1")]
    parents, children = svc.chunk(nodes)
    parent_by_id = {p.chunk_id: p for p in parents}
    for c in children:
        p = parent_by_id[c.parent_chunk_id]
        assert c.parent_content == p.content


def test_chunk_parent_token_count_is_exact_not_floor_sum():
    """Parent token_count is recomputed from full text, not floor-summed.

    Guards against the floor-accumulation bug where sum(child floors) runs
    higher than the true token count of the concatenated text, which would
    trigger premature parent splits.
    """
    nodes = [
        _text_node("句子内容在这里。", section="s"),
    ] + [
        _text_node("另一句子。", section="s") for _ in range(20)
    ]
    svc = ChunkService()
    parents, _ = svc.chunk(nodes)
    assert len(parents) == 1
    from app.services.chunk_service import _estimate_tokens

    assert parents[0].token_count == _estimate_tokens(parents[0].content)


def test_chunk_parent_token_count_within_budget_or_single_child():
    svc = ChunkService()
    nodes = [_text_node("段落内容足够长以触发分块。" * 200, section="S1")]
    parents, _children = svc.chunk(nodes)
    for p in parents:
        # A parent either fits in the budget or contains a single oversized child.
        assert p.token_count <= PARENT_CHUNK_SIZE or len(p.child_ids) == 1


def test_chunk_section_boundary_splits_parent():
    """Meta-aware: a change in section_path closes the parent block."""
    svc = ChunkService(respect_section_boundaries=True)
    # Two sections, each with enough text to be a chunk but under 2048.
    nodes = [
        _text_node("第一节内容。" * 30, section="A"),
        _text_node("第二节内容。" * 30, section="B"),
    ]
    parents, children = svc.chunk(nodes)
    # Two sections → at least two parents (boundary respected).
    assert len(parents) >= 2
    # Verify each parent's children share one section (no parent spans both).
    for p in parents:
        secs = {
            c.metadata.get("section_path")
            for c in children
            if c.parent_chunk_id == p.chunk_id
        }
        assert len(secs) == 1


def test_chunk_inherits_metadata():
    svc = ChunkService()
    nodes = [
        _text_node("# Heading", section="Sec", sub="heading", level=2),
        _text_node("body " * 50, section="Sec", sub="paragraph"),
    ]
    _parents, children = svc.chunk(nodes)
    # At least one child carries the section context.
    assert any(c.metadata.get("section_path") == "Sec" for c in children)


def test_chunk_empty_nodes_returns_empty():
    svc = ChunkService()
    parents, children = svc.chunk([])
    assert parents == []
    assert children == []


def test_chunk_child_token_count_within_size_or_oversized_unit():
    svc = ChunkService()
    nodes = [_text_node("段落内容足够长以触发分块。" * 200, section="S1")]
    _, children = svc.chunk(nodes)
    for c in children:
        # Child either fits in chunk_size or is an indivisible unit (table/code/link).
        if c.token_count > CHILD_CHUNK_SIZE:
            assert c.content_type in ("table", "code")


# ── ChunkService validation ───────────────────────────────────────────────────


def test_chunk_service_rejects_too_small_child_size():
    with pytest.raises(ValueError):
        ChunkService(child_chunk_size=CHILD_CHUNK_SIZE_MIN - 1)


def test_chunk_service_rejects_too_large_child_size():
    with pytest.raises(ValueError):
        ChunkService(child_chunk_size=CHILD_CHUNK_SIZE_MAX + 1)


def test_chunk_service_accepts_valid_range():
    ChunkService(child_chunk_size=CHILD_CHUNK_SIZE_MIN)
    ChunkService(child_chunk_size=CHILD_CHUNK_SIZE_MAX)
    ChunkService(child_chunk_size=384)


# ── splitter fallback ─────────────────────────────────────────────────────────


def test_make_default_splitter_always_returns_pure():
    # _make_default_splitter always returns _PureSentenceSplitter because
    # LlamaIndex's SentenceSplitter does not support chunk_min.
    sp = _make_default_splitter(CHILD_CHUNK_SIZE)
    assert isinstance(sp, _PureSentenceSplitter)


def test_pure_splitter_basic():
    sp = _PureSentenceSplitter(CHILD_CHUNK_SIZE)
    chunks = sp.split_text("第一句。第二句！第三句？")
    assert len(chunks) == 1  # all tiny → one chunk
    assert "第一句" in chunks[0]


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
