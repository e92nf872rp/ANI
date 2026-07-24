# SPEC: ani-gateway KB 路由与 SSE 端点 (P0)

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-016,017)
> - UX: N/A — backend-only
> Generated: 2026-07-23 | Target branch: main | Product line: core (Services / ani-gateway)

## 1. Summary

### 1.1 What This SPEC Covers
扩展 `repo/services/ani-gateway/`（Go + Hertz）：补齐 3 个缺失 KB 路由（citations/sessions/permissions）使 12 端点全部就位，用 gRPC client 替换 9 个 KB stub handler 路由到 kb-service，路由 `/api/v1/vector-stores/*` 与 `/api/v1/objects/*` 到 Core，实现 SSE 流式问答端点（gateway 持有，调 rag-engine 检索 + vLLM streaming 透传）。本 SPEC 同时定义 SSE 事件协议（回答 PRD+UX 的 Open Question）。

### 1.2 PRD Reference
- Source: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX source: N/A — backend-only（但 SSE 事件协议由本 SPEC 定义，供 Console 消费）
- User Stories covered: US-016, US-017
- Functional Requirements covered: FR-8

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| KB handler 实现 | gRPC client 调 kb-service（替换 stub） | PRD US-016 强制；gateway 不实现 KB 业务 |
| SSE 归属 | gateway 持有 `/query/stream` 端点 | PRD Non-Goals：rag-engine 仅同步 Query，SSE 在 gateway |
| SSE 实现 | 调 rag-engine 检索（同步 gRPC）+ 构造 prompt + 调 vLLM streaming（OpenAI 兼容 /v1/chat/completions stream）+ token 透传 + 末尾 sources 事件 | US-017 强制 |
| SSE 事件协议 | 定义 `token`/`sources`/`done`/`error` 四事件（见 §4.3） | 用户决策：在 SPEC 中定义，回答 PRD/UX Open Question |
| 路由表 | 12 端点全注册（9 KB + citations/sessions/permissions + SSE） | US-016 AC |
| Core 路由 | `/api/v1/vector-stores/*`、`/api/v1/objects/*` 反向代理 Core | US-016 AC |

---

## 2. Architecture

### 2.1 System Context
ani-gateway 是 API 网关，负责路由/RBAC/租户注入/限流/SSE。KB 端点路由到 kb-service（gRPC），Core 端点路由到 Core，SSE 端点在 gateway 持有并编排 rag-engine 检索 + vLLM 流式。

```
Console ──HTTP──→ ani-gateway ─┬─gRPC─→ kb-service (9 KB 端点 + 3 P1 UNIMPLEMENTED)
                              ├─proxy─→ Core (/vector-stores/*, /objects/*)
                              └─SSE──→ rag-engine 检索(gRPC) + vLLM streaming(透传)
```

### 2.2 Component Design
- **kb_resources.go** [MODIFY]：替换 9 个 stub 为 gRPC client 调用；补齐 3 个 P1 路由（citations/sessions/permissions）返回 501（kb-service P1 RPC UNIMPLEMENTED）
- **kb_grpc_client.go** [NEW]：grpcio 等价 Go gRPC client，连接 kb-service
- **kb_sse.go** [NEW]：SSE handler，编排 rag-engine 检索 + vLLM streaming + 事件输出
- **core_proxy.go** [NEW 或复用]：`/vector-stores/*`、`/objects/*` 反向代理 Core

### 2.3 Module Interactions
1. 9 KB 端点：gateway 收请求 → RBAC + 租户注入 → gRPC client 调 kb-service → 透传响应
2. 3 P1 端点：gateway → gRPC client → kb-service 返回 UNIMPLEMENTED → gateway 返回 501
3. Core 端点：gateway → 反向代理 Core（RBAC scope:vector-stores/objects）
4. SSE 端点：gateway → rag-engine Query（gRPC，取 sources）→ 构造 prompt → vLLM streaming → token 事件透传 → sources 事件 → done 事件

### 2.4 File Structure
```
repo/services/ani-gateway/
├── internal/router/
│   ├── kb_resources.go          [MODIFY: 替换 stub + 补 3 路由]
│   ├── kb_grpc_client.go        [NEW: kb-service gRPC client]
│   ├── kb_sse.go                [NEW: SSE handler]
│   └── core_proxy.go            [NEW 或复用: Core 反向代理]
├── internal/middleware/        [复用: RBAC/tenant/ratelimit]
└── main.go                     [MODIFY: 注册 gRPC client + Core proxy 路由]
```

---

## 3. Data Model

### 3.1 Schema Changes
无 gateway 数据库。SSE 事件协议 schema 见 §4.3。

### 3.2 Entity Definitions
N/A — gateway 不持久化。

### 3.3 Relationships
N/A。

