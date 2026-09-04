# BOSS「资源池与容量态势」接口方案

> 版本：v0.1（评审稿）
> 整理时间：2026-09-03
> 对应原型：产品原型-9.03（本地未入库）
> 读者：评审人（架构 / 后端 / 前端）

***

## 1. 背景与目标

BOSS 原型「平台运营总览」→「资源池与容量态势」（路由 `/boss/overview/capacity`）以及「平台资源池总览」（路由 `/boss/ops/pool`）需要展示**平台级容量数据**：区域卡片（名称/状态/是否可开通/GPU 空闲·总量/节点·AZ/租户数）+ 顶部指标（区域数、GPU 总量、GPU 空闲、租户数）。

当前后端**没有**对应汇总接口。已与产品确认口径：

> **「现在就把整个平台认为是一个区域」**（2026-09-03）

因此本方案新增**一个只读的平台容量汇总接口**，返回 **1 条区域记录**（整平台 = 默认区域），数据从**真实集群计算**，不做静态/占位值。

**数据范围（已确认）：** 平台级（跨租户）只读容量视图，不涉及区域主数据 CRUD（启用/停用/开放开通/刷新容量）——本方案只做只读汇总，写操作口径待区域主数据落地后再评审。

***

## 2. 现状与复用评估

### 2.1 可复用的现有能力

| 能力           | 位置                                          | 说明                                                           |
| ------------ | ------------------------------------------- | ------------------------------------------------------------ |
| GPU 节点/设备/标签 | `GPUInventory.ListNodeClasses`（K8s adapter） | 返回 GPU 节点，含 `Ready`、`Devices`、`Labels`（zone 等）、`Allocatable` |
| GPU 占用分布     | `GET /api/v1/gpu-inventory/occupancy`       | total / in\_use / available / fault / by\_gpu\_type          |
| 租户数          | `TenantService.ListAvailableTenants`        | status <> 'disabled' 的租户列表                                   |
| K8s Pod 查询   | `KubernetesRESTClient`                      | 查 pod（含 label selector、nodeName、phase）                       |

### 2.2 为什么不能直接复用 `/gpu-inventory/occupancy`

现有 occupancy 的 `in_use` 计算是**租户级**口径：

- `gpu_inventory_resources.go` 的 `gpuNodeOccupancy` 用 `middleware.GetTenantID(c)` 过滤本租户的 GPU Pod（`ani.kubercloud.io/tenant-id=<tenant>`），只统计**当前登录租户**的 Running GPU Pod；

- BOSS（`scope=platform`）没有租户上下文，直接调它会把 in\_use 算成 0（平台管理员名下无 Pod），得到错误的"全部空闲"。

**结论：** 平台级容量需要**跨所有租户 namespace 的 GPU 占用统计**，是新的计算逻辑，不能直接复用租户级 occupancy。

### 2.3 路径与 RBAC 约束（关键）

Gateway 的 `scopeAllowedForPath`（`middleware/auth.go`）路由白名单：

- `/api/v1/platform/*`、`/api/v1/admin/*` → **仅** **`scope=platform`（BOSS）** 可访问；

- `/api/v1/observability/*` → **仅** **`scope=tenant`** 可访问（平台管理员无权访问）；

- `/api/v1/svc/*` → platform 与 tenant 均可（角色级 RBAC 由 rbac.go 校验）。

因此本容量接口**必须放在** **`/api/v1/platform/*`** **或** **`/api/v1/admin/*`** **前缀下**，不能放 `/observability/*`（与租户级 resource\_trend 分开，语义也不同：前者平台级、后者租户级）。

***

## 3. 方案：新增平台容量汇总接口

### 3.1 接口契约

```
GET /api/v1/platform/capacity
OperationID：getPlatformCapacity
```

