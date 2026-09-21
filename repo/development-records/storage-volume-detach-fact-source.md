# STORAGE-VOLUME-DETACH-FACT-SOURCE-A — 块存储卸载双事实源一致化

完成日期：2026-09-18
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
分支：`fix/routing-loadbalancer-bugs`
验证结果：本批次相关 `go test` 全通过，`gofmt -l` 改动文件无输出；K8s 测试环境 ani-system（10.10.1.66:30080）实测 5 项断言全 PASS，存量不一致卷 `test-ly-vol` 已实测收敛

## 实现了什么

收口块存储卸载 `POST /api/v1/instances/{instance_id}/lifecycle`（`action=detach_volume`）对"卷侧已挂载、实例侧无 attachment"的卷一律 `409 capability resource conflict: volume is not attached` 的缺陷（来源 `kjs-study/修复bug/块存储卸载问题分析与修复记录.md`，用例 块存储-4，未入库）。

真实根因：**"是否已挂载"存在两个互不交叉的事实源**。

1. Console 卷列表按**卷侧** `mount_instance_id` 非空显示"已挂载"并提供「卸载」按钮，该字段由卷级 `MountVolume` 写入 `storage_volumes`。
2. Console「卸载卷」实际发出的请求是**实例级** `detach_volume`，其预检 `lifecyclePrecheck` 只认**实例侧** `record.Status.Storage`（`storage_attachments`）。

实例维度的 attach 会回调卷级 `MountVolume` 写卷侧（`bindCreateStorage`），但实例维度 detach（`applyVolumeBinding`）只移除实例侧 attachment、**不回退卷侧**。两者一旦漂移（状态重算、网关重启、历史数据），前端认为已挂载、后端判定未挂载 → 卸载必然 409，界面卡死在「卸载」按钮且关联资源永不解除。

