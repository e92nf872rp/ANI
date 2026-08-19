# 前端：操作历史页（详情页「操作历史」Tab）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现详情页「操作历史」Tab：查询套餐操作历史（游标分页表格 + 本地 result 过滤）。覆盖 US-011 前端部分。

> **实现补充说明：** 原 issue 规格含 action + result 两个筛选 Select，实际实现仅有 result 筛选 Select（本地过滤），未实现 action 筛选（设计决策：审计日志量小，action 维度过滤需求低）。后端不支持服务端 action/result 过滤，前端仅做本地 result 过滤。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/` only
- Frozen exclusions: 不修改后端代码

## Acceptance Criteria
- [x] 在 PlanDetailPage 的「操作历史」TabPanel 内实现 `AuditLogsTab.tsx`（对齐 UX §4.3 Tab3 + §5.3 历史组件）
- [x] 加载 `GET /tenant-plans/{planId}/audit-logs`（limit/cursor）
- [x] 一个筛选 `Select`：result（success/failure，clearable），本地过滤（placeholder 为「全部结果」，未选中时不过滤；action 筛选未实现）
- [x] `Table` 展示 action(操作)/result(Tag: 成功/失败)/details(可读化摘要)/created_at(时间) — 无 user_id 列（API 不返回 user_id）
- [x] `Pagination` 上一页/下一页由 `next_cursor` 驱动
- [x] 空态 `Empty`「暂无操作历史」；错误态 `Alert theme="error"` + 重试
- [x] `pnpm type-check` / `pnpm lint` 通过

## Dependencies
#15（详情页基础 + Tabs 框架）、#13（操作历史后端 API 可用）

## Type
frontend

## Priority
high

## References
- UX: §4.3 操作历史 Tab / §5.3 历史组件 / §6.6 历史状态 / §7 文案
- SPEC: §4.2 audit-logs schema
