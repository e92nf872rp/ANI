# INSTANCE-VM-LIFECYCLE-HOTFIX-A

> **日期**：2026-09-15　**分支**：`hotfix/network-store-read`（fork: djm-afk）　**PR**：e92nf872rp/ANI#168　**状态**：live verified（隔离测试环境 ani-test2，镜像 tag `test2-20260915` 系列）
> **性质**：Feature batch（hotfix 系列，VM 真实底座生命周期能力补齐）
> **来源**：测试异常结果记录 VM 系列与 `特有Bug修复问题清单.md`（VM-02/03、VM-09、VM-07、VM-10、VM-06）

## 背景

VM 实例四类异常记录回归测试暴露生命周期操作在真实 KubeVirt 底座上的成组缺陷：数据盘新建不建卷、重启终态错误、快照回滚失败、重建未实现、NFS 文件系统挂载报错。本批次按"逐项定位根因 → 真实环境验证 → 逐项闭环"完成修复，全部经过 ani-test2 隔离环境真实验证。

## 修复内容

### 1. VM-02/VM-03：数据盘新建盘真实建卷与 PVC 映射（提交 4e6430d）

- **现象**：创建带新数据盘（无 `volume_id`）的 VM 失败，VMI 停在 `Pending`/`ErrorPvcNotFound`；实例自动启动看似未生效（实为 PVC 未建）。
- **根因**：`instance_service.go` 实例创建链路对不带 `volume_id` 的新数据盘只渲染 PVC 引用，不触发卷创建；PVC claim 名生成与实际卷 PVC 命名约定（`storageProviderName("vol", volumeID)`）不一致；默认 StorageClass 为集群不存在的 `standard`。
- **修复**：新增 `provisionVMDataDisks`（以实例创建幂等键派生卷创建幂等键，幂等创建卷后再渲染 PVC）；`dryrun_renderer.go` 修正 `vmVolumeClaimName`；`storage_renderer.go`/`storage_service.go` 默认 StorageClass 改 `ani-block`。
- **验证**：含 10Gi 新数据盘的 VM 创建成功（镜像 `test2-20260915-b`），PVC Bound、VMI Running 并分配 IP。

### 2. VM-09：重启改为 stop-等待停稳-start 确定性流程（提交 5d495e5 → 94c35cd）

- **现象**：详情页"重启"后实例终态为 stopped。
- **根因**：KubeVirt 原生 `restart` 子资源在 VM 使用 legacy `spec.running`（无 runStrategy）时只停不启；首版修复（原生 restart）不适用于本产品 VM 形态。
- **修复**：`kubernetes_lifecycle_executor.go` 重启实现为 stop → 轮询 printableStatus（2s 间隔，2min 超时）→ start；stop 的"not running"冲突按幂等忽略。
- **验证**：镜像 `test2-20260915-c` 下 VM `test-recreate` 重启成功回到 running；4 个单测覆盖。

### 3. VM-07：快照回滚接入 KubeVirt VirtualMachineSnapshot/Restore（提交 3b73bb9 + a7e8f63）

- **现象**：详情页"卷与快照"回滚快照失败；异常信息未解析。
- **根因与修复**（四层）：
  1. 原实现无真实回滚能力：新增 `applyKubeVirtSnapshot`/`applyKubeVirtRestore`，接入 KubeVirt `VirtualMachineSnapshot`/`VirtualMachineRestore` CR（快照 CR 名 = 快照记录 ID 1:1）；
  2. 快照 ID 含 `snap_` 前缀下划线不符 DNS-1123（曾致 422）：统一 `snap-` 前缀，非法字符替换为 `-`，历史记录归一化映射；
  3. 运行中回滚：KubeVirt 拒绝对运行中 VM restore，流程改为 stop → 停稳 → restore → start；Restore 完成判定用 `status.complete=true`（该 CR 无 `status.phase` 字段）；CSI RBD restore ~20Gi 实测 5m13s，超时设 15min；全程 `context.WithoutCancel` 防客户端断连把 VM 留在停机态；
  4. RBAC：`sprint13-production-shaped-gateway-rbac.yaml` 补 `snapshot.kubevirt.io` 的 snapshots/restores 操作权限（ClusterRole rules 原子列表，必须完整清单 `kubectl apply`）。
- **环境侧**：KubeVirt 需开启 `Snapshot` feature gate（与 `DeclarativeHotplugVolumes` 并存，merge patch 整表替换 featureGates）。
- **验证**：镜像 `test2-20260915-i`，Running VM 创建快照 `Succeeded` → 回滚 → `complete=true`（约 5min）→ 自动开机回到 running，VMI 正常分配 IP。

