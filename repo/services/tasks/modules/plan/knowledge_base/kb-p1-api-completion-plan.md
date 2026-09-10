# 知识库 P1 接口补全方案（#19–#25：权限 / 配置 / 重建 / 模型 / 审计）

> 版本：v1.0.0-draft ｜ 日期：2026-09-08
> 上游计划：[kb-api-completion-plan.md](./kb-api-completion-plan.md)（本方案是其 §1.2「config/rebuild/models 三组路径另立批次」+ §1.3/§2.6/§2.7 所指的落地文档）
> 前置事实：KB-API-B1/B2/B3（issue-040~048，SPEC [spec-services-kb-api-completion.md](../../spec/core/knowledge/spec-services-kb-api-completion.md)）已全部合入——#1~#18 共 18 个端点三层（OpenAPI / proto / 实现）全通
> 目标：完成 #19~#25 共 7 个端点，KB 接口面 25/25 闭环，并清空 `services-route-baseline.yaml` 中 KB 相关的 4 条 `spec_not_in_code`

---

## 0. 范围与批次总览

### 0.1 接口 → 批次映射

| # | 方法 | 路径 | 批次 | 现状 | 交付物 |
|---|---|---|---|---|---|
| 20 | PUT | `.../permissions` | **B4** | 契约三层就绪，kb-service UNIMPLEMENTED stub | kb_permissions 表 + 实现 + 读接口入口 |
| 19 | GET | `.../permissions` | **B4** | OpenAPI/proto 均未声明 | 新增契约（v1.yaml GET + proto GetKBPermissions）+ 实现 |
| 22 | GET | `.../config` | **B5** | v1.yaml 已声明，proto 未声明，Gateway 未注册 | proto GetKBConfig + ocr_enabled 迁移 + Gateway 路由 + retrieval_mode 契约修正 |
| 25 | GET | `.../models` | **B5** | v1.yaml 已声明，proto 未声明，Gateway 未注册 | proto ListKBModels + Core 模型聚合 + Gateway 路由 |
| 24 | POST | `.../rebuild` | **B6** | v1.yaml 已声明，proto 未声明，Gateway 未注册 | proto RebuildKB + 重建编排器（async_tasks + outbox + consumer） |
| 23 | PUT | `.../config` | **B7** | v1.yaml 已声明，proto 未声明，Gateway 未注册 | proto UpdateKBConfig + 触发重建联动（依赖 B6 的 rebuild 编排） |
| 21 | GET | `.../audit-logs` | **B8** | OpenAPI/proto 均未声明 | kb_audit_log 表 + 写侧埋点（全链路）+ 读接口 |

### 0.2 批次依赖关系

```
B4（权限）──────────┐
                     ├──→ 两者独立可并行
B5（config 读 + models）┘
        │
        └──→ B6（rebuild，依赖 B5 的 retrieval_mode 契约修正与 ocr_enabled 迁移）
                  │
                  └──→ B7（PUT config，依赖 B6 的 rebuild 编排做「改嵌入模型/分块 → 自动重建」联动）

B8（audit-logs）──── 独立批次，但埋点覆盖 B4~B7 的写操作，建议最后合入以一次性埋全
```

- **B4 与 B5 可并行**：无共享表/契约交集（B4 改 permissions 域，B5 改 config/models 域 + `knowledge_bases` 列）。
- **B6 必须晚于 B5**：rebuild 编排按新契约读取 `retrieval_mode`/`ocr_enabled` 作为重建参数，避免按漂移契约实现后返工。
- **B7 必须晚于 B6**：PUT config 的核心联动「修改 embedding_model / chunk_size → 自动触发 rebuild」直接复用 B6 的重建编排函数。
- **B8 建议压轴**：audit 埋点横切 B4~B7 全部写操作，最后合入可一次埋全，避免中间批次反复改写侧代码。

### 0.3 不在本方案范围

- 查询侧权限执行闭环（list/get/query 按 `public_read` / `allowed_user_ids` 过滤可见性）——见 §10.2 Q2，B4 只交付管理面读写，执行闭环留待后续批次（涉及 proto 请求补 user_id 链路，影响面大）。
- 会话重命名 PATCH、引用详情 GET 等上游 plan 标注的「同步待补（低优先级）」项。
- `chunk_size` 默认值漂移（proto 注释 1024 vs DB 默认 512）——与 #19~#25 无关，登记为 §10.2 Q5。

---

## 1. 现状核验（Frozen Facts，2026-09-08）

以下均为 2026-09-08 对代码库的直接核验结果，实施批次以此为基准。

### 1.1 契约层

**proto（`repo/api/proto/kb/v1/kb_service.proto`，现 19 个 RPC）：**

- 已声明且已实现 18 个（含 B3 的 `ReparseDocument`，L64-68）。
- `UpdateKBPermissions`（L55）：`rpc UpdateKBPermissions(UpdateKBPermissionsRequest) returns (KnowledgeBase);`——**返回 KnowledgeBase 而非权限对象**（PUT 后前端需再 GET 回显白名单，此为既有契约设计，照契约实现）。
- 请求消息（L313-319 附近）：`tenant_id / kb_id / idempotency_key / public_read / allowed_user_ids`。
- **不存在的 RPC**：`GetKBPermissions` / `ListKBAuditLogs` / `GetKBConfig` / `UpdateKBConfig` / `RebuildKB` / `ListKBModels`——B4/B5/B6/B7/B8 各自新增。
- `CreateKBRequest`（L73-82）：`retrieval_mode = 8  // vector | hybrid(默认) | keyword`——proto 侧字段名、枚举、默认值与 DB 一致。

**OpenAPI（`repo/api/openapi/services/v1.yaml`）：**

- `/knowledge-bases/{kb_id}/permissions`（L2747-2768）：**仅 PUT**，返回 KnowledgeBase；B4 需补 GET。
- `/knowledge-bases/{kb_id}/config`（L2800-2843）：GET + PUT 均已声明；B5 补 proto + Gateway，B7 补 PUT 实现。
- `/knowledge-bases/{kb_id}/rebuild`（L2845-2870）：POST 已声明（202 + AsyncTask）；B6 补 proto + Gateway。
- `/knowledge-bases/{kb_id}/models`（L2872-2890）：GET 已声明；B5 补 proto + Gateway。
- KB 子路径**无 audit-logs**（审计是独立域：L3757/4288/4580/4826 为租户/计费等其他域的 audit-logs）；B8 需新增路径声明。
- Schema 就绪情况：`UpdateKBPermissionsRequest`（L948-954）、`KBConfig`（L956-968，**含漂移**）、`UpdateKBConfigRequest`（L970-983）、`ModelList`+`Model`（L985-999）、`RebuildKnowledgeBaseRequest`（L1008-1013）；B4 需新增 `KBPermissions`，B8 需新增 `KBAuditLog` / `KBAuditLogListResponse`。

### 1.2 数据层

**`knowledge_bases` 实际列**（`repo/deploy/migrations/20260501000100_init_schema.sql` L308-323 + kb-service 迁移 003/004）：

```
id, tenant_id, name, description, embedding_model(D:'bge-m3'),
chunk_size(D:512), top_k(D:5), score_threshold(D:0.3),
status CHECK(active|rebuilding|deleted, D:'active'), doc_count(D:0),
created_at, updated_at,
retrieval_mode TEXT NOT NULL D:'hybrid' CHECK(vector|hybrid|keyword),   -- 迁移 003
vector_store_id TEXT                                                     -- 迁移 004
```

- **无 `ocr_enabled` 列**——B5 迁移新增。
- 唯一性：partial unique index `idx_knowledge_bases_tenant_name_active ON (tenant_id, name) WHERE status <> 'deleted'`（软删不占名，迁移 20260903000100）。
- `kb_chunks` / `kb_messages`（含 source_chunks JSONB）/ 会话缓存（Redis `ani:prod:session:kb:{session_id}`，TTL 24h，LTRIM 20）就绪。
- **`kb_permissions`、`kb_audit_log` 两表均不存在**——B4/B8 新建。
- kb-service 自有迁移目录：`repo/services/kb-service/migrations/`，现有 001（pg_trgm）/ 002（kb_chunks，RESTRICTIVE RLS）/ 003（retrieval_mode）/ 004（vector_store_id）——**本方案新迁移从 005 起编号**。
- 全库 RLS 约定：`ENABLE/FORCE ROW LEVEL SECURITY` + RESTRICTIVE `tenant_isolation` policy + 查询前 `set_tenant_context(conn, tenant_id)`（`app/repositories/rls.py`）。

### 1.3 Gateway 层

