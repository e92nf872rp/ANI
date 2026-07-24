# UX: Console 知识库平台

> Interaction specification derived from: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md`
> Part of ani-workflow artifact triad — next: `/prd-to-spec`
> Generated: 2026-07-23 | Product: Console | UI stack: TDesign React + TanStack Router

---

## 1. Page Type

### 1.1 Classification

| Screen | Page type | In app shell? | Route |
|--------|-----------|---------------|-------|
| 知识库列表 | list | yes | `/kb` |
| 知识库详情（3-tab 布局壳） | detail (tab shell) | yes | `/kb/$kbId` |
| 概览 tab | detail (info panel) | yes | `/kb/$kbId/overview` |
| 文档 tab | list + upload + drawer | yes | `/kb/$kbId/documents` |
| 问答 tab | chat (split view) | yes | `/kb/$kbId/chat` |
| 数据接入（P1 占位） | placeholder | yes | `/kb/$kbId/data-ingestion` |
| 检索实验室（P1 占位） | placeholder | yes | `/kb/$kbId/lab` |
| 权限管理（P1 占位） | placeholder | yes | `/kb/$kbId/permissions` |
| 操作历史（P1 占位） | placeholder | yes | `/kb/$kbId/history` |

### 1.2 Pattern Reference

- **列表页**：镜像现有 `repo/frontends/console/src/routes/models/index.tsx`（顶栏标题+操作按钮 + `Table` + `Tag` 状态列 + 行操作 `Link`）与现有 `repo/frontends/console/src/routes/kb/index.tsx`（已存在基础骨架，需增强）
- **详情 3-tab**：TanStack Router 文件路由级导航，`__root.tsx` 作为 tab 布局壳，子路由各占一个 tab；P1 占位页采用同构路由
- **文档上传**：复用 TDesign `Upload` 拖拽模式 + `Dialog` 元数据表单
- **问答**：现有 `repo/frontends/console/src/routes/kb/$kbId/chat.tsx` 为同步单会话基础，需扩展为多会话 + SSE 流式

---

## 2. Information Architecture

### 2.1 Routes & Entry Points

| Route | Entry | Auth required |
|-------|-------|---------------|
| `/kb` | 侧边栏「知识库」菜单（`__root.tsx` Menu 已存在） | yes |
| `/kb/$kbId/overview` | 列表行「进入」操作 → 默认重定向到此 tab | yes |
| `/kb/$kbId/documents` | 详情页 tab 导航 | yes |
| `/kb/$kbId/chat` | 列表行「问答」操作（现有）/ 详情页 tab 导航 | yes |
| `/kb/$kbId/data-ingestion` | 详情页 tab 导航（P1 占位） | yes |
| `/kb/$kbId/lab` | 详情页 tab 导航（P1 占位） | yes |
| `/kb/$kbId/permissions` | 详情页 tab 导航（P1 占位） | yes |
| `/kb/$kbId/history` | 详情页 tab 导航（P1 占位） | yes |

### 2.2 Navigation Relationship

- 侧边栏一级菜单「知识库」（`__root.tsx` `Menu.MenuItem value="kb"` 已存在，`BookIcon`）→ `/kb` 列表
- 列表行操作进入 `/kb/$kbId/overview`，面包屑：`知识库 > {kb.name}`
- 详情页顶部 3 个激活 tab（概览/文档/问答）+ 4 个 P1 占位 tab（灰色，点击展示「P1 规划中」提示）
- 从列表「问答」直达 `/kb/$kbId/chat`（兼容现有快捷入口）

### 2.3 PRD Coverage Map

| PRD item | Screen / section |
|----------|------------------|
| US-019 列表页与 3 tab 布局 | §1 列表页、§1 详情 tab 壳、§3.1 主流程步 1-2 |
| US-019 P1 占位页 | §1 占位页、§3.2 次流程 |
| US-020 概览页（入库/问答配置 + 重建） | §3.1 主流程步 3、§4 概览布局、§5 概览组件 |
| US-020 文档页（上传+元数据+状态筛选+重试+解析详情） | §3.1 主流程步 4-5、§4 文档布局、§5 文档组件 |
| US-021 问答页（多会话+同步/流式+TopK+引用卡片） | §3.1 主流程步 6-7、§4 问答布局、§5 问答组件、§6 问答状态 |
| FR-16 列表+3tab+占位 | §1 全部 |
| FR-17 概览配置改触发重建 | §3.1 主流程步 3、§5 概览重建按钮 |
| FR-18 文档解析重试 | §5 文档重试按钮、§7 消息 |
| US-001/002 后端契约修复（间接） | §5 字段名以修复后 proto/OpenAPI 为准 |

---

## 3. User Flow

### 3.1 Primary Flow

```text
1. 用户点击侧边栏「知识库」 → 进入 /kb 列表页
   → 系统展示知识库表格（name/status/doc_count/created_at）
   → 若无数据：Empty 插图 + 「新建知识库」主按钮

