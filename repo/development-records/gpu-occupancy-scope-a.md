# GPU-OCCUPANCY-SCOPE-A — GPU 占用口径统一与 30080 台账迁移补齐

完成日期：2026-09-23
对应 Sprint：hotfix 分支受控批次（ani-hotfix，分支 `hotfix/gpu-occupancy-scope`，基线 origin/main `c522868`）
验证结果：go build + go test（pkg、ani-gateway）通过；`validate_component_imports`、`validate_inference_legacy_control_plane`、`validate_gateway_authz_drift`（no drift）、`validate_core_gateway_authz_routes`（324 路由 / 250 registry / 0 error）、`git diff --check` 通过；live 验证 PASS（2026-09-23，ani-system 10.10.1.66:30080，gateway 镜像 `dev-20260923-gpu-occupancy-scope2`）

## 背景

用户报障 `/api/v1/gpu-inventory/occupancy` 与 `/api/v1/platform/capacity` 的 GPU 空闲数互相矛盾（前者 `available=24`，后者 `gpu_free=0`）。两个接口注入的是同一个 `GPUInventory` 与同一个 `KubernetesRESTClient`，设备集合同源，`total` 与 `gpu_total` 实测恒等（都是 24）；矛盾只在**占用口径**上。排查同时发现 30080 环境 `/gpu-inventory/events` 报 `42P01 relation "gpu_device_events" does not exist`。

## 根因（三层）

1. **`occupancy` 的 `in_use` 是租户级，平台 token 回退到占位租户**。`gpuNodeOccupancy` 用 `middleware.GetTenantID(c)` 过滤本租户 GPU Pod；平台 token 的 `tenant_id` 由 auth-service 置为 `uuid.Nil`，网关侧呈现为全零 UUID `00000000-0000-0000-0000-000000000000`（30080 access log 实测），旧代码只判空串、未识别全零 UUID，于是按该值去查命名空间 `ani-tenant-00000000-…-0000`（不存在）→ 0 个 Pod → `in_use=0`、`available=total`。设计文档 `repo/services/tasks/modules/plan/plan-platform-capacity.md` §3.4 已明确指出 occupancy 的 `in_use` 是租户级口径、平台级容量必须另算，但平台 token 侧的租户回退仍会把 occupancy 算成"全部空闲"。
2. **两侧都把「带租户 label 的 Running Pod」当作 GPU 占用，未校验 Pod 是否真的请求 GPU 资源**。`runningGPUPodCountsByNode` 只用存在性 label selector 统计集群内带 `ani.kubercloud.io/tenant-id` 的 Running Pod，偏离同文档 §3.4 写明的「Running 且请求 GPU 资源」口径。实测该集群有 53 个此类 Pod，其中只有 7 个真的请求 GPU（其余为 VM `virt-launcher-*`、`nginx`、`precheck-*`、`secret-bind-ctl-*` 等 CPU-only 工作负载）；每节点再按 `min(Pod 数, 设备数)` 截断，三个节点全部顶满 → `in_use=24` → `gpu_free=0`。occupancy 侧同源缺陷（只要求 instance label，不校验 GPU 请求）。
3. **30080 环境从未应用迁移 `20260911_001_gpu_device_surface.sql`**。该批次（GPU-POOL-SURFACE-A）只在 ani-test2 验证并执行过迁移，ani-system 的 PG 无 `gpu_device_overlays` / `gpu_device_events` 两表（`select tablename from pg_tables where tablename like 'gpu_device%'` 返回 0 行；ani-test2 返回 2 行），故 events 与 `PATCH /gpu-inventory/{device_id}` 均报 42P01。该环境无 `atlas_schema_revisions`，迁移按既有做法手工执行。

## 实现了什么

