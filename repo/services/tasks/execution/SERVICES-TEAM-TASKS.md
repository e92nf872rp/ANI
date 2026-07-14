# SERVICES TEAM 开发任务清单

> **Phase 3 对齐**：2026-06-17  
> **契约来源**：`docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md`（Services active +12 ops，+ schema/422 修补）  
> **总任务数**：16 | 待开始：13 | 进行中：0 | 已完成：3

> **当前实现状态：** 本文件是 Services 规划与执行清单，不代表接口已在 Gateway 可用。状态为“待开始”的任务，其 OpenAPI 操作可能已经声明但尚未注册 handler；method/path 事实以 `repo/architecture/services-route-baseline.yaml` 和 `make validate-services` 为准。新增实现必须先更新 OpenAPI，再注册 `/api/v1/svc` handler、生成物和测试。

> **不实现** deprecated 路径：`/gpu-containers*`、`/sandboxes*`

---

## 任务索引（Phase 2 新增映射）

| TASK | Phase 2 ops | 模块详文 |
|---|---|---|
| SVC-013 | inference logs / test / policies | `inference/inference-observability.md`、`inference-call-test.md`、`inference-rate-limit-policy.md` |
| SVC-014 | kb citations / sessions / permissions | `knowledge/kb-qa-flow.md`、`knowledge-base.md` |
| SVC-015 | tenant member / role / webhook deliveries | `tenant/tenant-management.md`、`role-permission-edit.md` |
| SVC-016 | integrations + bots | `integration/open-integration-overview.md` |

---

## TASK-SVC-001

状态：[x] 已完成（Phase 4 Sprint 1，2026-06-17）  
接口：`POST /api/v1/svc/models`  
优先级：P1  
被依赖：TASK-SVC-003  
本任务依赖：无  
模块详文：`docs/console-modules/inference/model-center.md`

验收：`POST /models` → 201

---

## TASK-SVC-002

状态：[x] 已完成（Phase 4 Sprint 1，2026-06-17）  
接口：`POST /api/v1/svc/models/import`（202 + AsyncTask）  
优先级：P1  
本任务依赖：TASK-SVC-001  
模块详文：`model-center.md`

---

## TASK-SVC-003

状态：[ ] 待开始  
接口：`POST /api/v1/svc/inference-services`  
优先级：P1  
被依赖：SVC-004、SVC-011、SVC-013  
本任务依赖：TASK-SVC-001（model ready）  
模块详文：`docs/console-modules/inference/inference-service.md`

Handler：

1. 校验 `model` → `Model.status=ready`
2. 未 ready → `422`（YAML 已举例 `MODEL_NOT_READY`）
3. 成功 → `202 + InferenceService`

---

## TASK-SVC-004

状态：[ ] 待开始  
接口：`PATCH /api/v1/svc/inference-services/{service_id}`  
优先级：P1  
本任务依赖：TASK-SVC-003  
模块详文：`inference-service.md`

前置：仅 `running` 可 PATCH；否则建议 422

---

## TASK-SVC-005

状态：[ ] 待开始  
接口：`GET/POST /api/v1/svc/knowledge-bases*`、documents  
优先级：P1  
本任务依赖：Core `vector-stores`  
模块详文：`docs/console-modules/knowledge/knowledge-base.md`

---

## TASK-SVC-006

状态：[ ] 待开始  
接口：`POST .../query`、`GET .../query/stream`  
优先级：P1  
本任务依赖：TASK-SVC-005  
模块详文：`docs/console-modules/knowledge/kb-qa-flow.md`

---

## TASK-SVC-007

状态：[x] 已完成（Phase 4 Sprint 1，2026-06-17）  
接口：Models 列表 / 详情 / 删除 / 版本  
优先级：P1  
本任务依赖：TASK-SVC-001  
模块详文：`model-center.md`

路径：`GET/DELETE /models*`、`GET .../versions`

---

## TASK-SVC-008

状态：[ ] 待开始  
接口：Inference 列表 / 详情 / 删除  
优先级：P1  
本任务依赖：TASK-SVC-003  
模块详文：`inference-service.md`

> Phase 2 修补：`DELETE` 响应补 `202 + AsyncTask` schema — handler 须对齐

---

## TASK-SVC-009

状态：[ ] 待开始  
接口：Knowledge base 详情 / 删除 / documents CRUD  
优先级：P1  
本任务依赖：TASK-SVC-005  
模块详文：`knowledge-base.md`

---

## TASK-SVC-010

状态：[ ] 待开始  
接口：`/api/v1/svc/tenant/*` 基础 8 条 + Phase 2 扩展  
优先级：P1  
本任务依赖：auth-service RBAC  
模块详文：`docs/console-modules/tenant/tenant-management.md`

**基础（Phase 1）**

| 能力 | 路径 |
|---|---|
| members CRUD | `/tenant/members*` |
| roles 列表 | `GET /tenant/roles` |
| SSO | `GET/PUT /tenant/sso` |
| webhooks | `GET/POST/DELETE /tenant/webhooks*` |

**Phase 2 扩展 → 见 TASK-SVC-015**

---

## TASK-SVC-011

状态：[ ] 待开始  
接口：Gateway `POST /v1/chat/completions`  
优先级：P2  
本任务依赖：TASK-SVC-003  
模块详文：`docs/console-modules/integration/openai-compatible-api.md`

> 不在 `services/v1.yaml`；Gateway `inferenceProxy` stub 验通

