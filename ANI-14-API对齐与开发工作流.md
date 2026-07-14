# ANI-14：API 对齐与功能开发工作流

> **用途**：本文档是 ANI 平台从"产品功能定义"到"可运行代码"的完整闭环工作流，是 Services 层和 Core 层后续迭代开发的**基石文档**。
>
> **维护者**：ANI 项目架构负责人
> **版本**：2026-06-09
>
> **状态：历史快照（只读）**：本文记录 2026-06-09 的 API 对齐批次，不是当前 Services PR 规则或运行事实来源。当前规则以 `CLAUDE.md`、`repo/CURRENT-SPRINT.md` 和 `ANI-SERVICES-TEAM-GUIDE.md` 为准；当前 Services 门禁以 `make validate-services` 为准。
> **下游文档**：执行本工作流后产生 `repo/docs/CORE-TEAM-TASKS.md` 和 `repo/docs/SERVICES-TEAM-TASKS.md`，分别触发两个团队的 AI coding 工作

---

## 0. 工作流全景与闭环逻辑

```
三个输入源
  ├─ repo/api/openapi/v1.yaml          (Core API 契约，已实现，57+ 条路径)
  ├─ repo/api/openapi/services/v1.yaml (Services API 契约，21 条路径，全部 501 占位)
  └─ repo/services/ani-services.html   (产品功能定义，内容最丰富，但接口定义不准确)

                    ↓ Phase 0

              GAP-REPORT.md
    （哪里不一致、哪里缺失、工作量估算）

                    ↓ Phase 1

          对齐后的 ani-services.html
  （路径/schema/前缀全部修正，补充前置条件+操作矩阵）

                    ↓ Phase 2

    扩充后的 v1.yaml + services/v1.yaml
    （HTML 中有但 YAML 缺的接口全部补入）

                    ↓ Phase 3

    ┌─────────────────────┬──────────────────────┬───────────────────────┐
    │ CORE-TEAM-TASKS.md  │ SERVICES-TEAM-TASKS.md│ TASK-DEPENDENCY-MAP.md│
    │ (Core 团队任务清单)  │ (Services 团队任务清单)│ (跨团队依赖 + 关键路径)│
    └─────────────────────┴──────────────────────┴───────────────────────┘

                    ↓ Phase 4（按 TASK 逐条执行）

    Core 团队：                          Services 团队：
    新增 pkg/adapters 实现               新增微服务业务逻辑
    + ani-gateway handler                + 替换 stubs.go notImplemented

                    ↓ 验收

    go build + go test + make validate-architecture
    → 通过后：在对应 TASK 标记 ✅，更新 v1.yaml 实现状态
    → 形成闭环，进入下一轮迭代
```

**闭环的关键**：Phase 3 输出的两份任务清单（TASK 块）是机器可读格式——每个 TASK 包含"需改哪些文件 + handler 逻辑说明 + 验收命令"，可以直接作为 AI coding agent 的输入，不需要人再次翻译。

**任务清单位置规则**：Phase 3 产生的 `repo/docs/CORE-TEAM-TASKS.md`、`repo/docs/SERVICES-TEAM-TASKS.md` 和 `repo/docs/TASK-DEPENDENCY-MAP.md` 是当前协作和执行入口；`repo/development-records/` 只记录完成后的历史批次、验证证据和追溯信息。不要把当前任务清单同时维护在两个目录，避免人类和 AI 读取到冲突状态。

---

## 架构约束（所有 Phase 必须遵守）

> AI 执行任何 Phase 前必须读取并遵守以下约束，违反约束的输出无效。

