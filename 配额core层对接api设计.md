# Core 配额 API 接口设计

> 状态: 接口定义
> 创建日期: 2026-07-31
> 适用范围: tenant-service 调用 Core 配额服务的接口契约

---

## 1. 新建配额

```
POST /api/v1/admin/tenants/{tenant_id}/quota
```

为指定租户批量初始化配额行。`used` 和 `reserved` 初始为 0。

**涉及表:** `resource_quota`（INSERT）、`resource_quota_meta`（SELECT 校验 resource_type 是否注册 + 读取 default_quota 兜底）

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
| resource_type | string | 是 | 配额维度标识（如 `gpu_count` / `cpu_core` / `memory_gb` / `storage_gb` / `token_count` / `kb_query_count` / `member_count` / `inference_service_count`） |
| total | integer | 否 | 配额上限值（>= 0）；未提供时取 `resource_quota_meta.default_quota` |

```json
{
  "items": [
    { "resource_type": "gpu_count", "total": 16 },
    { "resource_type": "cpu_core", "total": 128 },
    { "resource_type": "memory_gb" }
  ]
}
```

> 示例中 `memory_gb` 未提供 `total`，将取 `resource_quota_meta.default_quota` 作为上限。

**Response 200:**

```json
{
  "tenant_id": "uuid",
  "items": [
    { "resource_type": "gpu_count", "total": 16, "used": 0, "reserved": 0 },
    { "resource_type": "cpu_core", "total": 128, "used": 0, "reserved": 0 },
    { "resource_type": "memory_gb", "total": 512, "used": 0, "reserved": 0 }
  ]
}
```

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_NOT_FOUND | 租户不存在 |
| 409 | QUOTA_ALREADY_EXISTS | 某维度配额行已存在（已存在的跳过，其余正常创建） |
| 422 | QUOTA_RESOURCE_NOT_REGISTERED | resource_type 未在 resource_quota_meta 中注册或 enabled=false |
| 400 | VALIDATION_FAILED | total 为负数 / items 为空 / items 中 resource_type 重复 |

---

## 2. 修改配额上限

```
PUT /api/v1/admin/tenants/{tenant_id}/quota
```

批量修改指定租户多个维度的配额上限。不影响 `used` 和 `reserved`。若请求的 `total` 小于当前 `used + reserved`，则 `total` 自动收紧为 `used + reserved`，已有资源继续运行，不会强制停止或回收。

**自动收紧规则：** 若请求的 `total` 小于当前 `used + reserved`，则 `total` 自动调整为 `used + reserved` 的值（避免配额上限低于已占用值）。

**涉及表:** `resource_quota`（UPDATE total）

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
| total | integer | 是 | 新的配额上限值（>= 0） |

```json
{
  "items": [
    { "resource_type": "gpu_count", "total": 32 },
    { "resource_type": "storage_gb", "total": 4096 }
  ]
}
```

**Response 200:**

```json
{
  "tenant_id": "uuid",
  "items": [
    { "resource_type": "gpu_count", "total": 32, "used": 8, "reserved": 2, "tightened": false },
    { "resource_type": "storage_gb", "total": 4096, "used": 1024, "reserved": 0, "tightened": false }
  ]
}
```

> 示例中 `storage_gb` 请求 `total=4096`，但当前 `used+reserved=1024`，`total` 不少于 `used+reserved`，正常设为 4096，`tightened=false`。若请求 `total=512`（小于 `used+reserved=1024`），则自动收紧为 `1024`，`tightened=true`。

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_NOT_FOUND | 租户不存在 |
| 404 | QUOTA_NOT_FOUND | 某维度配额行不存在（需先调用新建） |
| 422 | QUOTA_RESOURCE_NOT_REGISTERED | resource_type 未注册或 enabled=false |
| 400 | VALIDATION_FAILED | total 为负数 / items 为空 / items 中 resource_type 重复 |

> 注：当请求的 `total` 小于 `used + reserved` 时，不报错，而是自动收紧为 `used + reserved`，并在响应中返回 `tightened: true`。前端根据 `tightened` 字段判断是否发生了自动收紧，无需处理错误。

---

## 3. 查询租户配额

```
GET /api/v1/admin/tenants/{tenant_id}/quota
```

查询指定租户所有维度的配额，包括用量、上限以及单位。

**涉及表:** `resource_quota`（SELECT 配额数据）、`resource_quota_meta`（JOIN 获取 unit / display_name）

**Path 参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| tenant_id | uuid | 租户 ID |

**Response 200:**

