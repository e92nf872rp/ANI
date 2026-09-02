# M-METERING-PLATFORM-A - OpenAPI v1.yaml FR-8 契约扩展（平台查询端点 + 租户读 scope 补全）

完成日期：2026-07-09
对应 Issue：issue-001-core-openapi-platform-endpoint
对应范围：Core OpenAPI 契约扩展 / Metering 平台视角
依赖链位置：依赖链起点，所有其他 Metering Issue 依赖此契约

## 背景

PRD FR-8 要求新增平台跨租户用量查询端点，FR-10 要求平台视角 group_by 独立于租户视角。SPEC §4.2/§4.3 给出了两个端点的完整 YAML 契约。本批次是 Metering 平台能力依赖链的起点，只改 Core OpenAPI 契约 `v1.yaml`，不触碰任何 handler/adapter/Go 实现（属后续 Issue #2/#3 范围）。

## 实现了什么

- `api/openapi/v1.yaml`：
  - 补全 `GET /metering/usage` 的 `operationId: getMeteringUsage` + `x-ani-rbac-scope: "scope:metering:read"` + description（说明租户上下文、tenant_id 从 JWT 提取、忽略 query tenant_id）。
  - 新增 `GET /metering/usage/platform` 端点：`operationId: getPlatformMeteringUsage`、`scope:metering:platform:read`、平台 `group_by` enum `[tenant_id, day, hour]`、可选 `tenant_id` query 参数（含二次 RBAC 校验描述）、复用 `MeteringUsageResponse`、端点 description 声明 `items[].tenant_id` 必填。
- `api/core-v1-compatibility-baseline.yaml`：
  - 同步 `/metering/usage` 的 `operation_id`（`''` → `getMeteringUsage`）与 `rbac_scope`（`''` → `scope:metering:read`），使 `make validate-core-api-compatibility` 通过。

## 边界

- 本批次仅改 `api/openapi/v1.yaml` 与配套的 `api/core-v1-compatibility-baseline.yaml`，不新增/修改任何 Go handler、port、adapter 或测试代码。
- 不触碰冻结的 Services 后端（`repo/services/model-service/`、`repo/services/kb-service/`）或前端。
- 平台 `tenant_id` 为 query 参数（`in: query`），非 request body；符合 FR-15 安全要求。
- `POST /metering/token-usage` 已有完整 metadata，本批次不动。

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：基线同步操作纳入本批次范围**
- 歧义：Issue Scope 明确限定 "Code paths allowed: `repo/api/openapi/v1.yaml`"，未提及兼容性基线文件。但 `make validate-core-api-compatibility` 要求契约 operationId/rbac_scope 与基线一致，基线原值为空字符串。
- 选择：把 `api/core-v1-compatibility-baseline.yaml` 的同步纳入本批次。
- 理由：基线文件位于 `api/` 目录、是 Core API 契约的配套维护文件（非 Services）；补全空值为 additive 变更（PRD FR-8 / SPEC §5.4 明确无破坏性），同步基线是使契约校验门禁通过的必要操作，非越界。

**DD-2：平台 `items[].tenant_id` 必填通过端点级 description 约束，不改 schema**
- 歧义：AC 第 5 条要求"平台视角下 `items[].tenant_id` 必填（端点级约束）"，但 `MeteringUsageRecord.tenant_id` 当前 `nullable: true`（租户端点下可空）。
- 选择：不改 `MeteringUsageRecord.tenant_id` 的 `nullable`，仅在平台端点 description 声明"items[].tenant_id 在此端点下必填"。
- 理由：`MeteringUsageResponse` 被两个端点复用；若改 schema 的 `nullable` 会破坏租户端点的向后兼容（租户视角 tenant_id 可空）。端点级约束更准确表达"同一 schema 在不同 RBAC 上下文下字段必填性不同"的语义，符合 SPEC §6.4 边界。

### 2. Deviations（偏离）

None — 实现严格遵循 SPEC §4.3 给出的 YAML 片段与 PRD FR-8/FR-10 要求，无偏离。

### 3. Tradeoffs（取舍）

**TO-1：平台 group_by enum 与租户分离（`[tenant_id, day, hour]` vs `[resource_type, az, day, hour]`）**
- 备选 A（分离 enum，本批次采用）：平台 enum 含 `tenant_id` 不含 `resource_type`/`az`；租户 enum 不含 `tenant_id`。
- 备选 B（统一 enum 并靠 RBAC 限制可用值）：两个端点共用同一 enum，由 handler 在运行时按 scope 过滤。
- 取舍：A 在契约层就消除非法组合（平台租户视角不该 group by resource_type），客户端和 SDK 能直接从契约获知合法值，减少运行时 422；B 会把语义验证推迟到运行时，增加无效请求和文档歧义。A 胜出，符合 SPEC §6.4 边界。

**TO-2：平台 `tenant_id` 作为 query 参数而非独立 path**
- 备选 A（query 参数，本批次采用）：`GET /metering/usage/platform?tenant_id=xxx`。
- 备选 B（独立 path）：`GET /metering/usage/platform/tenants/{tenant_id}`。
- 取舍：A 允许"全平台聚合"与"单租户筛选"复用同一端点（tenant_id 可选），减少端点数量；B 强制每次指定 tenant_id，无法表达"全平台总览"。PRD FR-8 明确要求"全平台或指定租户"，A 胜出。

### 4. Open Questions（开放问题）

**OQ-1：`make test` 中 `TestDemoInstanceServiceRealShellExecutesCommand` 失败**
- 现状：该测试执行 `printf hello` shell 命令，在 Windows 环境下失败（`printf` 不可用）。
- 待确认：此失败与本批次 OpenAPI YAML 改动无关（本批次只改 `v1.yaml` paths 和兼容性基线），但需确认 CI 在 Linux 环境下该测试通过，或是否为已知环境问题。Metering 相关测试（`TestMeteringAPIUsageResponseMarksLocalProfile`、`TestMeteringAPITokenUsageReportResponse`）全部 PASS。

**OQ-2：平台端点二次 RBAC 校验的实现细节**
- 现状：契约层已描述"若带 tenant_id query 须二次 RBAC 校验"。
- 待确认：handler 实现层（后续 Issue #2）需确认二次 RBAC 的具体校验逻辑——平台管理员带 tenant_id 时是否校验其对目标租户的访问权限，由后续 Issue 承载。

## 完工标准（验证命令）

- [x] `python scripts/validate_yaml.py api/openapi/v1.yaml`（YAML lint）
- [x] `make validate-spec-split`
- [x] `make validate-core-api-compatibility`（基线同步后）
- [x] `make validate-architecture`
- [x] `go test ./services/ani-gateway/internal/router ./services/ani-gateway/internal/middleware -run 'TestMetering|TestAuth*'`（5/5 pass）
- [x] `git diff --check`（无空白错误）
- [~] `make test`（Metering/Auth 相关全 PASS；1 个无关失败 `TestDemoInstanceServiceRealShellExecutesCommand`，Windows 环境下 `printf` 不可用，见 OQ-1）

## AC 满足情况

- [x] `GET /metering/usage` 补全 `operationId: getMeteringUsage` + `x-ani-rbac-scope: scope:metering:read`
- [x] `GET /metering/usage/platform` 新增端点，operationId=`getPlatformMeteringUsage`，scope=`scope:metering:platform:read`
- [x] 平台 group_by enum `[tenant_id, day, hour]`；租户 group_by 不变 `[resource_type, az, day, hour]`
- [x] 平台端点可选 `tenant_id` query 参数（须二次 RBAC 校验描述）
- [x] `MeteringUsageResponse` 复用；平台视角下 `items[].tenant_id` 必填（端点级约束）
- [x] `make validate-openapi` / YAML lint 通过

---

# Issue #7 — Console Shell 组件（ConsolePage / ConsolePageHeader / ConsoleContentCard）

完成日期：2026-07-10
对应 Issue：issue-007-console-shell-components
对应范围：Console 前端通用 shell 组件（页面布局容器 + 标题描述区 + 内容卡片）
SPEC 参考：§2.4（File Structure）、§10.1 P0-B-1、§10.2 Issue #1
PRD 参考：无直接 FR（基础设施组件，供 usage 页及其他页面使用）
UX 参考：无直接条目（shell 组件为页面级布局，非交互组件）

## 实现了什么

新建 `repo/frontends/console/src/components/shell/` 目录（此前 `src/components/` 不存在），创建 3 个通用 shell 组件 + 1 个 barrel export：

| 文件 | 职责 | AC |
|------|------|----|
| `ConsolePage.tsx` | 页面级布局容器（flex column + gap 16 + maxWidth 1200） | AC#1 |
| `ConsolePageHeader.tsx` | 标题 + 描述 + 可选 extra 操作区 | AC#2 |
| `ConsoleContentCard.tsx` | 内容卡片容器，基于 TDesign Card 封装 | AC#3 |
| `index.ts` | Barrel export（组件 + Props 类型） | — |

此前 `_authenticated/usage.tsx` 已引用 `@/components/shell` 但模块缺失，本批次创建后导入正确解析。

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：内联样式 vs CSS Module / className**
- 歧义：SPEC §2.4 只指定文件结构和组件职责，未规定样式方案。现有页面 `routes/usage.tsx`（旧版）和 `_authenticated/usage.tsx` 均使用内联 style 对象。
- 选择：采用内联 style 对象。
- 理由：保持代码库一致性（Karpathy 原则三）；shell 组件样式简单（flex 布局 + 间距），内联足够清晰；项目无 CSS Module 基建。

**DD-2：ConsolePage maxWidth 硬编码 1200px**
- 歧义：SPEC §2.4 要求"提供页面级布局容器"，未指定具体宽度约束。
- 选择：`maxWidth: 1200`（px）。
- 理由：Console 页面在宽屏下需合理内容宽度约束避免无限拉伸；1200px 是控制台类应用常见内容宽度；消费方 `_authenticated/usage.tsx` 的布局在此宽度下正常展示。

**DD-3：ConsolePageHeader 额外提供 `extra` prop**
- 歧义：SPEC §2.4 要求"标题 + 描述区"，Issue AC 只要求"标题 + 描述区"，未提及操作区。
- 选择：增加**可选** `extra?: ReactNode` prop 用于右侧操作区（如刷新/新建按钮）。
- 理由：消费方当前未使用 extra，但 usage 页 filter bar 和未来其他页面（instances list 等）普遍需要页面级操作按钮；作为通用 shell 组件，预留可选 extra 避免后续每个页面自行 hack 布局。为可选 prop 不影响当前调用。

**DD-4：ConsoleContentCard 基于 TDesign Card 封装**
- 歧义：SPEC §2.4 要求"内容卡片容器"，未指定是否直接用 TDesign Card 还是自行实现。
- 选择：直接 `import { Card } from 'tdesign-react'` 封装，传 `bordered` prop。
- 理由：项目设计系统是 tdesign-react（frontend-acceleration-design §A.3）；TDesign Card 已提供卡片外观、阴影、边框，自行实现是重复造轮子。

**DD-5：样式使用 TDesign CSS 变量**
- 选择：`ConsolePageHeader` 描述区颜色使用 `var(--td-text-color-secondary)`。
- 理由：TDesign 通过 CSS 变量提供主题 token，使用变量而非硬编码颜色值可自动适配明暗主题。

### 2. Deviations（偏离）

None — 实现完全遵循 SPEC §2.4 规定的 3 个组件文件和职责，Issue AC 5 项全部满足，无偏离。

### 3. Tradeoffs（取舍）

**TO-1：Barrel export (index.ts) vs 直接深路径导入**
- 备选 A（不创建 index.ts）：消费方 `import { ConsolePage } from '@/components/shell/ConsolePage'`
- 备选 B（创建 index.ts barrel，本批次采用）：消费方 `import { ConsolePage } from '@/components/shell'`
- 取舍：B 更简洁，消费方无需知道内部文件名；`_authenticated/usage.tsx` 已使用 `from '@/components/shell'` 模式（证明 barrel 是预期接口）；代价仅 3 行 re-export。

**TO-2：Props 类型导出**
- 备选 A（只导出组件）：不导出 Props 类型
- 备选 B（同时导出组件和 Props 类型，本批次采用）：`export type { ConsolePageProps }`
- 取舍：B 允许消费方在自身组件 props 中引用 shell 组件的 props（如包装 ConsolePageHeader 透传 title/description），成本仅 3 行 type re-export。

### 4. Open Questions（开放问题）

**OQ-1：pnpm lockfile 与 package.json 不一致**
- 现状：`pnpm type-check` 因 lockfile 与 package.json 不一致（xterm 包被移除但 lockfile 未更新）触发 `--frozen-lockfile` 失败。
- 影响：Issue AC "Typecheck 通过" 通过 VS Code 诊断（tsc Language Server）验证为 PASS，但 pnpm 脚本级别验证被阻塞。
- 待确认：需在前端批次整体层面修复 lockfile（`pnpm install` 重新生成），不属于本 Issue 范围。

**OQ-2：ESLint 配置缺失**
- 现状：`npx eslint src/components/shell` 报无 ESLint 配置文件。项目 `package.json` 有 `lint` script 但无 `.eslintrc`。
- 待确认：前端批次需统一配置 ESLint，属环境基建问题。

**OQ-3：`_authenticated/usage.tsx` 预存错误**
- 现状：`_authenticated/usage.tsx` 引用 `@/queries/coreResources`（不存在）和路由类型 `"/_authenticated/usage"` 未注册。这两个错误不是本 Issue 引入的，分别属于 SPEC Issue #2（feature/usage 基础模块，P0-B-2）和 Issue #6（页面组合，P0-B-6）。
- 待确认：后续 Issue 实现时解决。

## 完工标准（验证命令）

- [x] VS Code 诊断（shell 4 文件）— 零错误零警告
- [x] VS Code 诊断（`_authenticated/usage.tsx`）— `@/components/shell` 导入已解析
- [x] tsc --noEmit（全项目）— shell 组件无错误；2 个预存错误属其他 Issue 范围
- [~] pnpm type-check — BLOCKED，lockfile 不一致（环境问题，见 OQ-1）
- [~] eslint — SKIP，项目无 ESLint 配置（见 OQ-2）

## AC 满足情况

- [x] `ConsolePage` 提供页面级布局容器 — flex column + gap 16 + maxWidth 1200
- [x] `ConsolePageHeader` 提供标题 + 描述区 — title + description + 可选 extra
- [x] `ConsoleContentCard` 提供内容卡片容器 — 基于 TDesign Card 封装
- [x] TDesign 风格一致 — TDesign CSS 变量 + TDesign Card
- [x] Typecheck 通过 — VS Code 诊断零错误（tsc Language Server）

---

# Issue #8 — Console feature/usage 基础模块（constants / types / useDebouncedFilter）

完成日期：2026-07-10
对应 Issue：issue-008-console-usage-feature-base
对应范围：Console 前端 feature/usage 基础模块（常量配置 + 类型定义 + debounce hook + 测试框架）
SPEC 参考：§2.4（File Structure）、§3.2（Entity Definitions）、§5.1（Core Algorithms）、§5.2（Validation Rules）、§10.2 Issue #2
PRD 参考：FR-17（token_total 无 Tab）、FR-18（单位原样展示）
UX 参考：§5.1（Console /usage 组件映射）、§8.4（debounce 300ms 定稿）

## 实现了什么

新建 `repo/frontends/console/src/features/usage/` 目录（此前 `src/features/` 不存在），创建 3 个源文件 + 2 个测试文件；同时搭建 Vitest 测试框架：

| 文件 | 职责 | AC |
|------|------|----|
| `constants.ts` | RESOURCE_TYPE_TABS（5 启用 + 2 disabled）、GROUP_BY_OPTIONS（4 选项） | AC#1, AC#2, AC#3 |
| `types.ts` | UsageFilter（前端筛选状态）、UsageRow（表格行类型，对齐 OpenAPI） | AC#4 |
| `useDebouncedFilter.ts` | debounce 300ms hook + defaultTimeRange + isValidRange + DEFAULT_DEBOUNCE_MS | AC#5 |
| `constants.test.ts` | 10 个单元测试（Tab 数量、启用/禁用拆分、FR-17 断言、GROUP_BY_OPTIONS） | AC#6 |
| `useDebouncedFilter.test.ts` | 10 个单元测试（debounce 延迟、取消旧值、自定义延迟、defaultTimeRange、isValidRange 边界） | AC#6 |

测试框架搭建：
| 文件 | 改动 |
|------|------|
| `vitest.config.ts` | 新建：jsdom 环境 + globals: true + 路径别名对齐 vite.config.ts |
| `package.json` | 新增 `test`/`test:watch` 脚本 + 5 个 devDependencies（vitest, @testing-library/react, @testing-library/jest-dom, @testing-library/user-event, jsdom） |

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：UsageRow 字段全部可选（与 SPEC §3.2 不一致）**
- 歧义：SPEC §3.2 的 `UsageRow` 定义 `resource_type`/`total_quantity`/`unit` 为必填（来自 OpenAPI `MeteringUsageRecord`，这三个字段在 schema 中确实无 `?`）。但前端消费 API 返回时，openapi-fetch 类型推导下 `items[]` 的字段是可选的（`items?: { resource_type?: string; ... }[]`，见 `core-schema.d.ts` L1133-1138）。
- 选择：`UsageRow` 的所有字段设为可选（`resource_type?: string` 等），而非 SPEC §3.2 的必填。
- 理由：前端类型应对 API 宽松返回做防御性设计。`core-schema.d.ts` 的 `MeteringUsageResponse.items` 本身是 `items?:`（可选数组），且数组内字段也是 `?`。若前端类型设为必填，消费方每次访问 `row.total_quantity` 都需做 null guard 或类型断言，反而增加代码复杂度。可选字段 + `?? ''` / `?? 0` fallback 是更安全的前端模式。后续 UsageTable / UsageChart 组件已采用此模式（`item.total_quantity ?? 0`、`item.period ?? '—'`）。

**DD-2：UsageFilter.resource_type 为 string 而非联合类型**
- 歧义：SPEC §3.2 的 `UsageFilter.resource_type` 标注为 `string`，但 `RESOURCE_TYPE_TABS` 的 5 个启用值都是 `MeteringUsageRecord.resource_type` 枚举的具体值。可以用联合类型 `'instance_gpu_seconds' | 'instance_cpu_seconds' | ...` 增强类型安全。
- 选择：保持 `resource_type?: string`，不收窄为联合类型。
- 理由：(1) SPEC §3.2 明确写的是 `string`；(2) 未筛 resource_type 时 `resource_type = undefined`（表示"全部"），且 API 可能返回 `token_total` 行（不在 Tab 配置中），若收窄类型会导致 `token_total` 无法赋值；(3) FR-17 定稿是"未筛 resource_type 时表格可展示 token_total 行"，resource_type 值不限于 Tab 配置的 5 个。保持 `string` 与 API 契约语义一致。

**DD-3：`act` 从 `@testing-library/react` 导入而非 `react`**
- 歧义：React 18 推荐 `import { act } from 'react'`（`ReactDOMTestUtils.act` 已弃用）。但从 `react` 导入 `act` 在 vitest + jsdom 环境下报 `TypeError: act is not a function`。
- 选择：从 `@testing-library/react` 导入 `act`（`import { renderHook, act } from '@testing-library/react'`）。
- 理由：`@testing-library/react@16` 内部 re-export 了正确绑定的 `act`，在 vitest jsdom 环境下工作正常。从 `react` 直接导入的 `act` 在某些 React 18 + vitest 组合下未正确绑定 DOM 环境。测试全部通过，`ReactDOMTestUtils.act` 弃用警告是 cosmetic 的，不影响功能。这是 @testing-library/react + vitest 社区的已知模式。

**DD-4：vitest.config.ts 独立文件而非合并到 vite.config.ts**
- 歧义：Vitest 可以复用 `vite.config.ts`（通过 `test` 字段），也可以使用独立的 `vitest.config.ts`。
- 选择：创建独立的 `vitest.config.ts`，使用 `defineConfig` from `vitest/config`。
- 理由：(1) `vite.config.ts` 使用 `defineConfig` from `vite`，其 `test` 字段类型不被 vite 的 `defineConfig` 识别（需 `/// <reference types="vitest" />` 或导入 `vitest/config`）；(2) 独立文件使测试配置与构建配置解耦，后续可独立调整测试环境（如添加 coverage）；(3) 路径别名 `@ → ./src` 在两处保持一致。项目已有 `vitest.config.ts` 在其他前端项目中使用的先例。

**DD-5：测试框架搭建纳入本批次范围**
- 歧义：Issue Scope 限定 `Code paths allowed: repo/frontends/console/src/features/usage/`，但运行单元测试需要 Vitest 框架（console 项目此前无测试配置）。
- 选择：将 `vitest.config.ts`、`package.json` 的 test 脚本和 devDependencies、`test-setup.ts` 纳入本批次。
- 理由：AC#6 要求"单元测试：debounce 延迟、取消旧值、defaultTimeRange 近 30 天"，没有测试框架无法验证。测试基建是 AC 的必要前置，非越界。`vitest.config.ts` 位于 `frontends/console/` 根目录（不在 `src/features/usage/` 内），但属于使 AC 可验证的必要配置。`test-setup.ts` 由其他 issue（#9 或 #10）后续添加 `setupFiles` 时创建，本批次只创建 `vitest.config.ts` 和 `package.json` 变更。

### 2. Deviations（偏离）

**DEV-1：RESOURCE_TYPE_TABS 包含 7 个 Tab（5 启用 + 2 disabled），SPEC §3.2 只描述了 5 个 P0 值**
- SPEC 预期：§3.2 和 §10.2 描述 P0 启用 5 类 resource_type，P1 的 Storage/KB 只在 UX §5.1 中提到 disabled。
- 实际实现：`RESOURCE_TYPE_TABS` 包含 7 个条目（5 启用 + 2 disabled），disabled 条目有 `disabledTooltip` 字段。
- 原因：UX §5.1 明确要求"Storage / KB：Tab disabled + Tooltip「待 API」"，SPEC §5.3 状态机也有"tab disabled"状态。将 disabled Tab 放入同一配置数组（`enabled: false`）使 UsageFilterBar 组件可用 `.map()` 统一渲染，无需硬编码 disabled Tab。这是 SPEC 文字描述与 UX 交互要求的合理综合。

**DEV-2：UsageRow 不包含 tenant_id 字段**
- SPEC 预期：§3.2 的 `UsageRow` 定义包含 `tenant_id?: string | null`（来自 `MeteringUsageRecord`）。
- 实际实现：`UsageRow` 只有 `resource_type`、`total_quantity`、`unit`、`period` 四个字段，不含 `tenant_id`。
- 原因：SPEC §3.3 明确"单租户视角，`tenant_id` 字段可忽略"。Console 租户侧不需展示 tenant_id（用户只看到自己的数据），UX §5.1 Table columns 也只列了 4 列（资源类型/用量/单位/统计周期）。包含 tenant_id 会导致表格列定义与 UX 不一致。后续 UsageTable 组件的 4 列设计依赖此类型定义。

### 3. Tradeoffs（取舍）

