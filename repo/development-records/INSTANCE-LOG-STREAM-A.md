# INSTANCE-LOG-STREAM-A — 实例日志 SSE 流式输出（首屏回放 + 增量轮询）

完成日期：2026-09-01
对应 Sprint：Sprint 13（并行功能流：实例详情可观测性增强）
分支：`feat/instance-log-stream`（基于 origin/main）
方案依据：`kjs-study/实例详情相关文档/实例日志流式输出方案设计文档.md`（已确认的 query_range 轮询方案，实施阶段未翻案改用 tail WebSocket）
验证结果：定向 go test（adapter StreamLogs 三场景 + gateway stream handler）通过；`make validate-architecture`、`make validate-openapi-spec`（15 spec 测试 + 双 yaml 校验）、`validate_gateway_authz_drift`（no drift）、`make test-python`、`git diff --check` 全过。`make test` 中 `pkg/adapters/runtime` 的 `TestSandboxFileScriptsRejectSymlinks` / `TestSandboxFileScriptsAllowWorkspaceOperations` 因 Windows symlink 特权与 `os.O_DIRECTORY` 缺失失败，为 pristine 环境预存问题（与 TASKCENTER-C1/A1 记录一致），非本批引入。真实环境 curl 实测见下文。

## 实现了什么

Core 新增 `GET /api/v1/instances/{instance_id}/logs/stream` SSE 流式日志端点：连接建立后先以 Loki `query_range`（direction=backward）回放最近 `limit` 条历史日志（反转后按时间正序推送），再以同一游标（lastTS）进入 forward 增量轮询（默认 2s 间隔），持续推送新日志；增量结果按时间排序并按游标去重，保证无重复无乱序。日志持续从 Loki 读取（`INSTANCE_OBSERVABILITY_LOG_STORE=loki` profile），多租户隔离复用既有 `buildLokiLogQL` 的 namespace + pod 语义。连接时长上限 10 分钟（发 `done{reason:"timeout"}`）；客户端断开（ctx 取消）或 sink 写出失败立即退出；非 loki profile 降级 503 `LOG_STREAM_NOT_CONFIGURED`；预流错误（401/404/400）返回普通 JSON 不进入 SSE 流。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 `GET /instances/{instance_id}/logs/stream`（operationId=streamInstanceLogs，query 参数 level/limit/interval_seconds，`text/event-stream` 200 + 预流 400/401/403/404 + 503 降级声明，`x-ani-rbac-scope: scope:instances:read`，`x-ani-authz`：resource=instances / action=read / boundary=tenant / principal_kinds=[user, api_key]，显式 `security: BearerAuth ∥ ApiKeyAuth`） |
| `pkg/ports/instance_observability.go` | 修改 | 新增 `InstanceLogStreamRequest`（TenantID/InstanceID/Level/Limit/IntervalSeconds）与 `InstanceObservability.StreamLogs(ctx, request, sink)` 接口方法 |
| `pkg/adapters/runtime/loki_log_stream.go` | 新增 | Loki 流式实现：backward 回放 → lastTS 游标 → forward 轮询；复用 `buildLokiLogQL`/`parseLokiLogLine`/level 过滤；forward 结果排序去重；轮询失败下一周期自愈 |
| `pkg/adapters/runtime/loki_log_stream_test.go` | 新增 | 单测：回放→增量游标衔接、forward 去重、sink 断开立即退出 |
| `pkg/adapters/runtime/prometheus_instance_observability.go` | 修改 | `StreamLogs` 委托注入的 `*LokiLogStore`；logStore 缺失或类型不符返回 `ErrNotConfigured`（gateway 映射 503） |
| `pkg/adapters/runtime/local_instance_observability_service.go` | 修改 | local profile `StreamLogs` 返回 `ErrNotConfigured`（503 降级） |
| `services/ani-gateway/internal/router/instance_log_stream.go` | 新增 | SSE handler：预流校验（复用 `instanceForObservation`）→ `c.Hijack` 手写响应头逐帧写 + Flush（不沿用 kb_sse 缓冲式 `c.Write`）→ 10 分钟上限 `done{reason:"timeout"}` → sink 写出失败即取消 ctx |
| `services/ani-gateway/internal/router/instance_log_stream_test.go` | 新增 | handler 测试：非 loki profile 降级 503 JSON、SSE 端到端帧序列（log/done）、预流 404 无 SSE 帧 |
| `services/ani-gateway/internal/router/instances.go` | 修改 | 路由注册 `v1.GET("/instances/:instance_id/logs/stream", api.streamInstanceLogs)` |
| `services/ani-gateway/internal/router/instances_test.go` | 修改 | `metricsKindSpy` 补 `StreamLogs` 空实现（接口新增方法的测试桩） |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改 | 生成物（`make gen-gateway-authz`）：新 operation 的 generated policy |
| `sdks/core/{go,java,python,typescript}` | 修改 | 生成物（`make gen-core-sdk`）：streamInstanceLogs 客户端方法 |
| `docs/api/{core,index}.html` | 修改 | 生成物（`make gen-api-docs`）：静态 API 文档同步 |
| `frontends/console/src/api/core-schema.d.ts` | 修改 | 生成物（gen-core-schema.mjs）：Console Core API 类型同步 |

