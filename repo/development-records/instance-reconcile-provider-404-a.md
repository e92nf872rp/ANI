# INSTANCE-RECONCILE-PROVIDER-404-A

> 日期：2026-08-02
> 范围：ANI Core / Workload Reconcile / Kubernetes provider-loss persistence

## 目标

修复 Kubernetes 主 workload 资源被集群侧删除后，PostgreSQL 实例仍长期保持 `running` 的状态失配。本批复用已有 controller 契约，将缺失资源回写为 `failed/ProviderResourceLost`。

## 根因

1. `KubernetesRESTClient.Observe` 在主资源 GET 返回 HTTP 404 时，原样返回 `resilience.StatusError`，而 controller 只识别 `ports.ErrNotFound`。
2. Sandbox 的逻辑 provider 为 `kubernetes_sandbox_runtime`，但物理资源引用为 `kubernetes/Deployment/...`。`resourceFromRef` 在 HTTP 请求前就因 provider 不匹配返回 `ErrInvalid`，导致首次 v1 live 验证出现 reconcile failure。

## 实现

- 主资源 Observe 仅将 Kubernetes HTTP 404 包装为 `ports.ErrNotFound`
- 403、429、5xx、超时和网络错误保持原语义，不误判为资源丢失
- `kind=sandbox` 且逻辑 provider 为 `kubernetes_sandbox_runtime` 时，使用物理 provider `kubernetes` 校验 resource ref；Observation 对外仍保留逻辑 provider
- 复用 `LocalWorkloadReconcileController.markProviderMissing`，不新增状态机、port 或数据库 schema
- 新增 `validate-instance-reconcile-provider-loss-live-gate`，固定集群侧删除和重复 reconcile 幂等验收

## 真实环境结果

- Reconcile Worker：`docker.changqingyun.cn/ani/reconcile-worker:instance-provider-404-20260802-v2`
- 镜像 digest：`sha256:d2655a9fbb31638d899257a7f7179fcfe037c3365b82ef138a23cd08e26b6652`
- Core 创建：`sandbox_9` 达到 `running`
- 集群侧删除 Deployment 后：Core/PG 达到 `failed`，`reason=ProviderResourceLost`
- 额外等待一个常规 reconcile 周期后：仍为 `failed/ProviderResourceLost`
- Worker metrics：`successes=1`，`failures=0`
- Core lifecycle cleanup：最终 `deleted`，Deployment/Pod/Service 残留数为 0
- Evidence：`development-records/live-evidence/instance-reconcile-provider-loss-live-20260802.json`

## 验证

```bash
go test ./pkg/adapters/runtime -run 'TestKubernetesRESTClientObserveClassifiesPrimaryResourceNotFound|TestLocalWorkloadReconcileControllerPersistsKubernetesPrimaryResourceLoss' -count=1
make validate-instance-reconcile-provider-loss-live-gate
python3 scripts/validate_yaml.py deploy/real-k8s-lab/instance-reconcile-provider-loss-live-gate.yaml
```

## 边界

- 不改 Core OpenAPI v1，不修改 Services。
- 本批只收口 provider-loss 观测和 PG 状态一致性，不解决 `KubernetesSandboxRuntime` 的进程内 session/refs 重启恢复。
- 失败实例在 Core delete 后保留 `deleted` 审计历史，不做物理删除。
- 不自动 commit，保留人工确认关口。
