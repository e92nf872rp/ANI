# QUOTA-LIST-DISABLED-FILTER-A — `/quotas` 配额列表过滤已禁用租户

> 日期：2026-09-24　|　分支：`hotfix/gpu-occupancy-scope`（随 PR #185 与 GPU-OCCUPANCY-CARD-COUNT-A 一起评审合并；原独立 PR #187 已关闭）　|　状态：live verified（ani-test2）

## 问题现象

用户报障 BOSS 平台配额列表 `GET /api/v1/quotas`：租户管理侧【活跃】+【冻结】租户共 47 个，配额列表却返回 48 条，怀疑「禁用租户后配额数据没更新」。

## 根因

两层事实叠加，均属**代码逻辑如此**而非数据损坏：

1. **列表枚举不过滤租户状态**：`PostgresQuota.List`（`pkg/adapters/runtime/postgres_quota.go`）无 tenant_id 分页路径的第一步 SQL 为 `SELECT DISTINCT tenant_id FROM resource_quota …`，直接按配额表枚举租户，不 JOIN `tenants`、不过滤 `status`；租户名在第二步 LEFT JOIN 补充，禁用租户照常出现在结果中。
2. **禁用不清理配额行（既有语义）**：`TenantService.DisableTenant`（`services/tenant-service/internal/service/tenant_service.go`）只做前置校验（gpu/cpu/memory/storage 四维 `used+reserved>0` 拒绝）+ Core 状态转换 + 审计，注释明确「不释放资源」；`resource_quota`、`resource_reservations` 行全部保留。清理能力 `DeleteTenantQuota`（`DELETE /admin/tenants/{tenant_id}/quota`）是独立管理端点，禁用流程不自动触发。

链路：建租户/绑套餐/配额审批 → `resource_quota` 写入多维行；禁用（终态）→ 行保留；列表 → 不过滤状态 → 已禁用租户继续出现，列表条数与活跃+冻结租户数不一致。

## 修复（方案 A：列表过滤状态）

- `PostgresQuota.List` 分页第一步 SQL 改为 `JOIN tenants t ON t.id = rq.tenant_id` 并过滤 `t.status IN ('active', 'frozen')`；keyset 分页 cursor 逻辑不变（仍 `rq.tenant_id > $1::uuid` 原生 UUID 比较走索引）。
- **指定 `tenant_id` 的单租户查询（`GetMy` 路径）不过滤**：显式查询单个租户（含禁用）继续可用，与 `/admin/tenants/{tenant_id}/quota` 显式查询/清理能力互补。
- **`resource_quota` 行保留**：「禁用是终态、不释放资源」语义不变，不删数据。
- 新增回归测试 `TestPostgresQuotaStoreListFiltersDisabledTenants`：断言 step1 SQL 必须包含 `JOIN tenants` 与 `t.status IN ('active', 'frozen')`，防止回归。
- 同步 `api/openapi/v1.yaml` `/quotas` description：声明仅返回 active/frozen 租户、禁用租户配额行保留可显式查询。

## 验证

- `go test`（GO_PACKAGES 全量 12 模块）通过；仅 2 个与本批次无关的既有 Windows 环境性失败（`TestSandboxFileScripts*`：symlink 需管理员权限 + 缺 `python3`，改动前后均如此）。
- `validate_openapi_spec`（2 spec OK）、`validate_component_imports`（passed）、`validate_auth_gateway_contract`（valid）、`generate_gateway_authz_test` + `validate_gateway_authz_drift`（no drift）+ `validate_core_gateway_authz_routes`（324 routes / 250 registry / 0 error）、`gofmt -l` 无输出、`git diff --check` 干净。

## live 验证（PASS，ani-test2 10.10.1.66:30083，镜像 `test2-20260924-quotafilter2`）

**部署插曲一（环境被他批次占用）**：部署前 ani-test2 正在跑另一改动线镜像 `dev-20260924-gpu-card-count`（GPU-OCCUPANCY-CARD-COUNT-A，PR #185，未合 main）。直接用本批次基线构建会覆盖该修复，故按并集构建：本批次修复 cherry-pick 到 `hotfix/gpu-occupancy-scope`，镜像含两批次改动；原独立 PR #187 关闭，本批次随 PR #185 一起评审合并。**部署插曲二（live 验证抓到真 bug）**：首版 SQL `ORDER BY rq.tenant_id`（uuid 原始列）与 `SELECT DISTINCT rq.tenant_id::text` 表达式不一致，PG 报 `SQLSTATE 42P10`（DISTINCT 时 ORDER BY 必须出现在 select list）——本地 fake tx 测试不执行真 SQL 未拦住，首滚后 `/quotas` 500；修正为 `ORDER BY rq.tenant_id::text`（commit 6528cc4）换 tag `quotafilter2` 重滚后恢复。

**验证矩阵 7/7 PASS**（平台登录 root → token；验证脚本 `ai-scripts/verify_quotafilter_test2.py`）：

| # | 检查 | 结果 |
|---|---|---|
| A | 平台登录 | 200，token 正常 |
| B | `GET /admin/tenants` 真值 | total=54，active=31 / frozen=7 / disabled=16 |
| C | `GET /quotas` 全量分页 | 200（首滚 500 已修），29 个租户 |
| D1 | 列表 ⊆ active+frozen | 29 ⊆ 38（另 9 个 active/frozen 无配额行，既有行为不列出），extra=∅ |
| D2 | disabled 不泄漏 | 16 个禁用租户 0 出现（修复前 14 个有配额行的禁用租户全部混入） |
| E1 | 禁用租户配额行保留 | DB `DISTINCT tenant_id=43 == 列表 29 + disabled 有行 14`，行未删除 |
| E2 | API 与 DB 口径一致 | 列表 29 == 库内 active/frozen 有行 29 |

回归并集镜像中的 GPU-OCCUPANCY-CARD-COUNT-A：occupancy `physical_card_count=6 / logical_card_count=24` 正常（`in_use/available` 为动态占用值随真实 workload 漂移，7/17→8/16 属正常）。

## 边界与遗留

- 已禁用租户从列表消失后，管理员无法在配额列表页看到其历史配额/占用快照（数据仍在库内，可按 tenant_id 显式查询）；如需「列表带状态标注」的产品形态，属契约/前端独立批次。
- `result.Total` 仍是本页条数（非全量 total），分页语义未改动。
- OpenAPI 变更仅 description 文字，无 schema/枚举/端点变更，非破坏性。
