# Issue 3: 数据库迁移文件 — 配额套餐表 + tenants.plan_id + audit_logs 分区

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description

创建数据库迁移文件 `20260810000200_tenant_plan_management.sql`：配额套餐表（tenant_plans + plan_quota_limits）+ tenants.plan_id 列 + audit_logs 月分区表。

> **实现补充说明：** 原 issue 规格含两个迁移文件（文件 1 配额基础表 + 文件 2 配额套餐表）。实际实现中文件 1（resource_quota_meta / resource_quota / resource_reservations）由 Core Quota Service 批次完成，不在 quota-policy 批次范围内。本 issue 仅实现文件 2。`tenants.plan_id` 因存量数据不为空，从直接 NOT NULL 改为分 4 步实施（加列可空→插入 starter 套餐→回填存量→收紧 NOT NULL）。`plan_quota_limits` 显式声明 `FK → resource_quota_meta(resource_type)` 和 `FK → tenant_plans(id) ON DELETE CASCADE`。额外实现了 audit_logs 月分区表 + PK 修复（原规格未提及）。

## Scope
- Product line: boss
- Code paths allowed: `repo/deploy/migrations/` 或项目统一 migrations 目录

## Acceptance Criteria

### 文件 1: `20260810000100_resource_quota.sql`
- [x] ~~CREATE TABLE resource_quota_meta / resource_quota / resource_reservations~~ — **不在本批次**，由 Core Quota Service 批次完成

### 文件 2: `20260810000200_tenant_plan_management.sql`
- [x] CREATE TABLE tenant_plans（id PK / code / name / description / status CHECK IN ('draft','active','disabled') / is_deleted / deleted_at / created_at / updated_at）
- [x] CREATE UNIQUE INDEX idx_tenant_plans_code_active ON tenant_plans(code) WHERE is_deleted = FALSE（partial unique index，软删除后 code 可复用）
- [x] CREATE TABLE plan_quota_limits（plan_id + resource_type PK / total BIGINT nullable / CHECK (total IS NULL OR total >= 0) / FK → tenant_plans ON DELETE CASCADE / FK → resource_quota_meta(resource_type)）— 主键 (plan_id, resource_type) 已覆盖 plan_id 查询，无需额外索引
- [x] FK → resource_quota_meta(resource_type) 显式声明（REFERENCES resource_quota_meta(resource_type)）
- [x] ALTER TABLE tenants ADD COLUMN plan_id UUID — **分 4 步实施**：
  - 3a: ADD COLUMN IF NOT EXISTS plan_id UUID（可空）
  - 3b: INSERT starter 套餐（code='starter', status='active'）+ 8 维度默认限额
  - 3c: UPDATE tenants SET plan_id = starter.id WHERE plan_id IS NULL（回填存量）
  - 3d: ALTER TABLE tenants ALTER COLUMN plan_id SET NOT NULL（收紧约束）
- [x] CREATE TABLE audit_logs 月分区表（PK 修复 + 月分区创建）— **原规格未提及，实现时追加**
- [x] 迁移文件含 rollback 注释

## Dependencies
文件 1（`20260810000100_resource_quota.sql`）必须先执行（plan_quota_limits FK REFERENCES resource_quota_meta(resource_type) 依赖该表存在）

## Type
backend

## Priority
high

## References
- SPEC: §3.1 Schema Changes（§3.1.1 ~ §3.1.6）, §3.4 Migration Plan