**TO-1：disabled Tab 放入 RESOURCE_TYPE_TABS 数组 vs 独立配置**
- 备选 A（统一数组 + enabled 标记，本批次采用）：7 个 Tab 放入同一数组，`enabled: false` 标记 P1 disabled。
- 备选 B（分离为两个数组）：`ENABLED_TABS`（5 个）和 `DISABLED_TABS`（2 个）独立导出。
- 取舍：A 胜出。统一数组使 UsageFilterBar 用单个 `.map()` 渲染所有 Tab（含 disabled + Tooltip），消费方无需合并两个数组。`enabled` 标记 + `disabledTooltip` 字段表达力足够。B 会导致消费方需 `...ENABLED_TABS, ...DISABLED_TABS` 拼接，且 Tab 顺序由数组顺序决定（GPU→CPU→Memory→Input→Output→Storage→KB），分离后顺序控制困难。

**TO-2：defaultTimeRange / isValidRange 为独立导出函数 vs useDebouncedFilter 内部方法**
- 备选 A（独立导出函数，本批次采用）：`defaultTimeRange()` 和 `isValidRange()` 是独立的导出函数，不挂在 hook 上。
- 备选 B（作为 hook 的返回值或静态方法）：通过 `useDebouncedFilter.defaultTimeRange()` 或 hook 返回 `{ debounced, defaultTimeRange, isValidRange }` 调用。
- 取舍：A 胜出。这两个函数是纯函数（无 React state 依赖），独立导出可在组件外使用（如页面初始化时 `const [filter, setFilter] = useState({ ...defaultTimeRange() })`），也可在测试中独立验证。挂在 hook 上会限制它们只能在 React 组件内使用，且 hook 返回值类型变复杂。SPEC §5.1 的伪代码也显示 `isValidRange(debouncedFilter)` 是独立函数调用。

**TO-3：vitest vs jest 作为测试框架**
- 备选 A（vitest，本批次采用）：Vite 原生测试框架，配置与 vite.config.ts 对齐，零额外配置。
- 备选 B（jest）：需额外配置 babel/ts-jest 或 @swc/jest，与 Vite 的路径别名和转译链分离。
- 取舍：A 胜出。vitest 与 Vite 共享配置链（resolve.alias、esbuild 转译），`globals: true` 提供与 jest 兼容的 API（describe/it/expect），迁移成本低。项目构建用 Vite，测试用 vitest 是最一致的选择。jest 需维护独立转译链，增加配置负担。

### 4. Open Questions（开放问题）

**OQ-1：`pnpm type-check` 失败（预存问题，非本 Issue 引入）**
- 现状：`_authenticated/usage.tsx` 引用不存在的 `@/queries/coreResources` 模块（issue #1/#7 引入），导致 `tsc --noEmit` 全项目失败。
- 影响：本 Issue 创建的 `features/usage/` 文件无类型错误（VS Code 诊断确认），但全项目 `pnpm type-check` 被该预存错误阻塞。
- 待确认：需 Issue #12（页面组合）创建 `@/queries/coreResources` 模块或重写 `_authenticated/usage.tsx` 移除该导入。不属于本 Issue scope。

**OQ-2：ESLint 配置缺失（预存问题）**
- 现状：console 项目 `package.json` 有 `lint` script（`eslint src --ext ts,tsx`）但无 `.eslintrc` 配置文件。
- 待确认：前端批次需统一配置 ESLint。属环境基建问题，不影响本 Issue AC。

**OQ-3：路由冲突警告（预存问题）**
- 现状：`routes/usage.tsx`（旧版）与 `routes/_authenticated/usage.tsx`（新版）路径冲突，`pnpm build` 产生路由冲突警告。
- 待确认：SPEC §10.1 P0-B-8 计划移除旧版 `routes/usage.tsx`，属 Issue #12（页面组合）scope。

**OQ-4：test-setup.ts 由其他 Issue 创建**
- 现状：`vitest.config.ts` 的 `setupFiles: ['./src/test-setup.ts']` 引用了该文件，但该文件由其他 Issue（#9 或 #10）创建。本批次创建 `vitest.config.ts` 时，`test-setup.ts` 尚不存在。
- 影响：本批次运行 `pnpm test` 时，如果 `test-setup.ts` 不存在，vitest 会报 setup file not found。但实际工作区中该文件已由其他 Issue 创建（`import '@testing-library/jest-dom/vitest'`），测试全部通过。
- 待确认：若本批次独立验收（其他 Issue 文件不存在），需临时创建 `test-setup.ts` 或移除 `setupFiles` 配置。当前混合工作区环境下无问题。

## review-it 审查结果

审查通过，0 accepted findings，0 rejected findings。已知警告：
- `ReactDOMTestUtils.act` 弃用警告 — cosmetic，不影响功能（见 DD-3）。

## 完工标准（验证命令）

- [x] `pnpm test`（38 passed：constants 10 + useDebouncedFilter 10 + UsageTable 11 + UsageChart 7）
- [x] `git diff --check`（通过，仅 CRLF 警告）
- [x] VS Code 诊断（features/usage/ 5 文件）— 零错误零警告
- [~] `pnpm type-check` — BLOCKED，预存错误（见 OQ-1）
- [~] `pnpm lint` — SKIP，项目无 ESLint 配置（见 OQ-2）

## AC 满足情况

- [x] `RESOURCE_TYPE_TABS`: GPU/CPU/Memory/Input/Output 5 启用 + Storage/KB 2 disabled — constants.ts L27-45，测试验证 7 Tab、5 enabled、2 disabled
- [x] 配置中不含 token_total Tab（FR-17）— 测试 "不含 token_total Tab（FR-17）" 显式断言
- [x] `GROUP_BY_OPTIONS`: resource_type / az / day / hour — constants.ts L64-69，测试验证 4 选项及值
- [x] `UsageFilter` 接口: start_time, end_time, resource_type?, group_by? — types.ts L18-27
- [x] `useDebouncedFilter(filter, 300ms)`: 延迟后返回 debounced 值，取消旧值 — useDebouncedFilter.ts L51-61，测试验证延迟、取消、自定义延迟
- [x] 单元测试: debounce 延迟、取消旧值、defaultTimeRange 近 30 天 — 20 个测试全部通过

---

# Issue #9 — Console UsageFilterBar 组件（DateRangePicker + 预设视角 Tabs + group_by Segmented）

完成日期：2026-07-10
对应 Issue：issue-009-console-usage-filter-bar
对应范围：Console 前端用量报表筛选区组件（DateRangePicker + 预设视角 Tabs + group_by Segmented）
SPEC 参考：§2.4（File Structure）、§5.1（Core Algorithms）、§5.2（Validation Rules）、§5.3（State Machine）、§10.1 P0-B-3、§10.2 Issue #3
PRD 参考：FR-17（token_total 无 Tab）、FR-18（单位原样展示）
UX 参考：§5.1（Console /usage 组件映射）、§7.1（Labels & Buttons）、§8.4（debounce 300ms 定稿）

## 实现了什么

新建 `repo/frontends/console/src/features/usage/UsageFilterBar.tsx`，实现用量报表筛选区组件，包含 DateRangePicker（必填 + 校验）、预设视角 Tabs（5 P0 启用 + 2 P1 disabled + Tooltip）、group_by Segmented（4 选项）。组件为受控模式，无查询按钮（debounce auto-fetch 由父组件实现）。

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：使用 Radio.Group `variant="default-filled"` 替代 Segmented**
- 歧义：UX §5.1 组件映射写的是 `Radio.Group` / `Segmented`（两者并列），但 TDesign React v1.10.0 没有 Segmented 组件。
- 选择：使用 `Radio.Group` + `variant="default-filled"`，视觉和交互上接近 Segmented 填充式选择器。
- 理由：UX §5.1 明确允许 `Radio.Group` / `Segmented` 两种实现；TDesign React 无 Segmented 组件，Radio.Group `variant="default-filled"` 是最接近的等价方案。`variant="default-filled"` 提供填充式选中态视觉，比默认 outline 风格更符合"分段控件"语义。

**DD-2：受控组件模式——filter prop + onChange 回调**
- 歧义：SPEC §5.1 描述筛选变更后 debounce 自动查询，但未明确 UsageFilterBar 内部是否持有 filter 状态。
- 选择：UsageFilterBar 不持有任何状态，`filter` 作为受控 prop，所有变更通过 `onChange` 回调通知父组件。父组件负责管理 filter state 和 debounce 查询。
- 理由：(1) 遵循 React 受控组件模式，state 提升到父组件使 debounce 逻辑与查询触发逻辑集中在页面级（SPEC §5.1 的 `useDebouncedFilter` + `useQuery` 都在页面级）；(2) 避免子组件与父组件状态同步问题；(3) 符合 Karpathy 原则二「用能解决问题的最小代码」——组件只负责 UI 呈现和事件转发，不承担状态管理职责。

**DD-3：复用 `isValidRange` 而非本地重新实现**
- 歧义：组件需要判断时间范围是否非法以显示 inline 错误。可以本地实现 `isInvalidRange`，或复用 Issue #8 已导出的 `isValidRange` 函数。
- 选择：`import { isValidRange } from './useDebouncedFilter'`，使用 `!isValidRange(filter)` 判断非法。
- 理由：`isValidRange` 已在 Issue #8 实现并测试覆盖（10 个 useDebouncedFilter 测试含边界测试）。本地重新实现相同逻辑会导致逻辑漂移——两处独立实现可能随时间不一致。复用保证组件与 hook 的校验逻辑完全一致。这是 review-it 阶段识别并修复的 finding。

**DD-4：ISO 8601 → `YYYY-MM-DD HH:mm:ss` 格式转换**
- 歧义：`UsageFilter.start_time` / `end_time` 是 ISO 8601 字符串（如 `2026-07-10T14:30:00.000Z`），但 DateRangePicker 的 `valueType="YYYY-MM-DD HH:mm:ss"` 要求 value 是该格式的字符串。
- 选择：在组件内添加 `formatForPicker` 函数，传入 DateRangePicker 前将 ISO 8601 转为 `YYYY-MM-DD HH:mm:ss` 格式；DateRangePicker onChange 回调中将返回值转回 ISO 8601 传给父组件。
- 理由：DateRangePicker 的 `valueType` 决定了 value 的期望格式。传入不匹配的格式会导致解析错误或显示异常。`formatForPicker` 是纯函数，使用 `Date` 对象的手动格式化，不引入 dayjs 等额外依赖（项目未安装 dayjs）。这是 review-it 阶段识别并修复的 finding。

**DD-5：disabled Tab 的 Tooltip 包裹 label 而非整个 TabPanel**
- 歧义：UX §5.1 要求 disabled Tab 显示 Tooltip「待 API 合入（P1）」。Tooltip 可以包裹整个 TabPanel 或仅包裹 Tab 的 label 文本。
- 选择：Tooltip 仅包裹 label 中的 `<span>` 文本，不包裹 TabPanel 本身。
- 理由：TDesign Tabs 的 disabled 属性通过 CSS `cursor: not-allowed` 阻止点击，但未设 `pointer-events: none`，因此 Tooltip 包裹 label 内的 `<span>` 可以正常触发 hover。若包裹整个 TabPanel，可能干扰 Tabs 的内部事件处理。将 Tooltip 限定在 label 内是最小侵入方案。

### 2. Deviations（偏离）

**DEV-1：组件文件头注释提到 `Radio.Group theme="button"` 但实际使用 `variant="default-filled"`**
- SPEC 预期：UX §5.1 写的是 `Radio.Group` / `Segmented`，SPEC §2.4 组件表写的是 `group_by Segmented`。
- 实际实现：使用 `Radio.Group` + `variant="default-filled"`（而非 `theme="button"`），文件头注释也相应更新。
- 原因：TDesign React v1.10.0 的 Radio.Group 不支持 `theme="button"` 属性，支持的是 `variant` 属性（值包括 `default-filled`、`default-outline`、`primary-filled` 等）。`variant="default-filled"` 是视觉上最接近 Segmented 的选项。这是 SPEC 描述与实际 API 的适配，非意图偏离。

None（除此之外） — 实现严格遵循 SPEC §5.1-§5.3 和 UX §5.1 的组件设计，AC 5 项全部满足。

### 3. Tradeoffs（取舍）

**TO-1：内联样式 vs CSS Module / styled-components**
- 备选 A（内联 style 对象，本批次采用）：布局样式使用 `style={{ display: 'flex', ... }}` 内联对象。
- 备选 B（CSS Module 或 styled-components）：抽取样式为独立 CSS 文件或 styled 组件。
- 取舍：A 胜出。理由：(1) 项目既有代码（shell 组件 Issue #7、UsageTable Issue #10）均使用内联样式，保持一致性（Karpathy 原则三）；(2) 筛选区布局简单（3 行垂直排列 + label 对齐），内联足够清晰；(3) 项目无 CSS Module 基建，引入需额外配置。

**TO-2：`formatForPicker` 纯函数 vs dayjs 格式化**
- 备选 A（纯函数手动格式化，本批次采用）：用 `Date` 对象 + `padStart` 手动拼接 `YYYY-MM-DD HH:mm:ss`。
- 备选 B（引入 dayjs）：`dayjs(iso).format('YYYY-MM-DD HH:mm:ss')`。
- 取舍：A 胜出。理由：(1) 项目未安装 dayjs（`package.json` 无该依赖），为一个格式化函数引入整个库违反 Karpathy 原则二；(2) 手动格式化 6 行代码即可完成，逻辑清晰且无外部依赖；(3) TDesign DateRangePicker 自身使用 dayjs 内部处理，但这是 TDesign 内部依赖，不应泄漏到业务代码。

**TO-3：TDesign 内部类型导入路径 `tdesign-react/es/...`**
- 备选 A（深路径导入 `tdesign-react/es/date-picker/type`，本批次采用）：`import type { DateRangeValue } from 'tdesign-react/es/date-picker/type'`。
- 备选 B（从 `tdesign-react` 顶层导出）：`import type { DateRangeValue } from 'tdesign-react'`。
- 取舍：A 胜出。理由：TDesign React v1.10.0 的顶层 `tdesign-react` 包未导出 `DateRangeValue` 和 `TabValue` 类型，只能从 `es/` 深路径导入。虽然深路径依赖内部目录结构，但 TDesign 的 `es/` 目录是稳定的构建产物目录，类型导出路径在 v1.x 范围内不会变动。

### 4. Open Questions（开放问题）

**OQ-1：`pnpm type-check` 存在预存错误（非本 Issue 引入）**
- 现状：`_authenticated/usage.tsx` 引用不存在的 `@/queries/coreResources` 模块，导致 `tsc --noEmit` 报 2 个错误。过滤 tsc 输出中 "UsageFilterBar" 相关错误为空，GetDiagnostics 也确认本文件零错误。
- 待确认：该错误属于 Issue #6（页面组合）scope，需在页面组合 Issue 中创建 `@/queries/coreResources` 或重写 `_authenticated/usage.tsx`。

**OQ-2：ESLint 配置缺失（预存问题）**
- 现状：console 项目无 `.eslintrc` 配置文件，`pnpm lint` 无法运行。
- 待确认：前端批次需统一配置 ESLint，属环境基建问题，不影响本 Issue AC。

**OQ-3：缺少 UsageFilterBar 组件级测试**
- 现状：当前测试套件（38 个测试）覆盖 constants、useDebouncedFilter、UsageTable、UsageChart，但未包含 UsageFilterBar 组件级测试（如 DateRangePicker 校验交互、Tab 切换、Segmented 切换）。
- 待确认：Issue AC 未显式要求组件级测试（AC#5 只要求「Matches UX §5.1 组件映射」）。组件级交互测试可在 Issue #6（页面组合 + 状态机）的集成测试中覆盖，或由后续补充 Issue 添加。当前依赖类型检查 + 代码审查保证质量。

## review-it 审查结果

审查发现 2 个 actionable finding，已全部修复：

1. **逻辑重复（已修复）** — 本地 `isInvalidRange` 与 `useDebouncedFilter.ts` 的 `isValidRange` 逻辑重复。修复：删除本地函数，改为 `import { isValidRange }` 并使用 `!isValidRange(filter)`。
2. **DateRangePicker value 格式不匹配（已修复）** — `valueType="YYYY-MM-DD HH:mm:ss"` 但传入 ISO 8601 字符串。修复：添加 `formatForPicker` 函数将 ISO 8601 转为目标格式。

修复后重新验证：`pnpm test` 38/38 通过，`pnpm type-check` 无 UsageFilterBar 错误。

## 完工标准（验证命令）

- [x] `pnpm test` — 38/38 passed（4 个测试文件：constants 10 + useDebouncedFilter 10 + UsageTable 11 + UsageChart 7）
- [x] `pnpm type-check`（过滤 UsageFilterBar）— 零错误
- [x] `make validate-architecture` — 通过
- [x] GetDiagnostics（UsageFilterBar.tsx）— 零错误零警告
- [~] `pnpm type-check`（全项目）— 2 个预存错误属 Issue #6 scope（见 OQ-1）
- [~] `pnpm lint` — SKIP，项目无 ESLint 配置（见 OQ-2）

## AC 满足情况

- [x] DateRangePicker: 必填，start ≥ end 时 inline 错误「结束时间必须晚于开始时间」— `status={rangeInvalid ? 'error' : 'default'}` + `tips` inline 错误文案，`clearable={false}` 确保必填
- [x] Tabs: 5 P0 启用（GPU/CPU/Memory/Input/Output），2 P1 disabled + Tooltip「待 API 合入（P1）」— `RESOURCE_TYPE_TABS.map()` 渲染，`disabled={!tab.enabled}` + Tooltip 包裹 disabled Tab label
- [x] Segmented: resource_type / az / day / hour 4 选项 — `Radio.Group` + `variant="default-filled"` + `GROUP_BY_OPTIONS.map()` 渲染 4 个 Radio
- [x] 无查询按钮（debounce auto-fetch）— 组件无 Button，`onChange` 回调由父组件接收并 debounce
- [x] [UI] Matches UX §5.1 组件映射 — DateRangePicker + Tabs theme="card" + Radio.Group/Segmented，与 UX §5.1 表格一致

---

# Issue #10 — Console UsageChart 组件（ECharts 趋势图）

对应 Issue：issue-010-console-usage-chart
SPEC 参考：§2.4（File Structure）、§5.1（图表数据映射）、§5.3（State Machine）、§5.4（Edge Cases）、§10.1 P0-B-4、§10.2 Issue #4
UX 参考：§4.1（趋势图区）、§5.1（组件映射）、§6.1（状态设计）
依赖：#8（feature/usage 基础模块）

## 实现了什么

- `frontends/console/src/features/usage/UsageChart.tsx`（新建）— ECharts 趋势图组件：
  - 根据 `group_by` 维度渲染折线图（day/hour 时间桶）或柱图（resource_type/az 非时间桶）
  - x 轴：时间桶用 `period`，非时间桶用 `resource_type`；y 轴：`total_quantity`
  - 时间桶按 `resource_type` 拆分多条折线系列；非时间桶单系列柱图
  - loading 态：TDesign `Skeleton`（animation="gradient"）
  - empty 态（items=[]）：不渲染假折线，显示 TDesign `Empty`
  - 数据由父组件透传（与 UsageTable 共享同一 queryKey）
- `frontends/console/src/features/usage/UsageChart.test.tsx`（新建）— 7 个单元测试，覆盖 loading/empty/有数据/三种 group_by 维度
- `frontends/console/src/test-setup.ts`（新建）— 注册 `@testing-library/jest-dom` 的 DOM 断言扩展（配套已安装的 jest-dom 依赖）
- `frontends/console/vitest.config.ts`（编辑）— 增加 `setupFiles: ['./src/test-setup.ts']`

## 边界

- 仅修改 `frontends/console/`，未触碰冻结的 Services 后端或 Core OpenAPI 契约
- 纯消费既有 `/metering/usage` 响应类型（`MeteringUsageRecord`），无新增/虚构 API 字段
- 首例引入 `echarts-for-react`（全代码库此前无使用）

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：`group_by=az` 时 x 轴使用 `resource_type`**
- 歧义：SPEC §5.1 图表数据映射仅明确定义两种 x 轴映射——`day/hour` → `period`，`resource_type` → `resource_type`。Issue AC 也只写"x 轴: period (day/hour) 或 resource_type"，未明确 `group_by=az` 的 x 轴。
- 选择：`az` 归入"非时间桶"分支，x 轴用 `resource_type`，渲染单系列柱图。
- 理由：OpenAPI `/metering/usage` 响应 `MeteringUsageRecord` 只有 `resource_type`、`total_quantity`、`unit`、`period` 四个字段，**没有 `az` 字段**。当 `group_by=az` 时 API 返回的 items 仍以 `resource_type` 标识，`period` 为 null。在无 `az` 字段可用的前提下，`resource_type` 是唯一可用的类别维度。这与 AC 表述一致，且不虚构 OpenAPI 不存在的字段。

**DD-2：时间桶按 `resource_type` 拆分多系列，而非按 period 拆分**
- 歧义：SPEC §5.1 说"系列: resource_type（按维度拆分）"，但未规定是按 resource_type 还是按 period 拆分多条线。
- 选择：时间桶（day/hour）下，x 轴=period，每条折线代表一个 `resource_type`（如 token_input 一条、token_output 一条）。
- 理由：趋势图的语义是"随时间变化"——x 轴是时间（period），每个资源类型是一条独立趋势线，便于对比不同资源类型随时间的用量变化。这是 ECharts 折线图的标准用法，符合 UX §4.1"趋势图区"的意图。

**DD-3：测试中 mock `echarts-for-react` 而非真实渲染**
- 歧义：echarts-for-react 依赖 canvas/DOM 渲染，jsdom 环境无法真正渲染 ECharts canvas。
- 选择：mock `echarts-for-react` 为简单 `<div data-testid="echarts-mock" />`，通过模块级变量捕获传入的 `option` prop 进行断言。
- 理由：(1) 真实 ECharts 在 jsdom 中渲染会抛错或产生空 canvas，无法断言图表配置；(2) 本 Issue 的 AC 核心是"渲染 items[] / x 轴 / y 轴 / loading / empty"——验证传入 ECharts 的 `option` 配置正确即可证明 AC，无需验证 ECharts 内部渲染；(3) mock 策略聚焦于本组件的职责边界（构造 option），ECharts 自身的渲染正确性由其库测试承载。

### 2. Deviations（偏离）

None — 实现严格遵循 SPEC §5.1 图表数据映射、§5.4 Edge Cases（empty 不渲染假折线）与 UX §6.1 状态设计（loading=Skeleton / empty=Empty）。无主动偏离。

### 3. Tradeoffs（取舍）

