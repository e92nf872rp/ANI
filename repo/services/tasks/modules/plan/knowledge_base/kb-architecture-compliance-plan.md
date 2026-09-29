# 知识库管理架构合规改造方案（实施计划）

> 关联：ANI Services 知识库管理（kb-service / rag-engine / ani-gateway）
> 目标：使知识库管理部分符合 CLAUDE.md 强制架构边界（§3 Services 只能经 Core OpenAPI/SDK 调用 Core、禁止直连底层组件；§5 ports/adapters 组件边界；Services 业务资源只进 services/v1.yaml）。
> 状态：规划稿（未实现）
> 审查依据：CLAUDE.md、repo/api/openapi/v1.yaml、repo/api/openapi/services/v1.yaml、validate_component_imports.py、三个服务的知识库实现
>
> ## 关键架构事实（本方案的落点依据）
> - **ani-core 是外部平台内核服务（不在本 repo 内）**；ani-gateway 是其对外统一入口，对外暴露 `https://{host}/api/v1`（v1.yaml）：servers[0].url 前缀规范），Core 资源全部经 gateway 的 `/api/v1` 转发。
> - kb-service 走 Core OpenAPI 时即调 `ANI_GATEWAY_INTERNAL_URL + /api/v1`（[config.py:20-21,43-44](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L20-L44)），故"调 Core"在实际拓扑中等价于经 ani-gateway。
> - **Core 契约 v1.yaml 已具备的能力**：对象存储（`/buckets`、`/buckets/{id}/objects`、upload、presigned-url，`/objects/upload`、`/objects/{id}/download`）、向量存储（`/vector-stores` 含 search/rebuild-index/documents）、异步任务（`/tasks/{id}`）、邮件通知（`/notifications/email/*`）、密钥（`/secrets`）。
> - **Core 契约目前缺失的能力**：事件/消息总线（outbox 所需）、通用缓存/会话、通用数据托管。这三类是"Core 需新增"的核心；其余改动仅需在 Services 侧收口到已有 Core 能力。

---

## 0. 合规基线（现状正确，无需改动）

- **kb-service 经 Core OpenAPI REST 调 Core**：`core_api/client.py` 走 `/vector-stores`、`/objects`、`/buckets`（CreateKB/DeleteKB/DeleteDocument 经 Core API 承载向量集合，未直连 Milvus）——这是应推广的正确范式。
- **knowledge-bases 作为 Services 业务资源**：定义在 `services/v1.yaml`，由 gateway 在 `/api/v1/svc` 前缀转发，未回流 Core API；kb-service 不暴露业务 REST。
- **gateway 的 vector-store/object-store 走 pkg/ports + pkg/adapters**，未直接 import minio/milvus SDK。

---

## 1. 改动项清单（每项含改动说明与 Core 改动）

### P0 — 组件直连收口（合规核心，改动大）

#### 改动 1：kb-service 业务数据访问收口（可读/写/建表）

