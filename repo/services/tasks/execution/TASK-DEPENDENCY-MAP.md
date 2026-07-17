# 任务依赖关系图

> **Phase 3 对齐**：2026-06-17  
> **契约来源**：`docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md`

## 架构决策

- GPU 容器 / Sandbox：**Core 统一实例**（`/api/v1/instances*`），不实现 deprecated Services 路径
- OpenAPI 已声明（Phase 2）≠ handler 已实现（Phase 4）；当前 method/path 差异以 `repo/architecture/services-route-baseline.yaml` 和 `make validate-services` 为准

## Core 依赖链

```
TASK-CORE-001 (sandbox listInstances)
  └─ 被依赖 → CORE-005 实例观测、CORE-006 Sandbox 扩展

TASK-CORE-002 (lifecycle 422)
  └─ 被依赖 → Console 全实例类型动作置灰

TASK-CORE-003 (gpu-inventory)
  └─ 被依赖 → 首页 GPU 利用率、资源池概览（页面聚合）

TASK-CORE-005 (instances logs/events/metrics/exec)
  └─ 依赖 → CORE-001 或任意实例 CRUD 已可用

TASK-CORE-008 (volume snapshots)
  └─ 依赖 → volumes CRUD 基础 handler

TASK-CORE-009 (buckets/upload/download)
  └─ 依赖 → objects 元数据 CRUD

TASK-CORE-011 (vector insert + search 422)
  └─ 依赖 → vector-stores CRUD

TASK-CORE-012 (k8s workloads + create 422)
  └─ 依赖 → k8s-clusters 基础 CRUD
```

## Services 依赖链

```
TASK-SVC-001 (Model CRUD)
  ├─ 被依赖 → SVC-002 (Import)
  ├─ 被依赖 → SVC-003 (Inference deploy)
  └─ 被依赖 → SVC-007 (Models 读删版本)

TASK-SVC-003 (Inference deploy)
  ├─ 被依赖 → SVC-004 (PATCH)
  ├─ 被依赖 → SVC-008 (列表/删除)
  ├─ 被依赖 → SVC-011 (OpenAI Gateway)
  └─ 被依赖 → SVC-013 (logs/test/policies)

TASK-SVC-005 (Knowledge Base CRUD)
  ├─ 依赖 → Core vector-stores
  ├─ 被依赖 → SVC-006 (query/stream)
  ├─ 被依赖 → SVC-009 (documents)
  └─ 被依赖 → SVC-014 (citations/sessions/permissions)

TASK-SVC-010 (Tenant 基础)
  ├─ 被依赖 → SVC-015 (member/role/webhook deliveries)
  └─ 被依赖 → SVC-016 (integrations)
```

## Phase 2 新增 — 可并行批次

| 批次 | Core | Services |
|---|---|---|
| **A**（P1） | CORE-003、CORE-005 | SVC-013 |
| **B**（P2） | CORE-007~009、CORE-011~012 | SVC-014、SVC-015 |
| **C**（P3） | CORE-006、CORE-010、CORE-013 | SVC-016、SVC-011 |

与基础 CRUD **可并行**（无硬依赖）：CORE-001、CORE-002、SVC-001、SVC-005、SVC-010

## 关键路径（产品主流程）

```
SVC-001 Model ready
  → SVC-003 Inference deploy
    → SVC-013 test/logs/policies
    → SVC-011 OpenAI proxy
  → SVC-005 KB
    → SVC-006 query
    → SVC-014 citations/sessions
```

## Sprint 建议（Phase 4 起）

| Sprint | Core | Services |
|---|---|---|
| 1 | CORE-001/002 实例 + 422 | SVC-001/002/007 模型 |
| 2 | CORE-003 gpu-inventory | SVC-003/004/008 推理 CRUD |
| 3 | CORE-005 实例观测 | SVC-013 推理扩展 |
| 4 | CORE-007~009 网络/存储 | SVC-005/006/009 知识库 |
| 5 | CORE-011~012 向量/K8s | SVC-014/015 租户/KB 扩展 |
| 6 | CORE-006/010/013 收尾 | SVC-010/016/011 租户/接入/Gateway |
| **7（P0）** | **CORE-014** alerts + listTasks | **SVC-017** inference events |
| **8（Phase 3）** | CORE-015 secrets/encryption | SVC-018 AI 原生整域 |

