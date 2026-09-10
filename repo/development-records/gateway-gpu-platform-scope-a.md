# GATEWAY-GPU-PLATFORM-SCOPE-A：GPU 规格/清单接口放行 platform scope

> 状态：已完成（live 验证通过，10.10.1.66 rollout 镜像 `dev-20260907-gpuscope-a`）
> 批次类型：hotfix（修复 BOSS 平台账号访问 GPU 接口 403）
> 分支：ani-hotfix（commit `5679395`）

## 1. 背景与目标

BOSS 平台用 root（平台账号）访问 GPU 相关接口全部 403：

```
GET /api/v1/gpu-specs
{"code":"FORBIDDEN","message":"token scope not allowed for this path"}
```

定位：`/api/v1/gpu-specs*`、`/api/v1/gpu-inventory*` 在 OpenAPI 契约中无
`x-ani-authz`，属 `PolicySourceLegacy`，走 `authenticateLegacy` →
`scopeAllowedForPath`（`services/ani-gateway/internal/middleware/auth.go`）。
该函数末尾 `return scope == "tenant"`，而 root 平台登录签发的 token
`scope=platform`，被直接拒绝。

GPU 规格与设备清单是**集群级共享资源**，双域都要访问：

- Console（tenant scope）：创建 GPU 实例拉 `/gpu-specs`、`/gpu-specs/availability`（配额视角）、`/gpu-inventory`
- BOSS（platform scope）：GPU 池运维管理 `/gpu-specs`、`/gpu-inventory`、POST/DELETE

## 2. 为什么不走 V2 authz 契约（x-ani-authz）

V2 的 `boundary` 是单选枚举（`own`/`tenant`/`platform`），gateway
`DomainAllowsBoundary` 与 auth-service `principalDomainAllowsBoundary` 均为
**credential domain 互斥**判断：写 `platform` 则 Console 全 403，写 `tenant`
则 BOSS 仍 403，无法表达"双域共享"。且切 V2 需要重新生成
`zz_generated_core_policies.go`、种 permission 数据、gateway + auth-service
双服务部署，超出 hotfix 范围。GPU 接口迁移 V2（含 `cluster` boundary 扩展）
记为后续 Feature batch。

## 3. 实现

`scopeAllowedForPath` 参照既有 `/svc/`、`/admin/` 双放行分支写法，新增：

```go
// Cluster-level GPU catalog: both tenant (Console) and platform (BOSS)
// need read/manage access; role-level admission stays with rbac.go
// CheckPermission (platform-admin / tenant-admin).
if strings.HasPrefix(path, "/api/v1/gpu-specs") ||
    strings.HasPrefix(path, "/api/v1/gpu-inventory") {
    return scope == "platform" || scope == "tenant"
}
```

- sandbox scope token 仍被前面的分支挡住，不受影响
- 角色细分准入继续由 `authorizeLegacy` → `CheckPermission` 承担
  （platform token 走 `platform-admin` 角色判定，tenantID 为零 UUID 非空可通过）
- 未改 OpenAPI 契约、未改 auth-service、无生成物变更

测试：`auth_test.go` 新增 10 个用例，覆盖 gpu-specs / gpu-specs/{id} /
gpu-specs/availability / gpu-inventory / gpu-inventory/occupancy 在
platform / tenant / sandbox 三种 scope 下的允许与拒绝组合。

## 4. 验证

本地：`go build ./services/ani-gateway/...` 通过；`middleware`、`router` 包测试全通过。

live（10.10.1.66，root 平台登录 `POST /auth/platform/password/login` 拿
platform token，NodePort 30080）：

```
GET /gpu-specs                -> 200（返回 CRD 规格列表）
GET /gpu-specs/availability   -> 200
GET /gpu-inventory            -> 200（11 卡 RTX-4090，real provider）
GET /gpu-inventory/occupancy  -> 200（real_provider: true）
GET /instances                -> 403（负向对照：scope 隔离未被破坏）
```

## 5. 部署插曲与已知边界

- **滚动更新期间 PG 连接池耗尽（环境系统性隐患，非本批引入）**：
  gateway 新 Pod 启动时每个 store 独立开 20 连接池，旧 Pod（`maxUnavailable=0`
  下不退出）26 连接 + 新 Pod 请求 + metering/auth 等把 PG
  `max_connections=100` 打满，新 Pod 反复 `database not ready` → liveness
  kill。处置：`kubectl patch` 把 gateway Deployment 滚动策略改为
  `maxUnavailable:1, maxSurge:1` 后 rollout 成功。**该策略修改目前留在线上
  Deployment**；PG 连接余量偏紧待后续统一收敛（共享连接池或调大
  max_connections）。
- **`/gpu-specs/availability` 平台视角语义缺陷（本批不修）**：platform token
  的 tenantID 为零 UUID，配额按租户计算导致 `quota_remaining=0`、spec 显示
  `full`（但 `has_idle_devices=true`）。BOSS 当前未消费该端点；待 GPU 接口
  迁移 V2 authz（`cluster` boundary）批次一并设计平台视角。
- 后续项：GPU 接口迁移 V2 authz 契约（扩展 boundary 支持双域，如新增
  `cluster`），涉及契约、gateway/auth-service 校验语义、生成器与权限数据。
