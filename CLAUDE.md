# CLAUDE.md

This file provides mandatory guidance for Claude Code / Codex / Cursor / GPT coding agents working in this repository.

> 本文件是 AI/人类开发的**轻量入口和强制规则索引**。动态进度、批次细节和历史记录不在本文重复维护；首次加载必须按本文的读取顺序进入真实来源。

---

## 0. 当前状态

```text
本仓库范围：ANI Core 继续负责基础设施平台底座；ANI Services 进入受控并行 PR 阶段。
Core Sprint 13/14 既有事实继续有效：Sprint 13 production-shaped live gate 与 Sprint 14 resilience 隔离 fixture 结论不外推 full platform production ready。
Services 受控并行 PR 阶段：目录、API、handler、生成物和跨层边界分别受 CODEOWNERS 共同审查、API split、Services boundary gate、make validate-architecture 约束。
当前阶段：以 repo/CURRENT-SPRINT.md 为准
当前重心：以 repo/CURRENT-SPRINT.md 为准
当前不是 Phase 2：Phase 2 是 2026-10 以后延期能力
下一步入口：repo/CURRENT-SPRINT.md
文档导航：ANI-DOCS-INDEX.md
首个正式版本目标：v1.0.0，2026-09-30
```

本文只维护稳定规则、读取顺序和不可破坏的工程边界。当前 Sprint 的详细完成项、未完成项、验收命令和下一步，只允许维护在 `repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` Section 零和 `repo/development-records/README.md`。

### 仓库范围与 Services 受控解冻（强制）

1. **Core 保护范围只负责 ANI Core；本仓库同时允许 Services 受控 PR。** Core 是基础设施平台底座，对外输出 Core OpenAPI REST API、Core SDK、CLI。
2. **ANI Services 当前是受控并行 PR，不再按旧冻结规则处理。** Services 主责目录可按 `ANI-SERVICES-TEAM-GUIDE.md` 开发；触碰 Core 保护目录、Services API、Gateway mixed handler 或生成物时必须按 CODEOWNERS 共同审查。
3. **Services 解冻不解除跨层门禁。** Services 业务资源只进 `repo/api/openapi/services/v1.yaml`；实现必须先改 API 契约，再改 handler/SDK/生成物；PR 必须通过 API split、`python scripts/validate_services_boundary.py --root .`（Services boundary gate）和既有 `make validate-architecture`。
4. 跨层契约仍以 `repo/api/openapi/v1.yaml`（Core）为唯一真实来源；Services 业务资源不回流 Core API。

### 本地真实开发环境提示

当前工作区已具备三台真实物理开发/验证/测试服务器，可在需要执行真实底座开发、在线验证或 live gate 时使用。服务器硬件与软件系统基线、管理 IP、带外访问方式、SSH 登录用户名和密码记录在本机本地文件 `local-secrets/dev-physical-servers.md`。

该文件包含敏感凭据，已通过本机 Git 排除规则避免向 GitHub 推送；AI agent 可在需要真实环境时读取并使用，但不得把其中的 IP、用户名、密码或完整凭据内容复制到可提交文件、development records、公开日志或最终回复中。

---

## 1. 首次加载顺序

每次新会话、切换模型、上下文压缩或准备继续开发时，按顺序读取：

```text
1. CLAUDE.md
2. ANI-DOCS-INDEX.md
3. repo/CURRENT-SPRINT.md
4. ANI-06-开发计划.md 的 Section 零和当前 Sprint
5. 本次任务直接相关的 API 契约、代码、测试和批次记录
```

只根据 `repo/CURRENT-SPRINT.md` 判断当前要做什么。不要从历史 handoff、旧批次记录、路线图远期规划或 Phase 2 内容倒推当前任务。

---

## 2. 真实来源

| 问题 | 真实来源 |
|---|---|
| 文档导航和读取顺序 | `ANI-DOCS-INDEX.md` |
| 当前 Sprint、状态、验收命令 | `repo/CURRENT-SPRINT.md` |
| 全局阶段、Services 解锁门禁、延期项 | `ANI-06-开发计划.md` |
| 已完成批次索引 | `repo/development-records/README.md` |
| 单批次实现与验证细节 | `repo/development-records/*.md` |
| Core API 契约 | `repo/api/openapi/v1.yaml` |
| Services API 契约 | `repo/api/openapi/services/v1.yaml` |
| Core API Beta 准备矩阵 | `repo/api/core-beta-readiness.yaml` |
| Core API v1 兼容性基线 | `repo/api/core-v1-compatibility-baseline.yaml` |
| 产品边界、路线图、版本、代码规范、ports/adapters 细则 | `ANI-02`、`ANI-03`、`ANI-11`、`ANI-12`、`ANI-13` |

进度状态冲突时，以 `ANI-06-开发计划.md` Section 零和 `repo/CURRENT-SPRINT.md` 为准。工程约定冲突时，以本文的强制规则为准。