2. 用户点击「新建知识库」按钮 → 弹出 Dialog（名称/描述/嵌入模型/chunk_size/top_k）
   → 填写并确认 → POST /knowledge-bases（含 idempotency_key）
   → 成功：Message.success + 表格刷新 → 新行高亮
   → 失败：Message.error + Dialog 保持

3. 用户点击行「进入」操作 → /kb/$kbId/overview 概览 tab
   → 系统展示入库配置区（Embedding/chunk_size/OCR）+ 问答配置区（TopK/score_threshold/检索策略）+ P1 规划区
   → 用户修改 Embedding/chunk_size 并保存 → 弹 Popconfirm「修改配置将触发全库重建，确认？」
   → 确认 → POST /rebuild → Tag 变 rebuilding → Message.success「重建任务已提交」

4. 用户切换到「文档」tab → /kb/$kbId/documents
   → 系统展示文档表格（file_name/status/size_bytes/created_at + 行操作）
   → 用户拖拽文件到上传区 → 弹 Dialog「元数据填写」（每文件单独：文件名只读 + key-value 元数据表单）
   → 填写并确认 → 两步式上传（GET upload URL → PUT MinIO → POST notify）→ 新行出现，status=uploaded/pending

5. 文档异步解析中：表格行 status Tag 实时更新（uploaded/pending → parsing → indexing → ready/failed）
   → failed 行展示「重试」按钮 → 点击 POST /reparse → status 回到 parsing
   → 用户点击行「解析详情」→ Drawer 展示父子块层级 + 摘要 + metadata

6. 用户切换到「问答」tab → /kb/$kbId/chat
   → 系统左侧展示会话列表（GET /sessions）+ 顶部「新建会话」按钮
   → 右侧默认展示当前/最近会话消息流
   → 用户在底部输入框输入问题 → 选择同步发送（Enter）或流式发送（切换开关 → GET /query/stream）

7. 发送后：
   → 同步：按钮 loading + 「AI 思考中…」 → 返回 answer + sources → 消息流追加 assistant 卡片 + 引用卡片
   → 流式：按钮 loading + 「AI 思考中…」 → SSE 逐 token 追加到 assistant 卡片 → 末尾 sources 事件 → 引用卡片渲染
   → TopK 可在右侧顶部调节（InputNumber 1-20）
   → 引用卡片展示子块内容 + 父块上下文（可展开/收起）
```

### 3.2 Secondary Flows

```text
- 删除知识库：列表行「删除」操作 → Popconfirm「删除后不可恢复，确认？」→ DELETE → 表格移除
- 删除文档：文档行「删除」操作 → Popconfirm → DELETE /documents/{doc_id} → 表格移除
- 切换会话：问答页左侧会话项点击 → 右侧消息流切换
- 删除会话：会话项右侧「删除」图标 → Popconfirm → DELETE → 列表移除
- P1 占位 tab 点击：展示「该能力在 P1 规划中，暂不可用」空态提示
- 返回列表：详情页面包屑「知识库」链接 → /kb
- 全库重建中：概览页保存配置或文档页操作时，后端返回 409 kb.rebuilding → Message.warning「全库重建进行中，请稍后」+ 禁用写操作
```

### 3.3 Flow Diagram

```mermaid
flowchart LR
  A[侧边栏知识库] --> B[/kb 列表]
  B -->|新建 Dialog| B
  B -->|行进入| C[/kb/$kbId 概览]
  C --> D[/kb/$kbId/documents 文档]
  C --> E[/kb/$kbId/chat 问答]
  D -->|拖拽上传+元数据 Dialog| D
  D -->|行解析详情 Drawer| D
  E -->|新建会话| E
  E -->|发送 同步/SSE| E
  C -->|P1 占位| F[占位提示]
  D -->|P1 占位| F
  E -->|P1 占位| F
