# INFERENCE-SERVICE-HTTP-ADAPTER-C3

> 日期：2026-08-15
> 状态：superseded
> 被取代：`INFERENCE-SERVICE-GATEWAY-GRPC-C4`
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md`
> 前置：Core PR #99、Services PR #101、`INFERENCE-SERVICE-CONTROL-PLANE-C1`、`INFERENCE-SERVICE-LIFECYCLE-CONTROL-PLANE-C2`

独立服务内的 `/api/v1/svc` 产品 HTTP mux 与后续 HMAC TenantResolver 方案已推翻。平台契约要求产品 HTTP 只在 ANI Gateway，服务间走新的 `InferenceControl` gRPC。C1/C2 控制面保留；本批次 HTTP adapter 代码已删除。

## 完成范围

- 新增 cluster-internal HTTP application adapter：严格 JSON DTO、模型版本 UUID 规范化、CPU/GPU 资源转换，以及稳定错误码映射。单独出现的 `gpu_count_per_pod` 不得推断为 GPU。
- `NewHandler` 用 Go 1.25 `http.ServeMux` 方法路由挂上已批准契约路径：list/create/get/scale/lifecycle/delete 与 operation query。租户身份只来自注入的 `TenantResolver`，不从 JSON 或未鉴权 header 取值。
- create 返回 `202` 公共 `ServiceView`；scale/lifecycle/delete 返回 `202` `OperationView`；list/get/operation 返回 `200`。投影复用 C2 public view，`invocation_url`/`endpoint_url` 保持 null，不序列化 domain 实体或内部 runtime 字段。
- 错误响应只含 `code` 与脱敏 `message`。未知依赖错误映射为 `503 DEPENDENCY_UNAVAILABLE`，不回传 SQL/provider 原文。
- `Server` 只接受 loopback 或显式 cluster-pod IP，拒绝 `:port` / `0.0.0.0` / `[::]`；超时必须为正；`Run` 在 context 取消后优雅关闭，context 驱动的 `http.ErrServerClosed` 返回 nil。

## Design Decisions

- C3 只装配 HTTP 适配器，不发明生产租户解析器，也不做可执行 composition root。这样未完成的身份边界不会被误绑到公网或 Gateway。
- 所有写操作先解析租户，再解码 JSON 或调用用例，避免无身份请求进入控制面。
- create 的 HTTP 体是资源投影而不是 AsyncTask，与已批准 OpenAPI `202 InferenceService` 一致；异步身份放在 `current_operation_id`。

## Deviations

- 本批次没有修改 ANI Gateway，也没有替换现有 inference stub。
- 没有生产 `TenantResolver`、进程入口、服务发现或 TLS/auth middleware。
- 带 build tag 的真实 pgx integration suite 在未配置 DSN 时明确 SKIP；不构成 PostgreSQL live evidence。

## Tradeoffs

- 考虑过在 C3 同时接线 Gateway。按当前范围，inference-service 自身 HTTP 语义必须先独立可测，Gateway delegation 后置。
- 考虑过从请求 header 读取租户。这会在身份未冻结时形成可伪造入口。最终只接受注入 resolver。
- 考虑过 create 同时返回 resource 与 task。契约只批准 `202 InferenceService`，因此保持单资源投影。

## Open Questions

- 下一批次需要服务身份、真实 ModelCatalog、Core `platform-workloads` SDK adapter 和 composition root。ANI Gateway 仍推迟到 standalone inference-service 被证明可运行之后。
- 真实 PostgreSQL/RLS/concurrency live gate 仍要在完整服务链形成后执行。
- 不可重试分类、dead-letter、scale rollback、Deployment/LWS/vLLM 继续后置。

## 验证证据

```text
cd repo/services/inference-service
GOWORK=off go test -race ./... -count=1
GOWORK=off go test -tags=integration ./internal/repository -run TestPostgresControlPlaneIntegration -count=1 -v

cd repo
python3 scripts/validate_inference_control_plane_migration_test.py
python3 scripts/validate_inference_control_plane_migration.py
PATH=/tmp/ani-pybin:$PATH make validate-services
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
git diff --check
```

真实 PostgreSQL DSN 未配置时，tagged integration test 必须明确 SKIP；该结果只证明测试代码可编译，不构成 PG live evidence。

## 明确未完成

- 无生产租户解析器、可执行 composition root、Gateway delegation、auth middleware 或进程部署。
- 无真实 ModelCatalog/Core SDK adapter、Core `platform-workloads` handler/port/adapter。
- 无不可重试分类、dead-letter、scale rollback/失败清理完整闭环。
- 无 migration live apply、真实 PostgreSQL/RLS/concurrency 执行证据。
- 无 Deployment、LeaderWorkerSet、Volcano、vLLM、CPU/GPU/跨节点推理或清理 live evidence。

## 下一批次边界

下一批次实现服务身份、真实 ModelCatalog/Core SDK adapter 与 composition root；ANI Gateway 继续推迟，直到 standalone inference-service 被证明可运行。任何后续批次在对应 live gate 前都不得标记 control-plane ready、runtime ready 或 production ready。