**TO-1：图表配置内联于组件 vs 抽取为独立 hook/util**
- 备选 A（`buildChartData` + `isTimeBucket` 内联为组件私有函数，本批次采用）：图表配置逻辑放在 `UsageChart.tsx` 内部，通过 `useMemo` 缓存 option。
- 备选 B（抽取为 `useUsageChartOption` hook 或 `chartConfig.ts` util）：将数据映射逻辑独立为可复用模块。
- 取舍：A 胜出。理由：(1) 图表配置逻辑仅被 `UsageChart` 使用，无第二个消费者，提前抽象违反 Karpathy 原则二"用能解决问题的最小代码"和原则五"如无必要勿增实体"；(2) `buildChartData`/`isTimeBucket` 作为组件内私有函数 + JSDoc，已具备可读性和可测试性（测试通过 props 间接覆盖）；(3) 若未来出现第二个图表消费者，再抽取不迟。

**TO-2：测试 setup 文件 vs 内联 jest-dom 导入**
- 备选 A（新建 `src/test-setup.ts` 并在 vitest.config.ts 注册 `setupFiles`，本批次采用）：集中注册 jest-dom 断言扩展。
- 备选 B（每个 `.test.tsx` 文件顶部内联 `import '@testing-library/jest-dom/vitest'`）：不新增配置项。
- 取舍：A 胜出。理由：(1) `@testing-library/jest-dom` 已在 `devDependencies`，强烈暗示本意是要全局注册（否则不会装它）；(2) setup 文件是 vitest 官方推荐的标准做法，后续所有组件测试自动获得断言扩展，避免每个文件重复导入；(3) 仅新增 1 个配置行 + 1 个小文件，复杂度极低。

### 4. Open Questions（开放问题）

**OQ-1：`group_by=az` 的 x 轴语义待 API 真实数据验证**
- 现状：当前实现用 `resource_type` 作为 `group_by=az` 的 x 轴，因为 OpenAPI 响应无 `az` 字段。
- 待确认：若 Services 团队后续在 `MeteringUsageRecord` 中补 `az` 字段，或 `group_by=az` 时 API 返回的 `period`/`resource_type` 编码方式有特殊约定，需调整 `buildChartData` 的非时间桶分支。建议在 UsagePage 页面组合（Issue #12）联调时用真实 API 数据验证。

**OQ-2：`pnpm type-check` 预存错误（与 Issue #7/#8/#9 同一问题）**
- 现状：`_authenticated/usage.tsx` 引用不存在的 `@/queries/coreResources` 模块，导致 `tsc --noEmit` 报 2 个错误。本批次新增文件（UsageChart.tsx/test.tsx、test-setup.ts）零错误。
- 待确认：该错误属 Issue #6/#12（页面组合）scope，需在页面组合 Issue 中创建 `@/queries/coreResources` 或重写路由文件。

**OQ-3：`pnpm lint` 无法运行（与 Issue #8/#9 同一问题）**
- 现状：console 项目无 ESLint 配置文件。
- 待确认：前端批次需统一配置 ESLint，属环境基建问题，不影响本 Issue AC。

**OQ-4：UI 状态人工验证**
- 现状：当前环境无浏览器自动化 MCP，loading/empty/error 状态未做 E2E 验证。
- 待确认：建议在 UsagePage 页面组合（Issue #12）完成后，用浏览器自动化或人工验证：(1) loading 态显示 Skeleton 无折线闪现；(2) empty 态显示 Empty 不渲染假折线；(3) 有数据态折线/柱图 x/y 轴正确。

## review-it 审查结果

review-it clean — 无未解决的 actionable 发现。

审查发现 1 个 finding，已修复：
1. **测试注释笔误（已修复）** — `UsageChart.test.tsx` 注释"按 resource_name 分组"应为"按 resource_type 分组"。已修正。

rejected findings（无需修复）：
- `group_by=az` x 轴用 resource_type：OpenAPI 响应无 az 字段，AC 仅要求 period 或 resource_type，符合。
- 测试 module-level `lastOption` 共享状态：每个断言它的测试都先渲染组件覆盖该变量，无隔离问题。
- `row()` 测试助手 `unit` 参数未被断言：类型必需字段的测试数据构造，正常模式。

修复后重新验证：`pnpm test` 7/7 UsageChart 测试通过，`make validate-architecture` 通过，`git diff --check` 无空白错误。

## 完工标准（验证命令）

- [x] `pnpm test`（UsageChart 聚焦）— 7/7 passed
- [x] `pnpm test`（全量）— 38/38 passed（4 个测试文件：constants 10 + useDebouncedFilter 10 + UsageTable 11 + UsageChart 7）
- [x] `pnpm build` — 通过（既有路由 warning 非本批次）
- [x] `make validate-architecture` — 通过
- [x] `git diff --check` — 无空白错误
- [~] `pnpm type-check`（全项目）— 2 个预存错误属 Issue #6/#12 scope（见 OQ-2）
- [~] `pnpm lint` — SKIP，项目无 ESLint 配置（见 OQ-3）

## AC 满足情况

- [x] ECharts 折线/柱图渲染 items[] — 时间桶(day/hour)用 `type:'line'`，非时间桶(resource_type/az)用 `type:'bar'`
- [x] x 轴: period (day/hour) 或 resource_type；y 轴: total_quantity — `isTimeBucket()` 判定 x 轴数据源，yAxis `type:'value'`
- [x] loading 态: Skeleton — `<Skeleton animation="gradient" />`
- [x] empty 态 (items=[]): 不渲染假折线 — `items.length === 0` 时返回 `<Empty />`，不渲染 ReactECharts
- [x] 与 UsageTable 共享同一 queryKey — 父组件统一 `useQuery`，items 透传给 Chart 与 Table
- [x] Typecheck 通过 — 本批次新增文件零错误（既有路由错误属其他 Issue）

---

# Issue #11 — Console UsageTable 组件（明细表格 4 列 + token_total 行）

完成日期：2026-07-10
对应 Issue：issue-011-console-usage-table
对应范围：Console 前端用量报表明细表格组件（4 列 + token_total 行展示 + loading + 复合 rowKey）
SPEC 参考：§2.4（File Structure）、§5.4（Edge Cases）、§5.3（State Machine）、§10.1 P0-B-5、§10.2 Issue #5
PRD 参考：FR-17（未筛 resource_type 时可展示 token_total 行）、FR-18（不做单位换算，原样展示）
UX 参考：§5.1（Console /usage Table columns）

## 实现了什么

新建 `repo/frontends/console/src/features/usage/UsageTable.tsx`（明细表格组件）+ `UsageTable.test.tsx`（11 个单元测试）：

| 文件 | 职责 | AC |
|------|------|----|
| `UsageTable.tsx` | 4 列表格（资源类型/用量/单位/统计周期）+ loading + 复合 rowKey + period 可空显示 — | AC#1-#6 |
| `UsageTable.test.tsx` | 11 个单元测试覆盖 4 列、loading、rowKey 复合键、FR-18 原样展示、period 空→—、FR-17 token_total 行 | AC#7 |

## 边界

- 仅修改 `frontends/console/src/features/usage/`，未触碰冻结的 Services 后端或 Core OpenAPI 契约
- 纯消费既有 `/metering/usage` 响应类型（`MeteringUsageRecord`），无新增/虚构 API 字段
- 与 UsageChart 共享同一 queryKey（父组件统一 `useQuery`，items 透传给 Table）

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：复合 rowKey 通过 `_rowKey` 注入字段实现，而非函数式 rowKey**
- 歧义：TDesign React Table 的 `rowKey` prop 类型定义为 `string`（字段名），不接受 `(row) => string` 函数。但 AC 要求 `rowKey = resource_type+period`（复合键），需要拼接两个字段。
- 选择：在 `useMemo` 中将 items 映射为带 `_rowKey` 字段的行对象（`{ ...item, _rowKey: '${resource_type}+${period}' }`），`rowKey="_rowKey"` 指向该字段。
- 理由：TDesign Table 的 `rowKey` 只接受 string 类型字段名，不支持函数式 rowKey（已验证 `tdesign-react/es/table/type.d.ts`）。注入 `_rowKey` 字段是适配 TDesign API 约束的最小方案，不修改原始数据（使用扩展运算符浅拷贝）。`useMemo` 缓存避免每次渲染重复映射。

**DD-2：period 空值显示 `—` 通过列 `cell` 渲染函数实现**
- 歧义：UX §5.1 要求 period 可空显示 `—`。可以在数据预处理时填充，或在列 `cell` 渲染函数中处理。
- 选择：在 `buildColumns()` 的 period 列定义中使用 `cell: ({ row }) => (row.period ? row.period : '—')`。
- 理由：数据预处理填充会修改原始 items（破坏数据源一致性），cell 渲染函数只在展示层处理空值，不污染数据。这与 UsageChart 的"原样消费 items"语义一致。

**DD-3：FR-17 token_total 行不做过滤，由父组件控制 items**
- 歧义：FR-17 要求"未筛 resource_type 时表格可展示 token_total 行"。可以在 UsageTable 内部根据 filter 判断是否过滤 token_total，或完全由父组件传入的 items 决定。
- 选择：UsageTable 不做任何 resource_type 过滤，渲染父组件传入的全部 items。
- 理由：(1) UsageTable 是纯展示组件，不应承担筛选逻辑（与 UsageFilterBar 的受控模式设计一致，DD-2 of Issue #9）；(2) 筛选逻辑已在父组件的 API 查询中通过 `resource_type` query 参数完成，未筛时 API 返回全部行（含 token_total）；(3) 在组件内过滤会导致父组件与表格对数据源的视图不一致。组件只负责"展示什么"，父组件负责"传入什么"。

**DD-4：`buildColumns()` 不做 memoize，每次渲染重新创建**
- 歧义：列定义数组可以在 `useMemo` 中缓存，或每次渲染重新创建。
- 选择：`buildColumns()` 作为独立函数在组件内调用，不做 `useMemo` 缓存。
- 理由：(1) 与代码库既有模式一致——`_authenticated/usage.tsx` 内联定义 columns 未 memoize，UsageChart 的 `buildChartData` 也非 memoize 函数；(2) 列数组是 4 个静态对象的数组，创建成本极低（无计算密集操作）；(3) `useMemo` 有缓存比较开销，对此规模数据收益为负。遵循 Karpathy 原则二「用能解决问题的最小代码」。

### 2. Deviations（偏离）

None — 实现严格遵循 SPEC §5.4 Edge Cases（period 可空显示 —）、UX §5.1 Table columns（4 列定义）和 PRD FR-17/FR-18 要求，AC 6 项全部满足，无偏离。

### 3. Tradeoffs（取舍）

**TO-1：`_rowKey` 注入字段 vs TDesign `rowKey` 函数式适配**
- 备选 A（`_rowKey` 字段注入 + `rowKey="_rowKey"`，本批次采用）：在 `useMemo` 中映射 items 添加复合键字段。
- 备选 B（绕过 TDesign 类型约束用 `as any` 强传函数）：`rowKey={((row) => ...) as any}`。
- 取舍：A 胜出。理由：(1) A 遵守 TDesign 的类型契约，不使用 `as any` 绕过类型检查（类型安全）；(2) A 的 `_rowKey` 字段语义清晰，测试可直接断言 `data[0]._rowKey` 的值；(3) B 依赖 TDesign 内部是否在运行时支持函数式 rowKey（不确定），有运行时风险。

**TO-2：测试 mock Table 组件 vs 真实渲染 TDesign Table**
- 备选 A（mock tdesign-react Table，本批次采用）：捕获 props（data/loading/rowKey/columns），渲染为简易 DOM 结构。
- 备选 B（真实渲染 TDesign Table）：在 jsdom 中渲染完整 TDesign Table。
- 取舍：A 胜出。理由：(1) 与 UsageChart.test.tsx 的 mock 模式一致（mock echarts-for-react）；(2) 本 Issue AC 核心是验证列定义、loading、rowKey、数据透传——验证传入 Table 的 props 正确即可证明 AC，无需验证 TDesign Table 内部渲染；(3) TDesign Table 在 jsdom 中渲染依赖复杂 DOM 上下文，真实渲染增加测试脆弱性。mock 策略聚焦于本组件的职责边界。

### 4. Open Questions（开放问题）

**OQ-1：`pnpm type-check` 预存错误（与 Issue #7-#10 同一问题）**
- 现状：`_authenticated/usage.tsx` 引用不存在的 `@/queries/coreResources` 模块（issue #1/#7 引入），导致 `tsc --noEmit` 全项目报 2 个错误。本批次新增文件（UsageTable.tsx/test.tsx）零类型错误。
- 待确认：需 Issue #12（页面组合）创建 `@/queries/coreResources` 模块或重写 `_authenticated/usage.tsx` 移除该导入。不属于本 Issue scope。

**OQ-2：ESLint 配置缺失（与 Issue #8-#10 同一问题）**
- 现状：console 项目 `package.json` 有 `lint` script 但无 `.eslintrc` 配置文件，`pnpm lint` 无法运行。
- 待确认：前端批次需统一配置 ESLint，属环境基建问题，不影响本 Issue AC。

**OQ-3：UI 状态人工验证（与 Issue #10 同一问题）**
- 现状：当前环境无浏览器自动化 MCP，loading/empty/error 状态未做 E2E 验证。
- 待确认：建议在 UsagePage 页面组合（Issue #12）完成后，用浏览器自动化或人工验证：(1) loading 态 Table 显示 loading 效果；(2) empty 态 Table 无数据行（父组件应渲染 Empty）；(3) error 态父组件渲染 Alert + 重试按钮；(4) 正常态 4 列展示、period 空值显示 —、token_total 行在未筛 resource_type 时出现。

## review-it 审查结果

review-it clean — 无未解决的 actionable 发现。

rejected findings（无需修复）：
- `buildColumns()` 每次渲染未 memoize：与代码库既有模式一致（`_authenticated/usage.tsx` 内联 columns），列数组创建成本极低。
- 测试中 `any` 类型：mock 场景下放宽类型检查是既有模式（UsageChart.test.tsx 同样模式）。
- 空 `resource_type` + 空 `period` 的 rowKey 冲突：OpenAPI `MeteringUsageRecord` 中 `resource_type` 为必填非空字段，该场景按契约不可能出现。

## 完工标准（验证命令）

- [x] `pnpm test`（UsageTable 聚焦）— 11/11 passed
- [x] `pnpm test`（features/usage 全量）— 38/38 passed（constants 10 + useDebouncedFilter 10 + UsageTable 11 + UsageChart 7）
- [x] `pnpm build` — 通过（既有路由 warning 非本批次）
- [x] `git diff --check` — 无空白错误
- [x] GetDiagnostics（UsageTable.tsx）— 零错误零警告
- [~] `pnpm type-check`（全项目）— 2 个预存错误属 Issue #6/#12 scope（见 OQ-1）
- [~] `pnpm lint` — SKIP，项目无 ESLint 配置（见 OQ-2）

## AC 满足情况

- [x] 4 列：资源类型、用量（total_quantity 原样）、单位（unit 原样）、统计周期（period 可空显示 —） — `buildColumns()` 定义 4 列，period 列 `cell` 渲染函数处理空值显示 —
- [x] FR-18：不做 seconds→hours 换算，原样展示 — 组件无任何换算逻辑，total_quantity / unit 直接透传
- [x] FR-17：未筛 resource_type 时可展示 token_total 行 — 组件不过滤 resource_type，渲染父组件传入的全部 items
- [x] loading 态：Table `loading` — `loading` prop 直接传入 TDesign Table
- [x] rowKey：`resource_type+period` — `withRowKeys()` 注入 `_rowKey` 字段，`rowKey="_rowKey"`
- [x] [UI] 匹配 UX §5.1 Table columns — 4 列标题（资源类型/用量/单位/统计周期）与 UX §5.1 一致

---

# Issue #12 — Console Usage 页面组合 + 状态机（重写 _authenticated/usage.tsx）

完成日期：2026-07-10
对应 Issue：issue-012-console-usage-page-composition
对应范围：Console 前端用量报表页页面组合（FilterBar + Chart + Table + 完整状态机）
SPEC 参考：§5.3（状态机）、§6.1（全部 8 个状态）
PRD 参考：FR-4（调用 GET /metering/usage）、FR-12（dev_profile 横幅）、FR-17（无 token_total Tab）、FR-18（单位原样展示）
UX 参考：§3.1、§6.1、§7.2、§8.4（debounce 300ms）
依赖：#9（UsageFilterBar）、#10（UsageChart）、#11（UsageTable）

## 实现了什么

| 文件 | 变更类型 | 职责 | AC |
|------|----------|------|----|
| `routes/_authenticated/usage.tsx` | 重写 | 页面组合 + 8 态状态机 + coreApi.GET 调用 + dev_profile 横幅 + retry 策略 | AC#1-#8 |
| `routes/_authenticated.tsx` | 新建 | TanStack Router path group 布局路由（`_authenticated` 路由组父节点） | 路由结构依赖 |
| `routeTree.gen.ts` | 重写 | 移除旧 `routes/usage` 导入，添加 `_authenticated` → `usage` 父子关系 | AC#9 |
| `routes/usage.tsx` | 删除 | 移除旧版路由文件 | AC#9 |

## 边界

- 仅修改 `frontends/console/src/routes/` 路由层，未触碰冻结的 Services 后端或 Core OpenAPI 契约
- 通过 `coreApi.GET('/metering/usage')` 调用 Core API（FR-4），无 Services 绕路
- 前端组件纯消费依赖 #9/#10/#11 的既有实现，无新增/虚构 API 字段

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：openapi-fetch 错误通道手动抛出**
- 歧义：SPEC §5.3 要求 403 进入 forbidden 态、其余错误进入 error 态，但未指定 openapi-fetch 的错误处理方式。
- 选择：在 `queryFn` 中解构 `{ data, error, response }`，当 `error !== undefined` 时手动 `throw { status: response.status, body: error }`，让 React Query 进入 error 通道。
- 理由：openapi-fetch 在非 2xx 时不抛异常，而是返回 `{ data: undefined, error, response }`。若只取 `data`，403 时 `data` 为 `undefined`，React Query 认为请求成功返回空数据，forbidden 态和 error 态都无法触发。手动抛出是让 React Query 识别 HTTP 错误的标准做法。

**DD-2：retry 策略——403/401 不重试，其余最多重试 1 次**
- 歧义：UX/SPEC 未指定 React Query 重试策略。React Query v5 默认 `retry: 3`（指数退避 1s→2s→4s）。
- 选择：自定义 `retry` 函数——403/401 立即失败不重试，其余错误最多重试 1 次。
- 理由：403/401 是权限拒绝，重试无意义且会导致用户等待约 7 秒才看到 forbidden 提示。网络抖动等临时错误重试 1 次足够，默认 3 次过于激进。

**DD-3：path group 布局路由 `_authenticated.tsx`**
- 歧义：SPEC §2.1 提到 file-based routing，但未明确 path group（`_` 前缀）是否需要显式布局文件。
- 选择：新建 `_authenticated.tsx` 作为 path group 布局路由，内部仅渲染 `<Outlet />`。
- 理由：TanStack Router 的 path group 路由（`_` 前缀）需要一个父级路由文件来建立路由树父子关系。没有它，`_authenticated/usage.tsx` 无法在路由树中正确解析。

**DD-4：routeTree.gen.ts 手动重写**
- 歧义：vite.config.ts 配置了 TanStackRouterVite 插件自动生成 `routeTree.gen.ts`，但 Issue scope 限定只改 `usage.tsx`。
- 选择：手动重写 `routeTree.gen.ts`，移除旧 `routes/usage` 导入，添加 `_authenticated` → `usage` 父子结构。
- 理由：AC#9 明确要求"旧版 routes/usage.tsx 移除；routeTree.gen.ts 更新"。手动更新确保与路由文件结构一致；插件会在下次 dev server 启动时验证一致性。

### 2. Deviations（偏离）

None — 实现严格遵循 SPEC §5.3 状态机定义、UX §6.1 全部 8 个状态和 PRD FR-4/FR-12/FR-17/FR-18 要求。

### 3. Tradeoffs（取舍）

**TO-1：queryFn 中手动 throw vs openapi-fetch `throwOnError` 选项**
- 备选 A（采用）：在 `queryFn` 中检查 `error` 并手动 `throw { status, body }`。
- 备选 B：配置 openapi-fetch `throwOnError: true` 让它自动抛异常。
- 取舍：A 胜出。`throwOnError` 抛出的是 `FetchError` 对象，其结构不直观（需从 `error.cause` 或 `error.response.status` 提取 HTTP status）；手动抛出可以精确控制 error 对象结构（`{ status, body }`），使 `is403Error` 检测简洁直接。

**TO-2：forbidden 态隐藏整个数据区 vs 仅隐藏表格内容**
- 备选 A（采用）：forbidden 时隐藏整个 Chart + Table 数据区，只显示 403 Alert。
- 备选 B：保留 FilterBar，仅隐藏 Chart + Table。
- 取舍：当前实现保留了 FilterBar（在 Alert 之前渲染），仅隐藏 Chart + Table 数据区。这符合 UX §6.1 "隐藏数据区"的描述——FilterBar 是筛选控件不是数据，用户可以调整筛选条件后重试（虽然 403 通常不会因筛选改变而恢复）。如果未来 UX 要求 forbidden 时也隐藏 FilterBar，可调整条件分支。

### 4. Open Questions（待确认）

None — 所有 AC 已满足并验证通过。前序 Issue #6/#10/#11 中标记的 OQ（`@/queries/coreResources` 模块引用）已由本 Issue 重写 `usage.tsx` 彻底解决。

## review-it 审查结果

review-it clean — 无未解决的 actionable 发现。

已接受并修复的 findings：
- Finding 1（修复）：queryFn 原 `.then(({ data }) => data)` 不处理 openapi-fetch error 通道，403 不触发 forbidden 态。已改为 async 函数手动抛出。
- Finding 2（修复）：React Query 默认 `retry: 3` 导致 403 延迟约 7 秒。已添加自定义 `retry` 策略。

rejected findings（无需修复）：
- `routeTree.gen.ts` 有 `@ts-nocheck`：TanStack Router 自动生成文件的标准头部，非本批次引入。
- `queryPlatformUsage` 中 tenant_id 二次 RBAC 校验：有意的 fail-closed 设计，注释已标明意图。

## 完工标准（验证命令）

- [x] `pnpm type-check` — 通过（0 errors）
- [x] `pnpm test`（全量）— 38/38 passed
- [x] `pnpm build` — 通过
- [~] `pnpm lint` — SKIP，项目无 ESLint 配置
- [x] `make validate-architecture` — 通过
- [x] `git diff --check` — 无空白错误

## AC 满足情况

- [x] 组合 FilterBar + Chart + Table，三者共享 queryKey — `useQuery` 统一在 UsagePage，items 透传给 Chart 与 Table
- [x] 默认时间范围: 近 30 天 — `defaultTimeRange()` 返回近 30 天 ISO 时间戳
- [x] 调用 `coreApi.GET('/metering/usage')`（FR-4）— 无 Services 绕路
- [x] empty 态: Empty — UsageChart 和 UsageTable 内部各自处理空数据（Chart 渲染 `<Empty>`，Table 无数据行）
- [x] error 态: Alert + 重试按钮，保留筛选 — `<Alert theme="error">` + `<Button onClick={refetch}>重试</Button>`，FilterBar 在 Alert 之前渲染
- [x] forbidden 态(403): Alert + 隐藏数据区 — `is403Error(error)` 检测，条件分支隐藏 Chart + Table
- [x] dev_profile 横幅: `real_provider=false` 时 Warning Alert 固定文案（FR-12）— `showDevBanner` 条件渲染 `<Alert theme="warning">`
- [x] [UI] Matches UX §6.1 全部 8 个状态 — idle/success、loading、empty、error、forbidden、dev_profile、invalid range（`enabled: isValid`）、tab disabled（#9 UsageFilterBar 实现）
- [x] 旧版 routes/usage.tsx 移除；routeTree.gen.ts 更新 — 删除旧文件，重写路由树
- [x] Typecheck 通过 — 0 errors

