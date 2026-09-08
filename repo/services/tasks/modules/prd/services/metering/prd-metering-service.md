# PRD: Metering 平台（metering-service + Console 用量 + BOSS 平台计量）

> **版本：** v1.4（草案）
> **日期：** 2026-07-09
> **确认选项：** 1E / 2C / 3D（§7.1 / FR-8 已收敛）/ 4E / 5A
> **状态：** v1.4 — 钻取 API、算力验收分层、文档漂移、token_total / 单位展示已收敛（§6.4、FR-16～FR-18）

---

## 1. Introduction / Overview

ANI 需要一套 **可验收、可运营、可扩展** 的用量计量能力，覆盖 **AI Token** 与 **算力资源（CPU/GPU/内存）**，并分别服务于：

- **租户侧（Console）**：查看本租户用量趋势与明细
- **平台侧（BOSS）**：跨租户排行、趋势、钻取
- **业务侧（inference-service、kb-service 等）**：在推理/检索完成后 **可靠上报** 计量事件

当前仓库状态：

| 组件 | 状态 |
|------|------|
| Core `GET /api/v1/metering/usage` | 契约已冻结（租户上下文查询） |
| Core `POST /api/v1/metering/token-usage` | 契约已冻结（Token 写入） |
| Gateway `metering_resources.go` | local profile 实现 |
| `metering-service` 微服务 | **占位，目录未落地** |
| Console `usage-report` | 文档与部分前端已有 |
| BOSS 平台计量 7 模块 | 详文/PRD 已有；平台读 API 目标 **`GET /metering/usage/platform`**（`v1.yaml` **待 Core 批次 FR-8 合入**） |

本 PRD 定义 **metering-service 及关联 Console/BOSS 用量能力** 的 P0 产品边界。
**不做** 账单、发票、支付、对账结算（选项 5A）。

---

## 2. Goals

- 建立 **Token + 算力** 双轨计量闭环：采集 → 入账（Core）→ 查询（Console/BOSS）
- 落地 **metering-service** 微服务：统一承接 Services 层计量上报与编排，**不绕过 Core API**
- Console 租户用量页与 BOSS 平台计量页 **口径一致**（同一 `resource_type` 枚举）
- 明确 **Core 扩展批次**（平台跨租户查询、可选维度）与 **Services 实现批次** 的依赖顺序
- P0 可验收：inference Token 上报可查；**Token + 算力（CPU/GPU/内存）** 视角在 Console/BOSS 可切换展示（YAML 已冻结 `resource_type`）；算力 **真实数据** 以 live gate 为准（§11）

---

## 3. User Stories

### US-001: inference-service 上报 Token 用量

**Description:** 作为 inference-service 维护者，我希望在每次推理完成后可靠上报 Token 用量，以便租户与平台能看到真实消耗。

**Acceptance Criteria:**

- [ ] metering-service 提供内部接入点（gRPC 或经 Gateway 的服务间路径），接收 `tenant_id`、`model`、`input_tokens`、`output_tokens`、`request_id`、`instance_id`、`occurred_at`
- [ ] 上报 Core 时使用 `POST /api/v1/metering/token-usage`，且 **重试复用同一 `idempotency_key`**
- [ ] 重复上报返回 `duplicate` 或等价语义，不 double-count
- [ ] 上报失败写入 **独立 consumer group** 的重试队列；**可共用 NATS 集群**，不与 task-service 混用同一消费逻辑（见 §9-Q3）
- [ ] 集成测试证明：上报后 `GET /api/v1/metering/usage?resource_type=token_input|token_output` 在租户上下文可查

---

### US-002: 算力用量在 Core 可查询（Console/BOSS 可读）

**Description:** 作为租户管理员，我希望在 Console 看到 CPU/GPU/内存用量，以便了解算力消耗而不只关心 Token。

**Acceptance Criteria:**

