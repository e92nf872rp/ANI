# 模型仓库 P0 契约实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将模型仓库现有最小闭环与 7.29 产品原型的 P0 Catalog 能力对齐，补齐上传、导入任务查询、版本列表、基础筛选、响应字段和删除引用保护的 Services OpenAPI 契约。

**Architecture:** `repo/api/openapi/services/v1.yaml` 继续是唯一公开契约来源；独立 Python validator 固定模型仓库 P0 语义，通用 Services validator 固定所有新增/修改 operation 的 v1 鉴权格式。SDK 和静态 API 文档全部由 OpenAPI 重新生成，本批不修改 Gateway、model-service、数据库或 Console。

**Tech Stack:** OpenAPI 3.0 YAML、Python 3 + PyYAML/unittest、Make、既有四语言 Services SDK 生成器。

## Global Constraints

- 只在本地 `main` 分支修改；禁止创建分支或 worktree。
- 只修改契约、契约 validator、生成 SDK/API 文档和本设计/计划文档。
- 所有本批新增或修改的模型 operation 使用 `BearerAuth + ApiKeyAuth + x-ani-authz v1`，`resource=model`、`boundary=tenant`、`principal_kinds=[user]`。
- 不增加模型评测、优化、治理安全、来源血缘、版本退役、部署反馈、调用统计或协作订阅契约。
- 保持 additive 兼容，不删除既有字段、路径、HTTP 方法或成功状态码。
- 本批暂不 commit/stage/push，等待与推理限流契约一起发运。

---

### Task 1: 建立模型仓库专项契约门禁

**Files:**
- Create: `repo/scripts/validate_model_repository_contract.py`
- Create: `repo/scripts/validate_model_repository_contract_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Consumes: `api/openapi/services/v1.yaml`
- Produces: `validate(spec: dict[str, Any]) -> None`、Make target `validate-model-repository-contract`

- [x] **Step 1: 先写失败测试**

测试必须直接读取真实 Services OpenAPI，并分别断言：

```python
def test_model_schemas_expose_p0_fields(self):
    model = self.schemas["Model"]["properties"]
    version = self.schemas["ModelVersion"]["properties"]
    self.assertIn("description", model)
    self.assertIn("updated_at", model)
    self.assertEqual(model["versions"]["items"]["$ref"], "#/components/schemas/ModelVersion")
    self.assertIn("checksum_sha256", version)
    self.assertIn("storage_path", version)

def test_model_list_exposes_p0_filters(self):
    names = {p["name"] for p in self.paths["/models"]["get"]["parameters"]}
    self.assertTrue({"keyword", "source", "capability"}.issubset(names))

def test_p0_paths_exist(self):
    self.assertIn("get", self.paths["/models/{model_id}/versions"])
    self.assertIn("post", self.paths["/models/{model_id}/upload-url"])
    self.assertIn("get", self.paths["/model-import-tasks/{task_id}"])

def test_delete_declares_model_in_use_conflict(self):
    conflict = self.paths["/models/{model_id}"]["delete"]["responses"]["409"]
    self.assertIn("MODEL_IN_USE", conflict["description"])

def test_all_model_operations_use_v1_authz(self):
    validator.validate_authz_format(self.spec)
```

- [x] **Step 2: 运行测试并确认 RED**

Run:

```bash
cd repo
python3 scripts/validate_model_repository_contract_test.py
```

Expected: FAIL，原因只允许是所需字段、路径、409 语义或 v1 鉴权尚不存在。

- [x] **Step 3: 实现最小 validator**

validator 固定以下 operationId/action：

```python
REQUIRED_OPERATIONS = {
    ("get", "/models"): ("listModels", "read"),
    ("post", "/models"): ("createModel", "create"),
    ("post", "/models/import"): ("importModel", "create"),
    ("get", "/models/{model_id}"): ("getModel", "read"),
    ("delete", "/models/{model_id}"): ("deleteModel", "delete"),
    ("get", "/models/{model_id}/versions"): ("listModelVersions", "read"),
    ("post", "/models/{model_id}/versions"): ("createModelVersion", "create"),
    ("post", "/models/{model_id}/upload-url"): ("getModelUploadURL", "create"),
    ("get", "/model-import-tasks/{task_id}"): ("getModelImportTask", "read"),
}
```

标准鉴权对象固定为：

```python
{
    "version": "v1",
    "resource": "model",
    "action": action,
    "boundary": "tenant",
    "principal_kinds": ["user"],
}
```

validator 还必须校验：上传请求必填 `idempotency_key/version/file_name/size_bytes`，上传响应必填 `upload_url/storage_path/expires_at`，导入任务查询返回 `AsyncTask`，且禁止出现规划 schema 名称前缀 `ModelEvaluation`、`ModelOptimization`、`ModelGovernance`、`ModelUsage`。

- [x] **Step 4: 接入 Make 门禁**

新增：

```make
validate-model-repository-contract:
	python scripts/validate_model_repository_contract_test.py
	python scripts/validate_model_repository_contract.py
