"""Row Level Security helper for kb-service repositories (SPEC §8.1, FR-15).

All KB tables have RLS enabled with a restrictive policy:
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)

Each repository method MUST call `set_tenant_context(conn, tenant_id)` inside
the active transaction before any business SQL so that RLS filters rows by
tenant. This mirrors the pattern used by the Go services'
`pkg/adapters/runtime` stores.
"""
from __future__ import annotations

import asyncpg


async def set_tenant_context(conn: asyncpg.Connection, tenant_id: str) -> None:
    """Set the RLS tenant context for the current transaction.

    Must be called inside an active transaction (`conn.transaction()` or a
    pooled connection with `SET LOCAL`) so the setting is scoped to the tx.

    Note: PostgreSQL `SET LOCAL` does not support parameter binding ($1), so
    the tenant_id is safely inlined as a quoted string literal. The tenant_id
    is a UUID validated upstream (kb-service casts it to uuid.UUID before
    calling repositories), so SQL injection is not a concern here.
    """
    # Validate that tenant_id is a valid UUID before inlining it as a literal,
    # to defend against any unexpected non-UUID input.
    import uuid as _uuid

    _uuid.UUID(tenant_id)  # raises ValueError if not a valid UUID
    await conn.execute(f"SET LOCAL app.current_tenant_id = '{tenant_id}'")