- [ ] Console 用量页调用 `GET /api/v1/metering/usage`，`resource_type` 支持 `instance_cpu_seconds`、`instance_memory_gib_seconds`、`instance_gpu_seconds`（YAML 已冻结）
- [ ] 算力数据来源 **[Assumption]**：P0 以 Core 实例运行时/reconcile 聚合为主
- [ ] 若 **real provider 未就绪**：页面展示 **空态** 或 **`dev_profile` 数据 + 明显横幅**「非生产真实计量 / 待 live 验证」；**禁止伪造 0 值或假装生产就绪**（见 §9-Q2）
- [ ] **local/dev 环境**：GPU/CPU/内存 Tab 可切换；无 `instance_*` 数据时显示 **空态**（local profile 当前仅聚合 Token，见 §7.3）— **不** 因此判 P0-B 失败
- [ ] **live gate 环境**：至少一类 `instance_*` 在 Console 可查且与实例运行态一致
- [ ] 预设视角「GPU-Hours / CPU-Hours / Memory-GBHours」映射文档与 [`usage-report.md`](../../../../docs/console-modules/tenant/usage-report.md) 一致；**P0 不做前端单位换算**（见 FR-18）
- [ ] Typecheck/lint 通过；Console 在 browser 中验证 loading / empty / error 三态

---

### US-003: Console 租户用量报表（单租户）

**Description:** 作为租户用户，我希望在 Console 按时间范围查看用量趋势与明细，以便自助分析资源消耗。

**Acceptance Criteria:**

- [ ] 页面路径对齐 Console `用量与计量 / 租户用量报表`
- [ ] 支持 `start_time`、`end_time`（必填）、`resource_type`（可选）、`group_by=resource_type|az|day|hour`
- [ ] 趋势图与明细表 **共享同一查询上下文**
- [ ] 无数据、无权限、查询失败三态文案与 UI 可区分
- [ ] 页面 **不** 暴露 `POST /metering/token-usage` 给租户； **不** 展示账单金额字段
- [ ] Verify in browser using cursor-ide-browser MCP

---

### US-004: BOSS 平台跨租户用量（Token + 算力）

**Description:** 作为平台运营/财务分析人员，我希望在 BOSS 查看全平台各租户用量排行与趋势，以便做容量与成本分析（不含结算）。

**Acceptance Criteria:**

- [ ] BOSS「租户计费与用量」与 7 个平台计量专页（GPU/CPU/Memory/Token 等）口径与 [`boss-modules/metering/README.md`](../../../../docs/boss-modules/metering/README.md) 对齐
- [ ] 跨租户查询 **不得** 通过 Console 租户 JWT 轮询实现
- [ ] BOSS 正式读 API 为 **`GET /api/v1/metering/usage/platform`**（`operationId: getPlatformMeteringUsage`，见 FR-8）
- [ ] 平台 API 支持平台 RBAC、`group_by` 含 `tenant_id`、可选 `tenant_id` 筛选（后端鉴权）
- [ ] 现有 **`GET /api/v1/metering/usage` 语义不变**，仅租户 JWT 上下文；BOSS **不得** 用租户 API 冒充平台契约
- [ ] `Storage-GBDays`、`KB Queries` 在 YAML 未冻结前 UI 标注「待 API」或禁用，不伪造数据
- [ ] 支持从排行行钻取到单租户明细（**字段口径**对齐 Console usage-report；**API** 见 FR-16）
- [ ] 钻取 **必须** 调用 `GET /api/v1/metering/usage/platform?tenant_id={id}&start_time=…&end_time=…`（+ 可选 `resource_type`、`group_by=day|hour`）；**禁止** 用租户 `GET /metering/usage` 或切换 JWT 模拟租户
- [ ] Verify BOSS 页面 loading / empty / API-not-ready / error 四态

---

### US-005: kb-service 上报知识库查询用量（P1）

**Description:** 作为 kb-service 维护者，我希望在上报检索完成后记录 KB 查询次数，以便 BOSS「平台 KB Queries」有数据来源。

**Acceptance Criteria:**

- [ ] **[Assumption]** 依赖 Core **P1 批次 M-METERING-ENUM-A** 扩展 `resource_type=kb_query_count`（**不**与 FR-8 同批次，见 §9-Q5）
- [ ] kb 查询 **不得** 写入 `POST /metering/token-usage`（该接口为 Token 专用）；P1 须新增或扩展 Core **通用计量写入**契约（批次 M-METERING-ENUM-A 一并定义；具体 path 以 YAML 为准）
- [ ] metering-service 复用与 US-001 相同的 **编排模式**（idempotency、NATS 重试、Core SDK 写入）；写入 API 按 P1 YAML 落地
- [ ] YAML 未合入前，本 US 标记 **blocked**，不在 P0 验收范围

