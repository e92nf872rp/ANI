# M1-K8S-F · K8s Node Pool Local Profile

> 日期：2026-05-24
> 范围：Sprint 5 / K8s 集群管理
> 状态：API/local profile 代码边界已完成；真实 provider/live 节点池扩缩容未完成

## 目标

为 K8s 集群补齐节点池管理的最小 Core API 与 local profile 闭环：

- `POST /api/v1/k8s-clusters/{cluster_id}/node-pools`
- `GET /api/v1/k8s-clusters/{cluster_id}/node-pools`
- `GET /api/v1/k8s-clusters/{cluster_id}/node-pools/{node_pool_id}`
- `PATCH /api/v1/k8s-clusters/{cluster_id}/node-pools/{node_pool_id}`
- `DELETE /api/v1/k8s-clusters/{cluster_id}/node-pools/{node_pool_id}`

该批次证明 API 契约、ports、local runtime service、Gateway router 和 SDK/docs 生成闭环；不证明真实 vCluster/云厂商节点池已扩缩容。

## 实现摘要

- `api/openapi/v1.yaml`
  - 新增 `K8sClusterNodePoolGPU`、create/update request、node pool record 和 list response schema。
  - 新增 cluster-scoped node pool CRUD paths，创建和更新请求均要求 `idempotency_key`。
- `pkg/ports/k8s_clusters.go`
  - 新增 `K8sClusterNodePool*` request/record/state 类型。
  - `K8sClusterService` 新增 create/get/list/update/delete node pool 方法。
- `pkg/adapters/runtime/local_k8s_cluster_service.go`
  - 新增 tenant/cluster-scoped node pool local profile。
  - create/update 支持幂等；delete 标记为 `deleting`。
  - 保留 GPU intent 字段：vendor/model/count/resource_name。
- `pkg/adapters/runtime/k8s_cluster_proxy_forwarding_service.go`
  - forwarding wrapper 透传 node pool 方法，保持完整 `ports.K8sClusterService` 实现。
- `services/ani-gateway/internal/router/k8s_cluster_resources.go`
  - 新增 node pool router request/response、handlers 和 routes。
- `sdks/core/*` 与 `docs/api/*`
  - 由 `make gen-core-sdk` 和 `make gen-api-docs` 从 OpenAPI 重新生成。

## 验证

已按 TDD 先添加失败测试，再补实现。

已通过：

```bash
env GOCACHE=/Users/zhangfan/ANI/repo/.cache/go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test -count=1 ./pkg/adapters/runtime ./services/ani-gateway/internal/router -run 'TestLocalK8sClusterServiceManagesNodePools|TestK8sClusterAPIDevProfileAndIdempotency|TestK8sClusterAPIUsesInjectedProxyService'
env GOCACHE=/Users/zhangfan/ANI/repo/.cache/go-build GOMODCACHE=/Users/zhangfan/ANI/repo/.cache/gomod go test -count=1 ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway
```

完整回归验证见本批次收尾输出。

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
make validate-architecture
make validate-sprint4-closure
make test
git diff --check
```

## 未完成边界

- 未实现真实 provider 节点池 apply/update/delete。
- 未执行 live 节点池扩缩容验证。
- 未证明 GPU 节点池在真实 K8s/KubeVirt/vCluster/底层集群中可调度。
