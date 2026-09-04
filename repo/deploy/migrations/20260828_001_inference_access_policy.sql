-- C42 tenant-scoped inference access policies, bindings, and redacted events.
BEGIN;

CREATE TABLE IF NOT EXISTS inference_access_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'disabled' CHECK (status IN ('enabled','disabled')),
    description TEXT,
    priority INT NOT NULL DEFAULT 100 CHECK (priority >= 0),
    scope_type TEXT NOT NULL CHECK (scope_type IN ('tenant_default','inference_service','api_key','inference_service_api_key')),
    allow_all_tenant_keys BOOLEAN NOT NULL DEFAULT FALSE,
    rate_qps INT CHECK (rate_qps IS NULL OR rate_qps > 0),
    rate_rpm INT CHECK (rate_rpm IS NULL OR rate_rpm > 0),
    max_in_flight INT CHECK (max_in_flight IS NULL OR max_in_flight > 0),
    lease_ttl_seconds INT NOT NULL DEFAULT 60 CONSTRAINT inference_access_policies_lease_ttl_seconds_check CHECK (lease_ttl_seconds BETWEEN 1 AND 3600),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_inference_access_policies_tenant_name
    ON inference_access_policies(tenant_id, name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS inference_access_policy_services (
    policy_id UUID NOT NULL REFERENCES inference_access_policies(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    inference_service_id UUID NOT NULL REFERENCES inference_services(id) ON DELETE CASCADE,
    PRIMARY KEY (policy_id, inference_service_id),
    UNIQUE (tenant_id, inference_service_id, policy_id)
);
CREATE INDEX IF NOT EXISTS idx_inference_access_policy_services_tenant_service
    ON inference_access_policy_services(tenant_id, inference_service_id);

CREATE TABLE IF NOT EXISTS inference_access_policy_api_keys (
    policy_id UUID NOT NULL REFERENCES inference_access_policies(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    api_key_id UUID NOT NULL,
    key_prefix TEXT NOT NULL,
    effect TEXT NOT NULL CONSTRAINT inference_access_policy_api_keys_effect_check CHECK (effect IN ('scope','allow','deny')),
    CONSTRAINT inference_access_policy_api_keys_pkey PRIMARY KEY (policy_id, api_key_id, effect)
);
CREATE INDEX IF NOT EXISTS idx_inference_access_policy_api_keys_tenant_key
    ON inference_access_policy_api_keys(tenant_id, api_key_id);

CREATE TABLE IF NOT EXISTS inference_access_policy_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    policy_id UUID REFERENCES inference_access_policies(id) ON DELETE SET NULL,
    inference_service_id UUID REFERENCES inference_services(id) ON DELETE SET NULL,
    api_key_id UUID,
    key_prefix TEXT,
    request_id TEXT,
    openai_path TEXT,
    external_model TEXT,
    decision TEXT NOT NULL CHECK (decision IN ('allow','deny','rate_limited','concurrency_limited','policy_unavailable')),
    reason_code TEXT NOT NULL,
    http_status INT NOT NULL CHECK (http_status BETWEEN 100 AND 599),
    retry_after_seconds INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_inference_access_policy_events_tenant_created
    ON inference_access_policy_events(tenant_id, created_at DESC);

ALTER TABLE inference_access_policies ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policies FORCE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_services FORCE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_api_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_api_keys FORCE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE inference_access_policy_events FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS inference_access_policies_tenant_isolation ON inference_access_policies;
CREATE POLICY inference_access_policies_tenant_isolation ON inference_access_policies
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS inference_access_policy_services_tenant_isolation ON inference_access_policy_services;
CREATE POLICY inference_access_policy_services_tenant_isolation ON inference_access_policy_services
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS inference_access_policy_api_keys_tenant_isolation ON inference_access_policy_api_keys;
CREATE POLICY inference_access_policy_api_keys_tenant_isolation ON inference_access_policy_api_keys
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS inference_access_policy_events_tenant_isolation ON inference_access_policy_events;
CREATE POLICY inference_access_policy_events_tenant_isolation ON inference_access_policy_events
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

COMMIT;
