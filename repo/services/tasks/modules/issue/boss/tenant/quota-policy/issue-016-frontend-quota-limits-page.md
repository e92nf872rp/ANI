# 前端：套餐限额详情页（详情页「限额明细」Tab）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现详情页「限额明细」Tab：查询套餐限额展示视图 + 行内直接编辑 total 值 + 底部「保存并同步绑定租户」按钮批量提交（同步存量租户提示常驻）。覆盖 US-004、US-008 前端部分。

> **实现补充说明：** 组件归属 PlanDetailPage（独立整页）。提交按钮「保存并同步绑定租户」。成功文案「限额已修改，已同步 {tenant_count} 个存量租户」——前端用详情 `plan.tenant_count`（PUT 响应无 synced_tenant_count；真实同步数仅在审计 details）。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/` only
- Frozen exclusions: 不修改后端代码

## Acceptance Criteria
- [x] 在 PlanDetailPage 的「限额明细」TabPanel 内实现 `QuotaLimitsTab.tsx`（对齐 UX §4.3 Tab1 + §5.3 限额组件）
- [x] 加载 `GET /tenant-plans/{planId}/quota-limits` 展示 resource_type/display_name/unit/total
- [x] total 列直接为 `InputNumber`（min=0, step=1），预填后端兜底后的具体 total（NULL 已由 default_quota 赋值），无操作列（对齐 UX §5.3）
- [x] 常驻 `Alert theme="info"`：修改后自动同步已绑定该套餐的存量租户。已审批通过的配额变更申请维度将保留不覆盖。（对齐 UX §7.2）
- [x] 底部「保存并同步绑定租户」按钮（platform-admin/ops 可见，readonly 隐藏），点击 PUT 提交全部维度 `PUT /tenant-plans/{planId}/quota-limits`（body 带 `idempotency_key`）
- [x] 修改成功 `MessagePlugin.success`「限额已修改，已同步 {tenant_count} 个存量租户」+ 刷新限额列表
- [x] 修改失败 422 →「配额维度未注册或已禁用」；400 VALIDATION_FAILED →「校验失败：{message}」；保持已填值（对齐 UX §6.4）
- [x] `pnpm type-check` / `pnpm lint` 通过

## Dependencies
#15（详情页基础 + Tabs 框架）、#8（限额后端 API 可用）

## Type
frontend

## Priority
high

## References
- UX: §4.3 限额 Tab / §5.3 限额组件 / §6.4 限额状态 / §7 文案
- SPEC: §4.2 quota-limits GET/PUT schema
