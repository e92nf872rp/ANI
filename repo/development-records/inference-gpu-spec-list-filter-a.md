# 推理 GPU 规格 ID 与服务状态筛选（2026-09-28，66 集群 live verified）

本批次修复两个影响 Console 的接口契约缺口：

- `resources.accelerator.spec_id` 统一使用 `/api/v1/gpu-specs` 返回的公共 GPUSpec ID。Core 能力发现保留旧节点型号别名用于迁移期输入，按 GPUSpec 的整卡/vGPU 模式和显存校验准入，并把公共 ID 传入 Volcano 资源渲染。
- `/api/v1/svc/inference-services` 增加 `status` 过滤和现有前端使用的 `offset` 兼容参数。Gateway、`inference.control.v1`、Controller 和 PostgreSQL 全链路透传；数据库在分页前执行租户、墓碑和状态过滤。`cursor` 与显式 `offset` 同时传入返回 400。

新增回归测试覆盖公共 GPUSpec ID、vGPU 显存不匹配、旧型号别名、列表状态筛选、offset 分页、非法查询参数和 SQL 条件。

另外修正 Core runtime 对 snapshot manifest 目录物料的识别，使对象模型能够按
`/models/<model_version_id>` 挂载并被 vLLM 使用。

验证：

```text
go test ./services/ani-gateway/... ./services/inference-service/... -count=1  PASS
go test ./pkg/adapters/runtime ./services/ani-gateway/... -count=1              PASS
make gen-proto                                                               PASS
make validate-services                                                       PASS
git diff --check                                                             PASS
```

66 集群真实验证（tenant-qa-full-001）：

- `GET /api/v1/gpu-specs?available=true&limit=100` 返回 3 个可用 GPUSpec ID；使用
  `rtx4090-12g-4`，没有把 `gpu_type` 当作 `spec_id`。
- 使用错误的 `spec_id=NVIDIA-RTX-4090-49140MiB` 创建请求真实返回
  `422 ACCELERATOR_SPEC_UNAVAILABLE`，且没有进入 Core。
- 使用合法请求
  `resources.accelerator={spec_id:"rtx4090-12g-4",count_per_replica:1,memory:12285}`
  返回 202，服务响应原样回显三项 accelerator 字段，最终 `running`、`ready_replicas=1`、
  `generation=observed_generation=1`。
- Pod 实际使用 `volcano.sh/vgpu-number=1`、`volcano.sh/vgpu-memory=1228`，节点为
  `dev-phys-03`；model-fetcher init 成功，vLLM 0.17.0 `/health`、`/v1/models` 和
  `/v1/chat/completions` 均返回 200。
- 临时服务已通过 DELETE 完成清理，查询返回 404，按服务 ID 的 Pod/Service/Deployment
  标签查询无残留。

为使真实对象模型能够进入 Core，补齐了 Core 对
`object://.../snapshot/manifest.json` 目录物料的识别，并增加 snapshot target path
回归测试；66 集群 Gateway 更新为
`dev-20260929-acc-materialization-b`（digest
`sha256:514299d0c2cbd8de8b682a14528d753d7f29d809e87334263ef1fbf932c1b73c`）。