## P0 YAML 草案（文档层）

评审源：`docs/console-modules/openapi-drafts/p0/openapi-p0-yaml-draft.md` — **状态：待定**

```
CORE-014 (listAlerts + listTasks) — 待定
SVC-017 (inference events) — 待定
```

## Phase 2 Handler 文档验收

```
PHASE2-HANDLER-ACCEPTANCE-RECORD.md — 文档层 ✅
  ├─ Core: CORE-003～012 curl + 签核
  └─ Services: SVC-013～016（SERVICES-HANDLER-IMPLEMENTATION-GUIDE.md）
```

## Phase 3 AI 原生 ×7 详化（文档层 ✅）

```
openapi-phase3-agent-session-draft.md — ✅
openapi-phase3-agent-tool-permission-draft.md — ✅
openapi-phase3-agent-audit-draft.md — ✅
openapi-phase3-prompt-injection-guard-draft.md — ✅
openapi-phase3-mcp-tool-market-draft.md — ✅
openapi-phase3-ai-native-sandbox-security-draft.md — ✅
openapi-phase3-agent-orchestration-draft.md — ✅
  └─ ×7 详文 / prd / spec 已同步
  └─ 验收：tasks/phase3/acceptance/PHASE3-AI-NATIVE-ACCEPTANCE-RECORD.md
  └─ YAML 合入 / handler — ☐（ANI-main）
```

## Phase 3 知识库智能 ×3 详化（文档层 ✅）

```
openapi-phase3-kb-doc-intelligence-draft.md — ✅
openapi-phase3-kb-meeting-intelligence-draft.md — ✅
openapi-phase3-kb-video-intelligence-draft.md — ✅
  └─ ×3 详文 / prd / spec 已同步
  └─ 验收：tasks/phase3/acceptance/PHASE3-KB-INTELLIGENCE-ACCEPTANCE-RECORD.md
  └─ YAML 合入 / handler — ☐（ANI-main）
```

## Phase 3 模型增强 ×3 详化（文档层 ✅）

```
openapi-phase3-model-encryption-draft.md — ✅
openapi-phase3-model-recommend-config-draft.md — ✅
openapi-phase3-model-usage-stats-draft.md — ✅
  └─ ×3 详文 / prd / spec 已同步
  └─ 验收：tasks/phase3/acceptance/PHASE3-MODEL-ENHANCEMENT-ACCEPTANCE-RECORD.md
  └─ YAML 合入 / handler — ☐（ANI-main；usage-stats 含 Core v1.yaml 扩展）
```

## Phase 3 安全/合规扩展 ×4 详化（文档层 ✅）

```
openapi-phase3-api-key-audit-draft.md — ✅ (Core)
openapi-phase3-netsec-policy-draft.md — ✅ (Core)
openapi-phase3-compliance-draft.md — ✅ (Services 只读)
openapi-phase3-billing-export-draft.md — ✅ (Services)
  └─ 验收：tasks/phase3/acceptance/PHASE3-SECURITY-COMPLIANCE-ACCEPTANCE-RECORD.md
  └─ YAML 合入 / handler — ☐
```

## Phase 3 整域状态

**文档层**：17 套详化草案 ✅  
**联评**：`tasks/phase3/PHASE3-JOINT-REVIEW-AGENDA.md` — **待召开**（建议场次 A/B/C）  
**实现层**：YAML 合入 + handler — ☐（ANI-main）

## 相关文件（历史）

评审源：`docs/console-modules/openapi-drafts/phase3/openapi-phase3-domain-draft.md` §2–§4 → TASK-SVC-018

## 相关文件

- `tasks/execution/CORE-TEAM-TASKS.md`
- `tasks/execution/SERVICES-TEAM-TASKS.md`
- `docs/console-modules/governance/YAML-EXPANSION-SUMMARY-2026-06-17.md`
