# QUOTA-POLICY-ISSUE-14-18 — BOSS 配额套餐前端（列表/详情/限额/绑定/审计）

> 本地 issue：`issue-014` … `issue-018`（`repo/services/tasks/modules/issue/boss/tenant/quota-policy/`）
> 范围：仅 `repo/frontends/boss/src/`（+ `npm run gen-api` 刷新 `schema.d.ts`）  
> 分支模式：当前 pr 分支，保留本地 dirty（不 stash / 不自动 commit）

## 交付摘要

| Issue | 内容 |
|---|---|
| #14 | 路由 `/tenants/plans`、侧栏「租户管理 → 配额策略」、`api/tenant-plans.ts`、列表搜索/状态筛选/游标分页、创建 Dialog、行操作 |
| #15 | `PlanDetailDrawer` 基本信息 + 发布/禁用/删除 + Tabs 框架 |
| #16 | 「配额限额」Tab：行内编辑 + 批量 PUT + 同步提示 |
| #17 | 「绑定租户」Tab + `BindTenantDialog` |
| #18 | 「操作历史」Tab：游标分页表格 |

关键文件：

- `src/api/tenant-plans.ts`
- `src/auth/permissions.ts`
- `src/routes/_authenticated.tsx`（菜单）
- `src/routes/_authenticated/tenants/plans.tsx`
- `src/components/tenant-plans/*`
- `src/api/schema.d.ts`（gen-api）
- `src/routeTree.gen.ts`（vite 生成）

## Design Decisions

1. **写权限**：新增 `canWritePlatform()`，从 JWT `roles` / `realm_access.roles` 识别 `platform-admin`/`platform-ops` vs `platform-readonly`；无角色声明时默认可写，由后端 403 兜底。
2. **绑定租户选项**：平台尚无 `GET /tenants` 列表 API；Dialog 从 `GET /tenant-admins` 推导唯一租户，并提供 UUID Input 兜底。
3. **限额成功文案**：后端 `IdempotentResult` 无 `synced_tenant_count`，成功提示用详情里的 `tenant_count` 代入「已同步 N 个存量租户」。

## Deviations

| Spec / Issue | 实现 | 原因 |
|---|---|---|
| #15 服务端 action/result 筛选 + `user_id` 列 | 本地 Select 过滤；列用 `action/result/details/created_at` | 当前 OpenAPI audit-logs 仅 `limit/cursor`，响应无 `user_id` |
| UX 绑定 Dialog 纯 Select 租户列表 | Select + UUID 兜底 | 无租户列表 API |
| API 封装「11 个端点」 | 另含 `listQuotaMeta`、`updateTenantPlan`、`bindTenantPlan` | 对齐已落地契约（010/011/bind） |

## Tradeoffs

- 游标分页用 `cursorStack` + 估算 `total`，与收件人页不同但符合套餐 API 契约。
- 未引入新状态库；沿用 TanStack Query invalidate 模式。

## Open Questions

1. 租户列表 API 就绪后，应去掉 UUID 兜底并改为正式 Select 数据源。
2. 若后端恢复 audit 筛选或返回 `user_id`/`synced_tenant_count`，前端需对齐契约再改。
3. C: 盘空间耗尽时 `npm run lint` 可能失败；本机用 `.\node_modules\.bin\tsc.cmd --noEmit` 验证通过。

## Verification

```bash
cd repo/frontends/boss
npm run gen-api
.\node_modules\.bin\tsc.cmd --noEmit   # PASS
npx vite build                         # PASS（曾用于生成 routeTree）
```

`eslint`：本机缺少 `@typescript-eslint/eslint-plugin`（既有工程问题），未作为本批次阻断。
