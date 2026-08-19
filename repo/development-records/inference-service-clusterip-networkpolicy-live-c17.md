# INFERENCE-SERVICE-CLUSTERIP-NP-LIVE-C17

> 日期：2026-08-15
> 状态：live passed（lab Gateway 进程，未 rollout in-cluster `ani-gateway`）
> C25 已删除 lab Gateway harness 及本批次 runner；evidence 仍由 `make validate-inference-clusterip-networkpolicy-live-gate` 校验。
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` 阶段 F（不含 `/test`）
> 前置：`INFERENCE-SERVICE-CPU-VLLM-LIVE-C14`、`INFERENCE-SERVICE-CPU-VLLM-OPS-LIVE-C15`、`INFERENCE-SERVICE-GPU-ADMISSION-LIVE-C16`

## 完成范围

- 同一 `InferenceService` 入口的 runtime 只暴露 ClusterIP，并渲染默认拒绝的 Ingress NetworkPolicy。
- NetworkPolicy 只允许同租户 namespace、`kube-system`、`ani-system`，以及节点 InternalIP / Kube-OVN node overlay `/32`；禁止 `0.0.0.0/0` 和未授权 namespace。
- 产品 `/test` 保持未注册（404）。`running` 仍走 `kubernetes_proxy` 的 `/health` + 有界 Chat。
- stop 后 ClusterIP / NetworkPolicy / Deployment 消失；delete 后产品 404。
- 未改 OpenAPI，未滚动生产 Gateway，未触碰 `ani-vllm-cpu-smoke`。
- 不得标记 runtime ready，不得把本批次当成公网调用网关或阶段 G。

## Design Decisions

- `/test` 已推翻，阶段 F 只验收 ClusterIP + NetworkPolicy + stop/delete 失效，不复活产品测试入口。
- kubelet 探针可过、apiserver Service proxy 起初被丢掉：控制面流量在 Kube-OVN 上走节点 overlay，不只是 InternalIP。Apply 时列出节点 InternalIP 与 `ovn.kubernetes.io/ip_address`，只写入 `/32` ipBlock。
- 跨 namespace 探针用显式 Pod manifest 跑 `wget --timeout=5 --tries=1`，避免 `kubectl run` 误走镜像默认入口。
- Services 不直接调 Kubernetes；NetworkPolicy 由 Core platform-workload adapter 渲染。

## 验证证据

```text
cd repo
gofmt -w pkg/adapters/runtime/kubernetes_platform_workload_runtime.go \
  pkg/adapters/runtime/kubernetes_rest_client.go \
  pkg/adapters/runtime/kubernetes_platform_workload_test.go
go test ./pkg/adapters/runtime/ -count=1 -run 'PlatformWorkload|RenderPlatformWorkload|NodeInternalCIDRs'
PATH=/tmp/ani-pybin:$PATH make validate-inference-clusterip-networkpolicy-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
python3 scripts/run_inference_clusterip_networkpolicy_live_gate.py --kubeconfig /root/.kube/config
```

evidence：`development-records/live-evidence/inference-clusterip-networkpolicy-live-20260815.json`

## 明确未完成

- 无公网 `invocation_url` / 调用网关 / 鉴权限流计量。
- 无 GPU device-plugin live，无 LWS / 跨节点。
- 不得把 in-cluster Gateway 或推理产品链路标为 runtime ready。
