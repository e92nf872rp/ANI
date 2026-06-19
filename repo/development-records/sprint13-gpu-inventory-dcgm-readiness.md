# Sprint 13 切片 04 — GPU inventory / occupancy NVIDIA device-plugin + DCGM real provider 就绪声明

> 记录类型：Per-slice readiness（ANI-06「真实底座组件引入强制门禁」§153 的执行前声明）
> 工件归属：Sprint 13 / Core real provider 与 live gate 收敛
> 执行地图：[`sprint13-real-provider-readiness-plan.md`](sprint13-real-provider-readiness-plan.md)
> 状态：**code+contract ready, LIVE PENDING**（A 轨已完成；尚未跑通真实 live gate）。在 evidence 产出前，GPU inventory 与 occupancy 只可标 Tier1 local profile。

---

## 0. 已核对的真实事实（禁止臆测）

1. Sprint 12 已落地 GPU inventory / occupancy 契约与本地实现：`ports.GPUInventory`（`pkg/ports/gpu_inventory.go`）、`LocalGPUInventory`（`pkg/adapters/runtime/local_gpu_inventory.go`）和 Gateway `gpu_inventory_resources.go`。
2. OpenAPI 已定义 `listGPUInventory` 与 `getGPUOccupancy`，响应 schema 为 `GPUInventoryRecord`、`GPUInventoryListResponse`、`GPUOccupancyStats`，并保留 `x-ani-rbac-scope: scope:gpu-inventory:read`。
3. Sprint 5 已完成三节点 NVIDIA driver、NVIDIA Container Toolkit、device plugin、`nvidia.com/gpu` allocatable 与 GPU smoke Pod 真实验证；这些是 S04 前置事实，不等同于当前 Core API `/gpu-inventory` live evidence。
4. S04 A 轨只允许新增 adapter 只读代码、fake/mock 单测、契约级 live-gate 和文档闭环；不执行真实 `kubectl apply`、DCGM 部署、Prometheus/DCGM 查询或 GPU workload 写操作。

## 1. §153 五项声明

| 项 | 内容 |
|---|---|
| **当前状态** | contract + Tier1 local profile；Gateway 当前默认使用 `LocalGPUInventory`；A 轨已补 Kubernetes node/device-plugin label/capacity 的只读 adapter contract。 |
| **真实组件 + 版本** | NVIDIA device plugin / DCGM exporter / Kubernetes node labels；Kubernetes `v1.36.1` 已知，NVIDIA device plugin、driver、DCGM exporter 版本需 B 轨执行前在真实 lab 只读确认。 |
| **live gate 命令** | 本地契约：`make validate-gpu-contracts validate-gpu-inventory-live-gate`；真实 B 轨为 human-gated，需人工确认 kubeconfig/token、NVIDIA device-plugin/DCGM/Prometheus 来源和 evidence 输出路径。 |
| **evidence 输出路径** | `repo/development-records/sprint13-gpu-inventory-dcgm-live-result.md` + 非敏感 evidence JSON。 |
| **失败边界（不得声称）** | 若 `/gpu-inventory` 与 `/gpu-inventory/occupancy` 未在真实 NVIDIA device-plugin/DCGM 或等价后端跑通并归档 evidence，不得标 real-provider / runtime ready / production ready；不得用 Sprint 5 GPU smoke Pod 直接替代当前 Core API live evidence。 |

## 2. 代码边界

- A 轨已新增 `ports.GPUInventory` 的 Kubernetes 只读 adapter，不改 port 接口签名，不改 Gateway handler，不新增 `/api/v1/svc`。
- adapter 只从 Kubernetes Node list 文档解析 node labels、capacity/allocatable `nvidia.com/gpu`、node readiness 和 nodeInfo；DCGM 指标接入留 B 轨确认真实来源后推进。
- 失败必须 fail closed：Kubernetes API 返回非 2xx、JSON 非法、缺少可识别 GPU 资源时返回空清单或错误，不伪造 runtime ready。

## 3. 真实服务器安全

- A 轨不执行 Helm/kubectl apply，不部署 DCGM exporter，不创建 GPU Pod 或修改 node label。
- B 轨执行前必须由人工确认 kubeconfig/token、API server、DCGM/Prometheus endpoint、GPU 节点选择和证据输出路径；凭据不得写入可提交文件或回复。

## 4. 完成判定（A 轨）

```bash
cd repo && make test && make validate-gpu-contracts validate-gpu-inventory-live-gate && python scripts/validate_yaml.py api/openapi/v1.yaml && make validate-doc-entrypoints && git diff --check
```

## 5. 关联文档

- Sprint 13 执行地图：[`sprint13-real-provider-readiness-plan.md`](sprint13-real-provider-readiness-plan.md)
- 当前冲刺入口：[`../CURRENT-SPRINT.md`](../CURRENT-SPRINT.md)
- Sprint 5 GPU historical evidence：[`m1-k8s-live-k-gpu-scheduling-real-lab-progress.md`](m1-k8s-live-k-gpu-scheduling-real-lab-progress.md)
- S04 A 轨记录：[`sprint13-gpu-inventory-dcgm-a-track.md`](sprint13-gpu-inventory-dcgm-a-track.md)
- 代码：`pkg/ports/gpu_inventory.go`、`pkg/adapters/runtime/local_gpu_inventory.go`、`services/ani-gateway/internal/router/gpu_inventory_resources.go`