- `repo/services/ani-gateway/internal/router/kb_resources.go`：19 条路由已注册（L47-73），含 `PUT /knowledge-bases/:kb_id/permissions`（L65）——**permissions GET 未注册**（v1.yaml 也未声明，当前对账干净）。
- client 注入模式：`kbInjectedClient KBGRPCClient`（接口定义 `kb_grpc_client.go` L43）+ `registerKnowledgeBasesWithClient`；新 RPC 需同步扩接口 + 注入实现。
- handler 通用模式：`instanceTenantID(c)` 取租户（body 内 tenant_id 一律忽略，跨租户隔离 SPEC §7.1）→ `c.BindJSON` → `idempotency_key` 非空 + `uuid.Parse` 校验 → `a.client.XXX(...)` → `writeKBError(c, err)` → JSON。
- 202 共享 payload：`asyncTaskRefJSON{task_id, task_type, status}`（notify-uploaded / reparse 已用）。
- JSONB 透传：`jsonbToRaw`（chunk custom_metadata / message sources 先例）——B8 审计 detail 同模式。
- 用户身份：`middleware.GetUserID(c)` 已有先例（`admin_tenant_resources.go` L382-391 adminActorUserID）——B8 actor 链路复用。

### 1.4 kb-service 层

- `UpdateKBPermissions` 现为 stub：`p1_rpcs.py` `update_kb_permissions()` 返回 UNIMPLEMENTED；`grpc_server.py` L2038-2039 委托。B2 先例（ListKBCitations/ListKBSessions）为「直接在 grpc_server.py 实现、移除 p1_rpcs 委托」——B4 沿用。
- 幂等基建（`app/repositories/async_task.py`）：`find_by_idempotency_key(tenant_id, idempotency_key, task_type)`（task_type 收窄匹配防跨操作串台）、`create_task_in_tx` / `complete_task_in_tx`（同事务原子写）、`revive_task_in_tx`（failed→pending 自愈）。
- task_type 惯例：`kb.create` / `kb.update` / `kb.parse` / `kb.reparse`——本方案新增 `kb.perm.update`（B4）/ `kb.rebuild`（B6）/ `kb.config.update`（B7）。
- outbox event_type 惯例：`kb.parse` / `kb.reparse`——新增 `kb.rebuild`。
- NATS：解析消费走 **`ani.tasks.kb.parse.v2`**（`settings.nats_parse_subject_v2`，与 legacy `ani.tasks.kb.parse` 区分）；consumer `app/consumers/parse_consumer.py` 订阅 v2 subject，调 `ParseOrchestrator`。
- `ParseOrchestrator`（`app/services/parse_orchestrator.py`）已实现幂等重解析（删旧 chunks + Core 向量再写入，RAG-REFACTOR-STEP-5 验证）——B6 rebuild 按文档批量复用。
- Core API client（`app/core_api/client.py`）可查询平台侧数据——B5 ListKBModels 的模型聚合数据源。

### 1.5 契约漂移与修正决策（B5 统一处理）

v1.yaml 的 `KBConfig` / `UpdateKBConfigRequest` 与 proto / DB 存在三处漂移：

| 漂移点 | v1.yaml 现状 | proto / DB 现状 | 修正决策（B5） |
|---|---|---|---|
| 字段名 | `retrieval_strategy` | `retrieval_mode`（proto L81；DB 列名同） | v1.yaml 改名 `retrieval_mode`，与 `KnowledgeBase` schema（已用 `retrieval_mode`）及 CreateKB/Query 请求字段对齐 |
| 枚举值 | `[vector, hybrid]` | `vector \| hybrid \| keyword`（DB CHECK 三值） | v1.yaml 枚举补 `keyword` |
| 默认值 | `default: vector` | DB 默认 `hybrid`；proto 注释 hybrid(默认) | v1.yaml 默认值改 `hybrid` |

**修正理由**：`GET/PUT /config` 从未实现也从未注册路由（baseline `spec_not_in_code`），无任何调用方依赖旧字段名/枚举/默认值——修正无兼容负担，对齐 B1-B3 SPEC「目标端点未发布，契约修正免版本协商」论证。B1 交付的 Gateway `knowledgeBaseJSON` 已输出 `retrieval_mode`，修正后 v1.yaml 与实际响应面完全一致。

---

## 2. B4（#20 + #19）：权限读写对

### 2.1 目标与范围

交付 KB 级 ACL 管理面：`kb_permissions` 表落地 + `PUT /permissions`（替换 UNIMPLEMENTED stub）+ `GET /permissions`（新增契约）。查询侧执行闭环（list/get/query 可见性过滤）不在本批（§0.3 / Q2）。

### 2.2 契约改动

**proto 新增（`kb_service.proto`，插在 P1 声明区 UpdateKBPermissions 之后）：**

```proto
// GetKBPermissions returns the access permissions of a KB (P1).
// No kb_permissions row → returns defaults (public_read=false, empty list).
rpc GetKBPermissions(GetKBPermissionsRequest) returns (KBPermissions);

message GetKBPermissionsRequest {
  string tenant_id = 1;
  string kb_id     = 2;
}

message KBPermissions {
  string kb_id                     = 1;
  bool   public_read              = 2;
  repeated string allowed_user_ids = 3;
  google.protobuf.Timestamp updated_at = 4;
}
```

proto 已 import `google.protobuf.Timestamp`（KnowledgeBase 已用），无新增依赖。

**v1.yaml：`/knowledge-bases/{kb_id}/permissions` 路径补 get（置于 put 之前）：**

```yaml
    get:
      operationId: getKnowledgeBasePermissions
      summary: 读取知识库访问权限
      description: |
        回显 KB 权限配置（public_read + allowed_user_ids）。
        无权限记录时返回默认值（public_read=false, allowed_user_ids=[]），而非 404。
      tags: [KnowledgeBases]
      parameters:
        - { name: kb_id, in: path, required: true, schema: { type: string, format: uuid } }
      responses:
        "200":
          description: 当前权限配置
          content:
            application/json:
              schema: { $ref: '#/components/schemas/KBPermissions' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "403": { $ref: '#/components/responses/Forbidden' }
        "404": { $ref: '#/components/responses/NotFound' }
```

**v1.yaml 新增 schema（`components/schemas`，置于 UpdateKBPermissionsRequest 之后）：**

```yaml
    KBPermissions:
      type: object
      required: [kb_id, public_read, allowed_user_ids, updated_at]
      description: 知识库访问权限（B4）。对齐 kb_service.proto KBPermissions。
      properties:
        kb_id:            { type: string, format: uuid }
        public_read:      { type: boolean }
        allowed_user_ids: { type: array, items: { type: string, format: uuid } }
        updated_at:       { type: string, format: date-time }
```

注：`allowed_user_ids` 在 DB 为 `UUID[]`，schema 以 uuid 字符串数组表达；「返回展示名便于渲染」的增强（上游 plan §2.6 建议）依赖 Core 用户中心联查，列为 Q3 不阻塞本批。

### 2.3 数据层迁移（`repo/services/kb-service/migrations/005_kb_permissions.sql`）

```sql
-- B4 (#20/#19): KB-level ACL store. Defaults when no row exists:
-- public_read=false, allowed_user_ids=[] (upstream plan §1.3/§2.6).
CREATE TABLE kb_permissions (
  kb_id            UUID PRIMARY KEY REFERENCES knowledge_bases(id) ON DELETE CASCADE,
  tenant_id        UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  public_read      BOOLEAN NOT NULL DEFAULT FALSE,
  allowed_user_ids UUID[] NOT NULL DEFAULT '{}',
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE kb_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_permissions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_permissions
  AS RESTRICTIVE
  USING (tenant_id = current_setting('app.tenant_id')::uuid);

CREATE INDEX idx_kb_permissions_tenant ON kb_permissions(tenant_id);

GRANT SELECT, INSERT, UPDATE ON kb_permissions TO ani_app;
```

要点：
- RLS 形态对齐 `002_kb_chunks.sql`（RESTRICTIVE + `app.tenant_id`）；`kb_id` 为 PK 即与 KB 一对一，无需额外唯一索引。
- `tenant_id` 冗余列是 RLS policy 谓词所必需（`kb_chunks` 同模式）；`ON DELETE CASCADE` 随 KB 删除自动清理。
- 数组类型选 `UUID[]`（上游 plan §1.3 DDL 原样）；proto 层 `repeated string`，servicer 负责 uuid 合法性校验。

### 2.4 repository 层（`app/repositories/permission.py` 新建）

