# PR-M6 — Metering 查询 PG Adapter（V3 方案）实现

完成日期：2026-08-26
对应 Sprint：以 repo/CURRENT-SPRINT.md 为准
批次类型：Feature batch（计量查询落库读取：ports 扩展 + PgMeteringService + 平台查询端点 + pilot 鉴权接入）
依赖批次：PR-M5（metering 采集写入侧）；鉴权四批次（V2 pilot 链路）

> **说明：** 本文件记录 `plan-metering-query-pg-adapter-v3.md`（V3，含 14 项修订）的实现笔记。实现依据以 V3 为准，V2 与原方案仅作对照基线。详细差异清单见任务区 `implementation-diff-metering-query-pg-adapter.md`（未跟踪目录），本文件为其合规摘要（不含内网连接信息）。
> 提交状态：4 个 commit 在 `feat/metering-query-interface-v2` 分支本地待 push（150c203 / 188cf06 / efef54c / 073b1de）。

---

## 实现了什么

计量查询从内存 LocalMeteringService 扩展为可落 PG 读取：

1. **契约先行**：`repo/api/openapi/v1.yaml` 租户侧 `group_by` 枚举移除不支持的 `az`；平台端点 `tenant_id` 参数补 `format: uuid`；两个 metering 端点补 503 响应声明；`getPlatformMeteringUsage` 增加 `x-ani-authz` 扩展（resource=metering, action=read, boundary=platform, principal_kinds=[user]）。
2. **ports 扩展**：`MeteringService` 新增 `QueryPlatformUsage`；`MeteringUsageQueryRequest` 新增 `PlatformTenantID`。
3. **PgMeteringService**（`pkg/adapters/runtime/pg_metering_service.go` 新增）：租户视角走 `WithTenantTx` 依赖 RLS 过滤（SQL 无显式 tenant_id WHERE）；平台视角 `WithPlatformTx` 内 `SET LOCAL ROLE ani_metering_writer`（BYPASSRLS 角色，事务级自动重置）跨租户聚合；SQL 固定输出列（租户 4 列 / 平台 5 列），period 无时间聚合时 `NULL::text` 占位；`ReportTokenUsage` 委托内部 LocalMeteringService（token 内存写入不变）。
4. **写入侧 period 统一 UTC**：`collectors.go` 与 `metering_collection_service.go` 的 period 生成改为 `time.Now().UTC().Format("2006-01-02T15:04")`（评审 #4 配套，写入/查询时区一致）。
5. **Gateway 装配**（`services/ani-gateway/metering_runtime.go` 新增）：按 `METERING_PROVIDER_MODE` 装配（""/local → LocalMeteringService；postgres → `bootstrap.ConnectMetadataStore` 新建连接池 + Ping 校验，失败阻止启动不静默降级；未知值 ErrUnsupported 同样阻止启动）。
6. **平台查询 handler**：`router/metering_resources.go` 新增 `queryPlatformUsage`（tenant_id/resource_type/group_by 校验、503 METERING_UNAVAILABLE 错误映射、`newMeteringAPI` 支持注入）。
7. **pilot 鉴权接入**：`getPlatformMeteringUsage` 加入 pilot allowlist（方案 B）并重生成 `zz_generated_core_policies.go`；未扩展 `scopeAllowedForPath` 手工前缀清单。
8. **前端同步**：Console `constants.ts`/`constants.test.ts` 分组选项移除 az；`core-schema.d.ts` 重生成。

### 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | group_by 枚举收窄 + x-ani-authz + 503 + tenant_id format |
| `pkg/ports/metering.go` | 修改 | QueryPlatformUsage + PlatformTenantID |
| `pkg/adapters/runtime/pg_metering_service.go` | 新增 | PG 查询 adapter（租户 RLS / 平台 BYPASSRLS） |
| `pkg/adapters/runtime/pg_metering_service_test.go` | 新增 | 11 用例（聚合/过滤/RLS bypass/错误处理） |
| `pkg/adapters/metering/collectors.go` | 修改 | period UTC |
| `services/metering-service/internal/service/metering_collection_service.go` | 修改 | period UTC |
| `services/ani-gateway/metering_runtime.go` | 新增 | METERING_PROVIDER_MODE 装配 |
| `services/ani-gateway/internal/router/metering_resources.go` | 修改 | 平台查询 handler + 注入 |
| `services/ani-gateway/internal/authz/mode.go` + `zz_generated_core_policies.go` | 修改 | pilot allowlist 扩展 + 生成物 |
| `frontends/console/src/features/usage/constants.ts` 等 | 修改 | 分组选项移除 az |

---

## Design Decisions

1. **租户视角不写显式 tenant_id WHERE，完全依赖 RLS**
   - 模糊性：V3 方案既定设计，SQL 层如何过滤租户未逐条展开。
   - 选择：租户查询走 `WithTenantTx`（设置 `app.current_tenant_id`），由 RLS 策略过滤，SQL 不重复加 WHERE。
   - 理由：与平台其他租户隔离机制一致，避免两套过滤逻辑漂移；配套硬约束是连接用户必须非 superuser（见 Open Questions #1）。

