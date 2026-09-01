"""Pure-logic unit tests for parse_service (US-011 / SPEC §5.1).

These tests do NOT require docling, minio, milvus, or any remote service.
They validate the core parsing algorithms: table→HTML conversion, large
table splitting (row groups with header preservation), image placeholder
rewriting, page splitting, and markdown table/text segmentation.
"""
from __future__ import annotations

import importlib
import sys
from pathlib import Path
from unittest.mock import MagicMock

import pytest

# Force stub modules for optional heavy deps so parse_service imports cleanly
# without installing docling / llama_index readers.
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
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

from app.services.parse_service import (
    _build_image_placeholder,
    _caption_for,
    _compose_table,
    _decompose_html_table,
    _detect_heading,
    _emit_text_table_nodes,
    _estimate_tokens,
    _extract_html_table,
    _is_pipe_table,
    _pipe_table_to_html,
    _split_large_table,
    _split_tables_and_text,
    _split_text_by_page,
)

# ── _estimate_tokens ──────────────────────────────────────────────────────────


def test_estimate_tokens_min_one():
    assert _estimate_tokens("") == 1


def test_estimate_tokens_basic():
    # 10 chars / 2 = 5 tokens
    assert _estimate_tokens("a" * 10) == 5


# ── _is_pipe_table ───────────────────────────────────────────────────────────


def test_is_pipe_table_true_with_separator():
    lines = ["| H1 | H2 |", "|---|---|"]
    assert _is_pipe_table(lines) is True


def test_is_pipe_table_false_blank():
    assert _is_pipe_table([]) is False


def test_is_pipe_table_false_no_pipe():
    assert _is_pipe_table(["plain text"]) is False


# ── _pipe_table_to_html ──────────────────────────────────────────────────────


def test_pipe_table_to_html_basic():
    md = "| A | B |\n|---|---|\n| 1 | 2 |"
    html = _pipe_table_to_html(md)
    assert "<table>" in html
    assert "<th>A</th>" in html
    assert "<th>B</th>" in html
    assert "<td>1</td>" in html
    assert "<td>2</td>" in html


def test_pipe_table_to_html_empty():
    assert _pipe_table_to_html("") == ""


def test_pipe_table_to_html_drops_separator():
    md = "| A | B |\n|---|---|\n| 1 | 2 |"
    html = _pipe_table_to_html(md)
    # separator row should not appear as a data row
    assert "<td>---</td>" not in html


# ── _extract_html_table ─────────────────────────────────────────────────────


def test_extract_html_table_found():
    fenced = "```html\n<table><tr><td>x</td></tr></table>\n```"
    assert _extract_html_table(fenced) == "<table><tr><td>x</td></tr></table>"


def test_extract_html_table_not_found():
    assert _extract_html_table("no table here") == ""


# ── _decompose / _compose_html_table ─────────────────────────────────────────


def test_decompose_html_table():
    html = "<table>\n<tr><th>H</th></tr>\n<tr><td>1</td></tr>\n<tr><td>2</td></tr>\n</table>"
    header, rows = _decompose_html_table(html)
    assert "<th>H</th>" in header
    assert len(rows) == 2
    assert "<td>1</td>" in rows[0]


def test_decompose_html_table_empty():
    header, rows = _decompose_html_table("<table></table>")
    assert header == ""
    assert rows == []


def test_compose_table_with_header():
    out = _compose_table("<tr><th>H</th></tr>", ["<tr><td>1</td></tr>"])
    assert out.startswith("<table>")
    assert "<th>H</th>" in out
    assert "<td>1</td>" in out


def test_compose_table_no_header():
    out = _compose_table("", ["<tr><td>1</td></tr>"])
    assert out.startswith("<table>")
    assert "<td>1</td>" in out


# ── _split_large_table ───────────────────────────────────────────────────────


def test_split_large_table_small_unchanged():
    small = "<table>\n<tr><th>H</th></tr>\n<tr><td>x</td></tr>\n</table>"
    assert _split_large_table(small) == [small]


def test_split_large_table_splits_preserving_header():
    header = "<tr><th>H</th></tr>"
    # Build rows that together exceed the threshold (4097 chars => >2048 tokens).
    rows = [f"<tr><td>{'a' * 100}</td></tr>" for _ in range(50)]
    big = _compose_table(header, rows)
    groups = _split_large_table(big)
    assert len(groups) > 1
    # Every group must contain the header.
    for g in groups:
        assert "<th>H</th>" in g


