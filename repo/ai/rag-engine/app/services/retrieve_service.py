"""Hybrid retrieval service (US-014 / SPEC §2.2, §5.1).

``retrieve_service`` implements the hybrid retrieval strategy mandated by
SPEC §5.1 / PRD US-014:

1. **Vector retriever** — the KB's Milvus collection wrapped via
   ``VectorStoreIndex.from_vector_store(...).as_retriever(similarity_top_k=top_k)``
   so the Index layer embeds the query with the shared
   :class:`OpenAICompatibleEmbedding` (SPEC §1.3 "嵌入统一").
2. **Keyword retriever** — a :class:`BaseRetriever` subclass
   (:class:`PgTrgmRetriever`) that runs a pg_trgm similarity search against
   the ``kb_chunks`` table (GIN index ``idx_kb_chunks_content_trgm``,
   migrated by US-005). The ``%`` operator + ``similarity()`` function are
   the pg_trgm primitives.
3. **Fusion** — :class:`QueryFusionRetriever` with ``num_queries=1`` (turns
   off LLM query generation) and ``mode='reciprocal_rerank'`` (RRF) merges
   the two result lists (SPEC §5.1).
4. **Parent backfill** — after fusion:
   * a *child* hit returns with its denormalized ``parent_content`` (written
     by ``chunk_service`` + ``embed_service``); if ``parent_content`` is
     missing the service falls back to fetching the parent chunk from
     ``kb_chunks`` (SPEC §5.1 "检索命中子块后回填父块上下文").
   * a *doc_summary* hit triggers backfill of that document's parent blocks
     from ``kb_chunks`` (SPEC §5.1 "摘要命中后回填该文档的父块").
5. **No-result** — when the max score is below ``score_threshold`` the
   service returns an empty result list rather than hallucinating (SPEC §5.4).

Heavy/optional deps (LlamaIndex, asyncpg) are imported lazily so the module
loads in unit-test environments where only stubs are present.
"""
from __future__ import annotations

import logging
import re
import uuid
from dataclasses import dataclass, field
from typing import Any, Protocol

from app.core.config import settings
from app.core.milvus import build_vector_store_index

logger = logging.getLogger(__name__)

# ── SPEC §5.1 retrieve_service parameters ────────────────────────────────────
# RRF mode string accepted by LlamaIndex QueryFusionRetriever.
# SPEC §5.1 names it "reciprocal_reranking"; LlamaIndex 0.14.x uses the enum
# FUSION_MODES.RECIPROCAL_RANK = "reciprocal_rerank" (the RRF implementation).
FUSION_MODE_RRF = "reciprocal_rerank"
# num_queries=1 disables LLM query generation (SPEC §5.1 / PRD US-014 AC1).
FUSION_NUM_QUERIES = 1
# Default top_k (may be overridden by the caller).
DEFAULT_TOP_K = 5
# Default score threshold (SPEC §5.4: below this → no-result).
DEFAULT_SCORE_THRESHOLD = 0.3


def _run_async(coro):
    """Run a coroutine synchronously, handling nested event loops.

    LlamaIndex's QueryFusionRetriever runs ``_aretrieve`` inside an event
    loop, so ``asyncio.run()`` fails with ``RuntimeError: This event loop
    is already running``. We fall back to ``nest_asyncio`` to allow
    nested loops.

    Detection uses the ``_running_loop`` attribute (set by asyncio when a
    loop is already running) rather than string matching, which is fragile
    across Python versions and platforms.
    """
    import asyncio
    try:
        return asyncio.run(coro)
    except RuntimeError as exc:
        # asyncio.get_running_loop() raises RuntimeError when no loop is
        # running — that means the error came from the coroutine itself,
        # not from nested-loop detection, so re-raise.
        try:
            asyncio.get_running_loop()
        except RuntimeError:
            raise
        import nest_asyncio
        nest_asyncio.apply()
        return asyncio.run(coro)


# Minimal CJK stop-words dropped from segmented keyword queries. These are the
# common auxiliary/empty words that add no trigram signal to a pg_trgm match.
_CJK_STOPWORDS = frozenset(
    "的了是在有和与及或而并且为把我你他她它我们他们一个不也和这那么"
    "什么怎么怎么样如何哪些哪些什么原理提供支持包含需要要求请问请回答"
    "跟和之间同时进行以及从而因此所以也可以能够应该更就得"
)


