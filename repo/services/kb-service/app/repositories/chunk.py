"""kb_chunks repository (SPEC §2.4, §3.1, §8.1, §4.2).

kb-service reads kb_chunks for keyword (pg_trgm) retrieval (FR-7 mixed
retrieval). Writes are performed by rag-engine's parse_worker; this
repository exposes read + keyword-search APIs via the Core data plane
(`CoreClient.data_query`). RLS tenant filtering is applied by Core based
on the `X-Tenant-Id` header carried by the CoreClient (role="tenant").
"""
from __future__ import annotations

from typing import Any

from app.core_api.client import CoreClient

_CHUNK_COLUMNS = (
    "id, tenant_id, kb_id, doc_id, parent_chunk_id, chunk_type, "
    "content, parent_content, page_number, content_type, "
    "file_name, token_count, custom_metadata, created_at"
)


async def get_chunk(
    core: CoreClient, *, tenant_id: str, chunk_id: str
) -> dict[str, Any] | None:
    """SELECT a single kb_chunks row by id (RLS-scoped via Core)."""
    sql = f"""
        SELECT {_CHUNK_COLUMNS}
          FROM kb_chunks
         WHERE id = $1
    """
    result = await core.data_query(sql=sql, params=[chunk_id])
    rows = result.get("rows", [])
    return rows[0] if rows else None


async def list_chunks_by_doc(
    core: CoreClient,
    *,
    tenant_id: str,
    kb_id: str,
    doc_id: str,
    limit: int = 100,
) -> list[dict[str, Any]]:
    """List all chunks for a document (RLS-scoped via Core)."""
    sql = f"""
        SELECT {_CHUNK_COLUMNS}
          FROM kb_chunks
         WHERE kb_id = $1 AND doc_id = $2
         ORDER BY created_at ASC
         LIMIT $3
    """
    result = await core.data_query(sql=sql, params=[kb_id, doc_id, limit])
    return result.get("rows", [])


async def keyword_search(
    core: CoreClient,
    *,
    tenant_id: str,
    kb_id: str,
    query: str,
    limit: int = 10,
) -> list[dict[str, Any]]:
    """Keyword search on kb_chunks.content using pg_trgm (RLS-scoped via Core).

    Uses the GIN trigram index (idx_kb_chunks_content_trgm) via ILIKE for
    fuzzy keyword matching. Returns chunks ordered by similarity rank.
    The similarity() function and GIN index are preserved on the Core side
    (SPEC §4.2 — pg_trgm semantics unchanged).
    """
    sql = f"""
        SELECT {_CHUNK_COLUMNS},
               similarity(content, $1) AS rank
          FROM kb_chunks
         WHERE kb_id = $2 AND content ILIKE '%' || $1 || '%'
         ORDER BY rank DESC
         LIMIT $3
    """
    result = await core.data_query(sql=sql, params=[query, kb_id, limit])
    return result.get("rows", [])


async def count_chunks_by_doc(
    core: CoreClient, *, tenant_id: str, kb_id: str, doc_id: str
) -> int:
    """Count chunks for a document (RLS-scoped via Core)."""
    sql = "SELECT count(*) AS total FROM kb_chunks WHERE kb_id = $1 AND doc_id = $2"
    result = await core.data_query(sql=sql, params=[kb_id, doc_id])
    rows = result.get("rows", [])
    return rows[0].get("total", 0) if rows else 0