---

## 3. 强制架构边界

1. ANI 分为 ANI Core 和 ANI Services 两层。ANI Core 只负责基础设施平台能力，跨层控制面契约只输出 Core OpenAPI REST API、Core SDK、CLI；不得包含模型推理、RAG、PaaS 业务逻辑。
2. ANI Services 只能通过 Core OpenAPI REST API / Core SDK 调用 Core；禁止 import Core 代码包、直接调用 Core 内部 gRPC service 或直接操作底层组件。
3. `repo/services/model-service/` 和 `repo/services/kb-service/` 逻辑属于 ANI Services，暂存于 monorepo。Core 服务禁止调用它们。
4. Services 业务资源如 `models`、`inference-services`、`knowledge-bases` 必须维护在 `repo/api/openapi/services/v1.yaml`，不得回流到 Core API。
5. `CORE-DEV-PROFILE-A` 是 Core dev/local profile，不是 Services 业务 mock。Services 团队如需业务 mock，应在 Services 层自行建设。

---

## 4. API 与 SDK 强制规则

1. `repo/api/openapi/v1.yaml` 是 ANI Core 对外 REST API 和 Core/Services 跨层控制面契约的唯一真实来源；所有新 Core API 必须先改 API 契约，再写实现、测试和 SDK。
2. Core API `servers[0].url` 必须为 `https://{host}/api/v1`；Services API `servers[0].url` 必须为 `https://{host}/api/v1/svc`。
3. gRPC/Proto 用于 Core 内部高效通信和实现组织，不作为 Services 绕过 Core OpenAPI 的跨层产品契约；当 Proto 与 REST schema 描述同一资源冲突时，以 OpenAPI 契约为准。
4. Core API v1 允许新增可选 request 字段、response 字段、端点和枚举值；删除字段、改字段类型、删除端点、修改 HTTP 方法、修改错误语义均属于破坏性变更。
5. 所有 POST 创建和有副作用的 PUT/PATCH 必须支持 `idempotency_key`；客户端重试必须复用同一个 key。
6. Core SDK 来源是 `repo/api/openapi/v1.yaml`；Services SDK 来源是 `repo/api/openapi/services/v1.yaml`。SDK 不得包含对方层资源类型。
7. Core Mock Server 本地默认地址是 `http://127.0.0.1:4010/api/v1`，只覆盖 Core API 契约，不提供 Services 业务 mock。
8. Sprint 4 的 SDK helper、Mock Server、静态 API 文档和兼容性基线细节不在本文展开；以 `repo/CURRENT-SPRINT.md`、`repo/development-records/README.md` 和对应校验脚本为准。

---

## 5. 组件边界强制规则

1. `pkg/ports/` 中的 port 指产品能力抽象/接口边界，不是 TCP/IP 端口。
2. 只要某组件承载 ANI 产品能力、会被 Core service / Services / API handler 依赖，或存在合理替换/多实现可能，就必须经过 `pkg/ports/` 和 `pkg/adapters/`。
3. 业务服务不得直接依赖 MinIO、Milvus、NATS JetStream、Redis、Harbor、CloudNativePG 等组件 SDK；直接导入必须有 allowlist、`coupling_level` 和保留理由。
4. Kubernetes API 属于 `bounded_direct`：允许在 adapter/controller/preflight 等边界内原生使用；禁止在 Gateway handler、Core domain service、ANI Services 业务服务中直接使用 K8s SDK 或拼装 provider 对象。
5. `ports` 不封装完整 Kubernetes SDK；`ports` 只表达 ANI 产品意图，例如 `WorkloadRuntime`、`WorkloadProviderApply`、`NetworkProviderRenderer`。
6. VM、Container、GPU Container、Sandbox、Batch Job、Notebook、K8s Cluster、BM、DPU 都必须先经过 `WorkloadRuntime` 能力抽象；异构 GPU 发现、分类和调度必须经过 `GPUInventory`。

---

## 6. 开发、验证、文档闭环

1. 代码生成批次使用可回溯命名，例如 `M2.1-TASK-A`、`M1-INSTANCE-U`。历史 `Stage 3A/3B/3C` 仅可作为旧名说明。
2. 每个批次完成时，必须通过当前批次验收命令、`make test`、`make validate-architecture`、`git diff --check`。
3. 每个批次完成时，按批次类型执行不同更新规则：
   - **Feature batch**（新增产品能力、代码边界、API 路径、live gate 定义等）：必须更新 `repo/development-records/{批次名}.md`、`repo/development-records/README.md`、`repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md`（四个文件均需更新）。
   - **Guard micro-batch**（连续类型守卫、空值诊断守卫、路径守卫等同类重复批次，如 M1-REAL-LAB-* 系列）：**只在 `repo/development-records/guard-series/{series}-guard-index.md` 追加一行**（批次 ID + 类别 + guard 名称即为完整记录，不再单独建 `{批次名}.md`）+ 更新 `repo/CURRENT-SPRINT.md` 中对应 guard series 条目的"最新 guard ID"标记（单行）；不更新 README.md 完整列表，不更新 ANI-06 Section 零。guard 的可验证证据由对应校验脚本（如 `scripts/validate_real_k8s_profile.py`）及其测试承载，不需要逐 guard 的独立 markdown 记录。
   - 判断标准：批次属于已有系列（如 M1-REAL-LAB-*），且本质是边界检查/诊断守卫而非新产品能力。新系列首次建立时视为 Feature batch。
