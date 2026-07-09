# IMAGE-UPLOAD-CDI-A — Image upload ports/local adapter (Task 2) + CDI adapter/Gateway routes (Task 3)

完成日期：2026-07-09（Task 2）/ 2026-07-09（Task 3）

## Task 2 范围

本批次为 ISO/CDI image upload 补齐 Core port 与 local profile adapter：新增 `ImageImportService` 抽象，按 `api/openapi/v1.yaml` 中 `Image` / `CreateImageUploadRequest` / `ImageUploadSession` schema 对齐端口类型，并提供内存态 `LocalImageImportService`。

> 边界声明：Task 2 只实现 ports + local adapter。它不实现 CDI DataVolume adapter，不新增 Gateway `/images` routes，不实现 VM ISO boot，不修改 Services / `frontends/`，也不声明 real-provider、runtime ready 或 production ready。

## 实现内容

- 新增 `repo/pkg/ports/image_import.go`，定义 `ImageImportService`、`ImageFormat`、`ImageState`、`ImageRecord`、`ImageUploadCreateRequest`、`ImageUploadSession`、Get/List/Delete request/result 类型。
- 新增 `repo/pkg/adapters/runtime/local_image_import_service.go`，local profile 支持 `format=iso` 创建上传会话，状态为 `uploading`，返回短期 `upload_url`、`token`、`expires_at` 与 `method=POST`。
- 本期只支持 `iso`；`qcow2` / `raw` 返回 `ports.ErrUnsupported`。
- 创建请求按 `(tenant_id, idempotency_key)` 幂等重放同一上传会话；Get/List/Delete 均按 tenant 隔离，Delete 将镜像标记为 `deleted` 并从可见查询中移除。
- `repo/pkg/bootstrap/deps.go` 增加 `Capabilities.ImageImport`，默认注入 local adapter，供后续 Task 3 Gateway 接线复用。

## TDD 证据

- RED：`cd /root/kubercon/ANI/repo/pkg && go test ./adapters/runtime/ -run TestLocalImageImportCreateUploadISO -count=1` 失败，原因为 `NewLocalImageImportService`、`WithImageImportUploadBaseURL`、`ports.ImageUploadCreateRequest`、`ports.ImageFormatISO`、`ports.ImageStateUploading`、`ports.ImageGetRequest` 未定义。
- RED（扩展用例）：`cd /root/kubercon/ANI/repo/pkg && go test ./adapters/runtime/ -run 'TestLocalImageImport' -count=1` 失败，原因为新增 image import 类型/构造器未定义。
- GREEN：`cd /root/kubercon/ANI/repo/pkg && go test ./adapters/runtime/ -run 'TestLocalImageImport' -count=1` 通过。

## 注意事项

- 首次按 brief 在 `repo/` 运行 `go test ./pkg/adapters/runtime/ ...` 时，当前仓库布局不是单一 Go module，Go 未找到 main module；有效命令需在 `repo/pkg` module 根目录执行。
- local adapter 的默认上传 base URL 为 `http://127.0.0.1:31001`，与 CDI foundation NodePort 约定保持一致；测试可通过 `WithImageImportUploadBaseURL` 覆盖。
- Task 3 仍需补 CDI DataVolume/upload adapter、Gateway routes、OpenAPI handler 映射和相关 contract/runtime 验证。

## Task 2 关键文件

- `repo/pkg/ports/image_import.go`
- `repo/pkg/adapters/runtime/local_image_import_service.go`
- `repo/pkg/adapters/runtime/local_image_import_service_test.go`
- `repo/pkg/bootstrap/deps.go`

---

## Task 3 范围：CDI adapter + Gateway routes

在 Task 2 的 `ports.ImageImportService` / local adapter 基础上，补齐真实 CDI provider adapter 与 Gateway `/images*` 路由，使 Core 具备通过 CDI DataVolume/UploadTokenRequest 落地 ISO 上传的完整链路。

> 边界声明：本批次只交付 Core CDI adapter + Gateway 路由 + 运行时接线 + SDK/文档。它不实现 VM ISO boot（`boot_media`，见 Task 4），不修改 Services / `frontends/`，不新增 production-shaped live gate，不声明 production ready。当前沙箱环境没有可达的真实集群/Gateway，Step 4 现场冒烟未执行；仅有 fake REST 单测与假 HTTP transport 集成测试覆盖。

## Task 3 实现内容

