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

# Running-lease duration (seconds). Stamped by mark_running, renewed by
# set_progress (each completed document is a liveness heartbeat). An
# expired lease lets the next redelivery take over an orphaned running
# row (mirrors the Go task_repo AcquireLease / model-import-worker's
# 30-minute AckWait alignment).
DEFAULT_LEASE_SECONDS = 30 * 60


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

    Terminal-state guard: refuses to overwrite a row already in a terminal
    state ('completed'/'failed'/'cancelled'/'dead_letter'), mirroring the
    gateway's async_task_store UPDATE — under at-least-once redelivery,
    interleaved consumers must not overwrite each other's terminal state;
    the first completion wins.
    """
    result_json = json.dumps(result, default=str) if result else None
    await set_tenant_context(conn, tenant_id)
    res = await conn.execute(
        """
        UPDATE async_tasks
           SET status = $2, result = $3, completed_at = now(),
               updated_at = now()
         WHERE id = $1
           AND status NOT IN ('completed', 'failed', 'cancelled',
                              'dead_letter')
        """,
        uuid.UUID(task_id),
        status,
        result_json,
    )
    return res == "UPDATE 1"


async def mark_running(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    task_id: str,
    lease_seconds: int = DEFAULT_LEASE_SECONDS,
) -> bool:
    """Set a pending task to running + stamp started_at (RLS-scoped).

    Used by the rebuild consumer when it picks up a kb.rebuild task.
    Conditional UPDATE, two acquire paths:
      - status='pending' — the normal first pickup;
      - status='running' with an EXPIRED lease — orphan takeover: the
        consumer crashed mid-run (its row stays running forever without
        this path), and JetStream's MaxDeliver redelivery re-acquires it
        once the lease lapses. A live lease (active consumer) is never
        stolen — the duplicate delivery is skipped, as before.

    Mirrors the Go task_repo AcquireLease conditional
    (``lease_until IS NULL OR lease_until < NOW()``). ``set_progress``
    renews the lease on every heartbeat (each completed document), so
    the lease only expires when the consumer is genuinely dead.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        res = await conn.execute(
            """
            UPDATE async_tasks
               SET status = 'running',
                   started_at = now(),
                   lease_until = now() + ($2::int * INTERVAL '1 second'),
                   last_heartbeat_at = now(),
                   updated_at = now()
             WHERE id = $1
               AND (status = 'pending'
                    OR (status = 'running'
                        AND (lease_until IS NULL
                             OR lease_until < now())))
            """,
            uuid.UUID(task_id),
            lease_seconds,
        )
        return res == "UPDATE 1"


async def set_progress(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    task_id: str,
    progress_pct: int,
    lease_seconds: int = DEFAULT_LEASE_SECONDS,
) -> bool:
    """Advance a task's progress_pct monotonically (RLS-scoped).

    GREATEST guards against regressions from at-least-once reprocessing
    or concurrent updates; progress never moves backwards. async_tasks
    CHECK constrains progress_pct to 0-100 (init schema).

    Each progress write doubles as a liveness heartbeat: it renews
    ``lease_until`` / ``last_heartbeat_at`` so a long rebuild never
    loses its running lease to the orphan-takeover path in
    ``mark_running``.
    """
    progress_pct = max(0, min(100, int(progress_pct)))
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        res = await conn.execute(
            """
            UPDATE async_tasks
               SET progress_pct = GREATEST($2, progress_pct),
                   lease_until = now() + ($3::int * INTERVAL '1 second'),
                   last_heartbeat_at = now(),
                   updated_at = now()
             WHERE id = $1 AND status = 'running'
            """,
            uuid.UUID(task_id),
            progress_pct,
            lease_seconds,
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


async def delete_kb_create_replay_records(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str
) -> None:
    """Delete kb.create idempotency replay rows for a soft-deleted KB.

    Called by DeleteKB (C-fix for the delete → same-name re-create bug):
    kb-service falls back to the deterministic key create_kb:{tenant}:{name}
    when the HTTP request carries no idempotency_key, so every same-name
    create in a tenant shares one replay row. If the old KB's row survives
    the delete, the next same-name create replays the deleted KB's stale
    snapshot (old id, never inserted). Deleting the row at delete time
    makes the re-create take the fresh INSERT path. Best-effort: the
    caller treats failures as a warning, not an abort.
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.execute(
            """
            DELETE FROM async_tasks
             WHERE tenant_id = $1
               AND task_type = 'kb.create'
               AND resource_id = $2
            """,
            uuid.UUID(tenant_id),
            uuid.UUID(kb_id),
        )
