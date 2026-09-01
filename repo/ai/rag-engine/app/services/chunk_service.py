"""Parent-child chunking service (US-012 / SPEC §5.1).

Consumes the flat ``list[ParsedNode]`` emitted by ``parse_service`` and
produces parent + child chunks:

* **Meta-aware segmentation** (the user-requested optimisation): parse_service
  enriches every node with non-hierarchical context hints
  (``sub_type`` / ``heading_level`` / ``section_path`` / ``content_type``).
  ``chunk_service`` uses these hints to decide *where* to cut *before*
  applying ``chunk_size``:

    - Tables / code blocks (``content_type`` in
      :data:`INDIVISIBLE_TYPES`) become atomic child chunks — never split.
    - Images are extracted to MinIO and inlined as ``[图片: …](oss_url)``
      markdown links, which flow into the surrounding text/parent segment
      (NOT a standalone chunk). The link itself stays atomically intact via
      the sentence splitter's markdown-link rule.
    - Markdown links ``[text](url)`` and fenced code blocks inside text are
      treated as atomic units by the sentence splitter.
    - Heading nodes (``sub_type='heading'``) start a new text segment and are
      preferred as parent-block boundaries so a parent stays within one
      section (``section_path`` breadcrumb).

* **SentenceSplitter** (SPEC §5.1): child chunks target up to ``chunk_size``
  tokens (default 1024),
  preferring sentence boundaries; a single sentence longer than
  ``chunk_size`` is force-truncated. The default splitter wraps LlamaIndex's
  :class:`SentenceSplitter` when genuinely importable and falls back to a
  pure-logic sentence splitter with the same semantics otherwise, so the
  module loads and is unit-testable without the heavy dependency.

* **Fixed-window parent nesting**: consecutive child chunks accumulate into
  a parent block up to ``PARENT_CHUNK_SIZE`` (2048) tokens (SPEC §5.1). Each
  child's ``parent_chunk_id`` points at its parent and the parent's full
  text is denormalized into every child's ``parent_content``.
"""
from __future__ import annotations

import re
import uuid
from dataclasses import dataclass, field
from typing import Any, Protocol

from app.services.parse_service import ParsedNode, _compose_table, _html_table_rows

# ── Constants (SPEC §5.2) ─────────────────────────────────────────────────────

# Child chunk size (tokens). Fixed at 256 — child chunks are sentence-level
# segments (1-2 sentences). NOT controlled by KB chunk_size.
CHILD_CHUNK_SIZE = 256
# Lower bound for a child chunk. Once a chunk reaches this size, the next
# sentence boundary is preferred as the cut point, so child chunks land in
# [CHILD_CHUNK_MIN, CHILD_CHUNK_SIZE] rather than always filling toward the
# upper bound.
CHILD_CHUNK_MIN = 64
# Parent block default size (tokens). Controlled by KB chunk_size (default
# 1024 when KB row is missing or has no chunk_size set). The value is
# overridable by each parse task via the KB's chunk_size.
PARENT_CHUNK_SIZE = 1024
# Validation bounds for child_chunk_size (used by ChunkService). These mirror
# the per-KB ``chunk_size`` bounds in api/openapi/services/v1.yaml
# (minimum: 1, maximum: 8192), so any value valid for a KB is valid here.
CHILD_CHUNK_SIZE_MIN = 1
CHILD_CHUNK_SIZE_MAX = 8192
# Rough tokens-per-char heuristic — same as parse_service, so thresholds are
# consistent across the parse → chunk pipeline.
CHARS_PER_TOKEN = 2

# Content types that must never be split (SPEC §5.1: 表格/代码块).
# Images are NOT atomic — they flow into the surrounding text/parent segment
# (a standalone image chunk is not useful for semantic retrieval). The image
# link itself remains atomically intact via _LINK_RE.
INDIVISIBLE_TYPES = {"table", "code"}

# Sentence boundary: CJK terminal punctuation （。！？）and ASCII (.!?) followed
# by whitespace, plus newlines. Keep CJK punctuation glued to its sentence.
_SENTENCE_SPLIT_RE = re.compile(r"(?<=[。！？!?])\s*|\n+|(?<=[.!?])\s+")
# Markdown link ``[text](url)`` and image ``![alt](url)`` — atomic, never split.
_LINK_RE = re.compile(r"!?\[([^\]]*)\]\(([^)]*)\)")
# Fenced code block ```lang\n...\n``` — atomic, never split.
_CODE_FENCE_RE = re.compile(r"```[^\n]*\n[\s\S]*?```", re.MULTILINE)


