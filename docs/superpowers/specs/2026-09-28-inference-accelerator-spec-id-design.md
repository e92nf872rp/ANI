# Inference Accelerator GPUSpec ID 统一设计

## 背景

推理服务创建请求目前同时接触两套 GPU 标识：Core `/gpu-specs` 返回的 GPUSpec 资源 ID（例如 `rtx4090-12g-4`），以及 Core `platform-workload-capabilities` 返回的节点型号 ID（例如 `gpu-nvidia-geforce-rtx-4090`）。Services v1 将 `resources.accelerator.spec_id` 直接送入能力准入，因此前端从 `/gpu-specs` 选择的规格会得到 `ACCELERATOR_SPEC_UNAVAILABLE`。

这两个标识表达的层次不同：GPUSpec ID 是用户选择的、包含整卡/vGPU规格语义的资源目录 ID；节点型号 ID 是 Core 调度实现的内部能力键。前端不应了解后者。

## 目标和边界

- 将 Core GPUSpec ID 定为用户侧和 Services v1 的唯一规范 `spec_id`。
- 保留 Services 的 `count_per_replica` 语义；进入 Core platform-workloads 时显式转换为 `count`。
- 让 Core 在准入阶段解析 GPUSpec ID 到节点型号、GPU 模式、显存和调度资源，不把内部型号键暴露给前端。
- 在一个兼容窗口内接受历史节点型号 ID，并在响应、持久化快照和新示例中统一使用 GPUSpec ID。
- 整卡与 vGPU 通过 `memory` 区分：整卡省略，vGPU 使用正的 MiB 值；`memory: 0` 继续拒绝。
- 保持 `acc`、`accelerator.count` 等非正式字段不进入 Services 契约。

本设计不改变模型命令、dtype、镜像或租户认证逻辑，也不允许 accelerator 请求在无可用规格时静默降级为 CPU。

## 规范契约

Services v1 创建请求使用：

```json
{
  "resources": {
    "cpu": "4",
    "memory": "16Gi",
    "accelerator": {
      "spec_id": "rtx4090-12g-4",
      "count_per_replica": 1,
      "memory": 12285
    }
  }
}
```

整卡请求省略 `memory`；vGPU 请求的 `memory` 必须为正数。`spec_id` 必须是 `/gpu-specs` 或 `/gpu-specs/availability` 返回的 GPUSpec ID。Services 内部仍保存 `count_per_replica`，Core 请求体才使用 `count`。

Core 能力响应对外应列出规范 GPUSpec ID，以及 `available`、整卡/ vGPU 可用数量和显存上限。若现有响应暂时只能返回通用能力结构，可增加可选的规范规格列表字段；现有节点型号字段只作为过渡别名，不再作为前端选择值。

## 组件和数据流

1. 前端调用 `/api/v1/gpu-specs/availability`，仅展示 `status=available` 的 GPUSpec；选择后原样提交 `spec_id`，并根据规格模式填写或省略 `memory`。
2. ANI Gateway 按 Services v1 解析 `resources.accelerator`，验证 `spec_id` 非空、`count_per_replica >= 1`、`memory`（如存在）大于零，再把字段映射到 inference-service gRPC。Gateway 不根据 GPU 型号字符串猜测规格。
3. inference-service 在 Core 准入前保留规范 GPUSpec ID。它调用 Core OpenAPI 的能力/解析接口，得到不可变的规格解析结果，并将该结果纳入创建意图和运行时请求指纹。
4. Core 解析 GPUSpec ID，校验规格存在、可用、租户容量和拓扑；随后根据解析结果生成 Kubernetes/Volcano 资源请求。Core platform-workload 请求中的 `resources.accelerator.spec_id` 可以继续使用规范 ID，Core 内部自行完成节点型号映射。
5. Core 的平台工作负载响应和 Services 查询响应使用规范 GPUSpec ID；旧节点型号别名只用于输入兼容和审计诊断。

## 兼容策略

- 在迁移窗口内，Core 接受当前的节点型号 ID（如 `gpu-nvidia-geforce-rtx-4090`）作为 deprecated alias，并立即解析到一个或多个可用 GPUSpec。若一个型号对应多个 GPUSpec，必须要求请求通过 `memory` 或显式规格 ID消除歧义；不能任选一个规格。
- Services 的旧 `gpu_type` / `gpu_count_per_pod` 只做已有 v1 兼容入口，规范化后同样进入 GPUSpec 解析；只传数量不能推断 GPU。
- `acc`、`accelerator.count` 和规范 GPUSpec ID之外的任意型号文本返回 `400 INVALID_ARGUMENT` 或 `422 ACCELERATOR_SPEC_UNAVAILABLE`，错误中包含规范字段名和请求的 `spec_id`，避免静默忽略。
- 兼容窗口结束后，节点型号 alias 从请求契约中移除；数据库中的已创建服务保留规范 GPUSpec 快照，不受目录后续变化影响。

## 错误语义

- 字段缺失、数量非正数、`memory <= 0` 或别名无法消歧：`400 INVALID_ARGUMENT`。
- GPUSpec 不存在、已禁用或无法映射到节点能力：`422 ACCELERATOR_SPEC_UNAVAILABLE`。
- 规格存在但当前容量不足：`422 INSUFFICIENT_CAPACITY`。
- 请求的 placement/topology 不满足规格或副本条件：`422 UNSUPPORTED_TOPOLOGY`。

错误响应应保留稳定的 `code`，并在内部日志中记录规范 `spec_id`、解析出的 GPU 模式、请求显存和能力快照版本；不得记录认证凭据。

## 测试和上线验证

- Services OpenAPI contract test：确认 `spec_id`、`count_per_replica`、`memory` 的结构和限制。
- Gateway unit test：确认规范 GPUSpec ID、旧节点型号 alias、`count`/`acc` 错误输入和 `memory: 0` 的映射/拒绝行为。
- Core adapter test：用整卡、vGPU、同型号多 GPUSpec 和无可用规格的能力快照验证解析、消歧和资源转换。
- inference-service test：确认规范 ID进入 request hash/持久化快照，错误分别映射为 400/422，且 accelerator 不会降级为 CPU。
- 旧集群 live gate：先读取 `/gpu-specs/availability` 和规范能力列表，再用一个可用 vGPU GPUSpec 创建服务，检查 Core workload 的 Volcano 资源、节点匹配和运行状态；随后删除测试服务并确认幂等重试不改变规格快照。
- 在前端切换到规范 ID 后，回归 CPU 请求、整卡请求和 vGPU 请求三条路径。

## 实施顺序

1. 先修改并冻结 Core 跨层能力/解析契约。
2. 实现 Core 的 GPUSpec 解析和节点型号映射，补齐 adapter 与 live gate。
3. 更新 Services v1 文档、Gateway/inference-service 映射及生成 SDK。
4. 更新 Console 的 GPU 选择和创建请求。
5. 在旧集群执行 live gate，确认 `ACCELERATOR_SPEC_UNAVAILABLE` 不再由 ID 层次不一致触发。
