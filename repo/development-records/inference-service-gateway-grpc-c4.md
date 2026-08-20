# INFERENCE-SERVICE-GATEWAY-GRPC-C4

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md`
> 前置：Core PR #99、Services PR #101、`INFERENCE-SERVICE-CONTROL-PLANE-C1`、`INFERENCE-SERVICE-LIFECYCLE-CONTROL-PLANE-C2`
> 推翻：`INFERENCE-SERVICE-HTTP-ADAPTER-C3` 独立服务产品 HTTP，以及错误的服务内 HMAC TenantResolver / HTTP composition root

## 完成范围

- 入口按平台契约重置：Console/SDK → ANI Gateway HTTP `/api/v1/svc/inference-services*` → inference-service 内部 `inference.control.v1.InferenceControl` gRPC → C1/C2 Creator/Controller/Worker。
- 删除独立服务 `/api/v1/svc` HTTP mux 与 inbound JWT 身份方案。旧 `inference.v1.InferenceServiceRPC`（`GetEndpointURL` / `UpdateStatus`）不复活。
- 新增 `api/proto/inference/control/v1/inference_control.proto` 与 `pkg/generated/pb/inference/control/v1` 生成物。消息不包含 `runtime_ref` / `runtime_endpoint` / `invocation_url`。
- inference-service 以 gRPC 进程入口运行（默认 `:9104` / health `:9204`），复用 `pkg/bootstrap.MustConnect` + `RunGRPC` 连接 PostgreSQL/NATS/Redis；控制面 store 使用 `deps.DB`。不监听产品 HTTP。Catalog/runtime 仍用 fake。
- Gateway 从 `middleware.GetTenantID` 注入租户，不信任 JSON / `X-Tenant-Id`，也不回退 `demo-tenant`。`INFERENCE_SERVICE_GRPC_ADDR` 为空时产品写/读 handler 返回 503，Gateway 仍可启动。
- create 返回 `202 InferenceService`；scale/lifecycle/delete 返回 `202 AsyncTask`；list/get/operation 返回 `200`。`invocation_url` / `endpoint_url` 固定 null。policies 返回 `501 FEATURE_NOT_AVAILABLE`。
- 稳定错误码：`INVALID_ARGUMENT=400`，`NOT_FOUND=404`，`NAME_CONFLICT` / `IDEMPOTENCY_CONFLICT` / `OPERATION_IN_PROGRESS=409`，`MODEL_NOT_READY` / `MODEL_INCOMPATIBLE` / `INVALID_STATE_TRANSITION` / `UNSUPPORTED_TOPOLOGY=422`，`DEPENDENCY_UNAVAILABLE=503`。
- 清空 create/scale/delete handler baseline 例外；注册 lifecycle / operation / policies 后删除对应 route baseline 例外。`POST /inference-services/{service_id}/test` 仍保持未实现例外。

## Design Decisions

- 复用 kb-service 已落地的 Gateway HTTP → 内部 gRPC 模式，而不是让 inference-service 再暴露一套产品 HTTP。
- 新 proto 放在 `api/proto` + `pkg/generated`，Gateway 只依赖生成物，不 import `services/inference-service`。
- C1/C2 控制面、PG repository、fake catalog/runtime 全部保留；本批次只纠正入口层。

## Deviations

- 没有真实 ModelCatalog HTTP adapter、Core `platform-workloads` SDK adapter、Core handler 或 LWS/vLLM live。
- 没有 PostgreSQL live apply / RLS concurrency evidence。无 DSN 时 tagged integration test 必须明确 SKIP。
- `POST /inference-services/{service_id}/test` 与 logs 真实采集仍未建设；logs 继续返回空列表。

## Tradeoffs

- 考虑过保留 C3 HTTP 再让 Gateway 反代。这会让独立服务成为第二产品入口，并与 auth/model/kb 的 gRPC 进程模型冲突。
- 考虑过复活旧 `InferenceServiceRPC`。它的 GetEndpointURL/UpdateStatus 语义与已批准 OpenAPI 和 C1/C2 控制面冲突。
- Catalog 继续 fake：未登记模型版本的 create 返回 `NOT_FOUND`，不伪造 ready 模型。
- 进程入口复用 `pkg/bootstrap`：与 model-service/auth-service 对齐，启动会连接 PostgreSQL/NATS/Redis；控制面只用 `deps.DB`，不把 Core ports/adapters 引进 Creator/Controller。已在 `architecture/services-boundary-baseline.yaml` 登记精确例外。

## 验证证据

```text
cd repo/services/inference-service && GOWORK=off go test -race ./... -count=1
cd repo
go test ./services/ani-gateway/internal/router/ -count=1 -run Inference
python3 scripts/validate_inference_service_contract.py
python3 scripts/validate_inference_control_plane_migration.py
PATH=/tmp/ani-pybin:$PATH make validate-services
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
git diff --check
```

真实 PostgreSQL DSN 未配置时，tagged integration test 必须明确 SKIP；该结果只证明测试代码可编译，不构成 PG live evidence。

## 明确未完成

- 无真实 ModelCatalog / Core SDK `platform-workloads` adapter、Core handler/port/adapter。
- 无 migration live apply、真实 PostgreSQL/RLS/concurrency 执行证据。
- 无 Deployment、LeaderWorkerSet、Volcano、vLLM、CPU/GPU/跨节点推理或清理 live evidence。
- 无调用网关、`invocation_url`、test 端点或策略生效。

## 下一批次边界

下一批次 `INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5` 已接 Core `platform-workloads` local handler 与可选 Core SDK adapter。真实 ModelCatalog 与 CPU provider live gate 仍待后续批次。任何后续批次在对应 live gate 前都不得标记 control-plane ready、runtime ready 或 production ready。
