"""outbox_events repository (SPEC §2.4, §6.1).

Covers the outbox pattern: NotifyDocumentUploaded writes an outbox event in
the same transaction as the business write, and a separate dispatcher polls
undispatched events to publish to NATS (US-010 wires the dispatcher).
"""
from __future__ import annotations

import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


async def insert_event(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    aggregate_type: str,
    aggregate_id: str,
    event_type: str,
    payload: dict[str, Any] | None = None,
) -> int:
    """INSERT an outbox_events row and return its id (RLS-scoped).

    Called inside the same transaction as the business write so the event is
    committed atomically with the business state change (SPEC §6.1).
    """
    import json

    payload_json = json.dumps(payload or {}, default=str)
    # NOTE: this must be called inside an existing transaction so the outbox
    # row commits only if the business write also commits. Callers should
    # NOT wrap in a new transaction here (they manage the tx externally).
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        """
        INSERT INTO outbox_events
            (aggregate_type, aggregate_id, event_type, tenant_id, payload)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id
        """,
        aggregate_type,
        uuid.UUID(aggregate_id),
        event_type,
        uuid.UUID(tenant_id),
        payload_json,
    )
    return int(row["id"])


async def list_undispatched(
    conn: asyncpg.Connection, *, limit: int = 100
) -> list[dict[str, Any]]:
    """List undispatched outbox events ordered by created_at.

    Used by the dispatcher worker. The outbox publisher role bypasses RLS
    (ani_outbox_publisher BYPASSRLS), so this query runs without tenant
    context — it scans across tenants as required by the dispatcher.
    """
    rows = await conn.fetch(
        """
        SELECT id, aggregate_type, aggregate_id, event_type, tenant_id,
               payload, published, published_at, created_at
          FROM outbox_events
         WHERE published = FALSE
         ORDER BY created_at ASC
         LIMIT $1
        """,
        limit,
    )
    return [dict(r) for r in rows]


async def mark_dispatched(
    conn: asyncpg.Connection, *, event_id: int
) -> bool:
    """Mark an outbox event as published.

    Runs as the caller's role; the dispatcher uses the ani_outbox_publisher
    BYPASSRLS role (see list_undispatched), so this UPDATE is NOT RLS-scoped
    — it marks by primary key across all tenants, which is the correct
    behavior for the cross-tenant dispatcher.
    """
    result = await conn.execute(
        """
        UPDATE outbox_events
           SET published = TRUE, published_at = now()
         WHERE id = $1
        """,
        event_id,
    )
    return result == "UPDATE 1"


async def mark_dispatched_batch(
    conn: asyncpg.Connection, *, event_ids: list[int]
) -> int:
    """Mark multiple outbox events as published in one UPDATE (BYPASSRLS).

    Returns the number of rows updated. Like mark_dispatched, runs under the
    caller's role (dispatcher uses ani_outbox_publisher BYPASSRLS), so it is
    NOT RLS-scoped — it marks by primary key across all tenants.
    """
    if not event_ids:
        return 0
    result = await conn.execute(
        """
        UPDATE outbox_events
           SET published = TRUE, published_at = now()
         WHERE id = ANY($1::int[])
        """,
        event_ids,
    )
    # asyncpg execute returns "UPDATE N"; parse the count.
    try:
        return int(result.split()[-1])
    except (IndexError, ValueError):
        return 0
