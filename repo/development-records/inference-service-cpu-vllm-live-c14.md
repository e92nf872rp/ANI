# INFERENCE-SERVICE-CPU-VLLM-LIVE-C14

> 日期：2026-08-15
> 状态：live passed（lab Gateway 进程，未 rollout in-cluster `ani-gateway`）
> C25 已删除 lab Gateway harness；本记录 evidence 仍由 `make validate-inference-cpu-vllm-live-gate` 校验。复跑走 C21+ 现网 Gateway。
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 D
> 前置：`INFERENCE-SERVICE-RUNTIME-C13`、`INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-C12`

## 完成范围

- 同一产品入口 `POST /api/v1/svc/inference-services` 跑通单节点 CPU vLLM live。不传 `resources.accelerator`。
- lab Gateway 进程注入 `InferenceControl` gRPC；本地 inference-service 经 Core SDK 创建 `platform-workloads`。未滚动生产 `ani-system/ani-gateway`。
- 模型来自独立 smoke PVC 的 CSI 快照恢复，不占用、不删除 `ani-vllm-cpu-smoke`。
- `running` 必须通过 Kubernetes API service proxy 的 `/health` 与有界 `/v1/chat/completions`。不注册产品 `/test`。
- 集群无 `nvidia.com/gpu` allocatable，GPU live 记 `skipped_no_device_plugin`。
- 未改 OpenAPI，无 LWS，不得标记 runtime ready。

## Design Decisions

- CPU/GPU 仍是同一入口；本轮只执行 CPU 规格。
- 集群外 lab 进程打不到 ClusterIP，因此 Health/Smoke 走 `INFERENCE_RUNTIME_PROBE_VIA=kubernetes_proxy`。
- 源模型 PVC 为 RWO 且已被 smoke 占用，使用 VolumeSnapshot + 跨 namespace 静态 VolumeSnapshotContent 恢复。
- vLLM CPU 启动参数对齐已知可跑的 smoke：`float32` / `max-model-len 1024` / `enforce-eager`，并用 `env` 注入 CPU KV cache 环境变量。
- readiness `failureThreshold=90`，不设 liveness，避免加载期被杀。
- Kubernetes Service 必须是 DNS-1035（不能以数字开头）。PlatformWorkload 把数字开头的名字写成 `pw-{name}`，Deployment / Service / endpoint 共用该名字。
- create 一旦写出 `runtime_ref`，后续 retry 只 Observe，不再 Ensure/PATCH，避免 RWO 卷上的滚动重建。
- Ensure 若已拿到 `resource_id` 但随后 Observe 失败，仍先落 `runtime_ref`，再按 retry 等待 ready。

## 验证证据

```text
cd repo
gofmt -w pkg/adapters/runtime/kubernetes_platform_workload_runtime.go \
  services/inference-service/main.go \
  services/inference-service/internal/engine/launch.go \
  services/inference-service/internal/runtime/coresdk/adapter.go \
  services/inference-service/internal/runtime/coresdk/probe_kube.go \
  services/ani-gateway/cmd/platform-workload-live/main.go
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload'
cd services/inference-service && GOWORK=off go test ./internal/engine/ ./internal/runtime/coresdk/ ./internal/catalog/fake/ ./internal/reconcile/ -count=1
PATH=/tmp/ani-pybin:$PATH make validate-inference-cpu-vllm-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
python3 scripts/run_inference_cpu_vllm_live_gate.py --kubeconfig /root/.kube/config
```

evidence：`development-records/live-evidence/inference-cpu-vllm-live-20260815.json`

## 明确未完成

- GPU device-plugin / `nvidia.com/gpu` live 未跑。
- 无 LWS / Volcano / 跨节点，不实现 `/test`，无公网 `invocation_url`。
- 不得把 in-cluster Gateway 或推理产品链路标为 runtime ready。