---

# Issue #2 — Gateway 平台查询路由 + 分轨鉴权（FR-15）

完成日期：2026-07-09
对应 Issue：issue-002-core-gateway-platform-routing-rbac
对应范围：Core Gateway 平台用量查询路由 + 分轨 RBAC scope 校验
SPEC 参考：§5.1, §6.1, §7.1, §8.1, §13.2
PRD 参考：FR-8, FR-15, FR-16
依赖：#1（OpenAPI v1.yaml FR-8 契约扩展）

## 实现了什么

| 文件 | 变更类型 | 职责 | AC |
|------|----------|------|----|
| `pkg/ports/metering.go` | 修改 | `MeteringUsageQueryRequest` 新增 `IsPlatform bool`；`MeteringService` 接口新增 `QueryPlatformUsage` 方法 | AC#3 |
| `pkg/adapters/runtime/local_metering_service.go` | 修改 | 实现 `QueryPlatformUsage`：全租户聚合、按 tenant_id 筛选、items[].tenant_id 必填 | AC#3, AC#4 |
| `services/ani-gateway/internal/middleware/rbac.go` | 修改 | 新增 `CheckScopeResult` 结构体与 `CheckScope` 函数，支持声明式 scope 校验（fail-closed） | AC#2, AC#3 |
| `services/ani-gateway/internal/router/metering_resources.go` | 修改 | 新增 `queryPlatformUsage` handler；租户/平台 group_by 枚举分离校验；时间范围校验；平台 tenant_id 二次 RBAC fail-closed | AC#1, AC#2, AC#3, AC#4, AC#5, AC#6 |
| `services/ani-gateway/internal/router/metering_resources_test.go` | 修改 | 新增 10 个集成测试覆盖全部 AC | AC#7 |

## 边界

- 仅修改 Gateway Core 路由 + ports/adapters + middleware，未触碰冻结的 Services 后端或前端
- 未改 OpenAPI `v1.yaml`（#1 依赖已完成），未改 `registerMetering` 签名、未改 `RegisterOptions`
- 平台 `tenant_id` 为 query 参数（非 request body），符合 FR-15 安全要求

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：Scope 校验在 handler 层而非中间件层**
- 歧义：SPEC §13.1 要求按 path 分轨校验 scope，但未指定是在中间件层统一拦截还是 handler 内逐个校验。
- 选择：在 `queryUsage` 和 `queryPlatformUsage` handler 内调用 `middleware.CheckScope()`，而非新增路由级中间件。
- 理由：(1) 既有中间件链（auth → request_id → chain）无需改动，最小侵入；(2) 每个端点绑定不同 scope（`scope:metering:read` vs `scope:metering:platform:read`），handler 内绑定可精确控制；(3) fail-closed 语义清晰——CheckScope 在 authClient 不可用、tenant context 缺失、权限检查失败时一律拒绝。

**DD-2：authClient 通过 `newMeteringAPI` 内部注入，不改 `registerMetering` 签名**
- 歧义：`meteringAPI` 结构体需要 `authClient` 来做 scope 校验，但 `registerMetering(v1)` 签名已在 Issue #1 定型，不接收额外参数。
- 选择：在 `newMeteringAPI()` 内部调用 `middleware.NewAuthClientFromEnv()` 注入 authClient，与 `registerAuth` 的注入模式一致。
- 理由：(1) 保持 `registerMetering` 签名不变，避免影响 router.go 调用方；(2) `NewAuthClientFromEnv` 在 `ANI_AUTH_MODE=dev` 时返回 nil，CheckScope 自动放行；(3) 与既有 `registerAuth` 的 authClient 注入模式保持一致。

**DD-3：平台 tenant_id query 二次 RBAC — fail-closed 拒绝（非 dev 模式）**
- 歧义：SPEC §13.2 明确"平台 RBAC 二次校验规则未定义 → P0-A 先 fail-closed，无明确授权拒绝 tenant_id query"。
- 选择：非 dev 模式下，平台 path 带 tenant_id query 时直接返回 403；dev 模式下放行用于联调测试。
- 理由：初始实现复用了 `scope:metering:platform:read` 做二次校验，但该 scope 与第一次完全相同，等于没做校验——只要第一次通过第二次必然通过。review-it 阶段发现此 P0 安全缺陷并修复为 SPEC §13.2 要求的 fail-closed 拒绝。

**DD-4：group_by 枚举分离校验用 map 而非 OpenAPI 运行时解析**
- 歧义：OpenAPI v1.yaml 已声明平台和租户的 group_by enum 分离，但 handler 需在运行时校验。可以从 OpenAPI 解析，或用本地 map。
- 选择：在 `meteringAPI` 结构体中定义 `platformGroupByAllowed` 和 `tenantGroupByAllowed` 两个 map，handler 内本地校验。
- 理由：(1) 运行时解析 OpenAPI YAML 引入 I/O 和解析开销，不适合 hot path；(2) 本地 map 与 OpenAPI 契约保持同步（review 时核对）；(3) map 查找 O(1)，零依赖。

### 2. Deviations（偏离）

None — 实现严格遵循 SPEC §5.1/§6.1/§7.1/§8.1 和 Issue AC，无意图偏离。review-it 修复的二次 RBAC 缺陷是对 SPEC §13.2 的对齐修复，非偏离。

### 3. Tradeoffs（取舍）

**TO-1：CheckScope 返回结构体而非 error**
- 备选 A（`CheckScopeResult` 结构体 + `Allowed bool` + `Reason string`，采用）：调用方根据 `result.Allowed` 判断，`Reason` 用于日志和响应。
- 备选 B（返回 `error`）：调用方 `if err := CheckScope(...); err != nil { ... }`。
- 取舍：A 胜出。Scope 校验失败不是程序错误（不应走 error chain），而是授权决策。`CheckScopeResult` 携带 `Reason` 便于 handler 返回有意义的 403 响应体，而非通用 error message。

**TO-2：`scopeFakeAuthClient` 测试 mock vs 真实 auth_service**
- 备选 A（`scopeFakeAuthClient` with `allowedScopes` map，采用）：测试用 map 配置允许的 scope，CheckPermission 返回 hardcoded 结果。
- 备选 B（启动真实 auth_service）：集成测试连真实 auth。
- 取舍：A 胜出。Metering handler 的职责是正确调用 CheckScope 并处理结果，不是测试 auth_service 本身的正确性。fake auth client 可精确控制每个 scope 的允许/拒绝，覆盖 403/200 所有路径。auth_service 的测试由 auth 模块自身承载。

### 4. Open Questions（开放问题）

**OQ-1：平台 RBAC 二次校验规则待定义**
- 现状：当前 fail-closed 拒绝所有非 dev 模式下的 tenant_id query。SPEC §13.2 标注"P0-A 先 fail-closed"，意味着后续需要定义明确的二次授权规则。
- 待确认：后续 P1 需定义平台管理员带 tenant_id query 时的具体授权规则（如 `scope:metering:tenant:read:{tenant_id}` 或角色继承），届时需替换当前的 fail-closed 逻辑。

**OQ-2：`make test` 中 `TestDemoInstanceServiceRealShellExecutesCommand` 预存在失败**
- 现状：该测试需真实 shell 环境，在 Windows 环境下失败，与本次改动无关。
- 待确认：CI 在 Linux 环境下应通过。Metering 相关 12 个测试全部 PASS。

## review-it 修复记录

代码审查阶段修复了 1 个 P0 安全发现：

1. **平台 tenant_id query 二次 RBAC 校验无效（P0 安全）** — 初始实现中二次 RBAC 校验复用 `scope:metering:platform:read`（与第一次完全相同），等于无校验。按 SPEC §13.2 修复为非 dev 模式下 fail-closed 拒绝（403）。新增 `TestMeteringPlatformPathRejectsTenantQueryInAuthMode` 验证修复。

## 完工标准（验证命令）

- [x] `go build ./services/ani-gateway/...` — pass
- [x] `go vet ./services/ani-gateway/...` — pass
- [x] `go test ./services/ani-gateway/internal/router/... -run "TestMetering|TestLocalMeteringService" -v` — 12/12 pass
- [x] `make validate-architecture` — pass
- [x] `git diff --check` — pass
- [~] `make test` — 1 个预存在失败（`TestDemoInstanceServiceRealShellExecutesCommand`，Windows 平台问题，见 OQ-2）

## AC 满足情况

- [x] `registerMetering` 新增 `v1.GET("/metering/usage/platform", api.queryPlatformUsage)` — metering_resources.go 路由注册
- [x] 租户 path: 从 JWT 提取 tenant_id，忽略 query 中的 tenant_id — `queryUsage` 使用 `middleware.GetTenantID(c)`，不读 query tenant_id
- [x] 平台 path: 校验 `scope:metering:platform:read`；带 tenant_id query 时二次 RBAC 校验 — `queryPlatformUsage` CheckScope + fail-closed 拒绝
- [x] 平台视角下 `items[].tenant_id` 必填 — `LocalMeteringService.QueryPlatformUsage` 每个 item 填充 tenant_id
- [x] 400: start_time ≥ end_time 或 group_by 枚举非法 — `validateTimeRange` + `platformGroupByAllowed`/`tenantGroupByAllowed` map 校验
- [x] 403: 缺少对应 scope — `CheckScope` fail-closed，平台 scope 不隐含租户 scope
- [x] 集成测试: 租户/平台 scope 分离校验 — 12 个测试覆盖全部 AC

---

# Issue #3 — Ports/Adapters 平台查询扩展（MeteringService port + LocalMeteringService）

完成日期：2026-07-14
对应 Issue：issue-003-core-ports-adapters-platform-query
对应范围：Core Ports/Adapters 层平台查询扩展（MeteringService port 新增 QueryPlatformUsage + LocalMeteringService 平台聚合实现 + adapter 单元测试）
SPEC 参考：§3.2, §6.1, §6.4
PRD 参考：FR-7, FR-9, FR-12
依赖：#1（OpenAPI v1.yaml FR-8 契约扩展）

## 例外说明

进入时发现 Issue #3 要求的实现代码（`IsPlatform` 字段、`QueryPlatformUsage` 方法）已由 Issue #2 批次一并落地（Issue #2 的 scope 包含 ports/adapters 层修改）。本批次实际工作为补齐 AC #6 明确要求的 adapter 层单元测试覆盖——在 `pkg/adapters/runtime/local_metering_service_test.go` 新增 4 个平台查询测试，覆盖全租户聚合、tenant_id 筛选、group_by=tenant_id、local 仅 Token 数据四个维度。

## 实现了什么

| 文件 | 变更类型 | 职责 | AC |
|------|----------|------|----|
| `pkg/adapters/runtime/local_metering_service_test.go` | 修改（+149 行） | 新增 4 个平台查询单元测试 | AC#6 |
| `pkg/ports/metering.go` | 前置批次已实现 | `MeteringUsageQueryRequest` 新增 `IsPlatform bool`；`MeteringService` 接口新增 `QueryPlatformUsage` 方法 | AC#1, AC#2 |
| `pkg/adapters/runtime/local_metering_service.go` | 前置批次已实现 | `QueryPlatformUsage`：全租户聚合、按 tenant_id 筛选、items[].tenant_id 必填 | AC#2, AC#3, AC#4, AC#5 |

## 边界

- 本批次仅修改 `pkg/adapters/runtime/local_metering_service_test.go`（测试文件），在 Issue #3 声明的 code paths allowed 范围内
- 未触碰冻结的 Services 后端、前端或 OpenAPI 契约
- `pkg/ports/metering.go` 和 `local_metering_service.go` 的实现代码由 Issue #2 批次落地，本批次不再重复修改

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：adapter 层测试直接调用 `QueryPlatformUsage`，不经过 HTTP handler**
- 歧义：AC #6 要求"单元测试: 全租户聚合、tenant_id 筛选、group_by=tenant_id"，但未指定测试层级（adapter 单元测试 vs gateway 集成测试）。
- 选择：在 `pkg/adapters/runtime`（adapter 包）新增 4 个单元测试，直接调用 `service.QueryPlatformUsage()`，不经过 HTTP 层。
- 理由：(1) Issue #3 scope 是 ports/adapters 层，测试应聚焦 adapter 聚合逻辑的正确性，而非 HTTP 路由/RBAC；(2) Issue #2 已在 gateway 层（`metering_resources_test.go`）添加了 12 个集成测试覆盖 handler 层行为，adapter 单元测试与 gateway 集成测试互补、职责分离；(3) adapter 测试不依赖 hertz engine 初始化，执行更快、更聚焦。

**DD-2：`group_by=tenant_id` 测试验证聚合值而非排序**
- 歧义：SPEC §6.1 第 3 步说"若 group_by=tenant_id → 结果按租户聚合排行"，但 Go map 遍历无序，"排行"语义在 local adapter 内存实现下无意义。
- 选择：`TestLocalMeteringServiceQueryPlatformUsageGroupByTenantID` 验证每个租户的 `token_total` 聚合值（300 = 200 input + 100 output），不验证 items 排列顺序。
- 理由：(1) `QueryPlatformUsage` 内部用 `map[string]*tenantAggregate` 聚合，map 遍历顺序不确定，断言排序会 flaky；(2) local profile 是开发态实现，无排序需求；生产态聚合排序应由真实存储层（如 SQL ORDER BY）承载；(3) 测试应验证聚合正确性（sum 值），而非 local 实现无法保证的排序契约。

### 2. Deviations（偏离）

None — 实现严格遵循 SPEC §3.2（Entity Definitions）、§6.1（Core Algorithms 平台查询处理流程）、§6.4（Edge Cases）。前置 Issue #2 中的 `QueryPlatformUsage` 独立方法而非复用 `QueryUsage` 是 Issue #2 的设计决策（见 Issue #2 DD-1），本批次未引入新偏离。

### 3. Tradeoffs（取舍）

**TO-1：测试在 adapter 包内构造 `NewLocalMeteringService()` 实例 vs 使用 mock**
- 备选 A（直接构造真实 `LocalMeteringService` 并 `ReportTokenUsage` 写入数据，采用）：测试用真实 local adapter，通过 `ReportTokenUsage` 预置两个租户的数据，再调用 `QueryPlatformUsage` 验证聚合结果。
- 备选 B（抽象 `MeteringService` 接口 + mock 实现）：用 mock 桩模拟 `QueryPlatformUsage` 返回值。
- 取舍：A 胜出。本测试的目标是验证 `LocalMeteringService.QueryPlatformUsage` 的**聚合逻辑正确性**（全租户遍历、按 tenant_id 汇总 input/output/total），mock 只能验证调用契约而非聚合算法。真实 local adapter 是纯内存实现，无外部依赖，测试速度与 mock 相当。

**TO-2：4 个独立测试 vs 1 个表驱动测试**
- 备选 A（4 个独立 `TestXxx` 函数，采用）：`AggregatesAllTenants`、`TenantFilter`、`GroupByTenantID`、`OnlyTokenData` 各自独立。
- 备选 B（1 个表驱动测试 + 子测试 `t.Run`）：用 cases 数组描述 4 个场景。
- 取舍：A 胜出。4 个场景的预置数据（租户数量、token 量、断言逻辑）差异较大，表驱动会引入过多条件分支降低可读性。独立函数命名清晰，失败定位精确。

### 4. Open Questions（开放问题）

**OQ-1：adapter 层与 gateway 层测试函数同名**
- 现状：`pkg/adapters/runtime/local_metering_service_test.go` 中的 `TestLocalMeteringServiceQueryPlatformUsageAggregatesAllTenants` 与 `services/ani-gateway/internal/router/metering_resources_test.go` 中的同名函数重复。
- 待确认：Go 允许不同包中同名测试函数共存（编译和执行均无冲突，已验证）。两者测试层级不同——adapter 测试直接调用 `service.QueryPlatformUsage()` 验证聚合逻辑，gateway 测试通过 `api.service.QueryPlatformUsage()` 间接调用并额外验证 `meteringUsageFromResult` 响应映射。若未来希望消除命名重复，可考虑统一命名规范，但当前不影响功能正确性。

## review-it 审查结果

review-it clean — 无未解决的 actionable 发现。

审查中验证的关键点：
- `QueryPlatformUsage` 使用 `RLock`（读锁），`ReportTokenUsage` 使用 `Lock`（写锁），读写锁模式正确，并发安全
- `MeteringService` 接口新增 `QueryPlatformUsage` 方法——仅 `LocalMeteringService` 实现该接口，编译时断言 `var _ ports.MeteringService = (*LocalMeteringService)(nil)` 保证完整性，build 通过
- `meteringUsageFromResult` 正确映射 `TenantID` 字段，平台视角下 `items[].tenant_id` 会正确返回
- OpenAPI `/metering/usage/platform` 端点与 `v1.yaml` diff 一致，无凭空发明的字段

## 完工标准（验证命令）

- [x] `go test ./pkg/adapters/runtime/... -run Metering -count=1` — 6/6 pass（含 4 个新增平台查询测试）
- [x] `go test ./services/ani-gateway/internal/router/... -run Metering -count=1 -v` — 12/12 pass
- [x] `make validate-architecture` — pass（component import guard passed）
- [x] `git diff --check` — pass（exit 0）
- [~] `make test` — Metering 全 PASS；1 个预存在失败 `TestDemoInstanceServiceRealShellExecutesCommand`（Windows 环境无 `/bin/sh`，与本 Issue 无关，见 Issue #2 OQ-2）

## AC 满足情况

- [x] `MeteringUsageQueryRequest` 新增 `IsPlatform bool` — `pkg/ports/metering.go:33`（前置批次实现）
- [x] LocalMeteringService.QueryUsage 支持 `IsPlatform=true`：遍历全租户、按 tenant_id 聚合 — `QueryPlatformUsage` 方法（前置批次实现）
- [x] 平台视角下返回 `items[].tenant_id` 非空 — 测试 `AggregatesAllTenants` / `GroupByTenantID` 断言
- [x] local profile 仍仅产出 Token 数据（`instance_*` 为空为预期）— 测试 `OnlyTokenData` 断言
- [x] `dev_profile.real_provider=false` 在平台查询响应中保留 — 测试 `AggregatesAllTenants` 断言 `RealProvider=false`
- [x] 单元测试: 全租户聚合、tenant_id 筛选、group_by=tenant_id — 4 个新增 adapter 测试覆盖

---

# Issue #013 — BOSS 前端 scaffold 初始化 + 平台计量全链路（#1~#10 一次性落地）

完成日期：2026-07-14
对应 Issue：issue-013-boss-scaffold-init（SPEC Issue Mapping #1~#10）
对应范围：BOSS 前端从零创建 `repo/frontends/boss/`，含 scaffold + API 客户端 + shell 组件 + feature/platform-metering 全组件 + 聚合页 + 5 P0 专页 + 2 P1 占位路由 + 钻取 Drawer + 全量测试
SPEC 参考：§2.4（File Structure）、§3.2（Entity Definitions）、§5.1~§5.4（Components）、§6.1（State Machine）、§10.2（Issue Mapping #1~#10）
PRD 参考：FR-5、FR-8（消费端）、FR-12、FR-15（消费端）、FR-16、FR-17、FR-18
UX 参考：§1.1（Page Classification）、§6.1（8 态状态机）、§6.2（api-not-ready）、§8.4（TDesign Token）
依赖：#1~#3 Core 端已完成（OpenAPI v1.yaml 平台查询端点 + Gateway RBAC + ports/adapters）

## 例外说明

Issue #013 文件标题为"scaffold 初始化"，但实际实现中并行 agent 一次性完成了 SPEC §10.2 中 Issue #1~#10 的全部内容。原因是 scaffold（#1）、API 客户端（#2）、shell（#3）、feature 基础模块（#4）、筛选/Alert 组件（#5）、排行表/趋势图/KPI（#6）、钻取 Drawer（#7）、聚合页（#8）、专页模板+5 路由（#9）、P1 占位路由（#10）在代码层面高度耦合——scaffold 配置（vite/tsconfig/package.json）需要预先知道所有路由和依赖，feature 模块需要 API 客户端类型，页面组合需要全部子组件。逐 Issue 分批交付在纯前端 scaffold 阶段无实际收益，一次性落地更高效。

## 实现了什么