def _tokenize_cn_keywords(query: str) -> list[str]:
    """Chinese-tokenize a keyword query for pg_trgm matching.

    Keyword retrieval against a full CJK sentence fails because matching the
    whole sentence as one trigram blob against a short child chunk dilutes the
    exact keyword (e.g. "混合检索" inside "…作业调度能力与混合检索原理…"),
    letting irrelevant chunks outrank the correct one. We therefore segment the
    query and return the meaningful tokens to match individually.

    Uses jieba when available (fast, glues the project's no-extra-dep goal);
    falls back to a punctuation/whitespace split otherwise. Tokens shorter than
    2 CJK chars, pure ASCII and stop-words are dropped; duplicates are removed.
    """
    try:
        import jieba
        raw_tokens = jieba.lcut(query) if query else []
    except Exception:  # noqa: BLE001  (jieba import/init failure → fallback)
        raw_tokens = re.split(r"[，。？！、；：,\s（）()\[\]{}<>「」『』\"'|/\\\dA-Za-z]", query)
    tokens: list[str] = []
    seen: set[str] = set()
    for tok in raw_tokens:
        t = tok.strip()
        if len(t) < 2:
            continue
        if t in _CJK_STOPWORDS:
            continue
        # keep only tokens containing CJK ideographs
        if not re.search(r"[\u4e00-\u9fff]", t):
            continue
        if t in seen:
            continue
        seen.add(t)
        tokens.append(t)
    return tokens


async def _execute_pg_trgm_search_tx(
    conn,
    query: str,
    kb_id: str,
    tenant_id: str,
    top_k: int,
) -> list[dict[str, Any]]:
    """Run the pg_trgm keyword search inside an explicit transaction.

    ``set_config(..., true)`` (is_local) only lives for the current
    transaction, and asyncpg commits each standalone statement by default —
    so the GUCs (RLS tenant context + ``pg_trgm.similarity_threshold``) would
    be reset before the SELECT runs. Wrapping the set + query in a single
    explicit transaction guarantees they are still in effect when the query
    executes.

    Chinese keyword matching needs tokenization: matching a full CJK sentence
    as one trigram blob against a ~360-char child chunk dilutes the exact
    keyword's weight (e.g. "混合检索" inside a long query), which is exactly
    what caused keyword mode to miss the correct chunk. The query is therefore
    segmented with jieba into semantic tokens and each token is scored with
    pg_trgm ``similarity()`` against CHILD chunks only. The per-chunk score is
    normalized to 0~1 query-token coverage (matched tokens / total query
    tokens), so it is comparable to the cosine score reported by the
    vector/hybrid paths and to the configured ``score_threshold`` — unlike the
    raw pg_trgm similarity sum (~0.06), which cannot be meaningfully compared
    against a threshold. ``parent_content`` is selected too so the caller can
    backfill the parent block (SPEC §5.1 parent backfill — match the child,
    return the parent's content).
    """
    tokens = _tokenize_cn_keywords(query)
    async with conn.transaction():
        await conn.execute(
            "SELECT set_config('app.current_tenant_id', $1, true)",
            tenant_id,
        )
        await conn.execute(
            "SELECT set_config('pg_trgm.similarity_threshold', '0.0', true)"
        )
        if not tokens:
            return []
        n_tokens = len(tokens)
        params: list[Any] = []
        where: list[str] = []
        score_exprs: list[str] = []
        hit_exprs: list[str] = []
        for i, tok in enumerate(tokens, start=1):
            params.append(tok)
            where.append(f"content % ${i}")
            score_exprs.append(f"coalesce(similarity(content, ${i}), 0)")
            hit_exprs.append(f"({score_exprs[-1]} > 0)::int")
        sum_sql = "(" + " + ".join(score_exprs) + ")"
        hits_sql = "(" + " + ".join(hit_exprs) + ")"
        rows = await conn.fetch(
            f"""
            SELECT id::text AS chunk_id, content, parent_content,
                   parent_chunk_id::text AS parent_chunk_id,
                   doc_id::text AS doc_id, file_name, page_number,
                   content_type, chunk_type,
                   {sum_sql} AS sum_sim,
                   {hits_sql} AS n_hits
            FROM kb_chunks
            WHERE kb_id = ${len(tokens) + 1} AND tenant_id = ${len(tokens) + 2}
              AND chunk_type = 'child'
              AND ({ " OR ".join(where) })
            ORDER BY n_hits DESC, sum_sim DESC
            LIMIT ${len(tokens) + 3}
            """,
            *(params + [uuid.UUID(kb_id), uuid.UUID(tenant_id), top_k]),
        )
        # Normalize to a 0~1 relevance comparable to the cosine score the
        # vector/hybrid paths report and to the configured score_threshold:
        # score = query-token coverage (fraction of query keywords the chunk
        # matches). A chunk hitting every query keyword → ~1.0; hitting none
        # → 0. Contrary to the raw pg_trgm similarity sum (~0.06), coverage
        # makes the keyword score meaningful against score_threshold.
        out: list[dict[str, Any]] = []
        for r in rows:
            d = dict(r)
            d["score"] = min(1.0, (d["n_hits"] or 0) / n_tokens)
            out.append(d)
        return out


