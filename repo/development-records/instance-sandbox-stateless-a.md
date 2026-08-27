# INSTANCE-SANDBOX-STATELESS-A

> 日期：2026-08-02
> 范围：ANI Core / Sandbox Runtime 无状态化、PG AsyncTask、Redis 幂等
> 状态：live passed

## 契约依据

- 继续遵循冻结的 Core `api/openapi/v1.yaml`，本批不修改 v1 契约、SDK、CLI 或 Console。
- Sandbox 子资源只接受当前租户的 Sandbox 实例；状态或 provider 能力不足返回 422。
- AsyncTask 必须可在 Gateway 重启后查询；Token 有效期内重放原响应，过期后返回 409 `IdempotencyResultExpired`。
- Services 不在本批范围。

## 实现

- 应用层从 PG 读取实例记录，构造 `SandboxExecutionContext` 传给 Runtime；Kubernetes 生命周期、文件、端口和 code-run 不再要求旧进程存在 session/refs map。
- Kubernetes Sandbox 使用 UUID；Local profile 保持原数字序列。
- 端口创建/关闭摘要写回 `workload_instances.sandbox_status`，生命周期保留摘要。
- 新增 `AsyncTaskStore`、Local/Metadata adapter 和增量迁移；Gateway 的 Sandbox、Storage、Vector 与 `/tasks/{id}` 使用注入 Store，移除 `completedTasks`。
- Kubernetes checkpoint list/create/restore/clone 明确返回能力不足，HTTP 映射 422。
- Redis 幂等覆盖 DELETE 和请求指纹；同 key 不同 intent 返回 409；Token 使用响应记录与无敏感正文 tombstone 双记录。

## 验证

已通过：Sandbox Runtime、AsyncTaskStore、Bootstrap、Middleware、Router 测试，`validate-async-task-store`、静态重启门禁和 `git diff --check`。

集群已应用 `20260802000100_async_tasks.sql`，并成功 rollout：

```text
docker.changqingyun.cn/ani/ani-gateway:instance-sandbox-stateless-20260802-v1
sha256:5817757f11e845b355fb6c1f4bee2a81b5d76cc43ebcf9d5ba37ba73d29ca563
```

`INSTANCE-SANDBOX-STATELESS-LIVE-GATE-A` 已通过真实完整顺序：

- 在同一 Sandbox 上先写文件、开放端口并创建 code-run AsyncTask；
- rollout restart Gateway，确认旧 Pod 被新 Ready Pod 替换；
- 重启后从 PG 读取实例、端口摘要和 AsyncTask，继续读取 Pod 文件并关闭既有 Service；
- Redis 使用同一 key 重放原 task，不同请求指纹返回 409；短 Token 有效期内重放原响应，过期后返回 409 `IdempotencyResultExpired`；
- Kubernetes checkpoint 返回 422 且不创建 task；pause/resume/delete 成功；
- 删除后 Deployment/Service 不存在，PG 保留 1 条 deleted 实例审计行和 1 条 task 审计行。

脱敏证据：`development-records/live-evidence/instance-sandbox-stateless-live-20260802.json`。

## 剩余边界

- `/workspace` 仍是 `emptyDir`；Gateway 重启不丢文件，但 Pod 重建会丢文件。
- Checkpoint 仍无真实 Kubernetes provider，实现前固定返回 422。
- 本次只证明 Sandbox Gateway 重启恢复路径，不外推为全部实例类型或 full platform production ready。
