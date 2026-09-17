# MODEL-CONFIG-M3 — KB 推理模型动态切换：SSE 流式切换 + 建库默认推理模型

> 批次：MODEL-CONFIG-M3（SSE 流式请求级切换 + 建库 default_inference_service）
> 前置：MODEL-CONFIG-M1（LLM per-request）+ M2（embedding per-KB）已在前一轮交付（rag-engine/kb-service 实现，v1.yaml 零变更）
> Spec：`docs/superpowers/specs/2026-09-11-model-config-dynamic-switching-design.md` §8 展望扩展

完成日期：2026-09-11（同日追加代码审查修复、env 化默认值、env 化二轮审查三批次；2026-09-14 追加 per-KB embedding 透传链补记、KB 维度探测、review-it 审查收口三批次，见各追加节）
状态：local verified（工作区交付、未提交）
验证结果（2026-09-14 收口时点）：kb-service pytest **361 passed**（基线 354 + 5 新回落链测试 + 2 新配置测试）；rag-engine pytest **166 passed**；gateway `go test ./...` 四包全 ok（gateway/authz/middleware/router）；model-service 测试 ok；`make GO_CACHE_ENV= PYTHON=python validate-services` 全绿（`✅ Services PR gate valid`；末尾 Windows 沙箱对 `> /dev/null` 重定向的误伤 exit 1 不构成 gate 失败）；`make validate-architecture` 通过（`component import guard passed` + `inference legacy control plane retired`）；SDK/docs 幂等再生成验证通过；契约基线无需变更（纯字段新增，无新 operation）。

## 目标

承接 M1+M2（模型配置从 env 冻结改为接口可配、per-request 切换不重启），补齐两个缺口：

1. **SSE 流式场景请求级切换推理模型**——流式问答 `GET /knowledge-bases/{kb_id}/query/stream` 此前无法携带 `inference_service_name`（契约未声明 query 参数，gateway 测试无断言）；
2. **建库时选默认推理模型**——`default_inference_service` 落 KB 行，问答时请求级为空自动回落 KB 默认值，再空回落全局 `"default"`（对应 rag-engine `settings.vllm_model`）。

## 三级回落链（本批次核心语义）

```
request.inference_service_name ──空──> kb_cfg["default_inference_service"]（KB 行）──空──> ""（由 rag-engine settings.vllm_model 接管）
```

- 请求级 > KB 级 > 全局默认，同步 Query 与 SSE 流式 Retrieve 两条问答路径同构；
- `default_inference_service` 是普通 TEXT 可空列（区别于 `embedding_model` NOT NULL）：改它只影响生成路由、无需重建索引，故不进 M3-config 的 rebuild 触发集；
- 空串归一化：repo 层 `default_inference_service or None`——空串落库为 NULL（"未设置"），行为与升级前一致；
- **审查修正（2026-09-11）**：全局兜底原为字面量 `"default"`，会作为非空字符串短路 rag-engine 的 `model or settings.vllm_model`，导致 vLLM 收到无效模型名。已改为空串 `""`，由 `settings.vllm_model` 真正接管默认模型路由。

## 变更清单

### 契约（v1.yaml 四处，全部 additive）

| 位置 | 内容 |
|---|---|
| `KnowledgeBase` schema（L398） | 新增 `default_inference_service`（string nullable，建库时选默认推理模型） |
| `KBQueryRequest`（L433） | 已有 `inference_service_name` 描述补强（为空回落 KB 默认） |
| `createKnowledgeBase` requestBody（L2497-2506） | 补 `default_inference_service` 字段 |
| SSE `queryKnowledgeBaseStream`（L2722） | query 参数补 `inference_service_name`（流式场景请求级切换模型入口） |

### proto（kb_service.proto 两字段 + 双端 stub 再生成）

- `CreateKBRequest.default_inference_service`（field 9）；
- `KnowledgeBase.default_inference_service`（field 14）；
- `QueryRequest/RetrieveRequest.inference_service_name`（field 8）M1 已预留，本批次接通消费端。
- 生成物：`pkg/generated/pb/kb/v1/kb_service.pb.go`（buf）+ `services/kb-service/app/generated/kb/v1/kb_service_pb2.py/.pyi`（grpc_tools）。

### 数据层

- `deploy/migrations/20260911000100_kb_default_inference_service.sql`：`knowledge_bases` 加 `default_inference_service TEXT NULL` 列（`deploy/migrations/atlas.sum` 同步重生成）。

