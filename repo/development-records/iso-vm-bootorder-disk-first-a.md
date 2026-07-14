# ISO-VM-BOOTORDER-DISK-FIRST-A — ISO VM 系统盘优先引导（装完自重启进系统）

完成日期：2026-07-14

## 范围

修正 `boot_media=iso` 的 KubeVirt `bootOrder`：首启能进安装程序，安装程序自动重启后进入已装系统，**不依赖**删 pod / 重建 VMI / 先 `detach_volume`。

> 边界：只改 Core 渲染、Gateway 默认与 OpenAPI 说明。不修改 Services/`frontends/`，不标 production ready。`detach_volume` 仍保留为装完后可选清理。

## 根因

`ISO-CDI-VM-CDROM-DETACH-A` 把 `bootOrder=1` 只放在 `rootdisk`，ISO CD-ROM **不写** `bootOrder`。KubeVirt 只把带 `bootOrder` 的设备当作启动设备，导致：

- 刚创建、系统盘空白时：**进不了 ISO 安装程序**
- 若人为只给盘加 bootOrder、或旧实例 ISO 仍为 1：装完后又会反复进安装介质

装完后靠 `detach_volume` 改 VM 模板通常要重建 VMI，不是访客自重启语义。

## 修复

- 渲染固定：`rootdisk.bootOrder=1`，`iso.bootOrder=2`（默认；`boot_media.boot_order` 覆盖且必须 `>= 2`）
- 首启：空白盘不可引导 → 回退 ISO → 进安装程序
- 装完访客自重启：盘可引导 → 优先进系统，无需删 pod
- OpenAPI `InstanceBootMedia.boot_order`：`minimum/default` 改为 `2`，说明与上述语义对齐
- Gateway 默认 `BootMediaBootOrder=2`；传入 `< 2` 拒绝

## 验证

```bash
cd /root/kubercon/ANI/repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestRenderVMWithISOBootMedia' -count=1
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'TestDemoSpecFromRequestUsesDefaultRootDiskSizeAndBootOrderForISOBootMedia' -count=1
python3 scripts/validate_component_imports.py --root .
git diff --check
```

## 相关文件

- `repo/pkg/adapters/runtime/dryrun_renderer.go`
- `repo/pkg/adapters/runtime/dryrun_renderer_test.go`
- `repo/pkg/ports/workload_runtime.go`
- `repo/services/ani-gateway/internal/router/demo_instances.go`
- `repo/services/ani-gateway/internal/router/demo_instances_test.go`
- `repo/api/openapi/v1.yaml`
