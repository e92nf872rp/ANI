# SPEC: Core Metering（OpenAPI 契约扩展 + Gateway + Ports/Adapters + metering-service）

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md` (v1.4)
> - UX: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
> Generated: 2026-07-09 | Target branch: main | Commit: —
>
> **Product line:** core
> **Code scope:** `repo/api/openapi/v1.yaml`、`repo/services/ani-gateway/internal/router/`、`repo/pkg/ports/`、`repo/pkg/adapters/runtime/`、`repo/services/metering-service/`（新建）
> **Source of truth:** OpenAPI `v1.yaml` 为唯一 API 权威源；FR-8 为待实施 Core 变更批次

---

## 1. Summary

### 1.1 What This SPEC Covers

本 SPEC 覆盖 metering 平台的 **Core 层** 全部技术变更：

1. **OpenAPI 契约扩展（FR-8）**：在 `v1.yaml` 新增 `GET /metering/usage/platform` 平台跨租户查询端点，补全 `GET /metering/usage` 的 `operationId` 与 `x-ani-rbac-scope`，定义平台 `group_by` 扩展枚举（含 `tenant_id`）。
2. **Gateway 处理器**：新增平台查询路由处理器，实现按 path 分轨鉴权（FR-15），从 JWT 提取租户上下文 vs 平台上下文。
3. **Ports/Adapters 扩展**：在 `MeteringService` port 新增平台查询方法，扩展 local adapter 支持平台视角。
4. **metering-service 微服务**：按 PRD FR-1～FR-3、FR-13 完整设计服务骨架——gRPC 接入、NATS 重试队列（独立 consumer group）、Core SDK 写入、可观测性（US-006）。

### 1.2 PRD Reference

- Source: `repo/services/tasks/modules/prd/services/metering/prd-metering-service.md` (v1.4)
- UX source: `repo/services/tasks/modules/ux/services/metering/ux-metering-service.md`
- User Stories covered: US-001（inference 上报）、US-002（算力可查询）、US-005（kb 上报 P1）、US-006（运维可观测）
- Functional Requirements covered: FR-1～FR-3、FR-6～FR-10、FR-13、FR-15

### 1.3 Design Decisions Summary

| Decision | Choice | Rationale |
|----------|--------|-----------|
| 平台查询 path | `GET /metering/usage/platform`（独立 path） | FR-8 定稿；与租户 path 分离，避免同 path 双语义（NG-8） |
| RBAC 三件套 | `scope:metering:read` / `scope:metering:platform:read` / `scope:metering:write` | Q1 定稿；path 与 scope 一一对应 |
| 平台 group_by 扩展 | 新增 `tenant_id` 枚举值 | 跨租户排行必需；租户 path 不扩展 |
| metering-service 通信 | gRPC 接入 + NATS 重试 | Services 层标准模式；独立 consumer group（FR-13） |
| metering-service 写入 Core | 通过 Core SDK / Core OpenAPI（`POST /metering/token-usage`） | FR-3；禁止 import Core 内部包 |
| 算力数据来源 | Core runtime/reconcile 聚合 | FR-9；Services 不重复采集 |
| Gateway 租户上下文 | 从 JWT 提取 `tenant_id`，忽略 query 中的 `tenant_id` | FR-15 安全要求 |

---

## 2. Architecture

### 2.1 System Context

```text
┌──────────────┐     gRPC      ┌─────────────────┐  Core SDK   ┌──────────────┐
│ inference-   │ ──────────►  │ metering-service │ ─────────► │ ani-gateway   │
│ service      │               │ (NATS 重试队列)   │             │ POST /metering│
└──────────────┘               └─────────────────┘             │ /token-usage  │
                                                               └──────┬───────┘
                                                                      │
               ┌──────────────┐  GET /metering/usage                │
               │ Console 前端  │ ◄──────────────────────────────────┤
               │ (租户 JWT)    │                                     │
               └──────────────┘                                     │
                                                                      │
               ┌──────────────┐  GET /metering/usage/platform        │
               │ BOSS 前端     │ ◄──────────────────────────────────┘
               │ (平台 JWT)    │
               └──────────────┘