```python
"""kb_permissions repository (B4)."""

async def get_permissions(conn, *, tenant_id: str, kb_id: str) -> dict:
    """读权限行；无行返回默认值（契约：非 404）。RLS 事务内。"""
    async with conn.transaction():
        await set_tenant_context(conn, tenant_id)
        row = await conn.fetchrow(
            "SELECT kb_id, public_read, allowed_user_ids, updated_at "
            "  FROM kb_permissions WHERE kb_id = $1",
            uuid.UUID(kb_id),
        )
    if row is None:
        return {"kb_id": kb_id, "public_read": False,
                "allowed_user_ids": [], "updated_at": None}
    return {"kb_id": str(row["kb_id"]), "public_read": row["public_read"],
            "allowed_user_ids": [str(u) for u in row["allowed_user_ids"]],
            "updated_at": row["updated_at"]}

async def upsert_permissions_in_tx(conn, *, tenant_id: str, kb_id: str,
                                   public_read: bool, allowed_user_ids: list[str]) -> None:
    """UPSERT 权限行（调用方事务内，与幂等记录原子提交）。"""
    await set_tenant_context(conn, tenant_id)
    await conn.execute(
        """
        INSERT INTO kb_permissions (kb_id, tenant_id, public_read, allowed_user_ids)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (kb_id) DO UPDATE
          SET public_read = EXCLUDED.public_read,
              allowed_user_ids = EXCLUDED.allowed_user_ids,
              updated_at = now()
        """,
        uuid.UUID(kb_id), uuid.UUID(tenant_id), public_read,
        [uuid.UUID(u) for u in allowed_user_ids],
    )
```

### 2.5 gRPC servicer 实现（`grpc_server.py`，替换 p1_rpcs 委托）

**GetKBPermissions（读路径，无幂等）：**

```
GetKBPermissions(request, context):
  1. 校验 tenant_id/kb_id 非空（否则 INVALID_ARGUMENT）
  2. conn = await pool.acquire()
     KB 存在性校验复用 get_kb 的 RLS 查询：不存在 → context.abort(NOT_FOUND)
     （KB 不存在时不能返回默认权限——404 语义与 getKnowledgeBase 一致）
  3. perm = permission_repo.get_permissions(conn, tenant_id, kb_id)
  4. 返回 KBPermissions(...)
     updated_at = perm.updated_at or kb.created_at   # 无行时回退 KB 创建时间，
                                                      # 避免 proto Timestamp 零值漏出 1970
```

**UpdateKBPermissions（写路径，幂等）：**

```
UpdateKBPermissions(request, context):
  1. 校验：tenant_id/kb_id/idempotency_key 非空且合法 uuid；
     allowed_user_ids 每项合法 uuid（不合法 → INVALID_ARGUMENT；保序去重）
  2. async with conn.transaction():        # 单事务原子提交（对齐 UpdateKB 模式）
       a. set_tenant_context；SELECT kb 行（不存在或 status='deleted' → NOT_FOUND）
       b. existing = async_task.find_by_idempotency_key(task_type="kb.perm.update")
          existing 且 status=completed → 直接回放 result（上次更新后的 KnowledgeBase）
       c. permission_repo.upsert_permissions_in_tx(...)       # §2.4
       d. SELECT kb 行（拿最新快照作为幂等 result）
       e. async_task.create_task_in_tx(task_type="kb.perm.update",
              resource_type="knowledge_base", resource_id=kb_id,
              payload={"public_read":..., "allowed_user_ids":[...]})
          并发同键 UniqueViolationError → 回读该行回放（对齐 CreateKB 竞态自愈）
       f. async_task.complete_task_in_tx(task_id, result=kb_json)
  3. 返回 _kb_row_to_pb(kb_row)            # 契约 returns KnowledgeBase
```

实现后删除 `p1_rpcs.py` 的 `update_kb_permissions` stub 与 `grpc_server.py` 的委托（对齐 B2 处理 ListKBCitations/ListKBSessions 的先例）。

### 2.6 Gateway（`kb_resources.go` + `kb_grpc_client.go`）

**路由注册（PUT permissions 旁）：**

```go
svc.GET("/knowledge-bases/:kb_id/permissions", api.getKnowledgeBasePermissions)
```

**handler + JSON 结构（新增）：**

```go
func (a *kbAPI) getKnowledgeBasePermissions(ctx context.Context, c *app.RequestContext) {
	if a.client == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "UNAVAILABLE", "kb-service gRPC client not configured")
		return
	}
	perm, err := a.client.GetKBPermissions(ctx, instanceTenantID(c), c.Param("kb_id"))
	if err != nil {
		writeKBError(c, err)
		return
	}
	c.JSON(http.StatusOK, kbPermissionsToJSON(perm))
}

type kbPermissionsJSON struct {
	KbID           string   `json:"kb_id"`
	PublicRead     bool     `json:"public_read"`
	AllowedUserIDs []string `json:"allowed_user_ids"`
	UpdatedAt      string   `json:"updated_at,omitempty"`
}

func kbPermissionsToJSON(p *kbv1.KBPermissions) kbPermissionsJSON {
	if p == nil {
		return kbPermissionsJSON{}
	}
	return kbPermissionsJSON{
		KbID:           p.GetKbId(),
		PublicRead:     p.GetPublicRead(),
		AllowedUserIDs: p.GetAllowedUserIds(),
		UpdatedAt:      protoTimestampToRFC3339(p.GetUpdatedAt()),
	}
}
```

`KBGRPCClient` 接口 + 实现补 `GetKBPermissions(ctx, tenantID, kbID) (*kbv1.KBPermissions, error)`（单参数透传，对齐 GetKB 形态）。

v1.yaml 的 get permissions 声明与 Gateway 注册同 PR 完成，`make validate-architecture` 全量对账直接通过，无需 baseline 登记。

### 2.7 错误码与幂等语义

| 场景 | gRPC | HTTP | 说明 |
|---|---|---|---|
| KB 不存在 / 已删 | NOT_FOUND | 404 | GET 与 PUT 同语义 |
| 幂等键缺失/非法/白名单含非法 uuid | INVALID_ARGUMENT | 400 | Gateway 前置校验 + servicer 双保险 |
| 重复 idempotency_key（已完成） | — | 200 回放 | 上次结果原样返回（同键重试语义，对齐 UpdateKB，非 409） |
| 并发同键竞态 | UniqueViolation 自愈 | 200 回放 | 回读行回放，对齐 CreateKB |
| 权限不足（非管理员改权限） | PERMISSION_DENIED | 403 | 沿用 Gateway 现有 RBAC 中间件（新增 scope 是 Q2 范畴） |

### 2.8 测试要点

- kb-service pytest（`python -m pytest`，make 盲区须显式跑）：
  - `get_permissions` 无行 → 默认值 + updated_at 回退 KB.created_at；有行 → 真值。
  - `update_permissions` 首次 upsert（INSERT 分支）→ 二次（DO UPDATE 分支）→ 同键重试回放同一 KB JSON。
  - RLS：跨租户读 kb_permissions 行不可见。
  - 级联删除：`DELETE FROM knowledge_bases` → kb_permissions 行随删。
- Gateway `go test ./internal/router/...`：GET permissions fake client 注入（对齐现有 `registerKnowledgeBasesWithClient` 测试模式）。
- 契约对账：`make validate-services` + `make validate-architecture`。

---

## 3. B5（#22 + #25）：配置读取 + 可用模型列表

### 3.1 目标与范围

`GET /config`（回显 KB 全部配置）+ `GET /models`（嵌入/推理候选模型列表）。含两处前置工程：`ocr_enabled` 列迁移 + `retrieval_strategy → retrieval_mode` 契约修正（§1.5）。

### 3.2 契约修正（v1.yaml，本批先行提交）

`KBConfig` / `UpdateKBConfigRequest` 两 schema 的 `retrieval_strategy` 字段同步修正（修正后全文）：

```yaml
    KBConfig:
      type: object
      description: |
        知识库配置（入库 + 问答）。对齐 UX §4.3 概览配置：入库配置区
        （embedding_model / chunk_size / ocr_enabled）与问答配置区
        （top_k / score_threshold / retrieval_mode）。
        B5 契约修正：原 retrieval_strategy 改名 retrieval_mode（对齐 KnowledgeBase
        schema / kb_service.proto / DB 列名），枚举补 keyword，默认值改 hybrid。
      properties:
        embedding_model: { type: string, description: "嵌入模型名，修改触发全库重建（UX §4.3）" }
        chunk_size:      { type: integer, minimum: 1, maximum: 8192, description: "分块大小（tokens）" }
        ocr_enabled:     { type: boolean, default: false, description: "是否启用 OCR（入库配置）" }
        top_k:           { type: integer, minimum: 1, maximum: 20, description: "问答 TopK" }
        score_threshold: { type: number, format: float, minimum: 0.0, maximum: 1.0, description: "相似度阈值" }
        retrieval_mode:  { type: string, enum: [vector, hybrid, keyword], default: hybrid, description: "检索策略：向量 / 混合 / 关键词" }

    UpdateKBConfigRequest:
      # 其余字段不变，retrieval_strategy 行替换为：
        retrieval_mode:  { type: string, enum: [vector, hybrid, keyword], description: "检索策略；空表示不修改" }
```

### 3.3 数据层迁移（`006_kb_ocr_enabled.sql`）

```sql
-- B5 (#22): KBConfig.ocr_enabled persistence. Default false (v1.yaml default).
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS ocr_enabled BOOLEAN NOT NULL DEFAULT FALSE;
```

（对齐 003/004 迁移的 `ADD COLUMN IF NOT EXISTS` 幂等风格。）