**改动说明**：kb-service 当前在 [repositories/knowledge_base.py:38](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/repositories/knowledge_base.py#L38) 等多个 repository 用 asyncpg 直连 PG，自建表并读写 `knowledge_bases`/`kb_documents`/`kb_chunks`/`kb_messages`/`kb_sessions`/`async_tasks`/`outbox_events`，且自研 RLS（[rls.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/repositories/rls.py)）。这违反 §3「Services 禁止直连底层组件（PG 为底层中间件之一）」。改造目标是消除 kb-service 对 PG 的直接耦合，数据面经明确边界访问。

**方案（二选一，需在决策点 D1 拍板）**：
- **方案 A（保守，推荐先落地）**：表仍归 kb-service 拥有，但把 asyncpg 直连封装下沉为 db-adapters，纳入架构守卫 allowlist（`coupling_level=adapter_with_extensions`）+ 理由 + `migrate_by`；RLS 收敛为基于 Core 租户隔离的明确实现或显式豁免。改动量小、不阻塞业务。
- **方案 B（彻底）**：迁移为 Core 托管数据 API——业务表由 Core 承载，kb-service/rag-engine 统一经 Core 数据面 API 读写。彻底消除直连，但周期长，且需 Core 新增数据面能力（见下方 Core 改动）。

**Core 如何改动**：
- 方案 A：Core 基本无需新增契约（仅可能复用现有 `/tenant`、`/secrets` 承载租户与连接信息）；改动集中在 Services 侧守卫与 repository 重构。
- 方案 B：需在 v1.yaml（Core 侧）新增统一的**数据面**契约，例如 `POST /data/rows`、`GET /data/tables/{table}?filter=`、`PATCH /data/rows/{id}`、`DELETE /data/rows/{id}`，及配套 RBAC scope（`scope:data:read/write`）、分页、租户隔离语义（tenant_id 从 JWT 提取），并落地 Core handler + 适配器（底层仍由 Core 连 PG）。SDK 再生成后 kb-service 改走该 API。

**涉及文件**：`kb-service/app/repositories/*`、`app/api/grpc_server.py`、`app/core/config.py`、`requirements.txt`、`migrations/*`；方案 B 另加 `api/openapi/v1.yaml`、Core handler/adapters、SDK。
**时间**：方案 A **2.0 人·日**；方案 B **另加 2.0 人·日**（Core 新增数据面）。

#### 改动 2：kb-service 消息通道（NATS）收口

**改动说明**：kb-service 在 [outbox/dispatcher.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/outbox/dispatcher.py) / [main.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/main.py) 直连 NATS 发布 `ani.tasks.kb.parse`（[config.py:30-31](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L30-L31)），违反 §3。改造为：kb-service 将"待解析文档"写入 outbox 表，经 Core 事件/消息 API 发布，rab-engine 经同一 Core 事件通道消费；移除 `nats_url` 直连与 `nats-py` 依赖。可复用 Core 现有的 outbox 模式（task-service 已有 `outbox_publisher.go` 范式）。
**Core 如何改动**：Core 需在 v1.yaml 新增**事件/消息总线**契约，例如 `POST /events`（发布）、`GET /events/consume?group=`（消费/游标）、`POST /events/subscriptions`（订阅管理），配套 `scope:events:publish/consume`。底层由 Core 连接 NATS，把 NATS 细节从 Services 侧彻底隐藏。SDK 再生成。
**涉及文件**：`kb-service/app/outbox/dispatcher.py`、`main.py`、`app/core/config.py`、`requirements.txt`；`api/openapi/v1.yaml`、Core handler/adapters、SDK。
**时间**：**0.8 人·日**（Services）+ **1.5 人·日**（Core 事件能力）。
**依赖**：Core 事件能力（改动 7 之一）。

#### 改动 3：kb-service 会话缓存（Redis）收口

**改动说明**：kb-service 在 [session/cache.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/session/cache.py)、[grpc_server.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/api/grpc_server.py)、[main.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/main.py) 直连 Redis（[config.py:34](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L34)）。改造为经 Core 缓存/会话能力读写，移除 `redis_url` 与 `redis` SDK。
**Core 如何改动**：Core 需在 v1.yaml 新增**缓存/会话**契约，例如 `PUT /cache/{key}`、`GET /cache/{key}`、`DELETE /cache/{key}`、`POST /cache/{key}/ttl`，带 `scope:cache:read/write` 与过期时间语义；底层由 Core 连接 Redis。SDK 再生成。
**涉及文件**：`kb-service/app/session/cache.py`、`app/api/grpc_server.py`、`main.py`、`app/core/config.py`、`requirements.txt`；`api/openapi/v1.yaml`、Core handler/adapters、SDK。
**时间**：**0.6 人·日**（Services）+ **1.0 人·日**（Core 缓存能力）。
**依赖**：Core 缓存能力（改动 7 之一）。

#### 改动 4：rag-engine 组件直连收口（最重）

**改动说明**：rag-engine 是直连最密集的服务——
- [core/milvus.py:20,52-57](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/app/core/milvus.py#L20-L57) 直连 Milvus（`pymilvus.connections` + `MilvusVectorStore`）；
- [clients/minio_client.py:13-26,30-77](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/app/clients/minio_client.py#L13-L77) 直连 MinIO 并自建桶 + 设**匿名读策略**；
- [workers/parse_worker.py](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/app/workers/parse_worker.py) 直连 NATS + asyncpg 跨界直写 kb-service 的表；
- [services/qa_service.py:104-115](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/app/services/qa_service.py#L104-L115) 直连 Redis 与 vLLM（`OpenAILike(api_base=vllm_api_base)`）；
- `repositories/chunks.py` 直接 `INSERT INTO kb_chunks`（该表归属 kb-service，跨界直写）；
- [main.py:144-145](file:///c:/Users/PC/Desktop/ANI/repo/ai/rag-engine/main.py#L144-L145) 自暴露 `/api/v1/kb` 业务 REST，绕过 gateway 统一转发与鉴权。

改造为全部经 Core/Services API 收口，详见下列 6 个子项及"Core 如何改动"。

**要做的（6 子项）**：
1. **Milvus 收口**：检索/写入改经 Core `/vector-stores`（复用 kb-service [core_api/client.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core_api/client.py) 范式），非直连 `pymilvus`。
   - Core：**无需新增**，`/vector-stores` 及 `/vector-stores/{id}/search`、`/documents` 已具备；仅在 SDK 侧确保暴露 search/write 方法。
2. **MinIO 收口**：改经 Core `/objects/upload`、`/objects/{id}/download`、`/buckets` 管理桶与策略，移除 `minio` SDK 与匿名读策略直设。
   - Core：**无需新增**，对象存储契约已具备；需确认 Core 支持桶级匿名读策略（否则补齐 `PUT /buckets/{id}/acl` 的 policy 能力，见改动 7）。
3. **解析任务/状态收口**：解析任务触发改经 Core 事件通道（同改动 2），解析状态与 `kb_chunks` 写入改为经 kb-service/Core 数据 API，**消除跨界直写 kb-service 的表**。
   - Core：依赖改动 2（事件）、改动 1 方案 B（数据面）或 kb-service 提供的 authorized 数据 API；需在 Core 确认/补齐对应契约。
4. **Redis 会话缓存收口**：经 Core 缓存能力。
   - Core：依赖改动 3 的缓存契约（改动 7 之一）。
5. **AI 推理收口**：embedding/LLM/OCR 默认经 Core 网关下行（若 Core 已承载 `model`/`inference` 能力，参考 `/model` 资源）或明确为 allowlist 豁免（决策点 D3）。
   - Core：若走收口，Core 需在契约上对 rag-engine 暴露模型推理/OCR 端点（参考 gateway 已有 [model_resources.go](file:///c:/Users/PC/Desktop/ANI/repo/services/ani-gateway/internal/router/model_resources.go)、[inference_resources.go](file:///c:/Users/PC/Desktop/ANI/repo/services/ani-gateway/internal/router/inference_resources.go) 的转发；Core 侧确认 v1.yaml 的模型/推理契约）。
6. **自暴露 REST/gRPC 收口**：收敛 `/api/v1/kb`，业务入口统一由 gateway 以 `services/v1.yaml` 为准转发，rag-engine 不再直接对外暴露业务资源。
   - Core：本子项无需 Core 改动，属 gateway/Services 边界规整。

**涉及文件**：`rag-engine/app/core/milvus.py`、`clients/minio_client.py`、`workers/parse_worker.py`、`repositories/chunks.py`、`routers/query.py`、`services/*`、`core/config.py`、`main.py`、`requirements.txt`；部分子项涉及 `api/openapi/v1.yaml` + SDK。
**时间**：**3.0 人·日**（Services）+ 依赖改动 1/2/3 的 Core 新增能力。
**依赖**：改动 1/2/3、改动 7。

---

### P1 — 契约与守卫对齐

#### 改动 5：为 Python 服务补齐组件边界守卫

**改动说明**：现有 [scripts/validate_component_imports.py:135](file:///c:/Users/PC/Desktop/ANI/repo/scripts/validate_component_imports.py#L135) 仅 `rglob("*.go")`，kb-service/rag-engine 的组件直连完全无门禁。改造为：
1. 扩展守卫覆盖 `*.py`：解析 `requirements.txt` + import AST，识别 minio/pymilvus/nats-py/redis/asyncpg 等底层 SDK 的直接 import。
2. 建立 allowlist 机制（`coupling_level` + `migrate_by` + `reason`），与 Go 守卫语义对齐；为改动 1/2/3/4 收敛前的存量直连登记过渡豁免。
3. 纳入 `make validate-architecture` 与 `make validate-services`。
- **Core：无需改动**（纯 Services 侧脚本与 allowlist）。
**涉及文件**：`scripts/validate_component_imports.py`、`architecture/`allowlist、`Makefile`。
**时间**：**1.0 人·日**。

#### 改动 6：gateway 契约对齐

**改动说明**：gateway [kb_resources.go:35](file:///c:/Users/PC/Desktop/ANI/repo/services/ani-gateway/internal/router/kb_resources.go#L35) 的 notify-uploaded 把 `doc_id` 放进 path（`/.../documents/{doc_id}/notify-uploaded`），与 [services/v1.yaml:1068](file:///c:/Users/PC/Desktop/ANI/repo/api/openapi/services/v1.yaml#L1068) 的不带 `doc_id` 的 `?.../notify-uploaded` 不一致；另 reparse/config/rebuild/models 四个已声明路径未在 gateway 注册。二者必须收敛，且与 Core 侧实际转发一致。方案二选一（决策点 D2）：修正 gateway 路径与 v1.yaml 强制一致，或调整 v1.yaml 与 gateway 现状一致；并补齐/收缩 4 个未注册路径。
- **Core 如何改动**：本项落在 Services 契约（services/v1.yaml）与 gateway，**Core 无需改动**；但需确保聚类对齐后与 Core 实际下游行为一致（走哪个下游端点），防止 Services 契约改后与 Core 转发失配。
**涉及文件**：`services/ani-gateway/internal/router/kb_resources.go`、`api/openapi/services/v1.yaml`、`validate_services_route_contract.py` 对应测试。
**时间**：**0.5 人·日**。

---

### P2 — 架构留痕

#### 改动 7：新增/确定 Core 承载能力的契约端点（供 P0 依赖）

**改动说明**：P0 改动 1/2/3/4 里几项依赖 Core 尚未具备的通用能力。需在 **Core 契约（v1.yaml）**补三类端点，统一走 gateway 的 `/api/v1` 转发，并产出对应 SDK；全部过 `validate-services`。
- **Core 如何改动（三类新增能力）**：
  1. **事件/消息总线**：`POST /events`、`GET /events/consume?group=`、`POST /events/subscriptions` + `scope:events:*`（供改动 2、改动 4-3）。底层接 NATS。
  2. **缓存/会话**：`PUT/GET/DELETE /cache/{key}`、`POST /cache/{key}/ttl` + `scope:cache:*`（供改动 3、改动 4-4）。底层接 Redis。
  3. **数据托管（仅方案 B 需要）**：数据面 CRUD 契约（供改动 1 方案 B、改动 4-3 的 kb_chunks 落库）。另确认 `PUT /buckets/{id}/acl` 是否已表达桶级匿名策略（供改动 4-2）。
  - 每类需在 Core 落地 handler + adapter，注册 `x-ani-rbac-scope`，纳入 SDK 生成 `make gen-core-sdk`。
**涉及文件**：`api/openapi/v1.yaml`、Core handler/adapters、`api/openapi/services/v1.yaml`（若涉及 Services 侧引用）、SDK。
**时间**：事件 **1.5 人·日**、缓存 **1.0 人·日**、数据面 **2.0 人·日**（按需）。

#### 改动 8：gateway→kb-service gRPC / rag-engine / vLLM 直连显式豁免留痕

**改动说明**：gateway 经 gRPC 调 kb-service（[kb_grpc_client.go](file:///c:/Users/PC/Desktop/ANI/repo/services/ani-gateway/internal/router/kb_grpc_client.go)）、直连 rag-engine/vLLM，这些跨层/跨服务通道未以 allowlist/coupling_level 声明。改造为：在架构 allowlist/豁免清单中显式声明这些既有通道，附理由与迁移计划；更新 `ANI-13` 或 services 团队 guide。
- **Core：无需改动**（架构留痕）。
**涉及文件**：`architecture/*.yaml`（allowlist）、`repo/CURRENT-SPRINT.md`、`repo/development-records/*`。
**时间**：**0.3 人·日**。

---

## 2. 关键决策点（需人工拍板）

- **D1（改动 1）**：kb-service 业务表走"仍由 kb-service 直连 PG、纳入守卫收敛"（方案 A，快、保守），还是"迁移为 Core 托管数据 API"（方案 B，彻底但需 Core 新增数据面、周期长）？
- **D2（改动 6）**：notify-uploaded 契约以"gateway 现状为准改 v1.yaml"，还是"v1.yaml 为准改 gateway"？（二者必须收敛，且不能与 Core 下游转发失配。）
- **D3（改动 4 子项 5）**：AI 推理（embedding/LLM/OCR）是否在本次一并收口到 Core 网关，还是先以 allowlist 豁免、后续批次处理？

---

## 3. 汇总时间表

| 改动 | 项 | Services（人·日） | Core（人·日） | 依赖 |
|---|---|---|---|---|
| 1 | kb-service 业务数据收口 | 2.0 | 0（A）/2.0（B 数据面） | — |
| 2 | kb-service NATS 收口 | 0.8 | 1.5（事件） | 改动 7 |
| 3 | kb-service Redis 收口 | 0.6 | 1.0（缓存） | 改动 7 |
| 4 | rag-engine 组件收口 | 3.0 | 视子项（复用 1/2/3） | 1/2/3、7 |
| 5 | Python 组件守卫 | 1.0 | 0 | — |
| 6 | gateway 契约对齐 | 0.5 | 0 | — |
| 7 | Core 契约端点（事件/缓存/数据面） | 0 | 1.5/1.0/2.0 | — |
| 8 | 架构豁免留痕 | 0.3 | 0 | — |
| **合计** | | **约 8.2** | **约 2.5–6.5** | |

> 说明：时间含编码+测试+通过 `make validate-services`/`make validate-architecture`/`git diff --check`；不含跨批次 PR 评审与合并（ANI 采用受控并行 PR，main 受保护串行收口）。Core 侧时间取决于 D1 是否选方案 B、D3 是否一并收口 AI 推理。
