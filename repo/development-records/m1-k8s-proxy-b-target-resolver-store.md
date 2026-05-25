# M1-K8S-PROXY-B · K8s Proxy Target Resolver Store

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/adapters/runtime -run 'TestLocalK8sClusterProxyTargetStore|TestK8sClusterProxyForwardingService' -v` EXIT:0

## 实现了什么

本批次补齐 K8s proxy forwarding adapter 的 per-cluster target resolver/store 边界。`LocalK8sClusterProxyTargetStore` 可按 `tenant_id + cluster_id` 注册、解析和删除目标 vCluster/K8s API Server endpoint 与 bearer token，并实现 `K8sClusterProxyTargetResolver`，供 `K8sClusterProxyForwardingService` 使用。

该批次不改变 Core OpenAPI 契约，不暴露 bearer token 到 API 响应，不代表 Gateway 默认生产接线或 live vCluster proxy 验证已经完成。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 `K8sClusterProxyTargetStore` 接口 |
| `pkg/adapters/runtime/k8s_cluster_proxy_target_store.go` | 新增 | 新增本地 per-cluster proxy target store/resolver |
| `pkg/adapters/runtime/k8s_cluster_proxy_target_store_test.go` | 新增 | 覆盖注册、解析、租户隔离、删除和 target 校验 |

## 完工标准达成

- [x] proxy target 可按 tenant/cluster 注册并解析。
- [x] 同 cluster id 不同 tenant 不会串读目标。
- [x] 删除 target 后解析返回 `ErrNotFound`。
- [x] 缺失 server 或非法 server 会拒绝注册。
- [x] targeted Go 测试通过。

## 备注

- 后续仍需 metadata/DB 持久化、真实 vCluster lifecycle provider 写入 target、Gateway/bootstrap 生产接线和 REAL-K8S-LAB-A live 模式验证。
- bearer token 当前只保存在 runtime adapter 边界，不进入 OpenAPI 响应。
