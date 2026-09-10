# MODEL-REPOSITORY-REMOTE-IMPORT（2026-09-03）

## 状态

本批次完成远程模型导入的本地/逻辑闭环（Task 1–6）。Gateway 入口、model-service 原子导入任务、公共 Hugging Face/ModelScope source adapter、确定性 `model.tar.gz` 归档、异步 worker、fetcher 安全解压和推理 runtime 目录接线均已落地。未执行真实集群、Docker、kubectl、Secret 读取或生产发布；不得据此标记 runtime/production ready。

## 已交付

- 模型导入 POST 路由（`/api/v1/svc/models/import`）通过 Gateway 调用 model-service gRPC，注入认证租户并返回 `202 + Location`；不修改 v1 OpenAPI/protobuf。
- model-service 以租户和幂等键原子创建 `model_import_tasks`、`async_tasks` 与 outbox 记录；同键相同请求 replay，冲突 fail-closed。
- 远程 source 仅允许公共 HTTPS，host 固定为 `huggingface.co`/`modelscope.cn`，拒绝路径穿越、redirect、非 2xx 和任何仓库凭据。
- worker 以租户隔离的对象 key 写入确定性 `model.tar.gz`，完成后注册 verified/ready model version；重复投递、租约和 fencing 可安全重放。
- fetcher 对归档执行有界解压，拒绝绝对路径、`..`、symlink/hardlink、重复项和 archive bomb，使用 sibling 临时目录 + 原子 rename，并写入完成 marker；runtime 对 archive-backed model 使用 `/models/<model-version-id>/` 并重写 `--model`/`--model-path`。
- real-k8s-lab profile 增加 `model-import-worker` Deployment 和 object-store/消息总线配置契约；当前示例填写不可变 digest，ConfigMap 与 Deployment 镜像必须保持一致。未配置发布 artifact 时应同时留空并 fail-closed，不能解析或猜测 mutable tag。

## 验证

本地已运行 remote-import validator、focused tests、archive/fetcher/runtime/model-service tests、race、vet、Go compile、OpenAPI、Services boundary 和 architecture 相关门禁（受控测试均通过）。`make validate-services`（通过本地 `python3` shim）完成 Services contract/route/SDK 生成检查后，在既有 `docs/api/core.html` 生成物漂移（`PlatformWorkloadModelMaterialization` schema 计数）处退出；本批次未覆盖或改写该生成物。完整 fetcher HTTP 测试在本沙箱因 IPv6 loopback `operation not permitted` 跳过/失败，属于环境限制，不宣称真实网络验证。

## 真实环境前置与阻塞

1. `model-import-worker` 与 `model-fetcher` 必须先构建并发布 digest-pinned 镜像；profile 中空 image 会 fail-closed，不能直接作为可运行部署。
2. model-service/worker 需要集群中已有的 `ani-services-runtime` 与 `ani-objectstore-production-shaped-runtime` Secret；本批次没有读取或复制任何 Secret 值。
3. model-fetcher→model-service gRPC 客户端现在强制使用 mTLS 配置（不再提供 insecure fallback），但 live 前仍必须预置 cert-manager CA/Certificate、每个租户 namespace 的 fetcher Role/RoleBinding，以及 9105 ingress/fetcher egress NetworkPolicy。`requester`/tenant 字段仍只是 discriminator，不是身份认证；服务端必须继续以证书 URI/工作负载 allowlist 做 tenant binding。
4. 远程 source 只接受 HTTPS。对象存储若通过 HTTP 暴露，必须显式配置受控的 insecure 例外并在 fetcher/runtime 中接线；不能因 MinIO 内网地址而默默放宽 scheme 校验。
5. 真实 PostgreSQL migration、MinIO presigned PUT/HEAD、导入→ready→推理调用、跨租户拒绝、重启幂等和长时间租约仍需独立 live gate 证据。

## 范围边界

本批次未实现 source adapter worker 的生产身份方案、加密密钥管理、Console UI、PostgreSQL/Redis 新能力、v1 契约变更或 live rollout。后续任务应先关闭上述安全/镜像/对象存储前置，再进行真实环境验证。

## 2026-09-04 integrity follow-up

