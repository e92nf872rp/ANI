# 前端：套餐详情页（独立页面 + 概览 + 4 Tab）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现套餐详情独立页面 `PlanDetailPage.tsx`：点击列表「详情」跳转独立页面，展示套餐基本信息（code/name/description/状态/绑定租户数/创建更新时间）+ 操作按钮（编辑信息/发布/停用/删除）。包含 `EditPlanInfoDialog.tsx` 弹窗修改 name/description。建立 Tabs 框架（概览/限额明细/绑定租户/操作历史 4 个 Tab），各业务 Tab 内容由后续 issue-016/017/018 填充。覆盖 US-003 前端部分 + US-005/006/007/010 的详情操作入口。

> **实现补充说明：** 组件形态从右侧滑出 Drawer 改为独立整页（`PlanDetailPage.tsx` + 路由跳转），提供更宽裕的信息展示空间。组件名从 `PlanDetailDrawer` 改为 `PlanDetailPage`。新增 `EditPlanInfoDialog.tsx` 弹窗用于修改套餐基本信息（issue-010 追加功能）。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/` only
- Frozen exclusions: 不修改后端代码

## Acceptance Criteria
- [x] 创建组件 `repo/frontends/boss/src/components/tenant-plans/PlanDetailPage.tsx`（独立整页，非 Drawer）
- [x] 页面标题区展示 code/name + 操作按钮区按 status 显示：编辑信息（`EditPlanInfoDialog` 弹窗，修改 name/description）/ 发布（Popconfirm，draft/disabled）/ 停用（Popconfirm，active）/ 删除（Popconfirm danger）
- [x] 打开时加载详情 `GET /tenant-plans/{planId}`；页面内 Skeleton 占位
- [x] 概览区只读展示：code/name/description/status(Tag)/tenant_count/created_at/updated_at（对齐 UX §4.3 基本信息区）
- [x] 创建 `EditPlanInfoDialog.tsx`：`Form layout="vertical"` 修改 name/description，提交 `PUT /tenant-plans/{planId}`（issue-010 追加）
- [x] 发布成功「套餐已发布」+ 刷新详情；禁用成功「套餐已停用」+ 刷新；删除成功「套餐已删除」+ 返回列表页
- [x] 建立 `Tabs` 四个 TabPanel：概览（默认选中，展示基本信息只读区）/ 限额明细（由 #16 填充）/ 绑定租户（由 #17 填充）/ 操作历史（由 #18 填充）
- [x] 权限：platform-admin/ops 可见写操作按钮；readonly 隐藏（对齐 UX §4.3）
- [x] 空态/错误态：页面内 `Alert theme="error"` + 重试按钮
- [x] `pnpm type-check` / `pnpm lint` 通过

## Dependencies
#14（列表页，复用 API 封装 + 路由跳转入口）、#1

## Type
frontend

## Priority
high

## References
- UX: §4.3 详情 Drawer / §5.3 详情组件 / §6.3 状态 / §7 文案
- SPEC: §2.4 frontend files / §4.2 详情 schema
