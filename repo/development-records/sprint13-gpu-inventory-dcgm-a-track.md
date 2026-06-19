# Sprint 13 S04 - GPU inventory / occupancy NVIDIA device-plugin + DCGM A-track

> 记录类型：Sprint 13 A-track completion record
> 日期：2026-06-19
> 范围：ANI Core only
> 状态：code+contract ready, LIVE PENDING

## 目标

把 Sprint 12 已落地的 `listGPUInventory` 与 `getGPUOccupancy` 从 Tier1 local profile 扩展到 NVIDIA device-plugin / DCGM 的真实 provider contract 代码边界。A 轨只做只读 adapter contract、fake/mock 单测、契约级 live-gate 和文档闭环；不执行真实 `kubectl apply`、DCGM 部署、GPU workload 写操作或 live gate。

## 实现

- `pkg/adapters/runtime/kubernetes_gpu_inventory.go`
  - 新增 `KubernetesGPUInventory`，实现既有 `ports.GPUInventory`。
  - 通过 `KubernetesRESTClient` 只读 `/api/v1/nodes`，解析 `nvidia.com/gpu` capacity/allocatable、`nvidia.com/gpu.product`、node readiness 和 nodeInfo。
  - `PlanScheduling` 返回基于 node selector 与 `nvidia.com/gpu` resourceName 的 contract scheduling decision。
- `pkg/adapters/runtime/kubernetes_gpu_inventory_test.go`
  - fake HTTP transport 覆盖 Kubernetes NodeList 解析与 label/nodeName filter。
- `deploy/real-k8s-lab/gpu-inventory-live-gate.yaml`
  - 新增 `SPRINT13-GPU-INVENTORY-DCGM-A` GPU inventory live gate contract。
- `scripts/validate_gpu_inventory_live_gate.py`
  - 新增 contract validator，固定 NVIDIA device-plugin node capacity、Core `/gpu-inventory`、Core `/gpu-inventory/occupancy` 与 DCGM metrics readable 四个 check；`--live` 保持 human-gated，不在 A 轨自动执行。

## 边界

- 未修改 `ports.GPUInventory` 签名。
- 未修改 Gateway handler。
- 未新增 `/api/v1/svc`。
- 未执行真实服务器/集群写操作。
- 未把 GPU inventory/occupancy 标记为 real-provider/runtime/production ready。

## 验证

已执行最终门禁：

```bash
cd repo && make test && make validate-gpu-contracts validate-gpu-inventory-live-gate && python scripts/validate_yaml.py api/openapi/v1.yaml && make validate-doc-entrypoints && git diff --check
```

关键输出：

```text
component import guard passed
auth gateway contract valid
PASS
validated heterogeneous GPU contracts under deploy/manifests/m1-gpu-a
SPRINT13-GPU-INVENTORY-DCGM-A contract valid; live execution is human-gated
Ran 5 tests in 0.006s
OK
validated 1 YAML files
document entrypoint boundaries valid
git diff --check passed
```

## 后续 B 轨

人工确认真实 kubeconfig/token、NVIDIA device-plugin 版本、DCGM exporter/Prometheus 来源、GPU 节点选择和 evidence 输出路径后，执行 human-gated live gate 并归档非敏感 evidence。真实 evidence 归档前，S04 保持 Tier1 local profile / LIVE PENDING。