---

### US-006: metering-service 运维可观测

**Description:** 作为平台 SRE，我希望看到 metering 管道健康状态，以便排查「有推理无用量」类问题。

**Acceptance Criteria:**

- [ ] metering-service 暴露 health/readiness
- [ ] 指标至少包含：上报成功/失败计数、duplicate 计数、Core API 延迟、队列积压深度
- [ ] 日志含 `tenant_id`、`request_id`、`idempotency_key`（不含 Token 明文内容）

---

## 4. Functional Requirements

- **FR-1:** 系统必须提供 **metering-service** 微服务，作为 Services 层计量编排入口（占位目录落地为可部署服务）
- **FR-2:** inference-service **必须** 通过 metering-service（或经其定义的 SDK/内部契约）上报 Token，**禁止** 直连 MinIO/DB 自记用量
- **FR-3:** metering-service **必须** 通过 **Core SDK / Core OpenAPI** 调用 `POST /api/v1/metering/token-usage`，禁止 import Core 内部包或直接写 Core 数据库
- **FR-4:** Console 租户用量 **必须** 继续使用 `GET /api/v1/metering/usage`（租户 JWT 上下文 + **`scope:metering:read`**，见 FR-8 / FR-15）；不新增 Services 查询绕路
- **FR-5:** BOSS 平台计量 **必须** 调用 **`GET /api/v1/metering/usage/platform`**（FR-8）；禁止用租户 `GET /metering/usage` 轮询；禁止信任未授权 `tenant_id` 参数
- **FR-6:** 所有 POST 写入 **必须** 支持 `idempotency_key`；客户端重试 **必须** 复用同一 key
- **FR-7:** `MeteringUsageRecord.resource_type` P0 只使用 YAML 已冻结枚举：`instance_cpu_seconds`、`instance_memory_gib_seconds`、`instance_gpu_seconds`、`token_input`、`token_output`、`token_total`
- **FR-8:** **[Core 变更批次 · M-METERING-PLATFORM-A]** 必须在 `repo/api/openapi/v1.yaml` 扩展计量读接口 RBAC 与平台 path，与 BOSS 详文及 [`boss-phase0-gap-metering.md`](../../../../docs/boss-modules/governance/boss-phase0-gap-metering.md) 对齐：
  - **租户读（补全 RBAC）：** 现有 `GET /api/v1/metering/usage` 须补 `operationId: getMeteringUsage`、`x-ani-rbac-scope: scope:metering:read`
  - **平台读（新增 path）：** `GET /api/v1/metering/usage/platform`
  - **operationId（平台）：** `getPlatformMeteringUsage`
  - **x-ani-rbac-scope（平台）：** `scope:metering:platform:read`（与租户读 **分离**；**不** 隐含 `scope:metering:read`，见 §9-Q1、FR-15）
  - **写入（不变）：** `POST /api/v1/metering/token-usage` 保持 `scope:metering:write`
  - **Query（必填）：** `start_time`, `end_time`（date-time）
  - **Query（可选）：** `resource_type`（复用 `MeteringUsageRecord.resource_type` enum）、`group_by`（平台扩展 enum：**含** `tenant_id`，并保留 `day`、`hour`；**不含** 对租户 API 的破坏性变更）、`tenant_id`（平台 RBAC 下可选筛选）
  - **Response:** 复用 `MeteringUsageResponse` / `MeteringUsageRecord`；平台视角下 **`items[].tenant_id` 必填**
  - **安全:** fail-closed；可选 `tenant_id` 须平台 RBAC 校验，**不得** 信任任意 query 越权
  - **不变更:** 现有 `GET /api/v1/metering/usage` 保持租户 JWT 上下文；其 `group_by` enum（`resource_type|az|day|hour`）**不** 扩展 `tenant_id`
  - **不推荐（Non-Goal）:** 同一 path `/metering/usage` 靠不同 RBAC token 区分租户/平台双语义；平台 scope **不** 自动包含租户 scope
