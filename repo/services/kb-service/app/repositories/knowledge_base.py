"""knowledge_bases repository (SPEC §2.4, §6.1, §4.2).

Covers CRUD on the `knowledge_bases` table via the Core data plane
(`CoreClient.data_query`). RLS tenant filtering is applied by Core based
on the `X-Tenant-Id` header carried by the CoreClient (role="tenant").
CreateKB also writes the Core vector-store id via the gateway client
(wired in the gRPC servicer layer, not here — this repository is pure data).
"""
from __future__ import annotations

from typing import Any

from app.core_api.client import CoreClient

_KB_COLUMNS = (
    "id, tenant_id, name, description, embedding_model, "
    "chunk_size, top_k, score_threshold, retrieval_mode, "
    "status, doc_count, created_at, updated_at"
)


async def create_kb(
    core: CoreClient,
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
    """INSERT a new knowledge_bases row and return it (SPEC §4.2 single tx).

    The `id` is generated server-side (gen_random_uuid). RLS context is set
    by Core based on the CoreClient's X-Tenant-Id header (role="tenant").
    """
    sql = f"""
        INSERT INTO knowledge_bases
            (tenant_id, name, description, embedding_model,
             chunk_size, top_k, score_threshold, retrieval_mode, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active')
        RETURNING {_KB_COLUMNS}
    """
    result = await core.data_query(
        sql=sql,
        params=[
            tenant_id, name, description, embedding_model,
            chunk_size, top_k, score_threshold, retrieval_mode,
        ],
    )
    rows = result.get("rows", [])
    return rows[0] if rows else {}


async def get_kb(
    core: CoreClient, *, tenant_id: str, kb_id: str
) -> dict[str, Any] | None:
    """SELECT a single knowledge_bases row by id (RLS-scoped via Core)."""
    sql = f"""
        SELECT {_KB_COLUMNS}
          FROM knowledge_bases
         WHERE id = $1
    """
    result = await core.data_query(sql=sql, params=[kb_id])
    rows = result.get("rows", [])
    return rows[0] if rows else None


async def list_kbs(
    core: CoreClient,
    *,
    tenant_id: str,
    limit: int = 20,
    cursor: str | None = None,
) -> tuple[list[dict[str, Any]], int]:
    """List knowledge_bases with cursor pagination (RLS-scoped via Core).

    Returns (rows, total). Cursor is the `id` of the last row of the previous
    page (lexicographic UUID ordering).
    """
    count_sql = "SELECT count(*) AS total FROM knowledge_bases"
    count_result = await core.data_query(sql=count_sql)
    count_rows = count_result.get("rows", [])
    total = count_rows[0].get("total", 0) if count_rows else 0

    if cursor:
        sql = f"""
            SELECT {_KB_COLUMNS}
              FROM knowledge_bases
             WHERE id > $1
             ORDER BY id ASC
             LIMIT $2
        """
        result = await core.data_query(sql=sql, params=[cursor, limit])
    else:
        sql = f"""
            SELECT {_KB_COLUMNS}
              FROM knowledge_bases
             ORDER BY id ASC
             LIMIT $1
        """
        result = await core.data_query(sql=sql, params=[limit])
    return result.get("rows", []), total


async def soft_delete_kb(
    core: CoreClient, *, tenant_id: str, kb_id: str
) -> bool:
    """Soft-delete a knowledge_base by setting status='deleted' (RLS-scoped).

    Returns True if a row was updated, False if not found (RLS hides other
    tenants' rows so NOT_FOUND is indistinguishable from cross-tenant).
    """
    sql = """
        UPDATE knowledge_bases
           SET status = 'deleted', updated_at = now()
         WHERE id = $1 AND status <> 'deleted'
    """
    result = await core.data_query(sql=sql, params=[kb_id])
    return result.get("rowcount", 0) == 1


async def get_kb_status(
    core: CoreClient, *, tenant_id: str, kb_id: str
) -> str | None:
    """Return the KB status (for rebuild precondition checks)."""
    sql = "SELECT status FROM knowledge_bases WHERE id = $1"
    result = await core.data_query(sql=sql, params=[kb_id])
    rows = result.get("rows", [])
    return rows[0].get("status") if rows else None


async def increment_doc_count(
    core: CoreClient, *, tenant_id: str, kb_id: str, delta: int = 1
) -> None:
    """Increment/decrement doc_count atomically (RLS-scoped via Core)."""
    sql = """
        UPDATE knowledge_bases
           SET doc_count = doc_count + $2,
               updated_at = now()
         WHERE id = $1
    """
    await core.data_query(sql=sql, params=[kb_id, delta])