# ── Retrieved source (mirrors proto SourceChunk + parent_content) ─────────────


@dataclass
class RetrievedSource:
    """A single retrieved chunk with parent-block context backfilled.

    Mirrors ``kb_service.proto`` ``SourceChunk`` plus ``parent_content``
    (SPEC §5.1 parent backfill) so ``qa_service`` can build a richer prompt
    and the gRPC ``Query`` RPC can return the cited passage + its context.
    """

    chunk_id: str
    doc_id: str
    file_name: str
    page: int | None
    content: str
    score: float
    chunk_type: str  # child | parent | doc_summary
    parent_content: str = ""
    parent_chunk_id: str = ""


# ── pg_trgm keyword retriever (BaseRetriever subclass) ───────────────────────


class _SearchFn(Protocol):
    """Sync callable returning pg_trgm rows for a query.

    Encapsulates the asyncpg call so :class:`PgTrgmRetriever` stays a real
    :class:`BaseRetriever` subclass while remaining unit-testable (tests
    inject a fake ``search_fn``). Row keys mirror :data:`RetrievedSource`.

    When ``tenant_id`` is passed, it overrides the RLS tenant context bound
    at ``make_pg_trgm_search_fn`` construction time (multi-tenant support).
    """

    def __call__(
        self, query: str, *, top_k: int, tenant_id: str = "", kb_id: str = ""
    ) -> list[dict[str, Any]]: ...


def make_pg_trgm_search_fn(
    pool_or_dsn: Any,
    *,
    kb_id: str,
    tenant_id: str,
) -> _SearchFn:
    """Build a sync ``search_fn`` for pg_trgm keyword retrieval.

    Args:
        pool_or_dsn: Either an asyncpg Pool (binds to the loop it was created
            on) or a DSN string (creates a fresh connection per search, binds
            to the current loop). A DSN is preferred when the search may run
            inside LlamaIndex's async fusion retriever (which runs in a
            separate thread/loop), since an asyncpg pool cannot be shared
            across event loops.
        kb_id: The KB to scope the search to. Acts as the default; each call
            may override it with the ``kb_id`` kwarg (the per-request KB from
            the retriever), so a single shared search_fn can serve multiple
            KBs in the production singleton service.
        tenant_id: RLS tenant context (default; overridable per call).

    The pg_trgm query uses the ``%`` similarity operator (GIN index
    ``idx_kb_chunks_content_trgm``) and the ``similarity()`` function for
    scoring (SPEC §8.3). The returned rows carry the fields needed to build
    :class:`RetrievedSource` and :class:`NodeWithScore`: ``chunk_id, content,
    parent_content, doc_id, file_name, page_number, content_type, chunk_type,
    score``.
    """
    is_dsn = isinstance(pool_or_dsn, str)

    async def _async_search(
        query: str, top_k: int, tid: str = "", kid: str = ""
    ) -> list[dict[str, Any]]:
        # Use per-call tenant_id/kb_id overrides if provided, else fall back
        # to the values bound at construction time (multi-KB / multi-tenant
        # support for the production singleton service).
        effective_tid = tid or tenant_id
        effective_kid = kid or kb_id
        if is_dsn:
            import asyncpg as _asyncpg
            conn = await _asyncpg.connect(dsn=pool_or_dsn)
            try:
                return await _execute_pg_trgm_search_tx(
                    conn, query, effective_kid, effective_tid, top_k
                )
            finally:
                await conn.close()
        else:
            async with pool_or_dsn.acquire() as conn:
                return await _execute_pg_trgm_search_tx(
                    conn, query, effective_kid, effective_tid, top_k
                )

    def _search(
        query: str, *, top_k: int, tenant_id: str = "", kb_id: str = ""
    ) -> list[dict[str, Any]]:
        return _run_async(_async_search(query, top_k, tenant_id, kb_id))

    return _search


