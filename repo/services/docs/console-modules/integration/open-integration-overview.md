# 开放与集成总入口

## 页面定位

`开放与集成总入口` 是 `Console / 开放与集成` 的租户侧接入总入口，用于帮助用户理解当前 ANI 平台对外开放的接入面、鉴权准备项和文档入口，并明确哪些能力已经具备权威源支撑、哪些仍属于待补边界。

当前模块属于接入聚合页，一级权威源为：

- `ANI-main/repo/api/openapi/v1.yaml`
- `ANI-main/repo/api/openapi/services/v1.yaml`

本页是文档与接入聚合页，不是新的资源管理页，也不是 `BOSS` 的运维通知与第三方平台运营集成页。

## 文档管理规则

- 本文是 `开放与集成总入口` 的主维护文档，页面定位、入口关系、边界说明和验收标准统一以本文为准
- `prototypes/ani-services-prototype-console.html` 和 `prototypes/ani-services-prototype.html` 只保留摘要、边界和详细材料入口
- `tasks/modules/prd/console/integration/prd-console-open-integration-overview.md` 与 `tasks/modules/spec/console/integration/spec-console-open-integration-overview.md` 作为辅助材料保留，不替代本文
- 当前冻结导航未单列“概览”页时，HTML 摘要落在既有 `REST API` 首屏说明中，不额外扩写新导航路径
- 如本文与辅助材料出现差异，先回到权威源和仓内工件事实核对，再统一回写

## Core / 文档层要求

- 本页本质是接入聚合页，不单独发明新的后端资源路径
- 本页引用的已冻结 API 路径必须来自现有 `Core / Auth` 权威源
- 本页可以汇总 `REST API 文档`、`API Key`、`OIDC 登录链路`、`ani CLI`
- 本页可以引用 `Services` 已冻结业务面作为“文档覆盖范围”，但不在本页重写其完整契约
- 本页不能把 `OpenAI 兼容 API`、`SDK 发版交付`、`Webhook`、`Bot`、`第三方业务系统集成` 写成当前已经冻结的正式接口组
- 本页不要求前端显式传 `tenant_id` 或 `X-Tenant-Id`

## 页面职责

- 提供 `开放与集成` 域的统一边界说明
- 汇总当前可直接对齐的 `REST API 文档` 与接入凭证准备项
- 说明 `API Key` 与 `OIDC` 在接入链路中的位置
- 提供“交互式登录 / 程序化调用 / 文档查阅 / CLI 调试”的推荐接入路径
- 对 `OpenAI 兼容 API / SDK` 保持文档占位说明；`Webhook / Bot / 第三方集成` 已有 P2 子模块详文（handler stub）

## 页面结构

- 首屏至少包含 `推荐接入路径`、`接入准备项`、`已冻结接入面`、`待补边界` 四个区块
- `推荐接入路径` 至少区分 `交互式登录`、`程序化调用`、`文档查阅`、`CLI 调试`
- `接入准备项` 至少覆盖 `OIDC`、`API Key`、`API 文档`、`CLI`
- 总入口只负责导航和边界，不重写 `API Key 管理` 或服务模块的完整契约正文

## 数据来源与分层约束

### 数据来源划分

- `Core` 数据
  - OIDC 登录与 token 链路
  - API Key 管理
- `Services` 数据
  - 模型、推理服务、知识库等已冻结业务面，作为 REST API 文档覆盖范围引用
- `Docs / Repo` 工件
  - API 文档 HTML
  - CLI 源码入口

### 关键边界

- 文档工件入口不等于业务 API 路径
- CLI 源码入口不等于对外下载页或发布页
- `Services` 路径在本页只作为文档覆盖面引用，不在本页重写资源定义
- OpenAI / SDK 当前为文档占位；Webhook / Bot / 第三方集成见 `integration-*.md` 子模块

## 冻结事实表

### Frozen Paths

