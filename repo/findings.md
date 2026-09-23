# Findings

- 本轮只允许操作老 ANI 仓库 `/root/kubercon/ANI/repo` 和当前集群；独立 `/root/kubercon/ani-inference-service` 只读不改。
- 已完成的模型导入证据在 `development-records/live-evidence/model-repository-remote-import-live-20260921.json`。
- 现有租户推理服务使用旧 PlatformWorkload runtime；新 snapshot 版本是否能通过最新物化/fetcher 创建实例仍需验证。

- 新 snapshot 真实创建服务 `34d7238c-855a-4e37-83e3-039c7762f628` 已经走过 Gateway、inference-service、Core、model-fetcher：init `model-fetcher` 使用最新 digest 并成功物化 manifest 到模型目录；vLLM 容器随后因目标节点 GPU 仅剩约 682 MiB 而 CUDA OOM，未达到 ready。
- CPU resources 不等于强制 CPU：当前 vLLM image 自动探测节点 GPU；按既定约束不能偷偷加入 `--device=cpu`。下一次验证应显式申请现有 `rtx4090-12g-4` vGPU 规格，避免把 GPU 资源语义改成静默 CPU。
- tenant-a 的两个历史 failed inference service 都没有运行中的 Deployment/Pod：一个 desired_state=deleted 且 runtime 已清理，另一个 create 超时且 Core workload 已 deleted；不能重试旧 operation，应保留历史并用新请求验证。
- 新 snapshot 推理服务 `2127be8c-c73f-4e7e-bf4b-1de4f95d95df` 使用 Qwen 0.5B snapshot、Core capability 权威 GPU ID `gpu-nvidia-geforce-rtx-4090` 与 12285 MiB vGPU 创建成功：status=running、ready_replicas=1、generation/observed_generation=1；HTTPRoute 与 AIGatewayRoute Accepted/ResolvedRefs，真实 Envoy Chat Completions 返回 HTTP 200。
- API Key 通过 Auth/Gateway 正式创建，scope 为 `scope:inference:invoke`，推理访问策略按 service+key 绑定；调用使用 `Authorization: Bearer <ani_...>`，符合当前 Envoy ext-auth adapter 的 API key contract。
- 生产身份链路验证：从 `inference-mint` Secret 读取 caller secret 后通过 Auth gRPC 以 `CallerService=inference-service` 签发短期服务 JWT，调用 Core `/platform-workload-capabilities` 返回 200；错误 secret 返回 PermissionDenied。Pod 使用 SecretKeyRef，不把租户 Header 当作服务身份。
- ModelScope `Qwen/Qwen2.5-1.5B-Instruct` 导入完成：11 文件、manifest revision `3c3787b7c81927cc64ad45dc32ff1c9ce2a5de34`、快照内容 `3,098,973,449` 字节（约 2.89 GiB），最大权重文件 `3,087,467,144` 字节，24 个 128 MiB multipart 分片，任务 completed/100%。
- 发现并修正模型目录元数据语义：snapshot version 存储对象只是 manifest，模型 `total_size_bytes` 应为 manifest 中的逻辑内容总量；新增 `ContentSizeBytes` 写入路径及测试。model-service digest `sha256:b4144285e911772f7ee14cfcbbedbf94f08ed0511464fb703dacf7c85f59b670` 和 model-import-worker digest `sha256:52f347e56ff11ae78cd95ec92be5c1629daa00fab1cf068acad7dc2220b34e55` 已完成 rollout；新的 BAAI/bge-small-en-v1.5 导入回读 `total_size_bytes=401109582`、manifest `3925` 字节，修复已在集群生效。

- 使用 tenant-a/admin 的旧 ANI 密码登录 Gateway 后，真实回读 ready 模型 `b4dce287-ad8e-4172-a84e-4e727811c2f3`、running 推理服务 `2127be8c-c73f-4e7e-bf4b-1de4f95d95df` 和 service+API-key policy；使用该租户已有 API key 通过旧 ANI Envoy `/v1/chat/completions` 返回 HTTP 200。独立 `ani-inference-service` 未参与。
- 2026-09-22 旧 ANI 真实验证：ModelScope `BAAI/bge-small-en-v1.5`（逻辑 401,109,582 字节）导入 ready，创建 CPU embedding 服务并通过 Envoy 返回 HTTP 200、384 维向量；随后服务、策略和 API key 已删除。
- 2026-09-22 旧 ANI 真实验证：ModelScope `Qwen/Qwen2.5-3B-Instruct`（逻辑 6,183,464,937 字节、13 文件）导入 ready，创建 `gpu-nvidia-geforce-rtx-4090` 1-vGPU 推理服务，真实 Chat Completions 返回 HTTP 200；随后删除服务释放 GPU。
- 大模型首次在版本注册阶段暴露 PostgreSQL int4 溢出：`ContentSizeBytes=6183464937` 被推断成 int4。`updateModelAfterVersionSQL` 增加 bigint 显式类型后，worker 从 PostgreSQL/MinIO 快照恢复并完成注册；未重新下载 6GB 文件。
- 修复后的 `model-service` 与 `model-import-worker` 已分别 rollout 到集群；模型服务回读仍为 `ready`、逻辑大小 `6183464937` 字节，worker/model-service 当前 Pod 均为 Ready。
