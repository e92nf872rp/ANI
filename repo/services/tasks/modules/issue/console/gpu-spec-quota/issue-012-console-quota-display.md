# Console 列表页配额/预留展示 + 队列页扩展

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: `repo/services/tasks/modules/ux/core/gpu-spec-quota/ux-gpu-spec-quota.md`
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为租户用户，在 Console GPU 容器列表页看到自己的配额和预留额度（可用余量），在队列配置页看到 Queue status 的 allocated 列 + state 徽标列。

## Scope

- Product line: console
- Code paths allowed: `repo/frontends/console/src/routes/_authenticated/compute/gpu-containers/index.tsx`、`repo/frontends/console/src/routes/_authenticated/settings/gpu-queues.tsx`、`repo/frontends/console/src/api/coreClient.ts`

## Acceptance Criteria

- [ ] 配额展示（`gpu-containers/index.tsx`，MODIFY）：调用 `GET /quotas/me`（Core API）展示本租户配额
- [ ] [UI] 配额卡片展示配额总量(total)/已用(used)/预留(reserved)/可用余量 — UX §4.6
- [ ] 预留展示（`gpu-containers/index.tsx`，MODIFY）：调用 `GET /reservations/me`（Core API）展示本租户预留额度
- [ ] [UI] 预留卡片展示 allocated + available(allocated - used - reserved) — UX §4.6
- [ ] 队列配置页（`gpu-queues.tsx`，MODIFY）：扩展 allocated 列展示 Queue status.allocated + 新增 state 徽标列展示 Queue status.state（open=success / closed=default / unknown=default）
- [ ] [UI] state 徽标：open=绿色 / closed=灰色 / unknown=默认 — UX §4.1 Tab 3 + §5.3
- [ ] 扩展 `api/coreClient.ts`：新增 /quotas/me + /reservations/me 调用
- [ ] [UI] 复用现有 `ConsolePage` + `ConsolePageHeader` + `ConsoleContentCard` 三件套 — UX §1.2
- [ ] `cd repo/frontends/console && npx tsc --noEmit` 通过
- [ ] `cd repo/frontends/console && npx vite build` 通过
- [ ] Verify in browser using an available browser automation tool or record manual verification steps

## Dependencies

#8

## Type

console

## Priority

high

## Labels

console

## Batch

M2.1-TASK-A

## References

- SPEC: §4.3 (GET /quotas/me, GET /reservations/me)
- UX: §4.6 (配额/预留卡片), §7.3 (Status Tag copy)
