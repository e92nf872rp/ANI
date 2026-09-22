# STORAGE-FILESYSTEM-MOUNT-COMMAND-STORE-A — 文件存储挂载命令接口 store 回落与命令口径对齐

完成日期：2026-09-20
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
分支：`feat/storage-bucket-delete`（基于 `418e275`）
来源：前端反馈 `GET /api/v1/filesystems/fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0/mount-command` 调用失败
验证结果：定向单测 `go test ./pkg/adapters/runtime/ -run 'TestLocalStorageService'` 通过；ani-system 实测 **8 项断言全 PASS / 0 FAIL**，并补 4 例详情/挂载命令逐字对比 **4/4 identical**

## 1. 实现了什么

修复 Core 文件存储「获取挂载命令」接口在网关重启后恒返回 404 的缺陷，并把该接口的命令口径对齐 `GET /filesystems/{id}` 的 `mount_command`。

语义（修复后）：

- 记录解析改走 `lookupFilesystemRecord`（内存命中优先，未命中回落到持久层 store 并回填内存），与 `ExpandFilesystem` / `MountFilesystem` / `UnmountFilesystem` / `CreateFilesystemMountTarget` 对齐。
- 挂载目标改走 `hydrateFilesystemMountTargets`，保证 store 模式下 `mountTargets` 有数据可用。
- **命令口径与详情接口一致**：优先回放落库命令 `record.MountCommand`（挂载时生成，携带真实挂载目标 IP 与实例实际挂载点），并由新增的 `storageFilesystemMountCommandParts` 反解出 `ip_address` / `mount_path`；仅当落库命令为空（历史 NULL 行）时才按挂载目标合成。
- 记录不存在或跨租户仍返回 `ports.ErrNotFound` → 404；无凭证仍 401。

无契约变更、无 ports 变更、无 DB 迁移、无生成物变更。

## 2. 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/storage_service.go` | 修改 | `GetFilesystemMountCommand` 改为 store 解析 + 口径对齐（§3）；新增包级辅助 `storageFilesystemMountCommandParts` |
| `pkg/adapters/runtime/storage_service_store_authority_test.go` | 修改 | 在既有 `TestLocalStorageServiceMountSurvivesRestartViaStore` 内追加断言（§5） |

代码锚点：

