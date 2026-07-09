# GPU-Scheduling-Issue-09: Console GPU 容器实例 + 创建 Dialog

> **批次类型：** Feature batch
> **依赖：** #1, #3, #7
> **完成日期：** 2026-07-06
> **SPEC：** `spec-console-gpu-scheduling.md` §5.1
> **UX：** `ux-console-gpu-scheduling.md` §4.3-4.6, §5.2, §5.4, §5.5

---

## 1. 范围

GPU 容器实例列表页 + 创建 Dialog + 详情页，消费 `GET/POST /instances`（kind=gpu_container）+ `GET /gpu-scheduling/queues`。

### 变更文件

| 文件 | 类型 | 说明 |
|---|---|---|
| `repo/frontends/console/src/routes/compute/gpu-containers/index.tsx` | NEW | 列表页（路由 `/compute/gpu-containers`） |
| `repo/frontends/console/src/routes/compute/gpu-containers/$instanceId.tsx` | NEW | 详情页（路由 `/compute/gpu-containers/$instanceId`） |
| `repo/frontends/console/src/routes/compute/gpu-containers/create-dialog.tsx` | NEW | 创建 Dialog 组件 |
| `repo/frontends/console/src/routes/__root.tsx` | MODIFY | 侧栏「算力与云资源」新增 GPU 容器实例入口 |

---

## 2. 实现要点

### 2.1 列表页

```
ConsolePage (shell)
  └── ConsolePageHeader (title + 创建按钮)
  └── ConsoleContentCard
      ├── 筛选：名称搜索 Input + 状态筛选 Select
      └── Table (name→Link / state→Tag / gpu.count / gpu.model / gpu.queue_name / 操作)
  └── CreateGpuContainerDialog
```

数据源：`GET /instances?kind=gpu_container`，客户端过滤 name + state。三态：loading（Table loading）/ empty（Empty）/ error（Alert + 重试）。

### 2.2 创建 Dialog

表单字段（对齐 `CreateInstanceRequest`）：
- name（Input, required）
- gpu_count（InputNumber min=1, default=1）
- allocation_mode（Radio: dedicated/vgpu, default=dedicated）
- workload_class（Radio: inference/training/batch, default=inference）
- queue_name（Select from `GET /gpu-scheduling/queues`, 可空）
- model（Select, 从列表数据派生, 可空）

提交逻辑：
1. `form.validate()` 校验必填
2. `crypto.randomUUID()` 生成 idempotency_key
3. `POST /instances`（kind=gpu_container）
4. 201 → `Message.success` + `navigate` 到详情页
5. 422 InsufficientGPU → `Message.error` + 保留表单
6. 422 QueueNotFound → `Message.error` + 高亮 queue_name 字段

### 2.3 详情页

```
ConsolePage (shell)
  ├── 返回列表 Link
  ├── ConsolePageHeader (name + state Tag)
  ├── 失败 Alert (state_reason, theme=error)
  ├── 调度中 Alert (provisioning, theme=info)
  ├── ConsoleContentCard "基本信息" (Descriptions)
  └── ConsoleContentCard "GPU 与调度" (Descriptions)
```

数据源：`GET /instances/{instance_id}`。

状态处理：
- 404 → Empty「实例不存在」
- loading → Skeleton
- error → Alert + 重试
- provisioning/pending/starting → Alert「调度中，预计 1-2 分钟」
- failed + state_reason → Alert theme=error
- gpu.resource_name 含 `vgpu` → 展示「vGPU 切片」；否则「整卡」

---

## 3. 验收

### 3.1 14 项 AC 全部通过

| AC | 验证 |
|---|---|
| 列表 GET /instances?kind=gpu_container | ✅ |
| 列表三态 loading/empty/error | ✅ |
| Dialog 表单 6 字段 | ✅ name + gpu_count + allocation_mode + workload_class + queue_name + model |
| 打开时 fetch queues | ✅ useQuery enabled=visible |
| crypto.randomUUID() | ✅ |
| 成功 Message.success + 跳转详情 | ✅ navigate to /compute/gpu-containers/$instanceId |
| 422 InsufficientGPU Message.error | ✅ |
| 422 QueueNotFound 高亮队列字段 | ✅ |
| submitting/validation-error/schedule-failed 三态 | ✅ form.validate + mutation.isPending + catch |
| GET /instances/{id} | ✅ |
| Descriptions 基本信息 + GPU 摘要 | ✅ 两张 Descriptions |
| state_reason Alert theme=error | ✅ |
| provisioning 提示「调度中，预计 1-2 分钟」 | ✅ |
| 404 Empty「实例不存在」 | ✅ |

### 3.2 验证命令

```bash
cd repo/frontends/console
npx tsc --noEmit     # EXIT_CODE: 0
npx vite build      # built in 3m 38s
```

### 3.3 已知限制

- Console 项目无 eslint config（pre-existing）
