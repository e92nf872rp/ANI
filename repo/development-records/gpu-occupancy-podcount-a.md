# GPU-OCCUPANCY-PODCOUNT-A — GPU 占用数量口径与多实例分段回显

> 日期：2026-09-28　分支：`hotfix/metering-gpu-lifecycle-events`（叠加于 QUOTA-READ-REPAIR-A / PR #191 之上）
> 状态：live verified（ani-test2 30083，镜像 `test2-20260928-quota-readrepair2`）
> 关联：QUOTA-READ-REPAIR-A 遗留项「gpu-inventory 占用口径四缺陷」中的 ②③（已由 PR #184 在分支内修复）之外的 ①④，及本批次实测补充发现

## 1. 背景与实测校准

用户报障 `/v1/gpu-inventory` 实例占用对象不对、租户占用回显不对。经实测校准，PR #184（GPU-OCCUPANCY-SCOPE-A，已在分支内）已修复四缺陷中的两项：

- ✅ 已修（#184）：非 GPU Pod 过滤（`ParseRunningGPUPodOccupancy` 只保留 Running + `ani-tenant-*` + 请求 `nvidia.com/gpu`/`nvidia.com/vgpu`/`volcano.sh/vgpu-number` 的 Pod）；
- ✅ 已修（#184）：平台视角 fallback `demo-tenant`（`platformScopeTenant` 识别占位值 + 集群级跨租户查询）；
- ❌ 本批次修：**多卡 Pod 少算**（每 Pod 固定占 1 台设备，`nvidia.com/gpu=2` 的 Pod 只标 1 台）；
- ❌ 本批次修（部分）：**回显对象任意**（每节点单 entry，全部 in_use 设备回显字典序最小的一个实例）；
- ⛔ 不可行（本批次实测确认）：**精确绑卡**——运行中 Pod 对象上没有任何设备分配信息（实测 `volcano.sh/devices-to-allocate` 为空，设备 ID 由 device plugin 经容器运行时注入，不落 Pod spec/annotation），planning 阶段也不选具体卡（调度器决策）。精确到卡需要新增调度结果回写机制（独立批次）。

## 2. 修复内容

### 2.1 数量口径（parser + 平台容量 + inventory 三处同源同步）

- `GPUPodOccupancyRecord` 新增 `GPUCount`（`podGPUCount`：跨容器 limits 之和，K8s 对扩展资源按容器独立分配设备；解析失败/非正数保守按 1，与旧「每 Pod 1 设备」口径一致）；
- `KubernetesPlatformCapacityService.runningGPUPodCountsByNode`：`counts[node]++` → `counts[node] += pod.GPUCount`（`/platform/capacity` 的 `gpu_free` 口径同步，与 `/gpu-inventory/occupancy` 的 `available` 继续一致）；
- gateway `gpuNodeOccupancyEntry` 新增 `GPUCount`（各 Pod 请求量之和）与 `Pods []gpuNodeOccupancyPod`（按实例名字典序稳定排序），设备标记从 `index < PodCount` 改为按 Pods 顺序逐段消费各自 GPUCount。

### 2.2 多实例分段回显

同节点多实例时，设备级 `instance_id`/`tenant_id` 回显按排序后的 Pods 依次分配（inst-a 占 1 台 → 设备 0、inst-b 占 2 台 → 设备 1-2），不再把全部 in_use 归到字典序最小的一个实例名；节点级摘要字段（`TenantID`/`InstanceID`）保留字典序最小实例名做兼容。Pod 真实归属来自 `ani.kubercloud.io/tenant-id` label（#184 已切到真实归属），平台视角跨租户回显即真实租户。

无契约变更（响应 schema 形状不变）、无 DB 迁移、无生成物变更。存量行为兼容：`PodCount` 字段保留；无实例 label 的裸 GPU Pod 仍只计数不回显（与修复前一致）。

## 3. 单测

- parser：`TestPodGPUCountSumsContainersAndFallsBackToOne`（多卡/切片/多容器求和/非法值回退 1/零值回退/无 GPU limits 回退）、`TestParseRunningGPUPodOccupancyCarriesGPUCount`（端到端携带）；
- router：`TestGPUNodeOccupancyMapFromPodsAggregatesGPUCountAndSortsPods`（Pending 不计、GPUCount=2+1、排序、摘要取最小）、`TestGPUInventoryListEchoesPerPodDeviceSegments`（4 设备节点：inst-a 占 1 台 → 设备 0、inst-b 占 2 台 → 设备 1-2、设备 3 available；occupancy 汇总 InUse=3/Available=1）；
- 既有 3 处直接构造 entry 的夹具同步补 `Pods`/`GPUCount`。

## 4. 门禁

`go build`、`go vet`、`pkg/adapters/runtime` + `services/ani-gateway` 全量测试通过（仅既有 Windows sandbox symlink 两用例环境性失败，与本批次无关）；gofumpt；`git diff --check`。

## 5. live 验证（ani-test2 30083，镜像 `test2-20260928-occupancy-podcount`，digest sha256:ed6d4ad1…）

真值独立复算（kubectl 遍历全部 `ani-tenant-*` 命名空间 Running Pod 取 `nvidia.com/gpu`/`volcano.sh/vgpu-number` limits 求和）：**12 个 GPU Pod × 各 1 卡 = in_use 12**（dev-phys-02 六个、dev-phys-03 六个，跨 3 个租户）。

| 检查 | 结果 |
|---|---|
| 平台 token `/gpu-inventory/occupancy` | **total=24 / in_use=12 / available=12 / tenant_count=3**（与真值 12 一致） |
| 平台 token `/platform/capacity` | **summary.gpu_free=12** 与 occupancy available=12 一致（同源同口径） |
| 租户 token occupancy（tenant-a） | **in_use=7 / available=17**（只计本租户 7 个 GPU Pod） |
| 设备级分段回显（dev-phys-02，6 台 in_use） | 回显 4 个不同实例 × 3 个租户：`metering-gpu-recheck-2`/`metering-vgpu`/`test-env`/`test-ly-gpu-container`/`testgpu`×2——修复前 6 台全部回显字典序最小的一个实例 |
| dev-phys-03 | 6 台 in_use，分段回显 4 个实例（`asd`/`metering-gpu-recheck-1`/`new-t`/`tc-gpu-…`×2/`test-ly-gpu-container`） |
| CPU-only Pod 回归 | nginx 等 CPU 实例零回显（#184 口径未回退） |

10/10 断言 PASS。测试脚本与真值复核均已留档（ai-scripts/step_occ_live_verify.py）。

## 6. 遗留

1. 精确绑卡：需调度结果回写机制（Pod 分配的设备 ID 落库/落注解后回显真实卡位），独立批次；
2. 共节点多实例的分段分配在「同一 Pod 组内多卡」场景仍无法区分具体卡位（本身无信息源）；
3. ani-system 已随 2026-09-28 统一部署覆盖（gateway 同镜像，详见 `quota-read-repair-a.md` §6）。