```json
{
  "tenant_id": "uuid",
  "items": [
    {
      "resource_type": "gpu_count",
      "total": 16,
      "used": 8,
      "reserved": 2,
      "unit": "card",
      "display_name": "GPU 卡数",
      "is_discrete": true
    },
    {
      "resource_type": "cpu_core",
      "total": 128,
      "used": 64,
      "reserved": 16,
      "unit": "core",
      "display_name": "vCPU 核数",
      "is_discrete": true
    },
    {
      "resource_type": "memory_gb",
      "total": 512,
      "used": 256,
      "reserved": 32,
      "unit": "gb",
      "display_name": "内存",
      "is_discrete": true
    },
    {
      "resource_type": "storage_gb",
      "total": 4096,
      "used": 1024,
      "reserved": 0,
      "unit": "gb",
      "display_name": "存储",
      "is_discrete": true
    },
    {
      "resource_type": "token_count",
      "total": 1000000,
      "used": 300000,
      "reserved": 0,
      "unit": "次",
      "display_name": "Token 额度",
      "is_discrete": true
    },
    {
      "resource_type": "kb_query_count",
      "total": 50000,
      "used": 12000,
      "reserved": 0,
      "unit": "次",
      "display_name": "KB 查询",
      "is_discrete": true
    },
    {
      "resource_type": "member_count",
      "total": 100,
      "used": 25,
      "reserved": 0,
      "unit": "人",
      "display_name": "成员数",
      "is_discrete": true
    },
    {
      "resource_type": "inference_service_count",
      "total": 10,
      "used": 3,
      "reserved": 0,
      "unit": "个",
      "display_name": "推理服务",
      "is_discrete": true
    }
  ]
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| tenant_id | uuid | 租户 ID |
| items | array | 各维度配额列表 |
| items[].resource_type | string | 配额维度标识 |
| items[].total | integer | 配额上限 |
| items[].used | integer | 运行时实扣（已 Confirm） |
| items[].reserved | integer | 运行时预占（已 Try 未 Confirm/Cancel） |
| items[].unit | string | 单位（来自 resource_quota_meta） |
| items[].display_name | string | 展示名称（来自 resource_quota_meta） |
| items[].is_discrete | boolean | 是否离散计数（true=整数，false=允许小数） |

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_NOT_FOUND | 租户不存在 |

---

## 4. 删除配额

```
DELETE /api/v1/admin/tenants/{tenant_id}/quota
```

直接删除该租户的所有配额行。用于租户禁用（资源删除）时清理配额。

**涉及表:** `resource_quota`（DELETE）、`resource_reservations`（DELETE 该租户的 TCC 预占流水）

**Path 参数:**

| 参数 | 类型 | 说明 |
|------|------|------|
| tenant_id | uuid | 租户 ID |

**Response 200:**

```json
{
  "tenant_id": "uuid",
  "message": "quota deleted"
}
```

**错误:**

| HTTP | code | 说明 |
|------|------|------|
| 404 | TENANT_NOT_FOUND | 租户不存在 |

---

## 5. 查询可用配额元数据

```
GET /api/v1/admin/quota-meta
```

查询 `resource_quota_meta` 表中所有 `enabled=true` 的配额维度，用于创建租户/套餐时展示可选项。

**涉及表:** `resource_quota_meta`（SELECT WHERE enabled=true）

**Response 200:**

```json
{
  "items": [
    {
      "resource_type": "gpu_count",
      "display_name": "GPU 卡数",
      "unit": "card",
      "default_quota": 4,
      "is_discrete": true
    },
    {
      "resource_type": "cpu_core",
      "display_name": "vCPU 核数",
      "unit": "core",
      "default_quota": 32,
      "is_discrete": true
    },
    {
      "resource_type": "memory_gb",
      "display_name": "内存",
      "unit": "gb",
      "default_quota": 128,
      "is_discrete": true
    },
    {
      "resource_type": "storage_gb",
      "display_name": "存储",
      "unit": "gb",
      "default_quota": 512,
      "is_discrete": true
    },
    {
      "resource_type": "token_count",
      "display_name": "Token 额度",
      "unit": "次",
      "default_quota": 1000000,
      "is_discrete": true
    },
    {
      "resource_type": "kb_query_count",
      "display_name": "KB 查询",
      "unit": "次",
      "default_quota": 50000,
      "is_discrete": true
    },
    {
      "resource_type": "member_count",
      "display_name": "成员数",
      "unit": "人",
      "default_quota": 50,
      "is_discrete": true
    },
    {
      "resource_type": "inference_service_count",
      "display_name": "推理服务",
      "unit": "个",
      "default_quota": 5,
      "is_discrete": true
    }
  ]
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| items | array | 可用配额维度列表 |
| items[].resource_type | string | 配额维度标识 |
| items[].display_name | string | 展示名称 |
| items[].unit | string | 单位 |
| items[].default_quota | integer | 默认上限（未提供 total 时兜底） |
| items[].is_discrete | boolean | 是否离散计数（true=整数，false=允许小数），用于校验新增/修改 total 的值类型 |

---

## 6. 错误码

| code | HTTP | 说明 |
|------|------|------|
| TENANT_NOT_FOUND | 404 | 租户不存在 |
| QUOTA_NOT_FOUND | 404 | 配额行不存在（修改时） |
| QUOTA_ALREADY_EXISTS | 409 | 配额行已存在（新建时） |
| QUOTA_RESOURCE_NOT_REGISTERED | 422 | resource_type 未注册或 enabled=false |
| VALIDATION_FAILED | 400 | 参数校验失败 |
