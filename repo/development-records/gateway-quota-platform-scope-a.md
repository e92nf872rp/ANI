# GATEWAY-QUOTA-PLATFORM-SCOPE-A：/quotas 收敛为 platform 专属并封堵跨租户泄露

> 状态：已完成（live 验证通过，10.10.1.66 rollout 镜像 `dev-20260907-quotas-a`）
> 批次类型：hotfix + 安全修复（跨租户配额读取泄露）
> 分支：ani-hotfix（commit `43aa63c`）
> 承接：GATEWAY-GPU-PLATFORM-SCOPE-A（同类 scope 问题，但修法相反）

## 1. 背景与目标

BOSS 访问 `GET /api/v1/quotas?limit=100` 报 403 `token scope not allowed for
this path`（GPU 池页面用 `/quotas` 派生租户下拉，`gpu-pool.tsx` 同时依赖
`/gpu-scheduling/queues`，一并 403）。

排查中确认这**不能照搬 GPU 的双放行修法**，并发现一个已存在的跨租户数据泄露：

- `listQuotas` handler（`router/quota_resources.go`）不注入租户过滤，
  `ports.QuotaListRequest` 的 `TenantID` 为空；
- `PostgresQuota.List` 空 TenantID 时走 `WithPlatformTx`，
  `SELECT DISTINCT tenant_id FROM resource_quota` 全表扫描；
- RLS 策略 `resource_quota_platform_bypass USING
  (current_setting('app.current_tenant_id', true) IS NULL)` 在平台事务下
  **绕过租户隔离返回全部租户行**；
- 而 `scopeAllowedForPath` 末尾默认放行 tenant → **任何租户 token 调用
  `/quotas` 即可读取全平台所有租户配额**（修复前 tenant scope 实测可访问）。

## 2. 实现

`services/ani-gateway/internal/middleware/auth.go` `scopeAllowedForPath`：

1. **`/api/v1/quotas` 精确匹配仅 platform**（对齐 `/admin/*` 模式）。用精确匹配
   而非前缀，避免误伤 `/quotas/me`——后者是租户自查（`GetMy` 走 `WithTenantTx`
   受 RLS 约束），保持 tenant-only、platform 拒绝。
2. **`/api/v1/gpu-scheduling*` 并入 GPU 双放行分支**。其 handler 自身按 tenant
   label 过滤（平台默认队列全员可见 + 本租户队列），platform token 只见平台默认
   队列，无跨租户泄露，与 gpu-specs/gpu-inventory 同类。

`auth_test.go` 新增 6 用例：quotas 列表 platform 允许 / tenant 拒绝（泄露防回归）、
quotas/me tenant 允许 / platform 拒绝、gpu-scheduling 双域允许。

## 3. 验证

本地：`go build ./...`、`middleware`/`router` 包测试通过。

live（10.10.1.66，`dev-20260907-quotas-a`）：

```
platform token:
  GET /quotas?limit=100      -> 200（39 行 / 39 个不同租户，跨租户总览确认）
  GET /gpu-scheduling/queues -> 200（平台默认队列）
  GET /gpu-specs             -> 200（回归通过）
  GET /gpu-inventory         -> 200（回归通过）
  GET /quotas/me             -> 403（精确匹配生效，platform 不占租户自查）
  GET /instances             -> 403（负向对照）
tenant token（tenant-a/admin）:
  GET /quotas?limit=100      -> 403  ★ 跨租户泄露已封堵（修复前该请求返回全平台配额）
  GET /quotas/me             -> 200（租户自查正常）
  GET /gpu-specs             -> 200（Console 回归通过）
```

## 4. 已知边界与后续

- **`/quotas` 无租户过滤依赖 handler 语义正确性**：本批用 scope 边界把它锁在
  platform 内是正确的最小修复；长期应迁移 V2 authz（`cluster`/platform boundary +
  permission 数据），与 GATEWAY-GPU-PLATFORM-SCOPE-A 后续项合并处理。
- Console 全仓只用 `/quotas/me`（`coreClient.ts` 注释与代码确认），无租户侧
  依赖 `/quotas` 列表，收紧无回归面。
- 其余"末尾默认放行 tenant"的 legacy 路径中若还有类似"handler 走 PlatformTx
  但路径对 tenant 开放"的组合，属同类风险，待按链路排查（不预防性批量 guard，
  遵守 Guard 冻结令）。
