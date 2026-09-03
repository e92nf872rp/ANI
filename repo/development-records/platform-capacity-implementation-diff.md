# 平台容量态势接口（platform/capacity）实现差异文档

> 编制时间：2026-09-03
> 分支：`feat/platform-capacity`（commit `64bc8ab`，未 push）
> 批次：`PLATFORM-CAPACITY-A`
> 方案文件：《容量态势接口方案.md》
> 说明：本文记录方案与最终落地代码之间的差异、取舍与现场决策。

***

## 0. 执行摘要

- 契约、ports、real/local 双 adapter、gateway 路由与 runtime 装配、生成物（authz 注册表 + Core SDK 四语言）、单测全部按方案落地，门禁全过，真实环境实测全过。
- 方案总体按 Plan A 执行；落地时对 **provider 命名、GPU Pod 统计口径、in_use 上限截断、azs 取值回退、降级字段语义** 五处做了细化或修正，见 §2。
- 无未解决阻塞；遗留风险见 §4。

***

## 1. 落地文件清单（commit 64bc8ab，+1200 行 / 19 文件）

| 层 | 文件 | 内容 |
|---|---|---|
| 契约 | `repo/api/openapi/v1.yaml` | 新增 `GET /platform/capacity` 路径 + `PlatformCapacityResponse` / `PlatformRegion` / `PlatformRegionCapacity` / `PlatformCapacitySummary` schema（+91 行） |
| ports | `repo/pkg/ports/platform_capacity.go` | `PlatformCapacityService` 接口与 Overview/Region/Summary/DevProfile 结构体 |
| real adapter | `repo/pkg/adapters/runtime/kubernetes_platform_capacity.go` | 组合 GPUInventory + KubernetesRESTClient + TenantService 的真实聚合实现 |
| local adapter | `repo/pkg/adapters/runtime/local_platform_capacity.go` | 确定性降级实现（与 gpu-inventory local fallback 惯例一致） |
| gateway 路由 | `repo/services/ani-gateway/internal/router/platform_capacity.go` | handler + 响应投影 + nil 回退 local |
| gateway 装配 | `repo/services/ani-gateway/platform_capacity_runtime.go`、`main.go`、`internal/router/router.go` | 按 `PLATFORM_CAPACITY_PROVIDER` 装配 |
| 生成物 | `internal/authz/zz_generated_core_policies.go`、`repo/sdks/core/**`（go/java/python/ts + sdk-metadata） | `gen-gateway-authz` + `gen-core-sdk` 重跑产物，零漂移 |
| 单测 | kubernetes/local/router/runtime 四个 `_test.go` | 见测试报告 |

## 2. 方案 → 实现差异（决策记录）

### 2.1 dev_profile.provider 命名：`kubernetes-platform-capacity`

- 方案文本示例写 `kubernetes_rest`；落地时 provider 字段语义为「适配器名」而非「环境变量值」，real adapter 取常量 `kubernetes-platform-capacity`（kubernetes_platform_capacity.go L47），local 降级取 `local-platform-capacity`。
- 取舍：与 instance observability 等既有 real adapter 的 provider 命名风格一致，便于前端区分真实链路名与环境开关值。
- 实测注意：真实环境验证时断言 provider 应为 `kubernetes-platform-capacity`，而非环境变量值 `kubernetes_rest`（首次实测脚本按方案示例断言曾误报 1 例 FAIL，修正断言后通过）。

### 2.2 跨租户 GPU Pod 统计口径：集群级 label selector 存在性统计

- 落地实现不做逐租户枚举，而是用 KubernetesRESTClient 在集群级按存在性 label selector（`ani.kubercloud.io/tenant-id` 存在）一次性列 Running GPU Pod，每 Pod 占 1 设备（与 gpu-inventory occupancy 语义一致）。
- 原因：平台级接口不持有多租户上下文，逐租户查询请求数 O(租户数) 且部分租户可能已无配额；集群级一次查询语义等价且更稳。

### 2.3 in_use 上限截断：`gpu_free` 不为负

- Pod 计数可能超过设备数（历史遗留/异常工作负载），落地时 `in_use = min(pod_count, gpu_total - fault)`，保证 `gpu_free >= 0`（契约层 `minimum: 0` 强制）。

### 2.4 azs 取值：zone label 优先 + beta label 回退

- `topology.kubernetes.io/zone` 优先，回退 `failure-domain.beta.kubernetes.io/zone`；两 label 均缺失则该节点不贡献 zone。实测集群 GPU 节点未打 zone label，返回空数组（契约允许，`缺失为空数组`）。

### 2.5 降级语义：单源失败不阻塞 200

- GPUInventory / nodes / TenantService 三个数据源任一失败时，接口仍返回 200：失败源字段置 0/空数组，`dev_profile.real_provider=false` 并写入 reason（注明哪个源失败）。全源成功才置 `real_provider=true`。

### 2.6 与方案一致但值得强调的点

- 整平台 = 1 个默认区域（id/code = `platform`），不做区域 CRUD；`summary` 是 regions 的投影（单区域下数值恒等）。
- 权限：`x-ani-rbac-scope: scope:capacity:read` + `x-ani-authz {resource: capacity, action: get, boundary: platform, principal_kinds: [user]}`；authz 注册表为生成物，契约即开关。
- runtime 装配：`PLATFORM_CAPACITY_PROVIDER` 空值 / `local` / `not_configured` → nil → router 层回退 local 降级（保证 gateway 未配置 provider 时仍能启动并返回 200）；`kubernetes_rest` → 真实链路；其它值启动报错。

## 3. 契约字段速览

```
GET /api/v1/platform/capacity   （Bearer，平台 token）
→ 200 {
  regions: [{ id, code, name, display_name, status, open_for_tenant,
              azs[], tenant_count,
              capacity: { gpu_total, gpu_free, nodes, cpu_cores, memory_gib } }],
  summary: { region_count, gpu_total, gpu_free, tenant_count, nodes, azs[] },
  dev_profile: { mode, provider, real_provider, reason }
}
403 FORBIDDEN（租户 token）；401（无凭证/坏 token）
```

## 4. 遗留风险与后续建议

1. **azs 恒为空**：测试环境 GPU 节点未打 zone label，区域 azs/summary.azs 均为空数组。建议后续给 GPU 节点补 zone label，无需改代码即可生效。
2. **memory_gib/cpu_cores 为 allocatable 总量**：契约口径为节点 allocatable 总量而非可用量；如需「可用容量」需另立字段或扩契约（当前方案明确不做）。
3. **tenant_count 依赖 TenantService**：租户服务不可用时为 0 并写 reason，BOSS 端需容忍 0 值显示。
4. **未 push**：分支仅本地 commit，push 与 PR 由用户决定。
