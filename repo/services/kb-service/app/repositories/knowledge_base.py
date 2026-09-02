"""knowledge_bases repository (SPEC §2.4, §6.1).

Covers CRUD on the `knowledge_bases` table with RLS tenant filtering.
CreateKB also writes the Core vector-store id via the gateway client
(wired in the gRPC servicer layer, not here — this repository is pure data).
"""
from __future__ import annotations

import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


async def create_kb(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    name: str,
    description: str = "",
    embedding_model: str = "bge-m3",
    chunk_size: int = 1024,
    top_k: int = 5,
    score_threshold: float = 0.3,
    retrieval_mode: str = "hybrid",
) -> dict[str, Any]:
    """INSERT a new knowledge_bases row and return it.

    The `id` is generated server-side (gen_random_uuid). RLS context is set so
    the INSERT satisfies the restrictive tenant_isolation policy.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            INSERT INTO knowledge_bases
                (tenant_id, name, description, embedding_model,
                 chunk_size, top_k, score_threshold, retrieval_mode, status)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active')
            RETURNING id, tenant_id, name, description, embedding_model,
                      chunk_size, top_k, score_threshold, retrieval_mode,
                      status, doc_count, created_at, updated_at, vector_store_id
            """,
            uuid.UUID(tenant_id),
            name,
            description,
            embedding_model,
            chunk_size,
            top_k,
            score_threshold,
            retrieval_mode,
        )
    return dict(row)


async def set_vector_store_id(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    vector_store_id: str,
) -> None:
    """Persist the Core API vector-store id onto the knowledge_bases row.

    Called by CreateKB after the Core `POST /vector-stores` returns the
    vector store id (Plan §3.1). RLS-scoped.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.execute(
            """
            UPDATE knowledge_bases
               SET vector_store_id = $2,
                   updated_at = now()
             WHERE id = $1
            """,
            uuid.UUID(kb_id),
            vector_store_id,
        )


async def get_kb(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str
) -> dict[str, Any] | None:
    """SELECT a single knowledge_bases row by id (RLS-scoped).

    Soft-deleted rows (status='deleted') are filtered out so callers see a
    deleted KB as NOT_FOUND (SPEC §6.1 DeleteKB semantics; the row stays for
    audit but is hidden from all read paths).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT id, tenant_id, name, description, embedding_model,
                   chunk_size, top_k, score_threshold, retrieval_mode,
                   status, doc_count, created_at, updated_at, vector_store_id
              FROM knowledge_bases
             WHERE id = $1 AND status <> 'deleted'
            """,
            uuid.UUID(kb_id),
        )
    return dict(row) if row else None


async def list_kbs(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    limit: int = 20,
    cursor: str | None = None,
) -> tuple[list[dict[str, Any]], int]:
    """List knowledge_bases with cursor pagination (RLS-scoped).

    Returns (rows, total). Cursor is the `id` of the last row of the previous
    page (lexicographic UUID ordering). Soft-deleted rows (status='deleted')
    are excluded from both the page and the total.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        total = await conn.fetchval(
            "SELECT count(*) FROM knowledge_bases WHERE status <> 'deleted'"
        )
        if cursor:
            rows = await conn.fetch(
                """
                SELECT id, tenant_id, name, description, embedding_model,
                       chunk_size, top_k, score_threshold, retrieval_mode,
                       status, doc_count, created_at, updated_at, vector_store_id
                  FROM knowledge_bases
                 WHERE id > $1 AND status <> 'deleted'
                 ORDER BY id ASC
                 LIMIT $2
                """,
                uuid.UUID(cursor),
                limit,
            )
        else:
            rows = await conn.fetch(
                """
                SELECT id, tenant_id, name, description, embedding_model,
                       chunk_size, top_k, score_threshold, retrieval_mode,
                       status, doc_count, created_at, updated_at, vector_store_id
                  FROM knowledge_bases
                 WHERE status <> 'deleted'
                 ORDER BY id ASC
                 LIMIT $1
                """,
                limit,
            )
    return [dict(r) for r in rows], total


async def soft_delete_kb(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str
) -> bool:
    """Soft-delete a knowledge_base by setting status='deleted' (RLS-scoped).

    Returns True if a row was updated, False if not found (RLS hides other
    tenants' rows so NOT_FOUND is indistinguishable from cross-tenant).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        result = await conn.execute(
            """
            UPDATE knowledge_bases
               SET status = 'deleted', updated_at = now()
             WHERE id = $1 AND status <> 'deleted'
            """,
            uuid.UUID(kb_id),
        )
    return result == "UPDATE 1"


async def get_kb_status(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str
) -> str | None:
    """Return the KB status (for rebuild precondition checks)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        return await conn.fetchval(
            "SELECT status FROM knowledge_bases WHERE id = $1",
            uuid.UUID(kb_id),
        )


async def increment_doc_count(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str, delta: int = 1
) -> None:
    """Increment/decrement doc_count atomically (RLS-scoped)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.execute(
            """
            UPDATE knowledge_bases
               SET doc_count = doc_count + $2,
                   updated_at = now()
             WHERE id = $1
            """,
            uuid.UUID(kb_id),
            delta,
        )
