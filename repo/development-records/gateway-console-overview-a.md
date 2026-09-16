# GATEWAY-CONSOLE-OVERVIEW-A — Console 首页概览统计聚合接口

> 批次类型：Feature batch
> 日期：2026-09-14 开发与首轮实测；2026-09-14 部分成功语义修订与第二轮实测；2026-09-14/15 ani-system 部署
> 分支：`feat/console-overview`；PR：#166（评审中）
> 状态：`LOCAL_VERIFIED + K8s 测试环境实测（ani-test2 + ani-system）`
> 方案/测试报告/前端对接文档：`kjs-study/首页概览相关文档/`（三份，未入库）

## 1. 背景与目标

Console 首页四类统计卡片（实例 / 推理服务 / 模型仓库 / 知识库）此前没有聚合端点：前端需并发调 `/instances`、`/svc/models`、`/svc/inference-services`、`/svc/knowledge-bases` 四个列表接口自行翻页计数，部分资源还缺总数口径。本批次新增 `GET /api/v1/overview`（operationId `getConsoleOverview`），一次返回四类资源的总数与状态分布。

方案选型（已与用户对齐）：Gateway BFF 聚合——ani-gateway 复用既有实例链路与三个 Services gRPC 客户端在服务端聚合计数，后端业务服务零改动、不新增各服务 count RPC、前端单请求。Core 不直接查 Services 表（架构红线不变），实例计数走 Core 实例链路（gateway 进程内），model/inference/kb 计数走既有 gRPC list。

## 2. 契约（API first）

`repo/api/openapi/v1.yaml` 新增：

- `GET /overview`：`x-ani-rbac-scope: scope:instances:read`；`x-ani-authz {version: v1, resource: overview, action: get, boundary: tenant, principal_kinds: [user, api_key]}`。
- 响应 `ConsoleOverviewResponse`（四部分必返）：`instances`（total + by_state 8 键：pending/provisioning/starting/running/stopping/stopped/failed/deleting，无 deleted 键）、`inference_services`（by_status 6 键）、`models`（by_status 4 键）、`knowledge_bases`（by_status 2 键）；固定键 0 值不省略。
- 错误码 401/403；无 503（见 §4 语义修订）。
- 口径：各部分与对应列表接口同口径，均不含 state/status=deleted。

生成物同步：Core SDK（go/java/python/typescript）、`repo/docs/api/*.html`、`services/ani-gateway/internal/authz/zz_generated_core_policies.go`（validate_gateway_authz 323 routes / 0 error）。

## 3. 实现（ani-gateway）

- `internal/router/console_overview.go`（新增）：`consoleOverview` handler；四数据源 `run()` 并行（WaitGroup + mutex 记 firstErr）；实例部分复用实例链路（`refreshAllStoreStatuses` + 全量分页列表 + 孤儿 Deployment 合并后按 state 计数，与 `/instances` 列表逐字段同口径）；model/inference/kb 部分走既有 `ModelServiceClient` / inference / kb gRPC 客户端，cursor 翻页走尽（每页聚合后继续 `next_cursor`），按 status 计数；gRPC 客户端未装配（nil）时该部分空计数（total=0、分布全 0）。
- `internal/router/instances.go`：在实例路由组注册 `registerConsoleOverview`。
- 数据源失败处理见 §4；失败记录 `slog.Warn`（source/tenant_id/err）。

## 4. 语义修订：整体 503 → 部分成功（用户决策）

初版语义为"任一数据源失败整体 503（无部分成功语义）"。用户决策改为**部分成功**：任一数据源失败**不整体报错**，整体仍 200，失败的那一部分以空计数返回（total=0、by_status/by_state 全 0），其余部分正常计数；服务端记 WARN 日志。同步修改：handler 删除整体 503 分支、v1.yaml 删除 503 响应声明并改 description、单测两个 503 用例重写为部分成功/未装配空部分用例。已实测降级场景（§6.2）。

## 5. 单元测试与门禁

- `internal/router/console_overview_test.go`（新增）4 用例：跨租户排除与 deleted 排除及全键分布断言；model cursor 翻页走尽聚合；部分成功（model 源失败→models 空、其余正常、整体 200）；未装配 gRPC 客户端→对应部分空。
- 通过：`go build ./services/ani-gateway/...`、`go test ./services/ani-gateway/...`（4 包全过）、`make validate-openapi-spec`、`make validate-gateway-authz`（323 routes / 0 error）、`make validate-architecture`、`git diff --check`；gofmt 通过。
- 未跑全量 `make test`：本批次仅 gateway 单包改动，定向测试覆盖；由 CI 全量门禁兜底。

