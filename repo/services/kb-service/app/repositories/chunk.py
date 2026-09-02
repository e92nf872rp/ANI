"""kb_chunks repository (SPEC §2.4, §3.1, §8.1).

kb-service owns kb_chunks: it reads them for keyword (pg_trgm) retrieval
(FR-7 mixed retrieval) and, after the RAG architecture refactor (Plan §3.2),
also writes them. This repository exposes read + keyword-search + batch
write/delete APIs.

keyword_search was refactored (Plan §3.2) to use jieba tokenization +
multi-token OR + token-coverage normalization, matching rag-engine's
PgTrgmRetriever behavior. write_chunks/delete_chunks_by_doc were migrated
from rag-engine's `repositories/chunks.py`.
"""
from __future__ import annotations

import json
import re
import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context

# Minimal CJK stop-words dropped from segmented keyword queries. These are the
# common auxiliary/empty words that add no trigram signal to a pg_trgm match.
# Mirrors rag-engine `_CJK_STOPWORDS` (retrieve_service.py) so the refactored
# kb-service keyword_search matches rag-engine PgTrgmRetriever behavior.
_CJK_STOPWORDS = frozenset(
    "的了是在有和与及或而并且为把我你他她它我们他们一个不也和这那么"
    "什么怎么怎么样如何哪些哪些什么原理提供支持包含需要要求请问请回答"
    "跟和之间同时进行以及从而因此所以也可以能够应该更就得"
)

_CJK_RE = re.compile(r"[\u4e00-\u9fff]")


def _tokenize_cn_keywords(query: str) -> list[str]:
    """Chinese-tokenize a keyword query for pg_trgm matching.

    Ported verbatim from rag-engine `retrieve_service._tokenize_cn_keywords`
    (Plan §3.2 requires matching behavior). Tokens shorter than 2 CJK chars,
    pure ASCII / non-CJK, and stop-words are dropped; duplicates removed
    (order-preserving). Uses jieba.lcut with a punctuation-split fallback when
    jieba is unavailable.
    """
    try:
        import jieba

        raw_tokens = jieba.lcut(query) if query else []
    except Exception:  # noqa: BLE001  (jieba import/init failure → fallback)
        raw_tokens = re.split(
            r"[，。？！、；：,\s（）()\[\]{}<>「」『』\"'|/\\\dA-Za-z]", query
        )
    tokens: list[str] = []
    seen: set[str] = set()
    for tok in raw_tokens:
        t = tok.strip()
        if len(t) < 2:
            continue
        if t in _CJK_STOPWORDS:
            continue
        # keep only tokens containing CJK ideographs
        if not _CJK_RE.search(t):
            continue
        if t in seen:
            continue
        seen.add(t)
        tokens.append(t)
    return tokens


async def get_chunk(
    conn: asyncpg.Connection, *, tenant_id: str, chunk_id: str
) -> dict[str, Any] | None:
    """SELECT a single kb_chunks row by id (RLS-scoped)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT id, tenant_id, kb_id, doc_id, parent_chunk_id, chunk_type,
                   content, parent_content, page_number, content_type,
                   file_name, token_count, custom_metadata, created_at
              FROM kb_chunks
             WHERE id = $1
            """,
            uuid.UUID(chunk_id),
        )
    return dict(row) if row else None


