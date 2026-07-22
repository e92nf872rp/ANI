# Issue #10: Console 队列设置页

> **Priority:** high
> **Depends On:** #1, #2, #7
> **Product line:** Console
> **Document Links:**
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md` §4.5, §5.3
> - SPEC: `repo/services/tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md` §5.1
> - Module: `repo/services/docs/console-modules/compute/gpu-management.md`

## Scope

- `repo/frontends/console/src/routes/settings/gpu-queues.tsx`（NEW）

## Description

GPU 调度队列设置页：平台默认队列只读 + 我的队列 CRUD。消费 `GET/POST/PATCH/DELETE /gpu-scheduling/queues`。

## Acceptance Criteria

- [x] fetch queues → 分两组：`is_platform_default=true`（只读 Table）/ `false`（可 CRUD Table）
- [x] RBAC：无 `scope:gpu-scheduling:write` → 隐藏「新建」按钮 + 行操作 + Alert「仅租户管理员可管理队列」
- [x] CRUD Dialog：name / weight / reclaimable / workload_class / project_id(可选)
- [x] 队列名 K8s 资源名正则校验 `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
- [x] weight：InputNumber min 1
- [x] 创建：`POST` + `Idempotency-Key` header
- [x] 删除：`Popconfirm` → `Message.success`
- [x] 删除平台默认队列 → `403` → `Message.error`「平台默认队列不可删除」
- [x] loading：Table loading
- [x] empty-custom：无自定义队列时 `Empty` + write 用户 CTA「新建队列」
- [x] read-only-user：无 write scope 时只读 + Alert
- [x] delete-confirm：Popconfirm 确认
- [x] error：`Alert theme="error"` + 重试
- [x] `pnpm type-check && pnpm lint && pnpm test && pnpm build` 通过（type-check + build 通过；lint 同 Console 无 eslint config）

## Validation

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm test
pnpm build
```
