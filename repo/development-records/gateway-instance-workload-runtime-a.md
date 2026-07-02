# GATEWAY-INSTANCE-WORKLOAD-RUNTIME-A — Gateway 实例 workload provider 接线

完成日期：2026-06-30
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：见本文「验收命令」

> 边界声明：本批次补齐 **ani-gateway `/instances` 对 M1 workload provider 的 env 注入**，与 `M1-INSTANCE-P` bootstrap wiring 对齐；不改 Core OpenAPI/SDK；**不新增 production-shaped live gate**；不标 full platform production ready。真实集群写操作仍受 `WORKLOAD_PROVIDER_APPLY_ENABLED` 与 Kubernetes 凭证门禁约束。

## 背景

`M1-INSTANCE-P` 已在 `pkg/bootstrap/deps.go` 支持 `WORKLOAD_PROVIDER=kubernetes_rest`，但 Gateway `demo_instances.go` 长期硬编码 `LocalProviderApply`，导致 Console/`POST /instances` 仅在 PG/内存模拟，无法经既有 ports/adapters 落到集群。

Sprint 13 S07 只覆盖 instance **observability**（Prometheus/kubelet），不包含 instance **create/apply** 的 Gateway provider 接线。

## 实现了什么

1. **Gateway workload runtime**（`services/ani-gateway/workload_runtime.go`）：
   - 读取 `WORKLOAD_PROVIDER` / `WORKLOAD_PROVIDER_APPLY_ENABLED` / `WORKLOAD_LIFECYCLE_*` / `WORKLOAD_OPS_*`
   - `kubernetes_rest` 时注入 `KubernetesProviderAdapter` + 可选 lifecycle/ops executor
   - 复用 `kubernetes_runtime.go` 统一 K8s REST client 配置
2. **Router 注入**（`main.go` → `router.RegisterOptions.InstanceWorkloadRuntime` → `demo_instances.go`）：
   - orchestrator 使用注入的 DryRun/Apply/StatusReader，而非写死 local provider
3. **响应语义对齐**：
   - `dev_profile` / `demo_notice` 在 `kubernetes_rest` 时反映真实 provider 配置（参照 network/storage/gpu handler 模式）
4. **真实 apply 前置修复**（adapter 层，属本批次最小闭环）：
   - workload identity `Secret` DNS-1123 名称清洗 + 先于 Deployment 渲染
   - admission / dry-run / apply 允许 `Secret` + `Deployment`/`VirtualMachine` 混合批次

## 关键文件

| 文件 | 说明 |
|---|---|
| `services/ani-gateway/workload_runtime.go` | Gateway env → `InstanceWorkloadRuntime` |
| `services/ani-gateway/workload_runtime_test.go` | local / kubernetes_rest 装配单测 |
| `services/ani-gateway/internal/router/instance_workload_runtime.go` | Router 侧 runtime 结构 |
| `services/ani-gateway/internal/router/demo_instances.go` | 注入 orchestrator；`dev_profile` / `demo_notice` |
| `services/ani-gateway/main.go` | 启动时装配并打日志 |
| `pkg/adapters/runtime/dryrun_renderer.go` | Secret 渲染与 DNS-1123 修复 |
| `pkg/adapters/runtime/admission.go` | 允许 Secret kind |
| `pkg/adapters/runtime/provider_dryrun.go` / `provider_apply.go` | 混合批次 provider 执行 |
| `scripts/validate_gateway_instance_workload_runtime.py` | 离线 wiring 契约 gate |
| `.env.example` | `WORKLOAD_*` 开发说明 |

## 本地开发用法

```bash
# .env（与 bootstrap M1-INSTANCE-P 一致）
WORKLOAD_PROVIDER=kubernetes_rest
WORKLOAD_PROVIDER_APPLY_ENABLED=true
WORKLOAD_LIFECYCLE_PROVIDER=kubernetes_rest
WORKLOAD_LIFECYCLE_APPLY_ENABLED=true
WORKLOAD_OPS_PROVIDER=kubernetes_rest
WORKLOAD_OPS_ENABLED=true
GPU_INVENTORY_PROVIDER=kubernetes_rest
# KUBECONFIG 或 KUBERNETES_* 见 SPRINT13-KUBERNETES-REST-CREDENTIAL-RESOLVER-A
```

未设置 `WORKLOAD_PROVIDER` 时行为与改前一致：local provider adapters。

## 验收命令

```bash
cd repo
make validate-gateway-instance-workload-runtime
go test ./services/ani-gateway/... ./pkg/adapters/runtime/... -count=1
make test
make validate-architecture
git diff --check
```

## 生产边界

- 不等同于 Sprint 13 S01–S07 已 passed 的 production-shaped live gate 结论扩展
- 未新增 `validate-instance-workload-live-gate`；后续若需标 production-shaped passed，须单独建 live gate + evidence JSON
- 镜像拉取、Harbor pull secret、长期 reconcile 仍属后续 release/operator gate