### 3.4 proto 新增

```proto
// GetKBConfig returns the ingest/query configuration of a KB (P1).
rpc GetKBConfig(GetKBConfigRequest) returns (KBConfig);

// ListKBModels returns the embedding/inference models available to a KB (P1).
rpc ListKBModels(ListKBModelsRequest) returns (ModelList);

message GetKBConfigRequest {
  string tenant_id = 1;
  string kb_id     = 2;
}

message KBConfig {
  string tenant_id       = 1;
  string kb_id           = 2;
  string embedding_model = 3;
  int32  chunk_size      = 4;
  bool   ocr_enabled     = 5;
  int32  top_k           = 6;
  float  score_threshold = 7;
  string retrieval_mode  = 8;   // vector | hybrid | keyword
}

message ListKBModelsRequest {
  string tenant_id = 1;
  string kb_id     = 2;
}

message Model {
  string id                    = 1;
  string name                  = 2;
  string display_name          = 3;
  string description           = 4;
  string source                = 5;   // upload | huggingface | modelscope | builtin
  repeated string capabilities = 6;  // e.g. embedding / text-generation
  string status                = 7;   // pending | downloading | ready | error | deleted
  int64  total_size_bytes      = 8;
  google.protobuf.Timestamp created_at = 9;
  google.protobuf.Timestamp updated_at = 10;
  repeated string versions     = 11;
}

message ModelList {
  repeated Model embedding_models = 1;
  repeated Model inference_models = 2;
}
```

消息体放 kb 包内自洽（对齐包内惯例：除 `common.v1.AsyncTaskRef` 外领域消息均在 kb 包）。`Model` / `ModelList` 字段复用 v1.yaml 既有 schema（上游 plan §1.8）。

### 3.5 servicer 实现

**GetKBConfig（读路径）：**

```
GetKBConfig(request, context):
  1. 校验 tenant_id/kb_id 非空 → INVALID_ARGUMENT
  2. RLS 事务内 SELECT kb 行（embedding_model, chunk_size, ocr_enabled, top_k,
       score_threshold, retrieval_mode）→ 不存在或 deleted → NOT_FOUND
  3. 返回 KBConfig（列直映射，kb_id/tenant_id 回填）
```

**ListKBModels（数据聚合，候选口径 A2）：**

```
ListKBModels(request, context):
  1. 校验 + KB 存在性（同上，404 语义）
  2. embedding_models：
     a. 内置候选常量：[{"name":"bge-m3", "source":"builtin",
                       "capabilities":["embedding"], "status":"ready"}]
        （bge-m3 是 DB embedding_model 默认值，必须恒在候选）
     b. core_api 查询租户已导入模型，过滤 capability 含 embedding 且 status=ready → 合并去重（按 name）
        （仅 ready 进入候选：pending/downloading 未就绪不列，避免前端选中后不可用）
  3. inference_models：
     core_api 查询租户 inference-services（vLLM 就绪服务）→ 转 Model{source:"builtin",
        capabilities:["text-generation"], status:"ready"}（服务名即 name/display_name）
  4. 返回 ModelList
```

Core 端具体查询端点/参数以 `app/core_api/client.py` 现有能力为准，实现时与模型中心对齐口径（上游 plan §1.8 原话列为假设 A2，不阻塞契约与骨架开发）。

### 3.6 Gateway

**路由注册：**

```go
svc.GET("/knowledge-bases/:kb_id/config", api.getKnowledgeBaseConfig)
svc.GET("/knowledge-bases/:kb_id/models", api.listKnowledgeBaseModels)
```

**handler（均为无 body gRPC 透传，对齐 getKnowledgeBase 模式）：**

```go
type kbConfigJSON struct {
	EmbeddingModel string  `json:"embedding_model"`
	ChunkSize      int32   `json:"chunk_size"`
	OcrEnabled     bool    `json:"ocr_enabled"`
	TopK           int32   `json:"top_k"`
	ScoreThreshold float32 `json:"score_threshold"`
	RetrievalMode  string  `json:"retrieval_mode"`
}

// getKnowledgeBaseConfig: client.GetKBConfig(ctx, tenantID, kbID) → kbConfigJSON → 200
// listKnowledgeBaseModels: client.ListKBModels(ctx, tenantID, kbID) →
//   {"embedding_models":[...], "inference_models":[...]} → 200（modelToJSON 数组映射）
```

`KBGRPCClient` 接口补两个单参数透传方法。

### 3.7 baseline 清理（`repo/architecture/services-route-baseline.yaml`）

本批注册 `GET /config`、`GET /models` 两条路由后，删除对应两条 `spec_not_in_code` 登记（现 L56-61、L74-79）。**注意**：`PUT /config`（现 L62-67）与 `POST /rebuild`（现 L68-73）仍为未实现，登记保留至 B7 / B6 各自清理。

### 3.8 测试要点

- pytest：GetKBConfig 列直映射（含 ocr_enabled 新列默认 false、retrieval_mode 三值）；ListKBModels 内置 bge-m3 恒在、Core 聚合（core_api client mock 注入）。
- 迁移幂等：006 重复执行不报错（IF NOT EXISTS）。
- Gateway：fake client 两 handler + `go test ./internal/router/...`。
- 对账：`make validate-services`（修正后 retrieval_mode 全量一致）+ `make validate-architecture`（baseline 减 2 条后无残留误报）。

---

## 4. B6（#24）：知识库重建 `POST /v1/knowledge-bases/{kb_id}/rebuild`

### 4.1 目标与语义

- 触发指定 KB 的**全文档重跑解析管线**（清洗 → 分块 → 嵌入 → 入库 Milvus）。
- 语义：异步 202 任务（`AsyncTaskRef`），KB `status` 置 `rebuilding`，任务完成后回 `active`。
- **写操作互斥**：rebuilding 期间所有写操作返回 409（见 §4.6 互斥矩阵）；**query 不拦截**（Q1 决策：旧行列数据仍可检索，读可用性优先）。
- 进度通过 `GET /async-tasks/{task_id}` 的 `progress_pct` 反映（0-100 整数）。

### 4.2 proto 新增

```protobuf
// P1 声明区追加（ReparseDocument 之后）
// 重建知识库：全文档重跑解析管线（异步任务）
rpc RebuildKB(RebuildKBRequest) returns (common.v1.AsyncTaskRef);

message RebuildKBRequest {
  string tenant_id = 1;
  string kb_id = 2;
  string idempotency_key = 3; // 客户端幂等键，uuid
}
```

v1.yaml 已有 `POST /rebuild` + `RebuildKnowledgeBaseRequest`（L2845-2870），无需改动。

### 4.3 servicer 实现（`grpc_server.py`）

七步事务（对齐 `ReparseDocument` 的写路径模式）：

```python
async def RebuildKB(self, request, context):
    # ① 校验
    tenant_id, kb_id = _require_uuids(request.tenant_id, request.kb_id)
    key = _require_idempotency_key(request.idempotency_key)

    # ② 幂等回放
    try:
        existing = await self.async_tasks.find_by_idempotency_key(
            tenant_id, key, task_type="kb.rebuild")
        if existing and existing.status in ("queued", "running"):
            return _task_ref(existing)          # 200 回放
        if existing and existing.status == "succeeded":
            return _task_ref(existing)          # 200 回放（不重复重建）
        # failed → 允许重试，走新建
    except Exception:
        pass  # 查询失败不阻塞主流程，交由 create_task_in_tx 竞态自愈

    async with self.db.pool.acquire() as conn:
        async with conn.transaction():          # ③ 事务开始
            set_tenant_context(conn, tenant_id)  # RLS

            # ④ 读 KB + 状态互斥
            kb = await self.kbs.get_kb_in_tx(conn, kb_id)   # 无 → 404
            if kb.status != "active":
                context.abort(grpc.StatusCode.FAILED_PRECONDITION,
                              "knowledge base is not active")

            # ⑤ 置位 + 建任务 + 发事件（同一事务，原子）
            await self.kbs.set_status_in_tx(conn, kb_id, "rebuilding")
            task = await self.async_tasks.create_task_in_tx(conn,
                tenant_id=tenant_id, task_type="kb.rebuild",
                idempotency_key=key,
                payload={"kb_id": kb_id, "retrieval_mode": kb.retrieval_mode,
                         "embedding_model": kb.embedding_model,
                         "chunk_size": kb.chunk_size,
                         "ocr_enabled": kb.ocr_enabled,
                         "total_docs": None})     # consumer 启动时回填
            await self.outbox.insert_in_tx(conn, event_type="kb.rebuild",
                payload={"task_id": task.task_id, "kb_id": kb_id})

    # ⑥ 返回（含 UniqueViolation 竞态自愈：捕获后回放已有任务）
    return common_v1.AsyncTaskRef(
        task_id=task.task_id, task_type=task.task_type, status=task.status)
```

