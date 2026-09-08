-- Upgrade pre-C41 policy key rows without granting new scope matches.
BEGIN;

ALTER TABLE inference_access_policies DISABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_api_keys DISABLE ROW LEVEL SECURITY;

DO $$
DECLARE
    legacy_two_column_primary_key BOOLEAN;
BEGIN
    SELECT EXISTS (
        SELECT 1
        FROM pg_constraint AS constraint
        WHERE constraint.conrelid = 'inference_access_policy_api_keys'::regclass
          AND constraint.contype = 'p'
          AND pg_get_constraintdef(constraint.oid) = 'PRIMARY KEY (policy_id, api_key_id)'
    )
    INTO legacy_two_column_primary_key;

    IF legacy_two_column_primary_key THEN
        IF EXISTS (
            SELECT 1
            FROM inference_access_policies AS policy
            WHERE policy.status = 'enabled'
              AND policy.scope_type IN ('api_key', 'inference_service_api_key')
              AND EXISTS (
                  SELECT 1
                  FROM inference_access_policy_api_keys AS key
                  WHERE key.policy_id = policy.id AND key.tenant_id = policy.tenant_id
              )
        ) THEN
            RAISE EXCEPTION 'C41_ACCESS_POLICY_SCOPE_RECONCILIATION_REQUIRED';
        END IF;
    END IF;
END $$;

ALTER TABLE inference_access_policy_api_keys
    DROP CONSTRAINT IF EXISTS inference_access_policy_api_keys_effect_check;
ALTER TABLE inference_access_policy_api_keys
    ADD CONSTRAINT inference_access_policy_api_keys_effect_check
    CHECK (effect IN ('scope', 'allow', 'deny'));

ALTER TABLE inference_access_policy_api_keys
    DROP CONSTRAINT IF EXISTS inference_access_policy_api_keys_pkey;
ALTER TABLE inference_access_policy_api_keys
    ADD CONSTRAINT inference_access_policy_api_keys_pkey
    PRIMARY KEY (policy_id, api_key_id, effect);

ALTER TABLE inference_access_policy_api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_api_keys FORCE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policies FORCE ROW LEVEL SECURITY;

COMMIT;