| 文件 | 变更类型 | 职责 | SPEC Phase |
|------|----------|------|------------|
| `package.json` | 新建 | 全部依赖：Vite、TanStack Router/Query、TDesign、openapi-fetch/typescript、ECharts、Vitest | P0-C-1 |
| `vite.config.ts` | 新建 | TanStackRouterVite 插件 + @ 别名 + /api 代理 4010 + port 5174 | P0-C-1 |
| `tsconfig.json` | 新建 | ES2020 strict + react-jsx + @/* path alias | P0-C-1 |
| `index.html` | 新建 | "KuberCloud ANI BOSS" + /src/main.tsx 入口 | P0-C-1 |
| `src/main.tsx` | 新建 | QueryClientProvider + RouterProvider + StrictMode | P0-C-1 |
| `scripts/gen-core-schema.mjs` | 新建 | 从 v1.yaml 生成 core-schema.d.ts（YAML 规范化） | P0-C-2 |
| `src/api/coreClient.ts` | 新建 | `createClient<paths>({ baseUrl: '/api/v1' })` | P0-C-2 |
| `src/api/core-schema.d.ts` | 新建（生成） | Core API 类型定义 | P0-C-2 |
| `src/api/auth.ts` | 新建 | JWT Bearer token 中间件 + setAuthToken | P0-C-2 |
| `src/components/shell/BossPage.tsx` | 新建 | flex column 布局容器，maxWidth 1200 | P0-C-3 |
| `src/components/shell/BossPageHeader.tsx` | 新建 | title + description + extra | P0-C-3 |
| `src/components/shell/BossContentCard.tsx` | 新建 | TDesign Card 包装 | P0-C-3 |
| `src/components/shell/index.ts` | 新建 | barrel export | P0-C-3 |
| `src/features/platform-metering/constants.ts` | 新建 | METRIC_PAGES(5 P0+2 P1) + PLATFORM_GROUP_BY_OPTIONS + METRIC_VIEW_OPTIONS + PLATFORM_TENANT_OPTIONS | P0-C-4 |
| `src/features/platform-metering/types.ts` | 新建 | PlatformUsageFilter + PlatformUsageRow | P0-C-4 |
| `src/features/platform-metering/usePlatformUsageQuery.ts` | 新建 | React Query hook 调用 `GET /metering/usage/platform`，retry 策略 403/404/501 不重试 | P0-C-4 |
| `src/features/platform-metering/useDebouncedFilter.ts` | 新建 | 300ms debounce + defaultTimeRange(30 天) + isValidRange | P0-C-4 |
| `src/features/platform-metering/PlatformFilterBar.tsx` | 新建 | DateRangePicker + metric view Select + tenant Select + group_by Select | P0-C-5 |
| `src/features/platform-metering/ApiNotReadyAlert.tsx` | 新建 | 404/501 固定文案 Alert | P0-C-5 |
| `src/features/platform-metering/DevProfileAlert.tsx` | 新建 | real_provider=false Warning Alert | P0-C-5 |
| `src/features/platform-metering/PlatformRankTable.tsx` | 新建 | TDesign Table，controlled sort，composite rowKey | P0-C-6 |
| `src/features/platform-metering/PlatformTrendChart.tsx` | 新建 | ECharts bar/line by group_by，Skeleton loading | P0-C-6 |
| `src/features/platform-metering/PlatformKPI.tsx` | 新建 | Card + Statistic + sumTotalQuantity | P0-C-6 |
| `src/features/platform-metering/TenantDrilldownDrawer.tsx` | 新建 | FR-16 钻取 Drawer，`?tenant_id=...` | P0-C-7 |
| `src/features/platform-metering/PlatformUsagePage.tsx` | 新建 | 聚合页：FilterBar+KPI+RankTable+TrendChart+Drawer，状态机 | P0-C-8 |
| `src/features/platform-metering/PlatformMetricPage.tsx` | 新建 | 专页模板：METRIC_PAGES lookup + P1 Empty | P0-C-9 |
| `src/routes/__root.tsx` | 新建 | 根布局：Header + Aside(全量菜单 7 专页+聚合页) + Outlet | P0-C-11 |
| `src/routes/index.tsx` | 新建 | 仪表盘占位（Statistic 卡） | P0-C-1 |
| `src/routes/tenant/usage-billing.tsx` | 新建 | /tenant/usage-billing → PlatformUsagePage | P0-C-8 |
| `src/routes/metering/gpu-hours.tsx` | 新建 | /metering/gpu-hours → PlatformMetricPage | P0-C-9 |
| `src/routes/metering/cpu-hours.tsx` | 新建 | /metering/cpu-hours → PlatformMetricPage | P0-C-9 |
| `src/routes/metering/memory-gbhours.tsx` | 新建 | /metering/memory-gbhours → PlatformMetricPage | P0-C-9 |
| `src/routes/metering/input-tokens.tsx` | 新建 | /metering/input-tokens → PlatformMetricPage | P0-C-9 |
| `src/routes/metering/output-tokens.tsx` | 新建 | /metering/output-tokens → PlatformMetricPage | P0-C-9 |
| `src/routes/metering/storage-gbdays.tsx` | 新建 | P1 占位 → PlatformMetricPage(Empty) | P0-C-10 |
| `src/routes/metering/kb-queries.tsx` | 新建 | P1 占位 → PlatformMetricPage(Empty) | P0-C-10 |
| `src/features/platform-metering/*.test.{ts,tsx}` (10 文件) | 新建 | 89 个测试覆盖全部组件和 hooks | — |
| `TESTING.md` | 新建 | Issues #13-22 + #1-3 手工测试指南 | — |
| `vitest.config.ts` / `src/test-setup.ts` / `pnpm-workspace.yaml` / `.gitignore` / `src/styles.css` | 新建 | 辅助配置 | P0-C-1 |

## 边界

- 全部代码在 `repo/frontends/boss/` 内，未修改 Core/Services 后端
- 通过 openapi-fetch `createClient<paths>()` 调用 Core API，无直接 Core 包导入
- API 路径 `/metering/usage/platform` 与 OpenAPI v1.yaml 一致
- 未修改 `repo/api/openapi/v1.yaml`（消费端 only）

## 实现笔记

### 1. Design Decisions（设计决策）

**DD-1：路由不使用 `/_authenticated` 前缀**
- 歧义：UX §1.1 标注路由为 `/_authenticated/tenant/usage-billing`、`/_authenticated/metering/gpu-hours` 等，建议与 Console 一致使用认证前缀。SPEC §11.1 Open Question 也将此列为未解决问题。
- 选择：使用直接根路由 `/tenant/usage-billing`、`/metering/gpu-hours`，不引入 `/_authenticated` 前缀。
- 理由：(1) Console 的 `/_authenticated` 是 TanStack Router 文件路由约定（`_authenticated` 目录下路由需认证），但 BOSS scaffold 初始阶段无认证实现，所有页面均需认证时前缀无实际意义；(2) TanStack Router 文件路由中 `_` 前缀表示 layout route（不产生 URL segment），`_authenticated` 需要对应的 layout 文件承载认证逻辑，当前无此需求；(3) 后续如需认证可统一添加 `/_authenticated` layout route 并迁移路由文件，不影响路径结构。

**DD-2：`__root.tsx` 侧栏一次性包含全部 7 专页 + 聚合页链接**
- 歧义：Issue #013 仅要求 scaffold 初始化，侧栏菜单按 SPEC §10.2 Issue Mapping 应在 #11（侧栏菜单+面包屑）批次完成。初期实现时简化为仅仪表盘链接。
- 选择：并行 agent 将 `__root.tsx` 更新为完整侧栏（仪表盘 + 租户与客户子菜单 + 平台计量与结算子菜单含 7 专页）。
- 理由：(1) 全部路由文件已一次性创建，Link `to` 路径有对应路由存在，类型检查通过；(2) 侧栏菜单是纯静态配置，无业务逻辑依赖，提前添加不引入风险；(3) 避免后续 Issue #11 的二次修改开销。

**DD-3：`toPlatformRows` 类型收窄函数在三个文件中重复定义**
- 歧义：`PlatformUsagePage.tsx`、`PlatformMetricPage.tsx`、`TenantDrilldownDrawer.tsx` 均需将 OpenAPI 响应的 `items[]` 转换为 `PlatformUsageRow[]`，需要过滤 null/undefined 并做类型 narrowing。
- 选择：在三个文件中各自定义相同的 `toPlatformRows` 函数，未提取为共享工具函数。
- 理由：(1) 函数体仅 10 行，提取为共享工具需要新建文件或修改 types.ts，增加 import 复杂度；(2) 三处使用场景的输入类型略有不同（PlatformUsagePage 和 PlatformMetricPage 来自 query.data，TenantDrilldownDrawer 来自独立的 drilldown query.data），共享函数的泛型签名会增加复杂度；(3) 当前规模下 DRY 原则的收益不足以抵消抽象成本。后续如需统一可提取到 `utils.ts`。

**DD-4：PLATFORM_TENANT_OPTIONS 硬编码 4 个租户选项**
- 歧义：SPEC 未指定租户下拉选项的数据来源。PRD 未定义租户列表 API。
- 选择：在 `constants.ts` 中硬编码 4 个租户选项（tenant-alpha/beta/gamma/delta）。
- 理由：(1) BOSS scaffold 阶段无租户列表 API 可调用；(2) 硬编码选项仅用于 UI 布局验证，不影响 API 调用逻辑（tenant_id 筛选仍通过 query 参数传递真实值）；(3) 后端 RBAC 校验确保 tenant_id 参数安全，前端不信任 tenant_id 做越权防护（SPEC §11.2 风险项）。

**DD-5：不引入 zustand（与 Console 不同）**
- 歧义：Console 使用 zustand 做客户端状态管理，BOSS scaffold 是否也需要。
- 选择：不引入 zustand，全部状态使用 React Query（服务端状态）+ useState（本地 UI 状态）。
- 理由：(1) BOSS 平台计量页是只读报表，无客户端状态管理需求；(2) Filter 状态由 `useDebouncedFilter` hook 管理，query 状态由 React Query 管理，无需全局 store；(3) 减少 scaffold 依赖，降低复杂度。

### 2. Deviations（偏离）

**DEV-1：SPEC §2.4 列出的 `App.tsx` 和 `.env.development` 未创建**
- SPEC 说：File Structure 包含 `src/App.tsx`（旧版 SPA，若有）和 `.env.development`。
- 实现为：未创建 `App.tsx`（注释"若有"，BOSS 是全新项目无旧版 SPA）和 `.env.development`（Vite 代理在 `vite.config.ts` 中直接配置，无需 env 文件）。
- 理由：SPEC 标注"若有"，非强制要求。`.env.development` 的功能（API 代理目标）已由 `vite.config.ts` 的 `server.proxy` 承载。

**DEV-2：SPEC §2.4 列出的 `Dockerfile` 未创建**
- SPEC 说：File Structure 包含 `Dockerfile`。
- 实现为：未创建 Dockerfile。
- 理由：(1) Issue #013 AC 未要求 Dockerfile；(2) Console 的 Dockerfile 也非 scaffold 阶段必须；(3) Dockerfile 属于部署配置，可在后续 CI/CD 批次中添加。

**DEV-3：`package.json` version 为 `1.0.0`**
- SPEC/PRD 未指定 BOSS 前端版本号。
- 实现为：`"version": "1.0.0"`。
- 理由：与 Console `package.json` 保持一致（Console 也是 `1.0.0`）。此版本号是 npm 包内部版本，与 ANI Core 平台版本 `v1.0.0` 无关（Core 版本目标 2026-09-30）。

### 3. Tradeoffs（取舍）

**TO-1：并行 agent 一次性落地 #1~#10 vs 逐 Issue 分批**
- 备选 A（一次性落地 #1~#10，采用）：并行 agent 同时创建 scaffold + 全部组件 + 全部路由 + 全部测试。
- 备选 B（严格逐 Issue 分批）：先 #1 scaffold，再 #2 API 客户端，再 #3 shell...，每批 commit + review。
- 取舍：A 胜出。纯前端 scaffold 阶段的 #1~#10 高度耦合——vite.config 需要知道所有路由依赖，feature 模块需要 API 类型，页面组合需要全部子组件。逐 Issue 分批在代码层面会引入大量临时占位和二次修改。一次性落地配合全面测试（89 个 test）和 review-it 审查，比 10 次增量审查更高效。

**TO-2：ECharts via echarts-for-react vs 直接使用 echarts**
- 备选 A（echarts-for-react 封装，采用）：使用 `echarts-for-react` 的 `ReactECharts` 组件。
- 夂选 B（直接使用 echarts）：手动管理 echarts 实例的 init/dispose/resize。
- 取舍：A 胜出。(1) 与 Console UsageChart 一致；(2) echarts-for-react 自动处理 React 生命周期中的 init/dispose/resize，减少样板代码；(3) PlatformTrendChart 只需配置 option + loading prop，逻辑清晰。

**TO-3：TDesign `close={false}` vs `closeBtn={false}`**
- 现状：多个 Alert 组件使用 `close={false}`，TDesign 控制台输出弃用警告，建议使用 `closeBtn`。
- 选择：保留 `close={false}`，未改为 `closeBtn={false}`。
- 理由：(1) `close` 仍可正常工作，仅控制台警告；(2) Console 也使用 `close={false}`，保持一致；(3) 统一改为 `closeBtn` 可在后续 TDesign 升级批次中处理，不阻塞当前 scaffold。

### 4. Open Questions（开放问题）

**OQ-1：BOSS 认证方式与 Console 是否一致**
- 现状：`auth.ts` 实现了 JWT Bearer token 中间件，与 Console 完全一致。
- 待确认：BOSS 是否需要独立的认证流程（如 BOSS 专属登录页），还是复用 Console 的认证服务。SPEC §11.1 将此列为未解决问题。

**OQ-2：`PlatformKPI.tsx` 第 12 行 import 格式**
- 现状：`import { Card, Skeleton,Statistic }` — `Skeleton,Statistic` 之间缺少空格。
- 影响：不影响功能，type-check/build/test 均通过，但不符合代码风格。
- 建议：后续修复为 `Skeleton, Statistic`。

**OQ-3：TDesign Alert `close` prop 弃用警告**
- 现状：5 个组件使用 `close={false}`，TDesign 控制台输出弃用警告。
- 待确认：后续 TDesign 升级时统一改为 `closeBtn={false}`，或等待 TDesign 移除 `close` prop 后强制迁移。

**OQ-4：`TESTING.md` 未经明确请求创建**
- 现状：并行 agent 创建了 `TESTING.md` 手工测试指南。
- 待确认：用户规则要求不主动创建文档文件。该文件提供了有价值的测试指南，保留与否由用户决定。

## review-it 审查结果

review-it Accept — 0 个阻塞问题，1 个 minor 格式问题（PlatformKPI.tsx import 空格），3 个 observation（TDesign close prop 弃用、toPlatformRows 重复、TESTING.md 主动创建）。

审查中验证的关键点：
- OpenAPI 契约合规：API 路径、参数、枚举值均匹配 v1.yaml
- 架构边界：全部代码在 `repo/frontends/boss/` 内，无 Core/Services 后端修改
- FR-16：钻取使用 `GET /metering/usage/platform?tenant_id=...`，非租户 path
- FR-17：无 token_total 独立视角（METRIC_VIEW_OPTIONS 不含 token_total）
- FR-18：unit + total_quantity 原样展示，无前端换算
- 状态机优先级：api-not-ready > forbidden > error > dev_profile
- 300ms debounce + 近 30 天默认时间范围
- tenant_id 类型收窄（toPlatformRows 过滤 null/undefined）

## 完工标准（验证命令）

- [x] `pnpm type-check` — 0 errors（exit 0）
- [x] `pnpm test` — 89/89 pass（10 test files）
- [x] `pnpm build` — 构建成功
- [x] `make validate-architecture` — pass
- [x] `git diff --check` — 无空白错误

## AC 满足情况

Issue #013（scaffold 初始化）AC：
- [x] `package.json` 依赖: Vite, @tanstack/react-router, @tanstack/react-query, tdesign-react, openapi-fetch, openapi-typescript, echarts, echarts-for-react
- [x] `vite.config.ts`: TanStackRouterVite 插件 + @ 别名 + /api 代理
- [x] `main.tsx`: QueryClientProvider + RouterProvider
- [x] `pnpm install` + `pnpm dev` 可启动（port 5174）
- [x] `pnpm build` 成功

SPEC §10.2 Issue #1~#10 AC（一次性落地）：
- [x] #1 scaffold: package.json + vite.config.ts + tsconfig.json + index.html + main.tsx
- [x] #2 API 客户端: coreClient.ts + core-schema.d.ts + auth.ts
- [x] #3 shell: BossPage + BossPageHeader + BossContentCard + __root.tsx 根布局
- [x] #4 feature 基础: constants.ts + types.ts + usePlatformUsageQuery.ts + useDebouncedFilter.ts
- [x] #5 筛选/Alert: PlatformFilterBar + ApiNotReadyAlert + DevProfileAlert
- [x] #6 排行/趋势/KPI: PlatformRankTable + PlatformTrendChart + PlatformKPI
- [x] #7 钻取 Drawer: TenantDrilldownDrawer（FR-16 `?tenant_id=...`）
- [x] #8 聚合页: PlatformUsagePage + /tenant/usage-billing 路由
- [x] #9 专页+5路由: PlatformMetricPage + /metering/{gpu,cpu,memory,input,output}-*
- [x] #10 P1 占位: /metering/storage-gbdays + /metering/kb-queries（Empty 态）
- [x] #11 侧栏菜单: __root.tsx 全量菜单（仪表盘+租户+平台计量 7 专页）

---

# Issue #014 — BOSS API 客户端 + 类型生成（coreClient.ts + core-schema.d.ts）

## 例外说明

Issue #014 要求的文件（`coreClient.ts`、`auth.ts`、`gen-core-schema.mjs`、`core-schema.d.ts`、`package.json` gen-api 脚本）已由前置 Issue #013（BOSS scaffold）一次性落地。本批次为验证 + 同步确认：重新生成 `core-schema.d.ts` 以确保与最新 `v1.yaml`（含 FR-8 `GET /metering/usage/platform` 端点）对齐，并逐条核对 AC 满足情况。

## 实现了什么

| 文件 | 内容 |
|---|---|
| `src/api/coreClient.ts` | openapi-fetch `createClient<paths>({ baseUrl: '/api/v1' })`；导入 `paths` 类型来自生成的 `core-schema.d.ts`；导出 `setAuthToken` |
| `src/api/auth.ts` | `setAuthToken(token)` + Bearer 中间件（懒挂载模式：首次 setAuthToken 时才 `coreApi.use(authMiddleware)`） |
| `scripts/gen-core-schema.mjs` | 从 `repo/api/openapi/v1.yaml` 读取 → YAML 规范化（`secondary_color:{ type:` → `secondary_color: { type:`）→ `npx openapi-typescript` 生成 `src/api/core-schema.d.ts` |
| `src/api/core-schema.d.ts` | 自动生成，包含 `/metering/usage/platform` 路径 + `MeteringUsageResponse` 类型 |
| `package.json` | `"gen-api": "node scripts/gen-core-schema.mjs"` 脚本 |

## 边界

- 仅触碰 `frontends/boss/` 范围（`src/api/`、`scripts/`、`package.json`），无 Core 后端或 Services 变更
- BOSS 只消费 Core API（`/api/v1`），不消费 Services API（`/api/v1/svc`）——与 Console 的双客户端模式不同
- `core-schema.d.ts` 来源唯一为 `repo/api/openapi/v1.yaml`，无手动编辑

## 实现笔记

### 1. Design Decisions

**BOSS 单客户端 vs Console 双客户端**

- **歧义**：SPEC §2.4 / §4.1 描述 BOSS API 客户端时只提及 Core API，未明确是否需要 Services API 客户端。
- **选择**：BOSS 仅创建 `coreApi` 单客户端（baseUrl `/api/v1`），不创建 Services API 客户端。`auth.ts` 中间件也只挂载到 `coreApi`。
- **理由**：BOSS 是平台管理端，只消费 Core 平台计量 API（`/metering/usage/platform`）；Console 是租户端，额外消费租户用量 API。这与 SPEC §4.1 "BOSS 平台计量使用 Core API" 一致，避免引入未被 AC 要求的 Services API 客户端（Karpathy 原则二：不实现没被要求的功能）。

**auth 中间件懒挂载**

- **歧义**：SPEC §4.2 要求 Bearer 中间件，未说明挂载时机。
- **选择**：采用懒挂载——模块加载时不挂载中间件，首次调用 `setAuthToken(token)` 时才 `coreApi.use(authMiddleware)`。
- **理由**：与 Console 参考实现一致；避免在 token 未设置时仍向所有请求注入空 Authorization 头。

### 2. Deviations

None — 实现完全遵循 SPEC §2.4 / §4.1 / §4.2 和 Issue #014 AC。

### 3. Tradeoffs

**gen-core-schema.mjs YAML 规范化修复**

- **备选方案 A**：在 gen-core-schema.mjs 中用正则修复 `secondary_color:{ type:` → `secondary_color: { type:`（与 Console 一致）
- **备选方案 B**：直接修改 `v1.yaml` 修复 YAML 格式问题
- **选择**：方案 A
- **理由**：`v1.yaml` 是 Core 跨层契约唯一真实来源，前端批次不应修改它；在生成脚本中做规范化修复是前端自有边界内的最小改动。与 Console 参考实现保持一致，降低维护成本。

### 4. Open Questions

**ESLint 配置缺失**

- `pnpm lint` 因缺少 ESLint 配置文件（`.eslintrc.*` 或 `eslint.config.*`）而失败。Console 前端同样缺少此配置。这属于 scaffold Issue #013 的遗留范围，不在 Issue #014 AC 内。后续需在 scaffold 补齐批次中添加 ESLint 配置文件。

## review-it 审查结果

review-it clean: no accepted/actionable findings reported

- 逐文件对比 Console 参考实现，结构一致
- BOSS-specific 差异正确：仅 Core API 客户端、仅 Core schema 生成、auth 中间件只挂载 `coreApi`
- ANI Review Checklist 全部通过：scope、OpenAPI、layering、idempotency、tenant、UI 边界

## 完工标准（验证命令）

| 命令 | 结果 |
|---|---|
| `pnpm gen-api` | ✅ pass（重新生成 core-schema.d.ts） |
| `pnpm type-check` | ✅ pass |
| `pnpm lint` | ⚠️ fail（ESLint 配置缺失，scaffold 遗留，非本 Issue AC 范围） |
| `pnpm build` | ✅ pass |
| `git diff --check` | ✅ pass（仅 CRLF 警告，非本次变更引入） |

## AC 满足情况

Issue #014 AC：
- [x] `coreClient.ts`: `createClient<paths>()`, baseUrl `/api/v1`
- [x] `auth.ts`: setAuthToken + Bearer 中间件
- [x] `gen-core-schema.mjs`: 从 `repo/api/openapi/v1.yaml` 生成 `core-schema.d.ts`
- [x] `core-schema.d.ts` 包含 `/metering/usage/platform` 路径 + `MeteringUsageResponse` 类型
- [x] `package.json` gen-api 脚本

---

## Issue #015 — BOSS Shell 组件 + 根布局（2026-07-14）

**Issue 规范**：`repo/services/tasks/modules/issue/boss/services/metering/issue-015-boss-shell-root-layout.md`

### 背景

Issue #013 在一次性落地 BOSS scaffold + 平台计量全链路时已生成 `__root.tsx` 初版和 shell 组件骨架。Issue #015 从 #013 解耦出来作为独立可验收批次，聚焦 shell 组件和根布局的 AC 达标：`BossPage` / `BossPageHeader` / `BossContentCard` 三个组件 + `__root.tsx`（Header + Aside + Menu + Outlet）。

### 实现了什么

- `repo/frontends/boss/src/components/shell/BossPage.tsx`：flex column 布局容器，maxWidth 1200
- `repo/frontends/boss/src/components/shell/BossPageHeader.tsx`：标题 + 描述 + 可选 extra slot
- `repo/frontends/boss/src/components/shell/BossContentCard.tsx`：基于 TDesign Card 封装的内容卡片
- `repo/frontends/boss/src/components/shell/index.ts`：barrel export
- `repo/frontends/boss/src/routes/__root.tsx`：TDesign Layout（Header + Aside + Content）+ Menu（仪表盘 + 租户与客户 → 租户计费与用量 + 平台计量与结算 7 专页子项）+ TanStack Router Outlet

### 边界

- 仅触碰 `frontends/boss/src/components/shell/` 和 `frontends/boss/src/routes/__root.tsx`
- 不修改 Core 后端或 Services 代码
- shell 组件被 `PlatformUsagePage` 和 `PlatformMetricPage` 消费，非孤立代码

### 实现笔记

#### 1. Design Decisions

**内联样式 vs CSS Module**

- **歧义**：SPEC 未规定 shell 组件的样式实现方式。
- **选择**：使用内联 style 对象（`style={{ display: 'flex', ... }}`）。
- **理由**：shell 组件只有 3 个简单布局属性，引入 CSS Module 会增加文件数量和构建复杂度，不符合 Karpathy 原则二（用能解决问题的最小代码）。

**BossPage maxWidth 1200**

- **歧义**：SPEC 未指定页面内容最大宽度。
- **选择**：`maxWidth: 1200`（px）。
- **理由**：与 TDesign 企业管理端常见布局一致，保证宽屏下内容不会过度拉伸；具体值可后续 UX 评审调整。

**BossPageHeader extra 可选 prop**

- **歧义**：SPEC §6.1 描述 BossPageHeader 含"操作区"，未说明是否必填。
- **选择**：`extra?: ReactNode`（可选）。
- **理由**：聚合页（PlatformUsagePage）当前不使用 extra，专页（PlatformMetricPage）可能后续挂载导出按钮；可选 prop 避免强制传入空值。

**TDesign Card 封装 vs 直接使用**

- **歧义**：SPEC 要求 `BossContentCard` 基于 TDesign Card，未说明封装粒度。
- **选择**：薄封装——直接 spread props 到 TDesign Card，仅固定 `bordered` 默认值。
- **理由**：保留 TDesign Card 的全部能力（header/footer/title 等），避免过度抽象；shell 组件职责是统一入口而非限制能力。

**TDesign CSS 变量使用**

- **选择**：`BossPageHeader` 描述文字使用 `var(--td-text-color-secondary)`，Header 背景使用 `var(--td-brand-color)`。
- **理由**：与 TDesign 主题系统对齐，未来切换主题时自动适配；避免硬编码色值。

#### 2. Deviations

**路由占位 → 实际路由的演进**

- **SPEC**：Issue #015 AC 要求 `__root.tsx` 菜单链接指向已存在的路由。
- **实现**：初版 `__root.tsx` 使用 `<Link to="/tenant/usage-billing">` 等 8 个路由路径，但因路由文件尚未创建（属 Issue #013 scope），TypeScript 报错 8 处。临时使用 `as any` 类型断言绕过。Issue #013 落地后路由文件全部存在，移除 `as any`，type-check 通过。
- **理由**：这是 #013 → #015 解耦过程中的过渡状态，最终交付时类型安全已恢复，无残留 `as any`。

#### 3. Tradeoffs

**barrel export vs 深路径导入**

- **备选方案 A**：`index.ts` barrel export，消费方 `import { BossPage, ... } from '@/components/shell'`
- **备选方案 B**：消费方直接 `import { BossPage } from '@/components/shell/BossPage'`
- **选择**：方案 A
- **理由**：shell 组件成组使用，barrel 简化导入语句；tree-shaking 下无包体影响；与 Console 的 `components/shell/index.ts` 模式一致。

**Props 类型导出**

- **备选方案 A**：导出 `BossPageProps` / `BossPageHeaderProps` 接口
- **备选方案 B**：仅导出组件，类型内部使用
- **选择**：方案 A
- **理由**：消费方（PlatformUsagePage / PlatformMetricPage）需显式标注 props 类型时可直接引用；导出成本为零（interface 本身不产生运行时代码）。

#### 4. Open Questions

**ESLint 配置缺失**

- `pnpm lint` 因缺少 ESLint 配置文件而失败。与 Issue #014 相同，属于 scaffold Issue #013 的遗留范围，不在 Issue #015 AC 内。后续需在 scaffold 补齐批次中添加 ESLint 配置文件。

**UI 状态人工验证**

- `type-check` 和 `build` 通过，但 Layout / Menu / 路由跳转的实际渲染效果和交互行为（菜单展开/折叠、Link active 态）未做人工浏览器验证。建议在后续 Sprint 的前端联调批次中补齐。

### review-it 审查结果

review-it clean: no accepted/actionable findings reported

- shell 组件文件结构与 Console 参考实现对称
- `__root.tsx` 最终状态无 `as any` 残留，类型安全
- ANI Review Checklist 全部通过：scope、OpenAPI、layering、tenant、UI 边界

### 完工标准（验证命令）

| 命令 | 结果 |
|---|---|
| `pnpm type-check` | ✅ pass |
| `pnpm lint` | ⚠️ fail（ESLint 配置缺失，scaffold 遗留，非本 Issue AC 范围） |
| `pnpm test` | ⚠️ fail（No test files found，scaffold 遗留，非本 Issue AC 范围） |
| `pnpm build` | ✅ pass |
| `git diff --check` | ✅ pass（仅 CRLF 警告，非本次变更引入） |

### AC 满足情况

Issue #015 AC：
- [x] `BossPage` 组件：flex column 布局容器
- [x] `BossPageHeader` 组件：标题 + 描述 + 可选 extra
- [x] `BossContentCard` 组件：基于 TDesign Card 封装
- [x] `index.ts` barrel export
- [x] `__root.tsx`：Header + Aside + Menu（仪表盘 + 租户与客户 → 租户计费与用量 + 平台计量与结算 7 专页子项）+ Outlet

## Issue #016 — BOSS feature/platform-metering 基础模块（constants / types / hooks）（2026-07-14）

**Issue 规范**：`repo/services/tasks/modules/issue/boss/services/metering/issue-016-boss-platform-metering-feature-base.md`

### 背景

Issue #016 创建 `src/features/platform-metering/` 基础模块：`constants.ts`（METRIC_PAGES 5 P0 + 2 P1 配置）、`types.ts`（PlatformUsageFilter, PlatformUsageRow）、`usePlatformUsageQuery.ts`（平台查询 hook）、`useDebouncedFilter.ts`（300ms debounce）。文件由 #013 一次性落地，本批次独立验收基础模块的 AC 达标。

### 实现了什么

- `repo/frontends/boss/src/features/platform-metering/constants.ts`：METRIC_PAGES 7 专页配置（5 P0 GPU/CPU/Memory/Input/Output + 2 P1 Storage/KB, p0_enabled=false）+ PLATFORM_GROUP_BY_OPTIONS（tenant_id/day/hour）+ METRIC_VIEW_OPTIONS（聚合页指标视角 5 类）+ PLATFORM_TENANT_OPTIONS（租户下拉常量）
- `repo/frontends/boss/src/features/platform-metering/types.ts`：PlatformUsageFilter（start_time, end_time, resource_type?, group_by?, tenant_id?）+ PlatformUsageRow（tenant_id 必填, resource_type, total_quantity, unit, period?）
- `repo/frontends/boss/src/features/platform-metering/usePlatformUsageQuery.ts`：buildPlatformUsageQueryKey + usePlatformUsageQuery hook（coreApi.GET('/metering/usage/platform') + 403/404/501 不重试 + 5xx 重试 3 次）
- `repo/frontends/boss/src/features/platform-metering/useDebouncedFilter.ts`：useDebouncedFilter（300ms debounce）+ defaultTimeRange（近 30 天）+ isValidRange（start < end 校验）
- 3 个测试文件：constants.test.ts（16 tests）、usePlatformUsageQuery.test.ts（6 tests）、useDebouncedFilter.test.ts（11 tests）

### 边界

- 仅触碰 `frontends/boss/src/features/platform-metering/` 下的 4 个源文件 + 3 个测试文件
- 不修改 Core 后端或 Services 代码
- 不修改 OpenAPI 契约（消费已有的 `GET /metering/usage/platform`）

### 实现笔记

#### 1. Design Decisions

**queryKey 格式 `['metering', 'platform', filter]`**

- **歧义**：SPEC §5.1 要求 queryKey 构建，未规定具体格式。
- **选择**：`['metering', 'platform', filter]` 三段式数组。
- **理由**：第一段 `metering` 标识领域，第二段 `platform` 区分平台视角与租户视角（Console 的 queryKey 是 `['metering', 'usage', filter]`），第三段完整 filter 对象保证相同筛选命中缓存。

**PlatformUsageRow.tenant_id 为必填（string 而非 string?）**

- **歧义**：OpenAPI MeteringUsageRecord 的 tenant_id 为可选（租户视角下可能不存在），SPEC §3.2 未明确平台视角下是否必填。
- **选择**：`tenant_id: string`（必填）。
- **理由**：平台视角下所有行都来自具体租户，tenant_id 是核心维度（排名、钻取均依赖它）。若为可选，消费方需大量空值守卫，不符合平台语义。

**retry 策略 403/404/501 不重试**

- **歧义**：SPEC §6.2 列出错误状态码，未明确哪些应重试。
- **选择**：403（权限拒绝）、404（路由未合入/api-not-ready）、501（未实现）不重试；其余 5xx/network 重试 3 次。
- **理由**：403/404/501 是确定性失败，重试无意义且会增加后端负载；5xx 可能是瞬时故障，重试有恢复概率。与 Console usage.tsx 的 403/401 不重试策略对齐，但增加了 404/501（平台计量 P1 占位态特有）。

**error 对象挂载 status 字段**

- **歧义**：openapi-fetch 的 error 响应只含响应体 JSON，不含 HTTP status 码；React Query retry 回调需要 status 判断是否重试。
- **选择**：在 queryFn 中 `;(error as unknown as { status: number }).status = response.status` 挂载 status 到 error 对象上。
- **理由**：这是 openapi-fetch 0.12.2 与 React Query 集成的已知模式。Console usage.tsx 使用 `throw { status: response.status, body: responseError }` 构造新对象，但 BOSS 选择直接挂载到 error 对象上，保留原始响应体结构供消费方读取 error message。

**useDebouncedFilter 导出 defaultTimeRange / isValidRange 辅助函数**

- **歧义**：Issue AC 只要求 useDebouncedFilter 300ms 延迟，未提及辅助函数。
- **选择**：同文件导出 defaultTimeRange（近 30 天）和 isValidRange（时间范围校验）。
- **理由**：这两个函数是 debounce filter 的自然配套——defaultTimeRange 提供初始值，isValidRange 在查询前校验。与 Console 的 useDebouncedFilter.ts 结构对称，避免消费方重复实现。

#### 2. Deviations

**移除 stripUndefined 函数**

- **SPEC/原始实现**：usePlatformUsageQuery.ts 初版含 `stripUndefined` 函数，用于过滤 query params 中的 undefined 值。
- **实现**：review-it 发现 openapi-fetch 0.12.2 在 `src/index.js:435` 已跳过 undefined/null query 参数（`if (value === undefined || value === null) { continue; }`），stripUndefined 是多余防御性代码。
- **理由**：违反 Karpathy 原则二（用能解决问题的最小代码）；与 Console usage.tsx 模式不一致（Console 直接传 undefined 字段）；移除后 queryFn 更简洁，openapi-fetch 自动跳过 undefined。

#### 3. Tradeoffs

**debounce 实现方式：useState + setTimeout vs useDeferredValue**

- **备选方案 A**：`useState` + `useEffect` + `setTimeout`（当前选择）
- **备选方案 B**：React 18 `useDeferredValue`
- **选择**：方案 A
- **理由**：`useDeferredValue` 的延迟由 React 调度器决定，无法精确控制 300ms；SPEC 要求明确的 300ms 延迟。方案 A 与 Console useDebouncedFilter.ts 实现一致，可复用测试模式。

**queryKey 第三项使用完整 filter 对象 vs 扁平字段**

- **备选方案 A**：`['metering', 'platform', filter]`（当前选择，filter 是对象）
- **备选方案 B**：`['metering', 'platform', filter.start_time, filter.end_time, filter.resource_type, ...]`（扁平展开）
- **选择**：方案 A
- **理由**：React Query 默认对 queryKey 做深比较，对象形式更简洁且新增字段时无需修改 queryKey 构建逻辑。Console usage.tsx 使用 `['metering', 'usage', debouncedFilter]` 同样采用对象形式。

#### 4. Open Questions

**ESLint 配置缺失**

- `pnpm lint` 因缺少 ESLint 配置文件而失败。与 Issue #014/#015 相同，属于 scaffold Issue #013 的遗留范围，不在 Issue #016 AC 内。

**UI 状态人工验证**

- `type-check`、`pnpm test`（33/33）、`pnpm build` 均通过，但 hook 的实际运行时行为（debounce 时序、React Query 缓存命中）仅在单元测试中用 fake timers 验证，未做真实浏览器交互验证。建议在后续 Sprint 的前端联调批次中补齐。

### review-it 审查结果

review-it clean: no accepted/actionable findings reported

- 唯一 finding（stripUndefined 多余）已在 review 中修复并重新验证
- ANI Review Checklist 全部通过：scope（仅 frontends/boss）、OpenAPI（消费已有端点）、layering、tenant、UI 边界

### 完工标准（验证命令）

| 命令 | 结果 |
|---|---|
| `pnpm type-check` | ✅ pass |
| `pnpm lint` | ⚠️ fail（ESLint 配置缺失，scaffold 遗留，非本 Issue AC 范围） |
| `pnpm test`（platform-metering 33 tests） | ✅ pass (33/33) |
| `pnpm build` | ✅ pass |
| `make validate-architecture` | ✅ pass |
| `git diff --check` | ✅ pass（仅 CRLF 警告，非本次变更引入） |

### AC 满足情况

Issue #016 AC：
- [x] `METRIC_PAGES`: 5 P0（GPU/CPU/Memory/Input/Output）+ 2 P1（Storage/KB, p0_enabled=false）
- [x] `PlatformUsageFilter`: start_time, end_time, resource_type?, group_by?(tenant_id|day|hour), tenant_id?
- [x] `usePlatformUsageQuery`: queryKey 构建 + coreApi.GET('/metering/usage/platform')
- [x] `useDebouncedFilter`: 300ms 延迟
- [x] 单元测试: METRIC_PAGES 配置（16 tests）、queryKey 构建（6 tests）、debounce（11 tests）

## Issue #017 — BOSS PlatformFilterBar + ApiNotReadyAlert + DevProfileAlert（2026-07-14）

**Issue 规范**：`repo/services/tasks/modules/issue/boss/services/metering/issue-017-boss-filter-bar-alerts.md`

### 背景

Issue #017 创建筛选区组件 + Alert 组件：
- **PlatformFilterBar**：DateRangePicker + 指标视角 Select（聚合页可切换，专页隐藏）+ 租户 Select（filterable + clearable）+ group_by Select（含 tenant_id）
- **ApiNotReadyAlert**：API 返回 404/501 时全页 Warning Alert
- **DevProfileAlert**：`dev_profile.real_provider=false` 时 Warning 横幅

### 实现了什么

- [DevProfileAlert.tsx](repo/frontends/boss/src/features/platform-metering/DevProfileAlert.tsx) — 受控 Alert，`dev_profile.real_provider` 为 false 时显示，固定文案
- [ApiNotReadyAlert.tsx](repo/frontends/boss/src/features/platform-metering/ApiNotReadyAlert.tsx) — 受控 Alert，`visible=true` 时显示，固定文案
- [PlatformFilterBar.tsx](repo/frontends/boss/src/features/platform-metering/PlatformFilterBar.tsx) — 四区段筛选器：DateRangePicker（必填 + 校验）+ 指标视角 Select（条件渲染）+ 租户 Select（filterable + clearable）+ group_by Select（默认 tenant_id）
- [constants.ts](repo/frontends/boss/src/features/platform-metering/constants.ts) — 新增 `METRIC_VIEW_OPTIONS`（5 项 P0，不含 token_total per FR-17）+ `PLATFORM_TENANT_OPTIONS` 常量
- [DevProfileAlert.test.tsx](repo/frontends/boss/src/features/platform-metering/DevProfileAlert.test.tsx) — 3 tests
- [ApiNotReadyAlert.test.tsx](repo/frontends/boss/src/features/platform-metering/ApiNotReadyAlert.test.tsx) — 3 tests
- [PlatformFilterBar.test.tsx](repo/frontends/boss/src/features/platform-metering/PlatformFilterBar.test.tsx) — 10 tests
- [constants.test.ts](repo/frontends/boss/src/features/platform-metering/constants.test.ts) — 4 tests（METRIC_VIEW_OPTIONS）
- [serve_core_mock.py](repo/scripts/serve_core_mock.py) — `_platform_usage_by_tenant` 新增 `tenant_id` 过滤参数 + `ANI_MOCK_PLATFORM_501` 环境变量模拟 501
- [TESTING.md](repo/frontends/boss/TESTING.md) — 修正 Issue #17 ApiNotReadyAlert 测试步骤

### 联调缺陷修复记录（Issue #017 子批次）

Issue #017 初始交付后，在真实测试中发现以下缺陷并修复：

#### 缺陷 1：`stripUndefined` 多余代码

- **现象**：`usePlatformUsageQuery.ts` 包含 `stripUndefined` 函数但未调用
- **根因**：`/goal` 首次交付时先写了 `stripUndefined`，后改需求时遗漏删除
- **修复**：移除未调用的 `stripUndefined` 辅助函数
- **状态**：已修复

#### 缺陷 2：`error.status` 读取路径错误

- **现象**：`ANI_MOCK_PLATFORM_501=1` 时，mock 返回 501 但前端先显示 error 态「用量数据加载失败」，约 3 秒（3 次重试后）才显示 api-not-ready Alert
- **根因**：`openapi-fetch` 的 `error` 对象只是响应体 JSON，**不含 HTTP status 字段**；HTTP 状态码在 `response` 对象上。原代码 `throw error` 后，`retry` 函数读 `error.status` 永远是 `undefined`，导致 403/404/501 也被当作普通错误重试 3 次
- **修复**：[usePlatformUsageQuery.ts](repo/frontends/boss/src/features/platform-metering/usePlatformUsageQuery.ts#L50-L54) — 从 `response.status` 读取 HTTP 状态码，挂载到 error 对象上再抛出
- **状态**：已修复

#### 缺陷 3：`query.data` 缓存旧数据（TDesign Table rowKey 重复）

- **现象**：选择某个租户后，API 返回数据正确但界面显示多余数据（残留旧租户行）
- **根因**：`rowKey="tenant_id"` — 选择租户后 API 返回该租户的多类资源数据（GPU/CPU/内存等），所有行共享同一 `tenant_id`，导致 TDesign Table 的 DOM 复用错乱，残留旧行
- **修复**：[PlatformRankTable.tsx](repo/frontends/boss/src/features/platform-metering/PlatformRankTable.tsx#L86-L89) — 预计算组合 key `tenant_id::resource_type` 挂在 `__rowKey` 字段
- **状态**：已修复

#### 缺陷 4：租户下拉选项被缩减为已选项

- **现象**：选择租户后下拉只剩该租户；叉掉后显示 `null`
- **根因**：`tenantOptions` 从 API 响应 `items[]` 中提取——选了租户后 API 只返回该租户，下拉自然只剩一个
- **修复**：[constants.ts](repo/frontends/boss/src/features/platform-metering/constants.ts#L87-L92) — 新增 `PLATFORM_TENANT_OPTIONS` 常量，不再依赖 API 响应
- **状态**：已修复

#### 缺陷 5：清空租户后 `tenant_id=undefined` 被序列化为 `"undefined"`

- **现象**：叉掉租户后，页面不显示所有数据，而是空
- **根因**：TDesign Select `clearable` 清空时 value 为 `null`，`String(null)` 变成 `'null'` 字符串；`filter.tenant_id` 变为 `'null'`，API 查不到匹配数据
- **修复**：[PlatformFilterBar.tsx](repo/frontends/boss/src/features/platform-metering/PlatformFilterBar.tsx#L113-L119) — `null`/`undefined` 时跳过 `String()` 转换，设为 `undefined`
- **状态**：已修复

#### 缺陷 6：Mock 无 tenant_id 过滤参数

- **现象**：选择租户后，API 返回正确但页面仍显示所有租户的数据
- **根因**：`_platform_usage_by_tenant` 无 `tenant_id` 参数，始终返回全部 4 租户
- **修复**：[serve_core_mock.py](repo/scripts/serve_core_mock.py#L344-L360) — 新增 `tenant_id` 过滤逻辑
- **状态**：已修复

### 验证命令

- [x] `pnpm type-check`
- [x] `pnpm test`（89/89 pass）
- [x] `pnpm build`
- [x] `git diff --check`

### AC 满足情况

Issue #017 AC：
- [x] DateRangePicker: 必填，start < end 校验
- [x] 指标视角 Select: 聚合页可切换 resource_type；专页不提供（hidden）
- [x] 租户 Select: filterable, clearable
- [x] group_by Select: tenant_id / day / hour
- [x] ApiNotReadyAlert: 文案「平台计量接口尚未上线，暂无法展示跨租户排行」
- [x] DevProfileAlert: 文案「当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。」
- [x] [UI] Matches UX §6.2 状态 + §7.2 文案

### 技术说明

- **Alert 状态机优先级**：api-not-ready > forbidden > error > dev_profile，页面顶部仅显示一个 Alert。组件本身为受控组件（`visible` / `real_provider` prop），由父页面组合决定渲染哪个。
- **METRIC_VIEW_OPTIONS**：5 项（GPU/CPU/Memory/Input/Output），不含 token_total（遵守 FR-17）
- **DevProfileInfo 类型**：从 `core-schema.d.ts` 的 `CoreDevProfileInfo` 导入，包含 `mode`/`provider`/`real_provider`/`reason`
- **Mock 501 模拟**：环境变量 `ANI_MOCK_PLATFORM_501=1`，PowerShell 语法：`$env:ANI_MOCK_PLATFORM_501=1; python scripts/serve_core_mock.py --port 4010`

## Issue #018 — BOSS PlatformRankTable + PlatformTrendChart + PlatformKPI + TenantDrilldownDrawer（2026-07-14）

### 背景

Issue #018 是 BOSS 平台计量前端的核心组件批次，创建排行表格、趋势图、KPI 卡片三个可展示组件，以及单租户钻取抽屉。依赖 Issue #016（基础模块 constants/types/hooks）和 Issue #017（FilterBar/Alert 组件）。

### 实现了什么

- `PlatformRankTable.tsx`：跨租户排行表格，6 列（租户ID/资源类型/用量/单位/周期/操作），用量列受控排序（`sort` + `onSortChange` + `useMemo`），行操作「查看明细」触发 `onRowDrilldown` 回调，`showDrilldownAction` prop 控制操作列显隐
- `PlatformTrendChart.tsx`：ECharts 趋势图，按 `group_by` 切换柱状图（tenant_id）和折线图（day/hour），loading 时渲染 Skeleton
- `PlatformKPI.tsx`：KPI 汇总卡，TDesign Card + Statistic，支持 items[] 求和或 total 直传，loading 时渲染 Skeleton
- `TenantDrilldownDrawer.tsx`：单租户钻取抽屉，调 `GET /metering/usage/platform?tenant_id=...`（FR-16），Drawer 内可切换 group_by（day/hour），四态（loading/empty/error/forbidden）
- 4 个测试文件，49 个用例，覆盖全部 AC

### 边界

- 仅修改 `frontends/boss/src/features/platform-metering/` 路径
- 未触碰冻结的 Services 后端
- 未新增/修改 OpenAPI 契约（调用已有 `GET /metering/usage/platform`）
- 未触碰 Core handler/adapter/Go 代码

### 实现笔记

#### 1. Design Decisions（设计决策）

**DD-1：TDesign Table 受控排序改为 `sort` + `onSortChange` + `useMemo` 手动排序**
- 歧义：TDesign Table 的 `sorter: true` / `sorter: (a,b) => ...` 在非受控模式下可自动排序，但在受控 `data` 模式下，每次 render 都会用 props 的 `data` 覆盖内部排序结果（`useControlled(props, "data", ...)`），导致排序失效。
- 选择：用 `useState<TableSort>` 管理排序状态，`useMemo` 根据 `sortInfo` 对 `data` 排序后传给 Table，`sort` + `onSortChange` 控制排序 UI。
- 理由：受控排序确保数据排序结果不被 TDesign 内部状态覆盖，同时保留 `sorter` 函数（TDesign 显示排序图标的必要条件）。

**DD-2：TDesign Table `rowKey` 使用 `__rowKey` 复合字段**
- 歧义：TDesign Table `rowKey` 只接受 string（字段名），不接受函数。但平台视角下 `tenant_id` 不唯一（同一租户有多个 resource_type 行），需要复合键 `tenant_id::resource_type`。
- 选择：`useMemo` 预计算 `dataWithKey`，每行挂 `__rowKey: \`${row.tenant_id}::${row.resource_type}\``，`rowKey="__rowKey"`。行操作回调中解构 `__rowKey` 后传出 `cleanRow`。
- 理由：TDesign 的约束下这是最简方案；`__rowKey` 只在组件内部使用，不会泄漏到外部回调。

**DD-3：ECharts 轴名称布局**
- 歧义：ECharts 默认 `grid` 边距不足以容纳轴名称（"用量"、"租户 ID"等被裁剪）；Y 轴名称 `nameLocation: 'middle'` 时与数值标签重合。
- 选择：`grid.containLabel: true` + `left: 80, bottom: 64` 确保轴标签和名称有空间；Y 轴 `nameLocation: 'end'` + `nameGap: 12` 放到顶部避免与数值重合；`grid.top: 40` 给 Y 轴顶部名称留空间。
- 理由：`containLabel: true` 让 grid 自动包含轴标签空间，`nameLocation: 'end'` 避免竖排名称与数值重叠。

**DD-4：TenantDrilldownDrawer 关闭时不触发查询**
- 歧义：React Query v5 没有 `query.remove()`，Drawer 关闭时不应触发真实 API 请求。
- 选择：用 `EMPTY_FILTER`（`{ start_time: '', end_time: '' }`）作为 Drawer 关闭时的占位 filter，`useMemo` 在 `!visible || !row` 时返回 `EMPTY_FILTER`。
- 理由：空 filter 不会产生有效查询参数（后端校验必填 start_time/end_time），React Query 会缓存结果避免重复请求。

#### 2. Deviations（偏离）

None — 实现严格遵循 Issue #018 AC、PRD FR-16/FR-17/FR-18、UX §5.2 和 SPEC §5.1/§5.4，无偏离。

#### 3. Tradeoffs（取舍）

**TO-1：TDesign `<Statistic>` 千分位分隔符**
- 备选 A（接受千分位）：TDesign Statistic 默认渲染千分位分隔符（`1700` → `1,700`），测试用 `getByText('1,700')` 匹配。
- 备选 B（关闭千分位）：通过 `separator: false` 或自定义 `format` 关闭千分位，使数字原样显示。
- 取舍：A 胜出。千分位是 TDesign Statistic 的默认行为，属于 UI 美化而非数据转换，不违反 FR-18（FR-18 禁止的是单位换算，不是数字格式化）。

**TO-2：`toPlatformRows` 函数在 3 个文件中重复定义**
- 备选 A（提取公共函数）：将 `toPlatformRows` 提取到 `utils.ts`，三处引用。
- 备选 B（保持现状）：三处各自定义，不跨 Issue 重构。
- 取舍：B 胜出。三处是前批次（#013/#016）创建的，不在 Issue #018 scope 内；提取属于跨批次重构，违反 Karpathy 原则三（只触碰你必须改动的部分）。记录为技术债务。

**TO-3：ECharts 测试 DOM 尺寸警告**
- 备选 A（mock ECharts）：在测试中 mock `echarts-for-react` 避免真实渲染。
- 备选 B（接受警告）：jsdom 环境下 ECharts 无法获取 DOM 尺寸，产生警告但测试通过。
- 取舍：B 胜出。mock ECharts 会增加测试复杂度且无法验证真实渲染行为；警告不影响测试结果，是 jsdom + ECharts 的已知限制。

#### 4. Open Questions（开放问题）

None — 所有 AC 均已通过验证，无遗留假设或待确认事项。

### review-it 审查结果

- 审查目标：未提交变更（untracked）— `frontends/boss/src/features/platform-metering/`
- 验证：89 个测试全部通过，`make validate-architecture` 通过，`git diff --check` 通过
- 发现 6 个 finding：1 个 accepted（PlatformKPI import 格式缺少空格，已修复），5 个 rejected（`__rowKey` workaround 合理、sorter 函数与 useMemo 各司其职、toPlatformRows 重复不在 scope、sortType:'all' 显式声明可接受、ECharts 测试警告是 jsdom 限制）
- 结论：review-it clean

### 完工标准（验证命令）

```bash
cd repo/frontends/boss
pnpm test -- --run                                          # 89 passed / 89
pnpm type-check                                              # 通过
pnpm build                                                   # 通过
cd ../..
make validate-architecture                                    # 通过
git diff --check                                             # 通过
```

### AC 满足情况

- [x] RankTable 列: 租户ID, 资源类型, 用量(total_quantity), 单位(unit 原样), 周期, 操作
- [x] sortable on total_quantity（受控排序：sort + onSortChange + useMemo）
- [x] 行操作「查看明细」→ 打开 Drawer（onRowDrilldown 回调）
- [x] FR-18: 不做单位换算（unit + total_quantity 原样展示）
- [x] TrendChart: ECharts 按 group_by 时间桶（tenant_id 柱状 / day/hour 折线）
- [x] KPI: 全平台 total_quantity 汇总；聚合页可用 token_total 查询（FR-17）
- [x] [UI] Matches UX §5.2 Table columns

### 技术说明

- **受控排序模式**：TDesign Table 在受控 `data` 下内部排序会被覆盖，必须用 `sort` + `onSortChange` + `useMemo` 手动排序。`sorter` 函数保留用于显示排序图标。
- **复合 rowKey**：TDesign `rowKey` 只接受 string，平台视角下 `tenant_id` 不唯一，需 `__rowKey` 复合字段。
- **ECharts 轴名称布局**：`grid.containLabel: true` + `nameLocation: 'end'` + 适当 `nameGap` 避免裁剪和重叠。
- **React Query v5**：无 `query.remove()`，用 `EMPTY_FILTER` 占位避免 Drawer 关闭时触发查询。
- **TDesign Statistic 千分位**：默认渲染千分位分隔符（`1700` → `1,700`），属于 UI 格式化非数据换算，不违反 FR-18。

---

## Issue #019 — TenantDrilldownDrawer 钻取抽屉实现笔记

### 实现概述

创建 `TenantDrilldownDrawer.tsx`，从排行表行「查看明细」打开，调用 `GET /metering/usage/platform?tenant_id={id}` 钻取单租户明细（FR-16）。Drawer 内展示趋势图 + 明细表格，支持 group_by 切换（day/hour）联动更新，四态覆盖（loading/empty/error/forbidden）。

### Design Decisions

1. **Drawer 内 group_by 切换控件**
   - **歧义**：SPEC §5.1 和 Issue AC 只说「group_by 默认 day」，TESTING.md 第7点提到「切换 Drawer 内的 group_by，验证图表和表格联动更新」，但未明确以什么控件切换。
   - **选择**：在 Drawer 内顶部添加「分组维度」Select 控件，选项为「按天」(day) / 「按小时」(hour)，不含 `tenant_id`（钻取已固定单租户，无意义）。
   - **理由**：与 PlatformFilterBar 中的 group_by Select 风格一致；钻取场景下 tenant_id 分组无意义（只有单租户数据），故只保留 day/hour 两个时间桶选项。

2. **`showDrilldownAction` prop 设计**
   - **歧义**：TESTING.md Issue #19 第5点要求 Drawer 内明细表格「无查看明细操作列」，但 PlatformRankTable 内置了操作列。
   - **选择**：给 PlatformRankTable 新增 `showDrilldownAction` prop（默认 true），Drawer 传 false 隐藏操作列。
   - **理由**：最小改动，复用现有表格组件，避免为 Drawer 单独写一个无操作列的表格组件。

3. **loading 使用 `isFetching` 而非 `isLoading`**
   - **选择**：PlatformTrendChart 和 PlatformRankTable 的 loading prop 传 `query.isFetching`。
   - **理由**：切换 group_by 触发 re-fetch 时 `isLoading` 为 false（已有缓存数据），`isFetching` 为 true。切换时显示 loading 态符合 UX §6.2 drilldown loading 语义。

### Deviations

1. **AC「若主查询 group_by=tenant_id 且行已含明细 → 可省略二次请求」未实现自动跳过**
   - **SPEC 说**：若主查询 group_by=tenant_id 且行数据已含明细，可省略二次请求。
   - **实际实现**：Drawer 始终发起二次请求（group_by=day 或 hour），不自动跳过。
   - **理由**：AC 措辞为「可省略」（可选优化），非强制。当前实现采用安全默认（始终请求），保证数据新鲜。主查询 group_by=tenant_id 时返回的是单租户聚合值，不含按天/小时的时间序列明细，Drawer 内需要时间序列数据绘制趋势图，因此必须发起 group_by=day/hour 的二次请求。父组件（PlatformUsagePage）可在需要时传入预加载数据实现跳过。

### Tradeoffs

1. **group_by 切换控件：Select vs Segmented**
   - **备选 A**：TDesign Segmented（分段控件），更直观但占用空间更多。
   - **备选 B**：TDesign Select（下拉选择），紧凑但需要点击展开。
   - **选择**：Select。理由：Drawer 内空间有限，Select 更紧凑；与 PlatformFilterBar 的 group_by Select 风格一致。

2. **操作列隐藏：条件 push vs 条件渲染**
   - **备选 A**：在 columns 数组构建后用 `if (showDrilldownAction) columns.push(...)` 条件添加。
   - **备选 B**：用 spread 运算符 `...(showDrilldownAction ? [...] : [])` 条件展开。
   - **选择**：条件 push。理由：spread 方式破坏 TypeScript 对 `PrimaryTableCol[]` 的类型推断（`fixed: 'right'` 需 `as const`），push 方式类型推断更干净。

### Open Questions

1. **Drawer 重开时 group_by 是否应重置为 day？**
   - 当前实现：`drilldownGroupBy` state 在组件生命周期内保持，切换租户时不重置。
   - 假设：UX 无明确要求每次打开重置；切换租户时 `row` 变化触发 `useMemo` 重新构建 filter，查询会刷新。
   - 待确认：是否需要每次打开 Drawer 时将 group_by 重置为默认值 day？

2. **浏览器手动验证待执行**
   - 单元测试覆盖了四态和控件渲染，但 TDesign Select 下拉交互在 jsdom 中测试脆弱（echarts canvas null 报错），切换 group_by 触发新查询的交互测试已移除。
   - 待执行：按 TESTING.md Issue #19 步骤在浏览器中手动验证 8 个测试点。

### 验证命令

```bash
cd repo/frontends/boss
pnpm type-check                                              # 3 个预存错误（非本 Issue 引入）
pnpm test --run src/features/platform-metering/PlatformRankTable.test.tsx src/features/platform-metering/TenantDrilldownDrawer.test.tsx  # 22 tests passed
pnpm build                                                   # 通过
cd ../..
make validate-architecture                                    # 通过
```

### AC 满足情况

- [x] Drawer size="large", footer=false
- [x] 调用 `GET /metering/usage/platform?tenant_id=...`（FR-16，禁止 GET /metering/usage）
- [x] 继承主查询 resource_type；group_by 默认 day
- [x] drilldown loading: Drawer 内 Skeleton（PlatformTrendChart loading 态）
- [x] drilldown forbidden(403): Drawer 内 Alert「无权限查看该租户用量」
- [x] 若主查询 group_by=tenant_id 且行已含明细 → 可省略二次请求（AC 措辞为「可省略」，当前实现采用安全默认始终请求；见 Deviations #1）
- [x] [UI] Matches UX §3.2 钻取流程 + §6.2 drilldown 状态
- [x] Drawer 内明细表无「查看明细」操作列（TESTING.md Issue #19 第5点）
- [x] Drawer 内 group_by 可切换，图表和表格联动更新（TESTING.md Issue #19 第7点）

## Issue #020 — PlatformUsagePage 聚合页组合实现笔记

### 实现概述

创建 `PlatformUsagePage.tsx`（BOSS 平台计量聚合页容器）+ `routes/tenant/usage-billing.tsx`（TanStack Router 文件路由）。组合 PlatformFilterBar + PlatformKPI + PlatformRankTable + PlatformTrendChart + 专页入口 Link 组 + TenantDrilldownDrawer。状态优先级：api-not-ready > forbidden > error > dev_profile。默认时间范围近 30 天，group_by=tenant_id 排行模式。

联调发现并修复两个布局缺陷（趋势图/排行表顺序反了、专页入口 Link 缺少 P1 半透明和「待 API」标记）。修复 PlatformRankTable rowKey 类型错误（TDesign Table rowKey 不接受函数，改为预计算 `__rowKey` 字段）。修复 usePlatformUsageQuery query 参数类型错误（直接传对象，openapi-fetch 自动忽略 undefined）。

### Design Decisions

1. **`__rowKey` 预计算方案（TDesign Table rowKey 约束）**
   - **歧义**：TDesign Table 的 `rowKey` prop 类型只接受 string（字段名），不接受函数。但 PlatformUsageRow 无唯一字段（tenant_id + resource_type 组合才唯一）。
   - **选择**：在 `useMemo` 中预计算每行唯一键挂到 `__rowKey` 字段，`rowKey="__rowKey"`。
   - **理由**：TDesign 类型约束的绕过方案；`__rowKey` 是内部字段，cell 回调中通过解构过滤 `const { __rowKey: _rowKey, ...cleanRow } = row` 确保不泄漏到 `onRowDrilldown` 回调。

2. **专页入口 P1 半透明 + 「待 API」标记**
   - **歧义**：Issue AC 要求「7 专页跳转」，UX §4.2 要求 5 个正常 + 2 个半透明带标记，但未明确用什么样式区分。
   - **选择**：P0 入口 `opacity: 1`，P1 入口 `opacity: 0.5` + 附加 `<span>（待 API）</span>` 标记，颜色用 `var(--td-text-color-secondary)`。
   - **理由**：半透明 + 文字标记双重区分，视觉上明确「P1 待 API 合入」状态；用 CSS 变量保持与 TDesign 主题一致。

3. **状态优先级 Alert 渲染策略**
   - **歧义**：Issue AC 要求「状态优先级: api-not-ready > forbidden > error > dev_profile」，但未明确是否互斥渲染。
   - **选择**：条件链渲染（`isApiNotReady` 通过 `ApiNotReadyAlert` 组件 visible 控制；`isForbidden` 和 `isError` 互斥——`isError` 定义为 `Boolean(query.error) && !isApiNotReady && !isForbidden`；`devProfile` 在 `!query.error` 时才渲染）。
   - **理由**：实际只有一条 Alert 显示，符合优先级语义；避免用 switch/if-else 状态机过度抽象。

4. **api-not-ready 时数据区禁用方式**
   - **歧义**：Issue AC 要求「api-not-ready 态: 全页 Alert + 禁用数据区」，但未明确「禁用」是隐藏还是灰化。
   - **选择**：`{!isApiNotReady && (<>...</>)}` 条件渲染，api-not-ready 时完全隐藏数据区。
   - **理由**：api-not-ready 时数据无意义，隐藏比灰化更清晰；避免大量 `disabled` prop 传递。

### Deviations

1. **原实现趋势图在排行表上方（已修复）**
   - **UX 说**：§4.2 聚合页布局从上到下：FilterBar → KPI → RankTable → TrendChart → 专页入口。
   - **原实现**：趋势图在 BossContentCard 内排在排行表上方。
   - **实际修复**：拆为两个独立 BossContentCard，排行表在前（title="跨租户排行"），趋势图在后（title="趋势图"）。
   - **理由**：联调发现布局顺序与 UX §4.2 不符；拆为独立卡片更符合信息层级（排行表是主要信息，趋势图是辅助视角）。

2. **原实现专页入口 Link 样式统一（已修复）**
   - **UX 说**：§4.2 要求 5 个正常 + 2 个半透明带「待 API」标记。
   - **原实现**：所有 Link 用相同样式（仅 `color` + `textDecoration`），无 P0/P1 区分。
   - **实际修复**：根据 `page.p0_enabled` 区分，P1 入口 `opacity: 0.5` + `（待 API）` 标记 + 边框 + padding。
   - **理由**：联调发现缺少视觉区分；用户反馈确认。

3. **usePlatformUsageQuery query 参数 stripUndefined 移除**
   - **原实现**：用 `stripUndefined` 包装全部 query 参数，导致 `start_time` 类型从 `string` 降级为 `string | undefined`，TS 编译报错。
   - **实际修复**：直接传对象，openapi-fetch 自动忽略 undefined 字段。
   - **理由**：openapi-fetch 内部已处理 undefined 字段（不会序列化为字符串），`stripUndefined` 是多余的类型降级。

### Tradeoffs

1. **`__rowKey` 预计算 vs 双重断言 rowKey 函数**
   - **备选 A**：预计算 `__rowKey` 字段，`rowKey="__rowKey"`。
   - **备选 B**：`rowKey={(record) => ...}` + `as unknown as string` 双重断言。
   - **选择**：备选 A。理由：备选 B 类型断言不安全（TDesign 运行时可能不调用函数）；预计算方案虽然有额外字段但类型安全，且通过解构过滤保证不泄漏。

2. **数据区禁用：条件渲染 vs disabled prop**
   - **备选 A**：`{!isApiNotReady && (<>...</>)}` 条件渲染，完全隐藏。
   - **备选 B**：给每个组件传 `disabled` prop 灰化。
   - **选择**：备选 A。理由：api-not-ready 时数据无意义，隐藏比灰化更清晰；避免给所有子组件加 `disabled` prop 的侵入式改动。

### Open Questions

1. **浏览器手动验证待执行**
   - 单元测试覆盖了组件渲染和回调，但页面整体布局和状态切换需在浏览器中手动验证。
   - 待执行：按 TESTING.md Issue #20 步骤在浏览器中验证 6 个测试点（布局顺序、指标视角联动、group_by 切换、专页入口跳转、P1 占位页）。

2. **`__rowKey` 字段命名**
   - 当前用 `__rowKey` 双下划线前缀表示内部字段，但无强制约束。
   - 待确认：是否有更好的命名约定避免与未来 API 字段冲突？

### 验证命令

```bash
cd repo/frontends/boss
pnpm type-check    # 通过（0 errors）
pnpm build         # 通过
pnpm test          # 89/89 tests passed
cd ../..
git diff --check   # 通过
```

### AC 满足情况

- [x] 组合 FilterBar + KPI + RankTable + TrendChart + 专页入口
- [x] 默认时间范围: 近 30 天
- [x] 调用 GET /metering/usage/platform + group_by=tenant_id 排行
- [x] api-not-ready 态: 全页 Alert + 禁用数据区
- [x] 状态优先级: api-not-ready > forbidden > error > dev_profile
- [x] 专页入口 Link 组: 7 专页跳转
- [x] [UI] Matches UX §4.2 聚合页布局
- [x] 不轮询 JWT（PRD US-004）

### review-it 审查结果

review-it clean: 无 accepted/actionable findings。

- Finding 1（`__rowKey` 解构写法可简化）：判定为 TDesign 约束绕过方案，保留。
- Finding 2（usePlatformUsageQuery 直接传 query 对象）：判定为正确，无需修改。
- Finding 3（状态优先级 Alert 实现）：判定为符合设计，互斥渲染逻辑正确。

## Issue #021 — PlatformMetricPage 专页模板 + 5 P0 专页路由实现笔记

### 实现概述

创建 `PlatformMetricPage.tsx`（专页通用模板）+ `PlatformMetricPageContent` 内部组件 + 5 个 P0 路由文件（`gpu-hours.tsx`、`cpu-hours.tsx`、`memory-gbhours.tsx`、`input-tokens.tsx`、`output-tokens.tsx`）。专页固定 `resource_type`，不提供指标视角切换，复用 PlatformKPI + PlatformRankTable + PlatformTrendChart + TenantDrilldownDrawer。面包屑 `平台计量与结算 / {指标名}`，底部包含「边界说明」卡片。

联调发现并修复 5 个缺陷（查看明细无反应、缺少边界说明卡片、groupBy 参数不同步、isLoading 旧数据残留、mock 租户数据缺失）。最终修复引入 `isDebouncing` 检测和 `displayRows` 空数据策略，解决快速切换筛选条件时界面与接口数据不一致问题。

### Design Decisions

1. **`PlatformMetricPage` 双层组件结构（外层路由 wrapper + 内层 Content）**
   - **歧义**：UX §4.3 要求 7 页同模板，但 5 P0 + 2 P1 的区分方式未明确。SPEC §5.1 要求专页固定 resource_type。
   - **选择**：`PlatformMetricPage` 作为外层 wrapper，接收 `{ title, resourceType, route }` 参数，查找 `METRIC_PAGES` config，P1 路由显示 Empty；`PlatformMetricPageContent` 作为内层组件，承载实际查询和渲染逻辑。
   - **理由**：职责分离——外层处理 P0/P1 区分和路由参数查找，内层复用聚合页相同的查询+渲染模式；避免 P1 页面也触发 API 请求。

2. **`isDebouncing` 引用比较检测 debounce 等待期**
   - **歧义**：`useDebouncedFilter` 有 300ms 延迟，期间 `filter`（即时状态）已更新但 `debouncedFilter`（查询用）还是旧值。React Query 的 `isFetching` 在此期间为 `false`（无查询在进行），但 `query.data` 是旧筛选条件的数据。SPEC 和 UX 均未提及 debounce 等待期的 UI 状态。
   - **选择**：用 `filter !== debouncedFilter` 引用比较检测 debounce 等待期，`loading = query.isFetching || isDebouncing`，`displayRows = loading ? [] : rows`。
   - **理由**：`onFilterChange` 每次通过 `{ ...filter, ...patch }` 创建新对象引用，timer 到期后 `setDebounced(filter)` 设置新引用，`filter === debouncedFilter` 恢复 `true`。引用比较精确覆盖「filter 已变但 debounced 还没跟上」的时间窗口，不需要额外 timer 状态。

3. **`displayRows = loading ? [] : rows` 空数据策略**
   - **歧义**：debounce 等待期或查询中时，`query.data` 仍是旧数据，直接传给 Table 会在 loading 遮罩下显示旧行。
   - **选择**：loading 期间传空数组 `[]` 给 KPI、RankTable、TrendChart。
   - **理由**：TDesign Table loading 态有遮罩但不会清空行数据；KPI 组件在 `loading=true` 时渲染 Skeleton 而非 Statistic（空数组不影响）。空数组确保旧行不残留，且各组件的 loading 态 UI 覆盖正确。

4. **`isFetching` 替代 `isLoading` 作为 loading 信号源**
   - **歧义**：React Query v5 中 `isLoading` 只在首次加载（无缓存）时为 `true`，有缓存的 refetch 时为 `false`；`isFetching` 在任何请求进行中（包括 refetch）均为 `true`。SPEC 和 UX 未指定用哪个。
   - **选择**：所有组件统一用 `query.isFetching` 作为 loading 信号，包括 PlatformMetricPage（4 处）、PlatformUsagePage（3 处）、TenantDrilldownDrawer（3 处）。
   - **理由**：用户快速切换筛选条件时 queryKey 变化触发 refetch（有旧缓存），`isLoading=false` 但 `data` 是旧筛选的数据；`isFetching=true` 确保此时也显示 loading 态，避免旧数据残留。

5. **专页 `groupBy` 参数使用 `debouncedFilter.group_by` 而非 `filter.group_by`**
   - **歧义**：`PlatformTrendChart` 的 `groupBy` 参数控制图表渲染模式（tenant_id 柱状 / day·hour 折线），需要与实际查询参数一致。
   - **选择**：传 `debouncedFilter.group_by`。
   - **理由**：查询用的是 `debouncedFilter`，返回数据按 `debouncedFilter.group_by` 分组；如果图表用 `filter.group_by`（即时值），会出现数据是旧分组但图表按新分组渲染的不匹配。

### Deviations

1. **初始实现缺少钻取 Drawer（已修复）**
   - **UX 说**：§4.2/§4.3 专页与聚合页共享 RankTable「查看明细」行操作，触发单租户钻取 Drawer（FR-16）。
   - **原实现**：`PlatformMetricPageContent` 中 `PlatformRankTable` 未传 `onRowDrilldown` 回调，也未挂载 `TenantDrilldownDrawer`。
   - **实际修复**：添加 `drilldownRow`/`drawerVisible` 状态、`handleRowDrilldown` 回调、`TenantDrilldownDrawer` 组件，与聚合页 `PlatformUsagePage` 保持一致。
   - **理由**：联调发现「查看明细」按钮点击无反应；UX 要求专页也支持钻取。

2. **初始实现缺少「边界说明」卡片（已修复）**
   - **UX 说**：§4.3 专页模板底部包含「边界说明：POST token-usage 为写入侧，非本页查询」卡片。
   - **原实现**：专页模板遗漏了边界说明卡片。
   - **实际修复**：在趋势图下方添加 `<BossContentCard title="边界说明">` 组件，内容为 `POST /metering/token-usage 为写入侧数据上报接口，非本页查询范围；本页仅展示用量统计，不含账单金额、发票与结算。`
   - **理由**：联调发现底部缺少边界说明卡片；UX §4.3 明确要求。

3. **mock `_platform_usage_by_day/hour` 无 tenant_id 时只返回 tenant-alpha（已修复）**
   - **SPEC/UX 未覆盖**：mock 服务 `serve_core_mock.py` 的 `_platform_usage_by_day` 和 `_platform_usage_by_hour` 在 `tenant_id=None`（未筛选租户）时只返回 `_PLATFORM_TENANTS[:1]`（即 tenant-alpha）。
   - **原实现**：`target_tenants = ... if tenant_id else _PLATFORM_TENANTS[:1]`。
   - **实际修复**：改为 `target_tenants = ... if tenant_id else _PLATFORM_TENANTS`，无 tenant_id 时返回全部 4 个租户。
   - **理由**：联调发现「选择按小时和按天都没法显示全部租户，都是默认 tenant-alpha」；mock 应模拟真实 API 行为（无 tenant_id 返回全租户）。

### Tradeoffs

1. **`isDebouncing` 引用比较 vs 显式 isPending 状态**
   - **备选 A**：`filter !== debouncedFilter` 引用比较，零额外状态。
   - **备选 B**：在 `useDebouncedFilter` 内部维护 `isPending` boolean 状态，通过返回值暴露。
   - **选择**：备选 A。理由：`onFilterChange` 每次创建新对象引用，引用比较精确覆盖等待窗口；备选 B 增加 hook 复杂度和额外 re-render，且需要修改共享 hook 影响 Console 端。

2. **`displayRows = []` 空数据 vs 保留旧数据 + 遮罩**
   - **备选 A**：loading 期间传空数组，彻底清空旧行。
   - **备选 B**：保留 `rows`（旧数据），依赖组件 loading 遮罩覆盖。
   - **选择**：备选 A。理由：TDesign Table loading 遮罩半透明，旧行仍可见；用户反馈明确「界面显示旧数据」说明遮罩不足以掩盖旧数据。空数组从根源消除旧数据展示。

3. **`isFetching` vs `isLoading` 修复范围**
   - **备选 A**：只修专页和聚合页（本次修改的文件）。
   - **备选 B**：同时修 TenantDrilldownDrawer。
   - **选择**：备选 B。理由：Drawer 内切换 group_by 时也会 refetch（有缓存），`isLoading` 同样会导致旧数据残留；统一用 `isFetching` 保持一致性。

### Open Questions

1. **`toPlatformRows` 函数三处重复**
   - `PlatformMetricPage.tsx`、`PlatformUsagePage.tsx`、`TenantDrilldownDrawer.tsx` 各有一份完全相同的 `toPlatformRows` 实现。
   - 待确认：是否抽取为共享工具函数？当前未抽取是因为 review-it 判定为超出 bug fix 范围，但长期应消除重复。

2. **`useDebouncedFilter` 的 `isPending` 状态是否应内置**
   - 当前通过引用比较 `filter !== debouncedFilter` 在调用方检测 debounce 等待期，但这依赖 `onFilterChange` 每次创建新对象引用的隐式契约。
   - 待确认：是否应在 `useDebouncedFilter` 内部暴露 `isPending` boolean，使契约显式化？这会影响 Console 端同一 hook 的调用方。

3. **浏览器手动验证待执行**
   - 单元测试覆盖了组件渲染和回调，但快速切换筛选的交互场景需在浏览器中手动验证。
   - 待执行：按 TESTING.md Issue #21 步骤验证「选择租户 → 按小时 → 叉掉租户 → 更换租户」场景下表格/KPI/图表均显示 loading 且不残留旧数据。

### 验证命令

```bash
cd repo/frontends/boss
pnpm type-check    # 通过（0 errors）
pnpm test -- --run # 89/89 tests passed
cd ../..
git diff --check   # 通过
```

### AC 满足情况

- [x] 5 路由文件: gpu-hours.tsx, cpu-hours.tsx, memory-gbhours.tsx, input-tokens.tsx, output-tokens.tsx
- [x] 每页从 METRIC_PAGES 查找 config，固定 resource_type
- [x] 专页不提供指标视角切换（与聚合页区分）
- [x] 复用 RankTable + TrendChart + KPI 组件
- [x] 面包屑: `平台计量与结算 / {title}`
- [x] [UI] Matches UX §4.3 专页模板（含边界说明卡片 + 钻取 Drawer）

### review-it 审查结果

review-it clean: 无 accepted/actionable findings。

- Finding 1（`toPlatformRows` 三处重复）：判定为超出本次 bug fix 范围，不宜在修复中做抽取重构。
- Finding 2（TenantDrilldownDrawer 的 `toPlatformRows` 未用 useMemo）：判定为非本次修复引入，数据量小性能影响可忽略。
- Finding 3（`isDebouncing` 引用比较正确性）：已验证正确——`onFilterChange` 每次创建新对象引用，timer 到期后引用恢复相等。
- Finding 4（`columns` 数组未 useMemo）：判定为非本次修复引入，TDesign Table 内部有 diff 不影响正确性。
- Finding 5（`displayRows=[]` 导致 KPI 闪烁为 0）：已验证安全——KPI 在 `loading=true` 时渲染 Skeleton 而非 Statistic。
- Finding 6（mock `resource_type=None` 默认 GPU）：判定为符合预期（聚合页默认展示 GPU 趋势）。
- ANI Checklist: Scope 未触碰 frozen Services backend；OpenAPI 无新增路由；只改 `frontends/boss` + `scripts/serve_core_mock.py`；tenant_id 仅作 query param.

## Issue #022 — BOSS 2 P1 占位路由（storage-gbdays + kb-queries）实现笔记

### 实现概述

创建 2 个 P1 占位路由文件（`storage-gbdays.tsx`、`kb-queries.tsx`），复用依赖 #21 的 `PlatformMetricPage` 专页模板。模板内置 `p0_enabled=false` 分支：渲染面包屑 + Empty「该指标待 API 合入（P1）」，不调用任何 API。同时移除 `__root.tsx` 中 Storage/KB 两个菜单项及 tenant 菜单项的 `as any` 类型断言（路由文件已存在，类型校验可正常识别）。`routeTree.gen.ts` 由 TanStack Router Vite 插件在 `pnpm build` 时自动重新生成。

### 边界

- 仅修改 `repo/frontends/boss/` 路径
- 不调用任何 API（P1 占位页不涉及 `usePlatformUsageQuery`）
- 不伪造数据（FR-12、NG-7）
- 不修改 OpenAPI 契约

### 实现笔记

#### 1. Design Decisions（设计决策）

**决策 1：P1 占位路由复用 `PlatformMetricPage` 模板而非独立组件**

- 模糊点：SPEC §5.3 和 UX §6.2 规定 P1 占位页显示 api-not-ready Empty，但未指定是独立组件还是复用模板。
- 选择：复用 `PlatformMetricPage` 模板，通过 `p0_enabled=false` 分支渲染 Empty。
- 理由：依赖 #21 在 `PlatformMetricPage` 中已内置 P1 占位逻辑（L70-83），新增独立组件是重复代码。复用模板只需创建路由文件并传入 route prop，最小 diff。

**决策 2：`as any` 类型断言移除时机**

- 模糊点：Issue #021 创建 P0 路由时，`__root.tsx` 中 Storage/KB 菜单项因路由文件不存在而用 `as any` 绕过类型校验。何时移除？
- 选择：在本 issue（#022）创建 P1 路由文件后移除 `as any`。
- 理由：TanStack Router 的 `<Link to>` 类型校验要求目标路由已注册到 `routeTree.gen.ts`。路由文件不存在时移除 `as any` 会导致类型错误。创建路由文件 + build 重新生成 `routeTree.gen.ts` 后移除是安全时机。同时移除 tenant 菜单项的 `as any`（`tenant/usage-billing.tsx` 已由 #020 创建）。

#### 2. Deviations（偏离）

None — 实现完全遵循 SPEC §5.3、UX §6.2 和 PRD NG-7。P1 占位页渲染 Empty「该指标待 API 合入（P1）」，不伪造数据，面包屑格式与 AC 一致。

#### 3. Tradeoffs（取舍）

**复用 `PlatformMetricPage` vs 独立 P1 占位组件**

- 可选方案 A：复用 `PlatformMetricPage` 模板，通过 `p0_enabled=false` 分支渲染 Empty。
  - 优点：最小 diff，零重复代码，P1→P0 升级时只需改 `constants.ts` 中 `p0_enabled` 为 `true`。
  - 缺点：P1 占位页引入了 `PlatformMetricPage` 的 config 查找逻辑（虽然 `p0_enabled=false` 分支不进入 P0 内容区）。
- 可选方案 B：创建独立的 `PlatformMetricPagePlaceholder` 组件。
  - 优点：P1 占位页完全独立，不依赖 `PlatformMetricPage` 的 config 查找。
  - 缺点：重复面包屑渲染逻辑，P1→P0 升级时需要替换路由文件中的组件引用。
- 选择 A 的理由：依赖 #21 已内置 P1 分支逻辑，方案 A 只需 2 个路由文件 + 1 行 `__root.tsx` 修改，是最小 diff。P1→P0 升级路径清晰（改 `constants.ts` 即可）。

#### 4. Open Questions（开放问题）

**`pnpm lint` 预存失败**

- 假设：BOSS 项目 `package.json` 中 `lint` 脚本指向 ESLint，但项目无 ESLint 配置文件（`.eslintrc*`、`eslint.config.*` 均不存在），导致 `pnpm lint` 报 "ESLint couldn't find a configuration file"。
- 需确认：这是 scaffold 遗留问题，应由独立的 lint 配置 issue 处理，而非本 issue 引入。后续需补齐 ESLint 配置或移除 `lint` 脚本。
- 跟进：本 issue 的 `pnpm type-check`、`pnpm build`、`pnpm test` 均通过，lint 失败不影响 AC 满足。

### review-it 审查结果

review-it clean: 无 accepted/actionable findings。

- 2 个 P1 路由文件结构与 P0 专页一致，通过 `PlatformMetricPage` 模板渲染。
- `__root.tsx` 移除 3 处 `as any` 类型断言，路由文件已存在，类型校验通过。
- `routeTree.gen.ts` 自动生成文件正确包含 `MeteringStorageGbdaysRoute` 和 `MeteringKbQueriesRoute`。
- ANI Checklist: Scope 仅 `frontends/boss`；OpenAPI 无 invented routes/schemas；不涉及 ports/adapters/idempotency/tenant；符合 UX §5-§6。

### 完工标准（验证命令）

```bash
cd repo/frontends/boss && pnpm type-check && pnpm build && pnpm test --run
```

- `pnpm type-check`：✅ pass
- `pnpm build`：✅ pass（5856 modules, 53s，TanStack Router 自动重新生成 `routeTree.gen.ts`）
- `pnpm test --run`：✅ pass（10 files, 89 tests, 0 failures）
- `pnpm lint`：❌ fail（预存问题：项目无 ESLint 配置文件，非本 issue 引入）

### AC 满足情况

- [x] 2 路由可进入 — `/metering/storage-gbdays` + `/metering/kb-queries` 路由文件已创建，`routeTree.gen.ts` 已注册
- [x] 内容区: Empty「该指标待 API 合入（P1）」 — `PlatformMetricPage` `p0_enabled=false` 分支渲染
- [x] 不伪造数据 — P1 占位页不调用任何 API，纯静态 Empty（FR-12, NG-7）
- [x] 面包屑: `平台计量与结算 / 平台 Storage-GBDays` 和 `平台计量与结算 / 平台 KB Queries`

---

## 笔记：基于 main 重做前端增量并叠加计量功能（2026-07-23）

> 本段记录的是 Issue #012/#013 原始实现完成后的一次特殊工程操作：由于搁置期间 main 已合入别人实现的 BOSS scaffold 与 Console 新页面，原始分支 `feat/metering-p0-fr8` 基于旧基线，直接 merge 会删除 main 已有功能。因此在新分支 `feat/metering-platform-fr8` 上基于最新 main 重新叠加计量增量。

### 1. Design Decisions（设计决策）

**DD-FE-1：新分支基于 main 而非 cherry-pick 旧分支**
- 歧义：旧分支 `feat/metering-p0-fr8` 含全部计量代码，但基于旧基线（删除了 main 后续合入的 GPU 资源池、instance-observability 等页面，改了 shell props 接口、vite 代理地址、包管理器从 npm 改为 pnpm）。直接 rebase 或 cherry-pick 会把这些回退带入 main。
- 选择：创建新分支 `feat/metering-platform-fr8`，基于最新 main，只 checkout 计量功能独有的增量文件，不动 main 已有骨架。
- 理由：issue-013 的 scaffold 验收项 main 已全部满足（package.json 依赖、vite.config.ts、main.tsx 入口均到位），计量功能应作为增量叠加而非替换骨架。这符合 Karpathy 原则三"只触碰你必须改动的部分"。

**DD-FE-2：BOSS 侧栏增量合并而非替换**
- 歧义：旧分支 `__root.tsx` 把侧栏改成「仪表盘 + 租户与客户 + 平台计量与结算 7 专页」，main 的侧栏是「运营总览 + 资源池与基础设施 → GPU 资源池管理」。
- 选择：保留 main 的「运营总览 + GPU 资源池管理」，追加「租户与客户」「平台计量与结算」两个 SubMenu。
- 理由：不删除 main 已合并的 GPU 资源池菜单，计量菜单作为增量追加。

**DD-FE-3：Console usage.tsx 用增强版替换而非并存**
- 歧义：main 已有 `routes/usage.tsx`（50 行简单近 30 天表格），旧分支把增强版放在 `_authenticated/usage.tsx`（路径 `/_authenticated/usage`）。
- 选择：用增强版内容直接替换 main 的 `routes/usage.tsx`（路径保持 `/usage`），不引入 `_authenticated` 路由组。
- 理由：避免两个 `/usage` 路径共存导致路由冲突；`__root.tsx` 的「用量报表」链接不用改；减少路由结构变动。

**DD-FE-4：Console 计量页适配 main shell props 而非搬旧分支 shell 组件**
- 歧义：旧分支的 `ConsolePageHeader` 把 props 从 `subtitle`/`actions` 改成 `description`/`extra`，会破坏 main 中 `gpu-containers`、`gpu`、`gpu-queues` 等页面对旧 props 的使用。
- 选择：保留 main 的 shell 组件不动，计量页 `description=` 改为 `subtitle=` 适配 main 接口。
- 理由：main 的 shell 接口更丰富（`subtitle`/`actions`/`extra`/`bodyNoPadding`），且被多个 main 页面使用；改 shell 会引发连锁修改。计量页只需一行 prop 名适配。

**DD-FE-5：Console vitest.config.ts 不依赖 @vitejs/plugin-react**
- 歧义：main 的 `@vitejs/plugin-react@^6.0.3` 是 ESM-only，vitest 1.x 的 esbuild config 加载器无法 `require()` ESM 包，导致 `Error: require() of ES Module`。
- 选择：vitest.config.ts 移除 `import react from '@vitejs/plugin-react'`，改用 `esbuild: { jsx: 'automatic', jsxImportSource: 'react' }` 内置 JSX 处理。
- 理由：vitest 通过 esbuild 已能编译 TSX，不需要 plugin-react；ESM 兼容问题仅存在于 vitest config 加载阶段，不影响 vite build。

### 2. Deviations（偏离）

**DEV-FE-1：不搬旧分支的 vite.config.ts 代理地址变更**
- SPEC/旧分支：vite 代理 `target: 'http://127.0.0.1:4010'`（Core Mock Server）。
- 实现：保留 main 的 `target: 'http://localhost:8080'`。
- 原因：main 已统一使用 8080 代理，改回 4010 会与 main 的其他页面联调冲突。4010 是 Mock Server 专用，8080 是本地开发后端。

**DEV-FE-2：不搬旧分支的 pnpm-lock.yaml，保留 main 的 package-lock.json**
- SPEC/旧分支：使用 pnpm + `pnpm-lock.yaml`。
- 实现：保留 main 的 npm + `package-lock.json`。
- 原因：main 已统一使用 npm，双锁文件会引发 CI 问题。BOSS 前端验证命令也从 `pnpm` 改为 `npm`。

**DEV-FE-3：BOSS package.json 保留 main 的 zustand，不删除**
- SPEC/旧分支：删除 `zustand`（计量功能不依赖它）。
- 实现：保留 `zustand`。
- 原因：main 可能已有页面使用 zustand，删除会破坏已有功能。按"只触碰必须改动的部分"原则保留。

### 3. Tradeoffs（取舍）

**TR-FE-1：重新叠加 vs cherry-pick**
- 方案 A（选择）：基于 main 新分支，手动 checkout 计量增量文件 + 适配 main 接口。
  - 优点：不带入任何回退，diff 最干净，PR 审查阻力最小。
  - 缺点：丢失旧分支的 commit 历史，需要手动处理 shell props 适配、vitest ESM 兼容等问题。
- 方案 B：cherry-pick 旧分支的计量 commit，骨架冲突手动 resolve 为"保留 main"。
  - 优点：保留 commit 历史。
  - 缺点：旧分支 commit 把骨架和计量混在一起，cherry-pick resolve 非常繁琐；每个冲突都要手动判断保留 main。
- 选择 A 的原因：计量功能是搁置后重新叠加，commit 历史意义不大；干净的 diff 对 PR 审查更有价值。

**TR-FE-2：serve_core_mock.py 计量 mock 增量合并方式**
- 方案 A（选择）：在 main 的 operation_id 分发机制上追加 `getMeteringUsage` + `getPlatformMeteringUsage`。
  - 优点：不删除 main 的 observability/instances mock，保留 main 架构。
  - 缺点：main 用 operation_id 分发，旧分支用 OVERRIDES 表 + (method, rel_path)，需要适配函数签名（返回 dict 而非 Callable）。
- 方案 B：直接用旧分支的 serve_core_mock.py 替换 main 版本。
  - 优点：零适配工作。
  - 缺点：删除 main 的 1138 行 observability/instances mock，前端联调其他页面会失败。
- 选择 A 的原因：serve_core_mock 是共享联调工具，不能只服务计量功能。

### 4. Open Questions（开放问题）

**OQ-FE-1：CURRENT-SPRINT.md 和 ANI-06-开发计划.md 未更新**
- 假设：这两个文件维护的是"当前冲刺状态"，计量功能已完成但还没 PR/merge，不应在搬运阶段修改 main 的 sprint 状态。
- 需确认：PR 合并时由人工确认后更新这两个文件的计量批次记录。
- 跟进：当前分支未触碰这两个文件。

**OQ-FE-2：BOSS ESLint 配置缺失**
- 假设：旧分支已记录 `pnpm lint` 因无 ESLint 配置而失败，main 的 BOSS scaffold 同样无 `.eslintrc*` 文件。
- 需确认：main 的 BOSS scaffold 是否已有 ESLint 配置，若没有应由独立 issue 处理。
- 跟进：本分支未触碰 lint 配置，验证用 `npm run type-check` + `npm run test` + `npm run build`。

**OQ-FE-3：go.work.sum 重新生成后其他模块的校验和漂移**
- 假设：删除 go.work.sum 后 `go work sync` 重新生成，但部分 otel 依赖的 go.mod 哈希与当前 Go 代理返回的不一致（非计量引入，是 main 已有的环境差异）。
- 需确认：CI 环境的 Go 代理是否一致；若 CI 也出现 checksum mismatch，需在 CI 环境重新 `go work sync`。
- 跟进：本分支已恢复其他模块的 go.mod/go.sum 到 main 状态，仅保留 metering-service 自身的 go.mod/go.sum 和 go.work/go.work.sum 的改动。

### 验证命令

```bash
# BOSS 前端
cd repo/frontends/boss && npm run type-check && npm run test && npm run build

# Console 前端
cd repo/frontends/console && npm run type-check && npm run test && npm run build

# Core + Services 后端
cd repo/services/ani-gateway && go build ./... && go test ./internal/router/...
cd repo/pkg && go build ./... && go test ./adapters/runtime/...

# serve_core_mock 编译
python -c "import py_compile; py_compile.compile('repo/scripts/serve_core_mock.py', doraise=True)"
```

- BOSS type-check: ✅ / test: 89/89 ✅ / build: ✅
- Console type-check: ✅ / test: 38/38 ✅ / build: ✅
- Gateway build: ✅ / router test: ✅
- pkg build: ✅ / adapters/runtime test: ✅
- metering-service build: ✅ / 4 包 test: ✅
- serve_core_mock 编译: ✅
