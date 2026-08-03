# SPEC: Console 知识库平台前端 (P0)

> Technical specification derived from:
> - PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-019,020,021,022)
> - UX: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md`
> Generated: 2026-07-23 | Target branch: main | Product line: console
>
> Scope: **only** `repo/frontends/console/` | Source of truth: consume OpenAPI — no backend changes in UI-only batch

## 1. Summary

### 1.1 What This SPEC Covers
Console 前端知识库平台 P0：列表页 + 单库 3 tab（概览/文档/问答）+ 4 P1 占位页。消费 `services/v1.yaml` 修复后契约（spec-services-kb-service.md US-001/002）与 ani-gateway SSE 事件协议（spec-services-ani-gateway-kb.md §4.3）。不在本批次做后端改动。

### 1.2 PRD Reference
- Source: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
- UX source: `repo/services/tasks/modules/ux/console/knowledge/ux-console-knowledge-base-platform.md`
- User Stories covered: US-019, US-020, US-021, US-022（前端侧）
- Functional Requirements covered: FR-16, FR-17, FR-18

### 1.3 Design Decisions Summary
| Decision | Choice | Rationale |
|----------|--------|-----------|
| 路由方案 | TanStack Router 文件路由，`__root.tsx` 改 tab 布局壳 | UX §1.2 + 现有栈 |
| 数据获取 | `@tanstack/react-query` + `openapi-fetch`（现有 `api` client） | UX §8.4 假设 |
| UI 库 | TDesign React 现有依赖 | UX §8.4 |
| SSE 消费 | 浏览器原生 EventSource + 按 spec-services-ani-gateway-kb.md §4.3 事件协议解析 | US-021 + 本 SPEC 跨文档契约 |
| 字段命名 | 以 US-001 修复后契约为准（`parse_status` 非 `status`） | UX §5 + spec-services-kb-service US-001 |
| 现有文件改造 | `kb/index.tsx` 增强、`kb/$kbId/chat.tsx` 扩展多会话+SSE | UX §1.2 |

---

## 2. Architecture

### 2.1 System Context
Console 前端经 ani-gateway 消费 Services API（`/api/v1/svc/knowledge-bases/*`）与 SSE（`/query/stream`）。不直接调 kb-service/rag-engine/Core。

```
Console ─HTTP/SSE─→ ani-gateway ─→ kb-service / rag-engine / Core
```

### 2.2 Component Design
（映射 UX §5 组件表，落地为文件）

- **列表页** `routes/kb/index.tsx` [MODIFY]：增强为表格+新建 Dialog+删除+空态
- **详情 tab 壳** `routes/kb/$kbId/__root.tsx` 或 `routes/kb/$kbId/route.tsx` [NEW]：面包屑+Tabs(3 激活+4 占位)+Outlet
- **概览 tab** `routes/kb/$kbId/overview.tsx` [NEW]：入库配置 Form + 问答配置 Form + 重建 + P1 规划
- **文档 tab** `routes/kb/$kbId/documents.tsx` [NEW]：表格+上传拖拽+元数据 Dialog+解析详情 Drawer+重试
- **问答 tab** `routes/kb/$kbId/chat.tsx` [MODIFY]：扩展多会话+TopK+SSE+引用卡片父块展开
- **P1 占位** `routes/kb/$kbId/{data-ingestion,lab,permissions,history}.tsx` [NEW]：Empty 占位

### 2.3 Module Interactions
- 列表 → 详情：行「进入」→ `/kb/$kbId/overview`
- 概览重建 → `POST /rebuild` → KB status=rebuilding → 文档/问答页禁用写操作
- 文档上传 → 两步式（`GET upload URL` → `PUT MinIO` → `POST notify`）
- 问答同步 → `POST /query`；流式 → `EventSource /query/stream` 按事件协议解析

### 2.4 File Structure
```
repo/frontends/console/src/routes/kb/
├── index.tsx                          [MODIFY: 列表增强]
├── $kbId/
│   ├── route.tsx                      [NEW: tab 布局壳 + 面包屑 + 删除]
│   ├── overview.tsx                   [NEW: 概览]
│   ├── documents.tsx                  [NEW: 文档]
│   ├── chat.tsx                       [MODIFY: 多会话+SSE]
│   ├── data-ingestion.tsx             [NEW: P1 占位]
│   ├── lab.tsx                        [NEW: P1 占位]
│   ├── permissions.tsx                [NEW: P1 占位]
│   └── history.tsx                    [NEW: P1 占位]
└── (shared: api hooks 可按需抽到 src/api/kb.ts)
```

> 注：`__root.tsx`（应用壳）已存在不动；详情 tab 壳用 `$kbId/route.tsx`（TanStack Router parentRoute 模式）。

---

## 3. Data Model

### 3.1 Schema Changes
无前端 DB。消费契约 schema：

### 3.2 Entity Definitions（TypeScript，对齐 US-001 修复后 services/v1.yaml）

```ts
type KnowledgeBase = {
  id: string
  name: string
  description?: string
  embedding_model?: string
  chunk_size?: number
  top_k?: number
  score_threshold?: number
  status: 'active' | 'rebuilding' | 'deleted'
  doc_count: number
  created_at: string
}

type KBDocument = {
  id: string
  knowledge_base_id: string
  file_name: string
  content_type: string | null
  size_bytes: number
  parse_status: 'pending' | 'parsing' | 'indexing' | 'ready' | 'failed'  // US-001 修复后
  custom_metadata?: Record<string, unknown>
  error_message?: string
  created_at: string
  parsed_at?: string
}

type KBSource = {
  doc_id: string
  file_name: string
  page: number
  content: string
  parent_content?: string  // 父块上下文（引用卡片展开）
  score: number
}

type KBQueryResponse = {
  answer: string
  sources: KBSource[]
  session_id: string
  input_tokens: number
  output_tokens: number
}
```

### 3.3 Relationships
N/A。

### 3.4 Migration Plan
无。依赖 services/v1.yaml 契约修复（spec-services-kb-service US-001）与新增端点（US-002）落地。

---

## 4. API Design

### 4.1 Endpoints（消费，不实现）

| Method | Path | 用途 | 关联 US |
|--------|------|------|---------|
| GET | `/api/v1/svc/knowledge-bases` | 列表 | US-019 |
| POST | `/api/v1/svc/knowledge-bases` | 新建 | US-019 |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}` | 详情 | US-019/020 |
| DELETE | `/api/v1/svc/knowledge-bases/{kb_id}` | 删除 | US-019 |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/documents` | 文档列表 | US-020 |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/documents` | 上传（两步式） | US-020 |
| DELETE | `/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}` | 删文档 | US-020 |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/documents/{doc_id}/reparse` | 重试 | US-020 / FR-18 |
| GET/PUT | `/api/v1/svc/knowledge-bases/{kb_id}/config` | 配置 | US-020 |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/rebuild` | 重建 | US-020 / FR-17 |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/models` | 模型列表 | US-020 |
| POST | `/api/v1/svc/knowledge-bases/{kb_id}/query` | 同步问答 | US-021 |
| GET | `/api/v1/svc/knowledge-bases/{kb_id}/query/stream` | SSE 问答 | US-021 |

> 两步式上传具体端点名以 US-001 修复后 services/v1.yaml operationId 为准（`getDocumentUploadURL` + `notifyDocumentUploaded`）。

### 4.2 Request/Response Schemas
见 §3.2 + services/v1.yaml。

### 4.3 SSE 事件消费（对齐 spec-services-ani-gateway-kb.md §4.3）

前端用 `EventSource`，按 `event` 字段分发：

```ts
const es = new EventSource(`/api/v1/svc/knowledge-bases/${kbId}/query/stream?question=...&session_id=...&top_k=...`)
es.addEventListener('token', e => appendDelta(JSON.parse(e.data).delta))
es.addEventListener('sources', e => renderSources(JSON.parse(e.data)))
es.addEventListener('done', e => finish(JSON.parse(e.data)))  // {session_id, input_tokens, output_tokens}
es.addEventListener('error', e => {
  // EventSource 原生 error 无 data；需通过自定义 error 事件（见 gateway SPEC）
  // 实现时若 gateway 发送 event: error，用 addEventListener('error', ...) 解析 data
  handleError(JSON.parse(e.data))
})
```

事件契约：`token`（多次，增量 delta）、`sources`（引用数组）、`done`（结束元数据）、`error`（{code,message}）。

### 4.4 Error Responses
统一错误 shape：`{"code":"UPPER_SNAKE","message":"...","request_id":"..."}`。409 `KB_REBUILDING` → 禁用写操作 + Message.warning。

---

## 5. Business Logic

### 5.1 Core Algorithms

**两步式上传（US-020）：**
```
1. 用户拖拽文件 → 校验格式(.pdf,.docx,.xlsx,.pptx,.md,.txt) + 大小(≤100MB) → 弹元数据 Dialog
2. 用户填 key-value 元数据 → 确认
3. GET upload URL（file_name/file_type/file_size/checksum_sha256/idempotency_key）→ 得 doc_id + upload_url
4. PUT upload_url（MinIO 直传）
5. POST notify（kb_id/doc_id/storage_path + custom_metadata）
6. 表格新行（parse_status=pending）+ Message.success「文档已上传，解析中」
```

**概览重建（US-020 / FR-17）：**
```
1. 用户改 embedding/chunk_size → 保存 → Popconfirm「修改配置将触发全库重建，确认？」
2. 确认 → PUT /config → POST /rebuild → KB status=rebuilding
3. 全局标记 kb.rebuilding → 文档/问答页写操作禁用
4. Message.success「重建任务已提交」
```

**文档解析重试（US-020 / FR-18）：**
```
failed 行 → 点击重试 → Popconfirm「重新解析将覆盖现有分块，确认？」→ POST /reparse → status 回 parsing
```

**问答 SSE（US-021）：**
```
1. 用户输入 → 选择同步(Enter)或流式(Switch)
2. 同步：POST /query → 追加 assistant 卡片 + 引用卡片
3. 流式：EventSource → 逐 token 追加 → sources 事件渲染引用 → done 恢复输入
4. TopK 可调（InputNumber 1-20）
5. 引用卡片展开父块上下文（parent_content）
```

### 5.2 Validation Rules
- 名称必填
- TopK 1-20
- score_threshold 0-1（step 0.1）
- 文件格式白名单 + ≤100MB
- question 非空才能发送

### 5.3 State Machine
见 UX §6（列表/概览/文档/问答各页 state 表）。关键态：idle/loading/empty/error/creating/saving/rebuilding/parsing/indexing/ready/failed/streaming-*。

### 5.4 Edge Cases
- KB 不存在（404）→ 整页 Empty「知识库不存在或已删除」
- KB rebuilding（409）→ 写操作禁用 + Alert
- KB deleted（status=deleted）→ 写操作禁用 + Tag
- SSE 流中断 → 卡片标记「[流式中断]」
- 解析详情 Drawer 加载失败 → Drawer 内 Message.error
- P1 tab 点击 → Empty「该能力在 P1 规划中，暂不可用」

---

## 6. Error Handling

### 6.1 Error Taxonomy
| code | UI 行为 | Copy |
|------|---------|------|
| `KB_NOT_FOUND` | 整页 Empty | 知识库不存在或已删除 |
| `KB_REBUILDING` | 写操作禁用 + Message.warning | 全库重建进行中，请稍后 |
| `DOC_UNSUPPORTED_TYPE` | Message.error | 不支持的文档格式 |
| `DOC_TOO_LARGE` | Message.error | 文件大小超限（上限 100MB） |
| `DOC_CHECKSUM_MISMATCH` | Message.error | 校验和不匹配 |
| `INFERENCE_UNAVAILABLE` | Message.error | 推理服务暂不可用 |
| `STREAM_INTERRUPTED` | 卡片标记 + Message.error | 流式响应中断 |
| 通用 5xx | Message.error | {error.message} |

### 6.2 Retry Strategy
- 列表/文档加载失败：重试链接
- 上传/创建/删除失败：操作可重试
- SSE 中断：用户重新发送

### 6.3 Failure Modes
- 网络错误：Message.error + 保持当前状态
- SSE 连接失败：Message.error + 输入恢复

---

## 7. Security

### 7.1 Authentication & Authorization
- 路由级 auth（现有 Console 模式）
- P0 无 KB 级权限 UI（P1 占位）
- 请求带现有 token（`api` client 注入）

### 7.2 Input Validation
- 文件格式/大小前端校验
- question 长度 1-2000
- custom_metadata key-value 非空校验

### 7.3 Data Protection
- presigned URL 不经服务端转发，客户端直传 MinIO
- 不在前端存储敏感数据

---

## 8. Performance

### 8.1 Expected Load
- 列表/文档分页
- SSE 单流持续 2-10s

### 8.2 Optimization Strategy
- react-query 缓存 + staleTime
- 文档表格分页/筛选本地态
- SSE 增量渲染（逐字追加，不重渲染整列表）

### 8.3 Database Considerations
无前端 DB。

---

## 9. Testing Strategy

### 9.1 Unit Tests
- 列表页：columns/空态/新建 Dialog/删除
- 概览：Form 校验 + 重建 Popconfirm
- 文档：上传流程 + 元数据表单 + 解析详情 Drawer
- 问答：多会话切换 + TopK + SSE 事件解析（mock EventSource）

### 9.2 Integration Tests
- 列表 → 详情 → 各 tab 路由
- 两步式上传 mock
- 重建 409 禁用写操作
- SSE mock 事件序列（token*→sources→done）

### 9.3 Edge Case Tests
- 空态/错误态/KB not found/rebuilding/deleted
- SSE 中断
- P1 占位

### 9.4 Acceptance Criteria Mapping
| US/FR | Test/Verify | Type | Description |
|-------|-------------|------|-------------|
| US-019 AC1 | test_kb_list_page | unit/integration | 列表表格 |
| US-019 AC2 | test_create_dialog | unit | 新建模态框 |
| US-019 AC3 | test_tab_layout | integration | __root 3 tab 布局 |
| US-019 AC4 | test_parent_route | integration | parentRoute 改造 |
| US-019 AC5 | test_p1_placeholders | integration | 4 占位页 |
| US-019 AC6 | typecheck/lint | gate | — |
| US-019 AC7 | browser verify | manual | 列表/空态/错误 |
| US-020 AC1 | test_overview_config | unit | 入库+问答配置+P1 区 |
| US-020 AC2 | test_rebuild_trigger | integration | 改 Embedding/chunk_size→重建 |
| US-020 AC3 | test_document_upload | integration | 拖拽+元数据 |
| US-020 AC4 | test_status_filter_retry | unit | 筛选+重试 |
| US-020 AC5 | test_parse_detail_drawer | unit | 父子块+摘要+metadata |
| US-020 AC6 | typecheck/lint | gate | — |
| US-020 AC7 | browser verify | manual | 上传/空态/错误/解析详情 |
| US-021 AC1 | test_chat_sessions | unit | 会话列表+消息流 |
| US-021 AC2 | test_sync_and_sse | integration | 同步+SSE |
| US-021 AC3 | test_topk_adjust | unit | TopK 调节 |
| US-021 AC4 | test_citation_card | unit | 引用+父块展开 |
| US-021 AC5 | typecheck/lint | gate | — |
| US-021 AC6 | browser verify | manual | loading/空态/错误/SSE 增量 |
| US-022 AC1-14 | e2e + live gate | e2e | 端到端联调（依赖后端 SPEC 全部就绪） |
| FR-16 | test_list_and_tabs | integration | 列表+3tab+占位 |
| FR-17 | test_rebuild | integration | 配置改触发重建 |
| FR-18 | test_reparse | integration | 解析重试 |

---

## 10. Implementation Plan

### 10.1 Phases
1. 列表页增强（US-019）
2. 详情 tab 壳 + P1 占位（US-019）
3. 概览 tab（US-020）
4. 文档 tab（US-020）
5. 问答 tab 扩展（US-021）
6. 端到端联调（US-022，依赖后端 SPEC）

### 10.2 Issue Mapping
| Issue | SPEC Sections | Priority | Depends On |
|-------|--------------|----------|------------|
| US-019 | 2, 5, 9 | high | spec-services-kb-service US-001/002 |
| US-020 | 2, 5, 9 | high | US-019 |
| US-021 | 2, 4.3, 5, 9 | high | spec-services-ani-gateway-kb US-017 |
| US-022 | 9.4 | high | 全部后端 SPEC |

### 10.3 Incremental Delivery
列表+tab 壳可先于后端就绪（mock 数据）；SSE 依赖 gateway 端点；端到端联调为最后关卡。

---

## 11. Open Questions & Risks

### 11.1 Unresolved Questions
- 两步式上传 operationId 以 US-001 修复后契约为准（实现时对齐）
- SSE `error` 事件在 EventSource 中的解析方式（浏览器原生 error 事件无 data；需 gateway 发送 `event: error` + data，前端用 `addEventListener('error', ...)` 读取，实现时联调验证）
- 全库重建期间写操作禁用范围（PRD Open Question：P0 建议禁上传/删除/重试，允许查询）
- `doc_count` 是否拆分可检索/总（PRD Open Question：P0 维持单一 doc_count）

### 11.2 Technical Risks
| Risk | Impact | Mitigation |
|------|--------|-----------|
| EventSource 自定义 error 事件兼容性 | medium | 实现时验证 addEventListener('error') 读 data；必要时降级 fetch ReadableStream |
| 契约字段漂移（parse_status） | medium | 依赖 US-001 契约修复先落地 + openapi-fetch 类型生成 |
| parent_content 在 sources 是否返回 | medium | 需 rag-engine retrieve 回填（spec-services-rag-engine US-014）；契约需补 parent_content 字段（US-001 待补） |

### 11.3 Assumptions
- 复用现有 Console `__root.tsx` 应用壳（Header+Aside+Content）
- 侧边栏「知识库」菜单已存在
- `@tanstack/react-query` + `openapi-fetch` + TDesign React 现有依赖
- Upload accept: `.pdf,.docx,.xlsx,.pptx,.md,.txt`，大小上限 100MB
- SSE 事件协议遵循 spec-services-ani-gateway-kb.md §4.3
- 字段命名遵循 spec-services-kb-service US-001 修复后契约（`parse_status`）
- `KBSource.parent_content` 字段需在 services/v1.yaml `KBQueryResponse.sources` 中补齐（US-001 待补项）
