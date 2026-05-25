# M1-K8S-PROXY-E · Gateway K8s Proxy Runtime Selection

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./services/ani-gateway -run TestGatewayK8sClusterServiceFromConfigUsesStaticForwardingTarget -v` EXIT:0

## 实现了什么

本批次补齐 Gateway 进程启动时的 K8s proxy runtime 选择能力。`services/ani-gateway/main.go` 不再只调用默认 local router 注册，而是通过 `newGatewayK8sClusterService` 读取配置后调用 `router.RegisterWithOptions`。

默认配置仍保持 local dev profile；显式设置 `K8S_CLUSTER_PROXY_MODE=forwarding_static` 时，Gateway 会组合：

- `runtime.NewLocalK8sClusterService()` 作为当前 cluster lifecycle local profile；
- `runtime.NewK8sClusterProxyForwardingService()` 作为 proxy 转发层；
- 静态 target resolver，从 `K8S_CLUSTER_PROXY_TARGET_SERVER` 和 `K8S_CLUSTER_PROXY_BEARER_TOKEN` 读取上游 vCluster/K8s API Server。

该模式用于 REAL-K8S-LAB-A live 前的最小 Gateway 转发烟测，不引入 Gateway handler 对 Kubernetes SDK 的直接依赖。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `services/ani-gateway/k8s_proxy_runtime.go` | 新增 | Gateway K8s proxy runtime 配置和 static forwarding resolver |
| `services/ani-gateway/k8s_proxy_runtime_test.go` | 新增 | 覆盖 `forwarding_static` 会转发到配置的上游并带 bearer token |
| `services/ani-gateway/main.go` | 修改 | 启动时根据配置注入 K8s cluster service 到 router |

## 完工标准达成

- [x] 默认/空模式仍保持 local router 行为。
- [x] `forwarding_static` 模式要求 `K8S_CLUSTER_PROXY_TARGET_SERVER`，缺失时 fail closed。
- [x] `forwarding_static` 模式将 `/proxy` 请求转发到配置的上游 API Server。
- [x] forwarding 请求携带配置的 bearer token。
- [x] targeted Go 测试通过。

## 备注

- 本批次不是完整 vCluster lifecycle provider；cluster create/get/list/delete 仍使用 local profile。
- 本批次不是 per-cluster metadata resolver 的 Gateway 默认接线；metadata store 已在 `M1-K8S-PROXY-C` 完成，后续需结合 Gateway/DB 初始化方式再接入。
- 本批次不是 REAL-K8S-LAB-A live 验证；真实 vCluster API Server 可达性仍需 live 门禁证明。
