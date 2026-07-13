# SANDBOX-KATA-WORKLOAD-RUNTIME-A — Sandbox 接入 Kubernetes/Kata workload runtime

完成日期：2026-07-13
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：local/logic verified + live smoke passed

> 边界声明：本批次让 Gateway 在 `WORKLOAD_PROVIDER=kubernetes_rest` 时，把 `POST /api/v1/instances` 的 `kind=sandbox` 走既有 WorkloadRuntime / Kubernetes provider orchestration，并使用 `runtimeClassName=sandbox-kata` 落到真实 Kata RuntimeClass。不新增独立 Sandbox provider，不声明 full platform production ready。

## 背景

Sandbox local profile 已完成，但真实集群安装 Kata 后，Gateway 仍默认把 `kind=sandbox` 交给本地 `SandboxRuntime`，不会渲染/应用 Kubernetes Deployment，也不会使用 `runtimeClassName=sandbox-kata`。

## 实现了什么

1. `LocalInstanceService` 增加 `WithSandboxWorkloadOrchestration(true)` 选项；默认行为保持 local sandbox profile。
2. Gateway workload provider 为 `kubernetes_rest` 时启用 sandbox workload orchestration，复用既有 dry-run / apply / status / store 链路。
3. `instanceRecordFromResult` 为 `WorkloadKindSandbox` 填充 `SandboxInstanceStatus`，响应 `dev_profile` 标为 real provider。
4. 补齐 `instance_plan_audits` / `workload_instances` 的 `workload_kind='sandbox'` 数据库约束迁移，并同步真实 lab 初始化 SQL/fixture。
5. `validate-gateway-instance-workload-runtime` 纳入 sandbox/Kata router 回归测试。

## Live evidence

脱敏 evidence：`development-records/live-evidence/sandbox-kata-workload-runtime-live-evidence.json`

本次 live smoke 经 Auth/Dex token 调用 Gateway 创建 sandbox，Kubernetes Deployment 使用 `runtimeClassName=sandbox-kata`，Pod 在真实节点 Running/Ready，日志可见 Kata guest kernel。临时 Deployment 已清理；API DELETE 在本次 smoke 中返回 404，因此清理使用 kubectl fallback。

## 验收命令

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -count=1
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -count=1
make validate-gateway-instance-workload-runtime
python scripts/validate_instance_audit.py deploy/manifests/m1-instance-e deploy/migrations/20260501_001_init_schema.sql
git diff --check
```

## 生产边界

- `sandbox-kata` 是 ANI 稳定入口；集群中的其它 `kata-*` RuntimeClass 是 Kata Helm chart 官方变体。
- 本批次只证明 sandbox create path 真实落到 Kubernetes/Kata；不覆盖长期 reconcile、镜像供应链、API 删除语义和 release/operator gate。
