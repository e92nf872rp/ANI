# RAG-REFACTOR-STEP-3-INTERFACE — kb-service 编排层抽象接口定义 (步骤 3-5 接口)

- **Issue:** issue-027-interface-kb-service-abstract-protocols
- **Branch:** `refactor/architecture-compliance`
- **Date:** 2026-08-17
- **Product line:** core (Services / kb-service)
- **Type:** interface (仅抽象 Protocol/dataclass 定义,不含任何具体实现)

## 变更文件

| 文件 | 变更内容 |
|------|----------|
| `services/kb-service/app/services/__init__.py` | 新建 `app/services/` 包标记 (Plan §3.5) |
| `services/kb-service/app/services/contracts.py` | 定义 5 个 `@runtime_checkable Protocol` + `QueryResult` dataclass,共 12 个抽象方法签名,全部使用 `*,` keyword-only 参数 |

## 1. Design Decisions

### 1.1 使用 `typing.Protocol` + `@runtime_checkable` 而非 `abc.ABC`

- **Ambiguity:** Issue AC 写"Python Protocol / ABC",未指定用哪种抽象基类机制。
- **Choice:** 使用 PEP 544 `typing.Protocol` + `@runtime_checkable`。
- **Rationale:** Protocol 支持结构类型(structural typing),具体实现类无需显式继承 Protocol 即可满足契约(duck typing + 静态检查)。这对编排层尤为重要——`CoreClient`/`RagEngineGRPCClient` 是既有类,强制它们继承一个新建的 ABC 会侵入式修改既有代码;Protocol 允许它们仅凭方法签名匹配。`@runtime_checkable` 提供 DI 布线时的 `isinstance` 兜底校验(仅校验方法名存在,不校验签名——Python 已知限制,但能捕获"完全缺失方法"的布线错误)。

### 1.2 `generate_stream` 声明为 `async def` 而非 `def`

- **Ambiguity:** gRPC `unary_stream` stub 返回的 `AsyncIterator` 可直接 `async for` 迭代,无需 `await`。`def` 返回 `AsyncIterator` 和 `async def` 返回 `AsyncIterator` 都能在运行时工作。但具体实现 `RagEngineGRPCClient.generate_stream` 会是 `async def` + `yield`(异步生成器,把 gRPC `GenerateToken` proto 转成 dict)。
- **Choice:** 声明为 `async def generate_stream(...) -> AsyncIterator[dict[str, Any]]`。
- **Rationale:** (1) 与同文件 `parse`/`embed`/`generate`(均为 `async def`)保持一致;(2) 与 Plan §3.4 原文 `async def generate_stream` 一致;(3) 异步生成器函数(`async def` + `yield`)不是协程函数(`inspect.iscoroutinefunction` 返回 False),所以调用方仍用 `async for tok in client.generate_stream(...)` 无需 `await`——经实测验证。运行时验证证明 `async def`+`yield` 实现满足 `runtime_checkable` Protocol 的 `isinstance` 检查。

### 1.3 全部 12 个方法使用 `*,` keyword-only 参数分隔符

