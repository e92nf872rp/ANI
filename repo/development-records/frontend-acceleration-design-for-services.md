# ANI 前端功能点加速设计文档（交付 ANI Services / 前端团队）

> **读者：** ANI Services 产品团队、前端工程团队（可能首次接触本仓库）、以及负责生成代码的 AI Coding agent（任意模型）。
> **文档目标：** 让产品功能点从「逐页从零设计」变为「契约 + 20 行页面规约 → AI 生成 80%、人工打磨 20%」，在保持统一前端风格的同时加速产品功能丰富，并打通「UI 点击 → API 调用」的端到端闭环。
> **记录类型：** Design / cross-team handoff（非完成记录）。
> **权威边界：** 本文是设计与协作约定；跨层契约真实来源仍是 `repo/api/openapi/v1.yaml`（Core）与 `repo/api/openapi/services/v1.yaml`（Services）。两者冲突时以 OpenAPI 契约为准。

---

## A. 项目背景补充（给首次接触本仓库的团队）

### A.1 ANI 是什么
ANI 是一套**专有云平台**。用户的所有操作都通过 **Web Console（前端）**完成，因此前端是产品价值的最终出口。平台分两层：

| 层 | 职责 | 对外接口 | 仓库位置 |
|---|---|---|---|
| **ANI Core** | 基础设施平台底座（workload/GPU/网络/存储/对象/向量/观测/密钥等） | Core OpenAPI REST（`/api/v1`）、Core SDK、CLI | 本仓库（活跃开发） |
| **ANI Services** | 模型推理、RAG、PaaS 等业务能力 | Services OpenAPI REST（`/api/v1/svc`） | 本仓库内**只读冻结骨架**，业务由**外部产品团队**开发 |

> **产品范围澄清（务必对齐，避免按陈旧认知做窄）：** ANI 专有云**不是玩具，也不是"模型推理平台"**——"模型推理"只是 Services 层的一个业务，且其旧骨架已冻结。ANI 的目标是**生产环境可用、功能丰富的基础设施平台**：计算（VM / 容器 / GPU 容器 / **AI-native 沙箱** / Batch / Notebook / 裸金属 / K8s 集群）、存储（块/文件/对象/快照）、网络（路由/隔离）、向量、观测、密钥与安全、多租户。前端必须能承载这个**全栈广度**且**可高效持续增加功能而不走进死胡同**——这正是本文「契约 + page-spec → 生成」方案的目的：广度靠**统一模板 + 契约驱动生成**线性扩展，而不是每类资源各写一套互不复用的页面。

### A.2 契约真实来源（前端类型与调用都从这里来）
- **Core 资源**：`repo/api/openapi/v1.yaml`，`servers[0].url = https://{host}/api/v1`。
- **Services 资源**（models、inference-services、knowledge-bases 等）：`repo/api/openapi/services/v1.yaml`，`servers[0].url = https://{host}/api/v1/svc`。
- 规则：**先改契约，再写实现/测试/SDK/前端**。所有 POST 创建与有副作用的 PUT/PATCH 必须带 `idempotency_key`，客户端重试复用同一 key。

### A.3 现有前端栈（已落地，请勿重搭，更不要抓竞品前端代码）
位置 `repo/frontends/console`，技术栈**已经是契约驱动的**：

| 能力 | 选型 | 说明 |
|---|---|---|
| 框架 | React 18 | — |
| **设计系统/组件库** | **tdesign-react `^1.10`** | 风格统一的基座，所有页面复用其组件与 token |
| 路由 | `@tanstack/react-router` | 文件式路由在 `src/routes/`（已有 `kb/`、`models/`、`settings/`） |
| 数据请求 | `@tanstack/react-query ^5` + **`openapi-fetch ^0.12`** | 类型安全的 API client |
| **类型生成** | **`openapi-typescript ^7`** | `make gen-console-api` **同时**生成两套：Services → `src/api/schema.d.ts`、Core → `src/api/core-schema.d.ts`（已存在，~187KB） |
| 图表 | echarts | 观测/仪表盘 |
| 状态 | zustand | 轻量本地状态 |