### 4. VM-10：重建（rebuild）落地（随提交 eee8b69/10ff30a 入库）

- **现象**：详情页"重建"无法执行。
- **根因**：契约、状态机、precheck、幂等均已支持 rebuild，但 K8s lifecycle executor 未实现（返回 unsupported action）。
- **修复**：新增 `applyKubeVirtRebuild`：运行中先停机停稳 → 捕获 VM CR 当前 spec → 删除 VM CR → 轮询消失 → 按原 spec 重新 apply（metadata.name/namespace 必须显式携带）→ 原为运行中则重新开机。重建语义：系统盘为 containerDisk（镜像派生、无状态），重建即"重装系统"；数据盘 PVC 不删、重建后自动重挂。
- **验证**：镜像 `test2-20260915-k`，rebuild 后 VM CR uid 变更（确认删除重建），VMI Running。

### 5. VM-06：NFS 文件系统 virtiofs 挂载（提交 eee8b69/10ff30a）

- **现象**：挂载 NFS 报错（admission 422 / QEMU 崩溃 / VM 卡停机态）。
- **根因与修复**（三层）：
  1. **virtiofs 无法热插**：共享文件系统 PVC 以 virtio 盘走 addvolume 子资源会因缺 `disk.img` 崩溃 VMI；新增 `applyKubeVirtFilesystem`：attach/detach 统一为 stop → 重写 VM spec（`domain.devices.filesystems[].virtiofs` 设备 + 对应 PVC 卷）→ start；
  2. **virtiofs tag 超限**：KubeVirt `filesystems[].name` 即 QEMU mount tag，上限 36 字节，`fs-` + 完整 UUID = 39 字节触发 "tag property must be 36 bytes or less"、QEMU 启动即崩溃、VMI 反复重建。新增 `kubeVirtFilesystemVolumeName`（去分隔符截断至 36 字节，确定性映射保证 detach 定位 attach 条目）；
  3. **停稳判定竞态**：`waitKubeVirtVMStopped` 原以"printableStatus 离开 Running"判停，优雅关闭经 `Stopping/Terminating`（VMI 仍在删）时 PATCH+start 被拒且错误被幂等忽略，VM 永久卡 stopped。改为严格等待 `printableStatus == "Stopped"`；停机后失败路径 best-effort 恢复开机。
- **环境侧**：KubeVirt 需开启 `EnableVirtioFsStorageVolumes` feature gate（v1.8.2；`VirtioFS` 为无效门控名，配置过会导致 422 "virtiofs feature gate is not enabled for PVC"）。
- **验证**：镜像 `test2-20260915-n`，attach → VM running→provisioning→running，VM spec 含 virtiofs 设备（`fs-fsdfe99bffb68c452888ceac8fe62ac73`，36 字节内）与 fs PVC 卷，PVC Bound，launcher 内 virtiofs 容器正常；detach → 设备/卷移除，VM 25s 内回 running。

## 变更文件

- `repo/pkg/adapters/runtime/instance_service.go`：数据盘真实建卷、快照 ID 命名
- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`：restart/snapshot/restore/rebuild/filesystem 五组 KubeVirt 生命周期实现
- `repo/pkg/adapters/runtime/dryrun_renderer.go`：PVC claim 命名修正
- `repo/pkg/adapters/runtime/storage_renderer.go`、`storage_service.go`：默认 StorageClass
- `repo/deploy/real-k8s-lab/sprint13-production-shaped-gateway-rbac.yaml`：KubeVirt start/stop/snapshot/restore 子资源权限
- 对应单测（executor/service/storage/renderer）
- 未改：Core OpenAPI 契约、SDK/生成物、Services 层、前端

## 验证汇总

- 单测：`go build ./...`、`go test ./pkg/adapters/runtime/`（lifecycle/filesystem/snapshot/restart/rebuild 全过；Windows 下 sandbox symlink 用例为既有环境限制）；`gofmt -l` 干净
- 真实环境：ani-test2 隔离测试环境，VM 生命周期五项操作（数据盘创建/重启/快照回滚/重建/NFS 挂载卸载）逐项 live 验证通过（镜像 tag `test2-20260915-b/c/i/k/n`）
- CI：PR #168 GitHub Actions 为准

## 遗留

- Block 模式卷热插（VM-05 遗留）：Filesystem/Block PVC 热插场景未支持
- PVC 根盘 VM 的"彻底重置根盘"重建语义待产品评估；`test-recreate` 孤儿实例记录可手动清理
- 环境侧 KubeVirt featureGates（`Snapshot`、`EnableVirtioFsStorageVolumes`）与 StorageClass 属环境配置，需在部署清单固化
