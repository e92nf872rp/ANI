# INSTANCE-CONSOLE-VNC-WS-A — VM Console/VNC WebSocket 代理（noVNC）

完成日期：2026-07-09
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：`go test ./pkg/adapters/runtime/ ./services/ani-gateway/... -count=1` EXIT:0；`git diff --check` EXIT:0。

> 边界声明：本批次修复 `POST /instances/{id}/console` 对 KubeVirt `/vnc` 的错误普通 HTTP GET 探测，并补齐与 exec 同构的 Gateway WebSocket 代理，供浏览器 noVNC 直连。**local/logic verified**；不宣称 production-shaped live gate 已通过，不标 full platform production ready。未改 Services / Console 前端实现（`frontends/` 冻结）。

## 背景

创建 console session 时，`KubernetesInstanceOps` 对 KubeVirt subresource `/vnc` / `/console` 发起普通 HTTP GET。KubeVirt 只接受 WebSocket Upgrade（`plain.kubevirt.io`），因此返回：

```text
400 Bad Request
websocket: the client is not using the websocket protocol:
'upgrade' token not found in 'Connection' header
```

前端 noVNC 需要的是可鉴权的浏览器 WebSocket URL，而不是集群内 KubeVirt subresource 地址。

## 实现了什么

1. **OpenAPI**
   - `InstanceConsoleSession` 增加可选 `token`
   - 新增 `GET /instances/{instance_id}/console/{session_id}`（WebSocket 握手入口）
   - 明确 `connect_url` 为浏览器可直连、内嵌短期 token 的 Gateway WS URL
2. **ports**
   - `WorkloadInstanceOpsResult.Token`
   - `WorkloadInstanceConsoleSession` / `GetConsoleSession` / `ConnectConsoleSession`
3. **adapter**
   - 创建 session 时只 GET VMI 校验 Running，不再探测 `/vnc`
   - 签发短期 token + `connect_url`
   - `ConnectConsoleSession` 以 `plain.kubevirt.io` 连接 KubeVirt，并与浏览器 WebSocket 字节透传
4. **Gateway**
   - 注册 `GET /instances/:instance_id/console/:session_id`
   - console 响应对齐 `session_id/protocol/connect_url/url/token/expires_at`
   - `INSTANCE_CONSOLE_BASE_URL`（可回退 `INSTANCE_OBSERVABILITY_EXEC_BASE_URL`）用于生成 `connect_url`

## 前端 noVNC 用法

```ts
const session = await POST('/api/v1/instances/{id}/console', { protocol: 'novnc' })
const rfb = new RFB(container, session.connect_url)
// connect_url 已含 token；不要再拼 KubeVirt URL，不要带集群凭据
```

## 关键文件

| 文件 | 说明 |
|---|---|
| `api/openapi/v1.yaml` | console session schema + WS connect path |
| `pkg/ports/workload_runtime.go` | console session ports |
| `pkg/adapters/runtime/kubernetes_instance_ops.go` | 修复探测；签发 session |
| `pkg/adapters/runtime/kubernetes_console_stream.go` | KubeVirt VNC/console WS 代理 |
| `pkg/adapters/runtime/instance_ops.go` | local profile 同步签发 token/connect_url |
| `services/ani-gateway/workload_runtime.go` | console base URL 注入 |
| `services/ani-gateway/internal/router/demo_instances.go` | console create/connect handler |

## 验收命令

```bash
cd repo
go test ./pkg/adapters/runtime/ ./services/ani-gateway/... -count=1
git diff --check
```

## 生产边界

- 未新增 production-shaped live gate / evidence JSON
- 真实 VNC 仍依赖 Running VMI + 可拉取 boot image + Gateway 可达 `INSTANCE_*_BASE_URL`
- Console 前端 noVNC 组件接线属于 Services/前端团队范围，本批次只提供 Core 契约与代理
