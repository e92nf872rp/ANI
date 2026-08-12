"""async_tasks repository (SPEC §2.4, §6.1, §4.2).

Covers the async_tasks table used for idempotency replay and parse task
tracking. CreateKB / NotifyDocumentUploaded write a task here to record the
idempotency_key result so retries return the same response.

All access goes through the Core data plane (`CoreClient.data_query`). RLS
tenant filtering is applied by Core based on the `X-Tenant-Id` header carried
by the CoreClient (role="tenant").
"""
from __future__ import annotations

import json
from typing import Any

from app.core_api.client import CoreClient


async def find_by_idempotency_key(
    core: CoreClient,
    *,
    tenant_id: str,
    idempotency_key: str,
) -> dict[str, Any] | None:
    """Look up an existing async_task by (tenant_id, idempotency_key).

    Returns the row (including `result`) if a prior call with the same key
    succeeded, enabling idempotent replay (SPEC §6.1, §6.4).
    """
    sql = """
        SELECT id, tenant_id, idempotency_key, task_type, resource_type,
               resource_id, status, attempt_count, max_attempts,
               progress_pct, payload, result, error_message,
               started_at, completed_at, created_at, updated_at
          FROM async_tasks
         WHERE tenant_id = $1 AND idempotency_key = $2
    """
    result = await core.data_query(sql=sql, params=[tenant_id, idempotency_key])
    rows = result.get("rows", [])
    return rows[0] if rows else None


async def create_task(
    core: CoreClient,
    *,
    tenant_id: str,
    idempotency_key: str,
    task_type: str,
    resource_type: str | None = None,
    resource_id: str | None = None,
    payload: dict[str, Any] | None = None,
    status: str = "pending",
) -> dict[str, Any]:
    """INSERT a new async_tasks row and return it (RLS-scoped via Core).

    `idempotency_key` is UNIQUE per tenant so a duplicate insert raises a
    unique violation (mapped to CoreAPIError by the data plane); callers
    should check find_by_idempotency_key first.
    """
    payload_json = json.dumps(payload or {}, default=str)
    sql = """
        INSERT INTO async_tasks
            (tenant_id, idempotency_key, task_type, resource_type,
             resource_id, status, payload)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING id, tenant_id, idempotency_key, task_type,
                  resource_type, resource_id, status, attempt_count,
                  max_attempts, progress_pct, payload, result,
                  error_message, started_at, completed_at,
                  created_at, updated_at
    """
    result = await core.data_query(
        sql=sql,
        params=[
            tenant_id, idempotency_key, task_type,
            resource_type, resource_id, status, payload_json,
        ],
    )
    rows = result.get("rows", [])
    if not rows:
        raise RuntimeError("async_tasks INSERT returned no row")
    return rows[0]


async def complete_task(
    core: CoreClient,
    *,
    tenant_id: str,
    task_id: str,
    result: dict[str, Any] | None = None,
    status: str = "completed",
) -> bool:
    """Mark a task completed with its result (RLS-scoped via Core)."""
    result_json = json.dumps(result, default=str) if result else None
    sql = """
        UPDATE async_tasks
           SET status = $2, result = $3, completed_at = now(),
               updated_at = now()
         WHERE id = $1
    """
    res = await core.data_query(
        sql=sql, params=[task_id, status, result_json]
    )
    return res.get("rowcount", 0) == 1


async def get_task(
    core: CoreClient, *, tenant_id: str, task_id: str
) -> dict[str, Any] | None:
    """SELECT a single async_tasks row by id (RLS-scoped via Core)."""
    sql = """
        SELECT id, tenant_id, idempotency_key, task_type, resource_type,
               resource_id, status, attempt_count, max_attempts,
               progress_pct, payload, result, error_message,
               started_at, completed_at, created_at, updated_at
          FROM async_tasks
         WHERE id = $1
    """
    result = await core.data_query(sql=sql, params=[task_id])
    rows = result.get("rows", [])
    return rows[0] if rows else None
