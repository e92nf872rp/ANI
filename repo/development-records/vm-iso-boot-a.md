# VM-ISO-BOOT-A — CreateInstance boot_media=iso（Task 4）

完成日期：2026-07-09

## 范围

在 Task 1-3（CDI foundation + Images API）之上，补齐 VM `boot_media=iso` 创建路径：CD-ROM 引导 + 空白系统盘，不破坏既有 `containerDisk` 路径。

> 边界声明：本批次只交付 Core planning/renderer/Gateway 边界内的 `boot_media=iso` 支持。它不实现 `boot_media.type=disk_image`（保留，返回 `ErrUnsupported`）。空白系统盘由 VM `spec.dataVolumeTemplates`（CDI `source.blank` + `ani-rbd-ssd` + `cdi.kubevirt.io/storage.bind.immediate.requested` 注解）在 apply 时创建；root volume 使用 `dataVolume.name`（非预置 PVC `claimName`）。CD-ROM 仍引用既有 Ready `Image` 的 PVC/DV。不修改 Services / `frontends/`，不声明 real-provider、runtime ready 或 production ready。live ISO 端到端（upload→Ready→create VM→noVNC 安装界面）尚未在真实集群 runtime 验证。

## 实现内容

### ports（`repo/pkg/ports/workload_runtime.go`）

- 新增 `StorageAttachmentCDROM StorageAttachmentKind = "cdrom"`，对齐 OpenAPI `InstanceRecord.volumes[].kind` 已有的 `cdrom` 枚举值。
- 新增 `VMBootMediaType`/`VMBootMediaISO`。
- `VMInstanceSpec` 增加 `BootMedia VMBootMediaType`、`BootMediaImageID string`、`BootMediaBootOrder int32`；`BootImage` 保留不变，两者语义互斥（由 planning 校验）。

### Renderer（`repo/pkg/adapters/runtime/dryrun_renderer.go`）

- `vmVolumes`/`vmDisks` 在 `spec.VM.BootMedia == VMBootMediaISO` 时分流到新增的 `vmISOVolumes`/`vmISODisks`；`renderVM` 同时产出 `dataVolumeTemplates`（`vmISODataVolumeTemplates`）：
  - root disk：`dataVolumeTemplates[0]` 使用 CDI `source.blank`，`storageClassName` 默认 `ani-rbd-ssd`（可由附件 `StorageClass` 覆盖），`storage.resources.requests.storage` 取自 `RootDisk.SizeGiB`，带 `cdi.kubevirt.io/storage.bind.immediate.requested: "true"` 注解；root volume（`rootdisk`）使用 `dataVolume.name`（与 template 名一致，取 `attachment.Name` 或 `<instance>-root`）。
  - cdrom 存储附件（`StorageAttachmentCDROM`）渲染为 `persistentVolumeClaim`（名为 `iso`），claim name = `attachment.SourceRef`（即 Ready `Image.id`），磁盘设备用 `cdrom.bus=sata` + `bootOrder`（默认 1，可由 `boot_media.boot_order` 覆盖）。
  - 该分支绝不产出 `containerDisk`/`containerdisk`，也不读取 `spec.VM.BootImage`。
- 既有 `containerDisk` 路径（`BootMedia` 为空）完全未改动。

### Planning（`repo/pkg/adapters/runtime/planning.go`）

- 新增 `PlanningRuntime.imageImport ports.ImageImportService` 字段与 `WithImageImportService(...)` option（未注入时跳过 Ready 校验，与现有 `WithGPUInventory` 的可选依赖模式一致）。
- 新增 `validateVMBootMedia`：
  - `BootMedia==""`：沿用原有 `BootImage` 必填校验。
  - `BootMedia==iso`：`BootImage` 必须为空（互斥）；`BootMediaImageID` 必填；`RootDisk.SizeGiB` 必须为正；若注入了 `ImageImportService`，调用 `Get` 校验 `State==ready`，否则返回 `ErrConflict`（未注入时只做结构校验，交给后续 provider apply 阶段兜底）。
  - 其它值：`ErrUnsupported`（覆盖 `disk_image` 等未实现类型）。
- `normalizeStorageAttachments`：当 `spec.Storage` 为空且 `spec.VM.BootMedia==iso` 时自动补一个 `cdrom` 附件（`SourceRef=BootMediaImageID`），供只设置 `VM` 字段、未显式填 `Storage` 的调用方（如渲染器单测）使用。
- `validateStorageAttachments`：`cdrom` 附件必须有 `SourceRef`（image_id），否则 `ErrInvalid`。

### Gateway（`repo/services/ani-gateway/internal/router/demo_instances.go`）

- `demoCreateInstanceRequest` 新增 `boot_media`（`demoBootMediaRequest{type, image_id, boot_order}`）与 `root_disk_size_gib`。
- `demoSpecFromRequest` 的 VM 分支：
  - `boot_media.type=iso` 时校验与 `boot_image` 互斥、`image_id` 必填；`root_disk_size_gib` 未传时默认 `40`（非正数报错）；`boot_order` 未传默认 `1`；构造 `Storage=[root_disk, cdrom]` 与 `VMInstanceSpec{BootMedia, BootMediaImageID, BootMediaBootOrder, RootDisk}`，不再默认容器盘镜像。
  - `boot_media.type=disk_image` 返回 `ports.ErrUnsupported`（本期不实现）。
  - 未传 `boot_media` 时完全沿用原 `containerDisk` 默认镜像路径，行为不变。
