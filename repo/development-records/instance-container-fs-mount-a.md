# INSTANCE-CONTAINER-FS-MOUNT-A

> **日期**：2026-09-16　**分支**：`hotfix/network-store-read`（fork: djm-afk）　**PR**：e92nf872rp/ANI#168　**状态**：live verified（隔离测试环境 ani-test2，镜像 tag `test2-20260916-fsmount`）
> **性质**：Feature batch（hotfix 系列，容器/GPU 容器实例共享文件系统挂载能力落地）
> **来源**：测试异常结果记录 容器/GPU 容器系列与 `特有Bug修复问题清单.md`（容器-5、GPU-4）

## 背景

容器实例与 GPU 容器实例详情页「存储与挂载」点击「挂载 NFS」提示不支持。前端提示即后端真实拒绝的透传：`KubernetesLifecycleExecutor.Apply` 把所有 `attach_filesystem`/`detach_filesystem` 无条件路由进 `applyKubeVirtFilesystem`（VM-06 修复引入的 KubeVirt virtiofs 通道），该函数入口按 `record.Kind != WorkloadKindVM` 硬拒绝——容器/GPU 容器的 Deployment 通道从未实现。服务层校验/precheck/record 持久化本就 kind 无关（`applyApprovedLifecycleSummary` 附件入库、重复 attach 拦截均已存在），缺口仅在 executor provider apply 层。容器实例即 Pod，NFS RWX PVC 直接以 volume + volumeMount 挂进 pod template 即可，无需停机/virtiofs/KubeVirt 门控。

## 修复内容

1. **按 kind 分流**：新增 `applyFilesystem` 路由——VM 走既有 `applyKubeVirtFilesystem`（停机-改 spec-开机），container/gpu_container 走新增 `applyKubernetesFilesystem`，其余 kind 明确 `ErrUnsupported`（"only supported for vm, container, and gpu_container instances"）。
2. **Deployment 定向 PATCH（attach）**：strategic-merge patch pod template——fs PVC 卷（claim 名 `storageProviderName("fs", filesystemID)`，volume 名沿用 `kubeVirtFilesystemVolumeName` 确定性推导）+ workload 容器 `volumeMount`（mount_path、read_only 同步）；容器名=Deployment 名（与 update_image 同模式），env/ports/其余卷不动；NFS PVC 为 RWX，多副本与滚动 surge Pod 可并发挂载，无 Multi-Attach 问题。
3. **delete 指令（detach）**：`$patch: delete` 按卷名删除 volumes 条目；`volumeMounts` 的 merge key 是 `mountPath` 而非 name，故 delete 指令必须携带挂载路径——mount path 优先从 record 附件（attach 时 `applyApprovedLifecycleSummary` 已入库）读取，record 缺失时回退读 live Deployment 反查同名 volume 的 volumeMount。
4. **收敛**：PATCH 触发滚动更新，由 reconciler 观测 Deployment 收敛翻转 RolloutStatus（与 scale/update_image 同机制）；服务层零改动、契约零改动（attach_filesystem 请求本就 kind 通用）。

一处修复覆盖 container 与 gpu_container（同一 executor 分支）。

## 变更文件

- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`：`applyFilesystem` 分流 + `applyKubernetesFilesystem` + `filesystemMountPathFromRecord` + `kubernetesFilesystemMountPath`
- 单测：`kubernetes_lifecycle_executor_test.go` 新增 3 用例（gpu_container attach 单次定向 PATCH 断言 claim/卷名/mountPath；container detach 双 delete 指令；record 无附件回退 live Deployment 反查 mountPath）
- 未改：OpenAPI 契约与生成物、SDK、Services 层、前端、DB schema、服务层

## 验证汇总

- 单测：`go build ./pkg/... ./services/ani-gateway/...`、`go vet`、`go test ./pkg/adapters/runtime/`（filesystem 用例全绿；Windows 下 sandbox symlink 用例为既有环境限制，CI Linux 正常）；gofmt 干净；`git diff --check` 干净
- 真实环境（ani-test2 隔离环境，gateway 镜像 `test2-20260916-fsmount`，NFS fs `fs_dfe99bff-...`，PVC Bound）：
  - **container**（nginx，实例 `fs-mount-ctl-45741`）：attach → Deployment `volumes`/`volumeMounts` 立即出现 fs PVC 卷（claim `fs-fs-dfe99bff-...`）与 `/mnt/nfs` 挂载、ready=1，record `storage_attachments` 新增 filesystem 条目（mount_path 一致、status=mounted）；detach → 卷与 volumeMount 移除、ready=1、record 附件清空、实例回 `running`
  - **gpu_container**（rtx4090-12g-4 + cuda 镜像，实例 `fs-mount-gpu-46345`）：attach/detach 集群侧与 record 断言同 container 全过，验证后实例已删除清理
- CI：PR #168 GitHub Actions 为准

## 遗留

- 整卡 GPU 规格实例创建受集群既有长跑 GPU 测试负载占用调度 Pending（与本次修复无关，环境资源问题）；GPU-4 验证改用 1/4 卡规格通过
- 契约 `attach_filesystem` 请求已 kind 通用、响应 `storage_attachments` 结构不变，前端无需新增字段对接；异常信息未解析走公共错误解析项
