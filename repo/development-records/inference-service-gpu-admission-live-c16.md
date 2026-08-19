# INFERENCE-SERVICE-GPU-ADMISSION-LIVE-C16

> 日期：2026-08-15
> 状态：live passed（lab Gateway 进程，未 rollout in-cluster `ani-gateway`）
> C25 已删除 lab Gateway harness 及本批次 runner；evidence 仍由 `make validate-inference-gpu-admission-live-gate` 校验。
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 E 准入
> 前置：`INFERENCE-SERVICE-CPU-VLLM-LIVE-C14`、`INFERENCE-SERVICE-CPU-VLLM-OPS-LIVE-C15`

## 完成范围

- 同一 `InferenceService` 入口在 Core 未公布可用 accelerator 时 fail-closed。
- `POST /api/v1/svc/inference-services` 带 `resources.accelerator` 返回 `422 ACCELERATOR_SPEC_UNAVAILABLE`，不落库、不创建 GPU Deployment。
- Core Kubernetes `PlatformWorkload.Create` 按 `Capabilities().accelerator_specs` 准入；当前 capabilities 为空列表。
- 集群无 `nvidia.com/gpu` allocatable，GPU runtime live 记 `skipped_no_device_plugin`。
- 未改 OpenAPI，未滚动生产 Gateway，未触碰 `ani-vllm-cpu-smoke`。
- 不得标记 GPU ready 或 runtime ready。

## Design Decisions

- CPU/GPU 仍是同一产品入口；本轮只证明 GPU 规格在无 inventory 时被拒绝，不把请求改写成 CPU。
- Creator 在写库前走 Core `GET /platform-workload-capabilities`；worker 若仍撞到 Core `PRECONDITION_FAILED`，记终态 `ACCELERATOR_SPEC_UNAVAILABLE`，不重试。
- Local adapter 继续接受 accelerator，供无集群的 local/logic GPU 路径测试。
- 阶段 E 的真实 GPU device-plugin / 单卡 live 仍后置。

## 验证证据

```text
cd repo
gofmt -w pkg/adapters/runtime/local_platform_workload.go \
  pkg/adapters/runtime/kubernetes_platform_workload.go \
  services/inference-service/internal/service/service.go \
  services/inference-service/internal/runtime/coresdk/adapter.go \
  services/inference-service/internal/reconcile/worker.go \
  services/inference-service/main.go
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload|AdmitPlatformWorkload'
cd services/inference-service && GOWORK=off go test ./internal/service/ ./internal/grpcapi/ ./internal/runtime/coresdk/ ./internal/reconcile/ -count=1
PATH=/tmp/ani-pybin:$PATH make validate-inference-gpu-admission-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
python3 scripts/run_inference_gpu_admission_live_gate.py --kubeconfig /root/.kube/config
```

evidence：`development-records/live-evidence/inference-gpu-admission-live-20260815.json`

## 明确未完成

- GPU device-plugin / `nvidia.com/gpu` runtime live 未跑。
- 无 LWS / Volcano / 跨节点，不实现 `/test`，无公网 `invocation_url`。
- 不得把 in-cluster Gateway 或推理产品链路标为 runtime ready。
