# PRD: BOSS 企业通知集成

## 1. 页面定位

BOSS / 平台集成与通知 / 企业通知集成（Phase 2） 平台侧页面。

本页属于 **BOSS** 平台侧能力，不等同于 Console 单租户自助控制台。执行口径遵循 [`ANI-main/ANI-14-API对齐与开发工作流.md`](../../ANI-main/ANI-14-API对齐与开发工作流.md) Phase 1。

## 2. 权威源结论

- 一级权威源：`ANI-main/repo/api/openapi/v1.yaml`（Core）、`ANI-main/repo/api/openapi/services/v1.yaml`（Services）
- 主维护源：[`docs/boss-modules/integration/enterprise-notification.md`](../../docs/boss-modules/integration/enterprise-notification.md)
- 平台级 API 多数尚未冻结；PRD 只定义产品边界，不自造正式契约。

## 3. Goals

- 明确 BOSS **全平台 / 多租户 / 资源池** 视角与 Console 租户视角的差异
- 收口页面结构、字段口径、操作矩阵与待补 API 边界
- 为 Phase 2 YAML 扩充与 Phase 4 handler 实现提供稳定产品基线

## 4. 用户故事

### US-001: 平台视角查看

作为平台管理员/SRE，我希望在 BOSS 查看 `企业通知集成` 的平台级摘要，以便判断全平台运行与风险。

验收标准：

- 文档明确平台级 RBAC 与跨租户聚合边界
- 不把 Console 单租户页面定义原样复制为 BOSS 正式 API
- 未冻结接口标注 `TODO-YAML`

### US-002: 与 Console 分工清晰

作为模块维护者，我希望 BOSS 与 Console 对照模块职责不混淆。

验收标准：

- 文档含 BOSS vs Console 分工表
- Console 对照链接正确（若有）
- 不自造 schema / 路径 / 返回码

### US-003: Core 合规

作为 API 对齐工程师，我希望 BOSS 文档遵守 ANI-14 架构约束。

验收标准：

- Core `/api/v1/*` 与 Services `/api/v1/svc/*` 分层正确
- 写操作口径含 `idempotency_key` 与统一错误结构
- `422` 仅按 §2.10 写法

## 5. 功能需求

- FR-1：必须定义 `企业通知集成` 的 BOSS 页面定位与非目标
- FR-2：必须含 `## 创建前置条件` 与 `## 操作可用性矩阵`
- FR-3：必须把平台级待补 API 标注为 `TODO-YAML`（交付类页标注 N/A）
- FR-4：必须与 PRD/SPEC/主维护源/HTML 摘要保持一致
- FR-5：优先级：**P2**

## 6. 非目标

- 不在本轮伪造 BOSS 平台级 REST 契约（除非 YAML 已声明且明确为只读参考）
- 不把交付安装流程写成已冻结 OpenAPI
- 不替代 Console 租户自助能力文档

## 7. 成功标准

- 主维护源、PRD、SPEC 三方口径一致
- 通过 `docs/boss-modules/governance/module-delivery-workflow.md` 复审清单
- 可支撑 Phase 2 TODO-YAML 提取

## 8. 开放问题

- 平台级 RBAC scope 命名与 Gateway  middleware 口径
- 跨租户聚合 API 归属 Core 还是 Services
- BOSS 平台 list 是否允许 optional `tenant_id` 筛选（须后端鉴权）

## 9. 主维护源

- [`docs/boss-modules/integration/enterprise-notification.md`](../../docs/boss-modules/integration/enterprise-notification.md)
- SPEC：[`tasks/modules/spec/boss/integration/spec-boss-enterprise-notification.md`](../../tasks/modules/spec/boss/integration/spec-boss-enterprise-notification.md)

## 10. 文档同步状态

- 同步日期：**2026-06-17**
- 详文状态：**满配（Core）** — [`docs/boss-modules/integration/enterprise-notification.md`](../../docs/boss-modules/integration/enterprise-notification.md)
- SPEC：[`spec-boss-enterprise-notification.md`](../spec/spec-boss-enterprise-notification.md)
- HTML 摘要：[`ani-services-prototype-boss.html`](../../prototypes/ani-services-prototype-boss.html) · `boss.integration.bot`
- 权威顺序：详文 > SPEC > PRD > HTML 摘要
