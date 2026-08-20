# INFERENCE-SERVICE-LOGS-C11

> 日期：2026-08-15
> 状态：local/logic verified
> 方案依据：`services/docs/console-modules/inference/inference-service-design.md` §13.2
> 前置：`INFERENCE-SERVICE-GATEWAY-GRPC-C4`、`INFERENCE-PLATFORM-WORKLOAD-LOCAL-C5`
> 不包含：`POST /inference-services/{service_id}/test`（C10 已推翻，不再实现）

## 完成范围

- 产品 `GET /api/v1/svc/inference-services/{service_id}/logs` 不再由 Gateway 固定返回空列表。租户只来自 `middleware.GetTenantID`，查询参数 `limit` / `cursor` / `level` 下传到内部 gRPC。
- 内部 proto 增加 `ListInferenceServiceLogs`。响应只投影 `timestamp` / `level` / `message` / `container` / `stream` / `next_cursor`，不包含 `runtime_ref`、`runtime_endpoint`、`replica` 或 Pod UID。
- inference-service 增加独立 `LogReader`：服务不存在、跨租户或已删除 → `NOT_FOUND`；服务存在但还没有 `runtime_ref`，或 runtime 已消失 → `200` 空列表。不把 runtime 塞进 Controller。
- `InferenceRuntime` 增加 `Logs`。fake 写一条控制面 `info` 行（不含 endpoint）；coresdk 调用 Core `GET /platform-workloads/{id}/logs`。Core local/k8s adapter 仍按 C9 live gate 返回空列表，本批次不拉真实 Pod log。
- 日志消息脱敏：Authorization / Bearer / secret / pre-signed / `runtime_ref` / `runtime_endpoint` / `internal.svc` / URL。
- 未改 OpenAPI。policies 继续 501。`/test` 继续不注册。

## Design Decisions

- 学 C10 的教训：logs 用独立 `LogReader` + `Server.WithLogs`，不污染 `ControlUseCase` 的所有 fake。
- Worker 与 LogReader 共用同一个 `InferenceRuntime` 实例，避免两套 runtime 状态。
- cursor 使用整数 offset，不把内部 ID 放进 cursor。
- Core logs 继续空列表，避免与 `INFERENCE-PLATFORM-WORKLOAD-K8S-LIVE-GATE-C9` 的 `logs-empty-until-live-log-store` 冲突。产品路径仍然可测：404、租户、查询参数、脱敏和空列表语义。

## Deviations

- 没有真实 Loki/LogStore 或 Kubernetes Pod log 采集。
- 没有改 Core OpenAPI / Services OpenAPI。
- 没有实现 `/test` 或 policies。

## 验证证据

```text
cd repo/services/inference-service && GOWORK=off go test -race ./... -count=1
cd repo/services/ani-gateway && go test ./internal/router/ -count=1 -run Inference
cd repo
python3 scripts/validate_inference_service_contract.py
python3 scripts/validate_services_route_contract.py
python3 scripts/validate_services_boundary.py
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

## 明确未完成

- 无真实集群 Pod log / Loki live evidence，不得标记 runtime ready 或 live passed。
- 无调用网关、`invocation_url`、policies 生效。
- 不实现 `/test`。

## 下一批次边界

经人工确认物理机/集群后，才允许按 C9 live gate 对真实集群写 PlatformWorkload，并另开批次建设真实 log store。在此之前不得把 C9 status 改为 live。
