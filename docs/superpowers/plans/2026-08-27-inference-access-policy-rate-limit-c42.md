# Inference Access Policy Rate Limit C42 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为推理服务补齐后端“限流与访问策略”能力，使租户可以管理策略和绑定，并让 Envoy AI Gateway 数据面在调用 vLLM 前完成 AK 认证、租户隔离、策略 allow/deny、QPS/RPM 和并发检查。

**Architecture:** Services OpenAPI 先新增策略控制面契约；ANI Gateway 只暴露 `/api/v1/svc` 后端 API 并委托 inference-service，不代理 OpenAI 数据面流量。inference-service 负责策略资源、绑定、匹配、限流状态和命中事件；envoy-authz-adapter 在现有 AK 校验和租户匹配后调用 inference-service 的内部 `CheckInferenceAccess` RPC，决定 Envoy ext_authz 的 allow/deny/429/503。

**Tech Stack:** Go 1.25、PostgreSQL 17、Redis/CacheStore 或现有内存可替换限流接口、gRPC/protobuf、Hertz ANI Gateway、Envoy ext_authz、Envoy AI Gateway v1.0.0 / Envoy Gateway v1.8.x、Python 3.12 contract/live validators、Services OpenAPI `repo/api/openapi/services/v1.yaml`。

## Global Constraints

- 只在本地 `main` 工作；不得创建、切换或推送任何非 `main` 分支。
- 写操作前确认 `git branch --show-current` 为 `main`；commit/push 前必须 `git fetch upstream main`，落后时用 merge，不用 rebase。
- API-first：先完成 `repo/api/openapi/services/v1.yaml` 契约批次并评审确认，再写 handler、service、adapter 实现。
- 本阶段只处理后端服务、契约、数据面鉴权和真实门禁；不实现 Console React 页面、路由、组件、状态管理或 Playground UI。
- Services API 契约生成物若由 `make validate-services` 产生漂移，只作为契约生成物处理；不得手写 Console 前端业务代码。
- ANI Gateway 只承担控制面 API；不得代理 `/v1/chat/completions`、`/v1/embeddings` 或 SSE。
- auth-service 继续是 AK 唯一认证来源；策略服务不保存 AK 明文，不重新实现 AK 校验。
- 策略检查输入中的 `api_key_id` 必须来自 auth-service `ValidatePrincipal` 的 `credential_id`，不得使用 AK 原文或客户端提交字段。
- 跨租户数据面访问继续返回 404；同租户策略拒绝返回 403；策略限流和并发超限返回 429；策略后端不可用返回 503 且 fail closed。
- 事件、日志、evidence 不得包含 AK 原文、Authorization 头、prompt、completion、embedding 输入文本或向量内容。
- 每个实现任务遵循 TDD：先失败测试，再最小实现，再通过测试。
- 未获得用户明确 commit/ship 授权前，不执行任何 commit、push、PR 或真实集群写操作。

---

## File Map

| 文件 | 职责 |
|---|---|
| `docs/superpowers/specs/2026-08-27-inference-access-policy-rate-limit-design.md` | 已确认的 C42 设计来源 |
| `repo/api/openapi/services/v1.yaml` | Services 公开 REST 契约，新增策略资源、绑定、事件和服务策略子路径 |
| `repo/scripts/validate_inference_access_policy_contract.py` | 静态校验策略契约、字段、错误语义和 501 移除 |
| `repo/scripts/validate_inference_access_policy_contract_test.py` | validator 变异测试 |
| `repo/Makefile` | 新增 C42 contract/local/live gate 入口 |
| `repo/api/proto/inference/control/v1/inference_control.proto` | Gateway/adapter 到 inference-service 的内部 gRPC 契约 |
| `repo/pkg/generated/pb/inference/control/v1/inference_control.pb.go` | proto 生成物 |
| `repo/pkg/generated/pb/inference/control/v1/inference_control_grpc.pb.go` | proto gRPC 生成物 |
| `repo/deploy/migrations/20260827_001_inference_access_policy.sql` | 策略、绑定、事件表及 RLS/索引 |
| `repo/scripts/validate_inference_access_policy_migration.py` | migration 静态校验 |
| `repo/scripts/validate_inference_access_policy_migration_test.py` | migration validator 测试 |
| `repo/services/inference-service/internal/domain/access_policy.go` | 策略领域类型和校验 |
| `repo/services/inference-service/internal/domain/access_policy_test.go` | 策略领域单测 |
| `repo/services/inference-service/internal/repository/store.go` | 增加策略 store 接口 |
| `repo/services/inference-service/internal/repository/postgres.go` | PostgreSQL 策略存取、事件写入和 RLS tenant context |
| `repo/services/inference-service/internal/repository/postgres_test.go` | policy repository 单元测试 |
| `repo/services/inference-service/internal/service/access_policy.go` | 策略 CRUD、绑定、匹配、限流、并发 lease、事件服务 |
| `repo/services/inference-service/internal/service/access_policy_test.go` | 策略服务 TDD 测试 |
| `repo/services/inference-service/internal/grpcapi/server.go` | 暴露策略 CRUD/绑定/事件/Check/Release RPC |
| `repo/services/inference-service/internal/grpcapi/convert.go` | 策略 domain/proto 转换 |
| `repo/services/inference-service/internal/grpcapi/server_test.go` | gRPC 策略接口测试 |
| `repo/services/ani-gateway/internal/router/inference_grpc_client.go` | 新增策略相关 Gateway → inference-service client 方法 |
| `repo/services/ani-gateway/internal/router/inference_resources.go` | 注册并实现策略 REST handlers；移除 `/policies` 501 |
| `repo/services/ani-gateway/internal/router/inference_resources_test.go` | REST 契约和错误映射测试 |
| `repo/services/envoy-authz-adapter/internal/authclient/client.go` | 改为调用 `ValidatePrincipal` 并返回 API key principal |
| `repo/services/envoy-authz-adapter/internal/authclient/client_test.go` | auth client V2 测试 |
| `repo/services/envoy-authz-adapter/internal/policyclient/client.go` | adapter 到 inference-service policy RPC 的有界 client |
| `repo/services/envoy-authz-adapter/internal/policyclient/client_test.go` | policy client 测试 |
| `repo/services/envoy-authz-adapter/internal/extauth/server.go` | ext_authz 增加策略检查和状态映射 |
| `repo/services/envoy-authz-adapter/internal/extauth/server_test.go` | allow/deny/429/503/header 清理测试 |
| `repo/services/envoy-authz-adapter/internal/config/config.go` | 增加 policy gRPC 地址和超时配置 |
| `repo/services/envoy-authz-adapter/main.go` | 注入 policy client |
| `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml` | 后续接入策略时追加 adapter policy 地址环境变量 |
| `repo/deploy/real-k8s-lab/inference-access-policy-live-gate.yaml` | C42 真实门禁契约 |
| `repo/scripts/validate_inference_access_policy_live_gate.py` | C42 live evidence 静态校验 |
| `repo/scripts/validate_inference_access_policy_live_gate_test.py` | live gate validator 测试 |
| `repo/scripts/run_inference_access_policy_live.py` | 真实 Envoy AI Gateway 端到端验证 runner |
| `repo/scripts/run_inference_access_policy_live_test.py` | runner 本地安全测试 |
| `repo/development-records/INFERENCE-SERVICE-ACCESS-POLICY-C42.md` | Feature batch 记录 |
| `repo/development-records/README.md` | 批次索引 |
| `repo/CURRENT-SPRINT.md` | 当前状态入口 |
| `ANI-06-开发计划.md` | Section 零 / 当前 Sprint 状态 |

---

## Phase 1: Contract-only PR

### Task 1: Services OpenAPI contract for inference policies

