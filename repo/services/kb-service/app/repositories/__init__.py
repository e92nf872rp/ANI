"""kb-service data access layer (SPEC §2.4, §8.1).

All repositories use asyncpg and set the RLS tenant context via
`SET LOCAL app.current_tenant_id = $1` inside each transaction so that
PostgreSQL Row Level Security policies filter rows transparently.
"""