```
1. URL 前缀规则（强制）：
   - Core API：https://{host}/api/v1/{resource}
   - Services API：https://{host}/api/v1/svc/{resource}（必须有 /svc/ 前缀）

2. 资源分层规则：
   - Core 负责：VM/容器/GPU/网络/存储/加密/K8s集群/镜像仓库/向量库/计量/GPU清单
   - Services 负责：AI 模型/推理服务/知识库/Console UI 聚合接口/BOSS 后台接口

3. HTML 中已知的路径错误（必须在 Phase 1 修正，路径已在 services/v1.yaml Phase 2 确认）：
   - GPU容器路径：应使用 /api/v1/svc/gpu-containers（Services 层）
   - Sandbox路径：应使用 /api/v1/svc/sandboxes（Services 层）
   - 知识库路径：应使用 /api/v1/svc/knowledge-bases（Services 层）
   - 模型路径：应使用 /api/v1/svc/models（Services 层，前缀必须含 svc/）
   - 推理服务路径：应使用 /api/v1/svc/inference-services（Services 层）
   - 虚机路径：应使用 /api/v1/instances（Core 层，不含 console/compute 前缀）

4. 所有 POST 创建和有副作用的 PUT/PATCH 必须含 idempotency_key 字段（类型 uuid）

5. 操作类型与响应码：
   - 同步创建 → 201 Created
   - 异步操作（模型导入、推理服务部署）→ 202 Accepted + AsyncTask schema
   - 查询 → 200 OK

6. Services Go 代码禁止 import：
   - pkg/adapters（基础设施 adapter，只有 Core 用）
   - pkg/ports（Core 内部 interface，新代码不允许）
   - pkg/bootstrap（Core 启动配置）
   允许 import：pkg/generated/pb/（proto 类型）、pkg/types（通用类型）

7. 所有 handler 函数第一步必须从 middleware.GetTenantID(c) 获取租户 ID，
   不得从请求 body 或 query 参数信任租户信息

8. Go 代码风格参考：
   - Handler 风格：repo/services/ani-gateway/internal/router/network_resources.go
   - gRPC 调用风格：repo/services/ani-gateway/internal/middleware/auth_client.go
   - 错误响应格式：{"code": "UPPER_SNAKE", "message": "...", "request_id": "..."}
```

---

## Phase 0：GAP 扫描 Prompt

**目的**：在做任何修改前产出客观差异报告，同时给出工作量估算。  
**执行方式**：将以下 prompt 和三个输入文件一起提交给 AI，只分析不修改文件。

```
[PHASE-0: GAP ANALYSIS — 不修改任何文件，只产出分析报告]

你是 ANI 平台 API 审计工程师。请阅读三个文件，产出结构化差异报告。

输入文件：
1. repo/api/openapi/v1.yaml
2. repo/api/openapi/services/v1.yaml
3. repo/services/ani-services.html

执行五项扫描，输出严格按以下 markdown 格式（后续 Phase 机器读取）：

---
# ANI GAP REPORT
生成时间：{YYYY-MM-DD}

## A. 路径错误/不一致列表
| HTML中的路径 | 正确路径 | 所属YAML | 问题类型 |
|---|---|---|---|
| GET /api/v1/gpu-containers | GET /api/v1/instances?kind=gpu_container | v1.yaml | 路径错误 |

## B. Schema 缺失列表
| 接口路径 | 缺失内容 | 严重程度 |
|---|---|---|
| POST /api/v1/svc/models | response body schema 未定义 | 高 |

## C. v1.yaml 中有但 HTML 未文档化的路径
| 路径 | 所属YAML | 建议处理 |
|---|---|---|

## D. HTML 中有但两个 YAML 都缺失的功能（★ 开发工作量来源 ★）
| 功能描述 | HTML 所在位置 | 建议路径 | 归属层 | 优先级 |
|---|---|---|---|---|
| 查询GPU实时利用率 | GPU容器详情页 | GET /api/v1/gpu-inventory/{id}/metrics | Core | P1 |
| 推理服务手动扩缩容 | 推理服务详情页 | PATCH /api/v1/svc/inference-services/{id}/scale | Services | P1 |

## E. 工作量统计（★ 项目计划依据 ★）
- HTML 接口总数：N 条
- 与 YAML 完全匹配（无需开发）：N 条
- 需修正路径/前缀（Phase 1 工作）：N 条
- 需补充 Schema（Phase 1 工作）：N 条
- 需新增到 Core v1.yaml（Phase 2 + Core 团队开发）：N 条，预估 handler 函数 N 个
- 需新增到 services/v1.yaml（Phase 2 + Services 团队开发）：N 条，预估 handler 函数 N 个
- Services 团队现有 501 占位待实现：21 条
- 总计 Core 团队待开发：N 个 handler
- 总计 Services 团队待开发：N 个 handler
---
```