### 3.4 Migration Plan
无。依赖 services/v1.yaml 契约修复（spec-services-kb-service.md US-001）与新增端点（US-002）。

---

## 4. API Design

### 4.1 Endpoints（12 KB 端点全路由）

| Method | Path | operationId | 路由目标 | 说明 |
|--------|------|-------------|----------|------|
| GET | `/api/v1/svc/knowledge-bases` | listKnowledgeBases | kb-service gRPC | 替换 stub |
| POST | `/api/v1/svc/knowledge-bases` | createKnowledgeBase | kb-service gRPC | 替换 stub |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}` | getKnowledgeBase | kb-service gRPC | 替换 stub |
| DELETE | `/api/v1/svc/knowledge-bases/{kb_id}` | deleteKnowledgeBase | kb-service gRPC | 替换 stub |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/documents` | listKnowledgeBaseDocuments | kb-service gRPC | 替换 stub |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/documents` | uploadKnowledgeBaseDocument | kb-service gRPC | 替换 stub（两步式） |
| DELETE | `/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}` | deleteKnowledgeBaseDocument | kb-service gRPC | 替换 stub |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/query` | queryKnowledgeBase | kb-service gRPC | 替换 stub |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/query/stream` | streamQueryKnowledgeBase | gateway SSE | 见 §4.3 |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/citations` | listKnowledgeBaseCitations | kb-service gRPC (P1) | 补齐路由 → 501 |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/sessions` | listKnowledgeBaseSessions | kb-service gRPC (P1) | 补齐路由 → 501 |
| PUT | `/api/v1/svc/knowledge-bases/{kb_id}/permissions` | updateKnowledgeBasePermissions | kb-service gRPC (P1) | 补齐路由 → 501 |

新增 US-002 端点（由 kb-service 承接）：
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}/reparse` | reparseKnowledgeBaseDocument | kb-service gRPC |
| GET/PUT | `/api/v1/svc/knowledge-bases/{kb_id}/config` | get/updateKnowledgeBaseConfig | kb-service gRPC |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/rebuild` | rebuildKnowledgeBase | kb-service gRPC |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/models` | listKnowledgeBaseModels | kb-service gRPC |

Core 代理：
| * | `/api/v1/vector-stores/*` | — | Core 反向代理 |
| * | `/api/v1/objects/*` | — | Core 反向代理 |

### 4.2 Request/Response Schemas
透传 kb-service gRPC 响应，由 services/v1.yaml 定义。SSE 见 §4.3。

### 4.3 SSE 事件协议（本 SPEC 定义）

**端点：** `GET /api/v1/svc/knowledge-bases/{kb_id}/query/stream`

**查询参数：**

| 参数 | 位置 | 必填 | 约束 |
|------|------|------|------|
| `kb_id` | path | yes | uuid |
| `question` | query | yes | 1-2000 字符 |
| `session_id` | query | no | uuid |
| `top_k` | query | no | 1-20，默认 5 |
| `score_threshold` | query | no | 0.0-1.0，默认 0.3 |
| `inference_service_name` | query | no | 默认 "default" |

**响应：** `200 + text/event-stream`，UTF-8，`Cache-Control: no-cache`，`Connection: keep-alive`

**事件序列：**
```
event: token
data: {"delta":"..."}

event: token
data: {"delta":"..."}

event: sources
data: [{"doc_id":"...","file_name":"...","page":1,"content":"...","score":0.87}, ...]

event: done
data: {"session_id":"...","input_tokens":123,"output_tokens":456}
```

**事件类型：**

| event | data | 触发 | 说明 |
|-------|------|------|------|
| `token` | `{"delta":"<增量文本>"}` | vLLM stream 每个 chunk | 多次发送，前端逐字追加 |
| `sources` | `SourceChunk[]`（同 KBQueryResponse.sources） | rag-engine 检索完成后、流式开始前或结束时 | 引用卡片渲染；本 SPEC 规定在流结束后发送（简化前端处理） |
| `done` | `{"session_id":"...","input_tokens":N,"output_tokens":N}` | vLLM stream 结束 | 正常结束，前端恢复输入 |
| `error` | `{"code":"...","message":"..."}` | 任意阶段失败 | 异常结束，前端标记中断 |

**错误事件 code：**

| code | HTTP（首部） | 说明 |
|------|-------------|------|
| `BAD_REQUEST` | 400 | question 非法/缺失 |
| `UNAUTHORIZED` | 401 | 未认证 |
| `KB_NOT_FOUND` | 404 | kb_id 不存在 |
| `INFERENCE_UNAVAILABLE` | 503 | vLLM 不可用 |
| `RETRIEVE_FAILED` | 503 | rag-engine 检索失败 |
| `STREAM_INTERRUPTED` | 500 | 流式中断 |

