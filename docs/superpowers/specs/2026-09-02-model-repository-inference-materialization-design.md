# 模型仓库到推理运行时物化设计

日期：2026-09-02
状态：已获用户确认，待实现

## 目标

让推理服务只使用模型仓库中已经登记、校验并归属当前租户的模型版本。模型文件以 MinIO 对象为权威来源，启动推理 Pod 时下载到租户 PVC，校验通过后才启动 vLLM/SGLang。现有 `pvc://` 实验路径继续兼容，但不再作为对象模型闭环的验收路径。

## 范围

- 复用集群现有 MinIO 和 model-service 的预签名上传/下载能力。
- 推理服务创建仍只提交 `model_version_id`，不接受任意下载地址。
- 仅允许 model-service 返回的当前租户、ready、不可变版本进入运行时。
- 新增对象模型的下载物化、大小/SHA-256 校验、失败闭环和重启复用。
- 保留现有 PVC 版本启动行为。
- ModelScope/HuggingFace 异步导入不在本切片实现，仍返回明确的未实现错误。

## 目标链路

```text
模型仓库 HTTP/Gateway
  ├─ 创建模型元数据
  ├─ 申请 MinIO 预签名 PUT
  ├─ 上传文件
  └─ 登记并校验不可变版本
          ↓
inference-service GetModelVersion（租户边界）
          ↓
model-service GetModelDownloadURL（仅 init-container 请求者）
          ↓
Core workload 的受限 model-fetcher init-container
          ↓
租户 PVC /models/.staging/<version>
  下载 → fsync → SHA-256/size 校验 → 原子 rename
          ↓
vLLM 从 /models/<version> 启动
          ↓
健康检查、任务 smoke、Envoy AI Gateway 发布
```

下载地址只作为 init-container 的短期运行时输入，不写入公开 API、数据库、日志或长期 Deployment 环境变量。下载器必须拒绝重定向、非 HTTPS（本地 MinIO 明确配置为 HTTP 时使用集群内受控地址）、超大文件、校验不匹配和跨租户对象。

上传时客户端应在 `getModelUploadURL` 请求中提供已计算的
`checksum_sha256`。响应会返回非敏感的 `upload_headers`，客户端必须将其中的
`x-amz-meta-sha256` 原样附加到 MinIO PUT；版本登记会重新读取对象大小和 SHA-256，
不会把 ETag（通常是 MD5）当作 SHA-256。为兼容旧客户端，checksum 字段保持可选，
但省略时若 MinIO 没有真实 SHA-256 checksum，版本登记会 fail-closed。

## 组件边界

### model-service

继续负责模型目录、版本状态、对象大小和 checksum 的权威校验。`GetModelDownloadURL` 只允许 `requester=init-container`，并且再次检查租户、ready 状态和 canonical `object://models/...` 路径。`requester` 只是协议辨识字段，不是身份认证；进入真实 E2E 前，model-fetcher 与 model-service 之间必须接入 mTLS 或等价的 workload identity 验证。

### inference-service

Catalog 解析模型版本后生成不可变的物化描述：版本 UUID、对象引用、目标文件名、期望大小和 SHA-256。它不保存或转发长期有效的 MinIO 凭据。PVC 版本继续生成现有 artifact；对象版本生成受限的 model-materialization 请求。

### Core runtime

在 `PlatformWorkloadCreateSpec` 增加 provider-neutral 的 model materialization 描述，由 Kubernetes runtime 渲染 publisher-owned init-container、共享 PVC 挂载和校验环境。Core 负责 Kubernetes 资源生命周期，不负责模型目录或租户业务判定。现有 `object://` 不能被静默忽略；没有可用物化配置时必须 fail closed。

### model-fetcher

使用独立、digest-pinned 的小镜像和只读配置，调用 model-service 获取短期下载地址，把单个模型文件写入共享 PVC。下载器不输出 URL、Authorization、对象 key 或 token；错误只返回稳定的错误码。重复启动发现已存在且 checksum 匹配的目标时直接成功，checksum 不匹配时删除临时文件并失败。

## 数据与安全约束

- `model_version_id` 是唯一产品引用；对象路径只能由 model-service 解析，禁止客户端把任意 `object://` 传给 Core。
- 目标目录按 tenant/model/version UUID 隔离，Pod 只挂载当前租户 PVC。
- init-container 的服务身份只允许调用 model-service 的下载描述接口，不能读取其他租户版本。
- 临时目录和最终文件使用同一 PVC；采用随机 staging 名、文件锁和原子 rename，避免并发副本看到半文件。
- vLLM 主容器依赖 init-container 成功，不得在下载失败时回退到网络下载或默认模型。
- 日志只记录稳定 reason、version UUID 和阶段，不记录 signed URL、AK、Bearer、对象 key 或数据库连接串。

## 失败与恢复

- 版本不存在、非 ready、跨租户、对象不存在、下载失败、大小/SHA 不匹配：operation 失败，vLLM 不启动。
- Pod 重启：已校验文件复用；未完成 staging 文件清理后重试。
- 多副本：每个 Pod 独立确认同一不可变版本；不共享跨租户目录。
- 模型版本被删除或变更：已创建服务继续使用冻结版本；新建服务重新解析并拒绝不可用版本。
- `pvc://` 版本仍按旧逻辑运行，不经过 model-fetcher。

## 验收

1. 真实 MinIO 上传并登记一个 tenant-a 模型版本，model-service 返回 ready 版本和预签名下载地址。
2. 创建对象版本推理服务，Pod init-container 下载并校验文件，vLLM Ready。
3. Pod 重启后不重复破坏已校验文件，服务仍可用。
4. 修改对象内容或 checksum 后，创建/启动失败且 vLLM 不启动。
5. tenant-b 使用 tenant-a 的 version ID 或对象引用得到 404/失败，不产生下载。
6. 通过 Envoy AI Gateway 使用 AK 调用 chat/embedding；验证 200、401、429 和 `Retry-After`。
7. 原有 `pvc://` 推理服务测试继续通过。

## 明确不做

- 本切片不实现 ModelScope/HuggingFace 导入 worker。
- 不把 MinIO SDK 引入 inference-service 内部业务包。
- 不删除现有 PVC 模型或直接修改其他租户资源。
