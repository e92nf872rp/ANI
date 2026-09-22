-- ANI Platform · Migration
-- Description: 安全组规则明细持久化（修复网关重启后详情页规则加载 404，规则此前仅存于网关内存）
-- Depends on: 20260916120000_network_security_groups_vpc_id.sql

CREATE TABLE IF NOT EXISTS network_security_group_rules (
    tenant_id           UUID        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id             TEXT        NOT NULL,
    security_group_id   TEXT        NOT NULL,
    priority            INT         NOT NULL,
    direction           TEXT        NOT NULL,
    protocol            TEXT        NOT NULL,
    port_range          TEXT        NOT NULL DEFAULT 'all',
    cidr                TEXT        NOT NULL DEFAULT '',
    action              TEXT        NOT NULL DEFAULT 'allow',
    description         TEXT        NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, rule_id)
);
CREATE INDEX IF NOT EXISTS idx_network_security_group_rules_sg
    ON network_security_group_rules (tenant_id, security_group_id, priority);

GRANT SELECT, INSERT, UPDATE, DELETE ON network_security_group_rules TO ani_app;

-- 对齐 network_security_groups 的三段 policy 模式：行可见 = 至少一条 PERMISSIVE 放行 AND 所有 RESTRICTIVE 通过。
-- 只有 RESTRICTIVE 时没有任何 PERMISSIVE 授权，对普通角色全部 deny（已在线上复现 0 行）。
ALTER TABLE network_security_group_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE network_security_group_rules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS network_security_group_rules_platform_bypass ON network_security_group_rules;
CREATE POLICY network_security_group_rules_platform_bypass ON network_security_group_rules
    AS PERMISSIVE
    FOR ALL
    USING (current_setting('app.current_tenant_id', true) IS NULL);
DROP POLICY IF EXISTS network_security_group_rules_self ON network_security_group_rules;
CREATE POLICY network_security_group_rules_self ON network_security_group_rules
    AS PERMISSIVE
    FOR ALL
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);
DROP POLICY IF EXISTS tenant_isolation ON network_security_group_rules;
CREATE POLICY tenant_isolation ON network_security_group_rules
    AS RESTRICTIVE
    USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid);

-- 存量回填：从 network_security_groups.rules JSONB 摘要恢复规则明细。
-- 摘要不含 rule_id，统一重新生成；以安全组为粒度防重，保证迁移幂等。
INSERT INTO network_security_group_rules
    (tenant_id, rule_id, security_group_id, priority, direction, protocol, port_range, cidr, action, description)
SELECT g.tenant_id,
       'sgr_' || replace(gen_random_uuid()::text, '-', ''),
       g.security_group_id,
       COALESCE((r ->> 'Priority')::int, 0),
       COALESCE(r ->> 'Direction', 'ingress'),
       COALESCE(r ->> 'Protocol', 'all'),
       COALESCE(r ->> 'PortRange', 'all'),
       COALESCE(r ->> 'CIDR', ''),
       COALESCE(r ->> 'Action', 'allow'),
       ''
FROM network_security_groups g,
     jsonb_array_elements(g.rules) AS r
WHERE g.state <> 'deleted'
  AND jsonb_typeof(g.rules) = 'array'
  AND NOT EXISTS (
      SELECT 1 FROM network_security_group_rules x
      WHERE x.tenant_id = g.tenant_id AND x.security_group_id = g.security_group_id
  );
