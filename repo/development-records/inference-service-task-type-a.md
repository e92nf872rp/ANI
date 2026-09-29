# INFERENCE-SERVICE-TASK-TYPE-A — 列表返回推理任务类型

完成日期：2026-09-29
验证结果：local verified；未部署或执行集群 live 验证

## 变更

`GET /api/v1/svc/inference-services` 的每条 `InferenceService` 增加 `task` 字段：

- `generate`：对话/文本生成，使用 `/v1/chat/completions`；
- `embed`：向量生成，使用 `/v1/embeddings`。

字段来源是创建时由模型能力推导并冻结的
`desired_spec.execution_profile.task`。vLLM 是运行引擎，不能用来区分这两类服务；调用方也不应根据模型名、镜像名或 SVC 地址猜测。

## 兼容性与验证

这是 Services OpenAPI、内部 `InferenceControl` proto、Gateway JSON 和 SDK/API docs 的向后兼容新增字段；旧客户端忽略该字段即可。已通过：

- `make validate-services`
- `go test ./services/inference-service/... ./services/ani-gateway/internal/router/... -count=1`
- 相关 race tests
- `git diff --check`

本批次不修改 KB，不部署集群。