- **FR-9:** 算力用量 P0 **以 Core 聚合为准**；metering-service **不重复实现** CPU/GPU/秒级采集逻辑，除非 Core 批次明确要求 Services 侧补报
- **FR-10:** 修改任何 OpenAPI 契约 **必须先改 YAML**，再实现 handler、测试、SDK/前端 codegen
- **FR-11:** 修改 `services/v1.yaml` 的 PR（若 P1 新增 Services 对外接口）**必须** 同步 Console 前端 API codegen 结果
- **FR-12:** Console/BOSS 在 **real provider 未就绪** 时 **必须** 区分空态与 `dev_profile` 联调数据，并展示「非生产真实计量 / 待 live 验证」类边界提示；**禁止** 用 0 或假数据冒充生产计量
- **FR-13:** metering-service 上报重试 **必须** 使用 **独立 NATS consumer group**（可共用 NATS 集群）；**禁止** P0 与 task-service 共用同一消费逻辑或部署单元
- **FR-14:** `POST /metering/token-usage` 的 `labels` **P0 不强制** schema；**P1** 在集成文档 **推荐** `model_id`、`inference_service_id`；**P2** 再评估是否写入 OpenAPI 可选字段
- **FR-15:** Gateway 对计量 RBAC **必须** 按 path 分轨鉴权（与 §9-Q1 定稿一致）：
  - `GET /metering/usage` → 校验 `scope:metering:read`；租户 ID 取自 JWT；**忽略或拒绝** 未授权 `tenant_id` query
  - `GET /metering/usage/platform` → 校验 `scope:metering:platform:read`；默认全平台；可选 `tenant_id` query 须二次 RBAC 校验
  - `POST /metering/token-usage` → 校验 `scope:metering:write`（已有）
  - 角色绑定：**租户用户/租户管理员** → `scope:metering:read`；**BOSS 平台运营** → `scope:metering:platform:read`；**inference/metering-service 服务账号** → `scope:metering:write`（仅上报，非平台全览）
- **FR-16:** BOSS 单租户钻取 **必须** 使用 **`GET /api/v1/metering/usage/platform`** 并带 **`tenant_id` query**（须 `scope:metering:platform:read` + 后端 RBAC 校验）；**禁止** 调用租户 `GET /metering/usage`、禁止 JWT 轮询、禁止 impersonate 租户上下文
- **FR-17:** `token_total` **P0 展示策略（已定稿）：** Console **不** 单独设「Token 合计」Tab；若 API 在未筛 `resource_type` 时返回 `token_total` 行，表格 **可展示**；BOSS 聚合页 KPI **可** 使用 `resource_type=token_total` 查询；Input/Output 专页仍分别固定 `token_input` / `token_output`
- **FR-18:** UI 视角名（GPU-Hours、CPU-Hours 等）为 **别名**；表格 **必须** 原样展示 API 的 `total_quantity` + `unit`；**P0 禁止** 前端自行做 seconds→hours 等单位换算（避免与后端口径漂移；P2 可评估统一展示层）

---

## 5. Non-Goals (Out of Scope)

- **NG-1:** 账单、发票、支付、对账、结算金额计算（用户选项 5A）
- **NG-2:** 定价策略、折扣、成本分摊模型
- **NG-3:** 配额超限实时告警（另立 PRD；本 PRD 仅计量事实层）
- **NG-4:** 租户在 Console 直接调用 `POST /metering/token-usage`
- **NG-5:** 自造未在 YAML 声明的 API path、operationId、错误码
- **NG-6:** metering-service 对外暴露第二套与 Core 冲突的计量语义
- **NG-7:** P0 实现 `storage_gb_days`、`kb_query_count` 展示与上报（**P1 批次 M-METERING-ENUM-A**，见 §9-Q5）
- **NG-8:** 用 **同 path** `GET /metering/usage` + 不同 RBAC 承载平台/租户双语义（与 BOSS 文档及 Core 路径惯例不一致）

---

## 6. Design Considerations

### 6.1 Console（租户侧）

- 主维护文档：[`usage-report.md`](../../../../docs/console-modules/tenant/usage-report.md)
- UI：TDesign；图表 ECharts
- 预设视角在同一页切换，不拆独立 REST 资源
- 与 [`prd-console-usage-report.md`](../../console/tenant/prd-console-usage-report.md) 对齐；**冲突优先级：** OpenAPI > 本文（母 PRD）> [`usage-report.md`](../../../../docs/console-modules/tenant/usage-report.md) > Console 子 PRD
- **Console 子 PRD** 仅作历史辅助；跨端边界与 P0 分期以 **本文 + UX** 为准