- `MinIOObjectStore.PutObject` 已改为按声明的 `SizeBytes` 直接流式上传，使用有界读取器拒绝短读/超读，不再将归档通过 `io.ReadAll` 载入内存；不可重放的流式 PUT 不会在多 endpoint 间重试。为保持既有 `PutObjectInput` 兼容，`SizeBytes=0` 仍表示未知长度并采用计数流式上传，远程导入始终提供正数归档大小并受严格边界保护。
- worker 提供的 `sha256:<hex>` 会规范化为 64 位十六进制并写入签名覆盖的 `x-amz-meta-sha256`；无效摘要在发请求前拒绝。StatObject 仍只接受真实 SHA-256 header，不把 ETag（即使是 64-hex）当作模型摘要。
- 远程导入创建模型时从现有 `repo_id` 命名信号推断 capability：embedding/e5/bge/gte 等命名写入 `embedding`，其余写入显式 `text-generation`，避免空 capabilities 被 inference-service 隐式当作 generation。该推断仅使用现有请求信息，不修改 v1 OpenAPI/protobuf；来源文件级能力识别仍属于后续增强。
- Hugging Face `/resolve` 下载与 API tree 请求分离：API 仍拒绝重定向；下载仅允许 HTTPS 跳转到代码中逐项列出的官方 CDN/Xet/LFS 主机（清单对应 Hugging Face 官方 `meta.json` 的当前端点），最多 3 跳并拒绝 userinfo、HTTP 降级和未在 allowlist 的主机，重定向请求会清除凭据头。
- 本轮未执行 live/kubectl/Secret/提交。新增 object-store 流式/签名/尺寸测试和 import capability 分类测试；focused、race、vet 已通过。

## 2026-09-04 fetcher marker/config hardening

- `model-fetcher` 的归档完成 marker 现在绑定当前 `model.tar.gz` 的实际 `archive_size_bytes` 与 SHA-256；每次复用前都会从已打开的归档文件计算指纹，陈旧/伪造 marker 不会被接受，marker 使用 `Lstat` 拒绝符号链接并限制大小，内容采用严格 JSON。归档内容变化时 fail-closed，不会静默复用旧目录。
- `parseConfig` 与 `runWithDependencies` 现在都要求非空 `MODEL_OBJECT_REF/--object-ref`。fetcher 只服务 object-backed materialization；缺少对象身份时在联系 model-service 前失败，避免服务响应被无绑定地接受。普通 PVC/直接路径流程不调用该 fetcher，未破坏兼容边界。

## 2026-09-04 identity/RBAC follow-up

- 租户级 model-fetcher `Certificate` 明确声明 `usages: [client auth]`，避免 cert-manager 默认用途导致 fetcher→model-service 的双向 TLS 握手被签发为错误的 server-only 证书；静态开发 fixture 与 Kubernetes runtime renderer 保持一致。
- Gateway 不新增或扩大 ClusterRole 的 `cert-manager.io/certificates` 权限。开发 profile 增加可复制到每个租户 namespace 的最小 `Role`/`RoleBinding`：仅允许 `ani-gateway`（来自 `ani-system`）在该租户 namespace 管理 `certificates` 与 `serviceaccounts`。不授予 `secrets`、通配符或跨 namespace 权限。
- 该 Role/RoleBinding 必须由平台 bootstrap/租户创建流程预先落地；Gateway 不能在缺少权限时自举这两个 RBAC 资源。remote-import validator 对模板形状、绑定主体、命名空间和无 ClusterRole 扩张执行 fail-closed 静态门禁。未预置时，创建带对象模型 materialization 的推理服务应视为明确前置条件未满足，不得宣称 live ready。
- `model-service-fetcher-ingress` 的普通 9103 控制面入口现在仅允许 `ani-system` 中的 `ani-gateway` 与 `inference-service` Pod；9105 仍只允许带 `ani.dev/model-fetcher-client=true` 标签的 fetcher peer。validator 对两个端口的 peer/selector 执行 fail-closed 检查，避免为修复控制面调用而误开放 mTLS 下载端口。

## 2026-09-04 archive workspace policy follow-up