**验收标准**：Section D 的列表即为后续开发的全量待办。人工确认列表合理后进入 Phase 1。

---

## Phase 1：HTML 对齐 YAML Prompt

**目的**：以两个 v1.yaml 为权威，修正 HTML 中所有错误的路径、前缀、字段定义；同时补充 HTML 普遍缺失的前置条件和操作可用性矩阵。  
**前置**：已有 GAP REPORT，重点参考 Section A 和 B。

```
[PHASE-1: HTML ← YAML ALIGNMENT]

你是 ANI 平台 API 文档工程师。根据 GAP REPORT 和两个 v1.yaml，修改 ani-services.html。

输入：GAP REPORT + repo/api/openapi/v1.yaml + repo/api/openapi/services/v1.yaml + HTML

修改规则（按优先级执行）：

规则1：修正 GAP REPORT Section A 中的路径错误
  将 HTML 中错误路径替换为正确路径，修改处加注释 <!-- ALIGNED: 旧路径→新路径 -->

规则2：为 GAP REPORT Section B 中 Schema 缺失的接口补充字段表格
  格式：| 字段名 | 类型 | 必填 | 说明 |
  字段定义从对应 v1.yaml 的 schema 中读取

规则3：对 GAP REPORT Section D 中的功能（HTML有但YAML缺），不删除内容，
  在接口旁加标记 <!-- TODO-YAML: 需新增到 {Core/Services} v1.yaml，建议路径：{path} -->

规则4：为每个创建接口（POST）补充"前置条件"小节
  ### 前置条件
  | 依赖项 | 要求状态 | 未满足时的 HTTP 响应 |
  |---|---|---|
  | model_version_id（UUID） | status = ready | 422 + code: MODEL_NOT_READY |

规则5：为每个有状态机的资源补充"操作可用性矩阵"
  ### 操作可用性矩阵
  | 操作 | pending | running | stopped | failed | 说明 |
  |---|---|---|---|---|---|
  | 启动 | ❌ | ❌ | ✅ | ✅ | — |
  | 停止 | ❌ | ✅ | ❌ | ❌ | — |
  | 删除 | ✅ | ❌ | ✅ | ✅ | 运行中不可直接删除 |

禁止操作：不得删除已有功能描述；不得改变 HTML 视觉样式；不得修改中文说明文字。

输出：修改后的完整 ani-services.html
```

---

## Phase 2：YAML 扩充 Prompt

**目的**：以对齐后的 HTML 为权威，将 HTML 中有但 YAML 缺失的接口（TODO-YAML 标记）补充进对应 v1.yaml。  
**前置**：Phase 1 已完成。

