"""kb_chunks repository (SPEC §2.4, §3.1, §8.1).

kb-service reads kb_chunks for keyword (pg_trgm) retrieval (FR-7 mixed
retrieval). Writes are performed by rag-engine's parse_worker; this
repository exposes read + keyword-search APIs.
"""
from __future__ import annotations

import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


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
    """Keyword search on kb_chunks.content using pg_trgm (RLS-scoped).

    Uses the GIN trigram index (idx_kb_chunks_content_trgm) via ILIKE for
    fuzzy keyword matching. Returns chunks ordered by similarity rank.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        rows = await conn.fetch(
            """
            SELECT id, tenant_id, kb_id, doc_id, parent_chunk_id, chunk_type,
                   content, parent_content, page_number, content_type,
                   file_name, token_count, custom_metadata, created_at,
                   similarity(content, $1) AS rank
              FROM kb_chunks
             WHERE kb_id = $2 AND content ILIKE '%' || $1 || '%'
             ORDER BY rank DESC
             LIMIT $3
            """,
            query,
            uuid.UUID(kb_id),
            limit,
        )
    return [dict(r) for r in rows]


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