### 6.2 BOSS（平台侧）

- 域索引：[`boss-modules/metering/README.md`](../../../../docs/boss-modules/metering/README.md)
- 聚合入口：[`tenant-usage-billing.md`](../../../../docs/boss-modules/tenant/tenant-usage-billing.md)
- 7 个专页 PRD 已存在；本 PRD 作为 **跨模块母 PRD**，不重复逐页细节

### 6.3 resource_type 视角映射（UI 名 ↔ YAML）

| UI 视角 | resource_type | P0 |
|---------|---------------|-----|
| GPU-Hours | `instance_gpu_seconds` | ✅ |
| CPU-Hours | `instance_cpu_seconds` | ✅ |
| Memory-GBHours | `instance_memory_gib_seconds` | ✅ |
| Input Tokens | `token_input` | ✅ |
| Output Tokens | `token_output` | ✅ |
| Storage-GBDays | `storage_gb_days` | ❌ P1（enum 扩展批次，见 §9-Q5） |
| KB Queries | `kb_query_count` | ❌ P1（enum 扩展批次，见 §9-Q5） |

### 6.4 UI 展示定稿（v1.4）

| 主题 | 决策 |
|------|------|
| **token_total** | Console 无独立 Tab；表格在未筛 resource_type 时可展示 API 返回的 `token_total` 行；BOSS 聚合 KPI 可用 `token_total` |
| **单位展示** | P0 原样展示 API `unit` + `total_quantity`；视角 Tab 文案不等同于换算后单位（FR-18） |
| **BOSS 钻取** | 仅 `GET /metering/usage/platform?tenant_id=…`；字段对齐 Console，API 不同（FR-16） |
| **local 算力** | local profile 可能仅有 Token 数据；算力 Tab 空态为 **预期**，非缺陷（§11 P0-B） |

---

## 7. Technical Considerations

### 7.1 选项 3D — 分层建议（Core vs Services）

| 能力 | 建议归属 | 理由 |
|------|----------|------|
| 计量事实存储与租户查询 | **Core** `GET /metering/usage` | 已有契约；Console 已对齐 |
| Token 写入 | **Core** `POST /metering/token-usage` | 已有契约；幂等/RBAC 在 Gateway |
| 平台跨租户查询 | **Core** `GET /metering/usage/platform`（FR-8） | 与 BOSS 7 模块、`tenant-usage-billing` 文档一致；租户/平台 **分 path** |
| inference/kb 事件编排、重试、队列 | **metering-service** | Services 层业务管道 |
| 算力秒级采集 | **Core runtime/reconcile** | 实例生命周期已在 Core；Services 不重复采集 |

**结论（3D）：** P0 **不新增** `/api/v1/svc/metering/*` 作为租户/平台查询入口；metering-service 以 **写入编排 + 内部 gRPC** 为主，读写 **统一落 Core API**。BOSS 平台读 **固定** `GET /metering/usage/platform`，Console 租户读 **固定** `GET /metering/usage`。

### 7.1.1 FR-8 API 形态决策（已收敛）

| 维度 | 租户（Console） | 平台（BOSS） |
|------|-----------------|--------------|
| Path | `GET /api/v1/metering/usage` | `GET /api/v1/metering/usage/platform` |
| operationId | `getMeteringUsage`（FR-8 补全） | `getPlatformMeteringUsage` |
| x-ani-rbac-scope | **`scope:metering:read`** | **`scope:metering:platform:read`** |
| 上下文 | JWT 租户 | 平台 RBAC |
| `group_by` | `resource_type` / `az` / `day` / `hour` | 含 **`tenant_id`** + `day` / `hour` |
| `items[].tenant_id` | 可不填或等于当前租户 | **必填** |

依据：[`boss-phase0-gap-metering.md`](../../../../docs/boss-modules/governance/boss-phase0-gap-metering.md)、[`tenant-usage-billing.md`](../../../../docs/boss-modules/tenant/tenant-usage-billing.md)、BOSS metering 7 专页。当前 `v1.yaml` **尚未** 声明 platform path，FR-8 为待实施 Core 批次。

### 7.2 选项 4E — 消费者优先级

