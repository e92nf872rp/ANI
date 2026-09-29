# 块存储卷 volume_mode 前端对接说明（STORAGE-VOLUME-MODE-A）

> 面向 Console / BOSS 前端。批次：`STORAGE-VOLUME-MODE-A`（分支 `feat/volume-mode-block`，PR #195）。
> 一句话：**建卷时新增了可选字段 `volume_mode`（默认 `filesystem`）。容器不用传；只有"给 VM 用"的卷才传 `"block"`。字段名是 `volume_mode`，不是 `volumeMode`。**

---

## 1. 背景（为什么要有这个字段）

块存储卷在底层是 Kubernetes PVC，PVC 有 `volumeMode`：

- `Filesystem`：卷被格式化成文件系统，容器按**目录**挂载（`volume_mounts`）。
- `Block`：卷是**裸设备**，给 VM 当数据盘用（guest 内以 `/dev/disk/by-id/ani-*` 呈现，支持**在线热插**，不重启 VM）。

此前 ANI 把所有卷都渲染成 `Filesystem`，导致**给 VM 挂云盘失败**：KubeVirt 对 `Filesystem` 卷做在线热插时要求卷内已有 `disk.img`，新建空盘没有该文件，virt-handler 会一直重试。改为按用途渲染后，VM 用 `block`、容器用 `filesystem`。

`volumeMode` 在 Kubernetes 上**创建后不可修改**，因此一个卷要么给 VM 用、要么给容器用，不能两用；改用途只能删卷重建。

---

## 2. 接口变更

### 2.1 创建卷 `POST /api/v1/volumes`

请求体（`CreateStorageVolumeRequest`）新增可选字段：

| 字段 | 类型 | 必填 | 默认 | 说明 |
|---|---|---|---|---|
| `volume_mode` | string，枚举 `block` \| `filesystem` | 否 | `filesystem` | 卷的用途模式 |

其余字段不变（`name`、`size_gib`、`idempotency_key` 仍必填；`storage_class` 默认 `ani-block`、`volume_type` 默认 `ssd` 都是可选）。

**请求示例（给 VM 用的卷）：**

```json
POST /api/v1/volumes
Content-Type: application/json

{
  "name": "vm-data-01",
  "size_gib": 100,
  "volume_mode": "block",
  "idempotency_key": "vol-vm-data-01-20260929"
}
```

**请求示例（给容器用的卷，可省略 `volume_mode`）：**

```json
{
  "name": "container-data-01",
  "size_gib": 20,
  "idempotency_key": "vol-container-data-01-20260929"
}
```

### 2.2 响应新增只读字段

创建（`201`）、列表 `GET /api/v1/volumes`、详情 `GET /api/v1/volumes/{volume_id}` 的卷对象新增 `volume_mode`：

```json
{
  "id": "vol_3ff555c8-...",
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "name": "vm-data-01",
  "size_gib": 100,
  "storage_class": "ani-block",
  "volume_type": "ssd",
  "volume_mode": "block",
  "state": "available",
  "reason": "observed Kubernetes PVC phase Bound"
}
```

> 前端可在卷列表/详情显示"用途：VM 数据盘 / 容器目录"，直接读 `volume_mode`。

### 2.3 列表新增 `volume_mode` 过滤与"按实例可挂载"过滤

`GET /api/v1/volumes` 新增可选查询参数：

| 参数 | 取值 | 说明 |
|---|---|---|
| `volume_mode` | `block` \| `filesystem` | 省略时不过滤；大小写不敏感；未知取值不命中任何卷 |
| `available_for_instance_id` | 实例 ID | **"选择已有云盘"推荐用法**。按目标实例过滤可挂载卷：实例必须存在且属于本租户（否则 `400 BAD_REQUEST`）；仅返回 `volume_mode` 与实例类型匹配（VM=block，其余工作负载=filesystem）且**未被任何活跃实例占用**的卷 |
| `state`（既有参数增强） | 状态名，**支持逗号分隔多值** | 如 `state=pending,available`，任一命中即返回；此前传多值会因精确匹配返回空列表 |

示例（VM 详情"挂载云盘"下拉，推荐）：

```
GET /api/v1/volumes?limit=100&state=pending,available&available_for_instance_id=inst_xxx
```

也可显式按用途筛选：`GET /api/v1/volumes?volume_mode=block`（只看 VM 盘）、`?volume_mode=filesystem`（只看容器盘）。可与 `keyword` / `search_field` / `in_use` 组合使用。

### 2.4 未变更的部分

- 卷的挂载/卸载接口、快照、扩容、自动快照策略等**签名不变**。
- 容器实例创建时的 `container.volume_mounts` / `gpu_container.volume_mounts`（`volume_id`+`mount_path`）**不变**。
- VM 实例创建时的 `vm.data_disks`（`volume_id` / `name` / `size_gib` / `volume_type` / `storage_class` / `encrypted` / `delete_on_failure` / `delete_with_instance`）**字段不变，且没有 `volume_mode`**——VM 数据盘的模式由后端自动决定。

---

## 3. 前端要做什么

### 3.1 建卷：按用途传 `volume_mode`

| 业务场景 | 前端应传 | 备注 |
|---|---|---|
| 容器 / GPU 容器按目录挂载 | 省略，或 `"filesystem"` | 默认即 `filesystem`，不传即可 |
| VM 数据盘（裸盘、在线热插） | `"block"` | **必须显式传**；不传会建出 `filesystem` 卷，之后挂 VM 会失败 |

