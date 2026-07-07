# INSTANCE-EXEC-WS-A — Instance exec WebSocket contract and gateway path

完成日期：2026-07-07
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：local/logic verified

> 边界声明：本批次只补 ANI Core `/api/v1/instances/{instance_id}/exec` 的浏览器 WebSocket 接入契约、Gateway handler、observability port/adapters 与测试；不修改 Console 前端，不新增 Services 路径，不放宽 `scope:instances:exec` 鉴权。

## 背景

Console 交互式终端使用浏览器原生 `WebSocket`，无法在握手时自定义 `Authorization` header。原 Core 只实现了 `POST /instances/{instance_id}/exec` 返回 `ws_url`，但没有注册对应的 WebSocket GET 入口，也没有一次性握手 token，导致前端拿到 URL 后直连被权限边界拒绝或无法路由。

## 实现了什么

1. **OpenAPI 契约**
   - `InstanceExecSession.ws_url` 明确为浏览器可直连 URL，必须包含短期 `token` query。
   - `InstanceExecSession.token` 明确为短期一次性 WebSocket 握手票据，与 `ws_url` query 中的 token 相同。
   - 新增 `GET /api/v1/instances/{instance_id}/exec/{session_id}`，operationId `connectInstanceExecSession`，RBAC `scope:instances:exec`。
   - WebSocket 帧协议固定为普通 text/binary 帧原始 stdin/stdout 透传；resize 使用 `{"type":"resize","cols":120,"rows":30}` 控制帧。
2. **Gateway / RBAC**
   - 注册 `GET /instances/:instance_id/exec/:session_id`。
   - `inferPermission` 将 `POST /instances/{id}/exec` 与 `GET /instances/{id}/exec/{session}` 映射到 `instances:exec`，不再误判为 `instances:create/get`。
   - GET 握手只接受 query token；缺失/错误 token 返回 401，过期返回 410，非 Upgrade 请求返回 400。
3. **ports/adapters**
   - `InstanceObservability` 新增 `GetExecSession`，用于按 tenant、instance、session、token 校验短期 session。
   - local 与 prometheus observability adapter 生成 15 分钟 TTL token，并把 token 内嵌到 `ws_url`。
4. **本地 WebSocket 数据面**
   - Gateway 使用 Hertz hijack 完成标准 WebSocket 101 握手。
   - 普通 text/binary 帧写入本地 shell stdin，stdout/stderr 以 binary 帧回传。
   - resize JSON 控制帧被识别并消费；当前 local profile 不声明真实 PTY resize。

## 单测覆盖

- RBAC exec 路径映射到 `instances:exec`。
- local/prometheus exec session 幂等创建、15 分钟 TTL、短期 token、`ws_url` 内嵌 token。
- `GetExecSession` 按 token 校验 session。
- Gateway WebSocket GET 缺 token 返回 401。

## 验证命令

```bash
python3 scripts/validate_yaml.py api/openapi/v1.yaml
python3 scripts/validate_component_imports.py --root .
GOCACHE=/tmp/ani-go-build-cache go test ./internal/middleware ./internal/router
GOCACHE=/tmp/ani-go-build-cache go test ./adapters/runtime -run 'Test(LocalInstanceObservabilityExecSessionIsIdempotentAndShortLived|PrometheusInstanceObservabilityCreatesIdempotentShortLivedExecSession)'
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make test GO_CACHE_ENV='GOCACHE=/tmp/ani-go-build-cache GOMODCACHE=/root/kubercon/ANI/repo/.cache/gomod'
git diff --check
```

结果：上述命令均通过；全量 `make test` 因 sandbox 禁止 `httptest` 监听端口需在提权环境执行，提权后通过。

## 已知边界

- 当前 Gateway 数据面实现为 local profile shell 透传；Prometheus/K8s observability adapter 已具备同一 session/token 契约，但真实 Kubernetes Pod exec streaming 仍需后续在 adapter 边界补 SPDY/WebSocket stream。
- local profile 使用 stdin/stdout 管道，不声明完整 PTY 行为或真实 resize 生效。
