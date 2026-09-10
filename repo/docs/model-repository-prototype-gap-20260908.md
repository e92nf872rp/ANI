# 模型仓库原型对照需求（2026-09-08）

## 结论

`/root/design/原型9.8` 已经把“模型 Catalog → 版本治理 → 兼容性检查 → 一键部署 → Playground/API 调用”定义成闭环；当前 ANI 服务端已覆盖模型元数据、远程导入、版本查询、上传和删除，但还没有完全覆盖原型中的版本治理、兼容性门禁、模型详情运营信息和前端闭环。

## 原型中明确存在的能力

- 模型来源：HuggingFace、ModelScope、本地上传、内置模型。
- 模型列表：按模型类型（Chat/Embedding）、状态（可用/导入中/失败）统计和筛选。
- 模型详情：版本列表、默认/发布版本、文件清单、模型卡、许可证、变更记录、导入任务和安全扫描结果。
- 版本治理：候选版本校验、发布为默认版本、历史版本保留/删除、发布原因和操作记录。
- 部署闭环：从模型版本一键创建推理服务，部署前做模型状态、版本状态和引擎推荐检查。
- 引擎推荐：Chat 推荐 vLLM，Embedding 推荐 TEI；根据模型大小和类型给出资源建议。
- 调试与调用：部署后 Playground 试聊/试推理，生成 API Key 调用 `/v1/chat/completions` 或 Embeddings 接口。
- 运维反馈：导入失败、评测失败、Webhook 通知和安全扫描结果可追踪。

## 当前实现已具备

- `GET /api/v1/svc/models`：模型列表、筛选、cursor 分页。
- `POST /api/v1/svc/models`：创建模型元数据。
- `POST /api/v1/svc/models/import`：HuggingFace/ModelScope 异步导入。
- `GET /api/v1/svc/models/{model_id}`：模型详情。
- `GET /api/v1/svc/models/{model_id}/versions`：版本列表和 cursor 分页。
- `POST /api/v1/svc/models/{model_id}/versions`：注册上传版本。
- `POST /api/v1/svc/models/{model_id}/upload-url`：预签名上传。
- `GET /api/v1/svc/model-import-tasks/{task_id}`：导入任务查询。
- 推理服务创建已关联 `model` 和 `model_version_id`。

## 当前缺失或需要变更

### P0：原型主流程必须打通

1. **版本发布治理**：增加候选版本校验、发布/撤销发布、设置默认版本接口；发布必须校验导入完成、文件完整性、校验和及安全扫描状态。
2. **部署前兼容性检查**：增加模型版本与运行时引擎的 compatibility check，返回推荐引擎、CPU/GPU、显存、最小内存和阻断原因；推理服务创建应支持引用检查结果或在服务端再次校验。
3. **导入状态闭环**：导入任务必须返回可轮询的 task，失败要有稳定错误码、阶段、进度和可读原因；补齐数据库迁移检查，避免服务镜像先于 schema 发布。
4. **前端模型仓库闭环**：模型列表、导入弹窗、详情版本 Tab、发布版本操作、部署入口、任务进度和失败重试需要接入真实 API，不能继续依赖原型 `AniStore`。

### P1：原型展示和治理信息

1. 扩展模型详情返回：`model_type`、`license`、`model_card`、`base_model`、`tags`、`default_version_id`、`security_status`、`usage_stats`。
2. 扩展版本返回：`stage`、`validation_status`、`files`、`changelog`、`published_at`、`published_by`、`size_bytes`、`checksum_sha256`。
3. 增加导入来源：本地上传入口和内置模型注册；远程导入继续禁止凭据透传。
4. 增加模型操作审计：导入、校验、发布、撤销、删除、部署及失败原因。
5. 增加 Webhook 事件：import completed/failed、validation failed、version published。

### P2：调试和运营增强

1. Playground 根据模型类型生成 Chat/Embedding 两种请求模板，并显示实际 endpoint、模型名和 API Key 使用说明。
2. 增加模型使用统计：调用次数、Token、延迟、错误率、最近调用时间。
3. 增加安全扫描详情和许可证确认门禁。

## 契约建议

优先拆为 v1 增量契约，不修改已有字段语义：

- `POST /models/{model_id}/versions/{version_id}/validate`
- `POST /models/{model_id}/versions/{version_id}/publish`
- `POST /models/{model_id}/versions/{version_id}/unpublish`
- `GET /models/{model_id}/versions/{version_id}/compatibility`
- `GET /models/{model_id}/operations`
- `GET /models/{model_id}/usage`

推理部署请求增加可选 `compatibility_check_id`；未传时服务端执行同等校验。所有列表保持 `created_at DESC, id DESC` 和 cursor 分页。

## 验收标准

- 从远程导入开始，前端可以看到任务进度和最终失败原因。
- 导入完成后，版本详情显示文件、大小、校验和、校验状态。
- 未通过校验或安全门禁的版本不能发布，也不能部署。
- 发布版本可从模型详情一键创建推理服务；服务详情能回溯模型和版本。
- 模型列表、版本列表、推理服务列表都支持 cursor 分页，最新记录在第一页最前。
- Playground 的 Chat 和 Embedding 请求均能基于真实服务 endpoint 完成调用。
