# Progress

- 2026-09-21: 创建本轮剩余验收计划，尚未开始新的集群变更。
- 2026-09-21: 创建 snapshot 推理服务；model-fetcher 物化成功，vLLM 因节点 GPU 资源不足 OOM，记录为资源选择问题，不修改代码。
- 2026-09-21: 读取 GPU spec，确认 `rtx4090-12g-4` 可用；历史失败服务审查完成，均无活动 runtime。
- 2026-09-21: 使用 Core 权威 GPU capability `gpu-nvidia-geforce-rtx-4090` 创建 snapshot 推理服务；runtime ready、AIGateway route Accepted、真实 API key 通过 Envoy 调用 HTTP 200。
- 2026-09-21: 通过 Auth gRPC fresh service token 验证 inference-service→Core Workload capability，错误 caller secret 被拒绝。
- 2026-09-21: ModelScope `Qwen/Qwen2.5-1.5B-Instruct` 完成 3,098,973,449-byte snapshot 导入，24 multipart parts、11 files、completed/100%。
- 2026-09-21: 修正 snapshot logical content size 与 manifest storage size 混用；model-service 和 model-import-worker digest 镜像完成 rollout。新的 BAAI/bge-small-en-v1.5 导入任务 completed/100%，API 回读 `total_size_bytes=401109582`、manifest `3925` 字节，逻辑大小修复已通过真实集群验证。
- 2026-09-21: 按要求只走旧 ANI：tenant-a/admin 密码登录旧 Gateway，回读 ready 模型和 running 推理服务，核对 service+API-key policy，并通过旧 ANI Envoy 完成 Chat Completions HTTP 200；独立 inference service 仓库未参与。
- 2026-09-22: 清理旧 GPU 测试服务和两个 stale e2e namespace；导入并验证 BAAI/bge-small-en-v1.5，CPU embedding HTTP 200/384 维。
- 2026-09-22: 导入并验证 Qwen/Qwen2.5-3B-Instruct，6.18GB、13 文件、ready；修复大于 int32 的 `total_size_bytes` 注册错误，复用已上传快照完成恢复；GPU Chat Completions HTTP 200。测试服务、策略和 API key 删除后，节点 GPU request 回到 0。
- 2026-09-22: 修复版 model-service 与 model-import-worker 完成 rollout，重新回读大模型仍为 ready/6,183,464,937 字节；旧 ANI 独立验证闭环完成。