构建命令（已存在，**已同时生成 Core 与 Services 两套类型**）：`make gen-console-api`：
```
npx openapi-typescript api/openapi/services/v1.yaml -o frontends/console/src/api/schema.d.ts   # Services
node frontends/console/scripts/gen-core-schema.mjs                                              # Core → core-schema.d.ts
```
> **现状（已核对代码，2026-06-22）：** 契约驱动的客户端管线**已经全部接好**——`src/api/client.ts`（Services，`/api/v1/svc`）与 `src/api/coreClient.ts`（Core，`/api/v1`，注释明示用于 instances/metering/tasks/registry）均已存在，类型也已生成。**因此"搭客户端"不是瓶颈。** 真正缺的见 B.3。

### A.4 Mock 与联调
- **Core Mock Server**：本地 `http://127.0.0.1:4010/api/v1`，**只覆盖 Core 契约**，不提供 Services 业务 mock。
- **Services Mock**：Core 不负责。Services 团队需为 `services/v1.yaml` 自建 mock（同样可用 OpenAPI mock 工具，如 Prism，指向 `services/v1.yaml`）。

### A.5 责任边界（谁做什么）
- **Core 团队（本仓库）**：保证契约无漂移、Mock 有真实示例、四语言 SDK 与契约同步。**Core 不在本仓库写 Console 页面。**
- **Services / 前端团队**：产品功能点定义、Console 页面、页面规约与生成器、Services 后端与 Services mock。

---

## B. 问题陈述与反模式

### B.1 两个真实瓶颈
1. **功能点定义慢**：每个产品功能要逐页人工设计交互、字段、动作，产出慢。
2. **UI 慢 + 风格易走样**：从零写页面慢，多人各写风格不一致，功能又怕「太随意」。

### B.2 明确反对的做法：抓取竞品前端
不要用 Kimi/webbridge 等把某竞品平台运行中的前端代码/逻辑/接口扒下来改造。原因：
- **法律风险**：抓取商业平台前端代码改成自有商业产品涉著作权/ToS。
- **架构逆行**：抓来的 UI 绑定的是**别人的** API shape 与领域模型，要拆下来再焊到 ANI 契约上，「裁剪改造」成本通常**高于**从自有契约生成。
- **不解决根因**：根因是产品定义未结晶，抓 UI 给你的是别人的产品决策，未必契合 ANI 领域。
- **风格更乱**：抓多个不同平台 → 风格更不统一，与目标相反。

> **可以做的是「参考交互范式」**：KubeSphere / OpenStack Horizon / Grafana / 公有云控制台的**交互模式**（资源列表/详情/创建向导/状态/日志/RBAC/多租户切换）是行业通用、非受保护的。借鉴**模式**，不复制**代码**。

### B.3 真正缺的是什么（已核对代码 2026-06-22，不是猜测）
现有 `frontends/console`：20 个源文件、~10.8k 行、**真实在用 openapi-fetch**，页面有 `index / kb / kb/$kbId/chat / models / models/import / registry / usage / settings / api-keys`。

诚实结论两点：
1. **客户端/类型管线已完备**（见 A.3 现状），所以瓶颈**不在搭基建**。
2. **现有页面几乎都围绕已冻结的旧 Services 语义**（kb / models / chat / RAG）——这正是「产品范围澄清」里说的陈旧框定。**平台广度页面（instances=VM/容器/GPU/沙箱、networks、storage/volumes、k8s-clusters、observability 等 Core 资源）目前根本不存在。**

> 因此真正要解决的是：**如何按平台广度，快速且风格统一地把"不存在的页面"成批生产出来。** 这恰是 C 节 page-spec + 模板方案的目标——它面对的是「广度铺开」，不是「从零搭架子」。

---

## C. 核心方案：AI 生成 80% + 人工打磨 20%（详解）

### C.1 为什么能做到 80/20——底层洞察
一个基础设施/PaaS 控制台，约 **90% 的页面是「对资源的增删改查 + 状态 + 日志」**。每个资源页几乎是两个输入的**确定性函数**：

```
资源页 = f( OpenAPI 资源 Schema , 页面规约 page-spec )
```

