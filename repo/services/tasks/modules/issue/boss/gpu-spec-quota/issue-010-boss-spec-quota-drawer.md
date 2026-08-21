# BOSS 规格管理 Drawer + 配额/预留分配 Drawer

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/gpu-spec-quota/prd-gpu-spec-quota.md`
- UX: `repo/services/tasks/modules/ux/core/gpu-spec-quota/ux-gpu-spec-quota.md`
- SPEC: `repo/services/tasks/modules/spec/core/gpu-spec-quota/spec-gpu-spec-quota.md`

## Description

作为平台运维管理员，在 BOSS 通过 Drawer 新建/删除 GPU 规格目录，并为租户分配配额上限和预留额度，下调时提示 clamp 效果。

## Scope

- Product line: boss
- Code paths allowed: `repo/frontends/boss/src/routes/_authenticated/ops/gpu-pool-quota-drawer.tsx`（NEW）、`gpu-spec-drawer.tsx`（NEW）、`gpu-pool.tsx`（集成接入）

## Acceptance Criteria

- [ ] [UI] 规格目录 Tab 内容实现（嵌入 gpu-pool.tsx Tab 4）：spec_id / gpu_type / gpu_mode(Tag) / shares / mb_per_share / 操作列 — UX §4.1 Tab 4
- [ ] [UI] 规格管理 Drawer（`gpu-spec-drawer.tsx`，NEW）：表单字段 spec_id/gpu_type/gpu_mode/shares/mb_per_share，创建时校验 gpu_type 对齐节点标签 — UX §3.1 Flow 3a
- [ ] [UI] 删除规格：Popconfirm 确认，有实例引用则禁用 + Tooltip 提示 — UX §3.1 Flow 3b, §7.2
- [ ] [UI] 规格模式标签：wholecard=default, vGPU=primary — UX §7.3
- [ ] 配额分配 Drawer（`gpu-pool-quota-drawer.tsx`，NEW）：选租户 Select + 配额上限 InputNumber + 预留额度 InputNumber → 调 Core API `PUT /admin/tenants/{tenant_id}/quota` + `PUT /admin/tenants/{tenant_id}/reservations`
- [ ] [UI] 配额/预留合为一个 Drawer 两个 InputNumber 字段 — UX §4.2, §8.3 假设
- [ ] [UI] 下调 clamp 时显示 `Message.success` "配额已下调至 N 卡（实际使用量），差额无法回收" — UX §7.2
- [ ] [UI] 预留 > 配额上限时 inline 校验提示"预留额度不能超过配额上限" — UX §7.2
- [ ] `cd repo/frontends/boss && npx tsc --noEmit` 通过
- [ ] `cd repo/frontends/boss && npx vite build` 通过
- [ ] Verify in browser using an available browser automation tool or record manual verification steps

## Dependencies

#9

## Type

boss

## Priority

high

## Labels

boss

## Batch

M2.1-TASK-A

## References

- SPEC: §4.3 (PUT /admin/tenants/{tenant_id}/quota, PUT /admin/tenants/{tenant_id}/reservations, POST /gpu-specs, DELETE /gpu-specs/{spec_id})
- UX: §4.1 Tab 4 (规格目录), §4.2 (配额分配 Drawer), §7.2 (Toast copy), §7.3 (Status Tag copy)