```

### 2.2 Component Design

| 组件 | 职责 | 边界 |
|------|------|------|
| **OpenAPI v1.yaml** | 定义计量读写契约权威源 | 先改 YAML 再实现 |
| **Gateway metering handler** | HTTP 路由、参数校验、租户/平台上下文提取、调用 port | 不含业务逻辑 |
| **MeteringService port** | 计量能力抽象接口 | 租户查询 + 平台查询 + Token 写入 |
| **LocalMeteringService adapter** | local profile 实现（Token 聚合） | local ≠ production ready |
| **metering-service** | Services 层计量编排：gRPC 接入、NATS 重试、Core SDK 写入、可观测 | 不含查询逻辑；查询走 Core API 直连 |
| **Core SDK** | 从 v1.yaml 生成的 Go SDK | metering-service 唯一写入 Core 的通道 |

### 2.3 Module Interactions

**Token 上报流程（US-001）：**

```text
1. inference-service 完成推理
2. inference-service → metering-service (gRPC ReportTokenUsage)
3. metering-service 校验 + 生成 idempotency_key（若未提供）
4. metering-service → Core SDK POST /metering/token-usage (带 idempotency_key)
5. Core 返回 accepted / duplicate
6. 若失败 → metering-service 写入 NATS 重试队列（独立 consumer group）
7. metering-service 消费重试 → 复用同一 idempotency_key 重试
```

**平台查询流程（US-004）：**

```text
1. BOSS 前端 → GET /api/v1/metering/usage/platform (带 scope:metering:platform:read)
2. Gateway 校验平台 scope → 调用 MeteringService.QueryPlatformUsage
3. adapter 返回 items[]（含 tenant_id 必填）
4. 若带 tenant_id query → Gateway 二次 RBAC 校验 → 过滤单租户
```

### 2.4 File Structure

```
repo/
├── api/openapi/
│   └── v1.yaml                                    [MODIFY: FR-8 变更]
├── pkg/ports/
│   └── metering.go                                [MODIFY: 新增平台查询方法]
├── pkg/adapters/runtime/
│   └── local_metering_service.go                  [MODIFY: 新增平台查询实现]
├── services/ani-gateway/internal/router/
│   ├── metering_resources.go                      [MODIFY: 新增平台路由]
│   └── middleware/
│       └── rbac.go                                [REVIEW: 按新 scope 规则]
└── services/metering-service/                     [NEW: 完整新建]
    ├── cmd/
    │   └── main.go
    ├── internal/
    │   ├── server/
    │   │   └── grpc_server.go
    │   ├── pipeline/
    │   │   ├── token_reporter.go
    │   │   └── retry_consumer.go
    │   ├── nats/
    │   │   └── consumer.go
    │   └── obs/
    │       └── metrics.go
    ├── proto/
    │   └── metering.proto
    ├── go.mod
    └── Dockerfile