```

---

## 4. Layout Regions

### 4.1 知识库列表页 `/kb`

```text
┌─────────────────────────────────────────────┐
│ [h2 知识库]              [新建知识库 Button]   │  ← 顶栏
├─────────────────────────────────────────────┤
│ ┌─────────────────────────────────────────┐ │
│ │ 名称│文档数│状态│创建时间│操作          │ │  ← Table
│ │ ... │ ...  │Tag │...     │进入 问答 删除 │ │
│ └─────────────────────────────────────────┘ │
│ [空态：Empty 插图 + 新建知识库按钮]            │  ← data.length=0
└─────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 顶栏 | h2「知识库」标题 + 右侧「新建知识库」Button primary | 镜像 models/index.tsx |
| 表格 | Table，列：name / doc_count / status(Tag) / created_at / actions(进入/问答/删除) | rowKey=id，dataIndex 对齐 OpenAPI |
| 空态 | Empty 组件 + 主 CTA「新建知识库」 | data.items.length=0 时 |

### 4.2 详情页 tab 壳 `/kb/$kbId`

```text
┌─────────────────────────────────────────────┐
│ [面包屑：知识库 > {kb.name}]    [删除知识库]  │  ← 面包屑 + 危险操作
├─────────────────────────────────────────────┤
│ [概览] [文档] [问答] [数据接入] [实验室] [权限] [历史] │  ← Tabs（前3激活，后4灰色占位）
├─────────────────────────────────────────────┤
│ [Outlet：当前 tab 内容]                       │
└─────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 面包屑 | 「知识库」链接（→ /kb）+ 当前 kb.name | 路由级 |
| 危险操作 | 右侧「删除知识库」Button danger + Popconfirm | 全库删除 |
| Tabs | 3 激活 tab（概览/文档/问答）+ 4 P1 占位 tab（灰色） | TanStack Router Link，激活态用 TDesign Tabs active 样式 |

### 4.3 概览 tab `/kb/$kbId/overview`

```text
┌─────────────────────────────────────────────┐
│ [入库配置区]                                  │
│  Embedding 模型 │ chunk_size │ OCR 启用       │
│  [保存配置 Button]                            │  ← 触发 Popconfirm → /rebuild
├─────────────────────────────────────────────┤
│ [问答配置区]                                  │
│  TopK │ score_threshold │ 检索策略(向量/混合) │
│  [保存配置 Button]                            │
├─────────────────────────────────────────────┤
│ [P1 规划区]                                   │
│  权限 / 审计 / 数据接入 / 检索实验室 / rerank  │  ← 灰色占位卡片
└─────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 入库配置 | Form：embedding_model(Input/Select) / chunk_size(InputNumber) / OCR(Switch)；底部「保存配置」Button primary | 改 Embedding/chunk_size 触发 Popconfirm 重建提示 |
| 问答配置 | Form：top_k(InputNumber 1-20) / score_threshold(InputNumber 0-1 step 0.1) / 检索策略(RadioGroup: 向量/混合)；「保存配置」Button | 不触发重建 |
| P1 规划 | 5 个灰色卡片，hover 提示「P1 规划中」 | 无交互 |

### 4.4 文档 tab `/kb/$kbId/documents`

```text
┌─────────────────────────────────────────────┐
│ [状态筛选 Select][重试策略...] [上传 Button]  │  ← 工具栏
├─────────────────────────────────────────────┤
│ ┌─────────────────────────────────────────┐ │
│ │ 文件名│大小│状态│创建时间│操作          │ │  ← Table
│ │ ...  │...│Tag │...     │解析详情 删除 重试│ │
│ └─────────────────────────────────────────┘ │
├─────────────────────────────────────────────┤
│ [拖拽上传区：Upload 拖拽组件]                 │  ← 常驻或点击展开
└─────────────────────────────────────────────┘
```

