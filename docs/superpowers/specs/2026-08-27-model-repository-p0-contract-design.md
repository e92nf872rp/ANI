# 模型仓库 P0 契约设计

> 日期：2026-08-27
> 范围：ANI Services 模型仓库后端契约
> 状态：契约已实现，待与推理限流契约一起发运

## 背景

产品原型把模型仓库定义为 Catalog：支持 HuggingFace、ModelScope 和本地上传，模型入库后可以查看版本并部署为推理服务。当前后端已经具备租户隔离的模型元数据、模型详情、模型软删除、本地 PVC 模型版本以及推理服务引用 `model_version_id` 的最小闭环，但公开 Services 契约与 Gateway 实现存在漂移，原型的导入和上传主路径尚无可用后端契约。

本批只修改 `repo/api/openapi/services/v1.yaml` 及其生成物和契约门禁，不实现 Gateway、model-service、worker 或 Console 前端。契约确认后，后端实现按 API-first 流程另行推进。

## 已确认边界

1. 保留模型仓库现有路径，不删除、不改 HTTP 方法，不改变既有成功状态码。
2. 本批实际修改或新增的模型仓库 operation 统一采用 v1 鉴权格式：
   - `security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]`
   - `x-ani-authz.version: v1`
   - `resource: model`
   - `boundary: tenant`
   - `principal_kinds: [user]`
   - action 按 `read/create/delete` 语义声明。
3. 未触碰的其他历史 Services operation 不做鉴权迁移。
4. P0 只覆盖模型 Catalog 的基础发现、上传、异步导入、版本查看和推理引用保护。
5. 原型中标注“规划”的模型评测、优化、治理安全、来源血缘、退役、部署反馈、调用统计和协作订阅不进入本批契约。
6. 一键部署仍由推理服务控制面承担；模型仓库不新增第二套部署资源。

## 契约改动

### 1. 补齐响应模型

`Model` 增加当前 Gateway 已返回且原型基础页面需要的字段：

- `description`
- `updated_at`
- `versions: ModelVersion[]`

`ModelVersion` 增加当前 Gateway 已返回且本地模型链路需要的字段：

- `checksum_sha256`
- `storage_path`

这些字段均为 additive 变更，不删除既有字段。

### 2. 扩展模型列表查询

`GET /models` 在既有 `status/limit/cursor` 之外增加：

- `keyword`：名称、显示名的基础搜索；
- `source`：`upload | huggingface | modelscope | builtin`；
- `capability`：`text-generation | embedding | speech-to-text`。

框架、许可证、规模、标签、Owner、收藏等筛选依赖尚未落地的模型卡片或协作数据，不在本批声明。

### 3. 补齐版本列表路径

新增契约 operation：

```text
GET /models/{model_id}/versions
```

返回游标页结构，`items` 为 `ModelVersion[]`。该路径当前 Gateway 已注册，本批将其纳入公开契约和 SDK。

### 4. 本地上传入口

新增：

```text
POST /models/{model_id}/upload-url
```

请求包含：

- `idempotency_key`
- `version`
- `file_name`
- `size_bytes`

响应包含：

- `upload_url`
- `storage_path`
- `expires_at`

该接口只申请有时限的预签名 PUT URL，不接收文件内容。客户端上传成功后继续调用既有 `POST /models/{model_id}/versions` 登记不可变版本。真实实现通过 ANI Services 到 Core OpenAPI/SDK 的对象存储边界完成，不允许 model-service 直接依赖 MinIO SDK。

### 5. 异步导入任务查询

保留：

```text
模型导入创建端点（路径为 `/models/import`）
```

新增：

```text
GET /model-import-tasks/{task_id}
```

查询返回既有 `AsyncTask`，用于读取 `status`、`progress_pct`、脱敏错误和最终资源 ID。本批不增加暂停、恢复、取消、覆盖版本或 webhook 管理接口。

### 6. 删除引用保护

`DELETE /models/{model_id}` 增加 `409 Conflict` 契约语义：存在未删除推理服务引用任一模型版本时返回稳定错误码 `MODEL_IN_USE`，不得删除模型。跨租户目标继续按 404 隐藏。

## 幂等与错误语义

- 模型创建、版本登记、上传地址申请和模型导入创建端点必须接受并重放同一个 `idempotency_key`。
- 同一租户、同一幂等键、同一请求意图返回首次结果；同键不同意图返回 409。
- 不存在或不属于当前租户的模型、版本、任务统一返回 404。
- 上传文件名非法、大小越界、来源或 capability 非法返回 400。
- 引用保护冲突返回 409，不把推理服务 ID 列表泄漏给无权限主体。

## 验证要求

契约测试至少证明：

1. 所有本批修改或新增的模型 operation 使用标准 v1 鉴权元数据；
2. 模型列表具备三项 P0 筛选；
3. 版本列表、上传 URL 和导入任务查询路径存在且响应 schema 正确；
4. `Model`、`ModelVersion` 补齐 additive 字段；
5. 删除模型声明 409 引用保护；
6. 原型规划能力没有被意外加入契约；
7. OpenAPI、Services contract、SDK 生成和 API 文档门禁通过。

## 后续实现顺序

契约合入后按以下顺序另起实现批次：

1. 修复 create/version 的幂等键传递与持久化；
2. 实现对象存储上传 URL adapter；
3. 实现 HuggingFace/ModelScope 导入 outbox、worker 和任务状态；
4. 实现 P0 列表筛选；
5. 实现删除前的推理引用检查；
6. 使用真实租户和真实对象存储执行上传、导入、模型版本和推理部署 live gate。