**建议**：在"创建云盘"弹窗中增加"用途/挂载目标"选择（`VM 数据盘` / `容器目录`），映射到 `volume_mode = block` / `filesystem`，并显式随请求发出，避免用户误选后无法挽回（模式不可变）。

### 3.2 建 VM 时新建数据盘：不用传模式

`POST /api/v1/instances` 的 `vm.data_disks` 中，若只给 `name` + `size_gib`（新建盘），**后端会自动按 `block` 建卷**，前端无需也无法传 `volume_mode`。

### 3.3 建 VM 时引用已有盘：只能选 block 卷

`vm.data_disks[].volume_id` 指向已有卷时，该卷必须是 `volume_mode=block`。建议前端在"选择已有云盘"下拉里**只列出 `volume_mode=block` 的卷**，或对 `filesystem` 卷置灰并提示"该卷为容器盘，VM 需要新建 block 卷"。

### 3.4 挂载/卸载（生命周期）

`POST /api/v1/instances/{instance_id}/lifecycle`：

- `action=attach_volume`：VM 只能挂 `block` 卷；容器只能挂 `filesystem` 卷。
- 建议前端先按卷的 `volume_mode` 与实例类型做前置过滤，减少无效请求。

---

## 4. 后端校验规则（VM 与容器严格区分）

| 入口 | 规则 | 失败 HTTP / code | 提示文案（后端返回的 message，前端可再包装） |
|---|---|---|---|
| `POST /volumes` | `volume_mode` 取值合法（大小写/空格归一，仅 `block`/`filesystem`） | `400 BAD_REQUEST` | `unsupported volume_mode "xxx"` |
| 建容器/GPU 容器实例挂卷 | 卷必须 `filesystem` | `400 BAD_REQUEST` | `volume "..." is block mode but container directory mount requires volume_mode=filesystem (volumeMode is immutable; recreate the volume)` |
| 建 VM 实例引用已有数据盘 | 卷必须 `block` | `400 BAD_REQUEST` | `volume "..." is filesystem mode but vm data disk requires volume_mode=block (volumeMode is immutable; recreate the volume)` |
| `attach_volume`（VM） | 卷必须 `block` | `400 INSTANCE_LIFECYCLE_FAILED` | `volume "..." is filesystem mode but vm disk hotplug requires volume_mode=block (volumeMode is immutable; recreate the volume)` |
| `attach_volume`（容器/GPU 容器） | 卷必须 `filesystem` | `400 INSTANCE_LIFECYCLE_FAILED` | `volume "..." is block mode but container directory mount requires volume_mode=filesystem (volumeMode is immutable; recreate the volume)` |

要点：

- 校验都在**真正下发 provider 之前**完成，失败不会产生"创建了一半"的实例或卷。
- 容器建实例挂卷的校验发生在 apply 之前；`attach_volume` 校验发生在记录 operation 之前。
- 错误 message 里已包含"该卷是什么模式、需要什么模式、需要重建"，前端可直接展示，也可自行改写为更友好的中文。

---

## 5. 存量数据与兼容性（重要）

- **所有存量卷的 `volume_mode` 都是 `filesystem`**（迁移时统一回填）。它们**不能直接给 VM 用**。
- 若要给 VM 用：**删除旧卷 → 用 `volume_mode=block` 新建 → 再挂载**。模式创建后不可改，没有"改成 block"的接口。
- 容器/GPU 容器**不受影响**，继续按目录挂卷。
- 新增字段是**可选**的，老前端不传 `volume_mode` 时行为与以前一致（建出 `filesystem` 卷）。

---

## 6. 前端改造清单（checklist）

- [ ] "创建云盘"弹窗新增"用途"选择（VM 数据盘 / 容器目录），并映射为 `volume_mode`（`block` / `filesystem`）。
- [ ] 创建请求带上 `volume_mode`（容器场景可不带，建议显式带上 `filesystem`）。
- [ ] 卷列表/详情展示 `volume_mode`（用途标签）。
- [ ] 卷列表按用途筛选：`GET /api/v1/volumes?volume_mode=block|filesystem`。
- [ ] VM/容器"选择已有云盘"下拉改用 `GET /api/v1/volumes?available_for_instance_id={实例ID}`（服务端自动按实例类型过滤 block/filesystem 并排除占用中的卷；未知实例返回 400）。
- [ ] `state` 过滤如需多值直接传逗号分隔（如 `state=pending,available`），后端按 any-of 命中。
- [ ] VM/容器 `attach_volume` 前按 `volume_mode` 做前置过滤。
- [ ] 处理两类新增错误：卷创建/建实例的 `400 BAD_REQUEST`、生命周期的 `400 INSTANCE_LIFECYCLE_FAILED`，把 message 展示给用户（含"需重建"提示）。

---

## 7. 验证参考（隔离测试环境实测）

- 建卷默认 → `volume_mode=filesystem`；显式 `block` → `volume_mode=block`。
- VM + block 卷 `attach_volume` → `200`（PVC `Bound`/`mode=Block`，VMI 热插盘 `Ready`）。
- 容器 + block 卷 `attach_volume` → `400 INSTANCE_LIFECYCLE_FAILED`；VM + filesystem 卷 → `400 INSTANCE_LIFECYCLE_FAILED`。

> 契约以 `repo/api/openapi/v1.yaml` 的 `CreateStorageVolumeRequest` / `StorageVolume` 为准。