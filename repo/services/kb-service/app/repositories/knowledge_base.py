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
    embedding_model: str,
    chunk_size: int = 1024,
    top_k: int = 5,
    # 0 = 未设置（M3 契约）：查询链路对 0 直接透传，由 DEFAULT_SCORE_THRESHOLD
    # 兜底；调用方（grpc_server）显式传 request.score_threshold or 0.0。
    # 注意：不要改回 0.3 —— 那是与"0=未设置"语义矛盾的历史默认值。
    score_threshold: float = 0.0,
    retrieval_mode: str = "hybrid",
    default_inference_service: str = "",
) -> dict[str, Any]:
    """INSERT a new knowledge_bases row and return it.

    The `id` is generated server-side (gen_random_uuid). RLS context is set so
    the INSERT satisfies the restrictive tenant_isolation policy.

    `default_inference_service` is a plain TEXT config column (migration
    20260911000100): it only affects generation routing, never the vector
    index. Empty string is normalised to NULL ("not set"); the Query/Retrieve
    fallback chain resolves NULL to "" (rag-engine then applies
    ``settings.vllm_model``).
    """
    if not embedding_model:
        raise ValueError("embedding_model is required (empty means unset)")
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            INSERT INTO knowledge_bases
                (tenant_id, name, description, embedding_model,
                 chunk_size, top_k, score_threshold, retrieval_mode,
                 default_inference_service, status)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active')
            RETURNING id, tenant_id, name, description, embedding_model,
                      chunk_size, top_k, score_threshold, retrieval_mode,
                      default_inference_service, status,
                      0 AS doc_count, created_at, updated_at, vector_store_id
            """,
            uuid.UUID(tenant_id),
            name,
            description,
            embedding_model,
            chunk_size,
            top_k,
            score_threshold,
            retrieval_mode,
            default_inference_service or None,
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
                   chunk_size, ocr_enabled, top_k, score_threshold, retrieval_mode,
                   default_inference_service, status,
                   (SELECT count(*) FROM kb_documents d
                     WHERE d.kb_id = knowledge_bases.id
                       AND NOT (d.parse_status = 'failed'
                                AND d.error_message = 'deleted')) AS doc_count,
                   created_at, updated_at, vector_store_id
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
                       default_inference_service, status,
                       (SELECT count(*) FROM kb_documents d
                         WHERE d.kb_id = knowledge_bases.id
                           AND NOT (d.parse_status = 'failed'
                                    AND d.error_message = 'deleted')) AS doc_count,
                       created_at, updated_at, vector_store_id
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
                       default_inference_service, status,
                       (SELECT count(*) FROM kb_documents d
                         WHERE d.kb_id = knowledge_bases.id
                           AND NOT (d.parse_status = 'failed'
                                    AND d.error_message = 'deleted')) AS doc_count,
                       created_at, updated_at, vector_store_id
                  FROM knowledge_bases
                 WHERE status <> 'deleted'
                 ORDER BY id ASC
                 LIMIT $1
                """,
                limit,
            )
    return [dict(r) for r in rows], total


async def update_kb(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    name: str = "",
    description: str = "",
) -> dict[str, Any] | None:
    """Update a knowledge_base's name/description (RLS-scoped).

    Empty `name`/`description` mean "keep current value" (COALESCE+NULLIF
    semantics, SPEC §5.1). A name colliding with another KB in the same
    tenant raises asyncpg.UniqueViolationError (SQLSTATE 23505, UNIQUE
    (tenant_id, name)) — the servicer maps that to ALREADY_EXISTS.

    Returns the updated row, or None when the kb_id is not visible to this
    tenant (RLS hides cross-tenant rows, so NOT_FOUND is indistinguishable).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            UPDATE knowledge_bases
               SET name = COALESCE(NULLIF($2, ''), name),
                   description = COALESCE(NULLIF($3, ''), description),
                   updated_at = now()
             WHERE id = $1 AND status <> 'deleted'
            RETURNING id, tenant_id, name, description, embedding_model,
                      chunk_size, top_k, score_threshold, retrieval_mode,
                      default_inference_service, status,
                      (SELECT count(*) FROM kb_documents d
                        WHERE d.kb_id = knowledge_bases.id
                          AND NOT (d.parse_status = 'failed'
                                   AND d.error_message = 'deleted')) AS doc_count,
                      created_at, updated_at, vector_store_id
            """,
            uuid.UUID(kb_id),
            name,
            description,
        )
    return dict(row) if row else None


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


