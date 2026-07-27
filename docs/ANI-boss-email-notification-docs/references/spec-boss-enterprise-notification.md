# SPEC: BOSS enterprise-notification

> Technical specification derived from: `tasks/modules/prd/boss/integration/prd-boss-enterprise-notification.md`  
> Revised: 2026-06-17 · 流程：ANI-14 Phase 1

## 1. Summary

### 1.1 What This SPEC Covers

收口 BOSS **平台集成与通知 / 企业通知集成（Phase 2）** 的权威源事实、平台级技术边界、Console 对照关系与 TODO-YAML 依赖。

### 1.2 PRD Reference

- Source: `tasks/modules/prd/boss/integration/prd-boss-enterprise-notification.md`
- 主维护源: `docs/boss-modules/integration/enterprise-notification.md`

## 2. Frozen Facts

### 2.1 Authority Sources

- `ANI-main/repo/api/openapi/v1.yaml`
- `ANI-main/repo/api/openapi/services/v1.yaml`

### 2.2 Verified Paths（BOSS 上下文）

**当前无本模块可直接引用的 BOSS 平台级冻结路径。**

<!-- TODO-YAML: 平台级 BOSS API -->

### 2.3 Non-Frozen Capabilities

- 平台级 `企业通知集成` 跨租户 list/aggregate API — **TODO-YAML**
- BOSS 专属 RBAC scope — 待 Core/Services 确认
- 见主维护源

### 2.4 Core 同步边界（2026-06-17）

- 租户 `createIntegrationBot` — **只读参考**
- 无 bot DELETE/PATCH in YAML
- 平台跨租户 Bot list — **TODO-YAML P2**

> 详文满配（Core）见主维护源；Services 租户 path **只读参考**；OpenAPI 已声明 ≠ handler 已实现。

## 3. Page Scope

### 3.1 Page Responsibilities

- 平台侧页面

### 3.2 Non-Goals

- 不定义 Console 租户自助契约
- 不把 YAML 未声明路径写成已实现
- 交付/白牌页不自造 REST schema

## 4. Architecture Constraints（ANI-14）

| 约束 | 要求 |
|---|---|
| Core 前缀 | `/api/v1/*` |
| Services 前缀 | `/api/v1/svc/*` |
| 租户边界 | JWT claims；不信任 body/query `tenant_id` |
| 写操作 | `idempotency_key` + 明确成功码 |
| 错误结构 | `code` / `message` / `request_id` |
| 422 | 仅 YAML 已声明 operation 可写冻结 |

## 5. 创建前置条件与操作矩阵

见主维护源 `docs/boss-modules/integration/enterprise-notification.md`（§创建前置条件、§操作可用性矩阵）。

## 6. 待补边界

- 平台级 BOSS API — **TODO-YAML**（交付页：**N/A**）
- Phase：**P2**

## 7. 主维护源与流程

- `docs/boss-modules/integration/enterprise-notification.md`
- `docs/boss-modules/governance/module-delivery-workflow.md`
- `ANI-main/ANI-14-API对齐与开发工作流.md`

OpenAPI 已声明 ≠ handler 已实现；BOSS 平台聚合 ≠ 租户级路径直接复用。
