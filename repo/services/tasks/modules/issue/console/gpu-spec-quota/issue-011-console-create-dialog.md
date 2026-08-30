# Console 创建 Dialog 改版 — 规格下拉四态 + 队列下拉

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: `repo/services/tasks/modules/ux/core/gpu-spec-quota/ux-gpu-spec-quota.md`
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为租户用户，在 Console 选规格创建 GPU 容器实例时，看到规格按可用性四态标注过滤，避免盲选后提交才报 409。创建表单新增调度队列下拉，单副本实例也必须关联队列。

## Scope

- Product line: console
- Code paths allowed: `repo/frontends/console/src/routes/_authenticated/compute/gpu-containers/-create-dialog.tsx`、`repo/frontends/console/src/api/coreClient.ts`

## Acceptance Criteria

- [ ] 创建 Dialog 改选规格模式（`-create-dialog.tsx`，MODIFY）：移除旧字段输入项（allocation_mode/model/gpu_count），新增规格下拉
- [ ] [UI] 规格下拉调用 `GET /gpu-specs/availability`（非赛 `GET /gpu-specs`），按返回 status 四态标注过滤 — UX §4.4
- [ ] [UI] 四态标注：`available`(available_count>0 可选,显示"剩余 N") / `full`(quota_remaining=0 灰标"配额已满") / `device_full`(quota_remaining>0 && !has_idle_devices 灰标"设备已满，暂无空闲") / `unavailable`(!has_matching_nodes 灰标"暂无匹配节点") — UX §7.3
- [ ] [UI] 前端选规格后实时重算：`new_quota_remaining = quota_remaining - sum(已选规格的 gpu_count)`，每规格重算 `available_count` 并刷新下拉，避免跨规格共享配额超卖 — UX §4.4
- [ ] 传 `spec_id` 创建实例（走规格模式 Volcano 调度）
- [ ] [UI] 创建表单新增调度队列下拉：GET /gpu-scheduling/queues，单副本实例也必须关联队列 — UX §4.5
- [ ] 扩展 `api/coreClient.ts`：新增 availability / queues 调用
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

- SPEC: §4.3 (GET /gpu-specs/availability, POST /instances), §4.4 (GPUSpecAvailability schema)
- UX: §4.4 (Console 创建 Dialog 改版), §4.5 (队列下拉), §7.3 (Status Tag copy)