def test_split_large_table_no_header_returns_original():
    big = "<table>\n<tr><td>" + "a" * 5000 + "</td></tr>\n</table>"
    assert _split_large_table(big) == [big]


def test_split_large_table_single_row_exceeds_threshold():
    header = "<tr><th>H</th></tr>"
    huge_row = f"<tr><td>{'a' * 5000}</td></tr>"
    small_row = "<tr><td>b</td></tr>"
    big = _compose_table(header, [huge_row, small_row])
    groups = _split_large_table(big)
    # The huge row becomes its own group.
    assert len(groups) >= 2
    assert huge_row in groups[0]


# ── _split_tables_and_text ───────────────────────────────────────────────────


def test_split_tables_and_text_text_only():
    segments = _split_tables_and_text("hello\nworld")
    assert segments == [("text", "hello\nworld")]


def test_split_tables_and_text_pipe_table():
    md = "intro\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\ntail"
    segments = _split_tables_and_text(md)
    kinds = [s[0] for s in segments]
    assert "table" in kinds
    assert "text" in kinds
    table_seg = next(s for s in segments if s[0] == "table")
    assert "<table>" in table_seg[1]
    assert "<th>A</th>" in table_seg[1]


def test_split_tables_and_text_fenced_html_table():
    md = "```html\n<table><tr><th>H</th></tr><tr><td>1</td></tr></table>\n```"
    segments = _split_tables_and_text(md)
    table_segs = [s for s in segments if s[0] == "table"]
    assert len(table_segs) == 1
    assert "<table>" in table_segs[0][1]


def test_split_tables_and_text_fenced_pipe_table():
    md = "```\n| A | B |\n|---|---|\n| 1 | 2 |\n```"
    segments = _split_tables_and_text(md)
    table_segs = [s for s in segments if s[0] == "table"]
    assert len(table_segs) == 1
    assert "<th>A</th>" in table_segs[0][1]


# ── image helpers ────────────────────────────────────────────────────────────


def test_caption_for_chinese():
    assert _caption_for("图 1：架构图") == "架构图"


def test_caption_for_english():
    assert _caption_for("Figure 2: System design") == "System design"


def test_caption_for_none():
    assert _caption_for("") is None
    assert _caption_for("not a caption") is None


def test_build_image_placeholder_with_caption():
    # _build_image_placeholder uses the provided caption verbatim (no parsing).
    assert _build_image_placeholder("图 1：架构图", "http://x/y.png", "架构图") == "[图片: 架构图](http://x/y.png)"


def test_build_image_placeholder_fallback_alt():
    assert _build_image_placeholder("some alt", "http://x/y.png") == "[图片: some alt](http://x/y.png)"


def test_build_image_placeholder_default():
    assert _build_image_placeholder("", "http://x/y.png") == "[图片: 图片](http://x/y.png)"


# ── _split_text_by_page ──────────────────────────────────────────────────────


def test_split_text_by_page_no_markers():
    nodes = list(_split_text_by_page("hello world", 1))
    assert len(nodes) == 1
    assert nodes[0].content == "hello world"
    assert nodes[0].page_number == 1
    assert nodes[0].content_type == "text"


def test_split_text_by_page_with_markers():
    text = "page1\n<!-- page: 2 -->\npage2\n<!-- page: 5 -->\npage5"
    nodes = list(_split_text_by_page(text, 1))
    assert len(nodes) == 3
    assert nodes[0].page_number == 1
    assert nodes[1].page_number == 2
    assert nodes[2].page_number == 5


def test_split_text_by_page_blank_segments_skipped():
    text = "<!-- page: 3 -->\nreal content"
    nodes = list(_split_text_by_page(text, 1))
    assert len(nodes) == 1
    assert nodes[0].page_number == 3
    assert nodes[0].content == "real content"


# ── ParseService integration (mocked DoclingReader) ──────────────────────────