| 项                | 值                                                                                    | 说明                                 |
| ---------------- | ------------------------------------------------------------------------------------ | ---------------------------------- |
| 方法               | GET                                                                                  | 只读，无 body                          |
| 权限               | `scope:capacity:read`（新 scope，走 `/platform/*` 前缀白名单 + x-ani-authz boundary=platform） | 仅 BOSS 平台角色可访问                     |
| principal\_kinds | `[user]`                                                                             | 平台账密/角色；API key 不授予 platform scope |

无查询参数（当前是"整平台=1区域"的固定汇总视图；后续引入多区域/区域筛选再扩展参数）。

### 3.2 返回结构（200）

```json
{
  "regions": [
    {
      "id": "platform",
      "code": "platform",
      "name": "平台",
      "display_name": "平台（默认区域）",
      "status": "enabled",
      "open_for_tenant": true,
      "azs": ["az-a", "az-b", "az-c"],
      "tenant_count": 12,
      "capacity": {
        "gpu_total": 48,
        "gpu_free": 18,
        "nodes": 16,
        "cpu_cores": 512,
        "memory_gib": 2048
      }
    }
  ],
  "summary": {
    "region_count": 1,
    "gpu_total": 48,
    "gpu_free": 18,
    "tenant_count": 12,
    "nodes": 16,
    "azs": ["az-a", "az-b", "az-c"]
  },
  "dev_profile": {
    "mode": "real",
    "provider": "kubernetes-platform-capacity",
    "real_provider": true,
    "reason": "capacity computed from real cluster (GPU inventory + nodes + tenant store)"
  }
}
```

**字段口径：**

- `regions`：固定返回 1 条（整平台 = 1 区域），`id/code/name/display_name/status/open_for_tenant` 为平台主数据常量（本方案写死；后续区域主数据落地后由主数据驱动）；

- `azs`：集群中 GPU 节点的 zone 去重（来自节点 label `topology.kubernetes.io/zone`，回退 `failure-domain.beta.kubernetes.io/zone`；无 zone label 时为空数组，前端显示 `—`）；

- `summary`：各字段为 regions 汇总（当前即单区域值），前端顶部指标直接消费。

### 3.3 数据来源与口径（整平台 = 1 区域）

| 字段                    | 数据源                                  | 计算口径                                                                                                           |
| --------------------- | ------------------------------------ | -------------------------------------------------------------------------------------------------------------- |
| `capacity.gpu_total`  | `GPUInventory.ListNodeClasses({})`   | 全部 GPU 节点的 `Devices` 总数（含 vGPU 切片），即平台全部 GPU 设备                                                                |
| `capacity.gpu_free`   | 同上 + 平台级占用                           | `gpu_total - in_use - fault`；`fault` = NotReady 节点上的设备数；`in_use` = 跨所有租户 namespace 的 Running GPU Pod 数（见 §3.4） |
| `capacity.nodes`      | `ListNodeClasses`                    | Ready 的 GPU 节点数（当前仅统计 GPU 节点；是否需要含非 GPU 节点见 §7 待确认项）                                                           |
| `capacity.cpu_cores`  | `ListNodeClasses` 节点 `Allocatable`   | 各 Ready GPU 节点 `cpu` allocatable 求和（Gi 精度向下取整）；可选扩展，不阻塞主链路                                                     |
| `capacity.memory_gib` | 同上                                   | 各 Ready GPU 节点 `memory` allocatable 求和（GiB）；同上                                                                 |
| `azs`                 | 节点 label                             | Ready GPU 节点 zone label 去重（缺失不报错）                                                                              |
| `tenant_count`        | `TenantService.ListAvailableTenants` | 返回租户条数（status <> 'disabled'）；该服务不可用时降级为 0 并写 dev\_profile.reason                                               |

**统一降级语义（承接 observability 的既有约定）：**

- 任一数据源失败不阻塞整个接口返回 200：缺失字段给 0/空，`dev_profile.real_provider=false` + `reason` 记录降级原因，让前端/运维能区分"真实无数据"与"链路降级"；