```

---

## 3. Data Model

### 3.1 Schema Changes（OpenAPI）

**无新增数据库表。** 本 SPEC 的数据模型变更全部在 OpenAPI schema 层面（见 §4 OpenAPI Change Plan）。

### 3.2 Entity Definitions（Ports 层扩展）

**现有 `MeteringUsageQueryRequest` 新增可选字段：**

```go
// pkg/ports/metering.go — 新增字段
type MeteringUsageQueryRequest struct {
    TenantID    string   // 租户查询时从 JWT 提取；平台查询时可为空（全平台）或指定单租户
    StartTime   string
    EndTime     string
    ResourceType string
    GroupBy     string
    IsPlatform  bool     // [NEW] true=平台查询，false=租户查询
}
```

**新增平台查询响应约束：** `items[].tenant_id` 在 `IsPlatform=true` 时**必填**。

### 3.3 Relationships

无新增实体关系。平台查询复用 `MeteringUsageRecord`，但 `tenant_id` 字段从 `nullable: true` 变为**条件必填**（平台视角下）。

### 3.4 Migration Plan

无数据库迁移。OpenAPI schema 变更为 **additive**（新增可选字段、新增端点、补全 operationId），不破坏现有消费者。

---

## 4. OpenAPI Change Plan (Core only)

### 4.1 Frozen Facts Table

| 项 | 状态 | 来源 |
|----|------|------|
| `GET /metering/usage` | **已冻结** path，**缺** operationId / scope | v1.yaml 行 3433 |
| `POST /metering/token-usage` | **已冻结**，operationId=`reportTokenUsage`, scope=`scope:metering:write` | v1.yaml 行 3465 |
| `GET /metering/usage/platform` | **未声明**（待补） | FR-8 待实施 |
| `MeteringUsageRecord.resource_type` enum | **已冻结** 6 值 | v1.yaml 行 705 |
| `MeteringUsageResponse` | **已冻结** | v1.yaml 行 715 |
| `storage_gb_days` / `kb_query_count` | **未在 enum** | P1 M-METERING-ENUM-A |

### 4.2 OpenAPI Change Plan

| Change | operationId | Compatibility | idempotency_key |
|--------|-------------|---------------|-----------------|
| 补全 `GET /metering/usage` operationId + scope | `getMeteringUsage` | 向后兼容（仅补全 metadata） | N/A（GET） |
| 新增 `GET /metering/usage/platform` | `getPlatformMeteringUsage` | 新增端点，向后兼容 | N/A（GET） |
| 平台 `group_by` 新增 `tenant_id` 枚举值 | `getPlatformMeteringUsage` | 新端点的参数，不影响现有 | N/A |
| `MeteringUsageRecord.tenant_id` 平台视角下必填 | — | 通过端点级 response 约束，不改 schema 的 nullable | N/A |

### 4.3 完整 YAML 变更片段

#### 4.3.1 补全 `GET /metering/usage`

```yaml
# 在 /metering/usage GET 操作中补全：
paths:
  /metering/usage:
    get:
      operationId: getMeteringUsage          # [NEW] 补全
      x-ani-rbac-scope: scope:metering:read  # [NEW] 补全
      tags: [Metering]
      summary: 查询租户用量
      description: |
        在租户 JWT 上下文中查询本租户的用量数据。
        tenant_id 从 JWT 提取，忽略 query 中的 tenant_id 参数。
      parameters:
        - { name: start_time, in: query, required: true, schema: { type: string, format: date-time } }
        - { name: end_time,   in: query, required: true, schema: { type: string, format: date-time } }
        - { name: resource_type, in: query, schema: { type: string } }
        - { name: group_by, in: query, schema: { type: string, enum: [resource_type, az, day, hour] } }
      # ... response 不变