async def list_chunks_by_doc(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    doc_id: str,
    limit: int = 100,
) -> list[dict[str, Any]]:
    """List all chunks for a document (RLS-scoped)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        rows = await conn.fetch(
            """
            SELECT id, tenant_id, kb_id, doc_id, parent_chunk_id, chunk_type,
                   content, parent_content, page_number, content_type,
                   file_name, token_count, custom_metadata, created_at
              FROM kb_chunks
             WHERE kb_id = $1 AND doc_id = $2
             ORDER BY created_at ASC
             LIMIT $3
            """,
            uuid.UUID(kb_id),
            uuid.UUID(doc_id),
            limit,
        )
    return [dict(r) for r in rows]


async def keyword_search(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    query: str,
    limit: int = 10,
) -> list[dict[str, Any]]:
    """Keyword search on kb_chunks.content using pg_trgm + jieba (RLS-scoped).

    [Plan §3.2 refactor] jieba tokenization + multi-token OR + token-coverage
    normalization, matching rag-engine's `PgTrgmRetriever`
    (`retrieve_service.py` `_execute_pg_trgm_search_tx`) behavior:
      - tokenize the query with jieba (dedup, order-preserving)
      - for each token build `content % $i` (trigram similarity) and accumulate
        `similarity(content, $i)` into sum_sim and `(... > 0)::int` into n_hits
      - score = min(1.0, n_hits / n_tokens)  (token coverage normalization)

    Signature unchanged. Returns rows with the same shape as before plus
    `score` (the normalized coverage score) instead of the raw `rank`.
    """
    # Tokenize (dedup, order-preserving, CJK-only, stop-words dropped) —
    # matches rag-engine's `_tokenize_cn_keywords` exactly.
    tokens = _tokenize_cn_keywords(query)
    if not tokens:
        return []
    n_tokens = len(tokens)

    # Parameter layout: tokens occupy $1..$n, kb_id=$(n+1),
    # tenant_id=$(n+2), limit=$(n+3).
    params: list[Any] = []
    where: list[str] = []
    score_exprs: list[str] = []
    hit_exprs: list[str] = []
    for i, tok in enumerate(tokens, start=1):
        params.append(tok)
        where.append(f"content % ${i}")
        sim_expr = f"coalesce(similarity(content, ${i}), 0)"
        score_exprs.append(sim_expr)
        hit_exprs.append(f"({sim_expr} > 0)::int")
    sum_sql = "(" + " + ".join(score_exprs) + ")"
    hits_sql = "(" + " + ".join(hit_exprs) + ")"

    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.execute(
            "SELECT set_config('pg_trgm.similarity_threshold', '0.0', true)"
        )
        rows = await conn.fetch(
            f"""
            SELECT id::text AS chunk_id, content, parent_content,
                   parent_chunk_id::text AS parent_chunk_id,
                   doc_id::text AS doc_id, file_name, page_number,
                   content_type, chunk_type,
                   {sum_sql} AS sum_sim,
                   {hits_sql} AS n_hits
              FROM kb_chunks
             WHERE kb_id = ${n_tokens + 1} AND tenant_id = ${n_tokens + 2}
               AND chunk_type = 'child'
               AND ({" OR ".join(where)})
            ORDER BY n_hits DESC, sum_sim DESC
            LIMIT ${n_tokens + 3}
            """,
            *(params + [uuid.UUID(kb_id), uuid.UUID(tenant_id), limit]),
        )
        # Normalize: score = min(1.0, n_hits / n_tokens) (rag-engine line 209).
        out: list[dict[str, Any]] = []
        for r in rows:
            d = dict(r)
            d["score"] = min(1.0, (d["n_hits"] or 0) / n_tokens)
            out.append(d)
        return out


async def count_chunks_by_doc(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str, doc_id: str
) -> int:
    """Count chunks for a document (RLS-scoped)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        return await conn.fetchval(
            "SELECT count(*) FROM kb_chunks WHERE kb_id = $1 AND doc_id = $2",
            uuid.UUID(kb_id),
            uuid.UUID(doc_id),
        )


# ── write / delete (migrated from rag-engine repositories/chunks.py, Plan §3.2) ──


def _to_uuid(value: str, *, field: str) -> uuid.UUID:
    """Parse a UUID string with a clear error message."""
    try:
        return uuid.UUID(value)
    except (ValueError, AttributeError, TypeError) as exc:
        raise ValueError(f"invalid UUID for {field!r}: {value!r}") from exc