- **输入①：OpenAPI 资源 Schema**（已存在、机器可读）。它已经包含：字段名、类型、是否必填、枚举值、格式（date-time/uri/...）、operationId（list/get/create/update/delete）。**这部分根本不需要人再描述一遍。**
- **输入②：页面规约 page-spec**（人写的 ~20 行 YAML）。它只补 Schema 推不出来的「产品决策」：列表显示哪些列、列顺序与中文标签、详情分区、有哪些动作、状态字段映射、关联资源跳转。

**「80%」= 从（Schema + page-spec）确定性生成的代码**；**「20%」= 人写 page-spec + 特殊交互/视觉打磨**。

### C.2 80% 具体生成什么（确定性产物）
给定一个资源，AI/生成器**确定性**产出：

| 产物 | 来源 | 工具 |
|---|---|---|
| TypeScript 类型 | OpenAPI schema | `openapi-typescript`（已有） |
| 类型安全 API client 调用（list/get/create/update/delete） | operationId | `openapi-fetch`（已有） |
| react-query hooks（queryKey、mutation 带 `idempotency_key`） | operationId + 约定 | 模板 |
| **ResourceList 页**（表格列、游标分页、过滤） | page-spec 列定义 + schema | 模板 |
| **CreateWizard 表单**（必填→校验、enum→下拉、format→控件、date→日期选择） | schema 字段约束 | 模板 |
| **ResourceDetail 页**（分区、字段展示、关联跳转） | page-spec 详情分区 | 模板 |
| 路由接线（`@tanstack/react-router`） | 资源名 | 模板 |
| Loading / Error / Empty 态 | 共享模板 | 模板 |

### C.3 20% 具体留给人（产品 + 打磨）
- 写 page-spec（产品决策：哪些列重要、措辞、分区、特殊控件如 GPU 拓扑视图/日志查看器）。
- 视觉与边界交互打磨、跨资源流程（如「创建实例后跳到详情并轮询状态」）。
- Schema 无法编码的业务规则（联动校验、特殊确认弹窗）。

### C.4 AI 如何生成（换模型也稳定）
两种等价实现，团队按习惯选其一：
- **(a) 确定性生成器脚本**：读 `{schema + page-spec}` 套**固定模板**输出页面文件。可重跑、可 diff、零随机性。
- **(b) AI Coding agent**：给 agent 一个固定 prompt：「按 `src/templates/` 的锁定模板，针对资源 X 的 schema 与 page-spec，生成 list/detail/create 页面与 hooks」。

> **为什么换模型也稳定**：输入（契约 + page-spec）是结构化的，模板是**锁定**的。模型只是把结构化输入填进固定模板，**契约和模板不变 → 产物语义不变**。这与本仓库「契约是唯一真实来源、AI 据此生成」的协作模式一致。

### C.5 一个完整的 worked example（以 **Core 平台资源 `instances`** 为例——刻意选平台核心资源，非已冻结的 models）

> `instances` 是 ANI 计算实例：一个 operationId 覆盖 **VM / 容器 / GPU 容器 / AI-native 沙箱**（`kind` 枚举），充分体现平台广度。下面 schema 节选自真实 `repo/api/openapi/v1.yaml` 的 `CreateInstanceRequest` / `InstanceRecord`。

**第 1 步｜契约已有的 schema（节选自 `v1.yaml`，真实字段）：**
```yaml
CreateInstanceRequest:
  required: [name, kind, idempotency_key]
  properties:
    idempotency_key: { type: string }                                  # 契约强制
    name:   { type: string, minLength: 1, maxLength: 128 }
    kind:   { type: string, enum: [vm, container, gpu_container, sandbox] }
    image:  { type: string, nullable: true }
    cpu:    { type: string, example: "2" }
    memory: { type: string, example: "4Gi" }
    gpu:    { type: object, properties: { vendor: {…}, model: {…}, count: {…} } }
    replicas: { type: integer, minimum: 1, default: 1 }
    sandbox_config: { $ref: '#/components/schemas/SandboxConfig' }
# InstanceRecord 含：id / kind / state(enum: pending..running..failed) / provider / endpoint / ssh / volumes / gpu
# operationId: listInstances / getInstance / createInstance
```