async def set_status_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    from_status: str,
    to_status: str,
) -> bool:
    """Atomically transition a KB status inside the caller's transaction.

    Conditional UPDATE ``WHERE status = $from``: when two rebuilds race,
    the first wins (active → rebuilding) and the loser's UPDATE matches
    0 rows — the servicer maps that to FAILED_PRECONDITION instead of a
    double rebuild. Also gates the rebuild consumer's exit transition
    (rebuilding → active) against a concurrent DeleteKB.

    Does NOT open its own transaction; commits atomically with the
    caller's other writes (RebuildKB: async_tasks + outbox + audit).
    """
    await set_tenant_context(conn, tenant_id)
    res = await conn.execute(
        """
        UPDATE knowledge_bases
           SET status = $2, updated_at = now()
         WHERE id = $1 AND status = $3
        """,
        uuid.UUID(kb_id),
        to_status,
        from_status,
    )
    return res == "UPDATE 1"


# Columns that UpdateKBConfig (P1 #23) may write. The dynamic SET below is
# column-name based, so this whitelist is the SQL-injection guard.
_CONFIG_COLUMNS = frozenset(
    {
        "embedding_model",
        "chunk_size",
        "ocr_enabled",
        "top_k",
        "score_threshold",
        "retrieval_mode",
    }
)


async def update_config_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    patch: dict[str, Any],
) -> dict[str, Any] | None:
    """Explicit-partial UPDATE of the six config columns (B7 #23).

    ``patch`` maps column names to new values. Unlike `update_kb`
    (COALESCE+NULLIF "empty keeps current"), absence from the patch IS
    the "keep current" signal — the caller derives it from tri-state
    proto fields. Only whitelisted config columns are accepted; anything
    else raises ValueError. embedding_model / chunk_size invalidate
    existing vectors; the servicer pairs those with a same-transaction
    rebuild (status flip + task + outbox) — this repo owns only the data
    write.

    Does NOT open its own transaction (mirrors `set_status_in_tx`) so the
    config write commits atomically with the caller's rebuild writes.
    Returns the updated row, or None when the kb_id is not visible to
    this tenant or soft-deleted — the caller maps that to NOT_FOUND.
    """
    if not patch:
        raise ValueError("patch must not be empty")
    unknown = set(patch) - _CONFIG_COLUMNS
    if unknown:
        raise ValueError(f"non-config columns in patch: {sorted(unknown)}")
    # dict preserves insertion order, so keys() and values() stay aligned.
    assignments = ", ".join(
        f"{col} = ${i}" for i, col in enumerate(patch.keys(), start=2)
    )
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        f"""
        UPDATE knowledge_bases
           SET {assignments}, updated_at = now()
         WHERE id = $1 AND status <> 'deleted'
        RETURNING id, tenant_id, name, description, embedding_model,
                  chunk_size, ocr_enabled, top_k, score_threshold, retrieval_mode,
                  default_inference_service, status,
                  (SELECT count(*) FROM kb_documents d
                    WHERE d.kb_id = knowledge_bases.id
                      AND NOT (d.parse_status = 'failed'
                               AND d.error_message = 'deleted')) AS doc_count,
                  created_at, updated_at, vector_store_id
        """,
        uuid.UUID(kb_id),
        *patch.values(),
    )
    return dict(row) if row else None