1. **占用判定收敛为单一入口**：新增 `pkg/adapters/runtime/gpu_pod_occupancy.go`，导出 `ParseRunningGPUPodOccupancy`（只保留「Running + 位于 `ani-tenant-*` 命名空间 + 请求 GPU 扩展资源」的 Pod）与内部 `podRequestsGPU`（判定 `nvidia.com/gpu` / `nvidia.com/vgpu` / `volcano.sh/vgpu-number`，与 `gpuNodeClassesFromKubernetesNodeList` 的设备枚举口径一致）。两个接口共用该解析器，杜绝再次分叉。
2. **平台容量改用该口径**：`KubernetesPlatformCapacityService.runningGPUPodCountsByNode` 由自建 pod 结构解析改为调用 `ParseRunningGPUPodOccupancy`，删除重复的 `platformCapacityTenantLabel` 常量与 `encoding/json` 依赖。
3. **gateway occupancy 视角分派**：`gpuNodeOccupancy` 抽出 `gpuNodeOccupancyForRequest`，新增 `platformScopeTenant`（同时识别空串与全零 UUID）——平台占位值走 `gpuNodeOccupancyForPlatform`（集群级跨租户查询，口径与 `/platform/capacity` 一致），其余走 `gpuNodeOccupancyForTenant`（本租户命名空间）。聚合逻辑抽为 `gpuNodeOccupancyMapFromPods`，Pod 记录新增 `TenantID` 供跨租户视角回显归属。
4. **迁移补齐**：在 ani-system PG 应用 `20260911_001_gpu_device_surface.sql`（两表 + RLS `platform_bypass` 单策略 + `ani_app` 授权），并以 `ani_app_user` 身份插入冒烟验证。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/gpu_pod_occupancy.go` | 新增 | `ParseRunningGPUPodOccupancy` + `podRequestsGPU` + `GPUTenantLabel`/`GPUInstanceLabel`/`GPUTenantNamespacePrefix` 常量，两个接口的唯一占用判定入口 |
| `pkg/adapters/runtime/gpu_pod_occupancy_test.go` | 新增 | 解析器 4 用例（非 GPU Pod / 平台命名空间 / Pending / 未调度剔除；缺 instance label 仍计占用；空 body 与非法 JSON；资源名白名单表驱动） |
| `pkg/adapters/runtime/kubernetes_platform_capacity.go` | 修改 | `runningGPUPodCountsByNode` 改用共享解析器；删除 `platformCapacityTenantLabel` 与 `encoding/json` |
| `pkg/adapters/runtime/kubernetes_platform_capacity_test.go` | 修改 | 既有用例补 GPU 资源声明；新增 `TestKubernetesPlatformCapacityExcludesNonGPUPods`（设备数 8 ≫ Pod 数，确保断言不被截断掩盖） |
| `services/ani-gateway/internal/router/gpu_inventory_resources.go` | 修改 | 视角分派 + `platformScopeTenant` + `gpuNodeOccupancyMapFromPods`；`fetchPodOccupancyFromK8s` 支持集群级查询并复用共享解析器；`gpuPodOccupancy` 增 `TenantID`；删除 `encoding/json` |
| `services/ani-gateway/internal/router/gpu_inventory_resources_test.go` | 修改 | 新增平台视角（空串与全零 UUID 两种取值）、租户视角端点断言、`platformScopeTenant` 分类用例 |

**契约零变更**：未改 `api/openapi/v1.yaml`，无 SDK/静态文档/authz 生成物变更，无新增迁移，无 DB schema 变更。

## 占用口径（修复后）

设 A = Ready 且被 Pod 占用、B = Ready 空闲、C = 有台账覆盖、D = NotReady、E = NotReady 且有台账覆盖：

| 字段 | 公式 |
|---|---|
| `occupancy.total` = `capacity.gpu_total` | 全部 GPU 设备数（含 NotReady 节点、含 vGPU 切片） |
| `occupancy.in_use`（租户视角） | 本租户命名空间内 Running 且请求 GPU 的 Pod 数，按节点 `min(Pod 数, 设备数)` 截断 |
| `occupancy.in_use`（平台视角） | 同公式，范围扩到全部 `ani-tenant-*` 命名空间 |
| `capacity.in_use` | 同平台视角，不读台账 |
| `occupancy.available` | `|B|`（平台 scope 下台账覆盖的卡被排除） |
| `capacity.gpu_free` | `gpu_total − in_use − fault`（契约定义，不扣台账） |

## 完工标准达成

- [x] go build + go test（pkg / ani-gateway 全量；pkg 仅既有 Windows symlink 用例失败，与批次无关）
- [x] validate_component_imports / validate_inference_legacy_control_plane / validate_gateway_authz_drift / validate_core_gateway_authz_routes / git diff --check
- [x] 口径收敛为单一解析器，两侧共用
- [x] live 验证 PASS（2026-09-23，ani-system，镜像 `dev-20260923-gpu-occupancy-scope2`）
- [x] 迁移在 30080 应用并冒烟通过

## Live 验证结果（2026-09-23，ani-system 10.10.1.66:30080）

集群事实：3 个 GPU 节点（dev-phys-02 / dev-phys-03 / kubercloud）× 8 张 vGPU 切片 = 24；Running 且请求 GPU 的租户 Pod = 7（独立复算脚本 `ai-scripts/gpu-capacity-divergence.py` 离线核对一致）。

| 检查项 | 修复前 | 修复后 |
|---|---|---|
| `GET /gpu-inventory/occupancy`（平台 token） | `total=24 in_use=0 available=24` | `total=24 in_use=7 available=17` |
| `GET /platform/capacity` | `gpu_total=24 gpu_free=0` | `gpu_total=24 gpu_free=17` |
| 租户 token `GET /gpu-inventory/occupancy` | — | `in_use=7 available=17`（与平台一致） |
| 租户 token `GET /platform/capacity` | — | 403（隔离未破坏） |
| `PATCH /gpu-inventory/{device_id}` → maintenance | 400 `42P01` | 200，`available` 17→16、`maintenance_count=1` |
| `GET /gpu-inventory/events` | 400 `42P01` | 200，事件带 `node_name`/`gpu_type`/`actor` |
| `PATCH` → idle 还原 | — | 200，`available` 回到 17、`maintenance_count` 消失 |

迁移落库确认：两表创建、`tableowner=ani`、`rowsecurity=t`、`gpu_device_overlays_platform_bypass` / `gpu_device_events_platform_bypass` 单策略、`ani_app` 授权齐全、`ani_app_user` 插入/读取冒烟通过。验证写入的台账行已清理（两表均 0 行）。

## 已知边界

- **`gpu_free` 不扣台账**（契约定义 `gpu_total − in_use − fault`）。台账存在 maintenance/unavailable 时：`gpu_free − occupancy.available = 台账覆盖卡数 − 其中同时被 Pod 占用的卡数`（live 实测维护窗口：`17 − 16 = 1 = 1 − 0`）。是否让 `gpu_free` 也扣台账属独立决策，需改 OpenAPI 描述并把 `GPUDeviceSurfaceStore` 注入 `KubernetesPlatformCapacityService`，本批次未做。
- **`occupancy.fault` 与 `capacity.gpu_fault` 可能不等**：NotReady 节点的卡被台账覆盖时 `applyToRecord` 无条件覆盖 `fault`，occupancy 记 maintenance/unavailable 而 capacity 仍记 fault，差为该类卡数。
- **租户 scope 不合并台账**（`loadSurfaceStateForRequest` 仅 `scope=platform` 合并，防跨租户泄露），故租户视角 `available` 与 `gpu_free` 另差一层「本租户 in_use − 跨租户 in_use」。前端「空闲未分配」应以 `occupancy.available` 为准。
- **`logical_card_count` 不等于 `gpu_total`**：vGPU 节点下它是 `Σ shares`（实测 96），与 `gpu_total` 对齐的是 `total` / `physical_card_count`（24）。
- **PATCH 幂等回放**：同 `Idempotency-Key` 重放直接返回缓存响应且不落库，验证脚本每次必须换新 key（本批次复验时曾因复用 key 误判为台账读路径故障）。
- **30080 环境无 `atlas_schema_revisions`**，迁移为手工 psql 应用；正式环境首启须走 atlas 迁移流程。
- ani-test2（30083）未部署本批次镜像；其 PG 早已有台账两表，不受影响。
- 本地 Windows `make test-go` 因 Makefile Unix 风格 env 前缀不可执行（预存问题），以等效环境变量手动执行同列表测试。