### kb-service（repositories + grpc_server）

- `app/repositories/knowledge_base.py`：`create_kb` INSERT 写入（`or None` 空串归一化）+ 4 条读路径 SELECT 补列（get/list/审计快照来源/Query+Retrieve kb_cfg 取数）；
- `app/api/grpc_server.py` 7 处：CreateKB 写入传参、`_query`/`_retrieve_stream` 的 kb_cfg 补 `default_inference_service` 键 + 回落链、`_kb_row_to_pb` 映射、`_kb_audit_snapshot` 快照键（未设置记 None）。
- `app/services/query_orchestrator.py`：`inference_service_name` 透传链路（M1 轮已改 rag_engine client，本批次接通 kb_cfg 回落源）。

### Gateway（ani-gateway）

- `kb_sse.go`：SSE handler 读 query 参数 → proto `RetrieveRequest.InferenceServiceName`（**代码 M1 轮已实现，本批次补契约声明与测试断言**）；
- `kb_resources.go` 4 处（本批次发现并补齐契约已声明但 handler 未传的缺口）：`createKnowledgeBaseRequest` 加 `DefaultInferenceService` 字段、CreateKB 调用传参、`knowledgeBaseJSON` 回显字段、`kbToJSON` 映射；
- 同步 Query 链路 JSON body → proto（既有代码，本批次补 fake 记录与断言）。

### 测试

| 层 | 文件 | 内容 |
|---|---|---|
| kb-service | `tests/test_us010_wiring.py` | 5 个新测试：`_RecordingRagGrpcClient`/`_KBDefaultConn`/`_KBDefaultPool` fake helper；Query 同步三级回落（请求级直用 / KB 级回落 / 双空回落 default）；Retrieve 流式请求级与 KB 级切换 |
| kb-service | `tests/test_audit_logging.py` | 既有快照精确 dict 断言补 `"default_inference_service": None`（fake 行未设置 → NULL 快照） |
| gateway | `kb_sse_test.go` | `TestSSE_NewPath_TokenSourcesDone` URL 加 `inference_service_name=qwen3-32b` + `lastReq.GetInferenceServiceName()` 断言 |
| gateway | `kb_resources_test.go` | fakeKBClient 补 `lastQueryReq`/`lastCreateKbReq` 记录；Query 测试加 JSON body `inference_service_name` 断言；CreateKB 测试（`TestKBRoutes_GrpcPassthroughCreate`）加请求传递与响应回显 `default_inference_service` 双向断言 |

## 关键文件改动

| 文件 | 变更 |
|---|---|
| `api/openapi/services/v1.yaml` | 修改（4 处 additive） |
| `api/proto/kb/v1/kb_service.proto` | 修改（2 字段） |
| `deploy/migrations/20260911000100_kb_default_inference_service.sql` | 新增（未跟踪，**必须随批次提交**） |
| `deploy/migrations/atlas.sum` | 修改 |
| `pkg/generated/pb/kb/v1/kb_service.pb.go` + kb-service Python pb2 三件套 | 再生成 |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改（1 写 + 4 读） |
| `services/kb-service/app/api/grpc_server.py` | 修改（7 处） |
| `services/kb-service/app/services/query_orchestrator.py` | 修改（回落链透传） |
| `services/ani-gateway/internal/router/kb_resources.go` | 修改（4 处：建库缺口补齐） |
| `services/ani-gateway/internal/router/kb_resources_test.go` / `kb_sse_test.go` | 修改（三链路断言） |
| `services/kb-service/tests/test_us010_wiring.py` / `test_audit_logging.py` | 修改 |
| 生成物（docs/api/services.html 等 SDK/docs） | 再生成（幂等验证） |

## 设计决策

### D1：default_inference_service 用可空 TEXT 列，不进 rebuild 触发集
- 与 `embedding_model` NOT NULL + "改需重建索引"相对：推理模型只影响生成路由，不触碰向量数据，改值零重建成本；
- 空串归一化 `or None` 落 NULL，保证"未设置"与"升级前行为"完全一致（回落 `"default"`）。

