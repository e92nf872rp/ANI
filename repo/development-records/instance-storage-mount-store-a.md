# INSTANCE-STORAGE-MOUNT-STORE-A — 存储挂载重启后 store 回落

完成日期：2026-09-02
对应 Sprint：Sprint 13/14 之间的实例链路热修复（hotfix/network-store-read）
验证结果：`go test ./pkg/adapters/runtime/` 全通（仅 Windows 本机环境必挂的 sandbox symlink 用例失败，与本次改动无关）；`go build ./...`（pkg + ani-gateway）通过；真实环境实例创建 201 验证通过

## 实现了什么

修复网关重启后实例创建挂载卷/文件系统必然报 `mount volume "...": capability resource not found` 的问题：`MountVolume`/`MountFilesystem`/`UnmountVolume`/`UnmountFilesystem` 原来只查进程内存 map，而资源解析阶段的 `GetVolume`/`GetFilesystem` 优先读 DB store，重启后内存清空导致"解析通过、挂载 404"的两个数据源不一致。

## 根因

实例创建分两阶段：`ResolveCreate` → `resolveStorage` 走 `GetVolume`/`GetFilesystem`（store 配置时优先读 DB）；`bindCreateStorage` → `MountVolume`/`MountFilesystem` 只查内存 map。写路径双写（内存+DB），读路径在 mount 侧只读内存，网关重新部署后即出现 DB 有记录、内存无记录的不一致。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/storage_service.go` | 修改 | 新增 `lookupVolumeRecord`/`lookupFilesystemRecord`（内存优先、miss 回落 store 并回填内存）与 `hydrateFilesystemMountTargets`（挂载前从 store 恢复 mount target）；四个 mount/unmount 方法改为走 helper |
| `pkg/adapters/runtime/storage_service_store_authority_test.go` | 修改 | 新增 `TestLocalStorageServiceMountSurvivesRestartViaStore` 回归测试：重启后（内存空、共享 store 有记录）MountVolume → MountFilesystem → UnmountVolume 全链路成功且挂载持久化到 store |

## 完工标准达成

- [x] `go test ./pkg/adapters/runtime/` 全通，含新增回归测试
- [x] `go build ./...`（pkg 与 ani-gateway module）通过
- [x] 真实环境（10.10.1.66，镜像 `dev-20260902-mount-store`）验证：网关 rollout 后用新 idempotency_key 创建挂载 `vol_0b7dbc7f` 的容器实例返回 201（state=provisioning，`storage_attachments` resolved，resource_refs 产出）
- [x] `make validate-architecture`、`git diff --check` 通过

## 备注

- 顺带发现（非本批次范围）：租户存储资源卷与文件系统均停在 `pending`（"observed Kubernetes PVC phase Pending"），即后端 PVC 未绑定；这解释了文件系统挂载门禁（要求 Available）拒绝创建的根因，也是卷真正可写前的潜在阻塞点，需单独批次排查 storage provider PVC 绑定链路。
- 幂等 key 行为为设计预期：修改请求体重新提交必须使用新 `idempotency_key`（网关指纹缓存 24h）。
