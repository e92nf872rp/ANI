"""outbox_events repository (SPEC §2.4, §6.1, §4.2).

Covers the outbox pattern: NotifyDocumentUploaded writes an outbox event
inline in the same CTE-based fold_sql as the business write (SPEC §4.2
cross-table atomic fold), and a separate dispatcher polls undispatched
events to publish to NATS (US-010 wires the dispatcher).

All access goes through the Core data plane (`CoreClient.data_query`). The
dispatcher uses `role="service"` for cross-tenant scanning (SPEC §4.2,
mirroring the former `ani_outbox_publisher` BYPASSRLS role).
"""
from __future__ import annotations

from typing import Any

from app.core_api.client import CoreClient


async def list_undispatched(
    core: CoreClient, *, limit: int = 100
) -> list[dict[str, Any]]:
    """List undispatched outbox events ordered by created_at (cross-tenant).

    Used by the dispatcher worker. The outbox publisher role bypasses RLS
    (former `ani_outbox_publisher` BYPASSRLS), so this query runs with
    `role="service"` (cross-tenant, SPEC §4.2) — it scans across tenants as
    required by the dispatcher.
    """
    sql = """
        SELECT id, aggregate_type, aggregate_id, event_type, tenant_id,
               payload, published, published_at, created_at
          FROM outbox_events
         WHERE published = FALSE
         ORDER BY created_at ASC
         LIMIT $1
    """
    result = await core.data_query(sql=sql, params=[limit], role="service")
    return result.get("rows", [])


async def mark_dispatched(
    core: CoreClient, *, event_id: int
) -> bool:
    """Mark an outbox event as published (cross-tenant, role=service).

    Runs as the service role (cross-tenant, SPEC §4.2), so this UPDATE is NOT
    RLS-scoped — it marks by primary key across all tenants, which is the
    correct behavior for the cross-tenant dispatcher.
    """
    sql = """
        UPDATE outbox_events
           SET published = TRUE, published_at = now()
         WHERE id = $1
    """
    result = await core.data_query(sql=sql, params=[event_id], role="service")
    return result.get("rowcount", 0) == 1


async def mark_dispatched_batch(
    core: CoreClient, *, event_ids: list[int]
) -> int:
    """Mark multiple outbox events as published in one UPDATE (cross-tenant).

    Returns the number of rows updated. Like mark_dispatched, runs under the
    service role (cross-tenant, SPEC §4.2), so it is NOT RLS-scoped — it marks
    by primary key across all tenants.
    """
    if not event_ids:
        return 0
    # Use ANY($1::int[]) so the whole batch is one parameterized statement.
    sql = """
        UPDATE outbox_events
           SET published = TRUE, published_at = now()
         WHERE id = ANY($1::int[])
    """
    result = await core.data_query(sql=sql, params=[event_ids], role="service")
    return result.get("rowcount", 0)