- `newDemoInstanceAPIWithOptions`/`registerDemoInstancesWithObservability`（及 `router.RegisterOptions`/`RegisterWithOptions`）新增 `ports.ImageImportService` 形参，转给 `PlanningRuntime`（`WithImageImportService`），使 Gateway 已接线的 `IMAGE_IMPORT_PROVIDER=local|cdi_rest`（Task 3）在创建 ISO 引导 VM 时自动校验 Image Ready 状态；`main.go` 无需改动（`RegisterOptions.ImageImportService` 已在 Task 3 接好）。

## TDD 证据

- RED：`cd repo/pkg && go test ./adapters/runtime/ -run TestRenderVMWithISOBootMediaUsesCdromAndBlankRoot -count=1` 编译失败（`ports.VMBootMediaISO`/`VMInstanceSpec.BootMedia`/`BootMediaImageID` 未定义）。
- GREEN：同命令通过；渲染内容包含 `VirtualMachine`、`dataVolumeTemplates`（`source.blank`）、root volume `dataVolume.name`、`cdrom`、`persistentVolumeClaim`（image_id）、`bootOrder: 1`、`rootdisk`，且不含 `containerDisk`/`containerdisk`。（审查修复后测试另断言 `storageClassName=ani-rbd-ssd` 与 GiB 请求值，见下文「审查修复」。）
- Planning 新增用例（`planning_test.go`）：`TestPlanningRuntimeRejectsVMWithBootImageAndBootMedia`、`TestPlanningRuntimeRejectsVMISOBootMediaWithoutImageID`、`TestPlanningRuntimeRejectsVMISOBootMediaWithoutRootDiskSize`、`TestPlanningRuntimeCreatesVMWithISOBootMediaWhenImageReady`、`TestPlanningRuntimeRejectsVMISOBootMediaWhenImageNotReady`、`TestPlanningRuntimeRejectsVMISOBootMediaWhenImageNotFound`（用 fake `ImageImportService` 覆盖 Ready/Uploading/NotFound）。
- Gateway 新增用例（`demo_instances_test.go`）：`TestDemoSpecFromRequestBuildsISOBootMediaCdromAndBlankRoot`、`TestDemoSpecFromRequestRejectsBootImageAndBootMediaTogether`、`TestDemoSpecFromRequestRejectsISOBootMediaWithoutImageID`、`TestDemoSpecFromRequestRejectsUnsupportedDiskImageBootMedia`、`TestDemoSpecFromRequestUsesDefaultRootDiskSizeAndBootOrderForISOBootMedia`、`TestDemoInstanceServiceCreatesVMWithISOBootMediaRendersCdromManifestNotContainerDisk`（端到端走 `api.service.Create` 断言渲染 manifest）、`TestDemoInstanceServiceRejectsISOBootMediaWhenWiredImageNotReady`（注入 `fakeImageImportService` 验证 Gateway→Planning→ImageImportService 接线，未 Ready 返回 `ErrConflict`）。
- 命令：`cd repo && go test ./pkg/adapters/runtime/ ./services/ani-gateway/internal/router/ -run 'ISO|BootMedia|Cdrom' -count=1` 全部通过（需在各自 module 根目录下分别执行，因 `pkg` 与 `services/ani-gateway` 是独立 Go module）。

## 验证

- `cd repo/pkg && go build ./... && go test ./... -count=1`：全部通过（无回归）。
- `cd repo/services/ani-gateway && go build ./... && go test ./... -count=1`：全部通过（无回归）。
- `cd repo/cli/ani && go build ./...`、`services/auth-service`、`services/model-service`、`services/reconcile-worker`、`services/task-service`、`tools/kms-sm4-live-fixture`：均通过（`VMInstanceSpec`/`StorageAttachmentKind` 无其它消费者）。
- `python3 scripts/validate_component_imports.py --root .`：`component import guard passed`。
- `git diff --check`：通过。
- `make test-go`（`GOCACHE`/`GOMODCACHE` 用 `required_permissions:["all"]` 让 Makefile 硬编码的 `/private/tmp/ani-go-build` 可写）：`go test ./pkg/... ./services/ani-gateway/... ./services/auth-service/... ./services/model-service/... ./services/task-service/... ./services/reconcile-worker/...` 全部 PASS。
- `make validate-architecture`（等价 `python3 scripts/validate_component_imports.py`）：通过。
- 本次未改动 OpenAPI（`boot_media`/`root_disk_size_gib`/`InstanceBootMedia`/`cdrom` 枚举已在此前设计批次写入 `api/openapi/v1.yaml`），因此未重新生成 SDK。
- Step 3 现场冒烟（真实集群 `boot_media=iso` 创建 + `kubectl get vm,vmi,pvc` + noVNC 安装界面）：**未执行**。本沙箱环境 `kubectl` 因 snap 权限不可用（`snap-confine ... cap_dac_override`），且无 `local-secrets/dev-physical-servers.md` 或其它可达真实集群凭据；按任务 brief 约定，用单测覆盖 renderer/planning/gateway 三层后标记 `DONE_WITH_CONCERNS`。

