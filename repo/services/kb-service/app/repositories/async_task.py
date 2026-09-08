"""async_tasks repository (SPEC §2.4, §6.1).

Covers the async_tasks table used for idempotency replay and parse task
tracking. CreateKB / NotifyDocumentUploaded write a task here to record the
idempotency_key result so retries return the same response.
"""
from __future__ import annotations

import json
import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


async def find_by_idempotency_key(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    idempotency_key: str,
    task_type: str | None = None,
) -> dict[str, Any] | None:
    """Look up an existing async_task by (tenant_id, idempotency_key).

    `task_type` narrows the match to one operation kind (e.g. 'kb.update'),
    so the same client uuid reused across different operations in a tenant
    never replays another operation's result.

    Returns the row (including `result`) if a prior call with the same key
    succeeded, enabling idempotent replay (SPEC §6.1, §6.4).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT id, tenant_id, idempotency_key, task_type, resource_type,
                   resource_id, status, attempt_count, max_attempts,
                   progress_pct, payload, result, error_message,
                   started_at, completed_at, created_at, updated_at
              FROM async_tasks
             WHERE tenant_id = $1 AND idempotency_key = $2
               AND ($3::text IS NULL OR task_type = $3)
            """,
            uuid.UUID(tenant_id),
            idempotency_key,
            task_type,
        )
    return dict(row) if row else None


async def create_task(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    idempotency_key: str,
    task_type: str,
    resource_type: str | None = None,
    resource_id: str | None = None,
    payload: dict[str, Any] | None = None,
    status: str = "pending",
) -> dict[str, Any]:
    """INSERT a new async_tasks row and return it (RLS-scoped).

    `idempotency_key` is UNIQUE per tenant so a duplicate insert raises
    asyncpg.UniqueViolationError; callers should check find_by_idempotency_key
    first.
    """
    async with conn.transaction():
        return await create_task_in_tx(
            conn,
            tenant_id=tenant_id,
            idempotency_key=idempotency_key,
            task_type=task_type,
            resource_type=resource_type,
            resource_id=resource_id,
            payload=payload,
            status=status,
        )


async def create_task_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    idempotency_key: str,
    task_type: str,
    resource_type: str | None = None,
    resource_id: str | None = None,
    payload: dict[str, Any] | None = None,
    status: str = "pending",
) -> dict[str, Any]:
    """INSERT an async_tasks row inside the caller's transaction (RLS-scoped).

    Does NOT open its own transaction. Used by NotifyDocumentUploaded (SPEC §6.1,
    US-010) so the async_tasks insert commits atomically with the kb_documents
    update and outbox_events insert. `idempotency_key` is UNIQUE per tenant.
    """
    payload_json = json.dumps(payload or {}, default=str)
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        """
        INSERT INTO async_tasks
            (tenant_id, idempotency_key, task_type, resource_type,
             resource_id, status, payload)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING id, tenant_id, idempotency_key, task_type,
                  resource_type, resource_id, status, attempt_count,
                  max_attempts, progress_pct, payload, result,
                  error_message, started_at, completed_at,
                  created_at, updated_at
        """,
        uuid.UUID(tenant_id),
        idempotency_key,
        task_type,
        resource_type,
        uuid.UUID(resource_id) if resource_id else None,
        status,
        payload_json,
    )
    return dict(row)


async def complete_task(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    task_id: str,
    result: dict[str, Any] | None = None,
    status: str = "completed",
) -> bool:
    """Mark a task completed with its result (RLS-scoped)."""
    async with conn.transaction():
        return await complete_task_in_tx(
            conn,
            tenant_id=tenant_id,
            task_id=task_id,
            result=result,
            status=status,
        )


async def complete_task_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    task_id: str,
    result: dict[str, Any] | None = None,
    status: str = "completed",
) -> bool:
    """Mark a task completed inside the caller's transaction (RLS-scoped).

    Does NOT open its own transaction. Used by UpdateKB/CreateKB so the
    async_tasks write commits atomically with the other statements in the
    same transaction (single-round-trip idempotency record).
    """
    result_json = json.dumps(result, default=str) if result else None
    await set_tenant_context(conn, tenant_id)
    res = await conn.execute(
        """
        UPDATE async_tasks
           SET status = $2, result = $3, completed_at = now(),
               updated_at = now()
         WHERE id = $1
        """,
        uuid.UUID(task_id),
        status,
        result_json,
    )
    return res == "UPDATE 1"


async def revive_task_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    task_id: str,
) -> dict[str, Any] | None:
    """Reset a failed async_tasks row to pending (RLS-scoped, in-tx).

    Used by the NotifyDocumentUploaded UNIQUE-race self-heal: notify
    synthesizes its idempotency_key from (tenant, kb, doc), so a re-upload
    after a failed parse cannot pick a fresh key — the failed row is
    revived to pending so the new outbox event re-runs and the parse
    consumer closes the same row.

    Conditional on status='failed' (returns None otherwise) so a concurrent
    status change between the caller's lookup and this UPDATE is detected
    instead of silently overwritten.
    """
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        """
        UPDATE async_tasks
           SET status = 'pending', result = NULL, error_message = NULL,
               completed_at = NULL, updated_at = now()
         WHERE id = $1 AND status = 'failed'
        RETURNING id, tenant_id, idempotency_key, task_type, resource_type,
                  resource_id, status, attempt_count, max_attempts,
                  progress_pct, payload, result, error_message,
                  started_at, completed_at, created_at, updated_at
        """,
        uuid.UUID(task_id),
    )
    return dict(row) if row else None


async def get_task(
    conn: asyncpg.Connection, *, tenant_id: str, task_id: str
) -> dict[str, Any] | None:
    """SELECT a single async_tasks row by id (RLS-scoped)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT id, tenant_id, idempotency_key, task_type, resource_type,
                   resource_id, status, attempt_count, max_attempts,
                   progress_pct, payload, result, error_message,
                   started_at, completed_at, created_at, updated_at
              FROM async_tasks
             WHERE id = $1
            """,
            uuid.UUID(task_id),
        )
    return dict(row) if row else None
