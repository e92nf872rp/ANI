# PRD: 统一实例可观测性（Core + Console）

> Revised: 2026-07-03
> 状态：已确认（含 OQ 决策）
> 扩展/替代：`prd-console-container-observability.md`（2026-06-17 简版）
> 详文：`repo/services/docs/console-modules/compute/container-observability.md`
> UX：`repo/services/tasks/modules/ux/console/compute/ux-console-instance-observability.md`
> Handler 契约：`TASK-CORE-005` + VM console + Sandbox security-events

## 1. Introduction / Overview

Console 中所有**计算实例**（`vm / container / gpu_container / sandbox / batch_job / notebook / k8s_cluster / bare_metal / dpu_node`）都需要在**实例详情**内完成运维观测：日志、事件、资源/GPU 指标，以及按 kind 差异化的终端/console/安全事件能力。

本 PRD 覆盖 **Core API 实现 + Console UI** 联合交付，并明确两层指标能力：

| 层级 | 用途 | API |
|------|------|-----|
| **快照** | 当前 CPU/内存/GPU/网络用量卡片 | `GET /api/v1/instances/{instance_id}/metrics` → `InstanceMetrics` |
| **时序** | 15m / 1h / 6h / 24h 趋势折线图 | `GET /api/v1/observability/query`（PromQL 代理） |

**已决策边界：**

- 快照数据由 Core adapter 从底层 exporter 聚合填入 `InstanceMetrics`（SPEC 定 exporter 选型与映射）；**不向 Console 暴露 Prometheus 地址**。
- 时序图表**必须**走 PromQL 代理；PromQL 模板与 label 映射在 SPEC/运维文档冻结，Console 不得硬编码未文档化 label。
- exec WebSocket 协议细节留 SPEC；PRD 只要求「能连上」。
- `batch_job`、`notebook` **不提供** exec Tab。
- provider 不支持指标的 kind **隐藏**指标 Tab（不展示空态 Tab）。
- GPU 等字段在 metrics.k8s.io 不可用时，通过**扩展 adapter**从其它 exporter 写入 `InstanceMetrics`（实现细节 SPEC 定）。

## 2. Goals

- Core 落地 TASK-CORE-005 四个观测端点 + `createInstanceConsoleSession` + `listInstanceSecurityEvents`。
- Console 为全部 9 种 `kind` 提供统一观测框架，按 kind 显示/隐藏 Tab。
- 指标 Tab：**快照卡片**（`getInstanceMetrics`）+ **时序图**（`/observability/query` + 冻结 PromQL 模板）。
- `gpu_container` 展示 GPU 利用率与显存（快照 + 时序，数据源分别由 adapter / PromQL 承担）。
- exec 仅 container / gpu_container / sandbox；VM 使用 console/VNC Tab。
- Sandbox 额外提供安全事件 Tab。

## 3. User Stories

### US-001: Core — 实例日志列表 handler

**Description:** 作为平台运维者，我希望通过 Core API 分页拉取实例日志，以便 Console 展示容器 stdout/stderr。

**Acceptance Criteria:**

- [ ] `GET /api/v1/instances/{instance_id}/logs` 返回 `200 + InstanceLogListResponse`
- [ ] 支持 Query：`limit`（默认 100）、`cursor`；响应含 `next_cursor`
- [ ] 实例不存在返回 `404`；无读权限返回 `403`；未认证返回 `401`
- [ ] 日志条目字段与 YAML `InstanceLogEntry` 一致
- [ ] RBAC：`scope:instances:read`
- [ ] 单元/集成测试覆盖成功路径与 404/403

### US-002: Core — 实例事件列表 handler

**Description:** 作为平台运维者，我希望查看实例关联事件，以便排查调度/重启原因。

**Acceptance Criteria:**

