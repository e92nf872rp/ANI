# INSTANCE-STORAGE-USAGE-A：存储资源占用标记与过滤

> 状态：已完成（本地验证通过；真实环境 live 验证见下文）
> 批次类型：Feature batch
> 方案来源：[design/instance-storage-usage-a.md](../design/instance-storage-usage-a.md)
> 前端对接：对接文档线下发送前端团队（不入库）

## 1. 背景与目标

块存储卷（RWO）同一时刻只能被一个活跃实例挂载，但后端没有任何占用可见性/过滤能力：实例卡 provisioning 时用户无从得知卷被谁占用。本批次（按 2026-09-04 范围决策）**只做占用可见性与过滤，不做后端拦截**：

1. `/volumes`、`/filesystems` 列表与详情响应内联返回 `in_use`（bool）与 `used_by`（占用实例列表）
2. 列表接口支持 `?in_use=true|false` 过滤（创建实例场景由前端用 `in_use=false` 过滤卷下拉）
3. 占用判定 helper 独立成型，供后续 PRECHECK-B（create/attach_volume/start/restart 预检 409）复用
4. 后端行为与现状完全兼容：不注入 store 时打标为空，无任何拦截

## 2. 实现

### 2.1 OpenAPI 契约（先改契约，再改实现）

`repo/api/openapi/v1.yaml`（全部 additive）：

- `listStorageVolumes` / `listStorageFilesystems`：新增 `in_use` query 参数
- `StorageVolume` / `StorageFilesystem` schema：新增 `in_use`、`used_by` 字段
- 新增 `StorageConsumerInfo` schema（instance_id / instance_name / kind / state / mount_path）

生成物：`frontends/console/src/api/core-schema.d.ts` 重新生成（`make gen-console-api`）。

### 2.2 占用判定 helper

`pkg/adapters/runtime/storage_consumers.go`：

- `StorageConsumer`：InstanceID / InstanceName / Kind / State / MountPath
- `ListStorageConsumers(ctx, store, tenantID, resourceType, resourceID)`：单资源形态（detail handler + 未来预检）
- `ListAllStorageConsumers(ctx, store, tenantID)`：批量形态，返回 `map["volume/vol_x"|"filesystem/fs_x"][]StorageConsumer`，list handler 每页一次扫描，避免 N+1
- 判定规则：`storage_attachments` 中 `ResourceType/ResourceID` 匹配，且实例状态 ∈ {pending, provisioning, starting, running, stopping}（attach 存在或即将存在的状态）；stopped/failed/deleting/deleted 不占用
- 遍历全部 kind：vm / container / gpu_container / inference / notebook / sandbox / batch_job
- 与方案文档的偏差记录：文档写的是 {provisioning, running}，实现补充了 pending/starting/stopping 三个 attach 存在或即将存在的过渡态，语义更准确

### 2.3 Gateway handler

`services/ani-gateway/internal/router/storage_resources.go`：

- `storageAPI` 增加 `instanceStore` 字段（可选依赖，nil 时打标恒为 false/[]，过滤为 no-op）
- `storageVolumeResponse` / `storageFilesystemResponse` 新增 `in_use`（恒输出）与 `used_by`（恒输出 `[]`，不输出 null）
- `listVolumes` / `listFilesystems`：解析 `in_use` 参数（非法值 400），批量打标后过滤
- `getVolume` / `getFilesystem`：单资源打标
- 接线：`router.go` 把 `options.GPUInstanceStore`（`ports.WorkloadInstanceStore`）传入 `registerStorageResourcesWithServiceAndTasksAndStore`
- 其余 handler（create/delete/mount/unmount/expand/snapshot）不注入 store，响应里 `in_use=false, used_by=[]`（新卷/已删卷语义正确；legacy mount API 的显式挂载不参与判定，与"不修 mount_instance_id"的非目标一致）

## 3. 测试

### 3.1 单元测试

- `pkg/adapters/runtime/storage_consumers_test.go`：
  1. 卷被 running 实例引用 → 1 个 consumer（含 mount_path）
  2. 卷仅被 stopped/deleted 实例引用 → 空
  3. 文件系统被 container + vm 两个 running 实例引用 → 全部返回
  4. 活跃过渡态（pending/provisioning/starting/running/stopping）占用、released 态（stopped/failed/deleting/deleted）不占用
  5. 批量形态：同实例双挂载去重、stopped 实例不贡献、空 resource_id 跳过
  6. nil store / 空 resourceID 安全返回
- `services/ani-gateway/internal/router/storage_consumers_router_test.go`（HTTP 级，Hertz ut）：
  1. 列表打标：running 占用卷 in_use=true（含 used_by），stopped 引用卷 in_use=false，空闲卷 used_by=[]
  2. `?in_use=false` 排除占用卷；`?in_use=true` 只含占用卷；非法值 400
  3. 详情打标
  4. 文件系统共享：container + gpu_container 双消费者，`?in_use=true` 命中
  5. nil store：全部 in_use=false、used_by=[]

### 3.2 验收命令

```
go build ./services/ani-gateway/... ./pkg/...        # 通过
go test ./pkg/adapters/runtime/ -run "TestListStorageConsumers|TestListAllStorageConsumers"   # ok
go test ./services/ani-gateway/internal/router/ -run "TestStorageVolumeList|TestStorageVolumeDetail|TestStorageFilesystemList|TestStorageOccupancy"   # ok
python scripts/validate_openapi_spec_test.py          # 15 tests OK
python scripts/validate_openapi_spec.py               # 2 specs valid
make validate-architecture                            # ✅ architecture guardrails valid
make test                                             # 见提交前记录
git diff --check                                      # 无空白错误
```

## 4. 真实环境 live 验证（待执行/执行记录）

环境：dev 集群 10.10.1.66，gateway NodePort 30080。

1. 用 ani-block 卷创建容器实例 → Running；`GET /volumes` 中该卷 `in_use=true`，`used_by` 含该实例
2. stop 实例 → 卷 `in_use` 翻转为 `false`
3. `GET /volumes?in_use=false` 包含该卷；`?in_use=true` 不包含
4. 多实例挂载同一文件系统 → `used_by` 返回多条
5. 过渡态确认：创建实例挂载被占用卷，API 不拦截（与现状一致）

## 5. 已知边界

- 孤儿 K8s Deployment（ANI store 无记录）不计入占用
- legacy Mount API 的显式挂载（`mount_instance_id`）不参与 `in_use` 判定
- 并发创建同抢一卷仍有竞态窗口（后端不拦截，K8s Multi-Attach 兜底）
- 旧实例重启撞上被占用卷的场景由后续 PRECHECK-B 在 start/restart 入口拦截
