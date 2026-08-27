# Issue 001: 新建 metering_usage_records 表 migration

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/metering/prd-metering-consumer.md`
- UX: N/A
- SPEC: `repo/services/tasks/modules/spec/core/metering/spec-metering-consumer.md`

## Description

新建 `metering_usage_records` 表和 `ani_metering_writer` 角色，作为计量采集的 DB 层基础设施。表含 UNIQUE 约束作为写入幂等兜底，角色 BYPASSRLS 供采集写侧跨租户写入。

## Scope
- Product line: core
- Code paths allowed: `repo/deploy/migrations/`

## Acceptance Criteria
- [ ] 新增 `repo/deploy/migrations/20260731000100_metering_usage.sql`
- [ ] `CREATE ROLE ani_metering_writer BYPASSRLS NOLOGIN`（类比 `ani_outbox_publisher` 范式）
- [ ] `CREATE TABLE metering_usage_records`：`id BIGSERIAL PK`、`tenant_id UUID FK tenants(id) ON DELETE CASCADE`、`resource_ref TEXT`、`resource_type TEXT`、`period TEXT`、`quantity DOUBLE PRECISION`、`unit TEXT`、`recorded_at TIMESTAMPTZ DEFAULT NOW()`
- [ ] `UNIQUE (tenant_id, resource_ref, resource_type, period)` 约束作为写入幂等 DB 层兜底
- [ ] `CREATE INDEX idx_meter_tenant_type_time ON metering_usage_records(tenant_id, resource_type, recorded_at)`
- [ ] `GRANT SELECT, INSERT, UPDATE, DELETE ON metering_usage_records TO ani_metering_writer`
- [ ] `ENABLE ROW LEVEL SECURITY`（不 FORCE RLS，写侧 BYPASSRLS 绕过）
- [ ] `CREATE POLICY tenant_isolation ON metering_usage_records USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)`（不加 `AS RESTRICTIVE`）
- [ ] migration 用 `BEGIN/COMMIT` 事务包裹
- [ ] 文件头声明 `Depends on: 20260501000100_init_schema.sql`

## Dependencies
None

## Type
core

## Priority
high

## Labels
core

## Batch
PR-M1

## SPEC Reference
- SPEC §3.1 Schema Changes
- SPEC §3.4 Migration Plan
- PRD FR-1, FR-2, FR-3