- [ ] `GET /api/v1/instances/{instance_id}/events` 返回 `200 + InstanceEventListResponse`
- [ ] 支持 `limit`、`cursor` 分页
- [ ] 事件字段与 YAML `InstanceEvent` 一致
- [ ] 错误语义：`401` / `403` / `404`
- [ ] RBAC：`scope:instances:read`

### US-003: Core — 实例指标快照 handler（含 adapter 扩展）

**Description:** 作为平台运维者，我希望获取实例当前资源/GPU 指标快照，以便 Console 展示监控卡片。

**Acceptance Criteria:**

- [ ] `GET /api/v1/instances/{instance_id}/metrics` 返回 `200 + InstanceMetrics`
- [ ] 响应必填：`instance_id`、`timestamp`；不可采集字段为 `null`，**禁止**用 0 代替缺失
- [ ] adapter 可从多个 exporter 聚合（如 metrics.k8s.io + DCGM 等），细节在 SPEC 冻结；Gateway 不暴露 Prometheus 地址
- [ ] `gpu_container` 在 exporter 可用时填充 GPU 相关字段
- [ ] 错误语义：`401` / `403` / `404`
- [ ] RBAC：`scope:instances:read`

### US-004: Core — 实例 exec 会话 handler

**Description:** 作为授权运维者，我希望创建 exec 会话，以便在 running 容器内排障。

**Acceptance Criteria:**

- [ ] `POST /api/v1/instances/{instance_id}/exec` 返回 `200 + InstanceExecSession`（含 `ws_url`、`expires_at`）
- [ ] Request body 必填 `idempotency_key`
- [ ] 实例非 `running` 返回 `422`；无 `scope:instances:exec` 返回 `403`
- [ ] 同一 `idempotency_key` 重试幂等
- [ ] RBAC：`scope:instances:exec`

### US-005: Core — VM console 会话 handler

**Description:** 作为 VM 运维者，我希望申请 console/VNC 会话，以便在 running 虚机内排障。

**Acceptance Criteria:**

- [ ] `POST /api/v1/instances/{instance_id}/console` 返回 `200 + InstanceConsoleSession`
- [ ] 仅 `kind=vm`；非 `running` 拒绝
- [ ] 支持 `protocol`：`console / vnc / novnc / serial`（与 `vm-management.md` 一致）
- [ ] RBAC：`scope:instances:console`

### US-006: Core — Sandbox 安全事件 handler

**Description:** 作为 Sandbox 用户，我希望查看实例安全事件，以便发现异常行为。

**Acceptance Criteria:**

- [ ] `GET /api/v1/instances/{instance_id}/security-events` 返回 `200 + InstanceSecurityEventListResponse`
- [ ] 支持 Query：`severity`、`limit`
- [ ] 错误语义：`401` / `403` / `404`
- [ ] RBAC：`scope:instances:read`

### US-007: Console — 统一观测 Tab 壳层（全 kind）

**Description:** 作为租户用户，我希望在任何计算实例详情中看到一致的观测入口，并按实例类型看到合适 Tab。

**Acceptance Criteria:**

- [ ] 所有 `kind` 实例详情均挂载「可观测性」区域
- [ ] 通用 Tab（provider 支持时）：日志、事件
- [ ] 指标 Tab：仅当该 kind 的 metrics capability 为 supported 时展示（见 §6 矩阵）；不支持则**隐藏 Tab**
- [ ] exec Tab：仅 `container`、`gpu_container`、`sandbox`
- [ ] console Tab：仅 `vm`
- [ ] 安全事件 Tab：仅 `sandbox`
- [ ] 上下文条：name、id、state、kind；`deleted` 不可进入
- [ ] browser 验证：container、vm、sandbox 三种 kind 的 Tab 差异

### US-008: Console — 日志 Tab

**Description:** 作为租户用户，我希望分页浏览实例日志，以便定位应用错误。

**Acceptance Criteria:**

