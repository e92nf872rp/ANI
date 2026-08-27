-- ANI Platform · Migration 005
-- Description: M1-NETWORK-A Core network resource persistence
-- Depends on: 20260519000400_instance_u_vm_protection.sql


CREATE TABLE IF NOT EXISTS network_vpcs (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    vpc_id          TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    cidr            TEXT        NOT NULL,
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, vpc_id)
);
CREATE INDEX IF NOT EXISTS idx_network_vpcs_tenant_state
    ON network_vpcs (tenant_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_subnets (
    tenant_id       UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subnet_id       TEXT        NOT NULL,
    vpc_id          TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    cidr            TEXT        NOT NULL,
    gateway         TEXT,
    state           TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, subnet_id),
    FOREIGN KEY (tenant_id, vpc_id) REFERENCES network_vpcs(tenant_id, vpc_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_network_subnets_tenant_vpc
    ON network_subnets (tenant_id, vpc_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_security_groups (
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    security_group_id   TEXT        NOT NULL,
    name                TEXT        NOT NULL,
    description         TEXT,
    rules               JSONB       NOT NULL DEFAULT '[]',
    state               TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, security_group_id)
);
CREATE INDEX IF NOT EXISTS idx_network_security_groups_tenant_state
    ON network_security_groups (tenant_id, state, updated_at DESC);

CREATE TABLE IF NOT EXISTS network_load_balancers (
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    load_balancer_id    TEXT        NOT NULL,
    name                TEXT        NOT NULL,
    vpc_id              TEXT        NOT NULL,
    subnet_id           TEXT,
    scheme              TEXT        NOT NULL DEFAULT 'internal'
        CHECK (scheme IN ('internal','public')),
    vip                 TEXT,
    listeners           JSONB       NOT NULL DEFAULT '[]',
    state               TEXT        NOT NULL
        CHECK (state IN ('pending','available','failed','deleting','deleted')),
    reason              TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, load_balancer_id),
    FOREIGN KEY (tenant_id, vpc_id) REFERENCES network_vpcs(tenant_id, vpc_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_network_load_balancers_tenant_vpc
    ON network_load_balancers (tenant_id, vpc_id, state, updated_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON
    network_vpcs,
    network_subnets,
    network_security_groups,
    network_load_balancers
TO ani_app;

ALTER TABLE network_vpcs ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_vpcs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON network_vpcs;
CREATE POLICY tenant_isolation ON network_vpcs
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE network_subnets ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_subnets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON network_subnets;
CREATE POLICY tenant_isolation ON network_subnets
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE network_security_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_security_groups FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON network_security_groups;
CREATE POLICY tenant_isolation ON network_security_groups
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

ALTER TABLE network_load_balancers ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_load_balancers FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON network_load_balancers;
CREATE POLICY tenant_isolation ON network_load_balancers
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

