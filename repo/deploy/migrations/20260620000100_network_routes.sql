-- Sprint 13 production-shaped closure: persist network route metadata for provider-backed Kube-OVN routes.

CREATE TABLE IF NOT EXISTS network_routes (
    tenant_id UUID NOT NULL,
    route_id TEXT NOT NULL,
    vpc_id TEXT NOT NULL,
    destination_cidr TEXT NOT NULL,
    next_hop_type TEXT NOT NULL,
    next_hop_id TEXT NOT NULL,
    description TEXT,
    state TEXT NOT NULL,
    provider TEXT,
    real_provider BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, route_id),
    FOREIGN KEY (tenant_id, vpc_id) REFERENCES network_vpcs(tenant_id, vpc_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_network_routes_tenant_vpc
    ON network_routes (tenant_id, vpc_id, state, updated_at DESC);

ALTER TABLE network_routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_routes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON network_routes;
CREATE POLICY tenant_isolation ON network_routes
    USING (tenant_id = current_setting('ani.tenant_id', true)::uuid)
    WITH CHECK (tenant_id = current_setting('ani.tenant_id', true)::uuid);
