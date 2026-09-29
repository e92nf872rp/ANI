# STORAGE-VOLUME-MODE-A — 存储卷 volume_mode（Block/Filesystem）契约与消费方校验

完成日期：2026-09-28
对应 Sprint：Sprint 13/14 之间的实例/存储链路热修复（分支 `feat/volume-mode-block`）
PR：[#195](https://github.com/e92nf872rp/ANI/pull/195)（`djm-afk:feat/volume-mode-block` → `e92nf872rp:main`）
批次类型：**Feature batch**（新增契约字段 + 控制面能力 + DB 迁移，非 guard micro-batch）
验证结果：`go build ./...`（`repo/pkg`、`repo/services/ani-gateway`）通过；`repo/pkg` 全量 `go test ./...` 仅既有 Windows sandbox symlink/python3 两用例环境性失败（与本批次无关），其余全绿；`gofmt -l` 无输出；`git diff --check` 通过；门禁脚本：`validate_openapi_spec`（2 spec OK）、`validate_core_api_compatibility`、`validate_storage_alpha_contract`、`validate_api_docs_contract`、`validate_spec_split_contract`、`validate_component_imports`、`validate_inference_legacy_control_plane`、`validate_gateway_authz_drift`（no drift）、`validate_core_gateway_authz_routes`（325 路由 / 251 registry / 0 error）、`validate_doc_entrypoints`、`validate_services_boundary`、SDK/API docs 生成幂等（零漂移）全通过；`atlas.sum` 按 Atlas 算法重算并校验一致。**live 验证 PASS（ani-test2，镜像 `test2-20260928-volumemode`），详见文末。**

## 实现了什么

为块存储卷引入 Kubernetes `volumeMode` 语义，让 VM 数据盘以**裸设备在线热插**、容器/GPU 容器继续**按目录挂载**，二者各自使用匹配模式的卷：

1. **契约新增 `volume_mode`**（`CreateStorageVolumeRequest`、`StorageVolume` 响应），枚举 `block`/`filesystem`，**默认 `filesystem`**（不打断既有 13 个按目录挂卷的容器/GPU 容器实例）。
2. **渲染按模式**：`KubernetesStorageRenderer.RenderVolume` 不再硬编码 `volumeMode: Filesystem`，改为按记录渲染；空值回退 `Filesystem`（保护存量卷的 re-observe）。
3. **VM 数据盘走 block**：`provisionVMDataDisks` 新建数据盘显式传 `volume_mode=block`。
4. **消费方 fail-fast 校验**（`volumeMode` 不可变，模式错配只能删卷重建）：
   - VM 建实例引用既有 `volume_id` 数据盘 → 必须 block；
   - 容器/GPU 容器建实例挂卷（目录挂载）→ 必须 filesystem，且在 provider apply **之前**校验，避免产生 provider 孤儿实例；
   - VM/容器 `attach_volume` 生命周期动作 → 同为上述模式要求，在记录 operation 之前拒绝。
5. **`ListVolumes` 对 pending 卷也 re-observe**（与 `GetVolume` 对齐）：WFFC PVC 在首个消费者出现后绑定，否则 Console 列表永远停留在 `pending`；并新增 `GET /api/v1/volumes?volume_mode=block|filesystem` 列表过滤（大小写不敏感，可与 state/keyword/in_use 组合），便于前端按用途筛选（VM 盘 / 容器盘）。
6. **DB 迁移** `storage_volumes.volume_mode`（可空 TEXT + CHECK，存量行回填 `filesystem`）。
7. **契约附带纠正**：`CreateStorageVolumeRequest.storage_class` 默认值 `standard` → `ani-block`（代码侧兜底 `defaultVolumeStorageClassName` 本已是 `ani-block`，契约文字过去不一致）。
8. **（追加，2026-09-29）卷列表过滤修复与"按实例可挂载"过滤**：Console VM 详情挂载云盘请求 `GET /volumes?state=pending,available&available_for_instance_id=…` 返回空列表。根因有二：① `state` 过滤为单值精确匹配，`"pending,available"` 不命中任何卷；② `available_for_instance_id` 是前端自造参数，后端契约与实现均不存在（静默忽略）。修复：`state` 支持逗号分隔多值 any-of（volumes/filesystems/objects/buckets/vector stores 共用过滤实现同步生效）；`available_for_instance_id` 进入契约并实现——校验实例存在且属于本租户（否则 400），按实例 Kind 推导要求的 `volume_mode`（VM=block，其余=filesystem），并排除占用中的卷（in_use）。

## 背景与决策（方案 A 取代方案 B）

真实环境（隔离测试环境）复现：VM 挂载块存储卷后 PVC 一直 `pending`，virt-handler 侧长时间重复重试。根因是 ANI 把块存储卷**硬编码渲染为 `volumeMode: Filesystem`**，而 KubeVirt 对 Filesystem 卷的**在线热插**要求卷内已存在 `disk.img`（`pkg/virt-handler/hotplug-disk`），空盘必然失败；非热插（VM 启动）路径 KubeVirt 会自行创建 `disk.img`，故问题只在热插路径暴露。

曾尝试方案 B（保持 Filesystem、VM 挂载改走「停机→改 VM spec→开机」）。经与用户确认改为**方案 A**：

- 契约新增 `volume_mode`，**block 给 VM**（在线热插、guest 内为裸设备，与既有的 `/dev/disk/by-id/ani-*` 设计对齐），**filesystem 给容器/沙箱**（按目录挂载）。
- 代价：`volumeMode` 在 Kubernetes 上**不可变**，存量 Filesystem 卷不能原地切换为 Block，VM 若要挂必须**删除重建**；且容器与 VM 必须各用各自模式的卷。

用户在方案 A 上补充三条决策：① `volume_mode` 默认值取 `filesystem`（保护存量容器实例）；② 存量卷保持 `filesystem`，VM 挂载时 **fail-fast 报明确错误**而非静默重试；③ 一并修 `storage_class` 契约默认值与 `ListVolumes` pending re-observe。

方案 B 的代码与记录保留在备份分支/tag（`hotfix/volume-block-mode`、`backup/plan-b-volume-block-mode`），PR 已关闭，仅作参考，不进入本批次。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | `CreateStorageVolumeRequest.volume_mode`（默认 filesystem）+ `StorageVolume.volume_mode`；`storage_class` 默认 `ani-block`；移除 `InstanceDiskSpec.volume_mode`（VM 磁盘模式由服务端固定为 block，输入字段无意义） |
| `deploy/migrations/20260928000100_storage_volumes_volume_mode.sql` | 新增 | `storage_volumes` 加 `volume_mode` 列 + 存量回填 `filesystem` + CHECK 约束 |
| `deploy/migrations/atlas.sum` | 修改 | 重算校验和（既有 61 条 hash 不变，追加本文件） |
| `pkg/ports/storage_resources.go` | 修改 | `StorageVolumeModeBlock`/`StorageVolumeModeFilesystem` 常量；`StorageVolumeRecord.VolumeMode`、`StorageVolumeCreateRequest.VolumeMode` |
| `pkg/adapters/runtime/storage_renderer.go` | 修改 | `RenderVolume` 按记录渲染 volumeMode；新增 `renderVolumeMode`（空值回退 Filesystem） |
| `pkg/adapters/runtime/storage_service.go` | 修改 | `normalizeStorageVolumeMode`（空值/大小写归一，默认 filesystem）、`requireVolumeMode`（模式错配统一错误）；create fingerprint 纳入 mode；`ListVolumes` 两条路径对 pending 卷 re-observe |
| `pkg/adapters/runtime/storage_store.go` | 修改 | `storage_volumes` INSERT/SELECT 增加 `volume_mode`；UPSERT `COALESCE(EXCLUDED.volume_mode, ...)` 保护既有值 |
| `pkg/adapters/runtime/instance_service.go` | 修改 | `provisionVMDataDisks` 新建数据盘 block + 既有 `volume_id` 数据盘必须 block；`validateCreateStorageModes`（容器挂卷 filesystem，pre-apply）；`validateAttachVolumeMode`（attach_volume 按 kind 校验） |
| `services/ani-gateway/internal/router/storage_resources.go` | 修改 | 请求/响应 DTO 增加 `volume_mode` 并映射；（追加）`state` 多值 any-of 解析、`storageAvailableForInstanceFilter`（available_for_instance_id → 实例存在性校验 + 模式推导 + 排除占用卷） |
| `services/ani-gateway/internal/router/vector_store_resources.go` | 修改 | （追加）共用过滤结构 `statuses` 多值化后同步 any-of 匹配 |
| `services/ani-gateway/internal/router/storage_resources_test.go` | 修改 | （追加）`state` 多值断言；`TestStorageHTTPVolumeListAvailableForInstance`（VM 只见空闲 block、容器只见 filesystem、组合 `state=pending,available`、占用卷排除、未知实例 400） |
| `pkg/adapters/runtime/instance_service_test.go` | 修改 | 2 个既有用例补种 `storedVolumes`（VM 既有盘 block、容器挂卷 filesystem）；新增 3 个模式校验用例 |
| `pkg/adapters/runtime/storage_service_test.go` | 修改 | 新增 `volume_mode` 默认/归一/非法值用例 |
| `pkg/adapters/runtime/storage_renderer_test.go` | 修改 | 新增 volumeMode 渲染用例（block/filesystem/空值回退） |

## 完工标准达成

- [x] 契约先改再实现；Core API v1 仅新增**可选**字段（非破坏性）
- [x] `go build ./...`（pkg + ani-gateway）通过
- [x] `go test ./...`（pkg）除既有 Windows 环境性失败外全绿；ani-gateway router 测试通过
- [x] `gofmt -l` 无输出、`git diff --check` 通过
- [x] 门禁（openapi/compat/storage-alpha/api-docs/spec-split/architecture/authz/doc-entrypoints/services-boundary/生成幂等）全通过
- [x] `atlas.sum` 重算并与 Atlas 算法一致（本地无 atlas 二进制，按 Atlas `NewHashFile` 累积哈希算法等价复算并校验）
- [x] 单测覆盖：默认值、归一、非法值、渲染三态、VM 既有盘错配拒绝、容器挂 block 拒绝、attach 模式错配拒绝（VM/容器）
- [x] **live 验证 PASS（ani-test2）**：VM 挂 block 卷在线热插成功（VMI disk Ready + virt-handler `successfully created block device`）、容器挂 filesystem 卷放行、容器挂 block 卷与 VM 挂 filesystem 卷均 fail-fast 400 —— 见文末

## 本机环境限制与等价执行

- 本机 PATH 无 `make`/`python`/`gofmt`/`node`：`make` 门禁改用等价 Python 脚本直跑（Anaconda python）；Go 用 `C:\Program Files\Go\bin`。
- `validate_sdk_alpha.py` 的 Go 冒烟段通过，随后因本机无 `node`（TypeScript `node --check`）无法跑完整；`validate_openapi_spec.py` 需 `openapi_spec_validator`（已安装）。
- `validate_openapi_spec.py`、SDK/docs 生成与幂等均已单独执行并零漂移。

## live 验证 PASS（ani-test2，隔离测试环境 NodePort 30083）

**镜像：** `docker.changqingyun.cn/ani/ani-gateway:test2-20260928-volumemode`，digest `sha256:d110eb39497d8e704726c0131dd81fd86d4785cab83b1b036d833c365d1ccd4f`；只 `kubectl set image`（未改 env），rollout 成功、Pod `ani-gateway-7c8d76d857-prxt8` 1/1 Running、healthz 200。部署前该环境 gateway 为 **kaiwu 线镜像 `anisys-20260928-kaiwu.3`**（Plan B 的 `test2-20260928-volumeblock` 已被其覆盖），回滚 tag 即 `anisys-20260928-kaiwu.3`。**未触碰 ani-system（生产）。**

**DB 迁移（ani-test2 PG，手工 psql 路径——该环境无 `atlas_schema_revisions`）：** 应用 `20260928000100_storage_volumes_volume_mode.sql` → `ALTER TABLE` / `UPDATE 8`（存量 8 行全部回填 `filesystem`）/ `DO`；回读 `volume_mode|YES|text`、`filesystem|8`、约束 `storage_volumes_volume_mode_check` 存在。

**API 验证（`POST /volumes` + `POST /instances/{id}/lifecycle` action=attach_volume）：**

| 用例 | 结果 | 证据 |
|---|---|---|
| 建卷默认 `volume_mode` | ✅ `filesystem` | 201 响应 `"volume_mode": "filesystem"` |
| 建卷显式 `volume_mode=block` | ✅ `block` | 201 响应 `"volume_mode": "block"` |
| 容器 + block 卷 | ✅ 400 拒绝 | `volume "vol_3ff555c8…" is block mode but container directory mount requires volume_mode=filesystem (volumeMode is immutable; recreate the volume)` |
| 容器 + filesystem 卷 | ✅ 200 放行 | `secret-bind-ctl-29670` attach 成功 |
| VM + filesystem 卷 | ✅ 400 拒绝 | `volume "vol_3826c6e6…" is filesystem mode but vm disk hotplug requires volume_mode=block (volumeMode is immutable; recreate the volume)` |
| VM + block 卷 | ✅ 200 放行 | `test-rebuild2` attach 成功，VM 保持 running |

**K8s 侧硬证据（VM block 在线热插成功 = 本批次核心）：**

- block PVC `vol-vol-3ff555c8-5d46-48de-a78f-16a8735a4522`：`Bound` / `mode=Block` / `sc=ani-block` / `volumeName=pvc-eaf1a12e-…`。
- VMI `test-rebuild2`：`disks=containerdisk volume-vol-3ff555c8-…`，`volumeStatus=… volume-vol-3ff555c8-…(Ready)`（热插盘就绪）。
- virt-handler 日志：`{"msg":"successfully created block device volume-vol-3ff555c8-…","pos":"mount.go:430"}`、`{"msg":"Marking volume volume-vol-3ff555c8-… as mounted in pod, it can now be attached","pos":"vm.go:488"}`——即 block 卷走裸设备创建路径，**不再触发 Filesystem 热插缺 `disk.img` 的反复重试**。
- ANI 卷状态经 re-observe 收敛：`state` 由 `pending` → `available`，`reason=observed Kubernetes PVC phase Bound`（`ListVolumes`/`GetVolume` 的 pending re-observe 亦生效）。

**清理：** 验证后 `detach_volume`（VM/容器各 200）+ `DELETE /volumes/{id}`（block/fs 各 200）已回收控制面记录；provider PVC 按既有语义不级联删除。

**未在 live 复验的点（有单测覆盖 / 属既有边界）：**

- VM 建实例引用既有 filesystem 数据盘（`provisionVMDataDisks` 路径）未跑 live（避免残留 VM 消耗配额），由单测 `TestLocalInstanceServiceCreateRejectsFilesystemVolumeForVMDataDisk`（断言 provider apply 前即拒绝、`orchestrator.creates==0`）覆盖；attach 路径（`validateAttachVolumeMode`）已 live 证明同一语义。
- 容器 + volume 挂载的 PVC 绑定：容器 attach 返回 200，但目标 PVC 仍 `Pending`（无消费者 Pod），属**容器挂块存储卷**的既有挂载机制（容器-3 RWO 约束/绑定路径），非本批次范围；本批次容器侧要求仅为**模式校验**，已 live 证明。

## 追加改动验证（2026-09-29，卷列表过滤 + available_for_instance_id）

本机等价执行（无 `make`，Go 用 `C:\Program Files\Go\bin`，Python 用 miniconda）：

- `go build ./...`（`repo/services/ani-gateway`）通过；
- `go test ./...`（`repo/services/ani-gateway` 全部 4 个包）通过；
- `gofmt -l`（ani-gateway 包）无输出；`git diff --check` 通过；
- `validate_openapi_spec`（YAML 解析 OK）与 `validate_storage_alpha_contract`（valid）直跑通过。

### live 验证 PASS（2026-09-29，ani-test2，镜像 `test2-20260929-availforinstance` digest `sha256:894af9103776…9fb0`）

- 构建/部署：从本 worktree（merge 06d94fd，已合入 origin/main）上传 `pkg`/`runtimeadmin`/`services/ani-gateway` 到构建机构建并推送 Harbor；`kubectl set image` 滚动 ani-test2 gateway（未改 env），rollout 成功，Pod 1/1 Running、healthz 200（启动期 DB 未就绪重试导致 4 次容器重启，DB 就绪后稳定，重启数不再增长）；部署前该环境 gateway 为 kaiwu 线 `anisys-20260928-kaiwu.3`，回滚 tag 同。**未触碰 ani-system。**
- 冲突解决：origin/main 合入 `feat/volume-mode-block`（merge commit 06d94fd），README.md / ANI-06 两处为同位置各自新增批次记录的冲突，两侧条目均保留；合并后 ani-gateway 全部包 `go test` 通过、`pkg` build/test 通过（仅既有 Windows sandbox symlink 两用例环境性失败）、契约门禁复跑通过。
- **API 七断言全 PASS（tenant-a，复用既有 running VM `test-rebuild2` 与 running 容器）**：

| 用例 | 结果 |
|---|---|
| `GET /volumes?limit=100&state=pending,available`（多值） | ✅ 200，新建 block/filesystem 两卷均返回（修复前必返回空） |
| `?limit=100&state=pending,available&available_for_instance_id=<VM>`（前端原始查询） | ✅ 200，只含 block 卷 |
| `?…&available_for_instance_id=<容器>` | ✅ 200，只含 filesystem 卷 |
| `available_for_instance_id=inst-nope-*`（未知实例） | ✅ 400 BAD_REQUEST（不再静默空列表） |
| attach 回归：容器+filesystem 卷 / VM+block 卷 | ✅ 均 200（merge 后 volume_mode 校验路径无回退） |
| 占用卷排除：filesystem 卷挂到容器后 | ✅ 从该容器的 `available_for_instance_id` 列表消失 |
| 清理：detach ×2 + DELETE ×2 | ✅ 全 200 控制面记录回收 |

- 验证脚本：`ai-scripts/vm_availforinstance_build_deploy.py`、`ai-scripts/vm_availforinstance_verify.py`、`ai-scripts/vm_availforinstance_probe*.py`（.gitignore 排除）。

## 备注

- **不改变 `running→stopped` 的配额语义**，与本批次无关。
- 存量 Filesystem 卷若要供 VM 使用需**删除重建**为 `volume_mode=block`；容器/GPU 容器不受影响。
- `volume_mode` 列允许 NULL 并由代码兜底 `filesystem`，以兼容历史行与空值写入路径。
- 方案 B 的实现与文档不在本分支（见 `backup/plan-b-volume-block-mode`）。
- 前端对接说明：`repo/design/storage-volume-mode-frontend-integration.md`（`volume_mode` 可选、容器默认 filesystem、VM 需显式 block、VM 新建数据盘后端自动 block、模式不可变与存量卷需重建、错误码清单）。