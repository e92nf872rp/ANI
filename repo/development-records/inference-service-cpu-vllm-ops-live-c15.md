# INFERENCE-SERVICE-CPU-VLLM-OPS-LIVE-C15

> 日期：2026-08-15
> 状态：live passed（lab Gateway 进程，未 rollout in-cluster `ani-gateway`）
> C25 已删除 lab Gateway harness；本记录 evidence 仍由 `make validate-inference-cpu-vllm-ops-live-gate` 校验。
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 D
> 前置：`INFERENCE-SERVICE-CPU-VLLM-LIVE-C14`、`INFERENCE-SERVICE-LOGS-C11`

## 完成范围

- 同一 CPU `InferenceService` 入口补齐阶段 D 还缺的三件事：真实产品 logs、RWO 下的 desired-replicas scale、lab 进程重启回读。
- Core Kubernetes `PlatformWorkload.Logs` 按 workload name label 读 Pod 日志，脱敏 Authorization/Bearer/password/JWT；产品 API 不输出 replica。
- Scale 允许从 `deploying` 发起，并可抢占 in-flight scale。Live 只断言 Deployment `spec.replicas`，不等 2 个 ready replica（模型 PVC 为 RWO）。
- 停 lab Gateway + inference-service 后再拉起同一二进制/环境，`GET` 同一 `service_id` 仍为 `running`。
- 未改 OpenAPI，未滚动生产 `ani-system/ani-gateway`，未触碰 `ani-vllm-cpu-smoke`。
- 不得标记 runtime ready。

## Design Decisions

- C12 validator/evidence 仍要求 logs-empty；C15 使用独立 gate 与 evidence。
- Pod 日志选择器用 `spec.Name`（产品 UUID），不用 DNS-1035 的 `pw-{name}`。Pending 副本的 `/log` 400 跳过，不让整页失败。
- RWO 无法让 replicas=2 变成 `running`；scale=2 后必须能再 PATCH 回 1，否则操作会卡死。
- 重启验证的是 lab 进程回读，不是 in-cluster Gateway rollout。

## 验证证据

```text
cd repo
gofmt -w pkg/adapters/runtime/kubernetes_platform_workload.go \
  pkg/adapters/runtime/kubernetes_platform_workload_runtime.go \
  pkg/adapters/runtime/kubernetes_platform_workload_test.go \
  services/ani-gateway/internal/router/platform_workloads.go \
  services/inference-service/internal/domain/transition.go \
  services/inference-service/internal/domain/transition_test.go
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload'
cd services/inference-service && GOWORK=off go test ./internal/domain/ ./internal/reconcile/ -count=1
PATH=/tmp/ani-pybin:$PATH make validate-inference-cpu-vllm-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-inference-cpu-vllm-ops-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
python3 scripts/run_inference_cpu_vllm_live_gate.py --kubeconfig /root/.kube/config
```

evidence：`development-records/live-evidence/inference-cpu-vllm-ops-live-20260815.json`

## 明确未完成

- GPU device-plugin / `nvidia.com/gpu` live 未跑。
- 无 LWS / Volcano / 跨节点，不实现 `/test`，无公网 `invocation_url`。
- 不得把 in-cluster Gateway 或推理产品链路标为 runtime ready。
