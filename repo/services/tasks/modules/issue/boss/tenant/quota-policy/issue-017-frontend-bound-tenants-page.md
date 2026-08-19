# 前端：套餐绑定租户 Tab（详情页「绑定租户」Tab）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现详情页「绑定租户」Tab：查询绑定到该套餐的租户列表 + 绑定操作（内联 Select 选租户 + POST /tenants/{tenantId}/plan 下发配额）。覆盖 US-009、US-010 前端部分。

> **实现补充说明：** 绑定操作从独立 `BindTenantDialog.tsx` 弹窗组件改为内联在 `BoundTenantsTab.tsx` 中的 `Select` + 按钮，简化交互层级。绑定租户列表使用 tenant-admins 推导租户名 + UUID 兜底展示。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/` only
- Frozen exclusions: 不修改后端代码

## Acceptance Criteria
- [x] 在 PlanDetailPage 的「绑定租户」TabPanel 内实现 `BoundTenantsTab.tsx`（对齐 UX §4.3 Tab2 + §5.3 绑定组件）
- [x] 加载 `GET /tenant-plans/{planId}/tenants` 展示 name/display_name/status(Tag)（active=绿/frozen=橙/disabled=红），不分页
- [x] 空态 `Empty`「未绑定租户」
- [x] 绑定操作内联实现：`Select`（filterable，加载 `GET /tenant-plans/{planId}/bindable-tenants` 可绑定租户列表，排除已绑定）+ 「分配」按钮（platform-admin/ops 可见，readonly 隐藏）
- [x] 绑定提交 `POST /tenants/{tenantId}/plan`（入参 idempotency_key + plan_id）
- [x] 绑定成功 `MessagePlugin.success`「套餐已绑定，配额已更新」+ 刷新绑定租户列表
- [x] 绑定失败 404 →「套餐不存在或未发布」；409 TENANT_STATE_INVALID →「租户已停用，不可绑定套餐」
- [x] `pnpm type-check` / `pnpm lint` 通过

## Dependencies
#15（详情页基础 + Tabs 框架）、#9（绑定后端 API 可用）、#12（可绑定租户 API）

## Type
frontend

## Priority
high

## References
- UX: §4.3 绑定租户 Tab / §5.3 绑定组件 / §6.5 绑定状态 / §7 文案
- SPEC: §4.2 绑定 schema / 绑定租户响应