### D2：三级回落链在 kb-service 收口，gateway 只透传
- 回落判定统一在 `_query`/`_retrieve_stream` 构造 kb_cfg 时完成（`request.inference_service_name or kb_cfg["default_inference_service"] or ""`），gateway 无需感知 KB 配置，两层职责清晰；
- SSE 与同步 Query 同构回落，测试覆盖两路径；
- 全局兜底语义（审查修正后）：`""` 空串传给 rag-engine，其 `model or settings.vllm_model` 兜底到默认模型——比硬编码 `"default"` 更正确且解耦部署配置。

### D3：Gateway 建库链路按契约补齐（非新增设计）
- v1.yaml 契约先行已声明 `default_inference_service`，review 中发现 `createKnowledgeBaseRequest`/CreateKB 调用/回显 JSON 三处 handler 缺口，按契约补齐——契约与实现漂移修正，无独立设计。

## 验证命令（已运行）

```
cd repo/services/kb-service
python -m pytest -q                       # 359 passed（基线 354 + 5 新测试）

cd repo/services/ani-gateway
go build ./...
go test ./...                             # 四包 ok（gateway/authz/middleware/router）

cd repo
python scripts/gen_sdk_alpha.py           # SDK Alpha artifacts generated
python scripts/generate_api_docs.py       # API docs generated
make GO_CACHE_ENV= PYTHON=python validate-services   # ✅ Services PR gate valid
```

> Windows 门禁注记：make 门禁末尾 `TRAE Sandbox Error: hit restricted C:\dev\null`（`> /dev/null` 重定向被沙箱拦截）为环境误伤，`"✅ Services PR gate valid"` 已打印，gate 实际通过；buf 工具链需 `$env:PATH="E:\GoPath\bin;$env:PATH"` 前缀。

## 开放问题

### OQ1：UpdateKBConfig 尚未暴露 default_inference_service 修改入口
建库时可设默认推理模型，但 `PUT /knowledge-bases/{kb_id}/config`（M3-config 契约冻结：embedding_model/chunk_size/ocr_enabled/top_k/score_threshold/retrieval_strategy）未含该字段——改 KB 默认推理模型当前只能建库时定。后续在 M3-config 实现批次扩字段（additive、无 rebuild 触发，成本极低）。

### OQ2：`GET /models` 清单仍未实现
建库/问答选模型的候选清单（`ModelList` 契约已冻结）归 M3-config 实现批次；当前调用方需自行知道 `inference_service_name` 取值。

### OQ3：live 验证待执行
本批次为 local verified；真实 Envoy AI Gateway 按 `model` 路由的端到端流式切换（SSE 携不同 inference_service_name → 不同 vLLM 实例）需部署后 live 验证，未跑前不标 runtime ready。

## 代码审查修复批次（2026-09-11 追加）

M3 交付后对 M1+M2+M3 全量改动做整体代码审查（TRAE-code-review skill 流程，两并行子代理 2/2 共识确认 5 个 Issue），用户授权 Fix All，全部修复并验证。

### Issue 清单与修复

| # | 级别 | 问题 | 修复 |
|---|---|---|---|
| 1 | 高 | kb-service 兜底字面量 `or "default"` 是非空字符串，短路 rag-engine `model or settings.vllm_model`，vLLM 收到无效模型名预期 model_not_found | Query/Retrieve 双路径兜底改 `or ""`；contracts.py 两处 docstring、20260911000100 migration 注释、v1.yaml 四处契约描述同步空串语义 |
| 2 | 高 | embedding 默认名三值不一致：CreateKB `"bge-m3"` ≠ config.py `"Qwen3-Embedding-0.6B"` ≠ .env `"BAAI/bge-m3"`（SiliconFlow 只认全名，裸名 model_not_found） | 三处全部收敛 `"BAAI/bge-m3"`：grpc_server.py CreateKB + core create_vector_store、knowledge_base.py 默认参数、rag-engine config.py；新增存量数据 migration `20260911000200_kb_embedding_model_full_name.sql`（向量维度 1024 不变，不重建索引）；v1.yaml default 同步 |
| 3 | 中 | Generate refine 循环每轮 120s timeout × n 轮串行，总时长可超客户端 gRPC 120s deadline，整轮结果变 DEADLINE_EXCEEDED | `generate_rpc_service.py` 加 `GENERATE_TOTAL_BUDGET_SECONDS = 110.0` 总预算，refine 循环首 `time.monotonic()` 检查，耗尽 break 返回已有答案 + warning 日志 |
| 4 | 低 | rag-engine `embeddings.py` 模块级 `_model` 别名 + `embed()` 死函数，生产路径无调用者 | 全部删除；`test_embeddings_no_llamaindex.py` 改用 registry 断言（`get_embed_model("") is default`）+ 负断言 `not hasattr(embeddings, "_model")` |
| 5 | 低 | `retrieve_service.py` 每次 retrieve 用 factory 建 CoreClient 不释放，泄漏 httpx.AsyncClient 连接池 | vector leg 改 `async with self._core_client_factory(tenant_id) as core:`；`test_retrieve_service.py` + `test_query_shadow.py` 两个 fake 补 `aclose`/`__aenter__`/`__aexit__` + `close_count`。parse_orchestrator 已有 `finally: await core.aclose()` 保护，无需改 |