**第 2 步｜人写 page-spec（~20 行，这就是「功能点定义」的轻量产物）：**
```yaml
# frontends/console/src/specs/instances.page.yaml
resource: instances
api: core                # 用 v1.yaml（Core）；Services 资源写 services
title: 计算实例
list:
  columns:               # Schema 推不出「显示哪些列/中文标签/顺序」——这是产品决策
    - { field: name,     label: 名称 }
    - { field: kind,     label: 类型 }
    - { field: state,    label: 状态, as: statusBadge }   # 复用状态徽章模板
    - { field: provider, label: 运行后端 }
  rowAction: detail
create:                  # 不写字段=默认取 schema 全部可写字段；这里只覆盖顺序/措辞
  fields: [name, kind, cpu, memory, image, gpu]
detail:
  sections:
    - { title: 基本信息, fields: [name, kind, state, provider, endpoint] }
    - { title: 资源,     fields: [cpu, memory, gpu, volumes] }
  actions:
    - { label: 启动, intent: lifecycle, op: start }   # 自定义动作，wiring 留给 20% 人工
    - { label: 停止, intent: lifecycle, op: stop }
```

**第 3 步｜生成（80%，确定性产出）：**
```
src/api/core-schema.d.ts                    # 已存在，无需新建；用 coreApi(`/api/v1`) 调用
src/routes/instances/index.tsx              # ResourceList：name/kind/state/provider 列 + 游标分页
src/routes/instances/$instanceId.tsx        # ResourceDetail：基本信息+资源分区 + 启动/停止按钮（占位）
src/routes/instances/new.tsx                # CreateWizard：kind 下拉(enum) + cpu/memory + idempotency_key
src/api/hooks/instances.ts                  # useListInstances / useGetInstance / useCreateInstance(react-query)
```
- `kind` 自动渲染成**下拉框**（schema 是 enum：vm/container/gpu_container/sandbox）；`name` 必填校验（schema required）；`state` 用 statusBadge 模板（pending→running→failed 状态机）。
- createInstance 的 mutation **自动注入 `idempotency_key`**（满足契约强制规则）。
- 选不同 `kind` 时表单按需展示 `gpu`/`sandbox_config`（由 schema 的 nullable/对象结构推导，模板内置条件渲染）。

**第 4 步｜人工打磨（20%）：**
- 给「启动/停止」按钮接真实生命周期 operationId + 危险操作确认（VM 的 `termination_protection`）。
- 状态徽章配色、`kind=gpu_container` 时展示 GPU 利用率小图（echarts）、`kind=sandbox` 时展示会话剩余时间。
- 「创建成功后跳详情并轮询 state 直到 running」这段跨资源流程。

### C.6 端到端功能点调用闭环（UI 点击 → API）
```
OpenAPI operationId
  → openapi-typescript 生成类型 (schema.d.ts)
  → openapi-fetch 生成类型安全调用
  → 生成的页面 + react-query hook
  → 调 /api/v1/svc/...（Services）或 /api/v1/...（Core）
  → 开发期打 Mock（Core: 127.0.0.1:4010；Services: 团队自建 mock）
  → 联调期切真实后端 handler
```
**改契约 → 重生成类型与页面 → 前后端同时更新**：每个功能点从点击到 API 调用都是契约驱动的闭环，避免前后端字段漂移。

---

## D. 落地步骤（可执行，标注责任域）

| 步骤 | 动作 | 责任域 | 依赖 |
|---|---|---|---|
| **F-P0a** 保持契约↔SDK↔类型无漂移 | 持续跑 `make validate-sdk-beta`。2026-06-23 复核该门禁已通过，`createNetworkRoute/createStorageBucket/createVolumeSnapshot/insertVectorStoreDocuments/uploadStorageObject` 等历史缺口已不再复现；后续目标是防止新增漂移。**前端类型继承契约，这是提速前置。** | **Core 团队** | — |
| ~~F-P0b Core 类型也生成~~ | **已完成（无需再做）**：`gen-console-api` 已含 `gen-core-schema.mjs`，`core-schema.d.ts` + `coreClient.ts` 均已存在。仅需在契约更新后**重跑** `make gen-console-api` | — | — |
| **F-P0c** Mock 补 examples | 在 OpenAPI 的 schema/response 补 `examples`，使 mock 返回真实形状数据，前端不等后端即可开发 | Core（Core 资源）/ Services（Services 资源） | — |
| **F1** 锁定页面模板 | 用 tdesign 实现 5 个模板组件：`ResourceList` / `ResourceDetail` / `CreateWizard` / `StatusTimeline` / `LogViewer`，放 `src/templates/`。**一次性**。 | 前端团队 | tdesign |
| **F2** 定义 page-spec + 生成器 | 固定 `*.page.yaml` schema；写生成器（脚本或 AI prompt）读 `{OpenAPI schema + page-spec}` → 输出页面 + hooks | 前端团队 | F1 |
| **F3** 逐资源出页 | 外部团队交付资源定义后，写 page-spec（分钟级）→ 生成 80% → 打磨 20% | Services + 前端 | F2 + 产品定义 |
| **F4** 切真后端 | 对应后端 handler / live gate 就绪后，从 mock 切真实 | 协同 | 后端就绪 |