def _build_pg_trgm_retriever(search_fn: _SearchFn, *, top_k: int, tenant_id: str = "", kb_id: str = ""):
    """Construct a LlamaIndex :class:`BaseRetriever` subclass for pg_trgm.

    The subclass wraps the sync ``search_fn`` and converts each row into a
    :class:`NodeWithScore` with schema-aligned metadata (SPEC §3.1) so the
    fusion retriever can merge vector + keyword results uniformly.
    """
    from llama_index.core.retrievers import BaseRetriever
    from llama_index.core.schema import NodeWithScore, TextNode, QueryBundle

    class PgTrgmRetriever(BaseRetriever):
        """pg_trgm keyword retriever (SPEC §5.1 / PRD US-014 AC1).

        A real :class:`BaseRetriever` subclass — not a duck-typed object — so
        :class:`QueryFusionRetriever` accepts it in its ``retrievers`` list
        (LlamaIndex validates ``isinstance(r, BaseRetriever)``).
        """

        def __init__(self, search: _SearchFn, *, top_k: int, tenant_id: str = "", kb_id: str = "") -> None:
            super().__init__()
            self._search = search
            self._top_k = top_k
            self._tenant_id = tenant_id
            self._kb_id = kb_id

        def _retrieve(self, query_bundle: QueryBundle) -> list[NodeWithScore]:
            query = getattr(query_bundle, "query_str", None) or str(query_bundle)
            rows = self._search(
                query,
                top_k=self._top_k,
                tenant_id=self._tenant_id,
                kb_id=self._kb_id,
            )
            nodes: list[NodeWithScore] = []
            for row in rows:
                node = TextNode(
                    id_=row["chunk_id"],
                    text=row["content"],
                    metadata={
                        "doc_id": row["doc_id"],
                        "kb_id": self._kb_id,
                        "tenant_id": self._tenant_id,
                        "chunk_id": row["chunk_id"],
                        "chunk_type": row.get("chunk_type", "child"),
                        "file_name": row.get("file_name", ""),
                        "page_number": row.get("page_number") or 0,
                        "content_type": row.get("content_type", "text"),
                        "parent_content": row.get("parent_content") or "",
                        "parent_chunk_id": (row.get("parent_chunk_id") and str(row["parent_chunk_id"])) or "",
                    },
                )
                nodes.append(NodeWithScore(node=node, score=float(row["score"] or 0.0)))
            return nodes

        async def _aretrieve(self, query_bundle: QueryBundle) -> list[NodeWithScore]:
            return self._retrieve(query_bundle)

    return PgTrgmRetriever(search_fn, top_k=top_k, tenant_id=tenant_id, kb_id=kb_id)


# ── Parent backfill helpers (SPEC §5.1) ──────────────────────────────────────


async def _query_parents(conn, doc_id: str, tenant_id: str) -> list[dict[str, Any]]:
    await conn.execute(
        "SELECT set_config('app.current_tenant_id', $1, true)",
        tenant_id,
    )
    rows = await conn.fetch(
        """
        SELECT id::text AS chunk_id, content, parent_content,
               doc_id::text AS doc_id, file_name, page_number,
               content_type, chunk_type
        FROM kb_chunks
        WHERE doc_id = $1 AND chunk_type = 'parent'
        ORDER BY id
        """,
        uuid.UUID(doc_id),
    )
    return [dict(r) for r in rows]


async def _query_one(conn, parent_chunk_id: str, tenant_id: str) -> dict[str, Any] | None:
    await conn.execute(
        "SELECT set_config('app.current_tenant_id', $1, true)",
        tenant_id,
    )
    row = await conn.fetchrow(
        """
        SELECT id::text AS chunk_id, content, parent_content,
               doc_id::text AS doc_id, file_name, page_number,
               content_type, chunk_type
        FROM kb_chunks
        WHERE id = $1 AND chunk_type = 'parent'
        """,
        uuid.UUID(parent_chunk_id),
    )
    return dict(row) if row else None


class _ParentLookupFn(Protocol):
    """Fetch parent blocks for backfill from kb_chunks.

    ``lookup_parents(doc_id)`` returns parent chunks for a doc;
    ``lookup_parent(parent_chunk_id)`` returns a single parent chunk.
    Both are sync so the retrieve path stays simple (the gRPC server is sync).
    """

    def lookup_parents(self, doc_id: str) -> list[dict[str, Any]]: ...

    def lookup_parent(self, parent_chunk_id: str) -> dict[str, Any] | None: ...


def make_parent_lookup_fn(pool_or_dsn: Any, *, tenant_id: str) -> _ParentLookupFn:
    """Build a sync parent-lookup helper from an asyncpg pool or DSN string.

    Used by the backfill path when a hit's ``parent_content`` is missing
    (child hit without denormalized parent text) or when a doc_summary hit
    needs the document's parent blocks (SPEC §5.1).

    Accepts either an asyncpg Pool (binds to the loop it was created on) or
    a DSN string (creates a fresh connection per lookup, consistent with
    ``make_pg_trgm_search_fn``).
    """
    is_dsn = isinstance(pool_or_dsn, str)

    async def _parents(doc_id: str) -> list[dict[str, Any]]:
        if is_dsn:
            import asyncpg as _asyncpg
            conn = await _asyncpg.connect(dsn=pool_or_dsn)
            try:
                return await _query_parents(conn, doc_id, tenant_id)
            finally:
                await conn.close()
        else:
            async with pool_or_dsn.acquire() as conn:
                return await _query_parents(conn, doc_id, tenant_id)

    async def _one(parent_chunk_id: str) -> dict[str, Any] | None:
        if is_dsn:
            import asyncpg as _asyncpg
            conn = await _asyncpg.connect(dsn=pool_or_dsn)
            try:
                return await _query_one(conn, parent_chunk_id, tenant_id)
            finally:
                await conn.close()
        else:
            async with pool_or_dsn.acquire() as conn:
                return await _query_one(conn, parent_chunk_id, tenant_id)

    class _Lookup:
        def lookup_parents(self, doc_id: str) -> list[dict[str, Any]]:
            return _run_async(_parents(doc_id))

        def lookup_parent(self, parent_chunk_id: str) -> dict[str, Any] | None:
            return _run_async(_one(parent_chunk_id))

    return _Lookup()