- **三写原子性**：`status='rebuilding'` + `async_tasks` 行 + `outbox` 事件在同一事务——任一失败全部回滚，KB 不会卡在 rebuilding。
- **task_type 惯例**：新增 `kb.rebuild`（现有 `kb.create` / `kb.update` / `kb.parse` / `kb.reparse`），幂等查询带 task_type 收窄防跨操作串台。

### 4.4 重建消费者 `app/consumers/rebuild_consumer.py`（新建）

- **NATS subject**：新增 `ani.tasks.kb.rebuild.v1`（不复用 `ani.tasks.kb.parse.v2`——rebuild 需要 KB 级聚合进度与生命周期，parse subject 语义是单文档；通过 `settings.nats_rebuild_subject` 配置，main.py 订阅模式对齐 parse_consumer）。

```python
class RebuildConsumer:
    async def handle(self, msg):
        evt = json.loads(msg.data)
        task_id, kb_id = evt["task_id"], evt["kb_id"]
        async with self.db.pool.acquire() as conn:
            set_tenant_context(conn, tenant_id)
            # ① 置 running
            await self.async_tasks.mark_running(task_id)
            # ② 圈定文档：ready + failed 均重跑（failed 是重建的典型动机）
            docs = await self.docs.list_by_kb(conn, kb_id,
                parse_status_in=("ready", "failed"))
            total = len(docs)
            await self.async_tasks.update_payload(task_id,
                {"total_docs": total})
            failed = []
            try:
                for i, doc in enumerate(docs):
                    try:
                        # ③ 直调 ParseOrchestrator（不经 NATS 逐文档发事件，
                        #    避免二级任务表膨胀；reparse_document 内部已含
                        #    完整管线与失败落库）
                        await self.orchestrator.reparse_document(
                            tenant_id, kb_id, doc.document_id)
                    except Exception:
                        failed.append(doc.document_id)   # 文档级失败不中断
                    finally:
                        # ④ 进度推进（整数，避免长事务）
                        await self.async_tasks.set_progress(task_id,
                            progress_pct=(i + 1) * 100 // total if total else 100)
            finally:
                # ⑤ 收尾：无论如何回 active（防 KB 永久卡 rebuilding）
                async with self.db.pool.acquire() as c2:
                    set_tenant_context(c2, tenant_id)
                    await self.kbs.set_status(c2, kb_id, "active")
                # ⑥ 终态
                if failed:
                    await self.async_tasks.complete_task(task_id,
                        status="failed" if len(failed) == total else "succeeded",
                        result={"failed_doc_ids": failed,
                                "rebuilt": total - len(failed)})
                else:
                    await self.async_tasks.complete_task(task_id,
                        status="succeeded", result={"rebuilt": total})
```

- **全部失败 vs 部分失败**：部分失败记 `succeeded` + `failed_doc_ids`（可用 `POST /reparse` 单独重跑残留）；全部失败记 `failed`。
- **失败文档不中断**：重建的意义就是修复历史失败，单文档异常只记录、继续下一份。
- **finally 兜底**：consumer 崩溃也回 `active`（若任务仍处 running，可由后台巡检复活——复用 `revive_task_in_tx` 机制，本批不新建巡检）。

### 4.5 Gateway

```go
svc.POST("/knowledge-bases/:kb_id/rebuild", api.rebuildKnowledgeBase)
```

handler 对齐 `reparseDocument` 模式：BindJSON 校验 idempotency_key（uuid）→ `client.RebuildKB(ctx, tenantID, kbID, key)` → 202 + `asyncTaskRefJSON{task_id, task_type, status}`。`KBGRPCClient` 补方法。

### 4.6 rebuilding 互斥矩阵（写操作 409 拦截）

| 操作 | 拦截 | 说明 |
|---|---|---|
| 上传文档（CreateDocument） | **409** | `NOT_FOUND`/`FAILED_PRECONDITION` → 409，message 带原因 |
| 通知已上传（NotifyUploaded） | **409** | 同上 |
| 单文档重解析（ReparseDocument） | **409** | 避免与 rebuild 并发竞争同一文档 |
| 删除文档（DeleteDocument） | **409** | 目标集合变更会破坏重建一致性 |
| 修改 KB（UpdateKB） | **409** | 基础信息在重建快照中 |
| PUT permissions（UpdateKBPermissions） | **409** | B4 §2.5 已含状态校验 |
| PUT config（UpdateKBConfig，B7） | **409** | B7 §5 将依赖本矩阵 |
| **query（QueryKnowledgeBase）** | **不拦截** | Q1 决策：旧向量数据仍有效，读可用性优先 |

实现方式：上述各 servicer 在读 KB 后统一加 `if kb.status == "rebuilding": abort(FAILED_PRECONDITION, "kb.rebuilding")`。

### 4.7 baseline 清理

本批注册 `POST /rebuild` 后，删除 baseline 中第三条登记（现 L68-73）。剩余 `PUT /config`（L62-67）留待 B7 清理。

### 4.8 测试要点

- servicer：三写原子性（mock conn 事务回滚时 status 不残留 rebuilding）；重复触发（幂等回放 200 同 task_id）；非 active 状态触发 → 409。
- consumer：fake ParseOrchestrator 验证——全量编排调用次数、progress_pct 单调递增至 100、单文档抛异常不中断后续、finally 回 active、全失败/部分失败的终态与 result。
- 互斥矩阵：rebuilding 状态下逐操作断言 409；query 断言 200。
- 对账：`make validate-services` + `make validate-architecture`（baseline 减至 1 条）。

---

## 5. B7（#23）：配置修改 `PUT /v1/knowledge-bases/{kb_id}/config`

### 5.1 目标与语义

- 修改影响检索管线行为的运行时配置：`embedding_model` / `chunk_size` / `ocr_enabled` / `top_k` / `score_threshold` / `retrieval_mode`。
- **重建联动**：`embedding_model` 或 `chunk_size` 变化意味着**既有向量数据失效**（不同模型/不同分块 → 向量空间不兼容），必须触发 rebuild（依赖 B6 编排）；其余四项（ocr_enabled/top_k/score_threshold/retrieval_mode）仅影响后续解析或查询行为，不强制重建。
- 同步 200（非异步）：纯配置更新无长耗时操作；隐含的 rebuild 以独立 AsyncTaskRef 附加在响应中（见 §5.5 响应设计）。

### 5.2 proto 新增

```protobuf
// P1 声明区追加
// 更新知识库配置；embedding_model/chunk_size 变更自动触发重建
rpc UpdateKBConfig(UpdateKBConfigRequest) returns (UpdateKBConfigResponse);

message UpdateKBConfigRequest {
  string tenant_id = 1;
  string kb_id = 2;
  string idempotency_key = 3;
  optional string embedding_model = 4;  // 未传 = 不变
  optional int32 chunk_size = 5;        // 未传 = 不变
  google.protobuf.BoolValue ocr_enabled = 6;  // 三态：未传=不变 / true / false
  optional int32 top_k = 7;
  optional float score_threshold = 8;
  optional string retrieval_mode = 9;   // vector | hybrid | keyword
}

message UpdateKBConfigResponse {
  KBConfig config = 1;
  common.v1.AsyncTaskRef rebuild_task = 2;  // 触发重建时非空，否则零值
}
```

- **三态表达**：`ocr_enabled` 用 `google.protobuf.BoolValue` wrapper——`optional bool` 无法区分「未传」与「显式 false」，而关闭 OCR 是合法操作。其余字段默认零值即「未传」（空串/0 本身不是合法值，值域校验兜底）。
- v1.yaml 的 `UpdateKBConfigRequest` schema 已按 B5 §3.2 修正（retrieval_mode 三值枚举）；响应 schema 需补 `rebuild_task`（`AsyncTaskRef` 引用，nullable）。

### 5.3 值域校验（servicer 前置）

| 字段 | 约束 | 拒绝（400 INVALID_ARGUMENT） |
|---|---|---|
| embedding_model | 必须在 `ListKBModels` 返回的可用嵌入模型集合内 | 未知模型 |
| chunk_size | 100 ≤ x ≤ 8192 | 越界；非 64 倍数不强制（登记 Q5 之外的宽松项） |
| ocr_enabled | 无 | — |
| top_k | 1 ≤ x ≤ 100 | 越界 |
| score_threshold | 0 ≤ x ≤ 1 | 越界 |
| retrieval_mode | ∈ {vector, hybrid, keyword} | 非法枚举值 |

全部字段未传 → 400 `no fields to update`（对齐 UpdateKB 的空更新拒绝模式）。

### 5.4 servicer 实现