2. **平台视角用 `SET LOCAL ROLE ani_metering_writer`（复用既有 BYPASSRLS 角色）**
   - 选择：事务内切换角色而非新建 migration/角色。
   - 理由：`SET LOCAL` 随 commit/rollback 自动重置，无连接池角色泄漏风险；复用 metering 写入侧既有角色，零新增 DDL。

3. **postgres 模式装配失败即阻止 Gateway 启动（不静默降级 local）**
   - 模糊性：方案 §3.7 要求明确，但降级行为易被实现成 warn+fallback。
   - 选择：DATABASE_URL 缺失或 Ping 失败返回错误，启动失败。
   - 理由：静默降级会让计量查询读到空内存数据却返回 200，属数据正确性事故；fail-fast 是唯一安全行为。

4. **period 时间过滤用 `to_char AT TIME ZONE 'UTC'` 与写入侧分钟对齐字符串直接比较**
   - 选择：`period >= to_char($n::timestamptz AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI')` 字符串闭区间比较。
   - 理由：period 是分钟对齐文本列，字符串序即时间序（UTC 契约下）；避免在查询侧做时区换算引入两次转换误差。

---

## Deviations

1. **to_char 格式串 `T` 双引号转义（对方案 SQL 示例的修正）**
   - 方案原文：`'YYYY-MM-DDTHH24:MI'`（T 未转义）。
   - 实际实现：`'YYYY-MM-DD"T"HH24:MI'`（pg_metering_service.go）。
   - 原因：PostgreSQL to_char 中 `DDTHH24` 的 `TH` 被解析为 DD 的序数后缀，HH24 失效，真实 PG 实测查询恒无结果。属方案 bug 修复，已用单测固化断言。

2. **Windows 下 `make gen-gateway-authz` 不可用**
   - 方案要求：make target 重新生成 authz 注册表。
   - 实际：target 内嵌 Unix `date` 语法在 PowerShell 报错，改为直接运行 `python scripts/generate_gateway_authz.py` + gofmt，生成物等价（drift 校验通过）。

3. **测试 fake Scan 扩展支持 `**string`**
   - 方案测试清单未提及 fake 细节；period 可空列扫描目标为 `*string`，Scan 传入 `**string`，既有 fake 不支持导致测试失败。扩展 `meteringFakeRow.Scan` 处理 `*string`/`string`/`nil`。

4. **routeTree.gen.ts 伴随再生成（不在方案文件清单）**
   - 前端 dev server 期间 TanStack Router 插件自动重排 import 顺序，无路由增删改，随批次提交保持工作区干净。

---

## Tradeoffs

1. **计量连接池：新建（bootstrap.ConnectMetadataStore）而非复用 quota store 池**
   - 备选：复用 quotaMetadataStore 连接池（评审 #7 曾讨论）。
   - 选择：按 V3 §0 决策表新建独立连接池。
   - 理由：计量与 quota 的容量 profile 不同，独立池故障隔离；V3 定稿已澄清此点，属既定设计而非临时决定。

2. **鉴权走 V2 pilot allowlist（方案 B）而非扩展 legacy scope 前缀清单**
   - 备选：`scopeAllowedForPath` 手工前缀（legacy 方案 A）。
   - 选择：`x-ani-authz` + pilot allowlist + `getPlatformMeteringUsage`。
   - 理由：V3 §3.6 明确为鉴权四批次合入后的既定方向；pilot 链路有 fail-closed 与 principal_kinds/boundary 语义，legacy 前缀无此能力。

---

## Open Questions

1. **生产部署 RLS 复核（最重要）**：租户隔离完全依赖 RLS，连接用户若是 superuser 则 RLS 不生效（本地实测已复现：superuser 连接下租户可见全平台数据）。部署 checklist 必须包含：Gateway DATABASE_URL 使用非特权用户（如 ani_app_user），上线前 `SELECT rolsuper FROM pg_roles WHERE rolname='<user>'` 复核。
2. **METERING_PROVIDER_MODE 部署 checklist**：漏配时静默走 local 内存模式（查不到 metering_usage_records 但返回 200），需写入部署文档。
3. **BOSS 登录 returnTo 缺 /boss 前缀**：前端既有 bug（登录后跳 `/metering/gpu-hours`），与本批次无关，建议另立 issue。
4. **测试库造数残留**：`resource_ref LIKE 'seed-%'` 模拟数据待清理（清理语句在任务区 seed 脚本头部，未跟踪目录）。

---

## Verification

| 命令/方式 | 结果 |
|---|---|
| `make test`（Windows 等价 go test 全包） | 通过；2 个 sandbox 用例失败为既有 Windows 环境问题（symlink 特权 + Python os.O_DIRECTORY），已用改动前 commit worktree 复现证明与本批次无关 |
| `make validate-architecture` | 通过 |
| `make validate-services` | 核心校验全部通过（make 递归调用的路径括号问题为 Windows 工具链问题，子脚本直跑通过） |
| `git diff --check` | 通过 |
| 单元测试 | PgMeteringService 11/11、handler 8/8、pilot 鉴权矩阵全过、collectors 24/24、前端 38/38 |
| 真实 PG 集成 | 造数后租户/平台/过滤/聚合全路径符合预期；RLS 用非特权角色验证生效 |
| 浏览器 E2E | BOSS 平台 GPU-Hours 页 + Console 用量报表（三种分组）全部 200 并正确渲染（截图存任务区） |