> 注：HTTP 状态码在响应首部即确定；错误事件用于流中段失败（此时首部已 200）。首部 400/401/404 不进入流，直接返回 JSON 错误。

**错误处理：**
- 流开始前错误（400/401/404）：返回标准 JSON 错误，不进入 SSE
- 检索失败：发送 `event: error` + `data: {"code":"RETRIEVE_FAILED",...}` + 关闭流
- vLLM 中断：发送 `event: error` + `data: {"code":"STREAM_INTERRUPTED",...}` + 关闭流

### 4.4 Error Responses
非流式错误沿用 Core/Services 统一错误 shape：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`。

### 4.4 Breaking Changes
无。替换 stub 为真实实现；补齐 3 个 P1 路由（此前 services/v1.yaml 已声明，gateway 未注册）。需 `make validate-architecture` + OpenAPI/Gateway route contract 校验通过。

---

## 5. Business Logic

### 5.1 Core Algorithms

**KB gRPC handler（通用）：**
```
1. middleware: RBAC + 租户注入 + 限流
2. 解析路径/请求体 → 构造 gRPC request
3. grpc_client.Call(req)  // 含超时
4. gRPC status → HTTP 映射（NOT_FOUND=404, INVALID_ARGUMENT=400, UNIMPLEMENTED=501, FAILED_PRECONDITION=409, UNAVAILABLE=503）
5. 返回 JSON
```

**SSE handler：**
```
1. 校验 question（空 → 400 JSON）；认证（401 JSON）；kb 存在性可由检索阶段返回
2. 设置 SSE 首部：Content-Type: text/event-stream, Cache-Control: no-cache, Connection: keep-alive
3. rag-engine Query(gRPC) 取 sources  // 同步检索
   失败 → event: error RETRIEVE_FAILED + 关闭
4. 构造 prompt（sources + question + 历史可选）
5. vLLM POST /v1/chat/completions stream=true  // OpenAI 兼容
6. 透传 SSE：
   for chunk in vllm_stream:
       delta = chunk.choices[0].delta.content
       if delta: write "event: token\ndata: {\"delta\":\"...\"}\n\n"
   失败 → event: error STREAM_INTERRUPTED + 关闭
