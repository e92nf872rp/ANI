# Console 模块文档

按 **业务域** 存放各 Console 页面的**主维护源**（模块详文）。治理与 OpenAPI 草案已拆到子目录，避免本层堆文件。

## 快速导航

### 治理与状态（先看这些）

| 文档 | 用途 |
|---|---|
| [console-document-status-board.md](governance/console-document-status-board.md) | **唯一总控** — 可用 / 待收口 / 权威源对齐 |
| [module-completion-matrix.md](governance/module-completion-matrix.md) | 模块 × 冻结路径对照 |
| [console-undefined-features-backlog.md](governance/console-undefined-features-backlog.md) | P0/P1/P2 backlog |
| [schema-completion-tracker.md](governance/schema-completion-tracker.md) | YAML vs handler 跟踪 |
| [module-delivery-workflow.md](governance/module-delivery-workflow.md) | 交付与 422 口径 |
| [notebook/README.md](../notebook/README.md) | 前端开发日志（按日记录） |

→ 完整列表：[governance/README.md](governance/README.md)

### OpenAPI 草案（规划 · 非冻结）

| 目录 | 内容 |
|---|---|
| [openapi-drafts/p0/](openapi-drafts/p0/) | P0 阻塞草案（**待定**） |
| [openapi-drafts/phase3/](openapi-drafts/phase3/) | Phase 3 ×17 + 整域索引 |

→ [openapi-drafts/README.md](openapi-drafts/README.md)

### 业务域模块详文

| 目录 | 内容 |
|---|---|
| [home/](home/) | 平台概览、首页区块 |
| [compute/](compute/) | VM、GPU、网络、存储、K8s |
| [inference/](inference/) | 模型中心、推理服务 |
| [knowledge/](knowledge/) | 知识库与智能 |
| [ai-native/](ai-native/) | Agent、MCP、编排 |
| [alerts/](alerts/) | 告警、异步任务中心 |
| [tenant/](tenant/) | 租户、API Key、用量 |
| [integration/](integration/) | SDK、Webhook、第三方 |
| [security/](security/) | 密钥、审计、合规 |

每个域内的 `README.md`（若有）列出该域子模块与草案链接。

## 维护规则

1. **HTML 摘要**：[`prototypes/ani-services-prototype-console.html`](../../prototypes/ani-services-prototype-console.html) 只保留导航与边界，详文在本目录。
2. **PRD/SPEC**：[`tasks/modules/`](../../tasks/modules/) — 辅助材料，不替代详文。
3. **新增模块**：在对应域下新建 `{module}.md`，并更新 governance 状态板。

## 不再在本层放什么

- ~~30 个 governance + openapi 混排 md~~ → `governance/`、`openapi-drafts/`
- ~~空目录 model/~~ — 模型详文在 `inference/`
