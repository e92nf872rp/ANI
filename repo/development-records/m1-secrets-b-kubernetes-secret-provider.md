# M1-SECRETS-B · Kubernetes Secret Provider Write Boundary

完成日期：2026-05-24
对应 Sprint：Sprint 5（2026-07-16 ~ 07-31）
验证结果：`go test ./pkg/adapters/runtime ./services/ani-gateway ./services/ani-gateway/internal/router` EXIT:0

## 实现了什么

本批次补齐 Secret API 从 local profile 向真实 Kubernetes Secret 写入的第一段 provider 边界。新增 `ports.SecretProviderApply`，`LocalSecretService` 可在创建 Secret 后调用 provider，将明文值写入底层 Kubernetes Secret，同时 API 响应仍只返回 metadata 和 key 名称，不返回明文。

新增 `KubernetesSecretProviderAdapter`，将 `SecretProviderApplyRequest` 渲染为 Kubernetes `Secret` manifest，并通过既有 `KubernetesRESTClient.ApplyManifests` 走 server-side apply。Kubernetes REST resource mapping 新增 `kubernetes/Secret`，路径为 `/api/v1/namespaces/{tenantNamespace}/secrets/{secretName}`。

Gateway 新增 `SECRET_PROVIDER_MODE=kubernetes_rest` runtime 选择。默认仍为 local profile；显式开启时复用 `KUBERNETES_API_HOST`、`KUBERNETES_BEARER_TOKEN` 和 `KUBERNETES_PROVIDER_FIELD_MANAGER` 组合 Kubernetes-backed Secret service，并通过 router `RegisterOptions.SecretService` 注入。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/secrets.go` | 修改 | 新增 Secret provider apply request/result/port，并在 `SecretRecord` 写入 provider evidence |
| `pkg/adapters/runtime/local_secret_service.go` | 修改 | 支持配置 Secret provider apply，provider 成功后记录 real provider evidence |
| `pkg/adapters/runtime/kubernetes_secret_provider.go` | 新增 | Kubernetes Secret provider adapter，渲染并 apply Secret manifest |
| `pkg/adapters/runtime/kubernetes_rest_client.go` | 修改 | 支持 `kubernetes/Secret` resource mapping |
| `services/ani-gateway/secret_runtime.go` | 新增 | Gateway Secret provider runtime 选择 |
| `services/ani-gateway/main.go` | 修改 | 注入 Secret runtime service |
| `services/ani-gateway/internal/router/router.go` | 修改 | `RegisterOptions` 增加 `SecretService` |
| `services/ani-gateway/internal/router/secret_resources.go` | 修改 | 支持注入 Secret service，并在响应中标记 Kubernetes provider evidence |

## 完工标准达成

- [x] Secret 创建可通过 provider port 写入 Kubernetes Secret。
- [x] Kubernetes REST client 支持 Secret server-side apply path。
- [x] Gateway 可通过显式环境变量选择 Kubernetes Secret provider runtime。
- [x] 默认 local profile 行为不变。
- [x] API 响应仍不返回 secret value。
- [x] targeted Go 测试通过。

## 备注

- 本批次完成 Kubernetes Secret 写入的代码边界和 Gateway runtime 选择；尚未用 REAL-K8S-LAB-A live 模式证明真实集群写入成功。
- 本批次不包含实例环境变量/文件挂载注入，Secret binding 仍只记录绑定意图。
- 本批次不包含真实 KMS/SM4 provider；传入 Kubernetes Secret 的值仍来自当前 Secret API request。
