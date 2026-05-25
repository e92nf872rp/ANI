# M1-K8S-PROXY-F · Gateway Metadata-backed K8s Proxy Runtime

完成日期：2026-05-23
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./services/ani-gateway -run TestGatewayK8sCluster -v` EXIT:0；`go test ./pkg/bootstrap -run 'TestConnectMetadataStoreRejectsInvalidDatabaseURL|TestNewCapabilities' -v` EXIT:0

## 实现了什么

本批次补齐 Gateway 进程启动时的 per-cluster metadata resolver 接线能力。显式设置 `K8S_CLUSTER_PROXY_MODE=forwarding_metadata` 时，Gateway 会通过 `DATABASE_URL` 初始化 `ports.MetadataStore`，再组合：

- `runtime.NewLocalK8sClusterService()` 作为当前 cluster lifecycle local profile；
- `runtime.NewMetadataK8sClusterProxyTargetStore()` 作为 tenant/cluster 维度的 proxy target resolver；
- `runtime.NewK8sClusterProxyForwardingService()` 作为 proxy 转发层。

该模式让 Gateway 可以按请求中的 tenant/cluster 从 metadata 表解析目标 vCluster/K8s API Server endpoint 和 bearer token，再转发 `/proxy` 请求；Gateway 仍不直接 import PostgreSQL/pgx/Kubernetes SDK，组件连接由 `pkg/bootstrap.ConnectMetadataStore` 承担。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/bootstrap/db.go` | 修改 | 新增 `ConnectMetadataStore`，从 `DATABASE_URL` 初始化 metadata store 并返回 close 函数 |
| `pkg/bootstrap/deps_test.go` | 修改 | 覆盖 metadata store helper 对非法 database URL fail closed |
| `services/ani-gateway/k8s_proxy_runtime.go` | 修改 | 新增 `forwarding_metadata` runtime 模式和 metadata connector |
| `services/ani-gateway/k8s_proxy_runtime_test.go` | 修改 | 覆盖 metadata resolver 会驱动上游转发，并验证 connector close |
| `services/ani-gateway/main.go` | 修改 | 启动时调用 runtime 构造层并在进程退出时关闭 metadata runtime |

## 完工标准达成

- [x] 默认/空模式仍保持 local router 行为。
- [x] `forwarding_static` 模式保持兼容。
- [x] `forwarding_metadata` 缺少 metadata store 或 `DATABASE_URL` 时 fail closed。
- [x] `forwarding_metadata` 可通过 metadata store 解析 tenant/cluster proxy target。
- [x] Gateway main 可通过 `K8S_CLUSTER_PROXY_MODE=forwarding_metadata` 和 `DATABASE_URL` 接入 metadata-backed resolver。
- [x] Gateway 不直接依赖 pgx/postgres 或 Kubernetes SDK。
- [x] targeted Go 测试通过。

## 备注

- 本批次不是完整 vCluster lifecycle provider；cluster create/get/list/delete 仍使用 local profile。
- 本批次不是 REAL-K8S-LAB-A live proxy 验证；真实 vCluster API Server 可达性仍需 live 门禁证明。
- 本批次不改变 bearer token 管理边界；后续仍需把 token 来源接入 KMS/Secret 管理链路。
