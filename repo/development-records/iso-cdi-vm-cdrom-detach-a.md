# ISO-CDI-VM-CDROM-DETACH-A — VM ISO 安装后卸载 CD-ROM

完成日期：2026-07-10

## 范围

收口 ISO 安装 VM 的重启引导问题：`boot_media=iso` 创建的 KubeVirt VM 会把 CD-ROM 作为 `bootOrder=1`，如果安装完成后不移除 ISO，重启会再次进入安装介质，而不是进入已安装到空白 root disk 的系统。

> 边界声明：本批次只补 Core runtime / Kubernetes lifecycle provider 的 CD-ROM detach 能力。不修改 Services / `frontends/`，不实现 unattended install，不标 production ready。

## 根因

- `VM-ISO-BOOT-A` 正确渲染了空白 root disk + ISO CD-ROM，但 CD-ROM disk 默认 `bootOrder=1`。
- 现有 `detach_volume` lifecycle 只更新 Core 元数据；在 Kubernetes provider 模式下不会 patch KubeVirt `VirtualMachine`。
- 因此 live VM 重启后仍按 VM spec 从 `iso` CD-ROM 启动。

## 修复

- `LocalInstanceService` 在 VM 的 `cdrom` 执行 `detach_volume` 时调用 provider lifecycle executor；普通 local data disk attach/detach 仍保持本地元数据行为。
- `KubernetesLifecycleExecutor` 支持 KubeVirt VM `detach_volume`：先 GET 当前 VM，再用 merge patch 写回删除指定 `volume_id` 后的 `spec.template.spec.domain.devices.disks` 与 `spec.template.spec.volumes`。
- 对当前 ISO 安装路径，安装完成后调用 `POST /api/v1/instances/{id}/lifecycle`，`action=detach_volume`，`volume_id=iso`，然后重启 VM 即应从 root disk 启动。

## 验证

```bash
cd /root/kubercon/ANI/repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesLifecycleExecutorDetachesVMCDROM|TestLocalInstanceServiceVMCDROMDetachCallsProviderLifecycle|TestLocalInstanceServiceVMVolumeBindingLocalProfile' -count=1
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -count=1
python3 scripts/validate_component_imports.py --root .
git diff --check
```

说明：第一次全量 runtime 测试在沙箱内因 `httptest` 无法监听本地临时端口失败；提升权限后同一命令通过。

## 相关文件

- `repo/pkg/adapters/runtime/instance_service.go`
- `repo/pkg/adapters/runtime/instance_service_test.go`
- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor.go`
- `repo/pkg/adapters/runtime/kubernetes_lifecycle_executor_test.go`
