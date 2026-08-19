# 前端：套餐列表页（含创建 Wizard）

## Document Links
- PRD: [prd-new-boss-tenant-quota-policy.md](../../prd/boss/tenant/prd-new-boss-tenant-quota-policy.md)
- UX: [ux-boss-tenant-quota-policy.md](../../ux/boss/tenant/ux-boss-tenant-quota-policy.md)
- SPEC: [spec-new-boss-tenant-quota-policy.md](../../spec/boss/tenant/spec-new-boss-tenant-quota-policy.md)

## Description
实现 BOSS 前端套餐列表页（搜索/状态筛选/游标分页/行操作）与创建套餐 Wizard（3 步向导：名称编码 → 限额配置 → 确认发布）。覆盖 US-001、US-002、US-005、US-006、US-007 的前端入口部分（列表行操作 + 创建）。

> **实现补充说明（对齐代码 2026-08-14）：** 路由为 `/tenants/quotas`（+ `/new` 独立创建页、`/$planId` 详情页），菜单项 `tenant-quotas`。创建为独立页 3 步 Wizard（非 Dialog、非列表内嵌）。API 封装共 14 个函数。列表搜索为**点击搜索/Enter**（非 debounce 300ms）；状态为 **Radio.Group**（非 Select）。空态「还没有配额套餐」；副标题「定义套餐并绑定到租户，限额变更自动同步」。

## Scope
- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/` only
- Frozen exclusions: 不修改后端代码

## Acceptance Criteria
- [x] 创建路由 `quotas/index.tsx`；创建页 `quotas.new.tsx`；详情页 `quotas.$planId.tsx`
- [x] 在 `_authenticated.tsx` 的 `Menu.SubMenu value="tenant"` 下新增 `Menu.MenuItem value="tenant-quotas"` 指向 `/tenants/quotas`
- [x] 创建 API 封装 `repo/frontends/boss/src/api/tenant-plans.ts`：封装全部 14 个端点共 14 个函数（openapi-fetch typed paths；写操作 body 带 `idempotency_key: crypto.randomUUID()`，DELETE 不传）
- [x] 列表页 `Table` 展示 name(套餐)/code(编码)/status(Tag)/tenant_count(绑定租户)/updated_at(更新时间)/操作列
- [x] 搜索框 `Input`（placeholder「按名称搜索」）+「搜索」/「重置」按钮（点击或 Enter 提交）+ 状态 `Radio.Group`（全部/启用/停用/草稿）
- [x] 状态 Tag 映射：draft=灰（"草稿"）/ active=绿（"启用"）/ disabled=红（"停用"）
- [x] 行操作：详情（常驻）/ 发布（draft/disabled）/ 停用（active）/ 删除（Popconfirm danger）；platform-readonly 仅详情
- [x] 分页：`Pagination` 上一页/下一页由 `next_cursor` 驱动，limit 默认 20（可选 10/20/50/100）
- [x] 空态 `Empty`「还没有配额套餐」+ 「新建套餐」按钮；错误态 `Alert theme="error"` + 重试
- [x] 创建 Wizard：`CreatePlanWizard.tsx`（独立页 3 步），step1 名称编码，step2 限额配置（quota-meta，留空传 null），step3 标题「确认发布」/按钮「确认创建」（提交后为草稿）
- [x] 创建成功 `MessagePlugin.success`「套餐已创建」+ 返回列表 invalidateQueries；409 PLAN_CODE_CONFLICT →「套餐代码已存在，请更换」；422 QUOTA_RESOURCE_NOT_REGISTERED →「配额维度未注册或已禁用」
- [x] 发布/禁用/删除成功 Message + 刷新；删除 409 TENANT_PLAN_IN_USE →「该套餐已关联租户，不可删除」
- [x] 拆分组件：`PlanTable.tsx` + `CreatePlanWizard.tsx`，路由页管 query/mutation
- [x] 创建 `planStatus.tsx` + `quotaResourceOrder.ts`
- [x] `pnpm type-check` / `pnpm lint` 通过

## Dependencies
#1（OpenAPI 契约生成前端类型）、#6（套餐列表/详情后端可用）

## Type
frontend

## Priority
high

## References
- UX: §4.1 套餐列表页 / §4.2 创建 Wizard / §5.1 列表组件 / §5.2 创建组件 / §6.1 列表状态 / §6.2 创建状态 / §7 文案
