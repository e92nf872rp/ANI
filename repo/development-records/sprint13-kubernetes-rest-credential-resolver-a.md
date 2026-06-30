# SPRINT13-KUBERNETES-REST-CREDENTIAL-RESOLVER-A — Kubernetes REST 凭证自动解析（本地 kubeconfig + in-cluster）

完成日期：2026-06-26
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：`go test ./pkg/adapters/runtime/... ./pkg/bootstrap/...`、`go test ./services/ani-gateway/...`、`python3 scripts/validate_component_imports.py --root .`、`git diff --check` 均为 EXIT 0

> 边界声明：本批次只改进 **Kubernetes REST client 凭证解析与 Gateway env 收敛**，不改 Core OpenAPI/SDK，不新增 live gate，不标 real-provider runtime ready 或 production ready。生产部署仍应使用显式 `KUBERNETES_*` env；kubeconfig 自动加载仅供本地开发便利。

## 背景

`SPRINT13-B-TRACK-PRODUCTION-SHAPED-POST-REVIEW` 已统一 in-cluster ServiceAccount 显式 env 透传，但本地开发仍需手工设置 `KUBERNETES_API_HOST` + `KUBERNETES_BEARER_TOKEN`（或 kubectl proxy）。Gateway 6 条 runtime 与 bootstrap 重复读取同一组 `KUBERNETES_*` env。

## 实现了什么

1. **凭证解析链**（`ResolveKubernetesRESTClientConfig`，在 `NewKubernetesRESTClient` 入口调用）：
   - 优先级：显式 config/env > `KUBECONFIG` / `~/.kube/config`（`client-go/clientcmd`）> in-cluster ServiceAccount 自动探测
   - 关闭自动解析：`KUBERNETES_CONFIG_AUTO_LOAD=false`
2. **统一 env 加载**：`LoadKubernetesRESTEnvFromOS()`（含 `KUBERNETES_REQUEST_TIMEOUT`）
3. **Gateway 收敛**：`kubernetes_runtime.go` 提供 `gatewayKubernetesRESTClientConfig` / `newGatewayKubernetesRESTClient`；`secret` / `storage` / `network` / `gpu` / `k8s_proxy` / `instance_observability` runtime 不再各自重复 7 行 `os.Getenv`
4. **依赖**：`pkg/go.mod` 新增 `k8s.io/client-go v0.30.3`，仅用于 kubeconfig 读取；HTTP 仍走既有手写 `KubernetesRESTClient`

## 关键文件

| 文件 | 说明 |
|---|---|
| `pkg/adapters/runtime/kubernetes_rest_config_resolve.go` | 凭证解析链 + kubeconfig/in-cluster loader |
| `pkg/adapters/runtime/kubernetes_rest_config_resolve_test.go` | explicit / kubeconfig / in-cluster / auto-load off 单测 |
| `pkg/adapters/runtime/kubernetes_env.go` | 集中 `KUBERNETES_*` env 读取 |
| `pkg/adapters/runtime/kubernetes_rest_client.go` | `NewKubernetesRESTClient` 入口调用 resolver |
| `services/ani-gateway/kubernetes_runtime.go` | Gateway 统一 K8s client 配置 helper |
| `pkg/bootstrap/deps.go` | `kubernetesRESTClientConfig` 补充 `RequestTimeout` |
| `.env.example`、`deploy/docker/README.md` | 本地连集群说明 |

## 本地开发用法

```bash
export SECRET_PROVIDER_MODE=kubernetes_rest   # 或其他 kubernetes_rest provider mode
# 可选：export KUBECONFIG=$HOME/.kube/config
```

显式 env（`KUBERNETES_API_HOST` 等）仍覆盖 kubeconfig，与 production-shaped 部署兼容。

## 验收命令

```bash
cd repo
go test ./pkg/adapters/runtime/... ./pkg/bootstrap/...
go test ./services/ani-gateway/...
python3 scripts/validate_component_imports.py --root .
git diff --check
```

## 生产边界

- 不替代 `validate-sprint13-b-track-production-shape` 对 lab kubeconfig / dev gateway evidence 的审查
- 不把 kubeconfig 自动加载标为 production ready
- 未新增 live gate；不改变既有 production-shaped passed 结论