### 审查中排除的非问题（两子代理一致确认）

- per-KB embedding registry 锁内构建 adapter（纯内存 ~1ms，无 IO）；
- `_estimate_tokens` 逐字符循环（asyncio.to_thread 卸载，<1% LLM 延迟）；
- `_orchestrators` dict 并发访问（单事件循环原子段）；
- `_truncate_history`/`_repack_context` 边界处理；
- 跨平台 release_linux.go/release_other.go build tag 拆分（b3-gate 轮已定方案）。

### 修复后验证

```
cd repo/ai/rag-engine    && python -m pytest -q   # 166 passed
cd repo/services/kb-service && python -m pytest -q  # 359 passed（含修复后 fake 上下文接口的 test_query_shadow 5 例）
```

### 修复涉及文件

| 文件 | 变更 |
|---|---|
| `services/kb-service/app/api/grpc_server.py` | 修改（兜底空串 ×2、embedding 全名 ×2、注释同步） |
| `services/kb-service/app/services/contracts.py` | 修改（docstring 空串语义 ×2） |
| `services/kb-service/app/repositories/knowledge_base.py` | 修改（默认 embedding 全名） |
| `services/kb-service/app/services/retrieve_service.py` | 修改（`async with` 释放 CoreClient） |
| `ai/rag-engine/app/core/config.py` | 修改（embedding 默认 `BAAI/bge-m3`） |
| `ai/rag-engine/app/services/generate_rpc_service.py` | 修改（110s 总预算） |
| `ai/rag-engine/app/core/embeddings.py` | 修改（删死代码） |
| `api/openapi/services/v1.yaml` | 修改（4 处描述同步 + CreateKB embedding default） |
| `deploy/migrations/20260911000200_kb_embedding_model_full_name.sql` | 新增（存量 bge-m3 → BAAI/bge-m3） |
| `deploy/migrations/20260911000100_kb_default_inference_service.sql` | 修改（注释语义同步） |
| `ai/rag-engine/tests/test_embeddings_no_llamaindex.py` | 重写（registry 断言） |
| `services/kb-service/tests/test_retrieve_service.py` / `test_query_shadow.py` | 修改（fake 补上下文接口） |

## env 化默认值批次（2026-09-11 追加）

用户决策：embedding 默认值不要硬编码字面量，跟随 env 变。kb-service 的 pydantic Settings 新增两键，替换全部硬编码点；运维换 embedding 模型只需改 `.env` 的 `EMBEDDING_MODEL` + `EMBEDDING_DIM` 两键（与 rag-engine 共读同一键），无需改代码。

### 变更

| 文件 | 变更 |
|---|---|
| `services/kb-service/app/core/config.py` | Settings 加 `embedding_model: str = "BAAI/bge-m3"` / `embedding_dim: int = 1024`（pydantic 自动映射 env `EMBEDDING_MODEL`/`EMBEDDING_DIM`，默认值仍与 .env 镜像兜底） |
| `services/kb-service/app/api/grpc_server.py` | CreateKB 兜底 `or "BAAI/bge-m3"` → `or settings.embedding_model` ×2（create_kb + create_vector_store）；`dim = 1024` → `settings.embedding_dim` |
| `services/kb-service/app/repositories/knowledge_base.py` | `create_kb` 的 `embedding_model` 改必填参数（repo 层不 import Settings，保持分层方向：API 层决定默认值，repo 层纯数据访问）+ 空值护栏 `ValueError` |
| `repo/.env.example` | 补 `EMBEDDING_DIM=1024` 声明 + `EMBEDDING_MODEL` 注释（标注 kb-service/rag-engine 共读） |
| `services/kb-service/tests/test_config.py` | 新增 2 测试：默认值断言（全名前缀）+ env 覆盖断言（EMBEDDING_MODEL/EMBEDDING_DIM 覆盖默认） |

