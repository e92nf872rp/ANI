# ANI-GATEWAY-OPENAI-ROUTE-BOUNDARY（2026-09-07）

## 状态

已完成 Gateway 控制面路由收口，并在现有 `ani-system` 集群滚动验证。该批次不包含 Console 前端改动，也没有新增或修改 v1 OpenAPI/protobuf；模型仓库 materialization contract 是此前独立审批的批次。

## 变更

- 移除 ANI Gateway 的旧 `POST /v1/chat/completions` 占位代理。OpenAI 兼容 chat/embedding 请求由独立 Envoy AI Gateway 数据面负责，内部 vLLM 的 OpenAI 路径保持不变。
- 保留旧 `GET /v1/inference/stream` 占位路径，待单独确定其归属后再处理，避免把历史兼容入口与新的数据面混在一起。
- `GET /api/v1/svc/models/{model_id}/versions` 使用已审批的 `ListModelVersions` 内部调用，返回 `items` 与 `next_cursor`，不再落入 501 stub。

## 验证

| 检查 | 结果 |
|---|---|
| Gateway route focused tests | 通过（含 chat 路由 404 回归与版本列表测试） |
| `go vet ./internal/router` | 通过 |
| Services route contract validator | 通过；既有 accepted baseline warnings 保留 |
| 文档入口、Python 编译、`git diff --check` | 通过 |
| live Gateway rollout | `ani-gateway` 1/1，镜像 digest `sha256:4dae93c3f1f080101b0dd2f35b98443185de6c5bf231dab70228ed49f631f563` |
| authenticated live smoke | `POST /v1/chat/completions` 返回 404；模型版本列表返回 200 |
| independent Envoy probe | Envoy NodePort 对 chat 请求返回 401，证明入口与认证边界存在；未据此宣称模型推理成功 |

## 边界与后续

独立 Envoy 当前仍以静态路由为主，动态 publisher/ext-auth 与真实模型 chat 成功响应尚未形成 live evidence。ANI Gateway 本身不再承担 OpenAI 数据面代理。远程导入/对象 materialization 仍需 tenant Certificate/RBAC、数据库迁移、镜像可拉取性和对象存储 HTTPS 或明确的受控 HTTP profile 后才能做真实导入验收。

本批次尚未 commit 或 push；工作树中仍有其他模型仓库、Core contract 和部署改动，提交时需按路径拆分。
