"""kb_messages repository (SPEC §2.4, §6.1).

Covers the Query flow: insert user + assistant messages into kb_messages
with RLS tenant filtering. Session history is also read here for multi-turn
context (Redis cache is a best-effort layer on top).
"""
from __future__ import annotations

import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


async def create_session(
    conn: asyncpg.Connection,
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
    rows — the existing row is kept and its id returned (SPEC §6.1 Query step 2).
    RLS-scoped. Opens its own transaction; use create_session_in_tx when the
    insert must participate in an outer transaction.
    """
    async with conn.transaction():
        return await create_session_in_tx(
            conn,
            tenant_id=tenant_id,
            kb_id=kb_id,
            user_id=user_id,
            title=title,
            session_id=session_id,
        )


async def create_session_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    user_id: str | None = None,
    title: str | None = None,
    session_id: str | None = None,
) -> str:
    """Like create_session but runs inside the caller's transaction (no own tx).

    Used by Query so create_session + insert_message(user) commit atomically
    (SPEC §6.1, US-010). Does NOT call set_tenant_context itself when the
    caller has already set it; but to be safe and self-contained we set it
    here too (asyncpg session variables are connection-scoped, idempotent).
    """
    sid = uuid.UUID(session_id) if session_id else None
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        """
        INSERT INTO kb_sessions (id, kb_id, tenant_id, user_id, title)
        VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, $5)
        ON CONFLICT (id) DO NOTHING
        RETURNING id
        """,
        sid,
        uuid.UUID(kb_id),
        uuid.UUID(tenant_id),
        uuid.UUID(user_id) if user_id else None,
        title,
    )
    if row is not None:
        return str(row["id"])
    assert sid is not None
    return str(sid)


async def insert_message(
    conn: asyncpg.Connection,
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
    """INSERT a kb_messages row and return it (RLS-scoped).

    role must be 'user' or 'assistant' (CHECK constraint). Opens its own
    transaction; use insert_message_in_tx when the insert must participate
    in an outer transaction.
    """
    async with conn.transaction():
        return await insert_message_in_tx(
            conn,
            tenant_id=tenant_id,
            session_id=session_id,
            role=role,
            content=content,
            source_chunks=source_chunks,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            duration_ms=duration_ms,
        )


async def insert_message_in_tx(
    conn: asyncpg.Connection,
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
    """INSERT a kb_messages row inside the caller's transaction (RLS-scoped).

    Does NOT open its own transaction. Used by Query so create_session +
    insert_message(user) commit atomically (SPEC §6.1, US-010).
    """
    import json

    chunks_json = json.dumps(source_chunks) if source_chunks else None
    await set_tenant_context(conn, tenant_id)
    row = await conn.fetchrow(
        """
        INSERT INTO kb_messages
            (session_id, tenant_id, role, content, source_chunks,
             input_tokens, output_tokens, duration_ms)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        RETURNING id, session_id, tenant_id, role, content,
                  source_chunks, input_tokens, output_tokens,
                  duration_ms, created_at
        """,
        uuid.UUID(session_id),
        uuid.UUID(tenant_id),
        role,
        content,
        chunks_json,
        input_tokens,
        output_tokens,
        duration_ms,
    )
    return dict(row)


async def list_session_messages(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    session_id: str,
    limit: int = 20,
) -> list[dict[str, Any]]:
    """List messages in a session ordered by created_at (RLS-scoped)."""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        rows = await conn.fetch(
            """
            SELECT id, session_id, tenant_id, role, content,
                   source_chunks, input_tokens, output_tokens,
                   duration_ms, created_at
              FROM kb_messages
             WHERE session_id = $1
             ORDER BY created_at ASC
             LIMIT $2
            """,
            uuid.UUID(session_id),
            limit,
        )
    return [dict(r) for r in rows]
