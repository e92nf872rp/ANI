# VM-NOVNC-USB-TABLET-A — KubeVirt VM 默认注入 USB tablet

完成日期：2026-07-14

## 范围

修复 ANI Core 创建 KubeVirt VirtualMachine 时 noVNC 鼠标坐标偏移的问题。Core 在 VM `domain.devices.inputs` 中默认注入 USB tablet absolute pointer：

```yaml
inputs:
  - type: tablet
    bus: usb
```

> 边界：只修改 Core VM manifest 渲染；不修改前端、Services、KubeVirt VNC WebSocket proxy 或 VM console API。该修复为 local/logic verified，不声明 production ready。

## 根因

浏览器 noVNC 的坐标是绝对位置；如果 VM 里只有传统 PS/2 相对鼠标，虚拟机内指针容易和浏览器指针产生偏移。KubeVirt 图形控制台 VM 通常应配置 tablet absolute pointer。

## 修复

- `vmDevices` 默认输出 `inputs: [{type: tablet, bus: usb}]`
- 保持原有 `disks`、`interfaces`、`networks`、console/VNC proxy 配置不变
- 新增幂等合并 helper：已有 `tablet/usb` 时不重复追加
- 覆盖普通 VM 渲染路径；ISO VM 复用同一 `vmDevices`，因此同步生效

## 验证

```bash
cd /root/kubercon/ANI/repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestKubernetesDryRunRendererRendersVMWithUSBTabletInput|TestAppendUSBTabletInputDoesNotDuplicateExistingTablet|TestRenderVMWithISOBootMedia|TestKubernetesDryRunRendererRendersVM' -count=1
git diff --check
```

## 相关文件

- `repo/pkg/adapters/runtime/dryrun_renderer.go`
- `repo/pkg/adapters/runtime/dryrun_renderer_test.go`
