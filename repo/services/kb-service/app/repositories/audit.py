"""kb_audit_log repository (B8 #21, plan §6.3/§6.4).

Covers the KB management-plane audit trail (`kb_audit_log` table,
migration 006). Write paths instrument business success and business
failure (404/409/429 after basic validation) inside the caller's
transaction; the read side is keyset-paginated over (created_at, id) DESC.

Parse/reparse operations record intent at task creation and update the
*same row* in place when the parse reaches a terminal state
(``update_parse_result_in_tx``), so the operation history shows a single
flipping entry rather than an intent row plus a separate result row.
"""
from __future__ import annotations

import json
import uuid
from datetime import datetime
from typing import Any

import asyncpg

from .cursor import parse_composite_cursor
from .rls import set_tenant_context


async def insert_audit_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    action: str,
    actor_user_id: str | None = None,
    before_state: dict[str, Any] | None = None,
    after_state: dict[str, Any] | None = None,
    error_code: str | None = None,
    error_msg: str | None = None,
) -> str:
    """INSERT an audit row and return its id (caller-owned transaction).

    Must run inside the same transaction as the business write so the audit
    row commits (or rolls back) atomically with the business change — same
    contract as outbox.insert_event. RLS-scoped via set_tenant_context.

    On the failure path the business write already failed; callers wrap
    this in a fresh transaction after the business transaction rolled back,
    so the audit row survives to record the failure.
    """
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        """
        INSERT INTO kb_audit_log
            (tenant_id, kb_id, actor_user_id, action,
             before_state, after_state, error_code, error_msg)
        VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7, $8)
        RETURNING id
        """,
        uuid.UUID(tenant_id),
        uuid.UUID(kb_id),
        uuid.UUID(actor_user_id) if actor_user_id else None,
        action,
        json.dumps(before_state, default=str) if before_state is not None else None,
        json.dumps(after_state, default=str) if after_state is not None else None,
        error_code,
        error_msg,
    )
    return str(row["id"])


async def update_parse_result_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    action: str,
    task_id: str,
    result_overlay: dict[str, Any],
    error_code: str | None,
    error_msg: str | None,
) -> int:
    """UPDATE the doc.parse/doc.reparse intent row in place (caller-owned tx).

    The intent row written at task creation carries ``task_id`` inside its
    ``after_state`` JSONB; the parse consumer flips that same row when the
    doc reaches a terminal parse_status — error_code/error_msg are set
    (NULL on success) and ``result_overlay`` is JSONB-merged into
    ``after_state`` (terminal parse_status / chunk_count / task_id), so the
    operation history shows one entry per operation, not two.

    Returns the number of rows updated. 0 means no intent row carried this
    task_id (e.g. written by an older build, or the audit was skipped) —
    the caller falls back to a result INSERT to keep the trail complete.
    """
    await set_tenant_context(conn, tenant_id)
    status = await conn.execute(
        """
        UPDATE kb_audit_log
           SET after_state = COALESCE(after_state, '{}'::jsonb) || $3::jsonb,
               error_code  = $4,
               error_msg   = $5
         WHERE tenant_id = $1
           AND kb_id = $2
           AND action = $6
           AND after_state->>'task_id' = $7
        """,
        uuid.UUID(tenant_id),
        uuid.UUID(kb_id),
        json.dumps(result_overlay, default=str),
        error_code,
        error_msg,
        action,
        task_id,
    )
    return int(status.split()[-1])


async def list_logs(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    limit: int = 20,
    cursor: str | None = None,
) -> list[dict[str, Any]]:
    """List KB audit logs with keyset pagination (plan §6.4, RLS-scoped).

    ORDER BY created_at DESC, id DESC (newest first). Cursor format
    ``{created_at_iso}|{log_id}`` — same composite keyset as sessions /
    messages / citations. The fetch is plain ``LIMIT limit`` and the
    servicer emits next_cursor when exactly ``limit`` rows came back
    (``len(rows) >= limit``) — the same boundary convention as every
    other List RPC in this service (an exactly-full page may cost the
    client one extra empty page to detect the end).
    """
    cursor_ts: datetime | None = None
    cursor_id: uuid.UUID | None = None
    if cursor:
        cursor_ts, cursor_id = parse_composite_cursor(cursor)

    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        if cursor:
            rows = await conn.fetch(
                """
                SELECT id, kb_id, actor_user_id, action,
                       before_state, after_state, error_code, error_msg, created_at
                  FROM kb_audit_log
                 WHERE kb_id = $1
                   AND (created_at, id) < ($2, $3)
                 ORDER BY created_at DESC, id DESC
                 LIMIT $4
                """,
                uuid.UUID(kb_id),
                cursor_ts,
                cursor_id,
                limit,
            )
        else:
            rows = await conn.fetch(
                """
                SELECT id, kb_id, actor_user_id, action,
                       before_state, after_state, error_code, error_msg, created_at
                  FROM kb_audit_log
                 WHERE kb_id = $1
                 ORDER BY created_at DESC, id DESC
                 LIMIT $2
                """,
                uuid.UUID(kb_id),
                limit,
            )
    return [
        {
            "id": str(r["id"]),
            "kb_id": str(r["kb_id"]),
            "actor_user_id": str(r["actor_user_id"]) if r["actor_user_id"] else None,
            "action": r["action"],
            "before_state": r["before_state"],
            "after_state": r["after_state"],
            "error_code": r["error_code"],
            "error_msg": r["error_msg"],
            "created_at": r["created_at"],
        }
        for r in rows
    ]
