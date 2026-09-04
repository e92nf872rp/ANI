# INSTANCE-RWO-PRECHECK-B：RWO 卷占用保守预检（create / attach_volume / start / resume 409）

> 状态：已完成（本地验证通过；真实环境 live 验证见下文）
> 批次类型：Feature batch
> 前置批次：[INSTANCE-STORAGE-USAGE-A](INSTANCE-STORAGE-USAGE-A.md)（占用打标 + 过滤，本批次复用其判定 helper）
> 前端对接：对接文档线下发送前端团队（不入库）

## 1. 背景与目标

USAGE-A 批次解决了占用可见性，但保留了三个拦截缺口：

1. **创建实例时选择被占用卷**：API 放行 → 实例卡 provisioning（K8s Multi-Attach 报错），用户只能事后从事件里看到冲突
2. **运行中实例 attach_volume 被占用卷**：同样放行 → 卡 provisioning
3. **停机实例的卷被新实例接管后再启动**（2026-09-04 用户场景）：实例 A running 挂卷 → stop → 实例 B 创建/挂载同一卷并 running → 实例 A start，若调度到不同节点即 RWO Multi-Attach 冲突

本批次按既定方案实现**保守 RWO 占用预检**：命中占用直接 `409 Conflict`（`ports.ErrConflict`），错误消息携带占用实例 ID 与状态；文件系统（RWX）共享语义不参与预检；stopped/failed/deleted 实例不构成占用。store 读取失败 fail-open（不阻塞业务操作），K8s Multi-Attach controller 仍是并发竞态的最终兜底。

非目标：restart 不做预检（运行中实例自身持有卷，其他实例已被本预检挡住；重启不改变卷绑定）；孤儿 K8s Deployment（store 无记录）不参与判定；legacy mount API 的显式挂载不参与判定。

## 2. 实现

### 2.1 创建入口（resolver）

`pkg/adapters/runtime/instance_resource_resolver.go`：

- `LocalInstanceResourceResolver` 新增可选 `instances ports.WorkloadInstanceStore` 字段与 `WithWorkloadStore(store)` 链式装配方法（nil 时跳过预检，与 USAGE-A 的 nil 安全降级一致）
- `resolveStorage` 的 `checkVolume`：卷状态校验通过后调用 `ListStorageConsumers(ctx, r.instances, tenantID, "volume", volumeID)`，命中活跃消费者即返回
  `fmt.Errorf("%w: instance volume %q is occupied by instance %q (%s)", ports.ErrConflict, ...)`（新实例尚无 ID，无需排除自身）
- 覆盖全部卷入口：`spec.Storage` attachments、VM system disk / data disks、container `VolumeMounts`
- store 读取出错 fail-open（跳过预检），不阻塞创建

### 2.2 生命周期入口（applyLifecycle precheck）

`pkg/adapters/runtime/instance_service.go`：

- 新增 `volumeOccupancy`（volumeID + 占用消费者）与 `(*LocalInstanceService).volumeOccupancyConflict(ctx, record, request)`：
  - `attach_volume`：检查 `request.VolumeID`
  - `start` / `resume`：检查实例记录自身引用的全部卷（`recordVolumeIDs` 汇总 `record.Status.Storage` + `record.StorageAttachments` 中 `ResourceType=="volume"` 的去重 ResourceID）——覆盖"停机期间卷被接管"场景
  - 消费者判定复用 `ListStorageConsumers`，排除实例自身（`start` 允许从 running/starting 等活跃态出发，自身引用不算冲突）
- `applyLifecycle` 在 `transition` 之后把冲突结果传入 `lifecyclePrecheck(record, request, next, occupancy)`；`lifecyclePrecheck` 新增占用分支：`blockedLifecyclePrecheck(details, "volume_occupied_by_active_instance", volume %q is occupied by instance %q (%s))`
- 失败语义与既有 precheck 完全一致：operation 记录 `failed` + `FailureReason=volume_occupied_by_active_instance`（含 precheck step），API 返回 `ErrConflict` → Gateway 409，消息带占用实例 ID

### 2.3 装配接线

- `pkg/bootstrap/deps.go`：core 装配处 resolver 链上 `.WithWorkloadStore(instanceStore)`
- `services/ani-gateway/internal/router/instances.go`：gateway runtime 装配处同样接线（`store` 即实例 store）

## 3. 测试

### 3.1 单元测试

`pkg/adapters/runtime/instance_resource_resolver_test.go`：