- `POST /api/v1/auth/oidc/begin`
- `POST /api/v1/auth/token`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`
- `GET /api/v1/auth/api-keys`
- `POST /api/v1/auth/api-keys`
- `DELETE /api/v1/auth/api-keys/{key_id}`

### Frozen Services Coverage

- `GET /api/v1/svc/models`
- `GET /api/v1/svc/inference-services`
- `GET /api/v1/svc/knowledge-bases`

### Frozen Supporting Entrypoints

- `ANI-main/repo/docs/api/index.html`
- `ANI-main/repo/docs/api/core.html`
- `ANI-main/repo/docs/api/services.html`
- `ANI-main/repo/cli/ani/main.go`

### Non-Frozen Capabilities

- OpenAI 兼容 API 正式接入页
- Go SDK / Python SDK / TypeScript Client / Java SDK 的独立交付页
- Webhook 配置与事件投递管理
- 企业微信 / 钉钉 Bot
- 第三方业务系统集成

## 推荐接入路径

| 接入目标 | 推荐路径 | 前置准备 | 下一跳 |
|---|---|---|---|
| 登录 Console 或交互式使用 | OIDC | 租户登录上下文、回调地址 | `安全与身份概览` |
| 脚本、服务或 SDK 调用 | API Key | 创建凭证、确认 scopes | `API Key 管理` |
| 查阅正式接口 | REST API 文档 | 选择 `Core` 或 `Services` 文档 | `docs/api/*.html` |
| 本地调试与命令行操作 | ani CLI | 凭证准备、命令行环境 | `ANI-main/repo/cli/ani/main.go` |

## 接入准备矩阵

| 接入方式 | 当前口径 | 依赖 | 当前结论 |
|---|---|---|---|
| REST API | 使用现有 OpenAPI 文档工件 | `docs/api/*.html`、`v1.yaml` | 已冻结 |
| Bearer 登录 | 使用 OIDC 登录与 token 链路 | `/api/v1/auth/oidc/begin`、`/api/v1/auth/token`、`/api/v1/auth/refresh`、`/api/v1/auth/logout` | 已冻结 |
| API Key 调用 | 使用 `API Key 管理` 创建凭证 | `/api/v1/auth/api-keys*` | 已冻结 |
| ani CLI | 作为现有客户端仓内入口 | `ANI-main/repo/cli/ani/main.go` | 已存在源码入口，不等于独立 Console 页 |
| OpenAI / SDK | 文档占位 | 待补 | 见 `openai-compatible-api.md` 或 SDK 导航 |
| Webhook / Bot / 第三方集成 | 子模块详文已补 | 部分 | Phase 2 YAML；handler stub |

## 页面区块与数据来源映射

| 区块 | 主要来源层 | 数据口径说明 | 跳转目标 |
|---|---|---|---|
| 推荐接入路径 | Core / Docs / Repo | 用任务流而不是平铺链接组织入口 | 安全域子页 / API 文档 / CLI |
| 已冻结接入面概览 | Core / Docs | 汇总当前可以安全引用的接入面 | API 文档 / API Key 子页 |
| 接入准备项 | Core / Docs | 说明鉴权方式、登录方式和客户端选择建议 | API 文档 / CLI |
| 待补能力说明 | 规划项 | 仅说明未冻结的接入子域 | 后续模块文档 |

## 字段级定义

### 鉴权与登录链路字段

| 字段 | 来源 schema | 说明 |
|---|---|---|
| `tenant_name` | `BeginOIDCLoginRequest.tenant_name` | 租户登录上下文 |
| `redirect_uri` | `BeginOIDCLoginRequest.redirect_uri` / `CompleteOIDCLoginRequest.redirect_uri` | OIDC 回调地址 |
| `authorization_url` | `BeginOIDCLoginResponse.authorization_url` | OIDC 授权跳转地址 |
| `state` | `BeginOIDCLoginResponse.state` | 登录状态串 |
| `key_prefix` | `APIKeyInfo.key_prefix` | API Key 前缀 |
| `key_value` | `CreateAPIKeyResponse.key_value` | 仅创建成功时返回一次 |

### 文档与工件字段

| 字段 | 来源 | 说明 |
|---|---|---|
| `core_docs_entry` | `ANI-main/repo/docs/api/core.html` | Core API 文档入口 |
| `services_docs_entry` | `ANI-main/repo/docs/api/services.html` | Services API 文档入口 |
| `index_docs_entry` | `ANI-main/repo/docs/api/index.html` | 文档目录入口 |
| `cli_entry` | `ANI-main/repo/cli/ani/main.go` | CLI 仓内入口 |

## 字段展示规则

| 场景 | 展示规则 | 说明 |
|---|---|---|
| 已冻结接入面 | 显示可安全引用的路径或工件入口 | 不补写未冻结协议 |
| 凭证未准备 | 显示“先准备 API Key”或“先走 OIDC”建议 | 不伪造可直接调用结果 |
| 文档工件不可访问 | 显示文档暂不可用 | 不伪造替代 API 路径 |
| 待补接入面 | 说明态展示，不显示下载或配置按钮 | 避免误导 |

## 字段口径与单位

| 字段 | 口径建议 | 单位/格式 |
|---|---|---|
| `tenant_name` | 登录上下文租户，不等于租户配置页信息 | 字符串 |
| `redirect_uri` | 必须与注册回调地址一致 | URI |
| `authorization_url` | 授权跳转地址 | URI |
| `key_prefix` | 凭据识别前缀，不等于可直接调用凭据 | 字符串 |
| `docs_entry` | 仓内文档工件路径 | 文件路径 |
| `cli_entry` | 仓内 CLI 源码入口 | 文件路径 |

## 状态与能力口径

### 接入能力边界

- 交互式登录：`OIDC begin -> token -> refresh -> logout`
- 程序化调用：`API Key`
- 文档查阅：`docs/api/*.html`
- CLI 入口：`cli/ani/main.go`
- 待补接入：`OpenAI / SDK`（文档占位）；`Webhook / Bot / 第三方集成` 已有子模块详文（Phase 2 YAML；handler stub）

### 待补能力边界

- OpenAI 兼容 API（独立 Envoy AI Gateway 数据面）— 详文 `openai-compatible-api.md`；`ani-gateway` 不承载 `/v1` chat 代理
- Go / Python / TypeScript Client / Java SDK 独立交付页
- Webhook — 详文 `integration-webhook-overview.md` <!-- ADDED-TO-YAML: 部分见 Services /api/v1/svc/tenant/webhooks (Phase 2 2026-06-17) -->
- 企业微信 / 钉钉 Bot — 详文 `integration-bot.md` <!-- ADDED-TO-YAML: POST /api/v1/svc/integrations/bots (Services v1.yaml, Phase 2 2026-06-17) -->
- 第三方业务系统集成 — 详文 `integration-third-party.md` <!-- ADDED-TO-YAML: GET/POST /api/v1/svc/integrations (Services v1.yaml, Phase 2 2026-06-17) -->

## 创建前置条件

| 项 | 说明 |
|---|---|
| 本页定位 | 接入导航总入口，**不承接**算力/存储等资源创建 |
| 凭证与登录 | API Key 创建见 `tenant/api-key-management.md`；OIDC 见 `tenant/security-identity-overview.md` |

## 操作可用性矩阵

| 操作 | 已登录成员 | 租户管理员 | 说明 |
|---|---|---|---|
| 查看开放与集成总入口 | 可用 | 可用 | 接入总入口 |
| 进入安全与身份概览 | 可用 | 可用 | 查看登录与鉴权边界 |
| 进入 API Key 管理 | 视权限而定 | 可用 | 先准备程序化凭证 |
| 查阅 API 文档 | 可用 | 可用 | 进入仓内文档工件 |
| 查看 CLI 入口 | 可用 | 可用 | 进入仓内源码入口 |
| 使用待补接入面 | 不可用 | 不可用 | 当前仅说明态 |

## 使用规则

- 程序化调用推荐使用 `API Key`，交互式登录推荐走 `OIDC`
- API 字段、路径、返回码一律以 `v1.yaml` 与生成文档为准
- 页面不得把文档入口页写成新的业务 API 路径
- 页面不得把待补接入方式写成“当前已可直接对接”

## 接口冻结规则

### `POST /api/v1/auth/oidc/begin`

- `operationId`: `beginOIDCLogin`
- `success`: `200 + BeginOIDCLoginResponse`
- `errors`: `400`

### `POST /api/v1/auth/token`

- `operationId`: `completeOIDCLogin`
- `success`: `200 + TokenPairResponse`
- `errors`: `400`、`401`

### `POST /api/v1/auth/refresh`

- `operationId`: 当前权威源未声明
- `success`: `200 + RefreshAccessTokenResponse`
- `errors`: `400`、`401`

### `POST /api/v1/auth/logout`

- `operationId`: `logout`
- `success`: `200 + RevokeStatusResponse`
- `errors`: `400`、`401`、`403`

### `GET /api/v1/auth/api-keys`

- `operationId`: `listAPIKeys`
- `success`: `200 + ListAPIKeysResponse`
- `errors`: `401`、`403`

### `POST /api/v1/auth/api-keys`

- `operationId`: `createAPIKey`
- `success`: `201 + CreateAPIKeyResponse`
- `errors`: `400`、`401`、`403`

### `DELETE /api/v1/auth/api-keys/{key_id}`

- `operationId`: `revokeAPIKey`
- `success`: `200 + {status: revoked}`
- `errors`: `400`、`401`、`403`、`404`

## 响应示例

### OIDC 登录启动成功

```json
{
  "authorization_url": "https://dex.example.com/auth?client_id=ani-console&state=oidc-state-002",
  "state": "oidc-state-002"
}
```

### API Key 创建成功

```json
{
  "key_id": "key_003",
  "key_value": "ani_live_93uLrQ4Tn8KdX2e",
  "key_prefix": "ani_live_93uL"
}
```

## 错误示例

### 接入准备不足

```json
{
  "code": "FORBIDDEN",
  "message": "permission denied",
  "request_id": "req-integration-403-001"
}
```

## 回填前置依赖

- 后续若要把 `OpenAI 兼容 API` 写成正式总入口能力，必须先确认正式权威源路径与返回码
- 后续若要把 `SDK / Webhook / Bot / 第三方业务系统集成` 写成正式模块，必须先补独立文档和契约来源

## 回填验收标准

- 正文明确区分已冻结接入面与待补接入能力
- 正文不把文档入口伪造成新的业务路径
- 正文不把 OpenAI / SDK / Webhook / Bot / 第三方集成写成已冻结接口
- 正文明确区分 API 路径、文档工件路径和仓内源码入口
- `PRD`、`SPEC`、HTML 摘要与本文件一致

## 产品经理补充定义

### 目标用户与权限视角

- 开发者或租户管理员：选择接入方式、准备凭证、进入 API 文档或 CLI
- 业务成员：查看当前可用接入面与所需前置条件
- 只读用户：查看接入说明与边界，但不承担凭证管理动作

### 页面结构补充

- 首屏至少包含 `推荐接入路径`、`接入准备项`、`已冻结接入面`、`待补边界` 四个区块
- 页面应把 `OIDC`、`API Key`、`REST API 文档`、`ani CLI` 组织成可执行的接入路径，而不是平铺链接目录
- 当前未冻结的 OpenAI、SDK、Webhook、Bot、第三方业务系统集成只能保留说明态

### 核心任务流

1. 用户先选择接入方式：交互式登录走 `OIDC`，程序化调用走 `API Key`，开发调试走 `REST API 文档 / CLI`
2. 用户根据当前状态判断是否需要先去 `API Key 管理` 创建凭证
3. 用户从入口页跳转到文档、CLI 或安全域子页完成接入准备，再返回入口页复核

### 页面状态与反馈

- 当租户尚无 API Key 时，页面需要突出“先准备凭证”的建议路径
- 当 API 文档工件不可访问时，页面显示文档暂不可用，而不是伪造替代路径
- 待补接入面必须展示为“规划中 / 待补”，不能展示可点击的下载或配置入口
- 页面必须区分“接入准备不足”和“能力尚未冻结”两类不同原因

### 跨模块协同

- 与 `安全与身份概览`、`API Key 管理` 协同，形成完整的接入准备闭环
- 与首页协同，只作为接入入口的摘要回跳，不承担系统运营通知职责
- 与服务类模块协同时，只通过 API 文档和凭证准备相连，不宣称已有 OpenAI 或 SDK 子页

### 产品验收补充

- 用户必须能在入口页判断“我该先去哪里准备接入”
- 文档入口、凭证入口、CLI 入口必须命名一致且逻辑连贯
- 待补接入面不能产生“看起来已经可以用”的误导
- 本页不得发明新的业务 API 路径或接入资源域