## 真实环境实测（curl，2026-09-01）

环境：本地 lab Gateway 进程（`ANI_AUTH_MODE=dev` + 真实 PG `ani_app_user` + Loki/Redis SSH 隧道 + Prometheus NodePort + `WORKLOAD_PROVIDER=kubernetes_rest`（apply 关闭，只读）+ `INSTANCE_OBSERVABILITY_LOG_STORE=loki`），未触碰 in-cluster `ani-gateway`。

- **路由注册**：Hertz 启动 dump 显示 `GET /api/v1/instances/:instance_id/logs/stream → streamInstanceLogs`。
- **首屏回放**：对 GPU 实例 `inst_35c3cf9f…`（name=test-gpu-inst-create）`limit=10` 请求，200 + `Content-Type: text/event-stream`，回放 Loki 中该实例 2 条历史日志（时间正序），随后保持连接（增量轮询挂起），curl `--max-time` 断开后 handler 立即退出（无 goroutine 泄漏，gateway 日志无异常）。
- **增量跟随**：对 nginx 实例 `inst_8457a05c…` 起 SSE（limit=3, interval_seconds=2）后，经 port-forward 向 nginx 发起真实 HTTP 请求（fluent-bit → Loki 采集延迟约 20s 内），`probe-incremental-final` 的 access log + error log 两条新日志以 `event: log` 帧增量到达，时间正序、无重复；首屏 3 条回放与增量帧无重叠。
- **预流错误**：不存在的实例 ID 返回普通 JSON 404 `INSTANCE_NOT_FOUND`（无 SSE 帧）；参数契约校验（level 非 enum、limit 越界/非整数、interval_seconds 越界 → 400）发生在实例存在性校验之后（实例不存在时 404 优先），由 handler 测试 `TestStreamInstanceLogs_QueryParamValidation` 覆盖。
- **done 事件**：`done{reason:"timeout"}`（10 分钟上限）由 handler 单测覆盖帧序列；真实等待 10 分钟不实际执行（按执行方案允许的偏差记录方式处理）。客户端断开路径经 curl `--max-time` 短连接实测。

## 真实环境 V2 鉴权链路实测（评审后补充，2026-09-02）

初版实测用 `ANI_AUTH_MODE=dev` 旁路认证（评审指出这无法暴露 generated policy 的鉴权问题），补充以本地 auth-service + gateway（`ANI_AUTH_MODE=auth_service`，`AUTH_SERVICE_ADDR=127.0.0.1:9101`）连真实集群 DB/Redis 重测：

- **租户 JWT（正例）**：`tenant-a/admin` 登录取 token，GET stream → **200** + `text/event-stream`，V2 链路（`ValidatePrincipal` + `CheckPermissionV2`）放行 instances/read/tenant，SSE 帧正常回放。
- **无凭证**：**401** `UNAUTHORIZED`；**无效 token**：**401**。
- **platform 身份访问 tenant 边界（负例）**：root 平台 token → **403** `FORBIDDEN`（boundary/domain 校验 deny 映射）。
- **API Key（正例）**：租户新建 `scope:instances:read` 的 API Key，`X-API-Key` 请求 → **200** + SSE 帧正常（api_key 主体路径放行）；**过期 API Key** → 401。
- generated policy 的 user/api_key 主体路径与边界拒绝均取得真实链路证据；DB 连接沿用 `ani_app_user`（`ani` 用户 NodePort 28P01 为已知存量问题，见 `RLS问题-platform-bypass-policy空字符串污染.md`）。

## 实测暴露的环境事实（非代码缺陷，记录备查）

1. `kubectl exec echo` 的输出不进入容器 stdout 日志文件，无法作为增量测试的日志源；增量验证改用 nginx 真实 HTTP 访问日志。
2. GPU 实例 pod（dev-phys-02）当前镜像静默且 fluent-bit 对新 pod 的采集仅在有新日志行时体现，测试期间该 pod 在 Loki 无新增数据。

## 验收命令与结果

| 命令 | 结果 |
|---|---|
| `go test ./pkg/adapters/runtime/ -run StreamLogs` | ok |
| `go test ./services/ani-gateway/internal/router/ -run Stream` | ok |
| `make validate-architecture` | ✅ |
| `make validate-openapi-spec` | ✅（15 tests OK + 双 yaml valid） |
| `python scripts/validate_gateway_authz_drift.py` | no drift |
| `make validate-gateway-authz` | ✅（生成器 18 tests / no drift / route coverage 285-235-0 / authz go test） |
| `make test-python` | ✅ |
| `git diff --check` | ✅ |
| `make test`（test-go） | 仅 Windows 预存 sandbox symlink 环境失败（与 main 基线一致，非本批引入） |

## 边界声明

- 本批为 Core 单功能批次，不含前端接入（方案 §3.5）、不含 K8s profile 流式实现、不引入 tail WebSocket。
- 真实环境实测基于 lab 本地进程 + 真实 Loki/PG/Prometheus，不等于 in-cluster Gateway 滚动验证，不得标记 runtime ready 或 production ready。
- 未改 `GET /instances/{id}/logs` 既有列表接口（Sprint 13 契约不破坏）；Core API v1 变更为 additive（新端点 + 新 query 参数），无需再生成兼容性基线。