### 设计说明

- **键名与 rag-engine 共读**：两个服务各自实例化自己的 Settings，读同一份 `.env` 的同两个键——换模型一处改、两服务同步生效（rag-engine embed 兜底 + kb-service 建库兜底/维度）；
- **repo 层必填而非读 settings**：repositories 是纯数据访问层，反向依赖 API 层配置会破坏分层；生产唯一调用方（grpc_server）已显式传参，必填参数 + 空值护栏把"漏传"从静默落空值变成建库时立刻报错；
- **存量 KB 不受影响**：默认值只作用于"建库请求未传 embedding_model"的新库；已落库的 KB 行不变（migration `20260911000200` 已处理存量 `bge-m3` → 全名）。

### 验证

```
cd repo/services/kb-service && python -m pytest -q   # 361 passed（359 + 2 新配置测试）
```

## env 化批次二轮审查修复（2026-09-11 追加）

用户指令："从整个项目的框架上查看本 issue 代码是否有问题，类型，性能优化等是否需要进行修改"。对 env 化批次 5 文件做全框架审查，两子代理并行二次校验一致确认 3 个低危（无高危/中危），用户选择 Fix All。

### Issue 清单与修复

| # | 危险度 | 问题 | 修复 |
|---|---|---|---|
| 1 | 低 | repo `create_kb` 的 `score_threshold: float = 0.3` 默认值与 M3"0=未设置"契约矛盾（grpc_server 注释明确"而不是硬编码 0.3"，生产路径显式传 `or 0.0`；但 repo 签名默认仍 0.3，未来调用方漏传即静默错存） | 默认值 0.3 → 0.0 + 注释说明契约（当前无调用方依赖 0.3，全仓唯一生产调用方 grpc_server 恒显式传参，零行为变化） |
| 2 | 低 | `request.embedding_model or settings.embedding_model` 在 `_create_kb` 内重复求值两次（DB 行 vs Core 向量库），今日值恒一致（settings 模块级单例），但该表达式承担"KB 行 ↔ VS 模型必须一致"约束，单边改动即静默漂移 | 提取局部变量 `embedding_model`，两处复用 + 注释指明同步约束 |
| 3 | 低 | 新配置测试 `test_settings_default_embedding_model_is_full_prefixed_name` 用 `Settings(_env_file=None)` 断言默认值，但该参数只禁 .env 文件、不隔 os.environ——宿主机/CI 若 export 过 `EMBEDDING_*` 则假阳性失败 | `_patch_env` helper 扩展清除语义（None 值 = pop）；断言前显式清除两个变量 |

### 审查中排除的非问题（两子代理一致确认）

- rag-engine 侧 `embedding_model` pydantic 默认值被 env 正常覆盖（参照实现无恙）；
- ani-gateway 代码中 `bge-m3` 字样仅为测试透传数据（任意字符串），非默认值依赖；
- 两处兜底表达式间无 `request` 重赋值路径（今日值必一致）；
- repo `chunk_size`/`top_k`/`retrieval_mode` 默认值与 grpc_server 传值一致（1024/5/hybrid），无矛盾。

### 修复后验证

```
cd repo/services/kb-service && python -m pytest -q   # 361 passed
cd repo/ai/rag-engine    && python -m pytest -q       # 166 passed
```

### 修复涉及文件

| 文件 | 变更 |
|---|---|
| `services/kb-service/app/repositories/knowledge_base.py` | `score_threshold` 默认值 0.3 → 0.0（对齐 0=未设置契约）+ 契约注释 |
| `services/kb-service/app/api/grpc_server.py` | `_create_kb` 提取局部变量 `embedding_model`，create_kb 与 create_vector_store 两处复用 |
| `services/kb-service/tests/test_config.py` | `_patch_env` 支持 None 清除语义；默认值断言前清除 `EMBEDDING_*` |

---

## per-KB embedding 透传链补记（M2 前轮交付，2026-09-14 补录）

M2（embedding per-KB）在前轮随 M1 交付但未在本记录留档，补记透传链全貌（2026-09-14 维度探测批次同链路复用）。

