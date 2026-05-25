# M1-K8S-PROXY-A · K8s Proxy Forwarding Adapter

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/adapters/runtime -run TestK8sClusterProxyForwardingService -v` EXIT:0

## 实现了什么

本批次补齐 K8s cluster proxy 的 runtime forwarding adapter 边界。`K8sClusterProxyForwardingService` 复用现有 `K8sClusterService` 契约，通过 resolver 获取目标 vCluster/K8s API Server endpoint 和 bearer token，并将 Core proxy 请求的 method、path、query、JSON body 和 Authorization 转发给上游 API Server。

该批次不改变 Core OpenAPI 契约，不改变 Gateway 默认 local profile，也不代表真实 vCluster 生命周期、默认生产接线或 live lab 验证已经完成。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/k8s_clusters.go` | 修改 | 新增 `K8sClusterProxyTarget` 和 `K8sClusterProxyTargetResolver` |
| `pkg/adapters/runtime/k8s_cluster_proxy_forwarding_service.go` | 新增 | 新增可注入 resolver/HTTP client/clock 的 proxy forwarding service |
| `pkg/adapters/runtime/k8s_cluster_proxy_forwarding_service_test.go` | 新增 | 验证 method/path/query/body/Authorization 转发、响应映射和 target identity guard |

## 完工标准达成

- [x] Core proxy request 可通过 runtime adapter 转发到 resolver 给出的上游 K8s/vCluster API Server。
- [x] 转发保留 method、normalized path、query、JSON body 和 bearer token。
- [x] 响应 status、headers、JSON body 映射回 `K8sClusterProxyRecord`。
- [x] resolver 返回的 tenant/cluster identity 不匹配时拒绝转发。
- [x] targeted Go 测试通过。

## 备注

- 后续仍需真实 vCluster 生命周期 provider、per-cluster resolver 持久化、Gateway/bootstrap 生产接线和 REAL-K8S-LAB-A live 模式验证。
- 默认 Gateway 仍返回 local dev profile proxy 响应，避免在没有真实 vCluster endpoint 时误标记 real-provider ready。