---

## E. 风格统一 + 产品丰富度如何兼得（回应「不能太随意」）
- **风格统一**：所有页面强制走 F1 的 5 个 tdesign 模板 + 设计 token → 「构造即一致」，不靠人盯。
- **功能不随意**：**page-spec 的 PR 评审 = 产品功能点定义的轻量 gate**。评审 page-spec（20 行）比评审整页 React 代码快得多，产品/设计在此把关「哪些列、哪些动作、措辞」，既快又不失控。
- **丰富度**：复用模板后，新增一个资源页的边际成本降到「写 page-spec + 少量打磨」，于是能在同样人力下覆盖更多功能点。

---

## F. 边界与不做什么
- 不抓取/复制任何竞品前端代码、逻辑或接口定义（见 B.2）。
- 不绕过契约：页面字段/调用必须来自 `v1.yaml` / `services/v1.yaml`，禁止前端自造未定义字段。
- Core 不在本仓库写 Console 页面；Console 与 page-spec/生成器属 Services/前端团队执行域。
- 产品功能点的最终定义权在外部产品团队；本方案只把「定义→可运行页面」的链路压到最短。
- 未接真实后端的页面只能标 mock 联调，不得标 production。

---

## G. 给 Core 团队的最小行动项（不越界、立即可做）
Core 能拉动整体前端速度的杠杆是「契约 readiness pack」：
1. 持续跑 `make validate-sdk-beta`，确认契约↔SDK 没有新增漂移（F-P0a；2026-06-23 已通过）。
2. 给 Core 契约补 `examples`，让 `127.0.0.1:4010` mock 返回真实形状（F-P0c）。

（注：Core 类型生成已完成——`gen-console-api` 已产出 `core-schema.d.ts`，无需再做。）

> 把这三件做扎实，外部前端团队按契约 codegen 即可飞，这是「迭代速度滞后」在 Core 侧的正解——而非 Core 自己去抓或建前端。

---

## 附录 H. 构建规格：page-spec 语法 + 模板契约 + 生成器 I/O（F1/F2 的实现基石）

> **诚实声明（2026-06-23）：** 本附录是 F1/F2 的**构建规格，对应内容当前尚不存在**——`frontends/console/src/templates/`、`src/specs/`、生成器脚本均为**待建**。本规格基于**已存在**的栈：`tdesign-react` + `@tanstack/react-router`（文件式路由 `src/routes/`）+ `@tanstack/react-query` + `openapi-fetch`（已有 `src/api/client.ts` Services、`src/api/coreClient.ts` Core）+ 已生成的 `schema.d.ts`/`core-schema.d.ts`。没有这份规格，"AI 生成 80%" 无法实现，因此它是开工基石。