- **Ambiguity:** Issue AC 和 Plan 均未指定参数 kind(位置参数 vs keyword-only)。初次实现用了位置参数(无 `*,`)。
- **Choice:** 经第二轮框架级审查后,全部 12 个方法改为 keyword-only(`*,` 分隔符)。
- **Rationale:** 既有 `CoreClient`([app/core_api/client.py](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core_api/client.py))100% 使用 `*,` 约定;既有调用方 [grpc_server.py:584-587](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/api/grpc_server.py#L584-L587) 用关键字传参。PEP 544 结构匹配考虑参数 kind——位置参数 Protocol 不能被 keyword-only 实现满足(mypy/pyright 会报 `Signature incompatible`)。若 Protocol 用位置参数,issue-030 实现时要么违背 Protocol(用 `*,`),要么违背既有 CoreClient 风格(用位置参数)。将 Protocol 对齐既有约定是正确方向。

### 1.4 `QueryResult` 用 `@dataclass` 而非 pydantic `BaseModel`

- **Ambiguity:** 项目在 `app/core/config.py` 用 pydantic `BaseSettings`,但响应 DTO 是否也应用 pydantic 未明说。
- **Choice:** 用 `@dataclass`,`sources` 字段用 `field(default_factory=list)`。
- **Rationale:** 经审查,项目仅用 pydantic 做 settings,从不用于响应 DTO;gRPC servicer 直接从 dict 构建 proto 对象(`kb_pb.QueryResponse(...)`)。`QueryResult` 是内部编排层值对象,无外部输入解析、无 JSON 序列化边界、无需校验——dataclass 是惯用且零开销的选择。`field(default_factory=list)` 正确避免共享可变默认值。

### 1.5 `CoreClientProtocol` 包含既有方法 + 新增方法的全量接口

- **Ambiguity:** `CoreClientProtocol` 应只声明编排层依赖的新增方法,还是也包含已有的 `request_download_url`/`delete_vector_store_documents`?
- **Choice:** 包含全量 5 个方法(3 新增 + 2 既有)。
- **Rationale:** 编排层(orchestrators)依赖的是 CoreClient 的完整接口面,而非仅新增部分。`ParseOrchestrator` 会调用 `request_download_url` 获取下载 URL 传给 rag-engine Parse RPC。把全量接口集中在 Protocol 中,让编排层的依赖关系一目了然,也便于 issue-030 实现时一次性补齐。

## 2. Deviations

### 2.1 `upload_object` 参数命名与 `request_upload_url` 对齐,新增 `idempotency_key`

- **Spec said:** Plan §3.3 未明确 `upload_object` 的参数名和是否需要 `idempotency_key`。初次实现用 `object_key` 无 `idempotency_key`。
- **Implemented:** 经第一轮审查修正:参数名 `object_key` → `key`(与既有 `CoreClient.request_upload_url` 的 `key` 参数名一致);新增 `idempotency_key: str` 必填参数(既有 `request_upload_url` 强制要求该参数,无默认值)。
- **Why:** 既有 `CoreClient.request_upload_url`([client.py:164-170](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/core_api/client.py#L164-L170))要求 `bucket_id`/`key`/`idempotency_key` 全部 keyword-only 且无默认值。`upload_object` 作为其上层封装,参数名和必填性必须与底层一致,否则具体实现需做 `object_key→key` 映射且调用方无法控制图片上传的幂等重试作用域。

### 2.2 `upload_object` 的 `bucket_id` docstring 示例修正

- **Spec said:** 初次实现 docstring 写 `bucket_id: Target bucket id (e.g. "kb-docs")`。
- **Implemented:** 改为 `bucket_id: Target bucket UUID (resolve the "kb-docs" name to an id via get_bucket_id_by_name first; do NOT pass the name directly — Core API keys buckets by UUID)`。
- **Why:** 既有解析模式 [grpc_server.py:358](file:///c:/Users/PC/Desktop/ANI/repo/services/kb-service/app/api/grpc_server.py#L358) 先用 `get_bucket_id_by_name(name="kb-docs")` 把名字解析成 UUID,再把 UUID 作为 `bucket_id` 传入。原示例会让实现者误以为可直接传名字 "kb-docs"。

## 3. Tradeoffs

### 3.1 `generate_stream`: `def` vs `async def`

- **Alternative A:** `def generate_stream(...) -> AsyncIterator[...]`(初次实现)
- **Alternative B:** `async def generate_stream(...) -> AsyncIterator[...]`(当前选择)
- **Choice:** Alternative B
- **Pros/Cons:** A 在运行时也能工作(`runtime_checkable` 不区分 def/async def);但 A 偏离 Plan §3.4 原文,且与同文件其他 async 方法不一致。B 的担忧"会让调用方需要先 await"经实测证伪——异步生成器函数不是协程函数,`async for tok in client.generate_stream(...)` 无需 await。B 在静态类型语义和一致性上更优。

### 3.2 `QueryResult.sources` 用 `list[dict[str, Any]]` vs 具体 `TypedDict`

- **Alternative A:** `list[dict[str, Any]]`(当前选择,未类型化 dict)
- **Alternative B:** 定义 `TypedDict` 编码 sources 的字段(chunk_id, doc_id, file_name, page, content, parent_content, parent_chunk_id, chunk_type, score)
- **Choice:** Alternative A
- **Pros/Cons:** B 提供更强的类型安全和自文档化,但 sources 的字段集在 retrieve/parse/query 三个编排层间可能不完全一致(如 retrieve 输出含 score,parse 中间产物可能不含)。作为纯接口 issue,先用 `list[dict[str, Any]]` + docstring 文档化字段,具体 TypedDict 可在功能 issue(issue-031/032/035)中按实际需要细化,不破坏 Protocol 契约。

### 3.3 位置参数 vs `*,` keyword-only

- **Alternative A:** 位置参数(无 `*,`,初次实现)
- **Alternative B:** `*,` keyword-only(当前选择)
- **Choice:** Alternative B
- **Pros/Cons:** A 写法更简洁,但与既有 CoreClient/RagEngineClient 100% `*,` 约定不一致,且 PEP 544 结构匹配会失败。B 牺牲一点简洁性,换取与既有代码约定一致、PEP 544 结构匹配通过、防止调用方误用位置参数。B 是正确方向。

## 4. Open Questions

### 4.1 `@runtime_checkable` 的实际效用是否值得

- **Question:** `@runtime_checkable` 的 `isinstance` 只校验方法名存在,不校验签名、不校验 async 性、不校验返回类型。一个有同步 stub 的 mock 也能通过 `isinstance(RagEngineClientProtocol)` 检查。
- **Impact:** 可能给开发者虚假信心,以为 `isinstance` 通过即代表完整 async 契约已验证。
- **Suggestion:** 保留 `@runtime_checkable`(能捕获"完全缺失方法"的布线错误,移除会丧失这层兜底),但在 issue-030 具体实现时应配合单元测试验证 async 行为,而非仅依赖 `isinstance`。如果后续引入 mypy/pyright 静态检查,Protocol 的结构匹配会提供更强的类型保证。

### 4.2 `QueryOrchestratorProtocol.query` 的 `history` 参数是否应包含当前轮 user message

- **Question:** Protocol docstring 明确记录"history 含当前轮 user message(复现旧行为:kb-service 先 append user 到 Redis 再调 rag-engine)",且"Generate RPC 在 history 末尾追加 question 为 USER 消息 → 当前轮 user 出现两次"。
- **Impact:** 这是有意复现旧 `{query_str}` template 行为,但看起来反直觉。如果未来要修正"user 出现两次"的旧行为,需同步修改 Protocol docstring 和 `RagEngineClientProtocol.generate` 的 docstring。
- **Suggestion:** issue-035(QueryOrchestrator 实现)实现时验证 LLM 实际行为是否符合预期;如需修正旧行为,在 issue-035 同步更新契约 docstring。

### 4.3 `CoreClientProtocol` 与既有 `CoreClient` 的结构匹配需待 issue-030 补齐后验证

- **Question:** 当前 `CoreClient` 缺少 `insert_vector_documents`/`search_vector_store`/`upload_object` 三个方法(属 issue-030 范围),所以 `isinstance(CoreClient, CoreClientProtocol)` 当前返回 False。
- **Impact:** 本 issue 的 Protocol 定义无法对既有 CoreClient 做运行时结构匹配验证——需等 issue-030 补齐方法后才能验证。
- **Suggestion:** issue-030 实现 `CoreClient` 新增方法时,应确认新方法也用 `*,` keyword-only(与本 Protocol 一致),并在 issue-030 的验证中运行 `isinstance(CoreClient(...), CoreClientProtocol)` 确认结构匹配。

## 5. Verification Commands Run

| Command | Result |
|---------|--------|
| `python -m py_compile services/kb-service/app/services/contracts.py services/kb-service/app/services/__init__.py` | PASS (exit 0) |
| `python -c "from app.services.contracts import QueryResult, ...; r = QueryResult(answer='x'); assert r.sources==[] and r.input_tokens==0"` | PASS (QueryResult 字段默认值正确) |
| `python _verify_async_proto.py`(临时脚本,已删除) | PASS (`async def`+`yield` 满足 Protocol,`async for` 无需 await) |
| `python _verify_fixes.py`(临时脚本,已删除) | PASS (upload_object 参数含 idempotency_key/key,FakeRag 满足 RagEngineClientProtocol) |
| `python _verify_kwonly.py`(临时脚本,已删除) | PASS (全 12 方法 keyword-only,CoreClient 既有方法参数 kind 与 Protocol MATCH) |
| `python scripts/validate_component_imports.py --root .` | `component import guard passed` (exit 0) |
| `git diff --check` | PASS (exit 0) |

## 6. AC 逐项核对

| AC | 状态 | 证据 |
|----|------|------|
| 新建 `app/services/` 目录 + `__init__.py` | ✅ | `app/services/__init__.py` 存在 |
| 新建 `app/services/contracts.py` 定义抽象接口 | ✅ | `contracts.py` 445 行,5 Protocol + 1 dataclass |
| `RetrieveServiceProtocol.retrieve(...) -> tuple[list[dict], float]` | ✅ | contracts.py:304-314 (keyword-only 签名,返回 `tuple[list[dict[str, Any]], float]`) |
| `ParseOrchestratorProtocol.process_document(...) -> None` | ✅ | contracts.py:353-364 (keyword-only 签名,返回 `None`) |
| `QueryOrchestratorProtocol.query(...) -> QueryResult` | ✅ | contracts.py:397-410 (keyword-only 签名,返回 `QueryResult`) |
| `RagEngineClientProtocol`: parse/embed/generate/generate_stream | ✅ | contracts.py:64-163 (4 方法均 `async def` + `*,`) |
| `CoreClientProtocol`: 5 个方法 | ✅ | contracts.py:180-289 (insert_vector_documents/search_vector_store/upload_object/request_download_url/delete_vector_store_documents) |
| `QueryResult` dataclass (answer/sources/session_id/input_tokens/output_tokens) | ✅ | contracts.py:27-49 (5 字段,`field(default_factory=list)`) |
| 完整 type hints | ✅ | 全 12 方法参数+返回值均标注,`from __future__ import annotations` 启用 PEP 604 |
| 不含具体实现 | ✅ | 全 12 方法体为 `...`,仅 `QueryResult` 有 field 默认值(无逻辑) |
| `python -m py_compile` 通过 | ✅ | exit 0 |

## 7. References

- Issue: `repo/services/tasks/modules/issue/core/knowledge/issue-027-interface-kb-service-abstract-protocols.md`
- Plan: `repo/services/tasks/modules/plan/plan-rag-architecture-compliance-design.md` §3.5 (app/services 目录), 步骤 4 (RetrieveService), 步骤 5 (ParseOrchestrator), 步骤 8A (QueryOrchestrator), §3.3 (CoreClient), §3.4 (RagEngineClient)
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- CLAUDE.md: §4.1 (先改 API 契约再写实现)
- 前序记录: `repo/development-records/RAG-REFACTOR-STEP-2-CONTRACT.md` (rag-engine proto 契约,issue-025)
- 依赖: issue-024 (Core 契约), issue-025 (rag-engine 契约) — 本 issue 接口依赖契约定义的类型