- [ ] 调用 `listInstanceLogs`，默认 `limit=100`，cursor 分页
- [ ] 展示：时间、级别、消息、container/stream（若有）
- [ ] 空日志展示空态；API 失败展示错误态 + `request_id`
- [ ] Typecheck/lint 通过；browser 验证 loading / empty / error

### US-009: Console — 事件 Tab

**Description:** 作为租户用户，我希望按时间查看实例事件，以便理解重启/告警原因。

**Acceptance Criteria:**

- [ ] 调用 `listInstanceEvents`，cursor 分页
- [ ] 展示：occurred_at、type、reason、message、count
- [ ] Typecheck/lint 通过；browser 验证 loading / empty / error

### US-010: Console — 指标 Tab（快照卡片）

**Description:** 作为租户用户，我希望一眼看到当前资源/GPU 用量。

**Acceptance Criteria:**

- [ ] 调用 `getInstanceMetrics`；展示 `timestamp` 与最后刷新时间
- [ ] 卡片：CPU、内存 used/total、网络 RX/TX；null 显示「暂不可用」
- [ ] `gpu_container` 额外展示 GPU 利用率、显存 used/total
- [ ] 手动刷新 + 可选 30s 自动刷新
- [ ] browser 验证 loading / partial-null / error

### US-011: Console — 指标 Tab（PromQL 时序图）

**Description:** 作为租户用户，我希望查看选定时间范围内的指标趋势。

**Acceptance Criteria:**

- [ ] 时间范围：15m / 1h / 6h / 24h（默认 1h）
- [ ] 通过 `GET /api/v1/observability/query` 拉取 PromQL 结果；RBAC：`scope:observability:read`
- [ ] PromQL 语句来自**已冻结模板**（SPEC/运维文档），按 `instance_id` + kind 注入参数；Console 不硬编码未文档化 label
- [ ] 至少 2 条曲线：CPU 利用率、内存使用率；`gpu_container` 额外 GPU 利用率、显存使用率
- [ ] PromQL 失败或无数据：图表区域展示错误/空态，**不伪造曲线**
- [ ] 不展示 Prometheus 内部地址
- [ ] browser 验证 loading / empty / error

### US-012: Console — 终端 Tab（exec）

**Description:** 作为授权运维者，我希望在浏览器内连接容器 exec。

**Acceptance Criteria:**

- [ ] 适用 kind：`container`、`gpu_container`、`sandbox`（**不含** batch_job、notebook）
- [ ] 仅 `running` + `scope:instances:exec` 可连接
- [ ] POST exec 带 `idempotency_key`；成功后使用 `ws_url` 建立连接（**能连上即可**；xterm/鉴权/resize 等留 SPEC）
- [ ] session 过期提示重新连接
- [ ] browser 验证 disabled / connecting / error

### US-013: Console — VM 控制台 Tab

**Description:** 作为 VM 运维者，我希望申请并打开 console/VNC 会话。

**Acceptance Criteria:**

- [ ] 仅 `kind=vm`；调用 `createInstanceConsoleSession`
- [ ] 协议：console / vnc / serial
- [ ] 仅 `running` 可点击
- [ ] browser 验证 disabled / connecting / error

### US-014: Console — Sandbox 安全事件 Tab

**Description:** 作为 Sandbox 用户，我希望查看安全事件列表。

**Acceptance Criteria:**

- [ ] 仅 `kind=sandbox`
- [ ] 调用 `listInstanceSecurityEvents`；支持 severity 筛选
- [ ] browser 验证 loading / empty / error

### US-015: Kind 差异化指标与 Tab 可见性

**Description:** 作为不同实例类型的用户，我希望只看到该 kind 支持的观测能力。

**Acceptance Criteria:**

- [ ] 指标 Tab 按 §6 capability 矩阵隐藏/展示（不支持则不渲染 Tab）
- [ ] `gpu_container` 在快照与时序中展示 GPU 系列（PromQL 模板 SPEC 冻结）
- [ ] 字段 null 时不隐藏整个指标 Tab（Tab 可见时），仅相关卡片「暂不可用」