```python
async def UpdateKBConfig(self, request, context):
    tenant_id, kb_id, key = ...  # ① 校验（对齐 B4 §2.5 步骤①）
    # ② 幂等回放（task_type="kb.config.update"）
    existing = await self._replay_if_active(tenant_id, key, "kb.config.update")
    if existing:
        return _config_response(existing.result["config"], 
                                existing.result.get("rebuild_task"))

    # ③ 值域校验（§5.3 表）
    ...  # 违规 → context.abort(INVALID_ARGUMENT, ...)

    async with self.db.pool.acquire() as conn:
        async with conn.transaction():
            set_tenant_context(conn, tenant_id)
            kb = await self.kbs.get_kb_in_tx(conn, kb_id)      # ④ 404
            if kb.status == "rebuilding":
                context.abort(FAILED_PRECONDITION, "kb.rebuilding")  # ⑤ 互斥

            # ⑥ 变更检测：只有显式传入且值不同的字段才 UPDATE
            patch, rebuild_needed = {}, False
            if request.HasField("embedding_model") and request.embedding_model != kb.embedding_model:
                patch["embedding_model"] = request.embedding_model
                rebuild_needed = True
            if request.HasField("chunk_size") and request.chunk_size != kb.chunk_size:
                patch["chunk_size"] = request.chunk_size
                rebuild_needed = True
            if request.HasField("ocr_enabled") and request.ocr_enabled.value != kb.ocr_enabled:
                patch["ocr_enabled"] = request.ocr_enabled.value   # 不触发重建
            ...  # top_k / score_threshold / retrieval_mode 同理

            if not patch:
                context.abort(INVALID_ARGUMENT, "no effective change")

            # ⑦ 变更前快照（供 B8 审计 before_state）
            before = _config_snapshot(kb)

            updated = await self.kbs.update_config_in_tx(conn, kb_id, patch)

            # ⑧ 幂等任务（记录最终 config + 是否触发 rebuild）
            task = await self.async_tasks.create_task_in_tx(conn, ...,
                task_type="kb.config.update", payload={"kb_id": kb_id,
                    "patch": patch, "rebuild_needed": rebuild_needed})

            # ⑨ rebuild 联动（同事务预置：置位 + 建重建任务 + outbox）
            rebuild_ref = None
            if rebuild_needed:
                await self.kbs.set_status_in_tx(conn, kb_id, "rebuilding")
                rt = await self.async_tasks.create_task_in_tx(conn, ...,
                    task_type="kb.rebuild", idempotency_key=f"rebuild:{key}")
                await self.outbox.insert_in_tx(conn, event_type="kb.rebuild",
                    payload={"task_id": rt.task_id, "kb_id": kb_id})
                rebuild_ref = _task_ref(rt)

            await self.async_tasks.complete_task_in_tx(conn, task.task_id,
                status="succeeded",
                result={"config": _config_dict(updated),
                        "rebuild_task": _ref_dict(rebuild_ref)})

    return UpdateKBConfigResponse(config=_config_msg(updated),
                                  rebuild_task=rebuild_ref)
```

- **嵌套幂等键**：隐含 rebuild 的 idempotency_key 用 `f"rebuild:{key}"` 派生——config 更新重放时 rebuild 亦不重复触发（两个 task_type 各自唯一约束，互不串台）。
- **完成时机**：config 更新本身在事务内即 `succeeded`（rebuild 是另一个异步任务，独立生命周期）；前端拿 `rebuild_task.task_id` 轮询进度。

### 5.5 Gateway

```go
svc.PUT("/knowledge-bases/:kb_id/config", api.updateKnowledgeBaseConfig)
```

- handler：BindJSON（`rebuild_task` 为 null 或 `{task_id,task_type,status}`）→ 200 + `{...config, "rebuild_task": {...}|null}`。
- `KBGRPCClient` 补方法；baseline 清理最后一条（现 L62-67），KB 相关 `spec_not_in_code` **归零**。

### 5.6 测试要点

- 三态：不传 ocr_enabled 不清零；显式 false 正确落库。
- 变更检测：同值重复提交 → 400 no effective change；仅 top_k 变化 → 无 rebuild_task。
- 联动：改 embedding_model → 响应含 rebuild_task、KB status=rebuilding、两条 async_tasks + 一条 outbox 同事务。
- 嵌套幂等：同 idempotency_key 重放 → 200 且不产生第二个 rebuild 任务。
- rebuilding 状态下 PUT → 409。

---

## 6. B8（#21）：审计日志 `GET /v1/knowledge-bases/{kb_id}/audit-logs`

### 6.1 目标与语义

- 记录并查询指定 KB 的**管理面写操作**审计轨迹（谁、何时、做了什么、改了什么、成功与否），支撑合规审计与问题回溯。
- 读接口：键集分页（CursorPageRequest/Meta，禁 OFFSET），按 `created_at DESC, id DESC` 稳定排序。

### 6.2 DDL 决策：字段方案择一

上游两份文档对 `kb_audit_log` 的字段设计不一致，**本方案裁定采用 plan--knowledge_base.md §11.2 的方案**（before/after 状态对 + 错误码对），理由与合并结果：

| 维度 | plan §2.7（result+detail） | plan--kb §11.2（before/after+error） | **裁定** |
|---|---|---|---|
| 变更内容表达 | `result` 枚举 + `detail` JSONB 自由结构 | `before_state`/`after_state` JSONB 两个快照 | **采用后者**：diff 语义明确，读写两侧无需对每种操作维护 detail 结构 |
| 失败表达 | 需塞进 detail | `error_code`/`error_msg` 独立列 | **采用后者**：失败可索引、可统计 |
| 操作类型 | 无独立列 | `action` VARCHAR | **采用后者** |

合并后 DDL（迁移 `007_kb_audit_log.sql`）：

```sql
CREATE TABLE IF NOT EXISTS kb_audit_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    kb_id         UUID NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    actor_user_id UUID,                          -- NULL = 系统内部（rebuild consumer 等）
    action        VARCHAR(64) NOT NULL,          -- kb.create|kb.update|kb.delete|
                                                 -- kb.permissions.update|kb.config.update|
                                                 -- kb.rebuild|doc.create|doc.delete|doc.reparse
    before_state  JSONB,                          -- NULL = 新建类操作
    after_state   JSONB,                          -- NULL = 删除类操作
    error_code    VARCHAR(64),                    -- NULL = 成功
    error_msg     TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_kb_audit_log_kb_time
    ON kb_audit_log (tenant_id, kb_id, created_at DESC, id DESC);
ALTER TABLE kb_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE kb_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kb_audit_log
    AS RESTRICTIVE USING (tenant_id = current_setting('app.tenant_id')::uuid);
GRANT SELECT, INSERT ON kb_audit_log TO ani_app;
```

- **不含 updated_at**：日志只写不改，`created_at` 单时间列即可。
- **保留 7 天 × 保留策略**：本批不建清理任务（数据量小），登记 §10.2 Q6 是否引入 TTL 清理。

### 6.3 写侧埋点（横切 B4~B7 + 既有写路径）

统一通过 `app/repositories/audit.py` 的 `insert_audit_in_tx(conn, ...)` 在**各写操作的业务事务内**同事务写入（保证审计与业务变更原子，不丢不假）：

| 操作（servicer） | action | before/after | 触发批次 |
|---|---|---|---|
| CreateKB | `kb.create` | before=NULL / after=KB 快照 | B8 补埋（既有代码） |
| UpdateKB | `kb.update` | before/after=基础信息快照 | B8 补埋 |
| DeleteKB | `kb.delete` | before=快照 / after=NULL | B8 补埋 |
| UpdateKBPermissions（B4） | `kb.permissions.update` | before/after=`{public_read, allowed_user_ids}` | B4 实现时即埋（B8 定契约） |
| UpdateKBConfig（B7） | `kb.config.update` | before/after=§5.4 步⑦快照 | B7 实现时即埋 |
| RebuildKB / Rebuild 完成（B6） | `kb.rebuild` | before/after=`{status}`（触发与终态各一条） | B6 实现时即埋 |
| CreateDocument / DeleteDocument / Reparse | `doc.*` | 文档级快照 | B8 补埋 |
| **失败也记** | 同 action | before=意图 / after=NULL + error_code/error_msg | 各 servicer 的 abort 分支 catch 后补记再 re-raise |

- **actor 链路**：Gateway 在 metadata 注入 `x-actor-user-id`（`middleware.GetUserID(c)`，先例见 admin_tenant_resources.go L382-391）；kb-service servicer 读取 metadata，取不到则记 NULL（系统内部操作）。
- **实施顺序说明**：B4/B6/B7 各自实现时就按本表埋点（契约先定），B8 合入时只补既有路径（Create/Update/DeleteKB、doc.*）+ 读接口——避免最后一次性大改。

### 6.4 proto 新增

```protobuf
// P1 声明区追加
// 查询知识库审计日志（键集分页，created_at DESC）
rpc ListKBAuditLogs(ListKBAuditLogsRequest) returns (ListKBAuditLogsResponse);

message ListKBAuditLogsRequest {
  string tenant_id = 1;
  string kb_id = 2;
  common.v1.CursorPageRequest page = 3;   // limit + cursor
}

message AuditLogEntry {
  string id = 1;
  string kb_id = 2;
  string actor_user_id = 3;    // 空 = 系统内部
  string action = 4;
  string before_state = 5;    // JSON 序列化，空 = 无
  string after_state = 6;
  string error_code = 7;
  string error_msg = 8;
  google.protobuf.Timestamp created_at = 9;
}

message ListKBAuditLogsResponse {
  repeated AuditLogEntry items = 1;
  common.v1.CursorPageMeta page = 2;
}
```