```

#### 4.3.2 新增 `GET /metering/usage/platform`

```yaml
  /metering/usage/platform:
    get:
      operationId: getPlatformMeteringUsage
      x-ani-rbac-scope: scope:metering:platform:read
      tags: [Metering]
      summary: 查询平台跨租户用量
      description: |
        在平台 RBAC 上下文中查询全平台或指定租户的用量数据。
        需 scope:metering:platform:read 权限。
        items[].tenant_id 在此端点下必填。
        若带 tenant_id query 须二次 RBAC 校验。
      parameters:
        - { name: start_time, in: query, required: true, schema: { type: string, format: date-time } }
        - { name: end_time,   in: query, required: true, schema: { type: string, format: date-time } }
        - { name: resource_type, in: query, schema: { type: string } }
        - { name: group_by, in: query, schema: { type: string, enum: [tenant_id, day, hour] } }
        - { name: tenant_id, in: query, required: false, schema: { type: string }, description: "可选筛选单租户，须平台 RBAC 校验" }
      responses:
        '200':
          description: 平台用量查询成功
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/MeteringUsageResponse'
        '403':
          description: 无平台计量查看权限
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ErrorResponse'
```

> **注意：** `group_by` 在平台端点使用**独立 enum** `[tenant_id, day, hour]`，与租户端点的 `[resource_type, az, day, hour]` 分离。租户端点 `group_by` **不** 扩展 `tenant_id`（FR-8 明确要求）。

---

## 5. API Design

### 5.1 Endpoints

| Method | Path | Description | Auth (scope) | Request | Response |
|--------|------|-------------|--------------|---------|----------|
| GET | `/metering/usage` | 租户用量查询 | `scope:metering:read` | `start_time*`, `end_time*`, `resource_type?`, `group_by?` | `MeteringUsageResponse` |
| GET | `/metering/usage/platform` | 平台跨租户用量查询 | `scope:metering:platform:read` | `start_time*`, `end_time*`, `resource_type?`, `group_by?`, `tenant_id?` | `MeteringUsageResponse`（`tenant_id` 必填） |
| POST | `/metering/token-usage` | Token 用量写入 | `scope:metering:write` | `ReportTokenUsageRequest`（含 `idempotency_key`） | `202 Accepted` + `TokenUsageReport` |

### 5.2 Request/Response Schemas

**`GET /metering/usage/platform` 请求示例：**

```json
GET /api/v1/metering/usage/platform?start_time=2026-07-01T00:00:00Z&end_time=2026-07-09T00:00:00Z&resource_type=token_input&group_by=tenant_id
```

**响应示例（平台视角，`tenant_id` 必填）：**

```json
{
  "items": [
    { "tenant_id": "tenant-001", "resource_type": "token_input", "total_quantity": 125000.0, "unit": "token", "period": null },
    { "tenant_id": "tenant-002", "resource_type": "token_input", "total_quantity": 83000.0, "unit": "token", "period": null }
  ],
  "total": 2,
  "dev_profile": {
    "mode": "local",
    "provider": "local-metering-service",
    "real_provider": false,
    "reason": "local profile records metering events; it is not a real metering backend execution"
  }
}
```

### 5.3 Error Responses

| HTTP Status | Error Code | Condition | User Message |
|------------|------------|-----------|--------------|
| 400 | `INVALID_TIME_RANGE` | start_time ≥ end_time 或格式非法 | 时间范围无效 |
| 403 | `FORBIDDEN` | 缺少对应 scope | 无权限查看用量数据 / 无权限查看平台计量数据 |
| 501 | `NOT_IMPLEMENTED` | 平台 API 尚未实现（local profile 无平台查询） | 平台计量接口尚未上线 |

### 5.4 Breaking Changes

**无破坏性变更。** 所有变更均为 additive：
- 补全 `operationId` 和 `x-ani-rbac-scope` 不影响现有客户端行为
- 新增 `/metering/usage/platform` 是全新端点
- 平台 `group_by` enum 是新端点的参数，不影响租户端点

---

## 6. Business Logic

### 6.1 Core Algorithms

**Gateway 租户查询处理（`getMeteringUsage`）：**

```text
1. 解析 query: start_time, end_time, resource_type?, group_by?
2. 校验 start_time < end_time
3. 从 JWT 提取 tenant_id（忽略 query 中的 tenant_id）
4. 调用 MeteringService.QueryUsage(req{TenantID, IsPlatform: false})
5. 返回 items[]（租户视角下 tenant_id 可空）
```

**Gateway 平台查询处理（`getPlatformMeteringUsage`）：**

```text
1. 解析 query: start_time, end_time, resource_type?, group_by?, tenant_id?
2. 校验 start_time < end_time
3. 若 group_by=tenant_id → 结果按租户聚合排行
4. 若 query 带 tenant_id → 二次 RBAC 校验（确认该平台用户有权查看此租户）
5. 调用 MeteringService.QueryUsage(req{TenantID: query.tenant_id?, IsPlatform: true})
6. 返回 items[]（平台视角下 items[].tenant_id 必填）
```

**LocalMeteringService 平台查询实现（local profile）：**

```text
1. 遍历内存中所有租户的 reports
2. 按 resource_type 过滤（若指定）
3. 按 tenant_id 聚合（group_by=tenant_id）或按时间桶聚合
4. 返回 items[]，每条含 tenant_id
5. dev_profile.real_provider = false
```

### 6.2 Validation Rules

| 规则 | 条件 |
|------|------|
| start_time 必填 | query 参数缺失 → 400 |
| end_time 必填 | query 参数缺失 → 400 |
| start_time < end_time | 否则 → 400 INVALID_TIME_RANGE |
| resource_type 枚举 | 值不在 `MeteringUsageRecord.resource_type` enum 中 → 400 |
| group_by 枚举 | 租户: `[resource_type, az, day, hour]`；平台: `[tenant_id, day, hour]` |
| 平台 tenant_id query | 须通过二次 RBAC 校验 |

### 6.3 State Machine

无状态机。计量查询为无状态读操作。

### 6.4 Edge Cases

| 场景 | 处理 |
|------|------|
| local profile 平台查询 | 返回内存中所有租户聚合数据 + `dev_profile.real_provider=false` |
| 无数据 | `items=[]`, `total=0`，不伪造 0 值 |
| 算力 `instance_*` 在 local 下 | LocalMeteringService 不产出 → items 为空 → 前端空态（PRD §7.3） |
| 平台查询但 group_by=resource_type | 拒绝：平台 group_by 不含 resource_type → 400 |

---

## 7. Error Handling

### 7.1 Error Taxonomy

| Error Code | HTTP Status | Condition | User Message |
|------------|-------------|-----------|--------------|
| `INVALID_TIME_RANGE` | 400 | start ≥ end | 时间范围无效 |
| `INVALID_RESOURCE_TYPE` | 400 | resource_type 不在枚举 | 资源类型无效 |
| `INVALID_GROUP_BY` | 400 | group_by 不在枚举 | 分组维度无效 |
| `FORBIDDEN` | 403 | 缺少 scope | 无权限 |
| `NOT_IMPLEMENTED` | 501 | 平台 API 未实现 | 平台计量接口尚未上线 |

### 7.2 Retry Strategy

**查询操作不重试。** 前端 React Query 自带重试策略。

**Token 写入重试（metering-service 内部）：**
- 写入 Core 失败 → 写入 NATS 重试队列
- 消费重试时**复用同一 `idempotency_key`**（FR-6）
- 退避策略：指数退避，最大 5 次
- 超过最大次数 → 记录 error 日志 + metrics 计数

### 7.3 Failure Modes

| 依赖失败 | 行为 |
|---------|------|
| Core API 不可用 | metering-service 写入 NATS 重试队列；查询返回 502/503 |
| NATS 不可用 | metering-service health 降级；上报返回 503 |
| local profile 无算力数据 | 返回 `items=[]`，不伪造（FR-12） |

---

## 8. Security

### 8.1 Authentication & Authorization

| 接口 | scope | 上下文来源 | 谁可调用 |
|------|-------|-----------|---------|
| `GET /metering/usage` | `scope:metering:read` | JWT tenant_id | 租户用户 / 租户管理员 |
| `GET /metering/usage/platform` | `scope:metering:platform:read` | 平台 RBAC | BOSS 平台运营 |
| `POST /metering/token-usage` | `scope:metering:write` | 服务账号 | inference / metering-service |

**Gateway 鉴权规则（FR-15）：**

- **租户 path**：校验 `scope:metering:read` → 从 JWT 取 `tenant_id` → **忽略或拒绝**未授权 `tenant_id` query
- **平台 path**：校验 `scope:metering:platform:read` → 默认全平台 → 可选 `tenant_id` query 须二次 RBAC 校验
- **平台 scope 不隐含租户 scope**：需同时访问两者时分别授予

### 8.2 Input Validation

- `start_time` / `end_time`：RFC3339 date-time 格式校验
- `resource_type`：枚举校验
- `group_by`：枚举校验（租户/平台分离）
- `tenant_id`（平台 query）：字符串非空校验 + RBAC 二次校验

### 8.3 Data Protection

- Token 写入日志不含 Token 明文内容（US-006）
- 日志含 `tenant_id`、`request_id`、`idempotency_key`（US-006）
- 无敏感字段加密需求（计量数据非敏感）

---

## 9. Performance

### 9.1 Expected Load

| 指标 | 估计 |
|------|------|
| Token 上报 QPS | 低（inference 完成后单条上报） |
| 租户查询 QPS | 中（Console 用户查看报表） |
| 平台查询 QPS | 低（BOSS 运营查看） |
| Console P95 查询 | < 3s（PRD §8） |
| BOSS Top N 排行 | N ≥ 20 租户（PRD §8） |

### 9.2 Optimization Strategy

- local profile：内存聚合，无需优化
- real provider：依赖 Core reconcile 聚合，查询走索引
- 平台排行：`group_by=tenant_id` 聚合在后端完成，避免前端逐租户轮询

### 9.3 Database Considerations

本 SPEC 不涉及数据库变更。real provider 的存储策略由 Core runtime 层负责。

---

## 10. Testing Strategy

### 10.1 Unit Tests

| 测试目标 | 范围 |
|---------|------|
| LocalMeteringService.QueryUsage | Token 聚合、resource_type 过滤、空数据 |
| LocalMeteringService.QueryPlatformUsage | [NEW] 全租户聚合、tenant_id 筛选、group_by=tenant_id |
| Gateway 参数校验 | start_time/end_time 必填、格式、范围 |
| Gateway 租户上下文提取 | 从 JWT 提取、忽略 query tenant_id |
| idempotency 去重 | 重复 key 返回 duplicate |

### 10.2 Integration Tests

| 测试 | 描述 |
|------|------|
| Token 上报 → 租户查询 E2E | POST token-usage → GET /metering/usage 可查 |
| 平台查询返回 tenant_id 必填 | GET /metering/usage/platform items[].tenant_id 非空 |
| 平台 tenant_id query 二次鉴权 | 无权限的 tenant_id → 403 |
| OpenAPI 契约一致性 | v1.yaml 变更后 SDK 生成成功 |

### 10.3 Edge Case Tests

| 场景 | 期望 |
|------|------|
| local profile 算力查询 | items=[] (LocalMeteringService 不产出 instance_*) |
| 平台查询 group_by=resource_type | 400 (平台 group_by 不含此值) |
| 租户查询 group_by=tenant_id | 400 (租户 group_by 不含此值) |
| 重复 idempotency_key 上报 | state=duplicate, 0 double-count |

### 10.4 Acceptance Criteria Mapping

| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-001 | Token 上报 E2E | integration | POST → GET 可查 |
| US-001 | idempotency 去重 | unit | 重复 key 返回 duplicate |
| US-002 | 算力 resource_type 查询 | integration | GET /metering/usage?resource_type=instance_gpu_seconds |
| US-006 | metering-service 指标 | unit | 上报成功/失败/duplicate 计数 |
| FR-8 | OpenAPI 平台端点 | contract | v1.yaml 新增 path + operationId |
| FR-15 | Gateway 分轨鉴权 | integration | 租户/平台 scope 分离校验 |
| FR-6 | idempotency_key 复用 | unit | 重试复用同一 key |

---

## 11. metering-service 微服务设计

> **注意：** CLAUDE.md 声明 Services 层已冻结。但用户确认按 PRD FR-1～FR-3 完整设计。本节为设计规范，实际实现由外部团队或后续解冻批次落地。

### 11.1 服务定位

metering-service 是 Services 层的**计量编排微服务**，职责：

1. 接收 inference-service 的 gRPC Token 上报
2. 通过 Core SDK 转发至 `POST /api/v1/metering/token-usage`
3. 失败时写入 NATS 重试队列（独立 consumer group）
4. 暴露 health/readiness 和 metrics（US-006）

**不做：** 查询逻辑（Console/BOSS 直连 Core API）、算力采集（Core runtime 负责）、推理/RAG 业务。

### 11.2 gRPC 接口（proto）

```protobuf
syntax = "proto3";
package metering.v1;
option go_package = "metering-service/internal/proto;meteringpb";