- local/dev profile 下 `ListNodeClasses` 走 `LocalGPUInventory`（2 节点：ani-gpu-a Ready×2 卡、ani-gpu-b NotReady×1 卡），可得到确定性的演示数据。

### 3.4 平台级 GPU 占用统计（in\_use）——本方案唯一的新计算逻辑

**目标：** 统计全平台当前被租户工作负载占用的 GPU 数。

**推荐口径：** 跨所有租户 namespace（`ani-tenant-*`）统计 **Running 且请求 GPU 资源**（`nvidia.com/gpu` / `nvidia.com/vgpu` / `volcano.sh/vgpu-number`）的 Pod 数。每个 GPU Pod 占 1 个设备记录（整卡 Pod = 1 张物理卡，vGPU Pod = 1 个切片），与现有 `gpuInventoryRecordFromDevice` 的 in\_use 语义一致。

**实现方式（二选一，推荐 A）：**

- **A（推荐）**：用 `KubernetesRESTClient` 枚举所有 namespace（过滤 `ani-tenant-` 前缀）或集群级 `GET /api/v1/pods` + label selector，统计 Running GPU Pod；平台管理员视角天然无租户边界，用"命名空间前缀 + GPU 资源请求"双重过滤避免把平台自身组件 Pod 算入；

- B：复用每个租户的 occupancy 查询（`gpuNodeOccupancy` 的跨租户版），对每个租户 namespace 分别查一次再累加——请求次数 = 租户数，不推荐。

**与现有** **`gpu-inventory/occupancy`** **的关系：** 本接口不调用它（它是租户级），只复用其 in\_use 语义（Running GPU Pod = 占 1 个设备）来保持一致的可读性。

### 3.5 安全与租户边界

- 接口只读、无租户/实例参数，无跨租户泄露面（返回的是平台聚合值，不包含租户明细）；

- `scopeAllowedForPath` 已保证 `/platform/*` 仅 `scope=platform` 可访问；`x-ani-authz boundary=platform` 再由 auth-service 角色 RBAC 兜底（platform-admin / platform-ops / platform-readonly）；

- 不接收/不透传前端 PromQL 或租户标识，杜绝注入面（与 resource\_trend 同一原则）。

***

## 4. 落地改动清单

| # | 文件                                                                  | 改动                                                                                                                                                          |
| - | ------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1 | `repo/api/openapi/v1.yaml`                                          | 新增 `/platform/capacity` path + `PlatformCapacityResponse`/`PlatformRegion` schema；`x-ani-rbac-scope: scope:capacity:read` + `x-ani-authz boundary=platform` |
| 2 | `repo/pkg/ports/platform_capacity.go`（新）                            | 新增 `PlatformCapacityService` 接口 + `PlatformCapacityOverview`/`PlatformRegion` 结构体，`GetCapacityOverview(ctx)`                                                |
| 3 | `repo/pkg/adapters/runtime/kubernetes_platform_capacity.go`（新）      | real adapter：组合 `GPUInventory` + `KubernetesRESTClient`（节点/跨租户 Pod）+ 租户计数，实现 §3.3/§3.4 口径                                                                   |
| 4 | `repo/pkg/adapters/runtime/local_platform_capacity.go`（新）           | local 降级实现（用 `LocalGPUInventory` 派生确定值）                                                                                                                     |
| 5 | `repo/services/ani-gateway/internal/router/platform_capacity.go`（新） | handler + 注册 `GET /platform/capacity`；注入依赖到 `RegisterOptions`                                                                                               |
| 6 | 生成物                                                                 | `make gen-gateway-authz`（新 scope 的 authz 策略）+ `make gen-core-sdk`（Go SDK client）                                                                            |
| 7 | 单元测试                                                                | adapter（real 解析 + local 降级 + 跨租户占用计数）、router、ports 层用例                                                                                                      |
| 8 | 文档                                                                  | `repo/development-records/{批次名}.md`、README、CURRENT-SPRINT、ANI-06 Section 零（Feature batch 四件套）                                                               |