v1.yaml 新增 `GET /audit-logs`（200 `AuditLogList`，`items[]` + `page`），schema 引用公共 CursorPage 结构（对齐 ListKBs 的分页形态）。

### 6.5 repository + servicer

```python
# app/repositories/audit.py
async def list_logs(conn, kb_id, limit, cursor):
    # cursor = base64("{created_at}|{id}")，WHERE (created_at, id) < (%s, %s)
    # ORDER BY created_at DESC, id DESC LIMIT n+1 → has_more 截断
    ...

# servicer：纯读，无幂等键；RLS set_tenant_context；404 校验 KB 归属
async def ListKBAuditLogs(self, request, context): ...
```

### 6.6 Gateway

```go
svc.GET("/knowledge-bases/:kb_id/audit-logs", api.listKnowledgeBaseAuditLogs)
```

handler：`cursor`/`limit` query 参数 → `client.ListKBAuditLogs` → 200 `{"items":[...], "page":{...}}`；`before_state`/`after_state` 以 JSON 字符串透传（前端 JSON.parse）。

### 6.7 测试要点

- 埋点原子性：业务事务回滚 → 无审计行；成功 → 审计行与业务变更同事务。
- 失败记录：业务失败分支（NOT_FOUND/ALREADY_EXISTS/FAILED_PRECONDITION/RESOURCE_EXHAUSTED）均产出 error_code/error_msg 行；INVALID_ARGUMENT（参数校验）不记（§6.8 偏差 3）。
- 分页：构造 25 条，limit=20 断言 cursor 往返、末页 has_more=false。
- RLS：跨租户查询空结果。
- actor：带/不带 `x-actor-user-id` metadata 两种路径。

### 6.8 B8 实施偏差登记（实施后回写，2026-09）

B8 已全部实施完成（b8-1~b8-10，工作区交付、未提交）。**B8 系提前实施**：§9 原排序为 B5/B6/B7 之后收尾，实际 B5/B6/B7 尚未实施（proto 无 GetKBConfig/ListKBModels/RebuildKB/UpdateKBConfig RPC，Gateway 无 config/models/rebuild 路由），B8 直接落地。以下为实施与原方案的偏差逐项登记，均以实际实现为准：

| # | plan 原表述 | 实际实现 | 裁定理由 |
|---|---|---|---|
| 1 | §6.2/§9：迁移文件 `007_kb_audit_log.sql` | `006_kb_audit_log.sql` | B5 尚未实施、`006_kb_ocr_enabled.sql` 从未创建，006 编号被 kb_audit_log 占用；B6/B7 的 action 枚举位（`kb.config.update`/`kb.rebuild`）已在迁移注释中预留，届时各自补埋点即用 |
| 2 | §6.4：`ListKBAuditLogsResponse { items, CursorPageMeta page }` | `{ items, next_cursor }` 扁平结构（string 可空） | 对齐 ListKBCitations/ListKBSessions 既有先例，proto 内减少一层公共依赖 |
| 3 | §6.3/§6.7：失败 404/409/400 各分支均产出审计行 | 失败口径 = `_AUDITED_FAILURE_CODES = {NOT_FOUND, ALREADY_EXISTS, FAILED_PRECONDITION, RESOURCE_EXHAUSTED}`（grpc_server.py L2839-2844）；INVALID_ARGUMENT（400 参数校验）不记 | 遵循用户决策「只记业务失败」；参数校验失败无审计价值且会被测试调用刷量 |
| 4 | §6.3：埋点表「失败也记」泛指各 servicer abort 分支 | 实际部分失败分支不埋：kb.create 的 409（ALREADY_EXISTS 时 knowledge_bases 行不存在，无法满足 kb_id NOT NULL FK，失败审计行结构上不可能）、kb.delete 的 404（KB 不存在无 before 可记）；doc_row-None 时跳过埋点防 FK 违反 | 失败分支取 before 意图快照不可得或无意义时不强行产出行；测试断言与实际行为对齐 |
| 5 | §6.3 埋点表未列 doc.parse | `doc.parse`（成功+失败）已埋：notify-uploaded 状态重置语义 | 状态重置属管理面写操作，补齐后 12 处埋点覆盖 9 个 action（§6.2 枚举去 B6/B7 两位） |
| 6 | §6.5：cursor 编码 `base64("{created_at}\|{id}")` | 实际实现一致（键集分页，LIMIT n+1 截断 has_more） | 无偏差，登记为核验通过项 |
| 7 | 未提及 limit 语义 | `limit or 20`：limit=0/缺省走默认 20，仅 (101, -1) 越界拒绝 | 0 视为未指定而非越界；与 OpenAPI 默认值语义一致 |
| 8 | 未提及 `_doc_audit_snapshot` 键集 | 8 键快照，不含 error_message 键 | parse_status=="pending" 已表达 reset 语义；测试断言按实际键集对齐 |
| 9 | 未提及新接口的 security/x-ani-authz 要求 | v1.yaml `listKnowledgeBaseAuditLogs` 补 `security: [{BearerAuth: []}, {ApiKeyAuth: []}]` + `x-ani-authz`（resource: knowledge_base / action: read / boundary: tenant / principal_kinds: [user]） | services-contract 对非 baseline 接口强制双声明，缺一即 validate-services 失败 |
| 10 | 未提及 SDK/API 文档再生成 | docs/api 再生成（services.html 补 audit-logs 路径）+ SDK 再生成（go/java/python/typescript client 各 +5 行，sdk-metadata.json +9 行，合计 6 文件 +34 行） | doc-api/sdks 幂等门禁要求契约与产物同步 |
| 11 | 未涉及 validate 脚本环境缺陷 | `validate_services_boundary.py` 新增 `EXCLUDED_DIRECTORY_NAMES` 排除集（.venv/bin/node_modules 等）+ `is_excluded_directory_member` 过滤，修复 rglob 扫 .venv 内非 UTF-8 joblib 夹具崩溃与 services/bin 误判 unknown_service_root；配套回归测试 + 断言修正（baseline warnings 5→4，RAG 重构删除 pymilvus 例外后断言漂移，此前被 .venv 崩溃掩盖） | Windows 工作区本地 .venv 触发；CI 无 venv 不受影响，但脚本对依赖树免疫属正确性修复 |
| 12 | 未涉及 compileall 扫描范围 | Makefile：`compileall -q -x '.*[\\/]\.venv[\\/].*' ai/rag-engine`（正则覆盖两种路径分隔符） | .venv 内 torch 文件用 Python 3.12 泛型语法，本机解释器 SyntaxError；排除依赖树属预期行为 |
| 13 | 未提及其余 b8-8 契约核对小项 | ⑤ 缩进脱钩/字母标记/doc_row-None FK 防御、intent possibly-unbound、nullableString、parse 批量断言失同步、12 处 TypeError（首跑暴露的存量运行时缺陷）均已在测试与实现侧对齐 | 随 b8-8 一并修复或对齐，明细见当次提交 |
| 14 | §6.2：单 RESTRICTIVE `tenant_isolation` policy；tenant_id 裸列 | 双 PERMISSIVE 策略（`kal_self` USING tenant_id + `kal_platform_bypass` TO ani_platform）对齐 005_kb_permissions 先例；`tenant_id` 含 `REFERENCES tenants(id) ON DELETE CASCADE` | 与 005 已合入模式保持一致，降低迁移审查差异面；plan §6.2 的单 policy 形态未被任何已合入迁移采用 |
| 15 | §9 原排序 B8 置于 B5/B6/B7 之后收尾；§6.3 B4 行「B4 实现时即埋」 | B8 提前实施（B5/B6/B7 未开工）；B4 的 `kb.permissions.update` 埋点由 B8 期间回补（B4 合入时 audit.py 与 insert_audit_in_tx 尚不存在，代码注释已引用 §6.3 契约） | 应用户需求先交付 #21；B4 回补埋点含 before=upsert 前 perm 行、after=intent、404 失败审计，同事务原子 |
| 16 | §6.3：actor 经 Gateway 注入 metadata 键 `x-actor-user-id` | 实现复用 Gateway 既有注入键 `x-user-id`（kb_resources.go / tenant_common.go 的 `metadata.AppendToOutgoingContext`），servicer `_actor_user_id` 读取该键并做 UUID 校验 | 复用既有约定零新增键、与 sessions 等先例一致；行为与 plan 意图等价，仅键名不同，B8 框架审查（2026-09-10）补登 |