## 4. Functional Requirements

- **FR-1:** Core 必须实现 `listInstanceLogs`、`listInstanceEvents`、`getInstanceMetrics`、`createInstanceExecSession`、`createInstanceConsoleSession`、`listInstanceSecurityEvents`，路径与 `v1.yaml` 一致。
- **FR-2:** 所有 GET 观测接口必须校验租户隔离，跨租户返回 `404`。
- **FR-3:** `getInstanceMetrics` 必须符合 `InstanceMetrics` schema；adapter 可聚合多 exporter 结果（SPEC 定）。
- **FR-4:** `createInstanceExecSession` 必须要求 `idempotency_key` 并幂等。
- **FR-5:** exec 在 `state != running` 时返回 `422`。
- **FR-6:** Console 指标**快照**必须调用 `getInstanceMetrics`。
- **FR-7:** Console 指标**时序图**必须调用 `/observability/query`，使用冻结 PromQL 模板。
- **FR-8:** Console 不得向客户端暴露 Prometheus 服务地址。
- **FR-9:** exec Tab 仅对 `container`、`gpu_container`、`sandbox` 展示；**不得**对 `batch_job`、`notebook` 展示 exec Tab。
- **FR-10:** VM 使用 console Tab（`createInstanceConsoleSession`），不得对 VM 展示 exec Tab。
- **FR-11:** Sandbox 展示安全事件 Tab（`listInstanceSecurityEvents`）。
- **FR-12:** 当 kind 的 metrics capability 为 unsupported 时，Console **必须隐藏**指标 Tab。
- **FR-13:** GPU 指标在底层 exporter 不可用时字段为 `null`；adapter 扩展方案在 SPEC 实现，UI 不伪造 0。

## 5. Non-Goals (Out of Scope)

- 实例级独立 Dashboard API（YAML 未声明 `/dashboard`）
- K8s 工作负载级观测（`k8s-workloads.md`）
- 修改 OpenAPI 或新增 instance 时序 REST 端点
- 本 PRD 内接入 Prometheus real provider（依赖 M1-OBS-A 后续 provider 批次；PRD 定义 Console/Core 契约行为）
- exec WebSocket 实现细节（组件选型、鉴权头、TTY resize → SPEC）
- PromQL 模板正文与 label 映射表维护（→ SPEC + 运维文档）
- Services 后端实现
- Boss 平台大盘 UI
- `batch_job`、`notebook` 的 exec Tab

## 6. Design Considerations

### Kind × Tab 矩阵

| kind | 日志 | 事件 | 指标 Tab | exec | console/VNC | 安全事件 |
|------|------|------|----------|------|-------------|----------|
| container | ✅ | ✅ | ✅ | ✅ | — | — |
| gpu_container | ✅ | ✅ | ✅ | ✅ | — | — |
| sandbox | ✅ | ✅ | ✅ | ✅ | — | ✅ |
| vm | ✅ | ✅ | ✅ | — | ✅ | — |
| batch_job | ✅ | ✅ | ✅ | — | — | — |
| notebook | ✅ | ✅ | ✅ | — | — | — |
| k8s_cluster | ✅ | ✅ | **隐藏** | — | — | — |
| bare_metal | ✅ | ✅ | **隐藏** | — | — | — |
| dpu_node | ✅ | ✅ | **隐藏** | — | — | — |

**metrics capability 判定（Console）：** 由 SPEC 提供 kind → supported | unsupported 映射；首期 `k8s_cluster`、`bare_metal`、`dpu_node` 为 unsupported（隐藏指标 Tab）。

### 指标双通道示意

```text
Console 指标 Tab
├── 快照卡片 ← GET /instances/{id}/metrics ← adapter ← exporter(s)
└── 时序图表 ← GET /observability/query?query=<frozen PromQL>
```

### 操作可用性