```
[PHASE-2: YAML ← HTML EXPANSION]

你是 ANI 平台 API 设计工程师。根据 Phase 1 对齐后的 HTML，
将所有 <!-- TODO-YAML --> 标记的接口补充进对应 v1.yaml。

输入：对齐后的 ani-services.html + 两个 v1.yaml

步骤1：提取所有 <!-- TODO-YAML --> 标记，整理为接口列表

步骤2：为每个新增接口生成 OpenAPI 路径定义
  每个路径必须包含（缺一不可）：
  - operationId（camelCase，全局唯一）
  - summary（一句中文说明）
  - tags（资源模块名）
  - security: [{BearerAuth: []}, {ApiKeyAuth: []}]
  - requestBody（POST/PUT/PATCH）：
    - required: [idempotency_key, ...其他必填字段]
    - idempotency_key: {type: string, format: uuid, description: "客户端生成UUID防重复提交"}
  - responses:
    - 成功：200/201/202（按同步/异步判断）+ 完整 schema $ref
    - 错误：400/401/403/404（有路径参数时）/409（有唯一性约束时）
  - 所有 $ref 的 schema 必须在 components/schemas 中定义

步骤3：将新接口插入 v1.yaml（Core 资源）或 services/v1.yaml（Services 资源），
  插入位置：同类资源路径组末尾

步骤4：将 HTML 中对应的 <!-- TODO-YAML --> 改为 <!-- ADDED-TO-YAML: {path} -->

输出：
1. 扩充后的完整 repo/api/openapi/v1.yaml
2. 扩充后的完整 repo/api/openapi/services/v1.yaml
3. 更新后的 ani-services.html
4. 变更摘要（严格按此格式，供 Phase 3 读取）：

---
# YAML EXPANSION SUMMARY
## 新增到 Core v1.yaml（N条）
| 路径 | HTTP方法 | operationId | 对应HTML功能 | 估计复杂度 |
|---|---|---|---|---|
| /gpu-inventory/{id}/metrics | GET | getGPUMetrics | GPU实时利用率 | 低 |

## 新增到 services/v1.yaml（N条）
| 路径 | HTTP方法 | operationId | 对应HTML功能 | 估计复杂度 |
|---|---|---|---|---|
| /inference-services/{id}/scale | PATCH | scaleInferenceService | 推理服务扩缩容 | 中 |
---
```

---

## Phase 3：工作分解输出 Prompt

**目的**：将扩充后的 YAML 转化为两个团队可直接执行的机器可读任务清单，同时给出跨团队依赖关系，支持并行开发。  
**前置**：Phase 2 已完成，YAML EXPANSION SUMMARY 已产出。

```
[PHASE-3: WORK BREAKDOWN — 产出三个文件]

你是 ANI 平台技术项目经理和架构师。请根据两个完整 v1.yaml 和 YAML EXPANSION SUMMARY，
生成三份结构化文档，作为两个开发团队的 AI coding agent 输入。

输入：扩充后的两个 v1.yaml + YAML EXPANSION SUMMARY

判断 TASK 归属：
- 新增到 v1.yaml 的路径 → CORE-TEAM-TASKS（Core 团队实现）
- 新增到 services/v1.yaml 的路径 → SERVICES-TEAM-TASKS（Services 团队实现）
- services/v1.yaml 中已有的 21 条 501 占位路径 → SERVICES-TEAM-TASKS

─────────────────────────────────────────────────────────
输出文件一：repo/docs/CORE-TEAM-TASKS.md

格式要求（每个 TASK 块必须自包含，可单独给 AI coding agent 执行）：

---
# CORE TEAM 开发任务清单
生成时间：{date}
总任务数：N | 待开始：N | 进行中：0 | 已完成：0
---

## TASK-CORE-001
状态：[ ] 待开始
接口：GET /api/v1/gpu-inventory/{gpu_id}/metrics
优先级：P1
被依赖：TASK-SVC-005（InferenceService GPU 选择）
本任务依赖：无（可立即开始）

需修改的文件：
- [ ] repo/services/ani-gateway/internal/router/gpu_resources.go（在此文件新增 handler）
- [ ] repo/services/ani-gateway/internal/router/router.go（在 RegisterWithOptions 注册路由）
- [ ] repo/pkg/ports/gpu_inventory.go（新增 GetGPUMetrics(ctx, gpuID, window) 方法）
- [ ] repo/pkg/adapters/gpu/metrics.go（新建，实现真实数据采集）

Handler 逻辑（供 AI coding agent 逐步实现）：
```
接口：GET /api/v1/gpu-inventory/{gpu_id}/metrics
路径参数：gpu_id (uuid, 必填)
查询参数：window (string, 枚举 1m/5m/1h, 默认 5m)