```text
[元数据 Dialog（每文件单独）]
┌──────────────────────────────┐
│ 上传文件：{file_name}（只读）│
│ ─────────────────────────── │
│ 元数据（key-value，可多行） │
│  [key Input][value Input][+] │
│  [key Input][value Input][x] │
│ ─────────────────────────── │
│        [取消]    [确认上传]  │
└──────────────────────────────┘
```

```text
[解析详情 Drawer]
┌──────────────────────────────────────────┐
│ 文档：{file_name}                         │
│ 状态：{Tag}  大小：{size}  页数：{pages} │
├──────────────────────────────────────────┤
│ [父子块层级 Tree]                         │
│  ▸ 父块1（2048 tokens）                   │
│    └ 子块1（512）                         │
│    └ 子块2（512）                         │
│  ▸ 父块2                                   │
├──────────────────────────────────────────┤
│ [文档摘要] {summary 文本}                  │
├──────────────────────────────────────────┤
│ [自定义元数据] {key: value, ...}          │
└──────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 工具栏 | 状态筛选 Select（全部/上传/解析中/已索引/失败）+ 上传 Button primary | 右侧 |
| 表格 | Table：file_name / size_bytes / status(Tag) / created_at / actions(解析详情 Drawer / 删除 / 重试) | rowKey=id |
| 拖拽上传 | TDesign Upload `theme="draggable"`，拖入文件后弹元数据 Dialog | 多文件 |
| 元数据 Dialog | Dialog：文件名只读 + 动态 key-value 元数据表单（Form + ArrayField） + 取消/确认上传 | 每文件单独 |
| 解析详情 Drawer | Drawer：父子块层级（Tree）+ 摘要文本 + 自定义元数据 | 行操作触发 |

### 4.5 问答 tab `/kb/$kbId/chat`

```text
┌──────────────┬──────────────────────────────────┐
│ [新建会话]    │ [TopK: InputNumber] [流式 Switch] │  ← 右侧顶栏
├──────────────┤                                  │
│ [会话1] ✓    │ ┌──────────────────────────────┐ │
│ [会话2]      │ │ user: {question}             │ │  ← 消息流
│ [会话3]  🗑️ │ │ assistant: {answer}           │ │
│ ...          │ │  [引用卡片1] [引用卡片2]      │ │
│              │ └──────────────────────────────┘ │
│              │ [输入框 + 发送 Button]            │  ← 底部输入
└──────────────┴──────────────────────────────────┘
```

```text
[引用卡片]
┌──────────────────────────────────────┐
│ 📄 {file_name} p.{page}  score={.xx} │
│ ─────────────────────────────────── │
│ {子块 content}                       │
│ [▸ 展开父块上下文]                    │  ← 点击展开 parent_content
└──────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 左侧会话栏 | 固定 240px 宽，顶部「新建会话」Button + 会话列表（时间倒序，当前会话高亮，每项右侧删除图标） | 240px 固定，独立滚动 |
| 右侧顶栏 | TopK InputNumber(1-20) + 流式/同步 Switch | 控制问答参数 |
| 消息流 | 滚动区域，user 消息右对齐，assistant 消息左对齐 + 引用卡片 | 现有 Card 模式扩展 |
| 底部输入 | Input(自适应高度) + 发送 Button；Enter 发送同步/流式由 Switch 决定 | 现有模式扩展 |
| 引用卡片 | Card：file_name + page + score + 子块 content + 可展开父块上下文 | sources 数组循环 |

### 4.6 P1 占位页（4 个）

```text
┌─────────────────────────────────────────────┐
│ [Empty 插图]                                │
│ 该能力在 P1 规划中，暂不可用                 │
└─────────────────────────────────────────────┘
```

| Region | Content | Notes |
|--------|---------|-------|
| 占位 | Empty 组件，描述「该能力在 P1 规划中，暂不可用」 | data-ingestion/lab/permissions/history 共用 |

---

## 5. Component Mapping

