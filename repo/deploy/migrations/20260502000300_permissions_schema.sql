-- ANI Platform · Migration 003
-- Description: Define and enforce permissions JSONB schema on roles table
-- Depends on: 20260501000100_init_schema.sql
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


-- PostgreSQL does not allow subqueries in CHECK constraints. Keep the
-- validation in an immutable function so the constraint remains enforced.
CREATE OR REPLACE FUNCTION ani_permissions_schema_valid(p_permissions JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
AS $function$
DECLARE
    elem JSONB;
BEGIN
    IF p_permissions IS NULL OR jsonb_typeof(p_permissions) <> 'array' THEN
        RETURN FALSE;
    END IF;

    FOR elem IN SELECT jsonb_array_elements(p_permissions)
    LOOP
        IF jsonb_typeof(elem) <> 'object' THEN
            RETURN FALSE;
        END IF;
        IF jsonb_typeof(elem->'resource') <> 'string' THEN
            RETURN FALSE;
        END IF;
        IF jsonb_typeof(elem->'actions') <> 'array' THEN
            RETURN FALSE;
        END IF;
        IF jsonb_array_length(elem->'actions') = 0 THEN
            RETURN FALSE;
        END IF;
        IF elem ? 'scope' THEN
            IF jsonb_typeof(elem->'scope') <> 'string' THEN
                RETURN FALSE;
            END IF;
            IF (elem->>'scope') NOT IN ('tenant', 'own', 'platform') THEN
                RETURN FALSE;
            END IF;
        END IF;
    END LOOP;

    RETURN TRUE;
END;
$function$;

-- The initial schema seeded legacy string permissions. Normalize those
-- built-in rows before adding the new constraint, while preserving their IDs
-- for any existing user_roles references.
UPDATE roles
SET permissions = CASE name
    WHEN 'platform-admin' THEN '[{"resource":"*","actions":["*"],"scope":"platform"}]'::jsonb
    WHEN 'tenant-admin' THEN '[{"resource":"*","actions":["*"],"scope":"tenant"}]'::jsonb
    WHEN 'user' THEN '[
        {"resource":"instances","actions":["create","read","list","start","stop","restart","delete"],"scope":"own"},
        {"resource":"networks","actions":["read","list"],"scope":"tenant"},
        {"resource":"volumes","actions":["create","read","list","delete"],"scope":"own"},
        {"resource":"objects","actions":["read","list"],"scope":"tenant"},
        {"resource":"gpu-inventory","actions":["read","list"],"scope":"tenant"}
    ]'::jsonb
    WHEN 'auditor' THEN '[{"resource":"*","actions":["read","list"],"scope":"tenant"}]'::jsonb
    ELSE permissions
END
WHERE tenant_id IS NULL
  AND name IN ('platform-admin', 'tenant-admin', 'user', 'auditor');

ALTER TABLE roles
    ADD CONSTRAINT roles_permissions_schema CHECK (
        ani_permissions_schema_valid(permissions)
    );

-- Platform built-in roles (tenant_id IS NULL = system-wide). PostgreSQL
-- UNIQUE (tenant_id, name) does not treat NULL tenant IDs as duplicates, so
-- use explicit existence checks instead of ON CONFLICT on that key.
INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000001', NULL, 'platform-admin',
       '[{"resource":"*","actions":["*"],"scope":"platform"}]'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'platform-admin'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000002', NULL, 'tenant-admin',
       '[{"resource":"*","actions":["*"],"scope":"tenant"}]'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'tenant-admin'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000003', NULL, 'user',
       '[
           {"resource":"instances","actions":["create","read","list","start","stop","restart","delete"],"scope":"own"},
           {"resource":"networks","actions":["read","list"],"scope":"tenant"},
           {"resource":"volumes","actions":["create","read","list","delete"],"scope":"own"},
           {"resource":"objects","actions":["read","list"],"scope":"tenant"},
           {"resource":"gpu-inventory","actions":["read","list"],"scope":"tenant"}
       ]'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'user'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO roles (id, tenant_id, name, permissions)
SELECT '00000000-0000-0000-0000-000000000004', NULL, 'auditor',
       '[{"resource":"*","actions":["read","list"],"scope":"tenant"}]'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM roles WHERE tenant_id IS NULL AND name = 'auditor'
)
ON CONFLICT (id) DO NOTHING;

COMMENT ON CONSTRAINT roles_permissions_schema ON roles IS
    'Enforces {resource:string, actions:[string+], scope?:tenant|own|platform}. '
    'See migration 003 header for full format docs.';