## 已知限制

1. 空白系统盘由 VM manifest 内 `dataVolumeTemplates`（CDI `source.blank`）在 KubeVirt/CDI apply 时创建，不再假定预置 root PVC 已存在；renderer 职责止于 manifest 正确性（与既有 dry-run renderer 定位一致）。
2. `boot_media.type=disk_image` 按 brief 要求保持未实现（`ErrUnsupported`），不臆造 qcow2/raw 直启路径。
3. **live ISO 端到端未 runtime 验证**：upload→`state=ready`→`boot_media=iso` 创建 VM→noVNC 安装界面整条链路未在真实集群跑通；仅有 renderer/planning/gateway 三层单测证据。不得据此声称 real-provider、runtime ready 或 production ready。
4. **`DELETE /images/{image_id}` 占用 409 未实现**（契约已声明，local/CDI adapter 均不检查 VM 占用）：见 [`image-upload-cdi-a.md`](./image-upload-cdi-a.md)「已知限制」；客户端在占用检查落地前不得删除仍被活跃 VM CD-ROM 引用的镜像。

## 审查修复（2026-07-09，同日）

审查发现 Critical 问题：ISO 路径原实现只让 `rootdisk` 引用一个假定已存在的 PVC `claimName`，从未产出任何"真实创建该 PVC"的资源；真实集群 apply 会因 claim 不存在而失败。

修复（`repo/pkg/adapters/runtime/dryrun_renderer.go`）：

- `renderVM` 在 `BootMedia==iso` 时，在 `VirtualMachine.spec` 增加 `dataVolumeTemplates`（新增 `vmISODataVolumeTemplates`）：`source.blank` + `storage.resources.requests.storage` 取自 `root_disk` 存储附件的 `SizeGiB`（`sizeGi(...)`）+ `storageClassName` 默认复用 `cdi_image_import.go` 已有的 `defaultCDIStorageClass`（`ani-rbd-ssd`，附件可用 `StorageClass` 覆盖）+ `cdi.kubevirt.io/storage.bind.immediate.requested: "true"` 注解（复用既有 `cdiImmediateBindAnnotation` 常量，与 Task 3 CDI upload DataVolume 一致）。
- `vmISOVolumes` 的 `rootdisk` volume 从 `persistentVolumeClaim.claimName` 改为 `dataVolume.name`（新增 `vmISORootDiskName` 保证与 `dataVolumeTemplates[0].metadata.name` 一致，取 `attachment.Name` 或 `<instance>-root`）。
- ISO cdrom volume（`iso`，`persistentVolumeClaim.claimName = BootMediaImageID`/`SourceRef`）保持不变——仍直接引用既有 Ready `Image` 的 PVC/DV，不重新创建。
- `containerDisk`/`containerdisk` 仍绝不在 ISO 分支出现；既有 `containerDisk` 路径未改动。

Important（已由 controller 裁决保留）：OpenAPI `root_disk_size_gib` 描述保留"未传时服务端默认 40"语义不变，只将措辞从"或由服务端默认"改为明确"未传时服务端默认 40"，不新增 Gateway 端拒绝校验。

测试更新：`dryrun_renderer_test.go`（`TestRenderVMWithISOBootMediaUsesCdromAndBlankRoot`）与 `demo_instances_test.go`（`TestDemoInstanceServiceCreatesVMWithISOBootMediaRendersCdromManifestNotContainerDisk`）新增断言：`dataVolumeTemplates` 存在且 `spec.source.blank`、`storageClassName=ani-rbd-ssd`、`storage.resources.requests.storage` 等于请求的 GiB、root disk volume 使用 `dataVolume.name` 而非 `persistentVolumeClaim`，cdrom/bootOrder/无 containerDisk 断言保留。

验证：`cd repo/pkg && go test ./adapters/runtime/ -run 'ISO|BootMedia|Cdrom' -count=1` 与 `cd repo/services/ani-gateway && go test ./internal/router/ -run 'ISO|BootMedia|Cdrom' -count=1` 全部 PASS；`go test ./...`（两个 module）无回归；`git diff --check`、`python3 scripts/validate_component_imports.py --root .` 通过。

## 关键文件

- `repo/pkg/ports/workload_runtime.go`
- `repo/pkg/adapters/runtime/dryrun_renderer.go`
- `repo/pkg/adapters/runtime/dryrun_renderer_test.go`
- `repo/pkg/adapters/runtime/planning.go`
- `repo/pkg/adapters/runtime/planning_test.go`
- `repo/services/ani-gateway/internal/router/demo_instances.go`
- `repo/services/ani-gateway/internal/router/demo_instances_test.go`
- `repo/services/ani-gateway/internal/router/router.go`