def _node_to_source(node_with_score: Any) -> RetrievedSource:
    """Convert a LlamaIndex ``NodeWithScore`` to a :class:`RetrievedSource`."""
    node = node_with_score.node
    meta = getattr(node, "metadata", {}) or {}
    return RetrievedSource(
        chunk_id=getattr(node, "id_", "") or meta.get("chunk_id", ""),
        doc_id=meta.get("doc_id", ""),
        file_name=meta.get("file_name", ""),
        page=meta.get("page_number") or None,
        content=getattr(node, "get_content", lambda: meta.get("content", ""))(),
        score=float(getattr(node_with_score, "score", 0.0) or 0.0),
        chunk_type=meta.get("chunk_type", "child"),
        parent_content=meta.get("parent_content", "") or "",
        parent_chunk_id=meta.get("parent_chunk_id", "") or "",
    )


def _backfill_parent_for_child(
    source: RetrievedSource,
    node: Any,
    lookup: _ParentLookupFn | None,
) -> RetrievedSource:
    """Backfill ``parent_content`` for a child hit (SPEC §5.1).

    The write path denormalizes the parent block full text into every child's
    ``parent_content`` metadata (``chunk_service`` + ``embed_service``), so
    the common case is a no-op. When the metadata is empty (e.g. legacy
    chunks or a partial write), fall back to fetching the parent chunk from
    ``kb_chunks`` via ``lookup.lookup_parent(parent_chunk_id)``.
    """
    if source.parent_content:
        return source
    if lookup is None:
        return source
    meta = getattr(node, "metadata", {}) or {}
    parent_chunk_id = meta.get("parent_chunk_id") or ""
    if not parent_chunk_id:
        return source
    parent = lookup.lookup_parent(parent_chunk_id)
    if parent:
        source.parent_content = parent.get("content", "") or ""
    return source


def _backfill_parents_for_summary(
    source: RetrievedSource,
    lookup: _ParentLookupFn | None,
) -> RetrievedSource:
    """Backfill the document's parent blocks for a doc_summary hit (SPEC §5.1).

    Concatenates all parent blocks of the same ``doc_id`` into
    ``parent_content`` so the QA prompt carries the document's structural
    context, not just the summary.
    """
    if not source.doc_id or lookup is None:
        return source
    parents = lookup.lookup_parents(source.doc_id)
    if parents:
        source.parent_content = "\n".join(p.get("content", "") for p in parents if p.get("content"))
    return source


def _return_parent_and_dedup(sources: list[RetrievedSource]) -> list[RetrievedSource]:
    """Surface the parent block and collapse children of the same parent.

    Per SPEC §5.1 parent-child retrieval, we match *child* segments but return
    the *parent* block so the cited passage carries full context. Multiple
    children of the same parent (e.g. a table + its caption, or several
    sentences of one heading) would otherwise return the same parent text
    several times; this dedups them keeping the highest-scoring child.
    """
    finalized: list[RetrievedSource] = []
    best: dict[str, RetrievedSource] = {}
    for src in sources:
        # Return the parent block as the source content (fall back to the
        # child's own text when no parent block exists).
        if src.chunk_type == "child" and src.parent_content:
            src.content = src.parent_content
        key = src.parent_chunk_id or ""
        if not key or src.chunk_type != "child":
            finalized.append(src)
            continue
        if key not in best or src.score > best[key].score:
            best[key] = src
    finalized.extend(best.values())
    return finalized


# ── RetrieveService ──────────────────────────────────────────────────────────


@dataclass
class RetrieveResult:
    """Outcome of a hybrid retrieve call."""

    sources: list[RetrievedSource] = field(default_factory=list)
    max_score: float = 0.0
    fusion_retriever: Any = None  # exposed for qa_service reuse


