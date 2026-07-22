# Console GPU 清单页面（spec-console-gpu-inventory-ui）

> 批次 ID: CONSOLE-GPU-INVENTORY-UI-A
> 产品线: Console (Line U)
> 类型: 练手批次（AI 开发闭环演练，不 commit 到主线）
> 日期: 2026-07-02
> 开发者: 董嘉明

## 产品计划映射

- PRD: `repo/services/tasks/modules/prd/console/compute/prd-console-gpu-inventory-ui.md`
- SPEC: `repo/services/tasks/modules/spec/console/compute/spec-console-gpu-inventory-ui.md`
- Core API: `repo/api/openapi/v1.yaml` operationId `listGPUInventory`（Sprint 12 已闭合 handler，Sprint 13 S04 已通过 production-shaped live gate）

## 实现范围

新增 Console GPU 设备清单只读页面，对应 spec §3 的"设备列表筛选"范围。

### 修改文件

| 文件 | 变更 |
|---|---|
| `repo/frontends/console/src/routes/gpu-inventory.tsx` | 新建：设备列表 + GPU 型号/状态筛选 |
| `repo/frontends/console/src/routes/__root.tsx` | 新增 GPU 清单菜单入口（CpuIcon） |
| `repo/frontends/console/src/routeTree.gen.ts` | TanStack Router 自动生成（新增 `/gpu-inventory` 路由） |

### 非目标（本次未做）

- 占用摘要卡片（spec §3 提及，本次范围仅列表 + 筛选）
- 实例详情跳转（spec §3 提及跳转 kind=gpu_container 实例详情，但实例详情页尚未落地）
- GPU 分配/回收写操作（spec §3 明确 Non-Goals，OpenAPI 未声明）

## 验证命令与结果

| 命令 | 结果 |
|---|---|
| `npx tsc --noEmit` | ✅ 全绿（0 errors） |
| `npx vite build` | ✅ 成功（5257 modules transformed, built in 3m51s） |
| `npx pnpm lint` | ❌ 失败（项目缺失 eslint 配置文件，属项目已有问题，非本批次引入） |
| `make validate-architecture` | ⏭ 跳过（纯前端改动，本机未配 Go 环境） |

## Design Decisions

### 1. 筛选选项从已加载数据派生，而非额外请求

- **Ambiguity**: spec §3 只说"设备列表筛选（vendor、state）"，未说明筛选选项来源。
- **Choice**: GPU 型号选项从首次 `/gpu-inventory` 响应数据中派生（`useMemo` + `Set`），不额外发请求。
- **Rationale**: 避免为筛选选项新增 API 调用；GPU 型号集合通常不大，从当前页数据派生足够。状态筛选用 OpenAPI 枚举硬编码。

### 2. 状态映射用 OpenAPI 枚举，不按 spec §2.3 的字段名

- **Ambiguity**: spec §2.3 写 `state: available / allocated / unavailable / maintenance`，但 OpenAPI `v1.yaml` 和生成的 `core-schema.d.ts` 是 `status: "available" | "in_use" | "fault" | "maintenance"`。
- **Choice**: 以 OpenAPI 为准，用 `status` 字段名和 `available/in_use/fault/maintenance` 枚举值。
- **Rationale**: CLAUDE.md 第 4 节明确"OpenAPI 契约是唯一真实来源"。spec 文档与 OpenAPI 不一致属 spec 侧问题。

### 3. Select onChange 用 SelectValue 类型适配

- **Ambiguity**: TDesign Select 的 onChange 签名是 `(value: SelectValue, context) => void`，`SelectValue` 是 `string | number | object`，与 `useState<string>` 的 setter 不直接兼容。
- **Choice**: 用 `(val: SelectValue) => setGpuType((val as string) ?? '')` 做类型转换。
- **Rationale**: TDesign 类型系统约束，`as string` 断言是 TDesign + TS 项目的标准做法。

## Deviations

### 1. spec §3 "跳转 kind=gpu_container 实例详情"未实现

- **Spec 说**: 点击可跳转到 kind=gpu_container 实例详情。
- **实现**: 归属实例列只读展示 instance_id 文本，不做跳转。
- **原因**: `/instances/$instanceId` 路由页面尚未落地，建链接会指向不存在的页面并破坏 build。符合 Karpathy 原则二（不为不存在的功能建链接）。待实例详情页落地后补跳转。

## Tradeoffs

### 1. gpu_type 筛选用 query 参数 vs 前端过滤

- **备选 A**: 用 OpenAPI query 参数 `gpu_type`/`status` 传给后端筛选（本次选择）。
- **备选 B**: 前端拿到全量数据后本地过滤。
- **选择 A 的理由**: 后端筛选支持分页，数据量大时不会把全部设备拉到前端；且 OpenAPI 已声明这两个 query 参数，用后端筛选是契约预期用法。
- **A 的缺点**: 每次筛选变化都触发新请求；但 TanStack Query 会缓存相同 queryKey 的结果。

## Open Questions

1. **spec §2.3 与 OpenAPI 字段不一致**: spec 写 `state/allocated/unavailable`，OpenAPI 是 `status/in_use/fault`。建议后续订正 spec 文档对齐 OpenAPI（CLAUDE.md 规则：OpenAPI 为准）。
2. **占用摘要卡片**: spec §3 提及占用摘要卡片（`/gpu-inventory/occupancy` API），本次未做。后续可作为独立小批次补齐。
3. **实例详情跳转**: 待 `/instances/$instanceId` 页面落地后，需回来补 `Link` 跳转。
4. **eslint 配置缺失**: `pnpm lint` 因项目无 eslint 配置文件而失败。这是 `repo/frontends/console/` 的已有问题，建议 Services 团队补 eslint 配置。

## 环境备注

- 本机 `gh` CLI 已装但 PATH 未自动刷新，需用全路径 `C:\Program Files\GitHub CLI\gh.exe`。
- 本机 `pnpm` 通过 `npm install -g pnpm` 安装，PATH 未刷新时用 `npx pnpm` 调用。
- `pnpm install` 需先 `pnpm approve-builds esbuild` 才能跑 `type-check`（pnpm 安全策略）。
- `routeTree.gen.ts` 由 `@tanstack/router-plugin/vite` 在 `vite build` 时自动生成。
