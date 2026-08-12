"""kb_messages repository (SPEC §2.4, §6.1, §4.2).

Covers the Query flow: insert user + assistant messages into kb_messages
with RLS tenant filtering. Session history is also read here for multi-turn
context (Redis cache is a best-effort layer on top).

All access goes through the Core data plane (`CoreClient.data_query`). RLS
tenant filtering is applied by Core based on the `X-Tenant-Id` header carried
by the CoreClient (role="tenant").

The Core data plane runs each /data/query call as a single transaction, so the
former `create_session_in_tx` + `insert_message_in_tx` (which shared a single
transaction) are replaced by `create_session_and_message` — a single CTE-based
SQL statement that inserts the session (ON CONFLICT DO NOTHING) and the user
message atomically (SPEC §4.2 cross-table atomic fold).
"""
from __future__ import annotations

import json
from typing import Any

from app.core_api.client import CoreClient


async def create_session(
    core: CoreClient,
    *,
    tenant_id: str,
    kb_id: str,
    user_id: str | None = None,
    title: str | None = None,
    session_id: str | None = None,
) -> str:
    """Insert a kb_sessions row if the session_id is new; return the session id.

    Uses ON CONFLICT (id) DO NOTHING so repeated calls for the same session_id
    (e.g. multi-turn Query RPCs that reuse session_id) don't create duplicate
    rows — the existing row is kept and its id returned (SPEC §6.1 Query
    step 2). RLS-scoped via Core.
    """
    sql = """
        INSERT INTO kb_sessions (id, kb_id, tenant_id, user_id, title)
        VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5)
        ON CONFLICT (id) DO NOTHING
        RETURNING id
    """
    result = await core.data_query(
        sql=sql,
        params=[session_id, kb_id, tenant_id, user_id, title],
    )
    rows = result.get("rows", [])
    if rows:
        return str(rows[0]["id"])
    # ON CONFLICT DO NOTHING returned no row → session_id was provided and
    # already exists; return the provided id as-is (B1 fix).
    assert session_id is not None
    return str(session_id)


async def insert_message(
    core: CoreClient,
    *,
    tenant_id: str,
    session_id: str,
    role: str,
    content: str,
    source_chunks: list[dict[str, Any]] | None = None,
    input_tokens: int | None = None,
    output_tokens: int | None = None,
    duration_ms: int | None = None,
) -> dict[str, Any]:
    """INSERT a kb_messages row and return it (RLS-scoped via Core).
    """
    chunks_json = json.dumps(source_chunks) if source_chunks else None
    sql = """
        INSERT INTO kb_messages
            (session_id, tenant_id, role, content, source_chunks,
             input_tokens, output_tokens, duration_ms)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        RETURNING id, session_id, tenant_id, role, content,
                  source_chunks, input_tokens, output_tokens,
                  duration_ms, created_at
    """
    result = await core.data_query(
        sql=sql,
        params=[
            session_id, tenant_id, role, content, chunks_json,
            input_tokens, output_tokens, duration_ms,
        ],
    )
    rows = result.get("rows", [])
    if not rows:
        raise RuntimeError("kb_messages INSERT returned no row")
    return rows[0]


async def create_session_and_message(
    core: CoreClient,
    *,
    tenant_id: str,
    kb_id: str,
    session_id: str,
    role: str,
    content: str,
    user_id: str | None = None,
    title: str | None = None,
    source_chunks: list[dict[str, Any]] | None = None,
    input_tokens: int | None = None,
    output_tokens: int | None = None,
    duration_ms: int | None = None,
) -> dict[str, Any]:
    """Atomically insert a kb_sessions row (if new) + a kb_messages row.

    Folds the former `create_session_in_tx` + `insert_message_in_tx` (which
    shared a single transaction) into a single CTE-based SQL statement so
    both writes commit atomically inside the Core data plane's single
    transaction (SPEC §4.2 cross-table atomic fold).

    The CTE `sess` inserts the session with ON CONFLICT (id) DO NOTHING and
    returns the effective session id (existing or newly generated). The
    main INSERT then inserts the message referencing that session id.

    Returns the inserted kb_messages row.
    """
    chunks_json = json.dumps(source_chunks) if source_chunks else None
    sql = """
        WITH sess AS (
            INSERT INTO kb_sessions (id, kb_id, tenant_id, user_id, title)
            VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5)
            ON CONFLICT (id) DO NOTHING
            RETURNING id
        ),
        effective_session AS (
            SELECT COALESCE(
                (SELECT id FROM sess),
                $1::uuid
            ) AS session_id
        )
        INSERT INTO kb_messages
            (session_id, tenant_id, role, content, source_chunks,
             input_tokens, output_tokens, duration_ms)
        SELECT es.session_id, $3, $6, $7, $8, $9, $10, $11
          FROM effective_session es
        RETURNING id, session_id, tenant_id, role, content,
                  source_chunks, input_tokens, output_tokens,
                  duration_ms, created_at
    """
    result = await core.data_query(
        sql=sql,
        params=[
            session_id, kb_id, tenant_id, user_id, title,
            role, content, chunks_json,
            input_tokens, output_tokens, duration_ms,
        ],
    )
    rows = result.get("rows", [])
    if not rows:
        raise RuntimeError("create_session_and_message INSERT returned no row")
    return rows[0]


async def list_session_messages(
    core: CoreClient,
    *,
    tenant_id: str,
    session_id: str,
    limit: int = 20,
) -> list[dict[str, Any]]:
    """List messages in a session ordered by created_at (RLS-scoped via Core)."""
    sql = """
        SELECT id, session_id, tenant_id, role, content,
               source_chunks, input_tokens, output_tokens,
               duration_ms, created_at
          FROM kb_messages
         WHERE session_id = $1
         ORDER BY created_at ASC
         LIMIT $2
    """
    result = await core.data_query(sql=sql, params=[session_id, limit])
    return result.get("rows", [])
