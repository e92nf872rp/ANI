# INFERENCE-INTERNAL-ENDPOINT-RESOLVER-A — KB 通过 served_model_name 解析内部 SVC

完成日期：2026-09-29
对应 Sprint：Sprint 13 / Services 受控并行 PR
验证结果：resolver 相关三包 race 测试通过；66 集群部署和 served_model_name resolver live 验证通过。
仓库级 `make validate-services` 通过临时 `python -> python3` PATH 兼容执行。

## 实现了什么

新增 inference-service 集群内部 gRPC resolver。KB 传入 `tenant_id + served_model_name` 后，
inference-service 查询当前租户按模型名匹配的推理服务，只有服务处于 `running` 且 runtime endpoint
已观测时，才返回规范化的内部 OpenAI `base_url`、`served_model_name` 和冻结的 `task`；不依赖 AI Gateway 发布状态。
`task=generate` 用于对话，`task=embed` 用于向量。公开 Services API、
AI Gateway 路由和 KB 数据库结构不变。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/inference/resolver/v1/inference_endpoint_resolver.proto` | 新增 | 集群内部 resolver RPC |
| `pkg/generated/pb/inference/resolver/v1/*` | 新增 | Go gRPC 生成物 |
| `services/inference-service/internal/service/endpoint_resolver.go` | 新增 | 租户、模型名、运行状态、generation、Service DNS/端口校验和 task 返回 |
| `services/inference-service/internal/grpcapi/endpoint_resolver.go` | 新增 | gRPC adapter 和错误映射 |
| `services/inference-service/main.go` | 修改 | 在现有内部 gRPC listener 注册 resolver |
| `services/inference-service/internal-endpoint-resolver.md` | 新增 | KB 对接说明和错误语义 |

## 完工标准达成

- [x] 同租户 running 服务按 `served_model_name` 返回 `/v1` 内部 base URL 和冻结的 `task`（`generate`/`embed`）
- [x] 跨租户、错误 Service DNS、错误端口、非 running 和缺失 endpoint 被拒绝
- [x] 旧 `GetEndpointURL` 未复活，公开响应继续隐藏 runtime endpoint
- [x] `go test ./services/inference-service/... -count=1`
- [x] resolver proto 单文件 Buf lint
- [x] `go test ./services/inference-service/internal/service ./services/inference-service/internal/grpcapi ./services/inference-service/internal/repository -race -count=1`
- [x] 66 集群 gRPC live 验证：`served_model_name=qwen3.5-0.8b` 返回实际 `service_id` 和私有 SVC `/v1` 地址
- [x] 66 集群 inference-service 镜像更新为 `dev-20260929-inference-served-model-b`（digest `sha256:334c54fb6876a375c28aa179bf5f19e35f066d3c9a23de06ea5bda8f28740b2f`）并完成 rollout
- [x] `make validate-services`（通过临时 `python -> python3` PATH 兼容执行）

## 备注

resolver 不把 `tenant_id` 当作调用方身份，也不要求用户 JWT/API Key。部署时应由集群网络
策略限制 9104 gRPC 的调用方；KB 团队负责接入生成的 resolver client。本批次不修改 KB。