| 优先级 | 消费者 | P0 交付 |
|--------|--------|---------|
| **P0** | inference-service | Token 上报管道 E2E |
| **P0** | Console 租户管理员 | 用量报表可读（Token + 算力 YAML 已冻结项） |
| **P0** | BOSS 平台运营 | 跨租户排行/趋势（依赖 FR-8 Core 批次） |
| **P1** | kb-service | KB 查询上报（依赖 resource_type 扩展） |
| **P2** | 其他 Services | 统一走 metering-service 管道，按 YAML 扩展 |

### 7.3 依赖与成熟度

- local profile 可证明 API/状态机；**不能** 声称 production-ready
- **local profile 现状（2026-07-09）：** `LocalMeteringService.QueryUsage` **仅聚合 Token**（`token_input` / `token_output` / `token_total`），**不** 产出 `instance_*`；P0-B Console 算力 Tab 在 local 下预期为 **空态 + dev 横幅**
- real-provider 需 live gate 证明 Token 上报与 **算力** 用量查询在真实环境一致
- 参考：`pkg/ports/metering.go`、`pkg/adapters/runtime/local_metering_service.go`

### 7.4 关键文件（实现参考）

| 路径 | 说明 |
|------|------|
| `repo/api/openapi/v1.yaml` | Core 计量契约（先改此文件） |
| `repo/services/ani-gateway/internal/router/metering_resources.go` | Gateway handler |
| `repo/services/metering-service/` | **待创建** |
| `repo/frontends/console/src/routes/_authenticated/usage.tsx` | Console 用量页 |
| `repo/frontends/boss/` | BOSS 计量页（若已 scaffold） |

---

## 8. Success Metrics

- inference 完成后 **99%** Token 事件在 60s 内入账（P0 实验环境可测）
- Console 用量页 P95 查询 < 3s（local profile 基线）
- BOSS 平台页可在单屏展示 Top N 租户排行（N≥20）且不逐租户轮询 JWT
- 重复 `idempotency_key` 上报 **0** double-count（自动化测试覆盖）
- 文档/契约/Console/BOSS 四者 `resource_type` 口径 **100%** 对齐 YAML 枚举

---

## 9. 决策记录（原 Open Questions · 已收敛）

以下原为开放问题；现按产品推荐方案 **写入 PRD**，实现与评审以此为准。若 Core/Gateway 评审有异议，在 PR 中显式推翻并回写本文。

### Q1：计量读写的 RBAC 权限叫什么？（已定稿）

| 项 | 结论 |
|----|------|
| **决策** | 计量 RBAC **三件套**，path 与 scope **一一对应**： |

| 接口 | scope | 谁用 |
|------|-------|------|
| `GET /api/v1/metering/usage` | **`scope:metering:read`** | Console 租户用户 |
| `GET /api/v1/metering/usage/platform` | **`scope:metering:platform:read`** | BOSS 平台运营 |
| `POST /api/v1/metering/token-usage` | **`scope:metering:write`** | inference / metering-service（YAML 已有，不变） |

| 项 | 说明 |
|----|------|
| **原则** | 平台 scope **不** 隐含租户 scope；Gateway **按 path 分轨**鉴权（见 **FR-15**） |
| **理由** | 与现有 `scope:{资源}:{动作}` 惯例一致（如 `scope:networks:read`、`scope:metering:write`）；租户只看自己、平台可看全租户，权限必须分开，避免越权 |
| **租户读补全** | 当前 `GET /metering/usage` 在 `v1.yaml` **尚未** 声明 scope；FR-8 批次 **一并补** `getMeteringUsage` + `scope:metering:read` |
| **不推荐** | 租户/平台共用 `scope:metering:read`；使用 `scope:platform:metering:read` 或 `scope:boss:metering:read` 等与 Core 命名惯例不一致的字符串 |

**Gateway 行为（定稿）：**

- **租户 path：** 校验 `scope:metering:read` → 从 JWT 取 `tenant_id` → 忽略或拒绝未授权 `tenant_id` query → 只返回本租户数据
- **平台 path：** 校验 `scope:metering:platform:read` → 默认全平台 → 若带 `tenant_id` query 须二次 RBAC 校验 → `items[].tenant_id` 必填

**角色绑定（定稿）：**