class RetrieveService:
    """Hybrid retrieval orchestrator (SPEC §5.1 / PRD US-014).

    Builds the :class:`QueryFusionRetriever` from a vector retriever (Milvus
    via ``VectorStoreIndex.as_retriever()``) and a keyword retriever
    (:class:`PgTrgmRetriever`), runs RRF fusion with ``num_queries=1``, then
    backfills parent-block context for child + doc_summary hits.

    Args:
        embed_model: Shared LlamaIndex embedding model. When ``None`` the
            global singleton is resolved so write + query embeddings match
            (SPEC §1.3).
        keyword_search_fn: Sync pg_trgm search callable (see
            :func:`make_pg_trgm_search_fn`). When ``None`` keyword retrieval
            is disabled and only the vector retriever is used — this keeps
            the service usable in environments without a live PG connection
            (e.g. unit tests, or when kb_chunks is not yet populated).
        parent_lookup: Optional parent-lookup helper for backfill (see
            :func:`make_parent_lookup_fn`). When ``None`` backfill relies
            solely on the denormalized ``parent_content`` metadata written
            by ``chunk_service`` + ``embed_service``.
    """

    def __init__(
        self,
        *,
        embed_model: Any | None = None,
        keyword_search_fn: _SearchFn | None = None,
        parent_lookup: _ParentLookupFn | None = None,
        llm: Any | None = None,
    ) -> None:
        self._embed_model = embed_model
        self._keyword_search_fn = keyword_search_fn
        self._parent_lookup = parent_lookup
        # LlamaIndex QueryFusionRetriever pre-resolves a default LLM in its
        # __init__ even when num_queries=1 (no query generation). We pass this
        # LLM to avoid the constructor trying to load a default OpenAI LLM
        # (which fails without OPENAI_API_KEY). The LLM is unused for fusion
        # with num_queries=1.
        self._llm = llm

    # ── public accessors (used by QAService) ──────────────────────────────────

    @property
    def parent_lookup(self) -> _ParentLookupFn | None:
        """Expose the parent-lookup helper so QAService can reuse it for
        backfill when extracting sources from ``engine.chat()`` response."""
        return self._parent_lookup

    def set_llm(self, llm: Any) -> None:
        """Inject the LLM used by QueryFusionRetriever (avoids the constructor
        resolving a default OpenAI LLM when ``num_queries=1``). Called by
        QAService before building the fusion retriever."""
        self._llm = llm

    @property
    def has_llm(self) -> bool:
        """Whether an LLM has been injected (QAService checks this before
        calling ``set_llm`` to avoid overwriting an existing one)."""
        return self._llm is not None

    # ── fusion retriever factory ─────────────────────────────────────────────

    def build_fusion_retriever(
        self,
        *,
        kb_id: str,
        top_k: int = DEFAULT_TOP_K,
        dim: int | None = None,
        tenant_id: str = "",
    ):
        """Build the :class:`QueryFusionRetriever` for a KB (SPEC §5.1).

        * Vector retriever: ``VectorStoreIndex.from_vector_store(...).as_retriever()``
          (Milvus collection ``kb_{kb_id 去横杠}``).
        * Keyword retriever: :class:`PgTrgmRetriever` (pg_trgm over kb_chunks).
        * Fusion: ``QueryFusionRetriever(retrievers=[...], num_queries=1,
          mode='reciprocal_rerank')``.
        """
        from llama_index.core.retrievers import QueryFusionRetriever

        index = build_vector_store_index(
            kb_id,
            dim=dim if dim is not None else settings.embedding_dim,
            embed_model=self._embed_model,
        )
        vector_retriever = index.as_retriever(similarity_top_k=top_k)

        retrievers = [vector_retriever]
        if self._keyword_search_fn is not None:
            keyword_retriever = _build_pg_trgm_retriever(
                self._keyword_search_fn, top_k=top_k, tenant_id=tenant_id, kb_id=kb_id
            )
            retrievers.append(keyword_retriever)

        fusion = QueryFusionRetriever(
            retrievers=retrievers,
            num_queries=FUSION_NUM_QUERIES,  # disable LLM query generation
            mode=FUSION_MODE_RRF,            # RRF
            llm=self._llm,  # avoid constructor resolving a default OpenAI LLM
            # Force sync retrieval path — avoids "Event loop is closed" when
            # called via asyncio.to_thread (the async path calls
            # MilvusVectorStore.aquery which needs AsyncMilvusClient, whose
            # grpc.aio channel binds to a loop that is closed when the
            # to_thread worker exits).
            use_async=False,
        )
        return fusion

    def build_vector_retriever(
        self,
        *,
        kb_id: str,
        top_k: int = DEFAULT_TOP_K,
        dim: int | None = None,
    ):
        """Build a vector-only retriever (Milvus cosine) for a KB.

        Used by ``vector_retrieve`` and by ``QAService.chat`` when the KB's
        ``retrieval_mode`` is ``vector`` (SPEC §5.1 向量检索).
        """
        index = build_vector_store_index(
            kb_id,
            dim=dim if dim is not None else settings.embedding_dim,
            embed_model=self._embed_model,
        )
        return index.as_retriever(similarity_top_k=top_k)

    def build_keyword_retriever(
        self,
        *,
        kb_id: str,
        top_k: int = DEFAULT_TOP_K,
        tenant_id: str = "",
    ):
        """Build a keyword-only (pg_trgm) retriever for a KB.

        Used by ``keyword_retrieve`` and by ``QAService.chat`` when the KB's
        ``retrieval_mode`` is ``keyword`` (SPEC §5.1 全文检索). Raises
        ``RuntimeError`` when no ``keyword_search_fn`` was injected.
        """
        if self._keyword_search_fn is None:
            raise RuntimeError(
                "keyword retrieval requires a keyword_search_fn; construct "
                "RetrieveService with keyword_search_fn=make_pg_trgm_search_fn(...)"
            )
        return _build_pg_trgm_retriever(
            self._keyword_search_fn, top_k=top_k, tenant_id=tenant_id, kb_id=kb_id
        )

    def vector_max_similarity(
        self,
        *,
        kb_id: str,
        question: str,
        top_k: int = DEFAULT_TOP_K,
        dim: int | None = None,
    ) -> float:
        """Return the best vector-similarity score for ``question``.

        The RRF fusion scores (rank-based, ~0.016) are NOT comparable to a
        cosine-similarity ``score_threshold``, so the no-hallucination gate
        must be evaluated against the actual vector similarity from the
        Milvus retriever (SPEC §5.4 "max_score < score_threshold → no-result"
        where ``max_score`` is a similarity). This helper returns that value.
        """
        index = build_vector_store_index(
            kb_id,
            dim=dim if dim is not None else settings.embedding_dim,
            embed_model=self._embed_model,
        )
        vector_retriever = index.as_retriever(similarity_top_k=top_k)
        try:
            nodes = vector_retriever.retrieve(question)
        except Exception as exc:  # noqa: BLE001
            logger.debug("vector_max_similarity failed: %s", exc)
            return 0.0
        if not nodes:
            return 0.0
        return max(
            (float(getattr(nws, "score", 0.0) or 0.0) for nws in nodes),
            default=0.0,
        )

    def vector_similarity_map(
        self,
        *,
        kb_id: str,
        question: str,
        top_k: int = DEFAULT_TOP_K,
        dim: int | None = None,
    ) -> dict[str, float]:
        """Return a ``{chunk_id: cosine_similarity}`` map for ``question``.

        Used by :class:`QAService` to produce readable 0~1 similarity scores
        for HYBRID results. Fusion mode returns RRF rank scores (~0.016),
        which are NOT comparable to a cosine ``score_threshold`` or to the
        ``score`` reported by vector/keyword modes. This helper maps each
        fused hit back to its actual Milvus cosine similarity (same
         similarity semantics as ``vector_retrieve``). Chunks that matched
        only via keyword (absent from the vector result) are simply not
        present in the returned map — the caller decides the fallback.
        """
        index = build_vector_store_index(
            kb_id,
            dim=dim if dim is not None else settings.embedding_dim,
            embed_model=self._embed_model,
        )
        vector_retriever = index.as_retriever(similarity_top_k=top_k)
        try:
            nodes = vector_retriever.retrieve(question)
        except Exception as exc:  # noqa: BLE001
            logger.debug("vector_similarity_map failed: %s", exc)
            return {}
        result: dict[str, float] = {}
        for nws in nodes:
            node = getattr(nws, "node", None)
            chunk_id = (
                getattr(node, "id_", "")
                or (getattr(node, "metadata", {}) or {}).get("chunk_id", "")
            )
            if not chunk_id:
                continue
            result[chunk_id] = float(getattr(nws, "score", 0.0) or 0.0)
        return result

    # ── retrieve ─────────────────────────────────────────────────────────────

    def retrieve(
        self,
        *,
        kb_id: str,
        question: str,
        top_k: int = DEFAULT_TOP_K,
        score_threshold: float = DEFAULT_SCORE_THRESHOLD,
        dim: int | None = None,
        tenant_id: str = "",
    ) -> RetrieveResult:
        """Run hybrid retrieval (vector + pg_trgm + RRF) + parent backfill.

        SPEC §5.1: QueryFusionRetriever merges the vector retriever (Milvus
        via ``VectorStoreIndex.as_retriever()``) and the keyword retriever
        (``PgTrgmRetriever``) with RRF (``num_queries=1``).

        Returns a :class:`RetrieveResult` with backfilled sources. When the
        max score is below ``score_threshold`` an empty result is returned
        (SPEC §5.4 no-result path — do not hallucinate).

        Args:
            tenant_id: RLS tenant context override (proto QueryRequest
                .tenant_id). When non-empty, the pg_trgm keyword retriever
                uses this tenant_id for RLS. When empty, falls back to the
                tenant_id bound at ``make_pg_trgm_search_fn`` construction.
        """
        fusion = self.build_fusion_retriever(
            kb_id=kb_id, top_k=top_k, dim=dim, tenant_id=tenant_id
        )
        nodes_with_scores = fusion.retrieve(question)
        return self._process_nodes(nodes_with_scores, fusion, score_threshold)

    # ── 单路向量检索（SPEC §5.1: MilvusVectorStore as retriever）────────────

    def vector_retrieve(
        self,
        *,
        kb_id: str,
        question: str,
        top_k: int = DEFAULT_TOP_K,
        score_threshold: float = DEFAULT_SCORE_THRESHOLD,
        dim: int | None = None,
    ) -> RetrieveResult:
        """Vector-only retrieval via Milvus (SPEC §5.1 向量检索).

        Uses ``VectorStoreIndex.from_vector_store(...).as_retriever()`` — the
        Index layer embeds the query via the shared embedding model then
        searches Milvus HNSW/COSINE. No keyword (pg_trgm) participation.

        Returns a :class:`RetrieveResult` with parent-backfilled sources.
        Below ``score_threshold`` → no-result (SPEC §5.4).
        """
        vector_retriever = self.build_vector_retriever(kb_id=kb_id, top_k=top_k, dim=dim)
        nodes_with_scores = vector_retriever.retrieve(question)
        return self._process_nodes(nodes_with_scores, vector_retriever, score_threshold)

    # ── 单路全文检索（SPEC §5.1: pg_trgm 关键词检索）────────────────────────

    def keyword_retrieve(
        self,
        *,
        kb_id: str,
        question: str,
        top_k: int = DEFAULT_TOP_K,
        score_threshold: float = DEFAULT_SCORE_THRESHOLD,
        tenant_id: str = "",
    ) -> RetrieveResult:
        """Keyword-only (full-text) retrieval via pg_trgm (SPEC §5.1 全文检索).

        Uses :class:`PgTrgmRetriever` (a :class:`BaseRetriever` subclass) to
        run a pg_trgm similarity search against the ``kb_chunks`` table (GIN
        index ``idx_kb_chunks_content_trgm``). No vector (Milvus) participation.

        Returns a :class:`RetrieveResult` with parent-backfilled sources.
        Below ``score_threshold`` → no-result (SPEC §5.4).

        Raises ``RuntimeError`` when no ``keyword_search_fn`` was injected
        (the service needs a pg_trgm search callable to run keyword-only
        retrieval).
        """
        if self._keyword_search_fn is None:
            raise RuntimeError(
                "keyword_retrieve requires a keyword_search_fn; construct "
                "RetrieveService with keyword_search_fn=make_pg_trgm_search_fn(...)"
            )
        keyword_retriever = self.build_keyword_retriever(
            kb_id=kb_id, top_k=top_k, tenant_id=tenant_id
        )
        nodes_with_scores = keyword_retriever.retrieve(question)
        return self._process_nodes(nodes_with_scores, keyword_retriever, score_threshold)

    # ── 共享的节点处理 + 父块回填 + 阈值过滤 ─────────────────────────────────

    def _process_nodes(
        self,
        nodes_with_scores: list,
        retriever: Any,
        score_threshold: float,
    ) -> RetrieveResult:
        """Convert nodes → sources, apply parent backfill, filter by threshold.

        Shared by ``retrieve`` (fusion), ``vector_retrieve``, and
        ``keyword_retrieve`` so all three paths use the same parent-backfill
        and no-result logic (SPEC §5.1 + §5.4).
        """
        sources: list[RetrievedSource] = []
        for nws in nodes_with_scores:
            src = _node_to_source(nws)
            if src.chunk_type == "child":
                _backfill_parent_for_child(src, nws.node, self._parent_lookup)
            elif src.chunk_type == "doc_summary":
                _backfill_parents_for_summary(src, self._parent_lookup)
            sources.append(src)
        # Return parent blocks and collapse children of the same parent.
        sources = _return_parent_and_dedup(sources)

        if not sources:
            return RetrieveResult(sources=[], max_score=0.0, fusion_retriever=retriever)

        max_score = max(s.score for s in sources)
        # SPEC §5.4: below threshold → no-result (do not hallucinate).
        if max_score < score_threshold:
            logger.debug(
                "retrieve_service: max_score %.3f < threshold %.3f → no-result",
                max_score,
                score_threshold,
            )
            return RetrieveResult(sources=[], max_score=max_score, fusion_retriever=retriever)

        return RetrieveResult(
            sources=sources, max_score=max_score, fusion_retriever=retriever
        )


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit("retrieve_service is a library module; import from qa_service / grpc_server")
