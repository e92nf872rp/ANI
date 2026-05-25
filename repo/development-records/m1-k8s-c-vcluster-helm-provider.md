# M1-K8S-C — vCluster Helm Provider Code Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 K8s cluster provider port、service provider options、vCluster Helm provider adapter、Gateway provider-mode 接线和 real provider dev_profile 映射；GREEN 后 targeted Go tests、`make validate-doc-entrypoints`、`make validate-real-k8s-profile`、`make validate-architecture`、`python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml` 和 `make test` 均通过。

## 实现了什么

为 K8s cluster lifecycle 增加 provider apply 边界：`LocalK8sClusterService` 可在创建集群时调用 vCluster provider，记录 real-provider 证据，并在 provider 返回 proxy target 时注册到 `K8sClusterProxyTargetStore`。新增 `VClusterHelmProviderAdapter`，通过 `helm upgrade --install <cluster_id> vcluster --repo https://charts.loft.sh --namespace <tenant namespace>` 建立 vCluster Helm release 代码边界；Gateway 可通过 `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 选择该 provider。

该批次不是 live vCluster 生命周期验收，不代表 Helm 命令已在 REAL-K8S-LAB-A 环境中执行成功，也不代表 kubeconfig 和 proxy 已通过真实 vCluster API Server 验证。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 `K8sClusterProviderApply` port、provider result 和 cluster provider evidence 字段 |
| `pkg/adapters/runtime/local_k8s_cluster_service.go` | 修改 | 支持 provider apply options、provider evidence 和 proxy target 注册 |
| `pkg/adapters/runtime/vcluster_helm_provider.go` | 新增 | vCluster Helm provider adapter 与命令 runner |
| `pkg/adapters/runtime/local_k8s_cluster_service_test.go` | 新增 | 覆盖 provider apply 和 proxy target 注册 |
| `pkg/adapters/runtime/vcluster_helm_provider_test.go` | 新增 | 覆盖 Helm provider 命令边界 |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | 新增 `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` runtime 选择 |
| `services/ani-gateway/k8s_proxy_runtime_test.go` | 修改 | 覆盖 Gateway provider-mode 接线 |
| `services/ani-gateway/internal/router/k8s_cluster_resources.go` | 修改 | vCluster provider 证据映射到 real dev_profile |
| `services/ani-gateway/internal/router/k8s_cluster_resources_test.go` | 修改 | 覆盖 vCluster real-provider response |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 provider port/options/adapter/Gateway config/response real profile
- [x] K8s cluster 创建路径可调用 vCluster provider 并记录 provider refs
- [x] provider 返回 proxy target 时可注册给 proxy target store
- [x] Gateway 可通过 `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 构造 provider-backed cluster service
- [x] `make test`、`make validate-architecture`、`make validate-doc-entrypoints`、`make validate-real-k8s-profile` 和 OpenAPI YAML 校验通过
- [ ] REAL-K8S-LAB-A live Helm 安装验证
- [ ] 返回可用 vCluster kubeconfig
- [ ] live proxy 访问租户 vCluster API Server 验证

## 备注

`VClusterHelmProviderAdapter` 当前只建立 Helm install/upgrade 执行边界和 provider evidence。后续 live 切片需要在真实 lab 中执行 Helm、读取 vCluster kubeconfig/ServiceAccount token，并把真实 proxy target 固化为可验证记录。