| 角色 | scope |
|------|-------|
| 租户用户 / 租户管理员 | `scope:metering:read` |
| BOSS 平台运营（只读） | `scope:metering:platform:read` |
| 需同时看平台排行 + 单租户 Console 口径 | 两个 scope **分别授予**（仍走对应 API，不混 path） |
| inference / metering-service 服务账号 | `scope:metering:write`（仅上报） |

**给 Core/Gateway 一句话：** 计量读权限定为 **`scope:metering:read`（租户）** 与 **`scope:metering:platform:read`（平台）** 两个 scope，分别挂在两个 GET path；写入仍为 **`scope:metering:write`**。

---

### Q2：算力数据若尚未在真实 K8s 跑通，Console/BOSS 怎么显示？

| 项 | 结论 |
|----|------|
| **决策** | **允许** 在 local/dev 环境展示 `dev_profile` 联调数据，但 **必须** 有醒目横幅：「非生产真实计量 / 待 live 验证」；**无数据** 时显示空态说明，**禁止伪造 0 或假数字** |
| **理由** | 研发需要联调接口；对客户/运营不能误导为「生产已就绪」。符合 local profile ≠ production ready 的项目规则 |
| **落地** | 见 **FR-12**、US-002 |

---

### Q3：计量上报失败的重试队列，和 task-service 放一起还是分开？

| 项 | 结论 |
|----|------|
| **决策** | **共用 NATS 集群**，**独立 consumer group**；metering-service **不** 与 task-service 混部署、不共用同一套消费业务逻辑 |
| **理由** | 共用中间件省运维；消费组分开避免「大任务堵死计量上报」。类比：同一快递站，计量件单独分拣口 |
| **落地** | 见 **FR-13**、US-001 |

---

### Q4：`labels` 要不要规定 `model_id`、`inference_service_id`？

| 项 | 结论 |
|----|------|
| **决策** | **P0 不强制**；**P1** 在 metering/inference 集成文档 **推荐** 上述 key；**P2** 再评估是否升格为 OpenAPI 可选字段 |
| **理由** | P0 目标是 Token **能报、能查**；固定 labels 用于 BOSS 钻取，属增强能力，不应拖慢主链路 |
| **落地** | 见 **FR-14** |

---

### Q5：`storage_gb_days`、`kb_query_count` 是否和 FR-8 同一 YAML 批次？

| 项 | 结论 |
|----|------|
| **决策** | **P1 单独 Core 批次**扩展 enum；**不** 与 FR-8（`GET /metering/usage/platform`）绑在同一 P0 PR |
| **理由** | FR-8 修的是 BOSS **跨租户查询通路**；storage/kb 是 **新增统计品种**，且 kb 依赖 US-005 上报管道。P0 先打通 platform 读 + Token 闭环更稳 |
| **P0 UI** | Storage-GBDays、KB Queries 视角 **禁用或标注「待 API」**（US-004） |
| **落地** | P1 批次名建议 **M-METERING-ENUM-A**；BOSS gap 文档已规划 enum 值 |

---

### 决策汇总

| # | 问题 | 结论 | 阻塞 P0？ |
|---|------|------|-----------|
| Q1 | 计量 RBAC 三件套 | 租户 `scope:metering:read` + 平台 `scope:metering:platform:read` + 写 `scope:metering:write` | 是（FR-8/FR-15） |
| Q2 | 无真实算力时 UI | 横幅/空态，不伪造 0 | 部分 |
| Q3 | NATS 部署 | 共用集群 + 独立 consumer group | 部分 |
| Q4 | labels 规范 | P0 不强制，P1 推荐 | 否 |
| Q5 | storage/kb enum | P1 单独批次 | 否（KB/Storage 专页） |

---

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| **Product line** | **cross-cutting**：`services`（metering-service 主）+ `console`（租户用量）+ `boss`（平台计量） |
| **Code scope** | `repo/services/metering-service/`（新建）；`repo/services/ani-gateway/`（路由，若需）；`repo/frontends/console/`；BOSS 前端；**Core 变更** `repo/api/openapi/v1.yaml` + handler（FR-8 批次，Core 团队） |
| **OpenAPI authority** | 租户读/Token 写：**Core `v1.yaml` 只读消费** + **FR-8 扩展需 Core 变更批次**；**不自造 API** |
| **Frozen exclusions** | 不在本 PRD 修改 `services/v1.yaml` 业务资源（P0）；不开发 model/kb 业务逻辑本身 |
| **idempotency_key** | **required on:** `POST /api/v1/metering/token-usage`；metering-service 内部转发必须透传 |
| **Module main doc** | Console: [`usage-report.md`](../../../../docs/console-modules/tenant/usage-report.md)；BOSS: [`metering/README.md`](../../../../docs/boss-modules/metering/README.md) |
| **Services 冻结边界** | Core 团队不猜 Services 业务；metering-service 只做计量管道，不做推理/RAG |

