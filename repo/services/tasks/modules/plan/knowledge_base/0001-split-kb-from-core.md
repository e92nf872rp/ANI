# ADR 0001: 将 kb-service 从 Core 中拆出（rev2）

- 状态: Proposed（rev2 重构稿，待评审；替代 rev1 的全部阶段结论）
- 日期: 2026-09-20（rev1: 2026-09-18）
- 范围: kb-service（含其对 rag-engine 的编排关系）从 Core 共享基础设施与 monorepo 中解耦，独立维护
- 依据: 全部结论来自当前代码实证（kb-service Python 源码、各 Go 服务源码、migrations DDL）与本轮评审新输入；不参考、不受约束于任何更早的规划文档

> **状态维护注（KB-SPLIT-P0，2026-09-21）：** 本 ADR 已按 §五-阶段 0 启动执行：基线快照 commit `68bf198f`，文档结构建于 `repo/services/kb-service/docs/`。**执行期唯一权威文本为 `repo/services/kb-service/docs/adr/0001-split-kb-from-core.md`**（与本文件纳入时逐字一致）；本文此后仅作规划期历史稿，不再随执行修订，差异清单与执行状态见 `repo/services/kb-service/docs/execution/status.md`。ADR 状态变更（Proposed → Accepted）由 #070 收尾批次统一执行。

***

## rev2 修订说明

评审对 rev1 的四个点给出新输入，本版针对性重构：

| # | 裁定点 | rev1 立场 | rev2 变化 |
| - | ------ | --------- | --------- |
| D1 | 向量存储与对象存储的使用方式 | 默认沿用 Core API 并固化契约 | **已裁定：向量与对象均由 kb 自管**（向量直连独立 Milvus；对象 kb 自有 bucket + 预签名直传），取用全部内网直连，不经 Core 中转 |
| D2 | 租户与身份 | 作为契约项保留 | 明确暂缓：拆分期零改动原样保留，仅收敛边界留调整钩子 |
| D3 | Core 契约策略 | pin commit + 契约测试，API 视为固定不变 | D1 裁定后 kb 不再调用 Core REST，Core 契约面为零；未来若出现新 Core 依赖按 RFC 演进 |
| D4 | C 层共享表归属 | (a) kb 建专属表 / (b) 保留共享，二选一待跨团队裁定 | 新输入"task 域也将拆分"→ 第三条路：outbox 归各服务自持、async_tasks 归未来 task 域、kb 幂等本地化，无需再等归属裁定 |

另按评审二轮意见：**删除全部过渡性设计（停写窗口、数据迁移、存量移交、停用序列、回滚预案、观察期）——无历史负担，首发即目标态，回滚 = 代码回退。**

***

## 一、领域界定

先定领域，再定拆法。领域界定以服务当前实际承担的职责和实际读写的数据为准。

### 1.1 领域使命

面向租户的知识全生命周期管理，三条主线：

- **知识摄入**：文档上传（预签名直传 kb 自管对象存储）、解析编排、图片产物入库、分块、摘要、向量化、索引写入
- **知识组织**：知识库实例与文档的元数据管理、状态推进、权限、审计
- **知识消费**：混合检索（向量 + pg\_trgm 关键词 + RRF 融合）、多轮问答（含流式生成）、会话管理

### 1.2 领域数据的三层归属（代码实证）

以 `app/repositories/*.py` 实际读写的表为准，按 DDL 所有权与写入方分为三层：

**A 层：kb 独有，DDL 已在 kb migrations（`migrations/001–007`）**

| 表              | DDL 位置                 | 要点                                                                       |
| --------------- | ------------------------ | -------------------------------------------------------------------------- |
| kb\_chunks      | `002_kb_chunks.sql`      | parent-child 分块；`(kb_id, doc_id)`、`parent_chunk_id` 索引 + pg\_trgm GIN；RLS |
| kb\_permissions | `005_kb_permissions.sql` | KB 级授权；RLS                                                                |
| kb\_audit\_log  | `006_kb_audit_log.sql`   | 只增审计；RLS                                                                  |

**B 层：kb 读写、DDL 在共享库（`repo/deploy/migrations/20260501000100_init_schema.sql`）、无其他服务写入方 → 拆分时纳入 kb 基线**