修复后：卷侧登记挂载的卷可被 detach 接受，且 detach 成功后**两侧同步回退**；卷侧挂载指向其它实例、以及从未挂载的卷仍返回 409（语义未放宽）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/instance_service.go` | 修改 | `instanceStorageBinder` 接口新增 `GetVolume`/`UnmountVolume`；新增 `volumeMountedToInstance`（按 `tenant_id+volume_id` 反查卷侧 `mount_instance_id` 是否指向本实例，读失败 fail-closed）与 `unmountVolumeSide`（清卷侧 mount 字段，`ErrNotFound` 容忍）；`lifecyclePrecheck` 新增 `volumeSideAttached` 入参，detach 分支由 `if !attached` 改为 `if !attached && !volumeSideAttached`，`details` 增 `volume_side_attached` 观测字段；`applyLifecycle` 预检前计算该标志，并在 detach 成功路径（移除实例侧 attachment 之后、落库之前）调用 `unmountVolumeSide`，失败记入 operation 时间线并返回错误 |
| `pkg/adapters/runtime/instance_service_test.go` | 修改 | `fakeInstanceStorageBinder` 补 `GetVolume`/`UnmountVolume` 与 `storedVolumes` 基建；新增 3 个回归用例 |

**无 OpenAPI 契约变更、无 handler 变更、无 SDK/生成物变更、无 DB 迁移。**

## 缺陷与修法

| # | 现象 | 根因 | 修法 |
|---|---|---|---|
| 块存储-4（后端主因） | 卸载卷恒 `409 volume is not attached`，卷侧仍显示已挂载、关联资源保留 | detach 预检只认实例侧 `storage_attachments`，而"已挂载"标识在卷侧 `mount_instance_id`，两者无一致性保障 | 预检认可"卷侧指向本实例"为已挂载；detach 成功后同步回退卷侧 mount 字段 |
| 块存储-4（前端辅因） | "挂载实例"列渲染 `mount_name`（假关联）、失败后不刷新 | Console 展示与请求装配问题 | **不在本批次范围**，由前端团队处理（见 bug 文档「前端（Console）」节） |

预检改动：

```go
attached := hasVolume(record.Status.Storage, volumeID)
details["volume_side_attached"] = volumeSideAttached
if request.Action == ports.WorkloadLifecycleAttachVolume && attached {
    return blockedLifecyclePrecheck(details, "volume_already_attached", "volume is already attached")
}
if request.Action == ports.WorkloadLifecycleDetachVolume {
    if isRootVolume(record.Status.Storage, volumeID) {
        return blockedLifecyclePrecheck(details, "root_volume_detach_forbidden", "root disk cannot be detached")
    }
    if !attached && !volumeSideAttached {
        return blockedLifecyclePrecheck(details, "volume_not_attached", "volume is not attached")
    }
}
```

卷侧反查与回退：

```go
volume, err := s.storage.GetVolume(ctx, ports.StorageResourceGetRequest{
    TenantID:   request.TenantID,
    ResourceID: volumeID,
})
if err != nil {
    return false
}
return strings.TrimSpace(volume.MountInstanceID) == instanceID
```

```go
_, err := s.storage.UnmountVolume(ctx, ports.StorageVolumeUnmountRequest{
    TenantID:       request.TenantID,
    VolumeID:       volumeID,
    IdempotencyKey: request.IdempotencyKey + ":unmount-volume:" + volumeID,
})
if errors.Is(err, ports.ErrNotFound) {
    return nil
}
```

设计说明：

- **attach 语义未放宽**：`volume_already_attached` 仍只看实例侧，卷侧标志不参与 attach 判断。
- **保护保留**：`root_volume_detach_forbidden`（系统盘不可卸载）原样保留且优先级高于卷侧判定。
- **跨实例不误伤**：只有当卷侧 `mount_instance_id` **等于本实例**时才接受并可回退；指向别的实例时预检仍拒绝（`volumeMountedToInstance` 返回 false），也不会去清别人的卷。
- **fail-closed**：`GetVolume` 出错（含 store 不可用、卷已删除）时按"卷侧未挂载"处理，退回修复前行为，不放大可卸载范围。
- **读路径无副作用**：`LocalStorageService.GetVolume` 的 observe-on-read 只对 `pending` 卷刷新 state/reason（30s 节流），不触碰 mount 字段。
- **幂等**：卷侧 unmount 的幂等键由生命周期 idempotency_key 派生（`:unmount-volume:<volumeID>`），与 create 路径的 `:mount-volume:` 前缀区分；生命周期整体重放由 operation store 在原路径早返回，不会重复回退。
- **失败语义**：卷侧回退失败时返回错误并写入 operation 时间线（`detach_volume` 步骤 failed、`volume_unmount_failed`），避免"实例侧已卸载、卷侧仍显示已挂载"的静默不一致；重试需换新 idempotency_key（与既有生命周期失败语义一致）。
- **不新增实体**：复用既有 `MountVolume`/`UnmountVolume` 与 `storage_volumes` 表，符合"停用两个事实源"的长期收敛方向但本次不做接口语义合并（见「遗留」）。

## 完工标准达成

- [x] 本批次相关 `go test`（`TestLocalInstanceServiceDetachVolume*` 等）全通过
- [x] `gofmt -l` 改动文件无输出
- [x] 镜像 `docker.changqingyun.cn/ani/ani-gateway:fix-20260918-blockdetach` 构建推送成功（digest `sha256:907f8be05243238a49800985787013ef18f1c713d454d8b1a1655b025dd59cb0`）
- [x] 部署至 ani-system（`kubectl set image` 未改 env → rollout 成功 → healthz 200）
- [x] 实测 5 项断言全 PASS / 0 FAIL

## 单测覆盖

| 用例 | 覆盖点 |
|---|---|
| `TestLocalInstanceServiceDetachVolumeAcceptsVolumeSideMount` | 核心回归：实例侧无 attachment、卷侧 `mount_instance_id` 指向本实例 → detach 成功（修复前 `ErrConflict`），且卷侧被 unmount 一次 |
| `TestLocalInstanceServiceDetachVolumeRejectsVolumeMountedElsewhere` | 反向：卷侧挂载指向**别的**实例 → 仍 `ErrConflict`，且不触发任何卷侧回退 |
| `TestLocalInstanceServiceDetachVolumeRollsBackBothFactSources` | 两侧一致场景：实例侧 attachment 被移除 **且** 卷侧同步回退，验证正常路径不回归 |
| `TestLocalInstanceServiceVMVolumeBindingLocalProfile`（既有） | VM attach/detach 走 provider、未注入 storage 时行为不变 |

## live 实测证据（ani-system 10.10.1.66:30080）

**前提**：Pod 随本批次镜像重启（`ani-gateway-74c7449f7b-q2lds`）；目标卷为 bug 文档记录的存量不一致卷 `test-ly-vol`（`vol_58d620be-6a6f-43e8-8b2e-9afb4c3b21c3`），卷侧 `mount_instance_id=inst_5d1cebef-3896-48ab-a8ed-978348f4ca3d`（`test-ly-bound`），实例侧 `storage_attachments` 只有文件系统 —— 即原 bug 的精确现场。

| 断言组 | 关键证据 |
|---|---|
| 卷侧已挂载、实例侧无 attachment | `GET /volumes` → 3/6 卷卷侧有 `mount_instance_id`；目标卷 `mount_name=volume-vol-58d620be…`、`in_use=false`、`used_by=[]`、`state=available`；`GET /instances/inst_5d1cebef…` → 200，`storage_attachments` 仅含 `fs_f600cbd5…`、卷不在实例侧 |
| 核心：detach 接受卷侧挂载 | `POST /instances/inst_5d1cebef…/lifecycle`（`detach_volume vol_58d620be…`）**409 → 200**，实例仍 `running`、`operation_id=a277543c-3bb2-403c-88c6-a957c375394f` |
| 卷侧字段同步回退 | 回读 `GET /volumes/vol_58d620be…` → 200，`mount_instance_id`/`mount_route`/`mount_name` 三字段全部消失，`reason=unmounted by local storage profile`，`mount_history` 追加 `{"at":"2026-09-18T03:52:16Z","action":"unmount","result":"success","target":"inst_5d1cebef…"}` |
| 已回退后再次 detach | 同卷再次 detach → **409** `volume is not attached`（语义未放宽，未变成"任意卷都能卸载"） |
| 从未挂载的卷 | 对未挂载卷 `vol_fc7f524b…` detach → **409** `volume is not attached` |
| 非本实例 | 卷侧挂载指向其它实例 / 不存在的实例 → 非 200（400 `no rows in result set`，实例不存在先于预检失败） |

原始请求/响应 JSON 逐字记录在 `kjs-study/修复bug/块存储卸载问题分析与修复记录.md`「实测记录」。

## 备注

- **实测即数据清扫**：本批次不新增迁移，存量不一致卷通过对其执行一次 detach 收敛（`test-ly-vol` 已在实测中收敛，卷侧 mount 字段清空并留 `unmount` 历史）。
- **其余卷侧挂载卷（只读探针）**：`tc-vol-20260910-full2`（`inst_bcf8757b…`）、`test-ly-container`（`inst_c90198c8…`）的卷侧挂载均**有**实例侧 attachment 对应（两侧一致），且实例已是 `state=deleted`，不属本 bug 场景，本次未触碰（只做只读 `GET` 取证）。
- **长期收敛（未做）**：把卷侧 `mount_instance_id` 收敛为实例侧 `storage_attachments` 的唯一事实源（卷级 `MountVolume` 与实例级 `AttachVolume` 语义合并）属接口语义收敛，改动面大，按 bug 文档建议留作独立批次。
- **前端遗留**：卷列表「挂载实例/关联资源」列仍渲染 `mount_name`（误导性假关联），卸载失败后不刷新纠正 —— 属 Console 侧改造点，后端已提供权威字段 `in_use`/`used_by`/`mount_instance_id`。
- **构建方式**：沿用"整体覆盖构建机源码树"路径——本地打包 `go.work`/`go.work.sum`/`pkg`/`services/ani-gateway` 上传后解包覆盖 `/root/ani-build`，`nohup` 后台构建 + 以 `digest:` 行/`docker images` 判断完成（命中层缓存，数分钟内完成）。
- **ani-test2 未部署**（本批次只部署 ani-system）。
- **流程文档**：本批次为 bug 收口类改动（无新能力边界/API 路径/live gate 定义），按 `CLAUDE.md` §6.3 仍按 Feature batch 更新四文件（本文件 + `README.md` + `CURRENT-SPRINT.md` + `ANI-06-开发计划.md`）。