执行顺序：
1. tenantID := middleware.GetTenantID(c)，空则返回 403 FORBIDDEN
2. 校验 gpu_id 是否为合法 uuid，否则返回 400 BAD_REQUEST
3. 调用 ports.GPUInventory.GetGPUMetrics(ctx, gpuID, window)
4. 如 gpu_id 不存在返回 404 NOT_FOUND
5. 返回 200 + GPUMetricsResponse

Response Schema (来自 v1.yaml components/schemas/GPUMetrics)：
  gpu_id:               string (uuid)
  timestamp:            string (RFC3339)
  utilization_pct:      number (0.0-100.0)
  memory_used_mib:      integer
  memory_total_mib:     integer
  temperature_celsius:  integer (nullable)
  power_watts:          number (nullable)
```

验收命令：
```bash
cd repo
go build ./services/ani-gateway/...
go test ./services/ani-gateway/internal/router/... -run TestGPUMetrics
# 集成验证（需真实环境）：
curl -H "X-API-Key: $TEST_KEY" \
  "http://localhost:8080/api/v1/gpu-inventory/{uuid}/metrics?window=5m"
# 期望：200 + GPUMetrics JSON 或 404 NOT_FOUND
```
---

（TASK-CORE-002、TASK-CORE-003… 按相同格式继续）

─────────────────────────────────────────────────────────
输出文件二：repo/docs/SERVICES-TEAM-TASKS.md

格式（与 CORE 相同，以下为 Services 特有的注意事项）：
- 每个 handler 必须通过 gRPC 调用对应微服务，不直接操作数据库
- 每个微服务内部实现必须分为：handler.go（HTTP解析）+ service.go（业务逻辑）+ repo.go（数据库）
- 替换 stubs.go 时注明：从 stubs.go 删除对应的 notImplemented 注册，在新文件中实现

示例 TASK 块：

---
## TASK-SVC-001
状态：[ ] 待开始
接口：POST /api/v1/svc/models
优先级：P1（TASK-SVC-003 依赖模型 ready 状态）
被依赖：TASK-SVC-003（CreateInferenceService）
本任务依赖：无（可立即开始）

需修改的文件：
- [ ] repo/services/ani-gateway/internal/router/model_resources.go（新建，替换 stubs.go registerModels）
- [ ] repo/services/ani-gateway/internal/router/stubs.go（删除 registerModels 函数）
- [ ] repo/services/ani-gateway/internal/router/router.go（替换 registerModels 调用来源）
- [ ] repo/services/model-service/internal/handler/model.go（新建 gRPC handler）
- [ ] repo/services/model-service/internal/service/model.go（新增 CreateModel 业务逻辑）
- [ ] repo/services/model-service/internal/repo/model.go（新增数据库写入）

Handler 逻辑（ani-gateway 层）：
```
接口：POST /api/v1/svc/models
Body：
  name:         string, 必填, 正则 ^[a-z0-9][a-z0-9.-]{0,62}$
  display_name: string, 必填, 1-128 字符
  description:  string, 可选, 最长 1024 字符
  capabilities: array[string], 枚举 [text-generation, embedding, speech-to-text]

前置条件：无

执行顺序：
1. tenantID := middleware.GetTenantID(c)，空则 403
2. 解析并校验 name（正则）+ display_name（非空），失败 400 BAD_REQUEST
3. gRPC 调用 model-service.CreateModel(ctx, tenantID, name, displayName, capabilities)
4. model-service 返回 409 CONFLICT 时，gateway 返回 409 + code: NAME_CONFLICT
5. 成功返回 201 + Model JSON
```

Handler 逻辑（model-service 内部）：
```
CreateModel(ctx, tenantID, name, displayName, capabilities):
1. 查询 models 表：SELECT id FROM models WHERE tenant_id=$1 AND name=$2，存在则返回 gRPC AlreadyExists
2. INSERT INTO models (id, tenant_id, name, display_name, capabilities, status, created_at)
   VALUES (uuid_generate_v4(), $1, $2, $3, $4, 'pending', NOW())