| 表                | kb 侧代码证据                                 | 要点                                                                        |
| ---------------- | ---------------------------------------- | ------------------------------------------------------------------------- |
| knowledge\_bases | kb migrations 003/004/007 仅 ALTER        | 聚合根；`tenant_id REFERENCES tenants(id) ON DELETE CASCADE`（跨库 FK 断裂点）       |
| kb\_documents    | `app/repositories/document.py`           | 文档状态机权威                                                                   |
| kb\_sessions     | `app/repositories/message.py`（INSERT/查询） | 会话元数据，kb 读写                                                               |
| kb\_messages     | `app/repositories/message.py`            | 会话消息，`tenant_id` 冗余在行上直接做隔离                                               |

**C 层：kb 读写、多领域共享 → 拆分时按 D4 处置**

| 表              | kb 侧证据                                                                                                     | 其他写入方（代码实证）                                                                                                                           | rev2 处置                                       |
| -------------- | ---------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| async\_tasks   | `app/repositories/async_task.py`；grpc\_server.py 幂等回放（CreateKB/NotifyDocumentUploaded/UpdateKB/Reparse 均写） | model-service `import_repo.go:82` INSERT；共享 `pkg/repo/task_repo.go:92` INSERT、`:390` SELECT                                           | **不接管**：归未来 task 域；kb 幂等本地化（见 D4）             |
| outbox\_events | `app/repositories/outbox.py`；`app/outbox/dispatcher.py` 轮询投递                                               | 共享 `pkg/adapters/runtime/outbox_writer.go:53` INSERT；`pkg/repo/task_repo.go:116/:436` INSERT；model-service `import_repo.go:91` INSERT | **kb 自建自己的 outbox 表**：outbox 是模式性资产，正确形态即每服务一张（见 D4） |

**核心资源状态机**（代码实证，拆分原样保留）：