### H.1 page-spec 完整语法（这是「功能点定义」的可评审产物）
```yaml
resource: <string>            # 资源名，决定路由 /<resource> 与文件夹名
api: core | services          # core → coreApi(/api/v1) + core-schema；services → api(/api/v1/svc) + schema
schema: <ComponentName>       # OpenAPI components.schemas 的记录类型名（如 InstanceRecord）
operations:                   # 省略则按 REST 约定推断；可逐项覆盖
  list:   <operationId>       # 默认 list<Resource>
  get:    <operationId>       # 默认 get<Resource>
  create: <operationId>       # 默认 create<Resource>
  update: <operationId?>      # 可选
  delete: <operationId?>      # 可选
title: <string>               # 页面中文标题
list:
  columns:
    - field: <schema 字段路径>          # 支持点路径，如 gpu.count
      label: <列标题>
      as: text|statusBadge|datetime|bytes|tag|link|code   # 渲染器；缺省 text
      width: <number?>
  filters: [<field>...]       # 可选；生成 query 过滤控件
  pagination: cursor          # 仅游标（契约统一 next_cursor）
create:
  fields: [<field>...]        # 省略 = schema 全部非 readOnly 字段
  hidden: [<field>...]        # 强制隐藏字段
detail:
  sections:
    - title: <string>
      fields: [<field>...]
actions:                      # 列表行 + 详情页动作
  - label: <string>
    intent: detail | lifecycle | custom
    op: <string?>             # lifecycle 用（start/stop/...），映射到对应 operationId
```
**约束：** `field` 必须存在于 `schema`；`api`/`schema`/operationId 必须在对应 OpenAPI 契约里能解析——生成器需校验，否则报错而非静默产错页。

### H.2 五个模板契约（F1 一次性实现，props 即接口）
| 模板 | props（TypeScript 接口要点） | 数据来源 |
|---|---|---|
| `ResourceList<T>` | `{ columns: ColumnSpec[]; query: ()=>UseQueryResult<CursorPage<T>>; onRow(id); filters? }` | react-query + openapi-fetch list |
| `ResourceDetail<T>` | `{ sections: SectionSpec[]; query:(id)=>UseQueryResult<T>; actions?: ActionSpec[] }` | get |
| `CreateWizard<T>` | `{ fields: FieldSpec[]（由 schema 推导）; submit:(body)=>...; onSuccess(id) }` | create，**自动注入 `idempotency_key`** |
| `StatusTimeline` | `{ state: string; stateReason?: string; history?: {revision,at}[] }` | 取自记录 state/状态机 |
| `LogViewer` | `{ query:(id)=>...; follow?: boolean }` | logs operationId（如有） |

> 渲染器 `as:` 全集：`text`（默认）、`statusBadge`（枚举→tdesign Tag 配色）、`datetime`（format=date-time）、`bytes`（数字→人类可读）、`tag`、`link`（跳关联资源）、`code`。F1 把这 7 个实现一次。

### H.3 生成器 I/O 与确定性映射规则（F2）
**输入：** `src/specs/<resource>.page.yaml` + 由 `api`+`schema` 解析出的 OpenAPI schema。
**输出（确定性，可重跑可 diff）：**
```
src/routes/<resource>/index.tsx          # ResourceList 实例化
src/routes/<resource>/$<resource>Id.tsx  # ResourceDetail 实例化
src/routes/<resource>/new.tsx            # CreateWizard 实例化
src/api/hooks/<resource>.ts              # useList/useGet/useCreate（coreApi 或 api）
```
**schema → UI 控件映射（生成器内置，无需人写）：**
| schema 约束 | 生成的控件/行为 |
|---|---|
| `required` | 表单字段必填校验 |
| `enum` | tdesign `Select`，选项=枚举值 |
| `format: date-time` | `DatePicker` / datetime 渲染 |
| `type: integer/number` | `InputNumber` |
| `type: boolean` | `Switch` |
| `nullable` 对象（如 `gpu`/`sandbox_config`） | 条件分组，按需展示 |
| `readOnly` | 只进详情，不进创建表单 |
| create mutation | 自动注入 `idempotency_key`（uuid v4），满足契约强制 |

### H.4 生成器**不**做（这就是「20% 人工」的边界）
- 自定义控件：GPU 利用率小图（echarts）、日志查看器交互、拓扑视图。
- 跨资源流程：创建后跳详情并轮询 state、批量操作。
- 业务联动校验、危险操作确认弹窗（如 VM `termination_protection`）。
- 文案、配色、空态细节。

> 有了 H.1–H.4，F1（实现 5 模板 + 7 渲染器）与 F2（按本规格写生成器）即可**独立开工**，不再依赖产品定义；产品定义到位后，F3 才是「写 page-spec → 生成 → 打磨」的高速循环。这三层（模板/生成器先建，page-spec 后填）的先后，是本方案能落地而非空谈的关键。