---

## 11. 交付分期建议

| 阶段 | 范围 | 验收 |
|------|------|------|
| **P0-A** | Core FR-8：`GET /metering/usage/platform` YAML + Gateway；metering-service 骨架 + Token 上报 E2E | inference → metering → Core → Console Token 可查 |
| **P0-B** | Console usage 页完善（Token + 算力视角 Tab、图表、group_by） | browser 三态 + `GET /metering/usage`；**local：** Token 有数据、算力可空态；**live gate：** 至少一类 `instance_*` 可查 |
| **P0-C** | BOSS **聚合页** + **5 个 P0 专页**（GPU / CPU / Memory / Input Tokens / Output Tokens）+ Storage/KB 路由占位（api-not-ready） | `GET /metering/usage/platform` + 钻取 FR-16；无 JWT 轮询 |
| **P1** | kb-service 接入 + **M-METERING-ENUM-A**（enum + **KB 写入 API** + `storage_gb_days`）+ Storage/KB 专页启用 | US-005 unblock |
| **P2** | labels OpenAPI 可选字段评估（FR-14）；UI 单位统一换算（若产品要求） | enum + 钻取增强 |

---

## 12. 相关文档

| 类型 | 路径 |
|------|------|
| Console 主维护 | `repo/services/docs/console-modules/tenant/usage-report.md` |
| Console PRD（辅助） | `repo/services/tasks/modules/prd/console/tenant/prd-console-usage-report.md` |
| UX（交互） | `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md` |
| BOSS metering 索引 | `repo/services/docs/boss-modules/metering/README.md` |
| BOSS 平台 Input Tokens PRD | `repo/services/tasks/modules/prd/boss/metering/prd-boss-platform-input-tokens.md` |
| BOSS metering GAP | `repo/services/docs/boss-modules/governance/boss-phase0-gap-metering.md` |
| Core ports | `repo/pkg/ports/metering.go` |
| Team Guide | `ANI-SERVICES-TEAM-GUIDE.md` §1.1 metering-service |

### 12.1 文档漂移说明（权威源）

**唯一 API 权威源：** `repo/api/openapi/v1.yaml`（截至 v1.4 撰写时）：

| 能力 | v1.yaml 实际状态 | 部分 BOSS/gap 文档声称 | 以谁为准 |
|------|------------------|------------------------|----------|
| `GET /metering/usage/platform` | **未声明** | ADDED-TO-YAML | **YAML**；FR-8 待实施 |
| `getMeteringUsage` + `scope:metering:read` | path 存在，**缺** operationId / scope | SDK metadata 已有 operationId | **FR-8 合入 YAML** |
| `storage_gb_days` / `kb_query_count` enum | **未在** `MeteringUsageRecord` enum | gap 称 P1 已合入 | **YAML**；P1 M-METERING-ENUM-A |

实现、SPEC、验收 **不得** 因 BOSS 原型或 gap 摘要中的「ADDED-TO-YAML」而假设已上线；以 **YAML + 本 PRD FR-8 / P1 批次** 为准。

---

## 13. 文档同步状态

- 创建日期：**2026-07-09**
- v1.1 更新：**2026-07-09** — FR-8 收敛为 `GET /api/v1/metering/usage/platform`
- v1.2 更新：**2026-07-09** — §9 开放问题按推荐方案收敛；新增 FR-12～FR-14
- v1.3 更新：**2026-07-09** — Q1 定稿：计量 RBAC 三件套 + FR-15 Gateway 分轨规则；FR-8 补全租户读 scope
- v1.4 更新：**2026-07-09** — FR-16 钻取 API；FR-17 token_total；FR-18 单位展示；§6.4 / §7.3 local 算力；§11 分期；§12.1 文档漂移；US-005 kb 写入与 token-usage 分离
- 基于用户确认：**1E / 2C / 3D / 4E / 5A**
