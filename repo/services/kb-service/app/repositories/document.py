"""kb_documents repository (SPEC §2.4, §6.1).

Covers CRUD on the `kb_documents` table with RLS tenant filtering.
The two-step upload flow (GetDocumentUploadURL + NotifyDocumentUploaded)
writes doc records here.
"""
from __future__ import annotations

import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


async def create_document(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    file_name: str,
    file_type: str,
    file_size_bytes: int,
    storage_path: str,
    checksum_sha256: str,
    custom_metadata: dict[str, Any] | None = None,
    doc_id: str | None = None,
    object_id: str | None = None,
) -> dict[str, Any]:
    """INSERT a new kb_documents row (parse_status='pending') and return it.

    `doc_id` is optional: if provided, the UUID is set explicitly (used by
    GetDocumentUploadURL which pre-reserves the id before the MinIO upload);
    otherwise Postgres generates it via gen_random_uuid().

    `object_id` is the Core API object UUID returned by ``POST /objects/upload``.
    It is persisted so the parse pipeline can download the object through the
    Core API by UUID (not by the MinIO ``storage_path``).
    """
    metadata_json = _to_jsonb(custom_metadata)
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        if doc_id:
            row = await conn.fetchrow(
                """
                INSERT INTO kb_documents
                    (id, kb_id, tenant_id, file_name, file_type,
                     file_size_bytes, storage_path, checksum_sha256,
                     parse_status, custom_metadata, object_id)
                VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10)
                RETURNING id, kb_id, tenant_id, file_name, file_type,
                          file_size_bytes, storage_path, checksum_sha256,
                          parse_status, chunk_count, error_message,
                          custom_metadata, created_at, parsed_at, object_id
                """,
                uuid.UUID(doc_id),
                uuid.UUID(kb_id),
                uuid.UUID(tenant_id),
                file_name,
                file_type,
                file_size_bytes,
                storage_path,
                checksum_sha256,
                metadata_json,
                object_id,
            )
        else:
            row = await conn.fetchrow(
                """
                INSERT INTO kb_documents
                    (kb_id, tenant_id, file_name, file_type,
                     file_size_bytes, storage_path, checksum_sha256,
                     parse_status, custom_metadata, object_id)
                VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)
                RETURNING id, kb_id, tenant_id, file_name, file_type,
                          file_size_bytes, storage_path, checksum_sha256,
                          parse_status, chunk_count, error_message,
                          custom_metadata, created_at, parsed_at, object_id
                """,
                uuid.UUID(kb_id),
                uuid.UUID(tenant_id),
                file_name,
                file_type,
                file_size_bytes,
                storage_path,
                checksum_sha256,
                metadata_json,
                object_id,
            )
    return dict(row)


async def get_document(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str, doc_id: str
) -> dict[str, Any] | None:
    """SELECT a single kb_documents row (RLS-scoped).

    Soft-deleted rows (parse_status='failed' + error_message='deleted', the
    marker written by soft_delete_document) are filtered out so callers see
    them as NOT_FOUND.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT id, kb_id, tenant_id, file_name, file_type,
                   file_size_bytes, storage_path, checksum_sha256,
                   parse_status, chunk_count, error_message,
                   custom_metadata, created_at, parsed_at, object_id
              FROM kb_documents
             WHERE id = $1 AND kb_id = $2
               AND NOT (parse_status = 'failed' AND error_message = 'deleted')
            """,
            uuid.UUID(doc_id),
            uuid.UUID(kb_id),
        )
    return dict(row) if row else None