- KB：created → active → deleting → deleted（CreateKB/DeleteKB RPC，`vector_store_name = "kb_{kb_id}"` 派生，[grpc\_server.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/api/grpc_server.py#L117)）
- Document：pending → parsing → indexing → ready | failed（[parse\_orchestrator.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/services/parse_orchestrator.py#L52)，异常落 failed + 脱敏 error\_message）
- 解析幂等：`parse-{doc_id}-{run_id}-b{batch_no}`，重跑生成新 run\_id 避免撞幂等键；重解析前先删旧 kb\_chunks 行与旧向量（无 ON CONFLICT，靠显式清理）
- Outbox：pending → published，at-least-once 投递
- 会话：Redis `ani:prod:session:kb:{session_id}`（TTL 24h，LTRIM 保留最近 20 条）+ DB 双写；Redis 是 best-effort 降级层，挂掉时 Query 走 DB-only

### 1.3 运行形态（代码实证的单进程架构，除标注外拆分原样保住）

（[main.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/main.py)）

- **双栈单进程**：uvicorn/FastAPI :8002（health/readyz/debug + 后台任务）+ gRPC :50053（全部业务 RPC）
- **双事件循环**：gRPC loop（servicer + DB pool + Redis 绑定）与 uvicorn loop（outbox dispatcher + consumers）相互独立；asyncpg 连接绑创建时的 loop，跨 loop 复用会报错——解析路径与查询路径各自持有独立的 `RagEngineGRPCClient` 实例
- **双 asyncpg pool**：均 `command_timeout=30s`（防 LB 静默断连）与获取超时 10s；消费者并发上限 4，pool 容量需覆盖 worst case（4 消费者 + 1 dispatcher）
- **NATS 走 JetStream**（[jetstream.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/consumers/jetstream.py)）：ANI\_TASKS stream（WorkQueue retention、file 存储、24h MaxAge）、durable push consumer、ManualAck、AckWait 30m、MaxDeliver 3、InProgress 心跳（ack\_wait/3）；ack 语义：handler 成功→Ack、poison pill→Ack 吞掉（outbox 行是运维事实源）、handler crash→不 Ack 不 Nak（静默等 ack\_wait 重投）、运行中→InProgress 续租
- **subject（目标态）**：`ani.tasks.kb.parse.v2`（kb 自有消费者路径）+ `ani.tasks.kb.rebuild.v1`（整库重建，长串行任务独立 subject 防交错）；v1 subject `ani.tasks.kb.parse` **不带入**
- **consumer 常开**：原 flag 门控（默认 OFF + OFF 时发 v1）逻辑**不带入**，v2 为唯一解析路径
- **启动顺序**：消费者先订阅、dispatcher 后启动（反序会丢启动积压）；停机逆序
- **降级语义（设计内行为，拆分后原样保留）**：NATS 挂→事件积压 outbox 延迟投递；Redis 挂→会话走 DB-only；两者都不使进程不健康
- **/readyz**：ok = db pool + outbox dispatcher + gRPC server；session\_cache / consumers 只上报不门控

### 1.4 rag-engine 在领域内的定位

- 无状态计算引擎，RPC 面：Parse / Embed / Generate / GenerateStream（[client.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/rag_engine/client.py)），EmbedResponse 用扁平 float 数组传输
- 不持久化业务数据、不拥有资源状态机、不直接访问业务数据库
- 依赖方向单向 kb-service → rag-engine（gRPC :50052），禁止回调；rag-engine 的 REST 端点不得反向调用 kb-service
- **v1 不带入**：现状 rag-engine 的 parse\_worker 消费 `ani.tasks.kb.parse` 并直接操作 PG/向量库——拆分后该路径直接删除（rag-engine 侧同步删代码），kb 是唯一编排者与状态权威，rag-engine 彻底无状态化
- 能力合同：embedding 输入 480 字符截断、摘要 ≤460 字符硬截断、子块 [64,256] token；`embedding_model`/`embedding_dim` 为共享环境键（默认 `BAAI/bge-m3` / 1024），阶段 3 配置脱钩时改为 kb 专属命名并显式声明来源
- kb 编排职责（[parse\_orchestrator.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/services/parse_orchestrator.py)）：取文档内网 URL → Parse → 图片直连上传 + 链接嵌入 → 重解析清旧块/旧向量 → 索引 → best-effort 摘要（前 3 父块，失败不阻塞）→ Embed → 向量直连写入 → kb\_chunks 写入 → ready | failed
- 查询编排（[query\_orchestrator.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/services/query_orchestrator.py)）：Retrieve → 三道无结果闸门（检索空 / 最高分 < 阈值 / 去重后 sources 空，前两道不调 LLM）→ Generate（StreamToken/Sources/Done/NoResult）；三模式 hybrid/vector/keyword，RRF K=60，parent 回填 + 去重

### 1.5 领域边界（明确不做）

- **不做跨领域通用文件管理**：kb 只管理自己领域的对象（文档正本 + 解析产物，见 D1），其他领域的文件不归 kb、也不向其他领域提供文件服务
- 不做身份与权限模型（D2 暂缓项）
- 不做任务中心、通知、计量账本（各自领域拥有，kb 只发事件；任务进度对外口径见 D4）
- 不新建共享 ANI 业务 runtime；不引入 LlamaIndex 等已被移除的依赖（RRF 为纯 Python 实现）
- **拆分期间不做引擎更换**：向量库平移不换型（pgvector 收敛评估放演进期，见 D1 与阶段 6）

### 1.6 已确认的外部事实（降低切换成本的正面证据）

- Gateway 对 KB 资源已是纯 gRPC 代理（`ani-gateway/internal/router/kb_resources.go` 经 `kb_grpc_client.go` 转发，含 SSE 流转换 `kb_sse.go`），不直连 kb 表——Gateway 路由切换只需改指向
- Gateway 的 task 资源路由（`task_resources.go`）依赖 async\_tasks——按 D4 处置：kb 不再写共享表，任务展示归 task 域
- MinIO 已有双视角访问先例：上传走 NodePort 对外地址、内部 pod 取用走 ClusterIP——D1 自管后由 kb 自主签发两类 URL，先例直接继承

***

## 二、四个裁定点（rev2 核心）

### D1: 向量存储与对象存储——已裁定：kb 自管

**裁定（2026-09-20）**：向量与对象均由 kb 自管。取用全部内网直连（pod → ClusterIP），不经 Core 中转、不依赖 Core 签发的 URL，消除"取用绕外部地址/公网"的问题。

**归属判据**（支撑裁定）：资源归谁看两点——数据由谁生成（写入方）、生命周期与谁的领域对象绑定。

| 资源       | 生成方                                                     | 生命周期绑定                                      | 判定       |
| ---------- | -------------------------------------------------------- | --------------------------------------------- | ---------- |
| 向量数据     | kb 编排 embedding 后写入（Core 原只是带租户登记的 CRUD 代理） | 与 KB/Document 1:1（`kb_{kb_id}` collection）     | **kb 自管** |
| 文档正本对象 | 外部上传（经 kb 预签名直传进 kb bucket）                     | 与 kb\_documents 行 1:1                        | **kb 自管** |
| 解析产物图片 | kb 解析管线生成，key `{kb_id}/{doc_id}/images/{uuid}.{ext}` | 与文档解析结果绑定，kb 唯一读写方              | **kb 自管** |

**向量自管**：

- 独立 Milvus 实例，kb 直连（pymilvus 进 kb 进程）insert/search/delete；CreateKB/DeleteKB 直接管 collection（`kb_{kb_id}`），随建随删，无存量迁移
- vector-stores / knowledge-base-link / 预计算向量插入等 Core 端点不再出现
- rag-engine 保持无状态，不受影响；选 Milvus 而非 pgvector：拆分期遵守"平移优先、不做引擎更换"纪律（换向量引擎放演进期评估）
- 代价：kb 承接 Milvus 运维（监控/备份/容量；现网 etcd 配额与 compaction 教训写入运维手册继承）

**对象自管 + 预签名直传**：

- kb 新建自有 bucket 承载正本与产物（无存量移交事项）；key 规则沿用设计（产物 `{kb_id}/{doc_id}/images/{uuid}.{ext}`）
- 上传流程：① 前端经 Gateway 请求 kb 签发预签名 PUT URL（对外视角 = MinIO NodePort 地址，时效受限、key 预定）→ ② 前端直传 MinIO → ③ 前端调 kb complete 端点，kb HeadObject 校验后登记 kb\_documents。NotifyDocumentUploaded 跨服务通知 RPC 不再存在
- 取用：解析时 kb 签发内网视角 URL（ClusterIP）给 rag-engine；markdown 占位 `[图片: caption](object_id)` 机制保留，object\_id 语义为 kb 自管对象标识（bucket/key）
- 删除：正本按 key 删、图片按 `{kb_id}/{doc_id}/images/` 前缀批量删——rev1 的"图片删除缺口"从设计上不存在
- 代价：kb 承担 bucket 配额/生命周期策略/备份；预签名 URL 时效与最小权限控制；前端上传逻辑需配合改造（从调 Core 上传端点改为 kb 签发直传）

**Core 依赖收缩结果**：objects 与 vector-stores 链路不再出现，[core\_api/client.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core_api/client.py) 不带入新仓库；kb 对 Core 仅剩租户上下文信任（在 D2 暂缓项内维持现状）。这是拆分的核心红利。

### D2: 租户与身份——暂缓项

- 拆分期**零改动**：`X-Tenant-Id` + `Authorization: Bearer` + dev 模式 `X-Dev-Tenant-ID`（仅限开发环境）原样保留。
- 只做边界收敛：入向租户上下文信任 Gateway 注入（阶段 1 的应用层租户校验建立在此之上）；kb 已无出向 Core 调用（D1），边界面天然收敛。
- 未来若身份机制调整（如服务间认证换型），只改边界层与租户校验实现，不动业务逻辑——有意留下的松耦合点，届时另立 ADR。
- 契约测试不针对具体 header 形态做过强断言，仅覆盖"租户上下文传递正确性"。

### D3: Core 契约——无调用即无契约

- D1 裁定后 kb 不再调用 Core REST（objects / vector-stores 均自管，bucket 由 kb 新建，无移交事项），**Core OpenAPI 契约与对应契约测试取消**。
- 若未来出现新的 Core 依赖，按 RFC 流程办理：kb 提案（端点、动机、兼容性影响）→ Core 评审 → 发新版本 → pin 升级。允许改：新增端点、废弃端点、语义修正；不允许：静默变更。
- 对 rag-engine proto（Parse/Embed/Generate/GenerateStream）与对上 gRPC 契约不受影响，照常 pin 版本。

### D4: C 层共享表——task 也将拆分，按资产性质处置

新输入："task 也会拆"。rev1 的 (a)/(b) 二选一作废：

1. **outbox\_events = 模式性资产，归各服务自持**。transactional outbox 的正确形态就是每服务一张自己的表 + 自己的 dispatcher。kb 基线建自己的 outbox 表（结构沿用现表设计，pending→published 状态机与 dispatcher 不变）。outbox 无归属争议，不需要跨团队裁定。
2. **async\_tasks = task 域资产，归未来 task-service**。kb 不接管、不写入共享表：
   - **幂等回放**（CreateKB/UpdateKB/Reparse 等的重复请求判定）——kb 内部语义，本地化：新建 `kb_operations`（`UNIQUE(tenant_id, idempotency_key)`，含 operation\_type/status/result），gRPC 回放读此表。
   - **任务进度对外可见**——kb 只发 outbox 事件；task-service（拆分后）订阅 kb 事件建任务，Gateway task 路由届时接 task-service。kb 同时提供 ListOperations/GetOperation gRPC（读 kb\_operations）作为自有查询接口，供 task-service 就绪前的进度查询。
3. **与 task 拆分的解耦接口**：kb 承诺的只有"outbox 事件 schema 稳定 + at-least-once 投递"；task-service 依赖事件而非 kb 的表。两次拆分互不阻塞。

***

## 三、拆分目标

1. **独立维护**：独立仓库、独立构建、独立版本、独立进程；Python 3.11 + FastAPI + gRPC，requirements 全钉死
2. **独立持久化与存储**：A+B 层表 + kb 自有 outbox + kb\_operations 纳入 kb 迁移基线，单一 DDL 权威；向量库（独立 Milvus）与对象 bucket 由 kb 自管；显式租户隔离（`tenant_id` 列保留 + 查询强制过滤），不依赖 RLS
3. **契约版本化且可演进**：对上（Gateway/前端）契约、对 rag-engine proto 契约固定版本引用；对 Core 无调用即无契约，未来新依赖走 RFC
4. **基线可追溯**：固定 monorepo 快照 commit，记录差异；拆分期间"平移优先、不做功能性重构"
5. **首发即目标态**：无历史包袱，不做迁移/停用/回滚预案；旧路径与旧依赖以"代码不带入"的方式消失

## 四、现状耦合盘点

| # | 耦合点               | 证据                                                                                          | 处置                                                     |
| - | -------------------- | --------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| 1 | 共享数据库           | `database_url` 默认连共享库（[config.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L17)） | 拆分即切：新代码直连 kb 专属库                            |
| 2 | 表 DDL 所有权分裂    | A 层 3 表在 kb migrations；B 层 4 表在共享库；C 层 2 表多写入方                                | B 层纳入基线；C 层按 D4（outbox 自建、async\_tasks 不接管）|
| 3 | 跨库 FK 断裂         | `knowledge_bases.tenant_id REFERENCES tenants(id) ON DELETE CASCADE`                          | 去 FK + 应用层租户校验 + CASCADE 语义改 kb 删除流程       |
| 4 | RLS + GUC 租户隔离   | [rls.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/repositories/rls.py) `SET LOCAL app.current_tenant_id` + platform\_bypass + `ani_app` 角色 | 显式过滤 + 复合外键 + 独立库角色体系重建                  |
| 5 | 共享 repo 根 `.env`  | [config.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core/config.py#L71) `env_file=".env"` 指向 monorepo 根 | 自建 `.env.example`；embedding 共享键改 kb 专属命名       |
| 6 | Core REST 调用面     | [core\_api/client.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core_api/client.py)（vector-stores + objects + link） | **不带入新仓库**（D1：向量与对象均 kb 自管）              |
| 7 | proto 生成物三份复制 | `app/generated/kb/v1/`、`app/generated/common/v1/`、`app/rag_engine/rag_pb2*`                 | 契约化：pin 版本统一分发                                  |
| 8 | 双解析路径并存       | flag `kb_parse_consumer_enabled` 门控 v1/v2                                                    | **不带入**：v2 为唯一路径，v1 代码与 flag 删除            |
| 9 | monorepo 构建/CI     | 构建与门禁挂 monorepo 统一入口                                                                | 拆仓时迁出                                                |
| 10 | 共享 async\_tasks 幂等回放 | grpc\_server.py 回放逻辑 + Gateway task 路由                                             | D4：kb\_operations 本地化；任务展示归 task 域（kb 只发事件）|

## 五、分阶段方案

### 阶段 0: 基线固定 + 协作排期

- 记录 monorepo 快照 commit 作为基线；冻结"拆分期间不做功能性重构"
- 建立文档结构：`docs/START-HERE.md`（唯一导航）、`docs/specs/`（行为规格）、`docs/adr/`（本文件为第一条）、`docs/execution/status.md`、`docs/execution/records/`
- **协作排期知会**：前端（上传改预签名直传）、Gateway（kb 路由改指向）、task 拆分相关方（kb outbox 事件 + 临时状态查询接口的聚合方式）
- 验收：基线 commit + 差异清单落档；现有单测全绿

### 阶段 1: 数据与存储基线（关键路径）

1. kb 自有 outbox 表 + `kb_operations` 表进入迁移基线（D4）
2. **B 层 DDL 纳入基线**：以 `app/repositories/*.py` 实际读写为准核对 4 表全部列、索引、约束、RLS 策略，含共享库后续迁移的增量 ALTER（`kb_name_unique_active`、`kb_sessions_kb_id_index`、`kb_embedding_model_full_name`、`kb_default_inference_service`）；重建完整 DDL，单一权威
3. 新建 kb 专属 PostgreSQL 实例；凭据最小权限分离（应用/运维）
4. **跨库 FK 处理**：去掉对 `tenants(id)` 的 FK 引用；租户存在性校验降级为应用层（CreateKB 时经 IAM/Core 验证）或信任 Gateway 传入的已认证租户上下文；`ON DELETE CASCADE` 语义改由 kb 自己的删除流程保证
5. **RLS → 显式租户隔离**：保留 `tenant_id` 列，删 GUC 依赖与 platform\_bypass 策略；每个 repository 查询显式过滤（RLS helper 退化为参数化过滤助手）；内部关系改复合外键 `(tenant_id, id)`；独立库角色体系重建（延续最小权限原则）；补跨租户负向测试（另一租户 GET/UPDATE 必须 404/空集）
6. SQL 集中管理：查询只留 `app/repositories/`（加门禁）；`migrations/` 是唯一 DDL 来源
7. **存储基线（D1）**：独立 Milvus 实例接入，collection 随 CreateKB 建立；kb 自管 bucket 新建与凭据配置；预签名签发/complete 端点 + 双视角 URL（NodePort 对外 / ClusterIP 对内）配置

验收：独立库全量单测 + 真实 PG 跨租户负向测试通过；证据标记 `pass / fail / not_verified`

### 阶段 2: 契约固化

- **对上 proto**（kb.v1 + common.v1）：归 kb 仓库；停止 `app/generated/` 散复制，改 pin 版本共享 proto 包；生成命令写进 docs
- **对 rag-engine proto**（Parse/Embed/Generate/GenerateStream）：共同 pin 同一版本；EmbedResponse 扁平数组等字段约定列入契约测试
- **对上前端的上传契约**（D1 新增）：签发预签名 PUT URL 端点 + complete 端点（HeadObject 校验 + 登记）；预签名时效、key 规则、双视角 URL 写入契约
- **对 Core**：无调用即无契约（D3）
- **D2**：身份原样保留，不设契约改造项

### 阶段 3: 构建 / 发布 / 运行独立

- 配置脱钩：自建 `.env.example`；`embedding_model`/`embedding_dim` 改 kb 专属命名或显式声明来源；敏感项走 K8s Secret（含 MinIO service account 凭据、Milvus 凭据）
- 独立 CI：镜像流水线迁出 monorepo；镜像独立 tag（SemVer）
- 运行合同（保持并强化 [main.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/main.py) 结构）：
  - `/readyz` 门控语义 = db pool + dispatcher + gRPC server；**不把 NATS/rag-engine/Milvus/MinIO 加入门控**（宕机均属设计内降级或快速失败）；依赖健康作为上报项
  - 补领域指标：outbox 积压量、consumer 在途/重试、解析 run 时长分布、三道无结果闸门命中率、降级事件计数、预签名签发/complete 成功率；指标标签控制基数
  - 日志关联 request / kb / document / run\_id
  - 保持消费者先订阅后投递、双 loop 资源绑定、JetStream ack 语义、优雅停机逆序
- **存储运维归属落地（D1）**：Milvus 监控/备份/容量（含 etcd 配额与 auto-compaction 配置教训）；bucket 生命周期策略与配额；预签名 URL 时效配置

### 阶段 4: 隔离验证

- 独立测试环境：专属 PG；独立 NATS stream（沿用 `ani.tasks.kb.*` subject 名）；独立 Milvus 实例；kb 自管 bucket
- 全链路验收走真实依赖（真实 PG / MinIO / NATS / rag-engine / Milvus）：预签名直传上传 → complete 登记 → 解析（v2 路径，内网 URL 拉取）→ 检索（三模式 × 三闸门）→ 会话（流式 + 降级）
- 专项回归：Redis/NATS 宕机降级路径；重解析幂等（同 run\_id 不撞幂等键、旧块旧向量清理）；跨租户负向；**D1 专项**——预签名直传全链路（过期 URL 拒绝、key 越权）、图片按前缀批量删除；**D4 专项**——重复 RPC 命中 kb\_operations 幂等回放
- 可观测性验收：/metrics 可被采集；上述指标在全链路验收中产出真实数据；确认无高基数标签
- 证据区分 `pass / fail / not_verified`，区分层次：源码级 ≠ 真实依赖级 ≠ 正式进程级 ≠ 产品链路级 ≠ 部署级

### 阶段 5: 仓库拆分（首发即目标态）

- **不带入清单**：v1 解析路径与 flag 门控、NotifyDocumentUploaded RPC、core\_api 模块、`app/generated` 复制件、共享 async\_tasks/outbox\_events 读写
- rag-engine 侧同步删除 parse\_worker 及其对 PG/向量库的直接写权限
- `repo/services/kb-service/` 整目录迁移独立仓库，记录拆分 commit 与差异；monorepo 侧删除目录后更新 CODEOWNERS 与文档索引
- 回滚 = git revert + 镜像重建，不设专门预案

### 阶段 6: 演进期（拆分完成后）

- 引擎收敛评估：pgvector 替代独立 Milvus（若规模允许，消一套运维）
- task-service 就绪后：订阅 kb outbox 事件承接任务展示，Gateway task 路由接 task-service
- 身份机制调整（D2 钩子）另立 ADR
- 若出现新的 Core 依赖，按 D3 RFC 流程办理

## 六、顺序与并行

- 阶段 0 → 1 是关键路径；存储基线（D1）在阶段 1 内并行建成
- 阶段 2、3 可与阶段 1 并行（上传契约依赖前端排期）
- 阶段 4 依赖 1、2、3；阶段 5 依赖 4 验收通过
- **kb 拆分与 task 拆分互不阻塞**（解耦接口 = kb outbox 事件 schema + at-least-once 投递）

## 七、后果

- 正面：kb 获得 A+B 层 + outbox + 幂等表的单一 DDL 权威；**向量与对象完全自管，热路径（索引写入、检索、图片上传/删除、文档取用）全部内网直连，少一跳 Core，取用 URL 自主签发不再绕外部地址**；图片删除缺口从设计上不存在；Core 契约面为零，core\_api 不带入；租户隔离从 RLS/GUC 转为显式约束；三份 proto 复制收敛；v1 不带入后 kb 是唯一编排者，rag-engine 彻底无状态化；C 层争议消解（outbox 模式归位、task 资产留给 task 域）；**首发即目标态，无停写窗口/迁移/回滚预案等过渡性负担**
- 代价：kb 承接 Milvus 与 bucket 的运维责任（监控/备份/配额/生命周期）；预签名直传需前端配合改造上传逻辑，并引入时效与越权控制的测试面；D2 暂缓意味着租户校验依赖 Gateway 信任链的形态保持
- 明确不做：拆分期间不换向量引擎（pgvector 评估放演进期）、不动身份机制、不做跨领域通用文件服务、不设停写窗口/数据迁移/停用序列/观察期（无存量负担，回滚即代码回退）
