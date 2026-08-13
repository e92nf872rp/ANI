"""kb_chunks write repository for rag-engine (US-012 / SPEC §2.4, §3.1, §5.1).

The ``kb_chunks`` table is owned by kb-service (migrated by
``002_kb_chunks.sql``) and read by kb-service for pg_trgm keyword retrieval.
Writes are performed by rag-engine's parse_worker after chunking: this
module provides the batched INSERT for parent chunks, child chunks (with
denormalized ``parent_content``) and doc-summary chunks.

Conventions (aligned with ``kb-service/app/repositories/chunk.py``):
* Uses ``asyncpg`` for the same pool/transaction semantics as kb-service.
* Sets the RLS tenant context via ``SELECT set_config('app.current_tenant_id', $1, true)``
  inside the active transaction before any INSERT (SPEC §8.1, FR-15).
* Metadata is stored in the ``custom_metadata`` JSONB column.
"""
from __future__ import annotations

import json
import uuid
from typing import Any

import asyncpg

from app.services.chunk_service import ChildChunk, ParentChunk


def _to_uuid(value: str, *, field: str) -> uuid.UUID:
    """Parse a UUID string with a clear error message.

    ``asyncpg`` would raise a bare ``ValueError`` on a malformed UUID,
    obscuring which field was invalid.
    """
    try:
        return uuid.UUID(value)
    except (ValueError, AttributeError, TypeError) as exc:
        raise ValueError(f"invalid UUID for {field!r}: {value!r}") from exc


async def set_tenant_context(conn: asyncpg.Connection, tenant_id: str) -> None:
    """Set the RLS tenant context for the current transaction.

    Mirrors ``kb-service/app/repositories/rls.set_tenant_context`` but uses
    ``set_config(...)`` so the value binds via asyncpg's parameter protocol.
    ``SET LOCAL ... = $1`` is a utility command and PostgreSQL rejects
    parameter substitution on it (``syntax error at or near "$1"``); the
    ``set_config`` function is the parameter-safe equivalent and is scoped
    to the current transaction when ``is_local = true``.
    """
    await conn.execute(
        "SELECT set_config('app.current_tenant_id', $1, true)", tenant_id
    )


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
    parents: list[ParentChunk],
    children: list[ChildChunk],
    summaries: list[ChildChunk] | None = None,
) -> int:
    """Batched INSERT of parent + child (+ optional summary) chunks.

    Parents are inserted first (children reference their ``parent_chunk_id``
    via FK), then children, then any doc-summary chunks. All inserts run
    inside a single transaction with the RLS tenant context set once.

    Returns the total number of inserted rows.
    """
    rows: list[tuple] = []
    # Parents first so child FK references resolve.
    for p in parents:
        rows.append(
            _row(
                chunk_id=p.chunk_id,
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                chunk_type="parent",
                content=p.content,
                file_name=file_name,
                page_number=p.page_number,
                content_type=p.content_type,
                parent_chunk_id=None,
                parent_content=None,
                token_count=p.token_count,
                custom_metadata=p.metadata,
            )
        )
    # Children: parent_chunk_id + parent_content denormalized (SPEC §5.1).
    for c in children:
        rows.append(
            _row(
                chunk_id=c.chunk_id,
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                chunk_type="child",
                content=c.content,
                file_name=file_name,
                page_number=c.page_number,
                content_type=c.content_type,
                parent_chunk_id=c.parent_chunk_id,
                parent_content=c.parent_content,
                token_count=c.token_count,
                custom_metadata=c.metadata,
            )
        )
    # Doc-summary chunks (US-012 summary_service): chunk_type=doc_summary.
    for s in summaries or []:
        rows.append(
            _row(
                chunk_id=s.chunk_id,
                tenant_id=tenant_id,
                kb_id=kb_id,
                doc_id=doc_id,
                chunk_type="doc_summary",
                content=s.content,
                file_name=file_name,
                page_number=s.page_number,
                content_type=s.content_type,
                parent_chunk_id=s.parent_chunk_id,
                parent_content=s.parent_content,
                token_count=s.token_count,
                custom_metadata=s.metadata,
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
    """Delete all chunks for a document (RLS-scoped).

    Used by reparse (idempotency): clear prior chunks before re-writing.
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