- `model-import-worker` 的归档策略现在通过 `MODEL_IMPORT_MAX_FILES`、`MODEL_IMPORT_MAX_TOTAL_BYTES`、`MODEL_IMPORT_MAX_FILE_BYTES` 和 `MODEL_IMPORT_MAX_OUTPUT_BYTES` 显式配置；real-k8s profile 将文件数设为 10000、三个字节上限设为 3221225472（3GiB）。
- worker 的安全默认值与 4Gi `/tmp` `emptyDir` 保持一致并预留 1Gi headroom，避免此前 `BuildArchive` 零值默认 1TiB 与工作区不匹配而反复失败。显式环境覆盖必须为正整数，且不超过策略上限、单文件不超过总量；启动在连接数据库/NATS/对象存储前 fail-closed。
- remote-import validator 要求四个限制为非 Secret 的显式值，校验正数、3Gi 上限和相互关系。该变更不修改 v1 OpenAPI/protobuf，也不改变普通 PVC/直接路径流程。

## 2026-09-04 source snapshot follow-up

- Hugging Face tree listing now follows only the original HTTPS/443 API host's `Link: rel="next"` URL, with 32-page, 10000-file and 3Gi metadata budgets; cross-host, malformed, looping and over-page links fail closed. `/api/models/{repo}/revision/{revision}` resolves a branch/tag to a strict 40-hex commit.
- `model_import_tasks.resolved_revision` is added by `20260904000100_model_import_resolved_revision.sql`. The worker persists the resolved commit before calling `List`/`Open`, and subsequent deliveries use the persisted value; if the persistence capability is unavailable, the import is terminally rejected rather than downloading an unbound mutable revision. No v1/protobuf change.
- ModelScope's public model tree API has no continuation token and is known to cap a single listing at 3000 entries. The importer now re-enumerates an oversized root by issuing bounded `Root`-scoped shallow/recursive requests, so nested directories can be recovered without accepting the truncated prefix. If a single directory still returns 3000 or more direct entries (or the request/file budget is exhausted), the worker fails closed rather than importing an incomplete model.
- ModelScope mutable revisions now resolve through the provider's documented commit-history request, `GET <ModelScope API base>/models/{repo}/commits?Ref={revision}&PageNumber=1&PageSize=1`. The adapter accepts only `Code=200`, `Success=true`, a non-empty `Data.Commit` page, and a strict 40-hex `Id`; `TotalCount` is read from the live `Data.TotalCount` shape or the legacy SDK's top-level `TotalCount` shape, while conflicting values fail closed. An already-pinned commit is returned without a network call. The one-item page is an explicit pagination budget, so an incomplete or ambiguous response fails closed. To preserve the unchanged v1 generic default (`main`) for ModelScope repositories whose provider default is `master`, an explicit empty `main` history (`Commit:null`/`[]`, `TotalCount:0`) gets one bounded fallback request for `Ref=master`; a non-empty `main` history always wins and no other ref is rewritten. The worker persists the resolved commit before `List`/`Open`, so retries use one snapshot. `List`/`Open` still reject mutable values when called outside the worker.
- The ModelScope resolver was verified with a public read-only request for a `master` revision and has focused normal/race tests for the official endpoint/query, strict envelope/page validation, malformed/unsuccessful responses, and no-network commit reuse. Resolver failures remain classified as terminal policy errors or retryable dependency errors by the worker; a live import smoke is still required.
- The Gateway's existing generic default remains `main` to avoid changing the v1/protobuf contract. The ModelScope adapter first honors a real `main` ref, then performs one bounded `master` fallback only when the `main` history is an explicit empty success response (the common provider-default case); callers may also send `revision: "master"` or an explicit 40-hex commit. No schema or protobuf field was changed in this follow-up.

## 2026-09-04 deployment image consistency guard

- The static remote-import validator now compares the `model-import-worker` Deployment image with `ConfigMap/ani-inference-materialization.data.model_import_worker_image_ref` byte-for-byte after trimming. A digest mismatch (including one side being empty) fails the profile gate, preventing a release from silently running a different worker artifact than the configured reference. This is a deployment-only guard and does not change the v1/protobuf contract or runtime behavior.

## 2026-09-04 final verification

- After the object-reference requirement was made fail-closed, the generated-client TLS integration test was updated to provide the same canonical object reference in its request and model-service response. Model-service and model-fetcher full tests, including race tests, now pass outside the IPv6-restricted sandbox; model-service vet/build/tidy and fetcher vet/build/tidy also pass.
- Remote-import validator tests (27 cases), standalone validation, Atlas checksum validation, OpenAPI, SDK Alpha, Services semantic contract, and architecture guardrails pass. No live cluster mutation, Secret read, image push, migration apply, commit, or PR was performed.
