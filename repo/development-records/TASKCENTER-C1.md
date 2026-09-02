# TASKCENTER-C1 — 任务中心异步任务 Core 集成 · 契约批次

完成日期：2026-08-27
对应 Sprint：Sprint 13（并行功能流：任务中心异步任务 Core 集成）
分支：`feat/async-task-core-integration`
方案依据：仓库根目录 `async-task-core-integration-migration.md`（§6 阶段 A / §8.1）
验证结果：`make test` 等价拆解全通（validate-architecture / validate-auth-contract / validate-gateway-authz 四项 / go test 全包 / python compileall）；`python scripts/validate_core_api_compatibility.py` 通过（基线再生成后）；`api/openapi/v1.yaml` 通过 openapi_spec_validator；`git diff --check` 通过。本地 Windows 环境存量失败：`pkg/adapters/runtime` 两个 sandbox file script 测试（`TestSandboxFileScriptsRejectSymlinks` / `TestSandboxFileScriptsAllowWorkspaceOperations`）因 os.Symlink 特权与 `os.O_DIRECTORY` 缺失失败，已在 pristine HEAD worktree 复现确认为环境问题，非本批引入。

## 实现了什么

纯契约批次，不改任何运行时行为：AsyncTask 契约扩展 5 种 `instance.*` task_type 与 `instance` resource_type 并写入真进度语义（running 起步 / GET 单查懒同步 / 状态阶梯 / list 快照语义 / 实例 state→任务推进映射表）；新增 `GET /tasks` 列表契约（cursor 分页 + 筛选 + `x-ani-authz` + 401/403）；为既有 `GET /tasks/{task_id}` 补齐 operationId/security/`x-ani-authz`/`x-ani-rbac-scope` 与 401/403；新增 `TaskListResponse` schema；鉴权注册表两条 tasks 路由翻转为 generated policy（pilot 严格集合未扩，运行时行为零变化）；同步 Core SDK（4 语言）、API docs 与 Console core-schema 生成物；补齐存量契约缺口 `sandbox.checkpoint.restore`；Core API v1 兼容基线有意再生成。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | task_type enum +5 `instance.*`（不含 `inference.*`）；resource_type enum +`instance`；AsyncTask schema description 写入 §3.3 进度语义与 §5.2 映射表；新增 `TaskListResponse`；新增 `GET /tasks`（limit 1-100 默认 20 / cursor / status enum 筛选 / task_type、resource_type 前向兼容筛选，均含 `x-ani-authz` 五字段、`x-ani-rbac-scope: scope:tasks:read`、401/403）；`GET /tasks/{task_id}` 补 operationId=getTask、security、authz、rbac scope、401/403；补齐 `sandbox.checkpoint.restore`（存量缺口，`instances.go` 已在写该 task_type） |
| `api/core-v1-compatibility-baseline.yaml` | 修改 | `make gen-core-api-compat-baseline` 有意再生成：`GET /tasks/{task_id}` 补 operationId/rbac_scope 触发 changed-operationId 禁止项，必须再基线；再生成同时吸收 #113-#118 期间未再基线的 additive 漂移（旧基线本可通过校验，证明均为允许变更） |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改 | 生成物（`make gen-gateway-authz`，禁止手改）：`GET /api/v1/tasks` 新增 generated policy（OperationID=listAsyncTasks）；`GET /api/v1/tasks/{task_id}` 由 legacy 翻转为 generated（OperationID=getTask）；`functionalMVPPilotOperations` 未扩，仍维持 `{listQuotaMeta}` |
| `sdks/core/{go,java,python,typescript}` | 修改 | 生成物：新增 listAsyncTasks 客户端方法与 TaskListResponse 类型 |
| `docs/api/core.html`、`docs/api/index.html` | 修改 | 生成物：静态 API 文档同步 |
| `frontends/console/src/api/core-schema.d.ts` | 修改 | 生成物：Console Core API 类型同步（AsyncTask enum 扩展、TaskListResponse、listAsyncTasks） |
| `services/docs/console-modules/alerts/async-task-center.md` | 修改 | 任务中心页面文档的本方案与延后方案引用注记（开工前已存在的关联改动，随本批提交） |

## 完工标准达成（对照方案 §8.1）

- [x] 新增 5 个 `instance.*` task_type 与 `instance` resource_type 进入 enum 且生成物无漂移（生成器重跑 diff 恒定）
- [x] `GET /tasks` 声明齐全，兼容性门禁通过（`validate_core_api_compatibility` 基线再生成后通过；既有 operation 语义不变）
- [x] AsyncTask schema description 含真进度语义（running 起步 / GET 懒同步 / 状态阶梯 / list 快照）
- [x] 两个 tasks operation 均含 `x-ani-authz` 五字段与 401/403 响应声明；registry 中 Source=generated、OperationID 为 listAsyncTasks/getTask（`validate_gateway_authz_drift` no drift；route coverage 282/233/0 error；生成器测试 18 OK）
- [x] 契约不含任何取消操作（无 DELETE / cancel 路径）
- [x] `git diff --check` 与 `make validate-architecture` 通过
- [x] 不改任何运行时行为、不扩 pilot 严格集合、不做取消契约（遵守方案 §6 阶段 A 第 4 条）

## 备注（与方案的偏差，详见差异文档）

1. **兼容基线再生成**（方案未提）：为既有 `GET /tasks/{task_id}` 补 operationId 触发兼容门禁 changed-operationId 禁止项，按 Makefile `gen-core-api-compat-baseline`（仅有意更新时使用）再生成基线并随本批提交。
2. **补齐 `sandbox.checkpoint.restore`**（方案 §6 阶段 A 只要求 5 种 `instance.*`）：`instances.go` sandbox checkpoint restore 已写该 task_type 且测试断言存在，原 enum 缺失属存量契约缺口，additive 补齐。
3. **本地 `make` 包装层问题**：make 目标末尾的沙箱重定向噪音导致包装层 exit code 非 0（校验脚本本身全过），验收按等价拆解逐条执行；`validate-openapi-spec` 依赖 `rpds` 模块本地缺失，装到临时目录后验证 spec 通过。
4. 后续批次 `TASKCENTER-A1`（list + 实例真进度懒同步）按方案 §6 阶段 B 执行。
