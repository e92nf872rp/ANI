# M1-K8S-D — vCluster Kubeconfig Provider Code Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：TDD RED 已确认缺少 K8s cluster kubeconfig provider port、service kubeconfig provider option、vCluster CLI kubeconfig adapter 方法和 Gateway provider-mode kubeconfig 接线；GREEN 后 targeted Go tests、affected package tests、`make validate-doc-entrypoints`、`make validate-real-k8s-profile`、`make validate-architecture`、`python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml`、`make test` 和 `git diff --check` 均通过。

## 实现了什么

为 K8s cluster kubeconfig 增加 provider 读取边界：`LocalK8sClusterService` 对 real provider cluster 的 `GetKubeconfig` 可委托 `ports.K8sClusterKubeconfigProvider`，并保留 local profile cluster 的原有模拟 kubeconfig 行为。

`VClusterHelmProviderAdapter` 现在同时实现 Helm apply 和 kubeconfig provider。kubeconfig 路径通过 `vcluster connect <cluster_id> --namespace <tenant namespace> --print` 获取 kubeconfig，并支持通过 `KubeconfigServerTemplate` 或 `ProxyServerTemplate` 注入 `--server`。Gateway 在 `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 下会把同一个 adapter 同时接入 cluster apply 和 kubeconfig provider。

该批次不是 live kubeconfig 可用性验收，不代表 `vcluster connect` 已在 REAL-K8S-LAB-A 环境中成功执行，也不代表返回的 kubeconfig 已通过 `kubectl --kubeconfig` 验证。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 `K8sClusterKubeconfigProvider` port 和 request |
| `pkg/adapters/runtime/local_k8s_cluster_service.go` | 修改 | real provider cluster 的 kubeconfig 可委托 provider；local profile 行为保持不变 |
| `pkg/adapters/runtime/vcluster_helm_provider.go` | 修改 | 新增 `vcluster connect --print` kubeconfig provider 方法、server template 和 token 解析 |
| `pkg/adapters/runtime/local_k8s_cluster_service_test.go` | 修改 | 覆盖 provider-backed kubeconfig 委托和 identity 回填 |
| `pkg/adapters/runtime/vcluster_helm_provider_test.go` | 修改 | 覆盖 vCluster CLI kubeconfig 命令边界 |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | `vcluster_helm` provider mode 同时接入 apply 和 kubeconfig provider，并新增 `VCLUSTER_BINARY` / `VCLUSTER_KUBECONFIG_SERVER_TEMPLATE` |
| `services/ani-gateway/k8s_proxy_runtime_test.go` | 修改 | 覆盖 Gateway provider mode 下 kubeconfig 走 vCluster provider |

## 完工标准达成

- [x] 先写失败测试并确认 RED：缺少 kubeconfig provider port/options/adapter/Gateway 接线
- [x] real provider cluster 的 `GetKubeconfig` 可委托 kubeconfig provider
- [x] local profile cluster 的模拟 kubeconfig 行为保持不变
- [x] vCluster adapter 可通过 `vcluster connect --print` 获取 kubeconfig
- [x] Gateway `K8S_CLUSTER_PROVIDER_MODE=vcluster_helm` 同时接入 Helm apply 和 kubeconfig provider
- [ ] REAL-K8S-LAB-A live `vcluster connect` 执行验证
- [ ] 返回 kubeconfig 的真实 `kubectl --kubeconfig` 可用性验证
- [ ] live proxy 访问租户 vCluster API Server 验证

## 备注

本批次只把 kubeconfig 读取从 local profile 模拟推进到 provider adapter 代码边界。下一步仍需要真实 lab 环境执行 Helm install、`vcluster connect --print`、`kubectl --kubeconfig` 和 proxy live 验证，才能把 K8s cluster 主链路标为 real provider 可用。