| UI element | TDesign component | Props / variant | Data source |
|------------|-------------------|-----------------|-------------|
| 顶栏标题 | `<h2>` | — | 静态 |
| 新建知识库 Button | `Button` | `theme="primary"` | — |
| 知识库表格 | `Table` | columns: name/doc_count/status/created_at/actions; rowKey="id" | GET /knowledge-bases → items |
| 状态 Tag | `Tag` | active=success, rebuilding=warning, deleted=default | KnowledgeBase.status |
| 列表行进入 | `Link` (TanStack Router) | to="/kb/$kbId/overview" | row.id |
| 列表行问答 | `Link` | to="/kb/$kbId/chat" | row.id |
| 列表行删除 | `Button` + `Popconfirm` | `theme="danger"`, theme="danger" | row.id |
| 空态 | `Empty` | slot="image" + description + 子 Button | — |
| 新建模态框 | `Dialog` | visible 控制; footer 取消/确认 | — |
| 创建表单 | `Form` + `FormItem` | name(Input required) / description(Textarea) / embedding_model(Input default bge-m3) / chunk_size(InputNumber default 512) / top_k(InputNumber default 5) | POST /knowledge-bases body |
| 详情 tab 壳 | TanStack Router `Outlet` + `Tabs`（仅样式，激活态用路由 Link） | value 跟随当前路由 | — |
| 面包屑 | `Link` + 文本 | — | kb.name |
| 概览入库配置 | `Form` + `FormItem` | embedding_model(Input/Select) / chunk_size(InputNumber) / OCR(Switch) | GET/PUT /config |
| 概览重建按钮 | `Button` + `Popconfirm` | `theme="primary"` + "修改配置将触发全库重建，确认？" | POST /rebuild |
| 概览问答配置 | `Form` + `FormItem` | top_k(InputNumber 1-20) / score_threshold(InputNumber 0-1 step 0.1) / 检索策略(RadioGroup: 向量/混合) | GET/PUT /config |
| P1 规划卡片 | `Card` | 灰色 disabled 样式 | 静态 |
| 文档表格 | `Table` | columns: file_name/size_bytes/status/created_at/actions; rowKey="id" | GET /documents → items |
| 文档状态 Tag | `Tag` | uploaded=default, parsing=processing, indexing=processing, ready=success, failed=danger | KBDocument.status |
| 状态筛选 | `Select` | options: 全部/上传/解析中/已索引/失败 | 本地 state |
| 上传区 | `Upload` | `theme="draggable"`, multiple, accept 按格式 | — |
| 元数据 Dialog | `Dialog` | visible; footer 取消/确认上传 | — |
| 元数据表单 | `Form` + `ArrayField`（或动态 row） | key(Input) + value(Input) + add/remove | custom_metadata JSONB |
| 解析详情 | `Drawer` | visible; placement="right" | — |
| 父子块层级 | `Tree` | data: 父块→子块嵌套 | kb_chunks |
| 文档摘要 | `Text` / `Paragraph` | — | doc_summary |
| 自定义元数据展示 | `Descriptions` 或 JSON 展示 | — | custom_metadata |
| 重试按钮 | `Button` + `Popconfirm` | `theme="warning"`, 仅 failed 行显示 | POST /reparse |
| 会话列表 | `List`（自定义）或 `Menu` | 当前项 active; 每项右侧删除 | GET /sessions |
| 新建会话 | `Button` | `theme="primary"`, block | — |
| TopK 调节 | `InputNumber` | min=1, max=20, default=5 | 本地 state |
| 流式开关 | `Switch` | label="流式" | 本地 state |
| 消息卡片 | `Card` | user 右对齐 brand-light / assistant 左对齐 grey | messages state |
| 引用卡片 | `Card` | size="small" | KBQueryResponse.sources[] |
| 父块展开 | `Collapse` / 可点击 Text | — | parent_content |
| 输入框 | `Input` / `Textarea` | autosize; onEnter 发送 | question state |
| 发送按钮 | `Button` | `theme="primary"`, loading 绑定 | mutation pending |
| P1 占位 | `Empty` | description="该能力在 P1 规划中，暂不可用" | — |
| 危险删除知识库 | `Button` + `Popconfirm` | `theme="danger"` | DELETE /knowledge-bases/{kb_id} |
| 危险删除文档 | `Button` + `Popconfirm` | `theme="danger"` | DELETE /documents/{doc_id} |
| 成功提示 | `Message` | `theme="success"` | — |
| 错误提示 | `Message` | `theme="error"` | — |
| 警告提示 | `Message` | `theme="warning"` | 409 kb.rebuilding |
| 加载骨架 | `Skeleton` / Table loading | — | isLoading |