service MeteringIngest {
  // inference-service 调用，上报 Token 用量
  rpc ReportTokenUsage(TokenUsageReport) returns (TokenUsageAck);
}

message TokenUsageReport {
  string idempotency_key = 1;   // 必填，重试复用
  string tenant_id = 2;         // 上报方传入
  string source = 3;            // "inference-service"
  string model = 4;
  int64  input_tokens = 5;
  int64  output_tokens = 6;
  string request_id = 7;
  string instance_id = 8;
  string occurred_at = 9;       // RFC3339
  map<string, string> labels = 10; // P0 不强制
}

message TokenUsageAck {
  string state = 1;              // "accepted" | "duplicate"
  string report_id = 2;
  int64  total_tokens = 3;
}
```

### 11.3 NATS 重试队列

| 项 | 值 |
|----|-----|
| 集群 | 共用现有 NATS 集群（FR-13） |
| Stream | `METERING_TOKEN_USAGE` |
| Consumer group | `metering-service-retry`（**独立**，不与 task-service 混用） |
| 消息体 | `TokenUsageReport` protobuf |
| 重试策略 | 指数退避，最大 5 次 |
| 幂等 | 复用同一 `idempotency_key`（FR-6） |

### 11.4 可观测性（US-006）

**指标（Prometheus）：**

| 指标 | 类型 | 标签 |
|------|------|------|
| `metering_token_reports_total` | counter | `state=accepted\|duplicate\|failed` |
| `metering_core_api_latency_seconds` | histogram | `endpoint=token-usage` |
| `metering_nats_queue_depth` | gauge | `stream=METERING_TOKEN_USAGE` |

**Health/Readiness：**

- `/healthz`：进程存活
- `/readyz`：NATS 连接 + Core API 可达

**日志字段：** `tenant_id`、`request_id`、`idempotency_key`、`state`（不含 Token 明文）

### 11.5 部署

| 项 | 值 |
|----|-----|
| 语言 | Go |
| 框架 | grpc-go |
| 配置 | 环境变量（NATS_URL、CORE_API_BASE、GRPC_PORT） |
| 部署 | K8s Deployment（独立于 task-service） |

---

## 12. Implementation Plan

### 12.1 Phases

| Phase | 范围 | 依赖 |
|-------|------|------|
| P0-A-1 | OpenAPI v1.yaml 变更（FR-8） | — |
| P0-A-2 | Gateway 平台路由 + 分轨鉴权 | P0-A-1 |
| P0-A-3 | Ports/Adapters 平台查询方法 | P0-A-1 |
| P0-A-4 | metering-service 骨架 + Token 上报 E2E | P0-A-1（Core SDK 生成） |
| P1 | resource_type enum 扩展 + KB 写入 API | P0 全部完成 |

### 12.2 Issue Mapping

| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| #1 OpenAPI v1.yaml FR-8 变更 | 4.2, 4.3 | high | — |
| #2 Gateway 平台路由 + 鉴权 | 5.1, 6.1, 7.1, 8.1 | high | #1 |
| #3 Ports/Adapters 平台查询 | 3.2, 6.1, 6.4 | high | #1 |
| #4 metering-service 骨架 | 11.1～11.5 | high | #1（SDK） |
| #5 metering-service NATS 重试 | 11.3, 7.2 | high | #4 |
| #6 metering-service 可观测 | 11.4 | medium | #4 |

### 12.3 Incremental Delivery

- **Feature flag：** Gateway 平台路由可通过 `ANI_METERING_PLATFORM_ENABLED` 环境变量控制启用
- **local 优先：** P0-A-2/A-3 可在 local profile 验证，不需要 real provider
- **live gate：** metering-service E2E 需在真实环境验证 Token 上报闭环

---

## 13. Open Questions & Risks

### 13.1 Unresolved Questions

- metering-service 实现属于 Services 层冻结范围，实际开发需等待解冻或由外部团队交付
- Gateway RBAC 中间件当前从路径推断 resource:action，是否需要改为读取 `x-ani-rbac-scope` 声明？
- 平台 `tenant_id` query 的二次 RBAC 校验规则细节（哪些平台角色可查看哪些租户）

### 13.2 Technical Risks

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Services 层冻结导致 metering-service 无法落地 | P0-A Token 上报 E2E 阻塞 | Core 可暂以 Gateway 直连 `POST /token-usage` 承载；metering-service 延后 |
| 平台 RBAC 二次校验规则未定义 | 钻取功能越权风险 | P0-A 先 fail-closed，无明确授权拒绝 tenant_id query |
| local profile 无平台查询能力 | BOSS 前端无法联调 | LocalMeteringService 新增平台查询实现（内存全租户聚合） |

### 13.3 Assumptions

- Core SDK 从 v1.yaml 变更后自动生成，metering-service 依赖 SDK 调用 Core
- NATS 集群已部署且可创建新 stream
- Gateway RBAC 中间件可通过配置支持新的 scope 声明
- local profile 可模拟全租户数据用于 BOSS 联调
