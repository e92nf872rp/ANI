# ANI Services 团队开发指南

> 本文档面向 ANI Services 开发团队，说明可以修改的目录与文件范围、开发约定、与 ANI Core 的协作边界，以及 ANI Core 各目录的职责参考。
>
> **版本**：2026-06-09  
> **维护者**：ANI 项目架构负责人

---

## 0. 快速定位

当前治理状态：Services 受控并行 PR 阶段。Services 主责目录可以开发，但不是无门禁共享仓库；所有跨层改动必须通过 CODEOWNERS 共同审查、API split、Services boundary gate 和既有 architecture gate。Core Sprint 13/14 既有事实继续有效，受控解冻不代表 Services 代码已经全部边界合规或 production-ready。

| 我要做什么 | 去哪个目录 |
|---|---|
| 开发/修改后端微服务（Gateway、认证、模型、知识库等） | `repo/services/` |
| 开发/修改前端 Web 页面（Console 控制台、BOSS 后台） | `repo/frontends/` |
| 新增或修改 Services 层 REST API 接口定义 | `repo/api/openapi/services/v1.yaml` |
| 查阅 Core 提供的基础设施 API（只读参考） | `repo/api/openapi/v1.yaml` |
| 查阅 Core 提供的 Go SDK（只可调用，不可修改） | `repo/sdks/core/` |

---

## 1. Services 团队允许修改的目录与文件

### 1.1 `repo/services/` — 后端微服务代码

这是所有 **Go 语言后端服务**的代码目录。Services 团队在业务服务目录拥有主责开发权；触碰 Core-owned 服务、Gateway Core handler/middleware/runtime 或 Core 保护目录时必须共同审查。

#### `repo/services/ani-gateway/` — HTTP 网关（最核心，优先级最高）