**写侧（文档入库）**：`parse_consumer` 读 KB 行 `embedding_model` → `parse_orchestrator.run(embedding_model=...)` → `rag_engine.embed(texts=texts, model=embedding_model)` → rag-engine `EmbedRequest.model`（field 2，空 = 服务端默认）→ `grpc/server.py` Embed handler（`asyncio.to_thread(svc.embed, list(request.texts), request.model)`）→ `embed_rpc_service.embed` → `get_embed_model(model)`。

**读侧（查询检索）**：`grpc_server._query`/`_retrieve_stream` kb_cfg 取 `kb_row["embedding_model"]` → `query_orchestrator.query`/`query_stream(embedding_model=...)` → `retrieve_service.retrieve` → `self._rag_engine.embed(texts=[question], model=embedding_model)`（L188-190）。

**注册表**（`ai/rag-engine/app/core/embeddings.py`）：模块级 `_models` dict + `_default_name` + `_lock`；`get_embed_model(name)` 在锁内懒加载——未注册的模型名按 settings 连接参数（`embedding_api_base`/`embedding_api_key`）现建 `OpenAICompatibleEmbedding` 适配器并缓存，默认模型启动时 `init_embedding_model` 预热。适配器不用 LlamaIndex `OpenAIEmbedding`，因其对 model 名做 OpenAI 枚举校验、拒绝 `BAAI/bge-m3` 等自定义名（远端要求全名前缀）。同名模型适配器缓存后切换零成本。

**契约**：`rag.proto` `EmbedRequest.model = 2` HEAD 已有——M2 零 proto 修改，仅 `rag_pb2.py` 再生成；v1.yaml 零变更（gRPC 内部面，不经 gateway）。

**测试**：`ai/rag-engine/tests/test_embed_rpc.py` 3 个 M2 新测试（`EmbedRequest.model` 参数路由 ×2 + 空 model 走服务端默认）。

## KB 维度探测批次（2026-09-14 追加）

M3 主批次后接续：建库时 `embedding_model` 已可 per-KB 指定，但向量库 collection 维度仍固定读 env `EMBEDDING_DIM`（1024）——换不同维度的 embedding 模型需同步改 env 两处，漏改即建库错维。本批在 CreateKB 落地维度实测。

### 变更（唯一文件：`services/kb-service/app/api/grpc_server.py`，mtime 2026-09-14 09:29）

| 位置 | 内容 |
|---|---|
| `_create_kb`（L282-303） | KB 行落库后、Core `create_vector_store` 前：经 rag-engine Embed RPC 发 `texts=["dimension probe"]`（携带选定 `embedding_model`）实测维度；`except Exception` 仅 warning 不阻断；`dimension = probe_dim or settings.embedding_dim` |
| L2695-2736 | 模块级单例 `_default_grpc_client_instance` + `_default_rag_engine_grpc_client()`——无注入 factory 的兜底路径不再每次建库重建 gRPC channel |

### 设计决策

- **D1 探测降级不阻断建库**：探测异常（rag-engine 不可达/模型名无效）仅 `logger.warning` 并回落 env 值；探测点在 DB 事务提交之后，失败不回滚 KB 行；
- **D2 复用 `embedding_model` 局部变量**（主批次审查修复 Issue 2 引入的变量，L234-236 只求值一次）：DB 行、探测 RPC、向量库三处同一值，防单边漂移——探测与建库必然同模型；
- **D3 gRPC 单例防 channel 泄漏**：兜底路径全局缓存一次构造的 `RagEngineGRPCClient`（避免每次建库重建 channel 造成 connection storm；生产路径 main.py 启动时注入 factory，不走此兜底）；
- **D4 执行 spec §5.4 延迟项**：spec 原文"维度推导归 M3 rebuild 新建 vector store 时一并处理"——CreateKB 即新建 vector store 场景，本批落地该项。

### Deviations

- **相对 spec §5.4 "dim=1024 硬编码不动"**：新建 collection 的维度从固定 env 值改为实测值。这是同一条款后半句预留的延迟项（"维度推导归 M3 rebuild 新建 vector store 时一并处理"），非静默偏离；存量 KB/collection 不受影响；env `EMBEDDING_DIM` 角色从"权威值"降级为"探测失败兜底值"。

### Tradeoffs