**字段命名规则：** 所有 `dataIndex` / `form.name` 对齐 OpenAPI/proto 修复后字段名（`parse_status` 而非 `status`，待 US-001 契约修复后统一）。

---

## 6. State Design

### 6.1 知识库列表页

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | GET 200 + data 非空 | 展示表格 | Table |
| loading | GET in flight | Table `loading=true`，顶栏按钮可用 | Table, Skeleton |
| empty | GET 200 + items.length=0 | Empty 插图 + 「新建知识库」主按钮 | Empty, Button |
| error | GET 4xx/5xx | `Message.error`「加载知识库失败」+ 重试链接 | Message |
| creating | POST in flight | Dialog 确认按钮 loading，输入禁用 | Dialog, Button |
| create-success | POST 201 | `Message.success`「知识库已创建」+ Dialog 关闭 + 表格刷新 | Message, Dialog |
| create-error | POST 4xx | `Message.error` + Dialog 保持 | Message, Dialog |
| deleting | DELETE in flight | Popconfirm 确认 loading | Popconfirm |
| delete-success | DELETE 204 | `Message.success`「已删除」+ 表格刷新 | Message |

### 6.2 概览 tab

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | GET /config 200 | 展示入库配置 + 问答配置 + P1 规划区 | Form |
| loading | GET in flight | `Skeleton` 配置区 | Skeleton |
| error | GET 4xx | `Message.error`「加载配置失败」 | Message |
| saving | PUT /config in flight | 保存按钮 loading | Button |
| save-success | PUT 200 | `Message.success`「配置已保存」 | Message |
| rebuild-confirm | 改 Embedding/chunk_size 保存 | Popconfirm「修改配置将触发全库重建，确认？」 | Popconfirm |
| rebuilding | POST /rebuild 202 | `Message.success`「重建任务已提交」+ KB Tag 变 rebuilding + 写操作禁用 | Message, Tag |
| rebuild-conflict | POST /rebuild 409 | `Message.warning`「全库重建进行中，请稍后」 | Message |
| rebuild-error | POST 5xx | `Message.error`「重建失败」 | Message |

### 6.3 文档 tab

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | GET /documents 200 | 展示文档表格 | Table |
| loading | GET in flight | Table `loading=true` | Table |
| empty | items.length=0 | Empty「暂无文档，拖拽文件上传」+ 上传区引导 | Empty |
| error | GET 4xx | `Message.error`「加载文档失败」 | Message |
| uploading | 文件拖入 + 元数据确认 | Dialog 关闭 + 行新增（status=pending/uploaded）+ `Message.success`「上传中」 | Dialog, Message |
| upload-error | 上传失败 | `Message.error`「上传失败：{msg}」 | Message |
| parsing | status=parsing | 行 Tag=processing「解析中」 | Tag |
| indexing | status=indexing | 行 Tag=processing「索引中」 | Tag |
| ready | status=ready | 行 Tag=success「已索引」 | Tag |
| failed | status=failed | 行 Tag=danger「失败」+ 显示重试按钮 | Tag, Button |
| reparse-confirm | 点击重试 | Popconfirm「重新解析将覆盖现有分块，确认？」 | Popconfirm |
| reparsing | POST /reparse 202 | `Message.success`「已提交重新解析」+ status 回 parsing | Message, Tag |
| reparse-error | POST 5xx | `Message.error`「重新解析失败」 | Message |
| detail-loading | 打开 Drawer | Drawer `Skeleton` | Drawer, Skeleton |
| detail-ready | 数据返回 | 展示 Tree + 摘要 + metadata | Drawer, Tree |
| detail-error | 加载失败 | Drawer 内 `Message.error` | Message |
| deleting | DELETE in flight | Popconfirm loading | Popconfirm |
| delete-success | DELETE 204 | `Message.success`「已删除」+ 表格刷新 | Message |