## 6. K8s 测试环境实测（10.10.1.66）

### 6.1 ani-test2（NodePort 30083，镜像 `test2-20260914-g1` → `test2-20260914-g2`）

- 正常场景：租户 token 200；四类计数与四个列表接口翻页全量统计**逐字段一致**（实例 10、推理 8、模型 5、知识库 15）；5 次重复请求响应一致（幂等）；延迟 205~340ms（g2 复测 197~314ms）。
- 鉴权：无凭证 401、坏 token 401、平台 token 403（租户边界）。
- 降级故障注入：NetworkPolicy 断 model-service:9103 → 重启 gateway → 整体 200、models 空计数（total=0 全 0）、其余三类正常；gateway 日志 `WARN console overview data source degraded ... source=models`；恢复 netpol 后 models 恢复 total=5。
- 实测坑（已复现两次，记入环境注意事项）：**修改 NetworkPolicy 后不重启 gateway pod 不生效**——gRPC 复用 netpol 变更前建立的 HTTP/2 长连接；验证降级必须 rollout restart。降级时该次请求延迟抬升至失败源超时（约 5s）。

### 6.2 ani-system（NodePort 30080，镜像 `dev-20260914-overview`，2026-09-14）

- 按《更新K8s测试环境的auth-service和gateway操作步骤.md》执行：部署前 pod 实际 env 确认 `ANI_AUTH_MODE=auth_service` 与 `MODEL_SERVICE_GRPC_ADDR` 均已就绪（deployment spec 显示空印证"以 pod 实际 env 为准"铁律）→ **只 set image 不改 env**；etcd 预检 dbSize 871MiB/2GiB（43%）；rollout 成功；健康检查 200 `{"status":"ok","version":"v0.8.0"}`；ani-test2 镜像未被误动（仍 `test2-20260914-g2`）。
- 功能实测：租户 token 200（0.92s），实例 54（running 29 / failed 6 / 其他 19）、推理 8、模型 5、知识库 15，四类全部正常计数；无凭证/坏 token 401。

### 6.3 环境问题（非本批次代码缺陷）

ani-test2 gateway 访问 ani-system model-service:9103 曾被 `model-service-gateway-ingress` NetworkPolicy 拦（该策略此前未放行 ani-test2 命名空间 gateway 源），既有 `/svc/models` 同样失败；已 patch 放行并验证恢复。该过程同时真实验证了部分成功降级语义（§6.1）。

## 7. 前端对接

`kjs-study/首页概览相关文档/Console首页概览统计前端接口文档.md`：响应结构、错误码、数据口径、调用示例、接入要点（固定键、降级空计数展示口径、轮询建议 ≥30s、超时 ≥10s），及**统计卡片展示取值映射**（实例：运行中=by_state.running/异常=failed/其他=total−running−failed；推理服务同构；模型：可用=ready/失败=error/其他=差值；知识库：活跃=active/其他=total−active；"其他"统一用 total 差值计算，未来新增状态键自动归入）。

## 8. 决策记录

1. 聚合位置选 Gateway BFF（用户选定方案 B）：后端服务零改动、避免前端多次调用。
2. Core/Services 边界：实例计数走 Core 实例链路，Services 三资源走既有 gRPC list（未绕过 Core OpenAPI 跨层契约，Core 未直查 Services 表）。
3. 部分成功语义（用户决策，覆盖初版整体 503）：失败部分空计数 + WARN，整体 200。
4. "其他"卡片口径：`total` 差值，不按枚举键逐一累加（状态键演进安全）。
5. ani-test2 部署 tag `test2-20260914-g1/g2`、ani-system tag `dev-20260914-overview`（tag 纪律区分环境）。

## 9. 边界与遗留

- 本接口为租户边界，平台管理员 token 403（BOSS 不使用本接口）。
- model/inference/kb 计数走 list 翻页全量拉取，资源量大时是 O(n) 网络开销；当前量级（数百以内）无感知，后续可评估各服务增加 count RPC（本批次明确不做）。
- 降级时接口无法区分"真的没有资源"与"该部分降级为空"（前端按全 0 展示"暂无数据"，已写入对接文档）。
- 未验证：长期 soak、多租户高频轮询压测；不标记 runtime ready / production ready（本接口为只读聚合，无底座 provider 依赖声明）。