```

并从 `validate-services-contract` 调用该 target，使 GitHub Services gate 自动覆盖模型仓库专项契约。

---

### Task 2: 实现 Services OpenAPI P0 契约

**Files:**
- Modify: `repo/api/openapi/services/v1.yaml`
- Modify: `repo/architecture/services-contract-baseline.yaml`
- Modify: `repo/architecture/services-route-baseline.yaml`

**Interfaces:**
- Consumes: Task 1 的 `REQUIRED_OPERATIONS` 和 schema 断言
- Produces: 模型仓库 P0 OpenAPI、SDK 生成输入

- [x] **Step 1: 补齐 additive schemas**

在 `Model` 增加：

```yaml
description: { type: string, nullable: true }
updated_at: { type: string, format: date-time, nullable: true }
versions:
  type: array
  items: { $ref: '#/components/schemas/ModelVersion' }
```

在 `ModelVersion` 增加：

```yaml
checksum_sha256: { type: string, nullable: true }
storage_path: { type: string }
```

新增 `CreateModelUploadURLRequest` 与 `ModelUploadURLResponse`，分别固定设计文档中的请求/响应字段；`upload_url` 使用 `format: uri`，`expires_at` 使用 `format: date-time`，`size_bytes` 最小值为 1。

- [x] **Step 2: 修改模型 operation**

- `GET /models` 增加 `keyword/source/capability` query 参数。
- `GET /models/{model_id}/versions` 返回 `CursorPage + items: ModelVersion[]`。
- `POST /models/{model_id}/upload-url` 返回 `ModelUploadURLResponse`。
- `GET /model-import-tasks/{task_id}` 返回 `AsyncTask`。
- `DELETE /models/{model_id}` 增加描述含 `MODEL_IN_USE` 的 409 响应。
- 所有 `REQUIRED_OPERATIONS` 增加标准 security 与 `x-ani-authz`。

- [x] **Step 3: 清理已迁移的鉴权与路由基线**

从 `architecture/services-contract-baseline.yaml` 删除以下六个 `operation_security` 例外：

```text
listModels
createModel
importModel
getModel
deleteModel
createModelVersion
```

新增 operation 不允许写入鉴权或语义 baseline。

路由表面基线同步移除已由契约覆盖的 `GET /models/{model_id}/versions`，并登记两个 contract-only 新路径，明确 Gateway handler 由后续实现批次注册。

- [x] **Step 4: 运行 GREEN**

Run:

```bash
cd repo
python3 scripts/validate_model_repository_contract_test.py
python3 scripts/validate_model_repository_contract.py
PATH=/tmp/ani-pybin:$PATH make validate-services-contract
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec
```

Expected: 全部 PASS；Services accepted baseline 数量比当前 113 减少 6，预期为 107。

---

### Task 3: 刷新生成物并完成本地契约门禁

**Files:**
- Modify: `repo/sdks/services/go/anisdk/client.go`
- Modify: `repo/sdks/services/java/src/main/java/com/kubercloud/ani/services/ApiClient.java`
- Modify: `repo/sdks/services/python/kubercloud_ani_services/client.py`
- Modify: `repo/sdks/services/typescript/src/index.ts`
- Modify: `repo/sdks/services/typescript/src/index.mjs`
- Modify: `repo/sdks/services/sdk-metadata.json`
- Modify: `repo/docs/api/index.html`
- Modify: `repo/docs/api/services.html`

**Interfaces:**
- Consumes: Task 2 的 `api/openapi/services/v1.yaml`
- Produces: 与契约一致的四语言 Services SDK 和静态 API 文档

- [x] **Step 1: 重新生成 SDK 和文档**

Run:

```bash
cd repo
python3 scripts/gen_sdk_alpha.py
python3 scripts/generate_api_docs.py
```

Expected: 生成物包含 `listModelVersions`、`getModelUploadURL`、`getModelImportTask` 三个 operation，且既有限流生成物仍保留。

- [x] **Step 2: 运行专项与通用门禁**

Run:

```bash
cd repo
python3 scripts/validate_model_repository_contract_test.py
python3 scripts/validate_model_repository_contract.py
PATH=/tmp/ani-pybin:$PATH make validate-services-contract
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec
PATH=/tmp/ani-pybin:$PATH make validate-sdk-beta
PATH=/tmp/ani-pybin:$PATH make validate-doc-api
git diff --check
```

Expected: 全部 PASS。

- [x] **Step 3: 审核改动边界**

Run:

```bash
git status --short -uall
git diff --stat
```

确认没有 Gateway、model-service、数据库 migration、Console 或无关 Core 文件变化；保持所有文件未 stage、未 commit、未 push，等待与推理限流契约一起发运。
