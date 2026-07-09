-- ANI Platform · Migration 003
-- Description: Define and enforce permissions JSONB schema on roles table
-- Depends on: 20260501_001_init_schema.sql
-- Run: atlas migrate apply  OR  psql $DATABASE_URL -f <this_file>

-- ===========================================================================
-- Canonical permission entry format:
--   {
--     "resource":   "instances",                        (required, string)
--     "actions":    ["create","read","list","delete"],   (required, non-empty array)
--     "scope":      "tenant"                            (optional: tenant|own|platform)
--   }
-- Valid resources: instances | networks | volumes | filesystems | objects |
--   vector-stores | k8s-clusters | baremetal-hosts | gpu-inventory |
--   dpu-inventory | registry | encryption | secrets | metering |
--   observability | notifications | audit | users | tenants | api-keys
-- Wildcard "*" is allowed for platform-admin roles only.
-- ===========================================================================

BEGIN;

ALTER TABLE roles
    ADD CONSTRAINT roles_permissions_schema CHECK (
        jsonb_typeof(permissions) = 'array'
        AND (
            jsonb_array_length(permissions) = 0
            OR NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(permissions) AS elem
                WHERE
                    jsonb_typeof(elem) <> 'object'
                    OR jsonb_typeof(elem->'resource') <> 'string'
                    OR jsonb_typeof(elem->'actions')  <> 'array'
                    OR jsonb_array_length(elem->'actions') = 0
                    OR (elem ? 'scope'
                        AND (elem->>'scope') NOT IN ('tenant','own','platform'))
            )
        )
    );

-- Platform built-in roles (tenant_id IS NULL = system-wide)
INSERT INTO roles (id, tenant_id, name, permissions) VALUES
    (
        '00000000-0000-0000-0000-000000000001',
        NULL, 'platform-admin',
        '[{"resource":"*","actions":["*"],"scope":"platform"}]'
    ),
    (
        '00000000-0000-0000-0000-000000000002',
        NULL, 'tenant-admin',
        '[{"resource":"*","actions":["*"],"scope":"tenant"}]'
    ),
    (
        '00000000-0000-0000-0000-000000000003',
        NULL, 'user',
        '[
            {"resource":"instances",    "actions":["create","read","list","start","stop","restart","delete","exec"],"scope":"own"},
            {"resource":"networks",     "actions":["read","list"],"scope":"tenant"},
            {"resource":"volumes",      "actions":["create","read","list","delete"],"scope":"own"},
            {"resource":"objects",      "actions":["read","list"],"scope":"tenant"},
            {"resource":"gpu-inventory","actions":["read","list"],"scope":"tenant"}
        ]'
    ),
    (
        '00000000-0000-0000-0000-000000000004',
        NULL, 'auditor',
        '[{"resource":"*","actions":["read","list"],"scope":"tenant"}]'
    )
ON CONFLICT (tenant_id, name) DO UPDATE
    SET permissions = EXCLUDED.permissions;

COMMENT ON CONSTRAINT roles_permissions_schema ON roles IS
    'Enforces {resource:string, actions:[string+], scope?:tenant|own|platform}. '
    'See migration 003 header for full format docs.';

COMMIT;