- 新实现 [storage_service.go:1125-1157](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L1125-L1157)
- 反解辅助 [storage_service.go:2850-2860](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L2850-L2860)
- 复用 helper [`lookupFilesystemRecord` storage_service.go:483-510](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L483-L510)、[`hydrateFilesystemMountTargets` storage_service.go:514-545](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L514-L545)
- 命令生成器 [`storageFilesystemMountCommand` storage_service.go:2832-2848](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L2832-L2848)
- 落库命令写入点：`CreateFilesystem`（[storage_service.go:800](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L800)，`127.0.0.1` 占位）与 `MountFilesystem`（[storage_service.go:1075](file:///e:/go/project/ANI/repo/pkg/adapters/runtime/storage_service.go#L1075)，真实挂载目标 IP + 实例挂载点）
- 错误定义 [`ports.ErrNotFound` errors.go:8](file:///e:/go/project/ANI/repo/pkg/ports/errors.go#L8)

## 3. 修复前实现的问题（缺陷定位）

修复前 `GetFilesystemMountCommand` 只读进程内存：

```go
s.mu.RLock()
defer s.mu.RUnlock()
record, ok := s.filesystems[strings.TrimSpace(request.FilesystemID)]
if !ok || record.TenantID != request.TenantID || record.State == ports.StorageResourceDeleted {
    return ports.FilesystemMountCommand{}, ports.ErrNotFound   // ← 内存未命中即 404
}
```

- 该文件存储只存在于持久层（`storage_filesystems`）时，内存 map 未命中即 404，错误文案正是 `ports.ErrNotFound` = `capability resource not found`。
- 触发条件：网关 Pod 重启后内存 map 为空。线上 ani-system 的 gateway Pod 在报障时已重建约 39 小时，内存中不含历史文件存储。
- 这与同族已修缺陷完全同构：`ExpandFilesystem`（见 `kjs-study/修复bug/文件存储扩容问题分析与修复记录.md`）、`MountFilesystem`、`UnmountFilesystem`、`CreateFilesystemMountTarget`。

同时存在第二处不一致：修复前该接口**总是合成**挂载命令（用内存挂载目标 IP + `/mnt/<name>`），而 `GET /filesystems/{id}` 回显的是落库命令（挂载后为真实 IP 与实例实际挂载点）。即使 404 修好，两个接口对同一对象也会给出不同命令。本批次按「改为与详情一致」一并收敛。

## 4. 修复后实现（当前代码语义）

```go
record, found, err := s.lookupFilesystemRecord(ctx, request.TenantID, request.ResourceID)
if err != nil { return ports.FilesystemMountCommand{}, err }
if !found { return ports.FilesystemMountCommand{}, ports.ErrNotFound }
if err := s.hydrateFilesystemMountTargets(ctx, request.TenantID, record.FilesystemID); err != nil {
    return ports.FilesystemMountCommand{}, err
}
// 与 GET /filesystems/{id} 的 mount_command 口径一致：优先回放落库命令（挂载时生成，
// 携带真实挂载目标 IP 与实例实际挂载点）；仅落库为空（历史 NULL 行）时才按挂载目标合成。
if persisted := strings.TrimSpace(record.MountCommand); persisted != "" {
    ipAddress, mountPath := storageFilesystemMountCommandParts(persisted)
    return ports.FilesystemMountCommand{
        Command: persisted, Protocol: record.Protocol,
        IPAddress: ipAddress, MountPath: mountPath,
    }, nil
}
```

`storageFilesystemMountCommandParts` 反解 `mount -t <proto> <ip>:<export> <mount_path>`，格式不符时返回空值而不是回显猜测值。

## 5. 门禁与单测

- `gofmt -l` 两个改动文件无输出。
- `go test ./pkg/adapters/runtime/ -run 'TestLocalStorageService' -count=1` → `ok`。
- 回归测试在既有 `TestLocalStorageServiceMountSurvivesRestartViaStore` 内追加（**同一用例内构造「重启后内存为空」的新实例 `freshReader`**，复现线上场景）：
  1. `freshReader.GetFilesystemMountCommand` 必须命中 store，`Command` 非空、`Protocol == "nfs"`；
  2. `Command` 必须**逐字等于**挂载时落库的 `fsMounted.MountCommand`（锁定「与详情一致」口径）；
  3. `IPAddress` 非空且不是 `127.0.0.1`（证明带的是真实挂载目标 IP）；
  4. `MountPath == "/data"`（实例实际挂载点，而非 `/mnt/<name>` 合成值）；
  5. 不存在的文件存储 → `ErrNotFound`；跨租户 → `ErrNotFound`。

反向验证：把同一组断言跑在**修复前**的实现上，可复现同一条线上错误 `fresh GetFilesystemMountCommand() error = capability resource not found`，证明测试确实锁住了该缺陷。

> 说明：`make test` / `make validate-architecture` / `git diff --check` 按用户指示留到提交前统一执行，本记录不声明其通过。包内 `TestSandboxFileScriptsRejectSymlinks`、`TestSandboxFileScriptsAllowWorkspaceOperations` 为 Windows 本机环境限制（无 symlink 权限、`os.O_DIRECTORY` 缺失）导致的既有失败，与本次改动无关。

## 6. live 实测证据（ani-system 10.10.1.66:30080）

### 6.0 报障与修复前复现（已验证事实）

前端报障原文：

```
GET /api/v1/filesystems/fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0/mount-command
{
    "code": "NOT_FOUND",
    "message": "capability resource not found",
    "request_id": "req_5754cad1-76d9-45ef-a388-5f150cd015c2"
}
```

修复前线上探查（脚本 `repo/.tmp/probe_mountcmd.py`，`.tmp` 已 gitignore 不入库）：

| 探测 | 结果 |
|---|---|
| `GET /filesystems?limit=100` | 200，13 条，含目标文件存储 |
| `GET /filesystems/fs_f600cbd5-…` | 200，`mount_command = "mount -t ceph 10.0.0.11:/test-ly-nfs /test"` |
| `GET /filesystems/{目标}/mount-command` | **404** `NOT_FOUND capability resource not found` |
| 同批次另两个文件存储的同接口 | **同样 404**（排除单条数据特例，属接口级缺陷） |
| `storage_filesystems` / `storage_filesystem_mount_targets`（直查 PG） | 记录与 `mount_command` 存在；`fs_f600cbd5` 有两个 `status=available` 的挂载目标（`10.0.0.10`、`10.0.0.11`） |

### 6.1 部署

| 项 | 值 |
|---|---|
| 镜像 | `docker.changqingyun.cn/ani/ani-gateway:dev-20260920-mountcmd2` |
| digest | `sha256:e3ce8aa54a1f4c4242cf54ed143192bc174798876b030de54951e4791c5e5c23`（size 1588） |
| 部署前镜像 | `dev-20260920-mountcmd`（第一轮「总是合成」口径，已被本轮取代） |
| etcd 预检 | `dbSize=912990208` / `dbSizeQuota=2147483648`（约 42.5%，安全）；`revision=75417003` |
| 变更方式 | 仅 `kubectl set image -n ani-system`，**未改任何 env**（部署前后 75 个 env key 列表逐项一致） |
| rollout | `deployment "ani-gateway" successfully rolled out`；Pod `ani-gateway-67d5466-s2nmg` 1/1 Running |
| 就绪 | `GET /healthz` 首次探测即 200 |

Pod 随镜像重启 → 内存 map 为空，正是缺陷触发条件。

### 6.2 断言矩阵（脚本 `repo/.tmp/verify_mountcmd.py`，8 项）

```
login ok, token_len=862

GET /api/v1/filesystems -> 200 count=13

GET /api/v1/filesystems/fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0 -> 200
{"id": "fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0", "tenant_id": "00000000-0000-0000-0000-000000000001", "name": "test-ly-nfs", "protocol": "cephfs", "size_gib": 103, "endpoint": "local://test-ly-nfs", "performance_mode": "throughput", "mount_command": "mount -t ceph 10.0.0.11:/test-ly-nfs /test", "in_use": true, "used_by": [{"instance_id": "inst_5d1cebef-3896-48ab-a8ed-978348f4ca3d", "instance_name": "test-ly-bound", "kind": "container", "state": "running", "mount_path": "/test"}], "state": "available", "reason": "expanded by local storage profile", "dev_profile": {"mode": "local", "provider": "local-storage-service", "real_provider": false, "reason": "Core dev/local profile; provider execution is gated separately"}, "created_at": "2026-09-14T08:07:57Z", "updated_at": "2026-09-18T03:20:58Z"}

GET /api/v1/filesystems/fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0/mount-command -> 200
{"command": "mount -t ceph 10.0.0.11:/test-ly-nfs /test", "protocol": "cephfs", "ip_address": "10.0.0.11", "mount_path": "/test"}
  detail.mount_command='mount -t ceph 10.0.0.11:/test-ly-nfs /test'
  mount-command.command='mount -t ceph 10.0.0.11:/test-ly-nfs /test'  identical=True
  parsed ip='10.0.0.11' path='/test' want ip='10.0.0.11' path='/test'
  mount-command(fs_fd1901f0-7d86-4cfa-bcb1-e66f263ff579 name=test-ly-nfs) -> 200 {"command": "mount -t nfs 127.0.0.1:/test-ly-nfs /mnt/test-ly-nfs", "protocol": "nfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/test-ly-nfs"}
  mount-command(fs_014b4aa1-c96e-42f7-8b67-db64bfca0147 name=test-nfs) -> 200 {"command": "mount -t nfs 10.0.0.10:/test-nfs /test", "protocol": "nfs", "ip_address": "10.0.0.10", "mount_path": "/test"}
  mount-command(fs_c69ec271-a536-4836-aea9-f67faffaed3d name=tc-fs-20260910-full2) -> 200 {"command": "mount -t nfs 127.0.0.1:/tc-fs-20260910-full2 /mnt/tc-fs-20260910-full2", "protocol": "nfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/tc-fs-20260910-full2"}
  mount-command(fs_db95dc76-dbce-4689-b310-41debc5beaf7 name=filesystem-CephFS-dongjm) -> 200 {"command": "mount -t ceph 10.0.0.11:/filesystem-CephFS-dongjm /share", "protocol": "cephfs", "ip_address": "10.0.0.11", "mount_path": "/share"}
  mount-command(fs_e8602de0-7cd8-46b2-a49c-4b38970e62a7 name=test-instance-create-mount2) -> 200 {"command": "mount -t ceph 127.0.0.1:/test-instance-create-mount2 /mnt/test-instance-create-mount2", "protocol": "cephfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/test-instance-create-mount2"}
  mount-command(fs_9f1fa9af-0b8c-4517-8dcc-4cc66adf31ea name=test-instance-create-mount) -> 200 {"command": "mount -t nfs 127.0.0.1:/test-instance-create-mount /mnt/test-instance-create-mount", "protocol": "nfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/test-instance-create-mount"}
  mount-command(fs_306220e7-7691-483a-a643-509e88cd92ef name=filesystem-NFS-dongjm) -> 200 {"command": "mount -t nfs 10.0.0.12:/filesystem-NFS-dongjm /data", "protocol": "nfs", "ip_address": "10.0.0.12", "mount_path": "/data"}
  mount-command(fs_9e7089d4-fc8f-4ba9-bbdf-2412d4cea2e3 name=nfs-dongjm) -> 200 {"command": "mount -t nfs 10.0.0.10:/nfs-dongjm /date", "protocol": "nfs", "ip_address": "10.0.0.10", "mount_path": "/date"}
  mount-command(fs_79c3a84d-aa4d-416d-b2fe-a4178fbbee1a name=NFS-dongjm-2) -> 200 {"command": "mount -t nfs 127.0.0.1:/NFS-dongjm-2 /mnt/NFS-dongjm-2", "protocol": "nfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/NFS-dongjm-2"}
  mount-command(fs_c6890134-ebb9-46f3-9571-5072057f1c4c name=qwer) -> 200 {"command": "mount -t ceph 127.0.0.1:/qwer /mnt/qwer", "protocol": "cephfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/qwer"}
  mount-command(fs_18e81a74-4624-4813-b564-4f18f07dec2a name=test2) -> 200 {"command": "mount -t nfs 127.0.0.1:/test2 /mnt/test2", "protocol": "nfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/test2"}
  mount-command(fs_745ecdf8-fba8-4a9d-9bbe-fd4a93135d24 name=test) -> 200 {"command": "mount -t nfs 127.0.0.1:/test /mnt/test", "protocol": "nfs", "ip_address": "127.0.0.1", "mount_path": "/mnt/test"}

GET mount-command(fs_does_not_exist) -> 404
{"code": "NOT_FOUND", "message": "capability resource not found", "request_id": "req_cbd77bca-f3d1-4dd2-a3d3-f05048e08c75"}

GET mount-command(no token) -> 401

=== SUMMARY ===
ListFilesystems regression                       http=200 PASS
GetFilesystem regression                         http=200 PASS
GetFilesystemMountCommand(persisted fs)          http=200 PASS
mount-command == detail.mount_command            http=200 PASS
mount-command ip/mount_path parsed               http=200 PASS
GetFilesystemMountCommand(all listed fs)         http=n/a PASS
GetFilesystemMountCommand(missing) must 404      http=404 PASS
GetFilesystemMountCommand(no token) must 401     http=401 PASS
ALL_PASS=True
```

要点：

- 报障接口由 **404 转为 200**，且返回的 `command` 与详情接口 `mount_command` **逐字一致**、`ip_address`/`mount_path` 由落库命令反解得到（`ceph 10.0.0.11:/test-ly-nfs /test` → `10.0.0.11` / `/test`）。
- 列表中 13 个文件存储的该接口**全部 200**，证明是接口级修复而非单条数据特例。
- 负例保持正确：不存在 → 404、无凭证 → 401；列表与详情回归均 200。

### 6.3 口径一致性补充实测（4/4 identical）

脚本 `repo/.tmp/probe_mountcmd_null.py`，覆盖「落库命令为空」的边界样本：

```
fs_fd1901f0-7d86-4cfa-bcb1-e66f263ff579 name=test-ly-nfs
  detail.mount_command = 'mount -t nfs 127.0.0.1:/test-ly-nfs /mnt/test-ly-nfs'
  mount-command.command= 'mount -t nfs 127.0.0.1:/test-ly-nfs /mnt/test-ly-nfs'
  identical = True
fs_745ecdf8-fba8-4a9d-9bbe-fd4a93135d24 name=test
  detail.mount_command = 'mount -t nfs 127.0.0.1:/test /mnt/test'
  mount-command.command= 'mount -t nfs 127.0.0.1:/test /mnt/test'
  identical = True
fs_014b4aa1-c96e-42f7-8b67-db64bfca0147 name=test-nfs
  detail.mount_command = 'mount -t nfs 10.0.0.10:/test-nfs /test'
  mount-command.command= 'mount -t nfs 10.0.0.10:/test-nfs /test'
  identical = True
fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0 name=test-ly-nfs
  detail.mount_command = 'mount -t ceph 10.0.0.11:/test-ly-nfs /test'
  mount-command.command= 'mount -t ceph 10.0.0.11:/test-ly-nfs /test'
  identical = True
```

即：无论落库命令是否为空，两个接口返回的命令完全一致，「与详情一致」口径在两个分支上都成立。

## 7. 未实测项与边界

1. `127.0.0.1` 占位命令：未挂载过（或落库命令为 NULL）的文件存储仍返回 `mount -t <proto> 127.0.0.1:<name> /mnt/<name>`。这是 `CreateFilesystem` 落的占位值 / 合成兜底，**详情接口返回同样的值**（§6.3 已验证 identical），不是本接口独有偏差；但其对使用者无实际可挂载意义，属数据侧历史遗留。
2. 多挂载目标时的 IP 选择：仅影响落库命令为空、需要合成的路径；此时按 `s.mountTargets` 遍历取首个 `available` 且 IP 非空的目标，多目标下选择非确定性（与修复前行为一致，未改动）。
3. 跨租户 `mount-command` 未在测试环境实测（tenant-b 无可用凭据，历史批次同样受限）；由适配层单测覆盖 `ErrNotFound`。
4. ani-test2 未部署本批次。
5. 第一轮镜像 `dev-20260920-mountcmd`（digest `sha256:0fc3bb16fe9c3a96f4c80a54f5d94643b672512e7c437173ea3dfe9b7b284653`，修复 404 但「总是合成」命令）已被 `dev-20260920-mountcmd2` 取代，仍保留在构建机可回滚。

## 8. 遗留项

1. 前端若需要「可直接执行的挂载命令」，应由后端在挂载成功后把真实挂载目标 IP 与实例挂载点落库（`MountFilesystem` 已如此），未挂载的文件存储不应展示可执行命令——建议前端对未挂载对象隐藏或标注占位命令。
2. 历史 NULL `mount_command` 行可考虑一次性回填（按挂载目标/实例侧挂载关系推导），本批次不做。
3. ani-test2 需在下次并集部署时同步本批次改动。

## 9. 备注

- 本批次只动 2 个代码文件，未触碰 OpenAPI 契约、ports、生成物与 DB 迁移，因此无需 `validate-storage-alpha` 之外的契约类门禁。
- 同族缺陷（`capability resource not found` + memory-only 查找）在 storage 模块内已收敛：`ExpandFilesystem`（2026-09-18）、`MountFilesystem`/`UnmountFilesystem`（2026-09-18 块存储卸载批次）、`GetFilesystemMountCommand`（本批次）。