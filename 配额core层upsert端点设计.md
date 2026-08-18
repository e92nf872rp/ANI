# Core 配额 Upsert 端点设计

> 状态: 接口定义
> 创建日期: 2026-08-18
> 适用范围: Core 配额服务新增 upsert 端点

---

## 1. 问题背景

### 现有端点语义

Core 配额服务当前提供两个写入端点：

| 端点 | 语义 | 已存在维度 | 不存在维度 |
|------|------|-----------|-----------|
| `POST /admin/tenants/{tenant_id}/quota` | 新建 | `ON CONFLICT DO NOTHING` 跳过 | INSERT |
| `PUT /admin/tenants/{tenant_id}/quota` | 修改 | UPDATE total | 返回 `QUOTA_NOT_FOUND` |

两者均不支持"存在则更新、不存在则新建"的原子 upsert 语义。

### Services 层现有调用方式

tenant-service 的 `applyTenantQuotaItems`（`repo/services/tenant-service/internal/service/tenant_plan_service.go`）在绑定套餐或同步限额时，需要为租户写入多个维度的配额。由于不知道哪些维度已存在，必须：

1. **GetQuota** — 查询租户已有配额维度
2. **分流** — 已有维度放入 `putItems`，缺失维度放入 `createItems`
3. **PutQuota** — 更新已有维度
4. **CreateQuota** — 新建缺失维度

### 问题：跨调用部分失败

步骤3和步骤4是两次独立 HTTP 调用，对应两个独立 DB 事务。如果 PutQuota 成功但 CreateQuota 失败：

- 已 Put 的维度 total 已被修改，**无法自动回滚**（Core 端无事务引用可传递）
- Services 层只能 best-effort 补偿回滚：用 GetQuota 拿到的旧 total 再调 PutQuota 恢复
- 补偿本身也可能失败，导致数据不一致
- 额外增加了 1 次 GetQuota + 1 次补偿 PutQuota 的网络开销

### 补偿方案的局限

当前代码已实现补偿回滚（`tenant_plan_service.go` 步骤4 CreateQuota 失败时回滚 putItems 到旧值），但存在固有缺陷：

- 补偿回滚本身是第三次 HTTP 调用，也可能失败
- 补偿期间租户配额处于中间状态，并发请求可能读到不一致值
- 每次正常流程都需要额外一次 GetQuota 查询，即使绝大多数维度最终是 upsert

---

## 2. 解决方案：新增 Upsert 端点

### 端点定义

```
PUT /api/v1/admin/tenants/{tenant_id}/quota/upsert
```

批量 Upsert 指定租户多个维度的配额上限：**已存在的维度更新 total，不存在的维度新建行**。单次请求内所有维度在同一 DB 事务中原子完成。

### 与现有端点的区别

| 对比项 | `POST /quota`（新建） | `PUT /quota`（修改） | `PUT /quota/upsert`（新增） |
|--------|---------------------|---------------------|---------------------------|
| 已存在维度 | 跳过（DO NOTHING） | UPDATE total | UPDATE total |
| 不存在维度 | INSERT | **QUOTA_NOT_FOUND 错误** | INSERT |
| 事务范围 | 单事务 | 单事务 | 单事务 |
| 跨调用原子性 | N/A | N/A | **单次调用全部维度原子** |

### 端点规格

**涉及表:** `resource_quota`（INSERT ... ON CONFLICT DO UPDATE）、`resource_quota_meta`（SELECT 校验 resource_type 是否注册 + 读取 default_quota 兜底）

**Path 参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| tenant_id | uuid | 租户 ID |

**Request Body:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| items | array | 是 | 配额维度列表，至少 1 项 |

**items 每项结构:**

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| resource_type | string | 是 | 配额维度标识 |
| total | integer | 否 | 配额上限值（>= 0）；未提供时取 `resource_quota_meta.default_quota` |

```json
{
  "items": [
    { "resource_type": "gpu_count", "total": 32 },
    { "resource_type": "storage_gb", "total": 4096 },
    { "resource_type": "token_count" }
  ]
}
```

> 示例中 `token_count` 未提供 `total`，将取 `resource_quota_meta.default_quota` 作为上限。

**Response 200:**

```json
{
  "tenant_id": "uuid",
  "items": [
    { "resource_type": "gpu_count", "total": 32, "used": 8, "reserved": 2, "tightened": false },
    { "resource_type": "storage_gb", "total": 4096, "used": 1024, "reserved": 0, "tightened": false },
    { "resource_type": "token_count", "total": 1000000, "used": 0, "reserved": 0, "tightened": false }
  ]
}
```

> `tightened` 为 true 表示该维度因请求 total < used+reserved 而自动收紧为当前已占用值。新建维度（used=0, reserved=0）不会触发收紧。

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_NOT_FOUND | 租户不存在 |
| 422 | QUOTA_RESOURCE_NOT_REGISTERED | resource_type 未在 resource_quota_meta 中注册或 enabled=false |
| 400 | VALIDATION_FAILED | total 为负数 / items 为空 / items 中 resource_type 重复 |

---

### 改动文件

- [ ] `repo/api/openapi/v1.yaml` — 新增 `PUT /admin/tenants/{tenant_id}/quota/upsert` 路径定义
- [ ] `repo/pkg/ports/quota_admin.go` — `QuotaAdminService` 接口新增 `UpsertTenantQuota` 方法
- [ ] `repo/pkg/adapters/runtime/postgres_quota.go` — 实现 `UpsertTenantQuota`（`ON CONFLICT DO UPDATE` + GREATEST clamp + 回读 tightened）
- [ ] `repo/services/ani-gateway/internal/router/quota_resources.go` — 新增路由 handler