| 操作 | stopped | running | deleted |
|------|---------|---------|---------|
| logs/events/metrics | ✅ | ✅ | ❌ |
| exec（容器类） | ❌ | ✅ | ❌ |
| console（vm） | ❌ | ✅ | ❌ |

## 7. Technical Considerations

- OpenAPI 已声明 ≠ handler 已实现。
- 时序图依赖 `/observability/query`；local profile 下可能无真实时序数据，UI 须正确处理 empty/error。
- 快照与 PromQL 时序可以短期不一致（不同数据源）；UI 应标注各自 `timestamp` / 查询时间。
- adapter 扩展：在 `WorkloadInstanceOps` / metrics adapter 内聚合 exporter，禁止 Gateway 直接依赖 Prometheus SDK。
- exec：PRD 验收标准为「POST exec → ws 可连接」；其余留 SPEC。
- 性能 `[Assumption]`：日志首屏 ≤ 2s（100 条）；快照刷新 ≤ 1s（local profile 基准）。

## 8. Success Metrics

- 任意 supported kind 可在详情内完成日志 → 事件 → 指标（快照+PromQL 图）闭环。
- container / vm / sandbox 代表 kind 的 Tab 差异通过 browser 验收。
- `batch_job`、`notebook` 详情页不出现 exec Tab。
- `k8s_cluster` 等 unsupported kind 不出现指标 Tab。

## 9. Product Decisions（原 Open Questions，已关闭）

| ID | 决策 | 影响 |
|----|------|------|
| OQ-1 | **时序图走 PromQL 代理**（`/observability/query` + 冻结模板） | US-011、FR-7、SPEC PromQL |
| OQ-2 | **exec 细节 SPEC 定**；PRD 只要求能连上 | US-012、Non-Goals |
| OQ-3 | **batch_job、notebook 不要 exec Tab** | FR-9、§6 矩阵 |
| OQ-4 | **metrics 不支持则隐藏指标 Tab** | FR-12、US-007、US-015 |
| OQ-5 | **扩展 adapter，从其它 exporter 填 InstanceMetrics** | US-003、FR-3、SPEC adapter |

**SPEC 待产出（由上述决策衍生，不再作为开放问题）：**

- PromQL 模板库与 `instance_id` label 映射表
- adapter 多 exporter 聚合方案（含 GPU / DCGM 路径）
- kind → metrics capability 映射
- exec WebSocket 客户端协议

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core + console |
| Code scope | Core：Gateway handler + `pkg/adapters/runtime/` ops/metrics adapter；Console：`repo/frontends/console/` 实例详情观测 |
| OpenAPI authority | consume only — `repo/api/openapi/v1.yaml` |
| Frozen exclusions | Services 后端、新增 OpenAPI 路径、Boss 大盘 |
| idempotency_key | required on: `POST /api/v1/instances/{instance_id}/exec` |
| Module main doc | `repo/services/docs/console-modules/compute/container-observability.md`（后续须同步全 kind 口径） |
| Instance kinds | 全部 9 种；metrics Tab 按 capability 隐藏 |
| Prometheus | 时序经 `/observability/query`；快照经 `InstanceMetrics` + adapter/exporter；不暴露 Prometheus 地址 |

## References

- `repo/api/openapi/v1.yaml`
- `repo/services/tasks/execution/CORE-HANDLER-IMPLEMENTATION-GUIDE.md` §TASK-CORE-005
- `repo/services/docs/console-modules/compute/container-observability.md`
- `repo/services/docs/console-modules/compute/vm-management.md`
- `repo/services/docs/console-modules/compute/gpu-container-instance-management.md`
- `repo/services/docs/console-modules/compute/sandbox-instance-management.md`
- `repo/services/docs/console-modules/inference/inference-observability.md`（PromQL 模式参考）
- `repo/development-records/m1-instance-r-kubernetes-ops-execution.md`
- `repo/development-records/m1-obs-a.md`