**是什么：**  
浏览器、Console 前端、CLI、SDK 的**唯一对外 HTTP 入口**，监听 `:8080` 端口。使用 [Hertz](https://github.com/cloudwego/hertz) 框架（字节跳动开源的高性能 Go HTTP 框架）。

**已完成的部分（Core 团队已实现，Services 团队不需要也不应该修改其核心逻辑）：**

| 文件/目录 | 职责 | 状态 |
|---|---|---|
| `main.go` | 进程入口，启动 Hertz 服务，装配所有 runtime | ✅ 已完成 |
| `internal/middleware/auth.go` | JWT Bearer Token / API Key 鉴权中间件 | ✅ 已完成 |
| `internal/middleware/auth_client.go` | 通过 gRPC 调用 auth-service 验证 Token | ✅ 已完成 |
| `internal/middleware/rbac.go` | 基于角色的权限控制（RBAC） | ✅ 已完成 |
| `internal/middleware/audit.go` | 操作审计日志 | ✅ 已完成 |
| `internal/router/auth.go` | `/api/v1/auth/*` 认证接口（登录/Token/API Key） | ✅ 已完成 |
| `internal/router/network_resources.go` | VPC、子网、安全组接口 | ✅ 已完成 |
| `internal/router/storage_resources.go` | 云硬盘（Volume）、文件系统接口 | ✅ 已完成 |
| `internal/router/k8s_cluster_resources.go` | K8s 集群管理接口 | ✅ 已完成 |
| `internal/router/encryption_resources.go` | 数据加密（SM4/AES）接口 | ✅ 已完成 |
| `internal/router/secret_resources.go` | Secret/密钥注入接口 | ✅ 已完成 |
| `internal/router/observability.go` | 监控/日志/告警接口 | ✅ 已完成 |
| `internal/router/metering_resources.go` | 计量/用量统计接口 | ✅ 已完成 |
| `internal/router/registry_resources.go` | 容器镜像仓库（Harbor）接口 | ✅ 已完成 |

**Services 团队主责的 `/api/v1/svc/*` handler：**

这些接口归属 Services 业务 handler；实现时必须先更新 `repo/api/openapi/services/v1.yaml`，再改 handler、服务实现和生成物，不能直接把旧占位替换成绕过契约的逻辑：

| 占位函数 | 待实现接口 | 对接的微服务 |
|---|---|---|
| `registerModels()` | `GET/POST /api/v1/svc/models`、`GET/DELETE /api/v1/svc/models/:model_id`、模型版本接口、模型导入 | `model-service` |
| `registerInferenceServices()` | `GET/POST/PATCH/DELETE /api/v1/svc/inference-services/:service_id`、日志流 | `inference-service`（待建） |
| `registerKnowledgeBases()` | 知识库 CRUD、文档管理、向量查询、流式查询 | `kb-service` |
| `registerTasks()` | `GET/DELETE /api/v1/tasks/:task_id` | `task-service` |
| `inferenceProxy` | `POST /v1/chat/completions`、`GET /v1/inference/stream`（OpenAI 兼容） | `inference-service` |

**开发规范（ani-gateway 内）：**

- 新增路由时，必须在 `router.go` 的 `RegisterWithOptions` 中注册，不得绕过
- 新增对其他微服务的调用，必须通过 API/SDK 或明确评审过的 service client，不得直接 import 其他服务的业务代码包
- 禁止在 ani-gateway Services handler 中新增对 `pkg/ports`、`pkg/adapters`、`pkg/bootstrap` 或 Core service `internal` 包的 import（已有历史代码由 Services boundary gate 识别，不得扩散）
- 所有 handler 函数需从 `middleware.GetTenantID(c)` 获取租户 ID，不得信任请求体里的租户参数
- 有副作用的 POST/PUT/PATCH 必须支持 `idempotency_key`，同步 RBAC scope、审计事件、错误结构和 AsyncTask/状态机语义

---

#### `repo/services/auth-service/` — 认证/鉴权服务（gRPC）

**是什么：**  
独立运行的 gRPC 服务，监听 `:9101` 端口。负责用户登录（OIDC/SSO）、JWT 签发与验签、Refresh Token、API Key 管理。ani-gateway 的所有认证操作都通过 gRPC 调用此服务完成。

**已实现的核心文件：**

| 文件 | 职责 |
|---|---|
| `internal/service/oidc.go` | OIDC 标准登录流程（对接企业 SSO、GitHub 等） |
| `internal/service/jwt.go` + `token_issuer.go` | JWT 签发与验签，1 小时有效期 |
| `internal/service/refresh_tokens.go` | Refresh Token 管理，支持滑动续期 |
| `internal/service/api_keys.go` | API Key 创建/列出/撤销 |
| `internal/service/token_blocklist.go` | 登出黑名单（基于 Redis），防止 Token 被重放 |

**Services 团队主要开发任务：**

- 对接具体的 OIDC Provider 配置（企业 AD/LDAP、OAuth2 厂商）
- 多租户用户体系完善（租户隔离的 API Key 命名空间）
- 启动配置（`internal/config/`）完善，支持从环境变量加载 OIDC Provider 配置

---

#### `repo/services/model-service/` — 模型管理服务（待完整实现）

**是什么：**  
管理 AI 模型的元数据、版本信息、模型文件存储路径、导入状态。提供 gRPC 接口供 ani-gateway 路由层调用。

**当前状态：**  
基础框架已有（`go.mod`、`main.go`、`internal/config/`），核心业务逻辑尚未实现。

**Services 团队需要实现：**

- 模型元数据的 CRUD（`POST /api/v1/svc/models`，对应接口在 `repo/api/openapi/services/v1.yaml`）
- 模型版本管理（`Model` → `ModelVersion` 关联关系）
- 模型导入流程（`POST /api/v1/svc/models/import`，异步任务，对接对象存储）
- 模型状态机：`pending` → `importing` → `ready` / `failed`

**可以使用 Core 提供的能力（通过 Core SDK 或 HTTP 调用）：**

- 对象存储（MinIO）：通过 Core API `GET /api/v1/objects` 存取模型文件
- 异步任务：通过 Core API `GET /api/v1/tasks/:task_id` 查询导入进度

---

#### `repo/services/kb-service/` — 知识库服务（待从零实现）

**是什么：**  
管理向量知识库的创建、文档导入（解析、切片、向量化）、向量检索（RAG）。

**当前状态：**  
目录存在但为空，需要 Services 团队从零创建。

**Services 团队需要实现：**

- 知识库 CRUD，对应 `repo/api/openapi/services/v1.yaml` 中 `/knowledge-bases` 相关接口
- 文档导入流程：上传 → 解析（PDF/Word/TXT）→ 文本切片（chunking）→ 向量化（调用 Embedding 模型）→ 写入向量库
- 向量检索：接收用户 query → 向量化 → 在 Milvus 中检索 Top-K → 返回结果
- 流式查询：`GET /api/v1/svc/knowledge-bases/:kb_id/query/stream`（SSE 格式）

**可以使用 Core 提供的能力：**

- 向量存储（Milvus）：通过 Core API `POST/GET /api/v1/vector-stores` 操作向量集合
- 对象存储：通过 Core API 存取原始文档文件

---

#### `repo/services/task-service/` — 异步任务服务

**是什么：**  
处理耗时操作（模型导入、文档向量化等）的异步任务队列。使用 NATS JetStream 作为消息队列。

**Services 团队主要开发任务：**

- 完善任务状态机（`pending` → `running` → `succeeded` / `failed`）
- 为每类业务操作（模型导入、文档处理）注册对应的 Worker Handler
- 实现任务进度上报（百分比、当前步骤描述）

---

#### `repo/services/reconcile-worker/` — 后台对账 Worker

**是什么：**  
定时对账进程，用于修正数据库记录状态与实际基础设施状态不一致的情况（例如：数据库显示推理服务"运行中"，但实际 Pod 已崩溃）。

**Services 团队主要开发任务：**

- 完善各资源类型（推理服务、知识库）的对账逻辑
- 配置对账周期（框架已有定时触发机制）

---

#### `repo/services/metering-service/` — 计量服务（占位）

**是什么：**  
负责 AI 推理 Token 用量统计，供 BOSS 后台的用量报表使用。当前为占位目录。

**Services 团队主要开发任务：**

- 实现 Token 用量聚合（按租户、模型、时间维度）
- 对接 Core 的计量 API（`/api/v1/metering/usage`）

---

### 1.2 `repo/frontends/` — 前端 Web 应用

> 此目录下全部为 JavaScript/TypeScript/React 代码，与后端 Go 服务完全独立，使用 `npm` / `node` 工具链构建，不属于 Go 工程。

#### `repo/frontends/console/` — 用户控制台（Console）

**是什么：**  
面向租户用户的 Web 控制台，用于管理 AI 算力资源（VM/容器/GPU）、AI 模型、推理服务、知识库等。

**技术栈（已确定，不可随意变更）：**

| 依赖 | 说明 |
|---|---|
| React + react-dom | 前端框架 |
| **TDesign React**（`tdesign-react`） | UI 组件库（腾讯 TDesign，**唯一指定**，禁止混用 Ant Design / MUI 等） |
| `tdesign-icons-react` | TDesign 图标库 |
| `@tanstack/react-router` | 路由管理（文件式路由，在 `src/routes/` 下） |
| `@tanstack/react-query` | 服务端状态管理（API 数据缓存与同步） |
| `zustand` | 客户端全局状态管理 |
| `openapi-fetch` | 类型安全的 API 调用客户端，类型由 `services/v1.yaml` 代码生成 |
| `echarts` + `echarts-for-react` | 数据可视化图表 |

**目录结构约定：**

```
src/
├── api/          ← openapi-fetch 生成的类型定义和请求函数（不要手写，由 codegen 生成）
├── components/   ← 公共可复用组件（按功能模块分子目录）
├── routes/       ← 页面路由文件（TanStack Router 文件式路由，一个文件对应一个页面）
├── demo/         ← 开发调试用 demo，不出现在生产构建
├── App.tsx       ← 应用根组件
└── main.tsx      ← 应用入口，挂载 Router/QueryClient/Store
```

**开发约定：**

- 所有 API 调用必须通过 `openapi-fetch` 客户端发起，指向 `/api/v1/*` 或 `/api/v1/svc/*` 路径
- 不得直接使用 `fetch()` 或 `axios.get()` 拼接 URL 字符串，必须使用类型安全的 API 客户端
- UI 组件优先使用 TDesign 提供的组件，不引入其他 UI 框架
- 颜色、间距、字体使用 TDesign Design Token，不硬编码 CSS 颜色值
- 多租户：页面中所有资源列表的查询都必须携带当前 tenant 上下文（通过登录态 token 自动携带），不得展示跨租户数据

---

#### `repo/frontends/boss/` — 运营后台（BOSS）

**是什么：**  
面向平台运营人员的后台管理系统，用于租户管理、用户管理、资源配额、账单、系统监控等。

**当前状态：**  
目录存在但为空，需要 Services 团队从零搭建。技术栈和目录结构建议与 `console/` 保持一致（相同的 React + TDesign 技术栈）。

---

### 1.3 `repo/api/openapi/services/v1.yaml` — Services API 契约

**是什么：**  
Services 层所有业务 API 接口的**唯一权威定义文件**（OpenAPI 3.0 格式）。定义了 `model-service`、`kb-service`、`inference-service` 对外暴露的 HTTP 接口、请求/响应 Schema。

**基础 URL：** `https://{host}/api/v1/svc`  
（注意：Services API 的 URL 前缀是 `/api/v1/svc`，而非 `/api/v1`，两者不能混用）

**开发规则：**

- **每新增一个业务接口，必须先在此文件中定义好接口规范（Schema），再写实现代码**（API-First 原则）
- 维护完整的 request/response schema，包括所有必填字段、枚举值、错误码
- 此文件是前端 Console 使用 `openapi-fetch` 生成 TypeScript 类型的来源，接口定义变更后必须重新运行代码生成
- 修改此文件的 PR 必须同时提供对应的后端实现或明确标注 `TODO`
- `repo/sdks/services/`、`repo/docs/api/services.html` 和前端 schema 均为生成物；只能由 OpenAPI 生成流程更新，不得手工编辑绕过契约

**当前已定义的接口路径（14条）：**

```
GET    /models                              列出模型列表（游标分页）
POST   /models                              创建模型记录
POST   /models/import                       异步导入模型（返回 task_id）
GET    /models/{model_id}                   获取模型详情
DELETE /models/{model_id}                   删除模型
GET    /models/{model_id}/versions          列出模型版本
POST   /models/{model_id}/versions          创建新版本

GET    /inference-services                  列出推理服务
POST   /inference-services                  创建推理服务
GET    /inference-services/{service_id}     获取推理服务详情
PATCH  /inference-services/{service_id}     更新配置（扩缩容/修改参数）
DELETE /inference-services/{service_id}     删除推理服务

GET    /knowledge-bases                     列出知识库
POST   /knowledge-bases                     创建知识库
GET    /knowledge-bases/{kb_id}             获取知识库详情
DELETE /knowledge-bases/{kb_id}             删除知识库
GET    /knowledge-bases/{kb_id}/documents   列出文档
POST   /knowledge-bases/{kb_id}/documents   上传文档
DELETE /knowledge-bases/{kb_id}/documents/{doc_id}  删除文档
POST   /knowledge-bases/{kb_id}/query       向量查询（JSON）
GET    /knowledge-bases/{kb_id}/query/stream 向量查询（SSE 流式）
```

**待补充（当前缺失，开发前必须补全）：**

- 所有资源的完整 response schema（当前大部分 response 只有占位描述）
- 推理服务日志流接口（SSE）
- 资源状态枚举值（模型导入状态、推理服务运行状态）
- 操作可用性说明：哪些状态下哪些操作不可用（如：`ready` 状态的推理服务才能扩缩容）
- 创建前置条件的文档注释（如：创建推理服务前必须有 `ready` 状态的模型版本）

---

## 2. Services 团队禁止修改的目录

> 以下目录由 ANI Core 团队维护，通过 GitHub CODEOWNERS 在服务器端强制保护。Services 团队成员提交的 PR 如果触碰以下任何路径，会自动请求 Core 团队 review，且在 Core 团队 approve 之前无法合并。

| 目录/文件 | 原因 |
|---|---|
| `repo/pkg/` | Core 基础设施库代码（K8s、Ceph、OVN、GPU 等 adapter 实现） |
| `repo/api/openapi/v1.yaml` | Core API 契约（接口变更须由 Core 团队评审） |
| `repo/deploy/` | 集群部署配置（Helm Chart、K8s Manifest、Karmada） |
| `repo/sdks/core/` | Core 官方 SDK（由 Core API 契约自动生成，不手写） |
| `repo/sdks/services/` | Services 官方 SDK 生成物（由 `services/v1.yaml` 自动生成，不手写；提交生成结果需共同 review） |
| `repo/cli/` | ANI CLI 工具（`ani` 命令） |
| `repo/installer/` | ANI 一键安装器 |
| `repo/operators/` | K8s Operator；`repo/operators/inference-operator/` 是 Services/Core shared bounded operator，不是 Services-only 目录 |
| `repo/scripts/` | Core 构建/验证脚本 |
| `repo/architecture/` + `repo/design/` | Core 架构决策文档 |
| `CLAUDE.md` | AI 开发强制规则，只有架构负责人修改 |

**如果发现 Core API 不满足需求怎么办？**  
在 GitHub 提 Issue 并标记 `@ani/core-team`，说明缺失的功能点和使用场景。Core 团队评审通过后会扩展 `api/openapi/v1.yaml` 并实现对应能力。**Services 团队不得自行修改 Core 相关文件。**

---

## 3. 与 ANI Core 的交互规范

### 3.1 调用 Core API 的正确方式

Services 微服务需要使用基础设施能力（存取文件、操作向量库、查询实例状态等）时，通过 **Core OpenAPI REST API / Core SDK**（经由 ani-gateway）：

```go
// 方式一：直接使用 net/http 调用
resp, err := http.Get("http://ani-gateway:8080/api/v1/vector-stores/my-collection")

// 方式二（推荐）：使用 Core SDK（repo/sdks/core/ 的客户端）
client := coresdk.NewClient("http://ani-gateway:8080", apiKey)
result, err := client.VectorStores.Search(ctx, "my-collection", query)
```

### 3.2 禁止直接 import Core 内部代码包

```go
// ❌ 禁止：直接 import Core 内部代码
import "github.com/kubercloud/ani/pkg/adapters/runtime"   // 禁止
import "github.com/kubercloud/ani/pkg/ports"              // 新代码禁止
import "github.com/kubercloud/ani/pkg/bootstrap"          // 禁止

// ✅ 允许：import Core 的 proto 生成类型（纯数据结构，只读）
import authv1 "github.com/kubercloud/ani/pkg/generated/pb/auth/v1"
import commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
import modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"

// ✅ 允许：import Core 的通用类型
import "github.com/kubercloud/ani/pkg/types"
```

> **说明**：`ani-gateway` 目前历史遗留有直接 import `pkg/ports` 和 `pkg/adapters` 的代码，这些**暂时保留但不再新增**。Task 1 已为 `model-service` 的 `pkg/bootstrap` 依赖和 RAG `pymilvus` 直连建立精确 accepted baseline；它们只代表存量告警，不代表边界合规或 production-ready。model-service、kb-service 等新微服务严格通过 Core OpenAPI REST API / Core SDK 调用，不得新增 Core 内部包 import。

### 3.3 `go.mod` 中的 `replace` 指令

Services 微服务的 `go.mod` 中有：

```
replace github.com/kubercloud/ani/pkg => ../../pkg
```

这个指令让 Go 编译器在本地找到 `pkg` 目录。**仅在 Services 微服务需要 import `pkg/generated/pb/` 或 `pkg/types` 时才需要保留**。如果某个新微服务完全不 import Core 任何代码包，则可以删除这行。

---

## 4. 仓库协作规范（GitHub 工作流）

### 4.1 代码权限边界（CODEOWNERS）

仓库根目录的 `.github/CODEOWNERS` 文件定义了目录所有权。当 PR 触碰某目录时，GitHub 自动 Request Review 给该目录的所有者，且在所有者 approve 之前 PR 无法合并：

```
# Services 业务服务与产品侧代码（Services 主审，Core 共同可见）
/repo/services/model-service/                                      @viccao-yue @e92nf872rp
/repo/services/kb-service/                                         @viccao-yue @e92nf872rp
/repo/ai/                                                          @viccao-yue @e92nf872rp
/repo/frontends/                                                   @viccao-yue @e92nf872rp
/repo/operators/inference-operator/                                @viccao-yue @e92nf872rp
/repo/api/openapi/services/v1.yaml                                @e92nf872rp @viccao-yue

# Core 团队负责的目录（Services 团队修改需要 Core approve）
/repo/pkg/                                                         @e92nf872rp
/repo/api/openapi/v1.yaml                                         @e92nf872rp
/repo/deploy/                                                     @e92nf872rp
/repo/scripts/                                                    @e92nf872rp
/repo/sdks/core/                                                  @e92nf872rp
/repo/cli/                                                        @e92nf872rp
/repo/installer/                                                  @e92nf872rp
/CLAUDE.md                                                        @e92nf872rp
```

### 4.2 分支与 PR 规范

- **`main` 分支已启用保护**，任何人不得直接 `git push main`（服务器会拒绝）
- 开发流程：

```bash
# 1. 从 main 创建 feature 分支
git checkout -b feature/model-service-crud

# 2. 开发，提交到自己的分支
git add repo/services/model-service/...
git commit -m "feat(model-service): implement model CRUD"
git push origin feature/model-service-crud

# 3. 在 GitHub 提 Pull Request，目标分支选 main
# 4. 等待 @ani/services-team 中至少一人 approve
# 5. CI 通过后 Squash and Merge
```

- PR 描述需说明：改了什么、影响哪些 API 接口、如何本地验证
- 修改了 `services/v1.yaml` 的 PR，必须同时提交更新后的 Console 前端 API 类型生成结果
- 所有 Services POST/PUT/PATCH 的 request schema 必须要求 `idempotency_key`；异步 `202` 必须返回 `AsyncTask`，操作必须声明认证要求。当前已存在的逐操作缺口只可按 `repo/architecture/services-contract-baseline.yaml` 精确登记并接受 warning，新增缺口或 stale 基线不能合并。
- Services SDK 与静态 API docs 必须从 OpenAPI source 重新生成；`make validate-services` 会检查 `repo/sdks/core/`、`repo/sdks/services/`、`repo/docs/api/` 无生成漂移。
- Services PR 的最短必跑命令：

```bash
cd /Users/zhangfan/ANI/repo
make validate-services
make validate-doc-entrypoints
git diff --check
```

- 共同审查触发条件：PR 触碰 `repo/api/openapi/v1.yaml`、Core 保护目录、Gateway shared/mixed handler、`repo/api/openapi/services/v1.yaml`、Services SDK/API docs/Console schema 等 generated artifacts 时，必须在 PR 描述中列出触碰原因，并按 CODEOWNERS 请求 Core/Services 共同 review。

### 4.3 本地开发环境变量

| 变量名 | 说明 | 本地默认值 |
|---|---|---|
| `AUTH_SERVICE_ADDR` | auth-service gRPC 地址 | `127.0.0.1:9101` |
| `ANI_AUTH_MODE` | 设为 `dev` 可跳过 Token 验证（**仅本地开发**，不得提交到代码） | 不设置（生产强校验模式） |
| `X-Dev-Tenant-ID` | 配合 `ANI_AUTH_MODE=dev` 指定开发用租户 ID | `00000000-0000-0000-0000-000000000001` |
| `DATABASE_URL` | PostgreSQL 连接串 | — |
| `REDIS_ADDR` | Redis 地址 | — |
| `NATS_URL` | NATS JetStream 地址 | — |

---

## 5. ANI Core 各目录功能参考（只读）

> 以下是 Core 团队维护目录的说明，帮助 Services 团队了解 Core 提供了哪些现成能力，判断哪些功能不需要自己实现，直接调用 Core API 即可。

### `repo/pkg/` — Core 基础设施库

```
pkg/
├── ports/        ← 产品能力的 Go interface 定义（规范，不是实现）
│                    WorkloadRuntime（VM/容器/GPU容器/沙箱/批量任务调度）
│                    GPUInventory（GPU 发现、分类、调度）
│                    NetworkService（VPC/子网/安全组/负载均衡）
│                    StorageService（云盘/文件系统）
│                    K8sClusterService（K8s 集群生命周期）
│                    EncryptionService（SM4/AES 加密/解密）
│                    SecretService（K8s Secret 注入）
│                    CacheStore（Redis 缓存接口）
│                    VectorStore（Milvus 向量库接口）
│
├── adapters/     ← 上述 interface 的具体实现，对接真实基础设施组件
│   ├── runtime/      实例运行时（KubeVirt VM、K8s Pod、GPU 容器）
│   ├── gpu/          GPU 发现（NVIDIA/AMD/昇腾华为）
│   ├── redis/        Redis CacheStore 实现
│   ├── vectorstore/  Milvus VectorStore 实现
│   ├── objectstore/  MinIO 对象存储实现（兼容 S3 API）
│   ├── registry/     Harbor 镜像仓库实现
│   ├── postgres/     PostgreSQL 连接池
│   ├── nats/         NATS JetStream 消息队列实现
│   └── identity/     租户/用户身份基础数据
│
├── generated/    ← 由 .proto 文件生成的 Go 代码（机器生成，不要手写）
│   └── pb/
│       ├── auth/v1/     认证服务 proto 类型 ← Services 可 import
│       ├── common/v1/   公共类型：TenantContext、PageInfo 等 ← Services 可 import
│       └── model/v1/    模型相关 proto 类型 ← Services 可 import
│
├── types/        ← 跨层公用基础类型 ← Services 可 import
├── bootstrap/    ← Core 服务启动框架（Services 不引用）
├── nats/         ← NATS 工具函数（Services 不引用）
└── repo/         ← 数据库访问层 repository 接口（Services 不引用）
```

---

### `repo/api/openapi/v1.yaml` — Core API 契约（只读参考）

Core 对外暴露的所有 REST API 的接口定义文件（OpenAPI 3.0 格式）。  
**基础 URL：** `https://{host}/api/v1`

Services 团队**调用**这些接口，不修改这个文件。主要能力分组如下：

| 路径前缀 | 能力 | Services 是否常用 |
|---|---|---|
| `/api/v1/instances` | VM/容器/GPU容器/沙箱/批量任务实例管理 | 可能（管理推理服务底层实例） |
| `/api/v1/networks/*` | VPC、子网、安全组、弹性 IP、负载均衡 | 可能（推理服务网络配置） |
| `/api/v1/volumes` | 云硬盘（块存储，可挂载到实例） | 可能 |
| `/api/v1/filesystems` | 共享文件系统（NFS/CephFS，多实例共享） | 可能（模型文件共享挂载） |
| `/api/v1/objects` | 对象存储（MinIO，类 S3 API，用于存文件） | **常用**（模型文件上传/下载） |
| `/api/v1/vector-stores` | 向量集合管理（Milvus） | **常用**（知识库向量存储与检索） |
| `/api/v1/k8s-clusters/*` | K8s 集群生命周期 | 一般不直接使用 |
| `/api/v1/registry/*` | Harbor 镜像仓库管理 | 一般不直接使用 |
| `/api/v1/encryption/*` | 数据加密/解密（SM4/AES，国密合规） | 可能（敏感模型数据加密） |
| `/api/v1/secrets` | K8s Secret 注入（凭证安全传递） | 可能（推理服务凭证注入） |
| `/api/v1/auth/*` | 认证（OIDC 登录/JWT Token/API Key） | 已由 ani-gateway + auth-service 实现 |
| `/api/v1/observability/*` | 监控指标、日志查询、告警规则 | **常用**（推理服务监控、日志） |
| `/api/v1/metering/*` | 用量计量（CPU/GPU 算力秒、Token 数） | **常用**（BOSS 用量报表数据来源） |
| `/api/v1/tasks/*` | 异步任务状态查询与取消 | **常用**（模型导入进度查询） |
| `/api/v1/gpu-inventory` | GPU 资源发现与分类（机型/显存/数量） | 可能（推理服务调度参数参考） |

---

### `repo/sdks/` — 官方 SDK（调用入口）

```
sdks/
├── core/      ← Core API 的官方 Go/Python 客户端 SDK
│                （由 api/openapi/v1.yaml 自动代码生成，不手写）
└── services/  ← Services API 的官方 SDK
                 （由 api/openapi/services/v1.yaml 自动代码生成，不手写）
```

Services 微服务调用 Core API 时，优先使用 `sdks/core/` 提供的客户端，而非手写 HTTP 请求字符串。

---

### `repo/deploy/` — 部署配置（参考）

```
deploy/
├── helm/         ← ANI 全量 Helm Chart（K8s 标准化部署）
├── manifests/    ← 原始 K8s YAML Manifest（调试用）
├── migrations/   ← 数据库 Schema 迁移文件（PostgreSQL，按版本顺序执行）
├── docker/       ← 各服务 Dockerfile
├── real-k8s-lab/ ← 真实物理集群验证环境配置
└── ...
```

Services 团队如需为新微服务新增 Dockerfile 或 Helm 配置，在此目录下提 PR，需要 Core 团队 review 后合并。

---

### `repo/cli/` — ANI CLI 工具

面向开发者和 DevOps 的命令行工具（`ani` 命令），调用 Core API。由 Core 团队维护。Services 不修改，但可以提 Issue 请求 Core 团队增加对 Services 资源（模型、推理服务等）的 CLI 子命令支持。

---

## 6. 开发前检查清单

开始开发新功能前，请确认以下事项：

- [ ] 确认所需的 Core 基础设施能力已存在（查阅 `repo/api/openapi/v1.yaml`）；若不存在，先提 Issue 给 Core 团队，不要猜测实现
- [ ] 在 `repo/api/openapi/services/v1.yaml` 中定义好新接口的 Schema，再开始写后端实现代码（API-First）
- [ ] 确认本 PR 未修改 Core 保护目录；如必须触碰，已在 PR 描述中列出 Core review 原因
- [ ] 新建微服务时，`go.mod` 模块名使用 `github.com/kubercloud/ani/services/{service-name}` 格式
- [ ] 所有配置项通过环境变量读取，放在 `internal/config/` 目录，不硬编码
- [ ] 本地联调可设置 `ANI_AUTH_MODE=dev` 跳过 Token 验证，但此配置**绝不能提交到代码或 PR**
- [ ] PR 提交前运行 `go build ./...` 和 `go test ./...`，确保编译和单元测试通过
- [ ] 修改了 `services/v1.yaml` 后，运行前端的 API 类型代码生成命令并提交更新结果
- [ ] 运行 Services boundary gate、API split、architecture gate 和 `git diff --check`

---

*文档维护：如有疑问或发现内容过期，请联系 ANI Core 团队或在仓库提 Issue。*