1. `RejectsCreateOnVolumeHeldByActiveInstance`：running 实例持有卷 → ResolveCreate 返回 `ErrConflict` 且消息含占用实例 ID
2. `AllowsCreateOnVolumeReleasedByStoppedInstance`：stopped 实例引用同卷 → 创建通过（released 不占用）
3. `AllowsCreateOnFilesystemHeldByActiveInstance`：running 实例挂同一文件系统 → 创建通过（RWX 豁免）

`pkg/adapters/runtime/instance_service_test.go`：

4. `StartBlockedWhenVolumeTakenWhileStopped`：实例 A stopped 挂卷、实例 B running 持同卷 → `Start` 返回 `ErrConflict`（消息含 `inst-b`），operation `failed` + `FailureReason=volume_occupied_by_active_instance`
5. `StartAllowedWhenVolumeFreeAgain`：持有者也 stopped → Start 成功
6. `AttachVolumeBlockedByActiveHolder`：running 实例 C attach 被 running 实例 B 持有的卷 → `ErrConflict` + operation failure reason
7. `AttachVolumeAllowsFilesystemShared`：仅文件系统共享持有者存在 → attach 其他卷不受影响

### 3.2 验收命令

```
go build ./pkg/... ./services/ani-gateway/...          # 通过
gofmt -l <changed files>                               # 无输出
go test ./pkg/adapters/runtime/ ./services/ani-gateway/... -count=1   # ok（仅 Windows 本机 sandbox symlink 特权类用例环境性失败，与本批次无关，见已知边界）
python scripts/validate_component_imports.py --root .  # passed
python scripts/validate_inference_legacy_control_plane.py  # passed
python scripts/validate_openapi_spec_test.py           # 15 tests OK
python scripts/validate_openapi_spec.py                # 2 specs valid
git diff --check                                       # 无空白错误
```

本批次未改 OpenAPI 契约（409 与 ErrConflict 语义为既有契约行为），无生成物变更。

## 4. 真实环境 live 验证（已执行，2026-09-04）

环境：dev 集群 10.10.1.66，gateway NodePort 30080，镜像 `ani-gateway:dev-20260904-rwo-precheck-b`。

主场景验证（16 项检查，15 PASS）：

1. 创建 ani-block 卷 → `in_use/used_by` 字段返回；`GET /volumes?in_use=false` 过滤生效
2. 实例 A 挂卷创建 → Running；卷 `in_use=true`、`used_by` 含 A（USAGE-A 标记正确）
3. 停止 A → 卷 `in_use=false`（stopped 不占用）
4. 同卷创建实例 B → Running（A stopped 时不拦截，符合预期）
5. 启动 A → `409 Conflict`，消息含 B 的实例 ID（核心场景：停机期间卷被接管后再启动被拦截）
6. 删除 B → 卷 `in_use=false`；再启动 A → 200 → Running
7. 运行中实例 C attach 已被 A 占用的卷 → `409`，消息含 A 的实例 ID
8. 唯一 FAIL：新建 NFS 文件系统后创建挂载实例未走通，属文件系统挂载目标供给问题，与 RWO 预检逻辑无关（见下 RWX 专项验证改用既有文件系统）

RWX 豁免专项验证（8 项检查全部 PASS，使用既有 available CephFS `fs_db95dc76`，基线已有 5 个共享消费者）：

9. 向已占用文件系统创建实例 D1 → 201（非 409）→ Running
10. D1 运行中并发创建 D2 挂同一文件系统 → 201（非 409）→ Running
11. 文件系统 `in_use=true`、`used_by` 含 D1/D2（消费者数 5→7，USAGE-A 聚合正确）
12. 清理：D1/D2 已删除，共享文件系统保留

结论：RWO 占用预检三入口（create / attach_volume / start-resume）与 USAGE-A 占用标记在真实集群行为符合设计；RWX 文件系统共享不被预检拦截。

## 5. 已知边界

- 孤儿 K8s Deployment、legacy mount API 显式挂载不参与判定（与 USAGE-A 一致）
- store 读失败 fail-open：预检放行，由 K8s Multi-Attach 兜底（预检是体验层拦截，不是数据安全边界）
- 并发窗口：两个创建请求同抢一卷时预检都可能通过（store 中尚无对方记录），仍由 K8s 兜底
- restart 不做预检：运行中实例自身持卷、其他实例已被预检挡住；滚动重启的节点漂移由 K8s attach 顺序约束