3. 返回完整 Model 对象
```

验收命令：
```bash
cd repo
go build ./services/...
go test ./services/ani-gateway/internal/router/... -run TestCreateModel
go test ./services/model-service/...
# 端到端验证：
curl -X POST http://localhost:8080/api/v1/svc/models \
  -H "X-API-Key: $TEST_KEY" -H "Content-Type: application/json" \
  -d '{"name":"llama3","display_name":"LLaMA 3","capabilities":["text-generation"]}'
# 期望：201 + {"id":"...","name":"llama3","status":"pending",...}
```
---

─────────────────────────────────────────────────────────
输出文件三：repo/docs/TASK-DEPENDENCY-MAP.md

格式：

---
# 任务依赖关系图
生成时间：{date}

## 依赖链（谁等谁）
TASK-CORE-001 (GPU Metrics API)
  └─ 被依赖 → TASK-SVC-005 (推理服务 GPU 选择，需读取 GPU 可用性)

TASK-SVC-001 (Model CRUD)
  └─ 被依赖 → TASK-SVC-003 (创建推理服务，需要 model_version status=ready)
  └─ 被依赖 → TASK-SVC-007 (模型导入，需要已有 Model 记录)

## 可立即并行开发的任务
Core 团队可立即开始（无依赖）：TASK-CORE-001, TASK-CORE-002, ...
Services 团队可立即开始（无依赖）：TASK-SVC-001, TASK-SVC-002, ...

## 关键路径（最长串行链）
{描述最长的依赖链}
估算工期：Core X 天 + Services Y 天 = 如串行执行 Z 天

## Sprint 建议分组
Sprint 1（并行）：{Core 无依赖任务} + {Services 无依赖任务}
Sprint 2（Core 完成后）：{Services 依赖 Core 的任务}
---
```

---

## Phase 4：代码生成 Prompt（按 TASK 逐条执行）

**目的**：将单个 TASK 块转化为可直接合并的 Go 代码。  
**执行方式**：每次执行时，提供 prompt + 一个 TASK 块 + 指定的参考代码文件。

```
[PHASE-4: HANDLER CODE GENERATION — 单 TASK 执行]

你是 ANI 平台 Go 后端工程师。根据以下 TASK 块和代码风格参考，
生成完整可提交的 Go 代码。

TASK 内容：
{粘贴 TASK-CORE-XXX 或 TASK-SVC-XXX 的完整内容}

代码风格参考（请先阅读这些文件）：
- repo/services/ani-gateway/internal/router/network_resources.go
- repo/services/ani-gateway/internal/router/auth.go
- repo/services/ani-gateway/internal/middleware/auth_client.go

生成要求：

1. 按 TASK 中"需修改的文件"列表生成每个文件的完整代码
2. Handler 函数固定结构（按顺序，不可颠倒）：
   a. BindJSON 解析请求体
   b. middleware.GetTenantID 获取租户 ID，为空立即返回 403
   c. 逐字段 if 校验必填项，失败立即返回 400
   d. 前置条件检查（gRPC 查询依赖资源状态），失败返回 422
   e. 执行核心业务（gRPC 调用微服务 或 Core SDK 调用）
   f. c.JSON 返回响应

3. 错误响应格式统一（复用现有 writeXxxError 风格）：
   400 → BAD_REQUEST   | 401 → UNAUTHORIZED | 403 → FORBIDDEN
   404 → NOT_FOUND     | 409 → CONFLICT      | 422 → PRECONDITION_FAILED
   429 → QUOTA_EXCEEDED| 503 → SERVICE_UNAVAILABLE

4. 禁止（违反则重写）：
   - Services 代码中 import pkg/adapters 或 pkg/ports
   - 硬编码 tenant_id 或 user_id
   - 空的 error 处理块 if err != nil {}
   - 使用 panic

5. 必须同时生成 _test.go 文件，覆盖：
   - 成功路径（201/200/202）
   - 必填字段缺失（400）
   - 前置条件未满足（422）
   - 资源不存在（404，如有路径参数）

6. 每个文件末尾附上验收步骤：
   cd repo && go build ./... && go test ./services/... -run Test{FunctionName}

完成后输出：
- 所有修改文件的完整内容
- 对 stubs.go 和 router.go 的 diff（哪行删/哪行加）
- 任务完成后的 TASK 块状态更新：[x] 已完成（替换原来的 [ ] 待开始）
```