- `repo/pkg/adapters/runtime/cdi_image_import.go`：新增 `CDIRESTClient` 接口（`CreateNamespacedResource` / `GetNamespacedResource` / `ListNamespacedResource` / `DeleteNamespacedResource`，只暴露通用 JSON REST 语义，Gateway/adapter 之外任何一层都不需要也不能拼 CDI YAML）与 `CDIImageImportService`：
  - `CreateUpload`：DataVolume `name` 由 `sha256(tenant_id/idempotency_key)` 派生（同键幂等，Kubernetes 本身即状态源，未新增数据库表）；manifest 带 `cdi.kubevirt.io/storage.bind.immediate.requested: "true"` 注解、`spec.source.upload`、`spec.storage.storageClassName` 默认 `ani-rbd-ssd`；随后创建 `UploadTokenRequest{spec.pvcName=<DataVolume名>}`，返回 `upload_url = CDI_UPLOADPROXY_URL + /v1beta1/upload` 与 `status.token`。
  - `Get`/`List`：读取 DataVolume，`status.phase` 映射到 `ports.ImageState`（`Pending/WaitForFirstConsumer→pending`、`UploadScheduled/UploadReady→uploading`、`Import*/Clone*→processing`、`Succeeded→ready`、`Failed→failed`）；`List` 按 `app.kubernetes.io/managed-by=ani-image-import` label selector 过滤，避免与未来 VM 系统盘 DataVolume 混淆。
  - `Delete`：删除 DataVolume 并返回 `deleting`（异步删除，不假定立即完成）。
  - `NewCDIKubernetesRESTClient`：复用 `kubernetes_rest_client.go` 的凭据解析（`ResolveKubernetesRESTClientConfig` → `kubernetesRESTHost` → `readKubernetesServiceAccountToken`/`kubernetesHTTPClient`，同包内直接复用私有函数，未重复实现）；对 CDI/upload API group 做原始 JSON REST 调用，404/409 分别映射为 `ports.ErrNotFound`/`ports.ErrConflict`。
  - 非 `iso` 格式在 `CreateUpload` 早退返回 `ports.ErrUnsupported`，不创建任何 CDI 资源。
- `repo/services/ani-gateway/internal/router/image_resources.go`：新增 `imageAPI`，注册 `GET /images`、`POST /images/uploads`、`GET /images/{image_id}`、`DELETE /images/{image_id}`；handler 只把 HTTP body/query 映射到 `ports.ImageUploadCreateRequest` / `ImageListRequest` / `ImageGetRequest` / `ImageDeleteRequest` 并转换响应，不触碰任何 CDI 细节；`writeImageError` 复用现有 `ErrNotFound/ErrConflict/ErrUnsupported/ErrInvalid → 404/409/400/400` 映射惯例。RBAC `scope:images:read/create/delete` 与 idempotency 校验均由既有通用 `RBAC`/`Idempotency` 中间件按路径/方法推断，未新增专属中间件。
- `repo/services/ani-gateway/internal/router/router.go`：`RegisterOptions` 增加 `ImageImportService` 字段，`RegisterWithOptions` 调用 `registerImageResourcesWithService`。
- `repo/services/ani-gateway/image_import_runtime.go` + `main.go` 接线：`IMAGE_IMPORT_PROVIDER=""/local` 走 `LocalImageImportService`（可选 `CDI_UPLOADPROXY_URL` 覆盖 local 上传 base URL，便于本地对齐真实 NodePort）；`IMAGE_IMPORT_PROVIDER=cdi_rest` 要求 `CDI_UPLOADPROXY_URL`，复用既有 `gatewayKubernetesRESTClientConfig` 凭据解析，构造 `CDIKubernetesRESTClient` + `CDIImageImportService`。
- `repo/deploy/isolated/business-stack.yaml`：Gateway 容器新增 `IMAGE_IMPORT_PROVIDER=cdi_rest`、`CDI_UPLOADPROXY_URL=https://192.168.102.51:31001`（沿用同文件其它 base URL 已使用的节点 IP；`cdi-uploadproxy-nodeport` 已在 CDI-FOUNDATION-A 固定 NodePort `31001`，TLS 由 CDI 自签证书提供，客户端直传自行处理信任）。
- SDK：`python3 scripts/gen_sdk_alpha.py` 重生成四语言 SDK；`sdk-metadata.json` 与各语言 client 现含 `listImages` / `createImageUpload` / `getImage` / `deleteImage` 与对应 schema。

## Task 3 TDD 证据