**工程顺序（遵守 ANI 强制规则）：** 先改 OpenAPI 契约 → 再实现 ports / adapter / router → 生成物 → 测试 → 文档。校验门禁：`make validate-openapi-spec`、`make validate-architecture`、`make test`、`git diff --check`。

***

## 5. 前端对接要点

- BOSS 前端 `coreClient` baseUrl 已是 `/api/v1`，只需新增一个 `getPlatformCapacity()` 客户端方法；

- 「资源池与容量态势」与「平台资源池总览」两页共用本接口：前者用 `regions[]` 渲染区域卡片（`capacity.gpu_free/gpu_total`、`nodes`、`azs.join(", ")`、`tenant_count`），后者用 `summary` 渲染顶部指标；

- 降级提示：`dev_profile.real_provider === false` 时按既有约定提示"数据链路降级"，不静默展示 0；

- 原型页的「启用/停用/开放开通/刷新容量」按钮在本方案只读接口下**不实现**（写操作后续评审），前端先隐藏或置灰。

***

## 6. 风险与边界（评审重点核对）

1. **in\_use 口径**：跨租户 Running GPU Pod 计数与现有租户级 occupancy 的"每 Pod 占 1 设备"语义一致，但无法精确到"节点的哪张卡"（规划阶段未持久化 device index，与现状一致）。整卡 1 Pod 1 卡、vGPU 1 Pod 1 切片，多卡 Pod（多容器多 GPU）当前按 Pod 数计 1 而非按 GPU 卡数计，**可能与实际占用数有偏差**——需确认是否接受，或后续按容器 GPU 请求数精确计数。
2. **节点数口径**：当前只统计 GPU 节点（ListNodeClasses 仅含 GPU 节点）。若"节点/AZ"希望表达整个平台所有 worker 节点，需扩展为直接查 K8s nodes（多一次查询）。
3. **非 GPU 资源的准确性**：cpu/memory 来自节点 allocatable 求和，未扣减系统预留/平台组件占用，是"总量"而非"可用量"，语义需与产品确认。
4. **区域常量硬编码**：id/code/name/status/open\_for\_tenant 写死为"整平台=1区域"；但**不落地区域主数据表**，未来多区域时需把常量迁移为主数据，属可预期的演进点（不引入超前抽象）。
5. **性能**：每次请求实时查询 GPU 节点 + 跨租户 Pod，规模大时可能变慢；当前规模可接受，后续可加缓存（本方案不引入缓存）。
6. **不新增不必要的实体**：仅新增 1 个 port + 2 个 adapter（real + local 降级），复用现有 `GPUInventory`、`KubernetesRESTClient`、`TenantService`，无新表/无新后台任务。

***

## 7. 待评审确认项

- [ ] 接口路径 `/api/v1/platform/capacity` 与操作名 `getPlatformCapacity` 是否认可？（放 `/platform/*` 前缀下以满足 BOSS scope 白名单）

- [ ] 新 scope `scope:capacity:read` 命名是否认可？（对标 `scope:metering:platform:read` 的 `:platform:` 后缀风格，也可用 `scope:platform:read`）

- [ ] 只读汇总、**不实现区域 CRUD 写操作**，是否认可？（启用/停用/开放开通等后续评审）

- [ ] in\_use 采用"Running GPU Pod 数"口径（每 Pod 占 1 设备），多卡 Pod 按 1 计的偏差是否接受？

- [ ] `nodes` 仅统计 GPU 节点，是否需要含全平台节点？

- [ ] cpu/memory 为节点 allocatable 总量口径（非可用量）是否接受？

- [ ] 新增 `PlatformCapacityService` port（而非在 router 层直接组合现有 adapter）是否符合评审预期？

<br />
