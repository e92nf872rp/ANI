# INFERENCE-SERVICE-GPU-VLLM-EAGER-C34

> 日期：2026-08-18
> 状态：live passed（生产 `ani-system/ani-gateway` + `inference-service`，`ANI_AUTH_MODE=auth_service`）
> 前置：`INFERENCE-SERVICE-GPU-SINGLE-NODE-LIVE-C32`、`GPU-PLUGIN-NODE-PARTITION-C33`

## 完成范围

- GPU vLLM launch 增加 `--enforce-eager`，与 CPU 路径一样跳过 V1 `torch.compile` / cudagraph。
- 现网保留的单节点整卡 GPU `InferenceService` 在 `dev-phys-02` 达到 Pod Ready，runtime `/health` 200。产品 GET 仍为 `running`；`invocation_url` / `endpoint_url` 保持 null。未做 delete。
- 现网 `inference-service` 滚动到 `gpu-eager-20260818`，后续 create/start 会带上该参数。`ANI_AUTH_MODE` 保持 `auth_service`。
- 未改 OpenAPI。跨节点 LWS 仍 skip。不得标记 GPU ready / runtime ready。

## Design Decisions

- P0 GPU 启动与 CPU 一样关 compile。不新增可配置开关。
- 已创建服务的 command/args 冻结在 Core spec 里；本次对现网 Deployment 补 `--enforce-eager` 让当前副本 Ready。产品路径的长期入口是 `engine.Launch`。

## Deviations

- C32 产品 GET `running` 时 kubelet Ready 仍可能为 false。C34 以 kubelet Ready + `/health` 200 收口启动。
- 未再走 stop/start，以免打断刚 Ready 的用户服务。

## Tradeoffs

- `--enforce-eager` 牺牲 CUDA graph 吞吐，换可接受的启动时间。0.5B 模型在 V1 compile 上卡住超过 30 分钟且 8000 未监听。
- 跨节点 LWS 需要至少两张可调度整卡且 PVC 可多节点使用；当前只有一个带 RBD CSI 的整卡 worker，且用户服务占用其中 1 GPU。

## Open Questions

- 跨节点 LWS / gang Queue 仍未 live。集群已有 LWS CRD、controller 与 `ani-inference` Queue；受节点分区、控制面无 RBD CSI、RWO 模型 PVC 限制，不能把 C20 gate 标成 live passed。
- 私有 Harbor `imagePullSecrets` 仍未做。

## 验证证据

```text
cd /root/kubercon/ANI/repo
GOWORK=off go test -C services/inference-service ./internal/engine ./internal/runtime/coresdk -count=1
PATH=/tmp/ani-pybin:$PATH make validate-inference-gpu-vllm-eager-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

evidence：`development-records/live-evidence/inference-gpu-vllm-eager-live-20260818.json`
gate：`deploy/real-k8s-lab/inference-gpu-vllm-eager-live-gate.yaml`（`status: live`）

## 明确未完成

- 跨节点 LWS runtime live。
- 不得把本批次标为 GPU ready / runtime ready。
