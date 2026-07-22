# Issue #9: Console GPU 容器实例 + 创建 Dialog

> **Priority:** high
> **Depends On:** #1, #3, #7
> **Product line:** Console
> **Document Links:**
> - UX: `repo/services/tasks/modules/prd/console/gpu-inventory/ux-console-gpu-scheduling.md` §4.3-4.6, §5.2
> - SPEC: `repo/services/tasks/modules/spec/console/gpu-inventory/spec-console-gpu-scheduling.md` §5.1
> - Module: `repo/services/docs/console-modules/compute/gpu-container-instance-management.md`

## Scope

- `repo/frontends/console/src/routes/compute/gpu-containers/index.tsx`（NEW）
- `repo/frontends/console/src/routes/compute/gpu-containers/$instanceId.tsx`（NEW）
- `repo/frontends/console/src/routes/compute/gpu-containers.create-dialog.tsx`（NEW）

## Description

GPU 容器实例列表 + 创建 Dialog + 详情页。消费 `GET/POST /instances`（`kind=gpu_container`）+ `GET /gpu-scheduling/queues`（队列 Select options）。

## Acceptance Criteria

### 列表页
- [x] `coreApi.GET /instances?kind=gpu_container`，列：name / state / gpu.vendor / gpu.model / gpu.count
- [x] loading / empty / error 三态

### 创建 Dialog
- [x] 表单：名称 + GPU 数量 + 分配模式(整卡/vGPU) + 工作负载类型 + 调度队列(Select from `GET /gpu-scheduling/queues`) + 型号偏好(可选)
- [x] 打开时 fetch queues → Select options
- [x] 提交生成 `idempotency_key`（`crypto.randomUUID()`）
- [x] 成功：`Message.success` + 跳转详情
- [x] `422 InsufficientGPU`：`Message.error` + 保留表单
- [x] `422 QueueNotFound`：高亮调度队列字段
- [x] submitting / validation-error / schedule-failed 三态

### 详情页
- [x] `coreApi.GET /instances/{id}`
- [x] `Descriptions` 展示实例信息 + GPU 摘要
- [x] `state_reason` Alert（失败时高亮 `theme="error"`）
- [x] provisioning 时提示「调度中，预计 1-2 分钟」
- [x] `404` 时 `Empty`「实例不存在」

## Validation

```bash
cd repo/frontends/console
pnpm type-check
pnpm lint
pnpm test
pnpm build
```