**Files:**
- Modify: `repo/api/openapi/services/v1.yaml`
- Create: `repo/scripts/validate_inference_access_policy_contract.py`
- Create: `repo/scripts/validate_inference_access_policy_contract_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Produces product schemas:
  - `InferenceAccessPolicy`
  - `InferenceAccessPolicyScope`
  - `InferenceAccessPolicyAccess`
  - `InferenceAccessPolicyRateLimits`
  - `InferenceAccessPolicyConcurrency`
  - `InferenceAccessPolicyBinding`
  - `InferenceAccessPolicyEvent`
  - request/response schemas named below.
- Produces REST operations:
  - `listInferenceAccessPolicies`
  - `createInferenceAccessPolicy`
  - `getInferenceAccessPolicy`
  - `patchInferenceAccessPolicy`
  - `deleteInferenceAccessPolicy`
  - `listInferenceServicePolicies`
  - `updateInferenceServicePolicies`
  - `listInferencePolicyEvents`
- Produces Make target `validate-inference-access-policy-contract`.

- [ ] **Step 1: Write the failing contract validator tests**

Add tests in `repo/scripts/validate_inference_access_policy_contract_test.py`:

```python
import unittest

import validate_inference_access_policy_contract as validator


class InferenceAccessPolicyContractTest(unittest.TestCase):
    def test_requires_policy_paths(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        paths = spec["paths"]
        self.assertIn("/inference-policies", paths)
        self.assertIn("/inference-policies/{policy_id}", paths)
        self.assertIn("/inference-services/{service_id}/policies", paths)
        self.assertIn("/inference-policy-events", paths)

    def test_service_policy_path_is_not_feature_not_available(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        operation = spec["paths"]["/inference-services/{service_id}/policies"]["put"]
        self.assertNotIn("501", operation["responses"])
        self.assertNotIn("FEATURE_NOT_AVAILABLE", operation.get("description", ""))

    def test_required_policy_schemas_exist(self) -> None:
        schemas = validator.load_spec("api/openapi/services/v1.yaml")["components"]["schemas"]
        for name in validator.REQUIRED_SCHEMAS:
            self.assertIn(name, schemas)

    def test_mutating_operations_require_idempotency_key(self) -> None:
        spec = validator.load_spec("api/openapi/services/v1.yaml")
        validator.validate_mutating_idempotency(spec)
```

- [ ] **Step 2: Run the focused validator test and capture RED**

Run:

```bash
cd repo
python3 scripts/validate_inference_access_policy_contract_test.py
```

Expected: FAIL because `validate_inference_access_policy_contract.py` and the new paths do not exist.

- [ ] **Step 3: Add the validator skeleton**

Create `repo/scripts/validate_inference_access_policy_contract.py`:

```python
#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

import yaml

REQUIRED_SCHEMAS = {
    "InferenceAccessPolicy",
    "InferenceAccessPolicyScope",
    "InferenceAccessPolicyAccess",
    "InferenceAccessPolicyRateLimits",
    "InferenceAccessPolicyConcurrency",
    "InferenceAccessPolicyBinding",
    "InferenceAccessPolicyEvent",
    "CreateInferenceAccessPolicyRequest",
    "PatchInferenceAccessPolicyRequest",
    "UpdateInferenceServicePoliciesRequest",
    "InferenceAccessPolicyListResponse",
    "InferenceAccessPolicyEventListResponse",
}

MUTATING_OPERATIONS = {
    ("post", "/inference-policies"),
    ("patch", "/inference-policies/{policy_id}"),
    ("delete", "/inference-policies/{policy_id}"),
    ("put", "/inference-services/{service_id}/policies"),
}


def load_spec(path: str) -> dict[str, Any]:
    with Path(path).open("r", encoding="utf-8") as handle:
        return yaml.safe_load(handle)


def _schema_has_idempotency(spec: dict[str, Any], schema_ref: str) -> bool:
    prefix = "#/components/schemas/"
    if not schema_ref.startswith(prefix):
        return False
    name = schema_ref[len(prefix):]
    schema = spec["components"]["schemas"][name]
    return "idempotency_key" in schema.get("required", []) and "idempotency_key" in schema.get("properties", {})


def validate_mutating_idempotency(spec: dict[str, Any]) -> None:
    for method, path in MUTATING_OPERATIONS:
        operation = spec["paths"][path][method]
        if method == "delete":
            parameters = operation.get("parameters", [])
            if any(p.get("name") == "Idempotency-Key" and p.get("in") == "header" and p.get("required") for p in parameters):
                continue
        body = operation.get("requestBody", {}).get("content", {}).get("application/json", {})
        schema_ref = body.get("schema", {}).get("$ref", "")
        if not _schema_has_idempotency(spec, schema_ref):
            raise AssertionError(f"{method.upper()} {path} must require idempotency_key")


def validate(spec: dict[str, Any]) -> None:
    schemas = spec["components"]["schemas"]
    missing = REQUIRED_SCHEMAS.difference(schemas)
    if missing:
        raise AssertionError(f"missing schemas: {sorted(missing)}")
    paths = spec["paths"]
    for path in ["/inference-policies", "/inference-policies/{policy_id}", "/inference-services/{service_id}/policies", "/inference-policy-events"]:
        if path not in paths:
            raise AssertionError(f"missing path: {path}")
    service_put = paths["/inference-services/{service_id}/policies"]["put"]
    if "501" in service_put.get("responses", {}):
        raise AssertionError("service policies must not return 501 after C42 contract")
    if "FEATURE_NOT_AVAILABLE" in service_put.get("description", ""):
        raise AssertionError("service policies description must not claim feature unavailable")
    validate_mutating_idempotency(spec)


def main() -> int:
    spec = load_spec("api/openapi/services/v1.yaml")
    validate(spec)
    print("inference access policy contract valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 4: Update Services OpenAPI schemas**

In `repo/api/openapi/services/v1.yaml`, replace the existing `InferenceServicePolicies` / `UpdateInferenceServicePoliciesRequest` reserved schemas with:

```yaml
    InferenceAccessPolicyScope:
      type: object
      required: [type]
      properties:
        type: { type: string, enum: [tenant_default, inference_service, api_key, inference_service_api_key] }
        inference_service_ids: { type: array, items: { type: string, format: uuid } }
        api_key_ids: { type: array, items: { type: string, format: uuid } }

    InferenceAccessPolicyAccess:
      type: object
      required: [allow_all_tenant_keys]
      properties:
        allow_all_tenant_keys: { type: boolean }
        allow_api_key_ids: { type: array, items: { type: string, format: uuid } }
        deny_api_key_ids: { type: array, items: { type: string, format: uuid } }

    InferenceAccessPolicyRateLimits:
      type: object
      properties:
        qps: { type: integer, minimum: 1, nullable: true }
        rpm: { type: integer, minimum: 1, nullable: true }

    InferenceAccessPolicyConcurrency:
      type: object
      properties:
        max_in_flight: { type: integer, minimum: 1, nullable: true }
        lease_ttl_seconds: { type: integer, minimum: 1, maximum: 3600, default: 60 }

    InferenceAccessPolicy:
      type: object
      required: [id, tenant_id, name, status, priority, scope, access, rate_limits, concurrency, created_at]
      properties:
        id: { type: string, format: uuid }
        tenant_id: { type: string, format: uuid }
        name: { type: string, minLength: 1, maxLength: 128 }
        status: { type: string, enum: [enabled, disabled] }
        description: { type: string, maxLength: 512, nullable: true }
        priority: { type: integer, minimum: 1, maximum: 10000 }
        scope: { $ref: '#/components/schemas/InferenceAccessPolicyScope' }
        access: { $ref: '#/components/schemas/InferenceAccessPolicyAccess' }
        rate_limits: { $ref: '#/components/schemas/InferenceAccessPolicyRateLimits' }
        concurrency: { $ref: '#/components/schemas/InferenceAccessPolicyConcurrency' }
        created_at: { type: string, format: date-time }
        updated_at: { type: string, format: date-time, nullable: true }

    CreateInferenceAccessPolicyRequest:
      type: object
      required: [idempotency_key, name, scope, access]
      properties:
        idempotency_key: { type: string, format: uuid }
        name: { type: string, minLength: 1, maxLength: 128 }
        description: { type: string, maxLength: 512, nullable: true }
        status: { type: string, enum: [enabled, disabled], default: enabled }
        priority: { type: integer, minimum: 1, maximum: 10000, default: 1000 }
        scope: { $ref: '#/components/schemas/InferenceAccessPolicyScope' }
        access: { $ref: '#/components/schemas/InferenceAccessPolicyAccess' }
        rate_limits: { $ref: '#/components/schemas/InferenceAccessPolicyRateLimits' }
        concurrency: { $ref: '#/components/schemas/InferenceAccessPolicyConcurrency' }

    PatchInferenceAccessPolicyRequest:
      type: object
      required: [idempotency_key]
      properties:
        idempotency_key: { type: string, format: uuid }
        name: { type: string, minLength: 1, maxLength: 128 }
        description: { type: string, maxLength: 512, nullable: true }
        status: { type: string, enum: [enabled, disabled] }
        priority: { type: integer, minimum: 1, maximum: 10000 }
        scope: { $ref: '#/components/schemas/InferenceAccessPolicyScope' }
        access: { $ref: '#/components/schemas/InferenceAccessPolicyAccess' }
        rate_limits: { $ref: '#/components/schemas/InferenceAccessPolicyRateLimits' }
        concurrency: { $ref: '#/components/schemas/InferenceAccessPolicyConcurrency' }

    InferenceAccessPolicyBinding:
      type: object
      required: [policy_id, inference_service_ids]
      properties:
        policy_id: { type: string, format: uuid }
        inference_service_ids: { type: array, items: { type: string, format: uuid } }

    InferenceAccessPolicyEvent:
      type: object
      required: [id, tenant_id, inference_service_id, decision, reason_code, http_status, created_at]
      properties:
        id: { type: string, format: uuid }
        tenant_id: { type: string, format: uuid }
        policy_id: { type: string, format: uuid, nullable: true }
        inference_service_id: { type: string, format: uuid }
        api_key_id: { type: string, format: uuid, nullable: true }
        key_prefix: { type: string, nullable: true }
        request_id: { type: string, nullable: true }
        openai_path: { type: string }
        external_model: { type: string, nullable: true }
        decision: { type: string, enum: [allow, deny, rate_limited, concurrency_limited, policy_unavailable] }
        reason_code: { type: string }
        http_status: { type: integer }
        retry_after_seconds: { type: integer, nullable: true }
        created_at: { type: string, format: date-time }
```

Add list/update response schemas:

```yaml
    InferenceAccessPolicyListResponse:
      type: object
      required: [items]
      properties:
        items: { type: array, items: { $ref: '#/components/schemas/InferenceAccessPolicy' } }
        next_cursor: { type: string, nullable: true }

    InferenceAccessPolicyEventListResponse:
      type: object
      required: [items]
      properties:
        items: { type: array, items: { $ref: '#/components/schemas/InferenceAccessPolicyEvent' } }
        next_cursor: { type: string, nullable: true }

    InferenceServicePolicies:
      type: object
      required: [service_id, policies]
      properties:
        service_id: { type: string, format: uuid }
        policies: { type: array, items: { $ref: '#/components/schemas/InferenceAccessPolicy' } }

    UpdateInferenceServicePoliciesRequest:
      type: object
      required: [idempotency_key, policy_ids]
      properties:
        idempotency_key: { type: string, format: uuid }
        policy_ids: { type: array, items: { type: string, format: uuid } }
```

- [ ] **Step 5: Add path operations**

Add paths under `paths:`:

Each C42 control-plane operation must follow the v1 authz format already used by Core `/admin/quota-meta`:

```yaml
security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
x-ani-authz:
  version: v1
  resource: inference_policy
  action: read | create | update | delete
  boundary: tenant
  principal_kinds: [user]
```

```yaml
  /inference-policies:
    get:
      operationId: listInferenceAccessPolicies
      summary: 查询推理访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: read
        boundary: tenant
        principal_kinds: [user]
      responses:
        "200":
          description: 推理访问策略列表
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceAccessPolicyListResponse' }
        "401": { $ref: '#/components/responses/Unauthorized' }
    post:
      operationId: createInferenceAccessPolicy
      summary: 创建推理访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: create
        boundary: tenant
        principal_kinds: [user]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/CreateInferenceAccessPolicyRequest' }
      responses:
        "201":
          description: 已创建推理访问策略
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceAccessPolicy' }
        "400": { $ref: '#/components/responses/BadRequest' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "403": { $ref: '#/components/responses/Forbidden' }
        "409": { $ref: '#/components/responses/Conflict' }
        "422": { $ref: '#/components/responses/InferenceUnprocessableEntity' }

  /inference-policies/{policy_id}:
    get:
      operationId: getInferenceAccessPolicy
      summary: 获取推理访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: read
        boundary: tenant
        principal_kinds: [user]
      parameters:
        - { name: policy_id, in: path, required: true, schema: { type: string, format: uuid } }
      responses:
        "200":
          description: 推理访问策略
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceAccessPolicy' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "404": { $ref: '#/components/responses/NotFound' }
    patch:
      operationId: patchInferenceAccessPolicy
      summary: 更新推理访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: update
        boundary: tenant
        principal_kinds: [user]
      parameters:
        - { name: policy_id, in: path, required: true, schema: { type: string, format: uuid } }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/PatchInferenceAccessPolicyRequest' }
      responses:
        "200":
          description: 已更新推理访问策略
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceAccessPolicy' }
        "400": { $ref: '#/components/responses/BadRequest' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "404": { $ref: '#/components/responses/NotFound' }
        "409": { $ref: '#/components/responses/Conflict' }
        "422": { $ref: '#/components/responses/InferenceUnprocessableEntity' }
    delete:
      operationId: deleteInferenceAccessPolicy
      summary: 删除推理访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: delete
        boundary: tenant
        principal_kinds: [user]
      parameters:
        - { name: policy_id, in: path, required: true, schema: { type: string, format: uuid } }
        - { name: Idempotency-Key, in: header, required: true, schema: { type: string, format: uuid } }
      responses:
        "204": { description: 已删除推理访问策略 }
        "400": { $ref: '#/components/responses/BadRequest' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "404": { $ref: '#/components/responses/NotFound' }
        "409": { $ref: '#/components/responses/Conflict' }
```

Replace `/inference-services/{service_id}/policies` with GET and PUT, and keep the existing path:

```yaml
  /inference-services/{service_id}/policies:
    get:
      operationId: listInferenceServicePolicies
      summary: 查询推理服务绑定的访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: read
        boundary: tenant
        principal_kinds: [user]
      parameters:
        - { name: service_id, in: path, required: true, schema: { type: string, format: uuid } }
      responses:
        "200":
          description: 推理服务策略绑定
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceServicePolicies' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "404": { $ref: '#/components/responses/NotFound' }
    put:
      operationId: updateInferenceServicePolicies
      summary: 更新推理服务绑定的访问策略
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: update
        boundary: tenant
        principal_kinds: [user]
      parameters:
        - { name: service_id, in: path, required: true, schema: { type: string, format: uuid } }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/UpdateInferenceServicePoliciesRequest' }
      responses:
        "200":
          description: 更新后的推理服务策略绑定
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceServicePolicies' }
        "400": { $ref: '#/components/responses/BadRequest' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "404": { $ref: '#/components/responses/NotFound' }
        "409": { $ref: '#/components/responses/Conflict' }
        "422": { $ref: '#/components/responses/InferenceUnprocessableEntity' }
```

Add events query:

```yaml
  /inference-policy-events:
    get:
      operationId: listInferencePolicyEvents
      summary: 查询推理访问策略命中记录
      tags: [InferenceServices]
      security: [{ BearerAuth: [] }, { ApiKeyAuth: [] }]
      x-ani-authz:
        version: v1
        resource: inference_policy
        action: read
        boundary: tenant
        principal_kinds: [user]
      parameters:
        - { name: inference_service_id, in: query, required: false, schema: { type: string, format: uuid } }
        - { name: policy_id, in: query, required: false, schema: { type: string, format: uuid } }
        - { name: api_key_id, in: query, required: false, schema: { type: string, format: uuid } }
        - { name: decision, in: query, required: false, schema: { type: string, enum: [allow, deny, rate_limited, concurrency_limited, policy_unavailable] } }
        - { name: limit, in: query, required: false, schema: { type: integer, minimum: 1, maximum: 200, default: 50 } }
        - { name: cursor, in: query, required: false, schema: { type: string } }
      responses:
        "200":
          description: 推理访问策略命中记录
          content:
            application/json:
              schema: { $ref: '#/components/schemas/InferenceAccessPolicyEventListResponse' }
        "401": { $ref: '#/components/responses/Unauthorized' }
```

- [ ] **Step 6: Register Make target and run contract gates**

Add to `repo/Makefile`:

```make
.PHONY: validate-inference-access-policy-contract
validate-inference-access-policy-contract:
	python scripts/validate_inference_access_policy_contract_test.py
	python scripts/validate_inference_access_policy_contract.py
```

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-contract
PATH=/tmp/ani-pybin:$PATH make validate-openapi-spec
PATH=/tmp/ani-pybin:$PATH make validate-services
git diff --check
```

Expected: all pass. If `make validate-services` updates generated Services schema artifacts, review the diff and include only generated contract artifacts; do not add React UI files.

- [ ] **Step 7: Contract-only review checkpoint**

Review `git status --short -uall`. The contract-only checkpoint may include:

```text
docs/superpowers/specs/2026-08-27-inference-access-policy-rate-limit-design.md
repo/api/openapi/services/v1.yaml
repo/scripts/validate_inference_access_policy_contract.py
repo/scripts/validate_inference_access_policy_contract_test.py
repo/Makefile
generated Services API artifacts produced by make validate-services
```

Stop for user confirmation before commit/push/PR because ANI requires API contract review before implementation.

---

## Phase 2: Backend implementation after contract confirmation

### Task 2: Internal proto for policy CRUD and data-plane checks

**Files:**
- Modify: `repo/api/proto/inference/control/v1/inference_control.proto`
- Modify generated: `repo/pkg/generated/pb/inference/control/v1/inference_control.pb.go`
- Modify generated: `repo/pkg/generated/pb/inference/control/v1/inference_control_grpc.pb.go`
- Modify tests: `repo/services/inference-service/internal/grpcapi/server_test.go`
- Modify tests: `repo/services/ani-gateway/internal/router/inference_grpc_client.go`

**Interfaces:**
- Adds internal RPCs to `service InferenceControl`:
  - `ListInferenceAccessPolicies`
  - `CreateInferenceAccessPolicy`
  - `GetInferenceAccessPolicy`
  - `PatchInferenceAccessPolicy`
  - `DeleteInferenceAccessPolicy`
  - `ListInferenceServicePolicies`
  - `UpdateInferenceServicePolicies`
  - `ListInferencePolicyEvents`
  - `CheckInferenceAccess`
  - `ReleaseInferenceAccessLease`
- Produces `CheckInferenceAccessResponse.Decision` values: `allow`, `deny`, `rate_limited`, `concurrency_limited`.

- [ ] **Step 1: Write compile-time RED in gRPC server tests**

In `repo/services/inference-service/internal/grpcapi/server_test.go`, add a test that calls:

```go
_, err := server.CheckInferenceAccess(context.Background(), &inferencecontrolv1.CheckInferenceAccessRequest{
	TenantId:           tenantID.String(),
	InferenceServiceId: serviceID.String(),
	ApiKeyId:           keyID.String(),
	KeyPrefix:          "ani_live",
	OpenaiPath:         "/v1/chat/completions",
	ExternalModel:      "svc-model",
	RequestId:          "req-c42",
})
```

Expected decision in the fake service path: `allow`.

- [ ] **Step 2: Run focused RED**

Run:

```bash
cd repo/services/inference-service
GOCACHE=/tmp/ani-go-cache go test ./internal/grpcapi -run TestCheckInferenceAccess -count=1
```

Expected: compile FAIL because proto messages and server method do not exist.

- [ ] **Step 3: Add proto messages and RPCs**

Append these messages to `repo/api/proto/inference/control/v1/inference_control.proto` using new field numbers and stable snake_case names:

```proto
message InferenceAccessPolicy {
  string id = 1;
  string tenant_id = 2;
  string name = 3;
  string status = 4;
  string description = 5;
  int32 priority = 6;
  InferenceAccessPolicyScope scope = 7;
  InferenceAccessPolicyAccess access = 8;
  InferenceAccessPolicyRateLimits rate_limits = 9;
  InferenceAccessPolicyConcurrency concurrency = 10;
  google.protobuf.Timestamp created_at = 11;
  google.protobuf.Timestamp updated_at = 12;
}

message InferenceAccessPolicyScope {
  string type = 1;
  repeated string inference_service_ids = 2;
  repeated string api_key_ids = 3;
}

message InferenceAccessPolicyAccess {
  bool allow_all_tenant_keys = 1;
  repeated string allow_api_key_ids = 2;
  repeated string deny_api_key_ids = 3;
}

message InferenceAccessPolicyRateLimits {
  int32 qps = 1;
  int32 rpm = 2;
}

message InferenceAccessPolicyConcurrency {
  int32 max_in_flight = 1;
  int32 lease_ttl_seconds = 2;
}
```

Add request/response messages matching the OpenAPI names. For nullable integers, use `0` as unset in proto and keep public REST validation responsible for rejecting explicit non-positive values.

- [ ] **Step 4: Generate protobuf Go code**

Run:

```bash
cd repo
make gen-proto
```

Expected: generated `inference_control.pb.go` and `inference_control_grpc.pb.go` change; no generated old `inference/v1` operator path is revived.

- [ ] **Step 5: Add generated-boundary validation**

Run:

```bash
cd repo
python3 scripts/validate_inference_legacy_control_plane.py
go test ./services/inference-service/internal/grpcapi -run TestCheckInferenceAccess -count=1
```

Expected: legacy validator passes; focused gRPC test now fails on unimplemented server method instead of missing types.

### Task 3: Migration and repository contract

**Files:**
- Create: `repo/deploy/migrations/20260827_001_inference_access_policy.sql`
- Create: `repo/scripts/validate_inference_access_policy_migration.py`
- Create: `repo/scripts/validate_inference_access_policy_migration_test.py`
- Modify: `repo/Makefile`
- Modify: `repo/services/inference-service/internal/repository/store.go`
- Modify: `repo/services/inference-service/internal/repository/postgres.go`
- Modify: `repo/services/inference-service/internal/repository/postgres_test.go`

**Interfaces:**
- Produces tables:
  - `inference_access_policies`
  - `inference_access_policy_services`
  - `inference_access_policy_api_keys`
  - `inference_access_policy_events`
- Extends repository with `AccessPolicyStore`.

- [ ] **Step 1: Write failing migration validator tests**

Create `repo/scripts/validate_inference_access_policy_migration_test.py` with assertions that the migration contains:

```python
required_tables = [
    "CREATE TABLE IF NOT EXISTS inference_access_policies",
    "CREATE TABLE IF NOT EXISTS inference_access_policy_services",
    "CREATE TABLE IF NOT EXISTS inference_access_policy_api_keys",
    "CREATE TABLE IF NOT EXISTS inference_access_policy_events",
]
required_rls = [
    "ALTER TABLE inference_access_policies ENABLE ROW LEVEL SECURITY",
    "ALTER TABLE inference_access_policy_events FORCE ROW LEVEL SECURITY",
    "current_setting('ani.tenant_id'",
]
for item in required_tables + required_rls:
    self.assertIn(item, migration)
```

- [ ] **Step 2: Run migration RED**

Run:

```bash
cd repo
python3 scripts/validate_inference_access_policy_migration_test.py
```

Expected: FAIL because migration and validator do not exist.

- [ ] **Step 3: Add idempotent migration**

Create `repo/deploy/migrations/20260827_001_inference_access_policy.sql` with:

```sql
CREATE TABLE IF NOT EXISTS inference_access_policies (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('enabled', 'disabled')),
    description TEXT,
    priority INTEGER NOT NULL DEFAULT 1000 CHECK (priority >= 1 AND priority <= 10000),
    scope_type TEXT NOT NULL CHECK (scope_type IN ('tenant_default', 'inference_service', 'api_key', 'inference_service_api_key')),
    allow_all_tenant_keys BOOLEAN NOT NULL DEFAULT true,
    rate_qps INTEGER CHECK (rate_qps IS NULL OR rate_qps >= 1),
    rate_rpm INTEGER CHECK (rate_rpm IS NULL OR rate_rpm >= 1),
    max_in_flight INTEGER CHECK (max_in_flight IS NULL OR max_in_flight >= 1),
    lease_ttl_seconds INTEGER NOT NULL DEFAULT 60 CHECK (lease_ttl_seconds >= 1 AND lease_ttl_seconds <= 3600),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_inference_access_policies_active_name
    ON inference_access_policies(tenant_id, lower(name))
    WHERE deleted_at IS NULL;
```

Add service/API-key join tables and event table with `tenant_id` on every row, `deleted_at` soft delete for policies, 7-day event query index, and RLS policies using `current_setting('ani.tenant_id', true)::uuid`.

- [ ] **Step 4: Add repository interface**

In `repo/services/inference-service/internal/repository/store.go`, add:

```go
type AccessPolicyStore interface {
	ListAccessPolicies(context.Context, uuid.UUID) ([]domain.AccessPolicy, error)
	CreateAccessPolicy(context.Context, domain.AccessPolicy, uuid.UUID, string) (domain.AccessPolicy, error)
	GetAccessPolicy(context.Context, uuid.UUID, uuid.UUID) (domain.AccessPolicy, error)
	PatchAccessPolicy(context.Context, domain.AccessPolicyPatch, uuid.UUID, string) (domain.AccessPolicy, error)
	DeleteAccessPolicy(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, time.Time) error
	ListServicePolicies(context.Context, uuid.UUID, uuid.UUID) ([]domain.AccessPolicy, error)
	UpdateServicePolicies(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID, uuid.UUID, string) ([]domain.AccessPolicy, error)
	ListPolicyEvents(context.Context, domain.AccessPolicyEventFilter) ([]domain.AccessPolicyEvent, string, error)
	RecordPolicyEvent(context.Context, domain.AccessPolicyEvent) error
}
```

- [ ] **Step 5: Write repository RED tests**

In `repo/services/inference-service/internal/repository/postgres_test.go`, add tests for:

```go
func TestAccessPolicyRepositoryAppliesTenantIsolation(t *testing.T)
func TestAccessPolicyRepositoryCreateReplayUsesIdempotency(t *testing.T)
func TestAccessPolicyRepositorySoftDeleteHidesPolicy(t *testing.T)
func TestAccessPolicyRepositoryRecordsOnlyRedactedEventFields(t *testing.T)
```

Use existing repository test style and assert no event field contains `"ani_"`, `"Authorization"`, prompt content, or embedding text.

- [ ] **Step 6: Implement PostgreSQL store methods**

Implement the `AccessPolicyStore` methods in `repo/services/inference-service/internal/repository/postgres.go` using existing `setTenant(ctx, tx, tenantID)` before every tenant-scoped query. Use advisory lock key:

```go
lockKey := tenantID.String() + "/inference_access_policy/" + idempotencyKey.String()
```

For create/patch/delete/update bindings, store `request_hash` in the existing operation/idempotency pattern only if an existing generic operation table is suitable; otherwise use `inference_access_policy_idempotency` in the new migration with columns `tenant_id`, `scope`, `idempotency_key`, `request_hash`, `response_json`, `created_at`.

- [ ] **Step 7: Run migration and repository tests**

Run:

```bash
cd repo
python3 scripts/validate_inference_access_policy_migration_test.py
python3 scripts/validate_inference_access_policy_migration.py
PATH=/tmp/ani-pybin:$PATH make validate-inference-control-plane-migration
GOCACHE=/tmp/ani-go-cache go test ./services/inference-service/internal/repository -count=1
git diff --check
```

Expected: all pass locally; PostgreSQL integration tests that require `INFERENCE_TEST_DATABASE_URL` may skip if the environment variable is absent, matching existing test convention.

### Task 4: Policy domain and checker service

**Files:**
- Create: `repo/services/inference-service/internal/domain/access_policy.go`
- Create: `repo/services/inference-service/internal/domain/access_policy_test.go`
- Create: `repo/services/inference-service/internal/service/access_policy.go`
- Create: `repo/services/inference-service/internal/service/access_policy_test.go`
- Modify: `repo/services/inference-service/internal/service/errors.go`

**Interfaces:**
- Produces:
  - `type AccessPolicy`
  - `type AccessPolicyScope`
  - `type AccessPolicyAccess`
  - `type AccessPolicyRateLimits`
  - `type AccessPolicyConcurrency`
  - `type AccessCheckInput`
  - `type AccessDecision`
  - `type AccessPolicyService`
  - `func NewAccessPolicyService(store repository.AccessPolicyStore, limiter RateLimiter, now func() time.Time) *AccessPolicyService`
- Produces decision values: `allow`, `deny`, `rate_limited`, `concurrency_limited`.

- [ ] **Step 1: Write domain RED tests**

Add tests:

```go
func TestAccessPolicyValidationRejectsInvalidScope(t *testing.T)
func TestAccessPolicyValidationRejectsClientSuppliedRawAPIKey(t *testing.T)
func TestAccessPolicyMatchPriorityOrder(t *testing.T)
func TestAccessPolicyDefaultAllowsTenantKeyWithoutCustomPolicy(t *testing.T)
```

The raw AK rejection test should pass strings with prefix `ani_` in `AllowAPIKeyIDs` and expect validation error `ErrRawAPIKeyRejected`.

- [ ] **Step 2: Run domain RED**

Run:

```bash
cd repo/services/inference-service
GOCACHE=/tmp/ani-go-cache go test ./internal/domain -run AccessPolicy -count=1
```

Expected: FAIL because domain types do not exist.

- [ ] **Step 3: Implement domain types and validation**

Create `access_policy.go` with explicit enums:

```go
type AccessPolicyStatus string
const (
	AccessPolicyEnabled AccessPolicyStatus = "enabled"
	AccessPolicyDisabled AccessPolicyStatus = "disabled"
)

type AccessPolicyScopeType string
const (
	ScopeTenantDefault AccessPolicyScopeType = "tenant_default"
	ScopeInferenceService AccessPolicyScopeType = "inference_service"
	ScopeAPIKey AccessPolicyScopeType = "api_key"
	ScopeInferenceServiceAPIKey AccessPolicyScopeType = "inference_service_api_key"
)
```

Validation rules:

- status must be `enabled` or `disabled`;
- priority must be 1..10000;
- `api_key_ids` and allow/deny IDs must parse as UUID;
- any value with prefix `ani_` is rejected;
- service IDs must parse as UUID;
- disabled policies are valid but never match.

- [ ] **Step 4: Write service RED tests**

In `repo/services/inference-service/internal/service/access_policy_test.go`, use an in-memory fake store and fake limiter. Cover:

```go
func TestCheckAccessAllowsWithoutCustomPolicy(t *testing.T)
func TestCheckAccessReturnsDenyForSameTenantAllowlistMiss(t *testing.T)
func TestCheckAccessReturnsRateLimitedWithRetryAfter(t *testing.T)
func TestCheckAccessReturnsConcurrencyLimitedWithLeaseTTL(t *testing.T)
func TestCheckAccessRecordsRedactedDenyAndLimitEvents(t *testing.T)
func TestReleaseAccessLeaseIsBestEffort(t *testing.T)
```

- [ ] **Step 5: Implement service and limiter interface**

In `access_policy.go`, implement:

```go
type RateLimiter interface {
	AllowFixedWindow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (allowed bool, retryAfter time.Duration, err error)
	AcquireLease(ctx context.Context, key string, limit int, ttl time.Duration, now time.Time) (leaseID string, allowed bool, retryAfter time.Duration, err error)
	ReleaseLease(ctx context.Context, leaseID string) error
}
```

The checker key format must be:

```text
tenant_id + "/" + inference_service_id + "/" + api_key_id + "/" + policy_id
```

The key must never include the raw AK, Authorization header, prompt, completion, embedding input, or response.

- [ ] **Step 6: Run service/domain tests**

Run:

```bash
cd repo/services/inference-service
GOCACHE=/tmp/ani-go-cache go test ./internal/domain ./internal/service -run AccessPolicy -count=1
```

Expected: PASS.

### Task 5: inference-service gRPC implementation

**Files:**
- Modify: `repo/services/inference-service/internal/grpcapi/server.go`
- Modify: `repo/services/inference-service/internal/grpcapi/convert.go`
- Modify: `repo/services/inference-service/internal/grpcapi/server_test.go`
- Modify: `repo/services/inference-service/internal/grpcapi/convert_test.go`
- Modify: `repo/services/inference-service/main.go`

**Interfaces:**
- `grpcapi.NewServer(creator, controller).WithAccessPolicies(policyService)` injects C42 service.
- gRPC errors map:
  - invalid UUID/request: `codes.InvalidArgument`
  - not found/cross tenant: `codes.NotFound`
  - policy deny decision remains successful Check response with `decision=deny`, `http_status=403`
  - limiter backend unavailable: `codes.Unavailable`

- [ ] **Step 1: Write gRPC RED tests**

Add:

```go
func TestCreateInferenceAccessPolicyDelegatesTenantFromRequest(t *testing.T)
func TestUpdateInferenceServicePoliciesDelegatesBinding(t *testing.T)
func TestCheckInferenceAccessMapsDenyAndRateLimit(t *testing.T)
func TestCheckInferenceAccessRejectsMissingAPIKeyID(t *testing.T)
func TestListInferencePolicyEventsDoesNotExposeSensitiveFields(t *testing.T)
```

- [ ] **Step 2: Run gRPC RED**

Run:

```bash
cd repo/services/inference-service
GOCACHE=/tmp/ani-go-cache go test ./internal/grpcapi -run 'InferenceAccessPolicy|CheckInferenceAccess|InferencePolicyEvents' -count=1
```

Expected: FAIL due missing server methods/converters.

- [ ] **Step 3: Implement converters**

Add conversion functions:

```go
func accessPolicyToProto(value domain.AccessPolicy) *inferencecontrolv1.InferenceAccessPolicy
func accessPolicyFromCreate(req *inferencecontrolv1.CreateInferenceAccessPolicyRequest) (domain.AccessPolicy, error)
func accessPolicyPatchFromProto(req *inferencecontrolv1.PatchInferenceAccessPolicyRequest) (domain.AccessPolicyPatch, error)
func accessCheckInputFromProto(req *inferencecontrolv1.CheckInferenceAccessRequest) (service.AccessCheckInput, error)
func accessDecisionToProto(value service.AccessDecision) *inferencecontrolv1.CheckInferenceAccessResponse
func accessPolicyEventToProto(value domain.AccessPolicyEvent) *inferencecontrolv1.InferenceAccessPolicyEvent
```

- [ ] **Step 4: Implement server methods**

Implement all C42 RPC methods in `server.go`. Each method must parse tenant and resource UUIDs server-side. For `CheckInferenceAccess`, require:

```go
tenant_id != ""
inference_service_id != ""
api_key_id != ""
openai_path in ["/v1/chat/completions", "/v1/embeddings"]
```

- [ ] **Step 5: Wire main.go**

In `repo/services/inference-service/main.go`, instantiate the policy service using the existing PostgreSQL store. For the first local implementation, use an in-process fixed-window limiter if no Redis/cache port exists in this service. Keep the limiter behind the `RateLimiter` interface so Redis can be swapped in without changing gRPC or REST contracts.

- [ ] **Step 6: Run inference-service gates**

Run:

```bash
cd repo/services/inference-service
GOCACHE=/tmp/ani-go-cache go test ./...
GOCACHE=/tmp/ani-go-cache go test -race ./...
go vet ./...
```

Expected: all pass.

### Task 6: ANI Gateway REST handlers for backend policy APIs

**Files:**
- Modify: `repo/services/ani-gateway/internal/router/inference_grpc_client.go`
- Modify: `repo/services/ani-gateway/internal/router/inference_resources.go`
- Modify: `repo/services/ani-gateway/internal/router/inference_resources_test.go`

**Interfaces:**
- Extends `InferenceControlClient` with policy methods matching Task 2 proto.
- Exposes Services REST operations from Task 1.
- Removes the `FEATURE_NOT_AVAILABLE` behavior from `updateInferenceServicePolicies`.

- [ ] **Step 1: Write REST RED tests**

Add tests:

```go
func TestInferenceAccessPolicyCRUDDelegatesTenant(t *testing.T)
func TestInferenceServicePoliciesNoLongerReturn501(t *testing.T)
func TestInferencePolicyEventsListUsesTenantContext(t *testing.T)
func TestDeleteInferenceAccessPolicyRequiresIdempotencyHeader(t *testing.T)
func TestInferenceAccessPolicyRejectsRawAPIKeyInRequest(t *testing.T)
```

The raw AK test sends `{"allow_api_key_ids":["ani_live_secret"]}` and expects `400 INVALID_ARGUMENT`.

- [ ] **Step 2: Run REST RED**

Run:

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'InferenceAccessPolicy|InferenceServicePolicies|InferencePolicyEvents' -count=1
```

Expected: FAIL because handlers and client methods are missing or `/policies` still returns 501.

- [ ] **Step 3: Extend Gateway gRPC client**

Add methods to `InferenceControlClient` and `inferenceGRPCClient`:

```go
ListInferenceAccessPolicies(ctx context.Context, tenantID string) (*inferencecontrolv1.ListInferenceAccessPoliciesResponse, error)
CreateInferenceAccessPolicy(ctx context.Context, tenantID string, req *inferencecontrolv1.CreateInferenceAccessPolicyRequest) (*inferencecontrolv1.InferenceAccessPolicy, error)
GetInferenceAccessPolicy(ctx context.Context, tenantID, policyID string) (*inferencecontrolv1.InferenceAccessPolicy, error)
PatchInferenceAccessPolicy(ctx context.Context, tenantID, policyID string, req *inferencecontrolv1.PatchInferenceAccessPolicyRequest) (*inferencecontrolv1.InferenceAccessPolicy, error)
DeleteInferenceAccessPolicy(ctx context.Context, tenantID, policyID, idempotencyKey string) error
ListInferenceServicePolicies(ctx context.Context, tenantID, serviceID string) (*inferencecontrolv1.InferenceServicePoliciesResponse, error)
UpdateInferenceServicePolicies(ctx context.Context, tenantID, serviceID string, req *inferencecontrolv1.UpdateInferenceServicePoliciesRequest) (*inferencecontrolv1.InferenceServicePoliciesResponse, error)
ListInferencePolicyEvents(ctx context.Context, tenantID string, req *inferencecontrolv1.ListInferencePolicyEventsRequest) (*inferencecontrolv1.ListInferencePolicyEventsResponse, error)
```

- [ ] **Step 4: Register REST routes**

In `registerInferenceServices`, add:

```go
svc.GET("/inference-policies", listInferenceAccessPolicies)
svc.POST("/inference-policies", createInferenceAccessPolicy)
svc.GET("/inference-policies/:policy_id", getInferenceAccessPolicy)
svc.PATCH("/inference-policies/:policy_id", patchInferenceAccessPolicy)
svc.DELETE("/inference-policies/:policy_id", deleteInferenceAccessPolicy)
svc.GET("/inference-policy-events", listInferencePolicyEvents)
svc.GET("/inference-services/:service_id/policies", listInferenceServicePolicies)
svc.PUT("/inference-services/:service_id/policies", updateInferenceServicePolicies)
```

Keep the existing `/inference-services/:service_id/policies` PUT path name and replace the 501 body with a gRPC delegation.

- [ ] **Step 5: Implement request/response JSON mapping**

Use request structs local to `inference_resources.go`:

```go
type createInferenceAccessPolicyRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	Priority       int32  `json:"priority"`
	Scope          accessPolicyScopeRequest `json:"scope"`
	Access         accessPolicyAccessRequest `json:"access"`
	RateLimits     accessPolicyRateLimitsRequest `json:"rate_limits"`
	Concurrency    accessPolicyConcurrencyRequest `json:"concurrency"`
}
```

Reject raw AK-shaped values in all `api_key_ids` fields before calling gRPC:

```go
func looksLikeRawAPIKey(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "ani_")
}
```

- [ ] **Step 6: Run Gateway gates**

Run:

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'InferenceAccessPolicy|InferenceServicePolicies|InferencePolicyEvents' -count=1
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/...
go vet ./services/ani-gateway/...
git diff --check
```

Expected: all pass.

### Task 7: envoy-authz-adapter policy check integration

**Files:**
- Modify: `repo/services/envoy-authz-adapter/internal/authclient/client.go`
- Modify: `repo/services/envoy-authz-adapter/internal/authclient/client_test.go`
- Create: `repo/services/envoy-authz-adapter/internal/policyclient/client.go`
- Create: `repo/services/envoy-authz-adapter/internal/policyclient/client_test.go`
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server.go`
- Modify: `repo/services/envoy-authz-adapter/internal/extauth/server_test.go`
- Modify: `repo/services/envoy-authz-adapter/internal/config/config.go`
- Modify: `repo/services/envoy-authz-adapter/internal/config/config_test.go`
- Modify: `repo/services/envoy-authz-adapter/main.go`

**Interfaces:**
- `authclient.Client.ValidatePrincipal(ctx, rawAK)` calls `auth.v1.AuthService/ValidatePrincipal` with `credential_scheme="api_key"`.
- `policyclient.Client.CheckInferenceAccess(ctx, policyclient.CheckRequest)` calls inference-service.
- `extauth.New(authValidator, policyChecker)` returns a server that enforces both auth and policy.

- [ ] **Step 1: Write authclient V2 RED tests**

Update `client_test.go` to assert:

```go
func TestValidatePrincipalUsesAPIKeyScheme(t *testing.T)
func TestValidatePrincipalReturnsCredentialID(t *testing.T)
func TestValidatePrincipalUsesBoundedContext(t *testing.T)
```

Expected fake request:

```go
&authv1.ValidatePrincipalRequest{
	Credential: "ani_live_secret",
	CredentialScheme: "api_key",
}
```

- [ ] **Step 2: Run authclient RED**

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go test ./internal/authclient -count=1
```

Expected: FAIL because current client only exposes `ValidateToken`.

- [ ] **Step 3: Implement V2 authclient**

Add:

```go
func (c *Client) ValidatePrincipal(ctx context.Context, token string) (*authv1.PrincipalContext, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.rpc.ValidatePrincipal(callCtx, &authv1.ValidatePrincipalRequest{
		Credential: token,
		CredentialScheme: "api_key",
	})
}
```

Keep `ValidateToken` only if existing tests or rollback code still use it; ext_authz C42 path must use `ValidatePrincipal`.

- [ ] **Step 4: Write policyclient and extauth RED tests**

Add tests:

```go
func TestCheckAllowsWhenAuthAndPolicyAllow(t *testing.T)
func TestCheckDeniedWhenPolicyDeniesSameTenant(t *testing.T)
func TestCheckReturns429WithRetryAfterForRateLimit(t *testing.T)
func TestCheckReturns429WithRetryAfterForConcurrencyLimit(t *testing.T)
func TestCheckReturns503WhenPolicyClientUnavailable(t *testing.T)
func TestCheckReturns404ForTenantMismatchBeforePolicyCall(t *testing.T)
func TestCheckPassesServiceModelPathAndRequestIDToPolicy(t *testing.T)
func TestCheckRemovesSensitiveHeadersOnAllow(t *testing.T)
```

The policy fake must receive `api_key_id` from `PrincipalContext.CredentialId`; it must not receive the raw AK.

- [ ] **Step 5: Implement policyclient**

Create `repo/services/envoy-authz-adapter/internal/policyclient/client.go`:

```go
type CheckRequest struct {
	TenantID string
	UserID string
	APIKeyID string
	KeyPrefix string
	InferenceServiceID string
	ExternalModel string
	OpenAIPath string
	RequestID string
	Stream bool
}

type Decision struct {
	Decision string
	HTTPStatus int
	ReasonCode string
	PolicyID string
	LeaseID string
	RetryAfterSeconds int
}
```

Map inference-service gRPC `Unavailable` and `DeadlineExceeded` to adapter 503.

- [ ] **Step 6: Update extauth server**

Current trusted context keys:

```go
const (
	targetTenantKey  = "ani.target_tenant_id"
	targetServiceKey = "ani.inference_service_id"
)
```

Add:

```go
const (
	targetModelKey = "ani.external_model"
)
```

In `Check`:

1. Extract Bearer raw `ani_*` AK.
2. Call `ValidatePrincipal`.
3. Require `principal_kind=api_key`, `credential_scheme=api_key`, `credential_domain=tenant`, non-empty `tenant_id`, non-empty `credential_id`.
4. Compare principal tenant with route target tenant; mismatch returns 404.
5. Call `CheckInferenceAccess`.
6. Map decisions:
   - allow -> `codes.OK`
   - deny + 403 -> denied 403
   - rate/concurrency -> denied 429 and set `Retry-After`
   - policy unavailable -> denied 503
7. Remove `authorization`, `x-api-key`, `x-ani-tenant-id`, `x-ani-user-id`.

- [ ] **Step 7: Add adapter config**

Extend config:

```go
type Config struct {
	GRPCPort int
	AuthServiceGRPCAddr string
	AuthTimeout time.Duration
	InferencePolicyGRPCAddr string
	PolicyTimeout time.Duration
}
```

Required env:

```text
AUTH_SERVICE_GRPC_ADDR
INFERENCE_POLICY_GRPC_ADDR
```

Defaults:

```text
GRPC_PORT=9002
AUTH_TIMEOUT=2s
POLICY_TIMEOUT=2s
```

- [ ] **Step 8: Run adapter gates**

Run:

```bash
cd repo/services/envoy-authz-adapter
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Expected: all pass.

### Task 8: Deployment manifest and contract live gate updates

**Files:**
- Modify: `repo/deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml`
- Create: `repo/deploy/real-k8s-lab/inference-access-policy-live-gate.yaml`
- Create: `repo/scripts/validate_inference_access_policy_live_gate.py`
- Create: `repo/scripts/validate_inference_access_policy_live_gate_test.py`
- Modify: `repo/Makefile`

**Interfaces:**
- Adds adapter env:
  - `INFERENCE_POLICY_GRPC_ADDR=inference-service.ani-system.svc.cluster.local:9105`
  - `POLICY_TIMEOUT=2s`
- Produces Make target `validate-inference-access-policy-live-gate`.

- [ ] **Step 1: Write live gate validator RED**

Create tests asserting the C42 live-gate yaml requires:

```python
required_checks = {
    "no_ak_chat_401",
    "no_ak_embeddings_401",
    "valid_ak_default_policy_chat_success",
    "valid_ak_default_policy_embeddings_success",
    "allowlist_key_success",
    "allowlist_miss_403",
    "cross_tenant_hidden_404",
    "qps_or_rpm_429",
    "concurrency_429",
    "policy_backend_down_503",
    "policy_events_redacted",
}
```

- [ ] **Step 2: Run live gate RED**

Run:

```bash
cd repo
python3 scripts/validate_inference_access_policy_live_gate_test.py
```

Expected: FAIL because validator and yaml do not exist.

- [ ] **Step 3: Update manifest and live gate contract**

In `inference-envoy-ai-gateway-c40.yaml`, add only the new adapter env vars and keep existing SecurityPolicy ext_authz target:

```yaml
- name: INFERENCE_POLICY_GRPC_ADDR
  value: inference-service.ani-system.svc.cluster.local:9105
- name: POLICY_TIMEOUT
  value: 2s
```

Create `repo/deploy/real-k8s-lab/inference-access-policy-live-gate.yaml` with the required check names above and evidence redaction rules:

```yaml
redaction:
  forbidden_substrings:
    - "Authorization"
    - "Bearer "
    - "ani_"
    - "prompt"
    - "embedding input"
```

- [ ] **Step 4: Register Make target**

Add:

```make
.PHONY: validate-inference-access-policy-live-gate
validate-inference-access-policy-live-gate:
	python scripts/validate_inference_access_policy_live_gate_test.py
	python scripts/validate_inference_access_policy_live_gate.py
```

- [ ] **Step 5: Run static live-gate validation**

Run:

```bash
cd repo
PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-live-gate
python3 scripts/validate_inference_envoy_ai_gateway_manifest.py deploy/real-k8s-lab/inference-envoy-ai-gateway-c40.yaml
git diff --check
```

Expected: all pass. This task does not apply manifests or create API keys.

### Task 9: Real Envoy AI Gateway runner after explicit live approval

**Files:**
- Create: `repo/scripts/run_inference_access_policy_live.py`
- Create evidence during live run only: `repo/development-records/live-evidence/inference-access-policy-live-YYYYMMDD.json`

**Interfaces:**
- Runner consumes:
  - `ANI_C42_OWNER_ACCESS_TOKEN`
  - `ANI_C42_FOREIGN_ACCESS_TOKEN`
  - `ANI_C42_GATEWAY_URL`
  - `KUBECONFIG`
- Runner creates temporary AKs via Services/Auth API, creates temporary policy resources through ANI Gateway, calls Envoy AI Gateway with AKs, and leaves inference service workloads in place unless the user authorizes cleanup.

- [ ] **Step 1: Write import-safe runner tests**

Runner test cases:

```python
def test_runner_requires_explicit_live_flag()
def test_runner_refuses_to_print_tokens_or_api_keys()
def test_runner_uses_envoy_gateway_url_not_clusterip()
def test_runner_records_chat_and_embeddings_io_summary_without_prompt()
def test_runner_leaves_services_when_keep_services_is_set()
```

- [ ] **Step 2: Run runner RED**

Run:

```bash
cd repo
python3 scripts/run_inference_access_policy_live_test.py
```

Expected: FAIL because runner does not exist.

- [ ] **Step 3: Implement runner**

Create `repo/scripts/run_inference_access_policy_live.py` with:

```python
def require_live_flag(args: argparse.Namespace) -> None:
    if not args.live:
        raise SystemExit("--live is required for C42 real gateway validation")

def require_envoy_url(url: str) -> None:
    if ".svc" in url or "cluster.local" in url or url.startswith("http://10."):
        raise SystemExit("ANI_C42_GATEWAY_URL must be the external Envoy AI Gateway URL")
```

The runner must:

1. create owner and foreign temporary AKs through authorized API calls;
2. call Envoy AI Gateway `/v1/chat/completions` and `/v1/embeddings`;
3. create allowlist, deny, low RPM/QPS, and concurrency policies through `/api/v1/svc/inference-policies`;
4. bind policy through `/api/v1/svc/inference-services/{service_id}/policies`;
5. verify expected HTTP statuses;
6. verify policy events are queryable and redacted;
7. revoke temporary AKs at the end;
8. keep inference service workloads when `--keep-services` is set.

- [ ] **Step 4: Run local runner tests**

Run:

```bash
cd repo
python3 scripts/run_inference_access_policy_live_test.py
python3 -m py_compile scripts/run_inference_access_policy_live.py
git diff --check
```

Expected: PASS.

- [ ] **Step 5: Execute live runner only after user approval**

After explicit approval and real tokens are provided, run:

```bash
cd repo
ANI_C42_OWNER_ACCESS_TOKEN="$OWNER_TOKEN" \
ANI_C42_FOREIGN_ACCESS_TOKEN="$FOREIGN_TOKEN" \
ANI_C42_GATEWAY_URL="$ENVOY_GATEWAY_URL" \
KUBECONFIG="$KUBECONFIG" \
python3 scripts/run_inference_access_policy_live.py --live --keep-services
```

Expected evidence summary:

```json
{
  "status": "passed",
  "data_plane": "envoy_ai_gateway",
  "checks": {
    "no_ak_chat_401": "passed",
    "no_ak_embeddings_401": "passed",
    "valid_ak_default_policy_chat_success": "passed",
    "valid_ak_default_policy_embeddings_success": "passed",
    "allowlist_key_success": "passed",
    "allowlist_miss_403": "passed",
    "cross_tenant_hidden_404": "passed",
    "qps_or_rpm_429": "passed",
    "concurrency_429": "passed",
    "policy_backend_down_503": "passed",
    "policy_events_redacted": "passed"
  }
}
```

### Task 10: Development record and final gates

**Files:**
- Create: `repo/development-records/INFERENCE-SERVICE-ACCESS-POLICY-C42.md`
- Modify: `repo/development-records/README.md`
- Modify: `repo/CURRENT-SPRINT.md`
- Modify: `ANI-06-开发计划.md`

**Interfaces:**
- Records C42 as `local/logic verified` until Task 9 live evidence passes.
- Records C42 as Envoy AI Gateway live verified only if Task 9 produces redacted passed evidence.

- [ ] **Step 1: Write development record**

Create `repo/development-records/INFERENCE-SERVICE-ACCESS-POLICY-C42.md` with:

```markdown
# INFERENCE-SERVICE-ACCESS-POLICY-C42

## 状态

- contract: passed
- local/logic: passed
- live: pending_or_passed_based_on_evidence

## 范围

- Services API 推理访问策略
- inference-service 策略存储、匹配、限流、并发 lease、事件
- ani-gateway 后端 API
- envoy-authz-adapter 数据面策略检查

## 不包含

- Console 前端页面或组件
- token 计费、余额扣减、账单
- prompt/completion/embedding 内容审计

## 验证命令

列出本批真实执行且通过的命令。

## 敏感信息处理

记录 evidence/log 没有 AK 原文、Authorization、prompt、completion、embedding 输入文本或向量内容。
```

- [ ] **Step 2: Update progress indexes**

Update:

- `repo/development-records/README.md`
- `repo/CURRENT-SPRINT.md`
- `ANI-06-开发计划.md`

Use status language consistent with actual verification result:

```text
INFERENCE-SERVICE-ACCESS-POLICY-C42 已完成 local/logic verified
```

Only use `live passed` after Task 9 real Envoy AI Gateway evidence passes.

- [ ] **Step 3: Run local CI-equivalent gates**

Run from `repo/`:

```bash
PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-contract
PATH=/tmp/ani-pybin:$PATH make validate-inference-access-policy-live-gate
PATH=/tmp/ani-pybin:$PATH make validate-services
PATH=/tmp/ani-pybin:$PATH make test
PATH=/tmp/ani-pybin:$PATH make validate-architecture
PATH=/tmp/ani-pybin:$PATH make validate-doc-entrypoints
git diff --check
```

Run module-specific gates:

```bash
cd repo/services/inference-service
GOCACHE=/tmp/ani-go-cache go test ./...
GOCACHE=/tmp/ani-go-cache go test -race ./...
go vet ./...

cd ../envoy-authz-adapter
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

Expected: all pass.

- [ ] **Step 4: Pre-commit synchronization gate**

Only after user explicitly authorizes commit:

```bash
cd /root/kubercon/ANI
git fetch upstream main
git status --short -uall
git branch --show-current
```

If local `main` is behind `upstream/main`, merge `upstream/main`, regenerate artifacts, rerun the gates in Step 3, then commit with a Conventional Commits message such as:

```text
feat(services): add inference access policy backend
```

Do not push or open PR until the user explicitly authorizes those actions.

---

## Success Criteria

- Services OpenAPI exposes the backend policy APIs and no longer advertises `/inference-services/{service_id}/policies` as 501.
- ANI Gateway policy REST handlers always derive tenant from auth context and never trust JSON tenant fields.
- inference-service persists tenant-isolated policies, bindings, events, and implements deterministic policy matching.
- envoy-authz-adapter uses auth-service `ValidatePrincipal` for API keys, passes only `credential_id`/tenant/service/path/model/request metadata to policy checks, and strips sensitive headers before vLLM.
- Valid AK + no custom policy can call chat and embeddings through Envoy AI Gateway.
- Policy allowlist/deny/rate/concurrency decisions produce 403/429/503 with redacted events.
- Local gates pass; live status is only upgraded after real Envoy AI Gateway evidence passes.

## Self-Review

- Spec coverage: C42 P0 policy resource, binding, API-key scope, service scope, tenant default, QPS/RPM, concurrency, events, chat/embeddings, fail closed, 404 cross-tenant hiding, and no frontend implementation are covered by Tasks 1-10.
- Type consistency: OpenAPI, proto, domain, Gateway REST, and adapter use `api_key_id` as UUID credential ID; raw `ani_*` values are only accepted as Authorization Bearer input at ext_authz/auth-service boundary.
- API-first: Task 1 is contract-only and stops for confirmation before Phase 2 implementation.