class _Splitter(Protocol):
    """Minimal contract for a sentence-aware splitter (LlamaIndex-compatible)."""

    def split_text(self, text: str) -> list[str]: ...


# ── Data structures ──────────────────────────────────────────────────────────


@dataclass
class ChildChunk:
    """A child chunk (≈1-8192 tokens) pointing at its parent block."""

    chunk_id: str
    content: str
    content_type: str
    page_number: int | None
    token_count: int
    parent_chunk_id: str | None = None
    parent_content: str | None = None
    # custom_metadata (JSONB): section_path / sub_type / heading_level / …
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass
class ParentChunk:
    """A parent block (≤2048 tokens) grouping consecutive child chunks."""

    chunk_id: str
    content: str  # full text = concatenation of child contents
    content_type: str  # always 'text' (parent blocks are text aggregates)
    token_count: int
    page_number: int | None
    metadata: dict[str, Any] = field(default_factory=dict)
    child_ids: list[str] = field(default_factory=list)


# ── Token estimation (matches parse_service) ─────────────────────────────────


def _estimate_tokens(text: str) -> int:
    """Conservative token estimate (chars / 2), minimum 1."""
    return max(1, len(text) // CHARS_PER_TOKEN)


def _html_escape(text: str) -> str:
    """Escape a raw cell value for safe embedding inside an HTML table cell."""
    return (
        text.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )


# ── Pure-logic sentence splitter (fallback / testable path) ───────────────────


def _split_sentences(text: str) -> list[str]:
    """Split text into sentences respecting CJK + ASCII punctuation."""
    text = text.strip()
    if not text:
        return []
    parts = _SENTENCE_SPLIT_RE.split(text)
    return [p.strip() for p in parts if p.strip()]


def _force_truncate(text: str, chunk_size: int) -> list[str]:
    """Force-truncate a too-long sentence into char-sized pieces.

    Links/code fences are never passed here (they are atomic); only plain
    sentences reach this path.
    """
    max_chars = max(1, chunk_size * CHARS_PER_TOKEN)
    return [text[i : i + max_chars] for i in range(0, len(text), max_chars)]


def _split_units(text: str) -> list[tuple[str, str]]:
    """Split text into atomic (kind, text) units.

    ``kind`` is ``'link'`` for markdown links, ``'code'`` for fenced code
    blocks, ``'sentence'`` for plain sentences. Links and code blocks are
    indivisible (SPEC §5.1).
    """
    units: list[tuple[str, str]] = []

    # First lift fenced code blocks (they may span newlines and contain
    # anything, including characters that look like links).
    code_spans: list[tuple[int, int, str]] = [
        (m.start(), m.end(), m.group(0)) for m in _CODE_FENCE_RE.finditer(text)
    ]

    def _emit_sentence_range(span: str) -> None:
        # Within a non-code span, lift markdown links as atomic units.
        last = 0
        for m in _LINK_RE.finditer(span):
            if m.start() > last:
                for s in _split_sentences(span[last : m.start()]):
                    units.append(("sentence", s))
            units.append(("link", m.group(0)))
            last = m.end()
        if last < len(span):
            for s in _split_sentences(span[last:]):
                units.append(("sentence", s))

    cursor = 0
    for start, end, block in code_spans:
        if start > cursor:
            _emit_sentence_range(text[cursor:start])
        units.append(("code", block))
        cursor = end
    if cursor < len(text):
        _emit_sentence_range(text[cursor:])

    return units


def _split_text_by_sentences(
    text: str,
    chunk_size: int,
    chunk_min: int = CHILD_CHUNK_MIN,
) -> list[str]:
    """Sentence-aware splitter mimicking LlamaIndex ``SentenceSplitter``.

    Accumulates atomic units (sentences / links / code fences) into chunks.
    Once a chunk reaches ``chunk_min`` tokens, the next sentence boundary is
    preferred as the cut point (rather than filling toward ``chunk_size``),
    so child chunks land in ``[chunk_min, chunk_size]``. ``chunk_size`` is
    still the hard upper bound. Links and code blocks are indivisible —
    they are emitted as standalone chunks if they would overflow, never
    truncated. Oversized plain sentences are force-truncated (SPEC §5.1).

    Char-based budget is used internally to avoid the rounding drift that
    accumulates when summing floored per-unit token estimates (e.g. a 13-char
    sentence is 6 tokens by floor but 6.5 tokens real; 85 such sentences
    would sum to 510 floored yet really be 552 tokens). The char budgets are
    ``chunk_size * CHARS_PER_TOKEN`` (hard cap) and
    ``chunk_min * CHARS_PER_TOKEN`` (preferred cut threshold) and are exact.
    """
    units = _split_units(text)
    if not units:
        return []

    max_chars = max(1, chunk_size * CHARS_PER_TOKEN)
    min_chars = max(0, chunk_min * CHARS_PER_TOKEN)
    chunks: list[str] = []
    current: list[str] = []
    current_chars = 0

    def flush() -> None:
        nonlocal current, current_chars
        if current:
            chunks.append("".join(current).strip())
            current = []
            current_chars = 0

    for kind, unit in units:
        ulen = len(unit)
        if kind in ("link", "code"):
            # Indivisible: never split. If it cannot fit in the current
            # chunk, flush first. The link stays with surrounding text so
            # images do not become standalone chunks.
            if current and current_chars + ulen > max_chars:
                flush()
            current.append(unit)
            current_chars += ulen
            continue
        # Plain sentence.
        if ulen > max_chars:
            flush()
            chunks.extend(_force_truncate(unit, chunk_size))
            continue
        # If adding this sentence would exceed the hard cap, flush first.
        if current and current_chars + ulen > max_chars:
            flush()
        current.append(unit)
        current_chars += ulen
        # Once the chunk reaches the min threshold, prefer to cut here at
        # the sentence boundary rather than accumulating toward the cap.
        if min_chars and current_chars >= min_chars:
            flush()

    flush()
    return [c for c in chunks if c]


class _PureSentenceSplitter:
    """Pure-logic :class:`_Splitter` implementation (no LlamaIndex dependency)."""

    def __init__(self, chunk_size: int, chunk_min: int = CHILD_CHUNK_MIN) -> None:
        self._chunk_size = chunk_size
        self._chunk_min = chunk_min

    def split_text(self, text: str) -> list[str]:
        return _split_text_by_sentences(text, self._chunk_size, self._chunk_min)


def _make_default_splitter(chunk_size: int, chunk_min: int = CHILD_CHUNK_MIN) -> _Splitter:
    """Build the default splitter.

    Always uses :class:`_PureSentenceSplitter` which honours ``chunk_min``
    (the preferred cut threshold). The LlamaIndex ``SentenceSplitter`` does
    not expose a min-size knob, so it cannot implement the [chunk_min,
    chunk_size] range required by SPEC §5.1 — it fills toward ``chunk_size``
    producing oversized child chunks.
    """
    return _PureSentenceSplitter(chunk_size, chunk_min)


# ── Meta-aware segmentation ───────────────────────────────────────────────────


@dataclass
class _Segment:
    """A run of nodes forming one splittable or atomic segment."""

    kind: str  # 'text' | 'atomic'
    nodes: list[ParsedNode]
    section_path: str = ""
    heading_level: int | None = None


def _segment_nodes(nodes: list[ParsedNode]) -> list[_Segment]:
    """Group ParsedNodes into segments using metadata hints.

    * Atomic nodes (``content_type`` in :data:`INDIVISIBLE_TYPES`) become
      their own ``'atomic'`` segment.
    * Heading nodes (``sub_type='heading'``) start a new ``'text'`` segment
      so each section is chunked independently and parent blocks prefer
      section-coherent boundaries.
    * A change in ``section_path`` (even without a heading node) also starts
      a new ``'text'`` segment — the meta-aware optimisation: content from
      different sections is never merged, so parent blocks stay within one
      section.
    * Consecutive paragraphs/lists under the same section accumulate into
      one ``'text'`` segment.
    """
    segments: list[_Segment] = []
    current: list[ParsedNode] = []
    current_section = ""
    current_level: int | None = None

    def flush() -> None:
        nonlocal current, current_level
        if current:
            segments.append(
                _Segment(
                    kind="text",
                    nodes=current,
                    section_path=current_section,
                    heading_level=current_level,
                )
            )
            current = []

    for node in nodes:
        if node.content_type in INDIVISIBLE_TYPES:
            # Atomic nodes break any in-progress text segment.
            flush()
            segments.append(
                _Segment(
                    kind="atomic",
                    nodes=[node],
                    section_path=node.metadata.get("section_path", ""),
                )
            )
            continue
        node_section = node.metadata.get("section_path", "")
        if node.metadata.get("sub_type") == "heading":
            # A heading starts a new text segment (semantic boundary).
            flush()
            current = [node]
            current_section = node_section
            current_level = node.metadata.get("heading_level")
            continue
        # Paragraph / list / plain text.
        # A change in section_path also breaks the segment (meta-aware):
        # content from different sections is never merged into one segment.
        if current and node_section and current_section and node_section != current_section:
            flush()
            current_level = None
            # Reset current_section so the guard below adopts the new
            # section; otherwise flush() (which does not reset
            # current_section) leaves the stale value and the new segment
            # would inherit the previous section's path.
            current_section = ""
        current.append(node)
        if not current_section:
            current_section = node_section
    flush()
    return segments


def _segment_text(nodes: list[ParsedNode]) -> str:
    """Concatenate a text segment's node contents (heading kept as prefix)."""
    return "\n\n".join(n.content for n in nodes if n.content)


def _segment_metadata(nodes: list[ParsedNode]) -> dict[str, Any]:
    """Derive inherited metadata for a segment from its leading node."""
    lead = nodes[0]
    meta: dict[str, Any] = {}
    for key in ("sub_type", "heading_level", "section_path"):
        val = lead.metadata.get(key)
        if val is not None:
            meta[key] = val
    return meta


# ── ChunkService ─────────────────────────────────────────────────────────────


class ChunkService:
    """Parent-child chunking (SPEC §5.1).

    Args:
        parent_chunk_size: Parent block target in tokens, controlled by KB
            ``chunk_size`` (default 1024). Consecutive child chunks accumulate
            into a parent block until this limit is reached.
        child_chunk_size: Child chunk size in tokens (fixed 256). Child chunks
            are sentence-level segments (1-2 sentences).
        child_chunk_min: Lower bound for child chunks in tokens (default
            ``CHILD_CHUNK_MIN=64``). Once a chunk reaches this size, the
            next sentence boundary is preferred as the cut point, so child
            chunks land in ``[child_chunk_min, child_chunk_size]`` rather
            than always filling toward the upper bound.
        splitter: Optional sentence-aware splitter (LlamaIndex
            :class:`SentenceSplitter`-compatible). When ``None`` a default
            is constructed via :func:`_make_default_splitter`.
        respect_section_boundaries: When ``True`` (default) a change in
            ``section_path`` closes the in-progress parent block even before
            reaching ``parent_chunk_size``, keeping parent blocks
            section-coherent (the meta-aware optimisation).
    """

    def __init__(
        self,
        *,
        parent_chunk_size: int = PARENT_CHUNK_SIZE,
        child_chunk_size: int = CHILD_CHUNK_SIZE,
        child_chunk_min: int = CHILD_CHUNK_MIN,
        splitter: _Splitter | None = None,
        respect_section_boundaries: bool = True,
    ) -> None:
        if not (CHILD_CHUNK_SIZE_MIN <= child_chunk_size <= CHILD_CHUNK_SIZE_MAX):
            raise ValueError(
                f"child_chunk_size must be in [{CHILD_CHUNK_SIZE_MIN}, "
                f"{CHILD_CHUNK_SIZE_MAX}]; got {child_chunk_size}"
            )
        # v1.yaml allows chunk_size down to 1; never let the preferred
        # cut threshold exceed the chunk size, clamp instead of erroring.
        child_chunk_min = min(child_chunk_min, child_chunk_size)
        if child_chunk_min < 1:
            raise ValueError(
                f"child_chunk_min must be >= 1; got {child_chunk_min}"
            )
        self._child_chunk_size = child_chunk_size
        self._child_chunk_min = child_chunk_min
        self._parent_chunk_size = parent_chunk_size
        self._splitter = splitter or _make_default_splitter(
            child_chunk_size, child_chunk_min
        )
        self._respect_section_boundaries = respect_section_boundaries

    def chunk(
        self,
        nodes: list[ParsedNode],
    ) -> tuple[list[ParentChunk], list[ChildChunk]]:
        """Chunk parse nodes into (parents, children)."""
        parents: list[ParentChunk] = []
        children: list[ChildChunk] = []

        # Step 1: meta-aware segmentation (decide WHERE to cut first).
        segments = _segment_nodes(nodes)

        # Step 2: split each segment into child chunks. Table segments become
        # a self-contained parent (the whole HTML table, header preserved) plus
        # one child per data row formatted as ``列名：值`` plain text (HTML
        # stripped). All other non-table children accumulate for fixed-window
        # parent nesting.
        nestable: list[ChildChunk] = []
        for seg in segments:
            if seg.kind == "atomic":
                node = seg.nodes[0]
                if node.content_type == "table":
                    table_parents, row_children = self._chunk_table(node, seg)
                    if not row_children:
                        # Un-decomposable table → treat as one atomic child.
                        child = self._atomic_child(node, seg)
                        nestable.append(child)
                        children.append(child)
                    else:
                        parents.extend(table_parents)
                        children.extend(row_children)
                else:
                    child = self._atomic_child(node, seg)
                    nestable.append(child)
                    children.append(child)
            else:
                text = _segment_text(seg.nodes)
                if not text.strip():
                    continue
                # Skip heading-only segments (e.g. a bare `# Sheet-name` title
                # that would otherwise become a standalone heading parent block).
                # The heading's section context is already baked into its sibling
                # table nodes' ``section_path`` at parse time, so dropping it here
                # does not affect which sheet a table parent belongs to.
                if all(
                    n.metadata.get("sub_type") == "heading" for n in seg.nodes
                ):
                    continue
                seg_meta = _segment_metadata(seg.nodes)
                page_number = seg.nodes[0].page_number
                for piece in self._splitter.split_text(text):
                    if not piece.strip():
                        continue
                    child = ChildChunk(
                        chunk_id=str(uuid.uuid4()),
                        content=piece,
                        content_type="text",
                        page_number=page_number,
                        token_count=_estimate_tokens(piece),
                        metadata=dict(seg_meta),
                    )
                    nestable.append(child)
                    children.append(child)

        # Step 3: fixed-window parent nesting (2048) with section-coherent
        # boundary preference (meta-aware optimisation) for non-table children.
        parents.extend(self._nest_parents(nestable))
        return parents, children

    # ── child builders ────────────────────────────────────────────────────────

    def _atomic_child(self, node: ParsedNode, seg: _Segment) -> ChildChunk:
        meta = _segment_metadata(seg.nodes)
        return ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content=node.content,
            content_type=node.content_type,
            page_number=node.page_number,
            token_count=_estimate_tokens(node.content),
            metadata=meta,
        )

    # ── table chunking (per-row children + chunk-split HTML-table parents) ────

    def _chunk_table(
        self, node: ParsedNode, seg: _Segment
    ) -> tuple[list[ParentChunk], list[ChildChunk]]:
        """Split an HTML table into table parents + per-row children.

        A Sheet/table becomes one or more self-contained **parent** blocks
        (each an HTML table carrying the header ``<th>`` row), produced by the
        same fixed-window chunking rule as text parents: rows are greedily
        packed up to ``PARENT_CHUNK_SIZE`` (2048) tokens, and a row is never
        split or truncated (``行不截断`` / ``每父块保留表头``).

        Each data row maps to one **child** chunk formatted as plain
        ``列名：值`` text with all HTML tags removed.
        """
        meta = _segment_metadata(seg.nodes)
        headers, records = _html_table_rows(node.content)
        if not headers or not records:
            # Un-decomposable table (no header / no data rows): signal the
            # caller to fall back to a single atomic child for fixed-window
            # nesting so content is not lost.
            return [], []

        # Precompute the header row once (reused by every parent block).
        header_html = "<tr>" + "".join(f"<th>{_html_escape(h)}</th>" for h in headers) + "</tr>"
        units: list[tuple[int, str, str]] = []  # (tokens, child_text, row_html)
        for record in records:
            lines = []
            for i, col in enumerate(headers):
                if not col:
                    continue
                val = record[i] if i < len(record) else ""
                if val:
                    lines.append(f"{col}：{val}")
            child_text = "\n".join(lines).strip()
            if not child_text:
                continue
            row_html = "<tr>" + "".join(f"<td>{_html_escape(v)}</td>" for v in record) + "</tr>"
            units.append((_estimate_tokens(child_text), child_text, row_html))
        if not units:
            return [], []

        # Greedily pack rows into parent blocks (≤ parent_chunk_size); a single
        # oversized row always gets its own parent (rows are never split).
        parents: list[ParentChunk] = []
        all_children: list[ChildChunk] = []
        group: list[tuple[int, str, str]] = []
        group_tokens = 0

        def flush() -> None:
            nonlocal group, group_tokens
            if not group:
                return
            parent_id = str(uuid.uuid4())
            rows_html = [r for (_, _, r) in group]
            parent_html = _compose_table(header_html, rows_html)
            children = [
                ChildChunk(
                    chunk_id=str(uuid.uuid4()),
                    content=text,
                    content_type="text",
                    page_number=node.page_number,
                    token_count=tokens,
                    metadata=dict(meta),
                    parent_chunk_id=parent_id,
                    parent_content=parent_html,
                )
                for (tokens, text, _) in group
            ]
            parents.append(
                ParentChunk(
                    chunk_id=parent_id,
                    content=parent_html,
                    content_type="table",
                    token_count=_estimate_tokens(parent_html),
                    page_number=node.page_number,
                    metadata=meta,
                    child_ids=[c.chunk_id for c in children],
                )
            )
            all_children.extend(children)
            group = []
            group_tokens = 0

        for (tokens, text, row_html) in units:
            if group and group_tokens + tokens > self._parent_chunk_size:
                flush()
            group.append((tokens, text, row_html))
            group_tokens += tokens
        flush()
        return parents, all_children

    # ── parent nesting ────────────────────────────────────────────────────────

    def _nest_parents(self, children: list[ChildChunk]) -> list[ParentChunk]:
        parents: list[ParentChunk] = []
        bucket: list[ChildChunk] = []
        bucket_tokens = 0
        bucket_section = ""

        def close_bucket() -> None:
            nonlocal bucket, bucket_tokens
            if not bucket:
                return
            parent = self._build_parent(bucket)
            parents.append(parent)
            bucket = []
            bucket_tokens = 0

        for child in children:
            child_section = child.metadata.get("section_path", "")
            section_changed = (
                self._respect_section_boundaries
                and bucket
                and child_section
                and bucket_section
                and child_section != bucket_section
            )
            overflow = bucket and bucket_tokens + child.token_count > self._parent_chunk_size
            if section_changed or overflow:
                close_bucket()
            if not bucket:
                bucket_section = child_section
            bucket.append(child)
            bucket_tokens += child.token_count
        close_bucket()
        return parents

    def _build_parent(self, bucket: list[ChildChunk]) -> ParentChunk:
        parent_id = str(uuid.uuid4())
        full_text = "\n".join(c.content for c in bucket if c.content).strip()
        # Recompute the parent token count from the full text rather than
        # summing child ``token_count`` values: each child estimate is
        # ``len // 2`` (floor), so summing floors accumulates drift and the
        # parent count runs high, triggering premature parent splits.
        token_count = _estimate_tokens(full_text)
        page_number = bucket[0].page_number
        # Parent metadata inherits the section context of its first child.
        meta: dict[str, Any] = {}
        for key in ("sub_type", "heading_level", "section_path"):
            val = bucket[0].metadata.get(key)
            if val is not None:
                meta[key] = val
        parent = ParentChunk(
            chunk_id=parent_id,
            content=full_text,
            content_type="text",
            token_count=token_count,
            page_number=page_number,
            metadata=meta,
            child_ids=[c.chunk_id for c in bucket],
        )
        # Wire children → parent + denormalize parent full text.
        for c in bucket:
            c.parent_chunk_id = parent_id
            c.parent_content = full_text
        return parent