- **实测维度 vs 固定 env**：实测消除"模型 ↔ env 维度不一致"整类错误（换模型只改 `EMBEDDING_MODEL` 一处）；代价 = 每次建库多一次 Embed RPC（一条短文本，延迟可忽略）；
- **降级 vs fail-fast**：降级保证 rag-engine 短暂不可用时建库仍可用；代价 = 降级路径下若 env 维度与模型真实维度不符，错误延迟到向量写入时才在 Core 端暴露。

### Open Questions

- **OQ4 无维度探测专属单测**：kb-service 测试目录 grep `probe_dim`/`dimension probe` 零命中——探测成功透传与异常降级回落两条路径无测试覆盖。建议补：fake factory 抛异常断言 warning + env 兜底、fake 返回 dim 断言实测值透传至 `create_vector_store`；
- **OQ5 live 探测路径未验证**：本地全 mock，真 rag-engine 的探测/降级分支未跑（与 OQ3 live 验证同批执行）。

### 验证

```
cd repo/services/kb-service && python -m pytest -q   # 361 passed（无新增测试，全量回归）
```

## review-it 审查收口批次（2026-09-14 追加）

M3 三批次（主批次 + 代码审查修复 + env 化两轮）交付后，用 review-it skill 对工作区全量改动做收口审查验证。

### 审查结论：5 候选 = 1 修复 + 4 拒绝

**修复（唯一）——atlas.sum h1 重算**（`deploy/migrations/atlas.sum` mtime 2026-09-14 10:39）：

- 症状：`atlas migrate validate` 报 sum 与 migrations 目录不匹配——新增 `20260911000100`/`20260911000200` 后 `atlas.sum` 未同步；
- 修复：下载 atlas.exe → `atlas migrate hash --dir file://deploy/migrations` 重算（现 L53/L54 两条 h1）；
- **教训**：atlas `h1:` 是 atlas 自有哈希算法（非纯 SHA256）——手工 PowerShell `Get-FileHash` 计算永远不匹配，必须用 `atlas migrate hash` 重算；`--dir` 需相对路径 `file://deploy/migrations` 格式；沙箱内 atlas.exe 下载/执行需用户批准（requires_approval）。

**拒绝（4 项）**：审查候选经验证后拒绝修改——含 outbox 竞态（用户明确否决修复，维持既定边界"不在本批处理"）在内，其余候选验证后确认为非本批引入/非问题，维持现状。

### 预存在迁移版本冲突（4 组，识别不修）

- `20260827_001_async_tasks_list_index.sql` ↔ `20260827000300_async_tasks_list_index.sql`、`20260827_001_user_roles_single_role.sql` ↔ `20260827000400_user_roles_single_role.sql`：同名迁移两版本号前缀并存，atlas 报 "multiple files with the same version"——**HEAD 即有，非 M3 批次引入**；
- 工作区曾尝试改名消歧（git 现状：旧文件 `D` + 新文件未跟踪即改名遗留）；atlas 拒绝改名——"applied migrations cannot be edited"（已应用迁移不可编辑/改名）；
- 决策：**不修**——修复被 atlas 拒绝 + 引入与 M3 无关的变更会污染批次。

### Deviations

None——纯审查收口批次，无 spec 偏离。

### Tradeoffs

- **不修预存在冲突 vs 修复**：修复需处理已应用迁移的改名（atlas 明确拒绝）或做 baseline 重建，代价与风险均超出本批范围；不修则 `atlas migrate validate` 对该 4 组的既有告警继续存在，留待专门批次统一处理。

### Open Questions

- **OQ6 迁移版本冲突遗留**：20260827 旧命名（`_001_`）与新命名（`0003xx`）并存且均已应用，需专门批次决定合并策略（baseline 重建或容忍双版本）；工作区当前的 D + 未跟踪改名痕迹须在提交前处理（恢复原名或完成改名决策），不得随 M3 批次提交；
- **OQ7 atlas.sum 须随批次提交**：重算后的 `atlas.sum` 与 `20260911000100`/`20260911000200` 两个迁移文件同批提交，否则部署端 `atlas migrate validate` 失败。

### 验证（收口时全绿）

```
cd repo/services/kb-service && python -m pytest -q    # 361 passed
cd repo/ai/rag-engine && python -m pytest -q          # 166 passed
cd repo/services/ani-gateway && go test ./...        # 四包 ok（gateway/authz/middleware/router）
model-service 测试 ok
make validate-architecture                            # "component import guard passed" + "inference legacy control plane retired"
```