def test_parse_service_rejects_unsupported_type():
    # Reload module so our patched stubs take effect for DoclingReader import.
    import app.services.parse_service as ps
    importlib.reload(ps)
    svc = ps.ParseService()
    with pytest.raises(ValueError, match="doc.unsupported_type"):
        svc.parse("foo.html")


def test_parse_service_text_only_path(tmp_path: Path):
    import app.services.parse_service as ps
    importlib.reload(ps)

    # Stub DoclingReader to return canned markdown.
    fake_doc = MagicMock()
    fake_doc.text = "hello\n\n| A | B |\n|---|---|\n| 1 | 2 |"
    ps.DoclingReader = MagicMock(return_value=MagicMock(load_data=lambda file_path: [fake_doc]))  # type: ignore[misc]

    # Use .docx so the parser routes through DoclingReader (TXT/MD read directly).
    f = tmp_path / "doc.docx"
    f.write_bytes(b"dummy")

    svc = ps.ParseService(uploader=None)
    nodes = svc.parse(str(f))
    assert len(nodes) >= 1
    kinds = {n.content_type for n in nodes}
    assert "text" in kinds
    assert "table" in kinds


# ── _detect_heading ──────────────────────────────────────────────────────────


def test_detect_heading_h1():
    assert _detect_heading("# Title") == (1, "Title")


def test_detect_heading_h3():
    assert _detect_heading("### Deep Section") == (3, "Deep Section")


def test_detect_heading_with_trailing_hash():
    assert _detect_heading("## Title ##") == (2, "Title")


def test_detect_heading_not_heading():
    assert _detect_heading("Regular paragraph text") is None


def test_detect_heading_blank():
    assert _detect_heading("") is None


def test_detect_heading_leading_blank_lines():
    assert _detect_heading("\n\n# Heading After Blanks") == (1, "Heading After Blanks")


# ── _emit_text_table_nodes metadata enrichment ───────────────────────────────


def test_emit_nodes_heading_sub_type():
    """Heading nodes get sub_type=heading + heading_level."""
    md = "# Chapter 1\n\nSome paragraph text."
    nodes = _emit_text_table_nodes(md)
    # Node[0] = heading, Node[1] = paragraph
    assert nodes[0].metadata["sub_type"] == "heading"
    assert nodes[0].metadata["heading_level"] == 1
    assert nodes[1].metadata["sub_type"] == "paragraph"


def test_emit_nodes_section_path_breadcrumbs():
    """section_path accumulates heading ancestry as breadcrumbs."""
    md = "# Chapter\n\nIntro text.\n\n## Section\n\nDetail text."
    nodes = _emit_text_table_nodes(md)
    # Find the node under "## Section"
    section_node = next(n for n in nodes if "Detail text" in n.content)
    assert section_node.metadata["section_path"] == "Chapter > Section"


def test_emit_nodes_section_path_pop_on_higher_level():
    """section_path pops deeper levels when a higher-level heading appears."""
    md = "# A\n\n## B\n\n# C\n\ntext"
    nodes = _emit_text_table_nodes(md)
    # The last text node is under "# C", section_path should be just "C"
    last_text = next(n for n in nodes if "text" in n.content)
    assert last_text.metadata["section_path"] == "C"


def test_emit_nodes_table_metadata():
    """Table nodes get sub_type=table + row_count + table_index."""
    md = "| A | B |\n|---|---|\n| 1 | 2 |"
    nodes = _emit_text_table_nodes(md)
    tbl = next(n for n in nodes if n.content_type == "table")
    assert tbl.metadata["sub_type"] == "table"
    assert tbl.metadata["row_count"] == 2  # header + 1 data row
    assert tbl.metadata["table_index"] == 1
    assert "is_large_table" in tbl.metadata


def test_emit_nodes_list_sub_type():
    """List-heavy text segments get sub_type=list."""
    md = "- item one\n- item two\n- item three"
    nodes = _emit_text_table_nodes(md)
    assert nodes[0].metadata["sub_type"] == "list"


def test_emit_nodes_table_inherits_section_path():
    """Table nodes carry the section_path of their surrounding headings."""
    md = "# Report\n\n| Col | Val |\n|---|---|\n| x | 1 |"
    nodes = _emit_text_table_nodes(md)
    tbl = next(n for n in nodes if n.content_type == "table")
    assert tbl.metadata["section_path"] == "Report"


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