**交付状态**：迁移 + proto/v1.yaml 契约 + 8 写路径埋点 + ListKBAuditLogs servicer + Gateway 路由/handler/KBGRPCClient + kb-service pytest 354 passed + Gateway go test 全绿 + `make validate-services`/`validate-architecture` 全绿 + SDK/docs 再生成幂等。

---

## 7. 公共实现模式（各批次统一遵循）

### 7.1 幂等模式（写操作通用）

```
① uuid/idempotency_key 校验（key 必须为 uuid 格式）
② find_by_idempotency_key(tenant_id, key, task_type)  ← task_type 收窄防跨操作串台
   ├─ queued/running/succeeded → 回放已有结果（200，同 task_id）
   └─ failed / 无记录 → 走新建
③ 业务事务内 create_task_in_tx
④ UniqueViolation（async_tasks_uq 唯一索引）→ 捕获后回放已有任务（竞态自愈）
```

- 新增 task_type：`kb.perm.update`（B4）/ `kb.rebuild`（B6）/ `kb.config.update`（B7）；B8 读接口无幂等。
- B7 的嵌套 rebuild 用派生键 `rebuild:{key}`，与 config 主任务分属两个 task_type，互不冲突。

### 7.2 RLS 租户隔离（全部数据访问）

- 每个连接使用前 `set_tenant_context(conn, tenant_id)`（`SET LOCAL app.tenant_id`）。
- 新表（kb_permissions / kb_audit_log）一律 `ENABLE + FORCE ROW LEVEL SECURITY` + RESTRICTIVE `tenant_isolation` policy + 显式 GRANT。

### 7.3 gRPC → HTTP 错误映射（Gateway `writeKBError` 统一）

| gRPC 状态 | HTTP | 场景 |
|---|---|---|
| NOT_FOUND | 404 | KB/文档不存在（含跨租户） |
| INVALID_ARGUMENT | 400 | uuid/枚举/值域/空更新 |
| FAILED_PRECONDITION | 409 | kb.rebuilding 互斥、status 非 active |
| RESOURCE_EXHAUSTED | 429 | 文档数上限（既有） |
| INTERNAL | 500 | 其余 |

### 7.4 键集分页（B8 audit-logs）

- 禁 OFFSET；cursor = `base64("{created_at_iso}|{uuid}")`；`WHERE (created_at, id) < (%s, %s)` 行值比较 + `ORDER BY created_at DESC, id DESC` + `LIMIT n+1` 判 has_more。
- 对齐 ListKBs / ListDocuments 既有 CursorPageRequest/Meta 形态。

---

## 8. 测试与验收策略

### 8.1 各层测试命令

| 层 | 命令 | 覆盖 |
|---|---|---|
| kb-service 单测 | `cd repo/services/kb-service && python -m pytest app/tests -x -q`（**显式运行，不用全部测试**） | 各批次 §x.8 测试要点 |
| 迁移 | pytest 同上（迁移文件幂等断言） | 005/006/007 重复执行 |
| Gateway | `cd repo/services/ani-gateway && go test ./internal/router/... -count=1` | fake client + 各新 handler |
| 契约对账 | `make validate-services` + `make validate-architecture` | spec↔代码全量一致 + baseline 收敛 |

### 8.2 批次验收门（每批 PR 必须全绿）

1. pytest 新增用例全过（各批次测试要点清单）。
2. `make validate-services`：25 端点 spec↔proto↔路由三层全一致。
3. `make validate-architecture`：本批应清理的 baseline 条目清零，无新误报。
4. `make gen-proto` 生成物无手改残留（`git diff --stat` 仅生成目录）。

### 8.3 端到端冒烟（B7 合入后）

```
创建 KB → 上传文档 → PUT config 改 embedding_model
  → 断言 200 + rebuild_task → 轮询 async-tasks 到 succeeded
  → 断言 KB status=active → GET audit-logs 断言 kb.config.update + kb.rebuild 两条
```

---

## 9. 实施顺序与批次清单

| 批次 | 内容 | 新迁移 | 新 proto RPC | 前置 | 清 baseline |
|---|---|---|---|---|---|
| **B4** | #20 PUT permissions + #19 GET permissions（已合入 commit 42e73385；kb.permissions.update 埋点由 B8 回补，见 §6.8 偏差 15） | 005_kb_permissions.sql | GetKBPermissions | 无（与 B5 并行） | — |
| **B5** | #22 GET config + #25 GET models + retrieval_mode 契约修正 | 006_kb_ocr_enabled.sql | GetKBConfig / ListKBModels | 无 | 2 条（L56-61、L74-79） |
| **B6** | #24 POST rebuild + 互斥矩阵 + 埋点 | —（无新表） | RebuildKB | B5 | 1 条（L68-73） |
| **B7** | #23 PUT config + rebuild 联动 + 埋点 | — | UpdateKBConfig | B6 | 1 条（L62-67）→ **归零** |
| **B8** | #21 GET audit-logs + 存量埋点补齐（**已提前实施**于 B5/B6/B7 之前，工作区交付；见 §6.8 偏差 15） | 006_kb_audit_log.sql（B5 未实施、006_kb_ocr_enabled.sql 未创建，007→006 顺延，见 §6.8 偏差 1） | ListKBAuditLogs | B4（埋点契约） | — |

- 每批一个 PR，对齐 B1/B2/B3 的 SPEC 惯例（Frozen Facts → 实现 → 测试 → 验收命令）。
- B4/B5 可同周并行开工；B6→B7 串行；B8 收尾。实际执行顺序偏离：B8 已先于 B5/B6/B7 交付（应用户需求），B4 埋点由 B8 回补，见 §6.8。

---

## 10. 风险与开放问题

### 10.1 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| rebuild 大 KB 长任务 | consumer 单协程跑数千文档，耗时可能数小时 | 本批不做并发（Karpathy 原则：先简单正确）；进度可见 + 失败不中断已够用；并发化登记后续优化 |
| rebuilding 互斥漏网 | 某写路径未加状态校验 → 数据竞争 | §4.6 矩阵 + 各 servicer 测试逐操作断言；validate-services 不覆盖运行时，靠单测兜底 |
| ocr_enabled 迁移锁表 | knowledge_bases 是热表 | `ADD COLUMN ... DEFAULT FALSE`（PG11+ 不重写表，仅短暂锁） |
| 审计写入放大 | 每写操作 +1 insert | 同事务零额外往返；数据量小；TTL 清理登记 Q6 |
| ListKBModels 依赖 Core 可用性 | Core 不可达 → models 500 | core_api client 既有超时/降级：内置 bge-m3 恒在保证非空响应 |

### 10.2 开放问题（Q 编号，正文已引用）

- **Q1（B6 §4.6 已决策）**：rebuilding 期间 query 是否拦截 → **不拦截**。理由：旧向量仍可检索，读可用性优先；rebuild 完成后新数据生效。
- **Q2（范围外，§0.3）**：查询侧权限执行闭环（list/get/query 按 public_read/allowed_user_ids 过滤）——B4 只交付管理面读写；执行闭环需 proto 请求补 user_id 链路，影响面大，留待后续批次独立设计。
- **Q3（B4 §2.6）**：allowed_user_ids 为裸 uuid 数组，前端展示用户名需另行批量查询用户服务——是否在 KBPermissions 响应中冗余展示名？**默认不冗余**（YAGNI），需要时前端调用户服务。
- **Q4（B6 §4.4）**：rebuild consumer 崩溃后任务卡 running——复用既有 `revive_task_in_tx` 后台机制，本批不新建巡检；若实测发现复活不及时再补。
- **Q5（B5 §3.2，范围外登记）**：chunk_size 默认值漂移（proto 注释 1024 vs DB 默认 512）——与 #19~#25 无关，单独小 PR 修正注释或对齐默认值。
- **Q6（B8 §6.2）**：kb_audit_log 是否需要 TTL 清理任务——数据量小先不做，观察增长后再议。

### 10.3 与既有文档的一致性声明

- 本方案 §2.3/§6.2 的 DDL 与上游 [kb-api-completion-plan.md](./kb-api-completion-plan.md) §1.3/§2.7 存在字段差异（kb_audit_log 择一裁定见 §6.2），以本方案为准；上游文档不再单独修订。
- retrieval_mode 契约修正（§1.5）依据「接口未发布、无兼容负担」——若 B5 合入前该接口已对外发布，需重启兼容性评估。
- B8 已实施（提前于 B5/B6/B7，工作区交付）：实施与本方案的偏差以 §6.8 登记的实际实现为准（迁移编号 006、响应扁平 next_cursor、失败口径四码、双 PERMISSIVE RLS 策略、actor 键 `x-user-id` 等 16 项）。

---

> **结语**：本方案落地后，KB 域 25 个端点三层全通（OpenAPI ↔ proto ↔ Gateway ↔ kb-service），`services-route-baseline.yaml` KB 相关 `spec_not_in_code` 归零，B1~B8 累计覆盖上游 plan 全部 P0+P1 清单。
