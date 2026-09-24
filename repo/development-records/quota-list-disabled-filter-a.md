# QUOTA-LIST-DISABLED-FILTER-A — `/quotas` 配额列表过滤已禁用租户

> 日期：2026-09-24　|　分支：`hotfix/quotas-list-filter-disabled`　|　状态：local verified（未部署，live 验证待部署后执行）

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
- **未部署、未做 live 验证**：修复生效需部署新 gateway 镜像后复测 `/api/v1/quotas` 条数与租户数对齐；部署后验证方式为该接口分页走尽计数 vs `SELECT count(*) FROM tenants WHERE status IN ('active','frozen')`。

## 边界与遗留

- 已禁用租户从列表消失后，管理员无法在配额列表页看到其历史配额/占用快照（数据仍在库内，可按 tenant_id 显式查询）；如需「列表带状态标注」的产品形态，属契约/前端独立批次。
- `result.Total` 仍是本页条数（非全量 total），分页语义未改动。
- OpenAPI 变更仅 description 文字，无 schema/枚举/端点变更，非破坏性。
