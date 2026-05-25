# M1-K8S-E · K8s Cluster Upgrade Code Boundary

> 日期：2026-05-24
> 范围：Sprint 5 / K8s 集群管理
> 状态：代码边界已完成；live vCluster 升级验证未完成

## 目标

为 K8s 集群补齐版本升级的最小 Core API 与 provider intent 边界：

- `POST /api/v1/k8s-clusters/{cluster_id}/upgrade`
- request body 支持 `idempotency_key` 和目标 `version`
- local profile 更新集群版本并保持 `running`
- vCluster provider mode 将升级意图委托给 provider adapter
- vCluster Helm adapter 使用 `helm upgrade --install` 表达目标 Kubernetes 版本

该批次只证明 API、ports/adapters、Gateway 接线和 Helm intent 渲染边界，不证明真实 vCluster 已完成 live 升级。

## 实现摘要

- `api/openapi/v1.yaml`
  - 新增 `K8sClusterUpgradeRequest` schema。
  - 新增 `POST /k8s-clusters/{cluster_id}/upgrade`，operationId 为 `upgradeK8sCluster`，RBAC scope 为 `scope:k8s-clusters:manage`。
- `pkg/ports/k8s_clusters.go`
  - 新增 `K8sClusterUpgradeRequest` 和 `K8sClusterService.UpgradeCluster`。
  - 新增 `K8sClusterProviderUpgradeRequest`、`K8sClusterProviderUpgradeResult` 和 `K8sClusterProviderUpgrade` provider port。
- `pkg/adapters/runtime/local_k8s_cluster_service.go`
  - 新增升级幂等记录。
  - local cluster 可更新 `Version`，状态保持 `running`。
  - real provider cluster 可通过 `WithK8sClusterProviderUpgrade` 委托 provider upgrade。
- `pkg/adapters/runtime/vcluster_helm_provider.go`
  - 新增 `UpgradeK8sCluster`。
  - Helm intent 为 `helm upgrade --install <release> vcluster --repo https://charts.loft.sh --namespace <tenant namespace> --create-namespace --repository-config= --set sync.toHost.service.enabled=true --set controlPlane.distro.k8s.version=<target>`。
- `pkg/adapters/runtime/k8s_cluster_proxy_forwarding_service.go`
  - forwarding wrapper 新增 `UpgradeCluster` 透传，保持 `ports.K8sClusterService` 完整实现。
- `services/ani-gateway/internal/router/k8s_cluster_resources.go`
  - 新增升级 handler 和路由。
- `services/ani-gateway/k8s_proxy_runtime.go`
  - `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 同时接入 provider apply、kubeconfig 和 upgrade。

## 验证

已按 TDD 先添加失败测试，再补实现。

已通过：

```bash
env GOCACHE=/Users/zhangfan/ANI/repo/.cache/go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test -count=1 ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway -run 'TestLocalK8sClusterServiceUpgradesClusterThroughProvider|TestVClusterHelmProviderAdapterRunsHelmUpgradeForClusterVersion|TestK8sClusterAPIUsesInjectedProxyService|TestGatewayK8sClusterServiceFromConfigUsesVClusterHelmProvider'
```

回归验证已通过：

```bash
make gen-core-sdk
make gen-api-docs
make validate-doc-entrypoints
python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml
make validate-doc-api
make validate-mock-a
make validate-sdk-beta
make validate-sdk-mock-smoke
make validate-core-beta
make validate-core-api-compatibility
make validate-real-k8s-profile
make validate-vcluster-live-gate
make validate-sprint4-closure
make validate-architecture
make test
git diff --check
```

## 未完成边界

- 未执行真实 vCluster live 升级。
- 未证明升级后返回 kubeconfig 可继续访问目标租户集群。
- 未证明 live proxy 在升级前后持续可用。
- 未实现或验收节点池管理。