### 6.4 问答 tab

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| idle | 进入有会话 | 左侧会话列表 + 右侧最近会话消息流 | List |
| empty-sessions | 无会话 | 左侧 Empty「暂无会话，点击新建」+ 右侧空 | Empty |
| session-loading | 切换会话 | 消息流 `Skeleton` | Skeleton |
| question-empty | 输入框为空 | 发送按钮 disabled | Button |
| querying-sync | POST /query in flight | 发送按钮 loading + 消息流「AI 思考中…」 | Button, Card |
| query-success | POST 200 | 消息流追加 assistant 卡片 + 引用卡片 | Card |
| query-error | POST 4xx/5xx | `Message.error`「问答失败：{msg}」 | Message |
| streaming-start | GET /query/stream 开始 | 发送按钮 loading + 消息流新增 assistant 卡片（空） | Button, Card |
| streaming-tokens | SSE token 事件 | assistant 卡片逐字追加内容 | Card |
| streaming-sources | SSE sources 事件 | 引用卡片渲染 | Card |
| streaming-end | SSE 结束 | 发送按钮恢复 + loading 结束 | Button |
| streaming-error | SSE 400/401 | `Message.error` + 卡片标记「[流式中断]」 | Message |
| session-deleting | DELETE in flight | Popconfirm loading | Popconfirm |
| session-delete-success | DELETE 204 | 列表移除 + 若删当前会话切换到第一个 | List |

### 6.5 详情页（通用）

| State | Trigger | UI behavior | Components |
|-------|---------|-------------|------------|
| kb-not-found | GET /knowledge-bases/{kb_id} 404 | 整页 `Empty`「知识库不存在或已删除」 | Empty |
| kb-deleted | kb.status=deleted | 写操作禁用 + Tag=deleted | Tag |
| kb-rebuilding | kb.status=rebuilding | 写操作禁用 + `Alert`「全库重建中」 | Alert |
| p1-placeholder | 点击 P1 tab | Empty「该能力在 P1 规划中，暂不可用」 | Empty |

---

## 7. Copy & Feedback

### 7.1 Labels & Buttons

| Element | Copy (zh-CN) | Notes |
|---------|--------------|-------|
| 侧边栏菜单 | 知识库 | 已存在 |
| 列表页标题 | 知识库 | h2 |
| 列表新建按钮 | 新建知识库 | 顶栏右侧 |
| 列表行操作 | 进入 / 问答 / 删除 | — |
| 新建 Dialog 标题 | 新建知识库 | — |
| 新建表单字段 | 名称 / 描述 / 嵌入模型 / 分块大小 / TopK | — |
| 新建确认 | 创建 | Button primary |
| 新建取消 | 取消 | Button |
| 详情面包屑 | 知识库 | 链接 |
| 详情删除 | 删除知识库 | Button danger |
| Tab 标签 | 概览 / 文档 / 问答 / 数据接入 / 检索实验室 / 权限管理 / 操作历史 | 前3激活，后4灰 |
| 概览入库配置 | 入库配置 | 区块标题 |
| 概览问答配置 | 问答配置 | 区块标题 |
| 概览 P1 规划 | P1 规划 | 区块标题 |
| 概览保存 | 保存配置 | Button primary |
| 重建确认 | 修改配置将触发全库重建，确认？ | Popconfirm content |
| 文档页上传 | 上传文档 | Button primary |
| 文档页状态筛选 | 全部 / 上传 / 解析中 / 已索引 / 失败 | Select |
| 文档行操作 | 解析详情 / 删除 / 重试 | — |
| 元数据 Dialog 标题 | 上传文件元数据 | — |
| 元数据确认 | 确认上传 | Button primary |
| 解析详情 Drawer | 文档解析详情 | Drawer title |
| 问答新建会话 | 新建会话 | Button |
| 问答流式开关 | 流式 | Switch label |
| 问答 TopK | TopK | InputNumber label |
| 问答发送 | 发送 | Button primary |
| 引用卡片展开 | 展开父块上下文 | 可点击文本 |

### 7.2 Messages