async def list_documents(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    parse_status: str | None = None,
    limit: int = 20,
    cursor: str | None = None,
) -> tuple[list[dict[str, Any]], int]:
    """List kb_documents with optional parse_status filter + cursor paging.

    Soft-deleted rows (parse_status='failed' + error_message='deleted') are
    excluded from both the page and the total.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        if parse_status:
            total = await conn.fetchval(
                """
                SELECT count(*) FROM kb_documents
                 WHERE kb_id = $1 AND parse_status = $2
                   AND NOT (parse_status = 'failed' AND error_message = 'deleted')
                """,
                uuid.UUID(kb_id),
                parse_status,
            )
            if cursor:
                rows = await conn.fetch(
                    """
                    SELECT id, kb_id, tenant_id, file_name, file_type,
                           file_size_bytes, storage_path, checksum_sha256,
                           parse_status, chunk_count, error_message,
                           custom_metadata, created_at, parsed_at
                      FROM kb_documents
                     WHERE kb_id = $1 AND parse_status = $2 AND id > $3
                       AND NOT (parse_status = 'failed' AND error_message = 'deleted')
                     ORDER BY id ASC
                     LIMIT $4
                    """,
                    uuid.UUID(kb_id),
                    parse_status,
                    uuid.UUID(cursor),
                    limit,
                )
            else:
                rows = await conn.fetch(
                    """
                    SELECT id, kb_id, tenant_id, file_name, file_type,
                           file_size_bytes, storage_path, checksum_sha256,
                           parse_status, chunk_count, error_message,
                           custom_metadata, created_at, parsed_at
                      FROM kb_documents
                     WHERE kb_id = $1 AND parse_status = $2
                       AND NOT (parse_status = 'failed' AND error_message = 'deleted')
                     ORDER BY id ASC
                     LIMIT $3
                    """,
                    uuid.UUID(kb_id),
                    parse_status,
                    limit,
                )
        else:
            total = await conn.fetchval(
                """
                SELECT count(*) FROM kb_documents
                 WHERE kb_id = $1
                   AND NOT (parse_status = 'failed' AND error_message = 'deleted')
                """,
                uuid.UUID(kb_id),
            )
            if cursor:
                rows = await conn.fetch(
                    """
                    SELECT id, kb_id, tenant_id, file_name, file_type,
                           file_size_bytes, storage_path, checksum_sha256,
                           parse_status, chunk_count, error_message,
                           custom_metadata, created_at, parsed_at
                      FROM kb_documents
                     WHERE kb_id = $1 AND id > $2
                       AND NOT (parse_status = 'failed' AND error_message = 'deleted')
                     ORDER BY id ASC
                     LIMIT $3
                    """,
                    uuid.UUID(kb_id),
                    uuid.UUID(cursor),
                    limit,
                )
            else:
                rows = await conn.fetch(
                    """
                    SELECT id, kb_id, tenant_id, file_name, file_type,
                           file_size_bytes, storage_path, checksum_sha256,
                           parse_status, chunk_count, error_message,
                           custom_metadata, created_at, parsed_at
                      FROM kb_documents
                     WHERE kb_id = $1
                       AND NOT (parse_status = 'failed' AND error_message = 'deleted')
                     ORDER BY id ASC
                     LIMIT $2
                    """,
                    uuid.UUID(kb_id),
                    limit,
                )
    return [dict(r) for r in rows], total


async def update_parse_status(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    doc_id: str,
    parse_status: str,
    error_message: str | None = None,
    chunk_count: int | None = None,
) -> bool:
    """Update a document's parse_status (RLS-scoped). Returns True if updated.

    Opens its own transaction. Use `update_parse_status_in_tx` when the update
    must participate in an outer transaction (e.g. NotifyDocumentUploaded,
    SPEC §6.1, US-010).
    """
    async with conn.transaction():
        return await update_parse_status_in_tx(
            conn,
            tenant_id=tenant_id,
            doc_id=doc_id,
            parse_status=parse_status,
            error_message=error_message,
            chunk_count=chunk_count,
        )


async def update_parse_status_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    doc_id: str,
    parse_status: str,
    error_message: str | None = None,
    chunk_count: int | None = None,
) -> bool:
    """Update a document's parse_status inside the caller's transaction.

    Does NOT open its own transaction. Caller must hold an active transaction
    and is responsible for committing/rolling back (SPEC §6.1 NotifyDocumentUploaded
    writes kb_documents + async_tasks + outbox_events atomically, US-010).
    """
    await set_tenant_context(conn, tenant_id)
    if parse_status == "ready":
        result = await conn.execute(
            """
            UPDATE kb_documents
               SET parse_status = $2,
                   error_message = $3,
                   chunk_count = COALESCE($4, chunk_count),
                   parsed_at = now()
             WHERE id = $1
            """,
            uuid.UUID(doc_id),
            parse_status,
            error_message,
            chunk_count,
        )
    else:
        result = await conn.execute(
            """
            UPDATE kb_documents
               SET parse_status = $2,
                   error_message = $3,
                   chunk_count = COALESCE($4, chunk_count)
             WHERE id = $1
            """,
            uuid.UUID(doc_id),
            parse_status,
            error_message,
            chunk_count,
        )
    return result == "UPDATE 1"


async def soft_delete_document(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str, doc_id: str
) -> bool:
    """Soft-delete a document by setting parse_status='deleted'.

    Note: the init schema CHECK constraint on kb_documents.parse_status only
    allows pending|parsing|indexing|ready|failed. To support soft-delete we
    mark the row as 'failed' with error_message='deleted' instead of adding a
    new enum value (which would require a migration). This keeps the row for
    audit while excluding it from ready/parse listings.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        result = await conn.execute(
            """
            UPDATE kb_documents
               SET parse_status = 'failed',
                   error_message = 'deleted'
             WHERE id = $1 AND kb_id = $2
            """,
            uuid.UUID(doc_id),
            uuid.UUID(kb_id),
        )
    return result == "UPDATE 1"


def _to_jsonb(value: dict[str, Any] | None) -> str:
    """Serialize a dict to a JSONB-compatible string (asyncpg accepts JSON text)."""
    import json

    return json.dumps(value or {}, default=str)
