# PLATFORM-CAPACITY-A — 平台容量态势只读汇总接口

完成日期：2026-09-03
对应 Sprint：Sprint 13 并行功能流（BOSS「资源池与容量态势」/「平台资源池总览」）
分支：`feat/platform-capacity`（commit `64bc8ab` + 生成物补齐 `fae60b0`，未 push）
方案依据：`services/tasks/modules/plan/plan-platform-capacity.md`
交付文档：本地方稿（implementation-diff / test-report / api，未入库）
验证结果：`make test`、`make validate-architecture`、`make validate-openapi-spec`、`validate-auth-contract`、`make validate-gateway-authz`、`gen-core-sdk` 零漂移、`git diff --check` 全过；真实环境实测（10.10.1.66 K8s 测试集群，镜像 `dev-20260903-platformcapacity`）平台/租户/无凭证/坏 token 四类用例全过（详见测试报告）。

## 实现了什么

Core 新增 `GET /api/v1/platform/capacity` 平台容量态势只读汇总端点：整平台 = 1 个默认区域（id/code=`platform`），不实现区域 CRUD。响应含 `regions`（主数据常量 + azs + tenant_count + capacity 五字段）、`summary`（regions 投影）与 `dev_profile`（真实链路标记）。数据由 `PlatformCapacityService` 从真实集群聚合：GPUInventory（`ListNodeClasses` 设备/zone/allocatable）+ KubernetesRESTClient（集群级存在性 label selector 统计跨租户 Running GPU Pod，每 Pod 占 1 设备，与 gpu-inventory occupancy 语义一致；in_use 超设备数截断保证 gpu_free ≥ 0）+ TenantService（可用租户数）。单数据源失败不阻塞 200：失败源字段置 0/空并写 `dev_profile.real_provider=false` + reason。Gateway runtime 由 `PLATFORM_CAPACITY_PROVIDER` 装配（`kubernetes_rest` 走真实链路；空/local/not_configured 回退确定性 local 降级，与 gpu-inventory fallback 惯例一致；未知值启动报错）。权限：`x-ani-rbac-scope: scope:capacity:read` + `x-ani-authz {resource: capacity, action: get, boundary: platform, principal_kinds: [user]}`，authz 注册表契约即开关。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 `GET /platform/capacity`（operationId=getPlatformCapacity）+ `PlatformCapacityResponse` / `PlatformRegion` / `PlatformRegionCapacity` / `PlatformCapacitySummary` schema（+91 行） |
| `pkg/ports/platform_capacity.go` | 新增 | `PlatformCapacityService` 接口与 Overview/Region/Summary/DevProfile 结构体 |
| `pkg/adapters/runtime/kubernetes_platform_capacity.go` | 新增 | real adapter：GPUInventory + KubernetesRESTClient + TenantService 三源聚合；单源降级；provider 常量 `kubernetes-platform-capacity` |
| `pkg/adapters/runtime/local_platform_capacity.go` | 新增 | local 确定性降级实现（nil 依赖安全） |
| `pkg/adapters/runtime/{kubernetes,local}_platform_capacity_test.go` | 新增 | 聚合口径 / in_use 截断 / 单源降级 / 确定性输出单测 |
| `services/ani-gateway/internal/router/platform_capacity.go` | 新增 | handler + 响应投影；service nil 时回退 local（保证未配置 provider 可启动） |
| `services/ani-gateway/platform_capacity_runtime.go` | 新增 | `PLATFORM_CAPACITY_PROVIDER` 装配分支 |
| `services/ani-gateway/main.go`、`internal/router/router.go` | 修改 | 装配与注册 |
| `services/ani-gateway/internal/authz/zz_generated_core_policies.go` | 修改 | 生成物（`make gen-gateway-authz`） |
| `sdks/core/{go,java,python,typescript}` + `sdk-metadata.json` | 修改 | 生成物（`make gen-core-sdk`，四语言零漂移） |
| `docs/api/{core,index}.html`、`frontends/console/src/api/core-schema.d.ts` | 修改 | 生成物补齐（commit `fae60b0`：gen-api-docs + gen-core-schema，Core Operations 236→237） |

## 真实环境实测（2026-09-03）

环境：10.10.1.66 K8s 测试集群，gateway deployment 镜像 `docker.changqingyun.cn/ani/ani-gateway:dev-20260903-platformcapacity`，env 含 `PLATFORM_CAPACITY_PROVIDER=kubernetes_rest` + `ANI_AUTH_MODE=auth_service`，Pod 1/1 Running，NodePort 30080。

- **平台 token 正例**：root 平台登录 → 200，返回真实集群数据 `gpu_total=11 / gpu_free=3 / nodes=3 / tenant_count=22 / cpu_cores=512 / memory_gib=1760`，`dev_profile.mode=real`、`real_provider=true`、`provider=kubernetes-platform-capacity`；9 项字段校验全过。
- **租户 token 越权负例**：tenant-a/admin 租户 token → 403 `FORBIDDEN principal not allowed by operation policy`（V2 generated policy platform 边界拒绝）。
- **无凭证 / 坏 token**：均 401。
- 数据合理性交叉核对：gpu_free 3 = gpu_total 11 − in_use 8 − fault 0，与 gpu-inventory occupancy 口径一致。
- 实测脚本断言笔误 1 例（provider 期望值误写为环境变量值 `kubernetes_rest`），修正断言后通过，非代码缺陷。

## 实现与方案差异（决策记录，详见差异文档）

1. `dev_profile.provider` 取适配器常量 `kubernetes-platform-capacity`（非环境变量值），与既有 real adapter 命名风格一致。
2. 跨租户 GPU Pod 统计采用集群级存在性 label selector 一次查询（非逐租户枚举），语义等价且请求数 O(1)。
3. `in_use = min(pod_count, gpu_total − fault)` 截断，保证 `gpu_free ≥ 0`（契约 `minimum: 0`）。
4. azs 取 `topology.kubernetes.io/zone` 优先 + beta label 回退；测试环境节点未打 zone label，返回空数组（契约允许）。

## 验收命令与结果

| 命令 | 结果 |
|---|---|
| `make test` | ✅（Windows sandbox symlink 预存失败与 main 基线一致，非本批引入） |
| `make validate-architecture` | ✅ |
| `make validate-openapi-spec` | ✅ |
| `make validate-auth-contract` | ✅ |
| `make validate-gateway-authz` | ✅ no drift |
| `make gen-core-sdk` 重跑 | ✅ 零漂移 |
| `make gen-api-docs` + `gen-core-schema` 补跑 | ✅（commit fae60b0） |
| `git diff --check` | ✅ |
| 真实环境实测 | ✅ 见上节 |

## 边界声明

- 本批为 Core 只读汇总单功能批次：不含前端接入、不含区域 CRUD、不含容量告警/预测；`cpu/memory` 为节点 allocatable 总量口径（非可用量），如需"可用容量"需另立契约字段。
- 真实环境实测基于测试集群真实 provider，证明的是本接口的 real 链路与平台/租户边界，不外推 full platform production ready。
- Core API v1 变更为 additive（新端点 + 新 schema），无需再生成兼容性基线。
- 分支未 push；push / PR 由用户决定。