---

## TASK-SVC-012

状态：[ ] 待开始  
接口：Services response schema 与 stub 移除  
优先级：P2  
参考：`docs/console-modules/governance/schema-completion-tracker.md`

---

## TASK-SVC-013

状态：[ ] 待开始  
接口：推理服务观测与策略（Phase 2 **+3**）  
优先级：P1  
本任务依赖：TASK-SVC-003  
模块详文：见任务索引

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/svc/inference-services/{service_id}/logs` | `getInferenceServiceLogs` |
| POST | `/api/v1/svc/inference-services/{service_id}/test` | `testInferenceService` |
| PUT | `/api/v1/svc/inference-services/{service_id}/policies` | `updateInferenceServicePolicies` |

验收：test 对 `running` 服务返回 200 + `InferenceServiceTestResponse`

---

## TASK-SVC-014

状态：[ ] 待开始  
接口：知识库扩展（Phase 2 **+3**）  
优先级：P2  
本任务依赖：TASK-SVC-005、TASK-SVC-006  
模块详文：`kb-qa-flow.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/citations` | `listKnowledgeBaseCitations` |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/sessions` | `listKnowledgeBaseSessions` |
| PUT | `/api/v1/svc/knowledge-bases/{kb_id}/permissions` | `updateKnowledgeBasePermissions` |

---

## TASK-SVC-015

状态：[ ] 待开始  
接口：租户管理扩展（Phase 2 **+3**）  
优先级：P2  
本任务依赖：TASK-SVC-010  
模块详文：`role-permission-edit.md`、`tenant-management.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/svc/tenant/members/{member_id}` | `getTenantMember` |
| PUT | `/api/v1/svc/tenant/roles/{role_id}` | `updateTenantRole` |
| GET | `/api/v1/svc/tenant/webhooks/{webhook_id}/deliveries` | `listWebhookDeliveries` |

> Phase 2 修补：`POST /tenant/webhooks` 增 `422` — handler 验通

---

## TASK-SVC-016

状态：[ ] 待开始  
接口：第三方集成（Phase 2 **+3**）  
优先级：P3  
本任务依赖：TASK-SVC-010  
模块详文：`open-integration-overview.md`

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/svc/integrations` | `listIntegrations` |
| POST | `/api/v1/svc/integrations` | `createIntegration` |
| POST | `/api/v1/svc/integrations/bots` | `createIntegrationBot` |

---

## TASK-SVC-017

状态：**待定**（P0 YAML 草案暂缓评审）  
接口：推理服务 events（**YAML 草案**）  
优先级：P0（排队中）  
本任务依赖：TASK-SVC-003（推理服务 CRUD 可用）  
模块详文：`inference/inference-observability.md`  
草案：`docs/console-modules/openapi-drafts/p0/openapi-p0-yaml-draft.md` §3

| 方法 | 路径 | operationId |
|---|---|---|
| GET | `/api/v1/svc/inference-services/{service_id}/events` | `listInferenceServiceEvents` |

验收：与 logs 同 RBAC；200 + `InferenceServiceEventListResponse`

---

## TASK-SVC-018

状态：[ ] 待开始（**AI 原生 ×7 详化草案** ✅ 2026-06-17）  
接口：AI 原生 + 知识库智能 + 模型/合规整域（Phase 3 规划）  
优先级：P2–P3  
本任务依赖：TASK-SVC-005、TASK-SVC-006  
模块详文：`ai-native/README.md`  
草案：
- Agent Session：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-agent-session-draft.md`
- Tool Permission：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-agent-tool-permission-draft.md`
- Agent Audit：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-agent-audit-draft.md`
- Prompt Guard：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-prompt-injection-guard-draft.md`
- MCP Tool Market：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-mcp-tool-market-draft.md`
- Agent Orchestration：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-agent-orchestration-draft.md`
- AI Native Sandbox Security：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-ai-native-sandbox-security-draft.md`
- KB Doc Intelligence：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-kb-doc-intelligence-draft.md`
- KB Meeting Intelligence：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-kb-meeting-intelligence-draft.md`
- KB Video Intelligence：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-kb-video-intelligence-draft.md`
- Model Encryption：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-model-encryption-draft.md`
- Model Recommend Config：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-model-recommend-config-draft.md`
- Model Usage Stats：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-model-usage-stats-draft.md`
- API Key Audit：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-api-key-audit-draft.md`
- Netsec Policy：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-netsec-policy-draft.md`
- Compliance：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-compliance-draft.md`
- Billing Export：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-billing-export-draft.md`
- 整域索引：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-domain-draft.md`

分阶段：~~AI 原生 ×7~~ ✅ → ~~KB ×3~~ ✅ → ~~模型增强 ×3~~ ✅ → ~~安全/合规 ×4~~ ✅（**Phase 3 文档层完成**）→ **YAML 合入 / handler** ☐

**下一步**：`tasks/phase3/PHASE3-JOINT-REVIEW-AGENDA.md` 整域联评（2～3 场）→ 通过后 §6 批次合入 YAML → handler（ANI-main）。

---

## 相关文件

- `docs/console-modules/openapi-drafts/p0/openapi-p0-yaml-draft.md`
- `docs/console-modules/openapi-drafts/phase3/openapi-phase3-domain-draft.md`
- `docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md`
- `tasks/execution/TASK-DEPENDENCY-MAP.md`
