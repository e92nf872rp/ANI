"""kb_permissions repository (B4, plan §2.4).

Covers the KB-level ACL store (`kb_permissions` table, migration 005) with
RLS tenant filtering. A KB with no permission row keeps the defaults
(public_read=false, allowed_user_ids=[]) — the read path returns defaults
instead of 404 (plan §2.5 GetKBPermissions contract).
"""
from __future__ import annotations

import uuid
from typing import Any

import asyncpg

from .rls import set_tenant_context


async def get_permissions(
    conn: asyncpg.Connection, *, tenant_id: str, kb_id: str
) -> dict[str, Any]:
    """Read the permission row; return defaults when no row exists (not 404).

    RLS-scoped (tenant_isolation policy on kb_permissions).
    """
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            """
            SELECT kb_id, public_read, allowed_user_ids, updated_at
              FROM kb_permissions
             WHERE kb_id = $1
            """,
            uuid.UUID(kb_id),
        )
    if row is None:
        return {
            "kb_id": kb_id,
            "public_read": False,
            "allowed_user_ids": [],
            "updated_at": None,
        }
    return {
        "kb_id": str(row["kb_id"]),
        "public_read": row["public_read"],
        "allowed_user_ids": [str(u) for u in row["allowed_user_ids"]],
        "updated_at": row["updated_at"],
    }


async def upsert_permissions_in_tx(
    conn: asyncpg.Connection,
    *,
    tenant_id: str,
    kb_id: str,
    public_read: bool,
    allowed_user_ids: list[str],
) -> None:
    """UPSERT the permission row (caller-owned transaction, RLS-scoped).

    Commits atomically together with the idempotency task record written by
    the servicer (plan §2.5 UpdateKBPermissions step 2c).
    """
    await set_tenant_context(conn, tenant_id)
    await conn.execute(
        """
        INSERT INTO kb_permissions (kb_id, tenant_id, public_read, allowed_user_ids)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (kb_id) DO UPDATE
          SET public_read = EXCLUDED.public_read,
              allowed_user_ids = EXCLUDED.allowed_user_ids,
              updated_at = now()
        """,
        uuid.UUID(kb_id),
        uuid.UUID(tenant_id),
        public_read,
        [uuid.UUID(u) for u in allowed_user_ids],
    )
