"""kb_messages repository (SPEC §2.4, §6.1).

Covers the Query flow: insert user + assistant messages into kb_messages
with RLS tenant filtering. Session history is also read here for multi-turn
context (Redis cache is a best-effort layer on top).
"""
from __future__ import annotations

import uuid
from datetime import datetime
from typing import Any

import asyncpg

from .cursor import parse_composite_cursor
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


async def list_sessions(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    limit: int = 20,
    cursor: str | None = None,
) -> list[dict[str, Any]]:
    """List sessions of a KB with aggregates (SPEC §5.1 #16, RLS-scoped).

    Single aggregate SQL: message_count = COUNT(m.id), last_active_at =
    MAX(m.created_at), last_query = correlated subquery taking the earliest
    user message content. Keyset pagination over (created_at, id) DESC —
    cursor format ``{created_at_iso}|{session_id}``.
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
                SELECT s.id, s.kb_id, s.created_at,
                       COUNT(m.id) AS message_count,
                       MAX(m.created_at) AS last_active_at,
                       (SELECT m2.content FROM kb_messages m2
                         WHERE m2.session_id = s.id AND m2.role = 'user'
                         ORDER BY m2.created_at ASC LIMIT 1) AS last_query
                  FROM kb_sessions s
                  LEFT JOIN kb_messages m ON m.session_id = s.id
                 WHERE s.kb_id = $1
                   AND (s.created_at, s.id) < ($2, $3)
                 GROUP BY s.id
                 ORDER BY s.created_at DESC, s.id DESC
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
                SELECT s.id, s.kb_id, s.created_at,
                       COUNT(m.id) AS message_count,
                       MAX(m.created_at) AS last_active_at,
                       (SELECT m2.content FROM kb_messages m2
                         WHERE m2.session_id = s.id AND m2.role = 'user'
                         ORDER BY m2.created_at ASC LIMIT 1) AS last_query
                  FROM kb_sessions s
                  LEFT JOIN kb_messages m ON m.session_id = s.id
                 WHERE s.kb_id = $1
                 GROUP BY s.id
                 ORDER BY s.created_at DESC, s.id DESC
                 LIMIT $2
                """,
                uuid.UUID(kb_id),
                limit,
            )
    return [dict(r) for r in rows]


async def get_session(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    session_id: str,
) -> dict[str, Any] | None:
    """SELECT a session with KB-ownership check (RLS-scoped, SPEC §5.1 #17).

    The session row must belong to the path kb_id; otherwise None → 404
    (no existence leak across KBs of the same tenant).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT id, kb_id, tenant_id, user_id, title, created_at
              FROM kb_sessions
             WHERE id = $1 AND kb_id = $2
            """,
            uuid.UUID(session_id),
            uuid.UUID(kb_id),
        )
    return dict(row) if row else None


async def list_session_messages_paged(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    session_id: str,
    limit: int = 100,
    cursor: str | None = None,
) -> list[dict[str, Any]]:
    """List session messages with keyset pagination (SPEC §5.1 #17, RLS-scoped).

    ORDER BY created_at ASC, id ASC — id is a random UUID (gen_random_uuid)
    so the composite key is mandatory as a tie-break for same-second user +
    assistant messages written in separate transactions. Cursor format
    ``{created_at_iso}|{message_id}``.
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
                SELECT id, session_id, tenant_id, role, content,
                       source_chunks, input_tokens, output_tokens,
                       duration_ms, created_at
                  FROM kb_messages
                 WHERE session_id = $1
                   AND (created_at, id) > ($2, $3)
                 ORDER BY created_at ASC, id ASC
                 LIMIT $4
                """,
                uuid.UUID(session_id),
                cursor_ts,
                cursor_id,
                limit,
            )
        else:
            rows = await conn.fetch(
                """
                SELECT id, session_id, tenant_id, role, content,
                       source_chunks, input_tokens, output_tokens,
                       duration_ms, created_at
                  FROM kb_messages
                 WHERE session_id = $1
                 ORDER BY created_at ASC, id ASC
                 LIMIT $2
                """,
                uuid.UUID(session_id),
                limit,
            )
    return [dict(r) for r in rows]


async def delete_session(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    session_id: str,
) -> bool:
    """Delete a session and its messages in a single transaction (SPEC §5.1 #18).

    Both deletes are scoped by the kb_id ownership condition: the messages
    delete goes through a subquery on kb_sessions, so a session_id belonging
    to another KB of the same tenant is never touched (no cross-KB deletion,
    symmetric with get_session's ownership check). kb_messages is deleted
    explicitly first (the ON DELETE CASCADE would also cover it — kept
    explicit to align with the *_in_tx pattern and make test assertions
    possible), then kb_sessions with the same kb_id condition. Returns True
    when the session row existed (and was deleted), False when it did not
    (idempotent 204 for the servicer).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        await conn.execute(
            """
            DELETE FROM kb_messages
             WHERE session_id = $1
               AND session_id IN (SELECT id FROM kb_sessions WHERE id = $1 AND kb_id = $2)
            """,
            uuid.UUID(session_id),
            uuid.UUID(kb_id),
        )
        deleted = await conn.fetchval(
            """
            DELETE FROM kb_sessions
             WHERE id = $1 AND kb_id = $2
            RETURNING id
            """,
            uuid.UUID(session_id),
            uuid.UUID(kb_id),
        )
    return deleted is not None


async def list_citation_messages_paged(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    limit: int = 20,
    cursor: str | None = None,
) -> list[dict[str, Any]]:
    """Page assistant messages carrying source_chunks (SPEC §5.1 #15, RLS-scoped).

    Citations are expanded client-side from kb_messages.source_chunks JSONB,
    so pagination runs at message granularity: this returns the assistant
    messages of the KB whose source_chunks is non-NULL and not the 'null'
    string, ordered (created_at, id) DESC with a ``{created_at_iso}|{message_id}``
    composite keyset cursor. The servicer expands each message's JSON into
    per-(message, doc) citations.
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
                SELECT m.id, m.session_id, m.created_at, m.source_chunks
                  FROM kb_messages m
                  JOIN kb_sessions s ON s.id = m.session_id
                 WHERE s.kb_id = $1 AND m.role = 'assistant'
                   AND m.source_chunks IS NOT NULL AND m.source_chunks <> 'null'
                   AND (m.created_at, m.id) < ($2, $3)
                 ORDER BY m.created_at DESC, m.id DESC
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
                SELECT m.id, m.session_id, m.created_at, m.source_chunks
                  FROM kb_messages m
                  JOIN kb_sessions s ON s.id = m.session_id
                 WHERE s.kb_id = $1 AND m.role = 'assistant'
                   AND m.source_chunks IS NOT NULL AND m.source_chunks <> 'null'
                 ORDER BY m.created_at DESC, m.id DESC
                 LIMIT $2
                """,
                uuid.UUID(kb_id),
                limit,
            )
    return [dict(r) for r in rows]
