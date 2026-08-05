# Development Records — BOSS Tenant Quota Policy

> Batch: boss-tenant-quota-policy
> Date: 2026-08-04

---

## Issue #1: OpenAPI 契约 — services/v1.yaml 新增 tenant-plans 路径与 schema

### Design Decisions

1. **UnprocessableEntity 定义在 components/responses 而非 components/schemas**
   - Ambiguity: SPEC 未明确 422 错误响应应放在 schemas 还是 responses 下
   - Choice: 定义在 `components/responses/UnprocessableEntity`，引用现有 `ErrorResponse` schema
   - Rationale: OpenAPI 规范中 reusable response 应放在 `components/responses`，与现有 BadRequest/NotFound/Conflict 等保持一致

2. **Idempotency-Key 通过 header parameter 标注**
   - Ambiguity: 现有文件中 idempotency_key 有的放在 requestBody 字段（如 CreateModelRequest），有的放在 header
   - Choice: 使用 `Idempotency-Key` header parameter（required: true）
   - Rationale: SPEC §6 ANI Boundaries 明确标注 `idempotency_key required on: POST /tenant-plans, PATCH /tenant-plans/{id}/quota-limits, POST /tenants/{id}/plan`，使用 header 与 Gateway Redis 幂等中间件对接

3. **POST /tenant-plans 响应码用 200 而非 201**
   - Ambiguity: 现有文件中 POST /models 返回 201，POST /knowledge-bases 返回 201
   - Choice: 使用 200 + `IdempotentResult { id, message }` 响应
   - Rationale: SPEC §4.2 明确标注 `Response 200: { id, message: "tenant plan created" }`，与 SPEC 对齐

### Deviations

1. **search 参数从「模糊匹配 code 或 name」改为「模糊匹配 name」**
   - Spec said: SPEC §4.2 和 PRD US-002 均写 `search 关键字模糊查询（匹配 code 或 name，大小写不敏感）`
   - Implemented: `description: "模糊匹配 name"`（移除 code 匹配）
   - Why: 用户明确指示「套餐列表关键字只能是 name 不是 code」

2. **TenantPlanListItem 与 TenantPlan 字段完全相同但保持分离**
   - Spec said: SPEC §4.2 列表 items 和详情返回字段相同
   - Implemented: 定义两个独立 schema（TenantPlanListItem + TenantPlan）
   - Why: 语义不同，列表可能未来精简字段（如去掉 description），保持分离便于演进

### Tradeoffs

1. **PlanQuotaLimitInput.total 显式标注 format: int64**
   - Alternatives: (A) 省略 format（OpenAPI 默认 integer 为 int32），(B) 显式标注 int64
   - Choice: 显式标注 `format: int64`
   - Why: 与 DB `BIGINT` 类型对齐，避免前端类型生成时 int32 溢出风险

2. **PlanQuotaLimitInput 同时用于创建和修改限额**
   - Alternatives: (A) 定义 CreateQuotaLimitInput + UpdateQuotaLimitInput 两个 schema，(B) 复用一个
   - Choice: 复用 `PlanQuotaLimitInput`（resource_type + total）
   - Why: 两种场景字段完全相同，复用减少冗余。创建时在 CreateTenantPlanRequest.quota_limits 中引用，修改时在 UpdateQuotaLimitsRequest.items 中引用

### Open Questions

1. **TenantPlan 详情响应是否应包含 quota_limits**
   - SPEC §4.2 GET /tenant-plans/{planId} 详情响应不含 quota_limits（需单独调 GET /quota-limits）
   - 当前实现遵循 SPEC，但前端详情 Drawer 需并行调两个 API。如果后端愿意在详情响应中内联 quota_limits 可减少一次往返
   - Follow-up: 确认是否需要调整

### Verification Commands Run

- `npx yaml-lint repo/api/openapi/services/v1.yaml` — ✅ pass
- `git diff --stat` — 1 file changed, 352 insertions(+)
- AC checklist: 15/15 satisfied
