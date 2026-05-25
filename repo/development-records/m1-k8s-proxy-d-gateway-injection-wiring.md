# M1-K8S-PROXY-D · Gateway K8s Proxy Injection Wiring

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./services/ani-gateway/internal/router -run 'TestK8sClusterAPIUsesInjectedProxyService|TestK8sClusterAPIDevProfileAndIdempotency' -v` EXIT:0

## 实现了什么

本批次移除 Gateway K8s router 对 `NewLocalK8sClusterService()` 的硬编码依赖，新增 `RegisterWithOptions` 和 `newK8sClusterAPIWithService`，允许生产启动代码把已组合好的 `ports.K8sClusterService` 注入到 `/api/v1/k8s-clusters` 路由。

这使 Gateway 能接入前面 `M1-K8S-PROXY-A/B/C` 建立的 forwarding adapter、target resolver/store 和 metadata 持久化 store，而 router 本身仍只依赖 `ports.K8sClusterService`，不直接依赖 Kubernetes SDK 或底层 provider。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/ani-gateway/internal/router/router.go` | 修改 | 新增 `RegisterWithOptions`，暴露 K8s service 注入点 |
| `services/ani-gateway/internal/router/k8s_cluster_resources.go` | 修改 | 新增 `newK8sClusterAPIWithService` 和 `registerK8sClusterResourcesWithService` |
| `services/ani-gateway/internal/router/k8s_cluster_resources_test.go` | 修改 | 覆盖 K8s proxy 调用使用注入服务，同时保留 local profile 默认行为 |

## 完工标准达成

- [x] Gateway K8s router 可注入任意 `ports.K8sClusterService`。
- [x] 默认 `Register` 和 `newK8sClusterAPI` 仍使用 local dev profile，不破坏现有 Mock/API smoke。
- [x] proxy 调用会走注入服务返回的上游响应。
- [x] router targeted Go 测试通过。

## 备注

- 本批次不是 live proxy 验证，也没有把 `services/ani-gateway/main.go` 默认切到 forwarding mode。
- 后续仍需启动配置把 forwarding service + metadata target store 接入 Gateway main，并在 REAL-K8S-LAB-A live 模式下验证真实 vCluster API Server 转发。
