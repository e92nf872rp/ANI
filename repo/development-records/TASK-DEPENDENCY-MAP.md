# 任务依赖图

**生成日期**: 2026-06-09  
**关联文件**: `SERVICES-TEAM-TASKS.md`、`CORE-TEAM-TASKS.md`

---

## 第一批：无前置依赖（可立即并行开发）

以下任务无任何前置依赖，各团队可立即启动，多个 AI coding 会话同时执行：

| 任务 ID | 描述 | 验收命令 |
|---------|------|---------|
| TASK-SVC-001 | listModels — model_resources.go 骨架 | `curl .../api/v1/svc/models` → 200 |
| TASK-SVC-007 | listInferenceServices — inference_resources.go 骨架 | `curl .../api/v1/svc/inference-services` → 200 |
| TASK-SVC-011 | listKnowledgeBases — kb_resources.go 骨架 | `curl .../api/v1/svc/knowledge-bases` → 200 |
| TASK-SVC-017 | listGpuContainers — gpu_container_resources.go 骨架 | `curl .../api/v1/svc/gpu-containers` → 200 |
| TASK-SVC-019 | listSandboxes — sandbox_resources.go 骨架 | `curl .../api/v1/svc/sandboxes` → 200 |
| TASK-SVC-021 | listTenantMembers — tenant_resources.go 骨架 | `curl .../api/v1/svc/tenant/members` → 200 |
| TASK-CORE-001 | Branding handlers | `curl .../api/v1/branding` → 200 |
| TASK-CORE-002 | Task handlers | `curl .../api/v1/tasks/xxx` → 200/404 |

**第一批完成标准**：`stubs.go` 中 `registerModels`、`registerInferenceServices`、`registerKnowledgeBases` 三个函数的 `notImplemented` 调用全部清零。

---

## 第二批：依赖第一批通路验证

| 任务 ID | 描述 | 前置依赖 |
|---------|------|---------|
| TASK-SVC-002 | createModel | TASK-SVC-001（同文件） |
| TASK-SVC-003 | importModel | TASK-SVC-001 |
| TASK-SVC-004 | getModel | TASK-SVC-001 |
| TASK-SVC-005 | deleteModel | TASK-SVC-004 |
| TASK-SVC-006 | createModelVersion | TASK-SVC-004 |
| TASK-SVC-008 | createInferenceService | TASK-SVC-007 |
| TASK-SVC-009 | getInferenceService | TASK-SVC-007 |
| TASK-SVC-010 | deleteInferenceService | TASK-SVC-009 |
| TASK-SVC-012 | createKnowledgeBase | TASK-SVC-011 |
| TASK-SVC-013 | getKnowledgeBase / deleteKnowledgeBase | TASK-SVC-011 |
| TASK-SVC-018 | GPU容器高级接口 | TASK-SVC-017 |
| TASK-SVC-020 | Sandbox高级接口 | TASK-SVC-019 |

---

## 第三批：依赖数据管道就绪

| 任务 ID | 描述 | 前置依赖 |
|---------|------|---------|
| TASK-SVC-014 | uploadKnowledgeBaseDocument | TASK-SVC-012（知识库存在） |
| TASK-SVC-015 | queryKnowledgeBase | TASK-SVC-014（有索引数据） |
| TASK-SVC-016 | streamQueryKnowledgeBase | TASK-SVC-015 |

---

## 第四批：依赖 InferenceService 上线

| 任务 ID | 描述 | 前置依赖 |
|---------|------|---------|
| TASK-CORE-003 | ~~inferenceProxy（/v1/chat/completions）~~ 已取消，数据面归属 Envoy AI Gateway | — |

---

## 完整依赖链（关键路径）

```
第一批（并行）
  ├─ TASK-SVC-001 → TASK-SVC-002, 003, 004
  │                              └─ TASK-SVC-005, 006
  ├─ TASK-SVC-007 → TASK-SVC-008, 009 → TASK-SVC-010
  │                                └─ TASK-CORE-003（第四批）
  ├─ TASK-SVC-011 → TASK-SVC-012 → TASK-SVC-014 → TASK-SVC-015 → TASK-SVC-016
  │              └─ TASK-SVC-013
  ├─ TASK-SVC-017 → TASK-SVC-018
  ├─ TASK-SVC-019 → TASK-SVC-020
  ├─ TASK-SVC-021（独立）
  ├─ TASK-CORE-001（独立）
  └─ TASK-CORE-002（独立）
```

---

## 环境变量汇总

实现时所需 env var（列于此供 Services 团队统一配置）：

| Env Var | 用途 | Default |
|---------|------|---------|
| `MODEL_SERVICE_ADDR` | model-service gRPC/HTTP 地址 | `127.0.0.1:9201` |
| `INFERENCE_SERVICE_ADDR` | inference-service 地址 | `127.0.0.1:9202` |
| `KB_SERVICE_ADDR` | kb-service 地址 | `127.0.0.1:9203` |
| `GPU_CONTAINER_SERVICE_ADDR` | GPU容器编排服务地址 | `127.0.0.1:9204` |
| `SANDBOX_SERVICE_ADDR` | Sandbox编排服务地址 | `127.0.0.1:9205` |
| `TENANT_SERVICE_ADDR` | 租户管理服务地址 | `127.0.0.1:9206` |
| `AUTH_SERVICE_ADDR` | auth-service（已实现） | `127.0.0.1:9101` |
