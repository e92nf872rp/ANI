# M1-K8S-G — Node Pool Provider Boundary

完成日期：2026-05-25
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 `K8sClusterNodePoolProvider*` port、Kubernetes node pool provider adapter 和 Gateway runtime 配置；GREEN 后目标测试、Sprint 固定门禁、`make test` 与 `git diff --check` 均通过。

## 实现了什么

新增 K8s node pool provider 代码边界：`ports.K8sClusterNodePoolProvider` 表达 node pool create/update/delete intent，local K8s cluster service 只有在 node pool provider 真正 apply 后才把 node pool 标记为 real provider。

新增 `KubernetesNodePoolProviderAdapter`，将 node pool intent 渲染为 Cluster API `MachineDeployment` manifest，并通过已有 Kubernetes REST apply 通道提交。GPU intent 会以 labels 和 machine spec 中的 `gpu` 字段保留，便于后续 live 调度验证。

Gateway 新增 `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` runtime 选择，可与现有 `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 组合。该批次不是真实节点池 live 扩缩容或 GPU 调度验证；真实 lab 仍需后续 live gate 或执行记录证明。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 node pool provider request/result/interface |
| `pkg/adapters/runtime/local_k8s_cluster_service.go` | 修改 | create/update/delete node pool 接入 provider；无 provider 时保持 local profile |
| `pkg/adapters/runtime/kubernetes_node_pool_provider.go` | 新增 | Cluster API MachineDeployment manifest 渲染与 apply adapter |
| `pkg/adapters/runtime/kubernetes_rest_client.go` | 修改 | 支持 `clusterapi/MachineDeployment` REST resource mapping |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | 新增 `K8S_CLUSTER_NODE_POOL_PROVIDER_MODE=clusterapi_kubernetes_rest` runtime wiring |

## 完工标准达成

- [x] 先写失败测试并确认 RED：node pool provider port/adapter/Gateway config 不存在
- [x] Local service 只有 node pool provider apply 后才标记 node pool real provider
- [x] Kubernetes adapter 渲染 Cluster API `MachineDeployment`，包含 replicas、tenant namespace、clusterName、instanceType 和 GPU intent
- [x] Gateway runtime 可组合 `vcluster_helm` cluster provider 与 `clusterapi_kubernetes_rest` node pool provider
- [ ] 真实节点池 live 扩缩容验证
- [ ] GPU 节点池真实调度验证

## 验证命令

```bash
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./pkg/adapters/runtime -run 'TestLocalK8sClusterServiceKeepsNodePoolsLocalWithoutNodePoolProvider|TestLocalK8sClusterServiceAppliesNodePoolsThroughProvider|TestKubernetesNodePoolProviderAdapter|TestKubernetesRESTClientSupportsClusterAPIMachineDeployment' -v
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test ./services/ani-gateway -run 'TestGatewayK8sClusterServiceFromConfigUsesClusterAPINodePoolProvider|TestGatewayK8sClusterServiceFromConfigUsesVClusterHelmProvider' -v
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml deploy/real-k8s-lab/profile.yaml deploy/real-k8s-lab/vcluster-live-gate.yaml deploy/real-k8s-lab/kms-sm4-live-gate.yaml deploy/real-k8s-lab/secrets-live-gate.yaml
make validate-real-k8s-profile
make validate-vcluster-live-gate
make validate-kms-sm4-live-gate
make validate-secrets-live-gate
make validate-mock-a
make validate-doc-api
make validate-sdk-beta
make validate-sdk-mock-smoke
make validate-core-beta
make validate-core-api-compatibility
make validate-sprint4-closure
make validate-architecture
make test
git diff --check
```

## 备注

Delete 当前以 `replicas=0` 和 `ani.kubercloud.io/delete-intent=true` 表达 provider delete intent，避免在尚未固定 Cluster API provider 生命周期策略前直接删除真实底层资源。真实 provider 删除语义需要后续 live 验证和运维策略确认。