---

## 文档产出物位置约定

### `repo/docs/` 与 `repo/development-records/` 边界

`repo/docs/` 保存当前仍在使用的协作执行文档：任务清单、依赖图、GAP 报告和生成后的 API 静态文档。AI coding agent 和人工开发者领取任务时，以这里的 `CORE-TEAM-TASKS.md`、`SERVICES-TEAM-TASKS.md` 和 `TASK-DEPENDENCY-MAP.md` 为准。

`repo/development-records/` 保存已经完成的历史批次归档：每个批次做了什么、改了哪些关键文件、运行了哪些验证命令、留下了哪些证据。它不维护当前待办状态，也不作为团队任务清单入口。

如果历史上存在 `repo/development-records/CORE-TEAM-TASKS.md`、`repo/development-records/SERVICES-TEAM-TASKS.md` 或 `repo/development-records/TASK-DEPENDENCY-MAP.md`，它们只能视为旧快照或迁移前参考。新的 Phase 3 输出必须写入 `repo/docs/`；完成单个 TASK 后，再按批次记录规则把结果归档到 `repo/development-records/`，不得反向在归档目录继续维护待办清单。

| 产出物 | 存放位置 | 生命周期 |
|---|---|---|
| GAP REPORT | `repo/docs/gap-report-{YYYY-MM-DD}.md` | 当前对齐分析输入；新一轮对齐可新增一份 |
| 对齐后的 HTML | `repo/services/ani-services.html`（原地修改） | 持续维护 |
| 扩充后的 Core YAML | `repo/api/openapi/v1.yaml`（原地修改） | 唯一真实来源 |
| 扩充后的 Services YAML | `repo/api/openapi/services/v1.yaml`（原地修改） | 唯一真实来源 |
| Core 团队任务清单 | `repo/docs/CORE-TEAM-TASKS.md` | 当前执行入口，持续追踪，完成打✅ |
| Services 团队任务清单 | `repo/docs/SERVICES-TEAM-TASKS.md` | 当前执行入口，持续追踪，完成打✅ |
| 依赖关系图 | `repo/docs/TASK-DEPENDENCY-MAP.md` | 当前跨团队依赖入口，每次 Phase 3 后更新 |
| 批次完成记录 | `repo/development-records/{batch-id}.md` | TASK 完成后的历史归档，不维护当前待办 |
| 批次归档索引 | `repo/development-records/README.md` | 已完成批次索引，不作为当前任务清单 |

---

## 迭代节奏建议

```
初次建立基线（一次性）：
  Phase 0 → Phase 1 → Phase 2 → Phase 3
  人工 review 每个 Phase 输出后再执行下一个
  预计耗时：半天到一天

日常迭代（产品更新功能后）：
  产品团队更新 HTML → 局部执行 Phase 0（只扫描新增部分）
  → 局部执行 Phase 2 → 追加新 TASK 到任务清单
  预计耗时：1-2小时

代码开发（两个团队各自）：
  取各自 TASK 清单 → 按 DEPENDENCY-MAP 排序 → 逐条执行 Phase 4
  每个 TASK 独立执行，AI coding agent 单次处理一个 TASK
  完成后打✅并推 PR

验收闭环：
  PR 合并后 → 在对应 v1.yaml 接口旁标记 # status: implemented
  → 定期统计实现率（已实现 / 全量）= 平台完成度
```

---

*本文件版本：v1.0 | 下次更新触发条件：三个输入文件（两个 YAML + HTML）发生重大结构变更，或工作流流程本身需要调整*