4. Sprint 切换时，必须同步 `ANI-06-开发计划.md`、`repo/CURRENT-SPRINT.md`、`ANI-DOCS-INDEX.md`。
5. 进度文档必须以当前工作区真实代码、API 契约和测试为准；云端开发或 GitHub 推送状态不明时，不得把未落地能力标记为完成。
6. 从 Sprint 5 起，涉及 K8s、Kube-OVN、KubeVirt、vCluster、KMS/SM4、K8s Secret 注入等真实底座组件的能力，必须并行建设真实环境门禁；`REAL-K8S-LAB-A` 和 `make validate-real-k8s-profile` 是当前真实底座验证入口。local profile 只能证明 API/SDK/状态机/调用边界，不能标记为 real-provider、runtime ready 或 production ready。
7. `CLAUDE.md` 是轻量入口和稳定强制规则文件，禁止写入单批次完成清单、API path 长列表、文件级变更清单、每日开发流水账或历史归档。动态进度必须写入 `repo/CURRENT-SPRINT.md`、`ANI-06-开发计划.md` Section 零或 `repo/development-records/*.md`。
8. 修改文档入口后必须运行 `make validate-doc-entrypoints`，防止 `CLAUDE.md` 再次膨胀或与文档职责矩阵冲突。
9. **Guard 冻结令（2026-05-30 起强制）：** REAL-K8S-LAB guard 系列（`M1-REAL-LAB-*`）已达边际收益递减，默认**冻结，不再新增**。唯一例外：真实 live gate 执行中**实际复现了**某个缺陷，且该缺陷只能用一个新 guard 防回归——此时才允许新增一个 guard，并在 guard-index 追加一行注明它对应的真实缺陷。不得再为"假设可能出现"的输入、字段、空值、空格、类型边角预防性地批量生成 guard。当前精力必须投向真实 live gate 跑通，而非扩充契约校验器。

---

## 7. 提交与版本

提交前至少执行：

```bash
cd repo
make test
make validate-architecture
git diff --check
```

发布或预发布必须遵守 `ANI-12-版本管理策略.md`。ANI 使用 SemVer，首个正式版本是 `v1.0.0`，目标日期 `2026-09-30`；当前不得标记为 `v1.0.0` 或 RC。

---

## 8. Karpathy 五条开发原则

### 原则一：先思考，再编码
**不要假设。不要掩饰困惑。要揭示取舍。**

- 如果需求有歧义，明确说出来并询问，而不是悄悄选一种猜测实现
- 存在多种合理方案时，列出并说明各方案的取舍，由人决策
- 面对复杂问题，先陈述理解再动手
- 遇到不确定的地方，停下来问，而不是带着疑惑继续

### 原则二：用能解决问题的最小代码
**拒绝一切带有猜想的实现。**

- 不实现没被要求的功能，哪怕"感觉以后用得到"
- 不为一次性代码创建抽象层
- 不加"灵活性""可配置性"等未被要求的扩展点
- 200 行能写成 50 行的，重写

### 原则三：只触碰你必须改动的部分
**只清理你自己制造的脏。**

- 不顺手"优化"任务范围之外的代码、注释或格式
- 不重构没坏的东西
- 保持现有代码风格，即使你有不同偏好
- 发现死代码，提出来，不要自作主张删除

### 原则四：定义成功标准，循环迭代直到验证通过
**把任务转化为可验证的目标。**

- 每个任务开始前明确"什么状态算完成"
- 多步骤任务先列出简短计划和验证步骤
- 优先给 Claude 成功标准而非操作指令：不是"修复这个 bug"，而是"写一个能复现 bug 的测试，再修复它"

### 原则五：奥卡姆剃刀，控制复杂度
**如无必要，勿增实体；如有必要，必须证明。**

- 新增组件、服务、port、adapter、数据库表、后台 worker、队列、配置项或外部依赖前，必须说明它解决的当前问题、为什么现有边界不能承载、最小可验证闭环是什么
- 不得为了"未来可能需要"提前引入抽象、插件化、多实现、通用框架或复杂配置
- 不得以"简化"为理由绕过已经强制的架构边界、安全边界、多租户边界、API 契约、真实 provider 门禁、审计、幂等、测试和文档闭环
- 优先复用现有能力边界；只有当复用会破坏语义、稳定性、安全性或长期兼容性时，才允许新增实体