7. write "event: sources\ndata: <json sources>\n\n"
8. write "event: done\ndata: {\"session_id\":\"...\",\"input_tokens\":N,\"output_tokens\":N}\n\n"
9. flush + 关闭
```

### 5.2 Validation Rules
- question 1-2000
- top_k 1-20
- score_threshold 0.0-1.0
- kb_id uuid

### 5.3 State Machine
SSE 流状态：`init → retrieving → streaming → emitting_sources → done`（或 `init → error` / `streaming → error`）。

### 5.4 Edge Cases
- 客户端断开连接：gateway 检测到 writer 关闭，取消 vLLM stream（context cancel）
- 检索无命中（max_score < threshold）：sources=[]，仍走 LLM（让 LLM 回答无依据或拒答，由 prompt 控制）；或直接 done（实现时确认，P0 建议 sources=[] + done）
- vLLM 首字前失败：error STREAM_INTERRUPTED
- idempotency_key：SSE 端点不强制（GET），但前端可传 session_id 复用会话

---

## 6. Error Handling

### 6.1 Error Taxonomy
| Error Code | HTTP | Condition | User Message |
|------------|------|-----------|--------------|
| `BAD_REQUEST` | 400 | question 非法 | — |
| `UNAUTHORIZED` | 401 | 未认证 | — |
| `KB_NOT_FOUND` | 404 | kb_id 不存在 | — |
| `KB_REBUILDING` | 409 | KB 重建中 | 全库重建进行中 |
| `INFERENCE_UNAVAILABLE` | 503 | vLLM 不可用 | 推理服务暂不可用 |
| `RETRIEVE_FAILED` | 503 | rag-engine 检索失败 | 检索失败 |
| `STREAM_INTERRUPTED` | 500 | 流式中断 | 流式响应中断 |

### 6.2 Retry Strategy
- gRPC client 调 kb-service：超时/UNAVAILABLE 不自动重试，返回 503
- SSE 流中断：不自动重试，前端可发起新请求

### 6.3 Failure Modes
- kb-service 不可用：503
- rag-engine 检索不可用：SSE error RETRIEVE_FAILED
- vLLM 不可用：SSE error INFERENCE_UNAVAILABLE（若首字前）或 STREAM_INTERRUPTED（流中）

---

## 7. Security

### 7.1 Authentication & Authorization
- 现有 gateway RBAC：KB 端点对应 scope（待 services RBAC scope 定义，P0 复用 Services 认证）
- SSE 端点：token 透传（PRD US-017 AC：SSE token 透传），认证同其他 KB 端点
- 租户注入：middleware.GetTenantID 注入 gRPC metadata

### 7.2 Input Validation
- question 长度/必填
- 路径参数 uuid 校验
- SSE 响应内容不回显用户敏感信息

### 7.3 Data Protection
- SSE 流不持久化
- vLLM 调用经服务账号
- sources 不含跨租户数据（由 rag-engine 租户过滤保证）

---

## 8. Performance

### 8.1 Expected Load
- KB 端点：中频
- SSE：并发流数取决于问答活跃度，单流持续 2-10s

### 8.2 Optimization Strategy
- gRPC 连接复用（keepalive）
- SSE 流式透传（不缓冲完整响应）
- vLLM stream 透传用 io.Pipe / flush 立即发送

### 8.3 Database Considerations
无 gateway DB。

---

## 9. Testing Strategy

### 9.1 Unit Tests
- kb_grpc_client：mock kb-service，断言请求/响应映射 + status 映射
- kb_sse：mock rag-engine + mock vLLM stream，断言事件序列（token*→sources→done）
- kb_sse 错误：检索失败 → error RETRIEVE_FAILED；vLLM 中断 → error STREAM_INTERRUPTED
- core_proxy：路由转发 + RBAC scope

### 9.2 Integration Tests
- 12 端点全注册验证（route table）
- 9 KB 端点 gRPC 透传（kb-service mock）
- 3 P1 端点返回 501
- SSE 端到端：rag-engine mock + vLLM mock → 完整事件序列
- Core 代理：vector-stores/objects 转发

### 9.3 Edge Case Tests
- 客户端断开 → vLLM stream 取消
- 检索无命中 → sources=[] + done
- 流中 vLLm 错误 → error + 关闭
- 409 kb.rebuilding 透传

### 9.4 Acceptance Criteria Mapping
| US/FR | Test | Type | Description |
|-------|------|------|-------------|
| US-016 AC1 | test_12_endpoints_registered | integration | 12 端点全就位 |
| US-016 AC2 | test_9_stubs_replaced | integration | 9 stub 替换为 gRPC |
| US-016 AC3 | test_kb_routes_to_kb_service | integration | /svc/knowledge-bases/* → kb-service |
| US-016 AC4 | test_vector_stores_route | integration | /vector-stores/* → Core |
| US-016 AC5 | test_objects_route | integration | /objects/* → Core |
| US-016 AC6 | test_rbac_tenant_ratelimit | integration | RBAC + 租户 + 限流 |
| US-016 AC7 | make validate-architecture | gate | — |
| US-016 AC8 | make test | gate | — |
| US-017 AC1 | test_sse_endpoint_held | integration | SSE 在 gateway |
| US-017 AC2 | test_sse_retrieve_and_stream | integration | rag-engine 检索 + vLLM streaming |
| US-017 AC3 | test_sse_sources_event | integration | 末尾 sources 事件 |
| US-017 AC4 | test_sse_error_handling | integration | 400/401 处理 |
| US-017 AC5 | make test | gate | — |
| FR-8 | test_sync_and_sse | integration | 同步 + SSE |

---

## 10. Implementation Plan

### 10.1 Phases
1. 注册 3 缺失路由（citations/sessions/permissions）→ 501
2. kb_grpc_client + 替换 9 stub
3. core_proxy 路由 vector-stores/objects
4. kb_sse handler + 事件协议
5. 测试 + `make validate-architecture` + `make test`

### 10.2 Issue Mapping
| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| US-016 | 2, 4, 5, 9 | high | spec-services-kb-service US-008 |
| US-017 | 2, 4.3, 5, 9 | high | spec-services-rag-engine US-014,015 |

### 10.3 Incremental Delivery
路由注册与 stub 替换可先于 SSE 落地；SSE 依赖 rag-engine Query 与 vLLM 就绪。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- SSE `sources` 事件位置（流前 vs 流后）：本 SPEC 规定流后，实现时可调整（PRD Open Question 3，SSE 事件协议统一）
- 检索无命中时是否仍调 LLM：P0 建议调（让 LLM 拒答），实现时按 prompt 策略确认
- idempotency_key 在 SSE（GET）的传递方式（query 参数或忽略）：P0 不强制

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| vLLM streaming 透传 flush 延迟 | medium | 用 Hertz streaming writer + 立即 flush |
| gRPC client 连接管理 | low | keepalive + 重连 |
| SSE 事件协议前端兼容 | medium | 本 SPEC 与 Console SPEC 对齐，实现时联调 |

### 11.3 Assumptions
- kb-service gRPC server 已就绪（spec-services-kb-service US-008）
- rag-engine Query gRPC 已就绪（spec-services-rag-engine US-015）
- vLLM OpenAI 兼容 /v1/chat/completions stream 可用
- services/v1.yaml 12 端点 + US-002 新增端点已声明
- 现有 gateway middleware（RBAC/tenant/ratelimit）可复用
