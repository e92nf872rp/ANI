# M1-K8S-PROXY-C · K8s Proxy Target Metadata Store

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/adapters/runtime -run 'TestMetadataK8sClusterProxyTargetStore|TestLocalK8sClusterProxyTargetStore|TestK8sClusterProxyForwardingService' -v` EXIT:0

## 实现了什么

本批次补齐 K8s proxy target 的 metadata/DB 持久化边界。`MetadataK8sClusterProxyTargetStore` 通过 `ports.MetadataStore` 在租户事务内 upsert、resolve 和 delete `tenant_id + cluster_id` 对应的 vCluster/K8s API Server endpoint 与 bearer token，并实现 `K8sClusterProxyTargetStore`。

新增迁移 `20260523_008_k8s_cluster_proxy_targets.sql` 创建 `k8s_cluster_proxy_targets` 表、RLS tenant isolation、`ani_app` 授权和基础索引。

该批次不改变 Core OpenAPI 契约，不暴露 bearer token 到 API 响应，不代表真实 vCluster lifecycle provider 已经写入 target，也不代表 Gateway 默认生产接线或 live proxy 验证已经完成。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/k8s_cluster_proxy_target_metadata_store.go` | 新增 | metadata-backed K8s proxy target store/resolver |
| `pkg/adapters/runtime/k8s_cluster_proxy_target_metadata_store_test.go` | 新增 | 覆盖 upsert、resolve、missing 映射和 delete |
| `pkg/adapters/runtime/plan_audit_store_test.go` | 修改 | 扩展共享 metadata fake 的 QueryRow/Row 扫描能力 |
| `deploy/migrations/20260523_008_k8s_cluster_proxy_targets.sql` | 新增 | 新增 proxy target 持久化表、RLS 和授权 |

## 完工标准达成

- [x] proxy target 可持久化到 `k8s_cluster_proxy_targets`。
- [x] resolve 按 tenant/cluster 查询目标并返回 server/token。
- [x] missing target 映射为 `ports.ErrNotFound`。
- [x] delete 使用 tenant/cluster 条件删除目标。
- [x] targeted Go 测试通过。

## 备注

- bearer token 当前是 metadata-backed bridge，后续生产环境应迁移到 KMS 或 Kubernetes Secret provider 管理敏感材料。
- 后续仍需真实 vCluster lifecycle provider 写入 target、Gateway/bootstrap 生产接线和 REAL-K8S-LAB-A live 模式验证。
