"""kb_documents repository (SPEC §2.4, §6.1, §4.2).

Covers CRUD on the `kb_documents` table via the Core data plane
(`CoreClient.data_query`). RLS tenant filtering is applied by Core based
on the `X-Tenant-Id` header carried by the CoreClient (role="tenant").
The two-step upload flow (GetDocumentUploadURL + NotifyDocumentUploaded)
writes doc records here.
"""
from __future__ import annotations

import json
from typing import Any

from app.core_api.client import CoreClient

_DOC_COLUMNS = (
    "id, kb_id, tenant_id, file_name, file_type, "
    "file_size_bytes, storage_path, checksum_sha256, "
    "parse_status, chunk_count, error_message, "
    "custom_metadata, created_at, parsed_at, object_id"
)

_DOC_COLUMNS_NO_OBJECT_ID = (
    "id, kb_id, tenant_id, file_name, file_type, "
    "file_size_bytes, storage_path, checksum_sha256, "
    "parse_status, chunk_count, error_message, "
    "custom_metadata, created_at, parsed_at"
)


async def create_document(
    core: CoreClient,
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
    if doc_id:
        sql = f"""
            INSERT INTO kb_documents
                (id, kb_id, tenant_id, file_name, file_type,
                 file_size_bytes, storage_path, checksum_sha256,
                 parse_status, custom_metadata, object_id)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $10)
            RETURNING {_DOC_COLUMNS}
        """
        params: list[Any] = [
            doc_id, kb_id, tenant_id, file_name, file_type,
            file_size_bytes, storage_path, checksum_sha256,
            metadata_json, object_id,
        ]
    else:
        sql = f"""
            INSERT INTO kb_documents
                (kb_id, tenant_id, file_name, file_type,
                 file_size_bytes, storage_path, checksum_sha256,
                 parse_status, custom_metadata, object_id)
            VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $9)
            RETURNING {_DOC_COLUMNS}
        """
        params = [
            kb_id, tenant_id, file_name, file_type,
            file_size_bytes, storage_path, checksum_sha256,
            metadata_json, object_id,
        ]
    result = await core.data_query(sql=sql, params=params)
    rows = result.get("rows", [])
    return rows[0] if rows else {}


async def get_document(
    core: CoreClient, *, tenant_id: str, kb_id: str, doc_id: str
) -> dict[str, Any] | None:
    """SELECT a single kb_documents row (RLS-scoped via Core)."""
    sql = f"""
        SELECT {_DOC_COLUMNS}
          FROM kb_documents
         WHERE id = $1 AND kb_id = $2
    """
    result = await core.data_query(sql=sql, params=[doc_id, kb_id])
    rows = result.get("rows", [])
    return rows[0] if rows else None


async def list_documents(
    core: CoreClient,
    *,
    tenant_id: str,
    kb_id: str,
    parse_status: str | None = None,
    limit: int = 20,
    cursor: str | None = None,
) -> tuple[list[dict[str, Any]], int]:
    """List kb_documents with optional parse_status filter + cursor paging."""
    if parse_status:
        count_sql = (
            "SELECT count(*) AS total FROM kb_documents "
            "WHERE kb_id = $1 AND parse_status = $2"
        )
        count_result = await core.data_query(
            sql=count_sql, params=[kb_id, parse_status]
        )
        if cursor:
            sql = f"""
                SELECT {_DOC_COLUMNS_NO_OBJECT_ID}
                  FROM kb_documents
                 WHERE kb_id = $1 AND parse_status = $2 AND id > $3
                 ORDER BY id ASC
                 LIMIT $4
            """
            result = await core.data_query(
                sql=sql, params=[kb_id, parse_status, cursor, limit]
            )
        else:
            sql = f"""
                SELECT {_DOC_COLUMNS_NO_OBJECT_ID}
                  FROM kb_documents
                 WHERE kb_id = $1 AND parse_status = $2
                 ORDER BY id ASC
                 LIMIT $3
            """
            result = await core.data_query(
                sql=sql, params=[kb_id, parse_status, limit]
            )
    else:
        count_sql = "SELECT count(*) AS total FROM kb_documents WHERE kb_id = $1"
        count_result = await core.data_query(sql=count_sql, params=[kb_id])
        if cursor:
            sql = f"""
                SELECT {_DOC_COLUMNS_NO_OBJECT_ID}
                  FROM kb_documents
                 WHERE kb_id = $1 AND id > $2
                 ORDER BY id ASC
                 LIMIT $3
            """
            result = await core.data_query(
                sql=sql, params=[kb_id, cursor, limit]
            )
        else:
            sql = f"""
                SELECT {_DOC_COLUMNS_NO_OBJECT_ID}
                  FROM kb_documents
                 WHERE kb_id = $1
                 ORDER BY id ASC
                 LIMIT $2
            """
            result = await core.data_query(sql=sql, params=[kb_id, limit])
    count_rows = count_result.get("rows", [])
    total = count_rows[0].get("total", 0) if count_rows else 0
    return result.get("rows", []), total


async def update_parse_status(
    core: CoreClient,
    *,
    tenant_id: str,
    doc_id: str,
    parse_status: str,
    error_message: str | None = None,
    chunk_count: int | None = None,
) -> bool:
    """Update a document's parse_status (RLS-scoped via Core). Returns True if updated.

    Each data_query call is a single transaction (SPEC §4.2), so this replaces
    the former update_parse_status_in_tx variant — callers that previously
    embedded the update inside a larger cross-table transaction must now use a
    single multi-statement data_query call (see grpc_server NotifyDocumentUploaded).
    """
    if parse_status == "ready":
        sql = """
            UPDATE kb_documents
               SET parse_status = $2,
                   error_message = $3,
                   chunk_count = COALESCE($4, chunk_count),
                   parsed_at = now()
             WHERE id = $1
        """
    else:
        sql = """
            UPDATE kb_documents
               SET parse_status = $2,
                   error_message = $3,
                   chunk_count = COALESCE($4, chunk_count)
             WHERE id = $1
        """
    result = await core.data_query(
        sql=sql,
        params=[doc_id, parse_status, error_message, chunk_count],
    )
    return result.get("rowcount", 0) == 1


async def soft_delete_document(
    core: CoreClient, *, tenant_id: str, kb_id: str, doc_id: str
) -> bool:
    """Soft-delete a document by setting parse_status='failed'.

    Note: the init schema CHECK constraint on kb_documents.parse_status only
    allows pending|parsing|indexing|ready|failed. To support soft-delete we
    mark the row as 'failed' with error_message='deleted' instead of adding a
    new enum value (which would require a migration). This keeps the row for
    audit while excluding it from ready/parse listings.
    """
    sql = """
        UPDATE kb_documents
           SET parse_status = 'failed',
               error_message = 'deleted'
         WHERE id = $1 AND kb_id = $2
    """
    result = await core.data_query(sql=sql, params=[doc_id, kb_id])
    return result.get("rowcount", 0) == 1


def _to_jsonb(value: dict[str, Any] | None) -> str:
    """Serialize a dict to a JSONB-compatible string (Core accepts JSON text)."""
    return json.dumps(value or {}, default=str)
