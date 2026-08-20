# GPU-PLUGIN-NODE-PARTITION-C33

> 日期：2026-08-18
> 状态：live passed（节点 `gpu-mode` 分区：整卡 NVIDIA device-plugin / vGPU volcano-device-plugin）
> 前置：`INFERENCE-SERVICE-GPU-SINGLE-NODE-LIVE-C32`

## 完成范围

- 按节点拆开 NVIDIA GPU Operator device-plugin 与 `volcano-device-plugin`，避免再抢 `nvidia.com/gpu`。
- 整卡节点（用户指定 66/67 → `kubercloud`、`dev-phys-02`）：`ani.kubercloud.io/gpu-mode=wholecard`，NVIDIA device-plugin 广告 `nvidia.com/gpu`。
- vGPU 节点（用户指定 68 → `dev-phys-03`）：`gpu-mode=vgpu`，`nvidia.com/gpu.deploy.device-plugin=false`，只留 volcano-device-plugin 广告 `volcano.sh/vgpu-*`。
- 现网整卡 InferenceService 从 vGPU 节点迁到 `dev-phys-02`。未改 OpenAPI。不得标记 GPU ready / runtime ready。

## Design Decisions

- 分区粒度是节点，不是单卡 UUID。一张节点只跑一种 device-plugin。
- 沿用已有 `ani.kubercloud.io/gpu-mode` 与 volcano DS 的 `nodeSelector=gpu-mode=vgpu`。
- 关闭 vGPU 节点上的 NVIDIA device-plugin 用 GPU Operator 官方标签 `nvidia.com/gpu.deploy.device-plugin=false`，不改 ClusterPolicy 全局开关。

## Deviations

- C32 的 GPU runtime 当时落在后来划给 vGPU 的节点上。C33 把它迁到整卡节点。
- 控制面节点 66 是整卡，但没有 Rook RBD CSI，带 PVC 的推理 Pod 不能落在那里。

## Tradeoffs

- 把占用整卡节点 `nvidia.com/gpu` 记账的 demo GPU 容器（`ani.io/demo=true` 的 `test-2` / `test-dj`）缩到 0，否则 Volcano 认为 67 上两张卡已被占满，整卡推理无法调度。
- 默认命名空间里的 vGPU 测试 Deployment 从 67 迁到 68。

## Open Questions

- 控制面整卡节点没有 RBD CSI，PVC 推理只能用 67 的两张整卡。
- HAMi device-plugin 仍是 `gpu=none` 停着；旧 `hami.io/*` 注解还留在 vGPU 节点上。
- 跨节点 LWS 仍未 live。

## 验证证据

```text
cd /root/kubercon/ANI/repo
PATH=/tmp/ani-pybin:$PATH make validate-gpu-plugin-node-partition-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/gpu-plugin-node-partition-live-20260818.json`
gate：`deploy/real-k8s-lab/gpu-plugin-node-partition-live-gate.yaml`（`status: live`）

## 明确未完成

- 跨节点 LWS runtime live。
- 不得把本批次标为 GPU ready / runtime ready。