| Scenario | Type | Copy |
|----------|------|------|
| 知识库创建成功 | `Message.success` | 知识库已创建 |
| 知识库创建失败 | `Message.error` | 创建失败：{error.message} |
| 知识库删除成功 | `Message.success` | 已删除 |
| 知识库删除失败 | `Message.error` | 删除失败：{error.message} |
| 列表加载失败 | `Message.error` | 加载知识库失败 |
| 配置保存成功 | `Message.success` | 配置已保存 |
| 配置保存失败 | `Message.error` | 保存失败：{error.message} |
| 重建提交成功 | `Message.success` | 重建任务已提交 |
| 重建冲突 | `Message.warning` | 全库重建进行中，请稍后 |
| 重建失败 | `Message.error` | 重建失败：{error.message} |
| 文档上传中 | `Message.success` | 文档已上传，解析中 |
| 文档上传失败 | `Message.error` | 上传失败：{error.message} |
| 重新解析提交 | `Message.success` | 已提交重新解析 |
| 重新解析失败 | `Message.error` | 重新解析失败：{error.message} |
| 文档删除成功 | `Message.success` | 已删除 |
| 文档删除失败 | `Message.error` | 删除失败：{error.message} |
| 问答失败 | `Message.error` | 问答失败：{error.message} |
| 流式中断 | `Message.error` | 流式响应中断：{error.message} |
| 会话删除成功 | `Message.success` | 会话已删除 |
| 字段校验 | inline | 名称不能为空 / TopK 范围 1-20 / score_threshold 范围 0-1 |
| P1 占位 | Empty description | 该能力在 P1 规划中，暂不可用 |
| KB 不存在 | Empty description | 知识库不存在或已删除 |

---

## 8. Boundaries & Non-Goals

### 8.1 In Scope (UX)

- 知识库列表页（查看/新建/删除）
- 知识库详情 3-tab 布局壳 + 4 P1 占位 tab
- 概览 tab（入库配置 + 问答配置 + 重建触发）
- 文档 tab（上传 + 元数据 + 列表 + 状态筛选 + 重试 + 解析详情 Drawer）
- 问答 tab（多会话 + 同步/流式 + TopK + 引用卡片 + 父块展开）
- 列表/详情/文档/问答各页的 loading / empty / error / success / disabled 状态

### 8.2 Explicitly Out of Scope

- **P1 功能 UI 实现**：权限管理、操作历史、数据接入、检索实验室、rerank 只做占位 Empty，不实现表单/列表/图表
- **KB 级权限校验 UI**：P0 仅靠 RLS 隔离，无权限设置界面（占位页内无内容）
- **文档智能/会议智能/视频智能**：PRD Non-Goals，无对应 UI
- **解析任务历史独立页**：PRD Open Question（P1），P0 仅在文档行 Drawer 展示当前解析详情
- **SSE 事件协议设计**：UX 不定义事件格式，SPEC 负责
- **向量存储/对象存储/Core 路由**：后端能力，无直接 UI
- **OCR 调用界面**：用户无感，后端调用 AI 服务
- **本地安装 PaddleOCR**：后端决策，无 UI

### 8.3 Open UX Questions

- SSE 结束事件和异常事件的前端统一事件协议（与 SPEC 协同）
- `doc_count` 是否需要前端区分「可检索文档数」与「总文档数」（PRD Open Question）
- 全库重建期间文档页写操作禁用的具体范围（上传/删除/重试是否全禁，还是仅禁上传）
- 概览页检索策略「向量/混合」是否需要更细粒度（如 RRF 权重调节，P1）

### 8.4 Assumptions

- 使用现有 Console `__root.tsx` 应用壳（Header + Aside + Content），不新建布局
- 侧边栏「知识库」菜单已存在（`__root.tsx` `Menu.MenuItem value="kb"` + `BookIcon` + `Link to="/kb"`）
- TanStack Router 文件路由：`/kb/$kbId/overview` 等为新增文件路由，`__root.tsx` 改造为 tab 布局壳
- OpenAPI 字段名以 US-001 契约修复后为准（`KBDocument.status` → `parse_status`），UX 组件 `dataIndex` 需在实现时对齐
- Upload 组件 accept 按文档格式：`.pdf,.docx,.xlsx,.pptx,.md,.txt`
- 文件大小上限 100MB（前端校验，超限 `Message.error`「文件大小超限（上限 100MB）」）
- 复用 TDesign React 现有依赖（`tdesign-react` ^1.10.0、`tdesign-icons-react`），不新增 UI 库
- 数据获取统一用 `@tanstack/react-query` + `openapi-fetch`（现有 `api` client）