def _row(
    *,
    chunk_id: str,
    tenant_id: str,
    kb_id: str,
    doc_id: str,
    chunk_type: str,
    content: str,
    file_name: str,
    page_number: int | None = None,
    content_type: str | None = None,
    parent_chunk_id: str | None = None,
    parent_content: str | None = None,
    token_count: int | None = None,
    custom_metadata: dict[str, Any] | None = None,
) -> tuple:
    """Build one INSERT tuple bound to the kb_chunks column order."""
    return (
        _to_uuid(chunk_id, field="chunk_id"),
        _to_uuid(tenant_id, field="tenant_id"),
        _to_uuid(kb_id, field="kb_id"),
        _to_uuid(doc_id, field="doc_id"),
        _to_uuid(parent_chunk_id, field="parent_chunk_id") if parent_chunk_id else None,
        chunk_type,
        content,
        parent_content,
        page_number,
        content_type,
        file_name,
        token_count,
        json.dumps(custom_metadata or {}, default=str),
    )


_INSERT_SQL = """
INSERT INTO kb_chunks (
    id, tenant_id, kb_id, doc_id, parent_chunk_id, chunk_type,
    content, parent_content, page_number, content_type,
    file_name, token_count, custom_metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
"""


async def write_chunks(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    doc_id: str,
    file_name: str,
    parents: list[dict],
    children: list[dict],
    summaries: list[dict] | None = None,
) -> int:
    """Batched INSERT of parent + child (+ optional summary) chunks (RLS-scoped).

    Migrated from rag-engine `repositories/chunks.py` (Plan §3.2). Parents are
    inserted first (children reference their ``parent_chunk_id`` via FK), then
    children, then any doc-summary chunks. All inserts run inside a single
    transaction with the RLS tenant context set once.

    Each input dict is duck-typed: chunk_id, content, content_type,
    page_number, parent_chunk_id, parent_content, token_count, metadata.
    Returns the total number of inserted rows.
    """
    rows: list[tuple] = []
    # Parents first so child FK references resolve.
    for p in parents:
        rows.append(
            _row(
                chunk_id=p["chunk_id"],
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                chunk_type="parent",
                content=p["content"],
                file_name=file_name,
                page_number=p.get("page_number"),
                content_type=p.get("content_type"),
                parent_chunk_id=None,
                parent_content=None,
                token_count=p.get("token_count"),
                custom_metadata=p.get("metadata"),
            )
        )
    # Children: parent_chunk_id + parent_content denormalized (SPEC §5.1).
    for c in children:
        rows.append(
            _row(
                chunk_id=c["chunk_id"],
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                chunk_type="child",
                content=c["content"],
                file_name=file_name,
                page_number=c.get("page_number"),
                content_type=c.get("content_type"),
                parent_chunk_id=c.get("parent_chunk_id"),
                parent_content=c.get("parent_content"),
                token_count=c.get("token_count"),
                custom_metadata=c.get("metadata"),
            )
        )
    # Doc-summary chunks (chunk_type=doc_summary).
    for s in summaries or []:
        rows.append(
            _row(
                chunk_id=s["chunk_id"],
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                chunk_type="doc_summary",
                content=s["content"],
                file_name=file_name,
                page_number=s.get("page_number"),
                content_type=s.get("content_type"),
                parent_chunk_id=s.get("parent_chunk_id"),
                parent_content=s.get("parent_content"),
                token_count=s.get("token_count"),
                custom_metadata=s.get("metadata"),
            )
        )

    if not rows:
        return 0

    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.executemany(_INSERT_SQL, rows)
    return len(rows)


async def delete_chunks_by_doc(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    doc_id: str,
) -> int:
    """Delete all chunks for a document (RLS-scoped, in-transaction).

    Migrated from rag-engine `repositories/chunks.py` (Plan §3.2). Used by
    reparse (idempotency): clear prior chunks before re-writing. Returns the
    number of deleted rows.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        result = await conn.execute(
            "DELETE FROM kb_chunks WHERE kb_id = $1 AND doc_id = $2",
            _to_uuid(kb_id, field="kb_id"),
            _to_uuid(doc_id, field="doc_id"),
        )
    # asyncpg returns "DELETE N" — parse the count.
    try:
        return int(result.split()[-1])
    except (ValueError, IndexError):
        return 0