- RED：`cd repo/pkg && go test ./adapters/runtime/ -run TestCDIImageImport -count=1` 编译失败（`CDIRESTClient`/`NewCDIImageImportService`/`WithCDIImageImportClock`/`cdiImageNameAnnotation` 未定义）。
- GREEN：`cd repo/pkg && go test ./adapters/runtime/ -run TestCDIImageImport -count=1 -v`：6 个用例全部通过，覆盖 DataVolume+UploadTokenRequest 创建内容断言、幂等重放（不重复创建 DataVolume）、非 iso 拒绝、DV phase→ImageState 全枚举映射、Get 404、List/Delete。
- Gateway handler 单测：`go test ./services/ani-gateway/internal/router/... -run TestImageAPI -count=1 -v`：3 个用例通过（含注入 fake `ports.ImageImportService`）。
- Gateway runtime 接线单测（假 HTTP transport，验证 CDI REST 调用序列与 URL/方法）：`go test ./services/ani-gateway/... -run TestGatewayImageImport -count=1 -v`：4 个用例通过，含 `cdi_rest` 模式下 Get 404 → Create DataVolume → Create UploadTokenRequest 的完整序列断言。

## Task 3 验证

- `cd repo/pkg && go build ./... && go test ./adapters/runtime/... -count=1`：通过。
- `cd repo/services/ani-gateway && go build ./... && go test ./... -count=1`：通过。
- `cd repo && GOCACHE=<workspace 内可写目录> go test ./pkg/... ./services/ani-gateway/... ./services/auth-service/... ./services/model-service/... ./services/task-service/... ./services/reconcile-worker/... -timeout 120s`：全部 PASS（`make test` 默认硬编码 `GOCACHE=/private/tmp/ani-go-build`，本沙箱环境该路径写入被拒绝；用 workspace 内可写 GOCACHE 复现同一组 `go test` 命令后全部通过，属沙箱环境限制而非代码问题）。
- `make validate-architecture`：通过（`component import guard passed`）。
- `git diff --check`：通过。
- `make validate-sdk-beta`（含 `validate-sdk-alpha`）：通过。
- `python3 scripts/validate_yaml.py deploy/isolated/business-stack.yaml`：`validated 1 YAML files`。
- `python3 scripts/gen_sdk_alpha.py`：`SDK Alpha artifacts generated`，diff 确认新增 `listImages`/`createImageUpload`/`getImage`/`deleteImage` 与 Image schema。
- Step 4 现场冒烟：未执行。本沙箱环境 `kubectl` 因 snap 权限不可用，且无可达的真实集群/Gateway 或 `local-secrets/dev-physical-servers.md`；按任务 brief 约定，用 fake REST 单测 + 假 HTTP transport 集成测试替代，状态标记 `DONE_WITH_CONCERNS`。

## Task 3 已知问题（非本批次引入，记录以避免误判）

运行 Sprint 13 基线回归时发现以下项在本批次开始前已存在于工作区未提交改动中（均与 Images/CDI 无关，未在本批次修改涉及文件）：

- `make validate-core-beta`：`demo_instances.go` 中另一批未提交工作（VM Console/noVNC，`INSTANCE-CONSOLE-VNC-WS-A`）引入的 `NOT_IMPLEMENTED` 未在 readiness 矩阵登记。
- `make validate-core-api-compatibility`：`GET /branding` operationId 与兼容性基线不一致。
- `make validate-mock-a`：`connectInstanceConsoleSession` 缺 2xx 响应导致 Mock Server 路由构建失败。
- `make validate-doc-api`：`docs/api/core.html` 缺少某个 PUT 方法条目。

以上四项均可通过 `git stash`/对比确认发生在 `demo_instances.go`、`v1.yaml`、`docs/api/core.html` 的其它未提交批次改动中，本批次未触碰这些文件，故未在本次范围内修复，留给对应批次收口。

## 已知限制

### `DELETE /images/{image_id}` 占用检查（409）

OpenAPI 契约（`deleteImage`）声明：若镜像仍被 **Running/Provisioning** 状态 VM 的 CD-ROM 或系统盘引用，应返回 **409 Conflict**。

**当前实现（local adapter 与 CDI adapter）均未做占用检查**：`Delete` 直接删除 DataVolume/镜像元数据（local 侧标记 `deleted`），不查询 VM 是否仍挂载该 `image_id`。完整占用检测与 409 语义对齐留待后续批次。

**客户端须知**：在占用检查落地前，**不得**删除仍被活跃 VM CD-ROM/系统盘引用的镜像；否则可能导致运行中 VM 存储断链。契约已预留 409，实现尚未兑现。

## Task 3 关键文件

- `repo/pkg/adapters/runtime/cdi_image_import.go`
- `repo/pkg/adapters/runtime/cdi_image_import_test.go`
- `repo/services/ani-gateway/internal/router/image_resources.go`
- `repo/services/ani-gateway/internal/router/image_resources_test.go`
- `repo/services/ani-gateway/internal/router/router.go`
- `repo/services/ani-gateway/image_import_runtime.go`
- `repo/services/ani-gateway/image_import_runtime_test.go`
- `repo/services/ani-gateway/main.go`
- `repo/deploy/isolated/business-stack.yaml`
- `repo/sdks/core/**`（重生成）
